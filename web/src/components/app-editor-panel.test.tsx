import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AppEditorPanel } from "@/components/app-editor-panel";
import { AppsContext, type App, type AppDetailPayload, type AppsContextValue } from "@/context/apps-context-base";
import { forgetAppDetails, readAppDetail } from "@/hooks/use-app-detail";
import { flushPending } from "@/test/flush";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
let servers: { activeServer: { id: string; features?: Record<string, boolean> } | null; loading: boolean };
vi.mock("@/context/use-servers", () => ({ useServers: () => servers }));
vi.mock("@/hooks/use-shared-names", () => ({ useSharedNames: () => [] }));
vi.mock("@/components/code-editor", () => ({ CodeEditor: () => <textarea aria-label="code" /> }));
vi.mock("@/components/image-tag-picker", () => ({ ImageTagPicker: () => null }));

const detail: AppDetailPayload = {
  app: {
    app_id: "a1",
    config: btoa(JSON.stringify({ name: "shop", files: [{ filename: "compose.yml" }], variables: [] })),
    files: [{ name: "compose.yml", content: btoa("services: {}\n") }],
    variables: [],
  },
  revision: 1,
  available_revisions: null,
};

function renderPanel(
  saveApp: AppsContextValue["saveApp"],
  refresh = vi.fn(async () => {}),
  d: AppDetailPayload = detail,
  onOpenHistory?: () => void,
) {
  const value = {
    apps: [],
    getApp: vi.fn(async () => d),
    saveApp,
    getPublicKey: vi.fn(async () => "K"),
    refresh,
  } as unknown as AppsContextValue;
  render(
    <MemoryRouter>
      <AppsContext.Provider value={value}>
        <AppEditorPanel appId="a1" onOpenHistory={onOpenHistory} />
      </AppsContext.Provider>
    </MemoryRouter>,
  );
  return refresh;
}

// renderLive renders the panel with a reloadSignal the test can raise, and an
// app.get answer it can change (what a roll back in History does).
function renderLive(saveApp: AppsContextValue["saveApp"], first: AppDetailPayload = detail) {
  let current = first;
  const getApp = vi.fn(async () => current);
  const value = { apps: [], getApp, saveApp, getPublicKey: vi.fn(async () => "K"), refresh: vi.fn(async () => {}) } as unknown as AppsContextValue;
  const ui = (signal: number) => (
    <MemoryRouter>
      <AppsContext.Provider value={value}>
        <AppEditorPanel appId="a1" reloadSignal={signal} />
      </AppsContext.Provider>
    </MemoryRouter>
  );
  const view = render(ui(0));
  return {
    getApp,
    // rollBack is History changing the app to next.
    rollBack: (next: AppDetailPayload, signal: number) => {
      current = next;
      view.rerender(ui(signal));
    },
  };
}

function withVariable(name: string, value: string): AppDetailPayload {
  return {
    ...detail,
    app: {
      ...detail.app,
      config: btoa(JSON.stringify({ name: "shop", files: [{ filename: "compose.yml" }], variables: [{ name }] })),
      variables: [{ name, content: btoa(value) }],
    },
  };
}

// withStored is the loaded app with a stored ingress (a hostname without a
// port, which Save refuses).
function withStored(ingress: unknown, extra: Record<string, unknown> = {}): AppDetailPayload {
  return {
    ...detail,
    app: {
      ...detail.app,
      config: btoa(JSON.stringify({ name: "shop", files: [{ filename: "compose.yml" }], variables: [], ingress, ...extra })),
    },
  };
}

