import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter, useNavigate } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AppShell } from "./AppShell";
import { AuthProvider } from "@/store/auth";
import { ThemeProvider } from "./ThemeProvider";

/**
 * AppShell 内部用 useLocation/useNavigate，且子组件 Topbar 用 useAuth/useTheme，
 * 因此需要完整的 Router + QueryClient + Auth + Theme 提供者。
 * 这里只验证路由滚动复位（§3.2），不依赖任何业务页面 chunk。
 */
function Providers({ children }: { children: React.ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <ThemeProvider>
      <QueryClientProvider client={qc}>
        <AuthProvider>{children}</AuthProvider>
      </QueryClientProvider>
    </ThemeProvider>
  );
}

function Harness() {
  const navigate = useNavigate();
  return (
    <>
      <button type="button" onClick={() => navigate("/other")}>
        go-other
      </button>
      <AppShell>
        <div>page-content</div>
      </AppShell>
    </>
  );
}

describe("<AppShell /> 路由滚动复位 (§3.2)", () => {
  beforeEach(() => {
    // AuthProvider 启动探活会 fetch /user/status；这里给个 401 让它安静地
    // 落到未登录态，避免未处理的网络拒绝污染测试输出。AppShell 本身不依赖认证态。
    vi.stubGlobal(
      "fetch",
      vi.fn(() => Promise.resolve(new Response("{}", { status: 401 }))),
    );
  });
  afterEach(() => vi.unstubAllGlobals());

  it("pathname 变化时 main.scrollTop 复位到 0", async () => {
    const { container } = render(
      <MemoryRouter initialEntries={["/dashboard"]}>
        <Providers>
          <Harness />
        </Providers>
      </MemoryRouter>,
    );
    const main = container.querySelector("main");
    expect(main).not.toBeNull();
    // 等待 AuthProvider 启动探活异步 setState 在 act 内 settle，避免 act 警告
    await waitFor(() => expect(main).toBeInTheDocument());
    // 模拟用户在长列表底部滚动
    main!.scrollTop = 800;
    expect(main!.scrollTop).toBe(800);
    // 导航到新路由 → useEffect[pathname] 把 scrollTop 复位
    fireEvent.click(screen.getByText("go-other"));
    await waitFor(() => expect(main!.scrollTop).toBe(0));
  });

  it("pathname 不变时不强制复位（避免误清同页内滚动）", async () => {
    const { container } = render(
      <MemoryRouter initialEntries={["/dashboard"]}>
        <Providers>
          <Harness />
        </Providers>
      </MemoryRouter>,
    );
    const main = container.querySelector("main")!;
    await waitFor(() => expect(main).toBeInTheDocument());
    main.scrollTop = 300;
    // 同页内不导航，scrollTop 保持
    expect(main.scrollTop).toBe(300);
  });
});
