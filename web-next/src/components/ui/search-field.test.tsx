import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SearchField } from "./search-field";

describe("<SearchField />", () => {
  it("把输入传给 onChange", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <SearchField value="" onChange={onChange} placeholder="搜索渠道" />,
    );
    await user.type(screen.getByPlaceholderText("搜索渠道"), "gpt");
    expect(onChange).toHaveBeenCalled();
    expect(onChange.mock.calls.map((c) => c[0]).join("")).toBe("gpt");
  });

  it("透传 aria-label", () => {
    render(
      <SearchField value="" onChange={() => {}} aria-label="搜索密钥" />,
    );
    expect(screen.getByRole("textbox", { name: "搜索密钥" })).toBeInTheDocument();
  });
});
