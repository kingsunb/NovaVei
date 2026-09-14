import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  CircleDot,
  FlaskConical,
  Loader2,
  Play,
  Plus,
} from "lucide-react";
import { toast } from "sonner";

import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { PageToolbar } from "@/components/ui/page-toolbar";
import { Skeleton } from "@/components/ui/skeleton";
import { formatNumber } from "@/lib/utils";
import type { Channel } from "@/lib/types";
import {
  DEFAULT_GROUP_RELAY_CONFIG,
} from "@/lib/types";
import {
  EVAL_CONCURRENCY,
  EVAL_PROMPT,
  PRO_GROUP_NAME,
  extractEvalHtml,
  extractRenderableHtml,
  priorityFromOrder,
  type EvalResult,
  type EvalTarget,
} from "@/lib/model-eval";
import { SandboxPreview } from "@/components/model-eval/SandboxPreview";

/**
 * 模型评估页：对每个渠道的每个模型发送固定测试题（生成 SVG 鹈鹕骑自行车 2D
 * 动画的 HTML），把回复渲染到沙箱 iframe 里实时预览，按质量手动排优先级，
 * 最后一键创建/更新固定分组 pro。
 *
 * 三条规则：
 *  1. 测试报错 → 从列表移除（不进分组）
 *  2. 成功但未按 <<<RESULT>>> 包裹 → 自动最低优先级
 *  3. 成功且包裹合规 → 正常参与手动排序
 */
