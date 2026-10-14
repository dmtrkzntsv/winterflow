import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  ProfileContext,
  type ProfileContextValue,
} from "@/context/profile-context-base";
import OrgMembersPage from "@/pages/org-members";

const ME = {
  id: "u-me",
  name: "Ann",
  email: "ann@example.test",
  provider: "local",
  last_seen_at: "2026-10-10T12:00:00Z",
  created_at: "2026-10-01T12:00:00Z",
};
const BOB = { ...ME, id: "u-bob", name: "Bob", email: "bob@example.test" };

type Call = { path: string; body: unknown };

// fakeAPI answers the page's fetches by path and records every call.
function fakeAPI() {
  const calls: Call[] = [];
  const data: Record<string, unknown> = {
    "/api/v1/org/get-members": [ME, BOB],
    "/api/v1/org/get-organization": { org_id: "o-1", name: "Homelab", icon: "", color: "#64748b" },
    "/api/v1/org/create-user": { user_id: "u-new", email: "cy@example.test", temp_password: "TEMP-PASS-123456" },
    "/api/v1/org/remove-member": null,
    "/api/v1/org/update-organization": null,
  };
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    const path = new URL(url, "http://x").pathname;
    calls.push({ path, body: init?.body ? JSON.parse(String(init.body)) : undefined });
    if (!(path in data)) {
      return new Response(JSON.stringify({ success: false, message: "no route" }), { status: 404 });
    }
    return new Response(JSON.stringify({ success: true, message: "ok", data: data[path] }));
  });
  return { calls, fetchMock };
}

function renderPage(section: "organization" | "members") {
  const profile = {
    profile: { user_id: ME.id, name: ME.name, email: ME.email, must_change_password: false },
  } as ProfileContextValue;
  return render(
    <ProfileContext.Provider value={profile}>
      <OrgMembersPage section={section} />
    </ProfileContext.Provider>,
  );
}

describe("OrgMembersPage", () => {
  let api: ReturnType<typeof fakeAPI>;

  beforeEach(() => {
    api = fakeAPI();
    vi.stubGlobal("fetch", api.fetchMock);
    vi.spyOn(window, "confirm").mockReturnValue(true);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("lists members without roles and lets any member manage them", async () => {
    renderPage("members");

    const bobRow = (await screen.findByText("Bob")).closest("tr")!;
    expect(screen.queryByRole("columnheader", { name: "Role" })).not.toBeInTheDocument();
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
    // Everyone else can be reset and removed; you can't remove yourself.
    expect(within(bobRow).getByRole("button", { name: "Reset password" })).toBeInTheDocument();
    expect(within(bobRow).getByRole("button", { name: "Remove" })).toBeInTheDocument();
    const meRow = screen.getByText("(you)").closest("tr")!;
    expect(within(meRow).queryByRole("button", { name: "Remove" })).not.toBeInTheDocument();
  });

  it("shows only the organization as Settings → Organization", async () => {
    renderPage("organization");
    expect(screen.getByRole("heading", { level: 1, name: "Organization" })).toBeInTheDocument();
    expect(screen.getAllByRole("heading", { name: "Organization" })).toHaveLength(1);
    expect(await screen.findByText("Homelab")).toBeInTheDocument();
    // The organization card is editable by every member.
    expect(screen.getByRole("button", { name: /Edit/ })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Add user/ })).not.toBeInTheDocument();
  });

  it("shows only the members as Settings → Members", async () => {
    renderPage("members");
    expect(screen.getByRole("heading", { level: 1, name: "Members" })).toBeInTheDocument();
    expect(screen.getAllByText("Members")).toHaveLength(1);
    expect(await screen.findByText("Bob")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Edit/ })).not.toBeInTheDocument();
  });

  it("creates a user without a role", async () => {
    const user = userEvent.setup();
    renderPage("members");

    await user.click(await screen.findByRole("button", { name: /Add user/ }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).queryByText("Role")).not.toBeInTheDocument();
    await user.type(within(dialog).getByLabelText("Name"), "Cy");
    await user.type(within(dialog).getByLabelText("Email"), "cy@example.test");
    await user.click(within(dialog).getByRole("button", { name: "Create" }));

    expect(await screen.findByText("User created")).toBeInTheDocument();
    expect(screen.getByDisplayValue("TEMP-PASS-123456")).toBeInTheDocument();
    const created = api.calls.find((c) => c.path === "/api/v1/org/create-user");
    expect(created?.body).toEqual({ name: "Cy", email: "cy@example.test" });
    expect(api.calls.some((c) => c.path === "/api/v1/org/update-member")).toBe(false);
  });

  it("removes another member after confirmation", async () => {
    const user = userEvent.setup();
    renderPage("members");

    const bobRow = (await screen.findByText("Bob")).closest("tr")!;
    await user.click(within(bobRow).getByRole("button", { name: "Remove" }));

    expect(window.confirm).toHaveBeenCalled();
    const removed = api.calls.find((c) => c.path === "/api/v1/org/remove-member");
    expect(removed?.body).toEqual({ user_id: "u-bob" });
  });

  it("edits the organization", async () => {
    const user = userEvent.setup();
    renderPage("organization");

    await screen.findByText("Homelab");
    await user.click(screen.getByRole("button", { name: /Edit/ }));
    const dialog = await screen.findByRole("dialog");
    await user.clear(within(dialog).getByLabelText("Name"));
    await user.type(within(dialog).getByLabelText("Name"), "Lab");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    const saved = api.calls.find((c) => c.path === "/api/v1/org/update-organization");
    expect(saved?.body).toEqual({ name: "Lab", icon: "", color: "#64748b" });
  });
});
