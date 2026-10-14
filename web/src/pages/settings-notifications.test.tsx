import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import NotificationsSettingsPage from "@/pages/settings-notifications";
import { fakeAPI } from "@/test/fake-api";

vi.mock("@/context/use-servers", () => ({ useServers: () => ({ servers: [] }) }));

afterEach(() => vi.unstubAllGlobals());

const T = "2026-10-11T12:00:00Z";

describe("NotificationsSettingsPage", () => {
  it("puts the channels and the alert rules on one page", async () => {
    fakeAPI({ "/api/v1/alerts/get-rules": [], "/api/v1/alerts/get-channels": [] });
    render(
      <MemoryRouter>
        <NotificationsSettingsPage />
      </MemoryRouter>,
    );
    expect(screen.getByRole("heading", { level: 1, name: "Notifications" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Channels" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Alert rules" })).toBeInTheDocument();
    expect(await screen.findByText(/No channels yet/)).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: /Add rule/ })).toBeInTheDocument();
  });

  it("follows a channel rename in the rules without a reload", async () => {
    let name = "Ops";
    fakeAPI({
      "/api/v1/alerts/get-rules": [
        { id: "r1", target_kind: "server", metric: "cpu", threshold: 90, duration_minutes: 10, channel_ids: ["c1"], enabled: true, created_at: T },
      ],
      "/api/v1/alerts/get-channels": () => [
        {
          id: "c1",
          name,
          service: "ntfy",
          fields: { ui_service: "ntfy", server: "ntfy.sh" },
          is_default: true,
          enabled: true,
          last_error: "",
          last_sent_at: null,
          created_at: T,
        },
      ],
      "/api/v1/alerts/save-channel": () => {
        name = "Pager";
        return {};
      },
    });
    const user = userEvent.setup({ delay: null });
    render(
      <MemoryRouter>
        <NotificationsSettingsPage />
      </MemoryRouter>,
    );
    const rules = screen.getByRole("region", { name: "Alert rules" });
    expect(await within(rules).findByText("Ops")).toBeInTheDocument();
    await user.click(await screen.findByRole("button", { name: "Actions for Ops" }));
    await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
    const dialog = await screen.findByRole("dialog");
    await user.clear(within(dialog).getByLabelText("Name"));
    await user.type(within(dialog).getByLabelText("Name"), "Pager");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await within(rules).findByText("Pager")).toBeInTheDocument();
    expect(within(rules).queryByText("deleted channel")).not.toBeInTheDocument();
  });
});
