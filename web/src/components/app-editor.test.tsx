import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { AppEditor } from "@/components/app-editor";
import { emptyEditorState } from "@/lib/app-editor-io";
import type { AppEditorState } from "@/types/app-config";

vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServer: null, loading: true }) }));
vi.mock("@/components/code-editor", () => ({ CodeEditor: () => <textarea aria-label="code" /> }));
vi.mock("@/components/image-tag-picker", () => ({ ImageTagPicker: () => null }));

function renderEditor(state: AppEditorState) {
  return render(<AppEditor state={state} onChange={() => {}} />);
}

describe("AppEditor storage variables", () => {
  it("lists the referenced storage variables read-only, secrets masked", () => {
    const state: AppEditorState = {
      ...emptyEditorState(),
      storageVariables: [
        { name: "WF_STORAGE_HETZNER_BUCKET", storage: "hetzner", secret: false },
        { name: "WF_STORAGE_HETZNER_SECRET_ACCESS_KEY", storage: "hetzner", secret: true },
      ],
    };
    renderEditor(state);

    const rows = within(screen.getByRole("list", { name: "Storage variables" })).getAllByRole("listitem");
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent("WF_STORAGE_HETZNER_BUCKET");
    expect(rows[0]).toHaveTextContent("provided by storage hetzner");
    expect(rows[0]).not.toHaveTextContent("<encrypted>");
    expect(rows[1]).toHaveTextContent("<encrypted>");
    // Not editable: no inputs in the rows.
    expect(within(rows[0]).queryByRole("textbox")).not.toBeInTheDocument();
    // They count as variables: no empty state.
    expect(screen.queryByText("No variables yet.")).not.toBeInTheDocument();
  });

  it("keeps the empty state without any variable", () => {
    renderEditor(emptyEditorState());
    expect(screen.getByText("No variables yet.")).toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "Storage variables" })).not.toBeInTheDocument();
  });
});

describe("AppEditor files", () => {
  it("edits files through the shared files section", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<AppEditor state={emptyEditorState()} onChange={onChange} />);
    await user.click(screen.getAllByRole("button", { name: /Add file/ })[0]);
    const next = onChange.mock.lastCall![0] as AppEditorState;
    expect(next.config.files).toHaveLength(2);
    expect(Object.keys(next.files)).toHaveLength(2);
  });
});
