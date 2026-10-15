import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { TemplateEditor } from "@/components/template-editor";
import { bodyFromDraft, draftFromBody, type TemplateDraft } from "@/lib/template-draft";

vi.mock("@/components/code-editor", () => ({
  CodeEditor: ({ value, onChange, filename }: { value: string; onChange?: (v: string) => void; filename?: string }) => (
    <textarea aria-label={`Content of ${filename || "file"}`} value={value} onChange={(e) => onChange?.(e.target.value)} />
  ),
}));

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
      detected={<p>the detected panel</p>}
    />
  );
}

function start() {
  const draft = draftFromBody("shop", {
    files: { "compose.yaml": "services: {}\n" },
    summary: "A shop",
    description: "",
    icon: "",
    color: "",
  });
  render(<Harness initial={draft} />);
  return userEvent.setup();
}

describe("TemplateEditor", () => {
  it("edits the name, the look and the files", async () => {
    const user = start();
    await user.clear(screen.getByLabelText("Template name"));
    await user.type(screen.getByLabelText("Template name"), "shop2");
    await user.type(screen.getByLabelText("Summary"), " for teams");
    await user.type(screen.getByLabelText("Description"), "Sells things.");
    await user.click(screen.getAllByRole("button", { name: /Add file/ })[0]);
    // A template holds no secret: files have no secret toggle.
    expect(screen.queryByRole("button", { name: /Secret/ })).not.toBeInTheDocument();
    expect(latest()).toMatchObject({ name: "shop2", summary: "A shop for teams", description: "Sells things." });
    expect(latest().files).toHaveLength(2);
    await user.clear(screen.getByLabelText("Content of compose.yaml"));
    await user.click(screen.getByLabelText("Content of compose.yaml"));
    await user.paste("services: { app: { image: x } }");
    expect(bodyFromDraft(latest()).files?.["compose.yaml"]).toBe("services: { app: { image: x } }");
  });

  it("has placeholders that say what goes there, and shows the Detected panel after the files", () => {
    start();
    expect(screen.getByLabelText("Template name")).toHaveAttribute("placeholder", "my-stack");
    expect(screen.getByLabelText("Summary")).toHaveAttribute("placeholder", "One line about the app");
    // British spelling, like the rest of the UI.
    expect(screen.getByText("Colour")).toBeInTheDocument();
    const files = screen.getByLabelText("Content of compose.yaml");
    const panel = screen.getByText("the detected panel");
    expect(files.compareDocumentPosition(panel) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});
