import type { InputHTMLAttributes } from "react";
import { Search } from "lucide-react";
import { cn } from "@/lib/utils";
import { Input } from "./input";

/**
 * 搜索框 —— 统一列表页工具栏里「图标 + Input」的重复结构
 */
export function SearchField({
  value,
  onChange,
  placeholder,
  className,
  inputClassName,
  ...props
}: {
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  className?: string;
  inputClassName?: string;
} & Omit<InputHTMLAttributes<HTMLInputElement>, "value" | "onChange" | "size">) {
  return (
    <label className={cn("relative block", className)}>
      <Search
        className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-ink-subtle"
        aria-hidden
      />
      <Input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className={cn("h-8 w-56 pl-8", inputClassName)}
        {...props}
      />
    </label>
  );
}
