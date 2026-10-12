import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { NavMain } from "@/components/nav-main";
import { NavSecondary } from "@/components/nav-secondary";
import { TooltipProvider } from "@/components/ui/tooltip";
import { SidebarContext, type SidebarContextProps } from "@/components/ui/use-sidebar";
import { navBottom, navMain } from "@/lib/nav";

function renderNav(path: string) {
  const sidebar = { isMobile: false, state: "expanded" } as SidebarContextProps;
  return render(
    <MemoryRouter initialEntries={[path]}>
      <SidebarContext.Provider value={sidebar}>
        <TooltipProvider>
          <NavMain items={navMain} />
          <NavSecondary items={navBottom} />
        </TooltipProvider>
      </SidebarContext.Provider>
    </MemoryRouter>,
  );
}

describe("sidebar nav", () => {
  it("has Dashboard on top and Monitoring + Server Settings in one bottom group, no Platform label", () => {
    renderNav("/");
    expect(screen.queryByText("Platform")).not.toBeInTheDocument();
    const links = screen.getAllByRole("link").map((a) => a.textContent);
    expect(links).toEqual(["Dashboard", "Monitoring", "Server Settings"]);
    expect(screen.getAllByRole("list")).toHaveLength(2);
  });

  it("lights only the current entry", () => {
    renderNav("/monitoring");
    const active = screen
      .getAllByRole("link")
      .filter((a) => a.getAttribute("data-active") === "true")
      .map((a) => a.textContent);
    expect(active).toEqual(["Monitoring"]);
  });
});
