import { describe, expect, it, vi, afterEach } from "vitest";
import { act } from "@testing-library/react";
import { openSSE } from "./sse";

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  url: string;
  withCredentials = false;
  onopen: ((e: Event) => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onerror: ((e: Event) => void) | null = null;
  listeners: Record<string, ((e: Event) => void)[]> = {};
  readyState = 0;

  constructor(url: string, init?: { withCredentials?: boolean }) {
    this.url = url;
    this.withCredentials = !!init?.withCredentials;
    FakeEventSource.instances.push(this);
  }
  addEventListener(name: string, fn: (e: Event) => void) {
    (this.listeners[name] ||= []).push(fn);
  }
  close() {
    this.readyState = 2;
  }
  // test helpers
  fireOpen() {
    this.onopen?.(new Event("open"));
  }
  fireMessage(data: unknown) {
    this.onmessage?.(new MessageEvent("message", { data: JSON.stringify(data) }));
  }
  fireError() {
    this.onerror?.(new Event("error"));
  }
  fireEvent(name: string, data: unknown = { type: name }) {
    const event = new MessageEvent(name, {
      data: JSON.stringify(data),
    });
    this.listeners[name]?.forEach((fn) => fn(event));
  }
}

afterEach(() => {
  vi.restoreAllMocks();
  FakeEventSource.instances = [];
});

describe("openSSE", () => {
  it("建立连接时传 withCredentials=true", () => {
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
    openSSE("/api/v1/log/overview/stream", { onMessage: () => {} });
    const es = FakeEventSource.instances[0]!;
    expect(es.url).toBe("/api/v1/log/overview/stream");
    expect(es.withCredentials).toBe(true);
  });

  it("onMessage 解析 JSON 数据", () => {
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
    const fn = vi.fn();
    openSSE("/api/v1/x", { onMessage: fn });
    const es = FakeEventSource.instances[0]!;
    es.fireOpen();
    es.fireMessage({ id: 1, status: "running" });
    expect(fn).toHaveBeenCalledWith({ id: 1, status: "running" }, '{"id":1,"status":"running"}');
  });

  it("onError 触发后退避重连", async () => {
    vi.useFakeTimers();
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
    openSSE("/api/v1/x", { onMessage: () => {}, onError: vi.fn() });
    expect(FakeEventSource.instances).toHaveLength(1);
    FakeEventSource.instances[0]!.fireError();
    // 1s 后重连
    vi.advanceTimersByTime(1100);
    expect(FakeEventSource.instances.length).toBeGreaterThanOrEqual(2);
    vi.useRealTimers();
  });

  it("close 后不再重连", () => {
    vi.useFakeTimers();
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
    const handle = openSSE("/api/v1/x", { onMessage: () => {} });
    handle.close();
    FakeEventSource.instances[0]!.fireError();
    vi.advanceTimersByTime(5000);
    // 仍然只有 1 个
    expect(FakeEventSource.instances).toHaveLength(1);
    vi.useRealTimers();
  });

  it("指定 eventName 时走 addEventListener", () => {
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
    openSSE("/api/v1/x", { onMessage: () => {}, eventName: "update" });
    const es = FakeEventSource.instances[0]!;
    es.fireEvent("update");
    expect(es.listeners.update).toBeTruthy();
  });

  it("事件源 onerror 携带错误信息也能触发", () => {
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
    const onError = vi.fn();
    openSSE("/api/v1/x", { onMessage: () => {}, onError });
    const es = FakeEventSource.instances[0]!;
    act(() => {
      es.onerror?.(new Event("error"));
    });
    expect(onError).toHaveBeenCalledOnce();
  });
});

