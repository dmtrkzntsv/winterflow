import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { UnderlineTabs } from "@/components/underline-tabs";
import { TabsContent } from "@/components/ui/tabs";

const ITEMS = [
  { value: "overview", label: "Overview" },
  { value: "logs", label: "Logs" },
] as const;

function renderTabs(value: "overview" | "logs", onValueChange = vi.fn()) {
  return render(
    <UnderlineTabs value={value} onValueChange={onValueChange} items={[...ITEMS]} label="App sections">
      <TabsContent value="overview">overview panel</TabsContent>
      <TabsContent value="logs">logs panel</TabsContent>
    </UnderlineTabs>,
  );
}

describe("UnderlineTabs", () => {
  it("shows the selected tab's panel under an underline tab bar", () => {
    renderTabs("logs");
    expect(screen.getByRole("tablist", { name: "App sections" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Logs" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tab", { name: "Logs" })).toHaveClass("border-b-2", "min-h-10");
    expect(screen.getByText("logs panel")).toBeInTheDocument();
    expect(screen.queryByText("overview panel")).not.toBeInTheDocument();
  });

  it("reports the tab picked", async () => {
    const onValueChange = vi.fn();
    renderTabs("logs", onValueChange);
    await userEvent.setup().click(screen.getByRole("tab", { name: "Overview" }));
    expect(onValueChange).toHaveBeenCalledWith("overview");
  });

  it("scrolls inside itself on a phone instead of widening the page", () => {
    renderTabs("logs");
    const list = screen.getByRole("tablist");
    expect(list).toHaveClass("w-max");
    expect(list.parentElement).toHaveClass("max-w-full", "overflow-x-auto");
  });

  it("brings the selected tab into view", () => {
    const scrolled: Element[] = [];
    const spy = vi.spyOn(Element.prototype, "scrollIntoView").mockImplementation(function (this: Element) {
      scrolled.push(this);
    });
    renderTabs("logs");
    expect(scrolled).toContain(screen.getByRole("tab", { name: "Logs" }));
    spy.mockRestore();
  });
});
