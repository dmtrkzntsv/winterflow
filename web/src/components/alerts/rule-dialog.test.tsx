import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { RuleDialog } from "@/components/alerts/rule-dialog";
import type { Channel, Rule } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";

afterEach(() => vi.unstubAllGlobals());

const channels = [
  { id: "c1", name: "Ops", enabled: true },
  { id: "c2", name: "Pager", enabled: true },
] as Channel[];

describe("RuleDialog", () => {
  it("creates an app disk rule routed to chosen channels", async () => {
    const api = fakeAPI({ "/api/v1/alerts/save-rule": {} });
    const onSaved = vi.fn();
    const user = userEvent.setup({ delay: null });
    render(<RuleDialog open onOpenChange={() => {}} channels={channels} onSaved={onSaved} />);

    await user.click(screen.getByRole("combobox", { name: "Watch" }));
    await user.click(await screen.findByRole("option", { name: "an app" }));
    await user.click(screen.getByRole("combobox", { name: "Metric" }));
    await user.click(await screen.findByRole("option", { name: "Disk" }));
    expect(screen.getByLabelText(/Above \(GB\)/)).toHaveValue(50);
    await user.clear(screen.getByLabelText(/Above/));
    await user.type(screen.getByLabelText(/Above/), "120");
    await user.clear(screen.getByLabelText("For (minutes)"));
    await user.type(screen.getByLabelText("For (minutes)"), "15");
    await user.click(screen.getByLabelText("Specific channels"));
    await user.click(screen.getByLabelText("Pager"));
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(api.posted("/api/v1/alerts/save-rule")).toEqual([
      {
        id: "",
        target_kind: "app",
        metric: "disk",
        threshold: 120,
        duration_minutes: 15,
        channel_ids: ["c1", "c2"],
        enabled: true,
      },
    ]);
    expect(onSaved).toHaveBeenCalled();
  });

  it("has no threshold for down rules and checks the duration", async () => {
    const api = fakeAPI({ "/api/v1/alerts/save-rule": {} });
    const user = userEvent.setup({ delay: null });
    render(<RuleDialog open onOpenChange={() => {}} channels={[]} onSaved={() => {}} />);
    await user.click(screen.getByRole("combobox", { name: "Metric" }));
    await user.click(await screen.findByRole("option", { name: "is down" }));
    expect(screen.queryByLabelText(/Above/)).not.toBeInTheDocument();
    await user.clear(screen.getByLabelText("For (minutes)"));
    await user.type(screen.getByLabelText("For (minutes)"), "90");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(screen.getByRole("alert")).toHaveTextContent("1 to 60 minutes");
    expect(api.calls).toHaveLength(0);
  });

  it("edits a rule without changing what it watches", async () => {
    const rule: Rule = {
      id: "r1",
      target_kind: "server",
      metric: "memory",
      threshold: 90,
      duration_minutes: 10,
      channel_ids: [],
      enabled: true,
      created_at: "2026-10-11T12:00:00Z",
    };
    const api = fakeAPI({ "/api/v1/alerts/save-rule": rule });
    const user = userEvent.setup({ delay: null });
    render(<RuleDialog open onOpenChange={() => {}} rule={rule} channels={channels} onSaved={() => {}} />);
    expect(screen.getByRole("combobox", { name: "Watch" })).toBeDisabled();
    expect(screen.getByRole("combobox", { name: "Metric" })).toBeDisabled();
    expect(screen.getByLabelText("Inbox only")).toBeChecked();
    await user.clear(screen.getByLabelText(/Above/));
    await user.type(screen.getByLabelText(/Above/), "101");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(screen.getByRole("alert")).toHaveTextContent("at most 100");
    await user.clear(screen.getByLabelText(/Above/));
    await user.type(screen.getByLabelText(/Above/), "85");
    await user.click(screen.getByLabelText("Default channels"));
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(api.posted("/api/v1/alerts/save-rule")[0]).toMatchObject({ id: "r1", metric: "memory", threshold: 85, channel_ids: null });
  });
  it("reads as one sentence and closes on Cancel", async () => {
    fakeAPI({});
    const onOpenChange = vi.fn();
    const user = userEvent.setup({ delay: null });
    render(<RuleDialog open onOpenChange={onOpenChange} channels={channels} onSaved={() => {}} />);
    const dialog = screen.getByRole("dialog");
    for (const words of ["Alert when", "is above", "%", "for", "minutes", "Then notify"]) {
      expect(within(dialog).getByText(words)).toBeInTheDocument();
    }
    expect(screen.getByRole("combobox", { name: "Watch" })).toHaveTextContent("a server");
    expect(screen.getByRole("combobox", { name: "Metric" })).toHaveTextContent("CPU");
    expect(screen.getByRole("radiogroup", { name: "Notify" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});
