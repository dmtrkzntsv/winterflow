import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import OAuthAuthorizePage from "@/pages/oauth-authorize";
import { ApiError } from "@/lib/api";
import { fakeAPI } from "@/test/fake-api";

const leaveTo = vi.hoisted(() => vi.fn());
vi.mock("@/lib/leave", () => ({ leaveTo }));

const QUERY =
  "?client_id=c1&redirect_uri=https%3A%2F%2Fclaude.ai%2Fcb&response_type=code&code_challenge=abc" +
  "&code_challenge_method=S256&state=xyz&scope=mcp";
const REQUEST = {
  client_id: "c1",
  redirect_uri: "https://claude.ai/cb",
  response_type: "code",
  code_challenge: "abc",
  code_challenge_method: "S256",
  state: "xyz",
  scope: "mcp",
};
const DESCRIPTION = { client_name: "Claude", redirect_host: "claude.ai", is_loopback: false, error_redirect_url: "" };

function renderPage(search = QUERY) {
  return render(
    <MemoryRouter initialEntries={[`/oauth/authorize${search}`]}>
      <Routes>
        <Route path="/oauth/authorize" element={<OAuthAuthorizePage />} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => leaveTo.mockReset());
afterEach(() => vi.unstubAllGlobals());

describe("OAuthAuthorizePage", () => {
  it("names the app, where it goes and what it gets, and approves to the returned URL", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({
      "/api/v1/authserver/get-authorization-request": DESCRIPTION,
      "/api/v1/authserver/approve-authorization": { redirect_url: "https://claude.ai/cb?code=k&state=xyz" },
    });
    renderPage();
    expect(await screen.findByRole("heading", { level: 1, name: "Connect Claude?" })).toBeInTheDocument();
    expect(screen.getByText("claude.ai")).toBeInTheDocument();
    expect(screen.getByText("Full access to your organization's servers and apps")).toBeInTheDocument();
    const described = api.calls.find((c) => c.path === "/api/v1/authserver/get-authorization-request");
    expect(Object.fromEntries(new URLSearchParams(described?.query))).toEqual(REQUEST);
    await user.click(screen.getByRole("button", { name: "Approve" }));
    await waitFor(() => expect(leaveTo).toHaveBeenCalledWith("https://claude.ai/cb?code=k&state=xyz"));
    expect(api.posted("/api/v1/authserver/approve-authorization")).toEqual([REQUEST]);
    const init = api.fetchMock.mock.calls.find(([u]) => String(u).includes("approve-authorization"))?.[1];
    expect(new Headers(init?.headers).get("X-Winterflow-Request")).toBe("1");
  });

  it("declines to the returned URL", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({
      "/api/v1/authserver/get-authorization-request": DESCRIPTION,
      "/api/v1/authserver/decline-authorization": { redirect_url: "https://claude.ai/cb?error=access_denied" },
    });
    renderPage();
    await user.click(await screen.findByRole("button", { name: "Decline" }));
    await waitFor(() => expect(leaveTo).toHaveBeenCalledWith("https://claude.ai/cb?error=access_denied"));
    expect(api.posted("/api/v1/authserver/decline-authorization")).toEqual([REQUEST]);
  });

  it("says a loopback client runs on this computer", async () => {
    fakeAPI({
      "/api/v1/authserver/get-authorization-request": { ...DESCRIPTION, redirect_host: "127.0.0.1", is_loopback: true },
    });
    renderPage();
    expect(await screen.findByText(/It runs on this computer \(127\.0\.0\.1\)/)).toBeInTheDocument();
  });

  it("sends a bad request with a trusted redirect back to the app", async () => {
    fakeAPI({
      "/api/v1/authserver/get-authorization-request": {
        ...DESCRIPTION,
        error_redirect_url: "https://claude.ai/cb?error=invalid_request",
      },
    });
    renderPage();
    await waitFor(() => expect(leaveTo).toHaveBeenCalledWith("https://claude.ai/cb?error=invalid_request"));
    expect(screen.queryByRole("button", { name: "Approve" })).not.toBeInTheDocument();
  });

  it("shows why an untrusted request is refused, and never redirects", async () => {
    fakeAPI({ "/api/v1/authserver/get-authorization-request": new Error("unknown client") });
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("unknown client");
    expect(leaveTo).not.toHaveBeenCalled();
  });

  it("says when connecting apps is off", async () => {
    fakeAPI({ "/api/v1/authserver/get-authorization-request": new ApiError("not found", 404) });
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("Connecting apps isn't available on this Winterflow.");
  });

  it("keeps the choice open when approving fails", async () => {
    const user = userEvent.setup();
    fakeAPI({
      "/api/v1/authserver/get-authorization-request": DESCRIPTION,
      "/api/v1/authserver/approve-authorization": new Error("the request expired"),
    });
    renderPage();
    await user.click(await screen.findByRole("button", { name: "Approve" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("the request expired");
    expect(screen.getByRole("button", { name: "Approve" })).toBeEnabled();
    expect(leaveTo).not.toHaveBeenCalled();
  });
});
