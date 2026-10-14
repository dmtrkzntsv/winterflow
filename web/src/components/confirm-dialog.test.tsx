import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";

import { ConfirmDialog } from "@/components/confirm-dialog";

function Harness({ onConfirm }: { onConfirm: () => void | Promise<void> }) {
  const [open, setOpen] = useState(true);
  return (
    <>
      <p>{open ? "open" : "closed"}</p>
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title="Remove Bob?"
        description="Their account and tokens are deleted."
        confirmLabel="Remove"
        pendingLabel="Removing…"
        onConfirm={onConfirm}
      />
    </>
  );
}

describe("ConfirmDialog", () => {
  it("asks with a title, a body and the action's name", () => {
    render(<Harness onConfirm={vi.fn()} />);
    const dialog = screen.getByRole("alertdialog", { name: "Remove Bob?" });
    expect(dialog).toHaveTextContent("Their account and tokens are deleted.");
    expect(screen.getByRole("button", { name: "Remove" })).toHaveClass("bg-destructive");
  });

  it("closes on Cancel without acting", async () => {
    const onConfirm = vi.fn();
    render(<Harness onConfirm={onConfirm} />);
    await userEvent.setup().click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.getByText("closed")).toBeInTheDocument();
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("shows the action pending, then closes when it is done", async () => {
    let finish: () => void = () => {};
    const onConfirm = vi.fn(() => new Promise<void>((resolve) => (finish = resolve)));
    render(<Harness onConfirm={onConfirm} />);
    await userEvent.setup().click(screen.getByRole("button", { name: "Remove" }));
    expect(await screen.findByRole("button", { name: "Removing…" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
    finish();
    expect(await screen.findByText("closed")).toBeInTheDocument();
  });

  it("stays open to retry when the action fails", async () => {
    const onConfirm = vi.fn(() => Promise.reject(new Error("busy")));
    render(<Harness onConfirm={onConfirm} />);
    await userEvent.setup().click(screen.getByRole("button", { name: "Remove" }));
    expect(await screen.findByRole("button", { name: "Remove" })).toBeEnabled();
    expect(screen.getByText("open")).toBeInTheDocument();
  });
});
