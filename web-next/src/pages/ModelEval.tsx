import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "react-router-dom";
import { ArrowDown, ArrowUp, FlaskConical, History, Loader2, Plus, X } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { DEFAULT_GROUP_RELAY_CONFIG } from "@/lib/types";
import { EVAL_CONCURRENCY, EVAL_PROMPT, PRO_GROUP_NAME, findEvalTarget, formatEvalTime, priorityFromOrder, sameEvalTarget, type EvalRecordSummary, type EvalTarget } from "@/lib/model-eval";
import { formatNumber } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { PageToolbar } from "@/components/ui/page-toolbar";
import { QueryErrorBanner } from "@/components/ui/query-error";
import { SegmentedControl } from "@/components/ui/segmented-control";
import { Select } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { EvalDetail } from "@/components/model-eval/EvalDetail";
import { EvalHistory } from "@/components/model-eval/EvalHistory";
import { EvalOutcomeBadge } from "@/components/model-eval/EvalOutcomeBadge";
import { EvalSelection } from "@/components/model-eval/EvalSelection";

type EvalView = "history" | "current";
interface ActiveRun {
  controller: AbortController;
  stopQueued: boolean;
}

/** 排序只保留每个渠道模型选中的一次结果；完整的历次记录由服务端保存。 */
function mergeResult(previous: EvalRecordSummary[], record: EvalRecordSummary): EvalRecordSummary[] {
  const index = previous.findIndex((item) => sameEvalTarget(item, record));
  if (index >= 0) return previous.map((item, position) => position === index ? record : item);
  const rank = { ok: 0, violation: 1, error: 2 };
  const insertAt = previous.findIndex((item) => rank[item.outcome] > rank[record.outcome]);
  const next = [...previous];
  next.splice(insertAt < 0 ? next.length : insertAt, 0, record);
  return next;
}

