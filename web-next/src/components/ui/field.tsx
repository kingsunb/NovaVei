import type { ReactNode } from "react";

/**
 * 表单字段 —— 统一 4 个页面里的 Field 组件
 *  替代 Channels / Groups / Keys / Settings 里的 4 份重复实现
 *  支持 required 标记 + hint + 错误态 + aria-invalid 透传
 */
export function Field({
  label,
  hint,
  required,
  error,
  children,
}: {
  label: string;
  hint?: string;
  required?: boolean;
  error?: string;
  children: ReactNode;
}) {
  return (
    <label className="block">
      <span className="mb-1 flex items-center gap-1 text-xs font-medium text-ink-muted">
        {label}
        {required && (
          <>
            {/* 星号仅视觉呈现；读屏由 sr-only「必填」播报 */}
            <span aria-hidden="true" className="text-destructive">
              *
            </span>
            <span className="sr-only">必填</span>
          </>
        )}
      </span>
      {children}
      {hint && !error && <p className="mt-1 text-[11px] text-ink-subtle">{hint}</p>}
      {error && (
        <p role="alert" className="mt-1 text-[11px] text-destructive">
          {error}
        </p>
      )}
    </label>
  );
}
