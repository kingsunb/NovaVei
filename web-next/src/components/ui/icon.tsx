import * as React from "react";
import { cn } from "@/lib/utils";

type LucideIcon = React.ComponentType<{ className?: string }>;

interface IconProps {
  /** Lucide 图标组件 */
  as: LucideIcon;
  /** 与 Lucide 一致 */
  className?: string;
  /** 默认 true，装饰用；功能性图标（如唯一可点元素）需传 false 并自配 aria-label */
  decorative?: boolean;
}

/**
 * 图标包装 —— 解决两类问题：
 *  1. 装饰性图标（紧邻文本或作为 icon-only 按钮的视觉）应被屏幕阅读器忽略
 *  2. 集中管理 aria-hidden，避免 36+ 处重复声明
 *
 * 用法：
 *   <Icon as={Plus} className="h-4 w-4" aria-hidden />                       // 装饰（默认）
 *   <Icon as={Trash2} decorative={false} className="h-4 w-4" aria-hidden />  // 信息性，外部按钮带 aria-label
 */
export function Icon({ as: As, className, decorative = true }: IconProps) {
  return (
    <As
      className={cn("shrink-0", className)}
      aria-hidden={decorative ? true : undefined}
    />
  );
}
