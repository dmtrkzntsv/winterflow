import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ProfileContext, type ProfileContextValue } from "@/context/profile-context-base";
import OrgMembersPage from "@/pages/org-members";
import { fakeAPI } from "@/test/fake-api";

const toastError = vi.hoisted(() => vi.fn());
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: toastError } }));

const ME = {
  id: "u-me",
  name: "Ann Lee",
  email: "ann@example.test",
  provider: "local",
  last_seen_at: "2026-10-10T12:00:00Z",
  created_at: "2026-10-01T12:00:00Z",
};
const BOB = { ...ME, id: "u-bob", name: "Bob", email: "bob@example.test" };

function routes(over: Record<string, unknown> = {}) {
  return fakeAPI({
    "/api/v1/org/get-members": [ME, BOB],
    "/api/v1/org/get-organization": { org_id: "o-1", name: "Homelab", icon: "", color: "#64748b" },
    "/api/v1/org/create-user": { user_id: "u-new", email: "cy@example.test", temp_password: "TEMP-PASS-123456" },
    "/api/v1/org/reset-member-password": { temp_password: "RESET-PASS-1" },
    "/api/v1/org/remove-member": null,
    "/api/v1/org/update-organization": null,
    ...over,
  });
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

const setupUser = () => userEvent.setup({ delay: null });

describe("OrgMembersPage", () => {
  beforeEach(() => {
    toastError.mockReset();
    vi.spyOn(window, "confirm");
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("edits the organization's name, icon and colour in place, without its id", async () => {
    const user = setupUser();
    const api = routes();
    renderPage("organization");
    expect(screen.getByRole("heading", { level: 1, name: "Organization" })).toBeInTheDocument();
    const name = await screen.findByLabelText("Name");
    expect(name).toHaveValue("Homelab");
    expect(screen.queryByText("o-1")).not.toBeInTheDocument();
    const save = screen.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();
    await user.clear(name);
    expect(screen.getByRole("alert")).toHaveTextContent("Give the organization a name.");
    await user.type(name, "Lab");
    await user.click(save);
    expect(api.posted("/api/v1/org/update-organization")).toEqual([{ name: "Lab", icon: "", color: "#64748b" }]);
    expect(screen.queryByRole("button", { name: /Add member/ })).not.toBeInTheDocument();
  });

  it("lists members as rows, you included, with a menu for everyone else", async () => {
    routes();
    renderPage("members");
    expect(screen.getByRole("heading", { level: 1, name: "Members" })).toBeInTheDocument();
    expect(await screen.findByText("Bob")).toBeInTheDocument();
    expect(screen.getByText("(you)")).toBeInTheDocument();
    expect(screen.getByText("AL")).toBeInTheDocument();
    expect(screen.getByText("bob@example.test")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Actions for Bob" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Actions for Ann Lee" })).not.toBeInTheDocument();
    expect(screen.queryByRole("columnheader")).not.toBeInTheDocument();
  });

  it("adds a member and hands over the temporary password", async () => {
    const user = setupUser();
    const api = routes();
    renderPage("members");
    await user.click(await screen.findByRole("button", { name: /Add member/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Add" }));
    expect(within(dialog).getByRole("alert")).toHaveTextContent("Enter a name and an email.");
    await user.type(within(dialog).getByLabelText("Name"), "Cy");
    await user.type(within(dialog).getByLabelText("Email"), "cy@example.test");
    await user.click(within(dialog).getByRole("button", { name: "Add" }));
    expect(await screen.findByText("Member added")).toBeInTheDocument();
    expect(screen.getByLabelText("Temporary password")).toHaveValue("TEMP-PASS-123456");
    expect(screen.getByRole("button", { name: "Copy temporary password" })).toBeInTheDocument();
    expect(api.posted("/api/v1/org/create-user")).toEqual([{ name: "Cy", email: "cy@example.test" }]);
  });

  it("removes a member after a confirmation, never window.confirm", async () => {
    const user = setupUser();
    const api = routes();
    renderPage("members");
    await user.click(await screen.findByRole("button", { name: "Actions for Bob" }));
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(confirm).toHaveTextContent("Remove Bob?");
    await user.click(within(confirm).getByRole("button", { name: "Remove member" }));
    await waitFor(() => expect(api.posted("/api/v1/org/remove-member")).toEqual([{ user_id: "u-bob" }]));
    expect(window.confirm).not.toHaveBeenCalled();
    // Bob's row (and its menu) is gone: focus lands on Add member, not <body>.
    await waitFor(() => expect(screen.getByRole("button", { name: /Add member/ })).toHaveFocus());
  });

  it("keeps the confirmation when a removal is refused", async () => {
    const user = setupUser();
    routes({ "/api/v1/org/remove-member": new Error("cannot remove the last member") });
    renderPage("members");
    await user.click(await screen.findByRole("button", { name: "Actions for Bob" }));
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Remove member" }));
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith("Couldn't remove the member", { description: "cannot remove the last member" }),
    );
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(screen.getByText("Bob")).toBeInTheDocument();
    // The confirmation stays open and keeps focus inside it; cancelling returns to the opener.
    await user.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Actions for Bob" })).toHaveFocus());
  });

  it("resets a password after a confirmation and shows the new one once", async () => {
    const user = setupUser();
    const api = routes();
    renderPage("members");
    await user.click(await screen.findByRole("button", { name: "Actions for Bob" }));
    await user.click(await screen.findByRole("menuitem", { name: "Reset password" }));
    const confirm = await screen.findByRole("alertdialog");
    await user.click(within(confirm).getByRole("button", { name: "Reset password" }));
    expect(await screen.findByText("Password reset")).toBeInTheDocument();
    expect(screen.getByLabelText("Temporary password")).toHaveValue("RESET-PASS-1");
    expect(api.posted("/api/v1/org/reset-member-password")).toEqual([{ user_id: "u-bob" }]);
  });
});
