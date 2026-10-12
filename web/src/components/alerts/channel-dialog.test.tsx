import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ChannelDialog } from "@/components/alerts/channel-dialog";
import type { Channel } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";

afterEach(() => vi.unstubAllGlobals());

const stored: Channel = {
  id: "c1",
  name: "Ops",
  service: "ntfy",
  fields: { ui_service: "ntfy", server: "ntfy.example.com" },
  is_default: true,
  enabled: true,
  last_error: "",
  last_sent_at: null,
  created_at: "2026-10-11T12:00:00Z",
};

describe("ChannelDialog", () => {
  it("builds the Shoutrrr URL from the form and keeps secrets out of fields", async () => {
    const api = fakeAPI({ "/api/v1/alerts/save-channel": { id: "c9" } });
    const onSaved = vi.fn();
    const user = userEvent.setup({ delay: null });
    render(<ChannelDialog open onOpenChange={() => {}} onSaved={onSaved} />);

    await user.type(screen.getByLabelText("Name"), "Phone");
    await user.click(screen.getByRole("radio", { name: "ntfy" }));
    await user.type(screen.getByLabelText("Topic"), "wf-alerts");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(api.posted("/api/v1/alerts/save-channel")).toEqual([
      {
        id: "",
        name: "Phone",
        url: "ntfy://ntfy.sh/wf-alerts",
        fields: { ui_service: "ntfy", server: "ntfy.sh" },
        is_default: true,
        enabled: true,
      },
    ]);
    expect(onSaved).toHaveBeenCalled();
  });

  it("explains an invalid form instead of saving", async () => {
    const api = fakeAPI({});
    const user = userEvent.setup({ delay: null });
    render(<ChannelDialog open onOpenChange={() => {}} onSaved={() => {}} />);
    await user.type(screen.getByLabelText("Name"), "Bot");
    await user.type(screen.getByLabelText("Bot token"), "not-a-token");
    await user.type(screen.getByLabelText("Chats"), "1");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(screen.getByRole("alert")).toHaveTextContent("123456789:ABC");
    expect(api.calls).toHaveLength(0);
  });

  it("tests a draft by URL and shows the outcome", async () => {
    const api = fakeAPI({ "/api/v1/alerts/test-channel": { ok: false, error: "403 Forbidden" } });
    const user = userEvent.setup({ delay: null });
    render(<ChannelDialog open onOpenChange={() => {}} onSaved={() => {}} />);
    await user.click(screen.getByRole("radio", { name: "Webhook" }));
    await user.type(screen.getByLabelText("URL"), "https://hooks.example.com/x");
    await user.click(screen.getByRole("button", { name: /Send test/ }));
    expect(await screen.findByRole("status")).toHaveTextContent("Test failed: 403 Forbidden");
    expect(api.posted("/api/v1/alerts/test-channel")).toEqual([
      { url: "generic://hooks.example.com/x?template=json" },
    ]);
  });

  it("edits name and flags without resending the destination", async () => {
    const api = fakeAPI({
      "/api/v1/alerts/save-channel": stored,
      "/api/v1/alerts/test-channel": { ok: true, error: "" },
    });
    const user = userEvent.setup({ delay: null });
    render(<ChannelDialog open onOpenChange={() => {}} channel={stored} onSaved={() => {}} />);

    expect(screen.getByTestId("channel-destination")).toHaveTextContent("ntfy · ntfy.example.com");
    await user.click(screen.getByRole("button", { name: /Send test/ }));
    expect(api.posted("/api/v1/alerts/test-channel")).toEqual([{ id: "c1" }]);

    await user.clear(screen.getByLabelText("Name"));
    await user.type(screen.getByLabelText("Name"), "Ops 2");
    await user.click(screen.getByLabelText("Default channel"));
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(api.posted("/api/v1/alerts/save-channel")).toEqual([
      { id: "c1", name: "Ops 2", url: "", fields: {}, is_default: false, enabled: true },
    ]);
  });

  it("asks for the secrets again when the destination changes", async () => {
    const api = fakeAPI({ "/api/v1/alerts/save-channel": stored });
    const user = userEvent.setup({ delay: null });
    render(<ChannelDialog open onOpenChange={() => {}} channel={stored} onSaved={() => {}} />);
    await user.click(screen.getByRole("button", { name: "Change destination" }));
    // The public server is prefilled; the secret topic is not.
    expect(screen.getByLabelText("Server")).toHaveValue("ntfy.example.com");
    expect(screen.getByLabelText("Topic")).toHaveValue("");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Topic is required");
    await user.type(screen.getByLabelText("Topic"), "t2");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(api.posted("/api/v1/alerts/save-channel")[0]).toMatchObject({ url: "ntfy://ntfy.example.com/t2" });
  });
});
