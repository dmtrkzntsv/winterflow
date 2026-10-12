import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

vi.mock("@/lib/ecies", () => ({
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));

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

function renderDialog(mode: DialogMode, serverId?: string) {
  const onSave = vi.fn().mockResolvedValue(undefined);
  render(<SharedVariableDialog open mode={mode} keys={KEYS} serverId={serverId} onOpenChange={() => {}} onSave={onSave} />);
  return onSave;
}

describe("SharedVariableDialog", () => {
  it("adds a plain variable with reload off by default", async () => {
    const user = userEvent.setup();
    const onSave = renderDialog({ kind: "create" });
    expect(screen.getByRole("checkbox", { name: "Reload affected apps now" })).not.toBeChecked();
    await user.type(screen.getByLabelText("Name"), "SMTP_HOST");
    await user.type(screen.getByLabelText("Value"), "mail.example");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({ name: "SMTP_HOST", is_secret: false, value: "mail.example", description: "" });
  });

  it("encrypts a new org secret for every keyed server and says where it goes", async () => {
    const user = userEvent.setup();
    const onSave = renderDialog({ kind: "create" });
    await user.type(screen.getByLabelText("Name"), "SMTP_PASS");
    await user.click(screen.getByRole("switch", { name: "Secret" }));
    await user.type(screen.getByLabelText("Value"), "s3cret");
    expect(screen.getByText("Will be pushed to 2 servers (1 pending, no key yet)")).toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: "Reload affected apps now" }));
    await user.click(screen.getByRole("button", { name: "Save and push" }));
    expect(onSave).toHaveBeenCalledWith({
      name: "SMTP_PASS", is_secret: true, description: "", reload: true,
      ciphertexts: { a: "ct(KA):s3cret", b: "ct(KB):s3cret" },
    });
  });

  it("encrypts a server secret for that server only", async () => {
    const user = userEvent.setup();
    const onSave = renderDialog({ kind: "create" }, "b");
    await user.type(screen.getByLabelText("Name"), "LOCAL_KEY");
    await user.click(screen.getByRole("switch", { name: "Secret" }));
    await user.type(screen.getByLabelText("Value"), "k");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({ name: "LOCAL_KEY", is_secret: true, description: "", ciphertexts: { b: "ct(KB):k" } });
  });

  it("locks an existing secret: description edits keep the value", async () => {
    const user = userEvent.setup();
    const onSave = renderDialog({ kind: "edit", variable: SECRET });
    expect(screen.getByLabelText("Name")).toBeDisabled();
    expect(screen.getByRole("switch", { name: "Secret" })).toBeDisabled();
    expect(screen.queryByLabelText("Value")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({ name: "SMTP_PASS", is_secret: true, description: "old" });
  });

  it("replaces an existing secret's value everywhere", async () => {
    const user = userEvent.setup();
    const onSave = renderDialog({ kind: "edit", variable: SECRET });
    await user.click(screen.getByRole("button", { name: "Replace value" }));
    await user.type(screen.getByLabelText("Value"), "n3w");
    await user.click(screen.getByRole("button", { name: "Save and push" }));
    expect(onSave).toHaveBeenCalledWith({
      name: "SMTP_PASS", is_secret: true, description: "old",
      ciphertexts: { a: "ct(KA):n3w", b: "ct(KB):n3w" },
    });
  });

  it("provides a missing value for the missing servers only", async () => {
    const user = userEvent.setup();
    const onSave = renderDialog({ kind: "provide", variable: SECRET });
    expect(screen.getByText("Encrypts the value for: beta")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Value"), "s3cret");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({ name: "SMTP_PASS", is_secret: true, description: "old", ciphertexts: { b: "ct(KB):s3cret" } });
  });

  it("rejects a reserved name before saving", async () => {
    const user = userEvent.setup();
    renderDialog({ kind: "create" });
    await user.type(screen.getByLabelText("Name"), "WF_X");
    expect(screen.getByText("WF_X is reserved")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("provide names only keyed servers and lists keyless ones as pending", async () => {
    const user = userEvent.setup();
    const onSave = renderDialog({ kind: "provide", variable: { ...SECRET, missing_on: ["b", "p"] } });
    expect(screen.getByText("Encrypts the value for: beta. Pending (no key yet): new-box")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Value"), "x");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({ name: "SMTP_PASS", is_secret: true, description: "old", ciphertexts: { b: "ct(KB):x" } });
  });

  it("disables Save when no target server has a key", async () => {
    const user = userEvent.setup();
    renderDialog({ kind: "provide", variable: { ...SECRET, missing_on: ["p"] } });
    await user.type(screen.getByLabelText("Value"), "x");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("disables Save for an org secret when no server is keyed", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(
      <SharedVariableDialog open mode={{ kind: "create" }} keys={{ servers: [], pending_servers: KEYS.pending_servers }} onOpenChange={() => {}} onSave={onSave} />,
    );
    await user.type(screen.getByLabelText("Name"), "A");
    await user.click(screen.getByRole("switch", { name: "Secret" }));
    await user.type(screen.getByLabelText("Value"), "x");
    expect(screen.getByRole("button", { name: "Save and push" })).toBeDisabled();
  });
});
