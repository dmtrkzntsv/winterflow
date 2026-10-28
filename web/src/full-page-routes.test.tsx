import { render, screen } from "@testing-library/react";
import { RouterProvider, createMemoryRouter, useLocation } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { fullPageRoutes } from "@/routes";

const auth = vi.hoisted(() => ({ isAuthenticated: false, checked: true }));
vi.mock("@/context/use-auth", () => ({ useAuth: () => auth }));
vi.mock("@/pages/secret-request", () => ({ default: () => <p>secret request page</p> }));
vi.mock("@/pages/oauth-authorize", () => ({ default: () => <p>consent page</p> }));

function Where() {
  const { pathname, search } = useLocation();
  return <p data-testid="where">{`${pathname}${search}`}</p>;
}

function renderAt(url: string) {
  const router = createMemoryRouter([...fullPageRoutes, { path: "/login", element: <Where /> }], {
    initialEntries: [url],
  });
  return render(<RouterProvider router={router} />);
}

// The full-page routes are signed-in pages: a secret link or a consent
// screen opened signed out goes through /login and comes back.
describe("full-page routes", () => {
  it.each([
    ["/secrets/wfx_abc", "secret request page"],
    ["/oauth/authorize?client_id=c1", "consent page"],
  ])("%s requires a sign-in", async (url, page) => {
    renderAt(url);
    const where = await screen.findByTestId("where");
    expect(new URLSearchParams(where.textContent?.split("?")[1]).get("next")).toBe(url);
    expect(screen.queryByText(page)).not.toBeInTheDocument();
  });

  it.each([
    ["/secrets/wfx_abc", "secret request page"],
    ["/oauth/authorize?client_id=c1", "consent page"],
  ])("%s opens signed in", async (url, page) => {
    auth.isAuthenticated = true;
    try {
      renderAt(url);
      expect(await screen.findByText(page)).toBeInTheDocument();
    } finally {
      auth.isAuthenticated = false;
    }
  });
});
