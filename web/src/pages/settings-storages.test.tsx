import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";
import type { Docker } from "@/context/use-docker";
import type { Storage, StorageDraft } from "@/lib/storages";
import { StorageDialog } from "@/components/storage-dialog";
import { StoragesCard } from "@/pages/settings-storages";

const toastError = vi.fn();
const toastSuccess = vi.fn();
const toastWarning = vi.fn();
vi.mock("sonner", () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
    warning: (...args: unknown[]) => toastWarning(...args),
  },
}));

const setupUser = () => userEvent.setup({ delay: null });

const STORAGES: Storage[] = [
  {
    name: "hetzner",
    type: "s3",
    s3: {
      endpoint: "https://fsn1.example.com",
      region: "fsn1",
      bucket: "tenants",
      prefix: "wf/",
      access_key_id: "AKIA",
      secret_access_key: "<encrypted>",
      path_style: false,
    },
    location: "https://fsn1.example.com/tenants/wf/",
    apps: ["a1", "a2"],
  },
  { name: "disk", type: "local", local: { path: "/mnt/usb" }, location: "/mnt/usb", apps: [] },
];

// saved answers a save the way the agent does: the stored storage.
const saved = (d: StorageDraft) => ({
  storage: {
    name: d.name,
    type: d.type,
    location: d.type === "local" ? d.path : `${d.endpoint}/${d.bucket}/${d.prefix}`,
    apps: [],
  } as Storage,
  redeployed: [] as string[],
  warnings: [] as string[],
});

function fakeDocker(over: Partial<Docker> = {}): Docker {
  return {
    listStorages: vi.fn().mockResolvedValue(STORAGES),
    testStorage: vi.fn().mockResolvedValue({ ok: true }),
    saveStorage: vi.fn(async (d: StorageDraft) => saved(d)),
    deleteStorage: vi.fn().mockResolvedValue(undefined),
    ...over,
  } as unknown as Docker;
}

const draftFor = (name: string): StorageDraft => ({
  name,
  type: "local",
  path: "",
  endpoint: "",
  region: "",
  bucket: "",
  prefix: "",
  accessKeyId: "",
  secret: "",
  pathStyle: false,
});

function renderCard(docker: Docker) {
  const apps = {
    apps: [
      { id: "a1", name: "Shop" },
      { id: "a2", name: "Blog" },
    ],
  } as unknown as AppsContextValue;
  return render(
    <MemoryRouter>
      <AppsContext.Provider value={apps}>
        <StoragesCard docker={docker} />
      </AppsContext.Provider>
    </MemoryRouter>,
  );
}

