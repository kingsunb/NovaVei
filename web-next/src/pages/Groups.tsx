import { useEffect, useMemo, useRef, useState } from "react";
import { Field } from "@/components/ui/field";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Plus,
  Trash2,
  Search,
  Snowflake,
  Pencil,
  X,
  Check,
  ChevronUp,
  ChevronDown,
  CornerDownLeft,
  Users,
  ArrowRight,
  Circle,
  Zap,
  Hand,
} from "lucide-react";
import { toast } from "sonner";

import { api } from "@/lib/api";
import type { GroupRouteState } from "@/lib/types";
import {
  formatCountdown,
  remainingSeconds,
  useGroupRuntime,
  useNow,
} from "./useGroupRuntime";
import type {
  Channel,
  ChannelModel,
  Group,
  GroupItem,
  GroupMode,
  GroupRelayConfig,
} from "@/lib/types";
import { DEFAULT_GROUP_RELAY_CONFIG } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { CardGridSkeleton } from "@/components/ui/skeleton";
import { ConfirmButton } from "@/components/ui/confirm-button";
import { QueryErrorBanner } from "@/components/ui/query-error";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Pill } from "@/components/ui/pill";
import { Select } from "@/components/ui/select";
import { EmptyState } from "@/components/ui/empty-state";
import { Switch } from "@/components/ui/switch";
import { SegmentedControl } from "@/components/ui/segmented-control";
import { SearchField } from "@/components/ui/search-field";
import { PageToolbar } from "@/components/ui/page-toolbar";
import { cn, NAME_RULE, validateField } from "@/lib/utils";
import { PROVIDER_LABELS } from "./channels/constants";
import { ViewToggle } from "@/components/ui/view-toggle";
import { useViewMode } from "@/lib/use-view-mode";

/**
 * 分组模式标签 —— manual / failover 在 db/API 仍是英文短码（兼容旧数据），
 * UI 展示走这张字典。failover（故障转移）= 失败时自动切到下一成员，
 * manual = 管理员手动选成员。
 */
const MODE_LABELS: Record<GroupMode, string> = {
  manual: "手动",
  failover: "故障转移",
};

type ModeFilter = "all" | GroupMode;
type DraftGroupItem = GroupItem & { client_uid: string };

type GroupSort = "priority" | "name" | "mode" | "custom";

const GROUP_SORT_KEY = "nv-group-sort";

function loadGroupSort(): GroupSort {
  try {
    const v = localStorage.getItem(GROUP_SORT_KEY);
    if (v === "priority" || v === "name" || v === "mode" || v === "custom") return v;
  } catch {
    /* ignore */
  }
  return "priority";
}

let draftItemSeq = 0;
function nextDraftItemUid(prefix = "new") {
  draftItemSeq += 1;
  return `${prefix}:${draftItemSeq}`;
}

