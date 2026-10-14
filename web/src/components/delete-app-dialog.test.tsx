import { useState } from "react";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { DeleteAppDialog } from "@/components/delete-app-dialog";
import type { DeleteChoice } from "@/lib/delete-app";

function Harness({ onConfirm }: { onConfirm: (c: DeleteChoice) => Promise<void> }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        open
      </button>
      <DeleteAppDialog name="Shop" open={open} onOpenChange={setOpen} onConfirm={onConfirm} />
    </>
  );
}

describe("DeleteAppDialog", () => {
  it("keeps volumes and backups unless ticked", async () => {
    const user = userEvent.setup({ delay: null });
    const onConfirm = vi.fn(async () => {});
    render(<Harness onConfirm={onConfirm} />);
    await user.click(screen.getByRole("button", { name: "open" }));
    expect(screen.getByRole("alertdialog", { name: "Delete Shop?" })).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "Also delete its volumes" })).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "Also delete its backups" })).not.toBeChecked();
    expect(screen.getAllByRole("checkbox").map((c) => c.id)).toEqual(["delete-volumes", "delete-backups"]);
    await user.click(screen.getByRole("button", { name: "Delete app" }));
    expect(onConfirm).toHaveBeenLastCalledWith({ withVolumes: false, withBackups: false });
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
  });

  it("names what it deletes once ticked, and starts unticked at every opening", async () => {
    const user = userEvent.setup({ delay: null });
    const onConfirm = vi.fn(async () => {});
    render(<Harness onConfirm={onConfirm} />);
    await user.click(screen.getByRole("button", { name: "open" }));
    await user.click(screen.getByRole("checkbox", { name: "Also delete its backups" }));
    expect(screen.getByRole("button", { name: "Delete app and backups" })).toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: "Also delete its volumes" }));
    await user.click(screen.getByRole("button", { name: "Delete app, volumes and backups" }));
    expect(onConfirm).toHaveBeenLastCalledWith({ withVolumes: true, withBackups: true });
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());

    await user.click(screen.getByRole("button", { name: "open" }));
    expect(screen.getByRole("checkbox", { name: "Also delete its volumes" })).not.toBeChecked();
    await user.click(screen.getByRole("checkbox", { name: "Also delete its volumes" }));
    await user.click(screen.getByRole("button", { name: "Delete app and volumes" }));
    expect(onConfirm).toHaveBeenLastCalledWith({ withVolumes: true, withBackups: false });
  });

  it("shows progress while the delete runs, and can't be sent again", async () => {
    const user = userEvent.setup({ delay: null });
    let finish: () => void = () => {};
    render(<Harness onConfirm={() => new Promise<void>((resolve) => (finish = resolve))} />);
    await user.click(screen.getByRole("button", { name: "open" }));
    await user.click(screen.getByRole("button", { name: "Delete app" }));
    expect(await screen.findByRole("button", { name: "Deleting Shop…" })).toBeDisabled();
    expect(screen.getByRole("status")).toHaveTextContent("This can take a few minutes");
    await act(async () => finish());
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
  });

  it("stays open after a failed delete, for a retry", async () => {
    const user = userEvent.setup({ delay: null });
    const onConfirm = vi.fn(async () => Promise.reject(new Error("timed out")));
    render(<Harness onConfirm={onConfirm} />);
    await user.click(screen.getByRole("button", { name: "open" }));
    await user.click(screen.getByRole("button", { name: "Delete app" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Delete app" })).toBeEnabled());
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });
});
