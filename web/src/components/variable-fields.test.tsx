import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { VariableFields } from "@/components/variable-fields";
import type { DeclaredVariable } from "@/types/app-config";

const v = (name: string, extra: Partial<DeclaredVariable> = {}): DeclaredVariable => ({
  name,
  required: false,
  encrypted: false,
  default: null,
  services: ["app"],
  ...extra,
});

describe("VariableFields", () => {
  it("labels a field by its description, its name small beside it, and tags a secret", () => {
    render(
      <VariableFields
        vars={[v("TITLE", { default: "Notes" }), v("BASE_URL", { required: true, description: "Public URL" }), v("TOKEN", { encrypted: true })]}
        values={{}}
        onChange={vi.fn()}
      />,
    );
    const url = screen.getByLabelText("Public URL (BASE_URL)");
    const label = document.querySelector(`label[for="${url.id}"]`)!;
    expect(label).toHaveTextContent("Public URL*BASE_URL");
    const secret = document.querySelector(`label[for="${screen.getByLabelText("TOKEN").id}"]`)!;
    expect(secret).toHaveTextContent("TOKENSecret");
    expect(screen.getByLabelText("TITLE")).toHaveAttribute("placeholder", "Default: Notes");
    // Required first.
    expect(screen.getAllByRole("textbox")[0]).toBe(url);
  });

  it("flags a $ as typed, a missing value once tried, and hints a pattern mismatch", async () => {
    const values: Record<string, string> = { NAME: "$x", LANG: "english" };
    const vars = [v("NAME"), v("REQ", { required: true }), v("LANG", { pattern: "[a-z]{2}" }), v("KEY", { required: true, encrypted: true })];
    const { rerender } = render(<VariableFields vars={vars} values={values} onChange={vi.fn()} kept={["KEY"]} />);
    expect(screen.getByLabelText("NAME")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByText("A plain value can't contain $.")).toBeInTheDocument();
    expect(screen.queryByText("Enter a value: this variable is required.")).not.toBeInTheDocument();
    expect(screen.getByText("May not match [a-z]{2}; the server checks it on save.")).toBeInTheDocument();
    expect(screen.getByLabelText("KEY")).toHaveAttribute("placeholder", "Stored, leave blank to keep");
    rerender(<VariableFields vars={vars} values={values} onChange={vi.fn()} kept={["KEY"]} showRequired />);
    // The kept secret may stay blank; REQ may not.
    expect(screen.getAllByText("Enter a value: this variable is required.")).toHaveLength(1);
  });

  it("reports each change by name", async () => {
    const onChange = vi.fn();
    render(<VariableFields vars={[v("NAME")]} values={{}} onChange={onChange} />);
    await userEvent.setup().type(screen.getByLabelText("NAME"), "a");
    expect(onChange).toHaveBeenCalledWith("NAME", "a");
  });
});
