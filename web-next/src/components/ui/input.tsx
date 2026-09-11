import * as React from "react";
import { cn } from "@/lib/utils";

/**
 * 输入框 —— macOS 风格
 *  - 半透明玻璃底 + backdrop-blur
 *  - focus 时蓝色 ring + 边框加深
 */
export const Input = React.forwardRef<
  HTMLInputElement,
  React.InputHTMLAttributes<HTMLInputElement> & { invalid?: boolean }
>(({ className, type, invalid, ...props }, ref) => (
  <input
    type={type}
    ref={ref}
    aria-invalid={invalid || undefined}
    className={cn(
      "h-8 w-full rounded-control border bg-card/60 px-3 text-[13px] tracking-tight text-ink backdrop-blur-sm placeholder:text-ink-subtle",
      "transition-all duration-150",
      "focus:bg-card/80 focus:outline-none focus:ring-[3px] focus:ring-primary/15 focus:border-primary/40",
      "disabled:cursor-not-allowed disabled:opacity-50",
      invalid
        ? "border-destructive/50 focus:ring-destructive/15 focus:border-destructive"
        : "border-border/60",
      className,
    )}
    {...props}
  />
));
Input.displayName = "Input";

export const Textarea = React.forwardRef<
  HTMLTextAreaElement,
  React.TextareaHTMLAttributes<HTMLTextAreaElement>
>(({ className, ...props }, ref) => (
  <textarea
    ref={ref}
    className={cn(
      "min-h-[80px] w-full rounded-control border border-border/60 bg-card/60 px-3 py-2 text-[13px] tracking-tight text-ink backdrop-blur-sm placeholder:text-ink-subtle",
      "transition-all duration-150",
      "focus:bg-card/80 focus:outline-none focus:ring-[3px] focus:ring-primary/15 focus:border-primary/40",
      "disabled:cursor-not-allowed disabled:opacity-50",
      className,
    )}
    {...props}
  />
));
Textarea.displayName = "Textarea";
