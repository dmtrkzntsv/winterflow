import { afterEach, describe, expect, it, vi } from "vitest";

import { errorId, focusFirstInvalid, hasErrorIn, invalidAttrs } from "@/lib/field-errors";

describe("invalidAttrs", () => {
  it("marks a field with a message invalid and points at the message", () => {
    expect(invalidAttrs("app-name", "Give the app a name.", "font-mono")).toEqual({
      "aria-invalid": true,
      "aria-describedby": "app-name-error",
      className: "font-mono border-destructive focus-visible:border-destructive focus-visible:ring-destructive",
    });
    expect(errorId("app-name")).toBe("app-name-error");
  });

  it("keeps the red border on focus over a field's own focus border", () => {
    const { className } = invalidAttrs("file-f1", "Name this file.", "border-transparent focus-visible:border-input");
    expect(className.split(" ")).toContain("focus-visible:border-destructive");
    expect(className.split(" ")).not.toContain("focus-visible:border-input");
    expect(className.split(" ")).not.toContain("border-transparent");
  });

  it("leaves a fine field alone", () => {
    expect(invalidAttrs("app-name", undefined, "font-mono")).toEqual({
      "aria-invalid": undefined,
      "aria-describedby": undefined,
      className: "font-mono",
    });
  });
});

describe("hasErrorIn", () => {
  it("matches a key or a key prefix", () => {
    const errors = { name: "x", "var:v1": "y" };
    expect(hasErrorIn(errors, "var:")).toBe(true);
    expect(hasErrorIn(errors, "redirect-")).toBe(false);
    expect(hasErrorIn(errors, "redirect-", "name")).toBe(true);
    expect(hasErrorIn({}, "name")).toBe(false);
  });
});

describe("focusFirstInvalid", () => {
  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("focuses the first invalid field once the frame is painted", async () => {
    document.body.innerHTML =
      '<div id="root"><input id="a" /><input id="b" aria-invalid="true" /><input id="c" aria-invalid="true" /></div>';
    const scroll = vi.fn();
    // jsdom has none: the stub is in for this test only.
    const original = Element.prototype.scrollIntoView;
    Element.prototype.scrollIntoView = scroll;
    try {
      focusFirstInvalid(document.getElementById("root"));
      await new Promise((r) => requestAnimationFrame(() => r(null)));
      expect(document.activeElement?.id).toBe("b");
      expect(scroll).toHaveBeenCalledWith({ block: "center" });
    } finally {
      if (original) Element.prototype.scrollIntoView = original;
      else Reflect.deleteProperty(Element.prototype, "scrollIntoView");
    }
  });

  it("focuses a refusal message when no field is invalid, and prefers a field", async () => {
    document.body.innerHTML = '<div id="root"><input id="a" /><p id="m" tabindex="-1" data-refusal>No file</p></div>';
    focusFirstInvalid(document.getElementById("root"));
    await new Promise((r) => requestAnimationFrame(() => r(null)));
    expect(document.activeElement?.id).toBe("m");

    document.body.innerHTML =
      '<div id="root"><p id="m" tabindex="-1" data-refusal>x</p><input id="b" aria-invalid="true" /></div>';
    focusFirstInvalid(document.getElementById("root"));
    await new Promise((r) => requestAnimationFrame(() => r(null)));
    expect(document.activeElement?.id).toBe("b");
  });

  it("skips a field that only carries a warning (aria-invalid false)", async () => {
    document.body.innerHTML =
      '<div id="root"><input id="w" aria-invalid="false" /><input id="bad" aria-invalid="true" /></div>';
    focusFirstInvalid(document.getElementById("root"));
    await new Promise((r) => requestAnimationFrame(() => r(null)));
    expect(document.activeElement?.id).toBe("bad");
  });

  it("does nothing without an invalid field or a root", async () => {
    document.body.innerHTML = '<div id="root"><input id="a" /></div>';
    focusFirstInvalid(document.getElementById("root"));
    focusFirstInvalid(null);
    await new Promise((r) => requestAnimationFrame(() => r(null)));
    expect(document.activeElement).toBe(document.body);
  });
});
