import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { TemplateInstances } from "@/components/template-instances";
import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import type { InstanceView, TemplateView } from "@/lib/templates";
import { fakeAPI } from "@/test/fake-api";
import type { DeclaredVariable } from "@/types/app-config";

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

const v = (name: string, extra: Partial<DeclaredVariable> = {}): DeclaredVariable => ({
  name,
  required: false,
  encrypted: false,
  default: null,
  services: ["app"],
  ...extra,
});

const VIEW: TemplateView = {
  id: "t1",
  source: "org",
  slug: "",
  name: "twillingate",
  latest_revision: 3,
  withdrawn_at: null,
  revision: 3,
  body: { files: { "compose.yaml": "services: {}\n" }, summary: "", description: "", icon: "", color: "" },
  detected: {
    variables: [v("NAME", { required: true }), v("REGION", { required: true }), v("SESSION_KEY", { required: true, encrypted: true }), v("GREETING", { default: "Hiya" })],
    expose: [],
    backups: [],
    warnings: [],
  },
  note: "",
  created_at: "2026-10-12T10:00:00Z",
  created_by: "u1",
  revisions: null,
};

// R2 is the revision the instances run: GREETING's compose value changes in r3.
const R2: TemplateView = {
  ...VIEW,
  revision: 2,
  detected: {
    ...VIEW.detected,
    variables: [v("NAME", { required: true }), v("SESSION_KEY", { required: true, encrypted: true }), v("GREETING", { default: "Howdy" })],
  },
};
const R2_ROUTE = { "/api/v1/template/get-template": R2 };

const inst = (over: Partial<InstanceView>): InstanceView => ({
  key: "k1",
  server_id: "s1",
  app_id: "a1",
  template_id: "t1",
  template_name: "twillingate",
  revision: 2,
  name: "bakomi",
  values: { NAME: "bakomi" },
  secrets: { SESSION_KEY: "<encrypted>" },
  hosts: [],
  state: "created",
  status_code: 1,
  last_error: "",
  created_at: "2026-10-12T10:00:00Z",
  updated_at: "2026-10-12T10:00:00Z",
  ...over,
});

const onChanged = vi.fn();

function renderInstances(instances: InstanceView[], view = VIEW) {
  return render(
    <MemoryRouter>
      <NotificationsContext.Provider value={notifications}>
        <TemplateInstances view={view} instances={instances} onChanged={onChanged} />
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  onChanged.mockReset();
  toast.success.mockReset();
  toast.error.mockReset();
});
afterEach(() => vi.unstubAllGlobals());

