import { act, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { FiringBanner } from "@/components/alerts/firing-banner";
import type { ActiveAlert } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";

afterEach(() => vi.unstubAllGlobals());

const since = new Date(Date.now() - 12 * 60_000).toISOString();
const serverCPU: ActiveAlert = {
  rule_id: "r1", target_kind: "server", metric: "cpu", server_id: "s1", app_id: "",
  since, value: 93.4, threshold: 90, server_name: "prod-1", app_name: "",
};
const appDown: ActiveAlert = {
  rule_id: "r4", target_kind: "app", metric: "down", server_id: "s1", app_id: "a1",
  since, value: 0, threshold: 0, server_name: "prod-1", app_name: "nextcloud",
};

describe("FiringBanner", () => {
  it("lists the server's firing alerts and its apps', in plain words", async () => {
    const api = fakeAPI({ "/api/v1/alerts/get-active": [serverCPU, appDown] });
    render(
      <MemoryRouter>
        <FiringBanner serverId="s1" />
      </MemoryRouter>,
    );
    const banner = await screen.findByRole("region", { name: "Firing alerts" });
    expect(banner).toHaveTextContent("2 alerts firing");
    expect(banner).toHaveTextContent("prod-1 CPU above 90%");
    expect(banner).toHaveTextContent("for 12 min");
    expect(within(banner).getByRole("link", { name: "nextcloud is down" })).toHaveAttribute(
      "href",
      "/server/s1/app/a1?tab=metrics",
    );
    // The server's own alert is about the page it is on: no link.
    expect(within(banner).queryByRole("link", { name: "prod-1 CPU above 90%" })).not.toBeInTheDocument();
    expect(within(banner).getByRole("link", { name: /Inbox/ })).toHaveAttribute("href", "/inbox");
    expect(api.calls[0].query).toBe("?server_id=s1");
  });

  it("keeps to one app's alerts on an app", async () => {
    const api = fakeAPI({ "/api/v1/alerts/get-active": [appDown] });
    render(
      <MemoryRouter>
        <FiringBanner serverId="s1" appId="a1" />
      </MemoryRouter>,
    );
    const banner = await screen.findByRole("region", { name: "Firing alerts" });
    expect(banner).toHaveTextContent("1 alert firing");
    expect(banner).toHaveTextContent("nextcloud is down");
    expect(within(banner).queryByRole("link", { name: "nextcloud is down" })).not.toBeInTheDocument();
    expect(api.calls[0].query).toBe("?server_id=s1&app_id=a1");
  });

  it("shows five lines at most, then the rest as a link to the Inbox", async () => {
    const many: ActiveAlert[] = Array.from({ length: 8 }, (_, i) => ({
      ...appDown,
      rule_id: `r${i}`,
      app_id: `a${i}`,
      app_name: `app-${i}`,
    }));
    fakeAPI({ "/api/v1/alerts/get-active": many });
    render(
      <MemoryRouter>
        <FiringBanner serverId="s1" />
      </MemoryRouter>,
    );
    const banner = await screen.findByRole("region", { name: "Firing alerts" });
    expect(banner).toHaveTextContent("8 alerts firing");
    expect(within(banner).getAllByRole("listitem")).toHaveLength(5);
    expect(within(banner).getByRole("link", { name: "and 3 more" })).toHaveAttribute("href", "/inbox");
    expect(within(banner).queryByText("app-5 is down")).not.toBeInTheDocument();
  });

  it("renders nothing while all is quiet", async () => {
    const api = fakeAPI({ "/api/v1/alerts/get-active": [] });
    const { container } = render(
      <MemoryRouter>
        <FiringBanner serverId="s1" />
      </MemoryRouter>,
    );
    await vi.waitFor(() => expect(api.calls).toHaveLength(1));
    // Let the answer land and render before asserting that nothing shows.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    expect(container).toBeEmptyDOMElement();
  });
});
