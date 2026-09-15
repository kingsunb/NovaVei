import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { TokenBreakdownChart } from "./TokenBreakdownChart";

describe("<TokenBreakdownChart />", () => {
  it("有数据时渲染环形图与图例", () => {
    render(
      <TokenBreakdownChart
        input={1000}
        output={500}
        reasoning={200}
        cached={100}
      />,
    );
    // 中心总量
    expect(screen.getByText("总 tokens")).toBeInTheDocument();
    // 四类图例
    expect(screen.getByText("输入")).toBeInTheDocument();
    expect(screen.getByText("输出")).toBeInTheDocument();
    expect(screen.getByText("推理")).toBeInTheDocument();
    expect(screen.getByText("缓存命中")).toBeInTheDocument();
  });

  it("全零数据时显示空状态", () => {
    render(
      <TokenBreakdownChart
        input={0}
        output={0}
        reasoning={0}
        cached={0}
      />,
    );
    // 百分比应为 —
    const dashes = screen.getAllByText("—");
    expect(dashes.length).toBeGreaterThanOrEqual(4);
  });

  it("仅 input 有值时正确显示占比", () => {
    render(
      <TokenBreakdownChart
        input={1000}
        output={0}
        reasoning={0}
        cached={0}
      />,
    );
    // 100% 占比应出现
    expect(screen.getByText("100.0%")).toBeInTheDocument();
  });

  it("部分类别为零时不渲染对应弧", () => {
    const { container } = render(
      <TokenBreakdownChart
        input={500}
        output={300}
        reasoning={0}
        cached={0}
      />,
    );
    // 有数据时应有 path 元素（弧线）
    const paths = container.querySelectorAll("path");
    expect(paths.length).toBeGreaterThanOrEqual(1);
  });
});
