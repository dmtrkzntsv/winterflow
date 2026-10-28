import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import "@/lib/i18n";
import { LoginError } from "@/context/auth-context-base";
import LoginPage from "@/pages/login";
import { fakeAPI } from "@/test/fake-api";

const auth = vi.hoisted(() => ({
  isAuthenticated: false,
  checked: true,
  login: vi.fn(),
  signedIn: vi.fn(),
  logout: vi.fn(),
  logoutAll: vi.fn(),
}));
vi.mock("@/context/use-auth", () => ({ useAuth: () => auth }));

function Where() {
  const { pathname, search, hash } = useLocation();
  return <p data-testid="where">{`${pathname}${search}${hash}`}</p>;
}

function renderLogin(from?: { pathname: string; search?: string; hash?: string }, search = "") {
  return render(
    <MemoryRouter initialEntries={[{ pathname: "/login", search, state: from ? { from } : undefined }]}>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/register" element={<p>setup screen</p>} />
        <Route path="/" element={<p>home</p>} />
        <Route path="/server/:serverId/app/:appId" element={<Where />} />
        <Route path="/oauth/authorize" element={<Where />} />
      </Routes>
    </MemoryRouter>,
  );
}

async function signIn(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText("Email"), "ann@example.test");
  await user.type(screen.getByLabelText("Password"), "secret");
  await user.click(screen.getByRole("button", { name: "Sign in" }));
}

