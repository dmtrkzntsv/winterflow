import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { FieldMessage, FormField } from "@/components/form-field";
import { Input } from "@/components/ui/input";
import { invalidAttrs } from "@/lib/field-errors";

describe("FormField", () => {
  it("labels its control and shows the hint while the field is fine", () => {
    render(
      <FormField id="src-branch" label="Branch" hint="Usually main.">
        <Input id="src-branch" {...invalidAttrs("src-branch", undefined)} />
      </FormField>,
    );
    expect(screen.getByLabelText("Branch")).not.toHaveAttribute("aria-invalid");
    expect(screen.getByText("Usually main.")).toBeInTheDocument();
  });

  it("replaces the hint with the field's message, which describes the control", () => {
    render(
      <FormField id="src-branch" label="Branch" hint="Usually main." error="Enter a branch.">
        <Input id="src-branch" {...invalidAttrs("src-branch", "Enter a branch.")} />
      </FormField>,
    );
    const input = screen.getByLabelText("Branch");
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(input).toHaveAccessibleDescription("Enter a branch.");
    expect(screen.queryByText("Usually main.")).not.toBeInTheDocument();
  });
});

describe("FieldMessage", () => {
  it("renders only with a message", () => {
    const { container, rerender } = render(<FieldMessage id="files" />);
    expect(container).toBeEmptyDOMElement();
    rerender(<FieldMessage id="files" message="Add a compose file named compose.yml." />);
    expect(screen.getByText("Add a compose file named compose.yml.")).toHaveAttribute("id", "files-error");
  });
});
