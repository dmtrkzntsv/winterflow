import { fireEvent, render, screen } from "@testing-library/react";
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
  it("fades the edge where tabs are scrolled out of view", () => {
    renderTabs("overview");
    const strip = screen.getByTestId("tab-strip");
    expect(strip.className).not.toContain("mask-image");
    // A strip wider than its box: more tabs to the right, then both sides.
    Object.defineProperty(strip, "clientWidth", { configurable: true, value: 200 });
    Object.defineProperty(strip, "scrollWidth", { configurable: true, value: 400 });
    fireEvent.scroll(strip);
    expect(strip.className).toContain("to_right,black_calc(100%-2rem),transparent");
    strip.scrollLeft = 100;
    fireEvent.scroll(strip);
    expect(strip.className).toContain("transparent,black_2rem");
    strip.scrollLeft = 200;
    fireEvent.scroll(strip);
    expect(strip.className).toContain("to_left");
  });

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

  it("brings the selected tab into view by scrolling the tab strip alone, never the page", () => {
    const scrollIntoView = vi.fn();
    Element.prototype.scrollIntoView = scrollIntoView;
    const rect = (left: number, right: number) => ({ left, right, top: 0, bottom: 0, width: right - left, height: 0, x: left, y: 0, toJSON: () => ({}) });
    const spy = vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(function (this: Element) {
      if (this.getAttribute("role") === "tab") return this.textContent === "Logs" ? rect(300, 380) : rect(0, 80);
      return this.classList.contains("overflow-x-auto") ? rect(0, 200) : rect(0, 0);
    });
    renderTabs("logs");
    const strip = screen.getByRole("tablist").parentElement!;
    expect(strip.scrollLeft).toBe(180);
    expect(scrollIntoView).not.toHaveBeenCalled();
    spy.mockRestore();
    // @ts-expect-error restoring jsdom's lack of scrollIntoView
    delete Element.prototype.scrollIntoView;
  });
});
