import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Eraser, Loader2, Plus, Send, Square, User, Bot } from "lucide-react";

import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { DEFAULT_GROUP_RELAY_CONFIG } from "@/lib/types";
import type { APIKeyCreated } from "@/lib/types";
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

// ============================================================
// 类型
// ============================================================

interface ChatMessage {
  role: "user" | "assistant";
  content: string;
  /** 流式接收中 */
  streaming?: boolean;
  /** 本条消息的请求出错 */
  error?: string;
}

interface TargetSelection {
  kind: "group" | "channel";
  label: string;
  /** kind=group 时用 */
  groupName?: string;
  /** kind=channel 时用 */
  channelId?: number;
  /** kind=channel 时用 */
  channelModelId?: number;
}

// ============================================================
// 对话本地持久化
// ============================================================

const CHAT_STORAGE_PREFIX = "novaveil:chat:";

/** 为目标生成唯一的 localStorage 键。 */
function chatStorageKey(target: TargetSelection | null): string | null {
  if (!target) return null;
  return target.kind === "group"
    ? `${CHAT_STORAGE_PREFIX}group:${target.groupName}`
    : `${CHAT_STORAGE_PREFIX}channel:${target.channelId}:${target.channelModelId}`;
}

/** 从 localStorage 加载指定目标的对话消息。 */
function loadConversation(key: string | null): ChatMessage[] {
  if (!key) return [];
  try {
    const raw = localStorage.getItem(key);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as ChatMessage[];
    // 过滤掉流式中的消息（页面刷新时未完成的回复）
    return parsed.filter((m) => !m.streaming);
  } catch {
    return [];
  }
}

/** 将对话消息保存到 localStorage。 */
function saveConversation(key: string | null, messages: ChatMessage[]): void {
  if (!key) return;
  try {
    // 不保存流式中的消息
    const hasStreaming = messages.some((m) => m.streaming);
    if (hasStreaming) return;
    localStorage.setItem(key, JSON.stringify(messages));
  } catch {
    // 存储满或禁用时静默失败
  }
}

/** 删除指定目标的对话记录。 */
function clearConversation(key: string | null): void {
  if (!key) return;
  try {
    localStorage.removeItem(key);
  } catch {
    // ignore
  }
}

// ============================================================
// 流式请求工具
// ============================================================

/**
 * streamChatCompletion 向 /v1/chat/completions 发起流式请求, 逐 delta 回调.
 * 走完整 relay 管线 (APIKeyAuth → 路由 → 熔断 → 协议转换), 与下游客户端完全一致.
 */
