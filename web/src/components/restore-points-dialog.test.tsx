import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { RestorePointsDialog } from "@/components/restore-points-dialog";
import type { Backups } from "@/context/use-backups";
import { trackActivity } from "@/lib/backup-activity";
import type { EffectiveBackup, RestorePoint } from "@/lib/backups";

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock("sonner", () => ({
  toast: { success: (...a: unknown[]) => toastSuccess(...a), error: (...a: unknown[]) => toastError(...a) },
}));

const now = Math.floor(Date.now() / 1000);
const POINTS: RestorePoint[] = [
  { id: "p2", time: now - 600, kind: "manual", size: 2048, expires: now + 3 * 86_400 },
  { id: "p1", time: now - 86_400, kind: "pre-restore", size: 900 },
];
const database: EffectiveBackup = {
  name: "database",
  type: "sqlite",
  enabled: true,
  storage: "disk",
  every: "1h",
  volume: "data",
  path: "app.db",
  in_recipe: true,
  from_recipe: {},
};
const when = (p: RestorePoint) => new Date(p.time * 1000).toLocaleString();

function fake(over: Partial<Backups> = {}): Backups {
  return {
    listRestorePoints: vi.fn().mockResolvedValue(POINTS),
    restoreBackup: vi.fn().mockResolvedValue({ restored_point: "p2", pre_restore_point: "p3", warnings: ["/x: skipped"] }),
    deleteRestorePoint: vi.fn().mockResolvedValue(undefined),
    ...over,
  } as unknown as Backups;
}

