import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { StatusDot, StatusPill } from "@/components/status";

describe("StatusDot", () => {
  it("is an image named by its label in the tone's colour", () => {
    render(<StatusDot tone="ok" label="Running" />);
    const dot = screen.getByRole("img", { name: "Running" });
    expect(dot).toHaveClass("bg-status-ok");
    expect(dot).toHaveAttribute("data-tone", "ok");
  });

  it("is hidden from assistive tech without a label", () => {
    const { container } = render(<StatusDot tone="bad" />);
    const dot = container.querySelector("[data-tone]");
    expect(dot).toHaveAttribute("aria-hidden", "true");
    expect(dot).toHaveClass("bg-status-bad");
  });

  it("pulses unless the user asked for less motion", () => {
    const { container } = render(<StatusDot tone="warn" pulse />);
    expect(container.querySelector("[data-tone]")).toHaveClass("animate-pulse", "motion-reduce:animate-none");
  });
});

describe("StatusPill", () => {
  it("shows the word with a dot of its tone", () => {
    const { container } = render(<StatusPill status={{ tone: "warn", label: "Late" }} />);
    expect(screen.getByText("Late")).toHaveAttribute("data-tone", "warn");
    expect(container.querySelector("span[data-tone] > span[data-tone]")).toHaveClass("bg-status-warn");
  });
});
