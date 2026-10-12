import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { useDarkMode } from "./use-dark-mode";

describe("useDarkMode", () => {
  afterEach(() => document.documentElement.classList.remove("dark"));

  it("follows the .dark class on <html>", async () => {
    const { result } = renderHook(() => useDarkMode());
    expect(result.current).toBe(false);

    await act(async () => {
      document.documentElement.classList.add("dark");
      await Promise.resolve();
    });
    expect(result.current).toBe(true);

    await act(async () => {
      document.documentElement.classList.remove("dark");
      await Promise.resolve();
    });
    expect(result.current).toBe(false);
  });
});
