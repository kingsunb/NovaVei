/**
 * 运行时特性开关 —— P3 灰度替换的核心开关
 *
 * 设计：
 *  - 默认值来自构建期注入的 __flags/default.json（写死在镜像里）
 *  - 运行时从 /__flags/runtime.json 拉（部署时可挂载覆盖）
 *  - 同时支持 sessionStorage 本地覆盖（用户在 UI 调试时）
 *  - 拉取失败时降级到默认值，不阻塞应用
 *
 * 灰度决策：
 *  - 'new-web': boolean  — 全局是否启用新前端（kill switch）
 *  - 'rollout-percent': 0-100 — 灰度百分比（同一后端 5%→50%→100% 逐步放量）
 *  - 'sticky-bucket': boolean — 同一用户始终在同一边（按 user id 哈希）
 *
 * 不阻塞 SSR 启动；首屏可先用默认值渲染。
 */
const DEFAULTS_URL = "/__flags/default.json";
const RUNTIME_URL = "/__flags/runtime.json";
const LS_KEY = "nv-flags-override";

export interface Flags {
  /** 全局 kill switch；false 时整个应用渲染回滚提示页 */
  "new-web": boolean;
  /** 灰度百分比（0-100） */
  "rollout-percent": number;
  /** 同一用户始终走同一变体 */
  "sticky-bucket": boolean;
  /** A/B 模式：'new' 强制新前端，'old' 强制旧前端，'auto' 按百分比 */
  "ab-mode": "new" | "old" | "auto";
  /** 旧 web 入口路径（用于回退链接） */
  "legacy-path": string;
}

const DEFAULT_FLAGS: Flags = {
  "new-web": true,
  "rollout-percent": 100,
  "sticky-bucket": true,
  "ab-mode": "auto",
  "legacy-path": "/legacy",
};

let cache: { value: Flags; at: number } | null = null;
const TTL = 30_000;

/**
 * 清缓存（测试 / 运维强制刷新用）
 */
export function clearFlagsCache(): void {
  cache = null;
}

async function loadJson(url: string, signal?: AbortSignal): Promise<Partial<Flags> | null> {
  try {
    const res = await fetch(url, { cache: "no-store", signal });
    if (!res.ok) return null;
    return (await res.json()) as Partial<Flags>;
  } catch (err) {
    if (typeof console !== 'undefined') console.warn('[flags] loadJson failed:', err);
    return null;
  }
}

function readLocalOverride(): Partial<Flags> | null {
  try {
    const raw = localStorage.getItem(LS_KEY);
    if (!raw) return null;
    return JSON.parse(raw) as Partial<Flags>;
  } catch (err) {
    if (typeof console !== 'undefined') console.warn('[flags] loadJson failed:', err);
    return null;
  }
}

/** 用户 hash：稳定 0-100，用于 sticky-bucket */
export function userBucket(seed: string): number {
  let h = 0;
  for (let i = 0; i < seed.length; i++) {
    h = (h * 31 + seed.charCodeAt(i)) | 0;
  }
  return Math.abs(h) % 100;
}

/**
 * 匿名灰度桶（§3.4）：未登录用户首次计算时生成随机值并存入 sessionStorage，
 * 同一浏览器会话内保持一致，避免重渲染或无关状态更新时在新旧前端间反复翻转。
 *
 * - 仅在 `sticky-bucket` 开启且 userId 为空时使用；`sticky-bucket` 关闭时
 *   shouldUseNewWeb 仍走每次随机的既有语义，不在此静默改变灰度策略。
 * - sessionStorage 不可用时用模块级 ref 兜底，保证同一挂载周期内不重新随机。
 */
const ANON_BUCKET_KEY = "nv-anon-bucket";
let anonBucketFallback: string | null = null;

export function getAnonymousBucket(): string {
  try {
    const stored = sessionStorage.getItem(ANON_BUCKET_KEY);
    if (stored) return stored;
    const bucket = Math.random().toString();
    sessionStorage.setItem(ANON_BUCKET_KEY, bucket);
    return bucket;
  } catch {
    if (anonBucketFallback) return anonBucketFallback;
    anonBucketFallback = Math.random().toString();
    return anonBucketFallback;
  }
}

/** 清除匿名桶（测试 / 运维重置分桶用） */
export function clearAnonymousBucket(): void {
  anonBucketFallback = null;
  try {
    sessionStorage.removeItem(ANON_BUCKET_KEY);
  } catch {
    /* sessionStorage 不可用时无可清，静默忽略 */
  }
}

export function shouldUseNewWeb(flags: Flags, userId: string | null): boolean {
  if (!flags["new-web"]) return false;
  if (flags["ab-mode"] === "new") return true;
  if (flags["ab-mode"] === "old") return false;
  // auto
  let seed: string;
  if (flags["sticky-bucket"]) {
    // 粘性分桶：已登录用 userId；匿名用户用会话内稳定的匿名桶，
    // 避免同一会话重渲染时在新旧前端间翻转（§3.4）。
    seed = userId ?? getAnonymousBucket();
  } else {
    // 非粘性：保持既有"每次随机"语义，不静默改变灰度策略。
    seed = Math.random().toString();
  }
  return userBucket(seed) < flags["rollout-percent"];
}

/** 取最新 flags（带缓存） */
export async function loadFlags(opts?: { force?: boolean }): Promise<Flags> {
  if (!opts?.force && cache && Date.now() - cache.at < TTL) {
    return cache.value;
  }

  const [def, rt] = await Promise.all([
    loadJson(DEFAULTS_URL),
    loadJson(RUNTIME_URL),
  ]);
  const local = readLocalOverride();

  const merged: Flags = {
    ...DEFAULT_FLAGS,
    ...(def ?? {}),
    ...(rt ?? {}),
    ...(local ?? {}),
  };
  // 数值字段夹紧; typeof NaN === "number" 为 true, 必须额外拒绝 NaN, 防止
  // 后端误返 NaN 时让 rollout-percent 退化成无意义值, 把所有用户都锁在旧版。
  if (
    typeof merged["rollout-percent"] !== "number" ||
    Number.isNaN(merged["rollout-percent"])
  ) {
    merged["rollout-percent"] = DEFAULT_FLAGS["rollout-percent"];
  }
  merged["rollout-percent"] = Math.max(
    0,
    Math.min(100, merged["rollout-percent"]),
  );

  cache = { value: merged, at: Date.now() };
  return merged;
}

/** 用户本地覆盖（用于内部调试） */
export function setLocalOverride(partial: Partial<Flags>): void {
  try {
    const cur = readLocalOverride() ?? {};
    localStorage.setItem(LS_KEY, JSON.stringify({ ...cur, ...partial }));
    cache = null; // 强制 reload
  } catch (err) {
    if (typeof console !== 'undefined') console.warn('[flags] localStorage op failed:', err);
  }
}
