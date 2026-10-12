import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { SharedVariablesHint } from "@/components/shared-variables-hint";

describe("SharedVariablesHint", () => {
  it("lists names and scopes, collapsed by default, never values", () => {
    render(<SharedVariablesHint shared={[{ name: "SMTP_PASS", scope: "org" }, { name: "WF_APP_DIR", scope: "builtin" }]} />);
    const summary = screen.getByText("Shared variables available to this app (2)");
    expect(summary.closest("details")).not.toHaveAttribute("open");
    expect(screen.getByText("SMTP_PASS")).toBeInTheDocument();
    expect(screen.getByText("org")).toBeInTheDocument();
    expect(screen.getByText("built-in")).toBeInTheDocument();
  });

  it("renders nothing without shared names", () => {
    const { container } = render(<SharedVariablesHint shared={[]} />);
    expect(container).toBeEmptyDOMElement();
  });
});
