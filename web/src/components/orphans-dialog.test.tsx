import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { OrphansDialog } from "@/components/orphans-dialog";
import type { Backups } from "@/context/use-backups";
import type { Orphan } from "@/lib/backups";

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock("sonner", () => ({
  toast: { success: (...a: unknown[]) => toastSuccess(...a), error: (...a: unknown[]) => toastError(...a) },
}));

const now = Math.floor(Date.now() / 1000);
const ORPHANS: Orphan[] = [
  { app_id: "gone-1", app_name: "Old shop", deleted_at: now - 86_400, purge_at: now + 30 * 86_400, copies: 2, bytes: 2048, newest: now - 3600 },
  { app_id: "other-server", copies: 1, bytes: 500, newest: now - 600 },
  { app_id: "gone-2", app_name: "Blog", deleted_at: now - 86_400, purge_at: now - 60, copies: 0, bytes: 0 },
  { app_id: "gone-3", app_name: "Wiki", deleted_at: now - 86_400, copies: 1, bytes: 10 },
];

function fake(over: Partial<Backups> = {}): Backups {
  return {
    listOrphans: vi.fn().mockResolvedValue(ORPHANS),
    deleteOrphan: vi.fn().mockResolvedValue(6),
    ...over,
  } as unknown as Backups;
}

describe("OrphansDialog", () => {
  beforeEach(() => {
    toastSuccess.mockReset();
    toastError.mockReset();
  });

  it("lists each app's copies and when they go", async () => {
    render(<OrphansDialog backups={fake()} storage="disk" open onOpenChange={() => {}} />);
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByText("Old shop")).toBeInTheDocument();
    expect(within(dialog).getByText("other-server")).toBeInTheDocument();
    expect(within(dialog).getByText("Unknown app")).toBeInTheDocument();
    expect(within(dialog).getByText("Not deleted automatically (another server's?)")).toBeInTheDocument();
    expect(within(dialog).getByText("At the next daily sweep")).toBeInTheDocument();
    expect(within(dialog).getByText("Kept: no retention limit on this server")).toBeInTheDocument();
    expect(within(dialog).getByText(/^Deleted on /)).toBeInTheDocument();
    expect(within(dialog).getByText("2 · 2.0 kB")).toBeInTheDocument();
    expect(within(dialog).getByText("1 h ago")).toBeInTheDocument();
  });

  it("deletes an orphan after a confirmation", async () => {
    const user = userEvent.setup({ delay: null });
    const backups = fake();
    render(<OrphansDialog backups={backups} storage="disk" open onOpenChange={() => {}} />);
    await user.click(await screen.findByRole("button", { name: "Delete the backups of Old shop" }));
    const confirm = screen.getByRole("alertdialog");
    expect(confirm).toHaveTextContent("Delete every copy of Old shop (2 copies, 2.0 kB) from disk?");
    await user.click(within(confirm).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Delete the backups of other-server" }));
    expect(screen.getByRole("alertdialog")).toHaveTextContent("(1 copy, 500 B)");
    await user.click(screen.getByRole("button", { name: "Delete backups" }));
    await waitFor(() => expect(screen.queryByText("other-server")).not.toBeInTheDocument());
    expect(backups.deleteOrphan).toHaveBeenCalledWith("disk", "other-server");
    expect(toastSuccess).toHaveBeenCalledWith("Backups of other-server deleted", { description: "6 objects removed from disk." });
  });

  it("reports a refused delete and a failed list", async () => {
    const user = userEvent.setup({ delay: null });
    const backups = fake({ deleteOrphan: vi.fn().mockRejectedValue(new Error("the app Shop is on this server")) });
    render(<OrphansDialog backups={backups} storage="disk" open onOpenChange={() => {}} />);
    await user.click(await screen.findByRole("button", { name: "Delete the backups of Old shop" }));
    await user.click(screen.getByRole("button", { name: "Delete backups" }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith("Couldn't delete the backups", { description: "the app Shop is on this server" }));
    expect(screen.getByRole("button", { name: "Delete the backups of Old shop" })).toBeInTheDocument();

    render(<OrphansDialog backups={fake({ listOrphans: vi.fn().mockRejectedValue(new Error("the storage did not answer")) })} storage="s3" open onOpenChange={() => {}} />);
    expect(await screen.findByText("the storage did not answer")).toBeInTheDocument();
  });

  it("says when there is nothing", async () => {
    render(<OrphansDialog backups={fake({ listOrphans: vi.fn().mockResolvedValue([]) })} storage="disk" open onOpenChange={() => {}} />);
    expect(await screen.findByText("Every backup in this storage belongs to an app on this server.")).toBeInTheDocument();
  });
});
