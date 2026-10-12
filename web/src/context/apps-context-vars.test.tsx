import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AppsProvider } from "@/context/apps-context";
import { AuthContext, type AuthContextValue } from "@/context/auth-context-base";
import {
  NotificationsContext,
  type Notification,
  type NotificationHandler,
  type NotificationsContextValue,
} from "@/context/notifications-context-base";
import { ServersContext, type ServersContextValue } from "@/context/servers-context-base";
import { useApps } from "@/context/use-apps";

function Probe() {
  const { staleVarsByApp, reloadSharedVars } = useApps();
  const [err, setErr] = useState("");
  return (
    <>
      <pre data-testid="stale">{JSON.stringify(staleVarsByApp)}</pre>
      <button onClick={() => reloadSharedVars("a1").catch((e: Error) => setErr(e.message))}>reload</button>
      <p data-testid="err">{err}</p>
    </>
  );
}

function setup(reloadStatus = 200) {
  const handlers = new Set<NotificationHandler>();
  const notifications: NotificationsContextValue = {
    connected: true,
    subscribe: (h) => {
      handlers.add(h);
      return () => handlers.delete(h);
    },
    waitFor: vi.fn(),
  };
  const calls: { url: string; body?: string }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ url, body: init?.body as string | undefined });
      if (url.includes("/shared-variables/reload")) {
        if (reloadStatus !== 200) {
          return new Response(JSON.stringify({ success: false, message: "boom" }), { status: reloadStatus });
        }
        return new Response(JSON.stringify({ success: true, message: "ok", data: { servers: ["s1"] } }));
      }
      if (url.includes("get-apps-status")) {
        return new Response(
          JSON.stringify({ success: true, message: "", data: { apps: [{ app_id: "a1", status_code: 1, stale_vars: ["X"] }] } }),
        );
      }
      return new Response(JSON.stringify({ success: true, message: "", data: { apps: [] } }));
    }),
  );
  render(
    <AuthContext.Provider value={{ isAuthenticated: true } as AuthContextValue}>
      <ServersContext.Provider value={{ activeServerId: "s1" } as unknown as ServersContextValue}>
        <NotificationsContext.Provider value={notifications}>
          <AppsProvider>
            <Probe />
          </AppsProvider>
        </NotificationsContext.Provider>
      </ServersContext.Provider>
    </AuthContext.Provider>,
  );
  const emit = (n: Notification) => act(() => handlers.forEach((h) => h(n)));
  return { calls, emit };
}

const reloaded = (p: object): Notification => ({ type: "vars.reloaded", ref: "", payload: p });

describe("AppsProvider shared variable reload", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("posts the reload for one app and clears stale only on its own successful report", async () => {
    const { calls, emit } = setup();
    await waitFor(() => expect(screen.getByTestId("stale")).toHaveTextContent('"a1":["X"]'));

    await userEvent.click(screen.getByRole("button", { name: "reload" }));
    await waitFor(() => expect(calls.some((c) => c.url.includes("/shared-variables/reload"))).toBe(true));
    const call = calls.find((c) => c.url.includes("/shared-variables/reload"));
    expect(JSON.parse(call?.body ?? "{}")).toEqual({ server_id: "s1", app_ids: ["a1"] });

    emit(reloaded({ server_id: "s1", app_id: "a1", ok: false, error: "x", names: [] }));
    emit(reloaded({ server_id: "s1", app_id: "other", ok: true, names: [] }));
    emit(reloaded({ server_id: "s2", app_id: "a1", ok: true, names: [] }));
    expect(screen.getByTestId("stale")).toHaveTextContent('"a1":["X"]');

    emit(reloaded({ server_id: "s1", app_id: "a1", ok: true, names: ["X"] }));
    expect(screen.getByTestId("stale")).toHaveTextContent("{}");
  });

  it("surfaces a failed reload request", async () => {
    setup(500);
    await waitFor(() => expect(screen.getByTestId("stale")).toHaveTextContent('"a1"'));
    await userEvent.click(screen.getByRole("button", { name: "reload" }));
    await waitFor(() => expect(screen.getByTestId("err")).not.toBeEmptyDOMElement());
  });
});
