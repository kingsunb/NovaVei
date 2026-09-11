import { describe, expect, it, vi } from "vitest";
import { loadFlags } from "@/lib/flags";

describe("flags console.warn on fetch failure", () => {
  it("当 runtime.json 拉取失败时输出 console.warn", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.stubGlobal("fetch", vi.fn(() => Promise.reject(new Error("boom"))));
    const f = await loadFlags({ force: true });
    expect(f["new-web"]).toBe(true); // 走 default
    expect(warn).toHaveBeenCalled();
    warn.mockRestore();
  });
});
