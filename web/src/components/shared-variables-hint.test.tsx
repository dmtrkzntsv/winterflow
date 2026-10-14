import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { SharedVariablesHint } from "@/components/shared-variables-hint";

describe("SharedVariablesHint", () => {
  it("names the shared variables in a line and folds the built-in ones away, never values", async () => {
    render(
      <SharedVariablesHint
        shared={[
          { name: "SMTP_PASS", scope: "org" },
          { name: "TZ", scope: "server" },
          { name: "WF_APP_DIR", scope: "builtin" },
        ]}
      />,
    );
    expect(screen.getByText(/Shared variables you can use/)).toHaveTextContent("Shared variables you can use: SMTP_PASS, TZ");
    expect(screen.queryByText("WF_APP_DIR")).not.toBeInTheDocument();
    await userEvent.setup().click(screen.getByRole("button", { name: "Built-in (1)" }));
    expect(screen.getByRole("list", { name: "Built-in variables" })).toHaveTextContent("WF_APP_DIR");
  });

  it("has no line without shared ones, and no fold without built-in ones", () => {
    const { unmount } = render(<SharedVariablesHint shared={[{ name: "WF_APP_DIR", scope: "builtin" }]} />);
    expect(screen.queryByText(/Shared variables you can use/)).not.toBeInTheDocument();
    unmount();
    render(<SharedVariablesHint shared={[{ name: "TZ", scope: "org" }]} />);
    expect(screen.queryByRole("button", { name: /Built-in/ })).not.toBeInTheDocument();
  });

  it("renders nothing without shared names", () => {
    const { container } = render(<SharedVariablesHint shared={[]} />);
    expect(container).toBeEmptyDOMElement();
  });
});
