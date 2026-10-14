import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { ResponsiveDialog } from "@/components/responsive-dialog";
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
