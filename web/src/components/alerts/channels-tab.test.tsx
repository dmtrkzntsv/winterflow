import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ChannelsTab } from "@/components/alerts/channels-tab";
import type { Channel } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";

afterEach(() => vi.unstubAllGlobals());

const channels: Channel[] = [
  {
    id: "c1",
    name: "Ops",
    service: "telegram",
    fields: { ui_service: "telegram" },
    is_default: true,
    enabled: true,
    last_error: "",
    last_sent_at: "2026-10-11T12:00:00Z",
    created_at: "2026-10-11T11:00:00Z",
  },
  {
    id: "c2",
    name: "Hook",
    service: "generic",
    fields: {},
    is_default: false,
    enabled: false,
    last_error: "dial tcp: connection refused",
    last_sent_at: null,
    created_at: "2026-10-11T11:00:00Z",
  },
];

describe("ChannelsTab", () => {
  it("lists channels with their state and deletes after confirmation", async () => {
    const api = fakeAPI({
      "/api/v1/alerts/get-channels": channels,
      "/api/v1/alerts/delete-channel": null,
      "/api/v1/alerts/test-channel": { ok: true, error: "" },
    });
    const user = userEvent.setup({ delay: null });
    render(<ChannelsTab />);

    const rows = await screen.findAllByTestId("channel-row");
    expect(rows).toHaveLength(2);
    expect(within(rows[0]).getByText("Default")).toBeInTheDocument();
    expect(within(rows[0]).getByText("Telegram")).toBeInTheDocument();
    expect(within(rows[1]).getByText("Disabled")).toBeInTheDocument();
    expect(within(rows[1]).getByText(/Last delivery failed: dial tcp/)).toBeInTheDocument();

    await user.click(within(rows[0]).getByRole("button", { name: /Test/ }));
    expect(api.posted("/api/v1/alerts/test-channel")).toEqual([{ id: "c1" }]);

    await user.click(screen.getByRole("button", { name: "Delete Hook" }));
    await user.click(await screen.findByRole("button", { name: "Delete" }));
    expect(api.posted("/api/v1/alerts/delete-channel")).toEqual([{ id: "c2" }]);
  });

  it("explains the empty state", async () => {
    fakeAPI({ "/api/v1/alerts/get-channels": null });
    render(<ChannelsTab />);
    expect(await screen.findByText("No channels yet")).toBeInTheDocument();
  });
});
