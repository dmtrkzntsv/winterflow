import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppSettingsTab } from "@/components/app-page/app-settings-tab";
import type { Ownership } from "@/components/app-page/ownership";
import { AppsContext, type App, type AppsContextValue } from "@/context/apps-context-base";
import { suggestAppLook } from "@/lib/app-look";
import type { InstanceView } from "@/lib/templates";
import { fakeNotifications } from "@/test/notifications";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

const APP: App = { id: "a1", serverId: "s1", name: "Shop", version: "3", icon: "", color: "#112233", createdAt: "" };
const detail = {
  app: {
    app_id: "a1",
    config: btoa(JSON.stringify({ name: "Shop", description: "Old", color: "#112233", files: [{ filename: "compose.yml" }], variables: [] })),
    files: [{ name: "compose.yml", content: btoa("services: {}\n") }],
    variables: [],
  },
  revision: 1,
  available_revisions: null,
};
const ctx = { rename: vi.fn(), getApp: vi.fn(), getRevisions: vi.fn(), getPublicKey: vi.fn(), saveApp: vi.fn(), refresh: vi.fn() };
const sse = fakeNotifications();

const INSTANCE = { template_id: "t1", template_name: "twillingate", state: "created", server_id: "s1", key: "k1" } as InstanceView;
const managed: Ownership = { loading: false, error: "", instance: INSTANCE, current: null, reload: vi.fn(async () => {}) };

function tab(app: App, over: { ownership?: Ownership; running?: boolean } = {}, props = { onDetached: vi.fn(), onSaveAsTemplate: vi.fn(), onDelete: vi.fn() }) {
  return (
    <sse.Provider>
      <AppsContext.Provider value={ctx as unknown as AppsContextValue}>
        <AppSettingsTab app={app} ownership={over.ownership ?? null} running={over.running ?? true} savingTemplate={false} {...props} />
      </AppsContext.Provider>
    </sse.Provider>
  );
}

function renderTab(over: { ownership?: Ownership; running?: boolean } = {}) {
  const props = { onDetached: vi.fn(), onSaveAsTemplate: vi.fn(), onDelete: vi.fn() };
  const view = render(tab(APP, over, props));
  return { ...props, unmount: view.unmount, rerender: (app: App) => view.rerender(tab(app, over, props)) };
}

// pickColour opens the icon and colour picker and sets a custom colour.
async function pickColour(user: ReturnType<typeof userEvent.setup>, colour: string) {
  await user.click(screen.getByRole("button", { name: "Change icon and colour" }));
  fireEvent.change(await screen.findByLabelText("Custom colour"), { target: { value: colour } });
  await user.keyboard("{Escape}");
}

