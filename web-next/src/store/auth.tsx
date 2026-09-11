import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useQueryClient } from "@tanstack/react-query";
import { api, APIError } from "@/lib/api";
import type { UserStatus } from "@/lib/types";

interface AuthState {
  isAuthenticated: boolean;
  username: string | null;
  mustChangePassword: boolean;
}

interface AuthContextValue extends AuthState {
  login: (username: string, password: string, expire: number) => Promise<void>;
  logout: () => Promise<void>;
  refreshStatus: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

/**
 * 认证上下文：靠 JWT cookie 维持登录态
 *  - 启动时调 /user/status 探活；200 即已登录
 *  - login() 调 /user/login，后端 Set-Cookie；前端不存 token
 *  - 必须改密标志来自 /user/status 响应
 *  - logout() 取消在途查询并清空全部 React Query 缓存，防止下一个会话复用
 *    上一个用户的敏感数据（渠道明文 Key、API Key 明文等）。
 *  - generation 计数器：login/logout/refreshStatus 递增，异步结果返回时若
 *    generation 已过期则丢弃，防止旧请求覆盖新登录态（竞态守卫）。
 */
export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>({
    isAuthenticated: false,
    username: null,
    mustChangePassword: false,
  });
  const queryClient = useQueryClient();
  // 每次发起会改变登录态的异步操作时递增；结果返回时若不匹配则丢弃。
  const generationRef = useRef(0);

  // 启动探活
  useEffect(() => {
    let cancelled = false;
    const gen = generationRef.current;
    (async () => {
      try {
        // 信封 data 缺失/畸形时兜底为空对象，探活失败不该炸掉整棵组件树
        const s = (await api.status()) ?? ({} as UserStatus);
        if (cancelled || gen !== generationRef.current) return;
        setState((prev) => ({
          ...prev,
          isAuthenticated: true,
          // 旧后端可能不带 username: 空串回落 null, Topbar 走自己的兜底而不是空名
          username: s.username || prev.username,
          mustChangePassword: s.must_change_password ?? false,
        }));
      } catch (err) {
        // 401 等都视为未登录，UI 自然跳 login
        if (cancelled || gen !== generationRef.current) return;
        if (err instanceof APIError && err.status === 401) {
          setState((prev) => ({ ...prev, isAuthenticated: false }));
        }
        // 其他错误（网络/CORS）保持未登录态
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const login = useCallback<AuthContextValue["login"]>(
    async (username, password, expire = 0) => {
      const gen = ++generationRef.current;
      const s = await api.login({ username, password, expire });
      if (gen !== generationRef.current) return;
      setState({
        isAuthenticated: true,
        username,
        mustChangePassword: s.must_change_password,
      });
    },
    [],
  );

  const logout = useCallback(async () => {
    const gen = ++generationRef.current;
    // 1. 通知后端清除 JWT cookie（best-effort：后端不可达时前端仍清状态）。
    try {
      await api.logout();
    } catch {
      /* 即使后端失败，前端也清状态 */
    }
    if (gen !== generationRef.current) return;
    // 2. 取消在途查询，避免迟到的响应把上一个用户的敏感数据写回缓存。
    try {
      await queryClient.cancelQueries();
    } catch {
      /* cancelQueries 不应 reject，兜底 */
    }
    // 3. 清空全部查询缓存（渠道明文 Key、API Key 明文等不复用）。
    queryClient.clear();
    // 4. 更新认证状态。
    setState({ isAuthenticated: false, username: null, mustChangePassword: false });
  }, [queryClient]);

  const refreshStatus = useCallback(async () => {
    const gen = ++generationRef.current;
    try {
      const s = (await api.status()) ?? ({} as UserStatus);
      if (gen !== generationRef.current) return;
      // 同步回填 username: 改名后无需重新登录即可刷新 Topbar 显示
      setState((prev) => ({
        ...prev,
        username: s.username || prev.username,
        mustChangePassword: s.must_change_password ?? false,
      }));
    } catch (err) {
      if (gen !== generationRef.current) return;
      // JWT 过期或后端不可达: 401 让本地退到未登录态, 其他错误吞掉,
      // 避免业务页面因一次后台刷新崩溃。
      if (err instanceof APIError && err.status === 401) {
        setState({
          isAuthenticated: false,
          username: null,
          mustChangePassword: false,
        });
      }
    }
  }, []);

  const value = useMemo<AuthContextValue>(
    () => ({ ...state, login, logout, refreshStatus }),
    [state, login, logout, refreshStatus],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth 必须在 AuthProvider 内使用");
  return ctx;
}
