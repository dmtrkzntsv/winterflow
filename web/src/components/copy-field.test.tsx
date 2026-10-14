import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

const toastSuccess = vi.hoisted(() => vi.fn());
const toastError = vi.hoisted(() => vi.fn());
vi.mock("sonner", () => ({ toast: { success: toastSuccess, error: toastError } }));
const copy = vi.hoisted(() => vi.fn());
vi.mock("@/lib/clipboard", () => ({ copyText: copy }));

import { CopyField } from "@/components/copy-field";

describe("CopyField", () => {
  it("shows the value read-only and copies it", async () => {
    const user = userEvent.setup({ delay: null });
    copy.mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error("no"));
    render(<CopyField value="TEMP-123" label="Temporary password" />);
    expect(screen.getByLabelText("Temporary password")).toHaveValue("TEMP-123");
    expect(screen.getByLabelText("Temporary password")).toHaveAttribute("readonly");
    await user.click(screen.getByRole("button", { name: "Copy temporary password" }));
    expect(copy).toHaveBeenCalledWith("TEMP-123");
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith("Copied"));
    await user.click(screen.getByRole("button", { name: "Copy temporary password" }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith("Couldn't copy: select the text and copy it yourself"));
  });

  describe("the Copied mark", () => {
    afterEach(() => vi.useRealTimers());
    const icon = () => screen.getByRole("button", { name: "Copy token" }).querySelector("svg");

    it("goes away after a moment", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      copy.mockResolvedValue(undefined);
      render(<CopyField value="wfp_1" label="Token" />);
      const before = icon()?.getAttribute("class");
      await act(async () => screen.getByRole("button", { name: "Copy token" }).click());
      expect(icon()?.getAttribute("class")).not.toBe(before);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(2100);
      });
      expect(icon()?.getAttribute("class")).toBe(before);
    });

    it("does not carry over to a new value", async () => {
      copy.mockResolvedValue(undefined);
      const { rerender } = render(<CopyField value="wfp_1" label="Token" />);
      const before = icon()?.getAttribute("class");
      await act(async () => screen.getByRole("button", { name: "Copy token" }).click());
      expect(icon()?.getAttribute("class")).not.toBe(before);
      rerender(<CopyField value="wfp_2" label="Token" />);
      expect(icon()?.getAttribute("class")).toBe(before);
    });
  });
});
