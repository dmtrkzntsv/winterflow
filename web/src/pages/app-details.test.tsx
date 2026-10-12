import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Outlet, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";
import AppDetailsPage from "@/pages/app-details";
import type { BackupHealth } from "@/lib/backups";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
vi.mock("@/components/app-backups", () => ({ AppBackups: () => <p>the backups tab</p> }));
vi.mock("@/components/app-metrics", () => ({ AppMetricBlocks: () => null, AppMetricsTab: () => null }));
// Links point into the active server's scope (/server/:id/...).
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));

const remove = vi.fn();

function renderPage(url: string, backupsByApp: Record<string, BackupHealth[]> = {}) {
  const value = {
    apps: [{ id: "a1", serverId: "s", name: "Shop", version: "3", icon: "", color: "", createdAt: "" }],
    statusByApp: { a1: 1 },
    backupsByApp,
    control: vi.fn(),
    remove,
    rename: vi.fn(),
  } as unknown as AppsContextValue;
  return render(
    <MemoryRouter initialEntries={[url]}>
      <AppsContext.Provider value={value}>
        <Routes>
          <Route element={<Outlet context={{ setBreadcrumbs: () => {} }} />}>
            <Route path="/app/:appId" element={<AppDetailsPage />} />
            <Route path="/server/srv-1" element={<p>the apps</p>} />
          </Route>
        </Routes>
      </AppsContext.Provider>
    </MemoryRouter>,
  );
}

describe("AppDetailsPage", () => {
  beforeEach(() => {
    remove.mockReset();
    for (const f of Object.values(toast)) f.mockReset();
  });

  it("marks the Backups tab when a backup is late or failing", async () => {
    renderPage("/app/a1?tab=backups", { a1: [{ name: "db", state: "failing", last_error: "disk full" }] });
    const tab = await screen.findByRole("tab", { name: /Backups/ });
    expect(tab.querySelector('[aria-label="A backup is late or failing"]')).not.toBeNull();
    expect(screen.getByText("the backups tab")).toBeInTheDocument();
  });

  it("has no mark while the backups are fine", async () => {
    renderPage("/app/a1?tab=backups", { a1: [{ name: "db", state: "ok" }] });
    const tab = await screen.findByRole("tab", { name: /Backups/ });
    expect(tab.querySelector('[aria-label="A backup is late or failing"]')).toBeNull();
  });

  it("shows progress while an app is deleted, and where its backups stayed", async () => {
    const user = userEvent.setup({ delay: null });
    let finish: (v: unknown) => void = () => {};
    remove.mockReturnValue(new Promise((resolve) => (finish = resolve)));
    renderPage("/app/a1?tab=settings", { a1: [{ name: "db", state: "ok" }] });
    await user.click(await screen.findByRole("button", { name: /Delete Shop/ }));
    await user.click(screen.getByRole("button", { name: "Delete app" }));
    expect(await screen.findByRole("button", { name: /Deleting Shop/ })).toBeDisabled();
    expect(screen.getByRole("status")).toHaveTextContent("This can take a few minutes");
    expect(remove).toHaveBeenCalledWith("a1", { deleteVolumes: false, deleteBackups: false });
    await act(async () => finish({ warnings: [], volumesDeleted: false, backupsDeleted: false }));
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith("App deleted", { description: expect.stringContaining("Other apps' backups") }),
    );
    // The deleted app's page is left for the list.
    expect(await screen.findByText("the apps")).toBeInTheDocument();
  });

  it("reports a failed delete and a delete with warnings", async () => {
    const user = userEvent.setup({ delay: null });
    remove.mockRejectedValueOnce(new Error("timed out"));
    renderPage("/app/a1?tab=settings");
    await user.click(await screen.findByRole("button", { name: /Delete Shop/ }));
    await user.click(screen.getByRole("button", { name: "Delete app" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Delete failed", { description: "timed out" }));
    remove.mockResolvedValueOnce({ warnings: ["volume x is in use"], volumesDeleted: false, backupsDeleted: false });
    await user.click(screen.getByRole("button", { name: /Delete Shop/ }));
    await user.click(screen.getByRole("button", { name: "Delete app" }));
    await waitFor(() => expect(toast.warning).toHaveBeenCalledWith("App deleted with cleanup warnings", { description: "volume x is in use" }));
  });
});
