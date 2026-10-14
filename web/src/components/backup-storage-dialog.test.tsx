import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { BackupStorageDialog } from "@/components/backup-storage-dialog";
import { stubViewport } from "@/test/media";

// Links point into the active server's scope (/server/:id/...).
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));

function renderDialog(load: () => Promise<string[]>, current = "") {
  const onPick = vi.fn();
  render(
    <MemoryRouter>
      <BackupStorageDialog backup="database" current={current} open load={load} onOpenChange={() => {}} onPick={onPick} />
    </MemoryRouter>,
  );
  return onPick;
}

describe("BackupStorageDialog", () => {
  it("picks one of the server's storages", async () => {
    const user = userEvent.setup({ delay: null });
    const onPick = renderDialog(async () => ["disk", "offsite"], "offsite");
    const select = await screen.findByLabelText("Storage");
    expect(select).toHaveValue("offsite");
    await user.selectOptions(select, "disk");
    await user.click(screen.getByRole("button", { name: "Turn on" }));
    expect(onPick).toHaveBeenCalledWith("disk");
  });

  it("only changes the storage of a backup that has one, when it is not being turned on", async () => {
    const user = userEvent.setup({ delay: null });
    const onPick = vi.fn();
    render(
      <MemoryRouter>
        <BackupStorageDialog backup="database" current="disk" open turnOn={false} load={async () => ["disk", "offsite"]} onOpenChange={() => {}} onPick={onPick} />
      </MemoryRouter>,
    );
    expect(screen.getByRole("heading", { name: "Storage of database" })).toBeInTheDocument();
    await user.selectOptions(await screen.findByLabelText("Storage"), "offsite");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onPick).toHaveBeenCalledWith("offsite");
  });

  it("shows progress and can't be sent twice while the backup is turned on", async () => {
    render(
      <MemoryRouter>
        <BackupStorageDialog backup="database" current="disk" open busy load={async () => ["disk"]} onOpenChange={() => {}} onPick={() => {}} />
      </MemoryRouter>,
    );
    expect(await screen.findByRole("button", { name: "Turning on…" })).toBeDisabled();
  });

  it("starts on the first storage when the last one is gone", async () => {
    renderDialog(async () => ["disk"], "removed");
    expect(await screen.findByLabelText("Storage")).toHaveValue("disk");
  });

  it("points to Settings → Storages when there are none", async () => {
    renderDialog(async () => []);
    const link = await screen.findByRole("link", { name: "Add a storage in Settings → Storages" });
    expect(link).toHaveAttribute("href", "/server/srv-1/settings/storages");
    expect(screen.getByRole("button", { name: "Turn on" })).toBeDisabled();
  });

  it("says when the storages can't be loaded", async () => {
    renderDialog(() => Promise.reject(new Error("server offline")));
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("server offline"));
  });

  it("keeps the reason the backup could not be turned on in the dialog", async () => {
    render(
      <MemoryRouter>
        <BackupStorageDialog backup="database" current="disk" open error="storage disk is gone" load={async () => ["disk"]} onOpenChange={() => {}} onPick={() => {}} />
      </MemoryRouter>,
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("storage disk is gone");
  });

  it("is a bottom sheet on a phone", async () => {
    stubViewport(375);
    try {
      const user = userEvent.setup({ delay: null });
      const onPick = renderDialog(async () => ["disk"]);
      expect(screen.getByRole("dialog", { name: "Turn on database" })).toHaveClass("rounded-t-[10px]");
      await screen.findByLabelText("Storage");
      await user.click(screen.getByRole("button", { name: "Turn on" }));
      expect(onPick).toHaveBeenCalledWith("disk");
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
