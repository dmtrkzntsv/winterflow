import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useActiveAlerts } from "@/hooks/use-alerts";
import type { ActiveAlert } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";
import { fakeNotifications } from "@/test/notifications";

afterEach(() => vi.unstubAllGlobals());

const down: ActiveAlert = {
  rule_id: "r4", target_kind: "app", metric: "down", server_id: "s1", app_id: "a1",
  since: new Date().toISOString(), value: 0, threshold: 0, server_name: "prod-1", app_name: "nextcloud",
};

function Reader({ name, server = "s1" }: { name: string; server?: string }) {
  const { data } = useActiveAlerts(server);
  return <p data-testid={name}>{data.length}</p>;
}

const settle = () => act(async () => { await new Promise((r) => setTimeout(r, 20)); });

describe("useActiveAlerts", () => {
  it("shares one poller between readers of the same query", async () => {
    const api = fakeAPI({ "/api/v1/alerts/get-active": [down] });
    const sse = fakeNotifications();
    render(
      <sse.Provider>
        <Reader name="banner" />
        <Reader name="card" />
        <Reader name="other" server="s2" />
      </sse.Provider>,
    );
    await vi.waitFor(() => expect(screen.getByTestId("card")).toHaveTextContent("1"));
    expect(screen.getByTestId("banner")).toHaveTextContent("1");
    await settle();
    expect(api.calls.map((c) => c.query).sort()).toEqual(["?server_id=s1", "?server_id=s2"]);

    // One notification reaches both readers but reads once per query.
    act(() => sse.emit({ type: "alert", ref: "", payload: {} }));
    await settle();
    expect(api.calls.filter((c) => c.query === "?server_id=s1")).toHaveLength(2);
  });

  it("stops polling and forgets the data with its last reader", async () => {
    const api = fakeAPI({ "/api/v1/alerts/get-active": [down] });
    const first = render(<Reader name="a" />);
    await vi.waitFor(() => expect(screen.getByTestId("a")).toHaveTextContent("1"));
    first.unmount();
    const again = render(<Reader name="b" />);
    // Fresh start: no data from the previous mount, and a new read.
    expect(screen.getByTestId("b")).toHaveTextContent("0");
    await vi.waitFor(() => expect(screen.getByTestId("b")).toHaveTextContent("1"));
    expect(api.calls).toHaveLength(2);
    again.unmount();
  });
});