describe("StoragesCard", () => {
  beforeEach(() => {
    toastError.mockReset();
    toastSuccess.mockReset();
    toastWarning.mockReset();
  });

  it("lists name, type, location and the apps using each", async () => {
    renderCard(fakeDocker());
    expect(await screen.findByText("hetzner")).toHaveClass("font-mono");
    expect(screen.getByText("S3-compatible")).toBeInTheDocument();
    expect(screen.getByText("Local folder")).toBeInTheDocument();
    // Once in the Location column, once in the small-screen summary.
    expect(screen.getAllByText("https://fsn1.example.com/tenants/wf/")).toHaveLength(2);
    expect(screen.getAllByRole("link", { name: "Shop" })[0]).toHaveAttribute("href", "/app/a1");
    expect(screen.getAllByRole("link", { name: "Blog" })[0]).toHaveAttribute("href", "/app/a2");
    expect(screen.getByText("Not used")).toBeInTheDocument();
  });

  it("keeps the location and the apps visible under the name on small screens", async () => {
    renderCard(fakeDocker());
    const cell = (await screen.findByText("hetzner")).closest("td")!;
    expect(within(cell).getByText("https://fsn1.example.com/tenants/wf/")).toHaveClass("md:hidden");
    expect(within(cell).getByRole("link", { name: "Shop" }).closest(".sm\\:hidden")).not.toBeNull();
    expect(within(cell).getByRole("link", { name: "Blog" })).toHaveAttribute("href", "/app/a2");
    // A storage nobody uses gets no apps line, only its location.
    const idle = screen.getByText("disk").closest("td")!;
    expect(within(idle).getByText("/mnt/usb")).toBeInTheDocument();
    expect(within(idle).queryByText("Not used")).not.toBeInTheDocument();
  });

  it("shows an empty state", async () => {
    renderCard(fakeDocker({ listStorages: vi.fn().mockResolvedValue([]) }));
    expect(await screen.findByText(/No storages yet/)).toBeInTheDocument();
  });

  it("adds a local folder after testing it", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderCard(docker);

    await user.click(await screen.findByRole("button", { name: /Add storage/ }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Add storage")).toBeInTheDocument();
    const save = within(dialog).getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();

    await user.type(within(dialog).getByLabelText("Name"), "Backup-Disk");
    expect(within(dialog).getByText("Compose files use it as ${WF_STORAGE_BACKUP_DISK_…}.")).toBeInTheDocument();
    await user.type(within(dialog).getByLabelText("Folder"), "backups");
    expect(within(dialog).getByText(/absolute folder path/)).toBeInTheDocument();
    await user.clear(within(dialog).getByLabelText("Folder"));
    await user.type(within(dialog).getByLabelText("Folder"), "/mnt/backup/winterflow");

    await user.click(within(dialog).getByRole("button", { name: "Test connection" }));
    expect(await within(dialog).findByText("Connection works")).toBeInTheDocument();
    expect(docker.testStorage).toHaveBeenCalledWith(expect.objectContaining({ name: "backup-disk", type: "local", path: "/mnt/backup/winterflow" }));

    await user.click(save);
    expect(docker.saveStorage).toHaveBeenCalledWith(expect.objectContaining({ name: "backup-disk", path: "/mnt/backup/winterflow" }), true);
    // The answer goes straight into the list, in name order.
    const row = (await screen.findByText("backup-disk")).closest("tr");
    expect(row?.nextElementSibling).toHaveTextContent("disk");
    expect(docker.listStorages).toHaveBeenCalledTimes(1);
    expect(toastSuccess).toHaveBeenCalledWith("Storage added", { description: undefined });
  });

  it("shows a failed test inline and lets a stale result go", async () => {
    const user = setupUser();
    const docker = fakeDocker({
      testStorage: vi
        .fn()
        .mockResolvedValueOnce({ ok: false, error: "write a test file: Access Denied" })
        .mockRejectedValueOnce(new Error("agent offline")),
    });
    renderCard(docker);

    await user.click(await screen.findByRole("button", { name: /Add storage/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("tab", { name: "S3-compatible" }));
    await user.type(within(dialog).getByLabelText("Name"), "hetzner2");
    await user.type(within(dialog).getByLabelText("Endpoint"), "http://s3.example.com");
    expect(within(dialog).getByText(/plain http is only allowed/)).toBeInTheDocument();
    await user.clear(within(dialog).getByLabelText("Endpoint"));
    await user.type(within(dialog).getByLabelText("Endpoint"), "https://s3.example.com");
    await user.type(within(dialog).getByLabelText("Bucket"), "tenants");
    await user.type(within(dialog).getByLabelText("Access key"), "AKIA");
    expect(within(dialog).getByText("Enter the secret key.")).toBeInTheDocument();
    await user.type(within(dialog).getByLabelText("Secret key"), "S3-PLAIN");
    await user.click(within(dialog).getByRole("checkbox", { name: "Path-style addressing" }));

    await user.click(within(dialog).getByRole("button", { name: "Test connection" }));
    expect(await within(dialog).findByText("write a test file: Access Denied")).toBeInTheDocument();
    expect(docker.testStorage).toHaveBeenCalledWith(
      expect.objectContaining({ type: "s3", bucket: "tenants", secret: "S3-PLAIN", pathStyle: true }),
    );
    // Editing a field clears the result.
    await user.type(within(dialog).getByLabelText("Prefix"), "wf");
    expect(within(dialog).queryByText("write a test file: Access Denied")).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Test connection" }));
    expect(await within(dialog).findByText("agent offline")).toBeInTheDocument();
  });

  it("edits keeping the secret, warning about the apps to redeploy", async () => {
    const user = setupUser();
    const docker = fakeDocker({
      saveStorage: vi.fn(async (d: StorageDraft) => ({ ...saved(d), redeployed: ["a1"], warnings: ["Blog has undeployed changes"] })),
    });
    renderCard(docker);

    await user.click(await screen.findByRole("button", { name: "Edit storage hetzner" }));
    const dialog = await screen.findByRole("dialog");
    // Name and type are the storage's identity: shown, not offered.
    expect(within(dialog).queryByLabelText("Name")).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("tab")).not.toBeInTheDocument();
    expect(within(dialog).getByText("Up to 2 running apps will be redeployed.")).toBeInTheDocument();
    expect(within(dialog).getByText("Leave empty to keep.")).toBeInTheDocument();
    expect(within(dialog).getByLabelText("Secret key")).toHaveValue("");
    expect(within(dialog).getByLabelText("Bucket")).toHaveValue("tenants");

    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(docker.saveStorage).toHaveBeenCalledWith(expect.objectContaining({ name: "hetzner", secret: "", bucket: "tenants" }), false);
    await waitFor(() =>
      expect(toastWarning).toHaveBeenCalledWith("Storage saved, but not every app was redeployed", {
        description: "Redeployed Shop.\nBlog has undeployed changes",
      }),
    );
  });

  it("reports a refused save and keeps the dialog open", async () => {
    const user = setupUser();
    const docker = fakeDocker({ saveStorage: vi.fn().mockRejectedValue(new Error("the connection test failed, nothing was saved: …")) });
    renderCard(docker);
    await user.click(await screen.findByRole("button", { name: "Edit storage disk" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith("Couldn't save storage", {
        description: "the connection test failed, nothing was saved: …",
      }),
    );
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("counts one running app in the singular", async () => {
    render(
      <StorageDialog
        docker={fakeDocker()}
        open
        edit={{ ...STORAGES[1], apps: ["a1"] }}
        onOpenChange={() => {}}
        onSaved={() => {}}
      />,
    );
    expect(await screen.findByText("Up to 1 running app will be redeployed.")).toBeInTheDocument();
  });

  it("wipes a typed secret when the dialog closes", async () => {
    const user = setupUser();
    const hetzner = STORAGES[0];
    const dialog = (open: boolean) => (
      <StorageDialog docker={fakeDocker()} open={open} edit={hetzner} onOpenChange={() => {}} onSaved={() => {}} />
    );
    // The same instance throughout (no remount): only the wipe can empty it.
    const { rerender } = render(dialog(true));
    await user.type(await screen.findByLabelText("Secret key"), "TYPED");
    expect(screen.getByLabelText("Secret key")).toHaveValue("TYPED");
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    rerender(dialog(false));
    rerender(dialog(true));
    expect(await screen.findByLabelText("Secret key")).toHaveValue("");
  });

  it("lets a save that answers after the dialog was closed and reopened leave the new dialog alone", async () => {
    const user = setupUser();
    let finish: (v: ReturnType<typeof saved>) => void = () => {};
    const docker = fakeDocker({
      saveStorage: vi.fn(() => new Promise<ReturnType<typeof saved>>((resolve) => (finish = resolve))),
    });
    renderCard(docker);

    await user.click(await screen.findByRole("button", { name: /Add storage/ }));
    let dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), "usb");
    await user.type(within(dialog).getByLabelText("Folder"), "/mnt/usb2");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(within(dialog).getByRole("button", { name: "Saving…" })).toBeDisabled();
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    // A new dialog, for another storage.
    await user.click(screen.getByRole("button", { name: "Edit storage disk" }));
    dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Edit storage")).toBeInTheDocument();

    finish(saved({ ...draftFor("usb"), path: "/mnt/usb2" }));
    // The save still lands in the list, reported as the add it was...
    expect(await screen.findByText("usb")).toBeInTheDocument();
    expect(toastSuccess).toHaveBeenCalledWith("Storage added", { description: undefined });
    // ...but the dialog open now stays open.
    expect(screen.getByRole("dialog")).toBe(dialog);
    expect(within(dialog).getByText("Edit storage")).toBeInTheDocument();
  });

  it("does not close a reopened dialog (same instance) when an old save answers", async () => {
    const user = setupUser();
    let finish: (v: ReturnType<typeof saved>) => void = () => {};
    const docker = fakeDocker({
      saveStorage: vi.fn(() => new Promise<ReturnType<typeof saved>>((resolve) => (finish = resolve))),
    });
    const onOpenChange = vi.fn();
    const onSaved = vi.fn();
    const dialog = (open: boolean) => (
      <StorageDialog docker={docker} open={open} edit={STORAGES[1]} onOpenChange={onOpenChange} onSaved={onSaved} />
    );
    const { rerender } = render(dialog(true));
    await user.click(await screen.findByRole("button", { name: "Save" }));
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onOpenChange).toHaveBeenCalledTimes(1);
    rerender(dialog(false));
    rerender(dialog(true));

    finish(saved(draftFor("disk")));
    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
    expect(onOpenChange).toHaveBeenCalledTimes(1);
  });

  it("never lets an older test result overwrite a newer one", async () => {
    const user = setupUser();
    const answers: ((v: { ok: boolean; error?: string }) => void)[] = [];
    const docker = fakeDocker({
      testStorage: vi.fn(() => new Promise<{ ok: boolean; error?: string }>((resolve) => answers.push(resolve))),
    });
    renderCard(docker);

    await user.click(await screen.findByRole("button", { name: "Edit storage disk" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Test connection" }));
    // Editing a field makes the running test stale; test again.
    await user.clear(within(dialog).getByLabelText("Folder"));
    await user.type(within(dialog).getByLabelText("Folder"), "/mnt/usb3");
    await user.click(within(dialog).getByRole("button", { name: "Test connection" }));
    expect(answers).toHaveLength(2);

    answers[1]({ ok: true });
    expect(await within(dialog).findByText("Connection works")).toBeInTheDocument();
    answers[0]({ ok: false, error: "the folder /mnt/usb does not exist" });
    await new Promise((r) => setTimeout(r, 0));
    expect(within(dialog).queryByText("the folder /mnt/usb does not exist")).not.toBeInTheDocument();
    expect(within(dialog).getByText("Connection works")).toBeInTheDocument();
  });

  it("blocks removing a storage in use and confirms otherwise", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderCard(docker);

    const inUse = await screen.findByRole("button", { name: "Remove storage hetzner" });
    expect(inUse).toHaveAttribute("aria-disabled", "true");
    expect(inUse).toHaveAttribute("title", "Used by 2 apps: remove its WF_STORAGE_ variables first");
    // The reason reaches keyboard and screen reader users too: the button
    // stays focusable and describes itself.
    expect(inUse).toHaveAccessibleDescription("Used by 2 apps: remove its WF_STORAGE_ variables first");
    // The reason is only the description's target, never read on its own
    // while browsing (the button's description already says it).
    const reason = document.getElementById(inUse.getAttribute("aria-describedby") ?? "");
    expect(reason).not.toBeNull();
    expect(reason).not.toBeVisible();
    inUse.focus();
    expect(inUse).toHaveFocus();
    await user.click(inUse);
    await user.keyboard("{Enter}");
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    const free = screen.getByRole("button", { name: "Remove storage disk" });
    expect(free).not.toHaveAttribute("aria-disabled");
    expect(free).not.toHaveAttribute("aria-describedby");

    await user.click(screen.getByRole("button", { name: "Remove storage disk" }));
    const confirm = await screen.findByRole("alertdialog");
    await user.click(within(confirm).getByRole("button", { name: "Remove storage" }));
    expect(docker.deleteStorage).toHaveBeenCalledWith("disk");
    await waitFor(() => expect(screen.queryByText("disk")).not.toBeInTheDocument());
    expect(toastSuccess).toHaveBeenCalledWith("Storage removed");
  });

  it("reports load and delete failures", async () => {
    const user = setupUser();
    const docker = fakeDocker({
      listStorages: vi.fn().mockRejectedValueOnce(new Error("agent offline")).mockResolvedValue(STORAGES),
      deleteStorage: vi.fn().mockRejectedValue(new Error("disk is used by Shop")),
    });
    renderCard(docker);
    expect(await screen.findByText("Couldn't load storages.")).toBeInTheDocument();
    expect(toastError).toHaveBeenCalledWith("Failed to load storages", { description: "agent offline" });

    await user.click(screen.getByRole("button", { name: "Refresh storages" }));
    await user.click(await screen.findByRole("button", { name: "Remove storage disk" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Remove storage" }));
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith("Couldn't remove storage", { description: "disk is used by Shop" }),
    );
    expect(screen.getByText("disk")).toBeInTheDocument();
  });
});
