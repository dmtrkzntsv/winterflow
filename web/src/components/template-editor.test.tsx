import { useState } from "react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { TemplateEditor } from "@/components/template-editor";
import { bodyFromDraft, draftFromBody, type TemplateDraft } from "@/lib/template-draft";

vi.mock("@/components/code-editor", () => ({
  CodeEditor: ({ value, onChange, filename }: { value: string; onChange?: (v: string) => void; filename?: string }) => (
    <textarea aria-label={`Content of ${filename || "file"}`} value={value} onChange={(e) => onChange?.(e.target.value)} />
  ),
}));

const COMPOSE = "services: {}\nx-winterflow:\n  backups:\n    - { name: db, type: sqlite, volume: data, path: a.db }\n";

const changed = vi.fn<(d: TemplateDraft) => void>();
// latest is the draft as the editor last changed it.
const latest = () => changed.mock.lastCall![0];

function Harness({ initial }: { initial: TemplateDraft }) {
  const [draft, setDraft] = useState(initial);
  return (
    <TemplateEditor
      draft={draft}
      onChange={(d) => {
        setDraft(d);
        changed(d);
      }}
    />
  );
}

function start(files: Record<string, string> = { "compose.yaml": COMPOSE }) {
  const draft = draftFromBody("shop", {
    files,
    parameters: [{ name: "NAME", default: "shop" }],
    app_name: "${NAME}",
    icon: "",
    color: "",
    ingress: null,
    backups: null,
  });
  render(<Harness initial={draft} />);
  return userEvent.setup();
}

