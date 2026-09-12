import { useMemo, useRef, useState } from "react";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Copy,
  Globe,
  KeyRound,
  Layers,
  Link2,
  Pencil,
  Plus,
  Search,
  Trash2,
  Download,
  Upload,
  FlaskConical,
} from "lucide-react";
import { toast } from "sonner";
import { Card } from "@/components/ui/card";
import { ConfirmButton } from "@/components/ui/confirm-button";
import { TableSkeleton } from "@/components/ui/skeleton";
import { QueryErrorBanner } from "@/components/ui/query-error";

import { api } from "@/lib/api";
import type { Channel, ChannelImportResult } from "@/lib/types";
import { Button } from "@/components/ui/button";

import { Input } from "@/components/ui/input";
import { Pill } from "@/components/ui/pill";
import { Switch } from "@/components/ui/switch";
import { cn, downloadText, formatNumber } from "@/lib/utils";
import { ViewToggle } from "@/components/ui/view-toggle";
import { useViewMode } from "@/lib/use-view-mode";
import {
  Dialog,

  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { PROVIDER_LABELS } from "./channels/constants";
import { ChannelEditor } from "./channels/channel-editor";


type Filter = "all" | "on" | "off";
type Sort = "custom" | "name" | "status" | "models";

export default function ChannelsPage() {
  const qc = useQueryClient();
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const [selectedTags, setSelectedTags] = useState<string[]>([]);
  // 默认按自定义排序（sort 值升序、同值按名称兜底），与渠道编辑器里的排序值联动；
  // 排序值允许重复、零值与负值，相同数值按渠道名称字母序排列。
  const [sort, setSort] = useState<Sort>("custom");
  const [viewMode, setViewMode] = useViewMode("nv-channel-view", "grid");
  const [editing, setEditing] = useState<Channel | "new" | null>(null);
  const [testingId, setTestingId] = useState<number | null>(null);
  const [pendingDelete, setPendingDelete] = useState<Channel | null>(null);
  const [confirmExport, setConfirmExport] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [importing, setImporting] = useState(false);
  // 排序值行内编辑草稿: 仅在用户正在输入时持有该行的文本值, 提交或失焦后清除,
  // 其余时候回退显示服务端 c.sort, 避免本地态与远端长期不一致。
  const [sortDraft, setSortDraft] = useState<Record<number, string>>({});

  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ["channels"],
    queryFn: api.listChannels,
  });

  const enableMut = useMutation({
    mutationFn: ({ id, enabled }: { id: number; enabled: boolean }) =>
      api.enableChannel(id, enabled),
    // 乐观更新：立即改本地缓存，失败回滚
    onMutate: async ({ id, enabled }) => {
      await qc.cancelQueries({ queryKey: ["channels"] });
      const prev = qc.getQueryData<Channel[]>(["channels"]);
      if (prev) {
        qc.setQueryData<Channel[]>(
          ["channels"],
          prev.map((c) => (c.id === id ? { ...c, enabled } : c)),
        );
      }
      return { prev };
    },
    onError: (err, _v, ctx) => {
      if (ctx?.prev) qc.setQueryData(["channels"], ctx.prev);
      toast.error(err.message);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ["channels"] });
    },
  });

  const deleteMut = useMutation({
    mutationFn: (id: number) => api.deleteChannel(id),
    onSuccess: () => {
      toast.success("已删除");
      setPendingDelete(null);
      qc.invalidateQueries({ queryKey: ["channels"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  // 复制渠道: 以原渠道为模板新建一条, 默认停用; 名称在原名后追加 "_copy",
  // 若已被占用则依次尝试 "_copy1"、"_copy2" … 直至不重名。
  const copyMut = useMutation({
    mutationFn: (vars: { sourceId: number; body: Omit<Channel, "id"> }) =>
      api.createChannel(vars.body),
    onSuccess: () => {
      toast.success("已复制");
      qc.invalidateQueries({ queryKey: ["channels"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const testMut = useMutation({
    mutationFn: (id: number) => api.testChannel(id),
    onMutate: (id) => setTestingId(id),
    // 只清掉自己那行的 testing 态：连点两行时，先返回的请求不能清掉后一行的指示。
    onSettled: (_data, _error, id) =>
      setTestingId((cur) => (cur === id ? null : cur)),
    onSuccess: (r) => {
      // 后端失败走 HTTP 5xx（onError），200 即成功
      toast.success(`连通 (${r.latency_ms}ms)`);
    },
    onError: (e: Error) => toast.error(e.message),
  });

  // 排序值行内编辑: 乐观更新本地缓存, 失败回滚并恢复输入框为服务端值。
  const sortMut = useMutation({
    mutationFn: ({ id, sort }: { id: number; sort: number }) =>
      api.updateChannel({ id, sort }),
    onMutate: async ({ id, sort }) => {
      await qc.cancelQueries({ queryKey: ["channels"] });
      const prev = qc.getQueryData<Channel[]>(["channels"]);
      if (prev) {
        qc.setQueryData<Channel[]>(
          ["channels"],
          prev.map((c) => (c.id === id ? { ...c, sort } : c)),
        );
      }
      return { prev };
    },
    onError: (err, _v, ctx) => {
      if (ctx?.prev) qc.setQueryData(["channels"], ctx.prev);
      toast.error(err.message);
    },
    onSettled: (_d, _e, vars) => {
      setSortDraft((d) => {
        const next = { ...d };
        delete next[vars.id];
        return next;
      });
      qc.invalidateQueries({ queryKey: ["channels"] });
    },
  });

  function commitSort(c: Channel, raw: string) {
    const trimmed = raw.trim();
    // 空输入或非数字: 不提交, 清除草稿回退显示服务端值。
    if (trimmed === "" || !/^-?\d+$/.test(trimmed)) {
      setSortDraft((d) => {
        const next = { ...d };
        delete next[c.id];
        return next;
      });
      return;
    }
    const next = parseInt(trimmed, 10);
    if (next === (c.sort ?? 0)) {
      // 值未变, 仅清除草稿。
      setSortDraft((d) => {
        const n = { ...d };
        delete n[c.id];
        return n;
      });
      return;
    }
    sortMut.mutate({ id: c.id, sort: next });
  }

  // 收集所有渠道标签，去重并按字母序排列
  const allTags = useMemo(() => {
    const set = new Set<string>();
    for (const c of data ?? []) {
      if (c.type === "custom") continue;
      for (const t of c.tags ?? []) set.add(t);
    }
    return Array.from(set).sort((a, b) => a.localeCompare(b));
  }, [data]);

  const rows = useMemo(() => {
    const list = data ?? [];
    return list
      // 自定义固定回复渠道在「自定义模型」页单独管理, 不混入渠道列表
      .filter((c) => c.type !== "custom")
      .filter((c) =>
        filter === "all" ? true : filter === "on" ? c.enabled : !c.enabled,
      )
      .filter((c) =>
        selectedTags.length === 0
          ? true
          : selectedTags.every((t) => (c.tags ?? []).includes(t)),
      )
      .filter((c) =>
        search
          ? c.name.toLowerCase().includes(search.toLowerCase()) ||
            c.base_url.toLowerCase().includes(search.toLowerCase())
          : true,
      )
      .sort((a, b) => {
        if (sort === "custom")
          return (a.sort ?? 0) - (b.sort ?? 0) || a.name.localeCompare(b.name);
        if (sort === "name") return a.name.localeCompare(b.name);
        if (sort === "status")
          return Number(b.enabled) - Number(a.enabled) ||
            a.name.localeCompare(b.name);
        return (b.models?.length ?? 0) - (a.models?.length ?? 0);
      });
  }, [data, search, filter, sort, selectedTags]);

  async function onExport() {
    try {
      const { text, filename } = await api.exportChannels();
      // 优先用服务端 Content-Disposition 建议的文件名（带时间戳），失败回退本地命名。
      downloadText(filename ?? `channels-${Date.now()}.txt`, text);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "导出失败");
    }
  }

  function onImportClick() {
    fileInputRef.current?.click();
  }

  // 导入: 读取所选 .txt 文件原文, 调后端按导出格式解析并批量建渠道。
  // 后端返回逐条成功/失败计数与原因; 整体 200 即视为成功并刷新列表。
  async function onImportFile(file: File) {
    setImporting(true);
    try {
      const text = await file.text();
      const result: ChannelImportResult = await api.importChannels(text);
      const summary = `成功 ${result.success} 个，失败 ${result.failed} 个`;
      if (result.errors && result.errors.length > 0) {
        toast.error(summary, { description: result.errors.join("\n") });
      } else {
        toast.success(summary);
      }
      qc.invalidateQueries({ queryKey: ["channels"] });
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "导入失败");
    } finally {
      setImporting(false);
    }
  }

  // 复制渠道: 排除 id; keys 清空 id/original_id 让后端重新生成;
  // models 清空 id/channel_id; 新渠道默认停用。
  function onCopy(c: Channel) {
    const list = data ?? [];
    const existing = new Set(list.map((x) => x.name));
    let newName = `${c.name}_copy`;
    let i = 1;
    while (existing.has(newName)) {
      newName = `${c.name}_copy${i}`;
      i++;
    }
    const { id: _id, ...rest } = c;
    copyMut.mutate({
      sourceId: c.id,
      body: {
        ...rest,
        name: newName,
        enabled: false,
        keys: c.keys.map((k) => {
          const { original_id: _oid, ...keyRest } = k;
          return { ...keyRest, id: "" };
        }),
        models: c.models.map((m) => ({ ...m, id: 0, channel_id: 0 })),
      },
    });
  }

  return (
    <div className="space-y-4">
      {/* 隐藏的文件选择器: 由「导入」按钮触发, 选定后读取文本并导入。 */}
      <input
        ref={fileInputRef}
        type="file"
        accept=".txt"
        className="hidden"
        onChange={(e) => {
          const file = e.target.files?.[0];
          if (file) onImportFile(file);
          // 重置 value 以便同一文件可重复选择。
          e.target.value = "";
        }}
      />
      {/* 工具栏 */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex items-center gap-1 rounded-control border border-border bg-card/60 p-0.5 text-xs">
          {(["all", "on", "off"] as const).map((f) => (
            <button
              key={f}
              onClick={() => setFilter(f)}
              className={cn(
                "rounded-[5px] px-2.5 py-1 transition-colors",
                filter === f
                  ? "bg-primary/12 font-medium text-primary-text"
                  : "text-ink-muted hover:text-ink",
              )}
            >
              {f === "all" ? "全部" : f === "on" ? "启用" : "停用"}
            </button>
          ))}
        </div>

        <div className="ml-auto flex items-center gap-2">
          <ViewToggle value={viewMode} onChange={setViewMode} />
          <label className="relative">
            <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-ink-muted" />
            <Input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="搜索渠道"
              className="h-8 w-56 pl-7"
            />
          </label>
          <select
            className="h-8 rounded-control border border-border bg-card px-2 text-xs text-ink-muted"
            value={sort}
            onChange={(e) => setSort(e.target.value as Sort)}
            aria-label="排序"
          >
            <option value="custom">按优先级</option>
            <option value="name">按名称</option>
            <option value="status">按状态</option>
            <option value="models">按模型数</option>
          </select>
          <Button
            variant="ghost"
            size="sm"
            className="gap-1.5"
            onClick={onImportClick}
            loading={importing}
          >
            <Upload className="h-3.5 w-3.5" aria-hidden />
            导入
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="gap-1.5"
            onClick={() => setConfirmExport(true)}
          >
            <Download className="h-3.5 w-3.5" aria-hidden />
            导出
          </Button>
          <Button
            variant="primary"
            size="sm"
            className="gap-1.5"
            onClick={() => setEditing("new")}
          >
            <Plus className="h-3.5 w-3.5" aria-hidden />
            新建渠道
          </Button>
        </div>
      </div>

      {/* 标签筛选：仅当存在标签时显示 */}
      {allTags.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-xs text-ink-muted">标签筛选：</span>
          {allTags.map((t) => {
            const active = selectedTags.includes(t);
            return (
              <button
                key={t}
                onClick={() =>
                  setSelectedTags((prev) =>
                    active ? prev.filter((x) => x !== t) : [...prev, t],
                  )
                }
                className={cn(
                  "rounded-full border px-2.5 py-0.5 text-xs transition-colors",
                  active
                    ? "border-primary/30 bg-primary/15 font-medium text-primary-text"
                    : "border-border bg-card/60 text-ink-muted hover:text-ink",
                )}
              >
                {t}
              </button>
            );
          })}
          {selectedTags.length > 0 && (
            <button
              onClick={() => setSelectedTags([])}
              className="ml-1 text-xs text-ink-muted underline hover:text-ink"
            >
              清除筛选
            </button>
          )}
        </div>
      )}

      {/* 卡片网格 */}
      {isLoading ? (
        <TableSkeleton rows={6} />
      ) : isError ? (
        <QueryErrorBanner onRetry={() => refetch()} />
      ) : rows.length === 0 ? (
        <Card>
          <div className="py-12 text-center text-sm text-ink-muted">
            {data?.length === 0
              ? "还没有渠道，点右上角新建"
              : "没有匹配的渠道"}
          </div>
        </Card>
      ) : (
        <div className={cn("grid grid-cols-1 gap-3", viewMode === "grid" && "md:grid-cols-2 xl:grid-cols-3")}>
          {rows.map((c) => (
            <article
              key={c.id}
              onClick={() => setEditing(c)}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  setEditing(c);
                }
              }}
              tabIndex={0}
              role="button"
              aria-haspopup="dialog"
              aria-label={`编辑渠道 ${c.name}`}
              className="flex cursor-pointer flex-col gap-3 rounded-card border border-border bg-card p-4 transition-colors hover:bg-surface-subtle/40 focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              {/* 头部：名称 + 标签 + 开关 */}
              <header className="flex items-start justify-between gap-2">
                <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
                  <span className="font-semibold text-ink">{c.name}</span>
                  {c.tags?.map((t) => (
                    <Pill key={t} tone="info">
                      {t}
                    </Pill>
                  )) ?? null}
                </div>
                <div
                  onClick={(e) => e.stopPropagation()}
                  className="flex shrink-0 items-center gap-2"
                >
                  <Switch
                    checked={c.enabled}
                    onCheckedChange={(v) =>
                      enableMut.mutate({ id: c.id, enabled: v })
                    }
                  />
                  {c.enabled ? (
                    <Pill tone="success">启用</Pill>
                  ) : (
                    <Pill tone="neutral">停用</Pill>
                  )}
                </div>
              </header>

              {/* 信息面板 */}
              <div className="flex flex-col gap-1.5 rounded-xl border border-border/60 bg-surface-subtle/20 px-3 py-2">
                <div className="flex items-center gap-2">
                  <Pill tone="neutral" dot={false}>
                    {PROVIDER_LABELS[c.type] ?? c.type}
                  </Pill>
                  <span className="flex items-center gap-1 text-xs text-ink-muted">
                    <Layers className="size-3" />
                    {c.models?.length ?? 0} 模型
                  </span>
                  <span className="flex items-center gap-1 text-xs text-ink-muted">
                    <KeyRound className="size-3" />
                    {c.keys?.length || (c.key_masked ? 1 : 0)} 密钥
                  </span>
                </div>
                <div className="flex items-center gap-2 min-w-0">
                  <Link2 className="size-3.5 shrink-0 text-ink-muted" />
                  <span className="mono min-w-0 flex-1 truncate text-xs text-ink-muted">
                    {c.base_url}
                  </span>
                </div>
                {c.proxy && c.channel_proxy && (
                  <div className="flex items-center gap-2 min-w-0">
                    <Globe className="size-3.5 shrink-0 text-ink-muted" />
                    <span className="min-w-0 flex-1 truncate text-xs text-ink-muted">
                      {c.channel_proxy}
                    </span>
                  </div>
                )}
              </div>

              {/* 底部：统计 + 操作 */}
              <div className="flex items-center justify-between gap-2">
                <div className="flex items-center gap-3 text-xs text-ink-muted">
                  <span className="num">
                    RPM{" "}
                    {c.rate_limit_rpm > 0
                      ? formatNumber(c.rate_limit_rpm)
                      : "∞"}
                  </span>
                  <span className="num">
                    并发 {c.max_concurrent > 0 ? c.max_concurrent : "∞"}
                  </span>
                  <input
                    type="number"
                    step="1"
                    className="no-spin h-6 w-16 rounded-control border border-border bg-card px-1.5 text-right text-xs text-ink"
                    value={sortDraft[c.id] ?? String(c.sort ?? 0)}
                    disabled={
                      sortMut.isPending && sortMut.variables?.id === c.id
                    }
                    onClick={(e) => e.stopPropagation()}
                    onChange={(e) =>
                      setSortDraft((d) => ({ ...d, [c.id]: e.target.value }))
                    }
                    onBlur={(e) => commitSort(c, e.target.value)}
                    onKeyDown={(e) => {
                      e.stopPropagation();
                      if (e.key === "Enter") {
                        e.preventDefault();
                        (e.target as HTMLInputElement).blur();
                      }
                    }}
                    title="优先级：越小越靠前"
                    aria-label={`优先级 ${c.name}`}
                  />
                </div>
                <div
                  onClick={(e) => e.stopPropagation()}
                  className="flex items-center gap-0.5"
                >
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-7 w-7 p-0"
                    loading={testingId === c.id}
                    onClick={() => testMut.mutate(c.id)}
                    title="测试"
                    aria-label={`测试 ${c.name}`}
                  >
                    <FlaskConical className="h-3.5 w-3.5" aria-hidden />
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-7 w-7 p-0"
                    onClick={() => setEditing(c)}
                    title="编辑"
                    aria-label={`编辑 ${c.name}`}
                  >
                    <Pencil className="h-3.5 w-3.5" aria-hidden />
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-7 w-7 p-0"
                    loading={
                      copyMut.isPending &&
                      copyMut.variables?.sourceId === c.id
                    }
                    onClick={() => onCopy(c)}
                    title="复制"
                    aria-label={`复制 ${c.name}`}
                  >
                    <Copy className="h-3.5 w-3.5" aria-hidden />
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-7 w-7 p-0 text-destructive hover:bg-destructive/10"
                    onClick={() => setPendingDelete(c)}
                    title="删除"
                    aria-label={`删除 ${c.name}`}
                  >
                    <Trash2 className="h-3.5 w-3.5" aria-hidden />
                  </Button>
                </div>
              </div>
            </article>
          ))}
        </div>
      )}

      {/* 编辑 Sheet */}
      <ChannelEditor
        channel={editing}
        onClose={() => setEditing(null)}
        onSaved={() => {
          setEditing(null);
          qc.invalidateQueries({ queryKey: ["channels"] });
        }}
      />

      {/* 导出确认：明文 Key 与 base_url，写入用户设备前明确告知 */}
      <Dialog
        open={confirmExport}
        onOpenChange={(o) => !o && setConfirmExport(false)}
      >
        <DialogContent variant="dialog" size="sm">
          <DialogHeader>
            <DialogTitle>导出全部渠道</DialogTitle>
            <DialogDescription>
              导出文件包含每个渠道的上游地址、模型清单和所有 Key
              明文。请确认你只在安全环境下保存此文件，并避免在公网链路分享。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <DialogClose asChild>
              <Button variant="ghost" size="sm">
                取消
              </Button>
            </DialogClose>
            <Button
              variant="destructive"
              size="sm"
              onClick={async () => {
                setConfirmExport(false);
                await onExport();
              }}
            >
              我已了解，继续导出
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 删除两段式确认 */}
      <Dialog
        open={!!pendingDelete}
        onOpenChange={(o) => !o && setPendingDelete(null)}
      >
        <DialogContent variant="dialog" size="sm">
          <DialogHeader>
            <DialogTitle>删除渠道</DialogTitle>
            <DialogDescription>
              渠道 <span className="mono text-ink">{pendingDelete?.name}</span>{" "}
              删除后无法恢复，引用此渠道的分组可能失效。
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
              onConfirm={() =>
                pendingDelete && deleteMut.mutate(pendingDelete.id)
              }
              loading={deleteMut.isPending}
            />
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

// ---------------- 通用两段式确认按钮 ----------------

// ConfirmButton 已提取到 components/ui/confirm-button.tsx，本页直接复用。

// ---------------- 通用 Field ----------------

