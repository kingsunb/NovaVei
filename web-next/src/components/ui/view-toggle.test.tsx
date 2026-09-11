import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ViewToggle } from "./view-toggle";

describe("<ViewToggle />", () => {
  it("渲染网格和列表两个按钮", () => {
    render(<ViewToggle value="grid" onChange={() => {}} />);
    expect(screen.getByRole("button", { name: "网格" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "列表" })).toBeInTheDocument();
  });

  it("点击网格按钮调用 onChange('grid')", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<ViewToggle value="list" onChange={onChange} />);
    await user.click(screen.getByRole("button", { name: "网格" }));
    expect(onChange).toHaveBeenCalledWith("grid");
  });

  it("点击列表按钮调用 onChange('list')", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<ViewToggle value="grid" onChange={onChange} />);
    await user.click(screen.getByRole("button", { name: "列表" }));
    expect(onChange).toHaveBeenCalledWith("list");
  });

  it("自定义 aria-label/title", () => {
    render(
      <ViewToggle
        value="grid"
        onChange={() => {}}
        gridLabel="卡片"
        listLabel="表格"
      />,
    );
    expect(screen.getByRole("button", { name: "卡片" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "表格" })).toBeInTheDocument();
  });
});
