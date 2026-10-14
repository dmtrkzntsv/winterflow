import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import UserTokensPage from "@/pages/user-tokens";
import { fakeAPI } from "@/test/fake-api";

const toastError = vi.hoisted(() => vi.fn());
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: toastError } }));

const TOKEN = {
  token_id: "t1",
  name: "CI deploy",
  prefix: "wfp_ab12",
  expires_at: null,
  last_used_at: null,
  created_at: "2026-10-01T00:00:00Z",
};

describe("UserTokensPage", () => {
  beforeEach(() => {
    toastError.mockReset();
    vi.spyOn(window, "confirm");
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("lists tokens as rows and revokes one after a confirmation, never window.confirm", async () => {
    let list = [TOKEN];
    const api = fakeAPI({
      "/api/v1/user/get-tokens": () => list,
      "/api/v1/user/delete-token": () => {
        list = [];
        return null;
      },
    });
    const user = userEvent.setup({ delay: null });
    render(<UserTokensPage />);
    expect(screen.getByRole("heading", { level: 1, name: "API tokens" })).toBeInTheDocument();
    expect(await screen.findByText("CI deploy")).toBeInTheDocument();
    expect(screen.getByText("wfp_ab12…")).toBeInTheDocument();
    expect(screen.getByText(/Never expires · Never used/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Revoke CI deploy" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(confirm).toHaveTextContent("Anything that uses it stops working at once.");
    await user.click(within(confirm).getByRole("button", { name: "Revoke token" }));
    await waitFor(() => expect(api.posted("/api/v1/user/delete-token")).toEqual([{ token_id: "t1" }]));
    expect(window.confirm).not.toHaveBeenCalled();
    // The row is gone: focus lands on Generate token, not <body>.
    await waitFor(() => expect(screen.getByRole("button", { name: /Generate token/ })).toHaveFocus());
    expect(screen.queryByText("CI deploy")).not.toBeInTheDocument();
  });

  it("keeps the confirmation when a revoke fails", async () => {
    fakeAPI({ "/api/v1/user/get-tokens": [TOKEN], "/api/v1/user/delete-token": new Error("token not found") });
    const user = userEvent.setup({ delay: null });
    render(<UserTokensPage />);
    await user.click(await screen.findByRole("button", { name: "Revoke CI deploy" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Revoke token" }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith("Couldn't revoke the token", { description: "token not found" }));
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Revoke CI deploy" })).toHaveFocus());
  });

  it("drops a revoked token even when listing again fails, and shows the error above the rest", async () => {
    const other = { ...TOKEN, token_id: "t3", name: "Backup script" };
    let reads = 0;
    fakeAPI({
      "/api/v1/user/get-tokens": () => (++reads === 1 ? [TOKEN, other] : new Error("db down")),
      "/api/v1/user/delete-token": null,
    });
    const user = userEvent.setup({ delay: null });
    render(<UserTokensPage />);
    await user.click(await screen.findByRole("button", { name: "Revoke CI deploy" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Revoke token" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("db down");
    expect(screen.queryByText("CI deploy")).not.toBeInTheDocument();
    expect(screen.getByText("Backup script")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
  });

  it("reports a load failure with a way to try again", async () => {
    const api = fakeAPI({ "/api/v1/user/get-tokens": new Error("db down") });
    const user = userEvent.setup({ delay: null });
    render(<UserTokensPage />);
    expect(await screen.findByText("db down")).toBeInTheDocument();
    expect(screen.queryByText(/No tokens yet/)).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(api.posted("/api/v1/user/get-tokens")).toHaveLength(2));
  });

  it("generates a token with an expiry and shows it once with Copy", async () => {
    const api = fakeAPI({
      "/api/v1/user/get-tokens": [],
      "/api/v1/user/create-token": { token_id: "t2", token: "wfp_SECRET", prefix: "wfp_SE", name: "Backup script", expires_at: null },
    });
    const user = userEvent.setup({ delay: null });
    render(<UserTokensPage />);
    expect(await screen.findByText(/No tokens yet/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Generate token/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Generate" }));
    expect(within(dialog).getByRole("alert")).toHaveTextContent("Give the token a name.");
    await user.type(within(dialog).getByLabelText("Name"), "Backup script");
    await user.click(within(dialog).getByRole("radio", { name: "1 year" }));
    await user.click(within(dialog).getByRole("button", { name: "Generate" }));
    expect(await screen.findByLabelText("Token")).toHaveValue("wfp_SECRET");
    expect(screen.getByRole("button", { name: "Copy Token" })).toBeInTheDocument();
    expect(api.posted("/api/v1/user/create-token")).toEqual([{ name: "Backup script", expires_in_days: 365 }]);
    // The token is gone once the dialog closes.
    await user.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => expect(screen.queryByLabelText("Token")).not.toBeInTheDocument());
    await user.click(screen.getByRole("button", { name: /Generate token/ }));
    expect(await screen.findByLabelText("Name")).toHaveValue("");
    expect(screen.queryByDisplayValue("wfp_SECRET")).not.toBeInTheDocument();
  });
});
