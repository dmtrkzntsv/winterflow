import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useParams } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppsContext, type App, type AppsContextValue } from "@/context/apps-context-base";
import { WaitTimeoutError } from "@/context/notifications-context-base";
import { COMPOSE_CREATE_WAIT_MS, GIT_CREATE_WAIT_MS, useCreateApp } from "@/hooks/use-create-app";
import { emptyEditorState, newGitSource } from "@/lib/app-editor-io";
import {
  HELD_CREATE_CAP_MS,
  claimCreate,
  onCreateExpiry,
  onUnattendedCreate,
  pendingCreates,
  resetHeldCreates,
  settleLateCreate,
} from "@/lib/held-creates";
import type { AppEditorState } from "@/types/app-config";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), info: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

// timeout is a create the agent didn't answer in time, with its request id.
const timeout = (ref = "r1") => {
  const e = new WaitTimeoutError();
  e.ref = ref;
  return e;
};

const app = (id: string, name: string) => ({ id, name, serverId: "s1", version: "1", icon: "", color: "", createdAt: "" }) as App;

const named = (name: string, git = false): AppEditorState => {
  const s = emptyEditorState();
  return { ...s, config: { ...s.config, name, ...(git ? { source: { ...newGitSource(), repo_url: "https://x/y" } } : {}) } };
};

function Creator({ start, holding = "" }: { start: AppEditorState; holding?: string }) {
  const { create, busy, error, stillCreating } = useCreateApp("s1", holding);
  return (
    <>
      <button onClick={() => void create(start)}>create</button>
      <p>busy {String(busy)}</p>
      <p>still {stillCreating || "-"}</p>
      <p>error {error || "-"}</p>
    </>
  );
}

function Landing() {
  return <p>app {useParams().appId}</p>;
}

type SaveApp = AppsContextValue["saveApp"];

function setup(saveApp: SaveApp, start = named("shop"), holding = "") {
  const reconcile = vi.fn(async () => [] as App[]);
  const noteDeployFailure = vi.fn();
  const value = (apps: App[]) => ({ apps, saveApp, getPublicKey: async () => "K", reconcile, noteDeployFailure }) as unknown as AppsContextValue;
  const tree = (apps: App[], show = true) => (
    <MemoryRouter initialEntries={["/server/s1/new"]}>
      <AppsContext.Provider value={value(apps)}>
        <Routes>
          <Route path="/server/s1/new" element={show ? <Creator start={start} holding={holding} /> : <p>left</p>} />
          <Route path="/server/s1/app/:appId" element={<Landing />} />
          <Route path="/server/s1" element={<p>apps</p>} />
        </Routes>
      </AppsContext.Provider>
    </MemoryRouter>
  );
  const view = render(tree([app("old", "blog")]));
  return { ...view, tree, reconcile, noteDeployFailure };
}

