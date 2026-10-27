import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import "@/lib/i18n";
import RegisterPage from "@/pages/register";
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
const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

function renderRegister() {
  return render(
    <MemoryRouter initialEntries={["/register"]}>
      <Routes>
        <Route path="/register" element={<RegisterPage />} />
        <Route path="/login" element={<p>login page</p>} />
        <Route path="/" element={<p>home</p>} />
      </Routes>
    </MemoryRouter>,
  );
}

async function fill(user: ReturnType<typeof userEvent.setup>, password = "hunter22", confirm = password) {
  await user.type(screen.getByLabelText("Name"), "Ann");
  await user.type(screen.getByLabelText("Email"), "ann@example.test");
  await user.type(screen.getByLabelText("Password"), password);
  await user.type(screen.getByLabelText("Confirm password"), confirm);
}

beforeEach(() => {
  auth.login.mockReset();
  auth.signedIn.mockReset();
  toast.success.mockReset();
});
afterEach(() => vi.unstubAllGlobals());

describe("RegisterPage", () => {
  it("sets up a fresh instance: the first account, signed in at once", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({
      "/api/v1/auth/state": { bootstrap: true, registration_enabled: true },
      "/api/v1/auth/register": { user_id: "u1" },
    });
    renderRegister();
    expect(await screen.findByRole("heading", { level: 1, name: "Set up Winterflow" })).toBeInTheDocument();
    // Nobody can sign in yet: no account exists.
    expect(screen.queryByRole("link", { name: "Sign in" })).not.toBeInTheDocument();
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Set up Winterflow" }));
    expect(await screen.findByText("home")).toBeInTheDocument();
    expect(api.posted("/api/v1/auth/register")).toEqual([{ name: "Ann", email: "ann@example.test", password: "hunter22" }]);
    // The register answer set the session: no second sign-in.
    expect(auth.signedIn).toHaveBeenCalled();
    expect(auth.login).not.toHaveBeenCalled();
    const init = (api.fetchMock.mock.calls.find(([u]) => String(u).endsWith("/api/v1/auth/register")) ?? [])[1];
    expect(new Headers(init?.headers).get("X-Winterflow-Request")).toBe("1");
  });

  it("is an ordinary sign-up on a set-up instance with sign-up open", async () => {
    fakeAPI({ "/api/v1/auth/state": { bootstrap: false, registration_enabled: true } });
    renderRegister();
    expect(await screen.findByRole("heading", { level: 1, name: "Create an account" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Sign in" })).toHaveAttribute("href", "/login");
  });

  it("says sign-up is closed when it is", async () => {
    fakeAPI({ "/api/v1/auth/state": { bootstrap: false, registration_enabled: false } });
    renderRegister();
    expect(await screen.findByRole("heading", { level: 1, name: "Registration is closed" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Back to sign in" })).toHaveAttribute("href", "/login");
  });

  it("offers the form when the state can't be read (the server decides on submit)", async () => {
    fakeAPI({ "/api/v1/auth/state": new Error("down") });
    renderRegister();
    expect(await screen.findByRole("heading", { level: 1, name: "Create an account" })).toBeInTheDocument();
  });

  it("points at the field that is wrong instead of sending", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({ "/api/v1/auth/state": { bootstrap: false, registration_enabled: true } });
    renderRegister();
    await screen.findByRole("heading", { level: 1, name: "Create an account" });
    await fill(user, "hunter22", "hunter23");
    await user.click(screen.getByRole("button", { name: "Create account" }));
    expect(screen.getByRole("alert")).toHaveTextContent("The passwords don't match.");
    expect(screen.getByLabelText("Confirm password")).toHaveAttribute("aria-invalid", "true");
    expect(api.posted("/api/v1/auth/register")).toEqual([]);

    await user.clear(screen.getByLabelText("Password"));
    await user.type(screen.getByLabelText("Password"), "abc");
    await user.click(screen.getByRole("button", { name: "Create account" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Use at least 4 characters.");
    expect(screen.getByLabelText("Password")).toHaveAttribute("aria-invalid", "true");
  });

  it("shows the server's refusal on the form", async () => {
    const user = userEvent.setup();
    fakeAPI({
      "/api/v1/auth/state": { bootstrap: false, registration_enabled: true },
      "/api/v1/auth/register": new Error("email already registered"),
    });
    renderRegister();
    await screen.findByRole("heading", { level: 1, name: "Create an account" });
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Create account" }));
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("email already registered"));
    expect(auth.signedIn).not.toHaveBeenCalled();
  });
});
