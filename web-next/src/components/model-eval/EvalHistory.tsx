import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, History, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { findEvalTarget, formatEvalTime, type EvalHistoryQuery, type EvalOutcome, type EvalRecordSummary, type EvalTarget } from "@/lib/model-eval";
import { formatNumber } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { QueryErrorBanner } from "@/components/ui/query-error";
import { SearchField } from "@/components/ui/search-field";
import { Select } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { EvalOutcomeBadge } from "./EvalOutcomeBadge";

export function EvalHistory({
  channelId,
  modelName,
  targets,
  busy,
  onPreview,
  onReuse,
  onRepeat,
  onNew,
  onClearModel,
}: {
  channelId: number;
  modelName: string;
  targets: EvalTarget[];
  busy: boolean;
  onPreview: (id: number) => void;
  onReuse: (record: EvalRecordSummary) => void;
  onRepeat: (record: EvalRecordSummary) => void;
  onNew: () => void;
  onClearModel: () => void;
}) {
  const [search, setSearch] = useState("");
  const [outcome, setOutcome] = useState<EvalOutcome | "">("");
  const [page, setPage] = useState(1);
  const qc = useQueryClient();
  const filters: EvalHistoryQuery = {
    channel_id: channelId || undefined,
    model_name: modelName || undefined,
    query: search.trim() || undefined,
    outcome: outcome || undefined,
    page,
    page_size: 20,
  };
  const { data, isLoading, isFetching, isError, refetch } = useQuery({
    queryKey: ["model-eval", "history", filters],
    queryFn: ({ signal }) => api.listModelEvals(filters, signal),
  });
  const totalPages = Math.max(1, Math.ceil((data?.total ?? 0) / 20));
  const [confirmClearFailures, setConfirmClearFailures] = useState(false);
  const clearFailuresMut = useMutation({
    mutationFn: () => api.clearEvalFailures(),
    onSuccess: (res) => {
      void qc.invalidateQueries({ queryKey: ["model-eval", "history"] });
      toast.success(`已清空 ${res.removed} 条失败记录`);
      setConfirmClearFailures(false);
    },
    onError: (e: Error) => toast.error(e.message || "清空失败记录失败"),
  });

  return (
    <Card className="min-w-0 overflow-hidden">
      <div className="space-y-3 border-b border-border/50 p-4">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <History className="h-4 w-4 text-ink-muted" aria-hidden />
            <h2 className="text-sm font-semibold text-ink">评估历史</h2>
            {data && <span className="text-xs text-ink-subtle">{formatNumber(data.total)} 条记录</span>}
          </div>
          <div className="flex items-center gap-2">
            <Button type="button" variant="ghost" size="sm" onClick={() => setConfirmClearFailures(true)} disabled={clearFailuresMut.isPending}>
              <Trash2 className="h-3.5 w-3.5" aria-hidden />清空失败记录
            </Button>
            <Button type="button" variant="ghost" size="sm" onClick={() => void refetch()} disabled={isFetching}>
              <RefreshCw className={`h-3.5 w-3.5 ${isFetching ? "animate-spin" : ""}`} aria-hidden />
              刷新
            </Button>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <SearchField
            value={search}
            onChange={(value) => { setSearch(value); setPage(1); }}
            placeholder="搜索渠道或模型名称"
            aria-label="搜索评估历史"
            maxLength={200}
            className="min-w-0 flex-1 sm:max-w-sm"
            inputClassName="w-full"
          />
          <Select
            aria-label="评估结果筛选"
            value={outcome}
            onChange={(event) => { setOutcome(event.target.value as EvalOutcome | ""); setPage(1); }}
          >
            <option value="">全部结果</option>
            <option value="ok">格式合规</option>
            <option value="violation">格式不符</option>
            <option value="error">请求失败</option>
          </Select>
        </div>
        {modelName && (
          <div className="flex min-w-0 items-center gap-2 text-xs text-ink-muted">
            <span className="min-w-0 break-all">模型：<span className="font-mono">{modelName}</span></span>
            <button type="button" onClick={onClearModel} className="shrink-0 text-primary-text hover:underline">清除模型筛选</button>
          </div>
        )}
      </div>

      {confirmClearFailures && (
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-amber-500/20 bg-amber-500/5 px-4 py-3 text-xs">
          <span className="text-amber-700 dark:text-amber-400">确认清空所有评估结果为「请求失败」的历史记录？该操作不可恢复。</span>
          <div className="flex items-center gap-2">
            <Button type="button" variant="ghost" size="sm" onClick={() => setConfirmClearFailures(false)}>取消</Button>
            <Button type="button" variant="destructive" size="sm" onClick={() => clearFailuresMut.mutate()} loading={clearFailuresMut.isPending}>确认清空</Button>
          </div>
        </div>
      )}

      {isError ? (
        <div className="p-4"><QueryErrorBanner onRetry={() => void refetch()} /></div>
      ) : isLoading ? (
        <div className="space-y-3 p-4"><Skeleton className="h-20 w-full" /><Skeleton className="h-20 w-full" /><Skeleton className="h-20 w-full" /></div>
      ) : !data?.items.length ? (
        <EmptyState
          icon={<History className="h-5 w-5" />}
          title={search || outcome || modelName ? "没有匹配的评估记录" : "还没有评估历史"}
          hint={search || outcome || modelName ? "调整筛选条件后再查看。" : "完成一次模型评估后，结果会保存在这里，刷新页面也能回看。"}
          action={<Button type="button" size="sm" onClick={onNew}>选择模型开始评估</Button>}
        />
      ) : (
        <>
          <div className="hidden grid-cols-[minmax(0,1fr)_6rem_8rem_13rem] gap-4 border-b border-border/40 bg-ink/[0.02] px-4 py-2 text-[11px] text-ink-subtle xl:grid">
            <span>渠道 / 模型 / 时间</span><span>结果</span><span>耗时 / Token 用量</span><span className="text-right">操作</span>
          </div>
          <ul className="divide-y divide-border/40">
            {data.items.map((record) => {
              const available = !!findEvalTarget(record, targets);
              return (
                <li key={record.id} className="grid min-w-0 items-center gap-3 px-4 py-4 sm:grid-cols-[minmax(0,1fr)_auto] xl:grid-cols-[minmax(0,1fr)_6rem_8rem_13rem] xl:gap-4">
                  <div className="min-w-0">
                    <p className="break-all font-mono text-[13px] font-medium text-ink">{record.model_name}</p>
                    <p className="mt-1 truncate text-xs text-ink-muted" title={record.channel_name}>{record.channel_name} · {record.channel_type}</p>
                    <time dateTime={record.created_at} className="mt-1 block text-[11px] tabular-nums text-ink-subtle">{formatEvalTime(record.created_at)}</time>
                    {record.error && <p className="mt-1.5 line-clamp-2 break-all text-xs text-destructive" title={record.error}>{record.error}</p>}
                  </div>
                  <div className="flex flex-wrap items-center gap-2 sm:justify-self-end xl:justify-self-start">
                    <EvalOutcomeBadge outcome={record.outcome} />
                    {!available && <span className="text-[11px] text-ink-subtle">模型不可用</span>}
                  </div>
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs tabular-nums text-ink-muted xl:block xl:space-y-1">
                    <p>{formatNumber(record.latency_ms)} ms</p>
                    <p title="输入 / 输出 Token">{formatNumber(record.prompt_tokens)} / {formatNumber(record.completion_tokens)} <span className="text-[10px] text-ink-subtle">tok</span></p>
                  </div>
                  <div className="flex flex-wrap items-center gap-1 sm:justify-end">
                    <Button type="button" variant="secondary" size="sm" onClick={() => onPreview(record.id)}>查看</Button>
                    <Button type="button" variant="ghost" size="sm" onClick={() => onReuse(record)} disabled={busy || !available || record.outcome !== "ok"} title={record.outcome === "ok" ? "复用这次结果加入当前排序" : "仅格式合规的成功评估可加入排序"}>加入排序</Button>
                    <Button type="button" variant="ghost" size="sm" onClick={() => onRepeat(record)} disabled={busy || !available} title="新增一次评估，保留原记录">重测</Button>
                  </div>
                </li>
              );
            })}
          </ul>
        </>
      )}
      {data && data.total > 0 && !isError && (
        <div className="flex flex-wrap items-center justify-between gap-2 border-t border-border/50 px-4 py-3 text-xs text-ink-muted">
          <span>第 {page} / {totalPages} 页 · 每页 20 条</span>
          <div className="flex items-center gap-1">
            <Button type="button" variant="ghost" size="sm" onClick={() => setPage((value) => value - 1)} disabled={page <= 1 || isFetching}>
              <ChevronLeft className="h-3.5 w-3.5" aria-hidden />上一页
            </Button>
            <Button type="button" variant="ghost" size="sm" onClick={() => setPage((value) => value + 1)} disabled={page >= totalPages || isFetching}>
              下一页<ChevronRight className="h-3.5 w-3.5" aria-hidden />
            </Button>
          </div>
        </div>
      )}
    </Card>
  );
}
