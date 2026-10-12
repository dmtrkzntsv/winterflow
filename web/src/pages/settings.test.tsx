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
vi.mock("@/pages/settings-variables", () => ({
  VariablesCard: ({ serverId }: { serverId: string }) => <p>variables card {serverId}</p>,
}));
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

  it("offers the Variables tab for the URL's server", () => {
    ingress = false;
    renderAt("/server/s1/settings?tab=variables");
    expect(screen.getByRole("tab", { name: "Variables" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("variables card s1")).toBeInTheDocument();
  });

  it("offers the Storages tab on every server", () => {
    ingress = false;
    renderAt("/settings?tab=storages");
    expect(screen.getByRole("tab", { name: "Storages" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("storages card")).toBeInTheDocument();
  });

  it("scrolls the tab bar inside itself on a phone instead of widening the page", () => {
    ingress = true;
    renderAt("/settings?tab=storages");
    const list = screen.getByRole("tablist");
    // The list keeps its natural width; its wrapper is capped to the page
    // and scrolls.
    expect(list).toHaveClass("w-max");
    expect(list.parentElement).toHaveClass("max-w-full", "overflow-x-auto");
  });

  it("brings a linked tab into view in the scrolled bar", () => {
    ingress = true;
    const scrolled: Element[] = [];
    const spy = vi.fn(function (this: Element) {
      scrolled.push(this);
    });
    Element.prototype.scrollIntoView = spy;
    try {
      renderAt("/settings?tab=storages");
      expect(scrolled).toContain(screen.getByRole("tab", { name: "Storages" }));
    } finally {
      delete (Element.prototype as Partial<Element>).scrollIntoView;
    }
  });
});
