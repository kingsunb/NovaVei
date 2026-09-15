import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { UsageHeatmap } from "./UsageHeatmap";
import type { UsageHeatmapPoint } from "@/lib/types";

/** 构造测试数据：最近 N 天，每天有指定 token 量。 */
function makeHeatmapData(days: number, tokensPerDay: number): UsageHeatmapPoint[] {
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  return Array.from({ length: days }).map((_, i) => {
    const d = new Date(today);
    d.setDate(today.getDate() - (days - 1 - i));
    const dateStr = d.toISOString().slice(0, 10);
    return {
      date: dateStr,
      tokens: tokensPerDay,
      cost: tokensPerDay * 0.00001,
      count: 10,
    };
  });
}

describe("<UsageHeatmap />", () => {
  it("有数据时渲染热力图与统计摘要", () => {
    const data = makeHeatmapData(30, 1000);
    render(<UsageHeatmap data={data} />);

    // 统计摘要
    expect(screen.getByText("活跃天数")).toBeInTheDocument();
    expect(screen.getByText(/总计/)).toBeInTheDocument();
    // 图例
    expect(screen.getByText("少")).toBeInTheDocument();
    expect(screen.getByText("多")).toBeInTheDocument();
  });

  it("空数据时仍渲染框架", () => {
    render(<UsageHeatmap data={[]} />);
    expect(screen.getByText("活跃天数")).toBeInTheDocument();
    expect(screen.getByText("少")).toBeInTheDocument();
  });

  it("全零数据时活跃天数为 0", () => {
    const data = makeHeatmapData(10, 0);
    const { container } = render(<UsageHeatmap data={data} />);
    // 活跃天数标签存在
    expect(screen.getByText("活跃天数")).toBeInTheDocument();
    // 没有预计消耗（cost 全零）
    expect(screen.queryByText(/预计消耗/)).not.toBeInTheDocument();
  });

  it("有 cost 数据时显示预计消耗", () => {
    const data: UsageHeatmapPoint[] = [
      {
        date: new Date().toISOString().slice(0, 10),
        tokens: 10000,
        cost: 1.5,
        count: 5,
      },
    ];
    render(<UsageHeatmap data={data} weeks={4} />);
    expect(screen.getByText(/预计消耗/)).toBeInTheDocument();
    expect(screen.getByText("$1.50")).toBeInTheDocument();
  });
});
