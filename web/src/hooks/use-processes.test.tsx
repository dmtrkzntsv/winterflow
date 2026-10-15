import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { NotificationsContext, WaitTimeoutError, type Notification, type NotificationsContextValue } from "@/context/notifications-context-base";
import { PROCESS_POLL_MS, useProcesses } from "@/hooks/use-processes";
import { fakeAPI } from "@/test/fake-api";

const waitFor_ = vi.fn<(ref: string, ms?: number) => Promise<Notification>>();
const ctx = { connected: true, subscribe: () => () => {}, waitFor: waitFor_ } as unknown as NotificationsContextValue;
const wrapper = ({ children }: { children: ReactNode }) => (
  <NotificationsContext.Provider value={ctx}>{children}</NotificationsContext.Provider>
);
const LIST = { sampled_at: 1, ncpu: 4, mem_total: 1, total: 1, processes: [{ pid: 1 }] };

describe("useProcesses", () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    waitFor_.mockReset().mockResolvedValue({ type: "operation_result", ref: "r1", status: 0, payload: LIST });
  });
  afterEach(() => vi.useRealTimers());

  it("does nothing while disabled", async () => {
    const api = fakeAPI({ "/api/v1/server/get-processes": { request_id: "r1" } });
    renderHook(() => useProcesses("s1", false), { wrapper });
    await act(() => vi.advanceTimersByTimeAsync(PROCESS_POLL_MS * 3));
    expect(api.calls).toHaveLength(0);
  });

  it("polls one request at a time, 5 s apart, and waits 15 s for each", async () => {
    const api = fakeAPI({ "/api/v1/server/get-processes": { request_id: "r1" } });
    const { result } = renderHook(() => useProcesses("s1", true), { wrapper });
    await waitFor(() => expect(result.current.data?.ncpu).toBe(4));
    expect(api.calls[0].query).toBe("?server_id=s1");
    expect(waitFor_).toHaveBeenCalledWith("r1", 15000);
    await act(() => vi.advanceTimersByTimeAsync(PROCESS_POLL_MS - 100));
    expect(api.calls).toHaveLength(1);
    await act(() => vi.advanceTimersByTimeAsync(200));
    await waitFor(() => expect(api.calls).toHaveLength(2));
  });

  it("stops polling when disabled", async () => {
    const api = fakeAPI({ "/api/v1/server/get-processes": { request_id: "r1" } });
    const { rerender } = renderHook(({ on }) => useProcesses("s1", on), { wrapper, initialProps: { on: true } });
    await waitFor(() => expect(api.calls).toHaveLength(1));
    rerender({ on: false });
    await act(() => vi.advanceTimersByTimeAsync(PROCESS_POLL_MS * 3));
    expect(api.calls).toHaveLength(1);
  });

  it("drops a late reply after the server changed", async () => {
    fakeAPI({ "/api/v1/server/get-processes": { request_id: "r1" } });
    let release!: (n: Notification) => void;
    waitFor_.mockImplementationOnce(() => new Promise((r) => (release = r)));
    const { result, rerender } = renderHook(({ id }) => useProcesses(id, true), { wrapper, initialProps: { id: "s1" } });
    await waitFor(() => expect(waitFor_).toHaveBeenCalledTimes(1));
    rerender({ id: "s2" });
    await act(async () => release({ type: "operation_result", ref: "r1", status: 0, payload: { ...LIST, ncpu: 99 } }));
    await waitFor(() => expect(result.current.data?.ncpu).toBe(4)); // s2's own reply
    expect(result.current.data?.ncpu).not.toBe(99);
  });

  it("shows a timeout and keeps polling, keeping the last good data", async () => {
    const api = fakeAPI({ "/api/v1/server/get-processes": { request_id: "r1" } });
    const { result } = renderHook(() => useProcesses("s1", true), { wrapper });
    await waitFor(() => expect(result.current.data).not.toBeNull());
    waitFor_.mockRejectedValueOnce(new WaitTimeoutError());
    await act(() => vi.advanceTimersByTimeAsync(PROCESS_POLL_MS + 10));
    await waitFor(() => expect(result.current.error).toBe("The server did not answer in time."));
    expect(result.current.data?.ncpu).toBe(4);
    await act(() => vi.advanceTimersByTimeAsync(PROCESS_POLL_MS + 10));
    await waitFor(() => expect(api.calls.length).toBeGreaterThanOrEqual(3));
    await waitFor(() => expect(result.current.error).toBeNull());
  });

  it("refresh cuts the wait short", async () => {
    const api = fakeAPI({ "/api/v1/server/get-processes": { request_id: "r1" } });
    const { result } = renderHook(() => useProcesses("s1", true), { wrapper });
    await waitFor(() => expect(result.current.data).not.toBeNull());
    act(() => result.current.refresh());
    await waitFor(() => expect(api.calls).toHaveLength(2));
  });

  it("reports the agent's error", async () => {
    fakeAPI({ "/api/v1/server/get-processes": { request_id: "r1" } });
    waitFor_.mockResolvedValue({ type: "operation_result", ref: "r1", status: 1, error: "process list is only available on Linux" });
    const { result } = renderHook(() => useProcesses("s1", true), { wrapper });
    await waitFor(() => expect(result.current.error).toBe("process list is only available on Linux"));
  });
});
