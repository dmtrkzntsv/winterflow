import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useParams } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { AppsContext, type App, type AppsContextValue } from "@/context/apps-context-base";
import { WaitTimeoutError } from "@/context/notifications-context-base";
import { COMPOSE_CREATE_WAIT_MS, GIT_CREATE_WAIT_MS, useCreateApp } from "@/hooks/use-create-app";
import { emptyEditorState, newGitSource } from "@/lib/app-editor-io";
import type { AppEditorState } from "@/types/app-config";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() } }));

const app = (id: string, name: string) => ({ id, name, serverId: "s1", version: "1", icon: "", color: "", createdAt: "" }) as App;

const named = (name: string, git = false): AppEditorState => {
  const s = emptyEditorState();
  return { ...s, config: { ...s.config, name, ...(git ? { source: { ...newGitSource(), repo_url: "https://x/y" } } : {}) } };
};

function Creator({ start }: { start: AppEditorState }) {
  const { create, busy, error, stillCreating } = useCreateApp("s1");
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

function setup(saveApp: SaveApp, start = named("shop")) {
  const reconcile = vi.fn(async () => [] as App[]);
  const value = (apps: App[]) => ({ apps, saveApp, getPublicKey: async () => "K", reconcile, noteDeployFailure: vi.fn() }) as unknown as AppsContextValue;
  const tree = (apps: App[], show = true) => (
    <MemoryRouter initialEntries={["/server/s1/new"]}>
      <AppsContext.Provider value={value(apps)}>
        <Routes>
          <Route path="/server/s1/new" element={show ? <Creator start={start} /> : <p>left</p>} />
          <Route path="/server/s1/app/:appId" element={<Landing />} />
        </Routes>
      </AppsContext.Provider>
    </MemoryRouter>
  );
  const view = render(tree([app("old", "blog")]));
  return { ...view, tree, reconcile };
}

describe("useCreateApp", () => {
  it("waits 5 minutes for a compose app and 10 for a Git one", async () => {
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
    expect(GIT_CREATE_WAIT_MS).toBe(600_000);
  });

  it("keeps a create that outlived its wait claimed until the app appears, then opens it", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn<SaveApp>(async () => Promise.reject(new WaitTimeoutError()));
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

  it("gives the name back when the form is left", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn<SaveApp>(async () => Promise.reject(new WaitTimeoutError()));
    const { rerender, tree } = setup(saveApp);
    await user.click(screen.getByRole("button", { name: "create" }));
    await screen.findByText("still shop");
    rerender(tree([], false));
    expect(screen.getByText("left")).toBeInTheDocument();
    rerender(tree([]));
    saveApp.mockResolvedValueOnce([app("n1", "shop")]);
    await user.click(screen.getByRole("button", { name: "create" }));
    await waitFor(() => expect(saveApp).toHaveBeenCalledTimes(2));
    await act(async () => {});
  });
});
