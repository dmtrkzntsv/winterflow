import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppEditorPanel } from "@/components/app-editor-panel";
import { AppsContext, type AppDetailPayload, type AppsContextValue } from "@/context/apps-context-base";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServer: null, loading: true }) }));
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

function renderPanel(saveApp: AppsContextValue["saveApp"], refresh = vi.fn(async () => {})) {
  const value = {
    getApp: vi.fn(async () => detail),
    saveApp,
    getPublicKey: vi.fn(async () => "K"),
    refresh,
  } as unknown as AppsContextValue;
  render(
    <AppsContext.Provider value={value}>
      <AppEditorPanel appId="a1" />
    </AppsContext.Provider>,
  );
  return refresh;
}

describe("AppEditorPanel", () => {
  beforeEach(() => {
    toast.success.mockReset();
    toast.error.mockReset();
  });

  it("explains a save refused because a template took the app over, and re-reads the list", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn(async () => {
      throw new Error("this app is managed by template twillingate: update the instance or unlink it");
    });
    const refresh = renderPanel(saveApp);
    await user.type(await screen.findByLabelText("Name"), "-2");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("This app is managed by a template", {
        description: "Change it through its parameters, or detach it from the template in Settings first.",
      }),
    );
    expect(refresh).toHaveBeenCalled();
  });

  it("puts the page's actions in its bar, before the Save buttons", async () => {
    const value = {
      getApp: vi.fn(async () => detail),
      saveApp: vi.fn(),
      getPublicKey: vi.fn(async () => "K"),
      refresh: vi.fn(async () => {}),
    } as unknown as AppsContextValue;
    render(
      <AppsContext.Provider value={value}>
        <AppEditorPanel appId="a1" headerActions={<button type="button">History</button>} />
      </AppsContext.Provider>,
    );
    const history = await screen.findByRole("button", { name: "History" });
    const save = screen.getByRole("button", { name: "Save & redeploy" });
    expect(history.compareDocumentPosition(save) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("reports any other refusal as before", async () => {
    const user = userEvent.setup();
    const refresh = renderPanel(vi.fn(async () => Promise.reject(new Error("no storage nope"))));
    await user.type(await screen.findByLabelText("Name"), "-2");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Failed to update app", { description: "no storage nope" }));
    expect(refresh).not.toHaveBeenCalled();
  });
});
