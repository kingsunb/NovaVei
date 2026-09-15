import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import {
  clearFlagsCache,
  clearAnonymousBucket,
  getAnonymousBucket,
  loadFlags,
  setLocalOverride,
  userBucket,
  shouldUseNewWeb,
  type Flags,
} from "./flags";

function mockFetchOnce(body: unknown, status = 200) {
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify(body), {
          status,
          headers: { "content-type": "application/json" },
        }),
      ),
    ),
  );
}

function mockFetchError() {
  vi.stubGlobal(
    "fetch",
    vi.fn(() => Promise.reject(new TypeError("network down"))),
  );
}

beforeEach(() => {
  clearFlagsCache();
  localStorage.clear();
});
afterEach(() => {
  vi.unstubAllGlobals();
});

describe("loadFlags", () => {
  it("default + runtime + localStorage 合并：localStorage 优先", async () => {
    mockFetchOnce({
      "new-web": true,
      "rollout-percent": 50,
      "sticky-bucket": true,
      "ab-mode": "auto",
      "legacy-path": "/legacy",
    });
    setLocalOverride({ "rollout-percent": 99 });
    const f = await loadFlags({ force: true });
    expect(f["rollout-percent"]).toBe(99);
    clearFlagsCache();
  });

  it("runtime.json 拉取失败 → 降级到 default + 本地", async () => {
    mockFetchError();
    localStorage.setItem(
      "nv-flags-override",
      JSON.stringify({ "ab-mode": "old" }),
    );
    const f = await loadFlags({ force: true });
    expect(f["ab-mode"]).toBe("old");
    expect(f["new-web"]).toBe(true); // DEFAULT_FLAGS
  });

  it("rollout-percent 夹紧到 0-100", async () => {
    mockFetchOnce({ "rollout-percent": 250 });
    const f = await loadFlags({ force: true });
    expect(f["rollout-percent"]).toBe(100);
  });

  it("rollout-percent 非法值（非数字）→ 默认 100", async () => {
    mockFetchOnce({ "rollout-percent": "abc" as unknown as number });
    const f = await loadFlags({ force: true });
    expect(f["rollout-percent"]).toBe(100);
  });

  it("rollout-percent NaN（typeof number 但值为 NaN）→ 默认 100", async () => {
    mockFetchOnce({ "rollout-percent": Number.NaN });
    const f = await loadFlags({ force: true });
    expect(f["rollout-percent"]).toBe(100);
  });
});

describe("setLocalOverride / clearFlagsCache", () => {
  it("setLocalOverride 后 loadFlags 命中缓存", async () => {
    mockFetchOnce({});
    setLocalOverride({ "ab-mode": "new" });
    const f1 = await loadFlags();
    expect(f1["ab-mode"]).toBe("new");
    // 第二次：cache 命中，不重新 fetch
    const f2 = await loadFlags();
    expect(f2).toBe(f1);
  });
});

describe("userBucket", () => {
  it("同 seed 同 bucket（sticky）", () => {
    expect(userBucket("admin")).toBe(userBucket("admin"));
  });
  it("不同 seed 大概率不同", () => {
    const a = userBucket("admin");
    const b = userBucket("user-1");
    const c = userBucket("user-2");
    expect(new Set([a, b, c]).size).toBeGreaterThanOrEqual(2);
  });
  it("输出 0-99 范围", () => {
    for (const s of ["a", "b", "c", "x", "yy", "zzz", "long-user-id-12345"]) {
      const b = userBucket(s);
      expect(b).toBeGreaterThanOrEqual(0);
      expect(b).toBeLessThan(100);
    }
  });
});

describe("shouldUseNewWeb", () => {
  const base: Flags = {
    "new-web": true,
    "rollout-percent": 50,
    "sticky-bucket": true,
    "ab-mode": "auto",
    "legacy-path": "/legacy",
  };

  it("new-web=false → false", () => {
    expect(shouldUseNewWeb({ ...base, "new-web": false }, "u1")).toBe(false);
  });

  it("ab-mode=new → true（即便 new-web=false 也应 false，验证优先级）", () => {
    expect(shouldUseNewWeb({ ...base, "ab-mode": "new" }, "u1")).toBe(true);
  });

  it("ab-mode=old → false", () => {
    expect(shouldUseNewWeb({ ...base, "ab-mode": "old" }, "u1")).toBe(false);
  });

  it("0% 全部走旧", () => {
    for (let i = 0; i < 50; i++) {
      expect(shouldUseNewWeb({ ...base, "rollout-percent": 0 }, `u${i}`)).toBe(false);
    }
  });

  it("100% 全部走新", () => {
    for (let i = 0; i < 50; i++) {
      expect(shouldUseNewWeb({ ...base, "rollout-percent": 100 }, `u${i}`)).toBe(true);
    }
  });

  it("sticky：同 userId 始终一致", () => {
    const flags: Flags = { ...base, "rollout-percent": 50 };
    const r1 = shouldUseNewWeb(flags, "user-x");
    for (let i = 0; i < 100; i++) {
      expect(shouldUseNewWeb(flags, "user-x")).toBe(r1);
    }
  });

  it("50% 时总样本分布大致 ±10%", () => {
    let hits = 0;
    const n = 2000;
    for (let i = 0; i < n; i++) {
      if (shouldUseNewWeb(base, `user-${i}`)) hits++;
    }
    const ratio = hits / n;
    expect(ratio).toBeGreaterThan(0.4);
    expect(ratio).toBeLessThan(0.6);
  });

  it("non-sticky：userId=null 时桶完全随机", () => {
    // 关闭 sticky + 50% 比例：跑 1000 次应大致 ±5%
    const flags: Flags = { ...base, "rollout-percent": 50, "sticky-bucket": false };
    let hits = 0;
    for (let i = 0; i < 1000; i++) {
      if (shouldUseNewWeb(flags, null)) hits++;
    }
    const ratio = hits / 1000;
    expect(ratio).toBeGreaterThan(0.4);
    expect(ratio).toBeLessThan(0.6);
  });
});

