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
    // Once in the Type column, once under the name on small screens.
    expect(screen.getAllByText("S3-compatible")).toHaveLength(2);
    expect(screen.getAllByText("Local folder")).toHaveLength(2);
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
    // Nothing typed yet, no problem shown: Save stays clickable so it can
    // say what is missing.
    expect(save).toBeEnabled();

    await user.type(within(dialog).getByLabelText("Name"), "Backup-Disk");
    expect(within(dialog).getByText("Compose files use it as ${WF_STORAGE_BACKUP_DISK_…}.")).toBeInTheDocument();
    await user.type(within(dialog).getByLabelText("Folder"), "backups");
    expect(within(dialog).getByText(/absolute folder path/)).toBeInTheDocument();
    await user.clear(within(dialog).getByLabelText("Folder"));
    await user.type(within(dialog).getByLabelText("Folder"), "/mnt/backup/winterflow");

    await user.click(within(dialog).getByRole("button", { name: "Check folder" }));
    expect(await within(dialog).findByText("Folder is writable")).toBeInTheDocument();
    expect(docker.testStorage).toHaveBeenCalledWith(expect.objectContaining({ name: "backup-disk", type: "local", path: "/mnt/backup/winterflow" }));

    await user.click(save);
    // An add: the agent refuses it over an existing storage.
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
    await user.click(within(dialog).getByRole("radio", { name: "S3-compatible" }));
    await user.type(within(dialog).getByLabelText("Name"), "hetzner2");
    await user.type(within(dialog).getByLabelText("Endpoint"), "s3.example.com");
    expect(within(dialog).getByText("Use a URL like https://s3.example.com.")).toBeInTheDocument();
    await user.clear(within(dialog).getByLabelText("Endpoint"));
    await user.type(within(dialog).getByLabelText("Endpoint"), "https://s3.example.com");
    await user.type(within(dialog).getByLabelText("Region"), "fsn1");
    await user.type(within(dialog).getByLabelText("Bucket"), "tenants");
    await user.type(within(dialog).getByLabelText("Access key"), "AKIA");
    // Not reached yet: no complaint. Left empty: it says what is missing.
    expect(within(dialog).queryByText("Enter the secret key.")).not.toBeInTheDocument();
    await user.click(within(dialog).getByLabelText("Secret key"));
    await user.tab();
    expect(within(dialog).getByText("Enter the secret key.")).toBeInTheDocument();
    await user.type(within(dialog).getByLabelText("Secret key"), "S3-PLAIN");
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

    await user.click(await screen.findByRole("button", { name: "Edit storage hetzner" }));
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
    await user.click(await screen.findByRole("button", { name: "Edit storage disk" }));
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

    await user.click(await screen.findByRole("button", { name: "Edit storage disk" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Check folder" }));
    // Editing a field makes the running test stale; test again.
    await user.clear(within(dialog).getByLabelText("Folder"));
    await user.type(within(dialog).getByLabelText("Folder"), "/mnt/usb3");
    await user.click(within(dialog).getByRole("button", { name: "Check folder" }));
    expect(answers).toHaveLength(2);

    answers[1]({ ok: true });
    expect(await within(dialog).findByText("Folder is writable")).toBeInTheDocument();
    answers[0]({ ok: false, error: "the folder /mnt/usb does not exist" });
    await new Promise((r) => setTimeout(r, 0));
    expect(within(dialog).queryByText("the folder /mnt/usb does not exist")).not.toBeInTheDocument();
    expect(within(dialog).getByText("Folder is writable")).toBeInTheDocument();
  });

  it("blocks removing a storage in use and confirms otherwise", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderCard(docker);

    const inUse = await screen.findByRole("button", { name: "Remove storage hetzner" });
    expect(inUse).toHaveAttribute("aria-disabled", "true");
    expect(inUse).toHaveAttribute("title", "Used by Shop and Blog — remove the references first");
    // The reason reaches keyboard and screen reader users too: the button
    // stays focusable and describes itself.
    expect(inUse).toHaveAccessibleDescription("Used by Shop and Blog — remove the references first");
    // The reason is only the description's target, never read on its own
    // while browsing (the button's description already says it).
    const reason = document.getElementById(inUse.getAttribute("aria-describedby") ?? "");
    expect(reason).not.toBeNull();
    expect(reason).not.toBeVisible();
    inUse.focus();
    expect(inUse).toHaveFocus();
    // A click, a tap or Enter says why on screen, and removes nothing.
    await user.click(inUse);
    expect(toastWarning).toHaveBeenLastCalledWith("Can't remove hetzner", {
      description: "Used by Shop and Blog — remove the references first.",
    });
    toastWarning.mockReset();
    await user.keyboard("{Enter}");
    expect(toastWarning).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(docker.deleteStorage).not.toHaveBeenCalled();
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
    await user.click(within(dialog).getByRole("radio", { name: "S3-compatible" }));
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
    await user.type(within(dialog).getByLabelText("Name"), "usb");
    // The folder was never focused: its problem is hidden, the check is not.
    const check = within(dialog).getByRole("button", { name: "Check folder" });
    expect(check).toBeEnabled();
    await user.click(check);
    expect(docker.testStorage).not.toHaveBeenCalled();
    const folder = within(dialog).getByLabelText("Folder");
    expect(within(dialog).getByText("Use an absolute folder path, like /mnt/backup/winterflow.")).toBeInTheDocument();
    expect(folder).toHaveFocus();
    // Now on screen, the problem disables the check until it is fixed.
    expect(check).toBeDisabled();
    await user.type(folder, "/mnt/usb");
    expect(check).toBeEnabled();
    await user.click(check);
    expect(docker.testStorage).toHaveBeenCalledWith(expect.objectContaining({ name: "usb", path: "/mnt/usb" }));
  });

  it("keeps an edit's stored path-style when an IP endpoint is typed", async () => {
    const user = setupUser();
    renderCard(fakeDocker());
    await user.click(await screen.findByRole("button", { name: "Edit storage hetzner" }));
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
    expect(within(type).getByRole("radio", { name: "Local folder" })).toBeChecked();
    expect(within(dialog).getByLabelText("Name")).toHaveAttribute("placeholder", "e.g. backup-disk");
    await user.click(within(type).getByRole("radio", { name: "S3-compatible" }));
    expect(within(dialog).getByLabelText("Name")).toHaveAttribute("placeholder", "e.g. offsite");
    expect(within(dialog).getByLabelText("Prefix")).toHaveAttribute("placeholder", "e.g. winterflow/");
    expect(within(dialog).getByLabelText("Region")).toHaveAttribute("placeholder", "e.g. auto");
    expect(within(dialog).getByRole("button", { name: "Test connection" })).toBeInTheDocument();
  });

  it("turns path-style on for an IP or localhost endpoint until the user sets it", async () => {
    const user = setupUser();
    const dialog = await openAdd(user);
    await user.click(within(dialog).getByRole("radio", { name: "S3-compatible" }));
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
    await user.click(within(dialog).getByRole("radio", { name: "S3-compatible" }));
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

  it("shows a storage's backups of apps not on this server", async () => {
    const user = setupUser();
    backups.listOrphans.mockResolvedValue([{ app_id: "gone-1", app_name: "Old shop", copies: 2, bytes: 2048, deleted_at: 1790812800, purge_at: 1806364800 }]);
    renderCard(fakeDocker());
    // A labeled entry point, not an unlabeled icon.
    expect(await screen.findByRole("button", { name: "Other apps' backups in disk" })).toHaveTextContent("Other apps' backups");
    await user.click(screen.getByRole("button", { name: "Other apps' backups in disk" }));
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByText("Old shop")).toBeInTheDocument();
    expect(backups.listOrphans).toHaveBeenCalledWith("disk");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Other apps' backups in disk" })).toHaveFocus();
  });

  it("announces only the test result, not the button", async () => {
    const user = setupUser();
    const dialog = await openAdd(user);
    await user.type(within(dialog).getByLabelText("Name"), "usb");
    await user.type(within(dialog).getByLabelText("Folder"), "/mnt/usb");
    const check = within(dialog).getByRole("button", { name: "Check folder" });
    const status = within(dialog).getByRole("status");
    expect(status).not.toContainElement(check);
    await user.click(check);
    await waitFor(() => expect(status).toHaveTextContent("Folder is writable"));
  });

  it("locks the form and says what is going on while saving", async () => {
    const user = setupUser();
    let finish: (v: ReturnType<typeof saved>) => void = () => {};
    const docker = fakeDocker({
      saveStorage: vi.fn(() => new Promise<ReturnType<typeof saved>>((resolve) => (finish = resolve))),
    });
    renderCard(docker);
    await user.click(await screen.findByRole("button", { name: "Edit storage hetzner" }));
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
    await user.click(await screen.findByRole("button", { name: "Edit storage disk" }));
    const folder = (await screen.findByLabelText("Folder")) as HTMLInputElement;
    expect(folder).toHaveFocus();
    expect(folder.selectionStart).toBe("/mnt/usb".length);
    expect(folder.selectionEnd).toBe("/mnt/usb".length);
  });

  it("returns focus to the button that opened a dialog", async () => {
    const user = setupUser();
    renderCard(fakeDocker());
    const add = await screen.findByRole("button", { name: /Add storage/ });
    await user.click(add);
    await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(add).toHaveFocus());

    const edit = screen.getByRole("button", { name: "Edit storage disk" });
    await user.click(edit);
    await screen.findByRole("dialog");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(edit).toHaveFocus());

    // A removed storage takes its button along: focus goes to Add storage.
    await user.click(screen.getByRole("button", { name: "Remove storage disk" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(within(confirm).getByText("disk")).toHaveClass("[overflow-wrap:anywhere]");
    await user.click(within(confirm).getByRole("button", { name: "Remove storage" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Remove storage disk" })).not.toBeInTheDocument());
    expect(add).toHaveFocus();
  });
});

describe("StoragesCard layout", () => {
  it("gives the name the width on phones and keeps chips whole", async () => {
    renderCard(fakeDocker());
    const name = await screen.findByText("hetzner");
    // The Type column is hidden below sm; its badge moves under the name.
    expect(screen.getByRole("columnheader", { name: "Type" })).toHaveClass("hidden", "sm:table-cell");
    expect(within(name.closest("td")!).getByText("S3-compatible").parentElement).toHaveClass("sm:hidden");
    expect(screen.getAllByRole("link", { name: "Shop" })[0].closest(".whitespace-nowrap")).not.toBeNull();
  });

  it("keeps the header actions beside a shrinking description", async () => {
    renderCard(fakeDocker());
    const title = await screen.findByText("Storages");
    expect(title.parentElement).toHaveClass("flex-1", "basis-72", "min-w-0");
  });
});
