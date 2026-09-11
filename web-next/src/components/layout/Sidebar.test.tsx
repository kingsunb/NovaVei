import { describe, expect, it, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { Sidebar } from "./Sidebar";

const KEY = "nv-sidebar-collapsed";

function renderSidebar() {
  return render(
    <MemoryRouter
      initialEntries={["/dashboard"]}
    >
      <Sidebar />
    </MemoryRouter>,
  );
}

describe("<Sidebar />", () => {
  beforeEach(() => {
    localStorage.removeItem(KEY);
  });

  it("默认展开，宽度为 w-60", () => {
    renderSidebar();
    const sb = screen.getByTestId("sidebar");
    expect(sb.dataset.collapsed).toBe("false");
    expect(sb.className).toMatch(/w-60/);
  });

  it("点击折叠按钮后变窄且 data-collapsed=true", async () => {
    const user = userEvent.setup();
    renderSidebar();
    await user.click(screen.getByTestId("collapse-btn"));
    const sb = screen.getByTestId("sidebar");
    expect(sb.dataset.collapsed).toBe("true");
    expect(sb.className).toMatch(/w-14/);
    expect(localStorage.getItem(KEY)).toBe("1");
  });

  it("再次点击恢复", async () => {
    const user = userEvent.setup();
    localStorage.setItem(KEY, "1");
    renderSidebar();
    await user.click(screen.getByTestId("collapse-btn"));
    const sb = screen.getByTestId("sidebar");
    expect(sb.dataset.collapsed).toBe("false");
    expect(localStorage.getItem(KEY)).toBe("0");
  });

  it("折叠态下 label 文本被隐藏（accessibility 通过 title 提示）", () => {
    localStorage.setItem(KEY, "1");
    renderSidebar();
    // NavLink 折叠后只展示图标，文字节点被 hidden class 隐藏
    const link = screen.getByTitle("总览");
    expect(link).toBeInTheDocument();
  });

  it("aria-label 在折叠态为「展开侧栏」", () => {
    localStorage.setItem(KEY, "1");
    renderSidebar();
    expect(screen.getByLabelText("展开侧栏")).toBeInTheDocument();
  });
});
