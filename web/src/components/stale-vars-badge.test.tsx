import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { StaleVarsBadge } from "@/components/stale-vars-badge";
import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";
import {
  NotificationsContext,
  type Notification,
  type NotificationHandler,
  type NotificationsContextValue,
} from "@/context/notifications-context-base";

function renderBadge(stale: Record<string, string[]>, reload = vi.fn().mockResolvedValue(undefined)) {
  const hub: { h?: NotificationHandler } = {};
  const notifications: NotificationsContextValue = {
    connected: true,
    subscribe: (h) => {
      hub.h = h;
      return () => {
        hub.h = undefined;
      };
    },
    waitFor: vi.fn(),
  };
  const apps = { staleVarsByApp: stale, reloadSharedVars: reload } as unknown as AppsContextValue;
  render(
    <NotificationsContext.Provider value={notifications}>
      <AppsContext.Provider value={apps}>
        <StaleVarsBadge appId="a1" />
      </AppsContext.Provider>
    </NotificationsContext.Provider>,
  );
  return { reload, emit: (n: Notification) => act(() => hub.h?.(n)) };
}

const reloadButton = () => screen.getByRole("button", { name: /reload/i });

describe("StaleVarsBadge", () => {
  it("renders nothing for a fresh app", () => {
    renderBadge({ a2: ["X"] });
    expect(screen.queryByText(/Shared variables changed/)).not.toBeInTheDocument();
  });

  it("names the changed variables and reloads the app", async () => {
    const user = userEvent.setup();
    const { reload, emit } = renderBadge({ a1: ["SMTP_HOST", "SMTP_PASS"] });
    expect(screen.getByText("Shared variables changed — reload to apply")).toBeInTheDocument();
    expect(screen.getByText("SMTP_HOST, SMTP_PASS")).toBeInTheDocument();
    await user.click(reloadButton());
    expect(reload).toHaveBeenCalledWith("a1");
    expect(reloadButton()).toBeDisabled(); // waits for vars.reloaded
    emit({ type: "vars.reloaded", ref: "", payload: { server_id: "s", app_id: "other", ok: true, names: [] } });
    expect(reloadButton()).toBeDisabled();
    emit({ type: "vars.reloaded", ref: "", payload: { server_id: "s", app_id: "a1", ok: false, error: "compose up failed", names: ["SMTP_HOST"] } });
    expect(reloadButton()).toBeEnabled();
  });
});
