import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import UserIdentitiesPage from "@/pages/user-identities";
import { ApiError } from "@/lib/api";
import { resetAuthStateCache } from "@/lib/auth-state";
import { fakeAPI } from "@/test/fake-api";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

const STATE = {
  bootstrap: false,
  registration_enabled: false,
  oauth_server: true,
  providers: [
    { id: "google", name: "Google" },
    { id: "oidc", name: "Acme SSO" },
  ],
};
const GOOGLE = { provider: "google", email: "ann@gmail.test", created_at: new Date().toISOString() };

function renderPage(search = "") {
  return render(
    <MemoryRouter initialEntries={[`/settings/identities${search}`]}>
      <Routes>
        <Route path="/settings/identities" element={<UserIdentitiesPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  resetAuthStateCache();
  toast.success.mockReset();
  toast.error.mockReset();
});
afterEach(() => vi.unstubAllGlobals());

describe("UserIdentitiesPage", () => {
  it("lists linked accounts and offers to link the other providers, coming back here", async () => {
    fakeAPI({ "/api/v1/auth/state": STATE, "/api/v1/user/get-identities": [GOOGLE] });
    renderPage();
    const list = await screen.findByRole("list", { name: "Linked accounts" });
    expect(within(list).getByText("Google")).toBeInTheDocument();
    expect(within(list).getByText("ann@gmail.test")).toBeInTheDocument();
    const link = await screen.findByRole("link", { name: "Link Acme SSO" });
    const start = new URL(link.getAttribute("href") ?? "", "http://x");
    expect(start.pathname).toBe("/auth/oauth/oidc/start");
    expect(start.searchParams.get("mode")).toBe("link");
    expect(start.searchParams.get("return_to")).toBe("/settings/identities");
    expect(screen.queryByRole("link", { name: "Link Google" })).not.toBeInTheDocument();
  });

  it("unlinks after a confirmation", async () => {
    let list = [GOOGLE];
    const api = fakeAPI({
      "/api/v1/auth/state": STATE,
      "/api/v1/user/get-identities": () => list,
      "/api/v1/user/unlink-identity": () => {
        list = [];
        return null;
      },
    });
    const user = userEvent.setup({ delay: null });
    renderPage();
    await user.click(await screen.findByRole("button", { name: "Unlink" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(confirm).toHaveTextContent("Unlink Google?");
    await user.click(within(confirm).getByRole("button", { name: "Unlink" }));
    await waitFor(() => expect(api.posted("/api/v1/user/unlink-identity")).toEqual([{ provider: "google" }]));
    expect(await screen.findByText(/No linked accounts/)).toBeInTheDocument();
    expect(await screen.findByRole("link", { name: "Link Google" })).toBeInTheDocument();
  });

  it("says why the only way in can't be unlinked", async () => {
    fakeAPI({
      "/api/v1/auth/state": STATE,
      "/api/v1/user/get-identities": [GOOGLE],
      "/api/v1/user/unlink-identity": new ApiError("it is the only way to sign in to this account", 409),
    });
    const user = userEvent.setup({ delay: null });
    renderPage();
    await user.click(await screen.findByRole("button", { name: "Unlink" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Unlink" }));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Couldn't unlink the account", {
        description: "it is the only way to sign in to this account",
      }),
    );
  });

  it("says why a link failed, until dismissed", async () => {
    fakeAPI({ "/api/v1/auth/state": STATE, "/api/v1/user/get-identities": [] });
    const user = userEvent.setup({ delay: null });
    renderPage("?error=identity_taken");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "That provider account is already linked to another Winterflow account.",
    );
    await user.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
