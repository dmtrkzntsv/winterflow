import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";
import type { Docker } from "@/context/use-docker";
import type { Storage, StorageDraft } from "@/lib/storages";
import { StorageDialog } from "@/components/storage-dialog";
import { stubViewport } from "@/test/media";
import { StoragesSettings } from "@/pages/settings-storages";

// Links point into the active server's scope (/server/:id/...).
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));

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

const backups = vi.hoisted(() => ({ listOrphans: vi.fn(), deleteOrphan: vi.fn() }));
vi.mock("@/context/use-backups", () => ({ useBackups: () => backups }));

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
        <StoragesSettings docker={docker} />
      </AppsContext.Provider>
    </MemoryRouter>,
  );
}

// pick opens a storage row's "⋯" menu and chooses an item.
async function pick(user: ReturnType<typeof setupUser>, name: string, item: string) {
  await user.click(await screen.findByRole("button", { name: `Actions for ${name}` }));
  await user.click(await screen.findByRole("menuitem", { name: item }));
}

describe("StoragesSettings", () => {
  beforeEach(() => {
    toastError.mockReset();
    toastSuccess.mockReset();
    toastWarning.mockReset();
  });

  it("lists name, kind, location and the apps using each", async () => {
    renderCard(fakeDocker());
    expect(screen.getByRole("heading", { level: 1, name: "Storages" })).toBeInTheDocument();
    expect(await screen.findByText("hetzner")).toHaveClass("font-mono");
    expect(screen.getByText("S3 bucket · https://fsn1.example.com/tenants/wf/")).toBeInTheDocument();
    expect(screen.getByText("Folder on this server · /mnt/usb")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Shop" })).toHaveAttribute("href", "/server/srv-1/app/a1");
    expect(screen.getByRole("link", { name: "Blog" })).toHaveAttribute("href", "/server/srv-1/app/a2");
    expect(screen.getByText("Not used")).toBeInTheDocument();
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
    // Nothing typed yet, no problem shown: Save stays clickable so it can
    // say what is missing.
    expect(save).toBeEnabled();

    await user.type(within(dialog).getByLabelText("Name"), "Backup-Disk");
    expect(within(dialog).getByText("Compose files use it as ${WF_STORAGE_BACKUP_DISK_…}.")).toBeInTheDocument();
    await user.type(within(dialog).getByLabelText("Folder"), "backups");
    expect(within(dialog).getByText(/absolute folder path/)).toBeInTheDocument();
    await user.clear(within(dialog).getByLabelText("Folder"));
    await user.type(within(dialog).getByLabelText("Folder"), "/mnt/backup/winterflow");

    // A folder has no separate check: Save checks it.
    expect(within(dialog).queryByRole("button", { name: "Check folder" })).not.toBeInTheDocument();

    await user.click(save);
    // An add: the agent refuses it over an existing storage.
    expect(docker.saveStorage).toHaveBeenCalledWith(expect.objectContaining({ name: "backup-disk", path: "/mnt/backup/winterflow" }), true);
    // The answer goes straight into the list, in name order.
    const row = (await screen.findByText("backup-disk")).closest("li");
    expect(row?.nextElementSibling).toHaveTextContent("disk");
    expect(docker.listStorages).toHaveBeenCalledTimes(1);
    expect(toastSuccess).toHaveBeenCalledWith("Storage added", { description: undefined });
  });

  it("says nothing about the name before it is tried, then the error replaces its hint", async () => {
    const user = setupUser();
    renderCard(fakeDocker());
    await user.click(await screen.findByRole("button", { name: /Add storage/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("radio", { name: "S3 bucket" }));
    expect(within(dialog).queryByText(/Use 1-32 lowercase letters/)).not.toBeInTheDocument();
    expect(within(dialog).getByText("Lowercase letters, digits and hyphens.")).toBeInTheDocument();
    await user.type(within(dialog).getByLabelText("Name"), "Bad_Name");
    await user.tab();
    expect(within(dialog).getByText(/Use 1-32 lowercase letters/)).toBeInTheDocument();
    expect(within(dialog).queryByText(/Compose files use it as/)).not.toBeInTheDocument();
    expect(within(dialog).getByLabelText("Name")).toHaveAccessibleDescription(/Use 1-32 lowercase letters/);
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
    await user.click(within(dialog).getByRole("radio", { name: "S3 bucket" }));
    await user.type(within(dialog).getByLabelText("Name"), "hetzner2");
    await user.type(within(dialog).getByLabelText("Endpoint"), "s3.example.com");
    expect(within(dialog).getByText("Use a URL like https://s3.example.com.")).toBeInTheDocument();
    await user.clear(within(dialog).getByLabelText("Endpoint"));
    await user.type(within(dialog).getByLabelText("Endpoint"), "https://s3.example.com");
    await user.type(within(dialog).getByLabelText("Bucket"), "tenants");
    await user.type(within(dialog).getByLabelText("Access key"), "AKIA");
    // Not reached yet: no complaint. Left empty: it says what is missing.
    expect(within(dialog).queryByText("Enter the secret key.")).not.toBeInTheDocument();
    await user.click(within(dialog).getByLabelText("Secret key"));
    await user.tab();
    expect(within(dialog).getByText("Enter the secret key.")).toBeInTheDocument();
    await user.type(within(dialog).getByLabelText("Secret key"), "S3-PLAIN");
    await user.click(within(dialog).getByRole("button", { name: "Advanced" }));
    await user.type(within(dialog).getByLabelText("Region"), "fsn1");
    await user.click(within(dialog).getByRole("checkbox", { name: "Path-style addressing" }));

    await user.click(within(dialog).getByRole("button", { name: "Test connection" }));
    expect(await within(dialog).findByText("write a test file: Access Denied")).toBeInTheDocument();
    expect(docker.testStorage).toHaveBeenCalledWith(
      expect.objectContaining({ type: "s3", region: "fsn1", bucket: "tenants", secret: "S3-PLAIN", pathStyle: true }),
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

    await pick(user, "hetzner", "Edit");
    const dialog = await screen.findByRole("dialog");
    // Name and type are the storage's identity: shown, not offered.
    expect(within(dialog).queryByLabelText("Name")).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("radiogroup")).not.toBeInTheDocument();
    expect(within(dialog).getByText("Saving redeploys the apps that use it if they're running: Shop, Blog.")).toBeInTheDocument();
    expect(within(dialog).getByText("Leave empty to keep.")).toBeInTheDocument();
    expect(within(dialog).getByLabelText("Secret key")).toHaveValue("");
    expect(within(dialog).getByLabelText("Bucket")).toHaveValue("tenants");

    // Nothing changed yet: nothing to save (nor to redeploy).
    const save = within(dialog).getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();
    await user.type(within(dialog).getByLabelText("Prefix"), "x");
    await user.click(save);
    expect(docker.saveStorage).toHaveBeenCalledWith(expect.objectContaining({ name: "hetzner", secret: "", bucket: "tenants", prefix: "wf/x" }), false);
    // The warning asks for action: it stays until dismissed.
    await waitFor(() =>
      expect(toastWarning).toHaveBeenCalledWith(
        "Storage saved, but not every app was redeployed",
        expect.objectContaining({ duration: Infinity, closeButton: true }),
      ),
    );
    // One line per warning (toast descriptions keep newlines).
    expect(toastWarning.mock.calls[0][1].description).toBe("Redeployed Shop.\nBlog has undeployed changes");
  });

  it("reports a refused save inside the dialog, which stays open", async () => {
    const user = setupUser();
    const docker = fakeDocker({ saveStorage: vi.fn().mockRejectedValue(new Error("the connection test failed, nothing was saved: …")) });
    renderCard(docker);
    await pick(user, "disk", "Edit");
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Folder"), "2");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    // The reason stays next to the form; the toast only says it failed.
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("the connection test failed, nothing was saved: …");
    expect(toastError).toHaveBeenCalledWith("Couldn't save storage");
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    // Any change makes it stale.
    await user.type(within(dialog).getByLabelText("Folder"), "3");
    expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument();
  });

  it("puts a refused add's reason in the toast once the dialog is closed", async () => {
    const user = setupUser();
    let fail: (e: Error) => void = () => {};
    const docker = fakeDocker({ saveStorage: vi.fn(() => new Promise<never>((_, reject) => (fail = reject))) });
    renderCard(docker);
    await user.click(await screen.findByRole("button", { name: /Add storage/ }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), "usb");
    await user.type(within(dialog).getByLabelText("Folder"), "/mnt/usb2");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    fail(new Error("a storage named usb already exists on this server"));
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith("Couldn't add storage", {
        description: "a storage named usb already exists on this server",
      }),
    );
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
    expect(await screen.findByText("Saving redeploys the app that uses it if it's running: a1.")).toBeInTheDocument();
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
    expect(within(dialog).getByRole("button", { name: "Checking folder…" })).toBeDisabled();
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    // A new dialog, for another storage.
    await pick(user, "disk", "Edit");
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
    await user.type(await screen.findByLabelText("Folder"), "2");
    await user.click(screen.getByRole("button", { name: "Save" }));
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

    await pick(user, "hetzner", "Edit");
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Test connection" }));
    // Editing a field makes the running test stale; test again.
    await user.clear(within(dialog).getByLabelText("Bucket"));
    await user.type(within(dialog).getByLabelText("Bucket"), "tenants2");
    await user.click(within(dialog).getByRole("button", { name: "Test connection" }));
    expect(answers).toHaveLength(2);

    answers[1]({ ok: true });
    expect(await within(dialog).findByText("Connection works")).toBeInTheDocument();
    answers[0]({ ok: false, error: "no such bucket" });
    await new Promise((r) => setTimeout(r, 0));
    expect(within(dialog).queryByText("no such bucket")).not.toBeInTheDocument();
    expect(within(dialog).getByText("Connection works")).toBeInTheDocument();
  });

  it("explains why a storage in use can't be removed, and confirms otherwise", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderCard(docker);

    await user.click(await screen.findByRole("button", { name: "Actions for hetzner" }));
    const blocked = await screen.findByRole("menuitem", { name: "Remove" });
    expect(blocked).toHaveAttribute("title", "Used by Shop and Blog — remove the references first");
    await user.click(blocked);
    expect(toastWarning).toHaveBeenLastCalledWith("Can't remove hetzner", {
      description: "Used by Shop and Blog — remove the references first.",
    });
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(docker.deleteStorage).not.toHaveBeenCalled();

    await pick(user, "disk", "Remove");
    const confirm = await screen.findByRole("alertdialog");
    expect(confirm).toHaveTextContent("disk is removed from this server. Nothing stored in it is deleted.");
    await user.click(within(confirm).getByRole("button", { name: "Remove storage" }));
    expect(docker.deleteStorage).toHaveBeenCalledWith("disk");
    await waitFor(() => expect(screen.queryByText("disk")).not.toBeInTheDocument());
    expect(toastSuccess).toHaveBeenCalledWith("Storage removed");
    // The row's menu left with the row: focus goes to Add storage.
    await waitFor(() => expect(screen.getByRole("button", { name: /Add storage/ })).toHaveFocus());
  });

  it("reports load and delete failures, keeping the confirmation open", async () => {
    const user = setupUser();
    const docker = fakeDocker({
      listStorages: vi.fn().mockRejectedValueOnce(new Error("agent offline")).mockResolvedValue(STORAGES),
      deleteStorage: vi.fn().mockRejectedValue(new Error("disk is used by Shop")),
    });
    renderCard(docker);
    expect(await screen.findByText("Couldn't load storages.")).toBeInTheDocument();
    expect(toastError).toHaveBeenCalledWith("Couldn't load storages", { description: "agent offline" });

    await user.click(screen.getByRole("button", { name: "Try again" }));
    await pick(user, "disk", "Remove");
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Remove storage" }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith("Couldn't remove storage", { description: "disk is used by Shop" }));
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(screen.getAllByText("disk")).toHaveLength(2); // the row, and the confirmation
    // Cancelling after a failure puts focus back on the row's menu.
    await user.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Actions for disk" })).toHaveFocus());
  });

  it("opens Advanced at once on an edit that uses it", async () => {
    const user = setupUser();
    renderCard(fakeDocker());
    await pick(user, "hetzner", "Edit");
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByLabelText("Region")).toHaveValue("fsn1");
    expect(within(dialog).getByLabelText("Prefix")).toHaveValue("wf/");
  });
});

