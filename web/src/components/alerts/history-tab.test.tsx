import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { HistoryTab } from "@/components/alerts/history-tab";
import type { AlertEvent } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";

vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ servers: [{ id: "s1", name: "prod-1" }, { id: "s2", name: "lab" }] }),
}));

afterEach(() => vi.unstubAllGlobals());

const event = (id: string, over: Partial<AlertEvent> = {}): AlertEvent => ({
  id,
  rule_id: "r1",
  server_id: "s1",
  app_id: "",
  server_name: "prod-1",
  app_name: "",
  target_kind: "server",
  metric: "cpu",
  kind: "fired",
  value: 93,
  threshold: 90,
  title: `🔥 prod-1 · CPU 93% > 90% for 10m (${id})`,
  message: "",
  deliveries: [],
  created_at: "2026-10-11T12:00:00Z",
  ...over,
});

describe("HistoryTab", () => {
  it("shows events with targets and deliveries, filters and pages", async () => {
    const api = fakeAPI({
      "/api/v1/alerts/get-events": () =>
        api.calls.at(-1)?.query.includes("cursor")
          ? { events: [event("e3")], next_cursor: "" }
          : {
              events: [
                event("e1", {
                  app_id: "a1",
                  app_name: "nextcloud",
                  deliveries: [
                    { channel_id: "c1", channel_name: "Ops", ok: true, error: "" },
                    { channel_id: "c2", channel_name: "Mail", ok: false, error: "535 auth failed" },
                  ],
                }),
                event("e2", { kind: "resolved", title: "✅ Resolved · prod-1 · CPU 41%" }),
              ],
              next_cursor: "1760184000_e2",
            },
    });
    const user = userEvent.setup({ delay: null });
    render(
      <MemoryRouter>
        <HistoryTab />
      </MemoryRouter>,
    );

    const rows = await screen.findAllByTestId("event-row");
    expect(rows).toHaveLength(2);
    expect(within(rows[0]).getByRole("link", { name: "prod-1 / nextcloud" })).toHaveAttribute(
      "href",
      "/server/s1/app/a1?tab=metrics",
    );
    expect(within(rows[0]).getByText(/✓ Ops/)).toBeInTheDocument();
    expect(within(rows[0]).getByText(/✗ Mail/)).toHaveAttribute("title", "535 auth failed");
    expect(within(rows[1]).getByLabelText("Resolved")).toBeInTheDocument();
    expect(within(rows[1]).getByText("Not sent")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Load more" }));
    expect(await screen.findAllByTestId("event-row")).toHaveLength(3);
    expect(api.calls.at(-1)?.query).toContain("cursor=1760184000_e2");

    await user.click(screen.getByRole("combobox", { name: "Kind" }));
    await user.click(await screen.findByRole("option", { name: "Fired" }));
    expect(api.calls.at(-1)?.query).toBe("?kind=fired");
    await user.click(screen.getByRole("combobox", { name: "Server" }));
    await user.click(await screen.findByRole("option", { name: "lab" }));
    expect(api.calls.at(-1)?.query).toBe("?kind=fired&server_id=s2");
  });
});
