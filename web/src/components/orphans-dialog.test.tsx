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

// The dialog has two Close buttons: the corner X and the footer's.
const footerClose = () => screen.getAllByRole("button", { name: "Close" }).find((b) => !b.querySelector(".sr-only"))!;

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
    expect(within(dialog).getByText(/^Will be deleted on /)).toBeInTheDocument();
    expect(within(dialog).getByRole("columnheader", { name: "Automatic deletion" })).toBeInTheDocument();
    // On a phone the copies column is hidden: the app cell says it too.
    expect(within(dialog).getByText("2 copies · 2.0 kB · 1 hour ago")).toBeInTheDocument();
    expect(within(dialog).getByText("2 · 2.0 kB")).toBeInTheDocument();
    expect(within(dialog).getByText("1 hour ago")).toBeInTheDocument();
  });

  it("deletes an orphan after a confirmation", async () => {
    const user = userEvent.setup({ delay: null });
    const backups = fake();
    render(<OrphansDialog backups={backups} storage="disk" open onOpenChange={() => {}} />);
    await user.click(await screen.findByRole("button", { name: "Delete the backups of Old shop" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(confirm).toHaveTextContent("Delete every copy of Old shop (2 copies, 2.0 kB) from disk? This cannot be undone.");
    expect(confirm).not.toHaveTextContent("another server");
    await user.click(within(confirm).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    await user.click(screen.getByRole("button", { name: "Delete the backups of other-server" }));
    expect(await screen.findByRole("alertdialog")).toHaveTextContent("(1 copy, 500 B)");
    await user.click(screen.getByRole("button", { name: "Delete backups" }));
    await waitFor(() => expect(screen.queryByText("other-server")).not.toBeInTheDocument());
    expect(backups.deleteOrphan).toHaveBeenCalledWith("disk", "other-server");
    expect(toastSuccess).toHaveBeenCalledWith("Backups of other-server deleted", { description: "6 objects removed from disk." });
    // The row the focus was on is gone: it lands on the dialog's Close button.
    await waitFor(() => expect(footerClose()).toHaveFocus());
  });

  it("takes focus in the confirmation, and Escape closes only that", async () => {
    const user = userEvent.setup({ delay: null });
    render(<OrphansDialog backups={fake()} storage="disk" open onOpenChange={() => {}} />);
    const row = await screen.findByRole("button", { name: "Delete the backups of Old shop" });
    await user.click(row);
    const confirm = await screen.findByRole("alertdialog");
    expect(within(confirm).getByRole("button", { name: "Cancel" })).toHaveFocus();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Delete the backups of Old shop" })).toHaveFocus();
  });

  it("warns that a folder nobody marked as deleted may be another server's", async () => {
    const user = userEvent.setup({ delay: null });
    const backups = fake({ listOrphans: vi.fn().mockResolvedValue([{ app_id: "other-server", copies: 1, bytes: 500 }]) });
    render(<OrphansDialog backups={backups} storage="disk" open onOpenChange={() => {}} />);
    await user.click(await screen.findByRole("button", { name: "Delete the backups of other-server" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(confirm).toHaveTextContent("not marked as deleted by this server");
    expect(confirm).toHaveTextContent("live backups of an app on another server that shares this storage");
    expect(confirm).toHaveTextContent("is permanent");
    expect(confirm).toHaveTextContent("(1 copy, 500 B)");
  });

  it("reports a refused delete and a failed list", async () => {
    const user = userEvent.setup({ delay: null });
    const backups = fake({ deleteOrphan: vi.fn().mockRejectedValue(new Error("the app Shop is on this server")) });
    render(<OrphansDialog backups={backups} storage="disk" open onOpenChange={() => {}} />);
    await user.click(await screen.findByRole("button", { name: "Delete the backups of Old shop" }));
    await user.click(await screen.findByRole("button", { name: "Delete backups" }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith("Couldn't delete the backups", { description: "the app Shop is on this server" }));
    // The confirmation is gone and the row is still there to try again.
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Delete the backups of Old shop" })).toBeInTheDocument();
  });

  it("reports a failed list and tries again", async () => {
    const user = userEvent.setup({ delay: null });
    const listOrphans = vi.fn().mockRejectedValueOnce(new Error("the storage did not answer")).mockResolvedValue(ORPHANS);
    render(<OrphansDialog backups={fake({ listOrphans })} storage="s3" open onOpenChange={() => {}} />);
    expect(await screen.findByText("the storage did not answer")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Old shop")).toBeInTheDocument();
    expect(screen.queryByText("the storage did not answer")).not.toBeInTheDocument();
  });

  it("closes from its Close button, and says an object in the singular", async () => {
    const user = userEvent.setup({ delay: null });
    const onOpenChange = vi.fn();
    const backups = fake({ deleteOrphan: vi.fn().mockResolvedValue(1) });
    render(<OrphansDialog backups={backups} storage="disk" open onOpenChange={onOpenChange} />);
    await user.click(await screen.findByRole("button", { name: "Delete the backups of Old shop" }));
    await user.click(await screen.findByRole("button", { name: "Delete backups" }));
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith("Backups of Old shop deleted", { description: "1 object removed from disk." }));
    await user.click(footerClose());
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("says when there is nothing", async () => {
    render(<OrphansDialog backups={fake({ listOrphans: vi.fn().mockResolvedValue([]) })} storage="disk" open onOpenChange={() => {}} />);
    expect(await screen.findByText("Every backup in this storage belongs to an app on this server.")).toBeInTheDocument();
  });
});
