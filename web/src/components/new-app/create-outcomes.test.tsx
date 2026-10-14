import { act, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useParams } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { CreateOutcomes } from "@/components/new-app/create-outcomes";
import { AppsContext, type App, type AppsContextValue } from "@/context/apps-context-base";
import { HELD_CREATE_CAP_MS, claimCreate, deliverCreate, holdCreate, pendingCreates, settleLateCreate } from "@/lib/held-creates";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
let activeServerId = "s1";
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId }) }));

const app = (id: string, name: string) => ({ id, name, serverId: "s1", version: "1", icon: "", color: "", createdAt: "" }) as App;

function Landing() {
  return <p>app {useParams().appId}</p>;
}

function setup() {
  const reconcile = vi.fn(async () => [] as App[]);
  const noteDeployFailure = vi.fn();
  render(
    <MemoryRouter initialEntries={["/elsewhere"]}>
      <AppsContext.Provider value={{ reconcile, noteDeployFailure } as unknown as AppsContextValue}>
        <CreateOutcomes />
        <Routes>
          <Route path="/elsewhere" element={<p>elsewhere</p>} />
          <Route path="/server/s1/app/:appId" element={<Landing />} />
          <Route path="/server/:id" element={<p>apps</p>} />
        </Routes>
      </AppsContext.Provider>
    </MemoryRouter>,
  );
  return { reconcile, noteDeployFailure };
}

// held is a create of name on server that outlived its wait as ref.
function held(name: string, ref: string, server = "s1") {
  const c = claimCreate(server, name, new Set(["old"]))!;
  holdCreate(c.slot, ref);
  return c;
}

// openAction runs the Open action of the last toast.
function openAction(fn: typeof toast.success) {
  const opts = fn.mock.calls.at(-1)![1] as { action: { label: string; onClick: () => void } };
  expect(opts.action.label).toBe("Open");
  act(() => opts.action.onClick());
}

describe("CreateOutcomes", () => {
  beforeEach(() => {
    toast.success.mockReset();
    toast.error.mockReset();
    activeServerId = "s1";
  });

  it("says a create the user left is ready, and Open opens its app", () => {
    setup();
    held("notes", "r1");
    act(() => settleLateCreate("r1", { failed: false, error: "", list: [app("old", "notes"), app("n1", "notes")] }));
    expect(toast.success).toHaveBeenCalledWith("notes is ready", expect.anything());
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    openAction(toast.success);
    expect(screen.getByText("app n1")).toBeInTheDocument();
  });

  it("opens Apps when the list doesn't show the app yet", () => {
    setup();
    held("notes", "r1");
    act(() => settleLateCreate("r1", { failed: false, error: "", list: [] }));
    openAction(toast.success);
    expect(screen.getByText("apps")).toBeInTheDocument();
  });

  it("notes the failure on the app a late failure left, after reading the agent's list, and offers to open it", async () => {
    const { reconcile, noteDeployFailure } = setup();
    reconcile.mockResolvedValueOnce([app("old", "notes"), app("n1", "notes")]);
    held("notes", "r2");
    await act(async () => settleLateCreate("r2", { failed: true, error: "Error: port is already allocated" }));
    expect(reconcile).toHaveBeenCalledTimes(1);
    expect(noteDeployFailure).toHaveBeenCalledWith("n1", "Error: port is already allocated");
    expect(toast.error).toHaveBeenCalledWith("notes couldn't start", expect.objectContaining({ description: expect.stringContaining("port is already allocated") }));
    openAction(toast.error);
    expect(screen.getByText("app n1")).toBeInTheDocument();
  });

  it("says a late failure created nothing when the list holds no new app, or can't be read", async () => {
    const { reconcile, noteDeployFailure } = setup();
    reconcile.mockRejectedValueOnce(new Error("agent busy"));
    held("notes", "r3");
    await act(async () => settleLateCreate("r3", { failed: true, error: "pull access denied" }));
    expect(noteDeployFailure).not.toHaveBeenCalled();
    expect(toast.error).toHaveBeenCalledWith("notes couldn't be created", { description: "pull access denied" });
  });

  it("doesn't read the list again when the form already looked, nor another server's", async () => {
    activeServerId = "s2";
    const { reconcile } = setup();
    const c = claimCreate("s2", "notes", new Set())!;
    act(() => deliverCreate(c, { failed: true, error: "boom", left: null }));
    held("blog", "r4");
    await act(async () => settleLateCreate("r4", { failed: true, error: "boom" }));
    expect(reconcile).not.toHaveBeenCalled();
    expect(toast.error).toHaveBeenCalledTimes(2);
  });

  it("reads the agent's list once before a held create's name is freed at the cap", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const { reconcile } = setup();
      held("notes", "r5");
      await act(async () => vi.advanceTimersByTime(HELD_CREATE_CAP_MS));
      expect(reconcile).toHaveBeenCalledTimes(1);
      expect(pendingCreates()).toEqual([]);
    } finally {
      vi.useRealTimers();
    }
  });

  it("frees another server's held create at the cap without reading this one's list", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const { reconcile } = setup();
      held("notes", "r6", "s2");
      await act(async () => vi.advanceTimersByTime(HELD_CREATE_CAP_MS));
      expect(reconcile).not.toHaveBeenCalled();
      expect(pendingCreates()).toEqual([]);
    } finally {
      vi.useRealTimers();
    }
  });
});
