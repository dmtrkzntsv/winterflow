import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AddressRows } from "@/components/ingress-editor";
import type { AppEditorState, AppIngress, ExposedPort } from "@/types/app-config";

vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));

const EXPOSE: ExposedPort[] = [
  { id: "app:8080/http", container: "app", port: 8080, protocol: "http", host_port: 20013, bind: "127.0.0.1", pinned: false },
  { id: "admin:9000/http", container: "admin", port: 9000, protocol: "http", host_port: 20014, bind: "127.0.0.1", pinned: false },
  { id: "db:5432/tcp", container: "db", port: 5432, protocol: "tcp", host_port: 20015, bind: "0.0.0.0", pinned: false },
];

function Harness({ ingress, expose = EXPOSE }: { ingress: AppIngress; expose?: ExposedPort[] }) {
  const [state, setState] = useState<AppEditorState>({
    config: { id: "app-1", name: "x", files: [], variables: [], ingress },
    files: {},
    variables: {},
    storageVariables: [],
    expose,
  });
  return (
    <MemoryRouter>
      <AddressRows state={state} onChange={setState} appId="app-1" serverId="srv-1" registrations={[]} onRefresh={() => {}} />
      <output data-testid="ingress">{JSON.stringify(state.config.ingress)}</output>
    </MemoryRouter>
  );
}
const domains = () => JSON.parse(screen.getByTestId("ingress").textContent ?? "{}").domains;

describe("address rows: expose targets", () => {
  beforeEach(() => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ success: true, data: { available: true, covered: true, claims: null } }))));
  });
  afterEach(() => vi.unstubAllGlobals());

  it("offers the app's web ports, never a tcp one, and a port of the compose file's own", async () => {
    const user = userEvent.setup({ delay: null });
    render(<Harness ingress={{ domains: [], redirects: [] }} />);
    await user.click(screen.getByRole("button", { name: /Add hostname/ }));
    const select = screen.getByRole("combobox", { name: "Send to" });
    expect([...select.querySelectorAll("option")].map((o) => o.textContent)).toEqual(["app:8080", "admin:9000", "Port…"]);
    expect(domains()[0]).toMatchObject({ expose: "app:8080" });
    expect(select).toHaveClass("max-sm:h-10");
    expect(screen.queryByLabelText("Port")).not.toBeInTheDocument();
    await user.selectOptions(select, "admin:9000");
    expect(domains()[0]).toMatchObject({ expose: "admin:9000", upstream_port: "" });
    await user.selectOptions(select, "Port…");
    expect(domains()[0].expose).toBeUndefined();
    await user.type(screen.getByLabelText("Port"), "8088");
    expect(domains()[0]).toMatchObject({ upstream_port: 8088 });
  });

  it("keeps the port field alone for an app without expose entries", () => {
    render(<Harness ingress={{ domains: [{ id: "d1", domain: "a.example.com", upstream_port: 8080, ssl: true }], redirects: [] }} expose={[]} />);
    expect(screen.queryByRole("combobox", { name: "Send to" })).not.toBeInTheDocument();
    expect(screen.getByLabelText("Port")).toHaveValue(8080);
  });
});
