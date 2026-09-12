/**
 * 旧前端资源的运行时恢复。服务端更新后，浏览器里长开的旧页面会遇到两类问题：
 *  1. 懒加载路由去取已被新构建删除的 chunk → 动态模块加载失败；
 *  2. 版本看门狗（lib/version-check）比对出前后端构建错位。
 * 两者的恢复手段都是整页刷新（index.html 由后端下发 no-cache，reload 必得新前端）。
 */

/** isDynamicChunkError 识别常见的 Vite/浏览器动态模块加载失败。 */
export function isDynamicChunkError(error: Error): boolean {
  return /Failed to fetch dynamically imported module|Importing a module script failed|ChunkLoadError|Loading chunk [\w-]+ failed|error loading dynamically imported module/i.test(
    error.message,
  );
}

/** 会话级标记：本次会话已因过期 chunk 自动刷新过一次。 */
const STALE_CHUNK_RELOAD_FLAG = "novaveil-stale-chunk-reloaded";

/**
 * shouldAutoReloadStaleChunk 判定是否允许为过期 chunk 自动刷新，允许则记下
 * 「已刷新」标记。带 sessionStorage 一次性保护：刷新后仍立刻 chunk 失败
 * （如反向代理缓存了旧 index.html）时不循环刷新，改由 ErrorBoundary 的
 * fallback 提供手动刷新入口。
 */
export function shouldAutoReloadStaleChunk(): boolean {
  let alreadyReloaded = false;
  try {
    alreadyReloaded = sessionStorage.getItem(STALE_CHUNK_RELOAD_FLAG) === "1";
  } catch {
    // sessionStorage 不可用（隐私模式等）时按未刷新过处理，仅失去循环保护
  }
  if (alreadyReloaded) return false;
  try {
    sessionStorage.setItem(STALE_CHUNK_RELOAD_FLAG, "1");
  } catch {
    // ignore
  }
  return true;
}

/** recoverFromStaleBuild 整页刷新以加载新前端；返回 true 表示本次触发了刷新。 */
export function recoverFromStaleBuild(): boolean {
  if (!shouldAutoReloadStaleChunk()) return false;
  window.location.reload();
  return true;
}

/**
 * clearStaleChunkReloadFlag 页面稳定运行后（App 挂载 15s 无异常）清除标记，
 * 恢复下一次服务端更新时的自动刷新资格。
 */
export function clearStaleChunkReloadFlag(): void {
  try {
    sessionStorage.removeItem(STALE_CHUNK_RELOAD_FLAG);
  } catch {
    // ignore
  }
}
