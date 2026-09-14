import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { RotateCcw } from "lucide-react";
import { api } from "@/lib/api";
import { extractRenderableHtml, findEvalTarget, formatEvalTime, type EvalRecordSummary, type EvalTarget } from "@/lib/model-eval";
import { formatNumber } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { QueryErrorBanner } from "@/components/ui/query-error";
import { SegmentedControl } from "@/components/ui/segmented-control";
import { Skeleton } from "@/components/ui/skeleton";
import { EvalOutcomeBadge } from "./EvalOutcomeBadge";
import { SandboxPreview } from "./SandboxPreview";

interface DetailActions {
  targets: EvalTarget[];
  busy: boolean;
  onReuse: (record: EvalRecordSummary) => void;
  onRepeat: (record: EvalRecordSummary) => void;
}

export function EvalDetail({ id, onClose, ...actions }: DetailActions & { id: number | null; onClose: () => void }) {
  return (
    <Dialog open={id !== null} onOpenChange={(open) => { if (!open) onClose(); }}>
      {id !== null && <EvalDetailContent key={id} id={id} {...actions} />}
    </Dialog>
  );
}

function EvalDetailContent({ id, targets, busy, onReuse, onRepeat }: DetailActions & { id: number }) {
  const [view, setView] = useState<"preview" | "raw" | "prompt">("preview");
  const [replay, setReplay] = useState(0);
  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ["model-eval", "detail", id],
    queryFn: ({ signal }) => api.getModelEval(id, signal),
    staleTime: Infinity,
  });
  const html = useMemo(() => extractRenderableHtml(data?.content ?? ""), [data?.content]);
  const available = data && !!findEvalTarget(data, targets);

  return (
    <DialogContent variant="wide" className="w-full max-w-[1200px] sm:w-[92%] lg:w-4/5">
      <DialogHeader className="shrink-0">
        <DialogTitle className="break-all">{data ? data.model_name : "评估详情"}</DialogTitle>
        <DialogDescription className="break-all">{data ? `${data.channel_name} · ${formatEvalTime(data.created_at)}` : "读取已保存的评估记录"}</DialogDescription>
      </DialogHeader>
      <DialogBody className="min-h-0 space-y-4 p-4 sm:p-5">
        {isError ? <QueryErrorBanner onRetry={() => void refetch()} /> : isLoading ? (
          <><Skeleton className="h-16 w-full" /><Skeleton className="h-80 w-full" /></>
        ) : data && (
          <>
            <div className="flex flex-wrap items-center justify-between gap-3">
              <EvalOutcomeBadge outcome={data.outcome} />
              <div className="flex flex-wrap gap-x-5 gap-y-1 text-xs tabular-nums text-ink-muted">
                <span>耗时 {formatNumber(data.latency_ms)} ms</span>
                <span>输入 {formatNumber(data.prompt_tokens)} tok</span>
                <span>输出 {formatNumber(data.completion_tokens)} tok</span>
              </div>
            </div>
            {data.error && <p className="whitespace-pre-wrap break-all rounded-lg bg-destructive/5 p-3 text-xs leading-relaxed text-destructive">{data.error}</p>}
            {data.content_truncated && <p className="rounded-lg bg-amber-500/10 p-3 text-xs text-amber-700 dark:text-amber-400">回复超过 1 MB，已保留前 1 MB 内容，预览可能不完整。</p>}
            <div className="flex flex-wrap items-center justify-between gap-2">
              <SegmentedControl
                aria-label="评估详情内容"
                value={view}
                onChange={setView}
                options={[{ value: "preview", label: "效果预览" }, { value: "raw", label: "原始回复" }, { value: "prompt", label: "评估题目" }]}
              />
              {view === "preview" && html && <Button type="button" variant="ghost" size="sm" onClick={() => setReplay((value) => value + 1)}><RotateCcw className="h-3.5 w-3.5" aria-hidden />重播</Button>}
            </div>
            {view === "preview" ? html ? (
              <SandboxPreview key={replay} html={html} title={`${data.channel_name} · ${data.model_name} 评估预览`} />
            ) : (
              <EmptyState title="没有可预览的 HTML" hint="可以切换到原始回复查看内容，或查看本次请求的失败原因。" />
            ) : (
              <pre className="max-h-[65vh] overflow-auto whitespace-pre-wrap break-all rounded-lg bg-ink/[0.03] p-4 font-mono text-xs leading-relaxed text-ink-muted">
                {(view === "raw" ? data.content : data.prompt) || "本次请求未返回内容。"}
              </pre>
            )}
            <p className="text-[11px] leading-relaxed text-ink-subtle">格式状态只检查回复标记，画面质量请通过预览判断。</p>
          </>
        )}
      </DialogBody>
      <DialogFooter className="shrink-0 flex-wrap">
        {data && !available && <p className="mr-auto text-xs text-ink-subtle">渠道或模型已不可用，历史记录仍可查看。</p>}
        <Button type="button" variant="secondary" size="sm" onClick={() => { if (data) onReuse(data); }} disabled={!data || busy || !available || data.outcome === "error"}>加入当前排序</Button>
        <Button type="button" size="sm" onClick={() => { if (data) onRepeat(data); }} disabled={!data || busy || !available}>重新评估</Button>
      </DialogFooter>
    </DialogContent>
  );
}
