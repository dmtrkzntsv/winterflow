import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useParams } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { GitFlow } from "@/components/new-app/git-flow";
import { AppsContext, type App, type AppsContextValue } from "@/context/apps-context-base";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
vi.mock("@/lib/ecies", () => ({
  ENCRYPTED: "<encrypted>",
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));

const app = (id: string, name: string) => ({ id, name, serverId: "s1", version: "1", icon: "", color: "", createdAt: "" }) as App;

function Landing() {
  return <p>app {useParams().appId}</p>;
}

function renderFlow(
  saveApp = vi.fn<(body: unknown) => Promise<App[]>>(async () => [app("new1", "shop")]),
  getPublicKey = vi.fn(async () => "K"),
) {
  const value = { apps: [], saveApp, getPublicKey } as unknown as AppsContextValue;
  render(
    <MemoryRouter initialEntries={["/server/s1/new?from=git"]}>
      <AppsContext.Provider value={value}>
        <Routes>
          <Route path="/server/s1/new" element={<GitFlow serverId="s1" />} />
          <Route path="/server/s1/app/:appId" element={<Landing />} />
        </Routes>
      </AppsContext.Provider>
    </MemoryRouter>,
  );
  return { saveApp, getPublicKey };
}

describe("GitFlow", () => {
  it("creates an app that runs the repository's compose file on main, redeploying on new commits", async () => {
    const user = userEvent.setup();
    const { saveApp, getPublicKey } = renderFlow();
    await user.type(screen.getByLabelText("Name"), "shop");
    await user.type(screen.getByLabelText("Repository URL"), "https://github.com/acme/shop");
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(await screen.findByText("app new1")).toBeInTheDocument();
    const body = saveApp.mock.calls[0][0] as Record<string, Record<string, unknown> | unknown[]>;
    expect(body.files).toEqual([]);
    expect(body.source).toEqual({
      repo_url: "https://github.com/acme/shop",
      branch: "main",
      compose_path: "",
      auto_update: true,
      poll_seconds: 120,
      token: "",
    });
    expect((body.config as Record<string, unknown>).source).toEqual({
      repo_url: "https://github.com/acme/shop",
      branch: "main",
      compose_path: "",
      auto_update: true,
      poll_seconds: 120,
      token_set: false,
    });
    expect(getPublicKey).not.toHaveBeenCalled();
  });

  it("encrypts a private repository's token for the server", async () => {
    const user = userEvent.setup();
    const { saveApp } = renderFlow();
    await user.type(screen.getByLabelText("Name"), "shop");
    await user.type(screen.getByLabelText("Repository URL"), "https://github.com/acme/shop");
    await user.click(screen.getByRole("button", { name: "Private repository?" }));
    await user.type(screen.getByLabelText("Access token"), "tok");
    await user.click(screen.getByRole("button", { name: "Create app" }));
    await screen.findByText("app new1");
    const body = saveApp.mock.calls[0][0] as { source: { token: string }; config: { source: { token_set: boolean } } };
    expect(body.source.token).toBe("ct(K):tok");
    expect(body.config.source.token_set).toBe(true);
  });

  it("takes the compose path and auto-redeploy from Advanced", async () => {
    const user = userEvent.setup();
    const { saveApp } = renderFlow();
    await user.type(screen.getByLabelText("Name"), "shop");
    await user.type(screen.getByLabelText("Repository URL"), "https://github.com/acme/shop");
    await user.click(screen.getByRole("button", { name: "Advanced" }));
    await user.type(screen.getByLabelText("Compose file path"), "deploy/compose.yml");
    await user.click(screen.getByRole("checkbox", { name: /Redeploy automatically/ }));
    await user.click(screen.getByRole("button", { name: "Create app" }));
    await screen.findByText("app new1");
    expect(saveApp.mock.calls[0][0]).toMatchObject({ source: { compose_path: "deploy/compose.yml", auto_update: false } });
  });

  it("says what is missing next to the fields", async () => {
    const user = userEvent.setup();
    const { saveApp } = renderFlow();
    await user.type(screen.getByLabelText("Name"), "shop");
    await user.clear(screen.getByLabelText("Branch"));
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Some fields need attention.");
    expect(screen.getByLabelText("Repository URL")).toHaveAccessibleDescription("Enter the repository's URL.");
    expect(screen.getByLabelText("Branch")).toHaveAccessibleDescription("Enter a branch.");
    await waitFor(() => expect(screen.getByLabelText("Repository URL")).toHaveFocus());
    expect(saveApp).not.toHaveBeenCalled();

    await user.type(screen.getByLabelText("Repository URL"), "https://github.com/acme/shop");
    expect(screen.getByLabelText("Repository URL")).not.toHaveAttribute("aria-invalid");
    expect(screen.getByLabelText("Branch")).toHaveAttribute("aria-invalid", "true");
  });

  it("keeps a failed create on the form, showing the agent's last line", async () => {
    const user = userEvent.setup();
    renderFlow(vi.fn<(body: unknown) => Promise<App[]>>(async () => Promise.reject(new Error("clone failed\nfatal: repository not found\n"))));
    await user.type(screen.getByLabelText("Name"), "shop");
    await user.type(screen.getByLabelText("Repository URL"), "https://github.com/acme/none");
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(await screen.findByText("fatal: repository not found")).toBeVisible();
    expect(screen.getByRole("button", { name: "Create app" })).toBeEnabled();
  });
});