describe("TemplateInstances", () => {
  it("uses the current catalog logo and labels an older catalog instance for upgrade", () => {
    const catalogView: TemplateView = {
      ...VIEW,
      source: "catalog",
      catalog: { summary: "Current summary", description: "Current description", keywords: [], tags: [], alternative_to: [], license: "MIT", homepage_url: "", repository_url: "", documentation_url: "", sponsorship: [], logo_url: "https://winterflow.io/catalog/logos/example.svg" },
    };
    renderInstances([inst({ revision: 2 })], catalogView);
    const row = screen.getByRole("listitem");
    expect(within(row).getByText(/Upgrade available/)).toBeInTheDocument();
    expect(within(row).getByRole("presentation")).toHaveAttribute("src", "https://winterflow.io/catalog/logos/example.svg");
  });
  it("lists the apps per server with their version and status", () => {
    renderInstances([
      inst({}),
      inst({ key: "k2", server_id: "s2", app_id: "a2", revision: 3, name: "shop", values: { NAME: "shop" }, state: "pending", last_error: "pull failed" }),
    ]);
    const rows = screen.getAllByRole("listitem");
    expect(within(rows[0]).getByRole("link", { name: "bakomi" })).toHaveAttribute("href", "/server/s1/app/a1");
    expect(rows[0]).toHaveTextContent("on alpha");
    expect(rows[0]).toHaveTextContent("Version 2 (latest is 3)");
    expect(rows[0]).not.toHaveTextContent(/\br2\b/);
    expect(within(rows[0]).getByText("Running").querySelector(".bg-status-ok")).not.toBeNull();
    expect(within(rows[1]).getByText("Not created yet").closest("[title]")).toHaveAttribute("title", "pull failed");
    expect(rows[1]).toHaveTextContent("Version 3");
    expect(within(rows[1]).queryByRole("button", { name: "Update" })).not.toBeInTheDocument();
  });

  it("shows a renamed app by its current name, the created one for an app no longer listed", async () => {
    fakeAPI({
      "/api/v1/app/get-apps": (_: unknown, q: URLSearchParams) =>
        q.get("server_id") === "s1" ? { apps: [{ id: "a1", name: "Family Notes" }] } : { apps: [] },
    });
    renderInstances([inst({}), inst({ key: "k2", server_id: "s2", app_id: "gone", name: "shop", values: { NAME: "shop" } })]);
    expect(await screen.findByRole("link", { name: "Family Notes" })).toHaveAttribute("href", "/server/s1/app/a1");
    expect(screen.queryByRole("link", { name: "bakomi" })).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "shop" })).toBeInTheDocument();
  });

  it("names a server it doesn't know as \"a server\", never by its id", () => {
    renderInstances([inst({ server_id: "0f8e2c41-gone" })]);
    expect(screen.getByRole("listitem")).toHaveTextContent("on a server");
    expect(screen.queryByText(/0f8e2c41/)).not.toBeInTheDocument();
  });

  it("upgrades one instance, asking only for the newly required value", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({
      ...R2_ROUTE,
      "/api/v1/instance/upgrade-instance": { instance: inst({ revision: 3 }), request_id: "", warnings: [] },
    });
    renderInstances([inst({})]);
    await user.click(screen.getByRole("button", { name: "Update" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).queryByLabelText("NAME")).not.toBeInTheDocument();
    expect(within(dialog).queryByLabelText("SESSION_KEY")).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Update to version 3" }));
    expect(within(dialog).getByText("Enter a value: this variable is required.")).toBeInTheDocument();
    expect(api.posted("/api/v1/instance/upgrade-instance")).toEqual([]);

    await user.type(within(dialog).getByLabelText("REGION"), "eu");
    await user.click(within(dialog).getByRole("button", { name: "Update to version 3" }));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    expect(api.posted("/api/v1/instance/upgrade-instance")).toEqual([
      { server_id: "s1", key: "k1", revision: 3, values: { REGION: "eu" } },
    ]);
    expect(toast.success).toHaveBeenCalledWith("Updated to version 3");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("names the variables left to the compose file whose value the upgrade changes", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(R2_ROUTE);
    renderInstances([inst({})]);
    await user.click(screen.getByRole("button", { name: "Update" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("heading")).toHaveTextContent("Update bakomi to version 3");
    expect(dialog).toHaveTextContent("Values you set stay; variables left to the compose file take its new value.");
    expect(dialog).toHaveTextContent("a backup the version adds starts off");
    const changes = await within(dialog).findByRole("list", { name: "Defaults that change" });
    expect(changes).toHaveTextContent("GREETING: Howdy → Hiya (follows the compose file)");
    expect(api.calls.find((c) => c.path === "/api/v1/template/get-template")?.query).toBe("?id=t1&revision=2");
    expect(dialog).not.toHaveTextContent("The app will be started.");
  });

  it("leaves out a default the instance overrides and says a stopped app starts", async () => {
    const user = userEvent.setup();
    fakeAPI(R2_ROUTE);
    renderInstances([inst({ status_code: 5, values: { NAME: "bakomi", GREETING: "Yo" } })]);
    await user.click(screen.getByRole("button", { name: "Update" }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("The app will be started.");
    await waitFor(() => expect(within(dialog).queryByText(/Checking/)).not.toBeInTheDocument());
    expect(within(dialog).queryByRole("list", { name: "Defaults that change" })).not.toBeInTheDocument();
  });

  it("says when the instance's revision can't be read to compare defaults", async () => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/template/get-template": new Error("boom") });
    renderInstances([inst({})]);
    await user.click(screen.getByRole("button", { name: "Update" }));
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByText(/Couldn't read version 2 to compare its defaults: boom/)).toBeInTheDocument();
  });

  it("asks a pending instance for its secrets again and encrypts them for its server", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({
      ...R2_ROUTE,
      "/api/v1/server/get-public-key": { public_key: "K2" },
      "/api/v1/instance/upgrade-instance": { instance: inst({}), request_id: "", warnings: [] },
    });
    renderInstances([inst({ server_id: "s2", state: "pending", values: { NAME: "x", REGION: "eu" } })]);
    await user.click(screen.getByRole("button", { name: "Update" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("SESSION_KEY"), "hunter2");
    await user.click(within(dialog).getByRole("button", { name: "Update to version 3" }));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    expect(api.posted("/api/v1/instance/upgrade-instance")).toEqual([
      { server_id: "s2", key: "k1", revision: 3, secrets: { SESSION_KEY: "ct(K2):hunter2" } },
    ]);
    expect(JSON.stringify(api.calls)).not.toContain('"hunter2"');
  });

  it("keeps the dialog open with the reason when the server has no key", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({ ...R2_ROUTE, "/api/v1/server/get-public-key": new Error("public key unavailable for this server") });
    renderInstances([inst({ secrets: null, values: { NAME: "x", REGION: "eu" } })]);
    await user.click(screen.getByRole("button", { name: "Update" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("SESSION_KEY"), "hunter2");
    await user.click(within(dialog).getByRole("button", { name: "Update to version 3" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("This server has no encryption key yet");
    expect(api.posted("/api/v1/instance/upgrade-instance")).toEqual([]);
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("shows a pending instance's failure and deletes it after confirming", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({ "/api/v1/instance/delete-instance": { instance: inst({ state: "deleted" }), request_id: "", warnings: [] } });
    renderInstances([inst({}), inst({ key: "k2", app_id: "a2", state: "pending", last_error: "pull access denied" })]);
    const rows = screen.getAllByRole("listitem");
    expect(within(rows[0]).queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
    // The failure reads at body size, not as small red text.
    expect(within(rows[1]).getByText("pull access denied").closest(".text-sm")).not.toBeNull();
    await user.click(within(rows[1]).getByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("Its deploy never succeeded. Deleting removes whatever it left on alpha.");
    expect(dialog).not.toHaveTextContent(/key/i);
    // Only the apps' current names were read so far.
    expect(api.calls.map((c) => c.path)).toEqual(["/api/v1/app/get-apps"]);
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    expect(api.posted("/api/v1/instance/delete-instance")).toEqual([{ server_id: "s1", key: "k2" }]);
    expect(toast.success).toHaveBeenCalledWith("Deleted bakomi");
  });

  it("shows a long failure by its last line, the rest behind Details", () => {
    renderInstances([inst({ state: "pending", last_error: "Container web  Creating\nError: pull access denied\n" })]);
    const row = screen.getByRole("listitem");
    expect(within(row).getByText("Error: pull access denied")).toBeVisible();
    expect(within(row).getByText("Details")).toBeInTheDocument();
    expect(within(row).getByText(/Container web Creating/)).not.toBeVisible();
  });

  it("reports a failed delete of a pending instance and keeps the confirm open to retry", async () => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/instance/delete-instance": new Error("server offline") });
    renderInstances([inst({ state: "pending" })]);
    await user.click(screen.getByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Couldn't delete bakomi", { description: "server offline" }));
    expect(onChanged).not.toHaveBeenCalled();
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Delete" })).toBeEnabled();
  });

  it("says when no app uses the template", () => {
    renderInstances([]);
    expect(screen.getByText(/No app uses this template yet/)).toBeInTheDocument();
  });

  it("updates every created app on an older version after a confirm, not while a rollout runs", async () => {
    const user = userEvent.setup();
    const onUpgradeAll = vi.fn();
    const list = [inst({}), inst({ key: "k2", app_id: "a2", revision: 3 }), inst({ key: "k3", app_id: "a3", state: "pending", revision: 1 })];
    const tree = (active: boolean, instances = list) => (
      <MemoryRouter>
        <NotificationsContext.Provider value={notifications}>
          <TemplateInstances view={VIEW} instances={instances} onChanged={onChanged} onUpgradeAll={onUpgradeAll} rolloutActive={active} />
        </NotificationsContext.Provider>
      </MemoryRouter>
    );
    const { rerender } = render(tree(false));
    // Only k1 counts: k2 is on version 3, k3 was never created.
    expect(screen.getByText("1 app uses an older version. The latest is version 3.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Update all apps" }));
    const dialog = screen.getByRole("alertdialog");
    expect(dialog).toHaveTextContent("Update 1 app to version 3?");
    expect(dialog).toHaveTextContent(/stopped apps will be started/i);
    await user.click(within(dialog).getByRole("button", { name: "Update all" }));
    expect(onUpgradeAll).toHaveBeenCalledTimes(1);

    rerender(tree(true));
    expect(screen.getByRole("button", { name: "Update all apps" })).toBeDisabled();
    rerender(tree(false, [inst({ revision: 3 })]));
    expect(screen.queryByRole("button", { name: /Update all/ })).not.toBeInTheDocument();
  });
});
