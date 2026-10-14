import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { NavUser } from "@/components/nav-user";
import { SidebarContext, type SidebarContextProps } from "@/components/ui/use-sidebar";
import { AuthContext, type AuthContextValue } from "@/context/auth-context-base";
import { UserContext, type UserContextValue } from "@/context/user-context-base";
import { THEME_KEY, setThemePreference } from "@/lib/theme";

function renderNav() {
  const sidebar = { isMobile: false, state: "expanded" } as SidebarContextProps;
  const logout = vi.fn();
  const auth = { logout } as unknown as AuthContextValue;
  const user = {
    user: { id: "u-1", name: "Ann", picture: "", provider: "local" },
    loading: false,
  } as UserContextValue;
  render(
    <MemoryRouter>
      <SidebarContext.Provider value={sidebar}>
        <AuthContext.Provider value={auth}>
          <UserContext.Provider value={user}>
            <NavUser />
          </UserContext.Provider>
        </AuthContext.Provider>
      </SidebarContext.Provider>
    </MemoryRouter>,
  );
  return { logout };
}

describe("NavUser", () => {
  afterEach(() => {
    act(() => setThemePreference("system"));
    localStorage.clear();
    document.documentElement.classList.remove("dark");
  });

  it("switches the theme between Light, Dark and System", async () => {
    const user = userEvent.setup();
    renderNav();
    await user.click(screen.getByRole("button", { name: /Ann/ }));
    expect(await screen.findByRole("menuitemradio", { name: "System" })).toHaveAttribute("aria-checked", "true");
    expect(screen.getByRole("menuitemradio", { name: "Light" })).toHaveAttribute("aria-checked", "false");
    await user.click(screen.getByRole("menuitemradio", { name: "Dark" }));
    expect(document.documentElement).toHaveClass("dark");
    expect(localStorage.getItem(THEME_KEY)).toBe("dark");
  });

  it("logs out", async () => {
    const user = userEvent.setup();
    const { logout } = renderNav();
    await user.click(screen.getByRole("button", { name: /Ann/ }));
    await user.click(await screen.findByRole("menuitem", { name: /log ?out/i }));
    expect(logout).toHaveBeenCalled();
  });

  it("no longer carries the organization's pages (they live in Settings)", async () => {
    const user = userEvent.setup();
    renderNav();
    await user.click(screen.getByRole("button", { name: /Ann/ }));
    await screen.findByRole("menu");
    for (const name of ["Members", "Alerts", "Org variables", "Templates", "API tokens", "Change password"]) {
      expect(screen.queryByRole("menuitem", { name })).not.toBeInTheDocument();
    }
  });
});
