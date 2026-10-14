import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { RowMenu } from "@/components/row-menu";

describe("RowMenu", () => {
  it("runs an item with its trigger, skips empty entries and marks the destructive one", async () => {
    const user = userEvent.setup({ delay: null });
    const edit = vi.fn();
    const remove = vi.fn();
    render(
      <RowMenu
        label="Actions for disk"
        items={[{ label: "Edit", onSelect: edit }, false, null, { label: "Remove", destructive: true, onSelect: remove }]}
      />,
    );
    const trigger = screen.getByRole("button", { name: "Actions for disk" });
    await user.click(trigger);
    expect(await screen.findAllByRole("menuitem")).toHaveLength(2);
    expect(screen.getByRole("menuitem", { name: "Remove" })).toHaveClass("text-destructive");
    // Every item is a 40px tap target on phones (the vendored item's own class).
    for (const item of screen.getAllByRole("menuitem")) expect(item).toHaveClass("max-sm:min-h-10");
    await user.click(screen.getByRole("menuitem", { name: "Edit" }));
    expect(edit).toHaveBeenCalledWith(trigger);
    expect(remove).not.toHaveBeenCalled();
  });

  it("keeps a disabled item from running and explains it on hover", async () => {
    const user = userEvent.setup({ delay: null });
    const run = vi.fn();
    render(<RowMenu label="Actions for x" items={[{ label: "Run", onSelect: run, disabled: true, title: "Busy" }]} />);
    await user.click(screen.getByRole("button", { name: "Actions for x" }));
    const item = await screen.findByRole("menuitem", { name: "Run" });
    expect(item).toHaveAttribute("aria-disabled", "true");
    expect(item).toHaveAttribute("title", "Busy");
    await user.click(item);
    expect(run).not.toHaveBeenCalled();
  });

  it("renders nothing without an item", () => {
    const { container } = render(<RowMenu label="Actions for x" items={[false]} />);
    expect(container).toBeEmptyDOMElement();
  });
});
