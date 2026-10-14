// Vitest setup: jest-dom matchers (toBeInTheDocument, …) and a DOM reset
// between tests.
import "@testing-library/jest-dom/vitest";
import { cleanup, configure } from "@testing-library/react";
import { afterEach } from "vitest";

import { resetHeldCreates } from "@/lib/held-creates";

afterEach(() => {
  cleanup();
  // A create a test left held (it outlived its wait) must not claim its
  // name in the next test.
  resetHeldCreates();
});

// findBy*/waitFor default to 1 s, which a loaded runner with coverage on can
// miss for a debounced check or a dialog's first render.
configure({ asyncUtilTimeout: 3000 });

// jsdom has no ResizeObserver; Radix primitives (radio group, …) measure
// with it. A no-op stand-in is enough for behaviour tests.
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
};

// Radix Select and vaul's bottom sheet use pointer capture and
// scrollIntoView, which jsdom lacks.
Element.prototype.setPointerCapture ??= () => {};
Element.prototype.hasPointerCapture ??= () => false;
Element.prototype.releasePointerCapture ??= () => {};
Element.prototype.scrollIntoView ??= () => {};
