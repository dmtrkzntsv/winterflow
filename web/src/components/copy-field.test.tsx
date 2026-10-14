import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

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
});
