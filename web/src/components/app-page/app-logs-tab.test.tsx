import type { ReactNode } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppLogsTab } from "@/components/app-page/app-logs-tab";
import { NotificationsContext, type Notification, type NotificationsContextValue } from "@/context/notifications-context-base";
import { fakeAPI } from "@/test/fake-api";

vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));

const waitFor_ = vi.fn<(ref: string) => Promise<Notification>>();
const notifications = { connected: true, subscribe: () => () => {}, waitFor: waitFor_ } as unknown as NotificationsContextValue;
const wrap = (ui: ReactNode) => <NotificationsContext.Provider value={notifications}>{ui}</NotificationsContext.Provider>;

const LINES = [
  { timestamp: 1700000000, level: 3, message: "listening on :8080", container: "web" },
  { timestamp: 1700000001, level: 3, message: "GET /health", container: "web" },
];

describe("AppLogsTab", () => {
  beforeEach(() => {
    waitFor_.mockReset().mockResolvedValue({ status: 0, payload: { logs: LINES } } as Notification);
  });

  it("asks the agent for the app's last 200 lines and shows them", async () => {
    const api = fakeAPI({ "/api/v1/app/get-logs": { request_id: "r1" } });
    render(wrap(<AppLogsTab appId="a1" />));
    expect(await screen.findByText("listening on :8080")).toBeInTheDocument();
    expect(api.calls[0].query).toBe("?server_id=srv-1&app_id=a1&tail=200");
    expect(waitFor_).toHaveBeenCalledWith("r1");
    expect(screen.getByText("2 lines")).toBeInTheDocument();
  });

  it("names containers by their compose service, like the Overview", async () => {
    fakeAPI({ "/api/v1/app/get-logs": { request_id: "r1" } });
    waitFor_.mockResolvedValue({
      status: 0,
      payload: {
        logs: [
          { timestamp: 1700000000, level: 3, message: "ready", container: "web-1" },
          { timestamp: 1700000001, level: 3, message: "pong", container: "wf-a1-cache-1" },
          { timestamp: 1700000002, level: 3, message: "w1", container: "worker-1" },
          { timestamp: 1700000003, level: 3, message: "w2", container: "worker-2" },
        ],
      },
    } as Notification);
    render(wrap(<AppLogsTab appId="a1" />));
    const chips = await screen.findByRole("group", { name: "Containers" });
    expect([...chips.querySelectorAll("button")].map((b) => b.textContent)).toEqual(["All", "web", "cache", "worker-1", "worker-2"]);
  });

  it("reads again with the number of lines picked, and on Refresh", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({ "/api/v1/app/get-logs": { request_id: "r1" } });
    render(wrap(<AppLogsTab appId="a1" />));
    await screen.findByText("listening on :8080");
    await user.click(screen.getByRole("button", { name: "Refresh logs" }));
    await waitFor(() => expect(api.calls).toHaveLength(2));
    await user.click(screen.getByRole("combobox", { name: "Number of lines" }));
    await user.click(await screen.findByRole("option", { name: "Last 500 lines" }));
    await waitFor(() => expect(api.calls.at(-1)?.query).toBe("?server_id=srv-1&app_id=a1&tail=500"));
  });

  it("shows the agent's failure, and tries again", async () => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/app/get-logs": { request_id: "r1" } });
    waitFor_.mockResolvedValueOnce({ status: 1, error: "container not found" } as Notification);
    render(wrap(<AppLogsTab appId="a1" />));
    expect(await screen.findByText("container not found")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("GET /health")).toBeInTheDocument();
  });

  it("shows a refused request, and a reply without a request id", async () => {
    const api = fakeAPI({ "/api/v1/app/get-logs": new Error("server not found") });
    const { unmount } = render(wrap(<AppLogsTab appId="a1" />));
    expect(await screen.findByText(/server not found/)).toBeInTheDocument();
    unmount();
    api.calls.length = 0;
    fakeAPI({ "/api/v1/app/get-logs": {} });
    render(wrap(<AppLogsTab appId="a1" />));
    expect(await screen.findByText("No request id")).toBeInTheDocument();
  });

  it("starts empty again for the next app", async () => {
    fakeAPI({ "/api/v1/app/get-logs": { request_id: "r1" } });
    const { rerender } = render(wrap(<AppLogsTab key="a1" appId="a1" />));
    await screen.findByText("listening on :8080");
    waitFor_.mockResolvedValue({ status: 0, payload: { logs: [{ timestamp: 1700000002, level: 3, message: "other app", container: "web" }] } } as Notification);
    rerender(wrap(<AppLogsTab key="a2" appId="a2" />));
    expect(await screen.findByText("other app")).toBeInTheDocument();
    expect(screen.queryByText("listening on :8080")).not.toBeInTheDocument();
  });
});
