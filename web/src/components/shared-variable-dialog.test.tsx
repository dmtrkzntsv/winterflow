import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

vi.mock("@/lib/ecies", () => ({
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import { SharedVariableDialog, type DialogMode } from "@/components/shared-variable-dialog";
import type { EncryptionKeys, SharedVariable } from "@/lib/shared-variables";

const KEYS: EncryptionKeys = {
  servers: [
    { server_id: "a", name: "alpha", public_key: "KA" },
    { server_id: "b", name: "beta", public_key: "KB" },
  ],
  pending_servers: [{ server_id: "p", name: "new-box" }],
};
const SECRET: SharedVariable = {
  id: "v2", name: "SMTP_PASS", scope: "org", is_secret: true, value: "<encrypted>",
  description: "old", updated_at: "2026-10-11T00:00:00Z", updated_by: "u1", missing_on: ["b"],
};
const BETA = { id: "b", name: "beta" };

function renderDialog(
  mode: DialogMode,
  opts: { chooseScope?: boolean; keys?: EncryptionKeys; server?: { id: string; name: string } | null } = {},
) {
  const onSave = vi.fn().mockResolvedValue(undefined);
  const onOpenChange = vi.fn();
  render(
    <SharedVariableDialog
      open
      mode={mode}
      keys={opts.keys ?? KEYS}
      server={opts.server === undefined ? BETA : opts.server}
      chooseScope={opts.chooseScope ?? true}
      onOpenChange={onOpenChange}
      onSave={onSave}
    />,
  );
  return { onSave, onOpenChange };
}

const setupUser = () => userEvent.setup({ delay: null });

describe("SharedVariableDialog", () => {
  it("adds a plain org-wide variable, reload off by default", async () => {
    const user = setupUser();
    const { onSave, onOpenChange } = renderDialog({ kind: "create" });
    expect(screen.getByRole("checkbox", { name: "Reload apps that use it now" })).not.toBeChecked();
    await user.type(screen.getByLabelText("Name"), "SMTP_HOST");
    await user.type(screen.getByLabelText("Value"), "mail.example");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({ name: "SMTP_HOST", is_secret: false, value: "mail.example", description: "" }, "org");
    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
  });

  it("asks for the Secret switch before the Value", () => {
    renderDialog({ kind: "create" });
    const name = screen.getByLabelText("Name");
    const secret = screen.getByRole("switch", { name: "Secret" });
    const value = screen.getByLabelText("Value");
    expect(name.compareDocumentPosition(secret) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(secret.compareDocumentPosition(value) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("never encrypts a server-only secret for every server when there is no server", async () => {
    const user = setupUser();
    const { onSave } = renderDialog(
      { kind: "edit", variable: { ...SECRET, scope: "server", missing_on: [], description: "" } },
      { server: null },
    );
    await user.click(screen.getByRole("button", { name: "Replace value" }));
    await user.type(screen.getByLabelText("Value"), "n3w");
    expect(screen.getByText("This server has no public key yet, so it can't receive secrets.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(onSave).not.toHaveBeenCalled();
  });

  it("keeps the description and the scope under Advanced", async () => {
    const user = setupUser();
    const { onSave } = renderDialog({ kind: "create" });
    expect(screen.queryByLabelText("Description")).not.toBeInTheDocument();
    expect(screen.queryByRole("radiogroup", { name: "Applies to" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Advanced" }));
    expect(screen.getByRole("radio", { name: "All servers" })).toBeChecked();
    await user.type(screen.getByLabelText("Name"), "LOCAL_DIR");
    await user.type(screen.getByLabelText("Value"), "/srv");
    await user.type(screen.getByLabelText("Description"), "Data folder");
    await user.click(screen.getByRole("radio", { name: "beta only" }));
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith(
      { name: "LOCAL_DIR", is_secret: false, value: "/srv", description: "Data folder" },
      "server",
    );
  });

  it("offers no scope choice when there is none to make (standalone)", async () => {
    const user = setupUser();
    renderDialog({ kind: "create" }, { chooseScope: false });
    await user.click(screen.getByRole("button", { name: "Advanced" }));
    expect(screen.queryByRole("radiogroup", { name: "Applies to" })).not.toBeInTheDocument();
  });

  it("encrypts a new org secret for every keyed server and says where it goes", async () => {
    const user = setupUser();
    const { onSave } = renderDialog({ kind: "create" });
    await user.type(screen.getByLabelText("Name"), "SMTP_PASS");
    await user.type(screen.getByLabelText("Value"), "s3cret");
    await user.click(screen.getByRole("switch", { name: "Secret" }));
    expect(screen.getByText("Will be pushed to 2 servers (1 pending, no key yet)")).toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: "Reload apps that use it now" }));
    await user.click(screen.getByRole("button", { name: "Save and push" }));
    expect(onSave).toHaveBeenCalledWith(
      {
        name: "SMTP_PASS", is_secret: true, description: "", reload: true,
        ciphertexts: { a: "ct(KA):s3cret", b: "ct(KB):s3cret" },
      },
      "org",
    );
  });

  it("encrypts a server-only secret for that server only", async () => {
    const user = setupUser();
    const { onSave } = renderDialog({ kind: "create" });
    await user.type(screen.getByLabelText("Name"), "LOCAL_KEY");
    await user.type(screen.getByLabelText("Value"), "k");
    await user.click(screen.getByRole("switch", { name: "Secret" }));
    await user.click(screen.getByRole("button", { name: "Advanced" }));
    await user.click(screen.getByRole("radio", { name: "beta only" }));
    expect(screen.getByText("Will be pushed to 1 server")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith(
      { name: "LOCAL_KEY", is_secret: true, description: "", ciphertexts: { b: "ct(KB):k" } },
      "server",
    );
  });

  it("locks an existing secret: description edits keep the value", async () => {
    const user = setupUser();
    const { onSave } = renderDialog({ kind: "edit", variable: SECRET });
    expect(screen.getByLabelText("Name")).toBeDisabled();
    expect(screen.getByRole("switch", { name: "Secret" })).toBeDisabled();
    expect(screen.queryByLabelText("Value")).not.toBeInTheDocument();
    // The description was set: Advanced starts open; the scope is said, not offered.
    expect(screen.getByLabelText("Description")).toHaveValue("old");
    expect(screen.getByText("Applies to all servers.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({ name: "SMTP_PASS", is_secret: true, description: "old" }, "org");
  });

  it("replaces an existing secret's value everywhere", async () => {
    const user = setupUser();
    const { onSave } = renderDialog({ kind: "edit", variable: SECRET });
    await user.click(screen.getByRole("button", { name: "Replace value" }));
    await user.type(screen.getByLabelText("Value"), "n3w");
    await user.click(screen.getByRole("button", { name: "Save and push" }));
    expect(onSave).toHaveBeenCalledWith(
      { name: "SMTP_PASS", is_secret: true, description: "old", ciphertexts: { a: "ct(KA):n3w", b: "ct(KB):n3w" } },
      "org",
    );
  });

  it("edits a server-only secret for its server only", async () => {
    const user = setupUser();
    const { onSave } = renderDialog({ kind: "edit", variable: { ...SECRET, scope: "server", missing_on: [] } });
    expect(screen.getByText("Applies to beta only.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Replace value" }));
    await user.type(screen.getByLabelText("Value"), "n3w");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith(
      { name: "SMTP_PASS", is_secret: true, description: "old", ciphertexts: { b: "ct(KB):n3w" } },
      "server",
    );
  });

  it("provides a missing value for the missing servers only", async () => {
    const user = setupUser();
    const { onSave } = renderDialog({ kind: "provide", variable: SECRET });
    expect(screen.getByText("Encrypts the value for: beta")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Advanced" })).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("Value"), "s3cret");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith(
      { name: "SMTP_PASS", is_secret: true, description: "old", ciphertexts: { b: "ct(KB):s3cret" } },
      "org",
    );
  });

  it("names keyless servers as pending in a provide", () => {
    renderDialog({ kind: "provide", variable: { ...SECRET, missing_on: ["b", "p"] } });
    expect(screen.getByText("Encrypts the value for: beta. Pending (no key yet): new-box")).toBeInTheDocument();
  });

  it("disables Save when no target server has a key", async () => {
    const user = setupUser();
    renderDialog({ kind: "provide", variable: { ...SECRET, missing_on: ["p"] } });
    await user.type(screen.getByLabelText("Value"), "x");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("disables Save for an org secret when no server is keyed", async () => {
    const user = setupUser();
    renderDialog({ kind: "create" }, { keys: { servers: [], pending_servers: KEYS.pending_servers } });
    await user.type(screen.getByLabelText("Name"), "A");
    await user.type(screen.getByLabelText("Value"), "x");
    await user.click(screen.getByRole("switch", { name: "Secret" }));
    expect(screen.getByRole("button", { name: "Save and push" })).toBeDisabled();
  });

  it("rejects a reserved name before saving", async () => {
    const user = setupUser();
    renderDialog({ kind: "create" });
    await user.type(screen.getByLabelText("Name"), "WF_X");
    expect(screen.getByText("WF_X is reserved")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });
});