export default function GroupsPage() {
  const qc = useQueryClient();
  const [search, setSearch] = useState("");
  const [mode, setMode] = useState<ModeFilter>("all");
  const [editing, setEditing] = useState<Group | "new" | null>(null);
  const [pendingDelete, setPendingDelete] = useState<Group | null>(null);
  // 列表排序方式本地记忆；custom 的具体顺序来自后端 display_order
  const [sort, setSort] = useState<GroupSort>(loadGroupSort);
  const [viewMode, setViewMode] = useViewMode("nv-group-view", "grid");
  // 路由运行时流：冷却/亲和倒计时、半开探测与紧急兜底状态
  const runtime = useGroupRuntime();

  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ["groups"],
    queryFn: api.listGroups,
  });
  const { data: channels } = useQuery({
    queryKey: ["channels"],
    queryFn: api.listChannels,
  });
  const channelById = useMemo(
    () => new Map((channels ?? []).map((channel) => [channel.id, channel])),
    [channels],
  );

  const clearCooldownMut = useMutation({
    mutationFn: (id: number) => api.clearGroupCooldown(id),
    onSuccess: (result) => {
      toast.success(
        `已清除冷却：${result.member_items} 个成员、${result.key_cooldowns} 个 Key、${result.rate_windows} 个限速窗口`,
      );
      qc.invalidateQueries({ queryKey: ["groups"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const deleteMut = useMutation({
    mutationFn: (id: number) => api.deleteGroup(id),
    onSuccess: () => {
      toast.success("已删除");
      setPendingDelete(null);
      qc.invalidateQueries({ queryKey: ["groups"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  // 自定义展示顺序持久化：按目标全量顺序重排 display_order（1..N），只提交变化的分组
  const reorderMut = useMutation({
    mutationFn: async (updates: { id: number; display_order: number }[]) => {
      await Promise.all(
        updates.map((u) => api.updateGroup({ id: u.id, display_order: u.display_order })),
      );
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["groups"] }),
    onError: (e: Error) => toast.error(e.message || "保存顺序失败"),
  });

  function moveCustom(g: Group, dir: -1 | 1) {
    // 相邻对象取自当前筛选视图；交换落在全量列表上，保证筛选下也能看到预期的相对移动
    const idx = rows.findIndex((x) => x.id === g.id);
    const other = rows[idx + dir];
    if (!other) return;
    // 基准 = 后端返回的全量顺序；直接重排为 1..N，避免「有顺序者与 0 交换后
    // 掉回按名称桶」以及重复值等混合状态，任何一次移动都收敛到一致的顺序。
    const base = (data ?? []).map((x) => x.id);
    const i = base.indexOf(g.id);
    const j = base.indexOf(other.id);
    if (i < 0 || j < 0) return;
    const desired = base.slice();
    [desired[i], desired[j]] = [desired[j], desired[i]];
    const byId = new Map((data ?? []).map((x) => [x.id, x]));
    const updates = desired
      .map((id, pos) => ({ id, display_order: pos + 1 }))
      .filter((u) => (byId.get(u.id)?.display_order ?? 0) !== u.display_order);
    if (updates.length) reorderMut.mutate(updates);
  }

  const rows = useMemo(() => {
    const list = (data ?? [])
      .map((group) => ({ ...group, items: group.items ?? [] }))
      .filter((g) => (mode === "all" ? true : g.mode === mode))
      .filter((g) =>
        search ? g.name.toLowerCase().includes(search.toLowerCase()) : true,
      );
    switch (sort) {
      case "name":
        return list.sort((a, b) => a.name.localeCompare(b.name));
      case "mode":
        return list.sort(
          (a, b) =>
            a.mode.localeCompare(b.mode) || a.name.localeCompare(b.name),
        );
      case "custom":
        // 后端 GroupList 已按 display_order(≠0 优先) + 名称排序；这里保持该顺序
        return list;
      default:
        // priority：分组内成员最小 priority 在前（failover 的实际路由顺序）
        return list.sort(
          (a, b) =>
            Math.min(...a.items.map((i) => i.priority), Number.MAX_SAFE_INTEGER) -
            Math.min(...b.items.map((i) => i.priority), Number.MAX_SAFE_INTEGER),
        );
    }
  }, [data, search, mode, sort]);

  function changeSort(next: GroupSort) {
    setSort(next);
    try {
      localStorage.setItem(GROUP_SORT_KEY, next);
    } catch {
      /* ignore */
    }
  }

  return (
    <div className="space-y-4">
      <PageToolbar
        leading={
          <>
            <SegmentedControl
              aria-label="分组模式"
              value={mode}
              onChange={setMode}
              options={[
                { value: "all", label: "全部" },
                { value: "failover", label: MODE_LABELS.failover },
                { value: "manual", label: MODE_LABELS.manual },
              ]}
            />
            <label className="flex items-center gap-1.5 text-xs text-ink-muted">
              排序
              <Select
                className="h-8 text-xs"
                aria-label="分组排序方式"
                value={sort}
                onChange={(e) => changeSort(e.target.value as GroupSort)}
              >
                <option value="priority">按优先级</option>
                <option value="name">按名称</option>
                <option value="mode">按模式</option>
                <option value="custom">自定义</option>
              </Select>
            </label>
          </>
        }
        trailing={
          <>
            <ViewToggle value={viewMode} onChange={setViewMode} />
            <SearchField
              value={search}
              onChange={setSearch}
              placeholder="搜索分组…"
              aria-label="搜索分组"
            />
            <Button
              variant="primary"
              size="sm"
              className="gap-1.5"
              onClick={() => setEditing("new")}
            >
              <Plus className="h-3.5 w-3.5" aria-hidden />
              新建分组
            </Button>
          </>
        }
      />

      {isLoading ? (
        <CardGridSkeleton cards={6} />
      ) : isError ? (
        <QueryErrorBanner onRetry={() => refetch()} />
      ) : rows.length === 0 ? (
        <Card>
          <EmptyState
            icon={<Users className="h-5 w-5" aria-hidden />}
            title={data?.length ? "没有匹配的分组" : "还没有分组"}
            hint={data?.length ? undefined : "先在「渠道」页接入上游，再在此把它们组成故障转移分组"}
          />
        </Card>
      ) : (
        <div className={cn("grid grid-cols-1 gap-3", viewMode === "grid" && "md:grid-cols-2 xl:grid-cols-3")}>
          {rows.map((g, idx) => {
            const routeState = runtime.get(g.id);
            const sortedItems = g.items
              .slice()
              .sort((a, b) => a.priority - b.priority);
            return (
            <Card key={g.id}>
              <CardHeader>
                <div className="flex items-center gap-2">
                  {sort === "custom" && (
                    <span className="flex shrink-0 items-center gap-0.5">
                      <button
                        type="button"
                        aria-label={`上移分组 ${g.name}`}
                        disabled={idx === 0 || reorderMut.isPending}
                        onClick={() => moveCustom(g, -1)}
                        className="rounded p-0.5 text-ink-muted transition-colors hover:bg-surface-subtle hover:text-ink disabled:opacity-30"
                      >
                        <ChevronUp className="h-3.5 w-3.5" aria-hidden />
                      </button>
                      <button
                        type="button"
                        aria-label={`下移分组 ${g.name}`}
                        disabled={idx === rows.length - 1 || reorderMut.isPending}
                        onClick={() => moveCustom(g, 1)}
                        className="rounded p-0.5 text-ink-muted transition-colors hover:bg-surface-subtle hover:text-ink disabled:opacity-30"
                      >
                        <ChevronDown className="h-3.5 w-3.5" aria-hidden />
                      </button>
                    </span>
                  )}
                  <CardTitle>{g.name}</CardTitle>
                  <Pill tone={g.mode === "failover" ? "info" : "neutral"}>
                    {g.mode === "failover" ? (
                      <Zap className="mr-0.5 h-3 w-3" aria-hidden />
                    ) : (
                      <Hand className="mr-0.5 h-3 w-3" aria-hidden />
                    )}
                    {MODE_LABELS[g.mode]}
                  </Pill>
                  {routeState && routeState.emergency_active > 0 && (
                    <Pill tone="danger">紧急兜底</Pill>
                  )}
                </div>
                <span className="flex items-center gap-1 text-xs text-ink-muted">
                  <Users className="h-3 w-3" aria-hidden />
                  {g.items.length} 个成员
                </span>
              </CardHeader>
              <CardContent className="space-y-1.5">
                {/* 成员列表 */}
                <div className="rounded-xl border border-border/50 bg-surface-subtle/20 px-2.5 py-2">
                  {sortedItems
                    .slice(0, 4)
                    .map((it) => {
                      const chName = it.ref_group_name
                        ? null
                        : channelById.get(it.channel_model?.channel_id ?? 0)?.name ?? "?";
                      const modelName = it.ref_group_name ?? it.channel_model?.name ?? `#${it.channel_model_id}`;
                      return (
                        <div
                          key={it.id}
                          className="flex items-center justify-between gap-2 py-0.5 text-xs"
                        >
                          <span className="flex min-w-0 flex-1 items-center gap-1.5">
                            {it.ref_group_name ? (
                              <span className="truncate text-ink-muted">
                                → {it.ref_group_name}
                              </span>
                            ) : (
                              <>
                                <span className="shrink-0 truncate font-medium text-ink">
                                  {chName}
                                </span>
                                <ArrowRight className="h-3 w-3 shrink-0 text-ink-subtle" aria-hidden />
                                <span className="truncate text-ink-muted">
                                  {modelName}
                                </span>
                              </>
                            )}
                          </span>
                          <div className="flex shrink-0 items-center gap-1">
                            {routeState && (
                              <MemberRuntimeChips
                                state={routeState}
                                itemId={it.id}
                              />
                            )}
                            {g.mode === "manual" && g.active_item_id === it.id && (
                              <Pill tone="success">
                                <Circle className="mr-0.5 h-2 w-2 fill-current" aria-hidden />
                                当前
                              </Pill>
                            )}
                            <Pill tone="neutral" dot={false}>
                              #{it.priority}
                            </Pill>
                          </div>
                        </div>
                      );
                    })}
                  {g.items.length > 4 && (
                    <p className="pt-1 text-[11px] text-ink-muted">
                      +{g.items.length - 4} 个成员
                    </p>
                  )}
                  {g.items.length === 0 && (
                    <p className="py-1 text-center text-[11px] text-ink-muted">
                      暂无成员
                    </p>
                  )}
                </div>

                {/* 操作按钮 */}
                <div className="mt-2 flex items-center gap-1.5">
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-7 px-2 text-xs"
                    onClick={() => setEditing(g)}
                  >
                    <Pencil className="h-3.5 w-3.5" aria-hidden />
                    编辑
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-7 px-2 text-xs"
                    onClick={() => clearCooldownMut.mutate(g.id)}
                    title="清除所有成员的冷却状态"
                    aria-label={`清除 ${g.name} 的冷却`}
                  >
                    <Snowflake className="h-3.5 w-3.5" aria-hidden />
                    清冷却
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-7 px-2 text-xs text-destructive hover:bg-destructive/10"
                    aria-label={`删除分组 ${g.name}`}
                    onClick={() => setPendingDelete(g)}
                  >
                    <Trash2 className="h-3.5 w-3.5" aria-hidden />
                  </Button>
                </div>
              </CardContent>
            </Card>
            );
          })}
        </div>
      )}

      <GroupEditor
        group={editing}
        onClose={() => setEditing(null)}
        onSaved={() => {
          setEditing(null);
          qc.invalidateQueries({ queryKey: ["groups"] });
        }}
      />

      <Dialog
        open={!!pendingDelete}
        onOpenChange={(o) => !o && setPendingDelete(null)}
      >
        <DialogContent variant="dialog" size="sm">
          <DialogHeader>
            <DialogTitle>删除分组</DialogTitle>
            <DialogDescription>
              分组 <span className="mono text-ink">{pendingDelete?.name}</span>{" "}
              删除后无法恢复。引用此分组的其他分组会出现解析失败。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <DialogClose asChild>
              <Button variant="ghost" size="sm">
                取消
              </Button>
            </DialogClose>
            <ConfirmButton
              tone="destructive"
              onConfirm={() => pendingDelete && deleteMut.mutate(pendingDelete.id)}
              loading={deleteMut.isPending}
            />
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

// ---------------- 分组编辑 ----------------

function GroupEditor({
  group,
  onClose,
  onSaved,
}: {
  group: Group | "new" | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [name, setName] = useState("");
  const [mode, setMode] = useState<GroupMode>("manual");
  // 单个 relayConfig 对象管理全部路由策略字段，避免 17+ 个零散 useState。
  // 后端 DefaultGroupRelayConfig 同款默认（failover-first 调优版）。
  const [relayConfig, setRelayConfig] = useState<GroupRelayConfig>(
    DEFAULT_GROUP_RELAY_CONFIG,
  );
  const [activeItemId, setActiveItemId] = useState(0);
  // Tab 切分「成员」与「路由策略」两个独立页面，对齐 NovaVeil_api 编辑器布局。
  const [tab, setTab] = useState<"members" | "relay">("members");
  // 自动匹配：以分组名称为关键词，自动将名称包含该关键词的渠道模型加入分组成员。
  const [autoMatch, setAutoMatch] = useState(false);

  // 成员编辑需要：所有渠道（拉模型）和所有分组（用于引用）
  const { data: channels } = useQuery({
    queryKey: ["channels"],
    queryFn: api.listChannels,
  });
  const { data: groups } = useQuery({
    queryKey: ["groups"],
    queryFn: api.listGroups,
  });

  // 成员编辑草稿：保留 server 原始 items 引用 + 本地变更
  const [originalItems, setOriginalItems] = useState<DraftGroupItem[]>([]);
  const [draftItems, setDraftItems] = useState<DraftGroupItem[]>([]);
  // ref 供自动匹配 effect 读取最新 draftItems 而不将其纳入依赖（避免反馈循环）
  const draftItemsRef = useRef(draftItems);
  draftItemsRef.current = draftItems;

  // updateRelay 更新 relayConfig 的单个字段，保持其余字段不变。
  function updateRelay<K extends keyof GroupRelayConfig>(
    key: K,
    value: GroupRelayConfig[K],
  ) {
    setRelayConfig((prev) => ({ ...prev, [key]: value }));
  }

  useEffect(() => {
    if (group && group !== "new") {
      setName(group.name);
      setMode(group.mode);
      // relay_config 由后端负责字段兜底；这里再兜一道，避免旧版本/手工数据
      // 缺字段时本端把 0 / undefined 写回覆盖原值。兜底值取后端
      // DefaultGroupRelayConfig 同款默认。
      setRelayConfig({
        ...DEFAULT_GROUP_RELAY_CONFIG,
        ...(group.relay_config ?? {}),
      });
      setActiveItemId(group.active_item_id ?? 0);
      const sorted = [...(group.items ?? [])]
        .sort((a, b) => a.priority - b.priority)
        .map((item) => ({
          ...item,
          client_uid: item.client_uid ?? `saved:${item.id}`,
        }));
      setOriginalItems(sorted);
      setDraftItems(sorted);
      setAutoMatch(!!group.relay_config?.auto_match_models);
    } else {
      setName("");
      setMode("manual");
      setRelayConfig(DEFAULT_GROUP_RELAY_CONFIG);
      setActiveItemId(0);
      setOriginalItems([]);
      setDraftItems([]);
      setAutoMatch(false);
    }
    // 切换编辑目标时回到成员页
    setTab("members");
  }, [group]);

  // 自动匹配：以分组名称为关键词，扫描所有渠道模型并自动加入匹配项
  useEffect(() => {
    if (!autoMatch || !name.trim()) return;
    const q = name.trim().toLowerCase();
    const channelsData = channels ?? [];

    // 收集所有名称包含关键词的渠道模型
    const matchedModels: ChannelModel[] = [];
    for (const ch of channelsData) {
      for (const m of ch.models) {
        if (m.name.toLowerCase().includes(q)) {
          matchedModels.push(m);
        }
      }
    }

    // 过滤掉已在草稿中的
    const existingIds = new Set(
      draftItemsRef.current
        .filter((d) => !d.ref_group_name && d.channel_model_id > 0)
        .map((d) => d.channel_model_id),
    );
    const newMatches = matchedModels.filter((m) => !existingIds.has(m.id));
    if (newMatches.length === 0) return;

    setDraftItems((prev) => {
      const next = [
        ...prev,
        ...newMatches.map((model) => ({
          client_uid: nextDraftItemUid("auto"),
          id: 0,
          group_id: group && group !== "new" ? group.id : 0,
          channel_model_id: model.id,
          ref_group_name: "",
          channel_model: model,
          priority: 0,
        })),
      ];
      return next.map((item, idx) => ({ ...item, priority: idx + 1 }));
    });
  }, [autoMatch, name, channels, group]);

  // 自动匹配的模型总数（用于 UI 显示）
  const autoMatchCount = useMemo(() => {
    if (!autoMatch || !name.trim()) return 0;
    const q = name.trim().toLowerCase();
    let count = 0;
    for (const ch of channels ?? []) {
      for (const m of ch.models) {
        if (m.name.toLowerCase().includes(q)) count++;
      }
    }
    return count;
  }, [autoMatch, name, channels]);

  const isNew = !group || group === "new";
  const open = !!group;

  // 成员变更 diff：把 draftItems 拆成 add / update / delete
  function buildMemberDiff() {
    const originalByUid = new Map(originalItems.map((item) => [item.client_uid, item]));
    const draftByUid = new Map(draftItems.map((item) => [item.client_uid, item]));
    const toAdd: Array<{
      channel_model_id: number;
      ref_group_name: string;
      priority: number;
    }> = [];
    const toUpdate: Array<{ id: number; priority: number }> = [];
    const toDelete: number[] = [];
    for (const draftItem of draftItems) {
      const original = originalByUid.get(draftItem.client_uid);
      if (!original) {
        toAdd.push({
          channel_model_id: draftItem.channel_model_id,
          ref_group_name: draftItem.ref_group_name,
          priority: draftItem.priority,
        });
      } else if (original.priority !== draftItem.priority && draftItem.id > 0) {
        toUpdate.push({ id: draftItem.id, priority: draftItem.priority });
      }
    }
    for (const original of originalItems) {
      if (!draftByUid.has(original.client_uid)) toDelete.push(original.id);
    }
    return { toAdd, toUpdate, toDelete };
  }

  function moveItem(clientUid: string, dir: -1 | 1) {
    setDraftItems((prev) => {
      const i = prev.findIndex((x) => x.client_uid === clientUid);
      if (i < 0) return prev;
      const j = i + dir;
      if (j < 0 || j >= prev.length) return prev;
      const next = prev.slice();
      [next[i], next[j]] = [next[j], next[i]];
      return next.map((x, idx) => ({ ...x, priority: idx + 1 }));
    });
  }

  function removeItem(clientUid: string) {
    setDraftItems((prev) => {
      const next = prev.filter((item) => item.client_uid !== clientUid);
      return next.map((item, idx) => ({ ...item, priority: idx + 1 }));
    });
  }

  function addItem(it: {
    channel_model_id: number;
    ref_group_name: string;
    channel_model?: ChannelModel;
  }) {
    setDraftItems((prev) => [
      ...prev,
      {
        client_uid: nextDraftItemUid(),
        id: 0, // 0 表示新建，后端会分配
        group_id: group && group !== "new" ? group.id : 0,
        channel_model_id: it.channel_model_id,
        ref_group_name: it.ref_group_name,
        // 预填渠道模型快照，使新增成员在保存前就能显示「渠道 → 模型」
        // 而不是占位的「渠道模型 #ID」；保存后由后端返回值覆盖。
        channel_model: it.channel_model,
        priority: prev.length + 1,
      },
    ]);
  }

  const saveMut = useMutation({
    mutationFn: async () => {
      if (isNew) {
        const created = await api.createGroup({
          name,
          mode,
          active_item_id: 0,
          relay_config: relayConfig,
          items: draftItems.map((d) => ({
            id: 0,
            group_id: 0,
            channel_model_id: d.channel_model_id,
            ref_group_name: d.ref_group_name,
            priority: d.priority,
          })),
        });
        // 新成员在创建请求时 id 都是 0；创建成功后默认把第一成员设为
        // 手动模式当前成员，避免新建的 manual 分组没有可路由成员。
        if (mode === "manual" && created.items?.[0]?.id) {
          await api.setActiveGroupItem(created.id, created.items[0].id);
        }
        return created;
      }
      const diff = buildMemberDiff();
      // 仅向 patch 传递本次实际修改的 relay_config 字段；前端默认 0/undefined
      // 会把没动过的旧值一起覆盖，造成配置漂移。旧数据（或手工插入）可能没有
      // relay_config，先补齐后端默认值再合并，避免 {...undefined} 只发出改动
      // 字段、其余字段被后端按零值/校验拒绝。
      const previous: GroupRelayConfig = {
        ...DEFAULT_GROUP_RELAY_CONFIG,
        ...(group!.relay_config ?? {}),
      };
      // 逐字段比较，只提交发生变化的字段。
      const relayUpdates: Partial<GroupRelayConfig> = {};
      for (const key of Object.keys(relayConfig) as (keyof GroupRelayConfig)[]) {
        if (relayConfig[key] !== previous[key]) {
          // @ts-expect-error — 逐字段赋值，类型安全由循环保证
          relayUpdates[key] = relayConfig[key];
        }
      }
      const updated = await api.updateGroup({
        id: group!.id,
        name,
        mode,
        relay_config:
          Object.keys(relayUpdates).length > 0
            ? { ...previous, ...relayUpdates }
            : undefined,
        items_to_add: diff.toAdd,
        items_to_update: diff.toUpdate,
        items_to_delete: diff.toDelete,
      });
      if (mode === "manual") {
        // 若当前 active_item_id 指向未保存的新成员（id=0），保存后该 id 失效，
        // 此时回退到第一条已保存成员；若没有已保存成员，清空当前成员。
        const activeStillExists =
          activeItemId > 0 &&
          draftItems.some((item) => item.id === activeItemId);
        if (activeStillExists) {
          await api.setActiveGroupItem(group!.id, activeItemId);
        } else {
          const fallback = draftItems.find((item) => item.id > 0);
          if (fallback && fallback.id) {
            await api.setActiveGroupItem(group!.id, fallback.id);
          } else {
            await api.setActiveGroupItem(group!.id, null);
          }
        }
      }
      return updated;
    },
    onSuccess: () => {
      toast.success(isNew ? "已创建" : "已保存");
      onSaved();
    },
    onError: (e: Error) => toast.error(e.message || "保存失败"),
  });

  // 已加入分组的渠道模型 / 引用名 —— 左侧选择器据此显示「已添加」标记
  // 必须在 early return 之前调用，遵守 Rules of Hooks
  const addedModelIds = useMemo(
    () =>
      new Set(
        draftItems
          .filter((d) => !d.ref_group_name && d.channel_model_id > 0)
          .map((d) => d.channel_model_id),
      ),
    [draftItems],
  );
  const addedRefNames = useMemo(
    () =>
      new Set(
        draftItems.filter((d) => d.ref_group_name).map((d) => d.ref_group_name),
      ),
    [draftItems],
  );
  const otherGroups = useMemo(
    () =>
      (groups ?? []).filter(
        (g) => g.id !== (group && group !== "new" ? group.id : 0),
      ),
    [groups, group],
  );
  const channelById = useMemo(
    () => new Map((channels ?? []).map((c) => [c.id, c])),
    [channels],
  );

  if (!open) return null;

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent variant="wide">
        <DialogHeader className="pr-12">
          <DialogTitle>{isNew ? "新建分组" : `编辑：${name}`}</DialogTitle>
          <DialogDescription>
            配置分组基本信息、成员与路由策略
          </DialogDescription>
        </DialogHeader>

        {/* 基本信息：名称 + 模式，始终可见 */}
        <div className="grid grid-cols-1 gap-3 border-b border-border px-4 py-3 md:grid-cols-2">
          <Field
            label="名称"
            required
            error={validateField(name, NAME_RULE) ?? undefined}
          >
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="例如：gpt-4o-prod"
              invalid={!!validateField(name, NAME_RULE)}
              aria-invalid={!!validateField(name, NAME_RULE)}
            />
          </Field>
          <Field label="模式" hint="手动固定选中成员；故障转移按成员顺序并在失败时切换">
            <div className="flex gap-2">
              {(["manual", "failover"] as GroupMode[]).map((m) => (
                <button
                  key={m}
                  onClick={() => setMode(m)}
                  className={cn(
                    "rounded-control border px-3 py-1.5 text-sm",
                    mode === m
                      ? "border-primary bg-primary/10 text-primary-text"
                      : "border-border text-ink-muted",
                  )}
                >
                  {MODE_LABELS[m]}
                </button>
              ))}
            </div>
          </Field>
        </div>

        {/* Tab 切换：成员 | 路由策略 */}
        <div className="flex gap-1 border-b border-border px-4 py-2">
          {(["members", "relay"] as const).map((tb) => (
            <button
              key={tb}
              onClick={() => setTab(tb)}
              aria-pressed={tab === tb}
              className={cn(
                "rounded-control px-3 py-1.5 text-sm transition-colors",
                tab === tb
                  ? "bg-primary/12 font-medium text-primary-text"
                  : "text-ink-muted hover:text-ink",
              )}
            >
              {tb === "members" ? "成员" : "路由策略"}
            </button>
          ))}
        </div>

        {/* ---------- Tab: 成员 ---------- */}
        {tab === "members" && (
          <>
          {/* 自动匹配开关 */}
          <div className="border-b border-border px-4 py-2.5">
            <div className="flex items-center justify-between rounded-md border border-border px-3 py-2">
              <div className="min-w-0">
                <p className="text-sm font-medium text-ink">自动匹配模型</p>
                <p className="text-xs text-ink-muted">
                  {name.trim()
                    ? `以「${name.trim()}」为关键词，自动匹配所有名称包含该关键词的渠道模型`
                    : "请先填写分组名称"}
                </p>
                {autoMatch && autoMatchCount > 0 && (
                  <p className="mt-0.5 text-[11px] text-emerald-500">
                    已匹配 {autoMatchCount} 个模型
                  </p>
                )}
              </div>
              <Switch
                checked={autoMatch}
                onCheckedChange={(v) => {
                  setAutoMatch(v);
                  updateRelay("auto_match_models", v);
                }}
              />
            </div>
          </div>
          <div className="flex min-h-0 flex-1 flex-col md:flex-row">
            {/* 左栏：可用渠道与模型 */}
            <aside className="flex h-[36vh] min-h-0 flex-col border-b border-border md:h-auto md:w-[42%] md:border-b-0 md:border-r">
              <div className="flex items-center gap-1.5 border-b border-border px-4 py-2">
                <span className="text-xs font-medium text-ink-muted">
                  可用渠道与模型
                </span>
                <span className="ml-auto text-[11px] text-ink-subtle">
                  点击添加到分组
                </span>
              </div>
              <ChannelModelPicker
                channels={channels ?? []}
                groups={otherGroups}
                addedModelIds={addedModelIds}
                addedRefNames={addedRefNames}
                onAdd={addItem}
              />
            </aside>

            {/* 右栏：成员列表 */}
            <section className="flex min-h-0 flex-1 flex-col">
              <div className="flex-1 space-y-2 overflow-y-auto p-4">
                <div className="flex items-center gap-2">
                  <p className="text-xs font-medium tracking-wide text-ink-muted uppercase">
                    成员
                  </p>
                  <Pill tone="neutral">{draftItems.length}</Pill>
                  <span className="text-[11px] text-ink-subtle">
                    {mode === "failover" ? "按顺序故障转移" : "手动指定当前成员"}
                  </span>
                </div>

                <ul className="space-y-1.5" role="list">
                  {draftItems.length === 0 ? (
                    <li className="rounded-md bg-surface-subtle/40 p-6 text-center text-xs text-ink-muted">
                      还没有成员，从左侧点击渠道模型或引用分组来添加
                    </li>
                  ) : (
                    draftItems.map((it, idx) => {
                      const channel = it.channel_model
                        ? channelById.get(it.channel_model.channel_id)
                        : undefined;
                      const label = it.ref_group_name
                        ? `→ 引用：${it.ref_group_name}`
                        : it.channel_model?.name
                          ? `${channel?.name ?? "?"} → ${it.channel_model.name}（#${it.channel_model_id}）`
                          : `渠道模型 #${it.channel_model_id}`;
                      const channelDisabled =
                        !it.ref_group_name &&
                        channel !== undefined &&
                        !channel.enabled;
                      return (
                        <li
                          key={it.client_uid}
                          className={cn(
                            "flex items-center gap-2 rounded-md border border-border bg-card/60 px-2.5 py-1.5",
                            channelDisabled && "opacity-70",
                          )}
                        >
                          {mode === "manual" ? (
                            <label className="flex shrink-0 items-center gap-1 text-[11px] text-ink-muted">
                              <input
                                type="radio"
                                name="active-group-item"
                                checked={it.id > 0 && activeItemId === it.id}
                                disabled={it.id === 0}
                                onChange={() => it.id > 0 && setActiveItemId(it.id)}
                                aria-label={`设为当前成员 ${label}`}
                                className="h-3.5 w-3.5 accent-primary"
                              />
                              当前
                            </label>
                          ) : null}
                          <Pill tone="neutral">#{it.priority}</Pill>
                          <span className="mono flex-1 truncate text-sm text-ink">
                            {label}
                          </span>
                          {channelDisabled && (
                            <Pill tone="danger" className="text-[10px]">
                              已停用
                            </Pill>
                          )}
                          {it.id !== 0 && (
                            <Pill tone="info" className="text-[10px]">
                              已保存
                            </Pill>
                          )}
                          <div className="flex items-center gap-0.5">
                            <Button
                              variant="ghost"
                              size="icon"
                              className="h-7 w-7"
                              onClick={() => moveItem(it.client_uid, -1)}
                              disabled={idx === 0}
                              aria-label="上移"
                            >
                              <ChevronUp className="h-3.5 w-3.5" aria-hidden />
                            </Button>
                            <Button
                              variant="ghost"
                              size="icon"
                              className="h-7 w-7"
                              onClick={() => moveItem(it.client_uid, 1)}
                              disabled={idx === draftItems.length - 1}
                              aria-label="下移"
                            >
                              <ChevronDown className="h-3.5 w-3.5" aria-hidden />
                            </Button>
                            <Button
                              variant="ghost"
                              size="icon"
                              className="h-7 w-7 text-destructive hover:bg-destructive/10"
                              onClick={() => removeItem(it.client_uid)}
                              aria-label="移除"
                            >
                              <X className="h-3.5 w-3.5" aria-hidden />
                            </Button>
                          </div>
                        </li>
                      );
                    })
                  )}
                </ul>
              </div>
            </section>
          </div>
          </>
        )}

        {/* ---------- Tab: 路由策略 ---------- */}
        {tab === "relay" && (
          <div className="flex-1 overflow-y-auto p-4">
            <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
              <Field
                label="总尝试次数"
                hint="单个成员包含首次请求的总尝试次数"
              >
                <Input
                  type="number"
                  min={1}
                  value={relayConfig.member_max_attempts}
                  onChange={(e) =>
                    updateRelay(
                      "member_max_attempts",
                      Math.max(1, Number(e.target.value) || 1),
                    )
                  }
                />
              </Field>
              <Field
                label="网络错误重试次数"
                hint="代理/DNS/TLS 等网络错误连续重试次数，达到后走正常冷却通道；0 表示与总尝试次数一致"
              >
                <Input
                  type="number"
                  min={0}
                  value={relayConfig.member_infra_max_retries}
                  onChange={(e) =>
                    updateRelay(
                      "member_infra_max_retries",
                      Math.max(0, Number(e.target.value) || 0),
                    )
                  }
                />
              </Field>
              <Field
                label="重试间隔（秒）"
                hint="同一成员相邻两次尝试的等待时间"
              >
                <Input
                  type="number"
                  min={1}
                  value={relayConfig.member_retry_interval_seconds}
                  onChange={(e) =>
                    updateRelay(
                      "member_retry_interval_seconds",
                      Math.max(1, Number(e.target.value) || 1),
                    )
                  }
                />
              </Field>
              <Field
                label="单请求最大轮次"
                hint="单个请求内允许的最大选路轮次，超过后请求以失败收尾，防止失控轮转"
              >
                <Input
                  type="number"
                  min={1}
                  value={relayConfig.max_request_rounds}
                  onChange={(e) =>
                    updateRelay(
                      "max_request_rounds",
                      Math.max(1, Number(e.target.value) || 1),
                    )
                  }
                />
              </Field>
              <Field
                label="单请求截止时间（秒，0 为不限）"
                hint="单个请求的整体安全截止秒数，超过后不再发起新一轮尝试；填 0 表示不限时"
              >
                <Input
                  type="number"
                  min={0}
                  max={86400}
                  placeholder="0 (不限)"
                  value={relayConfig.max_request_seconds}
                  onChange={(e) =>
                    updateRelay(
                      "max_request_seconds",
                      Math.max(0, Number(e.target.value) || 0),
                    )
                  }
                />
              </Field>
              <Field
                label="非流式超时（秒）"
                hint="等待完整非流式响应的最长时间"
              >
                <Input
                  type="number"
                  min={1}
                  value={relayConfig.member_non_stream_response_timeout_seconds}
                  onChange={(e) =>
                    updateRelay(
                      "member_non_stream_response_timeout_seconds",
                      Math.max(1, Number(e.target.value) || 1),
                    )
                  }
                />
              </Field>
              <Field
                label="流式首事件超时（秒）"
                hint="等待首个有效流事件的最长时间"
              >
                <Input
                  type="number"
                  min={1}
                  value={relayConfig.member_stream_first_event_timeout_seconds}
                  onChange={(e) =>
                    updateRelay(
                      "member_stream_first_event_timeout_seconds",
                      Math.max(1, Number(e.target.value) || 1),
                    )
                  }
                />
              </Field>
              <Field
                label="冷却时间（秒）"
                hint="成员重试耗尽后暂停使用的时间"
              >
                <Input
                  type="number"
                  min={1}
                  value={relayConfig.member_cooldown_seconds}
                  onChange={(e) =>
                    updateRelay(
                      "member_cooldown_seconds",
                      Math.max(1, Number(e.target.value) || 1),
                    )
                  }
                />
              </Field>
              <Field
                label="冷却退避倍数"
                hint="半开探测失败后冷却时间乘以该倍数，最小为 1 表示不退避"
              >
                <Input
                  type="number"
                  min={1}
                  step={0.5}
                  value={relayConfig.cooldown_backoff_multiplier}
                  onChange={(e) =>
                    updateRelay(
                      "cooldown_backoff_multiplier",
                      Math.max(1, Number(e.target.value) || 1),
                    )
                  }
                />
              </Field>
              <Field
                label="冷却上限（秒）"
                hint="退避后的冷却时间不超过该值"
              >
                <Input
                  type="number"
                  min={1}
                  value={relayConfig.cooldown_max_seconds}
                  onChange={(e) =>
                    updateRelay(
                      "cooldown_max_seconds",
                      Math.max(1, Number(e.target.value) || 1),
                    )
                  }
                />
              </Field>
              <Field
                label="全冷却重试间隔（秒）"
                hint="所有成员冷却中时自动清除冷却并依次重试的基础间隔，每轮递增；0 表示不自动清除"
              >
                <Input
                  type="number"
                  min={0}
                  value={relayConfig.all_cooldown_retry_base_seconds}
                  onChange={(e) =>
                    updateRelay(
                      "all_cooldown_retry_base_seconds",
                      Math.max(0, Number(e.target.value) || 0),
                    )
                  }
                />
              </Field>
              <Field
                label="全冷却重试上限（秒）"
                hint="全冷却自动重试的间隔上限"
              >
                <Input
                  type="number"
                  min={1}
                  value={relayConfig.all_cooldown_retry_max_seconds}
                  onChange={(e) =>
                    updateRelay(
                      "all_cooldown_retry_max_seconds",
                      Math.max(1, Number(e.target.value) || 1),
                    )
                  }
                />
              </Field>
              <Field
                label="故障切换亲和时间（秒）"
                hint="备用成员首次请求成功后继续使用该成员的时间"
              >
                <Input
                  type="number"
                  min={0}
                  value={relayConfig.member_affinity_seconds}
                  onChange={(e) =>
                    updateRelay(
                      "member_affinity_seconds",
                      Math.max(0, Number(e.target.value) || 0),
                    )
                  }
                />
              </Field>
              <Field
                label="会话粘合时长（秒）"
                hint="粘合成员每次业务成功后滑动续期的时长"
              >
                <Input
                  type="number"
                  min={0}
                  value={relayConfig.session_sticky_seconds}
                  onChange={(e) =>
                    updateRelay(
                      "session_sticky_seconds",
                      Math.max(0, Number(e.target.value) || 0),
                    )
                  }
                />
              </Field>
              <Field
                label="后台探测间隔（秒）"
                hint="两次后台探测之间的间隔时间"
              >
                <Input
                  type="number"
                  min={1}
                  value={relayConfig.background_probe_interval_seconds}
                  onChange={(e) =>
                    updateRelay(
                      "background_probe_interval_seconds",
                      Math.max(1, Number(e.target.value) || 1),
                    )
                  }
                />
              </Field>
              <Field
                label="紧急兜底成员"
                hint="全部成员不可用时的最后放行成员（仅故障转移模式生效），业务失败照常计入冷却"
              >
                <Select
                  className="h-9 w-full text-sm"
                  value={relayConfig.emergency_item_id}
                  onChange={(e) =>
                    updateRelay(
                      "emergency_item_id",
                      Number(e.target.value) || 0,
                    )
                  }
                >
                  <option value={0}>关闭</option>
                  {draftItems
                    .filter((m) => m.id > 0)
                    .map((m) => (
                      <option key={m.id} value={m.id}>
                        {m.ref_group_name
                          ? `→ ${m.ref_group_name}`
                          : `${m.channel_model?.name ?? `#${m.channel_model_id}`}`}
                      </option>
                    ))}
                </Select>
              </Field>
            </div>

            {/* 开关类配置 */}
            <div className="mt-4 space-y-2">
              <Toggle
                label="会话粘合"
                description="同一会话的请求在粘合有效期内固定使用同一成员"
                checked={relayConfig.session_sticky_enabled}
                onChange={(v) => updateRelay("session_sticky_enabled", v)}
              />
              <Toggle
                label="后台定时探测"
                description="对处于冷却（OPEN）状态的成员周期性发起半开测试"
                checked={relayConfig.background_probe_enabled}
                onChange={(v) => updateRelay("background_probe_enabled", v)}
              />
              <Toggle
                label="协议透传偏好"
                description="开启后优先将请求透传给与客户端协议相同的渠道，减少转换开销"
                checked={relayConfig.prefer_passthrough}
                onChange={(v) => updateRelay("prefer_passthrough", v)}
              />
              <Toggle
                label="启用脱敏"
                description="对本分组的请求启用脱敏（须同时全局开启才生效）"
                checked={!!relayConfig.mask_enabled}
                onChange={(v) => updateRelay("mask_enabled", v)}
              />
            </div>
          </div>
        )}

        <DialogFooter>
          <Button variant="ghost" size="sm" onClick={onClose}>
            取消
          </Button>
          <Button
            variant="primary"
            size="sm"
            loading={saveMut.isPending}
            disabled={
              !name ||
              !!validateField(name, NAME_RULE) ||
              !(
                Number.isFinite(relayConfig.max_request_rounds) &&
                relayConfig.max_request_rounds >= 1
              ) ||
              !(
                Number.isFinite(relayConfig.member_cooldown_seconds) &&
                relayConfig.member_cooldown_seconds >= 1
              )
            }
            onClick={() => saveMut.mutate()}
          >
            保存
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/**
 * 左栏选择器：列出所有渠道及其模型，点击即加入分组成员。
 *  - 顶部搜索框按渠道名 / 模型名过滤
 *  - 停用渠道保留（方便提前配置备用成员），标注「已停用」
 *  - 已在当前分组中的模型 / 引用显示 ✓ 标记并禁用，不可重复添加
 *  - 底部「引用其他分组」区列出可被引用的分组
 */
function ChannelModelPicker({
  channels,
  groups,
  addedModelIds,
  addedRefNames,
  onAdd,
}: {
  channels: Channel[];
  groups: Group[];
  addedModelIds: Set<number>;
  addedRefNames: Set<string>;
  onAdd: (it: {
    channel_model_id: number;
    ref_group_name: string;
    channel_model?: ChannelModel;
  }) => void;
}) {
  const [search, setSearch] = useState("");
  // 渠道默认折叠，点击展开才显示其下模型（对齐 NovaVeil_api 的 Accordion 行为）。
  // 多个渠道可同时展开；搜索时自动展开命中渠道，避免折叠态下看不到匹配模型。
  const [expanded, setExpanded] = useState<Set<number>>(() => new Set());

  const q = search.trim().toLowerCase();
  const hasSearch = q.length > 0;

  const filtered = useMemo(() => {
    if (!q) return channels;
    return channels.filter(
      (c) =>
        c.name.toLowerCase().includes(q) ||
        c.models.some((m) => m.name.toLowerCase().includes(q)),
    );
  }, [channels, q]);

  // 搜索时同步过滤引用分组，否则底部「引用其他分组」始终全量展示
  const filteredGroups = useMemo(() => {
    if (!q) return groups;
    return groups.filter((g) => g.name.toLowerCase().includes(q));
  }, [groups, q]);
  // 搜索态下强制展开所有命中渠道；非搜索态用用户手动展开集合。
  const expandedIds = hasSearch
    ? new Set(filtered.map((c) => c.id))
    : expanded;

  function toggleChannel(id: number) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* 搜索 */}
      <div className="border-b border-border px-3 py-2">
        <label className="relative block">
          <Search
            className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-ink-muted"
            aria-hidden
          />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="搜索渠道或模型…"
            className="h-8 pl-7"
            aria-label="搜索渠道或模型"
          />
        </label>
      </div>

      {/* 渠道 → 模型 列表 */}
      <div
        className="flex-1 space-y-1.5 overflow-y-auto p-2"
        role="list"
        aria-label="选择渠道模型"
      >
        {filtered.length === 0 ? (
          <p className="p-6 text-center text-xs text-ink-muted">
            {search ? "没有匹配的渠道或模型" : "还没有可用渠道"}
          </p>
        ) : (
          filtered.map((channel) => {
            const isOpen = expandedIds.has(channel.id);
            const addedInChannel = channel.models.filter((m) =>
              addedModelIds.has(m.id),
            ).length;
            const available = channel.models.length - addedInChannel;
            // 搜索时只显示匹配的模型；非搜索态显示全部
            const visibleModels = hasSearch
              ? channel.models.filter((m) =>
                  m.name.toLowerCase().includes(q),
                )
              : channel.models;
            return (
            <div
              key={channel.id}
              className="overflow-hidden rounded-md border border-border"
            >
              {/* 渠道头：点击展开/折叠模型（默认折叠，对齐 NovaVeil_api） */}
              <button
                type="button"
                onClick={() => toggleChannel(channel.id)}
                aria-expanded={isOpen}
                aria-label={`渠道 ${channel.name}`}
                className="flex w-full items-center gap-1.5 bg-surface-subtle/30 px-2.5 py-1.5 text-left transition-colors hover:bg-surface-subtle/50"
              >
                <ChevronDown
                  className={cn(
                    "h-3.5 w-3.5 shrink-0 text-ink-muted transition-transform duration-200",
                    isOpen && "rotate-180",
                  )}
                  aria-hidden
                />
                <span
                  className={cn(
                    "flex-1 truncate text-sm font-medium",
                    !channel.enabled && "text-ink-muted",
                  )}
                >
                  {channel.name}
                </span>
                <Pill tone="neutral" className="text-[10px]">
                  {available}/{channel.models.length}
                </Pill>
                <Pill tone="neutral" className="text-[10px]">
                  {PROVIDER_LABELS[channel.type] ?? channel.type}
                </Pill>
                {!channel.enabled && (
                  <Pill tone="danger" className="text-[10px]">
                    已停用
                  </Pill>
                )}
              </button>
              {/* 模型行：展开后点击添加 */}
              {isOpen && (
                <div className="border-t border-border/60">
                  {visibleModels.length === 0 ? (
                    <p className="px-2.5 py-1.5 text-[11px] text-ink-subtle">
                      {hasSearch ? "无匹配模型" : "无模型"}
                    </p>
                  ) : (
                    visibleModels.map((m) => {
                      const added = addedModelIds.has(m.id);
                      return (
                        <button
                          key={m.id}
                          type="button"
                          disabled={added}
                          onClick={() =>
                            onAdd({
                              channel_model_id: m.id,
                              ref_group_name: "",
                              channel_model: m,
                            })
                          }
                          className={cn(
                            "flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-sm transition-colors",
                            added
                              ? "cursor-not-allowed opacity-60"
                              : "hover:bg-primary/[0.06] active:bg-primary/[0.1]",
                          )}
                          aria-label={
                            added
                              ? `${channel.name} ${m.name} 已添加`
                              : `添加 ${channel.name} ${m.name}`
                          }
                        >
                          {added ? (
                            <Check
                              className="h-3.5 w-3.5 shrink-0 text-emerald-500"
                              aria-hidden
                            />
                          ) : (
                            <Plus
                              className="h-3.5 w-3.5 shrink-0 text-ink-muted"
                              aria-hidden
                            />
                          )}
                          <span className="mono flex-1 truncate">{m.name}</span>
                        </button>
                      );
                    })
                  )}
                </div>
              )}
            </div>
            );
          })
        )}

        {/* 引用其他分组 */}
        {filteredGroups.length > 0 && (
          <div className="mt-2 overflow-hidden rounded-md border border-border">
            <div className="flex items-center gap-1.5 bg-surface-subtle/30 px-2.5 py-1.5">
              <CornerDownLeft
                className="h-3.5 w-3.5 text-ink-muted"
                aria-hidden
              />
              <span className="flex-1 text-sm font-medium">引用其他分组</span>
            </div>
            <div className="border-t border-border/60">
              {filteredGroups.map((g) => {
                const added = addedRefNames.has(g.name);
                return (
                  <button
                    key={g.id}
                    type="button"
                    disabled={added}
                    onClick={() =>
                      onAdd({ channel_model_id: 0, ref_group_name: g.name })
                    }
                    className={cn(
                      "flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-sm transition-colors",
                      added
                        ? "cursor-not-allowed opacity-60"
                        : "hover:bg-primary/[0.06] active:bg-primary/[0.1]",
                    )}
                    aria-label={
                      added
                        ? `引用分组 ${g.name} 已添加`
                        : `引用分组 ${g.name}`
                    }
                  >
                    {added ? (
                      <Check
                        className="h-3.5 w-3.5 shrink-0 text-emerald-500"
                        aria-hidden
                      />
                    ) : (
                      <Plus
                        className="h-3.5 w-3.5 shrink-0 text-ink-muted"
                        aria-hidden
                      />
                    )}
                    <span className="mono flex-1 truncate">→ {g.name}</span>
                  </button>
                );
              })}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function Toggle({
  label,
  description,
  checked,
  onChange,
}: {
  label: string;
  description: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="flex items-center justify-between rounded-md border border-border px-3 py-2">
      <div>
        <p className="text-sm font-medium text-ink">{label}</p>
        <p className="text-xs text-ink-muted">{description}</p>
      </div>
      <Switch checked={checked} onCheckedChange={onChange} />
    </div>
  );
}

/**
 * 成员运行时状态 chip：冷却/亲和实时倒计时、半开探测中。
 * useNow 只让本组件每秒重渲染，不拖动整页卡片（审计 §1.7 同款约束）。
 */
function MemberRuntimeChips({
  state,
  itemId,
}: {
  state: GroupRouteState;
  itemId: number;
}) {
  const now = useNow();
  const cooldownSecondsLeft = remainingSeconds(
    state.cooldowns?.[String(itemId)] ?? 0,
    now,
  );
  if (cooldownSecondsLeft > 0) {
    return (
      <Pill tone="danger">冷却 {formatCountdown(cooldownSecondsLeft)}</Pill>
    );
  }
  // 亲和挂在分组的 current_item_id 上，只有当前承载成员显示
  const affinitySecondsLeft = remainingSeconds(state.affinity_until, now);
  if (state.current_item_id === itemId && affinitySecondsLeft > 0) {
    return (
      <Pill tone="info">亲和 {formatCountdown(affinitySecondsLeft)}</Pill>
    );
  }
  if (state.probe_item_id === itemId) {
    return <Pill tone="warning">半开探测中</Pill>;
  }
  return null;
}

