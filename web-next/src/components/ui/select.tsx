import * as React from "react";
import { cn } from "@/lib/utils";

/**
 * 下拉框 —— 统一各页面散落的原生 <select>
 *  - 与 Input 同一套边框 / 玻璃底 / 焦点环，替代此前 h-7/h-8、有无焦点态不一的裸 select
 *  - appearance-none + .select-chevron 背景自绘箭头；保留原生键盘与读屏行为
 *  - 默认宽度 auto（随内容），需要撑满时调用方传 w-full
 */
export const Select = React.forwardRef<
  HTMLSelectElement,
  React.SelectHTMLAttributes<HTMLSelectElement>
>(({ className, children, ...props }, ref) => (
  <select
    ref={ref}
    className={cn(
      "select-chevron h-8 appearance-none rounded-control border border-border/60 bg-card/60 pl-2.5 pr-7 text-[13px] tracking-tight text-ink backdrop-blur-sm",
      "transition-all duration-150",
      "focus:bg-card/80 focus:outline-none focus:ring-[3px] focus:ring-primary/15 focus:border-primary/40",
      "disabled:cursor-not-allowed disabled:opacity-50",
      className,
    )}
    {...props}
  >
    {children}
  </select>
));
Select.displayName = "Select";
