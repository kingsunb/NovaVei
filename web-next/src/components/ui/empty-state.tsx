import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

/**
 * 空态 —— 统一各列表页「还没有数据 / 没有匹配」的展示
 *  - 圆底图标 + 标题 + 可选说明 + 可选动作（如「创建第一把密钥」）
 *  - 文案由调用方传入，组件只负责版式，不改动各页既有提示语
 */
export function EmptyState({
  icon,
  title,
  hint,
  action,
  className,
}: {
  icon?: ReactNode;
  title: ReactNode;
  hint?: ReactNode;
  action?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex flex-col items-center gap-1.5 px-6 py-12 text-center", className)}>
      {icon && (
        <div
          aria-hidden
          className="mb-1.5 flex h-12 w-12 items-center justify-center rounded-full bg-primary/[0.08] text-primary-text"
        >
          {icon}
        </div>
      )}
      <p className="text-sm font-medium text-ink-muted">{title}</p>
      {hint && <p className="max-w-sm text-xs leading-relaxed text-ink-subtle">{hint}</p>}
      {action && <div className="mt-2.5">{action}</div>}
    </div>
  );
}
