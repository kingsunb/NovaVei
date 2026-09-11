import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import GroupsPage from "./Groups";
import { ThemeProvider } from "@/components/layout/ThemeProvider";
import { sampleChannel, sampleGroup } from "@/test/fixtures/channels";

const STORAGE_KEY = "nv-auth";

function Wrapper({ children }: { children: React.ReactNode }) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return (
    <ThemeProvider>
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          {children}
        </MemoryRouter>
      </QueryClientProvider>
    </ThemeProvider>
  );
}

function jsonOk(data: unknown) {
  return new Response(JSON.stringify({ code: 200, message: "success", data }), {
    status: 200,
    headers: { "content-type": "application/json" },
  });
}

// GroupsPage 挂载即订阅 /group/runtime/stream（冷却/亲和倒计时）：
// jsdom 没有 EventSource，统一用假实现，事件由用例手动注入。
class FakeEventSource {
  static instances: FakeEventSource[] = [];
  url: string;
  withCredentials = false;
  onopen: ((e: Event) => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onerror: ((e: Event) => void) | null = null;
  listeners: Record<string, ((e: Event) => void)[]> = {};

  constructor(url: string, init?: { withCredentials?: boolean }) {
    this.url = url;
    this.withCredentials = !!init?.withCredentials;
    FakeEventSource.instances.push(this);
  }
  addEventListener(name: string, fn: (e: Event) => void) {
    (this.listeners[name] ||= []).push(fn);
  }
  close() {}
  fireEvent(name: string, data: unknown) {
    const event = new MessageEvent(name, { data: JSON.stringify(data) });
    this.listeners[name]?.forEach((fn) => fn(event));
  }
}

beforeEach(() => {
  localStorage.setItem(
    STORAGE_KEY,
    JSON.stringify({ isAuthenticated: true, username: "admin", mustChangePassword: false }),
  );
  vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
  FakeEventSource.instances = [];
});

afterEach(() => {
  vi.unstubAllGlobals();
});



describe("GroupEditor relay_config 默认值契约", () => {
  function setupGroupFetch(groups: unknown[]) {
    vi.stubGlobal(
      "fetch",
      vi.fn((url: string, init?: RequestInit) => {
        if (url.includes("/group/list")) return Promise.resolve(jsonOk(groups));
        if (url.includes("/channel/list")) return Promise.resolve(jsonOk([sampleChannel]));
        return Promise.resolve(jsonOk(null));
      }),
    );
  }

  it("新建分组：表单默认值取 DEFAULT_GROUP_RELAY_CONFIG（粘合开、轮次 600），不是 false/60", async () => {
    const user = userEvent.setup();
    setupGroupFetch([]);
    render(<GroupsPage />, { wrapper: Wrapper });

    await user.click(screen.getByRole("button", { name: "新建分组" }));
    await waitFor(() => screen.getByRole("dialog"));

    // sampleGroup 的服务端值是 60；若误用旧的手写默认 60 会与后端默认 600 混淆不了，
    // 所以这里断言 600 —— 只有真正引用 DEFAULT_GROUP_RELAY_CONFIG 才能得到。
    const maxRounds = screen.getByLabelText("最大轮次") as HTMLInputElement;
    expect(maxRounds.value).toBe("600");
    const cooldown = screen.getByLabelText("成员冷却（秒）") as HTMLInputElement;
    expect(cooldown.value).toBe("60");
    // 基本页签有 会话粘合 / 优先透传 / 启用脱敏 三个开关；粘合默认开
    const switches = screen.getAllByRole("switch");
    expect(switches.length).toBe(3);
    expect(switches[0]).toHaveAttribute("aria-checked", "true");
  });

  it("编辑已有分组：读服务端 relay_config，不被默认值覆盖", async () => {
    const user = userEvent.setup();
    // sampleGroup.relay_config: session_sticky_enabled=false, max_request_rounds=60
    setupGroupFetch([sampleGroup]);
    render(<GroupsPage />, { wrapper: Wrapper });
    await waitFor(() => screen.getByText("gpt-4o-prod"));

    await user.click(screen.getByRole("button", { name: /编辑/ }));
    await waitFor(() => screen.getByRole("dialog"));

    const maxRounds = screen.getByLabelText("最大轮次") as HTMLInputElement;
    expect(maxRounds.value).toBe("60");
    const switches = screen.getAllByRole("switch");
    expect(switches[0]).toHaveAttribute("aria-checked", "false");
  });
});

describe("GroupEditor 保存校验", () => {
  it("非法名称（NAME_RULE 不通过）时保存禁用，红字提示不可绕过", async () => {
    const user = userEvent.setup();
    vi.stubGlobal(
      "fetch",
      vi.fn((url: string) => {
        if (url.includes("/group/list")) return Promise.resolve(jsonOk([]));
        if (url.includes("/channel/list")) return Promise.resolve(jsonOk([sampleChannel]));
        return Promise.resolve(jsonOk(null));
      }),
    );
    render(<GroupsPage />, { wrapper: Wrapper });

    await user.click(screen.getByRole("button", { name: "新建分组" }));
    await waitFor(() => screen.getByRole("dialog"));

    const save = screen.getByRole("button", { name: "保存" });
    expect(save).toBeDisabled(); // 名称为空

    // Field 的 label 文本含 required 星号与 sr-only「必填」，须用前缀正则匹配
    await user.type(screen.getByLabelText(/^名称/), "a!b");
    expect(screen.getByText(/只能包含字母、数字、中文/)).toBeInTheDocument();
    expect(save).toBeDisabled();

    await user.clear(screen.getByLabelText(/^名称/));
    await user.type(screen.getByLabelText(/^名称/), "合法名称");
    expect(save).toBeEnabled();
  });
});

describe("buildMemberDiff 行为（间接通过 add/remove 后保存）", () => {
  it("新增成员 → 走 items_to_add；删除 → items_to_delete", async () => {
    const user = userEvent.setup();
    const calls: Array<{ url: string; body?: any }> = [];
    vi.stubGlobal(
      "fetch",
      vi.fn((url: string, init?: RequestInit) => {
        const body = init?.body ? JSON.parse(String(init.body)) : null;
        calls.push({ url, body });
        if (url.includes("/group/list")) return Promise.resolve(jsonOk([sampleGroup]));
        if (url.includes("/channel/list")) return Promise.resolve(jsonOk([sampleChannel]));
        if (url.includes("/group/update") && init?.method === "POST") {
          return Promise.resolve(jsonOk(sampleGroup));
        }
        if (url.includes("/group/cooldown/clear/")) return Promise.resolve(jsonOk(null));
        if (url.includes("/group/active/")) return Promise.resolve(jsonOk(null));
        return Promise.resolve(jsonOk(null));
      }),
    );

    render(<GroupsPage />, { wrapper: Wrapper });
    await waitFor(() => screen.getByText("gpt-4o-prod"));

    // 打开编辑
    await user.click(screen.getByRole("button", { name: /编辑/ }));
    await waitFor(() => screen.getByRole("dialog"));

    // 切到成员 Tab
    await user.click(screen.getByRole("tab", { name: /成员/ }));

    // 添加一个成员：选择渠道模型
    const select = screen.getByLabelText("选择渠道模型") as HTMLSelectElement;
    await user.selectOptions(select, "c:1:101"); // 选第二个模型

    // 点添加
    const addButtons = screen.getAllByRole("button", { name: /^添加$/ });
    await user.click(addButtons[0]!);

    // 现在应该有 3 个 #N
    await waitFor(() => {
      expect(screen.getByText("#3")).toBeInTheDocument();
    });

    // 保存
    await user.click(screen.getByRole("button", { name: /^保存$/ }));

    // 验证调用：body 应含 items_to_add
    await waitFor(() => {
      const updateCall = calls.find((c) => c.url.includes("/group/update"));
      expect(updateCall).toBeTruthy();
      expect(updateCall!.body).toHaveProperty("items_to_add");
      expect((updateCall!.body as any).items_to_add).toHaveLength(1);
      expect((updateCall!.body as any).items_to_add[0]).toEqual({
        channel_model_id: 101,
        ref_group_name: "",
        priority: 3,
      });
    });
  });
});

describe("分组卡片冷却/亲和实时倒计时", () => {
  function fireRuntime(payload: Record<string, unknown>) {
    act(() => {
      FakeEventSource.instances[0]?.fireEvent("runtime", payload);
    });
  }
  function stubListFetch() {
    vi.stubGlobal(
      "fetch",
      vi.fn((url: string) => {
        if (url.includes("/group/list")) return Promise.resolve(jsonOk([sampleGroup]));
        if (url.includes("/channel/list")) return Promise.resolve(jsonOk([sampleChannel]));
        return Promise.resolve(jsonOk(null));
      }),
    );
  }

  it("成员冷却中显示倒计时 chip，清零后消失", async () => {
    stubListFetch();
    render(<GroupsPage />, { wrapper: Wrapper });
    await waitFor(() => screen.getByText("gpt-4o-prod"));

    fireRuntime({
      group_id: 10,
      current_item_id: 0,
      probe_item_id: 0,
      affinity_until: 0,
      cooldowns: { "1": Date.now() + 30_000 },
      levels: {},
      half_opens: {},
      post_commit_strikes: {},
      emergency_item_id: 0,
      emergency_active: 0,
    });
    // ceil 取整，秒边界在 30s/29s 之间抖动
    await waitFor(() =>
      expect(screen.getByText(/冷却 29s|冷却 30s/)).toBeInTheDocument(),
    );

    // 冷却到期（前端按当前时间忽略过期条目）：chip 消失
    fireRuntime({
      group_id: 10,
      current_item_id: 0,
      probe_item_id: 0,
      affinity_until: 0,
      cooldowns: { "1": Date.now() - 1_000 },
      levels: {},
      half_opens: {},
      post_commit_strikes: {},
      emergency_item_id: 0,
      emergency_active: 0,
    });
    await waitFor(() =>
      expect(screen.queryByText(/冷却 \d/)).not.toBeInTheDocument(),
    );
  });

  it("当前承载成员显示亲和倒计时；紧急兜底显示卡片级标记", async () => {
    stubListFetch();
    render(<GroupsPage />, { wrapper: Wrapper });
    await waitFor(() => screen.getByText("gpt-4o-prod"));

    fireRuntime({
      group_id: 10,
      current_item_id: 2,
      probe_item_id: 0,
      affinity_until: Date.now() + 300_000,
      cooldowns: {},
      levels: {},
      half_opens: {},
      post_commit_strikes: {},
      emergency_item_id: 2,
      emergency_active: 1,
    });
    await waitFor(() =>
      expect(screen.getByText(/亲和 5:00|亲和 4:59/)).toBeInTheDocument(),
    );
    // 亲和只属于 current_item_id（成员 2），成员 1 不显示
    expect(screen.queryByText(/冷却 \d/)).not.toBeInTheDocument();
    expect(screen.getByText("紧急兜底")).toBeInTheDocument();
  });
});

describe("分组列表排序与自定义顺序", () => {
  beforeEach(() => {
    // jsdom localStorage 跨用例共享，清掉排序偏好避免用例间串扰
    localStorage.removeItem("nv-group-sort");
  });

  let fetchMock: ReturnType<typeof vi.fn>;
  let serverGroups: Array<Record<string, unknown>>;

  function groupJson(
    id: number,
    name: string,
    extra: Record<string, unknown> = {},
  ) {
    return {
      id,
      name,
      mode: "failover",
      active_item_id: 0,
      relay_config: { ...sampleGroup.relay_config },
      items: [] as unknown[],
      ...extra,
    };
  }

  /** mock 后端：/group/list 返回 serverGroups；/group/update 写入 display_order
   * 并按其排序（模拟后端 GroupList 的 display_order 排序契约）。 */
  function setupSortFetch() {
    fetchMock = vi.fn((url: string, init?: RequestInit) => {
      if (url.includes("/group/list")) return Promise.resolve(jsonOk(serverGroups));
      if (url.includes("/channel/list")) return Promise.resolve(jsonOk([sampleChannel]));
      if (url.includes("/group/update")) {
        const body = JSON.parse(String(init?.body ?? "{}")) as {
          id: number;
          display_order?: number;
        };
        serverGroups = serverGroups
          .map((g) =>
            g.id === body.id ? { ...g, display_order: body.display_order } : g,
          )
          .sort(
            (a, b) =>
              (Number(a.display_order) || Number.MAX_SAFE_INTEGER) -
              (Number(b.display_order) || Number.MAX_SAFE_INTEGER),
          );
        return Promise.resolve(jsonOk(null));
      }
      return Promise.resolve(jsonOk(null));
    });
    vi.stubGlobal("fetch", fetchMock);
  }

  function updateBodies() {
    return fetchMock.mock.calls
      .filter((call) => String(call[0]).includes("/group/update"))
      .map((call) => JSON.parse(String(call[1]?.body)));
  }

  function expectOrderedInDOM(a: HTMLElement, b: HTMLElement) {
    // 断言 a 在 b 之前（同一文档中的先后关系）
    expect(
      !!(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING),
    ).toBe(true);
  }

  it("切换为按名称排序立即生效并写入 localStorage，重新挂载后保持", async () => {
    const user = userEvent.setup();
    serverGroups = [
      groupJson(31, "c-group"),
      groupJson(32, "a-group"),
      groupJson(33, "b-group"),
    ];
    setupSortFetch();
    const { unmount } = render(<GroupsPage />, { wrapper: Wrapper });
    await waitFor(() => screen.getByText("a-group"));

    // 默认按优先级；成员全空时比较恒为 0，稳定排序保持后端返回顺序 c → a → b
    expectOrderedInDOM(screen.getByText("c-group"), screen.getByText("a-group"));
    expectOrderedInDOM(screen.getByText("a-group"), screen.getByText("b-group"));

    await user.selectOptions(screen.getByLabelText("分组排序方式"), "name");
    expect(localStorage.getItem("nv-group-sort")).toBe("name");
    expectOrderedInDOM(screen.getByText("a-group"), screen.getByText("b-group"));
    expectOrderedInDOM(screen.getByText("b-group"), screen.getByText("c-group"));

    unmount();
    render(<GroupsPage />, { wrapper: Wrapper });
    await waitFor(() => screen.getByText("a-group"));
    expect(
      (screen.getByLabelText("分组排序方式") as HTMLSelectElement).value,
    ).toBe("name");
    expectOrderedInDOM(screen.getByText("a-group"), screen.getByText("b-group"));
    expectOrderedInDOM(screen.getByText("b-group"), screen.getByText("c-group"));
  });

  it("按优先级排序：分组内成员最小 priority 在前", async () => {
    serverGroups = [
      groupJson(41, "晚接管组", {
        items: [
          { id: 1, group_id: 41, channel_model_id: 100, ref_group_name: "", priority: 5 },
        ],
      }),
      groupJson(42, "先接管组", {
        items: [
          { id: 2, group_id: 42, channel_model_id: 100, ref_group_name: "", priority: 2 },
        ],
      }),
    ];
    setupSortFetch();
    render(<GroupsPage />, { wrapper: Wrapper });
    await waitFor(() => screen.getByText("先接管组"));
    expectOrderedInDOM(screen.getByText("先接管组"), screen.getByText("晚接管组"));
  });

  it("custom 排序保持后端顺序；下移按全量重排提交且只更新变化的分组", async () => {
    const user = userEvent.setup();
    serverGroups = [
      groupJson(21, "甲组", { display_order: 1 }),
      groupJson(22, "乙组", { display_order: 2 }),
      groupJson(23, "丙组", { display_order: 3 }),
    ];
    setupSortFetch();
    render(<GroupsPage />, { wrapper: Wrapper });
    await waitFor(() => screen.getByText("甲组"));

    await user.selectOptions(screen.getByLabelText("分组排序方式"), "custom");
    // custom 直接沿用后端 display_order 排序结果
    expectOrderedInDOM(screen.getByText("甲组"), screen.getByText("乙组"));
    // 首尾边界禁用，中间分组可移动
    expect(screen.getByLabelText("上移分组 甲组")).toBeDisabled();
    expect(screen.getByLabelText("下移分组 丙组")).toBeDisabled();
    expect(screen.getByLabelText("上移分组 乙组")).toBeEnabled();

    await user.click(screen.getByLabelText("下移分组 乙组"));

    // 目标顺序 甲(1) 丙(2) 乙(3)：乙 2→3、丙 3→2，甲不变不提交
    await waitFor(() => expect(updateBodies()).toHaveLength(2));
    expect(updateBodies()).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ id: 22, display_order: 3 }),
        expect.objectContaining({ id: 23, display_order: 2 }),
      ]),
    );
    expect(updateBodies().some((b) => b.id === 21)).toBe(false);

    // onSuccess invalidate 后重拉，展示顺序收敛为新顺序
    await waitFor(() => {
      expectOrderedInDOM(screen.getByText("甲组"), screen.getByText("丙组"));
      expectOrderedInDOM(screen.getByText("丙组"), screen.getByText("乙组"));
    });
  });

  it("localStorage 排序值非法时回退为按优先级", async () => {
    localStorage.setItem("nv-group-sort", "bogus");
    serverGroups = [groupJson(51, "唯一组")];
    setupSortFetch();
    render(<GroupsPage />, { wrapper: Wrapper });
    await waitFor(() => screen.getByText("唯一组"));
    expect(
      (screen.getByLabelText("分组排序方式") as HTMLSelectElement).value,
    ).toBe("priority");
  });
});