export default function ModelEvalPage() {
  const qc = useQueryClient();
  const [params, setParams] = useSearchParams();
  const rawChannelId = Number(params.get("channel"));
  const channelId = Number.isSafeInteger(rawChannelId) && rawChannelId > 0 ? rawChannelId : 0;
  const modelName = params.get("model") ?? "";
  const view: EvalView = params.get("view") === "current" ? "current" : "history";
  const channelsQuery = useQuery({ queryKey: ["channels"], queryFn: api.listChannels });
  const groupsQuery = useQuery({ queryKey: ["groups"], queryFn: api.listGroups, enabled: view === "current" });
  const [selectedIds, setSelectedIds] = useState<Set<number>>(new Set());
  const [results, setResults] = useState<EvalRecordSummary[]>([]);
  const [detailId, setDetailId] = useState<number | null>(null);
  const [testing, setTesting] = useState(false);
  const [stopping, setStopping] = useState(false);
  const [progress, setProgress] = useState({ done: 0, total: 0 });
  const [requestErrors, setRequestErrors] = useState<Array<{ target: EvalTarget; message: string }>>([]);
  const activeRun = useRef<ActiveRun | null>(null);
  const mounted = useRef(false);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      if (activeRun.current) {
        activeRun.current.stopQueued = true;
        activeRun.current.controller.abort();
      }
    };
  }, []);

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
  const rankedResults = results.filter((record) => record.outcome !== "error" && findEvalTarget(record, allTargets));
  const failedCount = results.filter((record) => record.outcome === "error").length;
  const existingPro = groupsQuery.data?.find((group) => group.name === PRO_GROUP_NAME);

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

  const createProMut = useMutation({
    mutationFn: async () => {
      // 操作前重新读取渠道与分组，避免拿历史模型 ID 或过期成员覆盖现有分组。
      const [latestChannels, latestGroups] = await Promise.all([api.listChannels(), api.listGroups()]);
      const items = rankedResults.map((record, index) => {
        const channel = latestChannels.find((item) => item.id === record.channel_id && item.enabled);
        const target = channel?.models.find((model) => model.name === record.model_name);
        if (!target) throw new Error(`${record.channel_name} / ${record.model_name} 已不可用，请刷新后调整排序`);
        return {
          channel_model_id: target.id,
          ref_group_name: "",
          priority: priorityFromOrder(index, rankedResults.length),
        };
      });
      if (items.length === 0) throw new Error("没有可用于分组的评估结果");
      const pro = latestGroups.find((group) => group.name === PRO_GROUP_NAME);
      if (pro) {
        await api.updateGroup({
          id: pro.id,
          items_to_delete: (pro.items ?? []).map((item) => item.id).filter((id) => id > 0),
          items_to_add: items,
        });
      } else {
        await api.createGroup({
          name: PRO_GROUP_NAME,
          mode: "failover",
          active_item_id: 0,
          relay_config: DEFAULT_GROUP_RELAY_CONFIG,
          items: items.map((item) => ({ id: 0, group_id: 0, ...item })),
        });
      }
      return !!pro;
    },
    onSuccess: (updated) => {
      void qc.invalidateQueries({ queryKey: ["groups"] });
      toast.success(`分组 ${PRO_GROUP_NAME} 已${updated ? "更新" : "创建"}`);
    },
    onError: (error: Error) => toast.error(error.message || "保存分组失败"),
  });
  const busy = testing || createProMut.isPending;

  async function runTests(targets: EvalTarget[]) {
    // ref 同步锁住重复点击，避免 React 状态更新前发起两批计费请求。
    if (activeRun.current || createProMut.isPending || targets.length === 0) return;
    const run: ActiveRun = { controller: new AbortController(), stopQueued: false };
    activeRun.current = run;
    setTesting(true);
    setStopping(false);
    setRequestErrors([]);
    setProgress({ done: 0, total: targets.length });
    changeView("current");
    const queue = [...targets];
    let done = 0;
    let saved = 0;
    let failures = 0;
    let uncertain = 0;

    const worker = async () => {
      while (queue.length > 0 && !run.stopQueued) {
        const target = queue.shift();
        if (!target) return;
        try {
          const record = await api.runModelEval(target.channelModelId, run.controller.signal);
          saved += 1;
          if (record.outcome === "error") failures += 1;
          qc.setQueryData(["model-eval", "detail", record.id], record);
          if (mounted.current) setResults((previous) => mergeResult(previous, record));
          void qc.invalidateQueries({ queryKey: ["model-eval", "history"] });
        } catch (error) {
          if (run.controller.signal.aborted) return;
          uncertain += 1;
          if (mounted.current) {
            setRequestErrors((previous) => [...previous, { target, message: error instanceof Error ? error.message : "请求未完成" }]);
          }
        } finally {
          done += 1;
          if (mounted.current) setProgress({ done, total: targets.length });
        }
      }
    };

    try {
      await Promise.all(Array.from({ length: Math.min(EVAL_CONCURRENCY, targets.length) }, () => worker()));
    } finally {
      activeRun.current = null;
      void qc.invalidateQueries({ queryKey: ["model-eval", "history"] });
      if (mounted.current) {
        setTesting(false);
        setStopping(false);
        // 每轮结束按格式状态稳定分区，之后仍可手动调整所有成功结果。
        setResults((previous) => [
          ...previous.filter((record) => record.outcome === "ok"),
          ...previous.filter((record) => record.outcome === "violation"),
          ...previous.filter((record) => record.outcome === "error"),
        ]);
        if (saved > 0) toast.message(`已保存 ${saved} 条评估记录${failures > 0 ? `，其中 ${failures} 条请求失败` : ""}`);
        if (uncertain > 0) toast.error(`${uncertain} 个请求未取得结果，请到历史记录确认后再重试`);
      }
    }
  }

  function reuseResult(record: EvalRecordSummary) {
    if (busy || record.outcome === "error" || !findEvalTarget(record, allTargets)) return;
    setResults((previous) => mergeResult(previous, record));
    setDetailId(null);
    changeView("current");
    toast.success("已加入当前排序");
  }

  function repeatResult(record: EvalRecordSummary) {
    const target = findEvalTarget(record, allTargets);
    if (busy || !target) return;
    setDetailId(null);
    void runTests([target]);
  }

  function moveResult(record: EvalRecordSummary, direction: -1 | 1) {
    if (busy) return;
    const index = rankedResults.findIndex((item) => item.id === record.id);
    const neighbor = rankedResults[index + direction];
    if (!neighbor) return;
    setResults((previous) => {
      const next = [...previous];
      const from = next.findIndex((item) => item.id === record.id);
      const to = next.findIndex((item) => item.id === neighbor.id);
      if (from >= 0 && to >= 0) [next[from], next[to]] = [next[to], next[from]];
      return next;
    });
  }

  return (
    <div className="min-w-0 space-y-4 pb-4">
      <PageToolbar
        leading={<div className="flex items-center gap-2"><FlaskConical className="h-5 w-5 text-primary-text" aria-hidden /><h1 className="text-lg font-semibold tracking-tight text-ink">模型评估</h1></div>}
        trailing={view === "history" ? <Button type="button" size="sm" onClick={() => changeView("current")}><Plus className="h-3.5 w-3.5" aria-hidden />{testing ? "查看当前评估" : "新评估"}</Button> : undefined}
      />
      <p className="text-xs leading-relaxed text-ink-muted">用同一道 SVG 动画题比较模型表现，按渠道保存每次结果，支持回看、重测和按质量排序。</p>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <SegmentedControl
          aria-label="评估视图"
          value={view}
          onChange={changeView}
          size="md"
          options={[{ value: "history", label: "评估历史" }, { value: "current", label: `当前评估${results.length > 0 ? ` (${results.length})` : ""}` }]}
        />
        <label className="flex min-w-0 max-w-full items-center gap-2 text-xs text-ink-muted">
          <span className="shrink-0">渠道范围</span>
          <Select
            aria-label="渠道范围"
            className="min-w-0 max-w-[min(65vw,20rem)]"
            value={channelId}
            disabled={testing}
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

      {testing && (
        <Card className="space-y-3 p-4" role="status" aria-live="polite">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="flex items-center gap-2 text-xs text-ink"><Loader2 className="h-4 w-4 animate-spin text-primary-text" aria-hidden />{stopping ? "等待已发出的请求完成" : "正在评估"} · {progress.done} / {progress.total}</span>
            <Button type="button" variant="ghost" size="sm" disabled={stopping} onClick={() => { if (activeRun.current) activeRun.current.stopQueued = true; setStopping(true); }}>停止排队</Button>
          </div>
          <div role="progressbar" aria-label="评估进度" aria-valuemin={0} aria-valuemax={progress.total} aria-valuenow={progress.done} className="h-1.5 overflow-hidden rounded-full bg-ink/5"><div className="h-full rounded-full bg-primary transition-[width]" style={{ width: `${progress.total > 0 ? progress.done / progress.total * 100 : 0}%` }} /></div>
        </Card>
      )}

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
      ) : (
        <div className="grid min-w-0 items-start gap-4 xl:grid-cols-[19rem_minmax(0,1fr)]">
          <div className="min-w-0 space-y-3 xl:sticky xl:top-0">
            {channelsQuery.isLoading ? <Skeleton className="h-80 w-full" /> : (
              <EvalSelection key={channelId} targets={scopeTargets} selectedIds={selectedIds} onSelectionChange={setSelectedIds} disabled={busy || channelsQuery.isError} onRun={() => void runTests(selectedTargets)} onHistory={showHistory} />
            )}
            <details className="rounded-lg border border-border/50 px-4 py-3 text-xs text-ink-muted">
              <summary className="cursor-pointer font-medium">本次评估题目</summary>
              <p className="mt-2 whitespace-pre-wrap break-words leading-relaxed">{EVAL_PROMPT}</p>
            </details>
          </div>

          <div className="min-w-0 space-y-3">
            <Card className="overflow-hidden">
              <div className="space-y-2 border-b border-border/50 p-4">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <h2 className="text-sm font-semibold text-ink">当前排序 <span className="font-normal text-ink-subtle">· {rankedResults.length} 个可入组{failedCount > 0 ? ` / ${failedCount} 个失败` : ""}</span></h2>
                  <Button type="button" variant="secondary" size="sm" onClick={() => createProMut.mutate()} disabled={busy || rankedResults.length === 0 || !groupsQuery.isSuccess} loading={createProMut.isPending}>
                    <Plus className="h-3.5 w-3.5" aria-hidden />{existingPro ? "更新" : "创建"} {PRO_GROUP_NAME} 分组
                  </Button>
                </div>
                <p className="text-[11px] leading-relaxed text-ink-subtle">跨渠道排列，越靠前优先级越高；格式不符默认排后，失败结果保留在下方。{existingPro ? `更新将用当前排序替换 ${PRO_GROUP_NAME} 的现有成员。` : "可从历史记录加入已有结果。"}</p>
              </div>
              {groupsQuery.isError && <div className="p-4"><QueryErrorBanner onRetry={() => void groupsQuery.refetch()} /></div>}
              {results.length === 0 ? (
                <EmptyState icon={<FlaskConical className="h-5 w-5" />} title={testing ? "正在等待评估结果" : "选择模型，开始一次评估"} hint="结果自动保存到历史，也可以复用已有记录加入排序。" action={<Button type="button" variant="ghost" size="sm" onClick={() => changeView("history")}><History className="h-3.5 w-3.5" aria-hidden />查看历史记录</Button>} />
              ) : (
                <ul className="divide-y divide-border/40">
                  {results.map((record) => {
                    const rank = rankedResults.findIndex((item) => item.id === record.id);
                    const available = !!findEvalTarget(record, allTargets);
                    return (
                      <li key={record.id} className="min-w-0 space-y-3 p-4">
                        <div className="flex items-start gap-3">
                          <div className="flex w-7 shrink-0 flex-col items-center gap-0.5">
                            <span className="text-xs font-semibold tabular-nums text-ink-subtle">{rank < 0 ? "—" : `#${rank + 1}`}</span>
                            {rank >= 0 && <>
                              <Button type="button" variant="ghost" size="icon" className="h-6 w-6" onClick={() => moveResult(record, -1)} disabled={busy || rank === 0} aria-label={`上移 ${record.channel_name} ${record.model_name}`}><ArrowUp className="h-3.5 w-3.5" aria-hidden /></Button>
                              <Button type="button" variant="ghost" size="icon" className="h-6 w-6" onClick={() => moveResult(record, 1)} disabled={busy || rank === rankedResults.length - 1} aria-label={`下移 ${record.channel_name} ${record.model_name}`}><ArrowDown className="h-3.5 w-3.5" aria-hidden /></Button>
                            </>}
                          </div>
                          <div className="min-w-0 flex-1">
                            <p className="break-all font-mono text-[13px] font-medium text-ink">{record.model_name}</p>
                            <p className="mt-1 break-all text-xs text-ink-muted">{record.channel_name}</p>
                            <time dateTime={record.created_at} className="mt-1 block text-[11px] tabular-nums text-ink-subtle">{formatEvalTime(record.created_at)}</time>
                          </div>
                          <Button type="button" variant="ghost" size="icon" className="h-7 w-7 shrink-0" disabled={busy} onClick={() => setResults((previous) => previous.filter((item) => item.id !== record.id))} aria-label={`移除当前结果 ${record.channel_name} ${record.model_name}`} title="从当前排序移除，保留历史"><X className="h-3.5 w-3.5" aria-hidden /></Button>
                        </div>
                        <div className="flex flex-wrap items-center gap-x-3 gap-y-2 pl-10">
                          <EvalOutcomeBadge outcome={record.outcome} />
                          <span className="text-[11px] tabular-nums text-ink-subtle">{formatNumber(record.latency_ms)} ms · {formatNumber(record.completion_tokens)} 输出 tok</span>
                          {!available && <span className="text-[11px] text-ink-subtle">模型已不可用</span>}
                          <div className="flex flex-wrap items-center gap-1 sm:ml-auto">
                            <Button type="button" variant="secondary" size="sm" onClick={() => setDetailId(record.id)}>{record.outcome === "error" ? "查看详情" : "查看预览"}</Button>
                            <Button type="button" variant="ghost" size="sm" onClick={() => repeatResult(record)} disabled={busy || !available}>重测</Button>
                            <Button type="button" variant="ghost" size="sm" onClick={() => showHistory(record.channel_id, record.model_name)}>历史</Button>
                          </div>
                        </div>
                        {record.error && <p className="line-clamp-3 break-all pl-10 text-xs leading-relaxed text-destructive">{record.error}</p>}
                      </li>
                    );
                  })}
                </ul>
              )}
            </Card>
            {requestErrors.length > 0 && (
              <Card className="space-y-2 border border-destructive/20 p-4" role="alert">
                <p className="text-xs font-medium text-destructive">部分请求未取得结果</p>
                <p className="text-xs leading-relaxed text-ink-muted">请先到历史记录确认是否已保存，再决定重试。</p>
                {requestErrors.map(({ target, message }) => <p key={target.channelModelId} className="break-all text-xs text-ink-muted">{target.channelName} / {target.modelName}：{message}</p>)}
              </Card>
            )}
          </div>
        </div>
      )}

      <EvalDetail id={detailId} onClose={() => setDetailId(null)} targets={allTargets} busy={busy} onReuse={reuseResult} onRepeat={repeatResult} />
    </div>
  );
}
