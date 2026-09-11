import { describe, expect, it, beforeEach } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { useSidebar } from "./useSidebar";

const KEY = "nv-sidebar-collapsed";

describe("useSidebar", () => {
  beforeEach(() => {
    localStorage.removeItem(KEY);
  });

  it("默认未折叠", () => {
    const { result } = renderHook(() => useSidebar());
    expect(result.current.collapsed).toBe(false);
  });

  it("从 localStorage 读取初始状态", () => {
    localStorage.setItem(KEY, "1");
    const { result } = renderHook(() => useSidebar());
    expect(result.current.collapsed).toBe(true);
  });

  it("toggle 翻转状态并写 localStorage", () => {
    const { result } = renderHook(() => useSidebar());
    act(() => result.current.toggle());
    expect(result.current.collapsed).toBe(true);
    expect(localStorage.getItem(KEY)).toBe("1");
    act(() => result.current.toggle());
    expect(result.current.collapsed).toBe(false);
    expect(localStorage.getItem(KEY)).toBe("0");
  });

  it("跨标签页 storage 事件同步", () => {
    const { result } = renderHook(() => useSidebar());
    act(() => {
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: KEY,
          newValue: "1",
        }),
      );
    });
    expect(result.current.collapsed).toBe(true);
  });

  it("无关 storage 事件不触发更新", () => {
    const { result } = renderHook(() => useSidebar());
    const before = result.current.collapsed;
    act(() => {
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: "other-key",
          newValue: "1",
        }),
      );
    });
    expect(result.current.collapsed).toBe(before);
  });
});
