import { useEffect, useRef, useState } from "react";
import { cn } from "@/lib/utils";

/**
 * SandboxPreview 把模型生成的 HTML 在沙箱 iframe 里实时渲染出来。
 *
 * 安全约束：
 *  - sandbox="allow-scripts"：允许 JS 跑动画，但不给 allow-same-origin，
 *    iframe origin 为 null，脚本无法访问父页面 cookie/localStorage/DOM，
 *    fetch/XHR 也会因跨域 null origin 失败 → 天然满足"不要联网"。
 *  - 不给 allow-top-navigation / allow-popups：不能跳转/弹窗。
 *  - srcDoc 而非 src：内容内联，不产生额外网络请求。
 *
 * 自适应高度：iframe 加载后读取 contentDocument.body.scrollHeight 撑到内容
 * 高度（上限 maxHeight），避免动画被裁切；沙箱限制下 contentDocument 仍可读
 * （同源 null 的脚本访问受限于 allow-same-origin 缺失，但父页面读 contentDocument
 * 在多数浏览器允许；读取失败时回退固定高度）。
 */
export function SandboxPreview({
  html,
  title,
  className,
  minHeight = 320,
  maxHeight = 720,
}: {
  html: string;
  title: string;
  className?: string;
  minHeight?: number;
  maxHeight?: number;
}) {
  const ref = useRef<HTMLIFrameElement>(null);
  const [height, setHeight] = useState(minHeight);

  useEffect(() => {
    const iframe = ref.current;
    if (!iframe) return;
    const adjust = () => {
      try {
        const doc = iframe.contentDocument;
        if (!doc) return;
        const h = doc.documentElement?.scrollHeight ?? doc.body?.scrollHeight ?? 0;
        if (h > 0) setHeight(Math.min(Math.max(h, minHeight), maxHeight));
      } catch {
        // 沙箱限制下读取失败：保持 minHeight，不崩。
      }
    };
    // srcDoc 渲染是同步内联，load 事件后测量最稳；再加一个短延时兜底。
    iframe.addEventListener("load", adjust);
    const timer = window.setTimeout(adjust, 300);
    return () => {
      iframe.removeEventListener("load", adjust);
      window.clearTimeout(timer);
    };
  }, [html, minHeight, maxHeight]);

  return (
    <iframe
      ref={ref}
      title={title}
      srcDoc={html}
      sandbox="allow-scripts"
      className={cn(
        "w-full rounded-lg border border-border/50 bg-white",
        className,
      )}
      style={{ height: `${height}px` }}
    />
  );
}
