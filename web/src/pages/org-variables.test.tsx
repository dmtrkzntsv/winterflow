import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

vi.mock("@/lib/ecies", () => ({
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));
vi.mock("@/layouts/use-app-layout", () => ({ useAppBreadcrumbs: () => {} }));

import {
  NotificationsContext,
  type Notification,
  type NotificationHandler,
  type NotificationsContextValue,
} from "@/context/notifications-context-base";
import type { EncryptionKeys, OrgVariables, SharedVariablesApi } from "@/lib/shared-variables";
import OrgVariablesPage from "@/pages/org-variables";

const T = "2026-10-11T00:00:00Z";
const ORG: OrgVariables = {
  variables: [
    { id: "v1", name: "SMTP_HOST", scope: "org", is_secret: false, value: "mail", description: "relay", updated_at: T, updated_by: "u", missing_on: [] },
    { id: "v2", name: "SMTP_PASS", scope: "org", is_secret: true, value: "<encrypted>", description: "", updated_at: T, updated_by: "u", missing_on: [] },
    { id: "v3", name: "S3_KEY", scope: "org", is_secret: true, value: "<encrypted>", description: "", updated_at: T, updated_by: "u", missing_on: ["s3"] },
  ],
  servers: [
    { server_id: "s1", name: "alpha", has_key: true, revision: 4, agent_revision: 4, state: "synced" },
    { server_id: "s3", name: "gamma", has_key: true, revision: 4, agent_revision: 2, state: "pending" },
    { server_id: "s4", name: "new-box", has_key: false, revision: 4, agent_revision: null, state: "pending" },
  ],
};
const KEYS: EncryptionKeys = {
  servers: [
    { server_id: "s1", name: "alpha", public_key: "K1" },
    { server_id: "s3", name: "gamma", public_key: "K3" },
  ],
  pending_servers: [{ server_id: "s4", name: "new-box" }],
};

function fakeApi() {
  return {
    listOrg: vi.fn().mockResolvedValue(ORG),
    listServer: vi.fn(),
    keys: vi.fn().mockResolvedValue(KEYS),
    saveOrg: vi.fn().mockResolvedValue({}),
    saveServer: vi.fn(),
    deleteOrg: vi.fn().mockResolvedValue(undefined),
    deleteServer: vi.fn(),
    resync: vi.fn().mockResolvedValue(["s1", "s3", "s4"]),
    reload: vi.fn().mockResolvedValue(["s1"]),
  };
}

function renderPage(api: ReturnType<typeof fakeApi>) {
  const hub: { h?: NotificationHandler } = {};
  const notifications: NotificationsContextValue = {
    connected: true,
    subscribe: (h) => {
      hub.h = h;
      return () => {
        hub.h = undefined;
      };
    },
    waitFor: vi.fn(),
  };
  render(
    <MemoryRouter>
      <NotificationsContext.Provider value={notifications}>
        <OrgVariablesPage api={api as unknown as SharedVariablesApi} />
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
  return { emit: (n: Notification) => act(() => hub.h?.(n)) };
}

describe("OrgVariablesPage", () => {
  it("shows each secret's coverage and each server's sync state", async () => {
    const { emit } = renderPage(fakeApi());
    expect(await screen.findByText("All keyed servers; 1 pending (no key yet)")).toBeInTheDocument();
    expect(screen.getByText("Missing on gamma; 1 pending (no key yet)")).toBeInTheDocument();
    const servers = screen.getByRole("region", { name: "Servers" });
    expect(within(servers).getByText("alpha").closest("li")).toHaveTextContent("Synced");
    expect(within(servers).getByText("new-box").closest("li")).toHaveTextContent("No key yet");
    emit({ type: "vars.sync_failed", ref: "", payload: { server_id: "s3", error: "boom" } });
    expect(within(servers).getByText("gamma").closest("li")).toHaveTextContent("Sync failed");
  });

  it("provides a missing value for the missing server only", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    renderPage(api);
    await screen.findByText("Missing on gamma; 1 pending (no key yet)");
    await user.click(screen.getByRole("button", { name: "Provide value" }));
    expect(screen.getByText("Encrypts the value for: gamma")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Value"), "k3y");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(api.saveOrg).toHaveBeenCalledWith({ name: "S3_KEY", is_secret: true, description: "", ciphertexts: { s3: "ct(K3):k3y" } });
  });

  it("confirms where a new secret goes before pushing it", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    renderPage(api);
    await screen.findByText("All keyed servers; 1 pending (no key yet)");
    await user.click(screen.getByRole("button", { name: "Add variable" }));
    await user.type(screen.getByLabelText("Name"), "API_TOKEN");
    await user.click(screen.getByRole("switch", { name: "Secret" }));
    await user.type(screen.getByLabelText("Value"), "t0k");
    expect(screen.getByText("Will be pushed to 2 servers (1 pending, no key yet)")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save and push" }));
    expect(api.saveOrg).toHaveBeenCalledWith({
      name: "API_TOKEN", is_secret: true, description: "", ciphertexts: { s1: "ct(K1):t0k", s3: "ct(K3):t0k" },
    });
  });

  it("resyncs all servers or one variable, and reloads apps using a variable", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    renderPage(api);
    await screen.findByText("All keyed servers; 1 pending (no key yet)");
    await user.click(screen.getByRole("button", { name: "Resync all servers" }));
    expect(api.resync).toHaveBeenCalledWith({});
    await user.click(screen.getByRole("button", { name: "Resync SMTP_PASS" }));
    expect(api.resync).toHaveBeenCalledWith({ variable_id: "v2" });
    await user.click(screen.getByRole("button", { name: "Reload apps using SMTP_HOST" }));
    expect(api.reload).toHaveBeenCalledWith({ variable_id: "v1" });
  });

  it("deletes after confirmation", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    renderPage(api);
    await screen.findByText("All keyed servers; 1 pending (no key yet)");
    await user.click(screen.getByRole("button", { name: "Delete SMTP_HOST" }));
    await user.click(screen.getByRole("button", { name: "Delete" }));
    expect(api.deleteOrg).toHaveBeenCalledWith("v1");
  });
});
