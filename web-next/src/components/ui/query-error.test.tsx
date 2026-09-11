import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryErrorBanner } from "./query-error";

describe("<QueryErrorBanner />", () => {
  it("渲染错误提示与重试按钮", () => {
    render(<QueryErrorBanner onRetry={() => {}} />);
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expect(screen.getByText("加载失败，请检查网络后重试")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "重试" })).toBeInTheDocument();
  });

  it("点击重试调用 onRetry", async () => {
    const user = userEvent.setup();
    const onRetry = vi.fn();
    render(<QueryErrorBanner onRetry={onRetry} />);
    await user.click(screen.getByRole("button", { name: "重试" }));
    expect(onRetry).toHaveBeenCalledOnce();
  });
});
