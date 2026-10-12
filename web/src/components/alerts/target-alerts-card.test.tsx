import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { TargetAlertsCard } from "@/components/alerts/target-alerts-card";
import type { Override, Rule } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";

afterEach(() => vi.unstubAllGlobals());

const base = { channel_ids: null, enabled: true, created_at: "2026-10-11T12:00:00Z" };
const rules: Rule[] = [
  { ...base, id: "r1", target_kind: "server", metric: "cpu", threshold: 90, duration_minutes: 10 },
  { ...base, id: "r2", target_kind: "server", metric: "disk", threshold: 90, duration_minutes: 5 },
  { ...base, id: "r3", target_kind: "server", metric: "down", threshold: 0, duration_minutes: 5 },
  { ...base, id: "r4", target_kind: "app", metric: "down", threshold: 0, duration_minutes: 5 },
];
const overrides: Override[] = [
  { rule_id: "r1", server_id: "s1", app_id: "", disabled: false, threshold: 95, duration_minutes: null, channel_ids: null },
  { rule_id: "r2", server_id: "s1", app_id: "", disabled: true, threshold: null, duration_minutes: null, channel_ids: null },
];

function renderCard() {
  return render(
    <MemoryRouter>
      <TargetAlertsCard serverId="s1" targetLabel="prod-1" />
    </MemoryRouter>,
  );
}

describe("TargetAlertsCard", () => {
  it("shows what fires here and every server rule with its effective setting", async () => {
    fakeAPI({
      "/api/v1/alerts/get-rules": rules,
      "/api/v1/alerts/get-overrides": overrides,
      "/api/v1/alerts/get-channels": [],
      "/api/v1/alerts/get-active": [
        { rule_id: "r1", target_kind: "server", metric: "cpu", server_id: "s1", app_id: "", since: new Date().toISOString(), value: 97, threshold: 95, server_name: "prod-1", app_name: "" },
        { rule_id: "r4", target_kind: "app", metric: "down", server_id: "s1", app_id: "a1", since: new Date().toISOString(), value: 0, threshold: 0, server_name: "prod-1", app_name: "x" },
      ],
    });
    renderCard();
    const firing = await screen.findByRole("list", { name: "Firing" });
    // The app's alert belongs to the app's card, not the server's.
    expect(within(firing).getAllByRole("listitem")).toHaveLength(1);
    expect(firing).toHaveTextContent("CPU 97% > 95%");

    const rows = await screen.findAllByTestId("target-rule");
    expect(rows).toHaveLength(3);
    expect(rows[0]).toHaveTextContent("CPU above 95% for 10 min");
    expect(within(rows[0]).getByText("Adjusted")).toBeInTheDocument();
    expect(within(rows[1]).getByText("Off here")).toBeInTheDocument();
    expect(rows[2]).toHaveTextContent("Down for 5 min");
  });

  it("saves and resets an override for this target", async () => {
    const api = fakeAPI({
      "/api/v1/alerts/get-rules": rules,
      "/api/v1/alerts/get-overrides": overrides,
      "/api/v1/alerts/get-channels": [{ id: "c1", name: "Ops", enabled: true }],
      "/api/v1/alerts/get-active": [],
      "/api/v1/alerts/save-override": {},
      "/api/v1/alerts/delete-override": null,
    });
    const user = userEvent.setup({ delay: null });
    renderCard();
    expect(await screen.findByText("Nothing firing.")).toBeInTheDocument();

    await user.click(await screen.findByRole("button", { name: "Adjust Down for prod-1" }));
    expect(screen.queryByLabelText(/Above/)).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("For (minutes)"), "15");
    await user.click(screen.getByLabelText("Specific channels"));
    await user.click(screen.getByLabelText("Ops"));
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(api.posted("/api/v1/alerts/save-override")).toEqual([
      { rule_id: "r3", server_id: "s1", app_id: "", disabled: false, threshold: null, duration_minutes: 15, channel_ids: ["c1"] },
    ]);

    await user.click(screen.getByRole("button", { name: "Adjust CPU for prod-1" }));
    expect(screen.getByLabelText(/Above/)).toHaveValue(95);
    await user.click(screen.getByRole("button", { name: "Reset to rule" }));
    expect(api.posted("/api/v1/alerts/delete-override")).toEqual([{ rule_id: "r1", server_id: "s1", app_id: "" }]);
  });
});
