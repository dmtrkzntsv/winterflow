import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { CommandPaletteProvider } from "@/context/palette-context";
import { useCommandPalette, usePaletteActions } from "@/context/use-palette";

function State() {
  const { open, openPalette, setOpen } = useCommandPalette();
  return (
    <div>
      <span data-testid="open">{open ? "open" : "closed"}</span>
      <button onClick={openPalette}>open it</button>
      <button onClick={() => setOpen(false)}>close it</button>
    </div>
  );
}

function Page({ label, onRun }: { label: string; onRun: () => void }) {
  usePaletteActions("Shop", [{ id: "restart", label, run: onRun }]);
  return null;
}

function Groups() {
  const { groups } = useCommandPalette();
  return (
    <ul>
      {groups.flatMap((g) =>
        g.actions.map((a) => (
          <li key={`${g.heading}-${a.id}`}>
            <button onClick={a.run}>{`${g.heading}: ${a.label}`}</button>
          </li>
        )),
      )}
    </ul>
  );
}

describe("CommandPaletteProvider", () => {
  it("toggles on ⌘K and Ctrl+K, and opens on request", async () => {
    render(
      <CommandPaletteProvider>
        <State />
      </CommandPaletteProvider>,
    );
    const open = () => screen.getByTestId("open");
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    expect(open()).toHaveTextContent("open");
    fireEvent.keyDown(window, { key: "k", ctrlKey: true });
    expect(open()).toHaveTextContent("closed");
    fireEvent.keyDown(window, { key: "k", ctrlKey: true, shiftKey: true });
    expect(open()).toHaveTextContent("closed");
    await userEvent.setup().click(screen.getByRole("button", { name: "open it" }));
    expect(open()).toHaveTextContent("open");
  });

  it("keeps ⌘K from reaching the browser", () => {
    render(
      <CommandPaletteProvider>
        <State />
      </CommandPaletteProvider>,
    );
    const event = new KeyboardEvent("keydown", { key: "k", metaKey: true, cancelable: true });
    act(() => {
      window.dispatchEvent(event);
    });
    expect(event.defaultPrevented).toBe(true);
  });

  it("swallows a held ⌘K without toggling the palette back", () => {
    render(
      <CommandPaletteProvider>
        <State />
      </CommandPaletteProvider>,
    );
    fireEvent.keyDown(window, { key: "k", ctrlKey: true });
    const held = new KeyboardEvent("keydown", { key: "k", ctrlKey: true, repeat: true, cancelable: true });
    act(() => {
      window.dispatchEvent(held);
    });
    expect(held.defaultPrevented).toBe(true);
    expect(screen.getByTestId("open")).toHaveTextContent("open");
  });

  it("leaves ⌘K alone over another dialog, but still closes the palette itself", async () => {
    render(
      <CommandPaletteProvider>
        <State />
        <div role="dialog" data-state="open" />
      </CommandPaletteProvider>,
    );
    const open = () => screen.getByTestId("open");
    const event = new KeyboardEvent("keydown", { key: "k", ctrlKey: true, cancelable: true });
    act(() => {
      window.dispatchEvent(event);
    });
    expect(event.defaultPrevented).toBe(true);
    expect(open()).toHaveTextContent("closed");
    await userEvent.setup().click(screen.getByRole("button", { name: "open it" }));
    fireEvent.keyDown(window, { key: "k", ctrlKey: true });
    expect(open()).toHaveTextContent("closed");
  });

  it("collects the actions a page registers, with the latest handler, until it unmounts", async () => {
    const user = userEvent.setup();
    const first = vi.fn();
    const second = vi.fn();
    const { rerender } = render(
      <CommandPaletteProvider>
        <Page label="Restart" onRun={first} />
        <Groups />
      </CommandPaletteProvider>,
    );
    await user.click(screen.getByRole("button", { name: "Shop: Restart" }));
    expect(first).toHaveBeenCalledTimes(1);

    rerender(
      <CommandPaletteProvider>
        <Page label="Restart" onRun={second} />
        <Groups />
      </CommandPaletteProvider>,
    );
    await user.click(screen.getByRole("button", { name: "Shop: Restart" }));
    expect(second).toHaveBeenCalledTimes(1);
    expect(first).toHaveBeenCalledTimes(1);

    rerender(
      <CommandPaletteProvider>
        <Page label="Restart now" onRun={second} />
        <Groups />
      </CommandPaletteProvider>,
    );
    expect(screen.getByRole("button", { name: "Shop: Restart now" })).toBeInTheDocument();

    rerender(
      <CommandPaletteProvider>
        <Groups />
      </CommandPaletteProvider>,
    );
    expect(screen.queryByRole("button", { name: /Shop/ })).not.toBeInTheDocument();
  });

  it("is inert outside the provider", () => {
    render(
      <>
        <Page label="Restart" onRun={vi.fn()} />
        <State />
      </>,
    );
    expect(screen.getByTestId("open")).toHaveTextContent("closed");
  });
});
