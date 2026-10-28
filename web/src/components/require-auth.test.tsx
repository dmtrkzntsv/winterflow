import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { RequireAuth } from "@/components/require-auth";

const auth = vi.hoisted(() => ({ isAuthenticated: false, checked: true }));
vi.mock("@/context/use-auth", () => ({ useAuth: () => auth }));

function Where() {
  const { pathname, search } = useLocation();
  return <p data-testid="where">{`${pathname}${search}`}</p>;
}

function renderAt(url: string) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route path="/login" element={<Where />} />
        <Route
          path="*"
          element={
            <RequireAuth>
              <p>secret page</p>
            </RequireAuth>
          }
        />
      </Routes>
    </MemoryRouter>,
  );
}

describe("RequireAuth", () => {
  it("sends a signed-out visit to /login?next= with the whole address", () => {
    renderAt("/oauth/authorize?client_id=c1&state=s");
    const next = new URLSearchParams(screen.getByTestId("where").textContent?.split("?")[1]).get("next");
    expect(screen.getByTestId("where").textContent).toMatch(/^\/login\?next=/);
    expect(next).toBe("/oauth/authorize?client_id=c1&state=s");
  });

  it("sends home's visit to plain /login", () => {
    renderAt("/");
    expect(screen.getByTestId("where")).toHaveTextContent(/^\/login$/);
  });

  it("shows the page to a signed-in browser", () => {
    auth.isAuthenticated = true;
    try {
      renderAt("/oauth/authorize?client_id=c1");
      expect(screen.getByText("secret page")).toBeInTheDocument();
    } finally {
      auth.isAuthenticated = false;
    }
  });

  it("waits for the session probe", () => {
    auth.checked = false;
    try {
      renderAt("/x");
      expect(screen.queryByTestId("where")).not.toBeInTheDocument();
      expect(screen.queryByText("secret page")).not.toBeInTheDocument();
    } finally {
      auth.checked = true;
    }
  });
});
