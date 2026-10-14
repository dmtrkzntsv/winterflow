import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useParams } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ComposeFlow } from "@/components/new-app/compose-flow";
import { AppsContext, type App, type AppsContextValue } from "@/context/apps-context-base";
import { suggestAppLook } from "@/lib/app-look";
import { fakeAPI } from "@/test/fake-api";
import { stubViewport } from "@/test/media";
import { DEFAULT_COMPOSE } from "@/types/app-config";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
let server: { id: string; name: string; features?: Record<string, boolean> } = {
  id: "s1",
  name: "Alpha",
  features: { ingress: true },
};
vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ activeServer: server, activeServerId: server.id, loading: false, servers: [server] }),
}));
vi.mock("@/hooks/use-shared-names", () => ({ useSharedNames: () => [] }));
vi.mock("@/components/code-editor", () => ({
  CodeEditor: ({ value, onChange, filename }: { value: string; onChange?: (v: string) => void; filename?: string }) => (
    <textarea aria-label={`Content of ${filename || "file"}`} value={value} onChange={(e) => onChange?.(e.target.value)} />
  ),
}));
vi.mock("@/components/image-tag-picker", () => ({
  ImageTagPicker: ({ image }: { image: string }) => <button>tags of {image}</button>,
}));

const app = (id: string, name: string) => ({ id, name, serverId: "s1", version: "1", icon: "", color: "", createdAt: "" }) as App;

function Landing() {
  return <p>app {useParams().appId}</p>;
}

function renderFlow(saveApp: AppsContextValue["saveApp"]) {
  const value = {
    apps: [app("old", "shop")],
    saveApp,
    getPublicKey: vi.fn(async () => "K"),
  } as unknown as AppsContextValue;
  return render(
    <MemoryRouter initialEntries={["/server/s1/new?from=compose"]}>
      <AppsContext.Provider value={value}>
        <Routes>
          <Route path="/server/s1/new" element={<ComposeFlow serverId="s1" />} />
          <Route path="/server/s1/app/:appId" element={<Landing />} />
          <Route path="/server/s1" element={<p>the apps</p>} />
        </Routes>
      </AppsContext.Provider>
    </MemoryRouter>,
  );
}

// sent is the save-app body of the first call.
const sent = (saveApp: ReturnType<typeof vi.fn>) =>
  saveApp.mock.calls[0][0] as {
    app: Record<string, unknown>;
    config: Record<string, unknown>;
    files: unknown[];
    variables: unknown[];
    source?: unknown;
  };

beforeEach(() => {
  server = { id: "s1", name: "Alpha", features: { ingress: true } };
  toast.success.mockReset();
  toast.error.mockReset();
  fakeAPI({
    "/api/v1/domain/get-domains": { domains: [{ name: "example.com", tls: "auto" }] },
    "/api/v1/domains/check": { available: true, covered: true, registration: { name: "example.com", tls: "auto" }, claims: null },
  });
});
afterEach(() => vi.unstubAllGlobals());

