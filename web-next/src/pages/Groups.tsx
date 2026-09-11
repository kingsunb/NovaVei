import { useEffect, useMemo, useState } from "react";
import { Field } from "@/components/ui/field";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Plus,
  Trash2,
  Search,
  Snowflake,
  Pencil,
  X,
  ChevronUp,
  ChevronDown,
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
  DialogBody,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Pill } from "@/components/ui/pill";
import { Switch } from "@/components/ui/switch";
import { cn, NAME_RULE, validateField } from "@/lib/utils";

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
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex items-center gap-1 rounded-control border border-border bg-card/60 p-0.5 text-xs">
          {(["all", "failover", "manual"] as const).map((m) => (
            <button
              key={m}
              onClick={() => setMode(m)}
              aria-pressed={mode === m}
              className={cn(
                "rounded-[5px] px-2.5 py-1 transition-colors",
                mode === m
                  ? "bg-primary/12 font-medium text-primary-text"
                  : "text-ink-muted hover:text-ink",
              )}
            >
              {m === "all" ? "全部" : MODE_LABELS[m]}
            </button>
          ))}
        </div>

        <label className="flex items-center gap-1.5 text-xs text-ink-muted">
          排序
          <select
            aria-label="分组排序方式"
            className="h-7 rounded-control border border-border bg-card px-2 text-xs"
            value={sort}
            onChange={(e) => changeSort(e.target.value as GroupSort)}
          >
            <option value="priority">按优先级</option>
            <option value="name">按名称</option>
            <option value="mode">按模式</option>
            <option value="custom">自定义</option>
          </select>
        </label>

        <div className="ml-auto flex items-center gap-2">
          <label className="relative">
            <Search
              className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-ink-muted"
              aria-hidden
            />
            <Input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="搜索分组…"
              className="h-8 w-56 pl-7"
            />
          </label>
          <Button
            variant="primary"
            size="sm"
            className="gap-1.5"
            onClick={() => setEditing("new")}
          >
            <Plus className="h-3.5 w-3.5" aria-hidden />
            新建分组
          </Button>
        </div>
      </div>

      {isLoading ? (
        <CardGridSkeleton cards={6} />
      ) : isError ? (
        <QueryErrorBanner onRetry={() => refetch()} />
      ) : rows.length === 0 ? (
        <Card>
          <CardContent className="py-12 text-center text-sm text-ink-muted">
            {data?.length ? "没有匹配的分组" : "还没有分组"}
          </CardContent>
        </Card>
      ) : (
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
          {rows.map((g, idx) => {
            const routeState = runtime.get(g.id);
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
                    {MODE_LABELS[g.mode]}
                  </Pill>
                  {routeState && routeState.emergency_active > 0 && (
                    <Pill tone="danger">紧急兜底</Pill>
                  )}
                </div>
                <span className="text-xs text-ink-muted">
                  {g.items.length} 个成员
                </span>
              </CardHeader>
              <CardContent className="space-y-1.5">
                {g.items
                  .slice()
                  .sort((a, b) => a.priority - b.priority)
                  .slice(0, 4)
                  .map((it) => (
                    <div
                      key={it.id}
                      className="flex items-center justify-between gap-2 text-xs"
                    >
                      <span className="truncate text-ink-muted">
                        {it.ref_group_name
                          ? `→ ${it.ref_group_name}`
                          : `${channelById.get(it.channel_model?.channel_id ?? 0)?.name ?? "?"} → ${it.channel_model?.name ?? `#${it.channel_model_id}`}`}
                      </span>
                      <div className="flex shrink-0 items-center gap-1">
                        {routeState && (
                          <MemberRuntimeChips
                            state={routeState}
                            itemId={it.id}
                          />
                        )}
                        {g.mode === "manual" && g.active_item_id === it.id && (
                          <Pill tone="success">当前</Pill>
                        )}
                        <Pill tone="neutral">#{it.priority}</Pill>
                      </div>
                    </div>
                  ))}
                {g.items.length > 4 && (
                  <p className="text-[11px] text-ink-muted">
                    +{g.items.length - 4} 个成员
                  </p>
                )}

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
  const [sticky, setSticky] = useState(
    DEFAULT_GROUP_RELAY_CONFIG.session_sticky_enabled,
  );
  const [preferPassthrough, setPreferPassthrough] = useState(
    DEFAULT_GROUP_RELAY_CONFIG.prefer_passthrough,
  );
  const [maskEnabled, setMaskEnabled] = useState(false);
  const [maxRounds, setMaxRounds] = useState(
    DEFAULT_GROUP_RELAY_CONFIG.max_request_rounds,
  );
  const [cooldownSeconds, setCooldownSeconds] = useState(
    DEFAULT_GROUP_RELAY_CONFIG.member_cooldown_seconds,
  );
  const [activeItemId, setActiveItemId] = useState(0);
  const [tab, setTab] = useState<"basic" | "members">("basic");

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

  useEffect(() => {
    if (group && group !== "new") {
      setName(group.name);
      setMode(group.mode);
      // relay_config 由后端负责字段兜底；这里再兜一道，避免旧版本/手工数据
      // 缺字段时本端把 0 / undefined 写回覆盖原值。兜底值取后端
      // DefaultGroupRelayConfig 同款默认（与 legacy 表单一致），而不是 0/60。
      const relay = group.relay_config;
      setSticky(
        relay
          ? !!relay.session_sticky_enabled
          : DEFAULT_GROUP_RELAY_CONFIG.session_sticky_enabled,
      );
      setPreferPassthrough(
        relay
          ? !!relay.prefer_passthrough
          : DEFAULT_GROUP_RELAY_CONFIG.prefer_passthrough,
      );
      setMaskEnabled(!!relay?.mask_enabled);
      setMaxRounds(
        relay?.max_request_rounds ??
          DEFAULT_GROUP_RELAY_CONFIG.max_request_rounds,
      );
      setCooldownSeconds(
        relay?.member_cooldown_seconds ??
          DEFAULT_GROUP_RELAY_CONFIG.member_cooldown_seconds,
      );
      setActiveItemId(group.active_item_id ?? 0);
      const sorted = [...(group.items ?? [])]
        .sort((a, b) => a.priority - b.priority)
        .map((item) => ({
          ...item,
          client_uid: item.client_uid ?? `saved:${item.id}`,
        }));
      setOriginalItems(sorted);
      setDraftItems(sorted);
    } else {
      setName("");
      setMode("manual");
      // 新建表单与后端/legacy 默认对齐：会话粘合开、轮次 600（此前误用
      // false/60，新建出来的分组偏离 failover-first 默认还得手工调）。
      setSticky(DEFAULT_GROUP_RELAY_CONFIG.session_sticky_enabled);
      setPreferPassthrough(DEFAULT_GROUP_RELAY_CONFIG.prefer_passthrough);
      setMaskEnabled(false);
      setMaxRounds(DEFAULT_GROUP_RELAY_CONFIG.max_request_rounds);
      setCooldownSeconds(DEFAULT_GROUP_RELAY_CONFIG.member_cooldown_seconds);
      setActiveItemId(0);
      setOriginalItems([]);
      setDraftItems([]);
    }
    setTab("basic");
  }, [group]);

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

  function addItem(it: { channel_model_id: number; ref_group_name: string }) {
    setDraftItems((prev) => [
      ...prev,
      {
        client_uid: nextDraftItemUid(),
        id: 0, // 0 表示新建，后端会分配
        group_id: group && group !== "new" ? group.id : 0,
        channel_model_id: it.channel_model_id,
        ref_group_name: it.ref_group_name,
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
          // 未暴露到表单的字段全部取后端 DefaultGroupRelayConfig 同款默认值
          // （failover-first 调优版），避免新建分组继承过期的手写数值。
          relay_config: {
            ...DEFAULT_GROUP_RELAY_CONFIG,
            member_cooldown_seconds: cooldownSeconds,
            max_request_rounds: maxRounds,
            session_sticky_enabled: sticky,
            prefer_passthrough: preferPassthrough,
            mask_enabled: maskEnabled,
          },
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
      const relayUpdates: Partial<GroupRelayConfig> = {};
      if (!!sticky !== !!previous.session_sticky_enabled) {
        relayUpdates.session_sticky_enabled = sticky;
      }
      if (!!preferPassthrough !== !!previous.prefer_passthrough) {
        relayUpdates.prefer_passthrough = preferPassthrough;
      }
      if (!!maskEnabled !== !!previous.mask_enabled) {
        relayUpdates.mask_enabled = maskEnabled;
      }
      if (maxRounds !== previous.max_request_rounds) {
        relayUpdates.max_request_rounds = maxRounds;
      }
      if (cooldownSeconds !== previous.member_cooldown_seconds) {
        relayUpdates.member_cooldown_seconds = cooldownSeconds;
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

  if (!open) return null;

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent variant="sheet">
        <DialogHeader>
          <DialogTitle>{isNew ? "新建分组" : `编辑：${name}`}</DialogTitle>
          <DialogDescription>
            分组是客户端模型名的承载单元；成员可来自渠道模型或引用其他分组
          </DialogDescription>
        </DialogHeader>

        {/* 锚点 Tab —— WAI-ARIA tabs 模式：id/aria-controls 关联 + 左右方向键切换 */}
        <div
          className="flex items-center gap-1 border-b border-border bg-card/30 px-4"
          role="tablist"
          aria-label="分组编辑区块"
          onKeyDown={(e) => {
            if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
            e.preventDefault();
            const next = tab === "basic" ? "members" : "basic";
            setTab(next);
            document.getElementById(`group-tab-${next}`)?.focus();
          }}
        >
          {[
            { k: "basic", label: "基本信息" },
            { k: "members", label: `成员 (${draftItems.length})` },
          ].map((t) => (
            <button
              key={t.k}
              id={`group-tab-${t.k}`}
              type="button"
              role="tab"
              aria-selected={tab === t.k}
              aria-controls={`group-panel-${t.k}`}
              tabIndex={tab === t.k ? 0 : -1}
              onClick={() => setTab(t.k as typeof tab)}
              className={cn(
                "border-b-2 px-3 py-2 text-sm transition-colors",
                tab === t.k
                  ? "border-primary font-medium text-primary-text"
                  : "border-transparent text-ink-muted hover:text-ink",
              )}
            >
              {t.label}
            </button>
          ))}
        </div>

        <DialogBody
          className="space-y-4"
          role="tabpanel"
          id={`group-panel-${tab}`}
          aria-labelledby={`group-tab-${tab}`}
          tabIndex={0}
        >
          {tab === "basic" ? (
            <>
              <Field label="名称" required error={validateField(name, NAME_RULE) ?? undefined}>
                <Input
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="例如：gpt-4o-prod"
                  invalid={!!validateField(name, NAME_RULE)}
                  aria-invalid={!!validateField(name, NAME_RULE)}
                />
              </Field>
              <Field label="模式">
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

              <div className="grid grid-cols-2 gap-3">
                <Field
                  label="最大轮次"
                  error={
                    Number.isFinite(maxRounds) && maxRounds >= 1
                      ? undefined
                      : "最小为 1"
                  }
                >
                  <Input
                    type="number"
                    min={1}
                    value={maxRounds}
                    onChange={(e) => setMaxRounds(Number(e.target.value))}
                    invalid={!(Number.isFinite(maxRounds) && maxRounds >= 1)}
                    aria-invalid={!(Number.isFinite(maxRounds) && maxRounds >= 1)}
                  />
                </Field>
                <Field
                  label="成员冷却（秒）"
                  error={
                    Number.isFinite(cooldownSeconds) && cooldownSeconds >= 1
                      ? undefined
                      : "最小为 1"
                  }
                >
                  <Input
                    type="number"
                    min={1}
                    value={cooldownSeconds}
                    onChange={(e) => setCooldownSeconds(Number(e.target.value))}
                    invalid={
                      !(Number.isFinite(cooldownSeconds) && cooldownSeconds >= 1)
                    }
                    aria-invalid={
                      !(Number.isFinite(cooldownSeconds) && cooldownSeconds >= 1)
                    }
                  />
                </Field>
              </div>

              <Toggle
                label="会话粘合"
                description="同一会话的请求在粘合有效期内固定使用同一成员"
                checked={sticky}
                onChange={setSticky}
              />
              <Toggle
                label="优先透传"
                description="故障转移时优先选择与客户端协议相同的渠道直接透传"
                checked={preferPassthrough}
                onChange={setPreferPassthrough}
              />
              <Toggle
                label="启用脱敏"
                description="对本分组的请求启用脱敏（须同时全局开启才生效）"
                checked={maskEnabled}
                onChange={setMaskEnabled}
              />

              {!isNew && group && (
                <div className="rounded-md border border-border bg-surface-subtle/30 p-3 text-xs text-ink-muted">
                  <p>提示：切到「成员」Tab 可调整成员顺序、增删成员。</p>
                </div>
              )}
            </>
          ) : (
            <MembersTab
              items={draftItems}
              channels={channels ?? []}
              groups={(groups ?? []).filter(
                (g) => g.id !== (group && group !== "new" ? group.id : 0),
              )}
              mode={mode}
              activeItemId={activeItemId}
              onActiveChange={setActiveItemId}
              onMove={moveItem}
              onRemove={removeItem}
              onAdd={addItem}
            />
          )}
        </DialogBody>
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
              !(Number.isFinite(maxRounds) && maxRounds >= 1) ||
              !(Number.isFinite(cooldownSeconds) && cooldownSeconds >= 1)
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
 * Group 成员编辑子面板
 *  - 显示当前 draft 顺序（按 priority 升序）
 *  - 支持上移/下移（改 priority）和删除
 *  - 新增：渠道模型（从所有 enabled 渠道拉取）或引用其他分组
 *  - 实际写回时在 buildMemberDiff() 拆成 items_to_add/update/delete
 */
function MembersTab({
  items,
  channels,
  groups,
  mode,
  activeItemId,
  onActiveChange,
  onMove,
  onRemove,
  onAdd,
}: {
  items: DraftGroupItem[];
  channels: Channel[];
  groups: Group[];
  mode: GroupMode;
  activeItemId: number;
  onActiveChange: (id: number) => void;
  onMove: (clientUid: string, dir: -1 | 1) => void;
  onRemove: (clientUid: string) => void;
  onAdd: (it: { channel_model_id: number; ref_group_name: string }) => void;
}) {
  const [refName, setRefName] = useState("");
  const [selectedModel, setSelectedModel] = useState("");

  // 把所有渠道的所有模型展开成下拉选项；停用渠道也保留，方便提前配置备用成员。
  const modelOptions = channels.flatMap((c) =>
      c.models.map((m) => ({
        value: `c:${c.id}:${m.id}`,
        label: `${c.name}${c.enabled ? "" : "（已停用）"} → ${m.name}`,
        channel_model_id: m.id,
      })),
    );

  // channel_model 只有 channel_id，渠道名靠 channels 列表反查；
  // 引用成员（ref_group_name）解析到其他分组，不直接绑定渠道，单独展示。
  const channelById = useMemo(
    () => new Map(channels.map((c) => [c.id, c])),
    [channels],
  );

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <p className="text-sm text-ink-muted">
          {items.length} 个成员；故障转移模式下按顺序选择
        </p>
      </div>

      <ul className="space-y-1.5" role="list">
        {items.length === 0 ? (
          <li className="rounded-md bg-surface-subtle/40 p-4 text-center text-xs text-ink-muted">
            还没有成员，从下方添加
          </li>
        ) : (
          items.map((it, idx) => {
            const channel = it.channel_model
              ? channelById.get(it.channel_model.channel_id)
              : undefined;
            const label = it.ref_group_name
              ? `→ 引用：${it.ref_group_name}`
              : it.channel_model?.name
                ? `${channel?.name ?? "?"} → ${it.channel_model.name}（#${it.channel_model_id}）`
                : `渠道模型 #${it.channel_model_id}`;
            const itemKey = it.client_uid;
            // 渠道级停用状态：停用成员仍可列出但不会被故障转移选中，徽标突出提示。
            const channelDisabled =
              !it.ref_group_name && channel !== undefined && !channel.enabled;
            return (
              <li
                key={itemKey}
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
                      onChange={() => it.id > 0 && onActiveChange(it.id)}
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
                    onClick={() => onMove(it.client_uid, -1)}
                    disabled={idx === 0}
                    aria-label="上移"
                  >
                    <ChevronUp className="h-3.5 w-3.5" aria-hidden />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7"
                    onClick={() => onMove(it.client_uid, 1)}
                    disabled={idx === items.length - 1}
                    aria-label="下移"
                  >
                    <ChevronDown className="h-3.5 w-3.5" aria-hidden />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7 text-destructive hover:bg-destructive/10"
                    onClick={() => onRemove(it.client_uid)}
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

      <div className="space-y-2 border-t border-border pt-4">
        <p className="text-xs font-medium text-ink-muted">添加渠道模型成员</p>
        <div className="flex gap-2">
          <select
            className="h-8 flex-1 rounded-control border border-border bg-card px-2 text-sm"
            value={selectedModel}
            onChange={(e) => setSelectedModel(e.target.value)}
            aria-label="选择渠道模型"
          >
            <option value="">选择渠道模型</option>
            {modelOptions.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
          <Button
            variant="primary"
            size="sm"
            disabled={!selectedModel}
            onClick={() => {
              const opt = modelOptions.find((o) => o.value === selectedModel);
              if (opt) {
                onAdd({ channel_model_id: opt.channel_model_id, ref_group_name: "" });
                setSelectedModel("");
              }
            }}
          >
            <Plus className="h-3.5 w-3.5" aria-hidden />
            添加
          </Button>
        </div>

        {groups.length > 0 && (
          <>
            <p className="text-xs font-medium text-ink-muted">或引用其他分组</p>
            <div className="flex gap-2">
              <select
                className="h-8 flex-1 rounded-control border border-border bg-card px-2 text-sm"
                value={refName}
                onChange={(e) => setRefName(e.target.value)}
                aria-label="选择被引用分组"
              >
                <option value="">选择被引用分组</option>
                {groups.map((g) => (
                  <option key={g.id} value={g.name}>
                    {g.name}
                  </option>
                ))}
              </select>
              <Button
                variant="secondary"
                size="sm"
                disabled={!refName}
                onClick={() => {
                  onAdd({ channel_model_id: 0, ref_group_name: refName });
                  setRefName("");
                }}
              >
                <Plus className="h-3.5 w-3.5" aria-hidden />
                引用
              </Button>
            </div>
          </>
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

