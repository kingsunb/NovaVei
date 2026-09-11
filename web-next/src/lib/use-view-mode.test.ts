import { describe, it, expect, beforeEach } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useViewMode } from "./use-view-mode";

describe("useViewMode", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it("localStorage 为空时返回默认值", () => {
    const { result } = renderHook(() => useViewMode("test-key", "grid"));
    expect(result.current[0]).toBe("grid");
  });

  it("从 localStorage 读取已保存的值", () => {
    window.localStorage.setItem("test-key", "list");
    const { result } = renderHook(() => useViewMode("test-key", "grid"));
    expect(result.current[0]).toBe("list");
  });

  it("localStorage 值非法时回退默认值", () => {
    window.localStorage.setItem("test-key", "invalid");
    const { result } = renderHook(() => useViewMode("test-key", "grid"));
    expect(result.current[0]).toBe("grid");
  });

  it("change 写入 localStorage 并更新状态", () => {
    const { result } = renderHook(() => useViewMode("test-key", "grid"));
    expect(result.current[0]).toBe("grid");

    act(() => result.current[1]("list"));
    expect(result.current[0]).toBe("list");
    expect(window.localStorage.getItem("test-key")).toBe("list");
  });

  it("不同 storageKey 互不干扰", () => {
    window.localStorage.setItem("key-a", "list");
    const { result } = renderHook(() => useViewMode("key-b", "grid"));
    expect(result.current[0]).toBe("grid");
  });
});
