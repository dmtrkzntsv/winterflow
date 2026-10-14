import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { VariablesEditor } from "@/components/variables-editor";
import { emptyEditorState } from "@/lib/app-editor-io";
import type { FieldErrors } from "@/lib/field-errors";
import type { AppEditorState } from "@/types/app-config";

// withStoredSecret is an app whose TOKEN the agent holds (read back as the
// placeholder).
function withStoredSecret(): AppEditorState {
  const s = emptyEditorState();
  return {
    ...s,
    config: { ...s.config, variables: [{ id: "t", name: "TOKEN", is_encrypted: true, storedAs: "TOKEN" }] },
    variables: { t: "<encrypted>" },
  };
}

function Harness({
  errors = {},
  emptyNote,
  start = emptyEditorState,
}: {
  errors?: FieldErrors;
  emptyNote?: boolean;
  start?: () => AppEditorState;
}) {
  const [state, setState] = useState<AppEditorState>(start);
  return (
    <>
      <VariablesEditor
        state={state}
        onChange={setState}
        shared={[{ name: "SMTP_HOST", scope: "org" }]}
        errors={errors}
        emptyNote={emptyNote}
      />
      <output aria-label="state">{JSON.stringify({ vars: state.config.variables, values: state.variables })}</output>
    </>
  );
}
const state = () => JSON.parse(screen.getByRole("status", { name: "state" }).textContent ?? "{}");

describe("VariablesEditor", () => {
  it("adds, fills, marks secret, reveals and removes a variable", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    expect(screen.getByText("No variables yet.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Add variable/ }));
    await user.type(screen.getByRole("textbox", { name: "Variable name" }), "SMTP_HOST");
    expect(screen.getByText("overrides org value")).toBeInTheDocument();
    await user.type(screen.getByRole("textbox", { name: "Value of SMTP_HOST" }), "mail");
    await user.click(screen.getByRole("button", { name: /Secret/ }));
    expect(screen.getByLabelText("Value of SMTP_HOST")).toHaveAttribute("type", "password");
    await user.click(screen.getByRole("button", { name: "Show value" }));
    expect(screen.getByLabelText("Value of SMTP_HOST")).toHaveAttribute("type", "text");
    expect(state().vars[0]).toMatchObject({ name: "SMTP_HOST", is_encrypted: true });
    expect(Object.values(state().values)).toEqual(["mail"]);
    await user.click(screen.getByRole("button", { name: "Remove SMTP_HOST" }));
    expect(state().vars).toEqual([]);
  });

  it("gives the name a whole line on a phone, the value beside it from sm up", () => {
    render(<Harness start={withStoredSecret} />);
    const name = screen.getByRole("textbox", { name: "Variable name" });
    expect(name).toHaveClass("w-full", "sm:w-56");
    expect(name.parentElement).toHaveClass("flex-wrap", "sm:flex-nowrap");
  });

  it("never shows a stored secret or its placeholder: Replace opens an empty field", async () => {
    const user = userEvent.setup();
    render(<Harness start={withStoredSecret} />);
    expect(screen.getByText("Secret · set")).toBeInTheDocument();
    expect(screen.queryByDisplayValue("<encrypted>")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Value of TOKEN")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Show value" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Replace TOKEN" }));
    const field = screen.getByLabelText("Value of TOKEN");
    expect(field).toHaveValue("");
    expect(field).toHaveFocus();
    expect(field).toHaveAttribute("type", "password");
    // Show value only once there is a new value to show.
    expect(screen.queryByRole("button", { name: "Show value" })).not.toBeInTheDocument();
    await user.type(field, "hunter2");
    await user.click(screen.getByRole("button", { name: "Show value" }));
    expect(field).toHaveAttribute("type", "text");
    expect(state().values).toEqual({ t: "hunter2" });

    // Keep current goes back to the stored value.
    await user.click(screen.getByRole("button", { name: "Keep current" }));
    expect(screen.getByText("Secret · set")).toBeInTheDocument();
    expect(state().values).toEqual({ t: "<encrypted>" });
  });

  it("says an empty replacement keeps the stored value", async () => {
    const user = userEvent.setup();
    render(<Harness start={withStoredSecret} />);
    await user.click(screen.getByRole("button", { name: "Replace TOKEN" }));
    expect(screen.getByLabelText("Value of TOKEN")).toHaveAttribute("placeholder", "Leave empty to keep the current value");
  });

  it("empties a renamed stored secret: it needs a new value", async () => {
    const user = userEvent.setup();
    render(<Harness start={withStoredSecret} />);
    await user.type(screen.getByRole("textbox", { name: "Variable name" }), "2");
    expect(state().vars[0]).toMatchObject({ name: "TOKEN2", storedAs: "TOKEN" });
    expect(state().values).toEqual({ t: "" });
    const field = screen.getByLabelText("Value of TOKEN2");
    expect(field).toHaveValue("");
    expect(field).toHaveAttribute("placeholder", "value");
    expect(screen.queryByRole("button", { name: "Keep current" })).not.toBeInTheDocument();
    // Renamed back while still empty, it keeps the stored value again.
    await user.type(screen.getByRole("textbox", { name: "Variable name" }), "{Backspace}");
    expect(screen.getByLabelText("Value of TOKEN")).toHaveAttribute("placeholder", "Leave empty to keep the current value");
    expect(screen.getByRole("button", { name: "Keep current" })).toBeInTheDocument();
  });

  it("empties a stored secret turned plain, so the placeholder never shows as text", async () => {
    const user = userEvent.setup();
    render(<Harness start={withStoredSecret} />);
    await user.click(screen.getByRole("button", { name: /Secret$/ }));
    expect(screen.getByLabelText("Value of TOKEN")).toHaveValue("");
    expect(state().values).toEqual({ t: "" });
    expect(state().vars[0]).toMatchObject({ is_encrypted: false });
  });

  it("shows a value's message under it", async () => {
    const user = userEvent.setup();
    render(<Harness start={withStoredSecret} errors={{ "var:t:value": "A value can't start with <encrypted>." }} />);
    await user.click(screen.getByRole("button", { name: "Replace TOKEN" }));
    expect(screen.getByLabelText("Value of TOKEN")).toHaveAccessibleDescription("A value can't start with <encrypted>.");
  });

  it("shows a variable's message under it", async () => {
    const user = userEvent.setup();
    const { rerender } = render(<Harness />);
    await user.click(screen.getByRole("button", { name: /Add variable/ }));
    const id = state().vars[0].id as string;
    rerender(<Harness errors={{ [`var:${id}`]: "Name this variable." }} />);
    const name = screen.getByRole("textbox", { name: "Variable name" });
    expect(name).toHaveAttribute("aria-invalid", "true");
    expect(name).toHaveAccessibleDescription("Name this variable.");
  });

  it("can leave the empty note to the caller", () => {
    render(<Harness emptyNote={false} />);
    expect(screen.queryByText("No variables yet.")).not.toBeInTheDocument();
  });
});
