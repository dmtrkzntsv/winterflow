import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import InboxPage from "@/pages/inbox";
import { fakeAPI } from "@/test/fake-api";

const markAllRead = vi.fn();
let unread = 2;
vi.mock("@/hooks/use-inbox-unread", () => ({
  useInboxUnread: () => ({ count: unread, seenAt: 0, isUnread: () => true, markAllRead }),
}));
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ servers: [] }) }));

afterEach(() => {
  vi.unstubAllGlobals();
  unread = 2;
});

function renderPage() {
  fakeAPI({ "/api/v1/alerts/get-events": { events: [], next_cursor: "" } });
  return render(
    <MemoryRouter>
      <InboxPage />
    </MemoryRouter>,
  );
}

describe("InboxPage", () => {
  it("is the org's alert history with a way to the notification settings", async () => {
    renderPage();
    expect(screen.getByRole("heading", { level: 1, name: "Inbox" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Notification settings" })).toHaveAttribute("href", "/settings/notifications");
    expect(await screen.findByText(/No alerts yet/)).toBeInTheDocument();
  });

  it("marks everything read", async () => {
    renderPage();
    await userEvent.setup().click(screen.getByRole("button", { name: "Mark all as read" }));
    expect(markAllRead).toHaveBeenCalled();
  });

  it("offers Mark all as read only when something is unread", () => {
    unread = 0;
    renderPage();
    expect(screen.queryByRole("button", { name: "Mark all as read" })).not.toBeInTheDocument();
  });
});
