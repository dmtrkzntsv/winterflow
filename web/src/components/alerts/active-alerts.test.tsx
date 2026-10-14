import { act, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AppAlertsBanner, FiringAlertsLine } from "@/components/alerts/active-alerts";
import type { ActiveAlert } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";

afterEach(() => vi.unstubAllGlobals());

const since = new Date(Date.now() - 12 * 60_000).toISOString();
const firing: ActiveAlert[] = [
  { rule_id: "r1", target_kind: "server", metric: "cpu", server_id: "s1", app_id: "", since, value: 93.4, threshold: 90, server_name: "prod-1", app_name: "" },
  { rule_id: "r2", target_kind: "app", metric: "down", server_id: "s1", app_id: "a1", since, value: 7, threshold: 0, server_name: "prod-1", app_name: "nextcloud" },
];

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
    // Let the answer land and render before asserting that nothing shows.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
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
