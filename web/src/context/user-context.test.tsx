import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { UserProvider } from "@/context/user-context";
import { useUser } from "@/context/use-user";
import { fakeAPI } from "@/test/fake-api";

const auth = vi.hoisted(() => ({ isAuthenticated: true }));
vi.mock("@/context/use-auth", () => ({ useAuth: () => auth }));

function Probe() {
  const { user, loading, error } = useUser();
  return <p data-testid="user">{loading ? "loading" : error ? `error ${error}` : (user?.name ?? "nobody")}</p>;
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("UserProvider", () => {
  it("reads who is signed in from the profile", async () => {
    const api = fakeAPI({ "/api/v1/user/get-profile": { user_id: "u1", name: "Ann Lee" } });
    render(
      <UserProvider>
        <Probe />
      </UserProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("user")).toHaveTextContent("Ann Lee"));
    expect(api.calls.map((c) => c.path)).toEqual(["/api/v1/user/get-profile"]);
  });

  it("says why the profile couldn't be read", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    fakeAPI({ "/api/v1/user/get-profile": new Error("db down") });
    render(
      <UserProvider>
        <Probe />
      </UserProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("user")).toHaveTextContent("error db down"));
  });

  it("has nobody while signed out", async () => {
    auth.isAuthenticated = false;
    try {
      render(
        <UserProvider>
          <Probe />
        </UserProvider>,
      );
      await waitFor(() => expect(screen.getByTestId("user")).toHaveTextContent("nobody"));
    } finally {
      auth.isAuthenticated = true;
    }
  });
});