describe("TemplateEditor", () => {
  it("edits parameters: a secret loses its default, a pattern JS can't read is flagged", async () => {
    const user = start();
    await user.type(screen.getByLabelText("Default of NAME"), "2");
    expect(latest().parameters[0].default).toBe("shop2");
    await user.click(screen.getByRole("button", { name: "Details of NAME" }));
    await user.click(screen.getByRole("checkbox", { name: "NAME is required" }));
    await user.click(screen.getByLabelText("Pattern of NAME"));
    await user.paste("[[:alpha:]]+");
    expect(screen.getByText(/The browser can't check this pattern/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("Description of NAME"), "Instance name");
    await user.click(screen.getByRole("checkbox", { name: "NAME is secret" }));
    expect(screen.getByLabelText("Default of NAME")).toBeDisabled();
    expect(bodyFromDraft(latest()).parameters).toEqual([
      { name: "NAME", secret: true, required: true, pattern: "[[:alpha:]]+", description: "Instance name" },
    ]);
    await user.click(screen.getByRole("button", { name: "Remove NAME" }));
    expect(screen.getByText("No parameters yet.")).toBeInTheDocument();
  });

  it("shows Name and Default, the rest behind Details", async () => {
    const user = start();
    const head = screen.getByRole("group", { name: "Parameter columns" });
    expect(within(head).getAllByText(/./).map((e) => e.textContent)).toEqual(["Name", "Default"]);
    const toggle = screen.getByRole("button", { name: "Details of NAME" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByLabelText("Pattern of NAME")).not.toBeInTheDocument();
    await user.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByLabelText("Pattern of NAME")).toBeInTheDocument();
    expect(screen.getByLabelText("Description of NAME")).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "NAME is secret" })).toHaveClass("rounded-[4px]");
    expect(screen.getByRole("checkbox", { name: "NAME is required" })).toHaveClass("rounded-[4px]");
    await user.click(toggle);
    expect(screen.queryByLabelText("Pattern of NAME")).not.toBeInTheDocument();
  });

  it("tabs through a parameter row in the order it reads, on a phone too (no CSS reordering)", async () => {
    const user = start();
    await user.click(screen.getByRole("button", { name: "Details of NAME" }));
    const row = screen.getByLabelText("Parameter name").parentElement!;
    const controls = [
      screen.getByLabelText("Parameter name"),
      screen.getByLabelText("Default of NAME"),
      screen.getByRole("button", { name: "Details of NAME" }),
      screen.getByRole("button", { name: "Remove NAME" }),
      screen.getByLabelText("Description of NAME"),
    ];
    for (let i = 1; i < controls.length; i++) {
      expect(controls[i - 1].compareDocumentPosition(controls[i]) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    }
    for (const el of row.querySelectorAll("*")) expect(el.className.toString()).not.toMatch(/(^|\s)(max-sm:)?order-/);
  });

  it("says what a closed row holds, and opens a new parameter's details", async () => {
    const draft = draftFromBody("shop", {
      files: { "compose.yaml": COMPOSE },
      parameters: [{ name: "KEY", secret: true, required: true, pattern: "^x$" }],
      app_name: "shop",
      icon: "",
      color: "",
      ingress: null,
      backups: null,
    });
    render(<Harness initial={draft} />);
    const user = userEvent.setup();
    expect(screen.getByText("Secret · Required · Pattern")).toBeInTheDocument();
    expect(screen.getByLabelText("Default of KEY")).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Details of KEY" }));
    expect(screen.queryByText("Secret · Required · Pattern")).not.toBeInTheDocument();

    await user.click(screen.getAllByRole("button", { name: /Add parameter/ })[0]);
    expect(screen.getByRole("button", { name: "Details of parameter" })).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByLabelText("Pattern of parameter")).toBeInTheDocument();
  });

  it("has placeholders that fit and say what goes there", async () => {
    const user = start();
    expect(screen.getByLabelText("Template name")).toHaveAttribute("placeholder", "my-stack");
    expect(screen.getByLabelText("App name")).toHaveAttribute("placeholder", "my-app or ${NAME}");
    expect(screen.getByLabelText("Default of NAME")).toHaveAttribute("placeholder", "Default");
    await user.click(screen.getByRole("button", { name: "Details of NAME" }));
    expect(screen.getByLabelText("Pattern of NAME")).toHaveAttribute("placeholder", "e.g. ^[a-z0-9-]+$");
  });

  it("edits the app record and the files", async () => {
    const user = start();
    await user.clear(screen.getByLabelText("Template name"));
    await user.type(screen.getByLabelText("Template name"), "shop2");
    await user.clear(screen.getByLabelText("App name"));
    await user.type(screen.getByLabelText("App name"), "Shop");
    await user.click(screen.getAllByRole("button", { name: /Add file/ })[0]);
    expect(screen.queryByRole("button", { name: /Secret/ })).not.toBeInTheDocument();
    expect(latest()).toMatchObject({ name: "shop2", appName: "Shop" });
    expect(latest().files).toHaveLength(2);
  });

  it("keeps Domains & Routing behind More options until it has a row, and edits the rows", async () => {
    const user = start();
    expect(screen.queryByRole("heading", { name: "Domains & Routing" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "More options" }));
    expect(screen.getByRole("heading", { name: "Domains & Routing" })).toBeInTheDocument();
    expect(screen.getByText("No hostnames.")).toBeInTheDocument();
    await user.click(screen.getAllByRole("button", { name: "Add hostname" })[0]);
    expect(screen.queryByRole("button", { name: "More options" })).not.toBeInTheDocument();
    await user.click(screen.getByLabelText("Host"));
    await user.paste("${NAME}.example.com");
    await user.type(screen.getByLabelText("Port of ${NAME}.example.com"), "8080");
    await user.type(screen.getByLabelText("Service of ${NAME}.example.com"), "app");
    expect(bodyFromDraft(latest()).ingress).toEqual([{ host: "${NAME}.example.com", service: "app", port: 8080 }]);
    await user.click(screen.getByRole("button", { name: "Remove ${NAME}.example.com" }));
    expect(latest().ingress).toEqual([]);
  });

  it("offers one backup default per recipe backup", async () => {
    const user = start();
    await user.click(screen.getByRole("switch", { name: "Back up db" }));
    await user.type(screen.getByLabelText("Storage of db"), "tenants");
    expect(bodyFromDraft(latest()).backups).toEqual({ db: { enabled: true, storage: "tenants" } });
  });

  it("has no Backup defaults when the compose file declares no backups", () => {
    start({ "compose.yaml": "services: {}\n" });
    expect(screen.queryByRole("heading", { name: "Backup defaults" })).not.toBeInTheDocument();
  });
});
