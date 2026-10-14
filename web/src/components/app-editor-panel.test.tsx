import type { ReactNode } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AppEditorPanel } from "@/components/app-editor-panel";
import { AppsContext, type AppDetailPayload, type AppsContextValue } from "@/context/apps-context-base";

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
  headerActions?: ReactNode,
  d: AppDetailPayload = detail,
  onOpenHistory?: () => void,
) {
  const value = {
    getApp: vi.fn(async () => d),
    saveApp,
    getPublicKey: vi.fn(async () => "K"),
    refresh,
  } as unknown as AppsContextValue;
  render(
    <MemoryRouter>
      <AppsContext.Provider value={value}>
        <AppEditorPanel appId="a1" headerActions={headerActions} onOpenHistory={onOpenHistory} />
      </AppsContext.Provider>
    </MemoryRouter>,
  );
  return refresh;
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

  it("puts the page's actions in its bar, before the Save buttons", async () => {
    renderPanel(vi.fn(), vi.fn(async () => {}), <button type="button">History</button>);
    const history = await screen.findByRole("button", { name: "History" });
    const save = screen.getByRole("button", { name: "Save & redeploy" });
    expect(history.compareDocumentPosition(save) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
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

  it("sends a nameless app to Settings to get a name", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn();
    const nameless = { ...detail, app: { ...detail.app, config: btoa(JSON.stringify({ files: [{ filename: "compose.yml" }] })) } };
    renderPanel(saveApp, vi.fn(async () => {}), undefined, nameless);
    await user.click(await screen.findByRole("button", { name: /Add variable/ }));
    await user.type(screen.getByRole("textbox", { name: "Variable name" }), "PORT");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    expect(screen.getByRole("alert")).toHaveTextContent("This app has no name yet.");
    expect(screen.getByRole("link", { name: "Name it in Settings" })).toHaveAttribute("href", "/app/a1?tab=settings");
    expect(saveApp).not.toHaveBeenCalled();
  });

  it("opens Advanced for a redirect without a target, names it in the bar, and keeps it open while typing", async () => {
    const user = userEvent.setup();
    servers = { activeServer: { id: "s1", features: { ingress: true } }, loading: false };
    const ingress = {
      domains: [{ domain: "shop.example.com", upstream_port: 8080, ssl: true }],
      redirects: [{ domain: "old.example.com", path: "", to: "", code: 301 }],
    };
    renderPanel(vi.fn(), vi.fn(async () => {}), undefined, withStored(ingress));
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
    renderPanel(saveApp, vi.fn(async () => {}), undefined, withStored(ingress));
    await user.click(await screen.findByRole("button", { name: /Add variable/ }));
    await user.type(screen.getByRole("textbox", { name: "Variable name" }), "PORT");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    await waitFor(() => expect(saveApp).toHaveBeenCalled());
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("keeps the bar to Save & redeploy and a ⋯ menu on a phone", async () => {
    vi.stubGlobal("matchMedia", () => ({ matches: true, addEventListener: () => {}, removeEventListener: () => {} }));
    const user = userEvent.setup();
    const onOpenHistory = vi.fn();
    const saveApp = vi.fn<AppsContextValue["saveApp"]>(async () => []);
    renderPanel(saveApp, vi.fn(async () => {}), <button type="button">History</button>, detail, onOpenHistory);
    await user.click(await screen.findByRole("button", { name: /Add variable/ }));
    await user.type(screen.getByRole("textbox", { name: "Variable name" }), "PORT");
    expect(screen.queryByRole("button", { name: "History" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save draft" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "More save options" }));
    await user.click(screen.getByRole("menuitem", { name: "Save draft" }));
    await waitFor(() => expect(saveApp).toHaveBeenCalledWith(expect.objectContaining({ draft: true })));
    await user.click(screen.getByRole("button", { name: "More save options" }));
    await user.click(screen.getByRole("menuitem", { name: "History" }));
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
});
