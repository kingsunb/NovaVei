import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { Plus, Trash2 } from "lucide-react";
import { Icon } from "./icon";

describe("<Icon />", () => {
  it("默认 decorative=true → aria-hidden=true", () => {
    render(<Icon as={Plus} className="h-4 w-4" />);
    const svg = document.querySelector("svg")!;
    expect(svg).toHaveAttribute("aria-hidden", "true");
  });

  it("decorative=false → 不带 aria-hidden（功能性图标）", () => {
    render(
      <span>
        <Icon as={Trash2} className="h-4 w-4" decorative={false} />
        <span>删除</span>
      </span>,
    );
    const svg = document.querySelector("svg")!;
    expect(svg).not.toHaveAttribute("aria-hidden");
  });

  it("className 透传", () => {
    render(<Icon as={Plus} className="h-6 w-6 text-red-500" />);
    const svg = document.querySelector("svg")!;
    expect(svg).toHaveClass("h-6");
    expect(svg).toHaveClass("w-6");
    expect(svg).toHaveClass("text-red-500");
  });

  it("渲染 svg 节点", () => {
    const { container } = render(<Icon as={Plus} className="h-4 w-4" />);
    expect(container.querySelector("svg")).toBeInTheDocument();
  });
});
