import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { OverrideDialog } from "@/components/alerts/override-dialog";
import type { Channel, Override, Rule } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";

afterEach(() => vi.unstubAllGlobals());

const rule: Rule = {
  id: "r1",
  target_kind: "server",
  metric: "cpu",
  threshold: 90,
  duration_minutes: 10,
  channel_ids: [],
  enabled: true,
  created_at: "2026-10-11T12:00:00Z",
};
const channels = [{ id: "c1", name: "Ops", enabled: true }] as Channel[];
const existing: Override = {
  rule_id: "r1",
  server_id: "s1",
  app_id: "",
  disabled: false,
  threshold: 95,
  duration_minutes: null,
  channel_ids: [],
};

function open(props: { override?: Override; onOpenChange?: (o: boolean) => void } = {}) {
  return render(
    <OverrideDialog
      open
      onOpenChange={props.onOpenChange ?? (() => {})}
      rule={rule}
      override={props.override}
      channels={channels}
      serverId="s1"
      appId=""
      targetLabel="prod-1"
      onSaved={() => {}}
    />,
  );
}

describe("OverrideDialog", () => {
  it("reads like the rule dialog and says a silent rule goes to the Inbox only", () => {
    fakeAPI({});
    open();
    const dialog = screen.getByRole("dialog");
    // The description says it (the "Inbox only" radio label would pass this check on its own).
    expect(dialog).toHaveAccessibleDescription(/inbox only\. Empty fields keep the rule's values\./);
    expect(within(dialog).getByText("Alert when it")).toBeInTheDocument();
    expect(within(dialog).getByText("Then notify")).toBeInTheDocument();
    expect(screen.getByRole("radiogroup", { name: "Notify" })).toBeInTheDocument();
  });

  it("closes on Cancel without saving", async () => {
    const api = fakeAPI({});
    const onOpenChange = vi.fn();
    const user = userEvent.setup({ delay: null });
    open({ onOpenChange });
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(api.calls).toHaveLength(0);
  });

  it("shows its own message for an out-of-range duration instead of the browser's", async () => {
    const api = fakeAPI({});
    const user = userEvent.setup({ delay: null });
    open();
    await user.type(screen.getByLabelText("For (minutes)"), "90");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(screen.getByRole("alert")).toHaveTextContent("1 to 60 minutes");
    expect(api.calls).toHaveLength(0);
  });

  it("saves an adjustment and resets one back to the rule", async () => {
    const api = fakeAPI({ "/api/v1/alerts/save-override": {}, "/api/v1/alerts/delete-override": null });
    const user = userEvent.setup({ delay: null });
    open({ override: existing });
    expect(screen.getByLabelText(/Above/)).toHaveValue(95);
    await user.clear(screen.getByLabelText(/Above/));
    await user.type(screen.getByLabelText(/Above/), "99");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(api.posted("/api/v1/alerts/save-override")[0]).toMatchObject({ rule_id: "r1", threshold: 99, channel_ids: [] });
    await user.click(screen.getByRole("button", { name: "Reset to rule" }));
    expect(api.posted("/api/v1/alerts/delete-override")).toEqual([{ rule_id: "r1", server_id: "s1", app_id: "" }]);
  });
});
