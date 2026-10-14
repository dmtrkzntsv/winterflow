import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { VariablesEditor } from "@/components/variables-editor";
import { emptyEditorState } from "@/lib/app-editor-io";
import type { FieldErrors } from "@/lib/field-errors";
import type { AppEditorState } from "@/types/app-config";

function Harness({ errors = {}, emptyNote }: { errors?: FieldErrors; emptyNote?: boolean }) {
  const [state, setState] = useState<AppEditorState>(emptyEditorState);
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