async function streamChatCompletion(
  model: string,
  messages: { role: string; content: string }[],
  apiKey: string,
  onDelta: (delta: string) => void,
  signal?: AbortSignal,
): Promise<void> {
  const res = await fetch("/v1/chat/completions", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${apiKey}`,
    },
    body: JSON.stringify({ model, messages, stream: true }),
    signal,
  });

  if (!res.ok) {
    let msg = `请求失败 (${res.status})`;
    try {
      const text = await res.text();
      const parsed = JSON.parse(text) as { message?: string; error?: { message?: string } };
      if (parsed.message) msg = parsed.message;
      else if (parsed.error?.message) msg = parsed.error.message;
    } catch {
      // 保留默认错误
    }
    throw new Error(msg);
  }

  const contentType = res.headers.get("content-type") || "";

  // 非流式响应 (上游不支持 stream 或被转换): 直接取 choices[0].message.content
  if (!contentType.includes("text/event-stream")) {
    const data = (await res.json()) as {
      choices?: { message?: { content?: string } }[];
    };
    const content = data.choices?.[0]?.message?.content;
    if (content) onDelta(content);
    return;
  }

  // SSE 流式
  const reader = res.body!.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });

    const lines = buffer.split("\n");
    buffer = lines.pop() ?? "";

    for (const line of lines) {
      const trimmed = line.trim();
      if (!trimmed.startsWith("data:")) continue;
      const payload = trimmed.slice(5).trim();
      if (payload === "[DONE]") return;
      try {
        const parsed = JSON.parse(payload) as {
          choices?: { delta?: { content?: string } }[];
        };
        const delta = parsed.choices?.[0]?.delta?.content;
        if (delta) onDelta(delta);
      } catch {
        // 跳过畸形行
      }
    }
  }
}

// ============================================================
// 主组件
// ============================================================

export default function ChatPage() {
  const qc = useQueryClient();

  // --- 目标选择 ---
  const [target, setTarget] = useState<TargetSelection | null>(null);
  const [effectiveModel, setEffectiveModel] = useState("");
  const [preparingTarget, setPreparingTarget] = useState(false);
  const tempGroupRef = useRef<{ id: number; name: string } | null>(null);

  // --- 密钥选择 ---
  const [selectedKeyId, setSelectedKeyId] = useState<number | null>(null);
  const [keySecret, setKeySecret] = useState("");
  const [showKeyDialog, setShowKeyDialog] = useState(false);

  // --- 对话状态 ---
  const currentStorageKey = chatStorageKey(target);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [input, setInput] = useState("");
  const [streaming, setStreaming] = useState(false);
  const abortRef = useRef<AbortController | null>(null);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const prevStorageKeyRef = useRef<string | null>(null);
  // refs 供切换 effect 读取最新值而不触发重渲染
  const messagesRef = useRef(messages);
  messagesRef.current = messages;
  const streamingRef = useRef(streaming);
  streamingRef.current = streaming;

  // 切换目标时：保存旧对话，加载新对话
  useEffect(() => {
    const prevKey = prevStorageKeyRef.current;
    const newKey = currentStorageKey;

    if (prevKey !== newKey) {
      // 保存旧目标的对话（非流式状态才保存）
      if (prevKey && !streamingRef.current) {
        saveConversation(prevKey, messagesRef.current);
      }
      // 加载新目标的对话
      setMessages(loadConversation(newKey));
      prevStorageKeyRef.current = newKey;
    }
  }, [currentStorageKey]);

  // --- 数据查询 ---
  const { data: groups } = useQuery({ queryKey: ["groups"], queryFn: api.listGroups });
  const { data: channels } = useQuery({ queryKey: ["channels"], queryFn: api.listChannels });
  const { data: keys } = useQuery({ queryKey: ["keys"], queryFn: api.listKeys });

  // 构建目标下拉选项
  const { groupOptions, channelOptions } = useMemo(() => {
    const groupOptions = (groups ?? []).map((g) => ({
      value: `group:${g.name}`,
      label: g.name,
    }));
    const channelOptions = (channels ?? [])
      .filter((ch) => ch.enabled)
      .flatMap((ch) =>
        (ch.models ?? []).map((m) => ({
          value: `channel:${ch.id}:${m.id}`,
          label: `${ch.name} / ${m.name}`,
          channelModelId: m.id,
        })),
      );
    return { groupOptions, channelOptions };
  }, [groups, channels]);

  const enabledKeys = useMemo(
    () => (keys ?? []).filter((k) => k.enabled),
    [keys],
  );

  const hasTarget = !!target;
  const canSend = !!input.trim() && !streaming && !!effectiveModel && !!keySecret && !preparingTarget;

  // --- 临时分组管理: 选渠道模型时自动建临时分组, 切换/卸载时删除 ---

  useEffect(() => {
    let aborted = false;

    async function prepare() {
      // 清理上一轮临时分组
      if (tempGroupRef.current) {
        const prev = tempGroupRef.current;
        tempGroupRef.current = null;
        try {
          await api.deleteGroup(prev.id);
          qc.invalidateQueries({ queryKey: ["groups"] });
        } catch {
          // 尽力清理
        }
      }

      if (!target) {
        setEffectiveModel("");
        return;
      }

      if (target.kind === "group") {
        setEffectiveModel(target.groupName!);
        return;
      }

      // 渠道模型: 创建临时分组, 加入该渠道模型作为唯一成员
      setPreparingTarget(true);
      setEffectiveModel("");
      try {
        const tempName = `__chat_${Date.now()}`;
        const group = await api.createGroup({
          name: tempName,
          mode: "failover",
          active_item_id: 0,
          relay_config: DEFAULT_GROUP_RELAY_CONFIG,
        });
        if (aborted) {
          void api.deleteGroup(group.id).catch(() => {});
          return;
        }
        await api.updateGroup({
          id: group.id,
          items_to_add: [
            {
              channel_model_id: target.channelModelId!,
              ref_group_name: "",
              priority: 1,
            },
          ],
        });
        if (aborted) {
          void api.deleteGroup(group.id).catch(() => {});
          return;
        }
        tempGroupRef.current = { id: group.id, name: tempName };
        setEffectiveModel(tempName);
        qc.invalidateQueries({ queryKey: ["groups"] });
      } catch (e) {
        if (!aborted) {
          toast.error("创建临时分组失败: " + (e as Error).message);
          setTarget(null);
        }
      } finally {
        if (!aborted) setPreparingTarget(false);
      }
    }

    void prepare();
    return () => { aborted = true; };
  }, [target, qc]);

  // 卸载时清理临时分组 + 中断在途请求
  useEffect(() => {
    return () => {
      if (tempGroupRef.current) {
        void api.deleteGroup(tempGroupRef.current.id).catch(() => {});
      }
      abortRef.current?.abort();
    };
  }, []);

  // --- 密钥明文获取 ---
  useEffect(() => {
    if (!selectedKeyId) {
      setKeySecret("");
      return;
    }
    let aborted = false;
    api
      .getAPIKeySecret(selectedKeyId)
      .then((secret) => {
        if (!aborted) setKeySecret(secret);
      })
      .catch(() => {
        if (!aborted) {
          toast.error("获取密钥明文失败");
          setKeySecret("");
        }
      });
    return () => { aborted = true; };
  }, [selectedKeyId]);

  // 自动选中第一把可用密钥
  useEffect(() => {
    if (!selectedKeyId && enabledKeys.length > 0) {
      setSelectedKeyId(enabledKeys[0].id);
    }
  }, [enabledKeys, selectedKeyId]);

  // 自动滚动到底部
  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: "smooth" });
  }, [messages]);

  // 对话自动保存：非流式状态下将消息持久化到 localStorage
  useEffect(() => {
    if (!streaming) {
      saveConversation(currentStorageKey, messages);
    }
  }, [currentStorageKey, messages, streaming]);

  // --- 目标切换 ---
  const handleTargetChange = useCallback(
    (value: string) => {
      if (!value) {
        setTarget(null);
        return;
      }
      if (value.startsWith("group:")) {
        const name = value.slice(6);
        setTarget({ kind: "group", label: name, groupName: name });
      } else if (value.startsWith("channel:")) {
        const parts = value.split(":");
        const channelId = Number(parts[1]);
        const channelModelId = Number(parts[2]);
        const label = channelOptions.find((o) => o.channelModelId === channelModelId)?.label ?? "";
        setTarget({ kind: "channel", label, channelId, channelModelId });
      }
    },
    [channelOptions],
  );

  // --- 发送消息 ---
  const handleSend = useCallback(async () => {
    const text = input.trim();
    if (!text || !effectiveModel || !keySecret || streaming || preparingTarget) return;

    const userMsg: ChatMessage = { role: "user", content: text };
    const assistantMsg: ChatMessage = { role: "assistant", content: "", streaming: true };
    const history = [...messages, userMsg];
    setMessages([...history, assistantMsg]);
    setInput("");
    setStreaming(true);

    const controller = new AbortController();
    abortRef.current = controller;

    try {
      await streamChatCompletion(
        effectiveModel,
        history.map((m) => ({ role: m.role, content: m.content })),
        keySecret,
        (delta) => {
          setMessages((prev) => {
            const next = [...prev];
            const last = next[next.length - 1];
            if (last?.role === "assistant") {
              next[next.length - 1] = { ...last, content: last.content + delta };
            }
            return next;
          });
        },
        controller.signal,
      );
      setMessages((prev) => {
        const next = [...prev];
        const last = next[next.length - 1];
        if (last?.role === "assistant") {
          next[next.length - 1] = { ...last, streaming: false };
        }
        return next;
      });
    } catch (e) {
      if (controller.signal.aborted) return;
      const msg = (e as Error).message;
      setMessages((prev) => {
        const next = [...prev];
        const last = next[next.length - 1];
        if (last?.role === "assistant") {
          if (last.content === "") {
            next.pop();
          } else {
            next[next.length - 1] = { ...last, streaming: false };
          }
        }
        return [...next, { role: "assistant", content: "", error: msg }];
      });
    } finally {
      setStreaming(false);
      abortRef.current = null;
    }
  }, [input, effectiveModel, keySecret, streaming, preparingTarget, messages]);

  // --- 停止生成 ---
  const handleStop = useCallback(() => {
    abortRef.current?.abort();
    setStreaming(false);
    setMessages((prev) => {
      const next = [...prev];
      const last = next[next.length - 1];
      if (last?.role === "assistant" && last.streaming) {
        next[next.length - 1] = { ...last, streaming: false };
      }
      return next;
    });
  }, []);

  // --- 清空对话（含 localStorage） ---
  const handleClear = useCallback(() => {
    setMessages([]);
    clearConversation(currentStorageKey);
  }, [currentStorageKey]);

  // --- 密钥创建回调 ---
  const handleKeyCreated = useCallback((created: APIKeyCreated) => {
    setShowKeyDialog(false);
    setSelectedKeyId(created.id);
    setKeySecret(created.api_key);
    qc.invalidateQueries({ queryKey: ["keys"] });
  }, [qc]);

  // 目标下拉当前值
  const targetValue = target
    ? target.kind === "group"
      ? `group:${target.groupName}`
      : `channel:${target.channelId}:${target.channelModelId}`
    : "";

  const noTargets =
    groupOptions.length === 0 && channelOptions.length === 0;

  return (
    <div className="flex h-full flex-col gap-3">
      {/* 头部: 目标 + 密钥选择 */}
      <Card className="shrink-0 p-3">
        <div className="flex flex-wrap items-center gap-3">
          {/* 目标选择 */}
          <div className="flex min-w-0 items-center gap-2">
            <label className="shrink-0 text-xs font-medium text-ink-muted">目标</label>
            <select
              aria-label="选择对话目标"
              className="h-8 min-w-48 flex-1 rounded-control border border-border bg-card px-2 text-sm"
              value={targetValue}
              onChange={(e) => handleTargetChange(e.target.value)}
            >
              <option value="">选择目标…</option>
              {groupOptions.length > 0 && (
                <optgroup label="分组">
                  {groupOptions.map((o) => (
                    <option key={o.value} value={o.value}>
                      {o.label}
                    </option>
                  ))}
                </optgroup>
              )}
              {channelOptions.length > 0 && (
                <optgroup label="渠道">
                  {channelOptions.map((o) => (
                    <option key={o.value} value={o.value}>
                      {o.label}
                    </option>
                  ))}
                </optgroup>
              )}
            </select>
            {preparingTarget && (
              <Loader2 className="h-3.5 w-3.5 animate-spin text-ink-muted" aria-hidden />
            )}
          </div>

          {/* 密钥选择 */}
          <div className="flex min-w-0 items-center gap-2">
            <label className="shrink-0 text-xs font-medium text-ink-muted">密钥</label>
            {enabledKeys.length > 0 ? (
              <select
                aria-label="选择 API 密钥"
                className="h-8 min-w-40 flex-1 rounded-control border border-border bg-card px-2 text-sm"
                value={selectedKeyId ?? ""}
                onChange={(e) => setSelectedKeyId(e.target.value ? Number(e.target.value) : null)}
              >
                <option value="">选择密钥…</option>
                {enabledKeys.map((k) => (
                  <option key={k.id} value={k.id}>
                    {k.name || "未命名"}
                  </option>
                ))}
              </select>
            ) : (
              <span className="text-xs text-ink-subtle">无可用密钥</span>
            )}
            <Button
              variant="secondary"
              size="sm"
              className="gap-1.5"
              onClick={() => setShowKeyDialog(true)}
            >
              <Plus className="h-3.5 w-3.5" aria-hidden />
              创建密钥
            </Button>
          </div>

          {/* 清空 */}
          {messages.length > 0 && (
            <Button
              variant="ghost"
              size="sm"
              className="gap-1.5 text-ink-muted"
              onClick={handleClear}
            >
              <Eraser className="h-3.5 w-3.5" aria-hidden />
              清空
            </Button>
          )}
        </div>
      </Card>

      {/* 消息区 */}
      <div
        ref={scrollRef}
        className="min-h-0 flex-1 overflow-y-auto"
      >
        {noTargets ? (
          <div className="flex h-full items-center justify-center text-sm text-ink-muted">
            没有可用的分组或渠道模型, 请先在「渠道」和「分组」页面创建。
          </div>
        ) : messages.length === 0 ? (
          <div className="flex h-full items-center justify-center text-sm text-ink-subtle">
            {hasTarget
              ? "输入消息开始对话"
              : "选择一个分组或渠道模型开始对话"}
          </div>
        ) : (
          <div className="space-y-3">
            {messages.map((msg, i) => (
              <MessageBubble key={i} msg={msg} />
            ))}
          </div>
        )}
      </div>

      {/* 输入区 */}
      <Card className="shrink-0 p-3">
        <div className="flex items-end gap-2">
          <textarea
            aria-label="消息输入"
            rows={3}
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
                e.preventDefault();
                void handleSend();
              }
            }}
            placeholder={
              !hasTarget
                ? "请先选择目标…"
                : !keySecret
                  ? "请先选择或创建密钥…"
                  : "输入消息, ⌘/Ctrl+Enter 发送…"
            }
            disabled={!hasTarget || !keySecret || streaming || preparingTarget}
            className={cn(
              "min-h-0 flex-1 resize-none rounded-control border border-border bg-card px-3 py-2 text-sm",
              "placeholder:text-ink-subtle focus:outline-none focus:ring-1 focus:ring-primary/30",
              "disabled:cursor-not-allowed disabled:opacity-50",
            )}
          />
          {streaming ? (
            <Button variant="secondary" size="sm" className="gap-1.5" onClick={handleStop}>
              <Square className="h-3.5 w-3.5" aria-hidden />
              停止
            </Button>
          ) : (
            <Button
              variant="primary"
              size="sm"
              className="gap-1.5"
              disabled={!canSend}
              onClick={() => void handleSend()}
            >
              <Send className="h-3.5 w-3.5" aria-hidden />
              发送
            </Button>
          )}
        </div>
      </Card>

      {/* 密钥创建对话框 */}
      <KeyCreateDialog open={showKeyDialog} onClose={() => setShowKeyDialog(false)} onCreated={handleKeyCreated} />
    </div>
  );
}

// ============================================================
// 消息气泡
// ============================================================

function MessageBubble({ msg }: { msg: ChatMessage }) {
  if (msg.error) {
    return (
      <div className="flex justify-center">
        <div className="max-w-[80%] rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          ⚠ {msg.error}
        </div>
      </div>
    );
  }

  const isUser = msg.role === "user";
  return (
    <div className={cn("flex gap-2", isUser ? "justify-end" : "justify-start")}>
      {!isUser && (
        <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-primary/10 text-primary">
          <Bot className="h-4 w-4" aria-hidden />
        </div>
      )}
      <div
        className={cn(
          "max-w-[75%] whitespace-pre-wrap break-words rounded-lg px-3 py-2 text-sm",
          isUser
            ? "bg-primary text-primary-foreground"
            : "border border-border bg-card text-ink",
        )}
      >
        {msg.content}
        {msg.streaming && (
          <span className="ml-1 inline-block h-3.5 w-1.5 animate-pulse bg-ink-muted/60 align-middle" />
        )}
      </div>
      {isUser && (
        <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-ink/10 text-ink-muted">
          <User className="h-4 w-4" aria-hidden />
        </div>
      )}
    </div>
  );
}

// ============================================================
// 密钥创建对话框 (简化版)
// ============================================================

function KeyCreateDialog({
  open,
  onClose,
  onCreated,
}: {
  open: boolean;
  onClose: () => void;
  onCreated: (created: APIKeyCreated) => void;
}) {
  const [name, setName] = useState("");
  const [keyValue, setKeyValue] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (open) {
      setName("");
      setKeyValue("");
    }
  }, [open]);

  const handleCreate = async () => {
    setLoading(true);
    try {
      const created = await api.createKey({
        name: name.trim() || `chat-${Date.now()}`,
        api_key: keyValue.trim(),
        enabled: true,
        expire_at: 0,
        supported_models: "",
        max_concurrent: 0,
        rate_limit_rpm: 0,
      });
      onCreated(created);
      toast.success("密钥创建成功");
    } catch (e) {
      toast.error("创建失败: " + (e as Error).message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent variant="dialog" size="sm">
        <DialogHeader>
          <DialogTitle>创建 API 密钥</DialogTitle>
          <DialogDescription>
            创建一把不限模型、永不过期的密钥用于对话页。出于安全考虑, 完整密钥仅在创建时返回。
          </DialogDescription>
        </DialogHeader>
        <DialogBody>
          <div className="space-y-3">
            <div className="space-y-2">
              <label className="text-xs font-medium text-ink-muted">名称 (可选)</label>
              <Input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="留空自动命名"
                className="h-8"
              />
            </div>
            <div className="space-y-2">
              <label className="text-xs font-medium text-ink-muted">密钥值 (可选)</label>
              <Input
                value={keyValue}
                onChange={(e) => setKeyValue(e.target.value)}
                placeholder="留空自动生成"
                className="h-8"
              />
            </div>
          </div>
        </DialogBody>
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="ghost" size="sm">
              取消
            </Button>
          </DialogClose>
          <Button
            variant="primary"
            size="sm"
            disabled={loading}
            onClick={() => void handleCreate()}
          >
            {loading ? "创建中…" : "创建"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
