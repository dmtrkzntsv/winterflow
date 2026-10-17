import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { useTemplateLooks } from "@/hooks/use-template-looks";
import type { TemplateSummary, TemplatesApi } from "@/lib/templates";

function listed(id: string, revision: number, source: "org" | "catalog" = "org") {
  return { id, latest_revision: revision, source } as TemplateSummary;
}

function revision(summary: string) {
  return { body: { icon: "box", color: "blue", summary } } as Awaited<ReturnType<TemplatesApi["getRevision"]>>;
}

describe("useTemplateLooks", () => {
  it("loads only org revisions, at most four at a time", async () => {
    const complete: ((value: ReturnType<typeof revision>) => void)[] = [];
    const getRevision = vi.fn(() => new Promise<ReturnType<typeof revision>>((resolve) => complete.push(resolve)));
    const api = { getRevision } as unknown as Pick<TemplatesApi, "getRevision">;
    const list = [listed("one", 1), listed("two", 1), listed("three", 1), listed("four", 1), listed("five", 1), listed("catalog", 1, "catalog")];
    const { result } = renderHook(() => useTemplateLooks(api, list));

    expect(getRevision).toHaveBeenCalledTimes(4);
    await act(async () => complete[0](revision("First")));
    await waitFor(() => expect(getRevision).toHaveBeenCalledTimes(5));
    expect(getRevision).not.toHaveBeenCalledWith("catalog", 1);
    expect(result.current.one.summary).toBe("First");
  });

  it("ignores an older revision response after the list advances", async () => {
    const complete: ((value: ReturnType<typeof revision>) => void)[] = [];
    const getRevision = vi.fn(() => new Promise<ReturnType<typeof revision>>((resolve) => complete.push(resolve)));
    const api = { getRevision } as unknown as Pick<TemplatesApi, "getRevision">;
    const { result, rerender } = renderHook(({ list }) => useTemplateLooks(api, list), {
      initialProps: { list: [listed("one", 1)] },
    });

    rerender({ list: [listed("one", 2)] });
    expect(getRevision).toHaveBeenCalledWith("one", 2);
    await act(async () => complete[0](revision("Old")));
    expect(result.current.one).toBeUndefined();
    await act(async () => complete[1](revision("Current")));
    expect(result.current.one.summary).toBe("Current");
  });
});
