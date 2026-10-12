import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { NavUser } from "@/components/nav-user";
import {
  SidebarContext,
  type SidebarContextProps,
} from "@/components/ui/use-sidebar";
import { AuthContext, type AuthContextValue } from "@/context/auth-context-base";
import {
  ProfileContext,
  type ProfileContextValue,
} from "@/context/profile-context-base";
import { UserContext, type UserContextValue } from "@/context/user-context-base";

function renderNav() {
  const sidebar = { isMobile: false, state: "expanded" } as SidebarContextProps;
  const auth = { logout: vi.fn() } as unknown as AuthContextValue;
  const user = {
    user: { id: "u-1", name: "Ann", picture: "", provider: "local" },
    loading: false,
  } as UserContextValue;
  // A plain account: nothing in the profile says "admin" any more.
  const profile = {
    profile: { user_id: "u-1", name: "Ann", email: "ann@example.test", must_change_password: false },
  } as ProfileContextValue;
  return render(
    <MemoryRouter initialEntries={["/"]}>
      <SidebarContext.Provider value={sidebar}>
        <AuthContext.Provider value={auth}>
          <UserContext.Provider value={user}>
            <ProfileContext.Provider value={profile}>
              <Routes>
                <Route path="/" element={<NavUser />} />
                <Route path="/org/members" element={<p>members page</p>} />
                <Route path="/org/alerts" element={<p>alerts page</p>} />
              </Routes>
            </ProfileContext.Provider>
          </UserContext.Provider>
        </AuthContext.Provider>
      </SidebarContext.Provider>
    </MemoryRouter>,
  );
}

describe("NavUser", () => {
  it("offers Members to every user and navigates there", async () => {
    const user = userEvent.setup();
    renderNav();

    await user.click(screen.getByRole("button", { name: /Ann/ }));
    await user.click(await screen.findByRole("menuitem", { name: "Members" }));

    expect(await screen.findByText("members page")).toBeInTheDocument();
  });

  it("offers Alerts next to Members", async () => {
    const user = userEvent.setup();
    renderNav();

    await user.click(screen.getByRole("button", { name: /Ann/ }));
    await user.click(await screen.findByRole("menuitem", { name: "Alerts" }));

    expect(await screen.findByText("alerts page")).toBeInTheDocument();
  });
});