export default function ModelEvalPage() {
  const qc = useQueryClient();

  const { data: channels, isLoading: channelsLoading } = useQuery({
    queryKey: ["channels"],
    queryFn: api.listChannels,
  });
  const { data: groups } = useQuery({
    queryKey: ["groups"],
    queryFn: api.listGroups,
  });

  // 全部候选 = 所有启用渠道 × 各自模型。
  const allTargets = useMemo<EvalTarget[]>(() => {
    if (!channels) return [];
    const out: EvalTarget[] = [];
    for (const ch of channels) {
      if (!ch.enabled) continue;
      for (const m of ch.models) {
        out.push({
          channelId: ch.id,
          channelName: ch.name,
          channelType: ch.type,
          channelModelId: m.id,
          modelName: m.name,
        });
      }
    }
    return out;
  }, [channels]);

  // 勾选状态：默认全选，进页面即可一键开测。
  const [selectedIds, setSelectedIds] = useState<Set<number>>(new Set());
  // 仅首次加载时自动全选；之后用户手动增删不再被覆盖（修"全不选"被回弹的 bug）。
  const autoSelectedRef = useRef(false);
  useEffect(() => {
    if (!autoSelectedRef.current && allTargets.length > 0) {
      autoSelectedRef.current = true;
      setSelectedIds(new Set(allTargets.map((t) => t.channelModelId)));
    }
  }, [allTargets]);

  // 结果：channelModelId → EvalResult。报错的根本不进这个 Map（直接移除）。
  const [results, setResults] = useState<Map<number, EvalResult>>(new Map());
  // 所有成功结果的拖拽顺序（channelModelId 数组）。
  // 初始顺序：合规(wrapped)在前，未合规垫底；用户可自由上下移动。
  const [order, setOrder] = useState<number[]>([]);

  const [testing, setTesting] = useState(false);
  const [progress, setProgress] = useState({ done: 0, total: 0 });
  const [removedCount, setRemovedCount] = useState(0);

  // 卸载中止：剩余排队项不再发往上游（每个都是真实计费请求）。
  const abortedRef = useRef(false);
  useEffect(() => {
    abortedRef.current = false;
    return () => {
      abortedRef.current = true;
    };
  }, []);

  const selectedTargets = useMemo(
    () => allTargets.filter((t) => selectedIds.has(t.channelModelId)),
    [allTargets, selectedIds],
  );

  async function runTests() {
    if (testing || selectedTargets.length === 0) return;
    abortedRef.current = false;
    setTesting(true);
    setResults(new Map());
    setOrder([]);
    setRemovedCount(0);
    setProgress({ done: 0, total: selectedTargets.length });

    const queue = [...selectedTargets];
    let done = 0;
    let removed = 0;
    // 局部镜像：测试结束后用来做稳定分区，避免闭包读到 stale state。
    const localResults = new Map<number, EvalResult>();

    const worker = async () => {
      while (queue.length > 0) {
        if (abortedRef.current) return;
        const target = queue.shift();
        if (!target) break;
        try {
          const r = await api.testChannel(
            target.channelId,
            target.modelName,
            EVAL_PROMPT,
          );
          if (abortedRef.current) return;
          const { wrapped } = extractEvalHtml(r.content);
          const html = extractRenderableHtml(r.content);
          const result: EvalResult = {
            target,
            outcome: wrapped ? "ok" : "violation",
            content: r.content,
            html,
            latencyMs: r.latency_ms,
            promptTokens: r.prompt_tokens,
            completionTokens: r.completion_tokens,
            error: "",
          };
          setResults((prev) => new Map(prev).set(target.channelModelId, result));
          localResults.set(target.channelModelId, result);
          setOrder((prev) => [...prev, target.channelModelId]);
        } catch {
          if (abortedRef.current) return;
          // 规则 3：报错直接从列表移除，不进 results。
          removed += 1;
          setRemovedCount(removed);
        } finally {
          done += 1;
          setProgress({ done, total: selectedTargets.length });
        }
      }
    };

    await Promise.all(
      Array.from({ length: Math.min(EVAL_CONCURRENCY, selectedTargets.length) }, () =>
        worker(),
      ),
    );
    setTesting(false);
    // 稳定分区：合规(wrapped)在前，未合规(violation)垫底 = 自动最低优先级。
    setOrder((prev) => {
      const wrappedIds: number[] = [];
      const violationIds: number[] = [];
      for (const id of prev) {
        const r = localResults.get(id);
        if (r?.outcome === "ok") wrappedIds.push(id);
        else violationIds.push(id);
      }
      return [...wrappedIds, ...violationIds];
    });
    if (removed > 0) {
      toast.message(`${removed} 个模型测试失败，已从列表移除`);
    }
  }

  function moveOk(id: number, dir: -1 | 1) {
    setOrder((prev) => {
      const idx = prev.indexOf(id);
      if (idx === -1) return prev;
      const next = idx + dir;
      if (next < 0 || next >= prev.length) return prev;
      const arr = [...prev];
      [arr[idx], arr[next]] = [arr[next], arr[idx]];
      return arr;
    });
  }

  // 创建/更新 pro 分组。
  const existingPro = useMemo(
    () => groups?.find((g) => g.name === PRO_GROUP_NAME) ?? null,
    [groups],
  );

  const createProMut = useMutation({
    mutationFn: async () => {
      const orderedIds = order;
      const items = orderedIds
        .map((id) => results.get(id))
        .filter((r): r is EvalResult => !!r)
        .map((r, index) => ({
          channel_model_id: r.target.channelModelId,
          ref_group_name: "",
          priority: priorityFromOrder(index, orderedIds.length),
        }));
      if (items.length === 0) throw new Error("没有可用的评估结果");

      if (existingPro) {
        // 更新：先删旧成员再加新的。
        const oldItemIds = (existingPro.items ?? [])
          .map((it) => it.id)
          .filter((id) => id > 0);
        return api.updateGroup({
          id: existingPro.id,
          items_to_delete: oldItemIds,
          items_to_add: items,
        });
      }
      return api.createGroup({
        name: PRO_GROUP_NAME,
        mode: "failover",
        active_item_id: 0,
        relay_config: DEFAULT_GROUP_RELAY_CONFIG,
        items: items.map((it) => ({
          id: 0,
          group_id: 0,
          channel_model_id: it.channel_model_id,
          ref_group_name: it.ref_group_name,
          priority: it.priority,
        })),
      });
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["groups"] });
      toast.success(`分组 ${PRO_GROUP_NAME} 已${existingPro ? "更新" : "创建"}`);
    },
    onError: (e: Error) => toast.error(e.message || "创建分组失败"),
  });

  const hasResults = order.length > 0;

  return (
    <div className="space-y-4">
      <PageToolbar
        leading={
          <div className="flex items-center gap-2">
            <FlaskConical className="h-4 w-4 text-primary-text" />
            <h1 className="text-base font-semibold tracking-tight text-ink">
              模型评估
            </h1>
          </div>
        }
        trailing={
          <div className="flex items-center gap-2">
            {testing && (
              <span className="text-xs text-ink-muted">
                {progress.done}/{progress.total}
              </span>
            )}
            <Button
              variant="primary"
              size="sm"
              onClick={() => void runTests()}
              disabled={testing || selectedTargets.length === 0}
            >
              {testing ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : (
                <Play className="h-4 w-4" />
              )}
              {testing ? "测试中" : `开始测试 (${selectedTargets.length})`}
            </Button>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => createProMut.mutate()}
              disabled={!hasResults || createProMut.isPending}
            >
              <Plus className="h-4 w-4" />
              {existingPro ? `更新 ${PRO_GROUP_NAME}` : `创建 ${PRO_GROUP_NAME}`}
            </Button>
          </div>
        }
      />

      <p className="text-xs leading-relaxed text-ink-muted">
        对每个渠道的每个模型发送固定测试题（生成 SVG 鹈鹕骑自行车 2D 动画的
        HTML），在沙箱里实时预览效果。目视评估后上下排序，一键创建分组{" "}
        <code className="rounded bg-ink/5 px-1">{PRO_GROUP_NAME}</code>。
        测试报错的模型自动移除；未按格式包裹的自动降到最低优先级（仍可手动上移）。
      </p>

      {channelsLoading ? (
        <div className="space-y-2">
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
        </div>
      ) : (
        <SelectionPanel
          channels={channels ?? []}
          selectedIds={selectedIds}
          setSelectedIds={setSelectedIds}
        />
      )}

      {removedCount > 0 && (
        <div className="flex items-center gap-1.5 text-xs text-ink-subtle">
          <AlertTriangle className="h-3.5 w-3.5" />
          {removedCount} 个模型测试失败，已从列表移除
        </div>
      )}

      {!hasResults && !testing && (
        <EmptyState
          icon={<FlaskConical className="h-5 w-5" />}
          title="还没有评估结果"
          hint="勾选上方渠道模型后点击「开始测试」"
        />
      )}

      {/* 所有成功结果：统一可排序卡片，iframe 预览供人工评估 */}
      {hasResults && (
        <div className="space-y-3">
          <h2 className="text-sm font-semibold text-ink">
            评估结果（上下排序，越靠前优先级越高）
          </h2>
          {order.map((id, idx) => {
            const r = results.get(id);
            if (!r) return null;
            return (
              <ResultCard
                key={id}
                result={r}
                index={idx}
                total={order.length}
                onMove={(dir) => moveOk(id, dir)}
              />
            );
          })}
        </div>
      )}
    </div>
  );
}

