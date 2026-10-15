import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { DeclaredVariablesForm } from "@/components/declared-variables-form";
import { emptyEditorState } from "@/lib/app-editor-io";
import type { FieldErrors } from "@/lib/field-errors";
import type { AppEditorState, DeclaredVariable } from "@/types/app-config";

const DECLARED: DeclaredVariable[] = [
  { name: "BASE_URL", required: true, encrypted: false, pattern: "https?://.+", description: "Public URL the browser uses", default: "http://localhost:8080", services: ["app"] },
  { name: "SESSION_SECRET", required: true, encrypted: true, default: null, services: ["app"] },
];

function Harness({ start, errors }: { start: AppEditorState; errors?: FieldErrors }) {
  const [state, setState] = useState(start);
  return (
    <>
      <DeclaredVariablesForm state={state} onChange={setState} errors={errors} />
      <output aria-label="state">{JSON.stringify({ vars: state.config.variables, values: state.variables })}</output>
    </>
  );
}
const state = () => JSON.parse(screen.getByRole("status", { name: "state" }).textContent ?? "{}");

describe("DeclaredVariablesForm", () => {
  it("shows each variable with its description, default as placeholder and required marker", () => {
    render(<Harness start={{ ...emptyEditorState(), declared: DECLARED }} />);
    const base = screen.getByLabelText(/BASE_URL/);
    expect(base).toHaveAttribute("placeholder", "http://localhost:8080");
    expect(base).toHaveAttribute("type", "text");
    expect(screen.getByText("Public URL the browser uses")).toBeInTheDocument();
    expect(screen.getAllByLabelText("required")).toHaveLength(2);
    expect(screen.getByLabelText(/SESSION_SECRET/)).toHaveAttribute("type", "password");
  });

  it("hints at a pattern mismatch as you type, without flagging the field invalid", async () => {
    const user = userEvent.setup();
    render(<Harness start={{ ...emptyEditorState(), declared: DECLARED }} />);
    await user.type(screen.getByLabelText(/BASE_URL/), "notes");
    const hint = "May not match https?://.+; the server checks it on save.";
    expect(screen.getByText(hint)).toBeInTheDocument();
    expect(screen.getByLabelText(/BASE_URL/)).not.toHaveAttribute("aria-invalid");
    expect(screen.getByLabelText(/BASE_URL/)).toHaveAccessibleDescription(hint);
    await user.clear(screen.getByLabelText(/BASE_URL/));
    await user.type(screen.getByLabelText(/BASE_URL/), "https://notes.example.com");
    expect(screen.queryByText(/May not match/)).not.toBeInTheDocument();
    await user.type(screen.getByLabelText(/SESSION_SECRET/), "s3cret");
    expect(state().vars).toMatchObject([
      { name: "BASE_URL", is_encrypted: false },
      { name: "SESSION_SECRET", is_encrypted: true },
    ]);
    expect(Object.values(state().values)).toEqual(["https://notes.example.com", "s3cret"]);
  });

  it("masks a stored secret and replaces it on request", async () => {
    const user = userEvent.setup();
    const start: AppEditorState = {
      ...emptyEditorState(),
      declared: DECLARED,
      config: { ...emptyEditorState().config, variables: [{ id: "s", name: "SESSION_SECRET", is_encrypted: true, storedAs: "SESSION_SECRET" }] },
      variables: { s: "<encrypted>" },
      declaredRows: { SESSION_SECRET: "s" },
    };
    render(<Harness start={start} />);
    expect(screen.getByText("Secret · set")).toBeInTheDocument();
    expect(screen.queryByDisplayValue("<encrypted>")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Replace SESSION_SECRET" }));
    expect(screen.getByLabelText(/SESSION_SECRET/)).toHaveAttribute("placeholder", "Leave empty to keep the current value");
  });

  it("shows a refused save's message and renders nothing without declared variables", () => {
    const { unmount } = render(<Harness start={{ ...emptyEditorState(), declared: DECLARED }} errors={{ "decl:BASE_URL": "Enter a value: this variable is required." }} />);
    expect(screen.getByText("Enter a value: this variable is required.")).toBeInTheDocument();
    unmount();
    render(<Harness start={emptyEditorState()} />);
    expect(screen.queryByRole("group", { name: "Declared variables" })).not.toBeInTheDocument();
  });
});
