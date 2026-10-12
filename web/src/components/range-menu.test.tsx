import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ModeSwitch, RangeMenu } from "@/components/range-menu";

describe("RangeMenu", () => {
  it("shows the range on one button and picks another from its menu", async () => {
    const onRange = vi.fn();
    render(<RangeMenu range="24h" onRangeChange={onRange} />);
    const trigger = screen.getByRole("button", { name: "Range: Last 24 hours" });
    expect(trigger).toHaveTextContent("Last 24 hours");
    await userEvent.click(trigger);
    const items = await screen.findAllByRole("menuitemradio");
    expect(items.map((i) => i.textContent)).toEqual([
      "Last hour",
      "Last 12 hours",
      "Last 24 hours",
      "Last 7 days",
      "Last 30 days",
    ]);
    expect(screen.getByRole("menuitemradio", { name: "Last 24 hours" })).toHaveAttribute("aria-checked", "true");
    await userEvent.click(screen.getByRole("menuitemradio", { name: "Last 7 days" }));
    expect(onRange).toHaveBeenCalledWith("7d");
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("adds the Average/Peak switch only with a mode handler", async () => {
    const { rerender } = render(<RangeMenu range="1h" onRangeChange={vi.fn()} />);
    expect(screen.queryByRole("radio", { name: "Peak" })).toBeNull();
    const onMode = vi.fn();
    rerender(<RangeMenu range="1h" onRangeChange={vi.fn()} mode="avg" onModeChange={onMode} />);
    expect(screen.getByRole("radio", { name: "Average" })).toHaveAttribute("data-state", "on");
    await userEvent.click(screen.getByRole("radio", { name: "Peak" }));
    expect(onMode).toHaveBeenCalledWith("peak");
  });
});

describe("ModeSwitch", () => {
  it("keeps its value when the selected half is clicked again", async () => {
    const onMode = vi.fn();
    render(<ModeSwitch value="peak" onChange={onMode} />);
    expect(screen.getByRole("group", { name: "Average or peak" })).toHaveClass("bg-muted");
    await userEvent.click(screen.getByRole("radio", { name: "Peak" }));
    expect(onMode).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("radio", { name: "Average" }));
    expect(onMode).toHaveBeenCalledWith("avg");
  });
});
