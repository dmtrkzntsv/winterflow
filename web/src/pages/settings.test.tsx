import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import SettingsPage from "@/pages/settings";

let ingress = true;

vi.mock("@/layouts/use-app-layout", () => ({ useAppBreadcrumbs: () => {} }));
vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ activeServer: { id: "s1", features: { ingress } } }),
}));
vi.mock("@/context/use-docker", () => ({
  useDocker: () => ({
    hasServer: true,
    listRegistries: vi.fn().mockResolvedValue([]),
    listNetworks: vi.fn().mockResolvedValue([]),
  }),
}));
vi.mock("@/pages/settings-domains", () => ({ DomainsCard: () => <p>domains card</p> }));
vi.mock("@/pages/settings-volumes", () => ({ VolumesCard: () => <p>volumes card</p> }));
vi.mock("@/pages/settings-storages", () => ({ StoragesCard: () => <p>storages card</p> }));

function renderAt(url: string) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <SettingsPage />
    </MemoryRouter>,
  );
}

describe("SettingsPage", () => {
  it("offers the Domains tab on a server with ingress", () => {
    ingress = true;
    renderAt("/settings?tab=domains");
    expect(screen.getByRole("tab", { name: "Domains" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("domains card")).toBeInTheDocument();
  });

  it("hides it without ingress and falls back to registries", () => {
    ingress = false;
    renderAt("/settings?tab=domains");
    expect(screen.queryByRole("tab", { name: "Domains" })).not.toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Registries" })).toHaveAttribute("aria-selected", "true");
    expect(screen.queryByText("domains card")).not.toBeInTheDocument();
  });

  it("offers the Storages tab on every server", () => {
    ingress = false;
    renderAt("/settings?tab=storages");
    expect(screen.getByRole("tab", { name: "Storages" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("storages card")).toBeInTheDocument();
  });
});
