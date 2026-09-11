import { describe, expect, it } from "vitest";
import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Textarea } from "./input";

function Controlled() {
  const [v, setV] = useState("");
  return <Textarea value={v} onChange={(e) => setV(e.target.value)} />;
}

describe("<Textarea />", () => {
  it("渲染 textarea 元素", () => {
    render(<Textarea value="" onChange={() => {}} />);
    expect(screen.getByRole("textbox")).toBeInstanceOf(
      HTMLTextAreaElement,
    );
  });

  it("受控：onChange 触发且 value 同步", async () => {
    const user = userEvent.setup();
    render(<Controlled />);
    const ta = screen.getByRole("textbox");
    await user.type(ta, "hi");
    expect(ta).toHaveValue("hi");
  });
});
