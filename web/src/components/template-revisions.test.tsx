import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { TemplateRevisions } from "@/components/template-revisions";
import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import type { TemplateBody, TemplateView } from "@/lib/templates";
import { fakeAPI } from "@/test/fake-api";

const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.fn() } as NotificationsContextValue;

const body = (image: string, extra: Record<string, string> = {}): TemplateBody => ({
  files: { "compose.yaml": `services:\n  app:\n    image: ${image}\n`, ...extra },
  parameters: [{ name: "NAME", required: true }],
  app_name: "${NAME}",
  icon: "",
  color: "",
  ingress: null,
  backups: null,
});

const BODIES: Record<string, TemplateBody> = {
  "1": body("app:1"),
  "2": body("app:1", { Caddyfile: ":80" }),
  "3": { ...body("app:2", { Caddyfile: ":80" }), app_name: "shop-${NAME}" },
};

const VIEW: TemplateView = {
  id: "t1",
  name: "shop",
  latest_revision: 3,
  revision: 3,
  body: BODIES["3"],
  note: "",
  created_at: "2026-10-12T10:00:00Z",
  created_by: "u1",
  revisions: [3, 2, 1].map((revision) => ({
    revision,
    note: revision === 3 ? "bump image" : "",
    created_at: "2026-10-12T10:00:00Z",
    created_by: "u1",
  })),
};

function renderRevisions() {
  return render(
    <NotificationsContext.Provider value={notifications}>
      <TemplateRevisions view={VIEW} />
    </NotificationsContext.Provider>,
  );
}

afterEach(() => vi.unstubAllGlobals());

describe("TemplateRevisions", () => {
  it("shows each revision's changes against the one before", async () => {
    const user = userEvent.setup();
    fakeAPI({
      "/api/v1/template/get-template": (_: unknown, q: URLSearchParams) => ({ ...VIEW, body: BODIES[q.get("revision")!] }),
    });
    renderRevisions();
    expect(screen.getByText("bump image")).toBeInTheDocument();
    expect(screen.getByText("First version")).toBeInTheDocument();
    expect(screen.getByText("Version 3")).toBeInTheDocument();
    expect(screen.queryByText("r3")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Changes from version 2" }));
    const compose = await screen.findByRole("region", { name: "Changes to compose.yaml" });
    expect(within(compose).getByText("- image: app:1", { exact: false })).toBeInTheDocument();
    expect(within(compose).getByText("+ image: app:2", { exact: false })).toBeInTheDocument();
    const settings = screen.getByRole("region", { name: "Changes to template settings" });
    expect(settings).toHaveTextContent('+ "app_name": "shop-${NAME}",');

    await user.click(screen.getByRole("button", { name: "Changes from version 1" }));
    const added = await screen.findByRole("region", { name: "Changes to Caddyfile" });
    expect(added).toHaveTextContent("added");
    expect(screen.queryByRole("region", { name: "Changes to compose.yaml" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Changes from version 1" }));
    expect(screen.queryByRole("region", { name: "Changes to Caddyfile" })).not.toBeInTheDocument();
  });

  it("says when a revision changed nothing, and when it can't be read", async () => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/template/get-template": { ...VIEW, body: BODIES["1"] } });
    renderRevisions();
    await user.click(screen.getByRole("button", { name: "Changes from version 2" }));
    expect(await screen.findByText("No changes.")).toBeInTheDocument();
    vi.unstubAllGlobals();

    fakeAPI({ "/api/v1/template/get-template": new Error("template revision not found") });
    await user.click(screen.getByRole("button", { name: "Changes from version 1" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("template revision not found");
  });

  it("retries a failed load when the changes are opened again", async () => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/template/get-template": new Error("database down") });
    renderRevisions();
    const button = screen.getByRole("button", { name: "Changes from version 2" });
    await user.click(button);
    expect(await screen.findByRole("alert")).toHaveTextContent("database down");
    vi.unstubAllGlobals();

    fakeAPI({
      "/api/v1/template/get-template": (_: unknown, q: URLSearchParams) => ({ ...VIEW, body: BODIES[q.get("revision")!] }),
    });
    await user.click(button);
    await user.click(button);
    expect(await screen.findByRole("region", { name: "Changes to compose.yaml" })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
