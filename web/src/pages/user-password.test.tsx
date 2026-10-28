import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ProfileContext, type ProfileContextValue } from "@/context/profile-context-base";
import UserPasswordPage from "@/pages/user-password";
import { fakeAPI } from "@/test/fake-api";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

afterEach(() => vi.unstubAllGlobals());

function renderPage(mustChange = false, email = "ann@example.test") {
  const refresh = vi.fn().mockResolvedValue(undefined);
  const profile = {
    profile: { user_id: "u1", name: "Ann", email, must_change_password: mustChange },
    refresh,
  } as unknown as ProfileContextValue;
  render(
    <ProfileContext.Provider value={profile}>
      <MemoryRouter initialEntries={["/settings/password"]}>
        <Routes>
          <Route path="/settings/password" element={<UserPasswordPage />} />
          <Route path="/" element={<p>home</p>} />
        </Routes>
      </MemoryRouter>
    </ProfileContext.Provider>,
  );
  return { refresh };
}

async function fill(user: ReturnType<typeof userEvent.setup>, current: string, next: string, confirm: string) {
  await user.type(screen.getByLabelText("Current password"), current);
  await user.type(screen.getByLabelText("New password"), next);
  await user.type(screen.getByLabelText("Confirm new password"), confirm);
  await user.click(screen.getByRole("button", { name: "Change password" }));
}

describe("UserPasswordPage", () => {
  it("tells password managers which field is which", () => {
    renderPage();
    expect(screen.getByLabelText("Current password")).toHaveAttribute("autocomplete", "current-password");
    expect(screen.getByLabelText("New password")).toHaveAttribute("autocomplete", "new-password");
    expect(screen.getByLabelText("Confirm new password")).toHaveAttribute("autocomplete", "new-password");
  });

  it("changes the password and goes home", async () => {
    const api = fakeAPI({ "/api/v1/user/change-password": null });
    const user = userEvent.setup({ delay: null });
    const { refresh } = renderPage();
    expect(screen.getByRole("heading", { level: 1, name: "Password" })).toBeInTheDocument();
    await fill(user, "old-pass", "new-pass", "new-pass");
    expect(api.posted("/api/v1/user/change-password")).toEqual([{ current_password: "old-pass", new_password: "new-pass" }]);
    expect(await screen.findByText("home")).toBeInTheDocument();
    expect(refresh).toHaveBeenCalled();
  });

  it("says under the field what is wrong, without sending", async () => {
    const api = fakeAPI({});
    const user = userEvent.setup({ delay: null });
    renderPage();
    await fill(user, "old-pass", "new-pass", "other");
    expect(screen.getByRole("alert")).toHaveTextContent("The two new passwords don't match.");
    expect(screen.getByLabelText("Confirm new password")).toHaveAttribute("aria-invalid", "true");
    expect(api.calls).toHaveLength(0);
  });

  it("shows a refusal on the page", async () => {
    fakeAPI({ "/api/v1/user/change-password": new Error("current password is wrong") });
    const user = userEvent.setup({ delay: null });
    renderPage();
    await fill(user, "bad", "new-pass", "new-pass");
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("current password is wrong"));
  });

  it("explains a temporary password", () => {
    fakeAPI({});
    renderPage(true);
    // A notice, not an alert: only the form's own errors are alerts.
    expect(screen.getByRole("status")).toHaveTextContent(/temporary password/);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});

describe("UserPasswordPage, an account without a password", () => {
  const identity = (provider: string, email: string) => ({ provider, email, created_at: "2026-10-01T00:00:00Z" });

  async function fillNew(user: ReturnType<typeof userEvent.setup>, next: string, confirm: string) {
    await user.type(screen.getByLabelText("New password"), next);
    await user.type(screen.getByLabelText("Confirm new password"), confirm);
    await user.click(screen.getByRole("button", { name: "Set password" }));
  }

  it("sets a first password for the linked account's email, without a current one", async () => {
    const api = fakeAPI({
      "/api/v1/user/get-identities": [identity("google", "Ann@Example.test")],
      "/api/v1/user/set-password": null,
    });
    const user = userEvent.setup({ delay: null });
    const { refresh } = renderPage(false, "");
    expect(screen.getByRole("heading", { level: 1, name: "Set a password" })).toBeInTheDocument();
    expect(await screen.findByText("ann@example.test")).toBeInTheDocument();
    expect(screen.queryByLabelText("Current password")).not.toBeInTheDocument();
    expect(screen.getByLabelText("New password")).toHaveAttribute("autocomplete", "new-password");
    await fillNew(user, "new-pass", "new-pass");
    expect(api.posted("/api/v1/user/set-password")).toEqual([{ email: "ann@example.test", new_password: "new-pass" }]);
    expect(await screen.findByText("home")).toBeInTheDocument();
    expect(refresh).toHaveBeenCalled();
    expect(api.posted("/api/v1/user/change-password")).toEqual([]);
  });

  it("lets the user pick among the linked accounts' emails", async () => {
    const api = fakeAPI({
      "/api/v1/user/get-identities": [identity("google", "ann@example.test"), identity("oidc", "ann@work.test")],
      "/api/v1/user/set-password": null,
    });
    const user = userEvent.setup({ delay: null });
    renderPage(false, "");
    await user.click(await screen.findByRole("radio", { name: "ann@work.test" }));
    await fillNew(user, "new-pass", "new-pass");
    expect(api.posted("/api/v1/user/set-password")).toEqual([{ email: "ann@work.test", new_password: "new-pass" }]);
  });

  it("checks the confirmation and shows a refusal", async () => {
    const api = fakeAPI({
      "/api/v1/user/get-identities": [identity("google", "ann@example.test")],
      "/api/v1/user/set-password": Object.assign(new Error("another account uses this email address"), { status: 409 }),
    });
    const user = userEvent.setup({ delay: null });
    renderPage(false, "");
    await screen.findByText("ann@example.test");
    await fillNew(user, "new-pass", "other");
    expect(screen.getByRole("alert")).toHaveTextContent("The two new passwords don't match.");
    expect(api.posted("/api/v1/user/set-password")).toEqual([]);
    await user.clear(screen.getByLabelText("Confirm new password"));
    await user.type(screen.getByLabelText("Confirm new password"), "new-pass");
    await user.click(screen.getByRole("button", { name: "Set password" }));
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("another account uses this email address"));
  });

  it("can't set a password when no linked account gave an email", async () => {
    fakeAPI({ "/api/v1/user/get-identities": [identity("apple", "")] });
    renderPage(false, "");
    expect(await screen.findByRole("status")).toHaveTextContent(/can't be set/);
    expect(screen.getByRole("button", { name: "Set password" })).toBeDisabled();
  });
});
