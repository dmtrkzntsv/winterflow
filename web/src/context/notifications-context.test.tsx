import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { NotificationsProvider } from "@/context/notifications-context";
import { WAIT_TIMEOUT, WaitTimeoutError, type NotificationsContextValue } from "@/context/notifications-context-base";
import { useNotifications } from "@/context/use-notifications";

vi.mock("@/context/use-auth", () => ({ useAuth: () => ({ isAuthenticated: true }) }));

type Stream = { onopen?: () => void; onerror?: () => void; onmessage?: (e: { data: string }) => void };
let streams: Stream[];

beforeEach(() => {
  streams = [];
  vi.stubGlobal(
    "EventSource",
    class {
      onopen?: () => void;
      onerror?: () => void;
      onmessage?: (e: { data: string }) => void;
      constructor() {
        streams.push(this);
      }
      close() {}
    },
  );
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

// mount renders the provider and records every context value it hands out.
function mount() {
  const seen: NotificationsContextValue[] = [];
  function Probe() {
    seen.push(useNotifications());
    return null;
  }
  render(
    <NotificationsProvider>
      <Probe />
    </NotificationsProvider>,
  );
  const send = (n: unknown) => act(() => streams[0].onmessage?.({ data: JSON.stringify(n) }));
  return { seen, latest: () => seen[seen.length - 1], send };
}

describe("NotificationsProvider", () => {
  it("keeps subscribe and waitFor across reconnects; only connected changes", () => {
    const { seen, latest } = mount();
    const first = latest();
    expect(first.connected).toBe(false);
    act(() => streams[0].onopen?.());
    expect(latest().connected).toBe(true);
    act(() => streams[0].onerror?.());
    expect(latest().connected).toBe(false);
    expect(new Set(seen.map((v) => v.subscribe)).size).toBe(1);
    expect(new Set(seen.map((v) => v.waitFor)).size).toBe(1);
    expect(latest()).not.toBe(first);
  });

  it("delivers notifications to subscribers, past a faulty one and a malformed message", () => {
    const { latest, send } = mount();
    const got: string[] = [];
    latest().subscribe(() => {
      throw new Error("faulty");
    });
    const unsubscribe = latest().subscribe((n) => got.push(n.type));
    act(() => streams[0].onmessage?.({ data: "not json" }));
    send({ type: "status", ref: "" });
    unsubscribe();
    send({ type: "later", ref: "" });
    expect(got).toEqual(["status"]);
  });

  it("waitFor resolves with the result, also one that came before it asked", async () => {
    const { latest, send } = mount();
    send({ type: "app.save", ref: "r1", status: 0 });
    await expect(latest().waitFor("r1")).resolves.toMatchObject({ ref: "r1" });
    const pending = latest().waitFor("r2");
    send({ type: "other", ref: "r3" });
    send({ type: "app.save", ref: "r2", status: 1, error: "boom" });
    await expect(pending).resolves.toMatchObject({ ref: "r2", error: "boom" });
  });

  it("waitFor rejects with a WaitTimeoutError when no result comes in time", async () => {
    vi.useFakeTimers();
    const { latest } = mount();
    const pending = latest().waitFor("r9", 1000);
    vi.advanceTimersByTime(1000);
    await expect(pending).rejects.toThrow(WAIT_TIMEOUT);
    await expect(pending).rejects.toBeInstanceOf(WaitTimeoutError);
  });
});
