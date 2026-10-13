import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";

import { NotificationsContext, type Notification, type NotificationHandler } from "@/context/notifications-context-base";
import { useRollout } from "@/hooks/use-rollout";
import type { Rollout, RolloutsApi } from "@/lib/rollouts";
import { rollout } from "@/test/rollouts";

function setup(list: Promise<Rollout[]>, scopeKey?: string | null) {
  const hub: { h?: NotificationHandler } = {};
  const api = {
    list: vi.fn(() => list),
    stop: vi.fn(async (id: string) => rollout({ id, stop_requested: true })),
    upgradeAll: vi.fn(),
  } satisfies RolloutsApi;
  const wrapper = ({ children }: { children: ReactNode }) => (
    <NotificationsContext.Provider
      value={{
        connected: true,
        subscribe: (h) => {
          hub.h = h;
          return () => {
            hub.h = undefined;
          };
        },
        waitFor: vi.fn(),
      }}
    >
      {children}
    </NotificationsContext.Provider>
  );
  const view = renderHook(() => useRollout(api, "template_upgrade", scopeKey), { wrapper });
  const emit = (n: Notification) => act(() => hub.h?.(n));
  return { ...view, api, emit };
}

describe("useRollout", () => {
  it("reads the newest rollout of its scope, then follows its events", async () => {
    const { result, api, emit } = setup(Promise.resolve([rollout({ done: 1 }), rollout({ id: "r0", state: "done" })]), "t1");
    await waitFor(() => expect(result.current.rollout?.done).toBe(1));
    expect(api.list).toHaveBeenCalledWith("template_upgrade", "t1");

    emit({ type: "rollout", ref: "", payload: rollout({ done: 2 }) });
    expect(result.current.rollout?.done).toBe(2);
    // Another template's, another kind's, or an older rollout's event is not this page's.
    emit({ type: "rollout", ref: "", payload: rollout({ scope_key: "t2", done: 9 }) });
    emit({ type: "rollout", ref: "", payload: rollout({ kind: "vars_reload", done: 9 }) });
    emit({ type: "rollout", ref: "", payload: rollout({ id: "r0", created_at: "2026-10-01T00:00:00Z", done: 9 }) });
    emit({ type: "apps_status", ref: "", payload: rollout({ done: 9 }) });
    expect(result.current.rollout?.done).toBe(2);
    // A newer one replaces it.
    emit({ type: "rollout", ref: "", payload: rollout({ id: "r2", created_at: "2026-10-13T00:00:00Z", state: "queued" }) });
    expect(result.current.rollout?.id).toBe("r2");
  });

  it("keeps an event that beat the first read, and stops", async () => {
    let answer: (l: Rollout[]) => void = () => {};
    const { result, api, emit } = setup(new Promise((resolve) => (answer = resolve)));
    emit({ type: "rollout", ref: "", payload: rollout({ done: 4 }) });
    await act(async () => answer([rollout({ done: 1 })]));
    expect(result.current.rollout?.done).toBe(4);
    await act(() => result.current.stop("r1"));
    expect(api.stop).toHaveBeenCalledWith("r1");
    expect(result.current.rollout?.stop_requested).toBe(true);
  });

  it("follows nothing without a scope yet", () => {
    const { result, api, emit } = setup(Promise.resolve([rollout()]), null);
    emit({ type: "rollout", ref: "", payload: rollout() });
    expect(api.list).not.toHaveBeenCalled();
    expect(result.current.rollout).toBeNull();
  });

  it("shows nothing when the read fails", async () => {
    const { result, api } = setup(Promise.reject(new Error("down")));
    await waitFor(() => expect(api.list).toHaveBeenCalled());
    expect(result.current.rollout).toBeNull();
  });
});
