import { useMemo, useState } from "react";
import { cn } from "@/lib/utils";

/**
 * FormattedBody —— 日志请求体/响应体格式化展示（REQ-016）
 *
 *  - JSON 自动 2 空格缩进；SSE 逐事件块排版（data: 后 JSON 负载缩进）；其余原样
 *  - 顶部栏标注格式（JSON / SSE / Text）与字节大小（< 1 KB 或 X.X KB）
 *  - 一键复制原始内容（格式化前），复制后 2s 内显示「已复制」
 *  - 默认全展开（最高 60vh，超出滚动），无折叠/收起按钮
 *
 * 纯手写，不引入外部依赖；样式与既有 <pre> 一致。
 */
export function FormattedBody({
  content,
  loading = false,
  className,
}: {
  content: string;
  loading?: boolean;
  className?: string;
}) {
  const [copied, setCopied] = useState(false);

  const { format, formatted } = useMemo(() => detectAndFormat(content), [content]);
  const bytes = useMemo(
    () => (content ? new TextEncoder().encode(content).byteLength : 0),
    [content],
  );

  async function handleCopy() {
    try {
      await navigator.clipboard.writeText(content);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      /* 剪贴板不可用时静默忽略 */
    }
  }

  if (loading) {
    return (
      <div
        className={cn(
          "mono rounded-md bg-surface-subtle/30 p-3 text-xs text-ink-muted",
          className,
        )}
      >
        加载中…
      </div>
    );
  }

  if (!content) {
    return (
      <div
        className={cn(
          "mono rounded-md bg-surface-subtle/30 p-3 text-xs text-ink-muted",
          className,
        )}
      >
        （空）
      </div>
    );
  }

  return (
    <div className={cn("rounded-md bg-surface-subtle/30", className)}>
      {/* 顶部栏：格式标签 + 字节大小 + 复制按钮 */}
      <div className="flex items-center gap-2 border-b border-border/60 px-3 py-1.5 text-[11px] text-ink-muted">
        <span className="font-medium text-ink">{FORMAT_LABEL[format]}</span>
        <span>· {formatByteSize(bytes)}</span>
        <button
          type="button"
          onClick={handleCopy}
          className="ml-auto rounded-control px-1.5 py-0.5 transition-colors hover:bg-surface-subtle/60 hover:text-ink"
        >
          {copied ? "已复制" : "复制"}
        </button>
      </div>
      <pre className="mono max-h-[60vh] overflow-auto p-3 text-xs leading-relaxed">
        {formatted}
      </pre>
    </div>
  );
}

// ---------------- 内部工具 ----------------

type BodyFormat = "json" | "sse" | "text";

const FORMAT_LABEL: Record<BodyFormat, string> = {
  json: "JSON",
  sse: "SSE",
  text: "Text",
};

/** 字节大小展示：< 1 KB / X.X KB / X.X MB，与 REQ-016 规范一致。 */
function formatByteSize(bytes: number): string {
  if (bytes < 1024) return "< 1 KB";
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

/** 检测内容格式并产出格式化展示文本；非 JSON/SSE 保持原文。 */
function detectAndFormat(content: string): {
  format: BodyFormat;
  formatted: string;
} {
  if (!content) return { format: "text", formatted: "" };

  // JSON：以 { 或 [ 起始时尝试解析，避免对任意文本都付出 parse 成本
  const trimmed = content.trim();
  if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
    try {
      const parsed = JSON.parse(trimmed);
      return { format: "json", formatted: JSON.stringify(parsed, null, 2) };
    } catch {
      /* 非合法 JSON，回退到后续检测 */
    }
  }

  // SSE：含 data: 前缀的事件流
  if (/\bdata:\s/.test(content)) {
    return { format: "sse", formatted: formatSSE(content) };
  }

  return { format: "text", formatted: content };
}

/**
 * SSE 逐行排版：data: 行后的 JSON 负载做 2 空格缩进格式化；
 * 非 data: 行（event:/id:/空行等）与无法解析的 data: 负载保持原样。
 */
function formatSSE(content: string): string {
  return content
    .split("\n")
    .map((line) => {
      const m = /^(\s*data:\s*)(.*)$/.exec(line);
      if (!m) return line;
      const prefix = m[1];
      const payload = m[2];
      // [DONE] 等哨兵值与非 JSON 负载原样保留
      if (!payload || payload === "[DONE]") return line;
      try {
        const parsed = JSON.parse(payload);
        return `${prefix}${JSON.stringify(parsed, null, 2)}`;
      } catch {
        return line;
      }
    })
    .join("\n");
}