describe("openSSE 断连认证探活", () => {
  function statusResponse(status: number) {
    return new Response(
      JSON.stringify({
        code: status,
        message: status === 401 ? "unauthorized" : "success",
        data: null,
      }),
      { status, headers: { "content-type": "application/json" } },
    );
  }

  it("断连时探活 /user/status；401 借 http() 广播全局登出事件", async () => {
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
    const fetchMock = vi.fn((url: string) => {
      if (url.includes("/user/status")) {
        return Promise.resolve(statusResponse(401));
      }
      return Promise.resolve(statusResponse(200));
    });
    vi.stubGlobal("fetch", fetchMock);
    const handler = vi.fn();
    window.addEventListener("api:unauthorized", handler);
    try {
      openSSE("/api/v1/log/overview/stream", { onMessage: () => {} });
      FakeEventSource.instances[0]!.fireError();
      // 探活是发端于 onerror 的异步 fetch，链上有多个 await；轮询等待结果
      const probeSeen = () =>
        fetchMock.mock.calls.some((c) => String(c[0]).includes("/user/status"));
      for (let i = 0; i < 50 && !probeSeen(); i++) {
        await new Promise((r) => setTimeout(r, 10));
      }
      expect(probeSeen()).toBe(true);
      for (let i = 0; i < 50 && handler.mock.calls.length === 0; i++) {
        await new Promise((r) => setTimeout(r, 10));
      }
      expect(handler).toHaveBeenCalledTimes(1);
    } finally {
      window.removeEventListener("api:unauthorized", handler);
      vi.unstubAllGlobals();
    }
  });

  it("30s 内多次断连只探活一次（节流）", async () => {
    vi.useFakeTimers();
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
    const fetchMock = vi.fn((_url: string) =>
      Promise.resolve(statusResponse(200)),
    );
    vi.stubGlobal("fetch", fetchMock);
    try {
      openSSE("/api/v1/x", { onMessage: () => {} });
      FakeEventSource.instances[0]!.fireError();
      await vi.advanceTimersByTimeAsync(0);
      // 1s 退避后重连，再次断连：仍在 30s 节流窗口内
      await vi.advanceTimersByTimeAsync(1100);
      FakeEventSource.instances[1]!.fireError();
      await vi.advanceTimersByTimeAsync(0);
      const probes = fetchMock.mock.calls.filter((c) =>
        String(c[0]).includes("/user/status"),
      );
      expect(probes).toHaveLength(1);
    } finally {
      vi.useRealTimers();
      vi.unstubAllGlobals();
    }
  });

  it("非 JSON 数据仍回传原文", () => {
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
    const fn = vi.fn();
    openSSE("/api/v1/x", { onMessage: fn });
    const es = FakeEventSource.instances[0]!;
    es.fireOpen();
    // 直接触发 onmessage，绕过 fireMessage 的 JSON.stringify
    es.onmessage?.(new MessageEvent("message", { data: "plain text" }));
    expect(fn).toHaveBeenCalledWith("plain text", "plain text");
  });

  it("onMessage 回调抛错不破坏监听器", () => {
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
    const fn = vi.fn(() => {
      throw new Error("boom");
    });
    openSSE("/api/v1/x", { onMessage: fn });
    const es = FakeEventSource.instances[0]!;
    es.fireOpen();
    // 第一次抛错
    es.fireMessage({ a: 1 });
    expect(fn).toHaveBeenCalledTimes(1);
    // 第二次仍能收到消息
    es.fireMessage({ a: 2 });
    expect(fn).toHaveBeenCalledTimes(2);
  });

  it("error 后 close 清除重连定时器", () => {
    vi.useFakeTimers();
    vi.stubGlobal("EventSource", FakeEventSource as unknown as typeof EventSource);
    const handle = openSSE("/api/v1/x", { onMessage: () => {} });
    // 触发 error → 设置 timer
    FakeEventSource.instances[0]!.fireError();
    // close 时 timer 不为 null，走 clearTimeout 分支
    handle.close();
    vi.advanceTimersByTime(10_000);
    expect(FakeEventSource.instances).toHaveLength(1);
    vi.useRealTimers();
  });
});
