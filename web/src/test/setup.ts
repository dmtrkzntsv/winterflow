// Vitest setup: jest-dom matchers (toBeInTheDocument, …) and a DOM reset
// between tests.
import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

afterEach(() => {
  cleanup();
});

// jsdom has no ResizeObserver; Radix primitives (radio group, …) measure
// with it. A no-op stand-in is enough for behaviour tests.
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
};
