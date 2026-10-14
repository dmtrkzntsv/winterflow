import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ProfileContext, type ProfileContextValue } from "@/context/profile-context-base";
import UserPasswordPage from "@/pages/user-password";
import { fakeAPI } from "@/test/fake-api";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

afterEach(() => vi.unstubAllGlobals());

function renderPage(mustChange = false) {
  const refresh = vi.fn().mockResolvedValue(undefined);
  const profile = {
    profile: { user_id: "u1", name: "Ann", email: "ann@example.test", must_change_password: mustChange },
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
