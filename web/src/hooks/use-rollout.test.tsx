import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { NotificationsContext, type Notification, type NotificationHandler } from "@/context/notifications-context-base";
import { REREAD_MS, useRollout } from "@/hooks/use-rollout";
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
  const view = renderHook(({ scope }: { scope?: string | null }) => useRollout(api, "template_upgrade", scope), {
    wrapper,
    initialProps: { scope: scopeKey },
  });
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

  it("drops the previous scope's rollout when the scope changes", async () => {
    const t2 = rollout({ id: "r9", scope_key: "t2", created_at: "2026-10-01T00:00:00Z", done: 5 });
    const { result, api, emit, rerender } = setup(Promise.resolve([rollout()]), "t1");
    await waitFor(() => expect(result.current.rollout?.scope_key).toBe("t1"));

    api.list.mockImplementation(() => Promise.resolve([t2]));
    rerender({ scope: "t2" });
    expect(result.current.rollout).toBeNull();
    await waitFor(() => expect(result.current.rollout?.id).toBe("r9"));
    // t2's events apply although t2's rollout is older than the t1 one that was shown.
    emit({ type: "rollout", ref: "", payload: { ...t2, done: 6 } });
    expect(result.current.rollout?.done).toBe(6);
    // t1's events no longer apply.
    emit({ type: "rollout", ref: "", payload: rollout({ done: 9 }) });
    expect(result.current.rollout?.id).toBe("r9");

    rerender({ scope: null });
    expect(result.current.rollout).toBeNull();
    emit({ type: "rollout", ref: "", payload: t2 });
    expect(result.current.rollout).toBeNull();
  });

  it("follows nothing without a scope yet", () => {
    const { result, api, emit } = setup(Promise.resolve([rollout()]), null);
    emit({ type: "rollout", ref: "", payload: rollout() });
    expect(api.list).not.toHaveBeenCalled();
    expect(result.current.rollout).toBeNull();
  });

  it("never shows an ended rollout active again", async () => {
    const { result, emit } = setup(Promise.resolve([rollout()]), "t1");
    await waitFor(() => expect(result.current.rollout?.state).toBe("running"));
    emit({ type: "rollout", ref: "", payload: rollout({ state: "stopped", done: 3 }) });
    // A late event of the same rollout, or a stale stop answer, is older news.
    emit({ type: "rollout", ref: "", payload: rollout({ state: "running", done: 2 }) });
    expect(result.current.rollout?.state).toBe("stopped");
    act(() => result.current.set(rollout({ stop_requested: true })));
    expect(result.current.rollout).toMatchObject({ state: "stopped", done: 3 });
    // Another rollout, active, still replaces it.
    act(() => result.current.set(rollout({ id: "r2", created_at: "2026-10-13T00:00:00Z", state: "queued" })));
    expect(result.current.rollout?.id).toBe("r2");
  });

  it("never shows older progress than it has", async () => {
    const { result, emit } = setup(Promise.resolve([rollout()]), "t1");
    await waitFor(() => expect(result.current.rollout?.state).toBe("running"));
    // The stream's running event beat the upgrade's own queued answer.
    emit({ type: "rollout", ref: "", payload: rollout({ state: "running", done: 1 }) });
    act(() => result.current.set(rollout({ state: "queued", done: 0, skipped: 0 })));
    expect(result.current.rollout).toMatchObject({ state: "running", done: 1 });
    // A late event with an older count does not lower it.
    emit({ type: "rollout", ref: "", payload: rollout({ done: 4 }) });
    emit({ type: "rollout", ref: "", payload: rollout({ done: 2 }) });
    expect(result.current.rollout?.done).toBe(4);
    // Nor does a stop request get taken back by an older copy.
    act(() => result.current.set(rollout({ done: 4, stop_requested: true })));
    emit({ type: "rollout", ref: "", payload: rollout({ done: 4, stop_requested: false }) });
    emit({ type: "rollout", ref: "", payload: rollout({ done: 3, stop_requested: false }) });
    expect(result.current.rollout).toMatchObject({ done: 4, stop_requested: true });
  });

  it("shows nothing when the read fails", async () => {
    const { result, api } = setup(Promise.reject(new Error("down")));
    await waitFor(() => expect(api.list).toHaveBeenCalled());
    expect(result.current.rollout).toBeNull();
  });
});

describe("useRollout re-reads an active rollout", () => {
  afterEach(() => {
    vi.useRealTimers();
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
  });

  const tick = () => act(async () => vi.advanceTimersByTimeAsync(REREAD_MS));

  it("every 15 s while it is active and the page visible, then stops", async () => {
    vi.useFakeTimers();
    const { result, api } = setup(Promise.resolve([rollout({ done: 1 })]), "t1");
    await act(async () => {});
    expect(result.current.rollout?.done).toBe(1);
    expect(api.list).toHaveBeenCalledTimes(1);

    api.list.mockImplementation(() => Promise.resolve([rollout({ done: 2 })]));
    await tick();
    expect(api.list).toHaveBeenCalledTimes(2);
    expect(result.current.rollout?.done).toBe(2);

    // A hidden page reads nothing.
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
    await tick();
    expect(api.list).toHaveBeenCalledTimes(2);
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });

    // A newer rollout of the scope replaces it; a read that lacks the shown one keeps it.
    api.list.mockImplementation(() => Promise.resolve([]));
    await tick();
    expect(result.current.rollout?.done).toBe(2);

    api.list.mockImplementation(() => Promise.resolve([rollout({ state: "done", done: 12 })]));
    await tick();
    expect(result.current.rollout).toMatchObject({ state: "done", done: 12 });
    const calls = api.list.mock.calls.length;
    await tick();
    await tick();
    expect(api.list).toHaveBeenCalledTimes(calls); // ended: no more reads
  });

  it("takes a newer rollout from a re-read, and never revives an ended one", async () => {
    vi.useFakeTimers();
    const { result, api, emit } = setup(Promise.resolve([rollout()]), "t1");
    await act(async () => {});
    api.list.mockImplementation(() => Promise.resolve([rollout({ id: "r2", created_at: "2026-10-13T00:00:00Z", state: "queued" }), rollout()]));
    await tick();
    expect(result.current.rollout?.id).toBe("r2");

    // A re-read in flight when r2's end arrives does not revive it.
    let answer: (l: Rollout[]) => void = () => {};
    api.list.mockImplementation(() => new Promise((resolve) => (answer = resolve)));
    const reads = api.list.mock.calls.length;
    await tick();
    expect(api.list).toHaveBeenCalledTimes(reads + 1);
    emit({ type: "rollout", ref: "", payload: rollout({ id: "r2", created_at: "2026-10-13T00:00:00Z", state: "failed" }) });
    await act(async () => answer([rollout({ id: "r2", created_at: "2026-10-13T00:00:00Z", state: "running" })]));
    expect(result.current.rollout).toMatchObject({ id: "r2", state: "failed" });
  });

  it("does not lower the count of the shown rollout", async () => {
    vi.useFakeTimers();
    const { result, api } = setup(Promise.resolve([rollout({ done: 5 })]), "t1");
    await act(async () => {});
    api.list.mockImplementation(() => Promise.resolve([rollout({ done: 2 })]));
    await tick();
    expect(result.current.rollout?.done).toBe(5);
  });
});
