import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import UserConnectedAppsPage from "@/pages/user-connected-apps";
import { ApiError } from "@/lib/api";
import { fakeAPI } from "@/test/fake-api";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

const now = new Date().toISOString();
const CLAUDE = {
  grant_id: "g1",
  client_name: "Claude",
  redirect_host: "claude.ai",
  is_loopback: false,
  created_at: now,
  last_used_at: now,
};
const LOCAL = { ...CLAUDE, grant_id: "g2", client_name: "Cursor", redirect_host: "127.0.0.1", is_loopback: true };

beforeEach(() => {
  toast.success.mockReset();
  toast.error.mockReset();
});
afterEach(() => vi.unstubAllGlobals());

describe("UserConnectedAppsPage", () => {
  it("lists the apps with where they answer, and disconnects one after a confirmation", async () => {
    let list = [CLAUDE, LOCAL];
    const api = fakeAPI({
      "/api/v1/user/get-connected-apps": () => list,
      "/api/v1/user/revoke-connected-app": () => {
        list = [LOCAL];
        return null;
      },
    });
    const user = userEvent.setup({ delay: null });
    render(<UserConnectedAppsPage />);
    expect(await screen.findByText("Claude")).toBeInTheDocument();
    expect(screen.getByText("claude.ai")).toBeInTheDocument();
    expect(screen.getByText("On your computer")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Disconnect Claude" }));
    const confirm = await screen.findByRole("alertdialog");
    await user.click(within(confirm).getByRole("button", { name: "Disconnect" }));
    await waitFor(() => expect(api.posted("/api/v1/user/revoke-connected-app")).toEqual([{ grant_id: "g1" }]));
    await waitFor(() => expect(screen.queryByText("Claude")).not.toBeInTheDocument());
    expect(screen.getByText("Cursor")).toBeInTheDocument();
    expect(toast.success).toHaveBeenCalledWith("Claude disconnected");
  });

  it("keeps the app and says why when disconnecting fails", async () => {
    fakeAPI({
      "/api/v1/user/get-connected-apps": [CLAUDE],
      "/api/v1/user/revoke-connected-app": new Error("grant not found"),
    });
    const user = userEvent.setup({ delay: null });
    render(<UserConnectedAppsPage />);
    await user.click(await screen.findByRole("button", { name: "Disconnect Claude" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Disconnect" }));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Couldn't disconnect the app", { description: "grant not found" }),
    );
    expect(screen.getByText("Claude")).toBeInTheDocument();
  });

  it("says when none is connected", async () => {
    fakeAPI({ "/api/v1/user/get-connected-apps": null });
    render(<UserConnectedAppsPage />);
    expect(await screen.findByText(/No connected apps/)).toBeInTheDocument();
  });

  it("says when connecting apps is off on this server", async () => {
    fakeAPI({ "/api/v1/user/get-connected-apps": new ApiError("not found", 404) });
    render(<UserConnectedAppsPage />);
    expect(await screen.findByText("Connecting apps isn't available on this Winterflow.")).toBeInTheDocument();
  });
});
