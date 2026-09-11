import { useEffect, useState, useSyncExternalStore } from "react";
import { openSSE } from "@/lib/sse";
import type { GroupRouteState } from "@/lib/types";

/** 分组路由运行时状态：group_id → 最新快照。 */
export type GroupRuntimeMap = Map<number, GroupRouteState>;

/**
 * 订阅 /api/v1/group/runtime/stream（event: "runtime"）。
 * 后端连上即推全量快照、变更时推增量，重连后的快照会重新 upsert，
 * 因此本地不做清空；卸载由 openSSE 句柄关闭（含退避重连与 401 探活）。
 */
export function useGroupRuntime(): GroupRuntimeMap {
  const [states, setStates] = useState<GroupRuntimeMap>(() => new Map());
  useEffect(() => {
    const handle = openSSE<GroupRouteState>("/api/v1/group/runtime/stream", {
      eventName: "runtime",
      onMessage: (update) => {
        if (!update || typeof update.group_id !== "number") return;
        setStates((prev) => {
          const next = new Map(prev);
          next.set(update.group_id, update);
          return next;
        });
      },
    });
    return () => handle.close();
  }, []);
  return states;
}

// ---- 模块级 1s 时钟 ----
// 倒计时若放在页面顶层 setState，会把整页卡片拖进每秒重渲染（审计 §1.7 的教训）。
// 这里用 useSyncExternalStore 订阅模块级时钟：只有调用 useNow 的倒计时格子自己重渲染。

const clockListeners = new Set<() => void>();
let clockTimer: ReturnType<typeof setInterval> | null = null;
let clockNow = Date.now();

function subscribeClock(listener: () => void): () => void {
  clockListeners.add(listener);
  clockNow = Date.now();
  if (!clockTimer) {
    clockTimer = setInterval(() => {
      clockNow = Date.now();
      clockListeners.forEach((l) => l());
    }, 1000);
  }
  return () => {
    clockListeners.delete(listener);
    if (clockListeners.size === 0 && clockTimer) {
      clearInterval(clockTimer);
      clockTimer = null;
    }
  };
}

/** 当前时间（unix ms），每秒走一格；仅倒计时格子订阅。 */
export function useNow(): number {
  return useSyncExternalStore(subscribeClock, () => clockNow);
}

/**
 * 截止时间（unix ms）的剩余秒数；已到期或无截止返回 0——
 * 与后端「冷却到期条目由前端按当前时间忽略」的语义一致。
 */
export function remainingSeconds(until: number, now: number): number {
  if (!until || until <= now) return 0;
  return Math.ceil((until - now) / 1000);
}

/** 倒计时展示：<60s 显示 "45s"，更长显示 "29:45"。 */
export function formatCountdown(totalSeconds: number): string {
  if (totalSeconds < 60) return `${totalSeconds}s`;
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return `${minutes}:${String(seconds).padStart(2, "0")}`;
}
