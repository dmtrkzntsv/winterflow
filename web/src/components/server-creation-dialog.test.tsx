import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ServerSelectionDialog } from "@/components/server-creation-dialog";
import { fakeAPI } from "@/test/fake-api";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

const COMMAND = "curl -fsSL https://get.winterflow.io/agent | sudo bash";

function renderDialog(isOpen = true) {
  const onClose = vi.fn();
  const onServerAdded = vi.fn();
  const view = render(<ServerSelectionDialog isOpen={isOpen} onClose={onClose} onServerAdded={onServerAdded} />);
  const reopen = (open: boolean) =>
    view.rerender(<ServerSelectionDialog isOpen={open} onClose={onClose} onServerAdded={onServerAdded} />);
  return { onClose, onServerAdded, reopen };
}

// The code input is input-otp's transparent <input> over the slots; its
// container turns pointer events off, which user-event would refuse.
const setup = () => userEvent.setup({ pointerEventsCheck: 0 });

beforeEach(() => {
  toast.success.mockReset();
});
afterEach(() => vi.unstubAllGlobals());

describe("ServerSelectionDialog", () => {
  it("installs, then connects the server with the code, in two steps", async () => {
    const user = setup();
    const api = fakeAPI({ "/api/v1/server/register": { server: { id: "s9", name: "nas" } } });
    const { onClose, onServerAdded } = renderDialog();
    const dialog = screen.getByRole("dialog", { name: "Add a server" });
    expect(dialog).toHaveTextContent("Step 1 of 2");
    expect(screen.getByTestId("install-command")).toHaveTextContent(COMMAND);
    await user.click(within(dialog).getByRole("button", { name: "Copy" }));
    expect(await navigator.clipboard.readText()).toBe(COMMAND);
    expect(within(dialog).getByRole("button", { name: "Copied" })).toBeInTheDocument();

    await user.click(within(dialog).getByRole("button", { name: "Next: enter the code" }));
    expect(dialog).toHaveTextContent("Step 2 of 2");
    const add = within(dialog).getByRole("button", { name: "Add server" });
    expect(add).toBeDisabled();
    const code = within(dialog).getByLabelText("Connection code");
    // Codes have letters: phones get a text keyboard with capitals.
    expect(code).toHaveAttribute("inputmode", "text");
    expect(code).toHaveAttribute("autocapitalize", "characters");
    await user.click(code);
    await user.paste("ab12cd");
    expect(add).toBeEnabled();
    await user.click(add);
    await waitFor(() => expect(onServerAdded).toHaveBeenCalled());
    expect(api.posted("/api/v1/server/register")).toEqual([{ code: "AB12CD" }]);
    expect(onClose).toHaveBeenCalled();
    expect(toast.success).toHaveBeenCalledWith("Added nas");
  });

  it("explains a wrong or expired code in plain words and keeps it for a fix", async () => {
    const user = setup();
    fakeAPI({ "/api/v1/server/register": new Error("invalid registration code") });
    const { onClose } = renderDialog();
    await user.click(screen.getByRole("button", { name: "Next: enter the code" }));
    const code = screen.getByLabelText("Connection code");
    await user.click(code);
    await user.paste("AB12CD");
    await user.click(screen.getByRole("button", { name: "Add server" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("That code doesn't match a server waiting to connect");
    expect(code).toHaveValue("AB12CD");
    expect(onClose).not.toHaveBeenCalled();
    vi.unstubAllGlobals();

    fakeAPI({ "/api/v1/server/register": new Error("registration code has expired") });
    await user.click(screen.getByRole("button", { name: "Add server" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("That code has expired");
  });

  it("goes back to Copy a moment after copying, and links the quick-start guide", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const user = userEvent.setup({ pointerEventsCheck: 0, advanceTimers: vi.advanceTimersByTime });
      renderDialog();
      expect(screen.getByRole("link", { name: "Help" })).toHaveAttribute("href", "https://docs.winterflow.io/quick-start");
      await user.click(screen.getByRole("button", { name: "Copy" }));
      expect(await screen.findByRole("button", { name: "Copied" })).toBeInTheDocument();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(2100);
      });
      expect(screen.getByRole("button", { name: "Copy" })).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it("goes back from the code step to the command", async () => {
    const user = setup();
    renderDialog();
    await user.click(screen.getByRole("button", { name: "Next: enter the code" }));
    await user.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.getByRole("dialog")).toHaveTextContent("Step 1 of 2");
  });

  it("says when copying isn't possible, so the command is copied by hand", async () => {
    const user = setup();
    renderDialog();
    vi.spyOn(navigator.clipboard, "writeText").mockRejectedValue(new Error("denied"));
    await user.click(screen.getByRole("button", { name: "Copy" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Select the command and copy it yourself");
    expect(screen.getByTestId("install-command")).toHaveClass("select-all");
  });

  it("keeps provider links behind “Don't have a server yet?”", async () => {
    const user = setup();
    renderDialog();
    expect(screen.queryByRole("link", { name: /Hetzner/ })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Don't have a server yet?" }));
    const link = screen.getByRole("link", { name: /Hetzner/ });
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
  });

  it("never asks for an organization", async () => {
    const user = setup();
    renderDialog();
    expect(screen.getByRole("dialog")).not.toHaveTextContent(/organization/i);
    await user.click(screen.getByRole("button", { name: "Next: enter the code" }));
    expect(screen.getByRole("dialog")).not.toHaveTextContent(/organization/i);
  });

  it("starts over when closed and opened again", async () => {
    const user = setup();
    const { onClose, reopen } = renderDialog();
    await user.click(screen.getByRole("button", { name: "Next: enter the code" }));
    await user.click(screen.getByLabelText("Connection code"));
    await user.paste("AB1");
    await user.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalled();
    reopen(false);
    reopen(true);
    expect(await screen.findByRole("dialog")).toHaveTextContent("Step 1 of 2");
    await user.click(screen.getByRole("button", { name: "Next: enter the code" }));
    expect(screen.getByLabelText("Connection code")).toHaveValue("");
  });
});
