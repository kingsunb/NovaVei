import { useState } from "react";
import type { ViewMode } from "@/components/ui/view-toggle";

/**
 * useViewMode — 持久化视图模式到 localStorage 的小 hook。
 * 读取时校验合法值，写入时静默吞异常。
 */
export function useViewMode(storageKey: string, defaultValue: ViewMode) {
  const [value, setValue] = useState<ViewMode>(() => {
    if (typeof window === "undefined") return defaultValue;
    const stored = window.localStorage.getItem(storageKey);
    return stored === "grid" || stored === "list" ? stored : defaultValue;
  });

  const change = (v: ViewMode) => {
    setValue(v);
    try {
      window.localStorage.setItem(storageKey, v);
    } catch {
      // ignore
    }
  };

  return [value, change] as const;
}
