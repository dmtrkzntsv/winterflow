import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { ErrorText } from "@/components/error-text";
import { lastLine } from "@/lib/error-lines";

const COMPOSE = "Container web  Creating\nContainer web  Error\n\nError response from daemon: pull access denied for nope\n  \n";

describe("lastLine", () => {
  it("is the last non-empty line, trimmed", () => {
    expect(lastLine(COMPOSE)).toBe("Error response from daemon: pull access denied for nope");
    expect(lastLine("  one line  ")).toBe("one line");
    expect(lastLine("")).toBe("");
  });
});

describe("ErrorText", () => {
  it("shows the last line, the full text behind Details", async () => {
    const user = userEvent.setup();
    render(<ErrorText text={COMPOSE} alert prefix="Failed: " suffix=". Try again." />);
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expect(screen.getByText("Failed: Error response from daemon: pull access denied for nope. Try again.")).toBeVisible();
    const full = screen.getByText(/Container web Creating/);
    expect(full).not.toBeVisible();
    await user.click(screen.getByText("Details"));
    expect(full).toBeVisible();
    expect(full).toHaveTextContent("Container web Error");
  });

  it("has no Details for a one-line error", () => {
    render(<ErrorText text="server offline" icon />);
    expect(screen.getByText("server offline")).toBeInTheDocument();
    expect(screen.queryByText("Details")).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
