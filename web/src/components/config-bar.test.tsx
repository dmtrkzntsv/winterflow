import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ConfigBar } from "@/components/config-bar";

function phone(on: boolean) {
  vi.stubGlobal("matchMedia", () => ({
    matches: on,
    addEventListener: () => {},
    removeEventListener: () => {},
  }));
}
afterEach(() => vi.unstubAllGlobals());

function bar(props: { onOpenHistory?: () => void; discard?: () => void; dirty?: boolean; status?: string } = {}) {
  return render(
    <ConfigBar
      status={props.status ?? "Unsaved changes"}
      dirty={props.dirty ?? true}
      onOpenHistory={props.onOpenHistory}
      secondary={[
        { label: "Save draft", onSelect: vi.fn(), title: "Save a new version without redeploying" },
        { label: "Discard", onSelect: props.discard ?? vi.fn() },
      ]}
      primary={<button type="button">Save & redeploy</button>}
    />,
  );
}

describe("ConfigBar", () => {
  it("is only History while nothing is edited, on a desktop and on a phone", () => {
    const { container, unmount } = bar({ onOpenHistory: vi.fn(), dirty: false, status: "" });
    expect(screen.getAllByRole("button").map((b) => b.textContent?.trim())).toEqual(["History"]);
    expect(container.firstElementChild).not.toHaveClass("sticky");
    unmount();
    phone(true);
    bar({ onOpenHistory: vi.fn(), dirty: false, status: "" });
    expect(screen.getAllByRole("button").map((b) => b.textContent?.trim())).toEqual(["History"]);
  });

  it("keeps a status that says something while nothing is edited, and is nothing without History", () => {
    const { unmount } = bar({ dirty: false, status: "Managed by template x" });
    expect(screen.getByText("Managed by template x")).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    unmount();
    const { container } = bar({ dirty: false, status: "" });
    expect(container).toBeEmptyDOMElement();
  });

  it("is a sticky bar of Discard, Save draft and Save & redeploy once edited on a desktop", () => {
    const { container } = bar({ onOpenHistory: vi.fn() });
    const names = screen.getAllByRole("button").map((b) => b.textContent?.trim());
    expect(names).toEqual(["Discard", "Save draft", "Save & redeploy"]);
    expect(container.firstElementChild).toHaveClass("sticky");
    expect(screen.getByText("Unsaved changes")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "More save options" })).not.toBeInTheDocument();
  });

  it("keeps only the primary and a ⋯ menu on a phone", async () => {
    phone(true);
    const user = userEvent.setup();
    const onOpenHistory = vi.fn();
    const discard = vi.fn();
    bar({ onOpenHistory, discard });
    expect(screen.getAllByRole("button").map((b) => b.textContent)).toEqual(["Save & redeploy", ""]);
    await user.click(screen.getByRole("button", { name: "More save options" }));
    expect(screen.getAllByRole("menuitem").map((m) => m.textContent)).toEqual(["History", "Save draft", "Discard"]);
    await user.click(screen.getByRole("menuitem", { name: "History" }));
    expect(onOpenHistory).toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "More save options" }));
    await user.click(screen.getByRole("menuitem", { name: "Discard" }));
    expect(discard).toHaveBeenCalled();
  });

  it("doesn't wrap on a phone and clears the mobile top bar", () => {
    phone(true);
    const { container } = bar({ onOpenHistory: vi.fn() });
    const el = container.firstElementChild!;
    expect(el).toHaveClass("flex-nowrap", "max-lg:top-14");
  });

  it("has no History without a way to open it", async () => {
    bar();
    expect(screen.queryByRole("button", { name: "History" })).not.toBeInTheDocument();
  });

  it("opens History from the bar's own button on a desktop", async () => {
    const onOpenHistory = vi.fn();
    bar({ onOpenHistory, dirty: false });
    await userEvent.setup().click(screen.getByRole("button", { name: "History" }));
    expect(onOpenHistory).toHaveBeenCalled();
  });

  it("puts the status under the bar on a phone, in the bar on a desktop", () => {
    phone(true);
    const { container, unmount } = bar({ onOpenHistory: vi.fn() });
    expect(container.firstElementChild).not.toHaveTextContent("Unsaved changes");
    expect(screen.getByText("Unsaved changes")).toBeInTheDocument();
    unmount();
    phone(false);
    const desktop = bar({ onOpenHistory: vi.fn() });
    expect(desktop.container.firstElementChild).toHaveTextContent("Unsaved changes");
  });

  it("disables a menu action that is disabled", async () => {
    phone(true);
    const user = userEvent.setup();
    render(
      <ConfigBar
        status="x"
        dirty
        secondary={[{ label: "Discard", onSelect: vi.fn(), disabled: true }]}
        primary={<button type="button">Save</button>}
      />,
    );
    await user.click(screen.getByRole("button", { name: "More save options" }));
    expect(screen.getByRole("menuitem", { name: "Discard" })).toHaveAttribute("aria-disabled", "true");
  });
});
