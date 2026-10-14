import { afterEach, describe, expect, it, vi } from "vitest";

import { copyText } from "@/lib/clipboard";

const original = Object.getOwnPropertyDescriptor(navigator, "clipboard");

afterEach(() => {
  if (original) Object.defineProperty(navigator, "clipboard", original);
  else delete (navigator as { clipboard?: unknown }).clipboard;
  vi.restoreAllMocks();
});

describe("copyText", () => {
  it("uses the clipboard API when the page may", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    await copyText("wfp_abc");
    expect(writeText).toHaveBeenCalledWith("wfp_abc");
  });

  it("falls back to a selection copy on a plain-http origin, and fails when that is refused", async () => {
    Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true });
    const exec = vi.fn().mockReturnValueOnce(true).mockReturnValueOnce(false);
    Object.defineProperty(document, "execCommand", { value: exec, configurable: true });
    await copyText("one");
    expect(exec).toHaveBeenCalledWith("copy");
    await expect(copyText("two")).rejects.toThrow("copy rejected");
    expect(document.querySelector("textarea")).toBeNull();
  });

  it("puts the fallback field inside the dialog that has focus", async () => {
    Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true });
    const host = document.createElement("div");
    host.setAttribute("role", "dialog");
    const button = document.createElement("button");
    host.appendChild(button);
    document.body.appendChild(host);
    button.focus();
    let parent: Element | null = null;
    Object.defineProperty(document, "execCommand", {
      value: vi.fn(() => {
        parent = document.querySelector("textarea")?.parentElement ?? null;
        return true;
      }),
      configurable: true,
    });
    try {
      await copyText("inside");
      expect(parent).toBe(host);
      expect(host.querySelector("textarea")).toBeNull();
    } finally {
      host.remove();
    }
  });
});
