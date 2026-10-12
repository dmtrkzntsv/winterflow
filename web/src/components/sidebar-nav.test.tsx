import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { NavMain } from "@/components/nav-main";
import { NavSecondary } from "@/components/nav-secondary";
import { TooltipProvider } from "@/components/ui/tooltip";
import { SidebarContext, type SidebarContextProps } from "@/components/ui/use-sidebar";
import { navBottom, navMain, scopeNav } from "@/lib/nav";
import { serverPath } from "@/lib/server-paths";

const toServer = (sub = "") => serverPath("s1", sub);

function renderNav(path: string) {
  const sidebar = { isMobile: false, state: "expanded" } as SidebarContextProps;
  return render(
    <MemoryRouter initialEntries={[path]}>
      <SidebarContext.Provider value={sidebar}>
        <TooltipProvider>
          <NavMain items={scopeNav(navMain, toServer)} />
          <NavSecondary items={scopeNav(navBottom, toServer)} />
        </TooltipProvider>
      </SidebarContext.Provider>
    </MemoryRouter>,
  );
}

describe("sidebar nav", () => {
  it("has Dashboard on top and Monitoring + Server Settings in one bottom group, no Platform label", () => {
    renderNav("/server/s1");
    expect(screen.queryByText("Platform")).not.toBeInTheDocument();
    const links = screen.getAllByRole("link").map((a) => a.textContent);
    expect(links).toEqual(["Dashboard", "Monitoring", "Server Settings"]);
    expect(screen.getAllByRole("list")).toHaveLength(2);
  });

  it("lights only the current entry", () => {
    renderNav("/server/s1/monitoring");
    const active = screen
      .getAllByRole("link")
      .filter((a) => a.getAttribute("data-active") === "true")
      .map((a) => a.textContent);
    expect(active).toEqual(["Monitoring"]);
  });
});

describe("sidebar nav links", () => {
  it("points every entry into the server's scope", () => {
    renderNav("/server/s1");
    expect(screen.getByRole("link", { name: "Dashboard" })).toHaveAttribute("href", "/server/s1");
    expect(screen.getByRole("link", { name: "Monitoring" })).toHaveAttribute("href", "/server/s1/monitoring");
    expect(screen.getByRole("link", { name: "Server Settings" })).toHaveAttribute("href", "/server/s1/settings");
  });

  it("lights the Dashboard on the server's root only", () => {
    renderNav("/server/s1");
    const active = screen
      .getAllByRole("link")
      .filter((a) => a.getAttribute("data-active") === "true")
      .map((a) => a.textContent);
    expect(active).toEqual(["Dashboard"]);
  });
});
