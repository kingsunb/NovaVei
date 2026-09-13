import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";

import {
  clearStaleChunkReloadFlag,
  recoverFromStaleBuild,
  shouldAutoReloadStaleChunk,
} from "./app-recovery";

describe("recoverFromStaleBuild", () => {
  let reloadSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    sessionStorage.clear();
    reloadSpy = vi.spyOn(window.location, "reload").mockImplementation(() => {});
  });
  afterEach(() => reloadSpy.mockRestore());

  it("首次调用触发整页刷新并返回 true", () => {
    expect(recoverFromStaleBuild()).toBe(true);
    expect(reloadSpy).toHaveBeenCalledTimes(1);
  });

  it("同会话已刷新过则不再刷新，返回 false", () => {
    // 第一次：允许刷新
    expect(recoverFromStaleBuild()).toBe(true);
    // 第二次：sessionStorage 已标记，跳过
    expect(recoverFromStaleBuild()).toBe(false);
    expect(reloadSpy).toHaveBeenCalledTimes(1);
  });
});

describe("shouldAutoReloadStaleChunk sessionStorage 异常", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    sessionStorage.clear();
  });

  it("getItem 抛错时按未刷新过处理，仍允许刷新", () => {
    vi.spyOn(sessionStorage, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    const setItemSpy = vi
      .spyOn(sessionStorage, "setItem")
      .mockImplementation(() => {});
    expect(shouldAutoReloadStaleChunk()).toBe(true);
    expect(setItemSpy).toHaveBeenCalled();
  });

  it("setItem 抛错时静默忽略，仍返回 true", () => {
    vi.spyOn(sessionStorage, "setItem").mockImplementation(() => {
      throw new Error("quota");
    });
    expect(shouldAutoReloadStaleChunk()).toBe(true);
  });
});

describe("clearStaleChunkReloadFlag sessionStorage 异常", () => {
  afterEach(() => vi.restoreAllMocks());

  it("removeItem 抛错时静默忽略，不向外抛", () => {
    vi.spyOn(sessionStorage, "removeItem").mockImplementation(() => {
      throw new Error("denied");
    });
    expect(() => clearStaleChunkReloadFlag()).not.toThrow();
  });
});