// ─── 候选选择面板 ───────────────────────────────────────────

function SelectionPanel({
  channels,
  selectedIds,
  setSelectedIds,
}: {
  channels: Channel[];
  selectedIds: Set<number>;
  setSelectedIds: React.Dispatch<React.SetStateAction<Set<number>>>;
}) {
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const allTargets = channels.flatMap((ch) =>
    ch.enabled ? ch.models.map((m) => ({ ch, m })) : [],
  );
  const allSelected =
    allTargets.length > 0 && allTargets.every((t) => selectedIds.has(t.m.id));

  function toggleModel(id: number) {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }
  function toggleChannel(ch: Channel) {
    const ids = ch.models.map((m) => m.id);
    const allOn = ids.every((id) => selectedIds.has(id));
    setSelectedIds((prev) => {
      const next = new Set(prev);
      for (const id of ids) {
        if (allOn) next.delete(id);
        else next.add(id);
      }
      return next;
    });
  }
  function toggleAll() {
    setSelectedIds(() => {
      if (allSelected) return new Set();
      return new Set(allTargets.map((t) => t.m.id));
    });
  }
  function toggleExpand(id: number) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  if (allTargets.length === 0) {
    return (
      <EmptyState
        icon={<CircleDot className="h-5 w-5" />}
        title="没有可测试的模型"
        hint="请先在渠道页添加渠道和模型"
      />
    );
  }

  return (
    <Card className="p-4">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-xs font-medium text-ink-muted">
          选择测试范围（{selectedIds.size}/{allTargets.length}）
        </span>
        <button
          type="button"
          onClick={toggleAll}
          className="text-xs text-primary-text hover:underline"
        >
          {allSelected ? "全不选" : "全选"}
        </button>
      </div>
      <div className="space-y-1">
        {channels
          .filter((ch) => ch.enabled && ch.models.length > 0)
          .map((ch) => {
            const isOpen = expanded.has(ch.id) ?? ch.models.length <= 3;
            const channelIds = ch.models.map((m) => m.id);
            const channelSelected = channelIds.filter((id) =>
              selectedIds.has(id),
            ).length;
            return (
              <div key={ch.id} className="rounded-lg bg-ink/[0.02]">
                <div className="flex items-center gap-2 px-3 py-2">
                  <button
                    type="button"
                    onClick={() => toggleExpand(ch.id)}
                    className="text-ink-subtle hover:text-ink"
                    aria-label={isOpen ? "折叠" : "展开"}
                  >
                    {isOpen ? (
                      <ChevronDown className="h-3.5 w-3.5" />
                    ) : (
                      <ChevronRight className="h-3.5 w-3.5" />
                    )}
                  </button>
                  <label className="flex flex-1 cursor-pointer items-center gap-2 text-xs font-medium text-ink">
                    <input
                      type="checkbox"
                      checked={
                        channelSelected === channelIds.length &&
                        channelIds.length > 0
                      }
                      ref={(el) => {
                        if (el)
                          el.indeterminate =
                            channelSelected > 0 &&
                            channelSelected < channelIds.length;
                      }}
                      onChange={() => toggleChannel(ch)}
                      className="h-3.5 w-3.5 accent-[#007AFF]"
                    />
                    {ch.name}
                    <span className="text-ink-subtle">· {ch.type}</span>
                    <span className="text-ink-subtle">
                      （{channelSelected}/{channelIds.length}）
                    </span>
                  </label>
                </div>
                {isOpen && (
                  <div className="flex flex-wrap gap-x-4 gap-y-1 px-3 pb-2 pl-9">
                    {ch.models.map((m) => (
                      <label
                        key={m.id}
                        className="flex cursor-pointer items-center gap-1.5 text-xs text-ink-muted"
                      >
                        <input
                          type="checkbox"
                          checked={selectedIds.has(m.id)}
                          onChange={() => toggleModel(m.id)}
                          className="h-3.5 w-3.5 accent-[#007AFF]"
                        />
                        {m.name}
                      </label>
                    ))}
                  </div>
                )}
              </div>
            );
          })}
      </div>
    </Card>
  );
}

