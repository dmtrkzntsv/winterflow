import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { RulesTab } from "@/components/alerts/rules-tab";
import type { Rule } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";

afterEach(() => vi.unstubAllGlobals());

const base = { channel_ids: null, enabled: true, created_at: "2026-10-11T12:00:00Z" };
const rules: Rule[] = [
  { ...base, id: "r1", target_kind: "server", metric: "cpu", threshold: 90, duration_minutes: 10 },
  { ...base, id: "r2", target_kind: "server", metric: "down", threshold: 0, duration_minutes: 5 },
  { ...base, id: "r3", target_kind: "app", metric: "down", threshold: 0, duration_minutes: 5, channel_ids: ["c1"] },
];

describe("RulesTab", () => {
  it("groups rules by target and toggles one inline", async () => {
    const api = fakeAPI({
      "/api/v1/alerts/get-rules": rules,
      "/api/v1/alerts/get-channels": [{ id: "c1", name: "Ops", enabled: true }],
      "/api/v1/alerts/save-rule": {},
      "/api/v1/alerts/delete-rule": null,
    });
    const user = userEvent.setup({ delay: null });
    render(<RulesTab />);

    const servers = await screen.findByRole("region", { name: "Servers" });
    expect(within(servers).getAllByTestId("rule-row")).toHaveLength(2);
    expect(within(servers).getByText("CPU above 90% for 10 min")).toBeInTheDocument();
    const apps = screen.getByRole("region", { name: "Apps" });
    expect(within(apps).getByText("App down for 5 min")).toBeInTheDocument();
    expect(within(apps).getByText("Ops")).toBeInTheDocument();

    await user.click(screen.getByRole("switch", { name: "Enable CPU above 90% for 10 min" }));
    expect(api.posted("/api/v1/alerts/save-rule")[0]).toMatchObject({ id: "r1", enabled: false, threshold: 90 });

    await user.click(screen.getByRole("button", { name: "Delete Server down for 5 min" }));
    await user.click(await screen.findByRole("button", { name: "Delete" }));
    expect(api.posted("/api/v1/alerts/delete-rule")).toEqual([{ id: "r2" }]);
  });
});
