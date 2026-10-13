import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { TemplateInstances } from "@/components/template-instances";
import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import type { InstanceView, TemplateView } from "@/lib/templates";
import { fakeAPI } from "@/test/fake-api";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
vi.mock("@/lib/ecies", () => ({
  ENCRYPTED: "<encrypted>",
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));
vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ servers: [{ id: "s1", name: "alpha" }, { id: "s2", name: "beta" }] }),
}));

const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.fn() } as NotificationsContextValue;

const VIEW: TemplateView = {
  id: "t1",
  name: "twillingate",
  latest_revision: 3,
  revision: 3,
  body: {
    files: { "compose.yaml": "services: {}\n" },
    parameters: [
      { name: "NAME", required: true },
      { name: "REGION", required: true },
      { name: "SESSION_KEY", secret: true, required: true },
    ],
    app_name: "${NAME}",
    icon: "",
    color: "",
    ingress: null,
    backups: null,
  },
  note: "",
  created_at: "2026-10-12T10:00:00Z",
  created_by: "u1",
  revisions: null,
};

const inst = (over: Partial<InstanceView>): InstanceView => ({
  key: "k1",
  server_id: "s1",
  app_id: "a1",
  template_id: "t1",
  template_name: "twillingate",
  revision: 2,
  parameters: { NAME: "bakomi" },
  secrets: { SESSION_KEY: "<encrypted>" },
  state: "created",
  status_code: 1,
  last_error: "",
  created_at: "2026-10-12T10:00:00Z",
  updated_at: "2026-10-12T10:00:00Z",
  ...over,
});

const onChanged = vi.fn();

function renderInstances(instances: InstanceView[]) {
  return render(
    <MemoryRouter>
      <NotificationsContext.Provider value={notifications}>
        <TemplateInstances view={VIEW} instances={instances} onChanged={onChanged} />
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  onChanged.mockReset();
  toast.success.mockReset();
});
afterEach(() => vi.unstubAllGlobals());

describe("TemplateInstances", () => {
  it("lists instances per server with revision and status", () => {
    renderInstances([
      inst({}),
      inst({ key: "k2", server_id: "s2", app_id: "a2", revision: 3, parameters: { NAME: "shop" }, state: "pending", last_error: "pull failed" }),
    ]);
    const rows = screen.getAllByRole("listitem");
    expect(within(rows[0]).getByRole("link", { name: "bakomi" })).toHaveAttribute("href", "/server/s1/app/a1");
    expect(rows[0]).toHaveTextContent("on alpha");
    expect(rows[0]).toHaveTextContent("r2 · behind");
    expect(within(rows[0]).getByText("Running")).toBeInTheDocument();
    expect(within(rows[1]).getByText("Not created yet")).toHaveAttribute("title", "pull failed");
    expect(within(rows[1]).queryByRole("button", { name: "Upgrade" })).not.toBeInTheDocument();
  });

  it("upgrades one instance, asking only for the newly required value", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({ "/api/v1/instance/upgrade-instance": { instance: inst({ revision: 3 }), request_id: "", warnings: [] } });
    renderInstances([inst({})]);
    await user.click(screen.getByRole("button", { name: "Upgrade" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).queryByLabelText("NAME")).not.toBeInTheDocument();
    expect(within(dialog).queryByLabelText("SESSION_KEY")).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Upgrade to r3" }));
    expect(within(dialog).getByText("Required")).toBeInTheDocument();
    expect(api.calls).toEqual([]);

    await user.type(within(dialog).getByLabelText("REGION"), "eu");
    await user.click(within(dialog).getByRole("button", { name: "Upgrade to r3" }));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    expect(api.posted("/api/v1/instance/upgrade-instance")).toEqual([
      { server_id: "s1", key: "k1", revision: 3, parameters: { REGION: "eu" } },
    ]);
    expect(toast.success).toHaveBeenCalledWith("Upgraded to r3");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("asks a pending instance for its secrets again and encrypts them for its server", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({
      "/api/v1/server/get-public-key": { public_key: "K2" },
      "/api/v1/instance/upgrade-instance": { instance: inst({}), request_id: "", warnings: [] },
    });
    renderInstances([inst({ server_id: "s2", state: "pending", parameters: { NAME: "x", REGION: "eu" } })]);
    await user.click(screen.getByRole("button", { name: "Upgrade" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("SESSION_KEY"), "hunter2");
    await user.click(within(dialog).getByRole("button", { name: "Upgrade to r3" }));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    expect(api.posted("/api/v1/instance/upgrade-instance")).toEqual([
      { server_id: "s2", key: "k1", revision: 3, secrets: { SESSION_KEY: "ct(K2):hunter2" } },
    ]);
    expect(JSON.stringify(api.calls)).not.toContain('"hunter2"');
  });

  it("keeps the dialog open with the reason when the server has no key", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({ "/api/v1/server/get-public-key": new Error("public key unavailable for this server") });
    renderInstances([inst({ secrets: null, parameters: { NAME: "x", REGION: "eu" } })]);
    await user.click(screen.getByRole("button", { name: "Upgrade" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("SESSION_KEY"), "hunter2");
    await user.click(within(dialog).getByRole("button", { name: "Upgrade to r3" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("This server has no encryption key yet");
    expect(api.posted("/api/v1/instance/upgrade-instance")).toEqual([]);
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("says when no app uses the template", () => {
    renderInstances([]);
    expect(screen.getByText(/No app uses this template yet/)).toBeInTheDocument();
  });
});