describe("AppEditorPanel", () => {
  beforeEach(() => {
    toast.success.mockReset();
    toast.error.mockReset();
    servers = { activeServer: null, loading: true };
  });
  afterEach(() => vi.unstubAllGlobals());

  it("shares the page's read of the listed version, and reads afresh after every save", async () => {
    const user = userEvent.setup();
    forgetAppDetails();
    let port = "8080";
    const getApp = vi.fn(async () => withVariable("PORT", port));
    // The page read version 3 already.
    await readAppDetail(getApp, "a1", "3");
    // The API stores no version after a save: the list says "" from then on.
    const saveApp = vi.fn<AppsContextValue["saveApp"]>(async (body) => {
      port = body.variables[0]?.content ?? port;
      return [{ id: "a1", version: "" } as App];
    });
    const value = { apps: [{ id: "a1", version: "3" }], getApp, saveApp, getPublicKey: vi.fn(async () => "K"), refresh: vi.fn() };
    render(
      <MemoryRouter>
        <AppsContext.Provider value={value as unknown as AppsContextValue}>
          <AppEditorPanel appId="a1" />
        </AppsContext.Provider>
      </MemoryRouter>,
    );
    const field = await screen.findByRole("textbox", { name: "Value of PORT" });
    expect(getApp).toHaveBeenCalledTimes(1);
    await user.type(field, "1");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    await waitFor(() => expect(getApp).toHaveBeenCalledTimes(2));
    expect(await screen.findByRole("textbox", { name: "Value of PORT" })).toHaveValue("80801");
    // A second save in the same visit reads afresh too, though the version
    // didn't move: the form shows what was saved, not the first save's.
    await user.type(screen.getByRole("textbox", { name: "Value of PORT" }), "2");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    await waitFor(() => expect(getApp).toHaveBeenCalledTimes(3));
    expect(await screen.findByRole("textbox", { name: "Value of PORT" })).toHaveValue("808012");
    // The page's read of an app without a version is never served from memory.
    await readAppDetail(getApp, "a1", "");
    expect(getApp).toHaveBeenCalledTimes(4);
    forgetAppDetails();
  });

  it("shares its fresh read after a save as the read of the version the list then holds", async () => {
    const user = userEvent.setup();
    forgetAppDetails();
    const getApp = vi.fn(async () => withVariable("PORT", "8080"));
    const saveApp = vi.fn<AppsContextValue["saveApp"]>(async () => [{ id: "a1", version: "4" } as App]);
    const value = { apps: [{ id: "a1", version: "3" }], getApp, saveApp, getPublicKey: vi.fn(async () => "K"), refresh: vi.fn() };
    render(
      <MemoryRouter>
        <AppsContext.Provider value={value as unknown as AppsContextValue}>
          <AppEditorPanel appId="a1" />
        </AppsContext.Provider>
      </MemoryRouter>,
    );
    await user.type(await screen.findByRole("textbox", { name: "Value of PORT" }), "1");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    await waitFor(() => expect(getApp).toHaveBeenCalledTimes(2));
    await readAppDetail(getApp, "a1", "4");
    expect(getApp).toHaveBeenCalledTimes(2);
    forgetAppDetails();
  });

  it("explains a save refused because a template took the app over, and re-reads the list", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn(async () => {
      throw new Error("this app is managed by template twillingate: update the instance or unlink it");
    });
    const refresh = renderPanel(saveApp);
    await user.click(await screen.findByRole("button", { name: /Add variable/ }));
    await user.type(screen.getByRole("textbox", { name: "Variable name" }), "PORT");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("This app is managed by a template", {
        description: "Change it through its parameters, or detach it from the template in Settings first.",
      }),
    );
    expect(refresh).toHaveBeenCalled();
  });

  it("is only History until something is edited, then a sticky bar of Discard, Save draft and Save & redeploy", async () => {
    const user = userEvent.setup();
    const onOpenHistory = vi.fn();
    renderPanel(vi.fn(), vi.fn(async () => {}), detail, onOpenHistory);
    const history = await screen.findByRole("button", { name: "History" });
    expect(screen.queryByRole("button", { name: "Save & redeploy" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Discard" })).not.toBeInTheDocument();
    expect(screen.queryByText(/Every save is kept in History/)).not.toBeInTheDocument();
    await user.click(history);
    expect(onOpenHistory).toHaveBeenCalled();
    await user.click(await screen.findByRole("button", { name: /Add variable/ }));
    await user.type(screen.getByRole("textbox", { name: "Variable name" }), "PORT");
    const bar = screen.getByText("Unsaved changes").closest(".sticky")!;
    expect(within(bar as HTMLElement).getAllByRole("button").map((b) => b.textContent)).toEqual([
      "Discard",
      "Save draft",
      "Save & redeploy",
    ]);
    await user.click(screen.getByRole("button", { name: "Discard" }));
    expect(await screen.findByRole("button", { name: "History" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save & redeploy" })).not.toBeInTheDocument();
  });

  it("refuses a nameless file next to it instead of in a toast", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn();
    renderPanel(saveApp);
    await user.click((await screen.findAllByRole("button", { name: /Add file/ }))[0]);
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    const names = screen.getAllByRole("textbox", { name: "File name" });
    expect(names[1]).toHaveAccessibleDescription("Name this file.");
    await waitFor(() => expect(names[1]).toHaveFocus());
    expect(screen.getByRole("alert")).toHaveTextContent("Some fields need attention.");
    expect(saveApp).not.toHaveBeenCalled();
    expect(toast.error).not.toHaveBeenCalled();
  });

  it("clears a field's message as it is fixed and the summary with the last one", async () => {
    const user = userEvent.setup();
    renderPanel(vi.fn());
    await user.click(await screen.findByRole("button", { name: /Add variable/ }));
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    const name = screen.getByRole("textbox", { name: "Variable name" });
    expect(name).toHaveAccessibleDescription("Name this variable.");
    await user.type(name, "P");
    expect(name).not.toHaveAttribute("aria-invalid");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("says a nameless app needs a name, focuses the message, and doesn't send the user away with unsaved edits", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn();
    const nameless = { ...detail, app: { ...detail.app, config: btoa(JSON.stringify({ files: [{ filename: "compose.yml" }] })) } };
    renderPanel(saveApp, vi.fn(async () => {}), nameless);
    await user.click(await screen.findByRole("button", { name: /Add variable/ }));
    await user.type(screen.getByRole("textbox", { name: "Variable name" }), "PORT");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("This app has no name yet.");
    // Settings would drop the edits just made: no link while they are unsaved.
    expect(alert).toHaveTextContent("Save or discard your changes first, then name it in Settings.");
    expect(screen.queryByRole("link", { name: "Name it in Settings" })).not.toBeInTheDocument();
    // Nothing to focus but the message itself.
    await waitFor(() => expect(alert).toHaveFocus());
    expect(saveApp).not.toHaveBeenCalled();
  });

  it("links a nameless app to Settings once nothing is unsaved", async () => {
    const user = userEvent.setup();
    const nameless = { ...detail, app: { ...detail.app, config: btoa(JSON.stringify({ files: [{ filename: "compose.yml" }] })) } };
    renderPanel(vi.fn(), vi.fn(async () => {}), nameless);
    await user.click(await screen.findByRole("button", { name: /Add variable/ }));
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    // The edit is an empty row: undo it by removing the row; the refusal stays.
    await user.click(screen.getByRole("button", { name: /Remove variable/ }));
    expect(screen.getByRole("link", { name: "Name it in Settings" })).toHaveAttribute("href", "/app/a1?tab=settings");
  });

  it("opens Advanced for a redirect without a target, names it in the bar, and keeps it open while typing", async () => {
    const user = userEvent.setup();
    servers = { activeServer: { id: "s1", features: { ingress: true } }, loading: false };
    const ingress = {
      domains: [{ domain: "shop.example.com", upstream_port: 8080, ssl: true }],
      redirects: [{ domain: "old.example.com", path: "", to: "", code: 301 }],
    };
    renderPanel(vi.fn(), vi.fn(async () => {}), withStored(ingress));
    // Dirty the form so Save is enabled; the redirect is inside the closed Advanced.
    await user.click(await screen.findByRole("button", { name: /Add variable/ }));
    await user.type(screen.getByRole("textbox", { name: "Variable name" }), "PORT");
    expect(screen.queryByLabelText("Redirect to")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    const to = await screen.findByLabelText("Redirect to");
    expect(to).toHaveAccessibleDescription("Send it to a full address starting with http:// or https://.");
    await waitFor(() => expect(to).toHaveFocus());
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Some fields need attention. Send it to a full address starting with http:// or https://.",
    );
    await user.type(to, "h");
    expect(screen.getByLabelText("Redirect to")).toHaveFocus();
  });

  it("doesn't block Save on a stored ingress when the server has no built-in proxy", async () => {
    const user = userEvent.setup();
    servers = { activeServer: { id: "s1", features: {} }, loading: false };
    const saveApp = vi.fn<AppsContextValue["saveApp"]>(async () => []);
    const ingress = { domains: [{ domain: "shop.example.com", upstream_port: 0, ssl: true }], redirects: [] };
    renderPanel(saveApp, vi.fn(async () => {}), withStored(ingress));
    await user.click(await screen.findByRole("button", { name: /Add variable/ }));
    await user.type(screen.getByRole("textbox", { name: "Variable name" }), "PORT");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    await waitFor(() => expect(saveApp).toHaveBeenCalled());
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("doesn't count a stored ingress against Save while the server is still unknown", async () => {
    const user = userEvent.setup();
    servers = { activeServer: null, loading: true };
    const saveApp = vi.fn<AppsContextValue["saveApp"]>(async () => []);
    const ingress = { domains: [{ domain: "shop.example.com", upstream_port: 0, ssl: true }], redirects: [] };
    renderPanel(saveApp, vi.fn(async () => {}), withStored(ingress));
    await user.click(await screen.findByRole("button", { name: /Add variable/ }));
    await user.type(screen.getByRole("textbox", { name: "Variable name" }), "PORT");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    await waitFor(() => expect(saveApp).toHaveBeenCalled());
  });

  it("keeps the bar to Save & redeploy and a ⋯ menu on a phone", async () => {
    vi.stubGlobal("matchMedia", () => ({ matches: true, addEventListener: () => {}, removeEventListener: () => {} }));
    const user = userEvent.setup();
    const onOpenHistory = vi.fn();
    const saveApp = vi.fn<AppsContextValue["saveApp"]>(async () => []);
    renderPanel(saveApp, vi.fn(async () => {}), detail, onOpenHistory);
    await user.click(await screen.findByRole("button", { name: /Add variable/ }));
    await user.type(screen.getByRole("textbox", { name: "Variable name" }), "PORT");
    expect(screen.queryByRole("button", { name: "History" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save draft" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "More save options" }));
    await user.click(screen.getByRole("menuitem", { name: "Save draft" }));
    await waitFor(() => expect(saveApp).toHaveBeenCalledWith(expect.objectContaining({ draft: true })));
    // Saved: nothing is edited, so the bar is History alone again.
    await user.click(await screen.findByRole("button", { name: "History" }));
    expect(onOpenHistory).toHaveBeenCalled();
  });

  it("reports any other refusal as before", async () => {
    const user = userEvent.setup();
    const refresh = renderPanel(vi.fn(async () => Promise.reject(new Error("no storage nope"))));
    await user.click(await screen.findByRole("button", { name: /Add variable/ }));
    await user.type(screen.getByRole("textbox", { name: "Variable name" }), "PORT");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Failed to update app", { description: "no storage nope" }));
    expect(refresh).not.toHaveBeenCalled();
  });

  it("reads the app again after History changed it, so the next Save can't undo a roll back", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn<AppsContextValue["saveApp"]>(async () => []);
    const live = renderLive(saveApp, withVariable("PORT", "8080"));
    expect(await screen.findByDisplayValue("8080")).toBeInTheDocument();

    live.rollBack(withVariable("PORT", "9090"), 1);
    expect(await screen.findByDisplayValue("9090")).toBeInTheDocument();
    expect(screen.queryByDisplayValue("8080")).not.toBeInTheDocument();
    expect(screen.queryByText(/This app changed since you opened it/)).not.toBeInTheDocument();

    // A save from here carries the rolled-back value.
    await user.type(screen.getByRole("textbox", { name: "Value of PORT" }), "1");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    await waitFor(() => expect(saveApp).toHaveBeenCalled());
    const sent = saveApp.mock.calls[0][0] as unknown as { variables: { name: string; content: string }[] };
    expect(sent.variables).toEqual([expect.objectContaining({ name: "PORT", content: "90901" })]);
  });

  it("keeps unsaved edits after History changed the app, says so, and blocks saving until Reload", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn<AppsContextValue["saveApp"]>(async () => []);
    const live = renderLive(saveApp, withVariable("PORT", "8080"));
    const value = await screen.findByRole("textbox", { name: "Value of PORT" });
    await user.type(value, "1");

    live.rollBack(withVariable("PORT", "9090"), 1);
    const notice = await screen.findByText(
      "This app changed since you opened it (in History or by an update). Reload to see the current version — your unsaved edits will be lost.",
    );
    // The edit is still there, not discarded under the user.
    expect(screen.getByRole("textbox", { name: "Value of PORT" })).toHaveValue("80801");
    expect(screen.getByRole("button", { name: "Save & redeploy" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Save draft" })).toBeDisabled();

    await user.click(within(notice.closest("[role=alert]") as HTMLElement).getByRole("button", { name: "Reload" }));
    expect(await screen.findByDisplayValue("9090")).toBeInTheDocument();
    expect(screen.queryByText(/This app changed since you opened it/)).not.toBeInTheDocument();
    expect(saveApp).not.toHaveBeenCalled();
  });

  it("never lets an older read overwrite a newer one", async () => {
    const answers: ((d: AppDetailPayload) => void)[] = [];
    const getApp = vi.fn(() => new Promise<AppDetailPayload>((resolve) => answers.push(resolve)));
    const value = { apps: [], getApp, saveApp: vi.fn(), getPublicKey: vi.fn(async () => "K"), refresh: vi.fn(async () => {}) } as unknown as AppsContextValue;
    const ui = (signal: number) => (
      <MemoryRouter>
        <AppsContext.Provider value={value}>
          <AppEditorPanel appId="a1" reloadSignal={signal} />
        </AppsContext.Provider>
      </MemoryRouter>
    );
    const view = render(ui(0));
    await waitFor(() => expect(answers).toHaveLength(1));
    view.rerender(ui(1)); // History changed the app while the first read ran
    await waitFor(() => expect(answers).toHaveLength(2));
    answers[1](withVariable("PORT", "9090"));
    expect(await screen.findByDisplayValue("9090")).toBeInTheDocument();
    answers[0](withVariable("PORT", "8080"));
    await flushPending();
    expect(screen.getByDisplayValue("9090")).toBeInTheDocument();
    expect(screen.queryByDisplayValue("8080")).not.toBeInTheDocument();
  });

  it("tells the page whether it holds unsaved edits, and that it holds none once gone", async () => {
    const user = userEvent.setup();
    const onDirtyChange = vi.fn();
    const value = {
      apps: [],
      getApp: vi.fn(async () => withVariable("PORT", "8080")),
      saveApp: vi.fn(),
      getPublicKey: vi.fn(async () => "K"),
      refresh: vi.fn(async () => {}),
    } as unknown as AppsContextValue;
    const view = render(
      <MemoryRouter>
        <AppsContext.Provider value={value}>
          <AppEditorPanel appId="a1" onDirtyChange={onDirtyChange} />
        </AppsContext.Provider>
      </MemoryRouter>,
    );
    await user.type(await screen.findByRole("textbox", { name: "Value of PORT" }), "1");
    expect(onDirtyChange).toHaveBeenLastCalledWith(true);
    view.unmount();
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);
  });

  it("clears the History notice when the edits are discarded", async () => {
    const user = userEvent.setup();
    const live = renderLive(vi.fn(), withVariable("PORT", "8080"));
    await user.type(await screen.findByRole("textbox", { name: "Value of PORT" }), "1");
    live.rollBack(withVariable("PORT", "9090"), 1);
    await screen.findByText(/This app changed since you opened it/);
    await user.click(screen.getByRole("button", { name: "Discard" }));
    expect(await screen.findByDisplayValue("9090")).toBeInTheDocument();
    expect(screen.queryByText(/This app changed since you opened it/)).not.toBeInTheDocument();
  });

  it("sends the stored ingress, name, icon, colour and description unchanged", async () => {
    const user = userEvent.setup();
    servers = { activeServer: { id: "s1", features: { ingress: true } }, loading: false };
    const saveApp = vi.fn<AppsContextValue["saveApp"]>(async () => []);
    const ingress = {
      domains: [{ domain: "shop.example.com", upstream_port: 8080, ssl: true }],
      redirects: [{ domain: "old.example.com", path: "", to: "https://shop.example.com", code: 301 }],
    };
    renderPanel(
      saveApp,
      vi.fn(async () => {}),
      withStored(ingress, { icon: "store", color: "#112233", description: "The shop" }),
    );
    await user.click(await screen.findByRole("button", { name: /Add variable/ }));
    await user.type(screen.getByRole("textbox", { name: "Variable name" }), "PORT");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    await waitFor(() => expect(saveApp).toHaveBeenCalled());
    const sent = saveApp.mock.calls[0][0] as unknown as { app: unknown; config: Record<string, unknown> };
    expect(sent.app).toEqual({ id: "a1", name: "shop", icon: "store", color: "#112233" });
    expect(sent.config).toMatchObject({ name: "shop", description: "The shop", icon: "store", color: "#112233" });
    expect(sent.config.ingress).toMatchObject({
      domains: [expect.objectContaining({ domain: "shop.example.com", upstream_port: 8080, ssl: true })],
      redirects: [expect.objectContaining({ domain: "old.example.com", to: "https://shop.example.com", code: 301 })],
    });
  });

  it("ends a refused Save draft from the phone ⋯ menu with focus on the first bad field", async () => {
    vi.stubGlobal("matchMedia", () => ({ matches: true, addEventListener: () => {}, removeEventListener: () => {} }));
    const user = userEvent.setup();
    const saveApp = vi.fn<AppsContextValue["saveApp"]>(async () => []);
    renderPanel(saveApp);
    await user.click(await screen.findByRole("button", { name: /Add variable/ }));
    await user.click(screen.getByRole("button", { name: "More save options" }));
    await user.click(screen.getByRole("menuitem", { name: "Save draft" }));
    const name = screen.getByRole("textbox", { name: "Variable name" });
    expect(name).toHaveAccessibleDescription("Name this variable.");
    await waitFor(() => expect(name).toHaveFocus());
    // The menu handed focus back to its button before: it must not take it again.
    await new Promise((r) => setTimeout(r, 50));
    expect(name).toHaveFocus();
    expect(saveApp).not.toHaveBeenCalled();
  });
});