describe("useCreateApp", () => {
  beforeEach(() => {
    resetHeldCreates();
    toast.success.mockReset();
    toast.error.mockReset();
  });

  it("waits 5 minutes for a compose app and 9.5 for a Git one (below the API's 10-minute pending limit)", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn<SaveApp>(async () => [app("n1", "shop")]);
    const first = setup(saveApp);
    await user.click(screen.getByRole("button", { name: "create" }));
    await screen.findByText("app n1");
    expect(saveApp.mock.calls[0][1]).toEqual({ timeoutMs: COMPOSE_CREATE_WAIT_MS });
    first.unmount();

    const git = vi.fn<SaveApp>(async () => [app("n2", "repo")]);
    setup(git, named("repo", true));
    await user.click(screen.getByRole("button", { name: "create" }));
    await screen.findByText("app n2");
    expect(git.mock.calls[0][1]).toEqual({ timeoutMs: GIT_CREATE_WAIT_MS });
    expect(COMPOSE_CREATE_WAIT_MS).toBe(300_000);
    expect(GIT_CREATE_WAIT_MS).toBe(570_000);
  });

  it("keeps a create that outlived its wait claimed until the app appears, then opens it", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn<SaveApp>(async () => Promise.reject(timeout()));
    const { rerender, tree, reconcile } = setup(saveApp);
    await user.click(screen.getByRole("button", { name: "create" }));
    expect(await screen.findByText("still shop")).toBeInTheDocument();
    expect(screen.getByText("error -")).toBeInTheDocument();
    expect(screen.getByText("busy false")).toBeInTheDocument();
    // Its list may not be read (a standalone agent is still busy with it).
    expect(reconcile).not.toHaveBeenCalled();

    // A second Create of the same name sends nothing.
    await user.click(screen.getByRole("button", { name: "create" }));
    expect(saveApp).toHaveBeenCalledTimes(1);

    // Another app of that name (already listed before) is not it.
    rerender(tree([app("old", "blog"), app("other", "blog")]));
    expect(screen.getByText("still shop")).toBeInTheDocument();
    rerender(tree([app("old", "blog"), app("n1", "shop")]));
    expect(await screen.findByText("app n1")).toBeInTheDocument();
  });

  it("keeps every slow create's name claimed, not only the last", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn<SaveApp>(async () => Promise.reject(timeout()));
    function Two() {
      const { create, stillCreating } = useCreateApp("s1");
      return (
        <>
          <button onClick={() => void create(named("shop"))}>shop</button>
          <button onClick={() => void create(named("blog"))}>blog</button>
          <p>still {stillCreating || "-"}</p>
        </>
      );
    }
    const value = (apps: App[]) => ({ apps, saveApp, getPublicKey: async () => "K", reconcile: vi.fn(), noteDeployFailure: vi.fn() }) as unknown as AppsContextValue;
    const tree = (apps: App[]) => (
      <MemoryRouter initialEntries={["/server/s1/new"]}>
        <AppsContext.Provider value={value(apps)}>
          <Routes>
            <Route path="/server/s1/new" element={<Two />} />
            <Route path="/server/s1/app/:appId" element={<Landing />} />
          </Routes>
        </AppsContext.Provider>
      </MemoryRouter>
    );
    const { rerender } = render(tree([]));
    await user.click(screen.getByRole("button", { name: "shop" }));
    await screen.findByText("still shop");
    await user.click(screen.getByRole("button", { name: "blog" }));
    await screen.findByText("still blog");
    // Both stay claimed.
    await user.click(screen.getByRole("button", { name: "shop" }));
    await user.click(screen.getByRole("button", { name: "blog" }));
    expect(saveApp).toHaveBeenCalledTimes(2);
    // blog appears: shop is still being created, and its name still claimed.
    rerender(tree([app("b1", "blog")]));
    expect(await screen.findByText("app b1")).toBeInTheDocument();
  });

  it("keeps the name claimed after the form is left, and the reopened form opens the app when the late result comes", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn<SaveApp>(async () => Promise.reject(timeout("r7")));
    const { rerender, tree } = setup(saveApp);
    await user.click(screen.getByRole("button", { name: "create" }));
    await screen.findByText("still shop");
    rerender(tree([], false));
    expect(screen.getByText("left")).toBeInTheDocument();
    // Opened again: it still says so, and Create sends nothing.
    rerender(tree([]));
    expect(screen.getByText("still shop")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "create" }));
    expect(saveApp).toHaveBeenCalledTimes(1);
    // Another command's result is not it.
    act(() => settleLateCreate("other", { failed: false, error: "" }));
    expect(pendingCreates()).toHaveLength(1);
    act(() => settleLateCreate("r7", { failed: false, error: "", list: [app("old", "blog"), app("n1", "shop")] }));
    expect(await screen.findByText("app n1")).toBeInTheDocument();
    expect(toast.success).toHaveBeenCalledWith("App created");
    expect(pendingCreates()).toHaveLength(0);
  });

  it("explains a late failure on the reopened form, and frees Create", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn<SaveApp>(async () => Promise.reject(timeout("r8")));
    const { rerender, tree } = setup(saveApp);
    await user.click(screen.getByRole("button", { name: "create" }));
    await screen.findByText("still shop");
    rerender(tree([], false));
    rerender(tree([]));
    act(() => settleLateCreate("r8", { failed: true, error: "pull access denied" }));
    expect(await screen.findByText("error pull access denied")).toBeInTheDocument();
    expect(screen.getByText("still -")).toBeInTheDocument();
  });

  it("only toasts a late success on a form that holds another app's name now", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn<SaveApp>(async () => Promise.reject(timeout("r9")));
    const view = setup(saveApp);
    await user.click(screen.getByRole("button", { name: "create" }));
    await screen.findByText("still shop");
    view.unmount();
    setup(saveApp, named("blog"), "blog");
    act(() => settleLateCreate("r9", { failed: false, error: "", list: [app("n1", "shop")] }));
    expect(toast.success).toHaveBeenCalledWith("shop is ready", expect.objectContaining({ action: expect.objectContaining({ label: "Open" }) }));
    expect(screen.getByRole("button", { name: "create" })).toBeInTheDocument();
  });

  it("shows a create still in its first wait as still creating on a reopened form, which opens the app when it lands", async () => {
    const user = userEvent.setup();
    let answer: (list: App[]) => void = () => {};
    const saveApp = vi.fn<SaveApp>(() => new Promise<App[]>((resolve) => (answer = resolve)));
    const { rerender, tree } = setup(saveApp);
    await user.click(screen.getByRole("button", { name: "create" }));
    expect(await screen.findByText("busy true")).toBeInTheDocument();
    // The form that sent it says busy, not still creating.
    expect(screen.getByText("still -")).toBeInTheDocument();
    rerender(tree([], false));
    rerender(tree([]));
    expect(screen.getByText("still shop")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "create" }));
    expect(saveApp).toHaveBeenCalledTimes(1);
    await act(async () => answer([app("old", "blog"), app("n1", "shop")]));
    expect(await screen.findByText("app n1")).toBeInTheDocument();
    expect(pendingCreates()).toHaveLength(0);
  });

  it("doesn't open anything from a form that was left: the outcome goes to the shell", async () => {
    const user = userEvent.setup();
    let fail: (e: Error) => void = () => {};
    const saveApp = vi.fn<SaveApp>(() => new Promise<App[]>((_, reject) => (fail = reject)));
    const unattended = vi.fn();
    const { rerender, tree, reconcile } = setup(saveApp);
    onUnattendedCreate(unattended);
    await user.click(screen.getByRole("button", { name: "create" }));
    await screen.findByText("busy true");
    rerender(tree([], false));
    await act(async () => fail(new Error("refused: bad compose file")));
    expect(screen.getByText("left")).toBeInTheDocument();
    expect(unattended).toHaveBeenCalledWith(expect.objectContaining({ name: "shop" }), {
      failed: true,
      error: "refused: bad compose file",
      left: null,
    });
    expect(reconcile).not.toHaveBeenCalled();
  });

  it("hands a success that lands after the form was left to the shell", async () => {
    const user = userEvent.setup();
    let answer: (list: App[]) => void = () => {};
    const saveApp = vi.fn<SaveApp>(() => new Promise<App[]>((resolve) => (answer = resolve)));
    const unattended = vi.fn();
    const { rerender, tree } = setup(saveApp);
    onUnattendedCreate(unattended);
    await user.click(screen.getByRole("button", { name: "create" }));
    await screen.findByText("busy true");
    rerender(tree([], false));
    await act(async () => answer([app("n1", "shop")]));
    expect(screen.getByText("left")).toBeInTheDocument();
    expect(unattended).toHaveBeenCalledWith(expect.objectContaining({ name: "shop" }), { failed: false, error: "", list: [app("n1", "shop")] });
    expect(toast.success).not.toHaveBeenCalled();
  });

  it("doesn't open the app a late failure left when the form is left while the list is read", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn<SaveApp>(async () => Promise.reject(timeout("r6")));
    const unattended = vi.fn();
    const { rerender, tree, reconcile, noteDeployFailure } = setup(saveApp);
    onUnattendedCreate(unattended);
    await user.click(screen.getByRole("button", { name: "create" }));
    await screen.findByText("still shop");
    let listed: (list: App[]) => void = () => {};
    reconcile.mockImplementationOnce(() => new Promise<App[]>((resolve) => (listed = resolve)));
    act(() => settleLateCreate("r6", { failed: true, error: "port is already allocated" }));
    rerender(tree([], false));
    await act(async () => listed([app("old", "blog"), app("n1", "shop")]));
    expect(screen.getByText("left")).toBeInTheDocument();
    expect(noteDeployFailure).toHaveBeenCalledWith("n1", "port is already allocated");
    expect(unattended).toHaveBeenCalledWith(expect.objectContaining({ name: "shop" }), {
      failed: true,
      error: "port is already allocated",
      left: app("n1", "shop"),
    });
    expect(reconcile).toHaveBeenCalledTimes(1);
  });

  it("refuses a name another tab is creating", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn<SaveApp>(async () => [app("n1", "shop")]);
    setup(saveApp);
    act(() => void claimCreate("s1", "shop", new Set()));
    await user.click(screen.getByRole("button", { name: "create" }));
    expect(saveApp).not.toHaveBeenCalled();
    expect(screen.getByText("still shop")).toBeInTheDocument();
  });

  it("gives the name back after the cap when no result ever comes", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const saveApp = vi.fn<SaveApp>(async () => Promise.reject(timeout()));
      setup(saveApp);
      act(() => screen.getByRole("button", { name: "create" }).click());
      await screen.findByText("still shop");
      act(() => vi.advanceTimersByTime(HELD_CREATE_CAP_MS - 1000));
      expect(screen.getByText("still shop")).toBeInTheDocument();
      // The agent's list is read once before the name is freed.
      let listed: () => void = () => {};
      const reconcile = vi.fn(() => new Promise<void>((resolve) => (listed = resolve)));
      onCreateExpiry(reconcile);
      act(() => vi.advanceTimersByTime(1000));
      expect(reconcile).toHaveBeenCalledTimes(1);
      expect(screen.getByText("still shop")).toBeInTheDocument();
      await act(async () => listed());
      expect(screen.getByText("still -")).toBeInTheDocument();
      expect(pendingCreates()).toHaveLength(0);
    } finally {
      vi.useRealTimers();
    }
  });

  it("opens the app when a late result says the create went through and the list shows it", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn<SaveApp>(async () => Promise.reject(timeout("r2")));
    setup(saveApp);
    await user.click(screen.getByRole("button", { name: "create" }));
    await screen.findByText("still shop");
    // The list the provider read again shows it.
    act(() => settleLateCreate("r2", { failed: false, error: "", list: [app("old", "blog"), app("n1", "shop")] }));
    expect(await screen.findByText("app n1")).toBeInTheDocument();
    expect(toast.success).toHaveBeenCalledWith("App created");
  });

  it("says why when a late result says the create failed and left nothing, and frees Create", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn<SaveApp>(async () => Promise.reject(timeout("r3")));
    const { reconcile } = setup(saveApp);
    await user.click(screen.getByRole("button", { name: "create" }));
    await screen.findByText("still shop");
    act(() => settleLateCreate("r3", { failed: true, error: "no configuration file provided" }));
    expect(await screen.findByText("error no configuration file provided")).toBeInTheDocument();
    expect(screen.getByText("still -")).toBeInTheDocument();
    expect(reconcile).toHaveBeenCalled();
    saveApp.mockResolvedValueOnce([app("n1", "shop")]);
    await user.click(screen.getByRole("button", { name: "create" }));
    expect(await screen.findByText("app n1")).toBeInTheDocument();
  });

  it("still says why a late create failed when the agent's list can't be read", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn<SaveApp>(async () => Promise.reject(timeout("r5")));
    const { reconcile } = setup(saveApp);
    await user.click(screen.getByRole("button", { name: "create" }));
    await screen.findByText("still shop");
    reconcile.mockRejectedValueOnce(new Error("agent busy"));
    act(() => settleLateCreate("r5", { failed: true, error: "pull access denied" }));
    expect(await screen.findByText("error pull access denied")).toBeInTheDocument();
  });

  it("opens the app a late failure left behind with the failure noted, not as created", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn<SaveApp>(async () => Promise.reject(timeout("r4")));
    const { reconcile, noteDeployFailure } = setup(saveApp);
    await user.click(screen.getByRole("button", { name: "create" }));
    await screen.findByText("still shop");
    reconcile.mockResolvedValueOnce([app("old", "blog"), app("n1", "shop")]);
    act(() => settleLateCreate("r4", { failed: true, error: "port is already allocated" }));
    expect(await screen.findByText("app n1")).toBeInTheDocument();
    expect(noteDeployFailure).toHaveBeenCalledWith("n1", "port is already allocated");
    expect(toast.error).toHaveBeenCalledWith("The app was created but couldn't start", expect.anything());
    expect(toast.success).not.toHaveBeenCalled();
  });
});
