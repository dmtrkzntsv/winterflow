import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useTemplateInspection } from "@/hooks/use-template-inspection";
import type { Inspection, TemplateBody } from "@/lib/templates";

const body = (compose: string): TemplateBody => ({ files: { "compose.yaml": compose }, summary: "", description: "", icon: "", color: "" });
const answer = (problem: string): Inspection => ({ detected: { variables: [], expose: [], backups: [], warnings: [] }, problem });

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe("useTemplateInspection", () => {
  it("reads a body once it stopped changing, keeping the last answer while the next is read", async () => {
    const inspectTemplate = vi.fn(async (b: TemplateBody) => answer(b.files!["compose.yaml"]));
    const api = { inspectTemplate };
    const { result, rerender } = renderHook(({ b }) => useTemplateInspection(api, b, true, 500), { initialProps: { b: body("a") } });
    expect(result.current).toEqual({ inspection: null, error: "", checking: true });
    rerender({ b: body("ab") });
    await act(() => vi.advanceTimersByTimeAsync(500));
    // Only the body that stood still for the delay was read.
    expect(inspectTemplate).toHaveBeenCalledTimes(1);
    expect(result.current).toEqual({ inspection: answer("ab"), error: "", checking: false });
    rerender({ b: body("abc") });
    expect(result.current).toEqual({ inspection: answer("ab"), error: "", checking: true });
  });

  it("reports a failed read, keeping the last good answer", async () => {
    const inspectTemplate = vi.fn(async (b: TemplateBody) => {
      if (b.files!["compose.yaml"] === "bad") throw new Error("offline");
      return answer("");
    });
    const { result, rerender } = renderHook(({ b }) => useTemplateInspection({ inspectTemplate }, b, true, 10), {
      initialProps: { b: body("good") },
    });
    await act(() => vi.advanceTimersByTimeAsync(10));
    rerender({ b: body("bad") });
    await act(() => vi.advanceTimersByTimeAsync(10));
    expect(result.current).toEqual({ inspection: answer(""), error: "offline", checking: false });
  });

  it("asks nothing while disabled", async () => {
    const inspectTemplate = vi.fn(async () => answer(""));
    const { result } = renderHook(() => useTemplateInspection({ inspectTemplate }, body("a"), false, 10));
    await act(() => vi.advanceTimersByTimeAsync(50));
    expect(inspectTemplate).not.toHaveBeenCalled();
    expect(result.current).toEqual({ inspection: null, error: "", checking: false });
  });
});