describe("AppSettingsTab", () => {
  beforeEach(() => {
    for (const f of Object.values(ctx)) f.mockReset();
    for (const f of Object.values(toast)) f.mockReset();
    ctx.getApp.mockResolvedValue(detail);
    ctx.getRevisions.mockResolvedValue({ current: "b", deployed: "b", revisions: [] });
    ctx.getPublicKey.mockResolvedValue("K");
    ctx.saveApp.mockResolvedValue([]);
    ctx.rename.mockResolvedValue(undefined);
  });

  it("renames with one Save, without saving the app", async () => {
    const user = userEvent.setup();
    renderTab();
    expect(await screen.findByDisplayValue("Old")).toBeInTheDocument();
    const save = screen.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();
    await user.clear(screen.getByRole("textbox", { name: "App name" }));
    expect(save).toBeDisabled();
    await user.type(screen.getByRole("textbox", { name: "App name" }), "Store");
    await user.click(save);
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("App renamed"));
    expect(ctx.rename).toHaveBeenCalledWith("a1", "Store");
    expect(ctx.saveApp).not.toHaveBeenCalled();
  });

  it("does not count spaces around the name as a change", async () => {
    const user = userEvent.setup();
    renderTab();
    await screen.findByDisplayValue("Old");
    await user.type(screen.getByRole("textbox", { name: "App name" }), "  ");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("saves a new colour and description through the app's save, redeploying a running app", async () => {
    const user = userEvent.setup();
    renderTab();
    const description = await screen.findByDisplayValue("Old");
    await pickColour(user, "#ff0000");
    await user.clear(description);
    await user.type(description, "A small shop");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Saved"));
    const body = ctx.saveApp.mock.calls[0][0] as { draft: boolean; config: Record<string, unknown> };
    expect(body.draft).toBe(false);
    expect(body.config).toMatchObject({ color: "#ff0000", description: "A small shop" });
    expect(ctx.rename).not.toHaveBeenCalled();
  });

  it("is clean after a save even when the description can't be read back, so it never saves twice", async () => {
    const user = userEvent.setup();
    renderTab();
    const description = await screen.findByDisplayValue("Old");
    ctx.getApp.mockRejectedValue(new Error("agent offline"));
    await user.type(description, "!");
    ctx.getApp.mockResolvedValueOnce(detail);
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Saved"));
    expect(await screen.findByText(/Couldn't read the description/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(ctx.saveApp).toHaveBeenCalledTimes(1);
  });

  it("offers the same icon and colour picker as the create form, and picks automatically from the name", async () => {
    const user = userEvent.setup();
    renderTab();
    await screen.findByDisplayValue("Old");
    await user.click(screen.getByRole("button", { name: "Change icon and colour" }));
    await user.click(await screen.findByRole("button", { name: "Database" }));
    await user.click(screen.getByRole("button", { name: "Pick automatically from the name" }));
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Saved"));
    expect(ctx.saveApp.mock.calls[0][0]).toMatchObject({ config: suggestAppLook("Shop") });
  });

  it("keeps the user's colour when the rename went through and the rest failed to save", async () => {
    const user = userEvent.setup();
    const { rerender } = renderTab();
    await screen.findByDisplayValue("Old");
    // The rename lands and the list refreshes with it before the save fails.
    ctx.rename.mockImplementation(async () => rerender({ ...APP, name: "Store" }));
    ctx.saveApp.mockRejectedValueOnce(new Error("agent offline"));
    await user.clear(screen.getByRole("textbox", { name: "App name" }));
    await user.type(screen.getByRole("textbox", { name: "App name" }), "Store");
    await pickColour(user, "#ff0000");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Couldn't save", { description: "agent offline" }));
    await user.click(screen.getByRole("button", { name: "Change icon and colour" }));
    expect(await screen.findByLabelText("Custom colour")).toHaveValue("#ff0000");
    await user.keyboard("{Escape}");
    expect(screen.getByRole("textbox", { name: "App name" })).toHaveValue("Store");
    // Still unsaved: Save is offered again.
    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("keeps the change as a draft for a stopped app, and says when it goes live", async () => {
    const user = userEvent.setup();
    renderTab({ running: false });
    const description = await screen.findByDisplayValue("Old");
    await user.type(description, "!");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith("Saved", { description: "It goes live with the app's next deploy." }),
    );
    expect((ctx.saveApp.mock.calls[0][0] as { draft: boolean }).draft).toBe(true);
  });

  it("lets a template-managed app change only its name", async () => {
    const user = userEvent.setup();
    renderTab({ ownership: managed });
    expect(await screen.findByText("Icon, colour and description are set by template twillingate.")).toBeInTheDocument();
    expect(screen.getByLabelText("Description")).toBeDisabled();
    expect(screen.getByRole("button", { name: "Change icon and colour" })).toBeDisabled();
    await user.type(screen.getByRole("textbox", { name: "App name" }), "!");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(ctx.rename).toHaveBeenCalledWith("a1", "Shop!"));
    expect(ctx.saveApp).not.toHaveBeenCalled();
  });

  it("treats an app whose instance is gone as an ordinary app, but locks it while the instance is looked up", async () => {
    const gone: Ownership = { loading: false, error: "", instance: null, current: null, reload: vi.fn(async () => {}) };
    const { unmount } = renderTab({ ownership: gone });
    expect(await screen.findByDisplayValue("Old")).toBeEnabled();
    expect(screen.getByRole("button", { name: "Change icon and colour" })).toBeEnabled();
    expect(screen.queryByText(/are set by/)).not.toBeInTheDocument();
    // Its template section offers to start a template from it.
    expect(screen.getByRole("button", { name: "Save as template" })).toBeInTheDocument();
    unmount();
    renderTab({ ownership: { ...gone, loading: true } });
    await screen.findByDisplayValue("Old");
    expect(screen.getByRole("button", { name: "Change icon and colour" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Save as template" })).not.toBeInTheDocument();
  });

  it("says when the description can't be read, and still renames", async () => {
    const user = userEvent.setup();
    ctx.getApp.mockRejectedValueOnce(new Error("agent offline"));
    renderTab();
    expect(await screen.findByText(/Couldn't read the description: agent offline/)).toBeInTheDocument();
    expect(screen.getByLabelText("Description")).toBeDisabled();
    await user.type(screen.getByRole("textbox", { name: "App name" }), "!");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(ctx.rename).toHaveBeenCalledWith("a1", "Shop!"));
  });

  it("reports a refused save, and re-reads the list when a template took the app over", async () => {
    const user = userEvent.setup();
    ctx.saveApp.mockRejectedValueOnce(new Error("this app is managed by template twillingate: update the instance or unlink it"));
    renderTab();
    await user.type(await screen.findByDisplayValue("Old"), "!");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("This app is managed by a template", expect.anything()));
    expect(ctx.refresh).toHaveBeenCalled();
  });

  it("re-seeds the form when the app's saved name changes elsewhere", async () => {
    const { rerender } = renderTab();
    await screen.findByDisplayValue("Old");
    rerender({ ...APP, name: "Store" });
    expect(screen.getByRole("textbox", { name: "App name" })).toHaveValue("Store");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("starts a template from the app, and deletes it from the Danger zone", async () => {
    const user = userEvent.setup();
    const props = renderTab();
    await user.click(screen.getByRole("button", { name: "Save as template" }));
    expect(props.onSaveAsTemplate).toHaveBeenCalled();
    expect(screen.getByRole("region", { name: "Danger zone" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Delete app…" }));
    expect(props.onDelete).toHaveBeenCalled();
  });
});
