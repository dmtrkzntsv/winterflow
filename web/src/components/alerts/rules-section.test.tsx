import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { RulesSection } from "@/components/alerts/rules-section";
import type { Channel, Rule } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";

afterEach(() => vi.unstubAllGlobals());

const base = { channel_ids: null, enabled: true, created_at: "2026-10-11T12:00:00Z" };
const rules: Rule[] = [
  { ...base, id: "r1", target_kind: "server", metric: "cpu", threshold: 90, duration_minutes: 10 },
  { ...base, id: "r2", target_kind: "server", metric: "down", threshold: 0, duration_minutes: 5 },
  { ...base, id: "r3", target_kind: "app", metric: "down", threshold: 0, duration_minutes: 5, channel_ids: ["c1"] },
];
const channels = [{ id: "c1", name: "Ops", enabled: true }] as Channel[];

describe("RulesSection", () => {
  it("groups rules by target, toggles one inline and deletes one after a confirmation", async () => {
    let list = rules;
    const api = fakeAPI({
      "/api/v1/alerts/get-rules": () => list,
      "/api/v1/alerts/save-rule": {},
      "/api/v1/alerts/delete-rule": () => {
        list = rules.filter((r) => r.id !== "r2");
        return null;
      },
    });
    const user = userEvent.setup({ delay: null });
    render(<RulesSection channels={channels} />);

    const servers = await screen.findByRole("region", { name: "Servers" });
    expect(within(servers).getAllByTestId("rule-row")).toHaveLength(2);
    expect(within(servers).getByText("CPU above 90% for 10 min")).toBeInTheDocument();
    // On the default channels: no line about them.
    expect(within(servers).queryByText("Default channels")).not.toBeInTheDocument();
    const apps = screen.getByRole("region", { name: "Apps" });
    expect(within(apps).getByText("App down for 5 min")).toBeInTheDocument();
    expect(within(apps).getByText("Ops")).toBeInTheDocument();

    await user.click(screen.getByRole("switch", { name: "Enable CPU above 90% for 10 min" }));
    expect(api.posted("/api/v1/alerts/save-rule")[0]).toMatchObject({ id: "r1", enabled: false, threshold: 90 });

    await user.click(screen.getByRole("button", { name: "Actions for Server down for 5 min" }));
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(confirm).toHaveTextContent("Server down for 5 min. Its adjustments go with it; its alerts stay in the Inbox.");
    await user.click(within(confirm).getByRole("button", { name: "Delete rule" }));
    await waitFor(() => expect(api.posted("/api/v1/alerts/delete-rule")).toEqual([{ id: "r2" }]));
    // Focus lands on Add rule, not <body>, once the row is gone.
    await waitFor(() => expect(screen.getByRole("button", { name: /Add rule/ })).toHaveFocus());
    expect(within(servers).getAllByTestId("rule-row")).toHaveLength(1);
  });

  it("keeps the confirmation open and the rule when a delete fails", async () => {
    fakeAPI({
      "/api/v1/alerts/get-rules": rules,
      "/api/v1/alerts/delete-rule": new Error("rule is busy"),
    });
    const user = userEvent.setup({ delay: null });
    render(<RulesSection channels={channels} />);
    await user.click(await screen.findByRole("button", { name: "Actions for Server down for 5 min" }));
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
    const confirm = await screen.findByRole("alertdialog");
    await user.click(within(confirm).getByRole("button", { name: "Delete rule" }));
    await waitFor(() => expect(within(confirm).getByRole("button", { name: "Delete rule" })).toBeEnabled());
    expect(screen.getAllByTestId("rule-row")).toHaveLength(3);
    await user.click(within(confirm).getByRole("button", { name: "Cancel" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Actions for Server down for 5 min" })).toHaveFocus(),
    );
  });

  it("opens the rule dialog to edit one", async () => {
    fakeAPI({ "/api/v1/alerts/get-rules": rules });
    const user = userEvent.setup({ delay: null });
    render(<RulesSection channels={channels} />);
    await user.click(await screen.findByRole("button", { name: "Actions for CPU above 90% for 10 min" }));
    await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Edit rule")).toBeInTheDocument();
    expect(within(dialog).getByLabelText(/Above/)).toHaveValue(90);
  });

  it("says when a group has no rules", async () => {
    fakeAPI({ "/api/v1/alerts/get-rules": null });
    render(<RulesSection channels={[]} />);
    expect(await screen.findByText("No rules for servers yet.")).toBeInTheDocument();
    expect(screen.getByText("No rules for apps yet.")).toBeInTheDocument();
  });

  it("says the rules could not be loaded instead of claiming there are none", async () => {
    const api = fakeAPI({ "/api/v1/alerts/get-rules": new Error("boom") });
    const user = userEvent.setup({ delay: null });
    render(<RulesSection channels={[]} />);
    expect(await screen.findByRole("alert")).toHaveTextContent("boom");
    expect(screen.queryByText(/No rules for/)).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(api.posted("/api/v1/alerts/get-rules")).toHaveLength(2));
  });
});
