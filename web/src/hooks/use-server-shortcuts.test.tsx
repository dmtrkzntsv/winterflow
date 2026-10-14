import { render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useServerShortcuts } from "@/hooks/use-server-shortcuts";

const selectServer = vi.fn();
vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ servers: [{ id: "s1" }, { id: "s2" }], selectServer }),
}));

function Probe({ enabled }: { enabled: boolean }) {
  useServerShortcuts(enabled);
  return null;
}

const press = (init: KeyboardEventInit) => {
  const event = new KeyboardEvent("keydown", { cancelable: true, ...init });
  window.dispatchEvent(event);
  return event;
};

describe("useServerShortcuts", () => {
  afterEach(() => selectServer.mockClear());

  it("switches to the Nth server on ⌘N or Ctrl+N, instead of the browser's tab switch", () => {
    render(<Probe enabled />);
    expect(press({ key: "2", ctrlKey: true }).defaultPrevented).toBe(true);
    expect(selectServer).toHaveBeenLastCalledWith("s2");
    press({ key: "1", metaKey: true });
    expect(selectServer).toHaveBeenLastCalledWith("s1");
  });

  it("leaves numbers past the last server and other modifiers to the browser", () => {
    render(<Probe enabled />);
    expect(press({ key: "3", ctrlKey: true }).defaultPrevented).toBe(false);
    press({ key: "1", ctrlKey: true, altKey: true });
    press({ key: "1" });
    expect(selectServer).not.toHaveBeenCalled();
  });

  it("does nothing when disabled (a standalone install)", () => {
    render(<Probe enabled={false} />);
    press({ key: "1", ctrlKey: true });
    expect(selectServer).not.toHaveBeenCalled();
  });
});
