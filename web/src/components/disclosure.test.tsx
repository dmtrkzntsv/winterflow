import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { Disclosure } from "@/components/disclosure";

describe("Disclosure", () => {
  it("hides its fields behind More options until opened", async () => {
    const user = userEvent.setup();
    render(
      <Disclosure>
        <label>
          Region <input />
        </label>
      </Disclosure>,
    );
    const toggle = screen.getByRole("button", { name: "More options" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByLabelText("Region")).not.toBeInTheDocument();
    await user.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByLabelText("Region")).toBeInTheDocument();
  });

  it("takes another label and can start open", () => {
    render(
      <Disclosure label="Advanced" defaultOpen>
        <p>retention</p>
      </Disclosure>,
    );
    expect(screen.getByRole("button", { name: "Advanced" })).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText("retention")).toBeInTheDocument();
  });
});