// ─── 结果卡片 ───────────────────────────────────────────────

function ResultCard({
  result,
  index,
  total,
  onMove,
}: {
  result: EvalResult;
  index: number;
  total: number;
  onMove: (dir: -1 | 1) => void;
}) {
  const { target } = result;
  const isViolation = result.outcome === "violation";
  const [showRaw, setShowRaw] = useState(false);
  return (
    <Card className="overflow-hidden">
      <div className="flex items-center gap-2 border-b border-border/40 px-4 py-2.5">
        <div className="flex items-center gap-1.5">
          <button
            type="button"
            onClick={() => onMove(-1)}
            disabled={index === 0}
            aria-label="上移"
            className="rounded p-0.5 text-ink-subtle hover:bg-ink/5 hover:text-ink disabled:opacity-30"
          >
            <ArrowUp className="h-3.5 w-3.5" />
          </button>
          <button
            type="button"
            onClick={() => onMove(1)}
            disabled={index === total - 1}
            aria-label="下移"
            className="rounded p-0.5 text-ink-subtle hover:bg-ink/5 hover:text-ink disabled:opacity-30"
          >
            <ArrowDown className="h-3.5 w-3.5" />
          </button>
        </div>
        <div className="flex min-w-0 flex-1 items-center gap-2">
          <span className="truncate text-xs font-semibold text-ink">
            {target.channelName}
          </span>
          <span className="truncate text-xs text-ink-muted">
            {target.modelName}
          </span>
        </div>
        {isViolation && (
          <span className="shrink-0 rounded-full bg-amber-500/10 px-2 py-0.5 text-[10px] font-medium text-amber-600 dark:text-amber-400">
            未按格式输出
          </span>
        )}
        <div className="flex shrink-0 items-center gap-2 text-[10px] text-ink-subtle">
          {!isViolation && <CheckCircle2 className="h-3.5 w-3.5 text-emerald-500" />}
          <span>{result.latencyMs}ms</span>
          <span>·</span>
          <span>{formatNumber(result.completionTokens)} tok</span>
        </div>
      </div>
      <div className="p-3">
        {result.html ? (
          <SandboxPreview
            html={result.html}
            title={`${target.channelName} · ${target.modelName}`}
          />
        ) : (
          <div className="flex h-32 items-center justify-center rounded-lg bg-ink/[0.03] text-xs text-ink-subtle">
            无法提取可渲染的 HTML
          </div>
        )}
        <button
          type="button"
          onClick={() => setShowRaw((v) => !v)}
          className="mt-2 text-[11px] text-ink-subtle hover:text-ink hover:underline"
        >
          {showRaw ? "收起原始回复" : "查看原始回复"}
        </button>
        {showRaw && (
          <pre className="mt-2 max-h-48 overflow-auto rounded-lg bg-ink/[0.03] p-3 text-[11px] leading-relaxed text-ink-muted">
            {result.content.slice(0, 4000)}
            {result.content.length > 4000 ? "\n…（已截断）" : ""}
          </pre>
        )}
      </div>
    </Card>
  );
}
