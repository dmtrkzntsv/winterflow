import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { HistorySheet } from "@/components/app-page/history-sheet";
import type { Ownership } from "@/components/app-page/ownership";
import { forgetRevisions, resetRevisionsCache } from "@/lib/revisions";
import { AppsContext, type AppRevisions, type AppsContextValue } from "@/context/apps-context-base";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

const now = Math.floor(Date.now() / 1000);
const HISTORY: AppRevisions = {
  current: "cccccccc11",
  deployed: "bbbbbbbb11",
  revisions: [
    { hash: "cccccccc11", subject: "Changed compose.yml", timestamp: now - 60 },
    { hash: "bbbbbbbb11", subject: "Changed .env", timestamp: now - 7200 },
    { hash: "aaaaaaaa11", subject: "create", timestamp: now - 86400 },
  ],
};

const ctx = { getRevisions: vi.fn(), rollback: vi.fn(), control: vi.fn(), refresh: vi.fn() };

const onChanged = vi.fn();

function renderSheet(ownership: Ownership = null, open = true) {
  const onOpenChange = vi.fn();
  render(
    <AppsContext.Provider value={ctx as unknown as AppsContextValue}>
      <HistorySheet appId="a1" ownership={ownership} open={open} onOpenChange={onOpenChange} onChanged={onChanged} />
    </AppsContext.Provider>,
  );
  return onOpenChange;
}

describe("HistorySheet", () => {
  beforeEach(() => {
    for (const f of Object.values(ctx)) f.mockReset();
    for (const f of Object.values(toast)) f.mockReset();
    onChanged.mockReset();
    ctx.getRevisions.mockResolvedValue(HISTORY);
    resetRevisionsCache();
    // The real context forgets the app's history when these succeed, which
    // makes the open sheet read it again.
    ctx.rollback.mockImplementation(async () => forgetRevisions("a1"));
    ctx.control.mockImplementation(async () => forgetRevisions("a1"));
  });

  it("lists the versions by what changed and when, with the commit on hover", async () => {
    renderSheet();
    const sheet = await screen.findByRole("dialog", { name: "History" });
    expect(await within(sheet).findByText("3 versions.", { exact: false })).toBeInTheDocument();
    expect(within(sheet).getByText("Changed compose.yml")).toHaveAttribute("title", "Commit cccccccc");
    expect(within(sheet).getByText("Draft, not deployed")).toBeInTheDocument();
    expect(within(sheet).getByText("Live")).toBeInTheDocument();
    expect(within(sheet).getByText("2 hours ago")).toBeInTheDocument();
    expect(sheet).not.toHaveTextContent(/Revision/);
  });

  it("reads nothing while closed", () => {
    renderSheet(null, false);
    expect(ctx.getRevisions).not.toHaveBeenCalled();
  });

  it("is full width on a phone", async () => {
    renderSheet();
    const sheet = await screen.findByRole("dialog", { name: "History" });
    expect(sheet.className).toContain("w-full");
  });

  it("rolls back after confirming, and reads the history again", async () => {
    const user = userEvent.setup();
    renderSheet();
    const sheet = await screen.findByRole("dialog", { name: "History" });
    const older = (await within(sheet).findByText("create")).closest("li")!;
    await user.click(within(older).getByRole("button", { name: /Roll back/ }));
    const confirm = await screen.findByRole("alertdialog", { name: "Roll back to “create”?" });
    await user.click(within(confirm).getByRole("button", { name: "Roll back" }));
    await waitFor(() => expect(ctx.rollback).toHaveBeenCalledWith("a1", "aaaaaaaa11"));
    expect(toast.success).toHaveBeenCalledWith("Rolled back and redeployed");
    expect(onChanged).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(ctx.getRevisions).toHaveBeenCalledTimes(2));
  });

  it("keeps the roll back dialog open when the roll back fails", async () => {
    const user = userEvent.setup();
    ctx.rollback.mockRejectedValueOnce(new Error("agent offline"));
    renderSheet();
    const sheet = await screen.findByRole("dialog", { name: "History" });
    const older = (await within(sheet).findByText("create")).closest("li")!;
    await user.click(within(older).getByRole("button", { name: /Roll back/ }));
    const confirm = await screen.findByRole("alertdialog", { name: "Roll back to “create”?" });
    await user.click(within(confirm).getByRole("button", { name: "Roll back" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Roll back failed", { description: "agent offline" }));
    // Nothing changed, so nobody is told to read the app again.
    expect(onChanged).not.toHaveBeenCalled();
    expect(screen.getByRole("alertdialog", { name: "Roll back to “create”?" })).toBeInTheDocument();
    expect(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Roll back" })).toBeEnabled();
  });

  it("deploys the draft", async () => {
    const user = userEvent.setup();
    renderSheet();
    await user.click(await screen.findByRole("button", { name: "Deploy draft" }));
    await waitFor(() => expect(ctx.control).toHaveBeenCalledWith("a1", "start"));
    expect(toast.success).toHaveBeenCalledWith("Draft deployed");
    expect(onChanged).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(ctx.getRevisions).toHaveBeenCalledTimes(2));
  });

  it("holds Deploy draft and Roll back while the history is read again", async () => {
    renderSheet();
    const sheet = await screen.findByRole("dialog", { name: "History" });
    expect(await within(sheet).findByRole("button", { name: "Deploy draft" })).toBeEnabled();
    let land: (v: typeof HISTORY) => void = () => {};
    ctx.getRevisions.mockReturnValueOnce(new Promise((resolve) => (land = resolve)));
    act(() => forgetRevisions("a1"));
    await waitFor(() => expect(within(sheet).getByRole("button", { name: "Deploy draft" })).toBeDisabled());
    for (const button of within(sheet).getAllByRole("button", { name: /Roll back/ })) expect(button).toBeDisabled();
    await act(async () => land(HISTORY));
    await waitFor(() => expect(within(sheet).getByRole("button", { name: "Deploy draft" })).toBeEnabled());
  });

  it("offers no roll back for an app a template manages", async () => {
    renderSheet({ loading: false, error: "", instance: null, current: null, reload: vi.fn(async () => {}) });
    const sheet = await screen.findByRole("dialog", { name: "History" });
    expect(await within(sheet).findByText(/Managed by a template/)).toBeInTheDocument();
    expect(within(sheet).queryByRole("button", { name: /Roll back/ })).not.toBeInTheDocument();
  });

  it("shows a failed read with Try again", async () => {
    const user = userEvent.setup();
    ctx.getRevisions.mockRejectedValueOnce(new Error("agent offline"));
    renderSheet();
    expect(await screen.findByText("agent offline")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Changed compose.yml")).toBeInTheDocument();
  });
});
