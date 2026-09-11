import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { FormattedBody } from "./formatted-body";

describe("<FormattedBody />", () => {
  let writeTextSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    writeTextSpy = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue(undefined);
  });
  afterEach(() => writeTextSpy.mockRestore());

  it("loading 时显示加载中", () => {
    render(<FormattedBody content="" loading />);
    expect(screen.getByText("加载中…")).toBeInTheDocument();
  });

  it("空内容显示（空）", () => {
    render(<FormattedBody content="" />);
    expect(screen.getByText("（空）")).toBeInTheDocument();
  });

  it("JSON 内容格式化展示", () => {
    const json = '{"b":2,"a":1}';
    render(<FormattedBody content={json} />);
    expect(screen.getByText("JSON")).toBeInTheDocument();
    expect(screen.getByText(/"b": 2/)).toBeInTheDocument();
    expect(screen.getByText(/"a": 1/)).toBeInTheDocument();
  });

  it("SSE 内容格式化展示", () => {
    const sse = 'data: {"choices":[]}\n\ndata: [DONE]';
    render(<FormattedBody content={sse} />);
    expect(screen.getByText("SSE")).toBeInTheDocument();
  });

  it("纯文本保持原样", () => {
    render(<FormattedBody content="hello world" />);
    expect(screen.getByText("Text")).toBeInTheDocument();
    expect(screen.getByText("hello world")).toBeInTheDocument();
  });

  it("显示字节大小", () => {
    render(<FormattedBody content="hello world" />);
    expect(screen.getByText(/· .*/)).toBeInTheDocument();
  });

  it("点击复制调用 clipboard.writeText", async () => {
    render(<FormattedBody content='{"a":1}' />);
    fireEvent.click(screen.getByRole("button", { name: "复制" }));
    expect(writeTextSpy).toHaveBeenCalledWith('{"a":1}');
    expect(await screen.findByText("已复制")).toBeInTheDocument();
  });

  it("clipboard 不可用时静默忽略", async () => {
    writeTextSpy.mockRejectedValueOnce(new Error("denied"));
    render(<FormattedBody content='{"a":1}' />);
    fireEvent.click(screen.getByRole("button", { name: "复制" }));
    // 不抛异常、按钮仍显示"复制"（未进入 setCopied(true)）
    expect(screen.getByRole("button", { name: "复制" })).toBeInTheDocument();
  });
});
