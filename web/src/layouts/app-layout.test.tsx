import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useSidebar } from "@/components/ui/use-sidebar";
import { useCommandPalette } from "@/context/use-palette";
import { AppLayout } from "@/layouts/app-layout";
import { usePageTitle } from "@/layouts/use-page-title";
import { stubViewport } from "@/test/media";

let mustChange = false;
vi.mock("@/context/use-profile", () => ({
  useProfile: () => ({
    profile: { user_id: "u1", name: "Ann", email: "ann@example.test", must_change_password: mustChange },
  }),
}));
vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ servers: [], refresh: vi.fn(), selectServer: vi.fn() }),
}));
// The sidebar has its own tests; this stand-in shows whether the phone
// sheet is open.
vi.mock("@/components/app-sidebar", () => ({
  AppSidebar: function SidebarStub() {
    return <p>{useSidebar().openMobile ? "menu open" : "menu closed"}</p>;
  },
}));
vi.mock("@/components/command-palette", () => ({ CommandPalette: () => null }));
vi.mock("@/components/alerts/alert-toasts", () => ({ AlertToasts: () => null }));
vi.mock("@/components/server-creation-dialog", () => ({ ServerSelectionDialog: () => null }));

function Page({ title }: { title: string }) {
  usePageTitle(title);
  const { open } = useCommandPalette();
  return (
    <p>
      {title} body{open ? " (palette open)" : ""}
    </p>
  );
}

function renderAt(url: string) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route element={<AppLayout />}>
          <Route path="/server/s1/monitoring" element={<Page title="Monitoring" />} />
          <Route path="/settings/members" element={<Page title="Members" />} />
          <Route path="/settings/password" element={<Page title="Password" />} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

describe("AppLayout", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    mustChange = false;
    document.title = "";
  });

  it("has no breadcrumb or top bar on a desktop, and names the browser tab after the page", async () => {
    renderAt("/settings/members");
    expect(screen.getByText("Members body")).toBeInTheDocument();
    expect(screen.queryByRole("navigation", { name: /breadcrumb/i })).not.toBeInTheDocument();
    expect(screen.queryByTestId("mobile-top-bar")).not.toBeInTheDocument();
    await waitFor(() => expect(document.title).toBe("Members · WinterFlow"));
  });

  it("shows a slim top bar on a phone: menu, the page's title, search", async () => {
    stubViewport(375);
    const user = userEvent.setup();
    renderAt("/server/s1/monitoring");
    const bar = screen.getByTestId("mobile-top-bar");
    expect(await within(bar).findByText("Monitoring")).toBeInTheDocument();

    await user.click(within(bar).getByRole("button", { name: "Open menu" }));
    expect(screen.getByText("menu open")).toBeInTheDocument();

    await user.click(within(bar).getByRole("button", { name: "Search" }));
    expect(screen.getByText(/palette open/)).toBeInTheDocument();
  });

  it("caps reading pages at 1100px and gives Monitoring the full width", () => {
    const { unmount } = renderAt("/settings/members");
    expect(screen.getByTestId("page")).toHaveClass("max-w-[1100px]");
    unmount();
    renderAt("/server/s1/monitoring");
    expect(screen.getByTestId("page")).not.toHaveClass("max-w-[1100px]");
  });

  it("sends a user on a temporary password to Settings → Password", () => {
    mustChange = true;
    renderAt("/settings/members");
    expect(screen.getByText("Password body")).toBeInTheDocument();
  });
});
