import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { dayLabel } from "@/lib/alert-text";
import type { ActiveAlert, AlertEvent } from "@/lib/alerts";
import InboxPage from "@/pages/inbox";
import { fakeAPI } from "@/test/fake-api";

// Local times: "today" is Oct 13 at noon in whatever zone the runner uses.
const NOW = new Date(2026, 9, 13, 12, 0);
const at = (day: number, hour: number, minute = 0) => new Date(2026, 9, day, hour, minute).toISOString();
// The user last looked at 11:00 today.
const SEEN = new Date(2026, 9, 13, 11, 0).getTime();

const markAllRead = vi.fn();
let unread = 2;
vi.mock("@/hooks/use-inbox-unread", () => ({
  useInboxUnread: () => ({
    count: unread,
    seenAt: SEEN,
    isUnread: (createdAt: string) => Date.parse(createdAt) > SEEN,
    markAllRead,
  }),
}));
let servers = [
  { id: "s1", name: "homelab" },
  { id: "s2", name: "lab" },
];
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ servers }) }));

const event = (id: string, over: Partial<AlertEvent> = {}): AlertEvent => ({
  id,
  rule_id: "r1",
  server_id: "s1",
  app_id: "",
  server_name: "homelab",
  app_name: "",
  target_kind: "server",
  metric: "cpu",
  kind: "fired",
  value: 93,
  threshold: 90,
  title: "",
  message: "",
  deliveries: [],
  created_at: at(13, 10),
  ...over,
});

const ops = { channel_id: "c1", channel_name: "Ops", ok: true, error: "" };
const mail = { channel_id: "c2", channel_name: "Mail", ok: false, error: "535 auth failed" };
const history: AlertEvent[] = [
  event("e1", { app_id: "a1", app_name: "Website", target_kind: "app", metric: "down", threshold: 0, created_at: at(13, 11, 30), deliveries: [ops] }),
  event("e2", { kind: "resolved", value: 41, created_at: at(13, 11, 15) }),
  event("e3", { created_at: at(12, 22), deliveries: [ops, mail] }),
  event("e4", { kind: "resolved", value: 40, created_at: at(10, 9) }),
];
const firingCPU: ActiveAlert = {
  rule_id: "r9", target_kind: "server", metric: "cpu", server_id: "s1", app_id: "",
  since: at(13, 11, 48), value: 95, threshold: 90, server_name: "homelab", app_name: "",
};

function renderPage(
  {
    events = history,
    active = [firingCPU],
    next = "",
    channels = [{ id: "c1", name: "Ops" }],
  }: { events?: AlertEvent[]; active?: ActiveAlert[]; next?: string; channels?: unknown[] } = {},
) {
  const api = fakeAPI({
    "/api/v1/alerts/get-events": (_b: unknown, q: URLSearchParams) =>
      q.get("cursor") ? { events: [event("e9", { created_at: at(9, 9) })], next_cursor: "" } : { events, next_cursor: next },
    "/api/v1/alerts/get-active": active,
    "/api/v1/alerts/get-channels": channels,
  });
  const view = render(
    <MemoryRouter>
      <InboxPage />
    </MemoryRouter>,
  );
  return { api, ...view };
}

const lastEventsQuery = (api: ReturnType<typeof fakeAPI>) =>
  api.calls.filter((c) => c.path === "/api/v1/alerts/get-events").at(-1)?.query;

