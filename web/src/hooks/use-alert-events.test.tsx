import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useAlertEvents, type EventKindFilter } from "@/hooks/use-alert-events";
import type { AlertEvent } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";
import { fakeNotifications } from "@/test/notifications";

afterEach(() => vi.unstubAllGlobals());

const event = (id: string, over: Partial<AlertEvent> = {}): AlertEvent => ({
  id,
  rule_id: "r1",
  server_id: "s1",
  app_id: "",
  server_name: "prod-1",
  app_name: "",
  target_kind: "server",
  metric: "cpu",
  kind: "fired",
  value: 93,
  threshold: 90,
  title: `title ${id}`,
  message: "",
  deliveries: [],
  created_at: "2026-10-11T12:00:00Z",
  ...over,
});

type Props = { kind: EventKindFilter; server: string | null };

describe("useAlertEvents", () => {
  it("reads the newest page with the filters and pages on", async () => {
    const api = fakeAPI({
      "/api/v1/alerts/get-events": (_body: unknown, q: URLSearchParams) =>
        q.get("cursor") ? { events: [event("e3")], next_cursor: "" } : { events: [event("e1"), event("e2")], next_cursor: "c2" },
    });
    const { result, rerender } = renderHook(({ kind, server }: Props) => useAlertEvents(kind, server), {
      initialProps: { kind: "all", server: null },
    });
    await waitFor(() => expect(result.current.events.map((e) => e.id)).toEqual(["e1", "e2"]));
    expect(result.current.loading).toBe(false);
    expect(result.current.hasMore).toBe(true);
    expect(api.calls[0].query).toBe("");

    act(() => result.current.loadMore());
    await waitFor(() => expect(result.current.events.map((e) => e.id)).toEqual(["e1", "e2", "e3"]));
    expect(api.calls.at(-1)?.query).toBe("?cursor=c2");
    expect(result.current.hasMore).toBe(false);

    rerender({ kind: "fired", server: "s2" });
    await waitFor(() => expect(api.calls.at(-1)?.query).toBe("?kind=fired&server_id=s2"));
    await waitFor(() => expect(result.current.events.map((e) => e.id)).toEqual(["e1", "e2"]));
  });

  it("says why the history could not be read", async () => {
    fakeAPI({ "/api/v1/alerts/get-events": new Error("database is locked") });
    const { result } = renderHook(() => useAlertEvents("all", null));
    await waitFor(() => expect(result.current.error).toBe("database is locked"));
    expect(result.current.loading).toBe(false);
    expect(result.current.events).toEqual([]);
  });

  it("clears the previous filter's events at once, and keeps them cleared when the new first page fails", async () => {
    let fail = false;
    fakeAPI({
      "/api/v1/alerts/get-events": () => {
        if (fail) return new Error("boom");
        return { events: [event("e1")], next_cursor: "c1" };
      },
    });
    const { result, rerender } = renderHook(({ kind, server }: Props) => useAlertEvents(kind, server), {
      initialProps: { kind: "all", server: null },
    });
    await waitFor(() => expect(result.current.events).toHaveLength(1));
    expect(result.current.hasMore).toBe(true);

    fail = true;
    rerender({ kind: "fired", server: null });
    await waitFor(() => expect(result.current.error).toBe("boom"));
    expect(result.current.events).toEqual([]);
    expect(result.current.hasMore).toBe(false);
    expect(result.current.loading).toBe(false);
  });

  it("ignores a response to an outdated filter", async () => {
    const pending: { query: string; resolve: (page: unknown) => void }[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        (url: string) =>
          new Promise<Response>((resolve) => {
            pending.push({
              query: new URL(url, "http://x").search,
              resolve: (page) => resolve(new Response(JSON.stringify({ success: true, message: "ok", data: page }))),
            });
          }),
      ),
    );
    const { result, rerender } = renderHook(({ kind, server }: Props) => useAlertEvents(kind, server), {
      initialProps: { kind: "all", server: null },
    });
    await waitFor(() => expect(pending).toHaveLength(1));
    rerender({ kind: "fired", server: null });
    rerender({ kind: "resolved", server: null });
    await waitFor(() => expect(pending).toHaveLength(3));
    expect(pending[1].query).toBe("?kind=fired");
    expect(pending[2].query).toBe("?kind=resolved");

    pending[2].resolve({ events: [event("newer", { kind: "resolved" })], next_cursor: "" });
    await waitFor(() => expect(result.current.events.map((e) => e.id)).toEqual(["newer"]));
    pending[1].resolve({ events: [event("stale")], next_cursor: "" });
    pending[0].resolve({ events: [event("oldest")], next_cursor: "" });
    await act(() => new Promise((r) => setTimeout(r, 50)));
    expect(result.current.events.map((e) => e.id)).toEqual(["newer"]);
    expect(result.current.loading).toBe(false);
  });

  it("puts a new event on top when an alert fires, keeping the loaded pages", async () => {
    let first = [event("e1")];
    const api = fakeAPI({
      "/api/v1/alerts/get-events": (_body: unknown, q: URLSearchParams) =>
        q.get("cursor") ? { events: [event("old")], next_cursor: "" } : { events: first, next_cursor: "c1" },
    });
    const sse = fakeNotifications();
    const { result } = renderHook(() => useAlertEvents("all", null), { wrapper: sse.Provider });
    const ids = () => result.current.events.map((e) => e.id);
    await waitFor(() => expect(ids()).toEqual(["e1"]));
    act(() => result.current.loadMore());
    await waitFor(() => expect(ids()).toEqual(["e1", "old"]));

    first = [event("e2"), event("e1")];
    act(() => sse.emit({ type: "alert", ref: "", payload: {} }));
    await waitFor(() => expect(ids()).toEqual(["e2", "e1", "old"]));
    // The cursor of the loaded pages is untouched: nothing more to load.
    expect(result.current.hasMore).toBe(false);

    // Another notification type reads nothing; a repeated alert adds nothing.
    const reads = api.calls.length;
    act(() => sse.emit({ type: "apps_status", ref: "", payload: {} }));
    await act(() => new Promise((r) => setTimeout(r, 50)));
    expect(api.calls).toHaveLength(reads);
    act(() => sse.emit({ type: "alert", ref: "", payload: {} }));
    await waitFor(() => expect(api.calls).toHaveLength(reads + 1));
    await act(() => new Promise((r) => setTimeout(r, 50)));
    expect(ids()).toEqual(["e2", "e1", "old"]);
  });

  it("drops a live update made for an older filter", async () => {
    const pending: { query: string; resolve: (page: unknown) => void }[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        (url: string) =>
          new Promise<Response>((resolve) => {
            pending.push({
              query: new URL(url, "http://x").search,
              resolve: (page) => resolve(new Response(JSON.stringify({ success: true, message: "ok", data: page }))),
            });
          }),
      ),
    );
    const sse = fakeNotifications();
    const { result, rerender } = renderHook(({ kind, server }: Props) => useAlertEvents(kind, server), {
      initialProps: { kind: "all", server: null },
      wrapper: sse.Provider,
    });
    await waitFor(() => expect(pending).toHaveLength(1));
    pending[0].resolve({ events: [event("e1")], next_cursor: "" });
    await waitFor(() => expect(result.current.events.map((e) => e.id)).toEqual(["e1"]));
    act(() => sse.emit({ type: "alert", ref: "", payload: {} })); // live read for "all"
    await waitFor(() => expect(pending).toHaveLength(2));
    rerender({ kind: "resolved", server: null });
    await waitFor(() => expect(pending).toHaveLength(3));
    pending[2].resolve({ events: [event("r1", { kind: "resolved" })], next_cursor: "" });
    await waitFor(() => expect(result.current.events.map((e) => e.id)).toEqual(["r1"]));
    pending[1].resolve({ events: [event("f9")], next_cursor: "" });
    await act(() => new Promise((r) => setTimeout(r, 50)));
    expect(result.current.events.map((e) => e.id)).toEqual(["r1"]);
  });

  // pendingFetch answers each read when the test says so.
  function pendingFetch() {
    const pending: { query: string; resolve: (page: unknown) => void; fail: (m: string) => void }[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(
        (url: string) =>
          new Promise<Response>((resolve) => {
            pending.push({
              query: new URL(url, "http://x").search,
              resolve: (page) => resolve(new Response(JSON.stringify({ success: true, message: "ok", data: page }))),
              fail: (m) => resolve(new Response(JSON.stringify({ success: false, message: m }), { status: 500 })),
            });
          }),
      ),
    );
    return pending;
  }

  it("keeps an event a live update added while the first page was still loading", async () => {
    const pending = pendingFetch();
    const sse = fakeNotifications();
    const { result } = renderHook(() => useAlertEvents("all", null), { wrapper: sse.Provider });
    await waitFor(() => expect(pending).toHaveLength(1));
    act(() => sse.emit({ type: "alert", ref: "", payload: {} }));
    await waitFor(() => expect(pending).toHaveLength(2));
    // The live read is answered first (it saw the new event), then the older first page.
    pending[1].resolve({ events: [event("new"), event("e1")], next_cursor: "" });
    await waitFor(() => expect(result.current.events.map((e) => e.id)).toEqual(["new", "e1"]));
    pending[0].resolve({ events: [event("e1"), event("e0")], next_cursor: "c1" });
    await waitFor(() => expect(result.current.events.map((e) => e.id)).toEqual(["new", "e1", "e0"]));
    expect(result.current.hasMore).toBe(true);
    expect(result.current.loading).toBe(false);
  });

  it("clears a stale error once a live update reads fine", async () => {
    const pending = pendingFetch();
    const sse = fakeNotifications();
    const { result } = renderHook(() => useAlertEvents("all", null), { wrapper: sse.Provider });
    await waitFor(() => expect(pending).toHaveLength(1));
    pending[0].fail("database is locked");
    await waitFor(() => expect(result.current.error).toBe("database is locked"));
    act(() => sse.emit({ type: "alert", ref: "", payload: {} }));
    await waitFor(() => expect(pending).toHaveLength(2));
    pending[1].resolve({ events: [event("e1")], next_cursor: "" });
    await waitFor(() => expect(result.current.events.map((e) => e.id)).toEqual(["e1"]));
    expect(result.current.error).toBeNull();
  });

  it("shows no events for new filters from the very first render, not the old list for a frame", async () => {
    fakeAPI({ "/api/v1/alerts/get-events": { events: [event("e1")], next_cursor: "c1" } });
    const renders: { kind: EventKindFilter; ids: string[]; loading: boolean; hasMore: boolean }[] = [];
    const { result, rerender } = renderHook(
      ({ kind, server }: Props) => {
        const state = useAlertEvents(kind, server);
        renders.push({ kind, ids: state.events.map((e) => e.id), loading: state.loading, hasMore: state.hasMore });
        return state;
      },
      { initialProps: { kind: "all", server: null } },
    );
    await waitFor(() => expect(result.current.events).toHaveLength(1));
    rerender({ kind: "fired", server: null });
    const first = renders.find((r) => r.kind === "fired");
    expect(first).toEqual({ kind: "fired", ids: [], loading: true, hasMore: false });
    await waitFor(() => expect(result.current.events.map((e) => e.id)).toEqual(["e1"]));
  });
});
