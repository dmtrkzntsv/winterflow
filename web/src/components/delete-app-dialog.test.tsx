import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { DeleteAppDialog } from "@/components/delete-app-dialog";

describe("DeleteAppDialog", () => {
  it("keeps volumes and backups unless ticked", async () => {
    const user = userEvent.setup({ delay: null });
    const onConfirm = vi.fn();
    render(<DeleteAppDialog name="Shop" disabled={false} onConfirm={onConfirm} />);
    await user.click(screen.getByRole("button", { name: /Delete Shop/ }));
    const backups = screen.getByRole("checkbox", { name: "Also delete its backups" });
    expect(backups).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "Also delete its volumes" })).not.toBeChecked();
    // Under the volumes choice.
    expect(screen.getAllByRole("checkbox").map((c) => c.id)).toEqual(["delete-volumes", "delete-backups"]);
    await user.click(screen.getByRole("button", { name: "Delete app" }));
    expect(onConfirm).toHaveBeenLastCalledWith({ withVolumes: false, withBackups: false });
  });

  it("asks for the backups too once ticked", async () => {
    const user = userEvent.setup({ delay: null });
    const onConfirm = vi.fn();
    render(<DeleteAppDialog name="Shop" disabled={false} onConfirm={onConfirm} />);
    await user.click(screen.getByRole("button", { name: /Delete Shop/ }));
    await user.click(screen.getByRole("checkbox", { name: "Also delete its backups" }));
    expect(screen.getByRole("button", { name: "Delete app and backups" })).toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: "Also delete its volumes" }));
    await user.click(screen.getByRole("button", { name: "Delete app, volumes and backups" }));
    expect(onConfirm).toHaveBeenLastCalledWith({ withVolumes: true, withBackups: true });

    // A new opening starts unticked again.
    await user.click(screen.getByRole("button", { name: /Delete Shop/ }));
    await user.click(screen.getByRole("checkbox", { name: "Also delete its volumes" }));
    await user.click(screen.getByRole("button", { name: "Delete app and volumes" }));
    expect(onConfirm).toHaveBeenLastCalledWith({ withVolumes: true, withBackups: false });
  });

  it("shows progress while the delete runs, and can't be sent again", () => {
    render(<DeleteAppDialog name="Shop" disabled={false} deleting onConfirm={vi.fn()} />);
    expect(screen.getByRole("button", { name: /Deleting Shop/ })).toBeDisabled();
    expect(screen.getByRole("status")).toHaveTextContent("This can take a few minutes");
  });

  it("shows no progress when idle", () => {
    render(<DeleteAppDialog name="Shop" disabled={false} onConfirm={vi.fn()} />);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });
});
