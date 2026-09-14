import { useMemo } from "react";
import { cn } from "@/lib/utils";

/**
 * 使用稳定的预览视窗：隔离源 iframe 的 contentDocument 不可读，不能据此计算高度。
 * 保留脚本动画能力；独立源隔离父页面，CSP 限制外部资源和网络 API。
 */
export function SandboxPreview({
  html,
  title,
  className,
  minHeight = 360,
  maxHeight = 720,
}: {
  html: string;
  title: string;
  className?: string;
  minHeight?: number;
  maxHeight?: number;
}) {
  const srcDoc = useMemo(() => {
    const doc = new DOMParser().parseFromString(html, "text/html");
    doc.querySelectorAll('meta[http-equiv="refresh" i], base').forEach((node) => node.remove());
    const policy = doc.createElement("meta");
    policy.httpEquiv = "Content-Security-Policy";
    policy.content = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; media-src data: blob:; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'";
    doc.head.prepend(policy);
    return `<!DOCTYPE html>\n${doc.documentElement.outerHTML}`;
  }, [html]);

  return (
    <iframe
      title={title}
      srcDoc={srcDoc}
      sandbox="allow-scripts"
      referrerPolicy="no-referrer"
      className={cn(
        "block w-full rounded-lg border border-border/50 bg-white",
        className,
      )}
      style={{ height: `clamp(${minHeight}px, 65vh, ${maxHeight}px)` }}
    />
  );
}