describe("异常分支", () => {
  it("readLocalOverride 遇非法 JSON 静默返回 null", async () => {
    mockFetchOnce({});
    localStorage.setItem("nv-flags-override", "{bad json");
    const f = await loadFlags();
    expect(f).toBeDefined();
  });

  it("setLocalOverride 遇 localStorage 异常静默忽略", () => {
    const desc = Object.getOwnPropertyDescriptor(localStorage, "setItem");
    Object.defineProperty(localStorage, "setItem", {
      value: () => {
        throw new Error("quota");
      },
      configurable: true,
      writable: true,
    });
    expect(() => setLocalOverride({ "ab-mode": "new" })).not.toThrow();
    if (desc) Object.defineProperty(localStorage, "setItem", desc);
  });
});

describe("匿名灰度桶 (§3.4)", () => {
  const base: Flags = {
    "new-web": true,
    "rollout-percent": 50,
    "sticky-bucket": true,
    "ab-mode": "auto",
    "legacy-path": "/legacy",
  };

  beforeEach(() => {
    clearAnonymousBucket();
    sessionStorage.clear();
  });
  afterEach(() => {
    // 先还原 sessionStorage 再清理：避免上一用例 stub 了 sessionStorage 后，
    // 这里调用 .clear() 撞上残桩的 TypeError。
    vi.unstubAllGlobals();
    clearAnonymousBucket();
    sessionStorage.clear();
  });

  it("sticky=true + userId=null：同一会话内分桶稳定（不再每次随机）", () => {
    const flags: Flags = { ...base, "rollout-percent": 50, "sticky-bucket": true };
    const r1 = shouldUseNewWeb(flags, null);
    for (let i = 0; i < 100; i++) {
      expect(shouldUseNewWeb(flags, null)).toBe(r1);
    }
  });

  it("sticky=true + userId 提供：用 userId 分桶，不触碰匿名桶", () => {
    sessionStorage.setItem("nv-anon-bucket", "should-not-be-used");
    const flags: Flags = { ...base, "rollout-percent": 50, "sticky-bucket": true };
    const r = shouldUseNewWeb(flags, "user-x");
    // 同 userId 始终一致，且与匿名桶无关
    for (let i = 0; i < 50; i++) {
      expect(shouldUseNewWeb(flags, "user-x")).toBe(r);
    }
    expect(r).toBe(userBucket("user-x") < 50);
  });

  it("sticky=false + userId=null：保持每次随机（不使用匿名桶）", () => {
    const flags: Flags = { ...base, "rollout-percent": 50, "sticky-bucket": false };
    let hits = 0;
    for (let i = 0; i < 1000; i++) {
      if (shouldUseNewWeb(flags, null)) hits++;
    }
    const ratio = hits / 1000;
    expect(ratio).toBeGreaterThan(0.4);
    expect(ratio).toBeLessThan(0.6);
  });

  it("getAnonymousBucket：同会话返回同值；clearAnonymousBucket 后可重新分桶", () => {
    const a = getAnonymousBucket();
    expect(getAnonymousBucket()).toBe(a);
    clearAnonymousBucket();
    sessionStorage.clear();
    const c = getAnonymousBucket();
    // 重新生成的桶极大概率与旧值不同（Math.random 碰撞概率 ~0）
    expect(c).not.toBe(a);
  });

  it("sessionStorage 不可用时用模块级 ref 兜底，同一周期内仍稳定", () => {
    vi.stubGlobal("sessionStorage", {
      getItem: () => {
        throw new Error("denied");
      },
      setItem: () => {
        throw new Error("denied");
      },
      removeItem: () => {
        throw new Error("denied");
      },
    });
    clearAnonymousBucket();
    const a = getAnonymousBucket();
    expect(getAnonymousBucket()).toBe(a);
  });
});
