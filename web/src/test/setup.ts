// Vitest setup: jest-dom matchers (toBeInTheDocument, …) and a DOM reset
// between tests.
import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

afterEach(() => {
  cleanup();
});
