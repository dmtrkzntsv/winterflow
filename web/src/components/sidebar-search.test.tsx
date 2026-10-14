import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { SidebarSearch } from "@/components/sidebar-search";
import { TooltipProvider } from "@/components/ui/tooltip";
import { SidebarContext, type SidebarContextProps } from "@/components/ui/use-sidebar";
import { CommandPaletteContext, type CommandPaletteContextValue } from "@/context/palette-context-base";

const openPalette = vi.fn();
const setOpenMobile = vi.fn();

function renderSearch(isMobile: boolean) {
  const sidebar = { isMobile, state: "expanded", setOpenMobile } as unknown as SidebarContextProps;
  const palette = { open: false, setOpen: vi.fn(), openPalette, groups: [], register: () => () => {} } as CommandPaletteContextValue;
  render(
    <TooltipProvider>
      <SidebarContext.Provider value={sidebar}>
        <CommandPaletteContext.Provider value={palette}>
          <SidebarSearch />
        </CommandPaletteContext.Provider>
      </SidebarContext.Provider>
    </TooltipProvider>,
  );
}

describe("SidebarSearch", () => {
  beforeEach(() => {
    openPalette.mockClear();
    setOpenMobile.mockClear();
  });

  it("opens the palette from the desktop sidebar without touching the sheet", async () => {
    renderSearch(false);
    await userEvent.setup().click(screen.getByRole("button", { name: /Search/ }));
    expect(openPalette).toHaveBeenCalledTimes(1);
    expect(setOpenMobile).not.toHaveBeenCalled();
  });

  it("closes the phone sheet, then opens the palette", async () => {
    renderSearch(true);
    await userEvent.setup().click(screen.getByRole("button", { name: /Search/ }));
    expect(setOpenMobile).toHaveBeenCalledWith(false);
    expect(openPalette).toHaveBeenCalledTimes(1);
    expect(setOpenMobile.mock.invocationCallOrder[0]).toBeLessThan(openPalette.mock.invocationCallOrder[0]);
  });

  it("hides the keyboard shortcut on a phone or a touch screen", () => {
    renderSearch(true);
    const kbd = screen.getByRole("button", { name: /Search/ }).querySelector("kbd");
    expect(kbd).toHaveClass("max-sm:hidden", "pointer-coarse:hidden");
  });
});