beforeEach(() => {
  auth.login.mockReset();
  vi.spyOn(console, "error").mockImplementation(() => {});
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("LoginPage", () => {
  it("sends a fresh instance to the setup screen", async () => {
    fakeAPI({ "/api/v1/auth/state": { bootstrap: true, registration_enabled: true } });
    renderLogin();
    expect(await screen.findByText("setup screen")).toBeInTheDocument();
  });

  it("stays on the login form once set up, offering sign-up only when it is open", async () => {
    fakeAPI({ "/api/v1/auth/state": { bootstrap: false, registration_enabled: true } });
    renderLogin();
    expect(await screen.findByRole("link", { name: "Create an account" })).toHaveAttribute("href", "/register");
    expect(screen.getByRole("heading", { level: 1, name: "Welcome back" })).toBeInTheDocument();
    expect(screen.queryByText("setup screen")).not.toBeInTheDocument();
  });

  it("still lets people log in when the instance's state can't be read", async () => {
    const api = fakeAPI({ "/api/v1/auth/state": new Error("down") });
    renderLogin();
    await waitFor(() => expect(api.calls.some((c) => c.path === "/api/v1/auth/state")).toBe(true));
    expect(screen.getByRole("heading", { level: 1, name: "Welcome back" })).toBeInTheDocument();
    expect(screen.queryByText("setup screen")).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Create an account" })).not.toBeInTheDocument();
  });

  it("says a refused sign-in is a wrong email or password, and anything else plainly", async () => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/auth/state": { bootstrap: false, registration_enabled: false } });
    vi.spyOn(console, "error").mockImplementation(() => {});
    auth.login.mockRejectedValueOnce(new LoginError(401)).mockRejectedValueOnce(new Error("offline"));
    renderLogin();
    await user.type(screen.getByLabelText("Email"), "ann@example.test");
    await user.type(screen.getByLabelText("Password"), "nope");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByText("Wrong email or password. Forgot it? Ask a member to reset your password.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByText("We couldn't sign you in. Try again.")).toBeInTheDocument();
  });

  it("shows the snowflake over the heading and no terms on a self-hosted install", async () => {
    fakeAPI({ "/api/v1/auth/state": { bootstrap: false, registration_enabled: false } });
    renderLogin();
    expect(screen.getByTestId("auth-logo")).toBeInTheDocument();
    expect(screen.getByText("Sign in to Winterflow")).toBeInTheDocument();
    expect(screen.queryByText(/Terms of Service/)).not.toBeInTheDocument();
  });

  it("logs in and goes home", async () => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/auth/state": { bootstrap: false, registration_enabled: false } });
    auth.login.mockResolvedValue(undefined);
    renderLogin();
    await user.type(screen.getByLabelText("Email"), "ann@example.test");
    await user.type(screen.getByLabelText("Password"), "secret");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(auth.login).toHaveBeenCalledWith({ email: "ann@example.test", password: "secret" });
    expect(await screen.findByText("home")).toBeInTheDocument();
  });

  it("goes back to the page the person was headed for, with its query and hash", async () => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/auth/state": { bootstrap: false, registration_enabled: false } });
    auth.login.mockResolvedValue(undefined);
    renderLogin({ pathname: "/server/s1/app/a1", search: "?tab=metrics", hash: "#cpu" });
    await user.type(screen.getByLabelText("Email"), "ann@example.test");
    await user.type(screen.getByLabelText("Password"), "secret");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByTestId("where")).toHaveTextContent("/server/s1/app/a1?tab=metrics#cpu");
  });

  it.each([
    ["a protocol-relative URL", "//evil.example/x"],
    ["a backslash URL", "/\\evil.example/x"],
    ["a path hiding a tab", "/\t/evil.example/x"],
    ["an absolute URL", "https://evil.example/x"],
    ["a relative path", "server/s1"],
  ])("goes home instead of to %s", async (_label, pathname) => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/auth/state": { bootstrap: false, registration_enabled: false } });
    auth.login.mockResolvedValue(undefined);
    renderLogin({ pathname, search: "?tab=metrics", hash: "#cpu" });
    await user.type(screen.getByLabelText("Email"), "ann@example.test");
    await user.type(screen.getByLabelText("Password"), "secret");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByText("home")).toBeInTheDocument();
  });

  it.each([
    ["a query that joins into //", { pathname: "/", search: "/evil.example/x" }],
    ["a hash that joins into //", { pathname: "/", hash: "/evil.example/x" }],
    ["a query not starting with ?", { pathname: "/server", search: "x" }],
    ["a hash not starting with #", { pathname: "/server", hash: "x" }],
    ["a query hiding a newline", { pathname: "/server", search: "?a\n" }],
  ])("goes home instead of to %s", async (_label, from) => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/auth/state": { bootstrap: false, registration_enabled: false } });
    auth.login.mockResolvedValue(undefined);
    renderLogin(from);
    await user.type(screen.getByLabelText("Email"), "ann@example.test");
    await user.type(screen.getByLabelText("Password"), "secret");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByText("home")).toBeInTheDocument();
  });

  it("sends a returning visitor with a session home when the return path leaves the site", async () => {
    fakeAPI({ "/api/v1/auth/state": { bootstrap: false, registration_enabled: false } });
    auth.isAuthenticated = true;
    try {
      renderLogin({ pathname: "//evil.example/x" });
      expect(await screen.findByText("home")).toBeInTheDocument();
    } finally {
      auth.isAuthenticated = false;
    }
  });

  it("sends a returning visitor with a session straight to where they were headed", async () => {
    fakeAPI({ "/api/v1/auth/state": { bootstrap: false, registration_enabled: false } });
    auth.isAuthenticated = true;
    try {
      renderLogin({ pathname: "/server/s1/app/a1", search: "?tab=logs" });
      expect(await screen.findByTestId("where")).toHaveTextContent("/server/s1/app/a1?tab=logs");
    } finally {
      auth.isAuthenticated = false;
    }
  });

  it("offers a button per configured provider that starts its sign-in and comes back to next", async () => {
    fakeAPI({
      "/api/v1/auth/state": {
        bootstrap: false,
        registration_enabled: false,
        oauth_server: true,
        providers: [
          { id: "google", name: "Google" },
          { id: "oidc", name: "Acme SSO" },
        ],
      },
    });
    renderLogin(undefined, "?next=" + encodeURIComponent("/oauth/authorize?client_id=c1&state=s"));
    const google = await screen.findByRole("link", { name: "Continue with Google" });
    const start = new URL(google.getAttribute("href") ?? "", "http://x");
    expect(start.pathname).toBe("/auth/oauth/google/start");
    expect(start.searchParams.get("mode")).toBe("login");
    expect(start.searchParams.get("return_to")).toBe("/oauth/authorize?client_id=c1&state=s");
    expect(screen.getByRole("link", { name: "Continue with Acme SSO" })).toHaveAttribute(
      "href",
      expect.stringContaining("/auth/oauth/oidc/start?"),
    );
  });

  it("shows no provider block when none is configured", async () => {
    const api = fakeAPI({ "/api/v1/auth/state": { bootstrap: false, registration_enabled: false, providers: [] } });
    renderLogin();
    await waitFor(() => expect(api.calls.some((c) => c.path === "/api/v1/auth/state")).toBe(true));
    expect(screen.queryByRole("link", { name: /Continue with/ })).not.toBeInTheDocument();
  });

  it("goes to ?next= after signing in, query included", async () => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/auth/state": { bootstrap: false, registration_enabled: false } });
    auth.login.mockResolvedValue(undefined);
    renderLogin(undefined, "?next=" + encodeURIComponent("/oauth/authorize?client_id=c1&state=s"));
    await signIn(user);
    expect(await screen.findByTestId("where")).toHaveTextContent("/oauth/authorize?client_id=c1&state=s");
  });

  it.each([
    ["a protocol-relative URL", "//evil.example/x"],
    ["a backslash URL", "/\\evil.example/x"],
    ["an absolute URL", "https://evil.example/x"],
    ["a path hiding a newline", "/\n/evil.example"],
  ])("goes home instead of to a ?next= that is %s", async (_label, next) => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/auth/state": { bootstrap: false, registration_enabled: false } });
    auth.login.mockResolvedValue(undefined);
    renderLogin(undefined, "?next=" + encodeURIComponent(next));
    await signIn(user);
    expect(await screen.findByText("home")).toBeInTheDocument();
  });

  it("says why a provider sign-in failed", async () => {
    fakeAPI({ "/api/v1/auth/state": { bootstrap: false, registration_enabled: false } });
    renderLogin(undefined, "?error=registration_closed");
    expect(
      await screen.findByText("There's no Winterflow account for this email, and registration is closed."),
    ).toBeInTheDocument();
  });

  it("says why a provider sign-in failed and lets a retry resume ?next=", async () => {
    fakeAPI({
      "/api/v1/auth/state": {
        bootstrap: false,
        registration_enabled: false,
        providers: [{ id: "google", name: "Google" }],
      },
    });
    const next = "/oauth/authorize?client_id=c1&state=s";
    renderLogin(undefined, "?error=provider_error&next=" + encodeURIComponent(next));
    expect(await screen.findByText("The sign-in provider couldn't sign you in. Try again.")).toBeInTheDocument();
    const google = await screen.findByRole("link", { name: "Continue with Google" });
    const start = new URL(google.getAttribute("href") ?? "", "http://x");
    expect(start.searchParams.get("return_to")).toBe(next);
  });

  it("says an unknown provider error plainly", async () => {
    fakeAPI({ "/api/v1/auth/state": { bootstrap: false, registration_enabled: false } });
    renderLogin(undefined, "?error=weird");
    expect(await screen.findByText("The sign-in didn't work. Try again.")).toBeInTheDocument();
  });
});
