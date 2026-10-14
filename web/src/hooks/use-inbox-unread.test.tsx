import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  NotificationsContext,
  type Notification,
  type NotificationHandler,
  type NotificationsContextValue,
} from "@/context/notifications-context-base";
import { ProfileContext, type ProfileContextValue } from "@/context/profile-context-base";
import { useInboxUnread } from "@/hooks/use-inbox-unread";
import { inboxSeenKey, readInboxSeen } from "@/lib/inbox";
import { fakeAPI } from "@/test/fake-api";

function Probe() {
  const { count, isUnread, markAllRead } = useInboxUnread();
  return (
    <div>
      <span data-testid="count">{count}</span>
      <span data-testid="new">{String(isUnread("2026-10-13T11:00:00Z"))}</span>
      <button onClick={markAllRead}>read</button>
    </div>
  );
}

function renderProbe(userId: string | null = "u1") {
  const handlers = new Set<NotificationHandler>();
  const notifications = {
    connected: true,
    subscribe: (h: NotificationHandler) => {
      handlers.add(h);
      return () => handlers.delete(h);
    },
    waitFor: vi.fn(),
  } as unknown as NotificationsContextValue;
  const profile = {
    profile: userId ? { user_id: userId, name: "Ann", email: "", must_change_password: false } : null,
    loading: false,
    refresh: async () => {},
  } as ProfileContextValue;
  render(
    <NotificationsContext.Provider value={notifications}>
      <ProfileContext.Provider value={profile}>
        <Probe />
      </ProfileContext.Provider>
    </NotificationsContext.Provider>,
  );
  return { emit: (n: Notification) => act(() => handlers.forEach((h) => h(n))) };
}

const event = (id: string, at: string) => ({ id, created_at: at, kind: "fired" });
const count = () => screen.getByTestId("count");

describe("useInboxUnread", () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => vi.unstubAllGlobals());

  it("counts the events newer than the user's last visit", async () => {
    localStorage.setItem(inboxSeenKey("u1"), String(Date.parse("2026-10-13T10:00:00Z")));
    fakeAPI({
      "/api/v1/alerts/get-events": {
        events: [event("e1", "2026-10-13T11:00:00Z"), event("e2", "2026-10-13T09:00:00Z")],
        next_cursor: "",
      },
    });
    renderProbe();
    await waitFor(() => expect(count()).toHaveTextContent("1"));
    expect(screen.getByTestId("new")).toHaveTextContent("true");
  });

  it("starts from now on a first visit instead of counting old history", async () => {
    fakeAPI({ "/api/v1/alerts/get-events": { events: [event("e1", "2020-01-01T00:00:00Z")], next_cursor: "" } });
    renderProbe();
    await waitFor(() => expect(readInboxSeen("u1")).not.toBeNull());
    expect(count()).toHaveTextContent("0");
  });

  it("marks everything read, even an event stamped ahead of this browser's clock", async () => {
    localStorage.setItem(inboxSeenKey("u1"), "0");
    fakeAPI({ "/api/v1/alerts/get-events": { events: [event("e1", "2999-01-01T00:00:00Z")], next_cursor: "" } });
    renderProbe();
    await waitFor(() => expect(count()).toHaveTextContent("1"));
    await userEvent.setup().click(screen.getByRole("button", { name: "read" }));
    expect(count()).toHaveTextContent("0");
  });

  it("follows a mark made in another tab", async () => {
    localStorage.setItem(inboxSeenKey("u1"), "0");
    fakeAPI({ "/api/v1/alerts/get-events": { events: [event("e1", "2026-10-13T11:00:00Z")], next_cursor: "" } });
    renderProbe();
    await waitFor(() => expect(count()).toHaveTextContent("1"));
    act(() => {
      localStorage.setItem(inboxSeenKey("u1"), String(Date.parse("2027-01-01T00:00:00Z")));
      window.dispatchEvent(new StorageEvent("storage", { key: inboxSeenKey("u1") }));
    });
    expect(count()).toHaveTextContent("0");
  });

  it("re-reads the events when an alert fires", async () => {
    localStorage.setItem(inboxSeenKey("u1"), "0");
    let events: ReturnType<typeof event>[] = [];
    fakeAPI({ "/api/v1/alerts/get-events": () => ({ events, next_cursor: "" }) });
    const { emit } = renderProbe();
    await waitFor(() => expect(count()).toHaveTextContent("0"));
    events = [event("e1", "2026-10-13T11:00:00Z")];
    emit({ type: "alert", ref: "" });
    await waitFor(() => expect(count()).toHaveTextContent("1"));
  });

  it("counts nothing and reads nothing without a signed-in user", () => {
    const api = fakeAPI({ "/api/v1/alerts/get-events": { events: [], next_cursor: "" } });
    renderProbe(null);
    expect(count()).toHaveTextContent("0");
    expect(api.calls).toHaveLength(0);
  });

  it("keeps quiet when the events can't be read", async () => {
    localStorage.setItem(inboxSeenKey("u1"), "0");
    const api = fakeAPI({ "/api/v1/alerts/get-events": new Error("down") });
    renderProbe();
    await waitFor(() => expect(api.calls).toHaveLength(1));
    expect(count()).toHaveTextContent("0");
  });
});
