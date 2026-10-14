import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { UnsavedChangesGuard } from "@/components/unsaved-changes-guard";

// blocker stands in for react-router's: a navigation held, its proceed
// (which, for Back/Forward, lands only later) and reset.
const blocker = vi.hoisted(() => ({
  state: "blocked" as "blocked" | "unblocked" | "proceeding",
  proceed: vi.fn(),
  reset: vi.fn(),
}));
vi.mock("react-router-dom", async (original) => ({
  ...(await original<typeof import("react-router-dom")>()),
  useBlocker: () => ({ ...blocker }),
}));

beforeEach(() => {
  blocker.state = "blocked";
  blocker.proceed.mockReset();
  blocker.reset.mockReset();
});

const guard = (when = true) => <UnsavedChangesGuard when={when} stays={() => false} description="Edits go." />;

describe("UnsavedChangesGuard", () => {
  it("lets Discard go without resetting the blocker behind it (Back would ask again)", async () => {
    const user = userEvent.setup();
    render(guard());
    const ask = screen.getByRole("alertdialog", { name: "Discard unsaved changes?" });
    await user.click(within(ask).getByRole("button", { name: "Discard changes" }));
    expect(blocker.proceed).toHaveBeenCalledTimes(1);
    // The dialog closes with the blocker of its last render, still blocked.
    await waitFor(() => expect(blocker.reset).not.toHaveBeenCalled());
  });

  it("resets the blocker on Keep editing", async () => {
    const user = userEvent.setup();
    render(guard());
    await user.click(screen.getByRole("button", { name: "Keep editing" }));
    expect(blocker.reset).toHaveBeenCalledTimes(1);
    expect(blocker.proceed).not.toHaveBeenCalled();
  });

  it("asks again after a Discard once the blocker went back to idle", async () => {
    const user = userEvent.setup();
    const { rerender } = render(guard());
    await user.click(screen.getByRole("button", { name: "Discard changes" }));
    blocker.state = "unblocked";
    rerender(guard());
    blocker.state = "blocked";
    rerender(guard());
    await user.click(await screen.findByRole("button", { name: "Keep editing" }));
    expect(blocker.reset).toHaveBeenCalledTimes(1);
  });

  it("has the browser ask before a reload or closing the tab, only while there are edits", () => {
    blocker.state = "unblocked";
    const { rerender, unmount } = render(guard(true));
    const leave = () => {
      const e = new Event("beforeunload", { cancelable: true });
      window.dispatchEvent(e);
      return e.defaultPrevented;
    };
    expect(leave()).toBe(true);
    rerender(guard(false));
    expect(leave()).toBe(false);
    rerender(guard(true));
    unmount();
    expect(leave()).toBe(false);
  });
});