describe("ComposeFlow", () => {
  it("creates the app from the prefilled compose file and opens the new one, not the old one of the same name", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn(async () => [app("old", "shop"), app("new1", "shop")]);
    renderFlow(saveApp);
    expect(screen.getByLabelText("Content of compose.yml")).toHaveValue(DEFAULT_COMPOSE);
    await user.type(screen.getByLabelText("Name"), "shop");
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(await screen.findByText("app new1")).toBeInTheDocument();
    const body = sent(saveApp);
    expect(body.app).toEqual({ name: "shop", ...suggestAppLook("shop") });
    expect(body.files).toEqual([{ name: "compose.yml", encrypted: false, content: DEFAULT_COMPOSE }]);
    expect(body.variables).toEqual([]);
    // The untouched Address row is not sent: no ingress at all, as before.
    expect(body.config).not.toHaveProperty("ingress");
    expect(body).not.toHaveProperty("source");
    expect(toast.success).toHaveBeenCalledWith("App created and deploying");
  });

  it("lands on the Apps page when the answer doesn't list the new app yet", async () => {
    const user = userEvent.setup();
    renderFlow(vi.fn(async () => [app("old", "shop")]));
    await user.type(screen.getByLabelText("Name"), "shop");
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(await screen.findByText("the apps")).toBeInTheDocument();
  });

  it("saves a filled Address row as the app's address", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn(async () => [app("new1", "web")]);
    renderFlow(saveApp);
    await user.type(screen.getByLabelText("Name"), "web");
    await user.type(screen.getByLabelText("Hostname"), "example.com");
    await user.type(screen.getByLabelText("Port"), "8088");
    await user.click(screen.getByRole("button", { name: "Create app" }));
    await screen.findByText("app new1");
    expect(sent(saveApp).config.ingress).toEqual({
      domains: [{ domain: "example.com", upstream_port: 8088, ssl: true }],
      redirects: [],
    });
  });

  it("says what is wrong next to each field, opening the closed section holding one", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn();
    renderFlow(saveApp);
    await user.click(screen.getByRole("button", { name: "Variables (0)" }));
    await user.click(screen.getByRole("button", { name: /Add variable/ }));
    await user.click(screen.getByRole("button", { name: "Variables (1)" }));
    expect(screen.queryByRole("textbox", { name: "Variable name" })).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("Hostname"), "example.com");

    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Some fields need attention.");
    expect(screen.getByLabelText("Name")).toHaveAccessibleDescription("Give the app a name.");
    expect(screen.getByRole("textbox", { name: "Variable name" })).toHaveAccessibleDescription("Name this variable.");
    expect(screen.getByLabelText("Port")).toHaveAccessibleDescription("Enter a port from 1 to 65535.");
    await waitFor(() => expect(screen.getByLabelText("Name")).toHaveFocus());
    expect(saveApp).not.toHaveBeenCalled();
    expect(toast.error).not.toHaveBeenCalled();

    // Fixing a field clears its message at once.
    await user.type(screen.getByLabelText("Name"), "shop");
    expect(screen.getByLabelText("Name")).not.toHaveAttribute("aria-invalid");
  });

  it("keeps a revealed variable open while its name is typed, so focus stays", async () => {
    const user = userEvent.setup();
    renderFlow(vi.fn());
    await user.type(screen.getByLabelText("Name"), "shop");
    await user.click(screen.getByRole("button", { name: "Variables (0)" }));
    await user.click(screen.getByRole("button", { name: /Add variable/ }));
    await user.click(screen.getByRole("button", { name: "Variables (1)" }));
    await user.click(screen.getByRole("button", { name: "Create app" }));
    const field = await screen.findByRole("textbox", { name: "Variable name" });
    await waitFor(() => expect(field).toHaveFocus());

    await user.keyboard("A");
    // The message went with the first letter; the section stays open.
    const typed = screen.getByRole("textbox", { name: "Variable name" });
    expect(typed).toHaveValue("A");
    expect(typed).toHaveFocus();
    expect(typed).not.toHaveAttribute("aria-invalid");
  });

  it("asks for a compose file when the last one is removed", async () => {
    const user = userEvent.setup();
    renderFlow(vi.fn());
    await user.type(screen.getByLabelText("Name"), "shop");
    await user.click(screen.getByRole("button", { name: "Remove compose.yml" }));
    await user.click(screen.getByRole("button", { name: "Create app" }));
    const message = screen.getByText("Add a compose file named compose.yml.");
    // No control to focus: the message itself takes focus.
    await waitFor(() => expect(message).toHaveFocus());
    await user.click(screen.getByRole("button", { name: "Add another file" }));
    expect(screen.getByRole("textbox", { name: "File name" })).toBeInTheDocument();
  });

  it("keeps the description, redirects and image versions under More options", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn(async () => [app("new1", "shop")]);
    renderFlow(saveApp);
    expect(screen.queryByLabelText("Description")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "More options" }));
    expect(screen.getByRole("button", { name: "tags of nginx:alpine" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Add redirect/ })).toBeInTheDocument();
    await user.type(screen.getByLabelText("Description"), "The shop");
    await user.type(screen.getByLabelText("Name"), "shop");
    await user.click(screen.getByRole("button", { name: "Create app" }));
    await screen.findByText("app new1");
    expect(sent(saveApp).config.description).toBe("The shop");
  });

  it("opens More options for a half-made redirect and keeps it open while it is fixed", async () => {
    const user = userEvent.setup();
    renderFlow(vi.fn());
    await user.type(screen.getByLabelText("Name"), "shop");
    await user.click(screen.getByRole("button", { name: "More options" }));
    await user.click(screen.getByRole("button", { name: /Add redirect/ }));
    await user.type(screen.getByLabelText("Redirect hostname"), "old.example.com");
    await user.click(screen.getByRole("button", { name: "More options" }));
    expect(screen.queryByLabelText("Redirect to")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(screen.getByLabelText("Redirect to")).toHaveAccessibleDescription(
      "Send it to a full address starting with http:// or https://.",
    );
    await waitFor(() => expect(screen.getByLabelText("Redirect to")).toHaveFocus());
    await user.keyboard("h");
    expect(screen.getByLabelText("Redirect to")).toHaveValue("h");
    expect(screen.getByLabelText("Redirect to")).toHaveFocus();
  });

  it("has no Address row on a server without the built-in proxy", async () => {
    server = { id: "s1", name: "Alpha", features: {} };
    const user = userEvent.setup();
    const saveApp = vi.fn(async () => [app("new1", "shop")]);
    const api = fakeAPI({});
    renderFlow(saveApp);
    expect(screen.queryByLabelText("Hostname")).not.toBeInTheDocument();
    expect(screen.getByText(/no built-in web proxy/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("Name"), "shop");
    await user.click(screen.getByRole("button", { name: "Create app" }));
    await screen.findByText("app new1");
    expect(sent(saveApp).config).not.toHaveProperty("ingress");
    expect(api.calls.filter((c) => c.path === "/api/v1/domain/get-domains")).toEqual([]);
  });

  it("keeps a failed create on the form, showing the agent's last line", async () => {
    const user = userEvent.setup();
    renderFlow(vi.fn(async () => Promise.reject(new Error("Container web  Creating\nError: port is already allocated\n"))));
    await user.type(screen.getByLabelText("Name"), "shop");
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(await screen.findByText("Error: port is already allocated")).toBeVisible();
    expect(screen.getByRole("button", { name: "Create app" })).toBeEnabled();
  });

  it("drops the last failure when the next attempt is refused for a field", async () => {
    const user = userEvent.setup();
    renderFlow(vi.fn(async () => Promise.reject(new Error("Error: port is already allocated"))));
    await user.type(screen.getByLabelText("Name"), "shop");
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(await screen.findByText("Error: port is already allocated")).toBeVisible();
    await user.clear(screen.getByLabelText("Name"));
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(screen.getByLabelText("Name")).toHaveAccessibleDescription("Give the app a name.");
    expect(screen.queryByText("Error: port is already allocated")).not.toBeInTheDocument();
  });

  it("keeps Create in reach at the bottom of a phone screen", () => {
    stubViewport(375);
    renderFlow(vi.fn());
    expect(screen.getByTestId("create-bar")).toHaveClass("sticky", "bottom-0", "sm:static");
  });

  it("goes back to the Apps page on Cancel", async () => {
    const user = userEvent.setup();
    renderFlow(vi.fn());
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.getByText("the apps")).toBeInTheDocument();
  });
});
