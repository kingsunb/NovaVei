import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "react-router-dom";
import { FlaskConical, ListChecks, ListOrdered, Plus } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { EVAL_PROMPT, type EvalTarget } from "@/lib/model-eval";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { PageToolbar } from "@/components/ui/page-toolbar";
import { QueryErrorBanner } from "@/components/ui/query-error";
import { SegmentedControl } from "@/components/ui/segmented-control";
import { Select } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { EvalDetail } from "@/components/model-eval/EvalDetail";
import { EvalHistory } from "@/components/model-eval/EvalHistory";
import { EvalQueue } from "@/components/model-eval/EvalQueue";
import { EvalRanking } from "@/components/model-eval/EvalRanking";
import { EvalSelection } from "@/components/model-eval/EvalSelection";

type EvalView = "current" | "ranking" | "queue" | "history";

export default function ModelEvalPage() {
  const qc = useQueryClient();
  const [params, setParams] = useSearchParams();
  const rawChannelId = Number(params.get("channel"));
  const channelId = Number.isSafeInteger(rawChannelId) && rawChannelId > 0 ? rawChannelId : 0;
  const modelName = params.get("model") ?? "";
  const viewParam = params.get("view");
  const view: EvalView = viewParam === "current" || viewParam === "ranking" || viewParam === "queue" || viewParam === "history" ? (viewParam as EvalView) : "history";
  const channelsQuery = useQuery({ queryKey: ["channels"], queryFn: api.listChannels });
  const [selectedIds, setSelectedIds] = useState<Set<number>>(new Set());
  const [detailId, setDetailId] = useState<number | null>(null);

  const allTargets = useMemo<EvalTarget[]>(() => (channelsQuery.data ?? []).flatMap((channel) =>
    channel.enabled ? channel.models.map((model) => ({
      channelId: channel.id,
      channelName: channel.name,
      channelType: channel.type,
      channelModelId: model.id,
      modelName: model.name,
    })) : [],
  ), [channelsQuery.data]);
  const scopeTargets = useMemo(() => allTargets.filter((target) => !channelId || target.channelId === channelId), [allTargets, channelId]);
  const selectedTargets = scopeTargets.filter((target) => selectedIds.has(target.channelModelId));

  const enqueueMut = useMutation({
    mutationFn: (channelModelIds: number[]) => api.enqueueEvals(channelModelIds),
    onSuccess: (res) => {
      void qc.invalidateQueries({ queryKey: ["model-eval", "queue", "list"] });
      toast.success(`已加入 ${res.enqueued.length} 个评估任务，可在队列视图查看进度`);
      changeView("queue");
    },
    onError: (e: Error) => toast.error(e.message || "入队失败"),
  });

  function changeView(nextView: EvalView) {
    const next = new URLSearchParams(params);
    next.set("view", nextView);
    setParams(next, { replace: true });
  }

  function showHistory(nextChannelId: number, nextModelName = "") {
    const next = new URLSearchParams(params);
    next.set("view", "history");
    next.set("channel", String(nextChannelId));
    if (nextModelName) next.set("model", nextModelName);
    else next.delete("model");
    if (nextChannelId !== channelId) setSelectedIds(new Set());
    setParams(next, { replace: true });
  }

  function reuseResult(record: { id: number }) {
    setDetailId(null);
    void api.addEvalRankFromHistory(record.id).then((data) => {
      qc.setQueryData(["model-eval", "rank", "list"], data);
      changeView("ranking");
      toast.success("已加入排序");
    }).catch((e: Error) => toast.error(e.message || "加入排序失败"));
  }

  function repeatResult(record: { channel_model_id: number }) {
    setDetailId(null);
    enqueueMut.mutate([record.channel_model_id]);
  }

  const busy = enqueueMut.isPending;

  return (
    <div className="min-w-0 space-y-4 pb-4">
      <PageToolbar
        leading={<div className="flex items-center gap-2"><FlaskConical className="h-5 w-5 text-primary-text" aria-hidden /><h1 className="text-lg font-semibold tracking-tight text-ink">模型评估</h1></div>}
        trailing={view === "history" ? <Button type="button" size="sm" onClick={() => changeView("current")}><Plus className="h-3.5 w-3.5" aria-hidden />新评估</Button> : undefined}
      />
      <p className="text-xs leading-relaxed text-ink-muted">用同一道 SVG 动画题比较模型表现，按渠道保存每次结果，支持回看、重测和按质量排序。</p>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <SegmentedControl
          aria-label="评估视图"
          value={view}
          onChange={changeView}
          size="md"
          options={[
            { value: "current", label: "当前评估" },
            { value: "ranking", label: "评估排序" },
            { value: "queue", label: "评估队列" },
            { value: "history", label: "评估历史" },
          ]}
        />
        <label className="flex min-w-0 max-w-full items-center gap-2 text-xs text-ink-muted">
          <span className="shrink-0">渠道范围</span>
          <Select
            aria-label="渠道范围"
            className="min-w-0 max-w-[min(65vw,20rem)]"
            value={channelId}
            disabled={busy}
            onChange={(event) => {
              const next = new URLSearchParams(params);
              if (event.target.value === "0") next.delete("channel");
              else next.set("channel", event.target.value);
              next.delete("model");
              setSelectedIds(new Set());
              setParams(next, { replace: true });
            }}
          >
            <option value={0}>全部渠道</option>
            {(channelsQuery.data ?? []).map((channel) => <option key={channel.id} value={channel.id}>{channel.name}{channel.enabled ? "" : "（已停用）"}</option>)}
            {channelId > 0 && !channelsQuery.data?.some((channel) => channel.id === channelId) && <option value={channelId}>渠道 #{channelId}</option>}
          </Select>
        </label>
      </div>

      {channelsQuery.isError && <QueryErrorBanner onRetry={() => void channelsQuery.refetch()} />}

      {view === "history" ? (
        <EvalHistory
          key={`${channelId}:${modelName}`}
          channelId={channelId}
          modelName={modelName}
          targets={allTargets}
          busy={busy}
          onPreview={setDetailId}
          onReuse={reuseResult}
          onRepeat={repeatResult}
          onNew={() => changeView("current")}
          onClearModel={() => { const next = new URLSearchParams(params); next.delete("model"); setParams(next, { replace: true }); }}
        />
      ) : view === "ranking" ? (
        <EvalRanking busy={busy} onShowHistory={showHistory} />
      ) : view === "queue" ? (
        <EvalQueue busy={busy} />
      ) : (
        <div className="grid min-w-0 items-start gap-4 xl:grid-cols-[19rem_minmax(0,1fr)]">
          <div className="min-w-0 space-y-3 xl:sticky xl:top-0">
            {channelsQuery.isLoading ? <Skeleton className="h-80 w-full" /> : (
              <EvalSelection key={channelId} targets={scopeTargets} selectedIds={selectedIds} onSelectionChange={setSelectedIds} disabled={busy || channelsQuery.isError} onRun={() => enqueueMut.mutate(selectedTargets.map((t) => t.channelModelId))} onHistory={showHistory} />
            )}
            <details className="rounded-lg border border-border/50 px-4 py-3 text-xs text-ink-muted">
              <summary className="cursor-pointer font-medium">本次评估题目</summary>
              <p className="mt-2 whitespace-pre-wrap break-words leading-relaxed">{EVAL_PROMPT}</p>
            </details>
          </div>
          <div className="min-w-0 space-y-3">
            <EmptyState
              icon={<ListOrdered className="h-5 w-5" />}
              title="选择模型开始评估"
              hint="点击左侧「开始评估」将所选模型加入评估队列，任务按顺序自动执行，结果自动保存到历史并加入排序。"
              action={<Button type="button" variant="ghost" size="sm" onClick={() => changeView("queue")}><ListOrdered className="h-3.5 w-3.5" aria-hidden />查看评估队列</Button>}
            />
          </div>
        </div>
      )}

      <EvalDetail id={detailId} onClose={() => setDetailId(null)} targets={allTargets} busy={busy} onReuse={reuseResult} onRepeat={repeatResult} />
    </div>
  );
}