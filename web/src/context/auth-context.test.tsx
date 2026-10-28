import { act, render, screen, waitFor } from "@testing-library/react";
import { useEffect } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AuthProvider } from "@/context/auth-context";
import { LoginError } from "@/context/auth-context-base";
import { useAuth } from "@/context/use-auth";
import { fakeAPI } from "@/test/fake-api";

// ref holds the latest context value (set in an effect, not in render).
const ref: { current: ReturnType<typeof useAuth> | null } = { current: null };
function Probe() {
  const value = useAuth();
  useEffect(() => {
    ref.current = value;
  });
  return <p data-testid="state">{`${value.checked ? "checked" : "unknown"} ${value.isAuthenticated ? "in" : "out"}`}</p>;
}
const ctx = new Proxy({} as ReturnType<typeof useAuth>, {
  get: (_t, k) => (ref.current as unknown as Record<string | symbol, unknown>)[k],
});

const renderAuth = () =>
  render(
    <AuthProvider>
      <Probe />
    </AuthProvider>,
  );

const headerOf = (api: ReturnType<typeof fakeAPI>, path: string) => {
  const init = api.fetchMock.mock.calls.find(([u]) => String(u).endsWith(path))?.[1];
  return new Headers(init?.headers).get("X-Winterflow-Request");
};

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("AuthProvider", () => {
  it("is signed in when the profile reads", async () => {
    fakeAPI({ "/api/v1/user/get-profile": { user_id: "u1" } });
    renderAuth();
    await waitFor(() => expect(screen.getByTestId("state")).toHaveTextContent("checked in"));
  });

  it("is signed out on a 401", async () => {
    fakeAPI({ "/api/v1/user/get-profile": Object.assign(new Error("unauthorized"), { status: 401 }) });
    renderAuth();
    await waitFor(() => expect(screen.getByTestId("state")).toHaveTextContent("checked out"));
  });

  it("logs in with JSON and the app-request header", async () => {
    const api = fakeAPI({
      "/api/v1/user/get-profile": Object.assign(new Error("unauthorized"), { status: 401 }),
      "/api/v1/auth/login": { user: { id: "u1" } },
    });
    renderAuth();
    await waitFor(() => expect(screen.getByTestId("state")).toHaveTextContent("checked out"));
    await act(() => ctx.login({ email: "ann@example.test", password: "pw" }));
    expect(api.posted("/api/v1/auth/login")).toEqual([{ email: "ann@example.test", password: "pw" }]);
    expect(headerOf(api, "/api/v1/auth/login")).toBe("1");
    expect(screen.getByTestId("state")).toHaveTextContent("in");
  });

  it("throws a refused login as a LoginError with its status", async () => {
    fakeAPI({
      "/api/v1/user/get-profile": Object.assign(new Error("unauthorized"), { status: 401 }),
      "/api/v1/auth/login": Object.assign(new Error("bad"), { status: 401 }),
    });
    renderAuth();
    await waitFor(() => expect(screen.getByTestId("state")).toHaveTextContent("checked"));
    const err = await ctx.login({ email: "a@b", password: "x" }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(LoginError);
    expect(err).toMatchObject({ status: 401 });
  });

  it.each([
    ["logout", "/api/v1/auth/logout"],
    ["logoutAll", "/api/v1/user/logout-all"],
  ] as const)("%s posts to %s and signs this browser out", async (method, path) => {
    const api = fakeAPI({ "/api/v1/user/get-profile": { user_id: "u1" }, [path]: null });
    renderAuth();
    await waitFor(() => expect(screen.getByTestId("state")).toHaveTextContent("checked in"));
    await act(() => ctx[method]());
    expect(api.calls.some((c) => c.path === path)).toBe(true);
    expect(headerOf(api, path)).toBe("1");
    expect(screen.getByTestId("state")).toHaveTextContent("out");
  });

  it("signs this browser out even when the server refuses", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    fakeAPI({ "/api/v1/user/get-profile": { user_id: "u1" }, "/api/v1/user/logout-all": new Error("down") });
    renderAuth();
    await waitFor(() => expect(screen.getByTestId("state")).toHaveTextContent("checked in"));
    await act(() => ctx.logoutAll());
    expect(screen.getByTestId("state")).toHaveTextContent("out");
  });

  it("records a session set elsewhere", async () => {
    fakeAPI({ "/api/v1/user/get-profile": Object.assign(new Error("unauthorized"), { status: 401 }) });
    renderAuth();
    await waitFor(() => expect(screen.getByTestId("state")).toHaveTextContent("checked out"));
    act(() => ctx.signedIn());
    expect(screen.getByTestId("state")).toHaveTextContent("in");
  });
});
