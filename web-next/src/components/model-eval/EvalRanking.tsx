import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, ListChecks, Plus, RefreshCw, X } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { PRO_GROUP_NAME, extractRenderableHtml, formatEvalTime, type EvalRankSummary } from "@/lib/model-eval";
import { formatNumber } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { QueryErrorBanner } from "@/components/ui/query-error";
import { Skeleton } from "@/components/ui/skeleton";
import { EvalOutcomeBadge } from "./EvalOutcomeBadge";
import { SandboxPreview } from "./SandboxPreview";

export function EvalRanking({ busy, onShowHistory }: { busy: boolean; onShowHistory: (channelId: number, modelName?: string) => void }) {
  const qc = useQueryClient();
  const [expanded, setExpanded] = useState<Record<number, boolean>>({});
  const ranksQuery = useQuery({
    queryKey: ["model-eval", "rank", "list"],
    queryFn: ({ signal }) => api.listEvalRanks(signal),
  });
  const groupsQuery = useQuery({ queryKey: ["groups"], queryFn: api.listGroups });
  const items = ranksQuery.data?.items ?? [];
  const rankable = items.filter((r) => r.outcome !== "error");
  const failed = items.filter((r) => r.outcome === "error");
  const existingPro = groupsQuery.data?.find((g) => g.name === PRO_GROUP_NAME);

  const applyProMut = useMutation({
    mutationFn: () => api.applyProGroup(),
    onSuccess: (res) => {
      void qc.invalidateQueries({ queryKey: ["groups"] });
      toast.success(`分组 ${PRO_GROUP_NAME} 已${res.created ? "创建" : "更新"}，共 ${res.item_count} 个成员`);
    },
    onError: (e: Error) => toast.error(e.message || "更新分组失败"),
  });

  const moveMut = useMutation({
    mutationFn: (vars: { id: number; direction: -1 | 1 }) => api.moveEvalRank(vars.id, vars.direction),
    onSuccess: (data) => qc.setQueryData(["model-eval", "rank", "list"], data),
    onError: (e: Error) => toast.error(e.message || "调整排序失败"),
  });
  const removeMut = useMutation({
    mutationFn: (id: number) => api.removeEvalRank(id),
    onSuccess: (data) => qc.setQueryData(["model-eval", "rank", "list"], data),
    onError: (e: Error) => toast.error(e.message || "移除失败"),
  });

  function toggle(id: number) {
    setExpanded((prev) => ({ ...prev, [id]: !prev[id] }));
  }

  return (
    <Card className="min-w-0 overflow-hidden">
      <div className="space-y-2 border-b border-border/50 p-4">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <ListChecks className="h-4 w-4 text-ink-muted" aria-hidden />
            <h2 className="text-sm font-semibold text-ink">评估排序 <span className="font-normal text-ink-subtle">· {rankable.length} 个可入组{failed.length > 0 ? ` / ${failed.length} 个失败` : ""}</span></h2>
          </div>
          <div className="flex items-center gap-2">
            <Button type="button" variant="ghost" size="sm" onClick={() => void ranksQuery.refetch()} disabled={ranksQuery.isFetching}>
              <RefreshCw className={`h-3.5 w-3.5 ${ranksQuery.isFetching ? "animate-spin" : ""}`} aria-hidden />刷新
            </Button>
            <Button type="button" variant="secondary" size="sm" onClick={() => applyProMut.mutate()} disabled={busy || rankable.length === 0 || applyProMut.isPending} loading={applyProMut.isPending}>
              <Plus className="h-3.5 w-3.5" aria-hidden />{existingPro ? "更新" : "创建"} {PRO_GROUP_NAME} 分组
            </Button>
          </div>
        </div>
        <p className="text-[11px] leading-relaxed text-ink-subtle">跨渠道排列，越靠前优先级越高；格式不符默认排后，失败结果保留在下方。{existingPro ? `更新将用当前排序替换 ${PRO_GROUP_NAME} 的现有成员。` : "可从历史记录加入已有结果。"}</p>
      </div>

      {ranksQuery.isError ? (
        <div className="p-4"><QueryErrorBanner onRetry={() => void ranksQuery.refetch()} /></div>
      ) : ranksQuery.isLoading ? (
        <div className="space-y-3 p-4"><Skeleton className="h-20 w-full" /><Skeleton className="h-20 w-full" /><Skeleton className="h-20 w-full" /></div>
      ) : items.length === 0 ? (
        <EmptyState icon={<ListChecks className="h-5 w-5" />} title="还没有排序结果" hint="完成一次评估后结果会自动加入排序，也可以从历史记录手动加入。" />
      ) : (
        <ul className="divide-y divide-border/40">
          {rankable.map((record, index) => (
            <RankItem key={record.id} record={record} index={index} total={rankable.length} busy={busy} expanded={!!expanded[record.id]} onToggle={() => toggle(record.id)} onMove={(d) => moveMut.mutate({ id: record.id, direction: d })} onRemove={() => removeMut.mutate(record.id)} onHistory={() => onShowHistory(record.channel_id, record.model_name)} movePending={moveMut.isPending} removePending={removeMut.isPending} />
          ))}
          {failed.map((record) => (
            <li key={record.id} className="min-w-0 space-y-2 p-4 opacity-80">
              <div className="flex items-start gap-3">
                <span className="shrink-0 text-xs font-semibold tabular-nums text-ink-subtle">—</span>
                <div className="min-w-0 flex-1">
                  <p className="break-all font-mono text-[13px] font-medium text-ink">{record.model_name}</p>
                  <p className="mt-1 break-all text-xs text-ink-muted">{record.channel_name}</p>
                  <time dateTime={record.created_at} className="mt-1 block text-[11px] tabular-nums text-ink-subtle">{formatEvalTime(record.created_at)}</time>
                </div>
                <Button type="button" variant="ghost" size="icon" className="h-7 w-7 shrink-0" disabled={busy} onClick={() => removeMut.mutate(record.id)} aria-label={`移除 ${record.channel_name} ${record.model_name}`} title="从排序移除"><X className="h-3.5 w-3.5" aria-hidden /></Button>
              </div>
              <div className="flex flex-wrap items-center gap-x-3 gap-y-2 pl-7">
                <EvalOutcomeBadge outcome={record.outcome} />
                <span className="text-[11px] tabular-nums text-ink-subtle">{formatNumber(record.latency_ms)} ms</span>
                <Button type="button" variant="ghost" size="sm" onClick={() => onShowHistory(record.channel_id, record.model_name)}>历史</Button>
              </div>
              {record.error && <p className="line-clamp-3 break-all pl-7 text-xs leading-relaxed text-destructive">{record.error}</p>}
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

function RankItem({ record, index, total, busy, expanded, onToggle, onMove, onRemove, onHistory, movePending, removePending }: {
  record: EvalRankSummary;
  index: number;
  total: number;
  busy: boolean;
  expanded: boolean;
  onToggle: () => void;
  onMove: (direction: -1 | 1) => void;
  onRemove: () => void;
  onHistory: () => void;
  movePending: boolean;
  removePending: boolean;
}) {
  return (
    <li className="min-w-0 space-y-3 p-4">
      <div className="flex items-start gap-3">
        <div className="flex w-7 shrink-0 flex-col items-center gap-0.5">
          <span className="text-xs font-semibold tabular-nums text-ink-subtle">#{index + 1}</span>
          <Button type="button" variant="ghost" size="icon" className="h-6 w-6" onClick={() => onMove(-1)} disabled={busy || movePending || index === 0} aria-label="上移"><ArrowUp className="h-3.5 w-3.5" aria-hidden /></Button>
          <Button type="button" variant="ghost" size="icon" className="h-6 w-6" onClick={() => onMove(1)} disabled={busy || movePending || index === total - 1} aria-label="下移"><ArrowDown className="h-3.5 w-3.5" aria-hidden /></Button>
        </div>
        <div className="min-w-0 flex-1">
          <p className="break-all font-mono text-[13px] font-medium text-ink">{record.model_name}</p>
          <p className="mt-1 break-all text-xs text-ink-muted">{record.channel_name}</p>
          <time dateTime={record.created_at} className="mt-1 block text-[11px] tabular-nums text-ink-subtle">{formatEvalTime(record.created_at)}</time>
        </div>
        <Button type="button" variant="ghost" size="icon" className="h-7 w-7 shrink-0" disabled={busy || removePending} onClick={onRemove} aria-label="移除" title="从排序移除，保留历史"><X className="h-3.5 w-3.5" aria-hidden /></Button>
      </div>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 pl-10">
        <EvalOutcomeBadge outcome={record.outcome} />
        <span className="text-[11px] tabular-nums text-ink-subtle">{formatNumber(record.latency_ms)} ms · {formatNumber(record.completion_tokens)} 输出 tok</span>
        <div className="flex flex-wrap items-center gap-1 sm:ml-auto">
          <Button type="button" variant="ghost" size="sm" onClick={onToggle}>{expanded ? "收起预览" : "查看预览"}</Button>
          <Button type="button" variant="ghost" size="sm" onClick={onHistory}>历史</Button>
        </div>
      </div>
      {record.error && <p className="line-clamp-3 break-all pl-10 text-xs leading-relaxed text-destructive">{record.error}</p>}
      {expanded && <RankPreview id={record.id} channelName={record.channel_name} modelName={record.model_name} truncated={record.content_truncated} />}
    </li>
  );
}

function RankPreview({ id, channelName, modelName, truncated }: { id: number; channelName: string; modelName: string; truncated: boolean }) {
  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ["model-eval", "rank", "content", id],
    queryFn: ({ signal }) => api.getEvalRankContent(id, signal),
    staleTime: Infinity,
  });
  const html = data ? extractRenderableHtml(data.content) : "";
  return (
    <div className="pl-10">
      {truncated && <p className="mb-2 rounded-lg bg-amber-500/10 p-2 text-[11px] text-amber-700 dark:text-amber-400">回复超过 1 MB，已保留前 1 MB，预览可能不完整。</p>}
      {isError ? (
        <QueryErrorBanner onRetry={() => void refetch()} />
      ) : isLoading ? (
        <Skeleton className="h-72 w-full" />
      ) : html ? (
        <SandboxPreview html={html} title={`${channelName} · ${modelName} 评估预览`} />
      ) : (
        <EmptyState title="没有可预览的 HTML" hint="可以到历史记录查看原始回复。" className="py-6" />
      )}
    </div>
  );
}