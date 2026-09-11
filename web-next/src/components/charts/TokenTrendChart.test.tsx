import { describe, expect, it, vi, afterEach } from "vitest";
import { render, fireEvent } from "@testing-library/react";
import { TokenTrendChart } from "./TokenTrendChart";
import type { TokenTrendPoint } from "@/lib/types";

/**
 * jsdom 的 SVG getBoundingClientRect 恒为 0，无法触发 hover 命中计算；
 * 模拟真实 720×200 视口，让 onMouseMove 的 ratio→idx 计算走通。
 */
function mockSvgRect() {
  vi.spyOn(SVGElement.prototype, "getBoundingClientRect").mockReturnValue({
    left: 0,
    right: 720,
    top: 0,
    bottom: 200,
    width: 720,
    height: 200,
    x: 0,
    y: 0,
    toJSON: () => ({}),
  } as DOMRect);
}

/** 构造 range 档位的真实形状测试数据：点数与后端契约一致（24/28/30），t 单调递增。 */
function makePoints(count: number, stepMs: number, base = 1000): TokenTrendPoint[] {
  const end = Date.now();
  return Array.from({ length: count }).map((_, i) => ({
    t: end - (count - 1 - i) * stepMs,
    in: base + i * 10,
    out: base / 2 + i * 5,
  }));
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("<TokenTrendChart /> 真实数据渲染", () => {
  it("24h 渲染 24 点对应的两条折线", () => {
    const data = makePoints(24, 3600_000);
    const { container } = render(
      <TokenTrendChart range="24h" data={data} />,
    );
    const svg = container.querySelector("svg");
    expect(svg).toBeTruthy();
    const paths = container.querySelectorAll("path");
    expect(paths.length).toBe(2);
  });

  it("7d 与 30d 都能渲染折线与 X 轴标签", () => {
    const p7 = makePoints(28, 6 * 3600_000);
    const p30 = makePoints(30, 24 * 3600_000);
    const { container: c1 } = render(
      <TokenTrendChart range="7d" data={p7} />,
    );
    const { container: c2 } = render(
      <TokenTrendChart range="30d" data={p30} />,
    );
    expect(c1.querySelectorAll("path")).toHaveLength(2);
    expect(c2.querySelectorAll("path")).toHaveLength(2);
    // X 轴首/中/末三标签
    expect(c1.querySelectorAll("text").length).toBeGreaterThanOrEqual(8);
  });

  it("hover 命中：渲染指示圆点与 tooltip，mouseLeave 清除", () => {
    mockSvgRect();
    const data = makePoints(28, 6 * 3600_000);
    const { container } = render(
      <TokenTrendChart range="7d" data={data} />,
    );
    const svg = container.querySelector("svg");
    expect(svg).toBeTruthy();
    // 视口中点 → idx ≈ 数据中部，进入 hover 态
    fireEvent.mouseMove(svg!, { clientX: 360, clientY: 100 });
    // hover 指示：入/出两个圆点
    expect(container.querySelectorAll("circle")).toHaveLength(2);
    // tooltip：完整时间 + 入/出数值
    const tooltip = container.querySelector(
      '[data-testid="token-trend-tooltip"]',
    );
    expect(tooltip).not.toBeNull();
    expect(tooltip?.textContent).toContain("入");
    expect(tooltip?.textContent).toContain("出");
    expect(tooltip?.textContent).toMatch(/^\d{4}-\d{2}-\d{2}/);
    // 离开 svg → hover 清空，圆点与 tooltip 消失
    fireEvent.mouseLeave(svg!);
    expect(container.querySelectorAll("circle")).toHaveLength(0);
    expect(
      container.querySelector('[data-testid="token-trend-tooltip"]'),
    ).toBeNull();
  });

  it("24h range 的 tooltip 使用「月/日 时:00」完整格式", () => {
    mockSvgRect();
    const data = makePoints(24, 3600_000);
    const { container } = render(
      <TokenTrendChart range="24h" data={data} />,
    );
    const svg = container.querySelector("svg");
    // clientX=50 → viewBox x=50,rel=(50-36)/672≈0.02 → idx 0(首点)
    fireEvent.mouseMove(svg!, { clientX: 50, clientY: 100 });
    const tooltip = container.querySelector(
      '[data-testid="token-trend-tooltip"]',
    );
    expect(tooltip).not.toBeNull();
    expect(tooltip?.firstElementChild?.textContent).toMatch(
      /^\d{1,2}\/\d{1,2} \d{2}:00$/,
    );
    // tooltip 水平位置夹在容器内:首点不会 translateX 为负溢出
    const transform = (tooltip as HTMLElement).style.transform;
    expect(transform).toMatch(/translateX\((?:0|\d+(\.\d+)?)px\)/);
  });

  it("padding 区（y 轴标签侧）hover 不命中任何数据点", () => {
    mockSvgRect();
    const data = makePoints(24, 3600_000);
    const { container } = render(
      <TokenTrendChart range="24h" data={data} />,
    );
    const svg = container.querySelector("svg");
    // clientX=15 → viewBox x=15 < padL=36,落在 y 轴标签区,不选中首点
    fireEvent.mouseMove(svg!, { clientX: 15, clientY: 100 });
    expect(container.querySelectorAll("circle")).toHaveLength(0);
    expect(
      container.querySelector('[data-testid="token-trend-tooltip"]'),
    ).toBeNull();
  });

  it("data 变化后旧 hover 状态重置,不残留错位 tooltip", () => {
    mockSvgRect();
    const data = makePoints(28, 6 * 3600_000);
    const { container, rerender } = render(
      <TokenTrendChart range="7d" data={data} />,
    );
    const svg = container.querySelector("svg");
    fireEvent.mouseMove(svg!, { clientX: 360, clientY: 100 });
    expect(container.querySelectorAll("circle")).toHaveLength(2);
    // 切换数据（模拟 range 变更重取）→ hover 重置
    rerender(<TokenTrendChart range="24h" data={makePoints(24, 3600_000)} />);
    expect(container.querySelectorAll("circle")).toHaveLength(0);
    expect(
      container.querySelector('[data-testid="token-trend-tooltip"]'),
    ).toBeNull();
  });

  it("空数据渲染「暂无用量数据」占位且不画折线", () => {
    const { container } = render(
      <TokenTrendChart range="7d" data={[]} />,
    );
    expect(container.querySelector("svg")).toBeTruthy();
    expect(container.querySelectorAll("path")).toHaveLength(0);
    expect(container.textContent).toContain("暂无用量数据");
  });

  it("全零数据同样渲染「暂无用量数据」占位", () => {
    const zeros = makePoints(28, 6 * 3600_000).map(() => ({
      t: Date.now(),
      in: 0,
      out: 0,
    }));
    const { container } = render(
      <TokenTrendChart range="7d" data={zeros} />,
    );
    expect(container.querySelectorAll("path")).toHaveLength(0);
    expect(container.textContent).toContain("暂无用量数据");
  });

  it("mouseMove 越界（超出数据点范围）不进入 hover 态", () => {
    mockSvgRect();
    const data = makePoints(28, 6 * 3600_000);
    const { container } = render(
      <TokenTrendChart range="7d" data={data} />,
    );
    const svg = container.querySelector("svg");
    fireEvent.mouseMove(svg!, { clientX: 5000, clientY: 100 });
    expect(container.querySelectorAll("circle")).toHaveLength(0);
    expect(
      container.querySelector('[data-testid="token-trend-tooltip"]'),
    ).toBeNull();
  });
});
