import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import SharedVariablesSettingsPage from "@/pages/settings-shared-variables";

let active: { id: string; name: string } | null = { id: "s1", name: "Alpha" };
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServer: active }) }));
vi.mock("@/pages/org-variables", () => ({ default: () => <p>org variables</p> }));
vi.mock("@/pages/settings-variables", () => ({
  VariablesCard: ({ serverId }: { serverId: string }) => <p>server variables {serverId}</p>,
}));
vi.mock("@/lib/shared-variables", () => ({ createSharedVariablesApi: () => ({}) }));

describe("SharedVariablesSettingsPage", () => {
  afterEach(() => {
    active = { id: "s1", name: "Alpha" };
  });

  it("shows the organization's variables, then this server's own", () => {
    render(<SharedVariablesSettingsPage />);
    expect(screen.getByRole("heading", { level: 1, name: "Shared variables" })).toBeInTheDocument();
    expect(screen.getByText("org variables")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Alpha only" })).toHaveTextContent("server variables s1");
  });

  it("shows only the organization's without a server", () => {
    active = null;
    render(<SharedVariablesSettingsPage />);
    expect(screen.getByText("org variables")).toBeInTheDocument();
    expect(screen.queryByText(/server variables/)).not.toBeInTheDocument();
  });
});
