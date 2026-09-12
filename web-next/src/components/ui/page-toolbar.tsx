import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

/**
 * 列表页工具栏 —— 左侧筛选 / 右侧搜索与主操作，小屏自动折行。
 */
export function PageToolbar({
  leading,
  trailing,
  className,
}: {
  leading?: ReactNode;
  trailing?: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex flex-wrap items-center gap-2",
        className,
      )}
    >
      {leading && <div className="flex min-w-0 flex-wrap items-center gap-2">{leading}</div>}
      {trailing && (
        <div className="ml-auto flex min-w-0 flex-wrap items-center gap-2">{trailing}</div>
      )}
    </div>
  );
}