describe("RestorePointsDialog", () => {
  beforeEach(() => {
    toastSuccess.mockReset();
    toastError.mockReset();
  });

  it("lists the copies with their kind, size and expiry", async () => {
    render(<RestorePointsDialog appId="a1" backup={database} backups={fake()} open onOpenChange={() => {}} />);
    expect(await screen.findByText(when(POINTS[0]))).toBeInTheDocument();
    expect(screen.getByText("Manual")).toBeInTheDocument();
    expect(screen.getByText("Before a restore")).toBeInTheDocument();
    expect(screen.getByText("2.0 kB")).toBeInTheDocument();
    expect(screen.getByText(/expires in 3 days/)).toBeInTheDocument();
  });

  it("restores after a confirmation naming the pre-restore copy", async () => {
    const user = userEvent.setup({ delay: null });
    const backups = fake();
    const onRestored = vi.fn();
    render(<RestorePointsDialog appId="a1" backup={database} backups={backups} open onOpenChange={() => {}} onRestored={onRestored} />);
    await user.click(await screen.findByRole("button", { name: `Restore the copy of ${when(POINTS[0])}` }));
    const confirm = screen.getByRole("alertdialog");
    expect(confirm).toHaveTextContent("The app stops while its database is replaced, then starts again if it was running.");
    expect(confirm).toHaveTextContent("first saved as a pre-restore copy");
    expect(confirm).not.toHaveTextContent("excludes");
    expect(within(confirm).getByRole("button", { name: "Cancel" })).toHaveFocus();
    await user.click(within(confirm).getByRole("button", { name: "Restore" }));
    await waitFor(() => expect(onRestored).toHaveBeenCalled());
    expect(backups.restoreBackup).toHaveBeenCalledWith("a1", "database", "p2");
    expect(toastSuccess).toHaveBeenCalledWith(`database restored to ${when(POINTS[0])}`, {
      description: "The data it replaced was saved as p3.\n/x: skipped",
    });
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("reports a failed restore and keeps the confirmation", async () => {
    const user = userEvent.setup({ delay: null });
    const backups = fake({ restoreBackup: vi.fn().mockRejectedValue(new Error("the restore failed, the app's data is unchanged")) });
    render(
      <RestorePointsDialog appId="a1" backup={{ ...database, type: "files", exclude: ["*.tmp"] }} backups={backups} open onOpenChange={() => {}} />,
    );
    await user.click(await screen.findByRole("button", { name: `Restore the copy of ${when(POINTS[1])}` }));
    expect(screen.getByRole("alertdialog")).toHaveTextContent("while its files are replaced");
    expect(screen.getByRole("alertdialog")).toHaveTextContent("Files this backup excludes are kept as they are.");
    await user.click(screen.getByRole("button", { name: "Restore" }));
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith("Restore failed", {
        description: "the restore failed, the app's data is unchanged",
        duration: 30_000,
      }),
    );
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("the restore failed, the app's data is unchanged");
  });

  it("lets the dialog close while a restore runs, and reports its end", async () => {
    const user = userEvent.setup({ delay: null });
    let finish: (v: unknown) => void = () => {};
    const restoreBackup = vi.fn().mockReturnValue(new Promise((resolve) => (finish = resolve)));
    const backups = fake({ restoreBackup });
    const onOpenChange = vi.fn();
    const onRestored = vi.fn();
    const { unmount } = render(
      <RestorePointsDialog appId="a1" backup={database} backups={backups} open onOpenChange={onOpenChange} onRestored={onRestored} />,
    );
    await user.click(await screen.findByRole("button", { name: `Restore the copy of ${when(POINTS[0])}` }));
    await user.click(screen.getByRole("button", { name: "Restore" }));
    expect(await screen.findByText(/the app is stopped until it is done/)).toBeInTheDocument();
    const [close] = screen.getAllByRole("button", { name: "Close" });
    expect(close).toBeEnabled();
    await user.click(close);
    expect(onOpenChange).toHaveBeenCalledWith(false);
    unmount();
    finish({ restored_point: "p2", pre_restore_point: "p3", warnings: [] });
    await waitFor(() => expect(onRestored).toHaveBeenCalled());
    expect(toastSuccess).toHaveBeenCalledWith(`database restored to ${when(POINTS[0])}`, {
      description: "The data it replaced was saved as p3.",
    });
    expect(backups.listRestorePoints).toHaveBeenCalledTimes(1); // not refreshed once closed
  });

  it("disables Restore while another restore of the app runs", async () => {
    const end = trackActivity("a1", "media", "restore");
    render(<RestorePointsDialog appId="a1" backup={database} backups={fake()} open onOpenChange={() => {}} />);
    expect(await screen.findByRole("button", { name: `Restore the copy of ${when(POINTS[0])}` })).toBeDisabled();
    expect(screen.getByText(/A restore of this app is running/)).toBeInTheDocument();
    act(() => end());
    await waitFor(() => expect(screen.getByRole("button", { name: `Restore the copy of ${when(POINTS[0])}` })).toBeEnabled());
  });

  it("deletes a copy after a confirmation", async () => {
    const user = userEvent.setup({ delay: null });
    const backups = fake();
    render(<RestorePointsDialog appId="a1" backup={database} backups={backups} open onOpenChange={() => {}} />);
    await user.click(await screen.findByRole("button", { name: `Delete the copy of ${when(POINTS[1])}` }));
    expect(screen.getByRole("alertdialog")).toHaveTextContent("(900 B)");
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await user.click(screen.getByRole("button", { name: `Delete the copy of ${when(POINTS[1])}` }));
    await user.click(screen.getByRole("button", { name: "Delete copy" }));
    await waitFor(() => expect(screen.queryByText("Before a restore")).not.toBeInTheDocument());
    expect(backups.deleteRestorePoint).toHaveBeenCalledWith("a1", "database", "p1");
    expect(toastSuccess).toHaveBeenCalledWith("Copy deleted");
  });

  it("can't restore a backup that is off, and says when there are no copies or the list fails", async () => {
    render(<RestorePointsDialog appId="a1" backup={{ ...database, enabled: false }} backups={fake()} open onOpenChange={() => {}} />);
    expect(await screen.findByRole("button", { name: `Restore the copy of ${when(POINTS[0])}` })).toBeDisabled();
    expect(screen.getByText(/Turn the backup on to restore a copy/)).toBeInTheDocument();
    render(
      <RestorePointsDialog appId="a1" backup={database} backups={fake({ listRestorePoints: vi.fn().mockResolvedValue([]) })} open onOpenChange={() => {}} />,
    );
    expect(await screen.findByText("No copies yet. Back up now to make one.")).toBeInTheDocument();
    render(
      <RestorePointsDialog
        appId="a1"
        backup={database}
        backups={fake({ listRestorePoints: vi.fn().mockRejectedValue(new Error("list the restore points: the storage did not answer within 30s")) })}
        open
        onOpenChange={() => {}}
      />,
    );
    expect(await screen.findByText(/the storage did not answer within 30s/)).toBeInTheDocument();
  });
});
