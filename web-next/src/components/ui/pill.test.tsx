import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { Pill } from "./pill";

describe("<Pill />", () => {
  it("渲染文本与圆点", () => {
    render(<Pill tone="success">enabled</Pill>);
    const pill = screen.getByText("enabled");
    expect(pill).toBeInTheDocument();
    expect(pill.parentElement?.querySelector(".dot")).toBeTruthy();
  });

  it("dot=false 时不渲染圆点", () => {
    render(
      <Pill tone="neutral" dot={false}>
        no dot
      </Pill>,
    );
    const el = screen.getByText("no dot");
    expect(el.querySelector(".dot")).toBeNull();
  });

  it("不同 tone 应用不同 class 前缀", () => {
    const { container, rerender } = render(<Pill tone="danger">x</Pill>);
    expect(container.firstChild).toHaveClass("bg-red-500/[0.08]");
    rerender(<Pill tone="success">x</Pill>);
    expect(container.firstChild).toHaveClass("bg-emerald-500/[0.08]");
    rerender(<Pill tone="warning">x</Pill>);
    expect(container.firstChild).toHaveClass("bg-amber-500/[0.08]");
  });
});
