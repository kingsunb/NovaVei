import { useCallback, useEffect, useState } from "react";

const STORAGE_KEY = "nv-sidebar-collapsed";

/**
 * 侧边栏折叠状态 —— DESIGN.md §3 要求
 *  - 240px ↔ 56px
 *  - 状态写 localStorage，跨刷新保持
 *  - 不在 SSR 友好的环境运行，初始读 localStorage 同步初始化
 */
export function useSidebar() {
  const [collapsed, setCollapsed] = useState<boolean>(() => {
    try {
      return localStorage.getItem(STORAGE_KEY) === "1";
    } catch {
      return false;
    }
  });

  // 跨标签页同步
  useEffect(() => {
    function onStorage(e: StorageEvent) {
      if (e.key === STORAGE_KEY) {
        setCollapsed(e.newValue === "1");
      }
    }
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, []);

  const toggle = useCallback(() => {
    setCollapsed((prev) => {
      const next = !prev;
      try {
        localStorage.setItem(STORAGE_KEY, next ? "1" : "0");
      } catch {
        /* ignore */
      }
      return next;
    });
  }, []);

  return { collapsed, toggle };
}
