import { act, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ActiveAlertsStrip, AppAlertsBadge, AppAlertsBanner, FiringAlertsLine, FiringBadge } from "@/components/alerts/active-alerts";
import type { ActiveAlert } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";
import { fakeNotifications } from "@/test/notifications";

afterEach(() => vi.unstubAllGlobals());

const since = new Date(Date.now() - 12 * 60_000).toISOString();
const firing: ActiveAlert[] = [
  { rule_id: "r1", target_kind: "server", metric: "cpu", server_id: "s1", app_id: "", since, value: 93.4, threshold: 90, server_name: "prod-1", app_name: "" },
  { rule_id: "r2", target_kind: "app", metric: "down", server_id: "s1", app_id: "a1", since, value: 7, threshold: 0, server_name: "prod-1", app_name: "nextcloud" },
];

describe("ActiveAlertsStrip", () => {
  it("lists firing alerts with links, and refreshes on an alert notification", async () => {
    let list = firing.slice(0, 1);
    const api = fakeAPI({ "/api/v1/alerts/get-active": () => list });
    const sse = fakeNotifications();
    render(
      <MemoryRouter>
        <sse.Provider>
          <ActiveAlertsStrip />
        </sse.Provider>
      </MemoryRouter>,
    );
    const strip = await screen.findByRole("region", { name: "Active alerts" });
    expect(within(strip).getByRole("link", { name: "View history" })).toHaveAttribute("href", "/inbox");
    expect(strip).toHaveTextContent("1 active alert");
    expect(within(strip).getByRole("link", { name: "prod-1" })).toHaveAttribute("href", "/server/s1/monitoring");
    expect(strip).toHaveTextContent("CPU 93% > 90%");
    expect(strip).toHaveTextContent("12 min");

    list = firing;
    await act(async () => sse.emit({ type: "alert", ref: "", payload: {} }));
    expect(await screen.findByRole("link", { name: "prod-1 / nextcloud" })).toHaveAttribute(
      "href",
      "/server/s1/app/a1?tab=metrics",
    );
    expect(screen.getByRole("region", { name: "Active alerts" })).toHaveTextContent("2 active alerts");
    expect(api.calls.filter((c) => c.path === "/api/v1/alerts/get-active")).toHaveLength(2);
  });

  it("renders nothing while all is quiet", async () => {
    const api = fakeAPI({ "/api/v1/alerts/get-active": [] });
    const { container } = render(
      <MemoryRouter>
        <ActiveAlertsStrip />
      </MemoryRouter>,
    );
    await vi.waitFor(() => expect(api.calls).toHaveLength(1));
    expect(container).toBeEmptyDOMElement();
  });
});

describe("badges", () => {
  it("counts firing alerts and hides at zero", () => {
    const { rerender } = render(<FiringBadge count={3} />);
    expect(screen.getByTestId("firing-badge")).toHaveTextContent("3");
    rerender(<FiringBadge count={0} />);
    expect(screen.queryByTestId("firing-badge")).not.toBeInTheDocument();
  });

  it("asks for one app's alerts", async () => {
    const api = fakeAPI({ "/api/v1/alerts/get-active": firing.slice(1) });
    render(
      <MemoryRouter>
        <AppAlertsBadge serverId="s1" appId="a1" />
      </MemoryRouter>,
    );
    expect(await screen.findByTestId("firing-badge")).toHaveTextContent("1");
    expect(api.calls[0].query).toBe("?server_id=s1&app_id=a1");
  });
});

describe("FiringAlertsLine", () => {
  it("counts this server's firing alerts in one line that leads to the Inbox", async () => {
    const api = fakeAPI({ "/api/v1/alerts/get-active": firing });
    render(
      <MemoryRouter>
        <FiringAlertsLine serverId="s1" />
      </MemoryRouter>,
    );
    expect(await screen.findByRole("link", { name: /2 alerts firing/ })).toHaveAttribute("href", "/inbox");
    expect(api.calls[0].query).toBe("?server_id=s1");
  });

  it("says one alert in the singular, and nothing while all is quiet", async () => {
    const api = fakeAPI({ "/api/v1/alerts/get-active": firing.slice(0, 1) });
    const { unmount } = render(
      <MemoryRouter>
        <FiringAlertsLine serverId="s1" />
      </MemoryRouter>,
    );
    expect(await screen.findByRole("link", { name: /1 alert firing/ })).toBeInTheDocument();
    unmount();
    vi.unstubAllGlobals();
    const quiet = fakeAPI({ "/api/v1/alerts/get-active": [] });
    const { container } = render(
      <MemoryRouter>
        <FiringAlertsLine serverId="s1" />
      </MemoryRouter>,
    );
    await vi.waitFor(() => expect(quiet.calls).toHaveLength(1));
    expect(container).toBeEmptyDOMElement();
    expect(api.calls).toHaveLength(1);
  });
});

describe("AppAlertsBanner", () => {
  it("lists only this app's firing alerts, with how long, and leads to its metrics", async () => {
    const api = fakeAPI({ "/api/v1/alerts/get-active": firing });
    render(
      <MemoryRouter>
        <AppAlertsBanner serverId="s1" appId="a1" />
      </MemoryRouter>,
    );
    const banner = await screen.findByRole("status", { name: "Firing alerts" });
    expect(banner).toHaveTextContent("Down");
    expect(banner).toHaveTextContent("12 min");
    // The server's own CPU alert is not this app's.
    expect(banner).not.toHaveTextContent("CPU");
    expect(within(banner).getByRole("link", { name: "View metrics" })).toHaveAttribute("href", "/?tab=metrics");
    expect(api.calls[0].query).toBe("?server_id=s1&app_id=a1");
  });
});