describe("InboxPage", () => {
  beforeEach(() => {
    vi.useFakeTimers({ now: NOW, toFake: ["Date"] });
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    markAllRead.mockClear();
    unread = 2;
    servers = [
      { id: "s1", name: "homelab" },
      { id: "s2", name: "lab" },
    ];
  });

  it("is org-wide with a way to the notification settings and Mark all as read", async () => {
    renderPage();
    expect(screen.getByRole("heading", { level: 1, name: "Inbox" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Notification settings/ })).toHaveAttribute("href", "/settings/notifications");
    await userEvent.setup().click(screen.getByRole("button", { name: "Mark all as read" }));
    expect(markAllRead).toHaveBeenCalled();
  });

  it("says once, under the header, that alerts only show here without a channel", async () => {
    const { unmount } = renderPage({ channels: [] });
    const hint = await screen.findByText(/Alerts only show here/);
    expect(within(hint).getByRole("link", { name: "Add a notification channel →" })).toHaveAttribute(
      "href",
      "/settings/notifications",
    );
    unmount();
    renderPage();
    await screen.findByRole("region", { name: "Today" });
    expect(screen.queryByText(/Alerts only show here/)).not.toBeInTheDocument();
  });

  it("offers Mark all as read only when something is unread", () => {
    unread = 0;
    renderPage();
    expect(screen.queryByRole("button", { name: "Mark all as read" })).not.toBeInTheDocument();
  });

  it("pins what is firing now above the history", async () => {
    renderPage();
    const firing = await screen.findByRole("region", { name: "Firing now" });
    const row = within(firing).getByTestId("firing-row");
    expect(within(row).getByRole("link", { name: "homelab CPU above 90%" })).toHaveAttribute("href", "/server/s1/monitoring");
    expect(within(row).getByRole("img", { name: "Firing" })).toBeInTheDocument();
    // The title names the server: the line under it doesn't again.
    expect(row).toHaveTextContent(/above 90%for 12 min$/);
    const today = await screen.findByRole("region", { name: "Today" });
    expect(firing.compareDocumentPosition(today) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("groups the history by day in plain words, with delivery notes and unread dots", async () => {
    renderPage();
    const today = await screen.findByRole("region", { name: "Today" });
    const rows = within(today).getAllByTestId("event-row");
    expect(rows).toHaveLength(2);

    expect(within(rows[0]).getByRole("link", { name: "Website is down" })).toHaveAttribute("href", "/server/s1/app/a1?tab=metrics");
    // The title names the app, the line under it the server, once.
    expect(rows[0]).toHaveTextContent("Website is downhomelab · ");
    expect(rows[0]).toHaveTextContent("Sent to Ops");
    expect(within(rows[0]).getByRole("img", { name: "Fired" })).toBeInTheDocument();
    expect(within(rows[0]).getByRole("img", { name: "Unread" })).toBeInTheDocument();

    expect(within(rows[1]).getByRole("link", { name: "homelab CPU back to 41%" })).toHaveAttribute("href", "/server/s1/monitoring");
    expect(within(rows[1]).getByRole("img", { name: "Resolved" })).toBeInTheDocument();
    expect(rows[1]).not.toHaveTextContent("homelab ·");
    // Sent nowhere: the row says nothing about it.
    expect(rows[1]).not.toHaveTextContent("Only in the Inbox");
    // Newer than the last visit, but a resolution is no news: no dot.
    expect(within(rows[1]).queryByRole("img", { name: "Unread" })).not.toBeInTheDocument();

    const yesterday = screen.getByRole("region", { name: "Yesterday" });
    const failed = within(yesterday).getByText("Sent to Ops · Couldn't send to Mail");
    expect(failed).toHaveAttribute("title", "Mail: 535 auth failed");
    // The reason is text on the row, not only a tooltip; the note is in the
    // normal muted colour with a red dot, never small red text.
    expect(within(yesterday).getByText("Mail: 535 auth failed")).toBeVisible();
    expect(failed).toHaveClass("text-muted-foreground");
    expect(failed.className).not.toMatch(/status-bad/);
    expect(failed.querySelector('[data-tone="bad"]')).not.toBeNull();
    expect(screen.getByRole("region", { name: dayLabel(at(10, 9), NOW.getTime()) })).toBeInTheDocument();
    // No notification emoji or operators anywhere.
    expect(document.body).not.toHaveTextContent(/🔥|✅|>/);
  });

  it("filters by All / Firing / Resolved and by server", async () => {
    const { api } = renderPage();
    await screen.findByRole("region", { name: "Firing now" });
    const user = userEvent.setup();

    await user.click(screen.getByRole("radio", { name: "Firing" }));
    expect(lastEventsQuery(api)).toBe("?kind=fired");
    expect(screen.getByRole("region", { name: "Firing now" })).toBeInTheDocument();

    await user.click(screen.getByRole("radio", { name: "Resolved" }));
    expect(lastEventsQuery(api)).toBe("?kind=resolved");
    expect(screen.queryByRole("region", { name: "Firing now" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("combobox", { name: "Server" }));
    await user.click(await screen.findByRole("option", { name: "lab" }));
    expect(lastEventsQuery(api)).toBe("?kind=resolved&server_id=s2");
    expect(api.calls.filter((c) => c.path === "/api/v1/alerts/get-active").at(-1)?.query).toBe("?server_id=s2");

    await user.click(screen.getByRole("radio", { name: "All" }));
    expect(lastEventsQuery(api)).toBe("?server_id=s2");
  });

  it("offers the server filter only with two servers or more", async () => {
    servers = [{ id: "s1", name: "homelab" }];
    renderPage();
    await screen.findByRole("region", { name: "Today" });
    expect(screen.queryByRole("combobox", { name: "Server" })).not.toBeInTheDocument();
  });

  it("reads a removed server as all servers", async () => {
    const { api, rerender } = renderPage();
    const user = userEvent.setup();
    await user.click(screen.getByRole("combobox", { name: "Server" }));
    await user.click(await screen.findByRole("option", { name: "lab" }));
    expect(lastEventsQuery(api)).toBe("?server_id=s2");

    servers = [{ id: "s1", name: "homelab" }];
    rerender(
      <MemoryRouter>
        <InboxPage />
      </MemoryRouter>,
    );
    await vi.waitFor(() => expect(lastEventsQuery(api)).toBe(""));
  });

  it("loads more of the history", async () => {
    const { api } = renderPage({ next: "c1" });
    await screen.findByRole("region", { name: "Today" });
    await userEvent.setup().click(screen.getByRole("button", { name: "Load more" }));
    expect(await screen.findByRole("region", { name: dayLabel(at(9, 9), NOW.getTime()) })).toBeInTheDocument();
    expect(lastEventsQuery(api)).toBe("?cursor=c1");
    expect(screen.queryByRole("button", { name: "Load more" })).not.toBeInTheDocument();
  });

  it("says all is quiet when there is nothing", async () => {
    renderPage({ events: [], active: [] });
    expect(
      await screen.findByText("All quiet. You'll see alerts here when something needs attention."),
    ).toBeInTheDocument();
  });
});
