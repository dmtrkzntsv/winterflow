import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ChannelsSection } from "@/components/alerts/channels-section";
import { useAlertChannels } from "@/hooks/use-alert-channels";
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

function Harness() {
  const c = useAlertChannels();
  return <ChannelsSection channels={c.channels} loading={c.loading} error={c.error} onChanged={c.refresh} />;
}

describe("ChannelsSection", () => {
  it("lists channels as rows with their state, tests one and deletes one after a confirmation", async () => {
    let list = channels;
    const api = fakeAPI({
      "/api/v1/alerts/get-channels": () => list,
      "/api/v1/alerts/delete-channel": () => {
        list = channels.filter((c) => c.id !== "c2");
        return null;
      },
      "/api/v1/alerts/test-channel": { ok: true, error: "" },
    });
    const user = userEvent.setup({ delay: null });
    render(<Harness />);

    const rows = await screen.findAllByTestId("channel-row");
    expect(rows).toHaveLength(2);
    expect(within(rows[0]).getByText("Default")).toBeInTheDocument();
    expect(within(rows[0]).getByText("Telegram")).toBeInTheDocument();
    expect(within(rows[1]).getByText("Off")).toBeInTheDocument();
    const failed = within(rows[1]).getByText("Last delivery failed: connection refused");
    expect(failed).toBeInTheDocument();
    // Muted text beside a dot, not red text.
    expect(failed.closest("span.text-status-bad")).toBeNull();

    await user.click(within(rows[0]).getByRole("button", { name: "Actions for Ops" }));
    await user.click(await screen.findByRole("menuitem", { name: "Send test" }));
    await waitFor(() => expect(api.posted("/api/v1/alerts/test-channel")).toEqual([{ id: "c1" }]));

    await user.click(within(rows[1]).getByRole("button", { name: "Actions for Hook" }));
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(confirm).toHaveTextContent("Delete Hook?");
    await user.click(within(confirm).getByRole("button", { name: "Delete channel" }));
    await waitFor(() => expect(api.posted("/api/v1/alerts/delete-channel")).toEqual([{ id: "c2" }]));
    // Focus lands on Add channel, not <body>, once the row is gone.
    await waitFor(() => expect(screen.getByRole("button", { name: /Add channel/ })).toHaveFocus());
    expect(screen.getAllByTestId("channel-row")).toHaveLength(1);
  });

  it("keeps the confirmation open and the row when a delete fails", async () => {
    fakeAPI({
      "/api/v1/alerts/get-channels": channels,
      "/api/v1/alerts/delete-channel": new Error("channel is in use"),
    });
    const user = userEvent.setup({ delay: null });
    render(<Harness />);
    const rows = await screen.findAllByTestId("channel-row");
    await user.click(within(rows[1]).getByRole("button", { name: "Actions for Hook" }));
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
    const confirm = await screen.findByRole("alertdialog");
    await user.click(within(confirm).getByRole("button", { name: "Delete channel" }));
    await waitFor(() => expect(within(confirm).getByRole("button", { name: "Delete channel" })).toBeEnabled());
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(screen.getAllByTestId("channel-row")).toHaveLength(2);
    await user.click(within(confirm).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Actions for Hook" })).toHaveFocus());
  });

  it("explains the empty state", async () => {
    fakeAPI({ "/api/v1/alerts/get-channels": null });
    render(<Harness />);
    expect(await screen.findByText(/No channels yet/)).toBeInTheDocument();
  });

  it("says the list could not be loaded instead of claiming there are no channels", async () => {
    fakeAPI({ "/api/v1/alerts/get-channels": new Error("boom") });
    render(<Harness />);
    expect(await screen.findByRole("alert")).toHaveTextContent("boom");
    expect(screen.queryByText(/No channels yet/)).not.toBeInTheDocument();
  });

  it("offers Try again when the first load fails", async () => {
    let fail = true;
    fakeAPI({ "/api/v1/alerts/get-channels": () => (fail ? new Error("boom") : channels) });
    const user = userEvent.setup({ delay: null });
    render(<Harness />);
    expect(await screen.findByRole("alert")).toHaveTextContent("boom");
    fail = false;
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findAllByTestId("channel-row")).toHaveLength(2);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("adds a channel from a grid of services, Other (URL) among them", async () => {
    fakeAPI({ "/api/v1/alerts/get-channels": [] });
    const user = userEvent.setup({ delay: null });
    render(<Harness />);
    await user.click(await screen.findByRole("button", { name: /Add channel/ }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("radio", { name: "Telegram" })).toHaveAttribute("aria-checked", "true");
    expect(within(dialog).getByRole("radio", { name: "Other (URL)" })).toBeInTheDocument();
    expect(within(dialog).queryByRole("radio", { name: /Shoutrrr/ })).not.toBeInTheDocument();
  });
});
