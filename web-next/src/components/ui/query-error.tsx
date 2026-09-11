import { Button } from "./button";

/**
 * 查询失败横幅 —— 错误与「空数据」是两回事。
 * 各列表页在 isError 时渲染本组件，而不是落到「还没有数据」空态，
 * 避免 5xx/网络抖动被误读为没有数据、诱导用户重复创建。
 */
export function QueryErrorBanner({ onRetry }: { onRetry: () => void }) {
  return (
    <div
      role="alert"
      className="flex items-center justify-between rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2.5 text-sm text-destructive"
    >
      <span>加载失败，请检查网络后重试</span>
      <Button variant="outline" size="sm" onClick={onRetry}>
        重试
      </Button>
    </div>
  );
}
