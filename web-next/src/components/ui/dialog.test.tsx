import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { Dialog, DialogContent } from "./dialog";

describe("<DialogContent> variants", () => {
  it("variant=sheet 渲染", () => {
    render(
      <Dialog open>
        <DialogContent variant="sheet" data-testid="dc">
          sheet content
        </DialogContent>
      </Dialog>,
    );
    expect(screen.getByText("sheet content")).toBeInTheDocument();
  });

  it("variant=fullscreen 渲染", () => {
    render(
      <Dialog open>
        <DialogContent variant="fullscreen" data-testid="dc">
          fullscreen content
        </DialogContent>
      </Dialog>,
    );
    expect(screen.getByText("fullscreen content")).toBeInTheDocument();
  });

  it("size=sm dialog 渲染", () => {
    render(
      <Dialog open>
        <DialogContent variant="dialog" size="sm">
          small dialog
        </DialogContent>
      </Dialog>,
    );
    expect(screen.getByText("small dialog")).toBeInTheDocument();
  });

  it("size=lg dialog 渲染", () => {
    render(
      <Dialog open>
        <DialogContent variant="dialog" size="lg">
          large dialog
        </DialogContent>
      </Dialog>,
    );
    expect(screen.getByText("large dialog")).toBeInTheDocument();
  });
});
