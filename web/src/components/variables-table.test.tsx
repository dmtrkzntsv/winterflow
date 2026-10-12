import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { VariablesTable } from "@/components/variables-table";
import type { InheritedVariable, SharedVariable } from "@/lib/shared-variables";

const base: SharedVariable = {
  id: "v1", name: "SMTP_HOST", scope: "server", is_secret: false, value: "mail.local",
  description: "relay", updated_at: "2026-10-11T00:00:00Z", updated_by: "u1", missing_on: [],
};
const secret: SharedVariable = { ...base, id: "v2", name: "SMTP_PASS", is_secret: true, value: "<encrypted>", missing_on: ["s3"] };

function renderTable(extra: Partial<Parameters<typeof VariablesTable>[0]> = {}) {
  const handlers = { onEdit: vi.fn(), onDelete: vi.fn(), onReload: vi.fn(), onProvide: vi.fn(), onResync: vi.fn() };
  render(
    <MemoryRouter>
      <VariablesTable title="This server" variables={[base, secret]} serverNames={new Map([["s3", "srv-3"]])} {...handlers} {...extra} />
    </MemoryRouter>,
  );
  return handlers;
}

describe("VariablesTable", () => {
  it("shows plain values and never a secret value", () => {
    renderTable();
    const own = screen.getByRole("region", { name: "This server" });
    expect(within(own).getByText("mail.local")).toBeInTheDocument();
    expect(within(own).getByText("encrypted")).toBeInTheDocument();
    expect(screen.queryByText("<encrypted>")).not.toBeInTheDocument();
  });

  it("marks coverage and offers Provide value for missing secrets", async () => {
    const user = userEvent.setup();
    const h = renderTable({ showCoverage: true });
    expect(screen.getByText("Missing on srv-3")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Provide value" }));
    expect(h.onProvide).toHaveBeenCalledWith(secret);
    await user.click(screen.getByRole("button", { name: "Reload apps using SMTP_HOST" }));
    expect(h.onReload).toHaveBeenCalledWith(base);
    await user.click(screen.getByRole("button", { name: "Edit SMTP_PASS" }));
    expect(h.onEdit).toHaveBeenCalledWith(secret);
    await user.click(screen.getByRole("button", { name: "Delete SMTP_HOST" }));
    expect(h.onDelete).toHaveBeenCalledWith(base);
    await user.click(screen.getByRole("button", { name: "Resync SMTP_HOST" }));
    expect(h.onResync).toHaveBeenCalledWith(base);
  });

  it("groups inherited org variables read-only with overridden marks, then built-ins", () => {
    const inherited: InheritedVariable[] = [
      { ...base, id: "o1", scope: "org", value: "mail.org", overridden: true },
      { ...secret, id: "o2", scope: "org", overridden: false },
    ];
    renderTable({ inherited, builtins: [{ name: "WF_SERVER_NAME", value: "alpha", description: "This server's name" }, { name: "WF_TZ", value: "", description: "tz" }] });
    const org = screen.getByRole("region", { name: "Inherited from org" });
    expect(within(org).getByText("overridden")).toBeInTheDocument();
    expect(within(org).getByText("missing here")).toBeInTheDocument();
    expect(within(org).queryByRole("button", { name: /^Edit|^Delete/ })).not.toBeInTheDocument();
    expect(within(org).getByRole("link", { name: "Manage org variables" })).toHaveAttribute("href", "/org/variables");
    const builtins = screen.getByRole("region", { name: "Built-in" });
    expect(within(builtins).getByText("alpha")).toBeInTheDocument();
    expect(within(builtins).getByText("set by the agent")).toBeInTheDocument();
  });
});
