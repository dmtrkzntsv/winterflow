import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useNow } from "@/hooks/use-now";

afterEach(() => vi.useRealTimers());

describe("useNow", () => {
  it("re-reads the time every period", () => {
    vi.useFakeTimers({ now: 1_000_000 });
    const { result } = renderHook(() => useNow(10_000));
    expect(result.current).toBe(1_000_000);
    act(() => vi.advanceTimersByTime(10_000));
    expect(result.current).toBe(1_010_000);
  });

  it("runs no timer while disabled, and counts again once enabled", () => {
    vi.useFakeTimers({ now: 1_000_000 });
    const { result, rerender } = renderHook(({ on }) => useNow(10_000, on), { initialProps: { on: false } });
    expect(vi.getTimerCount()).toBe(0);
    act(() => vi.advanceTimersByTime(60_000));
    expect(result.current).toBe(1_000_000);
    rerender({ on: true });
    // Enabled again, it reads the time at once, not a period late.
    act(() => vi.advanceTimersByTime(0));
    expect(result.current).toBe(1_060_000);
    expect(vi.getTimerCount()).toBe(1);
    act(() => vi.advanceTimersByTime(10_000));
    expect(result.current).toBe(1_070_000);
    rerender({ on: false });
    expect(vi.getTimerCount()).toBe(0);
  });
});
