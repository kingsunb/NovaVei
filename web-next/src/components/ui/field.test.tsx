import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { Field } from "./field";

describe("<Field />", () => {
  it("渲染 label + children", () => {
    render(
      <Field label="用户名">
        <input />
      </Field>,
    );
    expect(screen.getByText("用户名")).toBeInTheDocument();
    expect(screen.getByRole("textbox")).toBeInTheDocument();
  });

  it("required=true → 显示 * 标记 + sr-only 必填", () => {
    const { container } = render(
      <Field label="密码" required>
        <input />
      </Field>,
    );
    expect(container.querySelector("span.text-destructive")).toHaveTextContent("*");
    // 读屏用文本「必填」播报，星号仅视觉（aria-hidden）
    expect(screen.getByText("必填")).toHaveClass("sr-only");
  });

  it("required=false → 不显示 *", () => {
    const { container } = render(
      <Field label="备注">
        <input />
      </Field>,
    );
    expect(container.querySelector("span.text-destructive")).toBeNull();
  });

  it("hint 显示（无 error 时）", () => {
    render(
      <Field label="邮箱" hint="用于接收通知">
        <input />
      </Field>,
    );
    expect(screen.getByText("用于接收通知")).toBeInTheDocument();
  });

  it("error 显示时覆盖 hint + role=alert", () => {
    render(
      <Field label="邮箱" hint="用于接收通知" error="格式不正确">
        <input />
      </Field>,
    );
    expect(screen.queryByText("用于接收通知")).toBeNull();
    const err = screen.getByText("格式不正确");
    expect(err).toHaveAttribute("role", "alert");
  });

  it("无 hint 无 error → 下方无提示", () => {
    const { container } = render(
      <Field label="x">
        <input />
      </Field>,
    );
    // 唯一 label 文本就是 "x"
    expect(container.querySelectorAll("p")).toHaveLength(0);
  });
});
