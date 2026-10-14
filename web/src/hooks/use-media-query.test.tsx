import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useMediaQuery } from "@/hooks/use-media-query";
import { useIsMobile } from "@/hooks/use-mobile";
import { stubViewport } from "@/test/media";

// seen records every render's answer: the first one must already be right.
const seen = vi.fn();
function Probe() {
  seen(useIsMobile());
  return null;
}

function Query({ q }: { q: string }) {
  return <p data-testid="match">{String(useMediaQuery(q))}</p>;
}

describe("useIsMobile", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    seen.mockClear();
  });

  it("is mobile below 1024px, on the very first render", () => {
    stubViewport(375);
    render(<Probe />);
    expect(seen.mock.calls[0][0]).toBe(true);
  });

  it("is a desktop at 1024px and up", () => {
    stubViewport(1024);
    render(<Probe />);
    expect(seen.mock.calls[0][0]).toBe(false);
  });

  it("reads a browser without matchMedia (jsdom) as a desktop", () => {
    render(<Probe />);
    expect(seen.mock.calls[0][0]).toBe(false);
  });
});

describe("useMediaQuery", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("re-renders when the query starts matching", () => {
    const listeners = new Set<() => void>();
    const mql = {
      matches: false,
      addEventListener: (_: string, l: () => void) => listeners.add(l),
      removeEventListener: (_: string, l: () => void) => listeners.delete(l),
    };
    vi.stubGlobal("matchMedia", () => mql);
    const { unmount } = render(<Query q="(max-width: 639px)" />);
    expect(screen.getByTestId("match")).toHaveTextContent("false");
    act(() => {
      mql.matches = true;
      listeners.forEach((l) => l());
    });
    expect(screen.getByTestId("match")).toHaveTextContent("true");
    unmount();
    expect(listeners.size).toBe(0);
  });
});