describe("StorageDialog form", () => {
  beforeEach(() => {
    toastError.mockReset();
    toastSuccess.mockReset();
    toastWarning.mockReset();
  });

  const openAdd = async (user: ReturnType<typeof setupUser>, docker = fakeDocker()) => {
    renderCard(docker);
    await user.click(await screen.findByRole("button", { name: /Add storage/ }));
    return screen.findByRole("dialog");
  };

  it("focuses the name on a desktop but leaves a phone's sheet alone", async () => {
    const user = setupUser();
    const dialog = await openAdd(user);
    await waitFor(() => expect(within(dialog).getByLabelText("Name")).toHaveFocus());
  });

  describe("on a phone", () => {
    afterEach(() => vi.unstubAllGlobals());

    it("does not pop the keyboard over the sheet", async () => {
      stubViewport(375);
      const user = setupUser();
      const dialog = await openAdd(user);
      expect(within(dialog).getByLabelText("Name")).not.toHaveFocus();
    });
  });

  it("opens Advanced to show a problem inside it", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    const dialog = await openAdd(user, docker);
    await user.click(within(dialog).getByRole("radio", { name: "S3 bucket" }));
    await user.type(within(dialog).getByLabelText("Name"), "offsite");
    await user.type(within(dialog).getByLabelText("Endpoint"), "https://s3.example.com");
    await user.type(within(dialog).getByLabelText("Bucket"), "tenants");
    await user.type(within(dialog).getByLabelText("Access key"), "AKIA");
    await user.type(within(dialog).getByLabelText("Secret key"), "S3-PLAIN");
    await user.click(within(dialog).getByRole("button", { name: "Advanced" }));
    await user.type(within(dialog).getByLabelText("Prefix"), "/x");
    // Folded away again, the problem is out of sight: Save stays usable
    // and, pressed, shows where the problem is instead of saving.
    await user.click(within(dialog).getByRole("button", { name: "Advanced" }));
    expect(within(dialog).queryByLabelText("Prefix")).not.toBeInTheDocument();
    const save = within(dialog).getByRole("button", { name: "Save" });
    expect(save).toBeEnabled();
    await user.click(save);
    expect(docker.saveStorage).not.toHaveBeenCalled();
    expect(within(dialog).getByLabelText("Prefix")).toHaveFocus();
    expect(within(dialog).getByText("Use folder names separated by /, like winterflow/.")).toBeInTheDocument();
  });

  it("keeps Advanced folded on an add", async () => {
    const user = setupUser();
    const dialog = await openAdd(user);
    await user.click(within(dialog).getByRole("radio", { name: "S3 bucket" }));
    expect(within(dialog).queryByLabelText("Region")).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("checkbox", { name: "Path-style addressing" })).not.toBeInTheDocument();
  });

  it("refuses to add a name the server already has, at the name", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    const dialog = await openAdd(user, docker);
    const name = within(dialog).getByLabelText("Name");
    await user.type(name, "disk");
    await user.type(within(dialog).getByLabelText("Folder"), "/mnt/other");
    expect(name).toHaveAttribute("aria-invalid", "true");
    expect(name).toHaveAccessibleDescription(expect.stringContaining("A storage named disk already exists. Edit it instead."));
    expect(within(dialog).getByRole("button", { name: "Save" })).toBeDisabled();
    await user.type(name, "2");
    expect(name).not.toHaveAttribute("aria-invalid");
    expect(within(dialog).getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("shows each problem under its field, and only once the user got there", async () => {
    const user = setupUser();
    const dialog = await openAdd(user);
    const name = within(dialog).getByLabelText("Name");
    const folder = within(dialog).getByLabelText("Folder");
    // Focus starts on the name.
    expect(name).toHaveFocus();
    await user.type(name, "usb");
    expect(within(dialog).queryByText(/absolute folder path/)).not.toBeInTheDocument();
    expect(folder).not.toHaveAttribute("aria-invalid");

    await user.type(folder, "relative");
    const error = within(dialog).getByText("Use an absolute folder path, like /mnt/backup/winterflow.");
    expect(folder).toHaveAttribute("aria-invalid", "true");
    expect(folder.getAttribute("aria-describedby")?.split(" ")).toContain(error.id);
    // Right under the field, not at the bottom of the form.
    expect(folder.parentElement).toContainElement(error);
  });

  it("says what is missing when Save meets a field the user never reached", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    const dialog = await openAdd(user, docker);
    await user.click(within(dialog).getByRole("radio", { name: "S3 bucket" }));
    await user.type(within(dialog).getByLabelText("Name"), "offsite");
    await user.type(within(dialog).getByLabelText("Endpoint"), "https://s3.example.com");
    await user.type(within(dialog).getByLabelText("Bucket"), "tenants");
    await user.type(within(dialog).getByLabelText("Access key"), "AKIA");
    // The secret was never focused: its problem is hidden, Save is not.
    const secret = within(dialog).getByLabelText("Secret key");
    expect(within(dialog).queryByText("Enter the secret key.")).not.toBeInTheDocument();
    const save = within(dialog).getByRole("button", { name: "Save" });
    expect(save).toBeEnabled();

    await user.click(save);
    expect(docker.saveStorage).not.toHaveBeenCalled();
    expect(within(dialog).getByText("Enter the secret key.")).toBeInTheDocument();
    expect(secret).toHaveAttribute("aria-invalid", "true");
    expect(secret).toHaveFocus();
    // Now on screen, the problem disables Save until it is fixed.
    expect(save).toBeDisabled();
    await user.type(secret, "S3-PLAIN");
    expect(save).toBeEnabled();
    await user.click(save);
    expect(docker.saveStorage).toHaveBeenCalledWith(expect.objectContaining({ name: "offsite", secret: "S3-PLAIN" }), true);
  });

  it("says what is missing when the test meets a field the user never reached", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    const dialog = await openAdd(user, docker);
    await user.click(within(dialog).getByRole("radio", { name: "S3 bucket" }));
    await user.type(within(dialog).getByLabelText("Name"), "offsite");
    // The endpoint was never focused: its problem is hidden, the test is not.
    const check = within(dialog).getByRole("button", { name: "Test connection" });
    expect(check).toBeEnabled();
    await user.click(check);
    expect(docker.testStorage).not.toHaveBeenCalled();
    const endpoint = within(dialog).getByLabelText("Endpoint");
    expect(endpoint).toHaveFocus();
    // Now on screen, the problems disable the test until they are fixed.
    expect(check).toBeDisabled();
    await user.type(endpoint, "https://s3.example.com");
    await user.type(within(dialog).getByLabelText("Bucket"), "backups");
    await user.type(within(dialog).getByLabelText("Access key"), "AKIA");
    expect(check).toBeDisabled();
    await user.type(within(dialog).getByLabelText("Secret key"), "S");
    expect(check).toBeEnabled();
    await user.click(check);
    expect(docker.testStorage).toHaveBeenCalledWith(expect.objectContaining({ name: "offsite", endpoint: "https://s3.example.com" }));
  });

  it("keeps an edit's stored path-style when an IP endpoint is typed", async () => {
    const user = setupUser();
    renderCard(fakeDocker());
    await pick(user, "hetzner", "Edit");
    const dialog = await screen.findByRole("dialog");
    const pathStyle = within(dialog).getByRole("checkbox", { name: "Path-style addressing" });
    expect(pathStyle).not.toBeChecked(); // as stored
    const endpoint = within(dialog).getByLabelText("Endpoint");
    await user.clear(endpoint);
    await user.type(endpoint, "http://127.0.0.1:9000");
    expect(pathStyle).not.toBeChecked();
  });

  it("labels the type choice and offers type-appropriate examples", async () => {
    const user = setupUser();
    const dialog = await openAdd(user);
    const type = within(dialog).getByRole("radiogroup", { name: "Type" });
    expect(within(type).getByRole("radio", { name: "Folder on this server" })).toBeChecked();
    expect(within(dialog).getByLabelText("Name")).toHaveAttribute("placeholder", "e.g. backup-disk");
    await user.click(within(type).getByRole("radio", { name: "S3 bucket" }));
    await user.click(within(dialog).getByRole("button", { name: "Advanced" }));
    expect(within(dialog).getByLabelText("Name")).toHaveAttribute("placeholder", "e.g. offsite");
    expect(within(dialog).getByLabelText("Prefix")).toHaveAttribute("placeholder", "e.g. winterflow/");
    expect(within(dialog).getByLabelText("Region")).toHaveAttribute("placeholder", "e.g. auto");
    expect(within(dialog).getByRole("button", { name: "Test connection" })).toBeInTheDocument();
  });

  it("turns path-style on for an IP or localhost endpoint until the user sets it", async () => {
    const user = setupUser();
    const dialog = await openAdd(user);
    await user.click(within(dialog).getByRole("radio", { name: "S3 bucket" }));
    await user.click(within(dialog).getByRole("button", { name: "Advanced" }));
    const endpoint = within(dialog).getByLabelText("Endpoint");
    const pathStyle = within(dialog).getByRole("checkbox", { name: "Path-style addressing" });
    await user.type(endpoint, "http://127.0.0.1:9000");
    expect(pathStyle).toBeChecked();
    await user.clear(endpoint);
    await user.type(endpoint, "https://s3.example.com");
    expect(pathStyle).not.toBeChecked();
    // The user's choice wins from then on.
    await user.click(pathStyle);
    await user.clear(endpoint);
    await user.type(endpoint, "https://s3.other.example.com");
    expect(pathStyle).toBeChecked();
  });

  it("keeps the user's path-style when the endpoint moves to an IP and back", async () => {
    const user = setupUser();
    const dialog = await openAdd(user);
    await user.click(within(dialog).getByRole("radio", { name: "S3 bucket" }));
    await user.click(within(dialog).getByRole("button", { name: "Advanced" }));
    const endpoint = within(dialog).getByLabelText("Endpoint");
    const pathStyle = within(dialog).getByRole("checkbox", { name: "Path-style addressing" });
    await user.type(endpoint, "https://s3.example.com");
    await user.click(pathStyle); // ticked on a host name: the user's choice
    await user.clear(endpoint);
    await user.type(endpoint, "http://10.0.0.5:9000");
    expect(pathStyle).toBeChecked();
    await user.clear(endpoint);
    await user.type(endpoint, "https://s3.example.com");
    expect(pathStyle).toBeChecked();
  });

  it("shows the backups of deleted apps kept in a storage", async () => {
    const user = setupUser();
    backups.listOrphans.mockResolvedValue([
      { app_id: "gone-1", app_name: "Old shop", copies: 2, bytes: 2048, deleted_at: 1790812800, purge_at: 1806364800 },
    ]);
    renderCard(fakeDocker());
    await user.click(await screen.findByRole("button", { name: "Actions for disk" }));
    // The link below the list is the one way in, not a row menu item too.
    expect(screen.queryByRole("menuitem", { name: "Backups of deleted apps" })).not.toBeInTheDocument();
    await user.keyboard("{Escape}");
    const link = screen.getByRole("button", { name: "Backups of deleted apps" });
    await user.click(link);
    await user.click(await screen.findByRole("menuitem", { name: "disk" }));
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByText("Old shop")).toBeInTheDocument();
    expect(backups.listOrphans).toHaveBeenCalledWith("disk");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await waitFor(() => expect(link).toHaveFocus());
  });

  it("announces only the test result, not the button", async () => {
    const user = setupUser();
    const dialog = await openAdd(user);
    await user.click(within(dialog).getByRole("radio", { name: "S3 bucket" }));
    await user.type(within(dialog).getByLabelText("Name"), "offsite");
    await user.type(within(dialog).getByLabelText("Endpoint"), "https://s3.example.com");
    await user.type(within(dialog).getByLabelText("Bucket"), "backups");
    await user.type(within(dialog).getByLabelText("Access key"), "AKIA");
    await user.type(within(dialog).getByLabelText("Secret key"), "S");
    const check = within(dialog).getByRole("button", { name: "Test connection" });
    const status = within(dialog).getByRole("status");
    expect(status).not.toContainElement(check);
    await user.click(check);
    await waitFor(() => expect(status).toHaveTextContent("Connection works"));
  });

  it("locks the form and says what is going on while saving", async () => {
    const user = setupUser();
    let finish: (v: ReturnType<typeof saved>) => void = () => {};
    const docker = fakeDocker({
      saveStorage: vi.fn(() => new Promise<ReturnType<typeof saved>>((resolve) => (finish = resolve))),
    });
    renderCard(docker);
    await pick(user, "hetzner", "Edit");
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Prefix"), "x");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(within(dialog).getByText(/Testing the storage and redeploying apps that use it…/)).toBeInTheDocument();
    expect(within(dialog).getByLabelText("Prefix")).toBeDisabled();
    expect(within(dialog).getByRole("button", { name: "Test connection" })).toBeDisabled();
    // Closing stays possible: the save goes on.
    expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeEnabled();
    finish(saved(draftFor("hetzner")));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("puts the caret at the end of an edit's first field without selecting it", async () => {
    const user = setupUser();
    renderCard(fakeDocker());
    await pick(user, "disk", "Edit");
    const folder = (await screen.findByLabelText("Folder")) as HTMLInputElement;
    expect(folder).toHaveFocus();
    expect(folder.selectionStart).toBe("/mnt/usb".length);
    expect(folder.selectionEnd).toBe("/mnt/usb".length);
  });

  it("links to the backups of deleted apps below the list, listing a storage only when asked", async () => {
    const user = setupUser();
    backups.listOrphans.mockReset().mockResolvedValue([]);
    // Several storages: the link asks which one.
    const { unmount } = renderCard(fakeDocker());
    const link = await screen.findByRole("button", { name: "Backups of deleted apps" });
    // Opening the page lists no storage (each listing reads the whole bucket).
    expect(backups.listOrphans).not.toHaveBeenCalled();
    await user.click(link);
    await user.click(await screen.findByRole("menuitem", { name: "disk" }));
    expect(await screen.findByRole("dialog")).toHaveTextContent("Backups of deleted apps");
    expect(backups.listOrphans).toHaveBeenLastCalledWith("disk");
    unmount();

    // One storage: the link opens its backups at once.
    renderCard(fakeDocker({ listStorages: vi.fn().mockResolvedValue([STORAGES[0]]) }));
    await user.click(await screen.findByRole("button", { name: "Backups of deleted apps" }));
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(backups.listOrphans).toHaveBeenLastCalledWith("hetzner");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.getByRole("button", { name: "Backups of deleted apps" })).toHaveFocus());
  });

  it("returns focus to what opened a dialog", async () => {
    const user = setupUser();
    renderCard(fakeDocker());
    const add = await screen.findByRole("button", { name: /Add storage/ });
    await user.click(add);
    await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(add).toHaveFocus());

    await pick(user, "disk", "Edit");
    await screen.findByRole("dialog");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.getByRole("button", { name: "Actions for disk" })).toHaveFocus());
  });
});
