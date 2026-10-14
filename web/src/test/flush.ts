import { act } from "@testing-library/react";

// flushPending runs, inside act, all the promise work already queued — a
// mocked fetch's answer read through, the state set after it, the effects
// that state starts. A macrotask starts only after every microtask queued
// before it, so this is an ordering guarantee, not a guess at how long the
// work takes on a slow runner. Use it before asserting that something did
// NOT happen, once the work that could cause it has been started or
// answered. (Work behind a timer needs fake timers instead.)
export function flushPending(): Promise<void> {
  return act(() => new Promise<void>((resolve) => setTimeout(resolve, 0)));
}
