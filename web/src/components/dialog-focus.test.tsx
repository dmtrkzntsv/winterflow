import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { Checkbox } from "@/components/ui/checkbox";
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet";
import { stubViewport } from "@/test/media";

afterEach(() => vi.unstubAllGlobals());

describe("dialog focus hooks", () => {
  it("tells the caller when a dialog opens and closes", async () => {
    const onOpen = vi.fn();
    const onClose = vi.fn();
    const dialog = (open: boolean) => (
      <ResponsiveDialog open={open} onOpenChange={() => {}} title="Edit" onOpenAutoFocus={onOpen} onCloseAutoFocus={onClose}>
        <input aria-label="Field" />
      </ResponsiveDialog>
    );
    const { rerender } = render(dialog(true));
    expect(onOpen).toHaveBeenCalled();
    rerender(dialog(false));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it("tells the caller when the phone sheet opens", () => {
    stubViewport(375);
    const onOpen = vi.fn();
    render(
      <ResponsiveDialog open onOpenChange={() => {}} title="Edit" onOpenAutoFocus={onOpen}>
        <input aria-label="Field" />
      </ResponsiveDialog>,
    );
    expect(onOpen).toHaveBeenCalled();
  });

  it("lets a confirmation hand focus somewhere else once it closes", async () => {
    const user = userEvent.setup({ delay: null });
    const onClose = vi.fn((e: Event) => e.preventDefault());
    function Harness() {
      const [open, setOpen] = useState(true);
      return (
        <ConfirmDialog
          open={open}
          onOpenChange={setOpen}
          title="Remove it?"
          confirmLabel="Remove"
          onConfirm={() => {}}
          onCloseAutoFocus={onClose}
        />
      );
    }
    render(<Harness />);
    await user.click(screen.getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });
});

describe("dialog layout and first focus", () => {
  it("focuses the first choice, not the close button", () => {
    render(
      <ResponsiveDialog open onOpenChange={() => {}} title="New app" description="How do you want to start?">
        <ul>
          <li>
            <a href="/compose">Compose file</a>
          </li>
          <li>
            <a href="/git">Git repository</a>
          </li>
        </ul>
      </ResponsiveDialog>,
    );
    expect(screen.getByRole("link", { name: "Compose file" })).toHaveFocus();
  });

  it("skips what Tab skips, such as a radio group's unchecked choices", () => {
    render(
      <ResponsiveDialog open onOpenChange={() => {}} title="Add">
        <button type="button" tabIndex={-1}>
          Hidden from Tab
        </button>
        <input aria-label="Name" disabled />
        <input aria-label="Path" />
      </ResponsiveDialog>,
    );
    expect(screen.getByLabelText("Path")).toHaveFocus();
  });

  it("keeps the footer outside the scrolling body", () => {
    render(
      <ResponsiveDialog open onOpenChange={() => {}} title="Long" footer={<button type="button">Save</button>}>
        <input aria-label="Field" />
      </ResponsiveDialog>,
    );
    const body = screen.getByTestId("dialog-body");
    expect(body.className).toContain("overflow-y-auto");
    expect(body).toContainElement(screen.getByLabelText("Field"));
    expect(body).not.toContainElement(screen.getByRole("button", { name: "Save" }));
  });

  it("focuses a sheet's title", () => {
    render(
      <Sheet open>
        <SheetContent aria-describedby={undefined}>
          <SheetTitle>History</SheetTitle>
          <button type="button">Roll back</button>
        </SheetContent>
      </Sheet>,
    );
    expect(screen.getByRole("heading", { name: "History" })).toHaveFocus();
  });

  it("lets the caller place focus itself", () => {
    render(
      <ResponsiveDialog
        open
        onOpenChange={() => {}}
        title="Edit"
        onOpenAutoFocus={(e) => {
          e.preventDefault();
          (document.querySelector("[aria-label='Second']") as HTMLElement).focus();
        }}
      >
        <input aria-label="First" />
        <input aria-label="Second" />
      </ResponsiveDialog>,
    );
    expect(screen.getByLabelText("Second")).toHaveFocus();
  });

  it("draws checkboxes as rounded squares, not circles", () => {
    render(<Checkbox aria-label="Also delete its volumes" />);
    const box = screen.getByRole("checkbox", { name: "Also delete its volumes" });
    expect(box.className).toContain("rounded-[4px]");
    expect(box.className).not.toMatch(/rounded-(sm|full)\b/);
  });
});
