import * as React from "react";
import { cn } from "@/lib/utils";

/**
 * 状态 Pill —— macOS 风格
 *  - 全圆 pill + 6px 圆点 + 11px 文字
 *  - 语义色用 Apple system colors 的低透明版本
 */
export type PillTone =
  | "neutral"
  | "success"
  | "warning"
  | "danger"
  | "info";

const TONE_CLASS: Record<PillTone, string> = {
  neutral: "bg-ink/[0.05] text-ink-muted [&>.dot]:bg-ink-subtle",
  // 文字用 700 深色阶：600 在 8% 色底上只有 ~3.4:1，不达 WCAG AA；暗色保持 400。
  success:
    "bg-emerald-500/[0.08] text-emerald-700 dark:text-emerald-400 [&>.dot]:bg-emerald-500",
  warning:
    "bg-amber-500/[0.08] text-amber-700 dark:text-amber-400 [&>.dot]:bg-amber-500",
  danger:
    "bg-red-500/[0.08] text-red-700 dark:text-red-400 [&>.dot]:bg-red-500",
  info: "bg-blue-500/[0.08] text-blue-700 dark:text-blue-400 [&>.dot]:bg-blue-500",
};

export function Pill({
  tone = "neutral",
  children,
  dot = true,
  className,
}: {
  tone?: PillTone;
  children: React.ReactNode;
  dot?: boolean;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full px-2 py-[3px] text-[11px] font-medium tracking-tight",
        TONE_CLASS[tone],
        className,
      )}
    >
      {dot && (
        <span
          aria-hidden
          className="dot h-1 w-1 rounded-full"
        />
      )}
      {children}
    </span>
  );
}
