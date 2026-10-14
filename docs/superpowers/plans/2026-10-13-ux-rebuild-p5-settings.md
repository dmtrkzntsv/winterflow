# UX rebuild, phase 5: the Settings pages — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign the body of every Settings page (spec §6) on the phase 1 shell: Server (General, Domains, Storages, Docker), Organization (General, Members, one merged Shared variables page, Notifications) and Account (Password, API tokens) — flat rows, plain words, `ResponsiveDialog` for every dialog, `ConfirmDialog` for every destructive action, optional fields behind "Advanced".

**Architecture:** Phase 1 already mounts each page at its route inside `SettingsLayout` (sub-menu, phone drill-in) — this phase replaces only what the pages render, never the routes. Each page body lives in its own file under `web/src/pages/` and renders its own `PageHeader`; `web/src/pages/server-settings.tsx` keeps the four server page components as thin guards ("no server", "no ingress") around them. Pure wording and merge rules go to `web/src/lib/` and are tested directly; the merged shared-variables page gets one hook (`useSharedVariablesSettings`) that calls exactly the routes the two old pages called. A few small shared parts (row "⋯" menu, refresh/remove buttons, list placeholder, copy field, focus hooks on the two dialog primitives) come first.

**Tech Stack:** React 19, React Router 7, TypeScript 5.9 (strict, `verbatimModuleSyntax`, `erasableSyntaxOnly`), Vite 7, Tailwind 4, vendored shadcn/ui (Radix, vaul), lucide-react 0.553, sonner, Vitest + Testing Library on jsdom. No new dependency, no Go change.

**Spec:** `docs/superpowers/specs/2026-10-13-ux-rebuild-design.md` — this plan is phase 5 of §8 ("Settings area (server, organization incl. merged variables and notifications, account)"), implementing §6 with the principles and primitives of §1.

**Base:** branch `feat/ux-rebuild` after phases 1–4. Phase 1 (`docs/superpowers/plans/2026-10-13-ux-rebuild-p1-shell.md`, Task 7 and "Interfaces for later phases") produced what this plan builds on; phases 2–4 touch other files. All commands run from the worktree root.

## Global Constraints

- The spec is binding. Frontend only: no Go change, no API change, `api/openapi.yaml` untouched. **No feature is removed:** every action the old pages offered is still reachable (sometimes in a row's "⋯" menu or under "Advanced").
- Routes are phase 1's and stay as they are: `/server/:id/settings/{general,domains,storages,docker}`, `/settings/{organization,members,variables,notifications,password,tokens}`; the components phase 1 mounts there keep their names and modules: `ServerGeneralPage`, `ServerDomainsPage`, `ServerStoragesPage`, `ServerDockerPage` (`@/pages/server-settings`), default `OrgMembersPage` with `section` (`@/pages/org-members`), default `SharedVariablesSettingsPage` (`@/pages/settings-shared-variables`), default `NotificationsSettingsPage` (`@/pages/settings-notifications`), default `UserPasswordPage` (`@/pages/user-password`), default `UserTokensPage` (`@/pages/user-tokens`).
- Security (CLAUDE.md): secret values never leave the agent and are never shown. Shared secrets keep the browser fan-out exactly: one ECIES ciphertext per keyed server (`encryptForServers`), a server-only secret for that server's key only, a "provide value" only for the keyed servers missing it; a secret row says "Secret", never `<encrypted>`. Typed secrets (registry password, PEM private key, S3 secret key, shared secret value) are wiped when their dialog closes. Shared-variable API calls stay identical to the old pages: `listOrg`, `listServer(serverId)`, `keys`, `saveOrg`, `saveServer(serverId, body)`, `deleteOrg(id)`, `deleteServer(serverId, id)`, `resync({} | {server_id} | {variable_id})`, `reload({server_id} | {server_id, names} | {variable_id})`.
- Never delete or edit anything under `web/src/components/ui/` (vendored shadcn/ui).
- Plain words (spec §1.3): "Orphaned" → "Not used by any app"; "Resync" → "Send again"; "Shoutrrr URL" → "Other (URL)"; "History only" → "Inbox only" (alert history lives in the Inbox since phase 1); no raw ids in visible text (an id may sit in a `title` for hover, or in an `aria-label`).
- One primary action per page: a page's own add button sits in its `PageHeader` actions and is the only filled button; a page made of several sections (Docker, Notifications) has no filled button — each section's add button is `variant="outline" size="sm"`.
- No `window.confirm` anywhere. Every destructive action (delete, remove, revoke, reset password) goes through `ConfirmDialog`; its action reports a failure with a toast and **rethrows**, so the confirmation stays open to retry or cancel (that is `ConfirmDialog`'s contract).
- Every dialog is a `ResponsiveDialog` (Dialog ≥640px, bottom sheet below). A form inside one has an `id`, and the footer's submit button points at it with `form="<id>"`.
- Status colours are tokens (`text-status-ok|warn|bad`, `bg-status-warn/15`, `StatusDot`, `StatusPill`), never `emerald-*`/`amber-*`/`red-*` in the files this plan touches.
- Type scale and spacing from phase 1: page title via `PageHeader`, section title via `Section` (15/600), body `text-sm`, 4/8 spacing; tap targets ≥40px on phones (`size-10 sm:size-8` for icon buttons).
- Lint (`eslint-plugin-react-hooks` 7): no `Date.now()` during render (use `useNow()` from `@/hooks/use-now`); no `ref.current` reads during render. `react-refresh`: component files export only components; hooks go in `web/src/hooks/use-*.ts`, pure helpers in `web/src/lib/`.
- TypeScript: `import type` for types, no `enum`, no unused imports/locals (remove an import when a step removes its last use).
- Tests: Vitest + Testing Library beside the code; `userEvent.setup({ delay: null })`; contexts provided the way existing tests do (`AppsContext.Provider`, `ProfileContext.Provider`, `NotificationsContext.Provider`, `vi.mock("@/context/use-servers", …)`); fetches answered by `fakeAPI` from `@/test/fake-api`; keep existing `data-testid`s (`channel-row`, `rule-row`).
- Coverage: changed web lines ≥80% (`tools/diffcov`); `web/src/test/`, `*.test.tsx`, `web/src/components/ui/`, `web/src/main.tsx` are exempt.
- Per task, before its commit: the task's Vitest files, `pnpm --dir web run lint`, `pnpm --dir web exec tsc -b` all pass. Commits are Conventional Commits ending with the line `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- `pnpm --dir web run build` empties `web/dist`: run `git checkout -- web/dist/.gitkeep` after any build.

## Review Focus

1. **A problem inside a collapsed "Advanced"** (an S3 prefix like `/x` typed and then folded away; an SMTP port of `x`). Expected: Save is not dead with no visible reason — pressing it opens "Advanced", shows the message under the field and puts focus there; nothing is sent. Pinned in Task 5 ("opens Advanced to show a problem inside it") and Task 10 ("keeps the SMTP port and sign-in under Advanced, and opens it when one is wrong").
2. **A refused or failed delete** (a domain still used by an app, a network with containers, an agent that went offline, a member removal the API refuses). Expected: the confirmation stays open, the reason is in a toast, the row stays in the list; after a successful removal focus lands on the page's add button instead of falling to `<body>`. Pinned in Task 2 ("offers no removal of a built-in network and keeps the confirmation open when a removal fails", "keeps the confirmation open and lists again when Docker refuses"), Task 4 ("removes a domain after confirmation and keeps it on a refusal"), Task 6 ("explains why a storage in use can't be removed, and confirms otherwise", "reports load and delete failures, keeping the confirmation open"), Task 7 ("keeps the confirmation when a removal is refused"), Task 12 ("keeps the confirmation when a revoke fails").
3. **Switching servers, or a slow answer, on Settings → Shared variables.** Expected: the previous server's "{server} only" rows and built-ins never show under the new server (the list shows it is loading until the new server answers, never a mix); a late answer for the old server is dropped; nothing can be edited or deleted in the old server's scope meanwhile. Pinned in Task 8 (hook tests "never shows the previous server's rows after a switch", "drops a late answer for a server switched away from").
4. **A channel renamed or deleted while the rules list is on the same page.** Expected: each rule's "notify" line follows at once (the page shares one channel list), never "deleted channel" for a channel that still exists. Pinned in Task 11 ("follows a channel rename in the rules without a reload").
5. **Secrets after a dialog closes or in a list** (registry password, PEM key, S3 secret key, a shared secret's value; a secret row). Expected: reopening shows empty fields; a secret row says "Secret" and never prints `<encrypted>`; a new org secret is encrypted once per keyed server, a server-only one for that server only. Pinned in Task 2 ("adds a registry, wiping the password when the dialog closes"), Task 4 ("wipes the certificate and key when the dialog closes"), Task 5 (the kept "wipes a typed secret when the dialog closes"), Task 9 (dialog: "encrypts a server-only secret for that server only", "encrypts a new org secret for every keyed server…"; page: "never prints a secret's value").

## File map

Created:

| File | Responsibility |
|---|---|
| `web/src/components/row-menu.tsx` | `RowMenu` — a row's "⋯" actions |
| `web/src/components/refresh-button.tsx` | `RefreshButton` — icon button that reloads a list |
| `web/src/components/remove-button.tsx` | `RemoveButton` — a row's single trash-icon action |
| `web/src/components/list-placeholder.tsx` | `ListPlaceholder` — spinner, then "couldn't load" + Try again |
| `web/src/components/copy-field.tsx` | `CopyField` — a value to hand over once, with Copy |
| `web/src/lib/clipboard.ts` | `copyText` (moved out of two pages) |
| `web/src/pages/settings-docker.tsx` | `DockerSettings` — registries, networks, volumes |
| `web/src/lib/server-facts.ts` | `serverFacts`, `versionLabel` |
| `web/src/lib/members.ts` | `initials` |
| `web/src/lib/tokens.ts` | `tokenMeta` |
| `web/src/hooks/use-shared-variables-settings.ts` | the merged shared-variables page's state and actions |
| `web/src/hooks/use-alert-channels.ts` | the org's channels, shared by both Notifications sections |
| `web/src/components/alerts/service-icon.tsx` | `ServiceIcon` |

Renamed: `web/src/components/alerts/channels-tab.tsx` → `channels-section.tsx` (`ChannelsSection`), `rules-tab.tsx` → `rules-section.tsx` (`RulesSection`), with their tests.

Deleted: `web/src/pages/org-variables.tsx` (+ test), `web/src/pages/settings-variables.tsx` (+ test), `web/src/components/variables-table.tsx` (+ test), `web/src/components/delete-variable-dialog.tsx`, `web/src/hooks/use-shared-variables-page.ts`.

Rewritten bodies: `server-settings.tsx`, `settings-domains.tsx` (`DomainsSettings`), `settings-storages.tsx` (`StoragesSettings`), `settings-volumes.tsx` (`VolumesSection`), `org-members.tsx`, `settings-shared-variables.tsx`, `settings-notifications.tsx`, `user-password.tsx`, `user-tokens.tsx`; dialogs `domain-dialog.tsx`, `storage-dialog.tsx`, `orphans-dialog.tsx`, `shared-variable-dialog.tsx`, `alerts/channel-dialog.tsx`, `alerts/rule-dialog.tsx`, `alerts/override-dialog.tsx`, `alerts/channel-picker.tsx`. Extended: `responsive-dialog.tsx`, `confirm-dialog.tsx`, `section.tsx` (phase 1 primitives), `lib/domains.ts`, `lib/storages.ts`, `lib/volumes.ts`, `lib/shared-variables.ts`, `lib/alerts.ts`.

---

### Task 1: Shared parts for the settings pages

**Files:**
- Modify: `web/src/components/responsive-dialog.tsx` (focus hooks)
- Modify: `web/src/components/confirm-dialog.tsx` (close focus hook)
- Modify: `web/src/components/section.tsx` (`Row` `testId`)
- Create: `web/src/components/row-menu.tsx`, `web/src/components/refresh-button.tsx`, `web/src/components/remove-button.tsx`, `web/src/components/list-placeholder.tsx`, `web/src/components/copy-field.tsx`, `web/src/lib/clipboard.ts`
- Test: `web/src/components/dialog-focus.test.tsx`, `web/src/components/row-menu.test.tsx`, `web/src/components/list-parts.test.tsx`, `web/src/components/copy-field.test.tsx`, `web/src/lib/clipboard.test.ts`

**Interfaces:**
- Consumes (phase 1): `ResponsiveDialog(ResponsiveDialogProps)`, `ConfirmDialog(ConfirmDialogProps)`, `Row`, `RowList`, `Section` from `@/components/section`; `DropdownMenu*` (`@/components/ui/dropdown-menu`).
- Produces:
  - `ResponsiveDialogProps` gains `onOpenAutoFocus?: (event: Event) => void` and `onCloseAutoFocus?: (event: Event) => void`, forwarded to the dialog and to the phone sheet.
  - `ConfirmDialogProps` gains `onCloseAutoFocus?: (event: Event) => void`.
  - `Row` gains `testId?: string` (rendered as `data-testid` on the `<li>`).
  - `RowMenuItem = { label: string; onSelect: (trigger: HTMLButtonElement | null) => void; destructive?: boolean; disabled?: boolean; title?: string }`; `RowMenu({ label, items: (RowMenuItem | false | null | undefined)[] })`.
  - `RefreshButton({ label, loading, onClick })`, `RemoveButton({ label, onClick })`, `ListPlaceholder({ loading, failed, onRetry? })`, `CopyField({ value, label })`.
  - `copyText(text: string): Promise<void>` (`@/lib/clipboard`).

If an earlier phase already created one of these files with the same export, keep that file, add only what is missing from the signatures above, and keep its tests.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/dialog-focus.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { stubViewport } from "@/test/media";

afterEach(() => vi.unstubAllGlobals());

describe("dialog focus hooks", () => {
  it("tells the caller when a dialog opens and closes", async () => {
    const onOpen = vi.fn();
    const onClose = vi.fn();
    const dialog = (open: boolean) => (
      <ResponsiveDialog open={open} onOpenChange={() => {}} title="Edit" onOpenAutoFocus={onOpen} onCloseAutoFocus={onClose}>
        <input aria-label="Field" />
      </ResponsiveDialog>
    );
    const { rerender } = render(dialog(true));
    expect(onOpen).toHaveBeenCalled();
    rerender(dialog(false));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it("tells the caller when the phone sheet opens", () => {
    stubViewport(375);
    const onOpen = vi.fn();
    render(
      <ResponsiveDialog open onOpenChange={() => {}} title="Edit" onOpenAutoFocus={onOpen}>
        <input aria-label="Field" />
      </ResponsiveDialog>,
    );
    expect(onOpen).toHaveBeenCalled();
  });

  it("lets a confirmation hand focus somewhere else once it closes", async () => {
    const user = userEvent.setup({ delay: null });
    const onClose = vi.fn((e: Event) => e.preventDefault());
    function Harness() {
      const [open, setOpen] = useState(true);
      return (
        <ConfirmDialog
          open={open}
          onOpenChange={setOpen}
          title="Remove it?"
          confirmLabel="Remove"
          onConfirm={() => {}}
          onCloseAutoFocus={onClose}
        />
      );
    }
    render(<Harness />);
    await user.click(screen.getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });
});
```

Create `web/src/components/row-menu.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { RowMenu } from "@/components/row-menu";

describe("RowMenu", () => {
  it("runs an item with its trigger, skips empty entries and marks the destructive one", async () => {
    const user = userEvent.setup({ delay: null });
    const edit = vi.fn();
    const remove = vi.fn();
    render(
      <RowMenu
        label="Actions for disk"
        items={[{ label: "Edit", onSelect: edit }, false, null, { label: "Remove", destructive: true, onSelect: remove }]}
      />,
    );
    const trigger = screen.getByRole("button", { name: "Actions for disk" });
    await user.click(trigger);
    expect(await screen.findAllByRole("menuitem")).toHaveLength(2);
    expect(screen.getByRole("menuitem", { name: "Remove" })).toHaveClass("text-destructive");
    await user.click(screen.getByRole("menuitem", { name: "Edit" }));
    expect(edit).toHaveBeenCalledWith(trigger);
    expect(remove).not.toHaveBeenCalled();
  });

  it("keeps a disabled item from running and explains it on hover", async () => {
    const user = userEvent.setup({ delay: null });
    const run = vi.fn();
    render(<RowMenu label="Actions for x" items={[{ label: "Run", onSelect: run, disabled: true, title: "Busy" }]} />);
    await user.click(screen.getByRole("button", { name: "Actions for x" }));
    const item = await screen.findByRole("menuitem", { name: "Run" });
    expect(item).toHaveAttribute("aria-disabled", "true");
    expect(item).toHaveAttribute("title", "Busy");
    await user.click(item);
    expect(run).not.toHaveBeenCalled();
  });

  it("renders nothing without an item", () => {
    const { container } = render(<RowMenu label="Actions for x" items={[false]} />);
    expect(container).toBeEmptyDOMElement();
  });
});
```

Create `web/src/components/list-parts.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ListPlaceholder } from "@/components/list-placeholder";
import { RefreshButton } from "@/components/refresh-button";
import { RemoveButton } from "@/components/remove-button";
import { Row, RowList } from "@/components/section";

describe("list parts", () => {
  it("shows a spinner while loading, then the failure with Try again", async () => {
    const user = userEvent.setup({ delay: null });
    const retry = vi.fn();
    const { rerender } = render(<ListPlaceholder loading failed="Couldn't load domains." onRetry={retry} />);
    expect(screen.getByRole("status", { name: "Loading" })).toBeInTheDocument();
    expect(screen.queryByText("Couldn't load domains.")).not.toBeInTheDocument();
    rerender(<ListPlaceholder loading={false} failed="Couldn't load domains." onRetry={retry} />);
    expect(screen.getByText("Couldn't load domains.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(retry).toHaveBeenCalled();
  });

  it("names the refresh and remove buttons and turns refresh off while loading", async () => {
    const user = userEvent.setup({ delay: null });
    const reload = vi.fn();
    const remove = vi.fn();
    const { rerender } = render(
      <>
        <RefreshButton label="Refresh domains" loading={false} onClick={reload} />
        <RemoveButton label="Remove registry ghcr.io" onClick={remove} />
      </>,
    );
    await user.click(screen.getByRole("button", { name: "Refresh domains" }));
    await user.click(screen.getByRole("button", { name: "Remove registry ghcr.io" }));
    expect(reload).toHaveBeenCalled();
    expect(remove).toHaveBeenCalled();
    rerender(<RefreshButton label="Refresh domains" loading onClick={reload} />);
    expect(screen.getByRole("button", { name: "Refresh domains" })).toBeDisabled();
  });

  it("puts a test id on a row", () => {
    render(
      <RowList>
        <Row title="Ops" testId="channel-row" />
      </RowList>,
    );
    expect(screen.getByTestId("channel-row")).toHaveTextContent("Ops");
  });
});
```

Create `web/src/lib/clipboard.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from "vitest";

import { copyText } from "@/lib/clipboard";

const original = Object.getOwnPropertyDescriptor(navigator, "clipboard");

afterEach(() => {
  if (original) Object.defineProperty(navigator, "clipboard", original);
  else delete (navigator as { clipboard?: unknown }).clipboard;
  vi.restoreAllMocks();
});

describe("copyText", () => {
  it("uses the clipboard API when the page may", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    await copyText("wfp_abc");
    expect(writeText).toHaveBeenCalledWith("wfp_abc");
  });

  it("falls back to a selection copy on a plain-http origin, and fails when that is refused", async () => {
    Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true });
    const exec = vi.fn().mockReturnValueOnce(true).mockReturnValueOnce(false);
    Object.defineProperty(document, "execCommand", { value: exec, configurable: true });
    await copyText("one");
    expect(exec).toHaveBeenCalledWith("copy");
    await expect(copyText("two")).rejects.toThrow("copy rejected");
    expect(document.querySelector("textarea")).toBeNull();
  });
});
```

Create `web/src/components/copy-field.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

const toastSuccess = vi.hoisted(() => vi.fn());
const toastError = vi.hoisted(() => vi.fn());
vi.mock("sonner", () => ({ toast: { success: toastSuccess, error: toastError } }));
const copy = vi.hoisted(() => vi.fn());
vi.mock("@/lib/clipboard", () => ({ copyText: copy }));

import { CopyField } from "@/components/copy-field";

describe("CopyField", () => {
  it("shows the value read-only and copies it", async () => {
    const user = userEvent.setup({ delay: null });
    copy.mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error("no"));
    render(<CopyField value="TEMP-123" label="Temporary password" />);
    expect(screen.getByLabelText("Temporary password")).toHaveValue("TEMP-123");
    expect(screen.getByLabelText("Temporary password")).toHaveAttribute("readonly");
    await user.click(screen.getByRole("button", { name: "Copy temporary password" }));
    expect(copy).toHaveBeenCalledWith("TEMP-123");
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith("Copied"));
    await user.click(screen.getByRole("button", { name: "Copy temporary password" }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith("Couldn't copy: select the text and copy it yourself"));
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/components/dialog-focus.test.tsx src/components/row-menu.test.tsx src/components/list-parts.test.tsx src/lib/clipboard.test.ts src/components/copy-field.test.tsx`
Expected: FAIL — the modules do not exist; `ResponsiveDialog`/`ConfirmDialog` ignore the focus props; `Row` ignores `testId`.

- [ ] **Step 3: Implement**

In `web/src/components/responsive-dialog.tsx`, add to `ResponsiveDialogProps` (after `className?: string;`):

```tsx
  // Focus hooks of the content: where focus goes when it opens (default:
  // its first field) and back to when it closes (default: what was focused
  // before it opened) — for a dialog opened from a row that may be gone.
  onOpenAutoFocus?: (event: Event) => void;
  onCloseAutoFocus?: (event: Event) => void;
```

add `onOpenAutoFocus, onCloseAutoFocus,` to the destructured parameters, and pass both to the two contents:

```tsx
        <DrawerContent
          className={cn("max-h-[90svh]", className)}
          onOpenAutoFocus={onOpenAutoFocus}
          onCloseAutoFocus={onCloseAutoFocus}
          {...describedBy}
        >
```

```tsx
      <DialogContent
        className={cn("max-h-[90svh] overflow-y-auto", className)}
        onOpenAutoFocus={onOpenAutoFocus}
        onCloseAutoFocus={onCloseAutoFocus}
        {...describedBy}
      >
```

In `web/src/components/confirm-dialog.tsx`, add to `ConfirmDialogProps` (after `children?: ReactNode;`):

```tsx
  // onCloseAutoFocus is where focus goes once the dialog closes (default:
  // back to what opened it — pass one when that leaves with its row).
  onCloseAutoFocus?: (event: Event) => void;
```

add `onCloseAutoFocus,` to the destructured parameters, and change the content line to:

```tsx
      <AlertDialogContent onCloseAutoFocus={onCloseAutoFocus} {...(description ? {} : { "aria-describedby": undefined })}>
```

In `web/src/components/section.tsx`, give `Row` the prop — add `testId,` to its destructured parameters, `testId?: string;` to its prop type (after `className?: string;`), and `data-testid={testId}` to the `<li`:

```tsx
    <li
      data-testid={testId}
      className={cn(
```

Create `web/src/components/row-menu.tsx`:

```tsx
import { useRef } from "react";
import { MoreHorizontal } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

export type RowMenuItem = {
  label: string;
  // onSelect gets the menu's button: a dialog opened from the menu hands
  // focus back to it when it closes (returnFocusTo).
  onSelect: (trigger: HTMLButtonElement | null) => void;
  destructive?: boolean;
  disabled?: boolean;
  title?: string;
};

// RowMenu is a row's "⋯" menu: its less frequent actions, the destructive
// one last and red. label names the button ("Actions for backup-disk").
// Falsy entries are skipped, so a caller can write `cond && item`.
export function RowMenu({ label, items }: { label: string; items: (RowMenuItem | false | null | undefined)[] }) {
  const trigger = useRef<HTMLButtonElement>(null);
  const shown = items.filter((item): item is RowMenuItem => Boolean(item));
  if (shown.length === 0) return null;
  return (
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger asChild>
        <Button
          ref={trigger}
          size="icon"
          variant="ghost"
          className="size-10 text-muted-foreground sm:size-8"
          aria-label={label}
        >
          <MoreHorizontal className="size-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {shown.map((item) => (
          <DropdownMenuItem
            key={item.label}
            disabled={item.disabled}
            title={item.title}
            className={cn(item.destructive && "text-destructive focus:text-destructive")}
            onSelect={() => item.onSelect(trigger.current)}
          >
            {item.label}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
```

Create `web/src/components/refresh-button.tsx`:

```tsx
import { RefreshCw } from "lucide-react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

// RefreshButton lists something again ("Refresh domains"); it is off and
// spinning while a load runs.
export function RefreshButton({ label, loading, onClick }: { label: string; loading: boolean; onClick: () => void }) {
  return (
    <Button
      size="icon"
      variant="ghost"
      className="size-10 text-muted-foreground sm:size-8"
      aria-label={label}
      title={label}
      disabled={loading}
      onClick={onClick}
    >
      <RefreshCw className={cn("size-4", loading && "animate-spin motion-reduce:animate-none")} />
    </Button>
  );
}
```

Create `web/src/components/remove-button.tsx`:

```tsx
import { Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";

// RemoveButton is a row's only action when that action is a removal: a
// trash icon named for what it removes ("Remove registry ghcr.io"). It
// opens a ConfirmDialog; it never removes by itself.
export function RemoveButton({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <Button
      size="icon"
      variant="ghost"
      className="size-10 text-muted-foreground hover:text-destructive sm:size-8"
      aria-label={label}
      title={label}
      onClick={onClick}
    >
      <Trash2 className="size-4" />
    </Button>
  );
}
```

Create `web/src/components/list-placeholder.tsx`:

```tsx
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";

// ListPlaceholder holds a list's place until its first answer: a spinner
// while loading, then what went wrong and (onRetry) a way to try again.
export function ListPlaceholder({
  loading,
  failed,
  onRetry,
}: {
  loading: boolean;
  failed: string;
  onRetry?: () => void;
}) {
  return (
    <div className="flex min-h-24 flex-col items-center justify-center gap-2 rounded-xl border border-dashed p-4 text-center text-sm text-muted-foreground">
      {loading ? (
        <Spinner />
      ) : (
        <>
          <p>{failed}</p>
          {onRetry ? (
            <Button size="sm" variant="outline" onClick={onRetry}>
              Try again
            </Button>
          ) : null}
        </>
      )}
    </div>
  );
}
```

Create `web/src/lib/clipboard.ts`:

```ts
// copyText copies text, also on a plain-http LAN origin where browsers
// hide navigator.clipboard (the constraint crypto.subtle has in lib/ecies):
// it falls back to the selection-based copy and rejects when that fails.
export async function copyText(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const area = document.createElement("textarea");
  area.value = text;
  area.style.position = "fixed";
  area.style.opacity = "0";
  document.body.appendChild(area);
  area.select();
  try {
    if (!document.execCommand("copy")) throw new Error("copy rejected");
  } finally {
    area.remove();
  }
}
```

Create `web/src/components/copy-field.tsx`:

```tsx
import { useState } from "react";
import { Check, Copy } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { copyText } from "@/lib/clipboard";

// CopyField shows a value handed over once (a temporary password, a new
// token) read-only, with a Copy button; focusing it selects it all.
export function CopyField({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await copyText(value);
      setCopied(true);
      toast.success("Copied");
    } catch {
      toast.error("Couldn't copy: select the text and copy it yourself");
    }
  };
  return (
    <div className="flex items-center gap-2">
      <Input
        readOnly
        aria-label={label}
        value={value}
        className="font-mono text-xs"
        onFocus={(e) => e.currentTarget.select()}
      />
      <Button
        type="button"
        variant="outline"
        size="icon"
        className="size-10 shrink-0 sm:size-9"
        aria-label={`Copy ${label.toLowerCase()}`}
        onClick={() => void copy()}
      >
        {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
      </Button>
    </div>
  );
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/components/dialog-focus.test.tsx src/components/row-menu.test.tsx src/components/list-parts.test.tsx src/lib/clipboard.test.ts src/components/copy-field.test.tsx src/components/responsive-dialog.test.tsx src/components/confirm-dialog.test.tsx src/components/section.test.tsx`
Expected: PASS (phase 1's primitive tests still pass).

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/components/responsive-dialog.tsx web/src/components/confirm-dialog.tsx web/src/components/section.tsx web/src/components/row-menu.tsx web/src/components/refresh-button.tsx web/src/components/remove-button.tsx web/src/components/list-placeholder.tsx web/src/components/copy-field.tsx web/src/lib/clipboard.ts web/src/components/dialog-focus.test.tsx web/src/components/row-menu.test.tsx web/src/components/list-parts.test.tsx web/src/components/copy-field.test.tsx web/src/lib/clipboard.test.ts
git commit -m "$(cat <<'EOF'
feat(web): row menu, list parts, copy field and dialog focus hooks for settings

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 2: Server → Docker — registries, networks and volumes on one page, every delete confirmed

**Files:**
- Create: `web/src/pages/settings-docker.tsx`, `web/src/pages/settings-docker.test.tsx`
- Rewrite: `web/src/pages/settings-volumes.tsx` (`VolumesCard` → `VolumesSection`), `web/src/pages/settings-volumes.test.tsx`
- Modify: `web/src/lib/volumes.ts`, `web/src/lib/volumes.test.ts` (`volumeState`)
- Modify: `web/src/pages/server-settings.tsx` (`ServerDockerPage`; `RegistriesCard`/`NetworksCard` move out), `web/src/pages/server-settings.test.tsx`

**Interfaces:**
- Consumes: Task 1 (`RefreshButton`, `RemoveButton`, `ListPlaceholder`, `ConfirmDialog`, `ResponsiveDialog`); phase 1 `PageHeader`, `Section`, `RowList`, `Row`, `EmptyState`, `StatusDot` (`@/components/status`), `StatusTone` (`@/lib/status`), `appSub` (`@/lib/routes`); `Docker` (`listRegistries`, `createRegistry`, `deleteRegistry`, `listNetworks`, `createNetwork`, `deleteNetwork`, `listVolumes`, `createVolume`, `deleteVolume`), `useServerPath`, `useApps`.
- Produces: `DockerSettings({ docker }: { docker: Docker })` (`@/pages/settings-docker`) — the whole Docker page with its `PageHeader`; `VolumesSection({ docker })` (`@/pages/settings-volumes`); `volumeState(v: Volume): { tone: StatusTone; label: string }` (`@/lib/volumes`).

- [ ] **Step 1: Write the failing tests**

Append to `web/src/lib/volumes.test.ts` (and add `volumeState` to its `import { … } from "@/lib/volumes"`):

```ts
describe("volumeState", () => {
  it("says in plain words whether a volume is used", () => {
    expect(volumeState(vol({ name: "a", in_use: true }))).toEqual({ tone: "ok", label: "In use" });
    expect(volumeState(vol({ name: "b", orphan: true, app_id: "gone" }))).toEqual({ tone: "warn", label: "Not used by any app" });
    expect(volumeState(vol({ name: "c" }))).toEqual({ tone: "idle", label: "Unused" });
  });
});
```

Create `web/src/pages/settings-docker.test.tsx`:

```tsx
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";
import type { Docker } from "@/context/use-docker";
import { DockerSettings } from "@/pages/settings-docker";

vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));

const toastError = vi.hoisted(() => vi.fn());
const toastSuccess = vi.hoisted(() => vi.fn());
vi.mock("sonner", () => ({ toast: { success: toastSuccess, error: toastError } }));

const setupUser = () => userEvent.setup({ delay: null });

function fakeDocker(over: Partial<Docker> = {}): Docker {
  return {
    listRegistries: vi.fn().mockResolvedValue([{ address: "ghcr.io" }]),
    createRegistry: vi.fn().mockResolvedValue(undefined),
    deleteRegistry: vi.fn().mockResolvedValue(undefined),
    listNetworks: vi.fn().mockResolvedValue([
      { id: "n1", name: "bridge", driver: "bridge", scope: "local" },
      { id: "n2", name: "proxy", driver: "bridge", scope: "local" },
    ]),
    createNetwork: vi.fn().mockResolvedValue(undefined),
    deleteNetwork: vi.fn().mockResolvedValue(undefined),
    listVolumes: vi.fn().mockResolvedValue([]),
    createVolume: vi.fn().mockResolvedValue(undefined),
    deleteVolume: vi.fn().mockResolvedValue(undefined),
    ...over,
  } as unknown as Docker;
}

function renderPage(docker: Docker) {
  const apps = { apps: [] } as unknown as AppsContextValue;
  return render(
    <MemoryRouter>
      <AppsContext.Provider value={apps}>
        <DockerSettings docker={docker} />
      </AppsContext.Provider>
    </MemoryRouter>,
  );
}

describe("DockerSettings", () => {
  beforeEach(() => {
    toastError.mockReset();
    toastSuccess.mockReset();
  });

  it("puts registries, networks and volumes on one page", async () => {
    renderPage(fakeDocker());
    expect(screen.getByRole("heading", { level: 1, name: "Docker" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Registries" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Networks" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Volumes" })).toBeInTheDocument();
    expect(await screen.findByText("ghcr.io")).toBeInTheDocument();
    expect(await screen.findByText("proxy")).toBeInTheDocument();
    expect(screen.getAllByText("bridge · local")).toHaveLength(2);
  });

  it("removes a registry only after a confirmation", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderPage(docker);
    await user.click(await screen.findByRole("button", { name: "Remove registry ghcr.io" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(docker.deleteRegistry).not.toHaveBeenCalled();
    await user.click(within(confirm).getByRole("button", { name: "Remove registry" }));
    expect(docker.deleteRegistry).toHaveBeenCalledWith("ghcr.io");
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(toastSuccess).toHaveBeenCalledWith("Registry removed");
    expect(docker.listRegistries).toHaveBeenCalledTimes(2);
  });

  it("offers no removal of a built-in network and keeps the confirmation open when a removal fails", async () => {
    const user = setupUser();
    const docker = fakeDocker({
      deleteNetwork: vi.fn().mockRejectedValue(new Error("network proxy has active endpoints")),
    });
    renderPage(docker);
    await screen.findByText("proxy");
    expect(screen.queryByRole("button", { name: "Remove network bridge" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Remove network proxy" }));
    const confirm = await screen.findByRole("alertdialog");
    await user.click(within(confirm).getByRole("button", { name: "Remove network" }));
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith("Couldn't remove the network", {
        description: "network proxy has active endpoints",
      }),
    );
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(screen.getByText("proxy")).toBeInTheDocument();
  });

  it("adds a registry, wiping the password when the dialog closes", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderPage(docker);
    await user.click(screen.getByRole("button", { name: /Add registry/ }));
    let dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Address"), "registry.example.com");
    await user.type(within(dialog).getByLabelText("Username"), "bot");
    await user.type(within(dialog).getByLabelText("Password"), "SECRET");
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    await user.click(screen.getByRole("button", { name: /Add registry/ }));
    dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByLabelText("Password")).toHaveValue("");
    const add = within(dialog).getByRole("button", { name: "Add" });
    expect(add).toBeDisabled();
    await user.type(within(dialog).getByLabelText("Address"), "registry.example.com");
    await user.type(within(dialog).getByLabelText("Username"), "bot");
    await user.type(within(dialog).getByLabelText("Password"), "pw");
    await user.click(add);
    expect(docker.createRegistry).toHaveBeenCalledWith("registry.example.com", "bot", "pw");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("creates a network", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderPage(docker);
    await user.click(screen.getByRole("button", { name: /Add network/ }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), " web ");
    await user.click(within(dialog).getByRole("button", { name: "Create" }));
    expect(docker.createNetwork).toHaveBeenCalledWith("web");
    await waitFor(() => expect(docker.listNetworks).toHaveBeenCalledTimes(2));
  });

  it("says so when a list is empty or can't be loaded", async () => {
    const user = setupUser();
    const listNetworks = vi.fn().mockRejectedValueOnce(new Error("agent offline")).mockResolvedValue([]);
    renderPage(fakeDocker({ listRegistries: vi.fn().mockResolvedValue([]), listNetworks }));
    expect(await screen.findByText("No registries. Add one to pull private images.")).toBeInTheDocument();
    expect(await screen.findByText("Couldn't load networks.")).toBeInTheDocument();
    expect(toastError).toHaveBeenCalledWith("Couldn't load networks", { description: "agent offline" });
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("No networks yet.")).toBeInTheDocument();
  });
});
```

Replace `web/src/pages/settings-volumes.test.tsx` with:

```tsx
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";
import type { Docker, Volume } from "@/context/use-docker";
import { VolumesSection } from "@/pages/settings-volumes";

// Links point into the active server's scope (/server/:id/...).
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));

const toastError = vi.hoisted(() => vi.fn());
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: toastError } }));

const HASH = "fedcba9876543210".repeat(4);

const VOLUMES: Volume[] = [
  { name: "wf-a1_data", app_id: "a1", project: "wf-a1", in_use: true, orphan: false, anonymous: false },
  { name: "wf-gone_db", app_id: "gone", project: "wf-gone", in_use: false, orphan: true, anonymous: false },
  { name: "shared", in_use: false, orphan: false, anonymous: false },
  { name: HASH, in_use: false, orphan: false, anonymous: true },
];

// fakeDocker stubs only what VolumesSection calls.
function fakeDocker(over: Partial<Docker> = {}): Docker {
  return {
    listVolumes: vi.fn().mockResolvedValue(VOLUMES),
    createVolume: vi.fn().mockResolvedValue(undefined),
    deleteVolume: vi.fn().mockResolvedValue(undefined),
    ...over,
  } as unknown as Docker;
}

function renderSection(docker: Docker) {
  const apps = { apps: [{ id: "a1", name: "Nextcloud" }] } as unknown as AppsContextValue;
  return render(
    <MemoryRouter>
      <AppsContext.Provider value={apps}>
        <VolumesSection docker={docker} />
      </AppsContext.Provider>
    </MemoryRouter>,
  );
}

describe("VolumesSection", () => {
  it("lists named volumes grouped, with owners and how many no app uses", async () => {
    renderSection(fakeDocker());
    expect(await screen.findByText("Winterflow apps")).toBeInTheDocument();
    expect(screen.getByText("Other volumes")).toBeInTheDocument();
    expect(screen.queryByText("Anonymous")).not.toBeInTheDocument();
    // Short names, full name kept in the title.
    expect(screen.getByTitle("wf-a1_data")).toHaveTextContent("data");
    expect(screen.getByTitle("shared")).toHaveTextContent("shared");
    expect(screen.getByRole("link", { name: "Nextcloud" })).toHaveAttribute("href", "/server/srv-1/app/a1");
    expect(screen.getByText("In use")).toBeInTheDocument();
    expect(screen.getByText("Not used by any app")).toBeInTheDocument();
    expect(screen.getByText(/3 volumes/)).toBeInTheDocument();
    expect(screen.getByText(/1 not used by any app/)).toBeInTheDocument();
    expect(screen.queryByText(/orphan/i)).not.toBeInTheDocument();
  });

  it("shows anonymous volumes only when switched on", async () => {
    const user = userEvent.setup({ delay: null });
    renderSection(fakeDocker());
    await user.click(await screen.findByRole("switch", { name: "Show 1 anonymous" }));
    expect(screen.getByText("Anonymous")).toBeInTheDocument();
    expect(screen.getByTitle(HASH)).toHaveTextContent(`${HASH.slice(0, 12)}…`);
    expect(screen.getByText(/4 volumes/)).toBeInTheDocument();
  });

  it("deletes an unused volume after confirmation and reloads", async () => {
    const user = userEvent.setup({ delay: null });
    const docker = fakeDocker();
    renderSection(docker);
    await screen.findByText("Winterflow apps");
    // A mounted volume offers no delete, every other one does.
    expect(screen.queryByRole("button", { name: "Delete volume wf-a1_data" })).not.toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /^Delete volume/ })).toHaveLength(2);
    await user.click(screen.getByRole("button", { name: "Delete volume shared" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("shared and all the data in it are erased for good.");
    await user.click(within(dialog).getByRole("button", { name: "Delete volume" }));
    expect(docker.deleteVolume).toHaveBeenCalledWith("shared");
    await waitFor(() => expect(docker.listVolumes).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
  });

  it("keeps the confirmation open and lists again when Docker refuses", async () => {
    const user = userEvent.setup({ delay: null });
    const docker = fakeDocker({ deleteVolume: vi.fn().mockRejectedValue(new Error("volume is in use")) });
    renderSection(docker);
    await user.click(await screen.findByRole("button", { name: "Delete volume shared" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Delete volume" }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith("Couldn't delete the volume", { description: "volume is in use" }));
    expect(docker.listVolumes).toHaveBeenCalledTimes(2);
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
  });

  it("validates the name before creating", async () => {
    const user = userEvent.setup({ delay: null });
    const docker = fakeDocker();
    renderSection(docker);
    await user.click(await screen.findByRole("button", { name: /Create volume/ }));
    const dialog = await screen.findByRole("dialog");
    const create = within(dialog).getByRole("button", { name: "Create" });
    expect(create).toBeDisabled();
    await user.type(within(dialog).getByLabelText("Name"), "-bad");
    expect(within(dialog).getByText(/Start with a letter or digit/)).toBeInTheDocument();
    expect(create).toBeDisabled();
    await user.clear(within(dialog).getByLabelText("Name"));
    await user.type(within(dialog).getByLabelText("Name"), "media");
    await user.click(create);
    expect(docker.createVolume).toHaveBeenCalledWith("media");
  });

  it("says so when the list can't be loaded", async () => {
    renderSection(fakeDocker({ listVolumes: vi.fn().mockRejectedValue(new Error("agent offline")) }));
    expect(await screen.findByText("Couldn't load volumes.")).toBeInTheDocument();
  });
});
```

In `web/src/pages/server-settings.test.tsx` (phase 1's): replace the line `vi.mock("@/pages/settings-volumes", () => ({ VolumesCard: () => <p>volumes card</p> }));` with

```tsx
vi.mock("@/pages/settings-docker", () => ({ DockerSettings: () => <p>docker settings</p> }));
```

and replace the test `it("stacks registries, networks and volumes on Docker", …)` with

```tsx
  it("shows the Docker resources", () => {
    renderPage(<ServerDockerPage />);
    expect(screen.getByText("docker settings")).toBeInTheDocument();
  });
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/volumes.test.ts src/pages/settings-docker.test.tsx src/pages/settings-volumes.test.tsx src/pages/server-settings.test.tsx`
Expected: FAIL — `volumeState`, `DockerSettings` and `VolumesSection` do not exist.

- [ ] **Step 3: Implement**

Append to `web/src/lib/volumes.ts` (and add `import type { StatusTone } from "@/lib/status";` to its imports):

```ts
// volumeState says whether a volume is used, in plain words: mounted by a
// container, labelled for a Winterflow app that is no longer here, or
// neither.
export function volumeState(v: Volume): { tone: StatusTone; label: string } {
  if (v.in_use) return { tone: "ok", label: "In use" };
  if (v.orphan) return { tone: "warn", label: "Not used by any app" };
  return { tone: "idle", label: "Unused" };
}
```

Replace `web/src/pages/settings-volumes.tsx` with:

```tsx
import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { Plus } from "lucide-react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { ListPlaceholder } from "@/components/list-placeholder";
import { RefreshButton } from "@/components/refresh-button";
import { RemoveButton } from "@/components/remove-button";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { Row, RowList, Section } from "@/components/section";
import { StatusDot } from "@/components/status";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { useApps } from "@/context/use-apps";
import type { Docker, Volume } from "@/context/use-docker";
import { useServerPath } from "@/context/use-server-path";
import { appSub } from "@/lib/routes";
import { cn } from "@/lib/utils";
import { groupVolumes, shortVolumeName, volumeNameError, volumeState } from "@/lib/volumes";

const errorText = (e: unknown) => (e instanceof Error ? e.message : undefined);

// ownerOf names who a volume belongs to: its app (a link), a deleted app,
// or the compose project; nothing for a plain volume.
function ownerOf(volume: Volume, appName: string | undefined, appHref: (id: string) => string): ReactNode {
  if (volume.app_id && appName) {
    return (
      <Link to={appHref(volume.app_id)} className="font-medium underline-offset-4 hover:underline">
        {appName}
      </Link>
    );
  }
  if (volume.orphan) return <span title={`No app ${volume.app_id} on this server`}>Deleted app</span>;
  if (volume.project) return <span title="Docker Compose project">{volume.project}</span>;
  return null;
}

// CreateVolumeDialog asks for a volume's name; closing it (any way) clears it.
function CreateVolumeDialog({
  docker,
  open,
  onOpenChange,
  onCreated,
}: {
  docker: Docker;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: () => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [name, setName] = useState("");
  const trimmed = name.trim();
  const nameError = trimmed ? volumeNameError(trimmed) : "";
  const canCreate = trimmed !== "" && !nameError && !busy;

  const changeOpen = (next: boolean) => {
    if (!next) setName("");
    onOpenChange(next);
  };

  const create = async () => {
    setBusy(true);
    try {
      await docker.createVolume(trimmed);
      toast.success("Volume created");
      changeOpen(false);
      await onCreated();
    } catch (e) {
      toast.error("Couldn't create the volume", { description: errorText(e) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <ResponsiveDialog
      open={open}
      onOpenChange={changeOpen}
      title="Create volume"
      description={
        <>
          An empty local volume. To mount it in an app, declare it in the compose file with{" "}
          <code className="whitespace-nowrap">external: true</code>.
        </>
      }
      footer={
        <>
          <Button type="button" variant="outline" onClick={() => changeOpen(false)}>
            Cancel
          </Button>
          <Button type="submit" form="volume-form" disabled={!canCreate}>
            {busy ? "Creating…" : "Create"}
          </Button>
        </>
      }
    >
      <form
        id="volume-form"
        className="grid gap-1.5"
        onSubmit={(e) => {
          e.preventDefault();
          if (canCreate) void create();
        }}
      >
        <Label htmlFor="vol-name">Name</Label>
        <Input
          id="vol-name"
          placeholder="my-volume"
          autoComplete="off"
          spellCheck={false}
          className={cn("font-mono", nameError && "border-destructive focus-visible:ring-destructive")}
          aria-invalid={nameError !== ""}
          aria-describedby="vol-name-hint"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <p id="vol-name-hint" className={cn("text-xs", nameError ? "text-destructive" : "text-muted-foreground")}>
          {nameError || "Letters, digits, dots, dashes and underscores."}
        </p>
      </form>
    </ResponsiveDialog>
  );
}

// VolumesSection is Settings → Docker → Volumes: Winterflow apps' volumes
// first, then the rest of the host's, anonymous ones behind a switch. Any
// volume no container mounts can be deleted, after a confirmation.
export function VolumesSection({ docker }: { docker: Docker }) {
  const { apps } = useApps();
  const toServer = useServerPath();
  const [items, setItems] = useState<Volume[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [showAnon, setShowAnon] = useState(false);
  const [creating, setCreating] = useState(false);
  const [toDelete, setToDelete] = useState<Volume | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setItems(await docker.listVolumes());
    } catch (e) {
      toast.error("Couldn't load volumes", { description: errorText(e) });
    } finally {
      setLoading(false);
    }
  }, [docker]);

  useEffect(() => {
    void load();
  }, [load]);

  // A failed delete lists again (the list may be stale: a container started
  // using the volume since it loaded) and keeps the confirmation open.
  const remove = async (name: string) => {
    try {
      await docker.deleteVolume(name);
      toast.success("Volume deleted");
      await load();
    } catch (e) {
      toast.error("Couldn't delete the volume", { description: errorText(e) });
      await load();
      throw e;
    }
  };

  const appNames = useMemo(() => new Map(apps.map((a) => [a.id, a.name])), [apps]);
  const all = items ?? [];
  const anonCount = all.filter((v) => v.anonymous).length;
  const visible = all.filter((v) => showAnon || !v.anonymous);
  const groups = groupVolumes(visible);
  const unusedByApps = visible.filter((v) => v.orphan).length;

  return (
    <Section
      title="Volumes"
      description="Where containers keep data that outlives a redeploy."
      actions={
        <>
          <RefreshButton label="Refresh volumes" loading={loading} onClick={() => void load()} />
          <Button size="sm" variant="outline" onClick={() => setCreating(true)}>
            <Plus className="size-4" /> Create volume
          </Button>
        </>
      }
    >
      {items === null ? (
        <ListPlaceholder loading={loading} failed="Couldn't load volumes." onRetry={() => void load()} />
      ) : (
        <div className="space-y-3">
          <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 text-sm">
            <p className="text-muted-foreground">
              {visible.length === 1 ? "1 volume" : `${visible.length} volumes`}
              {unusedByApps > 0 ? <span className="text-foreground">{` · ${unusedByApps} not used by any app`}</span> : null}
            </p>
            {anonCount > 0 ? (
              <div className="flex items-center gap-2">
                <Switch id="vol-show-anon" checked={showAnon} onCheckedChange={setShowAnon} />
                <Label htmlFor="vol-show-anon" className="font-normal text-muted-foreground">
                  Show {anonCount} anonymous
                </Label>
              </div>
            ) : null}
          </div>
          {unusedByApps > 0 ? (
            <p className="text-xs text-muted-foreground">
              A volume not used by any app carries a Winterflow app label, but that app isn't on this server: it was
              deleted, or the volume belongs to another Winterflow install on the same Docker host. Check before deleting.
            </p>
          ) : null}
          {visible.length === 0 ? (
            <EmptyState
              title={
                all.length > 0
                  ? "Only anonymous volumes here. Turn on “Show anonymous” to list them."
                  : "No volumes yet. Apps create theirs when they first deploy."
              }
            />
          ) : (
            groups.map(({ label, volumes }) => (
              <div key={label} className="space-y-1.5">
                {groups.length > 1 ? <h3 className="px-1 text-xs font-medium text-muted-foreground">{label}</h3> : null}
                <RowList label={label}>
                  {volumes.map((v) => {
                    const state = volumeState(v);
                    return (
                      <Row
                        key={v.name}
                        title={
                          <span className="font-mono" title={v.name}>
                            {shortVolumeName(v)}
                          </span>
                        }
                        description={ownerOf(v, v.app_id ? appNames.get(v.app_id) : undefined, (id) => toServer(appSub(id)))}
                        meta={
                          <span className="inline-flex items-center gap-1.5 whitespace-nowrap">
                            <StatusDot tone={state.tone} />
                            {state.label}
                          </span>
                        }
                        actions={
                          // Docker refuses to remove a mounted volume; don't offer it.
                          v.in_use ? null : <RemoveButton label={`Delete volume ${v.name}`} onClick={() => setToDelete(v)} />
                        }
                      />
                    );
                  })}
                </RowList>
              </div>
            ))
          )}
        </div>
      )}
      <CreateVolumeDialog docker={docker} open={creating} onOpenChange={setCreating} onCreated={load} />
      <ConfirmDialog
        open={toDelete !== null}
        onOpenChange={(o) => !o && setToDelete(null)}
        title="Delete this volume?"
        description={
          <>
            <code className="break-all font-mono text-foreground">{toDelete?.name}</code> and all the data in it are
            erased for good. This can't be undone.
          </>
        }
        confirmLabel="Delete volume"
        pendingLabel="Deleting…"
        onConfirm={() => (toDelete ? remove(toDelete.name) : undefined)}
      />
    </Section>
  );
}
```

Create `web/src/pages/settings-docker.tsx`:

```tsx
import { useCallback, useEffect, useState } from "react";
import { Plus } from "lucide-react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { ListPlaceholder } from "@/components/list-placeholder";
import { PageHeader } from "@/components/page-header";
import { RefreshButton } from "@/components/refresh-button";
import { RemoveButton } from "@/components/remove-button";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { Row, RowList, Section } from "@/components/section";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { Docker, Network, Registry } from "@/context/use-docker";
import { VolumesSection } from "@/pages/settings-volumes";

const errorText = (e: unknown) => (e instanceof Error ? e.message : undefined);

// Built-in networks can't be removed: no removal is offered for them.
const BUILTIN_NETWORKS = new Set(["bridge", "host", "none"]);

// useResourceList lists one kind of Docker resource and lists it again on
// demand; a failure keeps the last list and says what went wrong.
function useResourceList<T>(fetchList: () => Promise<T[]>, what: string) {
  const [items, setItems] = useState<T[] | null>(null);
  const [loading, setLoading] = useState(true);
  const reload = useCallback(async () => {
    setLoading(true);
    try {
      setItems(await fetchList());
    } catch (e) {
      toast.error(`Couldn't load ${what}`, { description: errorText(e) });
    } finally {
      setLoading(false);
    }
  }, [fetchList, what]);
  useEffect(() => {
    void reload();
  }, [reload]);
  return { items, loading, reload };
}

function AddRegistryDialog({
  docker,
  open,
  onOpenChange,
  onAdded,
}: {
  docker: Docker;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onAdded: () => Promise<void>;
}) {
  const [form, setForm] = useState({ address: "", username: "", password: "" });
  const [busy, setBusy] = useState(false);
  const canAdd = form.address.trim() !== "" && form.username.trim() !== "" && !busy;

  // The typed password must not outlive the dialog.
  const changeOpen = (next: boolean) => {
    if (!next) setForm({ address: "", username: "", password: "" });
    onOpenChange(next);
  };

  const add = async () => {
    setBusy(true);
    try {
      // useDocker encrypts the password with the server's key.
      await docker.createRegistry(form.address.trim(), form.username.trim(), form.password);
      toast.success("Registry added");
      changeOpen(false);
      await onAdded();
    } catch (e) {
      toast.error("Couldn't add the registry", { description: errorText(e) });
    } finally {
      setBusy(false);
    }
  };

  const field = (key: keyof typeof form, id: string, label: string, props: { type?: string; placeholder?: string } = {}) => (
    <div className="grid gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        type={props.type ?? "text"}
        placeholder={props.placeholder}
        autoComplete="off"
        spellCheck={false}
        value={form[key]}
        onChange={(e) => setForm((f) => ({ ...f, [key]: e.target.value }))}
      />
    </div>
  );

  return (
    <ResponsiveDialog
      open={open}
      onOpenChange={changeOpen}
      title="Add registry"
      description="The password is encrypted in your browser; only this server can read it."
      footer={
        <>
          <Button type="button" variant="outline" onClick={() => changeOpen(false)}>
            Cancel
          </Button>
          <Button type="submit" form="registry-form" disabled={!canAdd}>
            {busy ? "Adding…" : "Add"}
          </Button>
        </>
      }
    >
      <form
        id="registry-form"
        className="grid gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          if (canAdd) void add();
        }}
      >
        {field("address", "reg-addr", "Address", { placeholder: "registry.example.com" })}
        {field("username", "reg-user", "Username")}
        {field("password", "reg-pass", "Password", { type: "password" })}
      </form>
    </ResponsiveDialog>
  );
}

function RegistriesSection({ docker }: { docker: Docker }) {
  const { items, loading, reload } = useResourceList<Registry>(docker.listRegistries, "registries");
  const [adding, setAdding] = useState(false);
  const [toRemove, setToRemove] = useState<Registry | null>(null);

  const remove = async (address: string) => {
    try {
      await docker.deleteRegistry(address);
      toast.success("Registry removed");
      await reload();
    } catch (e) {
      toast.error("Couldn't remove the registry", { description: errorText(e) });
      throw e;
    }
  };

  return (
    <Section
      title="Registries"
      description="Private image registries apps on this server can pull from."
      actions={
        <>
          <RefreshButton label="Refresh registries" loading={loading} onClick={() => void reload()} />
          <Button size="sm" variant="outline" onClick={() => setAdding(true)}>
            <Plus className="size-4" /> Add registry
          </Button>
        </>
      }
    >
      {items === null ? (
        <ListPlaceholder loading={loading} failed="Couldn't load registries." onRetry={() => void reload()} />
      ) : items.length === 0 ? (
        <EmptyState title="No registries. Add one to pull private images." />
      ) : (
        <RowList label="Registries">
          {items.map((r) => (
            <Row
              key={r.address}
              title={<span className="font-mono">{r.address}</span>}
              actions={<RemoveButton label={`Remove registry ${r.address}`} onClick={() => setToRemove(r)} />}
            />
          ))}
        </RowList>
      )}
      <AddRegistryDialog docker={docker} open={adding} onOpenChange={setAdding} onAdded={reload} />
      <ConfirmDialog
        open={toRemove !== null}
        onOpenChange={(o) => !o && setToRemove(null)}
        title={`Remove ${toRemove?.address ?? "this registry"}?`}
        description="Apps on this server can't pull private images from it until you add it again."
        confirmLabel="Remove registry"
        pendingLabel="Removing…"
        onConfirm={() => (toRemove ? remove(toRemove.address) : undefined)}
      />
    </Section>
  );
}

function CreateNetworkDialog({
  docker,
  open,
  onOpenChange,
  onCreated,
}: {
  docker: Docker;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: () => Promise<void>;
}) {
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const canCreate = name.trim() !== "" && !busy;

  const changeOpen = (next: boolean) => {
    if (!next) setName("");
    onOpenChange(next);
  };

  const create = async () => {
    setBusy(true);
    try {
      await docker.createNetwork(name.trim());
      toast.success("Network created");
      changeOpen(false);
      await onCreated();
    } catch (e) {
      toast.error("Couldn't create the network", { description: errorText(e) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <ResponsiveDialog
      open={open}
      onOpenChange={changeOpen}
      title="Add network"
      description="A network apps can join to reach each other by name."
      footer={
        <>
          <Button type="button" variant="outline" onClick={() => changeOpen(false)}>
            Cancel
          </Button>
          <Button type="submit" form="network-form" disabled={!canCreate}>
            {busy ? "Creating…" : "Create"}
          </Button>
        </>
      }
    >
      <form
        id="network-form"
        className="grid gap-1.5"
        onSubmit={(e) => {
          e.preventDefault();
          if (canCreate) void create();
        }}
      >
        <Label htmlFor="net-name">Name</Label>
        <Input
          id="net-name"
          placeholder="my-network"
          autoComplete="off"
          spellCheck={false}
          className="font-mono"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
      </form>
    </ResponsiveDialog>
  );
}

function NetworksSection({ docker }: { docker: Docker }) {
  const { items, loading, reload } = useResourceList<Network>(docker.listNetworks, "networks");
  const [creating, setCreating] = useState(false);
  const [toRemove, setToRemove] = useState<Network | null>(null);

  const remove = async (name: string) => {
    try {
      await docker.deleteNetwork(name);
      toast.success("Network removed");
      await reload();
    } catch (e) {
      toast.error("Couldn't remove the network", { description: errorText(e) });
      throw e;
    }
  };

  return (
    <Section
      title="Networks"
      description="Docker networks on this server."
      actions={
        <>
          <RefreshButton label="Refresh networks" loading={loading} onClick={() => void reload()} />
          <Button size="sm" variant="outline" onClick={() => setCreating(true)}>
            <Plus className="size-4" /> Add network
          </Button>
        </>
      }
    >
      {items === null ? (
        <ListPlaceholder loading={loading} failed="Couldn't load networks." onRetry={() => void reload()} />
      ) : items.length === 0 ? (
        <EmptyState title="No networks yet." />
      ) : (
        <RowList label="Networks">
          {items.map((n) => (
            <Row
              key={n.id ?? n.name}
              title={<span className="font-mono">{n.name}</span>}
              description={[n.driver, n.scope].filter(Boolean).join(" · ") || undefined}
              actions={
                BUILTIN_NETWORKS.has(n.name) ? null : (
                  <RemoveButton label={`Remove network ${n.name}`} onClick={() => setToRemove(n)} />
                )
              }
            />
          ))}
        </RowList>
      )}
      <CreateNetworkDialog docker={docker} open={creating} onOpenChange={setCreating} onCreated={reload} />
      <ConfirmDialog
        open={toRemove !== null}
        onOpenChange={(o) => !o && setToRemove(null)}
        title={`Remove network ${toRemove?.name ?? ""}?`}
        description="Containers still attached to it keep Docker from removing it."
        confirmLabel="Remove network"
        pendingLabel="Removing…"
        onConfirm={() => (toRemove ? remove(toRemove.name) : undefined)}
      />
    </Section>
  );
}

// DockerSettings is Settings → Server → Docker: the server's registries,
// networks and volumes as sections of one page.
export function DockerSettings({ docker }: { docker: Docker }) {
  return (
    <div className="space-y-8">
      <PageHeader title="Docker" subtitle="Registries, networks and volumes on this server." />
      <RegistriesSection docker={docker} />
      <NetworksSection docker={docker} />
      <VolumesSection docker={docker} />
    </div>
  );
}
```

In `web/src/pages/server-settings.tsx`:
- replace the whole `ServerDockerPage` function with

```tsx
// ServerDockerPage is the server's Docker resources (registries, networks,
// volumes); it starts over on another server.
export function ServerDockerPage() {
  const docker = useDocker();
  const { activeServer } = useServers();
  if (!docker.hasServer || !activeServer) return <NoServer />;
  return <DockerSettings key={activeServer.id} docker={docker} />;
}
```

- delete the functions `RegistriesCard` and `NetworksCard` (now in `settings-docker.tsx`);
- replace `import { VolumesCard } from "@/pages/settings-volumes";` with `import { DockerSettings } from "@/pages/settings-docker";`;
- remove the imports left unused (`tsc -b` lists them): `Plus`, `Trash2`, `useCallback`, `useEffect`, `Card*`, `Table*`, `Dialog*`, `Spinner`, `Network`, `Registry`. `AgentCard` still uses `useState`, `toast`, `Button`, `Input`, `Label`, `Docker` until Task 3.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/volumes.test.ts src/pages/settings-docker.test.tsx src/pages/settings-volumes.test.tsx src/pages/server-settings.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/volumes.ts web/src/lib/volumes.test.ts web/src/pages/settings-docker.tsx web/src/pages/settings-docker.test.tsx web/src/pages/settings-volumes.tsx web/src/pages/settings-volumes.test.tsx web/src/pages/server-settings.tsx web/src/pages/server-settings.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): Settings → Docker as one page, every delete confirmed

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Server → General — the server's facts, status and agent update

**Files:**
- Create: `web/src/lib/server-facts.ts`, `web/src/lib/server-facts.test.ts`
- Rewrite: `web/src/pages/server-settings.tsx`, `web/src/pages/server-settings.test.tsx`

**Interfaces:**
- Consumes: phase 1 `serverStatus({ liveness, lastSeenAt, standalone })` (`@/lib/status`), `StatusPill` (`@/components/status`), `PageHeader`, `Section`, `serverSettingsPath`; `useServers()` (`activeServer`, `statusByServer`); `useDocker()` (`hasServer`, `updateAgent(version)`); `isStandalone` (`@/config`); `formatBytes` (`@/lib/utils`); `DockerSettings` (Task 2).
- Produces: `serverFacts(caps: Record<string, string>): { label: string; value: string }[]`, `versionLabel(version: string): string` (`@/lib/server-facts`); `ServerGeneralPage` (facts + "Updates"). `ServerDomainsPage` and `ServerStoragesPage` keep phase 1's bodies (`DomainsCard`, `StoragesCard`) until Tasks 4 and 6.

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/server-facts.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import { serverFacts, versionLabel } from "@/lib/server-facts";

describe("serverFacts", () => {
  it("lists what the agent reported, in order", () => {
    expect(
      serverFacts({
        server_ip: "203.0.113.7",
        system_cpu_cores: "4",
        system_memory_total: String(16 * 1024 ** 3),
        system_disk_total: String(512 * 1024 ** 3),
        version: "1.2.3",
      }),
    ).toEqual([
      { label: "IP address", value: "203.0.113.7" },
      { label: "CPU", value: "4 cores" },
      { label: "Memory", value: "16.0 GB" },
      { label: "Disk", value: "512.0 GB" },
    ]);
  });

  it("leaves out what was not reported, and says one core", () => {
    expect(serverFacts({})).toEqual([]);
    expect(serverFacts({ system_cpu_cores: "1", system_memory_total: "" })).toEqual([{ label: "CPU", value: "1 core" }]);
  });
});

describe("versionLabel", () => {
  it("writes a version with one v", () => {
    expect(versionLabel("1.2.3")).toBe("v1.2.3");
    expect(versionLabel("v261013.2")).toBe("v261013.2");
    expect(versionLabel(" ")).toBe("");
  });
});
```

Replace `web/src/pages/server-settings.test.tsx` with:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ServerDockerPage, ServerDomainsPage, ServerGeneralPage, ServerStoragesPage } from "@/pages/server-settings";

type FakeServer = {
  id: string;
  name: string;
  lastSeenAt: string | null;
  capabilities: Record<string, string>;
  features?: Record<string, boolean>;
};

const ALPHA: FakeServer = {
  id: "s1",
  name: "Alpha",
  lastSeenAt: "2026-10-13T09:00:00Z",
  capabilities: {
    server_ip: "203.0.113.7",
    system_cpu_cores: "4",
    system_memory_total: String(16 * 1024 ** 3),
    system_disk_total: String(512 * 1024 ** 3),
    version: "1.2.3",
  },
  features: { ingress: true },
};

const state = vi.hoisted(() => ({ server: null as FakeServer | null, online: true }));
const docker = vi.hoisted(() => ({ hasServer: true, updateAgent: vi.fn() }));
const toastSuccess = vi.hoisted(() => vi.fn());
const toastError = vi.hoisted(() => vi.fn());

vi.mock("@/context/use-servers", () => ({
  useServers: () => ({
    activeServer: state.server,
    statusByServer: state.server && state.online ? { [state.server.id]: "online" } : {},
  }),
}));
vi.mock("@/context/use-docker", () => ({ useDocker: () => docker }));
vi.mock("sonner", () => ({ toast: { success: toastSuccess, error: toastError } }));
vi.mock("@/pages/settings-domains", () => ({ DomainsCard: () => <p>domains page</p> }));
vi.mock("@/pages/settings-storages", () => ({ StoragesCard: () => <p>storages page</p> }));
vi.mock("@/pages/settings-docker", () => ({ DockerSettings: () => <p>docker page</p> }));

function renderPage(page: ReactElement) {
  return render(
    <MemoryRouter initialEntries={["/server/s1/settings/x"]}>
      <Routes>
        <Route path="/server/s1/settings/x" element={page} />
        <Route path="/server/s1/settings/general" element={<p>general page</p>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("server settings pages", () => {
  beforeEach(() => {
    state.server = ALPHA;
    state.online = true;
    docker.hasServer = true;
    docker.updateAgent.mockReset();
    toastSuccess.mockReset();
    toastError.mockReset();
  });

  it("shows the server's facts and status on General", () => {
    renderPage(<ServerGeneralPage />);
    expect(screen.getByRole("heading", { level: 1, name: "General" })).toBeInTheDocument();
    expect(screen.getByText("Alpha")).toBeInTheDocument();
    expect(screen.getByText("Online")).toBeInTheDocument();
    expect(screen.getByText("203.0.113.7")).toBeInTheDocument();
    expect(screen.getByText("4 cores")).toBeInTheDocument();
    expect(screen.getByText("16.0 GB")).toBeInTheDocument();
    expect(screen.getByText("512.0 GB")).toBeInTheDocument();
    expect(screen.getByText("Running v1.2.3.")).toBeInTheDocument();
  });

  it("says Offline once the agent is gone, and leaves out what it never reported", () => {
    state.online = false;
    state.server = { ...ALPHA, capabilities: {} };
    renderPage(<ServerGeneralPage />);
    expect(screen.getByText("Offline")).toBeInTheDocument();
    expect(screen.queryByText("IP address")).not.toBeInTheDocument();
    expect(screen.getByText("The agent hasn't reported its version yet.")).toBeInTheDocument();
  });

  it("updates the agent to a typed version", async () => {
    const user = userEvent.setup({ delay: null });
    docker.updateAgent.mockResolvedValue(undefined);
    renderPage(<ServerGeneralPage />);
    const update = screen.getByRole("button", { name: "Update" });
    expect(update).toBeDisabled();
    await user.type(screen.getByLabelText("Version"), " 1.2.4 ");
    await user.click(update);
    expect(docker.updateAgent).toHaveBeenCalledWith("1.2.4");
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith("Update started", {
        description: "The agent restarts on the new version in a moment.",
      }),
    );
    expect(screen.getByLabelText("Version")).toHaveValue("");
  });

  it("keeps the version when the update is refused", async () => {
    const user = userEvent.setup({ delay: null });
    docker.updateAgent.mockRejectedValue(new Error("a restore is running"));
    renderPage(<ServerGeneralPage />);
    await user.type(screen.getByLabelText("Version"), "1.2.4");
    await user.click(screen.getByRole("button", { name: "Update" }));
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith("Couldn't update the agent", { description: "a restore is running" }),
    );
    expect(screen.getByLabelText("Version")).toHaveValue("1.2.4");
  });

  it("shows Domains on a server with ingress", () => {
    renderPage(<ServerDomainsPage />);
    expect(screen.getByText("domains page")).toBeInTheDocument();
  });

  it("opens General instead of Domains on a server without ingress", async () => {
    state.server = { ...ALPHA, features: {} };
    renderPage(<ServerDomainsPage />);
    expect(await screen.findByText("general page")).toBeInTheDocument();
  });

  it("shows Storages", () => {
    renderPage(<ServerStoragesPage />);
    expect(screen.getByText("storages page")).toBeInTheDocument();
  });

  it("shows the Docker resources", () => {
    renderPage(<ServerDockerPage />);
    expect(screen.getByText("docker page")).toBeInTheDocument();
  });

  it("asks for a server when there is none", () => {
    docker.hasServer = false;
    state.server = null;
    renderPage(<ServerGeneralPage />);
    expect(screen.getByText("Select a server to manage its settings.")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/server-facts.test.ts src/pages/server-settings.test.tsx`
Expected: FAIL — `@/lib/server-facts` does not exist; General has no facts and its button is "Update agent".

- [ ] **Step 3: Implement**

Create `web/src/lib/server-facts.ts`:

```ts
import { formatBytes } from "@/lib/utils";

export type ServerFact = { label: string; value: string };

// serverFacts lists what a server's agent reported about its machine, in
// the order Settings → Server → General shows them; an unreported fact is
// left out (never shown as 0).
export function serverFacts(caps: Record<string, string>): ServerFact[] {
  const facts: ServerFact[] = [];
  if (caps.server_ip) facts.push({ label: "IP address", value: caps.server_ip });
  const cores = Number(caps.system_cpu_cores);
  if (Number.isFinite(cores) && cores > 0) {
    facts.push({ label: "CPU", value: cores === 1 ? "1 core" : `${cores} cores` });
  }
  const memory = formatBytes(caps.system_memory_total);
  if (memory) facts.push({ label: "Memory", value: memory });
  const disk = formatBytes(caps.system_disk_total);
  if (disk) facts.push({ label: "Disk", value: disk });
  return facts;
}

// versionLabel writes an agent version the way the UI shows it, "v1.2.3",
// whether the agent reported "1.2.3" or "v1.2.3"; "" stays "".
export function versionLabel(version: string): string {
  const v = version.trim();
  if (!v) return "";
  return /^v/i.test(v) ? v : `v${v}`;
}
```

Replace `web/src/pages/server-settings.tsx` with:

```tsx
import { useState, type ReactNode } from "react";
import { Navigate } from "react-router-dom";
import { toast } from "sonner";

import { PageHeader } from "@/components/page-header";
import { Section } from "@/components/section";
import { StatusPill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { isStandalone } from "@/config";
import { useDocker, type Docker } from "@/context/use-docker";
import { useServers } from "@/context/use-servers";
import { serverSettingsPath } from "@/lib/routes";
import { serverFacts, versionLabel } from "@/lib/server-facts";
import { serverStatus } from "@/lib/status";
import { DockerSettings } from "@/pages/settings-docker";
import { DomainsCard } from "@/pages/settings-domains";
import { StoragesCard } from "@/pages/settings-storages";

// Settings → Server: one page per section (the sub-menu is SettingsLayout's).
// Each addresses the server in the URL through useDocker / useServers; the
// page bodies render their own PageHeader.

function NoServer() {
  return <p className="text-sm text-muted-foreground">Select a server to manage its settings.</p>;
}

// Fact is one line of the server's facts: the label left, the value right;
// stacked on a phone.
function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5 px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
      <dt className="text-sm text-muted-foreground">{label}</dt>
      <dd className="text-sm font-medium sm:text-right">{children}</dd>
    </div>
  );
}

// AgentUpdate asks the agent to update itself to a version (the API has no
// latest-version lookup yet: the version is typed).
function AgentUpdate({ docker, running }: { docker: Docker; running: string }) {
  const [version, setVersion] = useState("");
  const [busy, setBusy] = useState(false);
  const target = version.trim();

  const update = async () => {
    setBusy(true);
    try {
      await docker.updateAgent(target);
      toast.success("Update started", { description: "The agent restarts on the new version in a moment." });
      setVersion("");
    } catch (e) {
      toast.error("Couldn't update the agent", { description: e instanceof Error ? e.message : undefined });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Section
      title="Updates"
      description={running ? `Running ${versionLabel(running)}.` : "The agent hasn't reported its version yet."}
    >
      <form
        className="flex flex-col gap-2 sm:flex-row sm:items-end"
        onSubmit={(e) => {
          e.preventDefault();
          if (target && !busy) void update();
        }}
      >
        <div className="grid flex-1 gap-1.5 sm:max-w-xs">
          <Label htmlFor="agent-version">Version</Label>
          <Input
            id="agent-version"
            placeholder="e.g. 1.2.3"
            autoComplete="off"
            spellCheck={false}
            value={version}
            onChange={(e) => setVersion(e.target.value)}
          />
        </div>
        <Button type="submit" disabled={busy || !target}>
          {busy ? "Updating…" : "Update"}
        </Button>
      </form>
      <p className="text-xs text-muted-foreground">
        The agent downloads that release, replaces itself and restarts. Only a newer version is applied.
      </p>
    </Section>
  );
}

// ServerGeneralPage is the server's General page: its name, whether its
// agent is connected, what the agent reported about the machine (read-only:
// the API has no rename) and the agent update.
export function ServerGeneralPage() {
  const docker = useDocker();
  const { activeServer, statusByServer } = useServers();
  if (!docker.hasServer || !activeServer) return <NoServer />;
  const caps = activeServer.capabilities ?? {};
  const status = serverStatus({
    liveness: statusByServer[activeServer.id],
    lastSeenAt: activeServer.lastSeenAt,
    standalone: isStandalone,
  });
  return (
    <div className="space-y-8">
      <PageHeader title="General" subtitle="This server and the agent that runs it." />
      <Section title="Server">
        <dl className="divide-y divide-border/70 rounded-xl border bg-card">
          <Fact label="Name">{activeServer.name}</Fact>
          <Fact label="Status">
            <StatusPill status={status} />
          </Fact>
          {serverFacts(caps).map((f) => (
            <Fact key={f.label} label={f.label}>
              {f.value}
            </Fact>
          ))}
        </dl>
      </Section>
      <AgentUpdate docker={docker} running={caps.version ?? ""} />
    </div>
  );
}

// ServerDomainsPage configures the embedded ingress. A server without it
// (switched to from another server's Domains page, or an old link) opens
// its General page instead.
export function ServerDomainsPage() {
  const docker = useDocker();
  const { activeServer } = useServers();
  if (!docker.hasServer || !activeServer) return <NoServer />;
  if (!activeServer.features?.ingress) {
    return <Navigate replace to={serverSettingsPath(activeServer.id)} />;
  }
  return (
    <div className="space-y-6">
      <PageHeader title="Domains" subtitle={activeServer.name} />
      <DomainsCard docker={docker} />
    </div>
  );
}

export function ServerStoragesPage() {
  const docker = useDocker();
  const { activeServer } = useServers();
  if (!docker.hasServer || !activeServer) return <NoServer />;
  return (
    <div className="space-y-6">
      <PageHeader title="Storages" subtitle={activeServer.name} />
      <StoragesCard docker={docker} />
    </div>
  );
}

// ServerDockerPage is the server's Docker resources (registries, networks,
// volumes); it starts over on another server.
export function ServerDockerPage() {
  const docker = useDocker();
  const { activeServer } = useServers();
  if (!docker.hasServer || !activeServer) return <NoServer />;
  return <DockerSettings key={activeServer.id} docker={docker} />;
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/server-facts.test.ts src/pages/server-settings.test.tsx src/routes.test.tsx`
Expected: PASS (the route table still mounts the four pages).

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/server-facts.ts web/src/lib/server-facts.test.ts web/src/pages/server-settings.tsx web/src/pages/server-settings.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): Settings → Server → General with the server's facts, status and update

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 4: Server → Domains — flat rows, plain HTTPS choices, expiry only when it matters

**Files:**
- Modify: `web/src/lib/domains.ts`, `web/src/lib/domains.test.ts` (`TLS_MODES` wording, `expiryNote`)
- Rewrite: `web/src/components/domain-dialog.tsx` (on `ResponsiveDialog`)
- Rewrite: `web/src/pages/settings-domains.tsx` (`DomainsCard` → `DomainsSettings`), `web/src/pages/settings-domains.test.tsx`
- Modify: `web/src/pages/server-settings.tsx` (`ServerDomainsPage`), `web/src/pages/server-settings.test.tsx`

**Interfaces:**
- Consumes: Task 1 (`RowMenu`, `RefreshButton`, `ListPlaceholder`, `ConfirmDialog` with `onCloseAutoFocus`, `ResponsiveDialog` with `onCloseAutoFocus`); phase 1 `PageHeader`, `RowList`, `Row`, `EmptyState`; `AppLinks` (`@/components/app-links`), `returnFocusTo` (`@/lib/focus`), `Docker.listDomains/createDomain/deleteDomain`, `useApps`.
- Produces:
  - `TLS_MODES[mode]` gains `state` ("Automatic HTTPS" | "Own certificate" | "HTTPS off") and `choice` ("Automatic (Let's Encrypt)" | "My own certificate" | "Off — behind Cloudflare Tunnel or another proxy"); `label` and `summary` stay (the app editor's `TLSBadge` and host hints still use them).
  - `expiryNote(d: Pick<RegisteredDomain, "tls" | "not_after">, now?: Date): { text: string; expired: boolean } | null` — only for a certificate within 30 days of its end or past it.
  - `DomainsSettings({ docker }: { docker: Docker })` (`@/pages/settings-domains`) — the whole Domains page with its `PageHeader`. `DomainDialog` keeps its props.

- [ ] **Step 1: Write the failing tests**

Append to `web/src/lib/domains.test.ts` (and add `expiryNote` to its `import { … } from "@/lib/domains"`):

```ts
describe("expiryNote", () => {
  const now = new Date("2026-10-13T00:00:00Z");

  it("speaks only when a certificate is close to its end or past it", () => {
    expect(expiryNote({ tls: "certificate", not_after: "2026-12-31T00:00:00Z" }, now)).toBeNull();
    expect(expiryNote({ tls: "certificate", not_after: "2026-10-23T00:00:00Z" }, now)).toEqual({
      text: "Expires in 10 days",
      expired: false,
    });
    expect(expiryNote({ tls: "certificate", not_after: "2026-10-14T00:00:00Z" }, now)).toEqual({
      text: "Expires in 1 day",
      expired: false,
    });
    expect(expiryNote({ tls: "certificate", not_after: "2026-10-01T00:00:00Z" }, now)).toEqual({
      text: "Expired 2026-10-01",
      expired: true,
    });
  });

  it("says nothing for a mode that renews itself or has no date", () => {
    expect(expiryNote({ tls: "auto", not_after: "2026-10-01T00:00:00Z" }, now)).toBeNull();
    expect(expiryNote({ tls: "certificate", not_after: null }, now)).toBeNull();
  });

  it("words each HTTPS mode plainly", () => {
    expect(TLS_MODES.auto.choice).toBe("Automatic (Let's Encrypt)");
    expect(TLS_MODES.certificate.choice).toBe("My own certificate");
    expect(TLS_MODES.off.choice).toBe("Off — behind Cloudflare Tunnel or another proxy");
    expect([TLS_MODES.auto.state, TLS_MODES.certificate.state, TLS_MODES.off.state]).toEqual([
      "Automatic HTTPS",
      "Own certificate",
      "HTTPS off",
    ]);
  });
});
```

Replace `web/src/pages/settings-domains.test.tsx` with:

```tsx
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";
import type { Docker } from "@/context/use-docker";
import { DomainDialog } from "@/components/domain-dialog";
import type { Domain, DomainTLS } from "@/lib/domains";
import { DomainsSettings } from "@/pages/settings-domains";

// Links point into the active server's scope (/server/:id/...).
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));

const toastError = vi.hoisted(() => vi.fn());
const toastSuccess = vi.hoisted(() => vi.fn());
const toastWarning = vi.hoisted(() => vi.fn());
vi.mock("sonner", () => ({ toast: { success: toastSuccess, error: toastError, warning: toastWarning } }));

// No per-keystroke delay: typing stays fast under a loaded CI runner.
const setupUser = () => userEvent.setup({ delay: null });

// pastePEM fills a PEM textarea in one paste, as users do.
async function pastePEM(user: ReturnType<typeof setupUser>, field: HTMLElement, value: string) {
  await user.click(field);
  await user.paste(value);
}

const day = 24 * 60 * 60 * 1000;
const inDays = (n: number) => new Date(Date.now() + n * day).toISOString();

const OFF = "Off — behind Cloudflare Tunnel or another proxy";
const OWN = "My own certificate";
const AUTO = "Automatic (Let's Encrypt)";

const DOMAINS: Domain[] = [
  { name: "*.twillingate.app", tls: "certificate", not_after: inDays(10), issuer: "CF Origin", apps: ["a1", "a2"] },
  { name: "old.example.com", tls: "certificate", not_after: inDays(-1), apps: [] },
  { name: "tunnel.example.com", tls: "off", apps: [] },
  { name: "example.com", tls: "auto", apps: ["a1"] },
  { name: "far.example.com", tls: "certificate", not_after: "2099-01-31T00:00:00Z", apps: [] },
];

function fakeDocker(over: Partial<Docker> = {}): Docker {
  return {
    listDomains: vi.fn().mockResolvedValue(DOMAINS),
    // The agent answers a create with the stored registration.
    createDomain: vi.fn(async (name: string, tls: DomainTLS) => ({ domain: { name, tls, apps: [] }, warnings: [] })),
    deleteDomain: vi.fn().mockResolvedValue([]),
    ...over,
  } as unknown as Docker;
}

function renderPage(docker: Docker) {
  const apps = {
    apps: [
      { id: "a1", name: "Shop" },
      { id: "a2", name: "Blog" },
    ],
  } as unknown as AppsContextValue;
  return render(
    <MemoryRouter>
      <AppsContext.Provider value={apps}>
        <DomainsSettings docker={docker} />
      </AppsContext.Provider>
    </MemoryRouter>,
  );
}

// openMenu opens a domain row's "⋯" menu and picks an item.
async function pick(user: ReturnType<typeof setupUser>, name: string, item: string) {
  await user.click(await screen.findByRole("button", { name: `Actions for ${name}` }));
  await user.click(await screen.findByRole("menuitem", { name: item }));
}

describe("DomainsSettings", () => {
  beforeEach(() => {
    toastError.mockReset();
    toastSuccess.mockReset();
    toastWarning.mockReset();
  });

  it("lists names, HTTPS in plain words, expiry only when it matters, and the apps using each", async () => {
    renderPage(fakeDocker());
    expect(screen.getByRole("heading", { level: 1, name: "Domains" })).toBeInTheDocument();
    expect(await screen.findByText("*.twillingate.app")).toHaveClass("font-mono");
    expect(screen.getAllByText("Own certificate")).toHaveLength(3);
    expect(screen.getByText("HTTPS off")).toBeInTheDocument();
    expect(screen.getByText("Automatic HTTPS")).toBeInTheDocument();
    const soon = screen.getByText("Expires in 10 days");
    expect(soon).toHaveClass("text-status-warn");
    expect(soon).toHaveAttribute("title", "Issued by CF Origin");
    expect(screen.getByText(/^Expired /)).toHaveClass("text-status-bad");
    // A certificate far from its end says nothing about it.
    expect(screen.queryByText(/2099/)).not.toBeInTheDocument();
    expect(screen.getByText(/2 apps:/)).toBeInTheDocument();
    expect(screen.getAllByRole("link", { name: "Shop" })[0]).toHaveAttribute("href", "/server/srv-1/app/a1");
    expect(screen.getByRole("link", { name: "Blog" })).toHaveAttribute("href", "/server/srv-1/app/a2");
    expect(screen.getAllByText("Not used")).toHaveLength(3);
  });

  it("adds a domain behind a tunnel", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderPage(docker);

    await user.click(await screen.findByRole("button", { name: /Add domain/ }));
    const dialog = await screen.findByRole("dialog");
    const submit = within(dialog).getByRole("button", { name: "Add" });
    expect(submit).toBeDisabled();

    await user.type(within(dialog).getByLabelText("Name"), "*.*.bad");
    expect(within(dialog).getByText(/Use a lowercase hostname/)).toBeInTheDocument();
    expect(submit).toBeDisabled();

    await user.clear(within(dialog).getByLabelText("Name"));
    await user.type(within(dialog).getByLabelText("Name"), "*.Tunnel.Example.com");
    await user.click(within(dialog).getByRole("radio", { name: OFF }));
    await user.click(submit);

    expect(docker.createDomain).toHaveBeenCalledWith("*.tunnel.example.com", "off", undefined);
    // The answer goes straight into the list, in name order: no new listing.
    const row = (await screen.findByText("*.tunnel.example.com")).closest("li");
    expect(row?.nextElementSibling).toHaveTextContent("*.twillingate.app");
    expect(docker.listDomains).toHaveBeenCalledTimes(1);
    expect(toastSuccess).toHaveBeenCalledWith("Domain added");
  });

  it("asks for a certificate and key when it is your own", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderPage(docker);

    await user.click(await screen.findByRole("button", { name: /Add domain/ }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), "*.secure.app");
    expect(within(dialog).queryByLabelText("Certificate (PEM)")).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole("radio", { name: OWN }));

    expect(within(dialog).getByText(/also serves the automatic-HTTPS addresses it covers/)).toBeInTheDocument();
    expect(within(dialog).queryByText(/Paste the certificate again/)).not.toBeInTheDocument();
    const submit = within(dialog).getByRole("button", { name: "Add" });
    await pastePEM(user, within(dialog).getByLabelText("Certificate (PEM)"), "CERT");
    expect(submit).toBeDisabled(); // the key is still missing
    await pastePEM(user, within(dialog).getByLabelText("Private key (PEM)"), "KEY");
    await user.click(submit);

    expect(docker.createDomain).toHaveBeenCalledWith("*.secure.app", "certificate", { certificate: "CERT", key: "KEY" });
  });

  it("replaces a certificate for an existing name", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderPage(docker);

    await pick(user, "*.twillingate.app", "Edit");
    const dialog = await screen.findByRole("dialog");
    // The name is shown, not editable: only the mode and certificate change.
    expect(within(dialog).queryByLabelText("Name")).not.toBeInTheDocument();
    expect(within(dialog).getByText("*.twillingate.app")).toBeInTheDocument();
    expect(within(dialog).getByRole("radio", { name: OWN })).toBeChecked();
    expect(within(dialog).getByText(/Paste the certificate again/)).toBeInTheDocument();
    await pastePEM(user, within(dialog).getByLabelText("Certificate (PEM)"), "NEW");
    await pastePEM(user, within(dialog).getByLabelText("Private key (PEM)"), "KEY");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(docker.createDomain).toHaveBeenCalledWith("*.twillingate.app", "certificate", { certificate: "NEW", key: "KEY" });
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith("Domain updated"));
  });

  it("edits the HTTPS mode of any registration", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderPage(docker);

    for (const d of DOMAINS) {
      expect(await screen.findByRole("button", { name: `Actions for ${d.name}` })).toBeInTheDocument();
    }
    await pick(user, "example.com", "Edit");
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("radio", { name: AUTO })).toBeChecked();
    expect(within(dialog).queryByLabelText("Certificate (PEM)")).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole("radio", { name: OFF }));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(docker.createDomain).toHaveBeenCalledWith("example.com", "off", undefined);
  });

  it("returns focus to what opened the dialog", async () => {
    const user = setupUser();
    renderPage(fakeDocker());
    const add = await screen.findByRole("button", { name: /Add domain/ });
    await user.click(add);
    await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(add).toHaveFocus());

    await pick(user, "example.com", "Edit");
    await screen.findByRole("dialog");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.getByRole("button", { name: "Actions for example.com" })).toHaveFocus());
  });

  it("shows the proxy's reload warnings after a create or a delete", async () => {
    const user = setupUser();
    const docker = fakeDocker({
      createDomain: vi.fn().mockResolvedValue({
        domain: { name: "x.example.com", tls: "auto", apps: [] },
        warnings: ["ingress reload failed (previous routing still active): boom"],
      }),
      deleteDomain: vi.fn().mockResolvedValue(["ingress: app-1 skipped"]),
    });
    renderPage(docker);

    await user.click(await screen.findByRole("button", { name: /Add domain/ }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), "x.example.com");
    await user.click(within(dialog).getByRole("button", { name: "Add" }));
    await waitFor(() =>
      expect(toastWarning).toHaveBeenCalledWith("Domain added, but the proxy reported a problem", {
        description: "ingress reload failed (previous routing still active): boom",
      }),
    );

    await pick(user, "tunnel.example.com", "Remove");
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Remove domain" }));
    await waitFor(() =>
      expect(toastWarning).toHaveBeenCalledWith("Domain removed, but the proxy reported a problem", {
        description: "ingress: app-1 skipped",
      }),
    );
    expect(toastSuccess).not.toHaveBeenCalled();
  });

  // The dialog is driven directly and reopened WITHOUT a remount, so only
  // the wipe-on-close (not the page's per-open key) can clear the PEMs.
  it("wipes the certificate and key when the dialog closes", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    function Harness() {
      const [open, setOpen] = useState(true);
      return (
        <>
          <button type="button" onClick={() => setOpen(true)}>
            reopen
          </button>
          <DomainDialog docker={docker} open={open} replace={DOMAINS[0]} onOpenChange={setOpen} onSaved={() => {}} />
        </>
      );
    }
    render(<Harness />);
    const cert = () => within(screen.getByRole("dialog")).getByLabelText("Certificate (PEM)");
    const key = () => within(screen.getByRole("dialog")).getByLabelText("Private key (PEM)");
    const closedAndGone = async () => {
      await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
      expect(screen.queryByDisplayValue("SECRET")).not.toBeInTheDocument();
    };
    const reopenEmpty = async () => {
      await user.click(screen.getByRole("button", { name: "reopen" }));
      await screen.findByRole("dialog");
      expect(cert()).toHaveValue("");
      expect(key()).toHaveValue("");
    };

    await pastePEM(user, cert(), "CERT");
    await pastePEM(user, key(), "SECRET");
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Cancel" }));
    await closedAndGone();
    await reopenEmpty();

    await pastePEM(user, key(), "SECRET");
    await user.keyboard("{Escape}");
    await closedAndGone();
    await reopenEmpty();

    await pastePEM(user, cert(), "NEW");
    await pastePEM(user, key(), "SECRET");
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Save" }));
    await closedAndGone();
    expect(docker.createDomain).toHaveBeenCalledWith("*.twillingate.app", "certificate", { certificate: "NEW", key: "SECRET" });
    await reopenEmpty();
  });

  it("keeps the dialog open with the name and mode when saving fails", async () => {
    const user = setupUser();
    const docker = fakeDocker({ createDomain: vi.fn().mockRejectedValue(new Error("certificate does not cover the name")) });
    renderPage(docker);

    await user.click(await screen.findByRole("button", { name: /Add domain/ }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), "*.secure.app");
    await user.click(within(dialog).getByRole("radio", { name: OWN }));
    await pastePEM(user, within(dialog).getByLabelText("Certificate (PEM)"), "CERT");
    await pastePEM(user, within(dialog).getByLabelText("Private key (PEM)"), "KEY");
    await user.click(within(dialog).getByRole("button", { name: "Add" }));

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith("Couldn't add domain", { description: "certificate does not cover the name" }),
    );
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(within(dialog).getByLabelText("Name")).toHaveValue("*.secure.app");
    expect(within(dialog).getByRole("radio", { name: OWN })).toBeChecked();
    expect(within(dialog).getByLabelText("Private key (PEM)")).toHaveValue("KEY");
  });

  it("removes a domain after confirmation and keeps it on a refusal", async () => {
    const user = setupUser();
    const docker = fakeDocker({
      deleteDomain: vi.fn().mockResolvedValueOnce([]).mockRejectedValueOnce(new Error("example.com is used by Shop")),
    });
    renderPage(docker);

    await pick(user, "tunnel.example.com", "Remove");
    const unused = await screen.findByRole("alertdialog");
    expect(unused).toHaveTextContent("Apps on this server will no longer be able to use tunnel.example.com.");
    expect(unused).not.toHaveTextContent("used by");
    await user.click(within(unused).getByRole("button", { name: "Remove domain" }));
    expect(docker.deleteDomain).toHaveBeenCalledWith("tunnel.example.com");
    await waitFor(() => expect(screen.queryByText("tunnel.example.com")).not.toBeInTheDocument());
    expect(toastSuccess).toHaveBeenCalledWith("Domain removed");
    // The row's menu left with the row: focus goes to Add domain.
    await waitFor(() => expect(screen.getByRole("button", { name: /Add domain/ })).toHaveFocus());

    await pick(user, "example.com", "Remove");
    const confirm = await screen.findByRole("alertdialog");
    expect(confirm).toHaveTextContent("It's used by Shop: remove those addresses first");
    await user.click(within(confirm).getByRole("button", { name: "Remove domain" }));
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith("Couldn't remove domain", { description: "example.com is used by Shop" }),
    );
    // A refusal keeps the confirmation (to cancel) and the row.
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(screen.getByText("example.com")).toBeInTheDocument();
    expect(docker.listDomains).toHaveBeenCalledTimes(1);
  });

  it("says so when the list can't be loaded, or is empty", async () => {
    const user = setupUser();
    const { unmount } = renderPage(fakeDocker({ listDomains: vi.fn().mockRejectedValue(new Error("agent offline")) }));
    expect(await screen.findByText("Couldn't load domains.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
    unmount();

    const docker = fakeDocker({ listDomains: vi.fn().mockResolvedValue([]) });
    renderPage(docker);
    expect(await screen.findByText(/No domains yet/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Refresh domains" }));
    await waitFor(() => expect(docker.listDomains).toHaveBeenCalledTimes(2));
  });
});
```

In `web/src/pages/server-settings.test.tsx`, change the mock line for domains to

```tsx
vi.mock("@/pages/settings-domains", () => ({ DomainsSettings: () => <p>domains page</p> }));
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/domains.test.ts src/pages/settings-domains.test.tsx src/pages/server-settings.test.tsx`
Expected: FAIL — `expiryNote`, `TLS_MODES[…].choice/state` and `DomainsSettings` do not exist.

- [ ] **Step 3: Implement**

In `web/src/lib/domains.ts`, replace the `TLS_MODES` declaration (comment included) with:

```ts
// TLS_MODES words each mode: its badge label and summary (the app editor's
// hostname hints), its state on the Domains page, its choice in the
// register dialog with the setup it fits. Listed in the order the dialog
// offers them.
export const TLS_MODES: Record<
  DomainTLS,
  { label: string; summary: string; state: string; choice: string; hint: string }
> = {
  auto: {
    label: "Auto TLS",
    summary: "Free Let's Encrypt certificate",
    state: "Automatic HTTPS",
    choice: "Automatic (Let's Encrypt)",
    hint: "A free certificate for each app address. Point the domain's DNS at this server (on Cloudflare: DNS only, grey cloud).",
  },
  certificate: {
    label: "Certificate",
    summary: "Your uploaded certificate",
    state: "Own certificate",
    choice: "My own certificate",
    hint: "Paste a certificate and its private key, such as a Cloudflare Origin certificate behind the Cloudflare proxy.",
  },
  off: {
    label: "TLS off",
    summary: "Plain HTTP, for a tunnel or proxy in front",
    state: "HTTPS off",
    choice: "Off — behind Cloudflare Tunnel or another proxy",
    hint: "This server serves plain HTTP; the tunnel or proxy in front of it handles HTTPS.",
  },
};
```

and append:

```ts
// expiryNote is what the Domains page says about a certificate-mode
// domain's certificate: nothing while it has 30 days or more, a countdown
// under that, the date once it is past. Other modes renew themselves or
// have no certificate here.
export function expiryNote(
  d: Pick<RegisteredDomain, "tls" | "not_after">,
  now: Date = new Date(),
): { text: string; expired: boolean } | null {
  if (d.tls !== "certificate") return null;
  const left = expiry(d.not_after, now);
  if (!left || left.grade === "ok") return null;
  if (left.grade === "expired") return { text: `Expired ${left.day}`, expired: true };
  return { text: `Expires in ${left.days === 1 ? "1 day" : `${left.days} days`}`, expired: false };
}
```

Replace `web/src/components/domain-dialog.tsx` with:

```tsx
import { useState, type RefObject } from "react";
import { toast } from "sonner";

import { ResponsiveDialog } from "@/components/responsive-dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Textarea } from "@/components/ui/textarea";
import type { Docker, DomainCreated } from "@/context/use-docker";
import { returnFocusTo } from "@/lib/focus";
import { domainNameError, TLS_MODES, type Domain, type DomainTLS } from "@/lib/domains";
import { cn } from "@/lib/utils";

// The three ways a registered domain can do HTTPS, in the dialog's order.
const MODES = Object.keys(TLS_MODES) as DomainTLS[];
const FORM_ID = "domain-form";

// DomainDialog adds a registration, or (replace set) swaps the HTTPS mode
// or certificate of an existing one — the agent replaces a name on create.
// The parent remounts it (key) per opening, so state starts from replace.
// It reports a failure itself; onSaved gets the stored registration and the
// proxy's warnings. returnFocus is where focus goes back to on close (the
// dialog has no trigger of its own).
export function DomainDialog({
  docker,
  open,
  replace,
  returnFocus,
  onOpenChange,
  onSaved,
}: {
  docker: Docker;
  open: boolean;
  replace: Domain | null;
  returnFocus?: RefObject<HTMLElement | null>;
  onOpenChange: (open: boolean) => void;
  onSaved: (created: DomainCreated) => void;
}) {
  const [name, setName] = useState(replace?.name ?? "");
  const [tls, setTLS] = useState<DomainTLS>(replace?.tls ?? "auto");
  const [certificate, setCertificate] = useState("");
  const [key, setKey] = useState("");
  const [busy, setBusy] = useState(false);

  const trimmed = name.trim();
  const nameError = trimmed ? domainNameError(trimmed) : "";
  const needsPair = tls === "certificate";
  const canSave =
    trimmed !== "" && !nameError && !busy && (!needsPair || (certificate.trim() !== "" && key.trim() !== ""));

  // The PEM material must not outlive the dialog: wipe it on every close
  // (save, Cancel, Escape, overlay, swipe down).
  const changeOpen = (next: boolean) => {
    if (!next) {
      setCertificate("");
      setKey("");
    }
    onOpenChange(next);
  };

  const save = async () => {
    setBusy(true);
    try {
      const created = await docker.createDomain(
        trimmed,
        tls,
        needsPair ? { certificate: certificate.trim(), key: key.trim() } : undefined,
      );
      changeOpen(false);
      onSaved(created);
    } catch (e) {
      toast.error(replace ? "Couldn't update domain" : "Couldn't add domain", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <ResponsiveDialog
      open={open}
      onOpenChange={changeOpen}
      title={replace ? "Edit domain" : "Add domain"}
      description={
        replace ? (
          <>
            Choose how HTTPS works for{" "}
            <code className="font-mono [overflow-wrap:anywhere] text-foreground">{replace.name}</code>.
          </>
        ) : (
          "Apps on this server can only use addresses under a domain you've added."
        )
      }
      className="sm:max-w-xl"
      onCloseAutoFocus={returnFocusTo(returnFocus)}
      footer={
        <>
          <Button type="button" variant="outline" onClick={() => changeOpen(false)}>
            Cancel
          </Button>
          <Button type="submit" form={FORM_ID} disabled={!canSave}>
            {busy ? "Saving…" : replace ? "Save" : "Add"}
          </Button>
        </>
      }
    >
      <form
        id={FORM_ID}
        className="grid gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          if (canSave) void save();
        }}
      >
        {/* The name is the registration's identity: editing changes only
            its mode, so an existing one is shown, not offered as a field. */}
        {replace ? null : (
          <div className="grid gap-1.5">
            <Label htmlFor="dom-name">Name</Label>
            <Input
              id="dom-name"
              placeholder="*.example.com"
              autoComplete="off"
              spellCheck={false}
              className={cn("font-mono", nameError && "border-destructive focus-visible:ring-destructive")}
              aria-invalid={nameError !== ""}
              aria-describedby="dom-name-hint"
              value={name}
              onChange={(e) => setName(e.target.value.toLowerCase())}
            />
            <p id="dom-name-hint" className={cn("text-xs", nameError ? "text-destructive" : "text-muted-foreground")}>
              {nameError || "app.example.com for one address, or *.example.com for any name directly under it."}
            </p>
          </div>
        )}
        <div className="grid gap-2">
          <Label id="dom-mode-label">HTTPS</Label>
          <RadioGroup
            aria-labelledby="dom-mode-label"
            value={tls}
            onValueChange={(v) => setTLS(v as DomainTLS)}
            className="gap-2"
          >
            {MODES.map((mode) => (
              <label
                key={mode}
                className={cn(
                  "flex cursor-pointer items-start gap-3 rounded-lg border p-3 text-sm transition-colors duration-150",
                  tls === mode && "border-primary bg-primary/5",
                )}
              >
                <RadioGroupItem
                  value={mode}
                  aria-label={TLS_MODES[mode].choice}
                  aria-describedby={`dom-mode-${mode}`}
                  className="mt-0.5"
                />
                <span className="min-w-0">
                  <span className="font-medium">{TLS_MODES[mode].choice}</span>
                  <span id={`dom-mode-${mode}`} className="block text-xs text-muted-foreground">
                    {TLS_MODES[mode].hint}
                  </span>
                </span>
              </label>
            ))}
          </RadioGroup>
        </div>
        {needsPair ? (
          <>
            <div className="grid gap-1.5">
              <Label htmlFor="dom-cert">Certificate (PEM)</Label>
              <Textarea
                id="dom-cert"
                rows={4}
                spellCheck={false}
                className="font-mono text-xs"
                placeholder="-----BEGIN CERTIFICATE-----"
                aria-describedby="dom-cert-hint"
                value={certificate}
                onChange={(e) => setCertificate(e.target.value)}
              />
              <p id="dom-cert-hint" className="text-xs text-muted-foreground">
                {replace?.tls === "certificate" ? "Paste the certificate again, or a new one. " : null}
                It also serves the automatic-HTTPS addresses it covers, instead of Let's Encrypt.
              </p>
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="dom-key">Private key (PEM)</Label>
              <Textarea
                id="dom-key"
                rows={4}
                spellCheck={false}
                className="font-mono text-xs"
                placeholder="-----BEGIN PRIVATE KEY-----"
                aria-describedby="dom-key-hint"
                value={key}
                onChange={(e) => setKey(e.target.value)}
              />
              <p id="dom-key-hint" className="text-xs text-muted-foreground">
                Encrypted in your browser; only this server can read it.
              </p>
            </div>
          </>
        ) : null}
      </form>
    </ResponsiveDialog>
  );
}
```

Replace `web/src/pages/settings-domains.tsx` with:

```tsx
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Globe, Plus } from "lucide-react";
import { toast } from "sonner";

import { AppLinks } from "@/components/app-links";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DomainDialog } from "@/components/domain-dialog";
import { EmptyState } from "@/components/empty-state";
import { ListPlaceholder } from "@/components/list-placeholder";
import { PageHeader } from "@/components/page-header";
import { RefreshButton } from "@/components/refresh-button";
import { RowMenu } from "@/components/row-menu";
import { Row, RowList } from "@/components/section";
import { Button } from "@/components/ui/button";
import { useApps } from "@/context/use-apps";
import type { Docker, DomainCreated } from "@/context/use-docker";
import { expiryNote, TLS_MODES, type Domain } from "@/lib/domains";
import { returnFocusTo } from "@/lib/focus";
import { cn } from "@/lib/utils";

const byName = (a: Domain, b: Domain) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0);

// toastResult reports a domain change: success, or a warning when the proxy
// reload that applied it reported problems (the change itself is stored).
function toastResult(done: string, warnings: string[]) {
  if (warnings.length > 0) {
    toast.warning(`${done}, but the proxy reported a problem`, {
      // One line each (toast descriptions keep newlines: index.css).
      description: warnings.join("\n"),
    });
  } else {
    toast.success(done);
  }
}

// DomainsSettings is Settings → Server → Domains: the names apps on this
// server may use, each with its HTTPS state, its certificate's end when it
// is near or past, and the apps using it. Add, edit and remove from here.
export function DomainsSettings({ docker }: { docker: Docker }) {
  const { apps } = useApps();
  const [items, setItems] = useState<Domain[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [replace, setReplace] = useState<Domain | null>(null);
  const [dialogKey, setDialogKey] = useState(0);
  const [toDelete, setToDelete] = useState<Domain | null>(null);
  // The dialogs have no trigger of their own: focus goes back to what
  // opened them, or to Add domain when that left with its row.
  const opener = useRef<HTMLElement | null>(null);
  const addButton = useRef<HTMLButtonElement | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setItems(await docker.listDomains());
    } catch (e) {
      toast.error("Couldn't load domains", { description: e instanceof Error ? e.message : undefined });
    } finally {
      setLoading(false);
    }
  }, [docker]);

  useEffect(() => {
    void load();
  }, [load]);

  // A create or delete answers with what changed, so the list is patched in
  // place rather than listed again (Refresh does that).
  const saved = ({ domain, warnings }: DomainCreated) => {
    toastResult(replace ? "Domain updated" : "Domain added", warnings);
    setItems((prev) => [...(prev ?? []).filter((d) => d.name !== domain.name), domain].sort(byName));
  };

  const remove = async (name: string) => {
    try {
      toastResult("Domain removed", await docker.deleteDomain(name));
      setItems((prev) => prev && prev.filter((d) => d.name !== name));
    } catch (e) {
      // A host only this name covers: the agent's message names the apps.
      toast.error("Couldn't remove domain", { description: e instanceof Error ? e.message : undefined });
      throw e;
    }
  };

  const names = useMemo(() => new Map(apps.map((a) => [a.id, a.name])), [apps]);

  const openDialog = (row: Domain | null, from: HTMLElement | null) => {
    opener.current = from;
    setReplace(row);
    setDialogKey((k) => k + 1);
    setDialogOpen(true);
  };

  const deleteApps = toDelete?.apps ?? [];

  return (
    <div className="space-y-6">
      <PageHeader
        title="Domains"
        subtitle="Addresses apps on this server can use, and how HTTPS works for each."
        actions={
          <>
            <RefreshButton label="Refresh domains" loading={loading} onClick={() => void load()} />
            <Button ref={addButton} onClick={(e) => openDialog(null, e.currentTarget)}>
              <Plus className="size-4" /> Add domain
            </Button>
          </>
        }
      />
      {items === null ? (
        <ListPlaceholder loading={loading} failed="Couldn't load domains." onRetry={() => void load()} />
      ) : items.length === 0 ? (
        <EmptyState icon={Globe} title="No domains yet. Add one, like *.example.com, before giving an app an address." />
      ) : (
        <RowList label="Domains">
          {items.map((d) => {
            const note = expiryNote(d);
            return (
              <Row
                key={d.name}
                // Names wrap rather than truncate: the suffix is the part that matters.
                title={<span className="whitespace-normal font-mono [overflow-wrap:anywhere]">{d.name}</span>}
                description={
                  <span className="flex flex-wrap gap-x-2">
                    <span>{TLS_MODES[d.tls].state}</span>
                    {note ? (
                      <span
                        title={d.issuer ? `Issued by ${d.issuer}` : undefined}
                        className={cn("font-medium", note.expired ? "text-status-bad" : "text-status-warn")}
                      >
                        {note.text}
                      </span>
                    ) : null}
                  </span>
                }
                meta={<AppLinks ids={d.apps ?? []} names={names} />}
                actions={
                  <RowMenu
                    label={`Actions for ${d.name}`}
                    items={[
                      { label: "Edit", onSelect: (trigger) => openDialog(d, trigger) },
                      {
                        label: "Remove",
                        destructive: true,
                        onSelect: (trigger) => {
                          opener.current = trigger;
                          setToDelete(d);
                        },
                      },
                    ]}
                  />
                }
              />
            );
          })}
        </RowList>
      )}
      <DomainDialog
        key={dialogKey}
        docker={docker}
        open={dialogOpen}
        replace={replace}
        returnFocus={opener}
        onOpenChange={setDialogOpen}
        onSaved={saved}
      />
      <ConfirmDialog
        open={toDelete !== null}
        onOpenChange={(o) => !o && setToDelete(null)}
        title="Remove this domain?"
        description={
          <>
            Apps on this server will no longer be able to use{" "}
            <code className="font-mono [overflow-wrap:anywhere] text-foreground">{toDelete?.name}</code>.
            {deleteApps.length > 0 ? (
              <>
                {" "}
                It's used by{" "}
                <span className="font-medium text-foreground">
                  {deleteApps.map((id) => names.get(id) ?? id).join(", ")}
                </span>
                : remove those addresses first, unless another domain on this server covers them.
              </>
            ) : null}
          </>
        }
        confirmLabel="Remove domain"
        pendingLabel="Removing…"
        onCloseAutoFocus={returnFocusTo(opener, addButton)}
        onConfirm={() => (toDelete ? remove(toDelete.name) : undefined)}
      />
    </div>
  );
}
```

In `web/src/pages/server-settings.tsx`, replace the `return ( <div …> <PageHeader title="Domains" … /> <DomainsCard docker={docker} /> </div> );` at the end of `ServerDomainsPage` with

```tsx
  return <DomainsSettings key={activeServer.id} docker={docker} />;
```

and the import `import { DomainsCard } from "@/pages/settings-domains";` with `import { DomainsSettings } from "@/pages/settings-domains";`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/domains.test.ts src/pages/settings-domains.test.tsx src/pages/server-settings.test.tsx src/components/host-hints.test.tsx src/components/ingress-editor.test.tsx`
Expected: PASS (the app editor's `TLSBadge` labels are unchanged). If `host-hints.test.tsx` does not exist, drop it from the command.

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/domains.ts web/src/lib/domains.test.ts web/src/components/domain-dialog.tsx web/src/pages/settings-domains.tsx web/src/pages/settings-domains.test.tsx web/src/pages/server-settings.tsx web/src/pages/server-settings.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): Settings → Domains as flat rows with plain HTTPS choices

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 5: The storage dialog — type first, S3 basics up front, the rest under "Advanced"

**Files:**
- Modify: `web/src/lib/storages.ts`, `web/src/lib/storages.test.ts` (`STORAGE_TYPES` wording, form order, `ADVANCED_FIELDS`)
- Rewrite: `web/src/components/storage-dialog.tsx` (on `ResponsiveDialog`)
- Modify: `web/src/pages/settings-storages.test.tsx` (the `StorageDialog form` tests and every test that types into the dialog)

**Interfaces:**
- Consumes: Task 1 (`ResponsiveDialog` with `onOpenAutoFocus`/`onCloseAutoFocus`); phase 1 `Disclosure({ label, open, onOpenChange, children })`; `Docker.testStorage/saveStorage`; `returnFocusTo`; `InlineError` (`@/components/inline-error`).
- Produces:
  - `STORAGE_TYPES = { local: "Folder on this server", s3: "S3 bucket" }`.
  - `DRAFT_FIELDS = ["name", "path", "endpoint", "bucket", "accessKeyId", "secret", "region", "prefix"]` (the new form order) and the S3 checks in that order.
  - `ADVANCED_FIELDS: readonly DraftField[] = ["region", "prefix"]`.
  - `StorageDialog` keeps its props (`docker, open, edit, existing?, usedBy?, returnFocus?, onOpenChange, onSaved`).

- [ ] **Step 1: Write the failing tests**

In `web/src/lib/storages.test.ts`, add `ADVANCED_FIELDS`, `DRAFT_FIELDS` and `STORAGE_TYPES` to the `import { … } from "@/lib/storages"` list and append:

```ts
describe("storage form order", () => {
  it("asks for the S3 basics first and the advanced fields last", () => {
    expect(DRAFT_FIELDS).toEqual(["name", "path", "endpoint", "bucket", "accessKeyId", "secret", "region", "prefix"]);
    expect(ADVANCED_FIELDS).toEqual(["region", "prefix"]);
    // A missing secret is reported before a bad prefix: it comes first on screen.
    const ok = { ...draftFrom(s3), name: "offsite", secret: "s3cret" };
    expect(draftProblem({ ...ok, secret: "", prefix: "/x" }, false)?.field).toBe("secret");
  });

  it("names the two kinds in plain words", () => {
    expect(STORAGE_TYPES).toEqual({ local: "Folder on this server", s3: "S3 bucket" });
  });
});
```

In `web/src/pages/settings-storages.test.tsx`, update the tests that drive the dialog (the list tests are rewritten in Task 6):

- Everywhere in the file: the string `"S3-compatible"` → `"S3 bucket"` and `"Local folder"` → `"Folder on this server"` (radio names, and the type badge the old list still shows until Task 6).
- In `it("shows a failed test inline and lets a stale result go", …)`: delete the line that types into "Region" where it is; after typing the access key and the secret (before the "Path-style addressing" click) add

```tsx
    await user.click(within(dialog).getByRole("button", { name: "Advanced" }));
    await user.type(within(dialog).getByLabelText("Region"), "fsn1");
```

- In `it("labels the type choice and offers type-appropriate examples", …)`: right after clicking the `S3 bucket` radio add `await user.click(within(dialog).getByRole("button", { name: "Advanced" }));` (Prefix and Region sit under it).
- In `it("turns path-style on for an IP or localhost endpoint until the user sets it", …)` and `it("keeps the user's path-style when the endpoint moves to an IP and back", …)`: right after clicking the `S3 bucket` radio add `await user.click(within(dialog).getByRole("button", { name: "Advanced" }));`.
- In `it("says what is missing when Save meets a field the user never reached", …)`: unchanged (the secret is not under Advanced).

Append inside `describe("StorageDialog form", …)`:

```tsx
  it("opens Advanced to show a problem inside it", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    const dialog = await openAdd(user, docker);
    await user.click(within(dialog).getByRole("radio", { name: "S3 bucket" }));
    await user.type(within(dialog).getByLabelText("Name"), "offsite");
    await user.type(within(dialog).getByLabelText("Endpoint"), "https://s3.example.com");
    await user.type(within(dialog).getByLabelText("Bucket"), "tenants");
    await user.type(within(dialog).getByLabelText("Access key"), "AKIA");
    await user.type(within(dialog).getByLabelText("Secret key"), "S3-PLAIN");
    await user.click(within(dialog).getByRole("button", { name: "Advanced" }));
    await user.type(within(dialog).getByLabelText("Prefix"), "/x");
    // Folded away again, the problem is out of sight: Save stays usable
    // and, pressed, shows where the problem is instead of saving.
    await user.click(within(dialog).getByRole("button", { name: "Advanced" }));
    expect(within(dialog).queryByLabelText("Prefix")).not.toBeInTheDocument();
    const save = within(dialog).getByRole("button", { name: "Save" });
    expect(save).toBeEnabled();
    await user.click(save);
    expect(docker.saveStorage).not.toHaveBeenCalled();
    expect(within(dialog).getByLabelText("Prefix")).toHaveFocus();
    expect(within(dialog).getByText("Use folder names separated by /, like winterflow/.")).toBeInTheDocument();
  });

  it("keeps Advanced folded on an add", async () => {
    const user = setupUser();
    const dialog = await openAdd(user);
    await user.click(within(dialog).getByRole("radio", { name: "S3 bucket" }));
    expect(within(dialog).queryByLabelText("Region")).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("checkbox", { name: "Path-style addressing" })).not.toBeInTheDocument();
  });
```


- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/storages.test.ts src/pages/settings-storages.test.tsx`
Expected: FAIL — the order and wording are the old ones, there is no "Advanced".

- [ ] **Step 3: Implement**

In `web/src/lib/storages.ts`:

- replace `STORAGE_TYPES` with

```ts
// STORAGE_TYPES names the two kinds in plain words (type-first dialog,
// rows).
export const STORAGE_TYPES: Record<StorageType, string> = {
  local: "Folder on this server",
  s3: "S3 bucket",
};
```

- replace the `DRAFT_FIELDS` declaration and its comment with

```ts
// DraftField is a field of the dialog, in form order: the S3 basics, then
// the advanced fields (region, prefix) folded under "Advanced". A problem
// belongs to one.
export const DRAFT_FIELDS = ["name", "path", "endpoint", "bucket", "accessKeyId", "secret", "region", "prefix"] as const;
export type DraftField = (typeof DRAFT_FIELDS)[number];

// ADVANCED_FIELDS sit under the dialog's "Advanced" disclosure.
export const ADVANCED_FIELDS: readonly DraftField[] = ["region", "prefix"];
```

- in `RULES`, reorder the `s3` checks to the form order (bodies unchanged):

```ts
  s3: [
    ["endpoint", (d) => endpointError(d.endpoint)],
    ["bucket", (d) => (BUCKET_RE.test(d.bucket.trim()) && !d.bucket.includes("..") ? "" : "Enter a valid bucket name.")],
    ["accessKeyId", (d) => accessKeyError(d.accessKeyId.trim())],
    // An edit keeps the stored secret when none is typed.
    ["secret", (d, editing) => (editing || d.secret ? "" : "Enter the secret key.")],
    ["prefix", (d) => (prefixValid(d.prefix.trim()) ? "" : "Use folder names separated by /, like winterflow/.")],
  ],
```

Replace `web/src/components/storage-dialog.tsx` with:

```tsx
import { useEffect, useMemo, useRef, useState, type RefObject } from "react";
import { CircleCheck, CircleX, Cloud, Folder } from "lucide-react";
import { toast } from "sonner";

import { Disclosure } from "@/components/disclosure";
import { InlineError } from "@/components/inline-error";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import type { Docker } from "@/context/use-docker";
import { returnFocusTo } from "@/lib/focus";
import {
  ADVANCED_FIELDS,
  DRAFT_FIELDS,
  draftFrom,
  draftProblem,
  pathStyleDefault,
  STORAGE_TYPES,
  storageVar,
  type DraftField,
  type Storage,
  type StorageDraft,
  type StorageSaved,
  type StorageType,
} from "@/lib/storages";
import { cn } from "@/lib/utils";

type Probe = { state: "idle" | "testing" } | { state: "ok" } | { state: "failed"; error: string };

const FORM_ID = "storage-form";

// fieldId is the id of a field's input.
const fieldId = (field: DraftField) => `st-${field}`;

const TYPE_ICON = { local: Folder, s3: Cloud } as const;

// COPY is the wording that differs by type.
const COPY: Record<StorageType, { hint: string; namePlaceholder: string; test: string; testing: string; ok: string }> = {
  local: {
    hint: "A folder on a disk attached to this server, such as a USB drive.",
    namePlaceholder: "e.g. backup-disk",
    test: "Check folder",
    testing: "Checking…",
    ok: "Folder is writable",
  },
  s3: {
    hint: "A bucket at an S3-compatible provider: Backblaze, Wasabi, Cloudflare R2, MinIO…",
    namePlaceholder: "e.g. offsite",
    test: "Test connection",
    testing: "Testing…",
    ok: "Connection works",
  },
};

const errorOf = (e: unknown) => (e instanceof Error ? e.message : "");

// StorageDialog adds a storage, or (edit set) changes one: the name and
// type are its identity, so an edit shows them instead of offering them.
// The type comes first; an S3 bucket asks for endpoint, bucket and keys,
// with region, prefix and path-style under "Advanced" (open on an edit that
// uses them). The parent remounts it (key) per opening. Save tests again on
// the agent and stores nothing when that fails. The typed secret never
// outlives the dialog: it is wiped on every close. existing are the
// server's storage names (an add can't reuse one); usedBy names the apps
// using the edited storage; returnFocus is where focus goes back on close.
export function StorageDialog({
  docker,
  open,
  edit,
  existing = [],
  usedBy,
  returnFocus,
  onOpenChange,
  onSaved,
}: {
  docker: Docker;
  open: boolean;
  edit: Storage | null;
  existing?: readonly string[];
  usedBy?: readonly string[];
  returnFocus?: RefObject<HTMLElement | null>;
  onOpenChange: (open: boolean) => void;
  onSaved: (saved: StorageSaved) => void;
}) {
  const initial = useMemo(() => draftFrom(edit), [edit]);
  const [draft, setDraft] = useState<StorageDraft>(initial);
  const [probe, setProbe] = useState<Probe>({ state: "idle" });
  const [busy, setBusy] = useState(false);
  const [saveError, setSaveError] = useState("");
  // Path-style follows the endpoint on an add until the user sets it.
  const [pathStyleSet, setPathStyleSet] = useState(false);
  // The furthest field (DRAFT_FIELDS index) the user changed or left: a
  // problem shows once the user reached its field.
  const [reached, setReached] = useState(-1);
  const [advanced, setAdvanced] = useState(() => Boolean(initial.region || initial.prefix || initial.pathStyle));
  // A field to focus once it is on screen: one under Advanced appears only
  // after Advanced opens.
  const pendingFocus = useRef<DraftField | null>(null);

  // Answers can arrive after the dialog moved on. A save may run for
  // minutes: when it answers after this dialog closed (or unmounted, the
  // parent remounting a new one), it still reports the save but must not
  // close whatever dialog is open now. A test answer counts only while no
  // newer test or edit superseded it.
  const generation = useRef(0); // bumped on every close and on unmount
  const probeSeq = useRef(0); // bumped on every test start, edit and close
  useEffect(
    () => () => {
      generation.current++;
    },
    [],
  );
  useEffect(() => {
    const field = pendingFocus.current;
    if (!field) return;
    const el = document.getElementById(fieldId(field));
    if (el) {
      pendingFocus.current = null;
      el.focus();
    }
  });

  const editing = edit !== null;
  const problem = draftProblem(draft, editing, editing ? [] : existing);
  const apps = usedBy ?? edit?.apps ?? [];
  const local = draft.type === "local";
  const copy = COPY[draft.type];
  // An edit that changes nothing has nothing to save (nor apps to redeploy).
  const dirty = !editing || JSON.stringify(draft) !== JSON.stringify(initial);

  // A problem shows under its field once the user touched that field or
  // moved past it to a later one — and is on screen only while its field
  // is (not folded under a closed Advanced).
  const shown = (field: DraftField) => problem?.field === field && reached >= DRAFT_FIELDS.indexOf(field);
  const onScreen = (field: DraftField) => advanced || !ADVANCED_FIELDS.includes(field);
  const hidden = problem !== null && !(shown(problem.field) && onScreen(problem.field));
  const touch = (field: DraftField) => setReached((r) => Math.max(r, DRAFT_FIELDS.indexOf(field)));

  // A problem the user can't see (a secret never focused, a prefix folded
  // away): Save and Test reveal every problem, open Advanced when the
  // problem is in it, and go to it, sending nothing.
  const reveal = () => {
    if (!problem) return false;
    setReached(DRAFT_FIELDS.length - 1);
    if (ADVANCED_FIELDS.includes(problem.field)) setAdvanced(true);
    const el = document.getElementById(fieldId(problem.field));
    if (el) el.focus();
    else pendingFocus.current = problem.field;
    return true;
  };

  // Any change makes the last test result and save failure stale.
  const set = (patch: Partial<StorageDraft>, field?: DraftField) => {
    probeSeq.current++;
    setDraft((d) => ({ ...d, ...patch }));
    setProbe({ state: "idle" });
    setSaveError("");
    if (field) touch(field);
  };

  const changeOpen = (next: boolean) => {
    if (!next) {
      generation.current++;
      probeSeq.current++;
      setDraft((d) => ({ ...d, secret: "" }));
      setProbe({ state: "idle" });
      setSaveError("");
    }
    onOpenChange(next);
  };

  const test = async () => {
    const seq = ++probeSeq.current;
    const current = () => seq === probeSeq.current;
    setProbe({ state: "testing" });
    try {
      const res = await docker.testStorage(draft);
      if (current()) setProbe(res.ok ? { state: "ok" } : { state: "failed", error: res.error || "The test failed." });
    } catch (e) {
      if (current()) setProbe({ state: "failed", error: errorOf(e) || "The test failed." });
    }
  };

  const save = async () => {
    const opened = generation.current;
    const stillOpen = () => opened === generation.current;
    setBusy(true);
    setSaveError("");
    try {
      const saved = await docker.saveStorage(draft, !editing);
      if (stillOpen()) changeOpen(false);
      onSaved(saved);
    } catch (e) {
      const title = editing ? "Couldn't save storage" : "Couldn't add storage";
      const reason = errorOf(e) || "The save failed.";
      // Open: the reason stays next to the form; closed: only the toast
      // can carry it.
      if (stillOpen()) {
        setSaveError(reason);
        toast.error(title);
      } else {
        toast.error(title, { description: reason });
      }
    } finally {
      setBusy(false);
    }
  };

  const field = (
    key: DraftField,
    label: string,
    value: string,
    onChange: (v: string) => void,
    props: { placeholder?: string; hint?: string; type?: string } = {},
  ) => {
    const id = fieldId(key);
    const invalid = shown(key);
    const describedBy = [props.hint ? `${id}-hint` : "", invalid ? `${id}-error` : ""].filter(Boolean).join(" ");
    return (
      <div className="grid gap-1.5">
        <Label htmlFor={id}>{label}</Label>
        <Input
          id={id}
          type={props.type ?? "text"}
          autoComplete="off"
          spellCheck={false}
          placeholder={props.placeholder}
          aria-invalid={invalid || undefined}
          aria-describedby={describedBy || undefined}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onBlur={() => touch(key)}
        />
        {props.hint ? (
          <p id={`${id}-hint`} className="text-xs text-muted-foreground">
            {props.hint}
          </p>
        ) : null}
        {invalid ? (
          <p id={`${id}-error`} className="text-xs text-destructive">
            {problem?.message}
          </p>
        ) : null}
      </div>
    );
  };

  const name = draft.name.trim();

  return (
    <ResponsiveDialog
      open={open}
      onOpenChange={changeOpen}
      title={editing ? "Edit storage" : "Add storage"}
      description={
        editing ? (
          <>
            {STORAGE_TYPES[draft.type]} <code className="font-mono text-foreground">{edit.name}</code>.
          </>
        ) : (
          "A folder or bucket backups and apps on this server can write to."
        )
      }
      className="sm:max-w-xl"
      onOpenAutoFocus={(e) => {
        // Add: the name (the type already has a choice made). Edit: the
        // caret goes to the end of the first field, its text not selected
        // (one stray key would replace it).
        const first = document.getElementById(fieldId(editing ? (local ? "path" : "endpoint") : "name"));
        if (!(first instanceof HTMLInputElement)) return;
        e.preventDefault();
        first.focus();
        first.setSelectionRange(first.value.length, first.value.length);
      }}
      onCloseAutoFocus={returnFocusTo(returnFocus)}
      footer={
        <>
          <Button type="button" variant="outline" onClick={() => changeOpen(false)}>
            Cancel
          </Button>
          {/* Enabled while a problem is out of sight, so Save can say
              what is missing; disabled once the problem is on screen. */}
          <Button type="submit" form={FORM_ID} disabled={(problem !== null && !hidden) || busy || !dirty}>
            {busy ? "Saving…" : "Save"}
          </Button>
        </>
      }
    >
      <form
        id={FORM_ID}
        className="grid gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          if (busy || !dirty || reveal()) return;
          void save();
        }}
      >
        {/* Disabled while saving: what is shown is what is being saved. */}
        <fieldset disabled={busy} className="grid min-w-0 gap-4">
          {editing ? null : (
            <>
              <div className="grid gap-1.5">
                <Label id="st-type-label">Type</Label>
                <RadioGroup
                  aria-labelledby="st-type-label"
                  className="grid gap-2 sm:grid-cols-2"
                  value={draft.type}
                  onValueChange={(v) => set({ type: v as StorageType })}
                >
                  {(Object.keys(STORAGE_TYPES) as StorageType[]).map((t) => {
                    const Icon = TYPE_ICON[t];
                    return (
                      <label
                        key={t}
                        className={cn(
                          "flex cursor-pointer items-start gap-3 rounded-lg border p-3 transition-colors duration-150",
                          draft.type === t && "border-primary bg-primary/5",
                        )}
                      >
                        <RadioGroupItem
                          value={t}
                          aria-label={STORAGE_TYPES[t]}
                          aria-describedby={`st-type-${t}-hint`}
                          className="mt-0.5"
                        />
                        <span className="min-w-0 text-sm">
                          <span className="flex items-center gap-1.5 font-medium">
                            <Icon className="size-4 text-muted-foreground" aria-hidden />
                            {STORAGE_TYPES[t]}
                          </span>
                          <span id={`st-type-${t}-hint`} className="block text-xs text-muted-foreground">
                            {COPY[t].hint}
                          </span>
                        </span>
                      </label>
                    );
                  })}
                </RadioGroup>
              </div>
              {field("name", "Name", draft.name, (v) => set({ name: v.toLowerCase() }, "name"), {
                placeholder: copy.namePlaceholder,
                hint: name ? `Compose files use it as \${${storageVar(name, "…")}}.` : "Lowercase letters, digits and hyphens.",
              })}
            </>
          )}

          {local ? (
            field("path", "Folder", draft.path, (v) => set({ path: v }, "path"), {
              placeholder: "/mnt/backup/winterflow",
              hint: "An existing folder on the disk (under /mnt or /media), not its mount point.",
            })
          ) : (
            <>
              {field(
                "endpoint",
                "Endpoint",
                draft.endpoint,
                (v) =>
                  // An IP address or localhost has no bucket.host names: on
                  // an add, path-style follows the endpoint until the user
                  // ticks or unticks it, then it never moves by itself.
                  set(!editing && !pathStyleSet ? { endpoint: v, pathStyle: pathStyleDefault(v) } : { endpoint: v }, "endpoint"),
                { placeholder: "https://s3.example.com" },
              )}
              {field("bucket", "Bucket", draft.bucket, (v) => set({ bucket: v }, "bucket"))}
              {field("accessKeyId", "Access key", draft.accessKeyId, (v) => set({ accessKeyId: v }, "accessKeyId"))}
              {field("secret", "Secret key", draft.secret, (v) => set({ secret: v }, "secret"), {
                type: "password",
                hint: editing ? "Leave empty to keep." : "Encrypted in your browser; only this server can read it.",
              })}
              <Disclosure label="Advanced" open={advanced} onOpenChange={setAdvanced}>
                {field("region", "Region", draft.region, (v) => set({ region: v }, "region"), {
                  placeholder: "e.g. auto",
                  hint: "Leave empty unless your provider asks for one.",
                })}
                {field("prefix", "Prefix", draft.prefix, (v) => set({ prefix: v }, "prefix"), {
                  placeholder: "e.g. winterflow/",
                  hint: "Optional: everything is written under this folder of the bucket.",
                })}
                <label className="flex items-center gap-2 text-sm">
                  <Checkbox
                    checked={draft.pathStyle}
                    onCheckedChange={(v) => {
                      setPathStyleSet(true);
                      set({ pathStyle: v === true });
                    }}
                    aria-label="Path-style addressing"
                  />
                  Path-style addressing (MinIO and some providers)
                </label>
              </Disclosure>
            </>
          )}

          {editing && apps.length > 0 ? (
            <p className="rounded-md bg-status-warn/15 px-3 py-2 text-sm">
              {/* The agent redeploys only the running ones (and skips an
                  app with undeployed changes, with a warning). */}
              {apps.length === 1
                ? `Saving redeploys the app that uses it if it's running: ${apps[0]}.`
                : `Saving redeploys the apps that use it if they're running: ${apps.join(", ")}.`}
            </p>
          ) : null}

          <div className="flex min-h-9 flex-wrap items-center gap-3">
            {/* Enabled while a problem is out of sight, like Save. */}
            <Button
              type="button"
              variant="outline"
              disabled={(problem !== null && !hidden) || probe.state === "testing"}
              onClick={() => {
                if (!reveal()) void test();
              }}
            >
              {probe.state === "testing" ? copy.testing : copy.test}
            </Button>
            {/* Only the result is announced, not the button's label. */}
            <span role="status" className="flex min-w-0">
              {probe.state === "ok" ? (
                <span className="flex items-center gap-1.5 text-sm text-status-ok">
                  <CircleCheck className="size-4" /> {copy.ok}
                </span>
              ) : probe.state === "failed" ? (
                <span className="flex items-start gap-1.5 text-sm text-destructive [overflow-wrap:anywhere]">
                  <CircleX className="mt-0.5 size-4 shrink-0" /> {probe.error}
                </span>
              ) : null}
            </span>
          </div>
        </fieldset>

        {busy ? (
          <p role="status" className="text-sm text-muted-foreground">
            {apps.length > 0
              ? "Testing the storage and redeploying apps that use it…"
              : "Testing the storage and saving it…"}{" "}
            You can close this dialog: the save goes on.
          </p>
        ) : null}
        {saveError ? (
          <InlineError icon className="rounded-md bg-destructive/10 px-3 py-2 [overflow-wrap:anywhere]">
            {saveError}
          </InlineError>
        ) : null}
      </form>
    </ResponsiveDialog>
  );
}
```

The existing tests "shows each problem under its field" (focus starts on the name), "puts the caret at the end of an edit's first field" and "announces only the test result, not the button" (one `role="status"` while idle) keep passing unchanged.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/storages.test.ts src/pages/settings-storages.test.tsx`
Expected: PASS (the list tests still drive phase 1's `StoragesCard`; Task 6 rewrites them).

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/storages.ts web/src/lib/storages.test.ts web/src/components/storage-dialog.tsx web/src/pages/settings-storages.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): type-first storage dialog with S3 extras under Advanced

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 6: Server → Storages — rows, a "⋯" menu, and "Backups of deleted apps"

**Files:**
- Rewrite: `web/src/pages/settings-storages.tsx` (`StoragesCard` → `StoragesSettings`)
- Rewrite: `web/src/components/orphans-dialog.tsx` (on `ResponsiveDialog`, rows, `ConfirmDialog`, raw app id only on hover)
- Modify: `web/src/pages/settings-storages.test.tsx`, `web/src/components/orphans-dialog.test.tsx`
- Modify: `web/src/pages/server-settings.tsx` (`ServerStoragesPage`), `web/src/pages/server-settings.test.tsx`

**Interfaces:**
- Consumes: Task 1 (`RowMenu`, `RefreshButton`, `ListPlaceholder`, `ConfirmDialog` with `onCloseAutoFocus`, `ResponsiveDialog` with `onCloseAutoFocus`); Task 5 (`StorageDialog`, `STORAGE_TYPES`); phase 1 `PageHeader`, `RowList`, `Row`, `EmptyState`; `AppLinks`, `useBackups()` (`listOrphans(storage)`, `deleteOrphan(storage, appId)`), `formatSize`, `Orphan` (`@/lib/backups`), `useNow`, `relativeTime`.
- Produces: `StoragesSettings({ docker }: { docker: Docker })` (`@/pages/settings-storages`) — the whole Storages page with its `PageHeader`; `OrphansDialog` keeps its props (`backups, storage, open, returnFocus?, onOpenChange`).

- [ ] **Step 1: Write the failing tests**

In `web/src/pages/settings-storages.test.tsx`:

- change the import `import { StoragesCard } from "@/pages/settings-storages";` to `import { StoragesSettings } from "@/pages/settings-storages";` and, in `renderCard`, render `<StoragesSettings docker={docker} />` instead of `<StoragesCard docker={docker} />`;
- add after `renderCard`:

```tsx
// pick opens a storage row's "⋯" menu and chooses an item.
async function pick(user: ReturnType<typeof setupUser>, name: string, item: string) {
  await user.click(await screen.findByRole("button", { name: `Actions for ${name}` }));
  await user.click(await screen.findByRole("menuitem", { name: item }));
}
```

- rename `describe("StoragesCard", …)` to `describe("StoragesSettings", …)`;
- replace the tests `it("lists name, type, location and the apps using each", …)`, `it("keeps the location and the apps visible under the name on small screens", …)` and `it("shows an empty state", …)` with:

```tsx
  it("lists name, kind, location and the apps using each", async () => {
    renderCard(fakeDocker());
    expect(screen.getByRole("heading", { level: 1, name: "Storages" })).toBeInTheDocument();
    expect(await screen.findByText("hetzner")).toHaveClass("font-mono");
    expect(screen.getByText("S3 bucket · https://fsn1.example.com/tenants/wf/")).toBeInTheDocument();
    expect(screen.getByText("Folder on this server · /mnt/usb")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Shop" })).toHaveAttribute("href", "/server/srv-1/app/a1");
    expect(screen.getByRole("link", { name: "Blog" })).toHaveAttribute("href", "/server/srv-1/app/a2");
    expect(screen.getByText("Not used")).toBeInTheDocument();
  });

  it("shows an empty state", async () => {
    renderCard(fakeDocker({ listStorages: vi.fn().mockResolvedValue([]) }));
    expect(await screen.findByText(/No storages yet/)).toBeInTheDocument();
  });
```

- in `it("adds a local folder after testing it", …)`: `.closest("tr")` → `.closest("li")`;
- in every test, replace a click on `screen.findByRole("button", { name: "Edit storage <name>" })` / `screen.getByRole("button", { name: "Edit storage <name>" })` with `await pick(user, "<name>", "Edit");` (tests: "edits keeping the secret…", "reports a refused save…", "lets a save that answers after…", "never lets an older test result…", "keeps an edit's stored path-style…", "locks the form…", "puts the caret at the end…");
- replace `it("blocks removing a storage in use and confirms otherwise", …)` and `it("reports load and delete failures", …)` with:

```tsx
  it("explains why a storage in use can't be removed, and confirms otherwise", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderCard(docker);

    await user.click(await screen.findByRole("button", { name: "Actions for hetzner" }));
    const blocked = await screen.findByRole("menuitem", { name: "Remove" });
    expect(blocked).toHaveAttribute("title", "Used by Shop and Blog — remove the references first");
    await user.click(blocked);
    expect(toastWarning).toHaveBeenLastCalledWith("Can't remove hetzner", {
      description: "Used by Shop and Blog — remove the references first.",
    });
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(docker.deleteStorage).not.toHaveBeenCalled();

    await pick(user, "disk", "Remove");
    const confirm = await screen.findByRole("alertdialog");
    expect(confirm).toHaveTextContent("disk is removed from this server. Nothing stored in it is deleted.");
    await user.click(within(confirm).getByRole("button", { name: "Remove storage" }));
    expect(docker.deleteStorage).toHaveBeenCalledWith("disk");
    await waitFor(() => expect(screen.queryByText("disk")).not.toBeInTheDocument());
    expect(toastSuccess).toHaveBeenCalledWith("Storage removed");
    // The row's menu left with the row: focus goes to Add storage.
    await waitFor(() => expect(screen.getByRole("button", { name: /Add storage/ })).toHaveFocus());
  });

  it("reports load and delete failures, keeping the confirmation open", async () => {
    const user = setupUser();
    const docker = fakeDocker({
      listStorages: vi.fn().mockRejectedValueOnce(new Error("agent offline")).mockResolvedValue(STORAGES),
      deleteStorage: vi.fn().mockRejectedValue(new Error("disk is used by Shop")),
    });
    renderCard(docker);
    expect(await screen.findByText("Couldn't load storages.")).toBeInTheDocument();
    expect(toastError).toHaveBeenCalledWith("Couldn't load storages", { description: "agent offline" });

    await user.click(screen.getByRole("button", { name: "Try again" }));
    await pick(user, "disk", "Remove");
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Remove storage" }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith("Couldn't remove storage", { description: "disk is used by Shop" }));
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(screen.getByText("disk")).toBeInTheDocument();
  });

  it("opens Advanced at once on an edit that uses it", async () => {
    const user = setupUser();
    renderCard(fakeDocker());
    await pick(user, "hetzner", "Edit");
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByLabelText("Region")).toHaveValue("fsn1");
    expect(within(dialog).getByLabelText("Prefix")).toHaveValue("wf/");
  });
```

- in `describe("StorageDialog form", …)`, replace `it("shows a storage's backups of apps not on this server", …)` and `it("returns focus to the button that opened a dialog", …)` with:

```tsx
  it("shows the backups of deleted apps kept in a storage", async () => {
    const user = setupUser();
    backups.listOrphans.mockResolvedValue([
      { app_id: "gone-1", app_name: "Old shop", copies: 2, bytes: 2048, deleted_at: 1790812800, purge_at: 1806364800 },
    ]);
    renderCard(fakeDocker());
    await pick(user, "disk", "Backups of deleted apps");
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByText("Old shop")).toBeInTheDocument();
    expect(backups.listOrphans).toHaveBeenCalledWith("disk");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Actions for disk" })).toHaveFocus();
  });

  it("returns focus to what opened a dialog", async () => {
    const user = setupUser();
    renderCard(fakeDocker());
    const add = await screen.findByRole("button", { name: /Add storage/ });
    await user.click(add);
    await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(add).toHaveFocus());

    await pick(user, "disk", "Edit");
    await screen.findByRole("dialog");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.getByRole("button", { name: "Actions for disk" })).toHaveFocus());
  });
```

- delete `describe("StoragesCard layout", …)` (the table and its columns are gone; `RowList` stacks rows on phones itself).

In `web/src/components/orphans-dialog.test.tsx`:

- in `it("lists each app's copies and when they go", …)`, replace everything after `expect(await within(dialog).findByText("Old shop")).toBeInTheDocument();` with:

```tsx
    // A raw app id is never text on screen: only on hover.
    expect(within(dialog).queryByText("gone-1")).not.toBeInTheDocument();
    expect(within(dialog).getByTitle("other-server")).toHaveTextContent("Unknown app");
    expect(within(dialog).getByText("Not deleted automatically (another server's?)")).toBeInTheDocument();
    expect(within(dialog).getByText(/At the next daily sweep/)).toBeInTheDocument();
    expect(within(dialog).getByText(/Kept: no retention limit on this server/)).toBeInTheDocument();
    expect(within(dialog).getByText(/Will be deleted on /)).toBeInTheDocument();
    expect(within(dialog).getByText("2 copies · 2.0 kB · 1 hour ago")).toBeInTheDocument();
```

- in `it("deletes an orphan after a confirmation", …)`: the toast expectation becomes `expect(toastSuccess).toHaveBeenCalledWith("Backups of an unknown app deleted", { description: "6 objects removed from disk." });`, and `await waitFor(() => expect(screen.queryByText("other-server")).not.toBeInTheDocument());` becomes `await waitFor(() => expect(screen.queryByTitle("other-server")).not.toBeInTheDocument());`;
- in `it("warns that a folder nobody marked as deleted may be another server's", …)`: add `expect(confirm).not.toHaveTextContent("other-server");`;
- replace the end of `it("reports a refused delete and a failed list", …)` (from the comment "The confirmation is gone…") with:

```tsx
    // The confirmation stays for another try or Cancel, and the row too.
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Delete the backups of Old shop" })).toBeInTheDocument();
```

In `web/src/pages/server-settings.test.tsx`, change the storages mock to `vi.mock("@/pages/settings-storages", () => ({ StoragesSettings: () => <p>storages page</p> }));`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/pages/settings-storages.test.tsx src/components/orphans-dialog.test.tsx src/pages/server-settings.test.tsx`
Expected: FAIL — `StoragesSettings` does not exist; the orphans dialog still prints the app id.

- [ ] **Step 3: Implement**

Replace `web/src/pages/settings-storages.tsx` with:

```tsx
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Cloud, Folder, HardDrive, Plus } from "lucide-react";
import { toast } from "sonner";

import { AppLinks } from "@/components/app-links";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { ListPlaceholder } from "@/components/list-placeholder";
import { OrphansDialog } from "@/components/orphans-dialog";
import { PageHeader } from "@/components/page-header";
import { RefreshButton } from "@/components/refresh-button";
import { RowMenu } from "@/components/row-menu";
import { Row, RowList } from "@/components/section";
import { StorageDialog } from "@/components/storage-dialog";
import { Button } from "@/components/ui/button";
import { useApps } from "@/context/use-apps";
import { useBackups } from "@/context/use-backups";
import type { Docker } from "@/context/use-docker";
import { returnFocusTo } from "@/lib/focus";
import { STORAGE_TYPES, type Storage, type StorageSaved } from "@/lib/storages";

const byName = (a: Storage, b: Storage) => a.name.localeCompare(b.name);

// listed joins names for a sentence: "A", "A and B", "A, B and C".
const listed = (names: string[]) => new Intl.ListFormat("en-GB", { type: "conjunction" }).format(names);

// StoragesSettings is Settings → Server → Storages: the folders and S3
// buckets backups and apps on this server write to, each with the apps
// using it; edit, look at the backups of deleted apps kept in it, or
// remove one nothing uses.
export function StoragesSettings({ docker }: { docker: Docker }) {
  const { apps } = useApps();
  const backups = useBackups();
  const [items, setItems] = useState<Storage[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [edit, setEdit] = useState<Storage | null>(null);
  const [dialogKey, setDialogKey] = useState(0);
  const [toDelete, setToDelete] = useState<Storage | null>(null);
  // The storage whose backups of deleted apps are shown.
  const [orphansOf, setOrphansOf] = useState<string | null>(null);
  // The dialogs have no trigger of their own: focus goes back to what
  // opened them, or to Add storage when that left with its row.
  const opener = useRef<HTMLElement | null>(null);
  const addButton = useRef<HTMLButtonElement | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setItems(await docker.listStorages());
    } catch (e) {
      toast.error("Couldn't load storages", { description: e instanceof Error ? e.message : undefined });
    } finally {
      setLoading(false);
    }
  }, [docker]);

  useEffect(() => {
    void load();
  }, [load]);

  const names = useMemo(() => new Map(apps.map((a) => [a.id, a.name])), [apps]);

  // A save answers with the stored storage: patch the list in place.
  const saved = ({ storage, redeployed, warnings }: StorageSaved) => {
    const done = edit ? "Storage saved" : "Storage added";
    const description =
      redeployed.length > 0 ? `Redeployed ${redeployed.map((id) => names.get(id) ?? id).join(", ")}.` : undefined;
    if (warnings.length > 0) {
      // It asks for action (each warning names its app): it stays until dismissed.
      toast.warning(`${done}, but not every app was redeployed`, {
        // One line each (toast descriptions keep newlines: index.css).
        description: [description, ...warnings].filter(Boolean).join("\n"),
        duration: Infinity,
        closeButton: true,
      });
    } else {
      toast.success(done, { description });
    }
    setItems((prev) => [...(prev ?? []).filter((s) => s.name !== storage.name), storage].sort(byName));
  };

  const remove = async (name: string) => {
    try {
      await docker.deleteStorage(name);
      toast.success("Storage removed");
      setItems((prev) => prev && prev.filter((s) => s.name !== name));
    } catch (e) {
      toast.error("Couldn't remove storage", { description: e instanceof Error ? e.message : undefined });
      throw e;
    }
  };

  const openDialog = (row: Storage | null, from: HTMLElement | null) => {
    opener.current = from;
    setEdit(row);
    setDialogKey((k) => k + 1);
    setDialogOpen(true);
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="Storages"
        subtitle="Folders and S3 buckets that backups and apps on this server can write to."
        actions={
          <>
            <RefreshButton label="Refresh storages" loading={loading} onClick={() => void load()} />
            <Button ref={addButton} onClick={(e) => openDialog(null, e.currentTarget)}>
              <Plus className="size-4" /> Add storage
            </Button>
          </>
        }
      />
      {items === null ? (
        <ListPlaceholder loading={loading} failed="Couldn't load storages." onRetry={() => void load()} />
      ) : items.length === 0 ? (
        <EmptyState
          icon={HardDrive}
          title="No storages yet. Add a folder, such as one on a USB disk under /mnt, or an S3 bucket."
        />
      ) : (
        <RowList label="Storages">
          {items.map((s) => {
            const used = s.apps ?? [];
            // Why the storage can't be removed (null: it can).
            const blocked =
              used.length > 0
                ? `Used by ${listed(used.map((id) => names.get(id) ?? id))} — remove the references first`
                : null;
            const Icon = s.type === "s3" ? Cloud : Folder;
            return (
              <Row
                key={s.name}
                leading={
                  <span className="flex size-9 items-center justify-center rounded-lg bg-muted">
                    <Icon className="size-4 text-muted-foreground" aria-hidden />
                  </span>
                }
                title={<span className="font-mono">{s.name}</span>}
                description={`${STORAGE_TYPES[s.type]} · ${s.location}`}
                meta={<AppLinks ids={used} names={names} />}
                actions={
                  <RowMenu
                    label={`Actions for ${s.name}`}
                    items={[
                      { label: "Edit", onSelect: (trigger) => openDialog(s, trigger) },
                      {
                        // Copies of deleted apps stay in the storage: findable here.
                        label: "Backups of deleted apps",
                        onSelect: (trigger) => {
                          opener.current = trigger;
                          setOrphansOf(s.name);
                        },
                      },
                      {
                        label: "Remove",
                        destructive: true,
                        title: blocked ?? undefined,
                        onSelect: (trigger) => {
                          // In use: the item says why (title), and choosing it says so on screen.
                          if (blocked) {
                            toast.warning(`Can't remove ${s.name}`, { description: `${blocked}.` });
                            return;
                          }
                          opener.current = trigger;
                          setToDelete(s);
                        },
                      },
                    ]}
                  />
                }
              />
            );
          })}
        </RowList>
      )}
      <StorageDialog
        key={dialogKey}
        docker={docker}
        open={dialogOpen}
        edit={edit}
        existing={(items ?? []).map((s) => s.name)}
        usedBy={(edit?.apps ?? []).map((id) => names.get(id) ?? id)}
        returnFocus={opener}
        onOpenChange={setDialogOpen}
        onSaved={saved}
      />
      {orphansOf ? (
        <OrphansDialog
          key={orphansOf}
          backups={backups}
          storage={orphansOf}
          open
          returnFocus={opener}
          onOpenChange={(o) => !o && setOrphansOf(null)}
        />
      ) : null}
      <ConfirmDialog
        open={toDelete !== null}
        onOpenChange={(o) => !o && setToDelete(null)}
        title="Remove this storage?"
        description={
          <>
            <code className="font-mono text-foreground [overflow-wrap:anywhere]">{toDelete?.name}</code> is removed from
            this server. Nothing stored in it is deleted.
          </>
        }
        confirmLabel="Remove storage"
        pendingLabel="Removing…"
        onCloseAutoFocus={returnFocusTo(opener, addButton)}
        onConfirm={() => (toDelete ? remove(toDelete.name) : undefined)}
      />
    </div>
  );
}
```

Replace `web/src/components/orphans-dialog.tsx` with:

```tsx
import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { InlineError } from "@/components/inline-error";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { Row, RowList } from "@/components/section";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import type { Backups } from "@/context/use-backups";
import { useNow } from "@/hooks/use-now";
import { formatSize, type Orphan } from "@/lib/backups";
import { returnFocusTo } from "@/lib/focus";
import { cn, relativeTime } from "@/lib/utils";

const day = (unix: number) => new Date(unix * 1000).toLocaleDateString();

// purgeLabel says when the daily sweep deletes an orphan's last copy: a
// deleted app's folder goes at the retention limit; another server's is
// never deleted automatically.
function purgeLabel(o: Orphan, now: number): string {
  if (!o.deleted_at) return "Not deleted automatically (another server's?)";
  if (!o.purge_at) return "Kept: no retention limit on this server";
  return o.purge_at * 1000 <= now ? "At the next daily sweep" : `Will be deleted on ${day(o.purge_at)}`;
}

const copiesLabel = (n: number) => `${n} ${n === 1 ? "copy" : "copies"}`;

// shownName is how an orphan is named on screen: its app's name, never its
// raw id (that one sits in a title, for hover).
const shownName = (o: Orphan) => o.app_name || "an unknown app";

// OrphansDialog lists the backups in a storage that belong to no app on
// this server: a deleted app's (named from its marker) or another server's
// sharing the storage, each deletable after a confirmation. The parent
// remounts it (key) per opening; returnFocus is where focus goes back on
// close.
export function OrphansDialog({
  backups,
  storage,
  open,
  returnFocus,
  onOpenChange,
}: {
  backups: Backups;
  storage: string;
  open: boolean;
  returnFocus?: RefObject<HTMLElement | null>;
  onOpenChange: (open: boolean) => void;
}) {
  const [items, setItems] = useState<Orphan[] | null>(null);
  const [error, setError] = useState("");
  const [confirm, setConfirm] = useState<Orphan | null>(null);
  const now = useNow();
  // The Delete button that opened the confirmation, and the footer's Close
  // button: focus goes back to the first one still on the page.
  const opener = useRef<HTMLElement | null>(null);
  const closeButton = useRef<HTMLButtonElement>(null);

  const load = useCallback(async () => {
    setError("");
    try {
      setItems(await backups.listOrphans(storage));
    } catch (e) {
      setError(e instanceof Error ? e.message : "The storage could not be listed.");
    }
  }, [backups, storage]);

  useEffect(() => {
    if (open) void load();
  }, [open, load]);

  const remove = async (o: Orphan) => {
    try {
      const n = await backups.deleteOrphan(storage, o.app_id);
      toast.success(`Backups of ${shownName(o)} deleted`, {
        description: `${n} ${n === 1 ? "object" : "objects"} removed from ${storage}.`,
      });
      setItems((prev) => prev && prev.filter((x) => x.app_id !== o.app_id));
    } catch (e) {
      toast.error("Couldn't delete the backups", { description: e instanceof Error ? e.message : undefined });
      throw e;
    }
  };

  return (
    <ResponsiveDialog
      open={open}
      onOpenChange={onOpenChange}
      title="Backups of deleted apps"
      description={
        <>
          In <code className="font-mono text-foreground">{storage}</code>: copies of apps deleted from this server, or of
          another server's apps sharing this storage.
        </>
      }
      className="sm:max-w-2xl"
      onCloseAutoFocus={returnFocusTo(returnFocus)}
      footer={
        <Button ref={closeButton} variant="outline" onClick={() => onOpenChange(false)}>
          Close
        </Button>
      }
    >
      {error ? (
        <div className="grid justify-items-start gap-2">
          <InlineError>{error}</InlineError>
          <Button variant="outline" size="sm" onClick={() => void load()}>
            Try again
          </Button>
        </div>
      ) : items === null ? (
        <div className="flex h-24 items-center justify-center">
          <Spinner />
        </div>
      ) : items.length === 0 ? (
        <EmptyState title="Every backup in this storage belongs to an app on this server." />
      ) : (
        <RowList label="Backups of deleted apps">
          {items.map((o) => (
            <Row
              key={o.app_id}
              title={<span title={o.app_id}>{o.app_name || "Unknown app"}</span>}
              description={[
                copiesLabel(o.copies),
                formatSize(o.bytes),
                o.newest ? relativeTime(new Date(o.newest * 1000), now) : "",
              ]
                .filter(Boolean)
                .join(" · ")}
              meta={
                <span className={cn(!o.deleted_at && "text-muted-foreground")}>
                  {o.deleted_at ? `Deleted ${day(o.deleted_at)} · ` : ""}
                  {purgeLabel(o, now)}
                </span>
              }
              actions={
                <Button
                  size="sm"
                  variant="ghost"
                  className="text-destructive hover:text-destructive"
                  aria-label={`Delete the backups of ${o.app_name || o.app_id}`}
                  onClick={(e) => {
                    opener.current = e.currentTarget;
                    setConfirm(o);
                  }}
                >
                  Delete
                </Button>
              }
            />
          ))}
        </RowList>
      )}
      {/* A dialog of its own, so it is on screen however far the list is
          scrolled, takes focus (on Cancel), and Escape closes only it. */}
      <ConfirmDialog
        open={confirm !== null}
        onOpenChange={(o) => !o && setConfirm(null)}
        title="Delete these backups?"
        description={
          confirm ? (
            confirm.deleted_at ? (
              <>
                Delete every copy of <strong>{shownName(confirm)}</strong> ({copiesLabel(confirm.copies)},{" "}
                {formatSize(confirm.bytes)}) from {storage}? This cannot be undone.
              </>
            ) : (
              <>
                This folder is not marked as deleted by this server, so it may hold the live backups of an app on another
                server that shares this storage. Deleting every copy of <strong>{shownName(confirm)}</strong> (
                {copiesLabel(confirm.copies)}, {formatSize(confirm.bytes)}) from {storage} is permanent, and that app
                would lose its backups. Delete only if you are sure nothing uses them.
              </>
            )
          ) : null
        }
        confirmLabel="Delete backups"
        pendingLabel="Deleting…"
        onCloseAutoFocus={returnFocusTo(opener, closeButton)}
        onConfirm={() => (confirm ? remove(confirm) : undefined)}
      />
    </ResponsiveDialog>
  );
}
```

In `web/src/pages/server-settings.tsx`, replace the body of `ServerStoragesPage` after the guard with

```tsx
  return <StoragesSettings key={activeServer.id} docker={docker} />;
```

and the import `import { StoragesCard } from "@/pages/settings-storages";` with `import { StoragesSettings } from "@/pages/settings-storages";`. (`PageHeader` stays: General uses it.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/pages/settings-storages.test.tsx src/components/orphans-dialog.test.tsx src/pages/server-settings.test.tsx src/lib/storages.test.ts`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/pages/settings-storages.tsx web/src/pages/settings-storages.test.tsx web/src/components/orphans-dialog.tsx web/src/components/orphans-dialog.test.tsx web/src/pages/server-settings.tsx web/src/pages/server-settings.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): Settings → Storages as rows with backups of deleted apps behind the menu

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 7: Organization → General and Members

**Files:**
- Create: `web/src/lib/members.ts`, `web/src/lib/members.test.ts`
- Rewrite: `web/src/pages/org-members.tsx`, `web/src/pages/org-members.test.tsx`

**Interfaces:**
- Consumes: Task 1 (`RowMenu`, `CopyField`, `ListPlaceholder`, `ConfirmDialog`, `ResponsiveDialog`); phase 1 `PageHeader`, `RowList`, `Row`; `IconPicker`, `Avatar`/`AvatarFallback` (`@/components/ui/avatar`), `useProfile()`, `readApiError`, `apiBaseUrl`; routes `/api/v1/org/get-organization`, `update-organization`, `get-members`, `create-user`, `reset-member-password`, `remove-member` (unchanged bodies).
- Produces: `initials(name: string, email: string): string` (`@/lib/members`); default `OrgMembersPage({ section }: { section: "organization" | "members" })` — `section` is now required (phase 1's route table always passes it).

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/members.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import { initials } from "@/lib/members";

describe("initials", () => {
  it("takes the first letters of the first two words, else of the email", () => {
    expect(initials("Ann Lee", "ann@example.test")).toBe("AL");
    expect(initials("bob", "bob@example.test")).toBe("B");
    expect(initials("", "cy.dee@example.test")).toBe("CD");
    expect(initials(" ", "")).toBe("?");
  });
});
```

Replace `web/src/pages/org-members.test.tsx` with:

```tsx
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/members.test.ts src/pages/org-members.test.tsx`
Expected: FAIL — `@/lib/members` does not exist; the page still has tables, an Edit dialog and `window.confirm`.

- [ ] **Step 3: Implement**

Create `web/src/lib/members.ts`:

```ts
// initials is a member's avatar text: the first letters of the first two
// words of the name, or of the email when there is no name; "?" for
// neither.
export function initials(name: string, email: string): string {
  const words = (name.trim() || email).split(/[\s@._-]+/).filter(Boolean);
  return (
    words
      .slice(0, 2)
      .map((w) => w.charAt(0).toUpperCase())
      .join("") || "?"
  );
}
```

Replace `web/src/pages/org-members.tsx` with:

```tsx
import { useCallback, useEffect, useState } from "react";
import { Plus } from "lucide-react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { CopyField } from "@/components/copy-field";
import { IconPicker } from "@/components/icon-picker";
import { ListPlaceholder } from "@/components/list-placeholder";
import { PageHeader } from "@/components/page-header";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { RowMenu } from "@/components/row-menu";
import { Row, RowList } from "@/components/section";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { apiBaseUrl } from "@/config";
import { useProfile } from "@/context/use-profile";
import { readApiError } from "@/lib/api";
import { initials } from "@/lib/members";

type Organization = { org_id: string; name: string; icon: string; color: string };

type Member = {
  id: string;
  name: string;
  email: string;
  provider: string;
  last_seen_at: string;
  created_at: string;
};

type TempPassword = { name: string; temp_password: string };

const DEFAULT_COLOR = "#64748b";

const errorText = (e: unknown) => (e instanceof Error ? e.message : undefined);

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${apiBaseUrl}${path}`, { credentials: "include", ...init });
  if (!res.ok) throw await readApiError(res);
  return ((await res.json()) as { data: T }).data;
}

function postJSON<T>(path: string, payload: unknown): Promise<T> {
  return api<T>(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
}

// OrganizationSettings is Settings → Organization → General: the name,
// icon and colour everyone in the organization sees, edited in place.
function OrganizationSettings() {
  const [org, setOrg] = useState<Organization | null>(null);
  const [failed, setFailed] = useState(false);
  const [name, setName] = useState("");
  const [icon, setIcon] = useState("");
  const [color, setColor] = useState(DEFAULT_COLOR);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setFailed(false);
    try {
      const o = await api<Organization>("/api/v1/org/get-organization");
      setOrg(o);
      setName(o.name);
      setIcon(o.icon);
      setColor(o.color || DEFAULT_COLOR);
    } catch {
      setFailed(true);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const trimmed = name.trim();
  const dirty = org !== null && (trimmed !== org.name || icon !== org.icon || color !== (org.color || DEFAULT_COLOR));

  const save = async () => {
    if (!org) return;
    setBusy(true);
    try {
      await postJSON("/api/v1/org/update-organization", { name: trimmed, icon, color });
      toast.success("Organization saved");
      setOrg({ ...org, name: trimmed, icon, color });
    } catch (e) {
      toast.error("Couldn't save the organization", { description: errorText(e) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader title="Organization" subtitle="Its name, icon and colour, as everyone in it sees them." />
      {org === null ? (
        <ListPlaceholder loading={!failed} failed="Couldn't load the organization." onRetry={() => void load()} />
      ) : (
        <form
          className="max-w-lg space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (dirty && trimmed && !busy) void save();
          }}
        >
          <div className="flex flex-wrap items-end gap-3">
            <div className="space-y-2">
              <Label>Icon</Label>
              <IconPicker value={icon} name={name} color={color} onChange={setIcon} />
            </div>
            <div className="min-w-0 flex-1 basis-48 space-y-2">
              <Label htmlFor="org-name">Name</Label>
              <Input
                id="org-name"
                value={name}
                maxLength={64}
                aria-invalid={!trimmed || undefined}
                aria-describedby={!trimmed ? "org-name-error" : undefined}
                onChange={(e) => setName(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="org-color">Colour</Label>
              <Input
                id="org-color"
                type="color"
                className="h-9 w-16 p-1"
                value={color}
                onChange={(e) => setColor(e.target.value)}
              />
            </div>
          </div>
          {!trimmed ? (
            <p id="org-name-error" role="alert" className="text-xs text-destructive">
              Give the organization a name.
            </p>
          ) : null}
          <Button type="submit" disabled={!dirty || !trimmed || busy}>
            {busy ? "Saving…" : "Save"}
          </Button>
        </form>
      )}
    </div>
  );
}

// AddMemberDialog creates an account with a one-time temporary password,
// then shows that password (the only time it is visible: the server keeps
// a bcrypt hash).
function AddMemberDialog({
  open,
  onOpenChange,
  onAdded,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onAdded: () => void;
}) {
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [created, setCreated] = useState<{ email: string; temp_password: string } | null>(null);

  // Nothing (the temporary password least of all) outlives the dialog.
  const changeOpen = (next: boolean) => {
    if (!next) {
      setName("");
      setEmail("");
      setError("");
      setCreated(null);
    }
    onOpenChange(next);
  };

  const create = async () => {
    if (!name.trim() || !email.trim()) {
      setError("Enter a name and an email.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      setCreated(
        await postJSON<{ email: string; temp_password: string }>("/api/v1/org/create-user", {
          name: name.trim(),
          email: email.trim(),
        }),
      );
      onAdded();
    } catch (e) {
      setError(errorText(e) ?? "Couldn't add the member.");
    } finally {
      setBusy(false);
    }
  };

  if (created) {
    return (
      <ResponsiveDialog
        open={open}
        onOpenChange={changeOpen}
        title="Member added"
        description={`Give ${created.email} this temporary password. It's shown only once; they choose their own when they first sign in.`}
        footer={<Button onClick={() => changeOpen(false)}>Done</Button>}
      >
        <CopyField value={created.temp_password} label="Temporary password" />
      </ResponsiveDialog>
    );
  }

  return (
    <ResponsiveDialog
      open={open}
      onOpenChange={changeOpen}
      title="Add member"
      description="Creates an account in this organization with a temporary password."
      footer={
        <>
          <Button type="button" variant="outline" disabled={busy} onClick={() => changeOpen(false)}>
            Cancel
          </Button>
          <Button type="submit" form="member-form" disabled={busy}>
            {busy ? "Adding…" : "Add"}
          </Button>
        </>
      }
    >
      <form
        id="member-form"
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          void create();
        }}
      >
        <div className="space-y-2">
          <Label htmlFor="member-name">Name</Label>
          <Input id="member-name" value={name} maxLength={64} onChange={(e) => setName(e.target.value)} />
        </div>
        <div className="space-y-2">
          <Label htmlFor="member-email">Email</Label>
          <Input id="member-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
        </div>
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : null}
      </form>
    </ResponsiveDialog>
  );
}

// MembersSettings is Settings → Organization → Members: everyone in the
// organization (there are no roles: any member manages the others), with
// Reset password and Remove for everyone but you, and Add member.
function MembersSettings() {
  const { profile } = useProfile();
  const [members, setMembers] = useState<Member[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [adding, setAdding] = useState(false);
  const [toReset, setToReset] = useState<Member | null>(null);
  const [toRemove, setToRemove] = useState<Member | null>(null);
  const [reset, setReset] = useState<TempPassword | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      setMembers((await api<Member[] | null>("/api/v1/org/get-members")) ?? []);
    } catch (e) {
      toast.error("Couldn't load the members", { description: errorText(e) });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const resetPassword = async (m: Member) => {
    try {
      const data = await postJSON<{ temp_password: string }>("/api/v1/org/reset-member-password", { user_id: m.id });
      setReset({ name: m.name, temp_password: data.temp_password });
    } catch (e) {
      toast.error("Couldn't reset the password", { description: errorText(e) });
      throw e;
    }
  };

  const remove = async (m: Member) => {
    try {
      await postJSON("/api/v1/org/remove-member", { user_id: m.id });
      toast.success(`${m.name} removed`);
      void refresh();
    } catch (e) {
      toast.error("Couldn't remove the member", { description: errorText(e) });
      throw e;
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="Members"
        subtitle="Everyone here can do everything in the organization."
        actions={
          <Button onClick={() => setAdding(true)}>
            <Plus className="size-4" /> Add member
          </Button>
        }
      />
      {members === null ? (
        <ListPlaceholder loading={loading} failed="Couldn't load the members." onRetry={() => void refresh()} />
      ) : (
        <RowList label="Members">
          {members.map((m) => {
            const isSelf = m.id === profile?.user_id;
            return (
              <Row
                key={m.id}
                leading={
                  <Avatar className="size-9">
                    <AvatarFallback className="text-xs">{initials(m.name, m.email)}</AvatarFallback>
                  </Avatar>
                }
                title={
                  <>
                    {m.name}
                    {isSelf ? <span className="font-normal text-muted-foreground"> (you)</span> : null}
                  </>
                }
                description={m.email || `${m.provider || "External"} account`}
                actions={
                  isSelf ? null : (
                    <RowMenu
                      label={`Actions for ${m.name}`}
                      items={[
                        m.email ? { label: "Reset password", onSelect: () => setToReset(m) } : null,
                        { label: "Remove", destructive: true, onSelect: () => setToRemove(m) },
                      ]}
                    />
                  )
                }
              />
            );
          })}
        </RowList>
      )}
      <AddMemberDialog open={adding} onOpenChange={setAdding} onAdded={() => void refresh()} />
      <ConfirmDialog
        open={toReset !== null}
        onOpenChange={(o) => !o && setToReset(null)}
        title={`Reset ${toReset?.name ?? "this member"}'s password?`}
        description="Their current password stops working at once. You get a temporary password to give them; they choose their own when they next sign in."
        confirmLabel="Reset password"
        pendingLabel="Resetting…"
        onConfirm={() => (toReset ? resetPassword(toReset) : undefined)}
      />
      <ConfirmDialog
        open={toRemove !== null}
        onOpenChange={(o) => !o && setToRemove(null)}
        title={`Remove ${toRemove?.name ?? "this member"}?`}
        description="Their account, API tokens and access are deleted for good."
        confirmLabel="Remove member"
        pendingLabel="Removing…"
        onConfirm={() => (toRemove ? remove(toRemove) : undefined)}
      />
      <ResponsiveDialog
        open={reset !== null}
        onOpenChange={(o) => !o && setReset(null)}
        title="Password reset"
        description={`A temporary password for ${reset?.name ?? ""}. It's shown only once; they choose their own when they next sign in.`}
        footer={<Button onClick={() => setReset(null)}>Done</Button>}
      >
        {reset ? <CopyField value={reset.temp_password} label="Temporary password" /> : null}
      </ResponsiveDialog>
    </div>
  );
}

// OrgMembersPage is Settings → Organization → General (section
// "organization") or Members (section "members").
export default function OrgMembersPage({ section }: { section: "organization" | "members" }) {
  return section === "organization" ? <OrganizationSettings /> : <MembersSettings />;
}
```

Check nothing else renders `<OrgMembersPage />` without a section:

```bash
grep -rn "OrgMembersPage" web/src --include=*.tsx | grep -v "section=" | grep -v "^web/src/pages/org-members"
```

Expected: only the `import` lines (phase 1's `routes.tsx` passes `section` on both routes, and `routes.test.tsx` mocks the module).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/members.test.ts src/pages/org-members.test.tsx src/routes.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/members.ts web/src/lib/members.test.ts web/src/pages/org-members.tsx web/src/pages/org-members.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): Organization General edited in place, Members as rows with confirmed actions

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 8: Shared variables — one list model and one hook for both scopes

**Files:**
- Modify: `web/src/lib/shared-variables.ts`, `web/src/lib/shared-variables.test.ts` (`variableRows`, `failedServers`)
- Create: `web/src/hooks/use-shared-variables-settings.ts`, `web/src/hooks/use-shared-variables-settings.test.tsx`

**Interfaces:**
- Consumes: `SharedVariablesApi` and the types of `@/lib/shared-variables` (`OrgVariables`, `ServerVariables`, `EncryptionKeys`, `SharedVariable`, `SaveVariable`, `SyncState`, `VariableScope`, `VarsReloaded`), `applySyncEvent`, `serverNameMap`; `useNotifications()`; `DialogMode` (type, `@/components/shared-variable-dialog`).
- Produces:
  - `VariableRow = { variable: SharedVariable; scopeLabel: string; overriddenHere: boolean }`; `variableRows(org: SharedVariable[], server: SharedVariable[], serverName: string): VariableRow[]` — one list sorted by name, the org's row first when both scopes define a name.
  - `failedServers(sync: Record<string, SyncState>, names: Map<string, string>): { id: string; name: string }[]` — servers whose last sync failed, by name.
  - `useSharedVariablesSettings(api: SharedVariablesApi, serverId: string | null)` (`@/hooks/use-shared-variables-settings`) returning `{ org: OrgVariables | null; server: ServerVariables | null; keys: EncryptionKeys; serverNames: Map<string, string>; sync: Record<string, SyncState>; loadFailed: boolean; dialog: DialogMode | null; dialogKey: number; openDialog(mode: DialogMode): void; closeDialog(): void; toDelete: SharedVariable | null; setToDelete(v: SharedVariable | null): void; save(body: SaveVariable, scope: VariableScope): Promise<void>; remove(v: SharedVariable): Promise<void>; sendAgain(serverIds: string[]): void; resendVariable(v: SharedVariable): void; reloadStale(): void; reloadVariable(v: SharedVariable): void; load(): Promise<void> }`.
  - Route mapping (identical to the two old pages): `save(body, "server")` → `api.saveServer(serverId, body)`, `save(body, "org")` → `api.saveOrg(body)`; `remove(v)` → `api.deleteServer(serverId, v.id)` for a server variable, `api.deleteOrg(v.id)` for an org one; `sendAgain([id])` → `api.resync({ server_id: id })`, `sendAgain([a, b, …])` → `api.resync({})`; `resendVariable(v)` → `api.resync({ variable_id: v.id })`; `reloadStale()` → `api.reload({ server_id: serverId })`; `reloadVariable(v)` → `api.reload({ server_id: serverId, names: [v.name] })` for a server variable, `api.reload({ variable_id: v.id })` for an org one.

- [ ] **Step 1: Write the failing tests**

In `web/src/lib/shared-variables.test.ts`, add `failedServers` and `variableRows` to the `import { … } from "@/lib/shared-variables"` list and append:

```ts
describe("one list for both scopes", () => {
  const v = (over: Partial<SharedVariable>): SharedVariable => ({
    id: "x", name: "X", scope: "org", is_secret: false, value: "v", description: "",
    updated_at: "2026-10-11T00:00:00Z", updated_by: "u1", missing_on: [], ...over,
  });

  it("merges the org's and this server's variables by name, org first", () => {
    const rows = variableRows(
      [v({ id: "o2", name: "SMTP_PASS" }), v({ id: "o1", name: "SMTP_HOST" })],
      [v({ id: "s1", name: "SMTP_HOST", scope: "server" }), v({ id: "s2", name: "A_LOCAL", scope: "server" })],
      "alpha",
    );
    expect(rows.map((r) => [r.variable.id, r.scopeLabel, r.overriddenHere])).toEqual([
      ["s2", "alpha only", false],
      ["o1", "All servers", true],
      ["s1", "alpha only", false],
      ["o2", "All servers", false],
    ]);
  });

  it("names the servers whose sync failed", () => {
    const names = new Map([["s1", "beta"], ["s2", "alpha"]]);
    expect(failedServers({ s1: "failed", s2: "failed", s3: "synced" }, names)).toEqual([
      { id: "s2", name: "alpha" },
      { id: "s1", name: "beta" },
    ]);
    expect(failedServers({ s4: "failed" }, names)).toEqual([{ id: "s4", name: "s4" }]);
    expect(failedServers({ s1: "pending" }, names)).toEqual([]);
  });
});
```

Create `web/src/hooks/use-shared-variables-settings.test.tsx`:

```tsx
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { toast } from "sonner";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import { useSharedVariablesSettings } from "@/hooks/use-shared-variables-settings";
import type {
  EncryptionKeys,
  OrgVariables,
  ServerVariables,
  SharedVariable,
  SharedVariablesApi,
} from "@/lib/shared-variables";
import { fakeNotifications } from "@/test/notifications";

const T = "2026-10-11T00:00:00Z";
const plain = (over: Partial<SharedVariable>): SharedVariable => ({
  id: "x", name: "X", scope: "org", is_secret: false, value: "v", description: "",
  updated_at: T, updated_by: "u", missing_on: [], ...over,
});
const ORG: OrgVariables = {
  variables: [plain({ id: "o1", name: "SMTP_HOST", value: "mail" })],
  servers: [
    { server_id: "srv-a", name: "alpha", has_key: true, revision: 3, agent_revision: 3, state: "synced" },
    { server_id: "srv-b", name: "beta", has_key: true, revision: 3, agent_revision: 1, state: "pending" },
  ],
};
const server = (id: string, value: string): ServerVariables => ({
  variables: [plain({ id: `s-${id}`, name: "LOCAL_DIR", scope: "server", value })],
  inherited: [],
  builtins: [{ name: "WF_SERVER_NAME", value: id, description: "This server's name" }],
  missing_org_secrets: [],
  sync: { server_id: id, name: id, has_key: true, revision: 3, agent_revision: 2, state: "pending" },
});
const KEYS: EncryptionKeys = {
  servers: [
    { server_id: "srv-a", name: "alpha", public_key: "KA" },
    { server_id: "srv-b", name: "beta", public_key: "KB" },
  ],
  pending_servers: [],
};

function fakeApi() {
  return {
    listOrg: vi.fn().mockResolvedValue(ORG),
    listServer: vi.fn((id: string) => Promise.resolve(server(id, `dir-${id}`))),
    keys: vi.fn().mockResolvedValue(KEYS),
    saveOrg: vi.fn().mockResolvedValue({}),
    saveServer: vi.fn().mockResolvedValue({}),
    deleteOrg: vi.fn().mockResolvedValue(undefined),
    deleteServer: vi.fn().mockResolvedValue(undefined),
    resync: vi.fn().mockResolvedValue([]),
    reload: vi.fn().mockResolvedValue([]),
  };
}

function setup(api: ReturnType<typeof fakeApi>, serverId: string | null) {
  const sse = fakeNotifications();
  const wrapper = ({ children }: { children: ReactNode }) => <sse.Provider>{children}</sse.Provider>;
  const view = renderHook(
    ({ id }: { id: string | null }) => useSharedVariablesSettings(api as unknown as SharedVariablesApi, id),
    { wrapper, initialProps: { id: serverId } },
  );
  return { ...view, emit: (n: Parameters<typeof sse.emit>[0]) => act(() => sse.emit(n)) };
}

describe("useSharedVariablesSettings", () => {
  beforeEach(() => vi.clearAllMocks());

  it("loads the org's variables, this server's and the keys", async () => {
    const api = fakeApi();
    const { result } = setup(api, "srv-a");
    await waitFor(() => expect(result.current.server?.variables[0].value).toBe("dir-srv-a"));
    expect(result.current.org?.variables[0].name).toBe("SMTP_HOST");
    expect(api.listServer).toHaveBeenCalledWith("srv-a");
    // Each server's sync state: the org list's, then this server's own report.
    expect(result.current.sync).toEqual({ "srv-a": "pending", "srv-b": "pending" });
    expect(result.current.serverNames.get("srv-b")).toBe("beta");
  });

  it("loads only the org's without a server", async () => {
    const api = fakeApi();
    const { result } = setup(api, null);
    await waitFor(() => expect(result.current.org).not.toBeNull());
    expect(result.current.server).toBeNull();
    expect(api.listServer).not.toHaveBeenCalled();
  });

  it("never shows the previous server's rows after a switch", async () => {
    const api = fakeApi();
    const { result, rerender } = setup(api, "srv-a");
    await waitFor(() => expect(result.current.server).not.toBeNull());
    api.listServer.mockReturnValue(new Promise(() => {})); // the new server's answer never lands
    rerender({ id: "srv-b" });
    expect(result.current.server).toBeNull();
    expect(result.current.org).toBeNull();
  });

  it("drops a late answer for a server switched away from", async () => {
    const api = fakeApi();
    let lateA: (v: ServerVariables) => void = () => {};
    api.listServer.mockImplementation((id: string) =>
      id === "srv-a" ? new Promise<ServerVariables>((r) => (lateA = r)) : Promise.resolve(server(id, "from-b")),
    );
    const { result, rerender } = setup(api, "srv-a");
    rerender({ id: "srv-b" });
    await waitFor(() => expect(result.current.server?.variables[0].value).toBe("from-b"));
    await act(async () => lateA(server("srv-a", "late")));
    expect(result.current.server?.variables[0].value).toBe("from-b");
  });

  it("follows sync states over SSE and toasts a failed reload once per burst", async () => {
    const api = fakeApi();
    const { result, emit } = setup(api, "srv-a");
    await waitFor(() => expect(result.current.org).not.toBeNull());
    emit({ type: "vars.sync_failed", ref: "", payload: { server_id: "srv-b", error: "boom" } });
    expect(result.current.sync["srv-b"]).toBe("failed");
    emit({ type: "vars.synced", ref: "", payload: { server_id: "srv-b", revision: 3 } });
    expect(result.current.sync["srv-b"]).toBe("synced");
    emit({ type: "vars.reloaded", ref: "", payload: { server_id: "srv-b", app_id: "a", ok: false, error: "compose failed", names: [] } });
    expect(toast.error).toHaveBeenCalledWith("Couldn't reload an app", { id: "vars-reload-failed", description: "compose failed" });
  });

  it("saves and deletes in the variable's own scope", async () => {
    const api = fakeApi();
    const { result } = setup(api, "srv-a");
    await waitFor(() => expect(result.current.server).not.toBeNull());
    const body = { name: "A", is_secret: false as const, value: "1", description: "" };
    await act(() => result.current.save(body, "server"));
    expect(api.saveServer).toHaveBeenCalledWith("srv-a", body);
    await act(() => result.current.save(body, "org"));
    expect(api.saveOrg).toHaveBeenCalledWith(body);
    await act(() => result.current.remove(result.current.server!.variables[0]));
    expect(api.deleteServer).toHaveBeenCalledWith("srv-a", "s-srv-a");
    await act(() => result.current.remove(result.current.org!.variables[0]));
    expect(api.deleteOrg).toHaveBeenCalledWith("o1");
  });

  it("reports a refused delete and rethrows it", async () => {
    const api = fakeApi();
    api.deleteOrg.mockRejectedValue(new Error("nope"));
    const { result } = setup(api, null);
    await waitFor(() => expect(result.current.org).not.toBeNull());
    await expect(result.current.remove(result.current.org!.variables[0])).rejects.toThrow("nope");
    expect(toast.error).toHaveBeenCalledWith("Couldn't delete the variable", { description: "nope" });
  });

  it("sends again to failed servers, marking them pending until the answer, back on an error", async () => {
    const api = fakeApi();
    const { result, emit } = setup(api, "srv-a");
    await waitFor(() => expect(result.current.org).not.toBeNull());
    emit({ type: "vars.sync_failed", ref: "", payload: { server_id: "srv-b" } });
    act(() => result.current.sendAgain(["srv-b"]));
    expect(api.resync).toHaveBeenCalledWith({ server_id: "srv-b" });
    expect(result.current.sync["srv-b"]).toBe("pending");
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Sent again"));

    api.resync.mockRejectedValueOnce(new Error("offline"));
    emit({ type: "vars.sync_failed", ref: "", payload: { server_id: "srv-a" } });
    act(() => result.current.sendAgain(["srv-a", "srv-b"]));
    expect(api.resync).toHaveBeenLastCalledWith({});
    await waitFor(() => expect(result.current.sync["srv-a"]).toBe("failed"));
  });

  it("reloads like the old pages did", async () => {
    const api = fakeApi();
    const { result } = setup(api, "srv-a");
    await waitFor(() => expect(result.current.server).not.toBeNull());
    act(() => result.current.reloadStale());
    expect(api.reload).toHaveBeenCalledWith({ server_id: "srv-a" });
    act(() => result.current.reloadVariable(result.current.server!.variables[0]));
    expect(api.reload).toHaveBeenCalledWith({ server_id: "srv-a", names: ["LOCAL_DIR"] });
    act(() => result.current.reloadVariable(result.current.org!.variables[0]));
    expect(api.reload).toHaveBeenCalledWith({ variable_id: "o1" });
    act(() => result.current.resendVariable(result.current.org!.variables[0]));
    expect(api.resync).toHaveBeenCalledWith({ variable_id: "o1" });
  });

  it("says so when the page can't be loaded", async () => {
    const api = fakeApi();
    api.listOrg.mockRejectedValue(new Error("down"));
    const { result } = setup(api, null);
    await waitFor(() => expect(result.current.loadFailed).toBe(true));
    expect(toast.error).toHaveBeenCalledWith("Couldn't load the shared variables", { description: "down" });
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/shared-variables.test.ts src/hooks/use-shared-variables-settings.test.tsx`
Expected: FAIL — `variableRows`, `failedServers` and the hook do not exist.

- [ ] **Step 3: Implement**

Append to `web/src/lib/shared-variables.ts`:

```ts
// VariableRow is one row of Settings → Shared variables: an org variable
// (every server) or the active server's own, and what the list says about
// it.
export type VariableRow = {
  variable: SharedVariable;
  // "All servers" for the org's, "{server} only" for the active server's.
  scopeLabel: string;
  // An org variable the active server replaces with a value of its own.
  overriddenHere: boolean;
};

// variableRows merges the org's variables and the active server's own into
// one list sorted by name, the org's row first when both define a name.
export function variableRows(org: SharedVariable[], server: SharedVariable[], serverName: string): VariableRow[] {
  const own = new Set(server.map((v) => v.name));
  const rows: VariableRow[] = [
    ...org.map((v) => ({ variable: v, scopeLabel: "All servers", overriddenHere: own.has(v.name) })),
    ...server.map((v) => ({ variable: v, scopeLabel: `${serverName} only`, overriddenHere: false })),
  ];
  return rows.sort((a, b) => {
    if (a.variable.name !== b.variable.name) return a.variable.name < b.variable.name ? -1 : 1;
    return a.variable.scope === b.variable.scope ? 0 : a.variable.scope === "org" ? -1 : 1;
  });
}

// failedServers lists the servers whose last sync failed, sorted by name
// (an unknown server by its id).
export function failedServers(
  sync: Record<string, SyncState>,
  names: Map<string, string>,
): { id: string; name: string }[] {
  return Object.entries(sync)
    .filter(([, state]) => state === "failed")
    .map(([id]) => ({ id, name: names.get(id) ?? id }))
    .sort((a, b) => a.name.localeCompare(b.name));
}
```

Create `web/src/hooks/use-shared-variables-settings.ts`:

```ts
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";

import type { DialogMode } from "@/components/shared-variable-dialog";
import { useNotifications } from "@/context/use-notifications";
import {
  applySyncEvent,
  serverNameMap,
  type EncryptionKeys,
  type OrgVariables,
  type SaveVariable,
  type ServerVariables,
  type SharedVariable,
  type SharedVariablesApi,
  type SyncState,
  type VariableScope,
  type VarsReloaded,
} from "@/lib/shared-variables";

const NO_KEYS: EncryptionKeys = { servers: [], pending_servers: [] };

const errorText = (e: unknown) => (e instanceof Error ? e.message : undefined);

type Page = { org: OrgVariables; server: ServerVariables | null; keys: EncryptionKeys };

// useSharedVariablesSettings holds Settings → Shared variables: the org's
// variables and the active server's own (serverId null: there is none),
// the encryption keys, every server's sync state kept live from
// vars.synced / vars.sync_failed, failed-reload toasts from vars.reloaded,
// the add/edit/provide dialog and delete state, and the actions. Each
// action calls the route the separate org and server pages called before
// they merged (see the plan's route mapping).
export function useSharedVariablesSettings(api: SharedVariablesApi, serverId: string | null) {
  const { subscribe } = useNotifications();
  // Everything scope-bound is stored with the server it belongs to and only
  // exposed while that is still the active one, so switching servers never
  // shows (or acts on) the previous server's rows.
  const scopeKey = serverId ?? "";
  const [loaded, setLoaded] = useState<(Page & { key: string }) | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const [sync, setSync] = useState<Record<string, SyncState>>({});
  const [dialogState, setDialogState] = useState<{ key: string; mode: DialogMode } | null>(null);
  const [dialogKey, setDialogKey] = useState(0);
  const [deleteState, setDeleteState] = useState<{ key: string; variable: SharedVariable } | null>(null);
  const current = loaded?.key === scopeKey ? loaded : null;
  const dialog = dialogState?.key === scopeKey ? dialogState.mode : null;
  const toDelete = deleteState?.key === scopeKey ? deleteState.variable : null;
  const keys = current?.keys ?? NO_KEYS;

  // Only the latest request's answer may land.
  const token = useRef(0);

  const fetchPage = useCallback(async (): Promise<Page> => {
    const [org, server, keys] = await Promise.all([
      api.listOrg(),
      serverId ? api.listServer(serverId) : Promise.resolve(null),
      api.keys(),
    ]);
    return { org, server, keys };
  }, [api, serverId]);

  const apply = useCallback(
    (page: Page) => {
      setLoaded({ key: scopeKey, ...page });
      setLoadFailed(false);
      const states: Record<string, SyncState> = Object.fromEntries(
        page.org.servers.map((s) => [s.server_id, s.state]),
      );
      if (page.server && serverId) states[serverId] = page.server.sync.state;
      setSync(states);
    },
    [scopeKey, serverId],
  );

  const failed = useCallback((e: unknown) => {
    setLoadFailed(true);
    toast.error("Couldn't load the shared variables", { description: errorText(e) });
  }, []);

  const load = useCallback(async () => {
    const mine = ++token.current;
    try {
      const page = await fetchPage();
      if (mine === token.current) apply(page);
    } catch (e) {
      if (mine === token.current) failed(e);
    }
  }, [fetchPage, apply, failed]);

  useEffect(() => {
    let mounted = true;
    const mine = ++token.current;
    fetchPage().then(
      (page) => {
        if (mounted && mine === token.current) apply(page);
      },
      (e: unknown) => {
        if (mounted && mine === token.current) failed(e);
      },
    );
    return () => {
      mounted = false;
    };
  }, [fetchPage, apply, failed]);

  useEffect(
    () =>
      subscribe((n) => {
        setSync((s) => applySyncEvent(s, n));
        if (n.type !== "vars.reloaded") return;
        const r = n.payload as VarsReloaded | undefined;
        if (!r || r.ok) return;
        // One toast per burst (a fixed id replaces the previous one).
        toast.error("Couldn't reload an app", { id: "vars-reload-failed", description: r.error });
      }),
    [subscribe],
  );

  // Names of every server: keyed and pending ones, and any the org's sync
  // list knows (a server whose sync failed may have no key).
  const serverNames = useMemo(() => {
    const names = serverNameMap(keys);
    for (const s of current?.org.servers ?? []) if (!names.has(s.server_id)) names.set(s.server_id, s.name);
    return names;
  }, [keys, current]);

  const openDialog = (mode: DialogMode) => {
    setDialogKey((k) => k + 1);
    setDialogState({ key: scopeKey, mode });
  };
  const closeDialog = () => setDialogState(null);
  const setToDelete = (variable: SharedVariable | null) =>
    setDeleteState(variable ? { key: scopeKey, variable } : null);

  // save stores a variable in its scope: the active server's own, or the
  // org's (every server). The dialog reports a failure.
  const save = async (body: SaveVariable, scope: VariableScope) => {
    if (scope === "server" && serverId) await api.saveServer(serverId, body);
    else await api.saveOrg(body);
    toast.success("Variable saved");
    await load();
  };

  // remove deletes a variable from its scope; a failure is reported and
  // rethrown (the confirmation stays open to retry).
  const remove = async (v: SharedVariable) => {
    try {
      if (v.scope === "server" && serverId) await api.deleteServer(serverId, v.id);
      else await api.deleteOrg(v.id);
      toast.success("Variable deleted");
      await load();
    } catch (e) {
      toast.error("Couldn't delete the variable", { description: errorText(e) });
      throw e;
    }
  };

  const act = (done: string, fn: () => Promise<unknown>, onError?: () => void) => {
    fn().then(
      () => toast.success(done),
      (e: unknown) => {
        onError?.();
        toast.error("Request failed", { description: errorText(e) });
      },
    );
  };

  // sendAgain pushes the variables again to servers whose sync failed: one
  // server by id, several with an empty target (every server). They read
  // pending until their agents answer, and go back on an error.
  const sendAgain = (ids: string[]) => {
    const before = sync;
    const mark = (pick: (id: string) => SyncState) =>
      setSync((s) => ({ ...s, ...Object.fromEntries(ids.map((id) => [id, pick(id)])) }));
    act(
      "Sent again",
      () => {
        mark(() => "pending");
        return api.resync(ids.length === 1 ? { server_id: ids[0] } : {});
      },
      () => mark((id) => before[id] ?? "failed"),
    );
  };

  // resendVariable pushes one org variable to every server again.
  const resendVariable = (v: SharedVariable) => act("Sent again", () => api.resync({ variable_id: v.id }));

  // reloadStale reloads the active server's running apps that use old values.
  const reloadStale = () => {
    if (serverId) act("Reload queued", () => api.reload({ server_id: serverId }));
  };

  // reloadVariable reloads the apps using a variable: a server variable's
  // on its server, an org variable's everywhere.
  const reloadVariable = (v: SharedVariable) =>
    act("Reload queued", () =>
      v.scope === "server" && serverId
        ? api.reload({ server_id: serverId, names: [v.name] })
        : api.reload({ variable_id: v.id }),
    );

  return {
    org: current?.org ?? null,
    server: current?.server ?? null,
    keys,
    serverNames,
    sync,
    loadFailed,
    dialog,
    dialogKey,
    openDialog,
    closeDialog,
    toDelete,
    setToDelete,
    save,
    remove,
    sendAgain,
    resendVariable,
    reloadStale,
    reloadVariable,
    load,
  };
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/shared-variables.test.ts src/hooks/use-shared-variables-settings.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/shared-variables.ts web/src/lib/shared-variables.test.ts web/src/hooks/use-shared-variables-settings.ts web/src/hooks/use-shared-variables-settings.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): one list model and hook for org and server shared variables

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 9: Settings → Shared variables — one page and one dialog for both scopes

**Files:**
- Rewrite: `web/src/components/shared-variable-dialog.tsx`, `web/src/components/shared-variable-dialog.test.tsx`
- Rewrite: `web/src/pages/settings-shared-variables.tsx`, `web/src/pages/settings-shared-variables.test.tsx`
- Delete: `web/src/pages/org-variables.tsx`, `web/src/pages/org-variables.test.tsx`, `web/src/pages/settings-variables.tsx`, `web/src/pages/settings-variables.test.tsx`, `web/src/components/variables-table.tsx`, `web/src/components/variables-table.test.tsx`, `web/src/components/delete-variable-dialog.tsx`, `web/src/hooks/use-shared-variables-page.ts`

The dialog's new `onSave(body, scope)` and the merged page land together: the two old pages cannot use the new dialog without either losing the server-only encryption target or growing a throwaway prop, so they go in the same commit.

**Interfaces:**
- Consumes: Task 1 (`ResponsiveDialog`, `RowMenu`, `ListPlaceholder`, `ConfirmDialog`); Task 8 (`useSharedVariablesSettings`, `variableRows`, `failedServers`, `VariableRow`); phase 1 `Disclosure`, `PageHeader`, `RowList`, `Row`, `EmptyState`; `ReloadRolloutStrip` (`@/components/rollout-strip`), `reloadableStaleCount` (`@/lib/app-status`), `coverageIncomplete`, `coverageLabel`, `encryptForServers`, `pushSummary`, `serverNameMap`, `variableNameError`, `createSharedVariablesApi`, `RolloutsApi`; `useServers()` (`activeServer`), `useApps()` (`staleVarsByApp`, `statusByApp`); `isStandalone` (`@/config`).
- Produces:
  - `DialogMode` (unchanged: `{ kind: "create" } | { kind: "edit"; variable } | { kind: "provide"; variable }`); `SharedVariableDialog({ open, mode, keys, server?: { id: string; name: string } | null, chooseScope?: boolean, onOpenChange, onSave: (body: SaveVariable, scope: VariableScope) => Promise<void> })`. A new variable is org-wide ("All servers") unless `chooseScope` and the user picks "{server} only" under Advanced; an edit or a provide keeps the variable's own scope. Encryption targets (unchanged rules): an org secret → every keyed server; a server-only secret → that server's key only; a provide → only the keyed servers in the variable's `missing_on`.
  - default `SharedVariablesSettingsPage({ api?: SharedVariablesApi; rollouts?: RolloutsApi })` — one list of the org's and the active server's variables; banners only when relevant ("Sync … failed" + Send again, "N apps on {server} use old values" + Reload, "N secrets aren't on {server} yet" + Provide value); built-ins folded; the reload rollout strip.

- [ ] **Step 1: Write the failing tests**

Replace `web/src/components/shared-variable-dialog.test.tsx` with:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

vi.mock("@/lib/ecies", () => ({
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import { SharedVariableDialog, type DialogMode } from "@/components/shared-variable-dialog";
import type { EncryptionKeys, SharedVariable } from "@/lib/shared-variables";

const KEYS: EncryptionKeys = {
  servers: [
    { server_id: "a", name: "alpha", public_key: "KA" },
    { server_id: "b", name: "beta", public_key: "KB" },
  ],
  pending_servers: [{ server_id: "p", name: "new-box" }],
};
const SECRET: SharedVariable = {
  id: "v2", name: "SMTP_PASS", scope: "org", is_secret: true, value: "<encrypted>",
  description: "old", updated_at: "2026-10-11T00:00:00Z", updated_by: "u1", missing_on: ["b"],
};
const BETA = { id: "b", name: "beta" };

function renderDialog(mode: DialogMode, opts: { chooseScope?: boolean; keys?: EncryptionKeys } = {}) {
  const onSave = vi.fn().mockResolvedValue(undefined);
  const onOpenChange = vi.fn();
  render(
    <SharedVariableDialog
      open
      mode={mode}
      keys={opts.keys ?? KEYS}
      server={BETA}
      chooseScope={opts.chooseScope ?? true}
      onOpenChange={onOpenChange}
      onSave={onSave}
    />,
  );
  return { onSave, onOpenChange };
}

const setupUser = () => userEvent.setup({ delay: null });

describe("SharedVariableDialog", () => {
  it("adds a plain org-wide variable, reload off by default", async () => {
    const user = setupUser();
    const { onSave, onOpenChange } = renderDialog({ kind: "create" });
    expect(screen.getByRole("checkbox", { name: "Reload apps that use it now" })).not.toBeChecked();
    await user.type(screen.getByLabelText("Name"), "SMTP_HOST");
    await user.type(screen.getByLabelText("Value"), "mail.example");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({ name: "SMTP_HOST", is_secret: false, value: "mail.example", description: "" }, "org");
    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
  });

  it("keeps the description and the scope under Advanced", async () => {
    const user = setupUser();
    const { onSave } = renderDialog({ kind: "create" });
    expect(screen.queryByLabelText("Description")).not.toBeInTheDocument();
    expect(screen.queryByRole("radiogroup", { name: "Applies to" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Advanced" }));
    expect(screen.getByRole("radio", { name: "All servers" })).toBeChecked();
    await user.type(screen.getByLabelText("Name"), "LOCAL_DIR");
    await user.type(screen.getByLabelText("Value"), "/srv");
    await user.type(screen.getByLabelText("Description"), "Data folder");
    await user.click(screen.getByRole("radio", { name: "beta only" }));
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith(
      { name: "LOCAL_DIR", is_secret: false, value: "/srv", description: "Data folder" },
      "server",
    );
  });

  it("offers no scope choice when there is none to make (standalone)", async () => {
    const user = setupUser();
    renderDialog({ kind: "create" }, { chooseScope: false });
    await user.click(screen.getByRole("button", { name: "Advanced" }));
    expect(screen.queryByRole("radiogroup", { name: "Applies to" })).not.toBeInTheDocument();
  });

  it("encrypts a new org secret for every keyed server and says where it goes", async () => {
    const user = setupUser();
    const { onSave } = renderDialog({ kind: "create" });
    await user.type(screen.getByLabelText("Name"), "SMTP_PASS");
    await user.type(screen.getByLabelText("Value"), "s3cret");
    await user.click(screen.getByRole("switch", { name: "Secret" }));
    expect(screen.getByText("Will be pushed to 2 servers (1 pending, no key yet)")).toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: "Reload apps that use it now" }));
    await user.click(screen.getByRole("button", { name: "Save and push" }));
    expect(onSave).toHaveBeenCalledWith(
      {
        name: "SMTP_PASS", is_secret: true, description: "", reload: true,
        ciphertexts: { a: "ct(KA):s3cret", b: "ct(KB):s3cret" },
      },
      "org",
    );
  });

  it("encrypts a server-only secret for that server only", async () => {
    const user = setupUser();
    const { onSave } = renderDialog({ kind: "create" });
    await user.type(screen.getByLabelText("Name"), "LOCAL_KEY");
    await user.type(screen.getByLabelText("Value"), "k");
    await user.click(screen.getByRole("switch", { name: "Secret" }));
    await user.click(screen.getByRole("button", { name: "Advanced" }));
    await user.click(screen.getByRole("radio", { name: "beta only" }));
    expect(screen.getByText("Will be pushed to 1 server")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith(
      { name: "LOCAL_KEY", is_secret: true, description: "", ciphertexts: { b: "ct(KB):k" } },
      "server",
    );
  });

  it("locks an existing secret: description edits keep the value", async () => {
    const user = setupUser();
    const { onSave } = renderDialog({ kind: "edit", variable: SECRET });
    expect(screen.getByLabelText("Name")).toBeDisabled();
    expect(screen.getByRole("switch", { name: "Secret" })).toBeDisabled();
    expect(screen.queryByLabelText("Value")).not.toBeInTheDocument();
    // The description was set: Advanced starts open; the scope is said, not offered.
    expect(screen.getByLabelText("Description")).toHaveValue("old");
    expect(screen.getByText("Applies to all servers.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({ name: "SMTP_PASS", is_secret: true, description: "old" }, "org");
  });

  it("replaces an existing secret's value everywhere", async () => {
    const user = setupUser();
    const { onSave } = renderDialog({ kind: "edit", variable: SECRET });
    await user.click(screen.getByRole("button", { name: "Replace value" }));
    await user.type(screen.getByLabelText("Value"), "n3w");
    await user.click(screen.getByRole("button", { name: "Save and push" }));
    expect(onSave).toHaveBeenCalledWith(
      { name: "SMTP_PASS", is_secret: true, description: "old", ciphertexts: { a: "ct(KA):n3w", b: "ct(KB):n3w" } },
      "org",
    );
  });

  it("edits a server-only secret for its server only", async () => {
    const user = setupUser();
    const { onSave } = renderDialog({ kind: "edit", variable: { ...SECRET, scope: "server", missing_on: [] } });
    expect(screen.getByText("Applies to beta only.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Replace value" }));
    await user.type(screen.getByLabelText("Value"), "n3w");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith(
      { name: "SMTP_PASS", is_secret: true, description: "old", ciphertexts: { b: "ct(KB):n3w" } },
      "server",
    );
  });

  it("provides a missing value for the missing servers only", async () => {
    const user = setupUser();
    const { onSave } = renderDialog({ kind: "provide", variable: SECRET });
    expect(screen.getByText("Encrypts the value for: beta")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Advanced" })).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("Value"), "s3cret");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith(
      { name: "SMTP_PASS", is_secret: true, description: "old", ciphertexts: { b: "ct(KB):s3cret" } },
      "org",
    );
  });

  it("names keyless servers as pending in a provide", () => {
    renderDialog({ kind: "provide", variable: { ...SECRET, missing_on: ["b", "p"] } });
    expect(screen.getByText("Encrypts the value for: beta. Pending (no key yet): new-box")).toBeInTheDocument();
  });

  it("disables Save when no target server has a key", async () => {
    const user = setupUser();
    renderDialog({ kind: "provide", variable: { ...SECRET, missing_on: ["p"] } });
    await user.type(screen.getByLabelText("Value"), "x");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("disables Save for an org secret when no server is keyed", async () => {
    const user = setupUser();
    renderDialog({ kind: "create" }, { keys: { servers: [], pending_servers: KEYS.pending_servers } });
    await user.type(screen.getByLabelText("Name"), "A");
    await user.type(screen.getByLabelText("Value"), "x");
    await user.click(screen.getByRole("switch", { name: "Secret" }));
    expect(screen.getByRole("button", { name: "Save and push" })).toBeDisabled();
  });

  it("rejects a reserved name before saving", async () => {
    const user = setupUser();
    renderDialog({ kind: "create" });
    await user.type(screen.getByLabelText("Name"), "WF_X");
    expect(screen.getByText("WF_X is reserved")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });
});
```

Replace `web/src/pages/settings-shared-variables.test.tsx` with:

```tsx
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
vi.mock("@/lib/ecies", () => ({
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));
const active = vi.hoisted(() => ({ server: null as { id: string; name: string } | null }));
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServer: active.server }) }));

import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";
import {
  NotificationsContext,
  type Notification,
  type NotificationHandler,
  type NotificationsContextValue,
} from "@/context/notifications-context-base";
import type { Rollout } from "@/lib/rollouts";
import type { EncryptionKeys, OrgVariables, ServerVariables, SharedVariablesApi } from "@/lib/shared-variables";
import SharedVariablesSettingsPage from "@/pages/settings-shared-variables";
import { rollout } from "@/test/rollouts";

const T = "2026-10-11T00:00:00Z";
const ORG: OrgVariables = {
  variables: [
    { id: "o1", name: "SMTP_HOST", scope: "org", is_secret: false, value: "mail", description: "Mail relay", updated_at: T, updated_by: "u", missing_on: [] },
    { id: "o2", name: "SMTP_PASS", scope: "org", is_secret: true, value: "<encrypted>", description: "", updated_at: T, updated_by: "u", missing_on: ["srv-a"] },
  ],
  servers: [
    { server_id: "srv-a", name: "alpha", has_key: true, revision: 3, agent_revision: 2, state: "pending" },
    { server_id: "srv-b", name: "beta", has_key: true, revision: 3, agent_revision: 3, state: "synced" },
  ],
};
const SERVER: ServerVariables = {
  variables: [{ id: "s1", name: "SMTP_HOST", scope: "server", is_secret: false, value: "local", description: "", updated_at: T, updated_by: "u", missing_on: [] }],
  inherited: [
    { ...ORG.variables[0], overridden: true },
    { ...ORG.variables[1], overridden: false },
  ],
  builtins: [{ name: "WF_SERVER_NAME", value: "alpha", description: "This server's name" }],
  missing_org_secrets: ["SMTP_PASS"],
  sync: { server_id: "srv-a", name: "alpha", has_key: true, revision: 3, agent_revision: 2, state: "pending" },
};
const KEYS: EncryptionKeys = {
  servers: [
    { server_id: "srv-a", name: "alpha", public_key: "KA" },
    { server_id: "srv-b", name: "beta", public_key: "KB" },
  ],
  pending_servers: [],
};

function fakeApi() {
  return {
    listOrg: vi.fn().mockResolvedValue(ORG),
    listServer: vi.fn().mockResolvedValue(SERVER),
    keys: vi.fn().mockResolvedValue(KEYS),
    saveOrg: vi.fn().mockResolvedValue({}),
    saveServer: vi.fn().mockResolvedValue({}),
    deleteOrg: vi.fn().mockResolvedValue(undefined),
    deleteServer: vi.fn().mockResolvedValue(undefined),
    resync: vi.fn().mockResolvedValue([]),
    reload: vi.fn().mockResolvedValue([]),
  };
}

// a4 is stale but stopped: the agent skips it, so it is not counted.
const STALE = { a1: ["SMTP_PASS"], a2: ["SMTP_HOST"], a3: [], a4: ["SMTP_HOST"] };

function renderPage(
  api: ReturnType<typeof fakeApi>,
  opts: { standalone?: boolean; stale?: Record<string, string[]>; running?: Rollout[] } = {},
) {
  const rollouts = { list: vi.fn().mockResolvedValue(opts.running ?? []), stop: vi.fn(), upgradeAll: vi.fn() };
  // Every subscriber hears each event (the page and its rollout strip).
  const handlers = new Set<NotificationHandler>();
  const notifications: NotificationsContextValue = {
    connected: true,
    subscribe: (h) => {
      handlers.add(h);
      return () => {
        handlers.delete(h);
      };
    },
    waitFor: vi.fn(),
  };
  const emit = (n: Notification) => act(() => handlers.forEach((h) => h(n)));
  const apps = {
    staleVarsByApp: opts.stale ?? STALE,
    statusByApp: { a1: 1, a2: 2, a3: 1, a4: 5 },
  } as unknown as AppsContextValue;
  render(
    <MemoryRouter>
      <NotificationsContext.Provider value={notifications}>
        <AppsContext.Provider value={apps}>
          <SharedVariablesSettingsPage
            api={api as unknown as SharedVariablesApi}
            rollouts={rollouts}
            standalone={opts.standalone ?? false}
          />
        </AppsContext.Provider>
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
  return { emit };
}

const setupUser = () => userEvent.setup({ delay: null });
const listRows = async () =>
  within(await screen.findByRole("list", { name: "Shared variables" })).getAllByRole("listitem");

describe("SharedVariablesSettingsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    active.server = { id: "srv-a", name: "alpha" };
  });

  it("lists the organization's and this server's variables in one list, with their scope", async () => {
    renderPage(fakeApi());
    expect(screen.getByRole("heading", { level: 1, name: "Shared variables" })).toBeInTheDocument();
    const rows = await listRows();
    expect(rows.map((r) => within(r).getByText(/^SMTP_/).textContent)).toEqual(["SMTP_HOST", "SMTP_HOST", "SMTP_PASS"]);
    expect(rows[0]).toHaveTextContent("All servers");
    expect(rows[0]).toHaveTextContent("mail");
    expect(rows[0]).toHaveTextContent("Mail relay");
    expect(rows[0]).toHaveTextContent("alpha uses its own value");
    expect(rows[1]).toHaveTextContent("alpha only");
    expect(rows[1]).toHaveTextContent("local");
    expect(rows[2]).toHaveTextContent("Secret");
    expect(rows[2]).toHaveTextContent("Missing on alpha");
  });

  it("never prints a secret's value", async () => {
    renderPage(fakeApi());
    await listRows();
    expect(screen.queryByText(/<encrypted>/)).not.toBeInTheDocument();
  });

  it("folds the built-in variables away, read-only", async () => {
    const user = setupUser();
    renderPage(fakeApi());
    const toggle = await screen.findByRole("button", { name: "Built-in variables (1)" });
    expect(screen.queryByText("WF_SERVER_NAME")).not.toBeInTheDocument();
    await user.click(toggle);
    const builtins = screen.getByRole("list", { name: "Built-in variables" });
    expect(within(builtins).getByText("WF_SERVER_NAME")).toBeInTheDocument();
    expect(within(builtins).queryByRole("button")).not.toBeInTheDocument();
  });

  it("says a sync failed only when it did, and sends again", async () => {
    const user = setupUser();
    const api = fakeApi();
    const { emit } = renderPage(api);
    await listRows();
    expect(screen.queryByRole("button", { name: "Send again" })).not.toBeInTheDocument();
    emit({ type: "vars.sync_failed", ref: "", payload: { server_id: "srv-b", error: "boom" } });
    const banner = screen.getByRole("alert", { name: "Sync" });
    expect(banner).toHaveTextContent("Sync to beta failed.");
    await user.click(within(banner).getByRole("button", { name: "Send again" }));
    expect(api.resync).toHaveBeenCalledWith({ server_id: "srv-b" });
    expect(screen.queryByRole("alert", { name: "Sync" })).not.toBeInTheDocument();
  });

  it("offers Reload while apps use old values, and only then", async () => {
    const user = setupUser();
    const api = fakeApi();
    renderPage(api);
    const banner = await screen.findByRole("status", { name: "Old values" });
    expect(banner).toHaveTextContent("2 apps on alpha use old values.");
    await user.click(within(banner).getByRole("button", { name: "Reload" }));
    expect(api.reload).toHaveBeenCalledWith({ server_id: "srv-a" });
  });

  it("has no reload banner when nothing is stale", async () => {
    renderPage(fakeApi(), { stale: {} });
    await listRows();
    expect(screen.queryByRole("status", { name: "Old values" })).not.toBeInTheDocument();
  });

  it("asks for an org secret this server lacks and encrypts it for this server only", async () => {
    const user = setupUser();
    const api = fakeApi();
    renderPage(api);
    const banner = await screen.findByRole("status", { name: "Missing secrets" });
    expect(banner).toHaveTextContent("1 secret isn't on alpha yet.");
    await user.click(within(banner).getByRole("button", { name: "Provide value" }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("Encrypts the value for: alpha");
    await user.type(within(dialog).getByLabelText("Value"), "s3cret");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(api.saveOrg).toHaveBeenCalledWith({
        name: "SMTP_PASS", is_secret: true, description: "", ciphertexts: { "srv-a": "ct(KA):s3cret" },
      }),
    );
    await waitFor(() => expect(api.listServer).toHaveBeenCalledTimes(2)); // listed again after the save
  });

  it("adds an org-wide variable by default and a server-only one under Advanced", async () => {
    const user = setupUser();
    const api = fakeApi();
    renderPage(api);
    await listRows();
    await user.click(screen.getByRole("button", { name: /Add variable/ }));
    let dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), "TZ");
    await user.type(within(dialog).getByLabelText("Value"), "UTC");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(api.saveOrg).toHaveBeenCalledWith({ name: "TZ", is_secret: false, value: "UTC", description: "" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    await user.click(screen.getByRole("button", { name: /Add variable/ }));
    dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), "LOCAL_DIR");
    await user.type(within(dialog).getByLabelText("Value"), "/srv");
    await user.click(within(dialog).getByRole("button", { name: "Advanced" }));
    await user.click(within(dialog).getByRole("radio", { name: "alpha only" }));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(api.saveServer).toHaveBeenCalledWith("srv-a", { name: "LOCAL_DIR", is_secret: false, value: "/srv", description: "" }),
    );
  });

  it("offers no scope in a standalone install, and edits a server-only variable in its own scope", async () => {
    const user = setupUser();
    const api = fakeApi();
    renderPage(api, { standalone: true });
    await listRows();
    await user.click(screen.getByRole("button", { name: /Add variable/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Advanced" }));
    expect(within(dialog).queryByRole("radiogroup", { name: "Applies to" })).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    const rows = await listRows();
    await user.click(within(rows[1]).getByRole("button", { name: "Actions for SMTP_HOST" }));
    await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
    const edit = await screen.findByRole("dialog");
    await user.clear(within(edit).getByLabelText("Value"));
    await user.type(within(edit).getByLabelText("Value"), "other");
    await user.click(within(edit).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(api.saveServer).toHaveBeenCalledWith("srv-a", { name: "SMTP_HOST", is_secret: false, value: "other", description: "" }),
    );
  });

  it("deletes in the variable's own scope after a confirmation", async () => {
    const user = setupUser();
    const api = fakeApi();
    renderPage(api);
    const rows = await listRows();
    await user.click(within(rows[1]).getByRole("button", { name: "Actions for SMTP_HOST" }));
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(confirm).toHaveTextContent("fall back to the organization's value");
    await user.click(within(confirm).getByRole("button", { name: "Delete variable" }));
    await waitFor(() => expect(api.deleteServer).toHaveBeenCalledWith("srv-a", "s1"));
    expect(api.deleteOrg).not.toHaveBeenCalled();
  });

  it("reloads and sends one variable again from its menu", async () => {
    const user = setupUser();
    const api = fakeApi();
    renderPage(api);
    const rows = await listRows();
    const menu = async (row: HTMLElement, item: string) => {
      await user.click(within(row).getByRole("button", { name: "Actions for SMTP_HOST" }));
      await user.click(await screen.findByRole("menuitem", { name: item }));
    };
    await menu(rows[0], "Reload apps using it");
    expect(api.reload).toHaveBeenCalledWith({ variable_id: "o1" });
    await menu(rows[0], "Send to servers again");
    expect(api.resync).toHaveBeenCalledWith({ variable_id: "o1" });
    await menu(rows[1], "Reload apps using it");
    expect(api.reload).toHaveBeenCalledWith({ server_id: "srv-a", names: ["SMTP_HOST"] });
    await user.click(within(rows[1]).getByRole("button", { name: "Actions for SMTP_HOST" }));
    expect(screen.queryByRole("menuitem", { name: "Send to servers again" })).not.toBeInTheDocument();
  });

  it("shows the org's reload rollout", async () => {
    renderPage(fakeApi(), {
      running: [rollout({ kind: "vars_reload", done: 0, skipped: 0, total: 2, current_app_name: "shop", current_server_id: "srv-b" })],
    });
    expect(await screen.findByText("Reloading 1 of 2: shop on beta…")).toBeInTheDocument();
  });

  it("shows only the organization's without a server", async () => {
    active.server = null;
    const api = fakeApi();
    renderPage(api);
    const rows = await listRows();
    expect(rows).toHaveLength(2);
    expect(api.listServer).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: /Built-in variables/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("status", { name: "Old values" })).not.toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/components/shared-variable-dialog.test.tsx src/pages/settings-shared-variables.test.tsx`
Expected: FAIL — the dialog has no "Advanced" or "Applies to" and `onSave` gets one argument; the page still stacks phase 1's two old pages.

- [ ] **Step 3: Implement**

Replace `web/src/components/shared-variable-dialog.tsx` with:

```tsx
import { useEffect, useState } from "react";
import { toast } from "sonner";

import { Disclosure } from "@/components/disclosure";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import {
  encryptForServers,
  pushSummary,
  serverNameMap,
  variableNameError,
  type EncryptionKeys,
  type SaveVariable,
  type SharedVariable,
  type VariableScope,
} from "@/lib/shared-variables";

export type DialogMode =
  | { kind: "create" }
  | { kind: "edit"; variable: SharedVariable }
  | { kind: "provide"; variable: SharedVariable };

const FORM_ID = "shared-variable-form";

// SharedVariableDialog adds or edits a shared variable, or provides a
// missing secret for the servers that lack it (gap fill). A new variable
// applies to all servers unless chooseScope offers "{server} only" (under
// Advanced) and the user picks it; an edit or a provide keeps the
// variable's scope. A server-only secret is encrypted for that server's key
// only, an org one for every keyed server, a provide for the keyed servers
// missing it. An existing secret's name, kind and value are locked:
// "Replace value" sends a new value; anything else is delete and recreate.
// Secret values are encrypted here and cleared on close. The parent
// remounts it (key) per opening; onSave reports nothing itself (it throws
// to keep the dialog open).
export function SharedVariableDialog({
  open,
  mode,
  keys,
  server = null,
  chooseScope = false,
  onOpenChange,
  onSave,
}: {
  open: boolean;
  mode: DialogMode;
  keys: EncryptionKeys;
  // server is the active server: a "{name} only" variable belongs to it.
  server?: { id: string; name: string } | null;
  // chooseScope offers "Applies to" on a new variable.
  chooseScope?: boolean;
  onOpenChange: (open: boolean) => void;
  onSave: (body: SaveVariable, scope: VariableScope) => Promise<void>;
}) {
  const existing = mode.kind === "create" ? null : mode.variable;
  const provide = mode.kind === "provide";
  const [scope, setScope] = useState<VariableScope>(existing?.scope ?? "org");
  const [name, setName] = useState(existing?.name ?? "");
  const [secret, setSecret] = useState(existing?.is_secret ?? false);
  const [value, setValue] = useState(existing && !existing.is_secret ? existing.value : "");
  const [description, setDescription] = useState(existing?.description ?? "");
  const [replace, setReplace] = useState(false);
  const [reload, setReload] = useState(false);
  const [busy, setBusy] = useState(false);

  const serverId = scope === "server" ? server?.id : undefined;
  const names = serverNameMap(keys);
  const scopeKeys = serverId ? keys.servers.filter((k) => k.server_id === serverId) : keys.servers;
  const pending = serverId ? keys.pending_servers.filter((p) => p.server_id === serverId) : keys.pending_servers;
  const typingSecret = secret && (mode.kind === "create" || provide || replace);
  const lockedSecret = mode.kind === "edit" && secret && !replace;
  const trimmed = name.trim();
  const nameError = mode.kind === "create" && trimmed ? variableNameError(trimmed) : "";
  // Who actually receives the new value: keyed servers only.
  const targets = provide ? keys.servers.filter((k) => existing?.missing_on.includes(k.server_id)) : scopeKeys;
  const keyless = provide ? (existing?.missing_on ?? []).filter((id) => !targets.some((k) => k.server_id === id)) : [];
  const noKey = typingSecret && targets.length === 0;
  const canSave = !busy && trimmed !== "" && !nameError && !noKey && (!typingSecret || value !== "");
  const pushesEverywhere = typingSecret && !provide && serverId === undefined;

  // The typed value must not outlive the dialog.
  useEffect(() => {
    if (!open) setValue("");
  }, [open]);

  const changeOpen = (next: boolean) => {
    if (!next) setValue("");
    onOpenChange(next);
  };

  const save = async () => {
    setBusy(true);
    try {
      const common = { name: trimmed, description, ...(reload ? { reload: true } : {}) };
      let body: SaveVariable;
      if (!secret) {
        body = { ...common, is_secret: false, value };
      } else if (typingSecret) {
        body = { ...common, is_secret: true, ciphertexts: await encryptForServers(value, targets) };
      } else {
        body = { ...common, is_secret: true };
      }
      await onSave(body, scope);
      changeOpen(false);
    } catch (e) {
      toast.error("Couldn't save the variable", { description: e instanceof Error ? e.message : undefined });
    } finally {
      setBusy(false);
    }
  };

  const title = provide ? `Provide ${existing?.name}` : existing ? `Edit ${existing.name}` : "Add variable";
  const label = (id: string) => names.get(id) ?? id;
  const targetNames = targets.map((k) => k.name).join(", ");
  const keylessNames = keyless.map(label).join(", ");
  const scopeText = scope === "server" && server ? `${server.name} only` : "all servers";

  return (
    <ResponsiveDialog
      open={open}
      onOpenChange={changeOpen}
      title={title}
      description={
        provide
          ? `Encrypts the value for: ${targetNames || "no server"}${keylessNames ? `. Pending (no key yet): ${keylessNames}` : ""}`
          : "Apps use it as ${NAME}. A secret is encrypted in your browser, once per server."
      }
      footer={
        <>
          <Button type="button" variant="outline" onClick={() => changeOpen(false)}>
            Cancel
          </Button>
          <Button type="submit" form={FORM_ID} disabled={!canSave}>
            {pushesEverywhere ? "Save and push" : "Save"}
          </Button>
        </>
      }
    >
      <form
        id={FORM_ID}
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          if (canSave) void save();
        }}
      >
        <div className="space-y-2">
          <Label htmlFor="sv-name">Name</Label>
          <Input
            id="sv-name"
            value={name}
            disabled={existing !== null}
            placeholder="SMTP_PASS"
            className="font-mono"
            autoComplete="off"
            spellCheck={false}
            aria-invalid={nameError ? true : undefined}
            aria-describedby={nameError ? "sv-name-error" : undefined}
            onChange={(e) => setName(e.target.value)}
          />
          {nameError ? (
            <p id="sv-name-error" className="text-sm text-destructive">
              {nameError}
            </p>
          ) : null}
        </div>
        {lockedSecret ? (
          <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border p-3 text-sm">
            <span className="text-muted-foreground">The value is encrypted and can't be shown.</span>
            <Button type="button" size="sm" variant="outline" onClick={() => setReplace(true)}>
              Replace value
            </Button>
          </div>
        ) : (
          <div className="space-y-2">
            <Label htmlFor="sv-value">Value</Label>
            <Textarea
              id="sv-value"
              rows={3}
              value={value}
              className="font-mono"
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => setValue(e.target.value)}
            />
            {typingSecret && !provide ? (
              <p className="text-sm text-muted-foreground">
                {noKey
                  ? serverId !== undefined
                    ? "This server has no public key yet, so it can't receive secrets."
                    : "No server has a public key yet, so there is nobody to receive this secret."
                  : pushSummary(scopeKeys.length, pending.length)}
              </p>
            ) : null}
          </div>
        )}
        {provide ? null : (
          <div className="flex items-center gap-2">
            <Switch id="sv-secret" checked={secret} disabled={existing !== null} onCheckedChange={setSecret} />
            <Label htmlFor="sv-secret">Secret</Label>
          </div>
        )}
        <div className="flex items-center gap-2">
          <Checkbox id="sv-reload" checked={reload} onCheckedChange={(c) => setReload(c === true)} />
          <Label htmlFor="sv-reload">Reload apps that use it now</Label>
        </div>
        {provide ? null : (
          <Disclosure label="Advanced" defaultOpen={Boolean(existing?.description)}>
            <div className="space-y-2">
              <Label htmlFor="sv-description">Description</Label>
              <Input id="sv-description" value={description} onChange={(e) => setDescription(e.target.value)} />
            </div>
            {mode.kind === "create" && chooseScope && server ? (
              <div className="space-y-2">
                <Label id="sv-scope-label">Applies to</Label>
                <RadioGroup
                  aria-labelledby="sv-scope-label"
                  value={scope}
                  onValueChange={(v) => setScope(v as VariableScope)}
                  className="gap-2"
                >
                  <label className="flex cursor-pointer items-center gap-2 text-sm">
                    <RadioGroupItem value="org" aria-label="All servers" />
                    All servers
                  </label>
                  <label className="flex cursor-pointer items-center gap-2 text-sm">
                    <RadioGroupItem value="server" aria-label={`${server.name} only`} />
                    {server.name} only
                  </label>
                </RadioGroup>
              </div>
            ) : existing ? (
              <p className="text-sm text-muted-foreground">Applies to {scopeText}.</p>
            ) : null}
          </Disclosure>
        )}
      </form>
    </ResponsiveDialog>
  );
}
```

(The `provide` branch of the old `save` — `else if (provide && existing)` — built the same body as the `typingSecret` branch; a provide is always `typingSecret`, so one branch covers both.)

Replace `web/src/pages/settings-shared-variables.tsx` with:

```tsx
import { useMemo, type ReactNode } from "react";
import { Braces, Lock, Plus } from "lucide-react";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { Disclosure } from "@/components/disclosure";
import { EmptyState } from "@/components/empty-state";
import { ListPlaceholder } from "@/components/list-placeholder";
import { PageHeader } from "@/components/page-header";
import { ReloadRolloutStrip } from "@/components/rollout-strip";
import { RowMenu } from "@/components/row-menu";
import { Row, RowList } from "@/components/section";
import { SharedVariableDialog } from "@/components/shared-variable-dialog";
import { Button } from "@/components/ui/button";
import { isStandalone } from "@/config";
import { useApps } from "@/context/use-apps";
import { useServers } from "@/context/use-servers";
import { useSharedVariablesSettings } from "@/hooks/use-shared-variables-settings";
import { reloadableStaleCount } from "@/lib/app-status";
import type { RolloutsApi } from "@/lib/rollouts";
import {
  coverageIncomplete,
  coverageLabel,
  createSharedVariablesApi,
  failedServers,
  variableRows,
  type SharedVariablesApi,
} from "@/lib/shared-variables";
import { cn } from "@/lib/utils";

// Notice is a banner shown only while something needs doing, with the one
// action that does it; label names it for assistive technology.
function Notice({
  label,
  tone,
  action,
  children,
}: {
  label: string;
  tone: "warn" | "bad";
  action: ReactNode;
  children: ReactNode;
}) {
  return (
    <div
      role={tone === "bad" ? "alert" : "status"}
      aria-label={label}
      className={cn(
        "flex flex-wrap items-center gap-3 rounded-xl border px-4 py-2.5 text-sm",
        tone === "bad" ? "border-status-bad/40 bg-status-bad/10" : "border-status-warn/40 bg-status-warn/10",
      )}
    >
      <span className="mr-auto min-w-0">{children}</span>
      {action}
    </div>
  );
}

// SharedVariablesSettingsPage is Settings → Shared variables: one list of
// the organization's variables ("All servers") and the active server's own
// ("{server} only"), the built-ins folded away, and banners only while
// something needs doing (a failed sync, apps on old values, a secret this
// server lacks). New variables are org-wide; "{server} only" is under
// Advanced, and not offered in a standalone install. api, rollouts and
// standalone are for tests.
export default function SharedVariablesSettingsPage({
  api: injected,
  rollouts,
  standalone = isStandalone,
}: {
  api?: SharedVariablesApi;
  rollouts?: RolloutsApi;
  standalone?: boolean;
}) {
  const api = useMemo(() => injected ?? createSharedVariablesApi(), [injected]);
  const { activeServer } = useServers();
  const { staleVarsByApp, statusByApp } = useApps();
  const page = useSharedVariablesSettings(api, activeServer?.id ?? null);
  const serverName = activeServer?.name ?? "";
  const server = activeServer ? { id: activeServer.id, name: activeServer.name } : null;

  const rows = page.org ? variableRows(page.org.variables, page.server?.variables ?? [], serverName) : null;
  const failed = failedServers(page.sync, page.serverNames);
  // Stopped stale apps apply on their next start; the agent skips them.
  const staleCount = activeServer ? reloadableStaleCount(staleVarsByApp, statusByApp) : 0;
  const missing = page.server?.missing_org_secrets ?? [];
  const firstMissing = page.server?.inherited.find((v) => missing.includes(v.name));
  const builtins = page.server?.builtins ?? [];
  const toDelete = page.toDelete;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Shared variables"
        subtitle="Values every app can use as ${NAME}. An app's own variable with the same name wins."
        actions={
          <Button disabled={page.org === null} onClick={() => page.openDialog({ kind: "create" })}>
            <Plus className="size-4" /> Add variable
          </Button>
        }
      />
      <ReloadRolloutStrip api={rollouts} serverNames={page.serverNames} />
      {failed.length > 0 ? (
        <Notice
          label="Sync"
          tone="bad"
          action={
            <Button size="sm" variant="outline" onClick={() => page.sendAgain(failed.map((f) => f.id))}>
              Send again
            </Button>
          }
        >
          {failed.length === 1
            ? `Sync to ${failed[0].name} failed.`
            : `Sync to ${failed.length} servers failed: ${failed.map((f) => f.name).join(", ")}.`}
        </Notice>
      ) : null}
      {staleCount > 0 ? (
        <Notice
          label="Old values"
          tone="warn"
          action={
            <Button size="sm" variant="outline" onClick={page.reloadStale}>
              Reload
            </Button>
          }
        >
          {staleCount === 1 ? `1 app on ${serverName} uses old values.` : `${staleCount} apps on ${serverName} use old values.`}
        </Notice>
      ) : null}
      {firstMissing ? (
        <Notice
          label="Missing secrets"
          tone="warn"
          action={
            <Button size="sm" variant="outline" onClick={() => page.openDialog({ kind: "provide", variable: firstMissing })}>
              Provide value
            </Button>
          }
        >
          {missing.length === 1 ? `1 secret isn't on ${serverName} yet.` : `${missing.length} secrets aren't on ${serverName} yet.`}
        </Notice>
      ) : null}
      {rows === null ? (
        <ListPlaceholder
          loading={!page.loadFailed}
          failed="Couldn't load the shared variables."
          onRetry={() => void page.load()}
        />
      ) : rows.length === 0 ? (
        <EmptyState icon={Braces} title="No shared variables yet. Add one to use it in any app as ${NAME}." />
      ) : (
        <RowList label="Shared variables">
          {rows.map(({ variable: v, scopeLabel, overriddenHere }) => {
            const missingCopies = v.is_secret && v.missing_on.length > 0;
            // Where a secret has no copy: the org's coverage, or this server.
            const gap =
              v.scope === "org"
                ? coverageIncomplete(v, page.keys)
                  ? coverageLabel(v, page.serverNames, page.keys)
                  : ""
                : missingCopies
                  ? `Missing on ${serverName}`
                  : "";
            return (
              <Row
                key={v.id}
                title={<span className="font-mono">{v.name}</span>}
                description={
                  <span className="flex flex-wrap items-center gap-x-2">
                    {/* Never a secret's value: the API only ever sends "<encrypted>". */}
                    {v.is_secret ? (
                      <span className="inline-flex items-center gap-1">
                        <Lock className="size-3" aria-hidden />
                        Secret
                      </span>
                    ) : (
                      <code className="truncate">{v.value}</code>
                    )}
                    {v.description ? <span>· {v.description}</span> : null}
                    {overriddenHere ? <span>· {serverName} uses its own value</span> : null}
                    {gap ? <span className="font-medium text-status-warn">· {gap}</span> : null}
                  </span>
                }
                meta={scopeLabel}
                actions={
                  <>
                    {missingCopies ? (
                      <Button size="sm" variant="outline" onClick={() => page.openDialog({ kind: "provide", variable: v })}>
                        Provide value
                      </Button>
                    ) : null}
                    <RowMenu
                      label={`Actions for ${v.name}`}
                      items={[
                        { label: "Edit", onSelect: () => page.openDialog({ kind: "edit", variable: v }) },
                        { label: "Reload apps using it", onSelect: () => page.reloadVariable(v) },
                        v.scope === "org" && { label: "Send to servers again", onSelect: () => page.resendVariable(v) },
                        { label: "Delete", destructive: true, onSelect: () => page.setToDelete(v) },
                      ]}
                    />
                  </>
                }
              />
            );
          })}
        </RowList>
      )}
      {builtins.length > 0 ? (
        <Disclosure label={`Built-in variables (${builtins.length})`}>
          <RowList label="Built-in variables">
            {builtins.map((b) => (
              <Row
                key={b.name}
                title={<span className="font-mono">{b.name}</span>}
                description={b.value ? <code>{b.value}</code> : "Set by the agent"}
                meta={b.description}
              />
            ))}
          </RowList>
        </Disclosure>
      ) : null}
      {page.dialog ? (
        <SharedVariableDialog
          key={page.dialogKey}
          open
          mode={page.dialog}
          keys={page.keys}
          server={server}
          chooseScope={!standalone && server !== null}
          onOpenChange={(o) => {
            if (!o) page.closeDialog();
          }}
          onSave={page.save}
        />
      ) : null}
      <ConfirmDialog
        open={toDelete !== null}
        onOpenChange={(o) => !o && page.setToDelete(null)}
        title={`Delete ${toDelete?.name ?? "this variable"}?`}
        description={
          toDelete?.scope === "server"
            ? "Apps that use it fall back to the organization's value, if there is one, on their next deploy or reload."
            : "It's removed from every server. Apps that use it lose the value on their next deploy or reload."
        }
        confirmLabel="Delete variable"
        pendingLabel="Deleting…"
        onConfirm={() => (toDelete ? page.remove(toDelete) : undefined)}
      />
    </div>
  );
}
```

Delete what the merged page replaces, after checking nothing else imports it:

```bash
grep -rnE "pages/org-variables|pages/settings-variables|components/variables-table|delete-variable-dialog|use-shared-variables-page" web/src --include=*.ts --include=*.tsx \
  | grep -vE "^web/src/(pages/org-variables|pages/settings-variables|components/variables-table|components/delete-variable-dialog|hooks/use-shared-variables-page)"
```

Expected: no output (phase 1's route table redirects `/org/variables`; only the old page files import each other). Then:

```bash
git rm web/src/pages/org-variables.tsx web/src/pages/org-variables.test.tsx web/src/pages/settings-variables.tsx web/src/pages/settings-variables.test.tsx web/src/components/variables-table.tsx web/src/components/variables-table.test.tsx web/src/components/delete-variable-dialog.tsx web/src/hooks/use-shared-variables-page.ts
```


- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/components/shared-variable-dialog.test.tsx src/pages/settings-shared-variables.test.tsx src/hooks/use-shared-variables-settings.test.tsx src/lib/shared-variables.test.ts src/routes.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/components/shared-variable-dialog.tsx web/src/components/shared-variable-dialog.test.tsx web/src/pages/settings-shared-variables.tsx web/src/pages/settings-shared-variables.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): one Shared variables page for org and server scopes

Replaces the separate organization and server variable pages: one list
with each variable's scope, banners only when a sync failed, apps use old
values or a secret is missing here, and "Applies to" under Advanced.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 10: Notifications → Channels — service icons, "Other (URL)", SMTP extras under Advanced

**Files:**
- Modify: `web/src/lib/alerts.ts`, `web/src/lib/alerts.test.ts` (`FieldSpec.advanced`, the raw service's label)
- Create: `web/src/components/alerts/service-icon.tsx`, `web/src/hooks/use-alert-channels.ts`
- Rename + rewrite: `web/src/components/alerts/channels-tab.tsx` → `channels-section.tsx` (`ChannelsSection`), `channels-tab.test.tsx` → `channels-section.test.tsx`
- Rewrite: `web/src/components/alerts/channel-dialog.tsx` (on `ResponsiveDialog`), modify `channel-dialog.test.tsx`
- Modify: `web/src/pages/settings-notifications.tsx`, `web/src/pages/settings-notifications.test.tsx`

**Interfaces:**
- Consumes: Task 1 (`RowMenu`, `ConfirmDialog`, `ResponsiveDialog`); phase 1 `Section`, `RowList`, `Row` (with `testId`), `EmptyState`, `Disclosure`, `PageHeader`; `alertsGet`/`alertsPost` (`@/hooks/use-alerts`), `buildChannel`, `channelService`, `channelSummary`, `SERVICES`, `serviceSpec`, `Channel`, `FieldSpec`, `ServiceId` (`@/lib/alerts`); `useNow`, `relativeTime`.
- Produces:
  - `FieldSpec.advanced?: boolean` — the email service's `port`, `username`, `password` are advanced; `serviceSpec("raw").label === "Other (URL)"`.
  - `ServiceIcon({ service: ServiceId; className?: string })` (`@/components/alerts/service-icon`).
  - `useAlertChannels(): { channels: Channel[]; loading: boolean; error: string | null; refresh: () => Promise<void> }` (`@/hooks/use-alert-channels`) — the page's one channel list, shared with the rules (Task 11).
  - `ChannelsSection({ channels, loading, error, onChanged }: { channels: Channel[]; loading: boolean; error: string | null; onChanged: () => Promise<void> | void })` (`@/components/alerts/channels-section`).
  - `ChannelDialog` keeps its props (`open, onOpenChange, channel?, onSaved`).

- [ ] **Step 1: Write the failing tests**

In `web/src/lib/alerts.test.ts`, add `serviceSpec` to the `import { … } from "@/lib/alerts"` list and append:

```ts
describe("channel form wording", () => {
  it("calls a raw Shoutrrr URL Other (URL) and folds SMTP's port and sign-in under Advanced", () => {
    expect(serviceSpec("raw").label).toBe("Other (URL)");
    expect(serviceSpec("email").fields.filter((f) => f.advanced).map((f) => f.key)).toEqual(["port", "username", "password"]);
    expect(SERVICES.filter((s) => s.id !== "email").every((s) => s.fields.every((f) => !f.advanced))).toBe(true);
  });
});
```

Rename the test file and replace its content:

```bash
git mv web/src/components/alerts/channels-tab.test.tsx web/src/components/alerts/channels-section.test.tsx
```

`web/src/components/alerts/channels-section.test.tsx`:

```tsx
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ChannelsSection } from "@/components/alerts/channels-section";
import { useAlertChannels } from "@/hooks/use-alert-channels";
import type { Channel } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";

afterEach(() => vi.unstubAllGlobals());

const channels: Channel[] = [
  {
    id: "c1",
    name: "Ops",
    service: "telegram",
    fields: { ui_service: "telegram" },
    is_default: true,
    enabled: true,
    last_error: "",
    last_sent_at: "2026-10-11T12:00:00Z",
    created_at: "2026-10-11T11:00:00Z",
  },
  {
    id: "c2",
    name: "Hook",
    service: "generic",
    fields: {},
    is_default: false,
    enabled: false,
    last_error: "dial tcp: connection refused",
    last_sent_at: null,
    created_at: "2026-10-11T11:00:00Z",
  },
];

function Harness() {
  const c = useAlertChannels();
  return <ChannelsSection channels={c.channels} loading={c.loading} error={c.error} onChanged={c.refresh} />;
}

describe("ChannelsSection", () => {
  it("lists channels as rows with their state, tests one and deletes one after a confirmation", async () => {
    const api = fakeAPI({
      "/api/v1/alerts/get-channels": channels,
      "/api/v1/alerts/delete-channel": null,
      "/api/v1/alerts/test-channel": { ok: true, error: "" },
    });
    const user = userEvent.setup({ delay: null });
    render(<Harness />);

    const rows = await screen.findAllByTestId("channel-row");
    expect(rows).toHaveLength(2);
    expect(within(rows[0]).getByText("Default")).toBeInTheDocument();
    expect(within(rows[0]).getByText("Telegram")).toBeInTheDocument();
    expect(within(rows[1]).getByText("Off")).toBeInTheDocument();
    expect(within(rows[1]).getByText(/Last delivery failed: dial tcp/)).toBeInTheDocument();

    await user.click(within(rows[0]).getByRole("button", { name: "Actions for Ops" }));
    await user.click(await screen.findByRole("menuitem", { name: "Send test" }));
    await waitFor(() => expect(api.posted("/api/v1/alerts/test-channel")).toEqual([{ id: "c1" }]));

    await user.click(within(rows[1]).getByRole("button", { name: "Actions for Hook" }));
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(confirm).toHaveTextContent("Delete Hook?");
    await user.click(within(confirm).getByRole("button", { name: "Delete channel" }));
    await waitFor(() => expect(api.posted("/api/v1/alerts/delete-channel")).toEqual([{ id: "c2" }]));
  });

  it("explains the empty state", async () => {
    fakeAPI({ "/api/v1/alerts/get-channels": null });
    render(<Harness />);
    expect(await screen.findByText(/No channels yet/)).toBeInTheDocument();
  });

  it("adds a channel from a grid of services, Other (URL) among them", async () => {
    fakeAPI({ "/api/v1/alerts/get-channels": [] });
    const user = userEvent.setup({ delay: null });
    render(<Harness />);
    await user.click(await screen.findByRole("button", { name: /Add channel/ }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("radio", { name: "Telegram" })).toHaveAttribute("aria-checked", "true");
    expect(within(dialog).getByRole("radio", { name: "Other (URL)" })).toBeInTheDocument();
    expect(within(dialog).queryByRole("radio", { name: /Shoutrrr/ })).not.toBeInTheDocument();
  });
});
```

In `web/src/components/alerts/channel-dialog.test.tsx`, append inside `describe("ChannelDialog", …)`:

```tsx
  it("keeps the SMTP port and sign-in under Advanced, and opens it when one is wrong", async () => {
    const api = fakeAPI({ "/api/v1/alerts/save-channel": {} });
    const user = userEvent.setup({ delay: null });
    render(<ChannelDialog open onOpenChange={() => {}} onSaved={() => {}} />);
    await user.type(screen.getByLabelText("Name"), "Mail");
    await user.click(screen.getByRole("radio", { name: "Email (SMTP)" }));
    expect(screen.queryByLabelText("Port")).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("SMTP server"), "smtp.example.com");
    await user.type(screen.getByLabelText("From"), "wf@example.com");
    await user.type(screen.getByLabelText("To"), "ops@example.com");
    await user.click(screen.getByRole("button", { name: "Advanced" }));
    expect(screen.getByLabelText("Port")).toHaveValue("587");
    await user.clear(screen.getByLabelText("Port"));
    await user.type(screen.getByLabelText("Port"), "x");
    await user.click(screen.getByRole("button", { name: "Advanced" }));
    expect(screen.queryByLabelText("Port")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Port must be a number");
    expect(screen.getByLabelText("Port")).toHaveValue("x");
    expect(api.calls).toHaveLength(0);
  });

  it("closes on Cancel without saving", async () => {
    const api = fakeAPI({});
    const onOpenChange = vi.fn();
    const user = userEvent.setup({ delay: null });
    render(<ChannelDialog open onOpenChange={onOpenChange} onSaved={() => {}} />);
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(api.calls).toHaveLength(0);
  });
```

In `web/src/pages/settings-notifications.test.tsx`, change `expect(await screen.findByText("No channels yet")).toBeInTheDocument();` to `expect(await screen.findByText(/No channels yet/)).toBeInTheDocument();`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/alerts.test.ts src/components/alerts/channels-section.test.tsx src/components/alerts/channel-dialog.test.tsx src/pages/settings-notifications.test.tsx`
Expected: FAIL — `ChannelsSection`, `useAlertChannels` and `advanced` do not exist; the raw service is "Shoutrrr URL".

- [ ] **Step 3: Implement**

In `web/src/lib/alerts.ts`:

- add to `FieldSpec` (after `defaultValue?: string;`):

```ts
  // advanced fields sit under the channel form's "Advanced" disclosure.
  advanced?: boolean;
```

- in the `email` service's `fields`, mark the three optional connection details:

```ts
      { key: "host", label: "SMTP server", placeholder: "smtp.example.com" },
      { key: "port", label: "Port", defaultValue: "587", advanced: true },
      { key: "username", label: "Username", optional: true, advanced: true },
      { key: "password", label: "Password", secret: true, optional: true, advanced: true },
      { key: "from", label: "From", placeholder: "winterflow@example.com" },
      { key: "to", label: "To", placeholder: "ops@example.com", hint: "Comma-separated." },
```

- in the `raw` service, change `label: "Shoutrrr URL",` to `label: "Other (URL)",` and its field hint to `hint: "Any Shoutrrr service URL (containrrr.dev/shoutrrr), such as matrix:// or pushbullet://.",`.

Create `web/src/components/alerts/service-icon.tsx`:

```tsx
import { Bell, BellRing, Hash, Link, Mail, MessageCircle, Send, Smartphone, Webhook, type LucideIcon } from "lucide-react";

import type { ServiceId } from "@/lib/alerts";

const ICONS: Record<ServiceId, LucideIcon> = {
  telegram: Send,
  discord: MessageCircle,
  slack: Hash,
  ntfy: Bell,
  gotify: BellRing,
  pushover: Smartphone,
  email: Mail,
  webhook: Webhook,
  raw: Link,
};

// ServiceIcon is a notification service's glyph (decoration: the service's
// name is always written next to it).
export function ServiceIcon({ service, className }: { service: ServiceId; className?: string }) {
  const Icon = ICONS[service];
  return <Icon className={className} aria-hidden />;
}
```

Create `web/src/hooks/use-alert-channels.ts`:

```ts
import { useCallback, useEffect, useState } from "react";

import { alertsGet } from "@/hooks/use-alerts";
import type { Channel } from "@/lib/alerts";

// useAlertChannels is the organization's notification channels, listed
// once for the Notifications page: the channel rows and the rules' "notify"
// lines read the same list, so a rename or delete shows in both at once.
export function useAlertChannels() {
  const [channels, setChannels] = useState<Channel[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      setChannels((await alertsGet<Channel[] | null>("get-channels")) ?? []);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't load the channels");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return { channels, loading, error, refresh };
}
```

Rename the section and replace its content:

```bash
git mv web/src/components/alerts/channels-tab.tsx web/src/components/alerts/channels-section.tsx
```

`web/src/components/alerts/channels-section.tsx`:

```tsx
import { useState } from "react";
import { BellRing, Plus } from "lucide-react";
import { toast } from "sonner";

import { ChannelDialog } from "@/components/alerts/channel-dialog";
import { ServiceIcon } from "@/components/alerts/service-icon";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { RowMenu } from "@/components/row-menu";
import { Row, RowList, Section } from "@/components/section";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { alertsPost } from "@/hooks/use-alerts";
import { useNow } from "@/hooks/use-now";
import { channelService, channelSummary, type Channel } from "@/lib/alerts";
import { relativeTime } from "@/lib/utils";

// ChannelsSection is Notifications → Channels: where alerts are sent, each
// with its service, default and on/off state and last delivery; Send test,
// Edit and Delete in its menu. channels come from the page (shared with
// the rules); onChanged lists them again.
export function ChannelsSection({
  channels,
  loading,
  error,
  onChanged,
}: {
  channels: Channel[];
  loading: boolean;
  error: string | null;
  onChanged: () => Promise<void> | void;
}) {
  // editing: undefined = closed, null = a new channel.
  const [editing, setEditing] = useState<Channel | null | undefined>(undefined);
  const [deleting, setDeleting] = useState<Channel | null>(null);
  const [testing, setTesting] = useState<string | null>(null);
  const now = useNow();

  const test = async (ch: Channel) => {
    setTesting(ch.id);
    try {
      const res = await alertsPost<{ ok: boolean; error: string }>("test-channel", { id: ch.id });
      if (res.ok) toast.success(`Test sent to ${ch.name}`);
      else toast.error(`${ch.name}: ${res.error}`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Test failed");
    } finally {
      setTesting(null);
      void onChanged();
    }
  };

  const remove = async (ch: Channel) => {
    try {
      await alertsPost("delete-channel", { id: ch.id });
      toast.success(`Deleted ${ch.name}`);
      void onChanged();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Couldn't delete the channel");
      throw e;
    }
  };

  return (
    <Section
      title="Channels"
      description="Where alerts are sent: a chat app, email, or any webhook."
      actions={
        <Button size="sm" variant="outline" onClick={() => setEditing(null)}>
          <Plus className="size-4" /> Add channel
        </Button>
      }
    >
      {error ? (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      ) : null}
      {!loading && channels.length === 0 ? (
        <EmptyState icon={BellRing} title="No channels yet: alerts only show in the Inbox until you add one." />
      ) : (
        <RowList label="Channels">
          {channels.map((ch) => (
            <Row
              key={ch.id}
              testId="channel-row"
              leading={
                <span className="flex size-9 items-center justify-center rounded-lg bg-muted">
                  <ServiceIcon service={channelService(ch)} className="size-4 text-muted-foreground" />
                </span>
              }
              title={
                <span className="flex items-center gap-2">
                  <span className="truncate">{ch.name}</span>
                  {ch.is_default ? <Badge variant="secondary">Default</Badge> : null}
                  {!ch.enabled ? <Badge variant="outline">Off</Badge> : null}
                </span>
              }
              description={channelSummary(ch)}
              meta={
                ch.last_error ? (
                  <span className="line-clamp-1 max-w-64 text-status-bad" title={ch.last_error}>
                    Last delivery failed: {ch.last_error}
                  </span>
                ) : ch.last_sent_at ? (
                  `Last sent ${relativeTime(new Date(ch.last_sent_at), now)}`
                ) : (
                  "Nothing sent yet"
                )
              }
              actions={
                <RowMenu
                  label={`Actions for ${ch.name}`}
                  items={[
                    {
                      label: testing === ch.id ? "Sending test…" : "Send test",
                      disabled: testing === ch.id,
                      onSelect: () => void test(ch),
                    },
                    { label: "Edit", onSelect: () => setEditing(ch) },
                    { label: "Delete", destructive: true, onSelect: () => setDeleting(ch) },
                  ]}
                />
              }
            />
          ))}
        </RowList>
      )}

      {editing !== undefined ? (
        <ChannelDialog
          key={editing?.id ?? "new"}
          open
          onOpenChange={(open) => !open && setEditing(undefined)}
          channel={editing ?? undefined}
          onSaved={() => void onChanged()}
        />
      ) : null}

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={`Delete ${deleting?.name ?? "this channel"}?`}
        description="Rules that send to it skip it from now on. This can't be undone."
        confirmLabel="Delete channel"
        pendingLabel="Deleting…"
        onConfirm={() => (deleting ? remove(deleting) : undefined)}
      />
    </Section>
  );
}
```

Replace `web/src/components/alerts/channel-dialog.tsx` with:

```tsx
import { useState } from "react";
import { Send } from "lucide-react";
import { toast } from "sonner";

import { ServiceIcon } from "@/components/alerts/service-icon";
import { Disclosure } from "@/components/disclosure";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { alertsPost } from "@/hooks/use-alerts";
import {
  buildChannel,
  channelService,
  channelSummary,
  SERVICES,
  serviceSpec,
  type Channel,
  type FieldSpec,
  type ServiceId,
} from "@/lib/alerts";
import { cn } from "@/lib/utils";

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  // channel is the one being edited; undefined creates a new one.
  channel?: Channel;
  onSaved: () => void;
};

type TestResult = { ok: boolean; error: string };

const FORM_ID = "channel-form";

// initialValues prefills a service form: stored non-secret fields when
// editing, field defaults otherwise. Secrets always start empty.
function initialValues(id: ServiceId, channel?: Channel): Record<string, string> {
  const values: Record<string, string> = {};
  for (const f of serviceSpec(id).fields) {
    values[f.key] = (!f.secret && channel?.fields?.[f.key]) || f.defaultValue || "";
  }
  return values;
}

// advancedShown says whether a service form opens with Advanced unfolded:
// one of its advanced fields holds something other than its default.
function advancedShown(id: ServiceId, values: Record<string, string>): boolean {
  return serviceSpec(id).fields.some((f) => f.advanced && (values[f.key] ?? "") !== (f.defaultValue ?? ""));
}

// ChannelDialog creates or edits a notification channel. The service form
// builds the Shoutrrr URL in the browser; the URL is sent once and stored
// encrypted. Editing keeps the destination unless "Change destination" is
// used, which needs the secrets again. Optional connection details (SMTP
// port and sign-in) sit under "Advanced", which opens by itself when the
// form can't be built.
export function ChannelDialog({ open, onOpenChange, channel, onSaved }: Props) {
  const editing = channel !== undefined;
  const firstService: ServiceId = channel ? channelService(channel) : "telegram";
  const [name, setName] = useState(channel?.name ?? "");
  const [service, setService] = useState<ServiceId>(firstService);
  const [values, setValues] = useState<Record<string, string>>(() => initialValues(firstService, channel));
  const [advanced, setAdvanced] = useState(() => advancedShown(firstService, initialValues(firstService, channel)));
  const [isDefault, setIsDefault] = useState(channel?.is_default ?? true);
  const [enabled, setEnabled] = useState(channel?.enabled ?? true);
  const [changing, setChanging] = useState(!editing);
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState<"save" | "test" | null>(null);
  const [test, setTest] = useState<TestResult | null>(null);

  const spec = serviceSpec(service);
  const basic = spec.fields.filter((f) => !f.advanced);
  const more = spec.fields.filter((f) => f.advanced);

  const pickService = (id: ServiceId) => {
    const next = initialValues(id, channel && channelService(channel) === id ? channel : undefined);
    setService(id);
    setValues(next);
    setAdvanced(advancedShown(id, next));
    setFormError(null);
    setTest(null);
  };

  // destination builds the URL from the form, showing why it cannot (and
  // the folded fields, where the problem may be).
  const destination = () => {
    try {
      const built = buildChannel(service, values);
      setFormError(null);
      return built;
    } catch (e) {
      setFormError(e instanceof Error ? e.message : "Invalid form");
      if (more.length > 0) setAdvanced(true);
      return null;
    }
  };

  const runTest = async () => {
    setTest(null);
    let body: Record<string, string>;
    if (changing) {
      const built = destination();
      if (!built) return;
      body = { url: built.url };
    } else {
      body = { id: channel!.id };
    }
    setBusy("test");
    try {
      setTest(await alertsPost<TestResult>("test-channel", body));
    } catch (e) {
      setTest({ ok: false, error: e instanceof Error ? e.message : "Test failed" });
    } finally {
      setBusy(null);
    }
  };

  const save = async () => {
    if (!name.trim()) {
      setFormError("Name is required");
      return;
    }
    let url = "";
    let fields: Record<string, string> = {};
    if (changing) {
      const built = destination();
      if (!built) return;
      ({ url, fields } = built);
    }
    setBusy("save");
    try {
      await alertsPost("save-channel", {
        id: channel?.id ?? "",
        name: name.trim(),
        url,
        fields,
        is_default: isDefault,
        enabled,
      });
      toast.success(editing ? "Channel updated" : "Channel added");
      onSaved();
      onOpenChange(false);
    } catch (e) {
      setFormError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setBusy(null);
    }
  };

  const input = (f: FieldSpec) => (
    <div key={f.key} className="space-y-1.5">
      <Label htmlFor={`channel-${f.key}`}>
        {f.label}
        {f.optional ? <span className="font-normal text-muted-foreground"> (optional)</span> : null}
      </Label>
      <Input
        id={`channel-${f.key}`}
        type={f.secret ? "password" : "text"}
        autoComplete="off"
        placeholder={f.placeholder}
        value={values[f.key] ?? ""}
        onChange={(e) => setValues((v) => ({ ...v, [f.key]: e.target.value }))}
      />
      {f.hint ? <p className="text-xs text-muted-foreground">{f.hint}</p> : null}
    </div>
  );

  return (
    <ResponsiveDialog
      open={open}
      onOpenChange={onOpenChange}
      title={editing ? "Edit channel" : "Add channel"}
      description="Where alert notifications go. Secrets are encrypted on the server and never shown again."
      className="sm:max-w-lg"
      footer={
        <>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button type="button" variant="outline" onClick={() => void runTest()} disabled={busy !== null}>
            <Send className="size-4" /> {busy === "test" ? "Sending…" : "Send test"}
          </Button>
          <Button type="submit" form={FORM_ID} disabled={busy !== null}>
            {busy === "save" ? "Saving…" : "Save"}
          </Button>
        </>
      }
    >
      <form
        id={FORM_ID}
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <div className="space-y-1.5">
          <Label htmlFor="channel-name">Name</Label>
          <Input
            id="channel-name"
            value={name}
            maxLength={64}
            placeholder="Ops on Telegram"
            onChange={(e) => setName(e.target.value)}
          />
        </div>

        {changing ? (
          <>
            <div className="space-y-1.5">
              <Label id="channel-service-label">Service</Label>
              <div className="grid grid-cols-3 gap-2" role="radiogroup" aria-labelledby="channel-service-label">
                {SERVICES.map((s) => (
                  <button
                    key={s.id}
                    type="button"
                    role="radio"
                    aria-checked={service === s.id}
                    onClick={() => pickService(s.id)}
                    className={cn(
                      "flex min-h-16 flex-col items-center justify-center gap-1 rounded-lg border px-2 py-2 text-xs transition-colors duration-150 hover:border-primary/40",
                      service === s.id && "border-primary bg-primary/5 font-medium text-foreground",
                    )}
                  >
                    <ServiceIcon service={s.id} className="size-5" />
                    {s.label}
                  </button>
                ))}
              </div>
            </div>
            {basic.map(input)}
            {more.length > 0 ? (
              <Disclosure label="Advanced" open={advanced} onOpenChange={setAdvanced}>
                {more.map(input)}
              </Disclosure>
            ) : null}
          </>
        ) : (
          <div className="flex items-center justify-between gap-3 rounded-md border px-3 py-2 text-sm">
            <span className="truncate" data-testid="channel-destination">
              {channelSummary(channel!)}
            </span>
            <Button type="button" variant="ghost" size="sm" onClick={() => setChanging(true)}>
              Change destination
            </Button>
          </div>
        )}

        <div className="flex items-center justify-between gap-3">
          <div>
            <Label htmlFor="channel-default">Default channel</Label>
            <p className="text-xs text-muted-foreground">Rules without their own channels notify here.</p>
          </div>
          <Switch id="channel-default" checked={isDefault} onCheckedChange={setIsDefault} />
        </div>
        <div className="flex items-center justify-between gap-3">
          <Label htmlFor="channel-enabled">Enabled</Label>
          <Switch id="channel-enabled" checked={enabled} onCheckedChange={setEnabled} />
        </div>

        {formError ? (
          <p role="alert" className="text-sm text-destructive">
            {formError}
          </p>
        ) : null}
        {test ? (
          <p role="status" className={cn("text-sm", test.ok ? "text-status-ok" : "text-destructive")}>
            {test.ok ? "Test notification sent." : `Test failed: ${test.error}`}
          </p>
        ) : null}
      </form>
    </ResponsiveDialog>
  );
}
```

Replace `web/src/pages/settings-notifications.tsx` with:

```tsx
import { ChannelsSection } from "@/components/alerts/channels-section";
import { RulesTab } from "@/components/alerts/rules-tab";
import { PageHeader } from "@/components/page-header";
import { Section } from "@/components/section";
import { useAlertChannels } from "@/hooks/use-alert-channels";

// NotificationsSettingsPage is Settings → Notifications: where alerts are
// sent (channels) and when they fire (rules), on one page. The channel list
// is loaded once here and shared, so the rules always name current
// channels.
export default function NotificationsSettingsPage() {
  const channels = useAlertChannels();
  return (
    <div className="space-y-8">
      <PageHeader title="Notifications" subtitle="Where alerts are sent, and when they fire." />
      <ChannelsSection
        channels={channels.channels}
        loading={channels.loading}
        error={channels.error}
        onChanged={channels.refresh}
      />
      <Section title="Alert rules">
        <RulesTab />
      </Section>
    </div>
  );
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/alerts.test.ts src/components/alerts/channels-section.test.tsx src/components/alerts/channel-dialog.test.tsx src/pages/settings-notifications.test.tsx`
Expected: PASS (the existing channel-dialog tests — "builds the Shoutrrr URL…", "explains an invalid form…", "tests a draft by URL…", "edits name and flags…", "asks for the secrets again…" — pass unchanged: their fields are not advanced).

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/alerts.ts web/src/lib/alerts.test.ts web/src/components/alerts/service-icon.tsx web/src/hooks/use-alert-channels.ts web/src/components/alerts/channels-section.tsx web/src/components/alerts/channels-section.test.tsx web/src/components/alerts/channel-dialog.tsx web/src/components/alerts/channel-dialog.test.tsx web/src/pages/settings-notifications.tsx web/src/pages/settings-notifications.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): notification channels as rows with a service grid and Advanced SMTP details

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 11: Notifications → Alert rules — rows, a sentence-style rule dialog, a matching override dialog

**Files:**
- Modify: `web/src/lib/alerts.ts`, `web/src/lib/alerts.test.ts` (`routingText`: "Inbox only")
- Modify: `web/src/components/alerts/channel-picker.tsx` ("Inbox only", a name for the group)
- Rewrite: `web/src/components/alerts/rule-dialog.tsx`, modify `rule-dialog.test.tsx`
- Rewrite: `web/src/components/alerts/override-dialog.tsx` (same fields and routes, `ResponsiveDialog`, sentence, Cancel)
- Rename + rewrite: `web/src/components/alerts/rules-tab.tsx` → `rules-section.tsx` (`RulesSection`), `rules-tab.test.tsx` → `rules-section.test.tsx`
- Rewrite: `web/src/pages/settings-notifications.tsx`, `web/src/pages/settings-notifications.test.tsx`

If phase 4 already restyled `override-dialog.tsx` or `target-alerts-card.tsx`, keep its changes and apply only what this task lists for the dialog (shell, sentence, Cancel, "Inbox only"); `target-alerts-card.test.tsx` must keep passing unchanged.

**Interfaces:**
- Consumes: Task 1 (`RowMenu`, `ConfirmDialog`, `ResponsiveDialog`); Task 10 (`useAlertChannels`, `ChannelsSection`); phase 1 `Section`, `RowList`, `Row` (`testId`), `PageHeader`; `alertsGet`/`alertsPost`, `ALERT_METRICS`, `hasThreshold`, `metricInfo`, `routingText`, `ruleSummary`, `Rule`, `Override`, `Channel`.
- Produces:
  - `routingText([] , …) === "Inbox only"` (alert history lives in the Inbox).
  - `RulesSection({ channels }: { channels: Channel[] })` (`@/components/alerts/rules-section`) — the org's rules grouped Servers / Apps; `RuleDialog` and `OverrideDialog` keep their props.
  - The rule dialog reads as one sentence: "Alert when [a server|an app] [metric|is down] is above [x] unit for [n] minutes", then "Then notify" (Default channels / Specific channels / Inbox only); controls keep their accessible names (`Watch`, `Metric`, `Above (unit)`, `For (minutes)`).

- [ ] **Step 1: Write the failing tests**

In `web/src/lib/alerts.test.ts`, change `expect(routingText([], channels)).toBe("History only");` to `expect(routingText([], channels)).toBe("Inbox only");`.

In `web/src/components/alerts/rule-dialog.test.tsx`:
- in the first test, change the option `{ name: "Apps" }` to `{ name: "an app" }`;
- in the second test, change the option `{ name: "Down" }` to `{ name: "is down" }`;
- in the third test, change `screen.getByLabelText("History only")` to `screen.getByLabelText("Inbox only")`;
- add `within` to its `@testing-library/react` import, and append inside `describe("RuleDialog", …)`:

```tsx
  it("reads as one sentence and closes on Cancel", async () => {
    fakeAPI({});
    const onOpenChange = vi.fn();
    const user = userEvent.setup({ delay: null });
    render(<RuleDialog open onOpenChange={onOpenChange} channels={channels} onSaved={() => {}} />);
    const dialog = screen.getByRole("dialog");
    for (const words of ["Alert when", "is above", "%", "for", "minutes", "Then notify"]) {
      expect(within(dialog).getByText(words)).toBeInTheDocument();
    }
    expect(screen.getByRole("combobox", { name: "Watch" })).toHaveTextContent("a server");
    expect(screen.getByRole("combobox", { name: "Metric" })).toHaveTextContent("CPU");
    expect(screen.getByRole("radiogroup", { name: "Notify" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
```

Rename the rules test and replace its content:

```bash
git mv web/src/components/alerts/rules-tab.test.tsx web/src/components/alerts/rules-section.test.tsx
```

`web/src/components/alerts/rules-section.test.tsx`:

```tsx
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { RulesSection } from "@/components/alerts/rules-section";
import type { Channel, Rule } from "@/lib/alerts";
import { fakeAPI } from "@/test/fake-api";

afterEach(() => vi.unstubAllGlobals());

const base = { channel_ids: null, enabled: true, created_at: "2026-10-11T12:00:00Z" };
const rules: Rule[] = [
  { ...base, id: "r1", target_kind: "server", metric: "cpu", threshold: 90, duration_minutes: 10 },
  { ...base, id: "r2", target_kind: "server", metric: "down", threshold: 0, duration_minutes: 5 },
  { ...base, id: "r3", target_kind: "app", metric: "down", threshold: 0, duration_minutes: 5, channel_ids: ["c1"] },
];
const channels = [{ id: "c1", name: "Ops", enabled: true }] as Channel[];

describe("RulesSection", () => {
  it("groups rules by target, toggles one inline and deletes one after a confirmation", async () => {
    const api = fakeAPI({
      "/api/v1/alerts/get-rules": rules,
      "/api/v1/alerts/save-rule": {},
      "/api/v1/alerts/delete-rule": null,
    });
    const user = userEvent.setup({ delay: null });
    render(<RulesSection channels={channels} />);

    const servers = await screen.findByRole("region", { name: "Servers" });
    expect(within(servers).getAllByTestId("rule-row")).toHaveLength(2);
    expect(within(servers).getByText("CPU above 90% for 10 min")).toBeInTheDocument();
    expect(within(servers).getAllByText("Default channels")).toHaveLength(2);
    const apps = screen.getByRole("region", { name: "Apps" });
    expect(within(apps).getByText("App down for 5 min")).toBeInTheDocument();
    expect(within(apps).getByText("Ops")).toBeInTheDocument();

    await user.click(screen.getByRole("switch", { name: "Enable CPU above 90% for 10 min" }));
    expect(api.posted("/api/v1/alerts/save-rule")[0]).toMatchObject({ id: "r1", enabled: false, threshold: 90 });

    await user.click(screen.getByRole("button", { name: "Actions for Server down for 5 min" }));
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(confirm).toHaveTextContent("Server down for 5 min. Its adjustments go with it; its alerts stay in the Inbox.");
    await user.click(within(confirm).getByRole("button", { name: "Delete rule" }));
    await waitFor(() => expect(api.posted("/api/v1/alerts/delete-rule")).toEqual([{ id: "r2" }]));
  });

  it("opens the rule dialog to edit one", async () => {
    fakeAPI({ "/api/v1/alerts/get-rules": rules });
    const user = userEvent.setup({ delay: null });
    render(<RulesSection channels={channels} />);
    await user.click(await screen.findByRole("button", { name: "Actions for CPU above 90% for 10 min" }));
    await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Edit rule")).toBeInTheDocument();
    expect(within(dialog).getByLabelText(/Above/)).toHaveValue(90);
  });

  it("says when a group has no rules", async () => {
    fakeAPI({ "/api/v1/alerts/get-rules": null });
    render(<RulesSection channels={[]} />);
    expect(await screen.findByText("No rules for servers yet.")).toBeInTheDocument();
    expect(screen.getByText("No rules for apps yet.")).toBeInTheDocument();
  });
});
```

Replace `web/src/pages/settings-notifications.test.tsx` with:

```tsx
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import NotificationsSettingsPage from "@/pages/settings-notifications";
import { fakeAPI } from "@/test/fake-api";

vi.mock("@/context/use-servers", () => ({ useServers: () => ({ servers: [] }) }));

afterEach(() => vi.unstubAllGlobals());

const T = "2026-10-11T12:00:00Z";

describe("NotificationsSettingsPage", () => {
  it("puts the channels and the alert rules on one page", async () => {
    fakeAPI({ "/api/v1/alerts/get-rules": [], "/api/v1/alerts/get-channels": [] });
    render(
      <MemoryRouter>
        <NotificationsSettingsPage />
      </MemoryRouter>,
    );
    expect(screen.getByRole("heading", { level: 1, name: "Notifications" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Channels" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Alert rules" })).toBeInTheDocument();
    expect(await screen.findByText(/No channels yet/)).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: /Add rule/ })).toBeInTheDocument();
  });

  it("follows a channel rename in the rules without a reload", async () => {
    let name = "Ops";
    fakeAPI({
      "/api/v1/alerts/get-rules": [
        { id: "r1", target_kind: "server", metric: "cpu", threshold: 90, duration_minutes: 10, channel_ids: ["c1"], enabled: true, created_at: T },
      ],
      "/api/v1/alerts/get-channels": () => [
        {
          id: "c1",
          name,
          service: "ntfy",
          fields: { ui_service: "ntfy", server: "ntfy.sh" },
          is_default: true,
          enabled: true,
          last_error: "",
          last_sent_at: null,
          created_at: T,
        },
      ],
      "/api/v1/alerts/save-channel": () => {
        name = "Pager";
        return {};
      },
    });
    const user = userEvent.setup({ delay: null });
    render(
      <MemoryRouter>
        <NotificationsSettingsPage />
      </MemoryRouter>,
    );
    const rules = screen.getByRole("region", { name: "Alert rules" });
    expect(await within(rules).findByText("Ops")).toBeInTheDocument();
    await user.click(await screen.findByRole("button", { name: "Actions for Ops" }));
    await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
    const dialog = await screen.findByRole("dialog");
    await user.clear(within(dialog).getByLabelText("Name"));
    await user.type(within(dialog).getByLabelText("Name"), "Pager");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await within(rules).findByText("Pager")).toBeInTheDocument();
    expect(within(rules).queryByText("deleted channel")).not.toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/alerts.test.ts src/components/alerts/rule-dialog.test.tsx src/components/alerts/rules-section.test.tsx src/pages/settings-notifications.test.tsx`
Expected: FAIL — `RulesSection` does not exist, the wording is "History only", the dialog has no sentence or Cancel, and the rules list its own channels.

- [ ] **Step 3: Implement**

In `web/src/lib/alerts.ts`, in `routingText` change `if (ids.length === 0) return "History only";` to `if (ids.length === 0) return "Inbox only";`, and in the `Rule` type comment change `[] = none (History only)` to `[] = none (Inbox only)`.

In `web/src/components/alerts/channel-picker.tsx`:
- change the comments' "History" to "the Inbox";
- give the group a name: `<RadioGroup aria-label="Notify" value={routing} …>`;
- change `["none", "History only"],` to `["none", "Inbox only"],`;
- change the hint `No channels yet: add one in the Channels tab.` to `No channels yet: add one under Channels.`.

Replace `web/src/components/alerts/rule-dialog.tsx` with:

```tsx
import { useState } from "react";
import { toast } from "sonner";

import { ChannelPicker } from "@/components/alerts/channel-picker";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { alertsPost } from "@/hooks/use-alerts";
import {
  ALERT_METRICS,
  hasThreshold,
  metricInfo,
  type AlertMetric,
  type Channel,
  type Rule,
  type TargetKind,
} from "@/lib/alerts";

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  rule?: Rule;
  channels: Channel[];
  onSaved: () => void;
};

const FORM_ID = "rule-form";

// RuleDialog creates or edits an org-wide alert rule, written as one
// sentence: "Alert when [a server] [CPU] is above [90] % for [10] minutes",
// then where to notify. A rule's target kind and metric are fixed once
// created.
export function RuleDialog({ open, onOpenChange, rule, channels, onSaved }: Props) {
  const editing = rule !== undefined;
  const [kind, setKind] = useState<TargetKind>(rule?.target_kind ?? "server");
  const [metric, setMetric] = useState<AlertMetric>(rule?.metric ?? "cpu");
  const [threshold, setThreshold] = useState(String(rule?.threshold ?? metricInfo("server", "cpu")!.suggested));
  const [minutes, setMinutes] = useState(String(rule?.duration_minutes ?? 10));
  const [channelIds, setChannelIds] = useState<string[] | null>(rule?.channel_ids ?? null);
  const [enabled, setEnabled] = useState(rule?.enabled ?? true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const info = metricInfo(kind, metric);
  // The unit after the number ("%", "GB"); a load has none.
  const unit = info?.unit === "load" ? "" : (info?.unit ?? "");

  const pick = (k: TargetKind, m: AlertMetric) => {
    const next = metricInfo(k, m) ?? metricInfo(k, "down")!;
    setKind(k);
    setMetric(next.metric);
    setThreshold(String(next.suggested));
  };

  const save = async () => {
    const t = Number(threshold);
    const d = Number(minutes);
    if (!Number.isInteger(d) || d < 1 || d > 60) {
      setError("Duration must be 1 to 60 minutes");
      return;
    }
    if (hasThreshold(metric) && (!Number.isFinite(t) || t <= 0 || (info!.max > 0 && t > info!.max))) {
      setError(info!.max > 0 ? `Threshold must be above 0 and at most ${info!.max}` : "Threshold must be above 0");
      return;
    }
    setBusy(true);
    try {
      await alertsPost("save-rule", {
        id: rule?.id ?? "",
        target_kind: kind,
        metric,
        threshold: hasThreshold(metric) ? t : 0,
        duration_minutes: d,
        channel_ids: channelIds,
        enabled,
      });
      toast.success(editing ? "Rule updated" : "Rule added");
      onSaved();
      onOpenChange(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <ResponsiveDialog
      open={open}
      onOpenChange={onOpenChange}
      title={editing ? "Edit rule" : "Add rule"}
      description={`Applies to every ${kind === "app" ? "app" : "server"} in the organization; each can adjust it on its own page.`}
      className="sm:max-w-lg"
      footer={
        <>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button type="submit" form={FORM_ID} disabled={busy}>
            {busy ? "Saving…" : "Save"}
          </Button>
        </>
      }
    >
      <form
        id={FORM_ID}
        className="space-y-5"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span>Alert when</span>
          <Select value={kind} onValueChange={(v) => pick(v as TargetKind, metric)} disabled={editing}>
            <SelectTrigger aria-label="Watch" className="h-9 w-auto min-w-28">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="server">a server</SelectItem>
              <SelectItem value="app">an app</SelectItem>
            </SelectContent>
          </Select>
          <Select value={metric} onValueChange={(v) => pick(kind, v as AlertMetric)} disabled={editing}>
            <SelectTrigger aria-label="Metric" className="h-9 w-auto min-w-28">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {ALERT_METRICS.filter((m) => m.kind === kind).map((m) => (
                <SelectItem key={m.metric} value={m.metric}>
                  {hasThreshold(m.metric) ? m.label : "is down"}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {hasThreshold(metric) ? (
            <>
              <span>is above</span>
              <Input
                aria-label={`Above (${info?.unit === "load" ? "load" : info?.unit})`}
                className="h-9 w-20"
                type="number"
                inputMode="decimal"
                min={0}
                max={info?.max || undefined}
                step="any"
                value={threshold}
                onChange={(e) => setThreshold(e.target.value)}
              />
              {unit ? <span>{unit}</span> : null}
            </>
          ) : null}
          <span>for</span>
          <Input
            aria-label="For (minutes)"
            className="h-9 w-16"
            type="number"
            min={1}
            max={60}
            value={minutes}
            onChange={(e) => setMinutes(e.target.value)}
          />
          <span>minutes</span>
        </div>
        <div className="space-y-1.5">
          <p className="text-sm font-medium">Then notify</p>
          <ChannelPicker value={channelIds} onChange={setChannelIds} channels={channels} idPrefix="rule" />
        </div>
        <div className="flex items-center justify-between">
          <Label htmlFor="rule-enabled">Enabled</Label>
          <Switch id="rule-enabled" checked={enabled} onCheckedChange={setEnabled} />
        </div>
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : null}
      </form>
    </ResponsiveDialog>
  );
}
```

Replace `web/src/components/alerts/override-dialog.tsx` with:

```tsx
import { useState } from "react";
import { toast } from "sonner";

import { ResponsiveDialog } from "@/components/responsive-dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Switch } from "@/components/ui/switch";
import { alertsPost } from "@/hooks/use-alerts";
import { hasThreshold, metricInfo, routingText, ruleSummary, type Channel, type Override, type Rule } from "@/lib/alerts";

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  rule: Rule;
  override?: Override;
  channels: Channel[];
  serverId: string;
  appId: string;
  // targetLabel names the server or app ("prod-1", "nextcloud").
  targetLabel: string;
  onSaved: () => void;
};

type Notify = "inherit" | "specific" | "none";

const FORM_ID = "override-form";

// OverrideDialog adjusts one rule for one server or app, in the rule
// dialog's sentence: switch it off here, change its threshold or duration,
// or send elsewhere. Empty fields keep the rule's value; "Reset to rule"
// removes the adjustment.
export function OverrideDialog({ open, onOpenChange, rule, override, channels, serverId, appId, targetLabel, onSaved }: Props) {
  const info = metricInfo(rule.target_kind, rule.metric);
  const unit = info?.unit === "load" ? "" : (info?.unit ?? "");
  const [on, setOn] = useState(!override?.disabled);
  const [threshold, setThreshold] = useState(override?.threshold != null ? String(override.threshold) : "");
  const [minutes, setMinutes] = useState(override?.duration_minutes != null ? String(override.duration_minutes) : "");
  const initialNotify: Notify =
    override?.channel_ids == null ? "inherit" : override.channel_ids.length === 0 ? "none" : "specific";
  const [notify, setNotify] = useState<Notify>(initialNotify);
  const [picked, setPicked] = useState<string[]>(override?.channel_ids ?? []);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const target = { server_id: serverId, app_id: appId };

  const save = async () => {
    let t: number | null = null;
    if (hasThreshold(rule.metric) && threshold.trim() !== "") {
      t = Number(threshold);
      if (!Number.isFinite(t) || t <= 0 || (info!.max > 0 && t > info!.max)) {
        setError(info!.max > 0 ? `Threshold must be above 0 and at most ${info!.max}` : "Threshold must be above 0");
        return;
      }
    }
    let d: number | null = null;
    if (minutes.trim() !== "") {
      d = Number(minutes);
      if (!Number.isInteger(d) || d < 1 || d > 60) {
        setError("Duration must be 1 to 60 minutes");
        return;
      }
    }
    const channelIds = notify === "inherit" ? null : notify === "none" ? [] : picked;
    setBusy(true);
    try {
      await alertsPost("save-override", {
        rule_id: rule.id,
        ...target,
        disabled: !on,
        threshold: t,
        duration_minutes: d,
        channel_ids: channelIds,
      });
      toast.success("Override saved");
      onSaved();
      onOpenChange(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setBusy(false);
    }
  };

  const reset = async () => {
    setBusy(true);
    try {
      await alertsPost("delete-override", { rule_id: rule.id, ...target });
      toast.success("Back to the rule's settings");
      onSaved();
      onOpenChange(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Reset failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <ResponsiveDialog
      open={open}
      onOpenChange={onOpenChange}
      title={`Adjust for ${targetLabel}`}
      description={`Rule: ${ruleSummary(rule)}, ${routingText(rule.channel_ids, channels).toLowerCase()}. Empty fields keep the rule's values.`}
      className="sm:max-w-lg"
      footer={
        <>
          {override ? (
            <Button type="button" variant="ghost" className="sm:mr-auto" onClick={() => void reset()} disabled={busy}>
              Reset to rule
            </Button>
          ) : null}
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button type="submit" form={FORM_ID} disabled={busy}>
            {busy ? "Saving…" : "Save"}
          </Button>
        </>
      }
    >
      <form
        id={FORM_ID}
        className="space-y-5"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <div className="flex items-center justify-between">
          <Label htmlFor="override-on">Alert on {targetLabel}</Label>
          <Switch id="override-on" checked={on} onCheckedChange={setOn} />
        </div>
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span>Alert when it</span>
          {hasThreshold(rule.metric) ? (
            <>
              <span>is above</span>
              <Input
                id="override-threshold"
                aria-label={`Above (${info?.unit})`}
                className="h-9 w-20"
                type="number"
                step="any"
                placeholder={String(rule.threshold)}
                value={threshold}
                disabled={!on}
                onChange={(e) => setThreshold(e.target.value)}
              />
              {unit ? <span>{unit}</span> : null}
            </>
          ) : (
            <span>is down</span>
          )}
          <span>for</span>
          <Input
            id="override-minutes"
            aria-label="For (minutes)"
            className="h-9 w-16"
            type="number"
            min={1}
            max={60}
            placeholder={String(rule.duration_minutes)}
            value={minutes}
            disabled={!on}
            onChange={(e) => setMinutes(e.target.value)}
          />
          <span>minutes</span>
        </div>
        <div className="space-y-2">
          <p className="text-sm font-medium">Then notify</p>
          <RadioGroup aria-label="Notify" value={notify} onValueChange={(v) => setNotify(v as Notify)} disabled={!on}>
            {(
              [
                ["inherit", "Same as the rule"],
                ["specific", "Specific channels"],
                ["none", "Inbox only"],
              ] as const
            ).map(([v, label]) => (
              <div key={v} className="flex items-center gap-2">
                <RadioGroupItem value={v} id={`override-${v}`} />
                <Label htmlFor={`override-${v}`} className="font-normal">
                  {label}
                </Label>
              </div>
            ))}
          </RadioGroup>
          {notify === "specific" ? (
            <div className="grid gap-1.5 pl-6">
              {channels.map((c) => (
                <div key={c.id} className="flex items-center gap-2">
                  <Checkbox
                    id={`override-ch-${c.id}`}
                    checked={picked.includes(c.id)}
                    onCheckedChange={(checked) => setPicked((p) => (checked ? [...p, c.id] : p.filter((id) => id !== c.id)))}
                  />
                  <Label htmlFor={`override-ch-${c.id}`} className="font-normal">
                    {c.name}
                  </Label>
                </div>
              ))}
            </div>
          ) : null}
        </div>
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : null}
      </form>
    </ResponsiveDialog>
  );
}
```

Rename the rules list and replace its content:

```bash
git mv web/src/components/alerts/rules-tab.tsx web/src/components/alerts/rules-section.tsx
```

`web/src/components/alerts/rules-section.tsx`:

```tsx
import { useCallback, useEffect, useState } from "react";
import { Plus } from "lucide-react";
import { toast } from "sonner";

import { RuleDialog } from "@/components/alerts/rule-dialog";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { RowMenu } from "@/components/row-menu";
import { Row, RowList, Section } from "@/components/section";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { alertsGet, alertsPost } from "@/hooks/use-alerts";
import { routingText, ruleSummary, type Channel, type Rule, type TargetKind } from "@/lib/alerts";
import { cn } from "@/lib/utils";

// RulesSection is Notifications → Alert rules: the org's rules, servers
// first, each with an inline on/off switch and Edit/Delete in its menu.
// channels come from the page (shared with Channels), so each rule's
// "notify" line names current channels.
export function RulesSection({ channels }: { channels: Channel[] }) {
  const [rules, setRules] = useState<Rule[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  // editing: undefined = closed, null = a new rule.
  const [editing, setEditing] = useState<Rule | null | undefined>(undefined);
  const [deleting, setDeleting] = useState<Rule | null>(null);

  const refresh = useCallback(async () => {
    try {
      setRules((await alertsGet<Rule[] | null>("get-rules")) ?? []);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't load the rules");
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const toggle = async (rule: Rule, enabled: boolean) => {
    setRules((rs) => rs && rs.map((r) => (r.id === rule.id ? { ...r, enabled } : r)));
    try {
      await alertsPost("save-rule", { ...rule, enabled });
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Update failed");
      void refresh();
    }
  };

  const remove = async (rule: Rule) => {
    try {
      await alertsPost("delete-rule", { id: rule.id });
      toast.success("Rule deleted");
      void refresh();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Couldn't delete the rule");
      throw e;
    }
  };

  const group = (kind: TargetKind, title: string) => {
    const list = (rules ?? []).filter((r) => r.target_kind === kind);
    return (
      <section className="space-y-2" aria-label={title}>
        <h3 className="px-1 text-xs font-medium text-muted-foreground">{title}</h3>
        {list.length === 0 ? (
          <p className="px-1 text-sm text-muted-foreground">No rules for {kind === "app" ? "apps" : "servers"} yet.</p>
        ) : (
          <RowList label={`${title} rules`}>
            {list.map((r) => (
              <Row
                key={r.id}
                testId="rule-row"
                leading={
                  <Switch
                    checked={r.enabled}
                    aria-label={`Enable ${ruleSummary(r)}`}
                    onCheckedChange={(v) => void toggle(r, v)}
                  />
                }
                title={<span className={cn(!r.enabled && "opacity-60")}>{ruleSummary(r)}</span>}
                description={routingText(r.channel_ids, channels)}
                actions={
                  <RowMenu
                    label={`Actions for ${ruleSummary(r)}`}
                    items={[
                      { label: "Edit", onSelect: () => setEditing(r) },
                      { label: "Delete", destructive: true, onSelect: () => setDeleting(r) },
                    ]}
                  />
                }
              />
            ))}
          </RowList>
        )}
      </section>
    );
  };

  return (
    <Section
      title="Alert rules"
      description="When an alert fires. A server or app can adjust a rule from its Monitoring or Metrics page."
      actions={
        <Button size="sm" variant="outline" onClick={() => setEditing(null)}>
          <Plus className="size-4" /> Add rule
        </Button>
      }
    >
      {error ? (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      ) : null}
      {rules === null && !error ? (
        <div className="flex h-16 items-center justify-center">
          <Spinner />
        </div>
      ) : (
        <div className="space-y-4">
          {group("server", "Servers")}
          {group("app", "Apps")}
        </div>
      )}

      {editing !== undefined ? (
        <RuleDialog
          key={editing?.id ?? "new"}
          open
          onOpenChange={(open) => !open && setEditing(undefined)}
          rule={editing ?? undefined}
          channels={channels}
          onSaved={() => void refresh()}
        />
      ) : null}

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title="Delete this rule?"
        description={`${deleting ? ruleSummary(deleting) : ""}. Its adjustments go with it; its alerts stay in the Inbox.`}
        confirmLabel="Delete rule"
        pendingLabel="Deleting…"
        onConfirm={() => (deleting ? remove(deleting) : undefined)}
      />
    </Section>
  );
}
```

Replace `web/src/pages/settings-notifications.tsx` with:

```tsx
import { ChannelsSection } from "@/components/alerts/channels-section";
import { RulesSection } from "@/components/alerts/rules-section";
import { PageHeader } from "@/components/page-header";
import { useAlertChannels } from "@/hooks/use-alert-channels";

// NotificationsSettingsPage is Settings → Notifications: where alerts are
// sent (channels) and when they fire (rules), on one page. The channel list
// is loaded once here and shared, so a renamed or deleted channel shows in
// the rules at once.
export default function NotificationsSettingsPage() {
  const channels = useAlertChannels();
  return (
    <div className="space-y-8">
      <PageHeader title="Notifications" subtitle="Where alerts are sent, and when they fire." />
      <ChannelsSection
        channels={channels.channels}
        loading={channels.loading}
        error={channels.error}
        onChanged={channels.refresh}
      />
      <RulesSection channels={channels.channels} />
    </div>
  );
}
```

Check nothing else imports the old names:

```bash
grep -rnE "alerts/(rules|channels)-tab|RulesTab|ChannelsTab" web/src --include=*.ts --include=*.tsx
```

Expected: no output.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/alerts.test.ts src/components/alerts/ src/pages/settings-notifications.test.tsx`
Expected: PASS — including `target-alerts-card.test.tsx` (the override dialog keeps "For (minutes)", "Above (…)", "Specific channels", "Reset to rule", "Save").

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/alerts.ts web/src/lib/alerts.test.ts web/src/components/alerts/channel-picker.tsx web/src/components/alerts/rule-dialog.tsx web/src/components/alerts/rule-dialog.test.tsx web/src/components/alerts/override-dialog.tsx web/src/components/alerts/rules-section.tsx web/src/components/alerts/rules-section.test.tsx web/src/pages/settings-notifications.tsx web/src/pages/settings-notifications.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): alert rules as rows with a sentence-style rule dialog sharing the page's channels

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 12: Account → Password and API tokens

**Files:**
- Rewrite: `web/src/pages/user-password.tsx`; create `web/src/pages/user-password.test.tsx`
- Create: `web/src/lib/tokens.ts`, `web/src/lib/tokens.test.ts`
- Rewrite: `web/src/pages/user-tokens.tsx`; create `web/src/pages/user-tokens.test.tsx`

**Interfaces:**
- Consumes: Task 1 (`ConfirmDialog`, `ResponsiveDialog`, `RemoveButton`, `ListPlaceholder`, `CopyField`); phase 1 `PageHeader`, `RowList`, `Row`, `EmptyState`; `useProfile()` (`profile.must_change_password`, `refresh`), `useTokens()` (`tokens`, `loading`, `error`, `refresh`, `create(name, days)`, `remove(id)`), `ApiToken` (`@/hooks/use-tokens`), `PasswordInput`, `Alert`, `useNow`, `relativeTime`.
- Produces: `tokenMeta(t: { created_at: string; expires_at: string | null; last_used_at: string | null }, now: number): string` (`@/lib/tokens`); default `UserPasswordPage` and `UserTokensPage` (same modules, same routes `/api/v1/user/change-password`, `get-tokens`, `create-token`, `delete-token`).

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/tokens.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import { tokenMeta } from "@/lib/tokens";

const day = (iso: string) => new Date(iso).toLocaleDateString();
const NOW = Date.parse("2026-10-13T12:00:00Z");

describe("tokenMeta", () => {
  it("says when a token was made, when it ends and when it was last used", () => {
    expect(
      tokenMeta({ created_at: "2026-10-01T00:00:00Z", expires_at: "2027-10-01T00:00:00Z", last_used_at: "2026-10-13T11:00:00Z" }, NOW),
    ).toBe(`Created ${day("2026-10-01T00:00:00Z")} · Expires ${day("2027-10-01T00:00:00Z")} · Last used 1 hour ago`);
  });

  it("says never, and expired", () => {
    expect(tokenMeta({ created_at: "2026-10-01T00:00:00Z", expires_at: null, last_used_at: null }, NOW)).toBe(
      `Created ${day("2026-10-01T00:00:00Z")} · Never expires · Never used`,
    );
    expect(tokenMeta({ created_at: "2026-01-01T00:00:00Z", expires_at: "2026-02-01T00:00:00Z", last_used_at: null }, NOW)).toBe(
      `Created ${day("2026-01-01T00:00:00Z")} · Expired ${day("2026-02-01T00:00:00Z")} · Never used`,
    );
  });
});
```

Create `web/src/pages/user-password.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ProfileContext, type ProfileContextValue } from "@/context/profile-context-base";
import UserPasswordPage from "@/pages/user-password";
import { fakeAPI } from "@/test/fake-api";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

afterEach(() => vi.unstubAllGlobals());

function renderPage(mustChange = false) {
  const refresh = vi.fn().mockResolvedValue(undefined);
  const profile = {
    profile: { user_id: "u1", name: "Ann", email: "ann@example.test", must_change_password: mustChange },
    refresh,
  } as unknown as ProfileContextValue;
  render(
    <ProfileContext.Provider value={profile}>
      <MemoryRouter initialEntries={["/settings/password"]}>
        <Routes>
          <Route path="/settings/password" element={<UserPasswordPage />} />
          <Route path="/" element={<p>home</p>} />
        </Routes>
      </MemoryRouter>
    </ProfileContext.Provider>,
  );
  return { refresh };
}

async function fill(user: ReturnType<typeof userEvent.setup>, current: string, next: string, confirm: string) {
  await user.type(screen.getByLabelText("Current password"), current);
  await user.type(screen.getByLabelText("New password"), next);
  await user.type(screen.getByLabelText("Confirm new password"), confirm);
  await user.click(screen.getByRole("button", { name: "Change password" }));
}

describe("UserPasswordPage", () => {
  it("changes the password and goes home", async () => {
    const api = fakeAPI({ "/api/v1/user/change-password": null });
    const user = userEvent.setup({ delay: null });
    const { refresh } = renderPage();
    expect(screen.getByRole("heading", { level: 1, name: "Password" })).toBeInTheDocument();
    await fill(user, "old-pass", "new-pass", "new-pass");
    expect(api.posted("/api/v1/user/change-password")).toEqual([{ current_password: "old-pass", new_password: "new-pass" }]);
    expect(await screen.findByText("home")).toBeInTheDocument();
    expect(refresh).toHaveBeenCalled();
  });

  it("says under the field what is wrong, without sending", async () => {
    const api = fakeAPI({});
    const user = userEvent.setup({ delay: null });
    renderPage();
    await fill(user, "old-pass", "new-pass", "other");
    expect(screen.getByRole("alert")).toHaveTextContent("The two new passwords don't match.");
    expect(screen.getByLabelText("Confirm new password")).toHaveAttribute("aria-invalid", "true");
    expect(api.calls).toHaveLength(0);
  });

  it("shows a refusal on the page", async () => {
    fakeAPI({ "/api/v1/user/change-password": new Error("current password is wrong") });
    const user = userEvent.setup({ delay: null });
    renderPage();
    await fill(user, "bad", "new-pass", "new-pass");
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("current password is wrong"));
  });

  it("explains a temporary password", () => {
    fakeAPI({});
    renderPage(true);
    expect(screen.getByText(/You're using a temporary password/)).toBeInTheDocument();
  });
});
```

Create `web/src/pages/user-tokens.test.tsx`:

```tsx
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import UserTokensPage from "@/pages/user-tokens";
import { fakeAPI } from "@/test/fake-api";

const toastError = vi.hoisted(() => vi.fn());
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: toastError } }));

const TOKEN = {
  token_id: "t1",
  name: "CI deploy",
  prefix: "wfp_ab12",
  expires_at: null,
  last_used_at: null,
  created_at: "2026-10-01T00:00:00Z",
};

describe("UserTokensPage", () => {
  beforeEach(() => {
    toastError.mockReset();
    vi.spyOn(window, "confirm");
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("lists tokens as rows and revokes one after a confirmation, never window.confirm", async () => {
    const api = fakeAPI({ "/api/v1/user/get-tokens": [TOKEN], "/api/v1/user/delete-token": null });
    const user = userEvent.setup({ delay: null });
    render(<UserTokensPage />);
    expect(screen.getByRole("heading", { level: 1, name: "API tokens" })).toBeInTheDocument();
    expect(await screen.findByText("CI deploy")).toBeInTheDocument();
    expect(screen.getByText("wfp_ab12…")).toBeInTheDocument();
    expect(screen.getByText(/Never expires · Never used/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Revoke CI deploy" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(confirm).toHaveTextContent("Anything that uses it stops working at once.");
    await user.click(within(confirm).getByRole("button", { name: "Revoke token" }));
    await waitFor(() => expect(api.posted("/api/v1/user/delete-token")).toEqual([{ token_id: "t1" }]));
    expect(window.confirm).not.toHaveBeenCalled();
  });

  it("keeps the confirmation when a revoke fails", async () => {
    fakeAPI({ "/api/v1/user/get-tokens": [TOKEN], "/api/v1/user/delete-token": new Error("token not found") });
    const user = userEvent.setup({ delay: null });
    render(<UserTokensPage />);
    await user.click(await screen.findByRole("button", { name: "Revoke CI deploy" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Revoke token" }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith("Couldn't revoke the token", { description: "token not found" }));
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
  });

  it("generates a token with an expiry and shows it once with Copy", async () => {
    const api = fakeAPI({
      "/api/v1/user/get-tokens": [],
      "/api/v1/user/create-token": { token_id: "t2", token: "wfp_SECRET", prefix: "wfp_SE", name: "Backup script", expires_at: null },
    });
    const user = userEvent.setup({ delay: null });
    render(<UserTokensPage />);
    expect(await screen.findByText(/No tokens yet/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Generate token/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Generate" }));
    expect(within(dialog).getByRole("alert")).toHaveTextContent("Give the token a name.");
    await user.type(within(dialog).getByLabelText("Name"), "Backup script");
    await user.click(within(dialog).getByRole("radio", { name: "1 year" }));
    await user.click(within(dialog).getByRole("button", { name: "Generate" }));
    expect(await screen.findByLabelText("Token")).toHaveValue("wfp_SECRET");
    expect(screen.getByRole("button", { name: "Copy token" })).toBeInTheDocument();
    expect(api.posted("/api/v1/user/create-token")).toEqual([{ name: "Backup script", expires_in_days: 365 }]);
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/tokens.test.ts src/pages/user-password.test.tsx src/pages/user-tokens.test.tsx`
Expected: FAIL — `@/lib/tokens` does not exist; the pages use cards, toasts for form errors and `window.confirm`.

- [ ] **Step 3: Implement**

Create `web/src/lib/tokens.ts`:

```ts
import { relativeTime } from "@/lib/utils";

const date = (iso: string) => new Date(iso).toLocaleDateString();

// tokenMeta is a token row's quiet line: when it was made, when it ends
// (or that it doesn't, or did), and when it was last used.
export function tokenMeta(
  t: { created_at: string; expires_at: string | null; last_used_at: string | null },
  now: number,
): string {
  const ends = !t.expires_at
    ? "Never expires"
    : Date.parse(t.expires_at) <= now
      ? `Expired ${date(t.expires_at)}`
      : `Expires ${date(t.expires_at)}`;
  const used = t.last_used_at ? `Last used ${relativeTime(new Date(t.last_used_at), now)}` : "Never used";
  return [`Created ${date(t.created_at)}`, ends, used].join(" · ");
}
```

Replace `web/src/pages/user-password.tsx` with:

```tsx
import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";

import { PageHeader } from "@/components/page-header";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { PasswordInput } from "@/components/ui/password-input";
import { apiBaseUrl } from "@/config";
import { useProfile } from "@/context/use-profile";
import { readApiError } from "@/lib/api";

type Problem = { field: "next" | "confirm" | "form"; message: string };

// UserPasswordPage is Settings → Account → Password: change the local
// account's password. Users flagged must_change_password are sent here by
// the layout until they choose their own. Problems show under the field
// they belong to.
export default function UserPasswordPage() {
  const navigate = useNavigate();
  const { profile, refresh } = useProfile();
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<Problem | null>(null);

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const current = String(form.get("current") ?? "");
    const next = String(form.get("next") ?? "");
    const confirm = String(form.get("confirm") ?? "");
    if (next.length < 4) {
      setProblem({ field: "next", message: "Use at least 4 characters." });
      return;
    }
    if (next !== confirm) {
      setProblem({ field: "confirm", message: "The two new passwords don't match." });
      return;
    }
    setProblem(null);
    setBusy(true);
    try {
      const res = await fetch(`${apiBaseUrl}/api/v1/user/change-password`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ current_password: current, new_password: next }),
      });
      if (!res.ok) throw await readApiError(res);
      toast.success("Password changed");
      await refresh();
      navigate("/");
    } catch (e) {
      setProblem({ field: "form", message: e instanceof Error ? e.message : "Couldn't change the password." });
    } finally {
      setBusy(false);
    }
  };

  const field = (name: "current" | "next" | "confirm", label: string) => {
    const invalid = problem?.field === name;
    return (
      <div className="space-y-2">
        <Label htmlFor={name}>{label}</Label>
        <PasswordInput
          id={name}
          name={name}
          required
          aria-invalid={invalid || undefined}
          aria-describedby={invalid ? `${name}-error` : undefined}
          toggleLabel="Show password"
          hideLabel="Hide password"
        />
        {invalid ? (
          <p id={`${name}-error`} role="alert" className="text-xs text-destructive">
            {problem?.message}
          </p>
        ) : null}
      </div>
    );
  };

  return (
    <div className="space-y-6">
      <PageHeader title="Password" subtitle="The password you sign in with." />
      {profile?.must_change_password ? (
        <Alert className="max-w-sm">
          <AlertDescription>You're using a temporary password: choose your own to continue.</AlertDescription>
        </Alert>
      ) : null}
      <form className="max-w-sm space-y-4" onSubmit={(e) => void handleSubmit(e)}>
        {field("current", "Current password")}
        {field("next", "New password")}
        {field("confirm", "Confirm new password")}
        {problem?.field === "form" ? (
          <p role="alert" className="text-sm text-destructive">
            {problem.message}
          </p>
        ) : null}
        <Button type="submit" disabled={busy}>
          {busy ? "Saving…" : "Change password"}
        </Button>
      </form>
    </div>
  );
}
```

(`PasswordInput` spreads the rest of its props onto the `<input>`: check `web/src/components/ui/password-input.tsx` passes `aria-invalid`/`aria-describedby` through — it does, as `...props`; it is vendored, do not edit it.)

Replace `web/src/pages/user-tokens.tsx` with:

```tsx
import { useState } from "react";
import { KeyRound, Plus } from "lucide-react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { CopyField } from "@/components/copy-field";
import { EmptyState } from "@/components/empty-state";
import { ListPlaceholder } from "@/components/list-placeholder";
import { PageHeader } from "@/components/page-header";
import { RemoveButton } from "@/components/remove-button";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { Row, RowList } from "@/components/section";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useNow } from "@/hooks/use-now";
import { useTokens, type ApiToken, type CreatedToken } from "@/hooks/use-tokens";
import { tokenMeta } from "@/lib/tokens";
import { cn } from "@/lib/utils";

const EXPIRY_OPTIONS = [
  { label: "30 days", days: 30 },
  { label: "90 days", days: 90 },
  { label: "1 year", days: 365 },
  { label: "No expiry", days: 0 },
];

const errorText = (e: unknown) => (e instanceof Error ? e.message : undefined);

// GenerateTokenDialog names a new token and its expiry, then shows the
// token once (only its hash is kept) with Copy.
function GenerateTokenDialog({
  open,
  onOpenChange,
  create,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  create: (name: string, days: number) => Promise<CreatedToken>;
}) {
  const [name, setName] = useState("");
  const [days, setDays] = useState(30);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [created, setCreated] = useState<CreatedToken | null>(null);

  // The token must not outlive the dialog.
  const changeOpen = (next: boolean) => {
    if (!next) {
      setName("");
      setDays(30);
      setError("");
      setCreated(null);
    }
    onOpenChange(next);
  };

  const generate = async () => {
    if (!name.trim()) {
      setError("Give the token a name.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      setCreated(await create(name.trim(), days));
    } catch (e) {
      setError(errorText(e) ?? "Couldn't generate the token.");
    } finally {
      setBusy(false);
    }
  };

  if (created) {
    return (
      <ResponsiveDialog
        open={open}
        onOpenChange={changeOpen}
        title="Token generated"
        description="Copy it now: you won't be able to see it again."
        footer={<Button onClick={() => changeOpen(false)}>Done</Button>}
      >
        <CopyField value={created.token} label="Token" />
      </ResponsiveDialog>
    );
  }

  return (
    <ResponsiveDialog
      open={open}
      onOpenChange={changeOpen}
      title="Generate token"
      description="It can do everything your account can."
      footer={
        <>
          <Button type="button" variant="outline" disabled={busy} onClick={() => changeOpen(false)}>
            Cancel
          </Button>
          <Button type="submit" form="token-form" disabled={busy}>
            {busy ? "Generating…" : "Generate"}
          </Button>
        </>
      }
    >
      <form
        id="token-form"
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          void generate();
        }}
      >
        <div className="space-y-2">
          <Label htmlFor="token-name">Name</Label>
          <Input
            id="token-name"
            placeholder="e.g. CI deploy"
            value={name}
            maxLength={64}
            onChange={(e) => setName(e.target.value)}
          />
        </div>
        <div className="space-y-2">
          <Label id="token-expiry-label">Expires</Label>
          <div role="radiogroup" aria-labelledby="token-expiry-label" className="flex flex-wrap gap-2">
            {EXPIRY_OPTIONS.map((o) => (
              <button
                key={o.days}
                type="button"
                role="radio"
                aria-checked={days === o.days}
                onClick={() => setDays(o.days)}
                className={cn(
                  "min-h-10 rounded-md border px-3 text-sm transition-colors duration-150 hover:border-primary/40 sm:min-h-8",
                  days === o.days && "border-primary bg-primary/5 font-medium",
                )}
              >
                {o.label}
              </button>
            ))}
          </div>
        </div>
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : null}
      </form>
    </ResponsiveDialog>
  );
}

// UserTokensPage is Settings → Account → API tokens: personal access
// tokens for scripts and CI (sent as "Authorization: Bearer wfp_…", or as
// the Basic-auth password); generate one, or revoke one after a
// confirmation.
export default function UserTokensPage() {
  const { tokens, loading, error, create, remove, refresh } = useTokens();
  const [generating, setGenerating] = useState(false);
  const [toRevoke, setToRevoke] = useState<ApiToken | null>(null);
  const now = useNow();

  const revoke = async (t: ApiToken) => {
    try {
      await remove(t.token_id);
      toast.success("Token revoked");
    } catch (e) {
      toast.error("Couldn't revoke the token", { description: errorText(e) });
      throw e;
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="API tokens"
        subtitle={
          <>
            Let scripts and CI call the API as you: send a token as{" "}
            <code className="font-mono">Authorization: Bearer wfp_…</code>.
          </>
        }
        actions={
          <Button onClick={() => setGenerating(true)}>
            <Plus className="size-4" /> Generate token
          </Button>
        }
      />
      {loading && tokens.length === 0 ? (
        <ListPlaceholder loading failed="" />
      ) : error ? (
        <ListPlaceholder loading={false} failed={error} onRetry={() => void refresh()} />
      ) : tokens.length === 0 ? (
        <EmptyState icon={KeyRound} title="No tokens yet. Generate one to call the API from a script." />
      ) : (
        <RowList label="API tokens">
          {tokens.map((t) => (
            <Row
              key={t.token_id}
              title={t.name}
              description={<span className="font-mono">{t.prefix}…</span>}
              meta={tokenMeta(t, now)}
              actions={<RemoveButton label={`Revoke ${t.name}`} onClick={() => setToRevoke(t)} />}
            />
          ))}
        </RowList>
      )}
      <GenerateTokenDialog open={generating} onOpenChange={setGenerating} create={create} />
      <ConfirmDialog
        open={toRevoke !== null}
        onOpenChange={(o) => !o && setToRevoke(null)}
        title={`Revoke ${toRevoke?.name ?? "this token"}?`}
        description="Anything that uses it stops working at once."
        confirmLabel="Revoke token"
        pendingLabel="Revoking…"
        onConfirm={() => (toRevoke ? revoke(toRevoke) : undefined)}
      />
    </div>
  );
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/tokens.test.ts src/pages/user-password.test.tsx src/pages/user-tokens.test.tsx src/routes.test.tsx src/layouts/app-layout.test.tsx`
Expected: PASS (the forced-password guard still lands on this page).

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/tokens.ts web/src/lib/tokens.test.ts web/src/pages/user-password.tsx web/src/pages/user-password.test.tsx web/src/pages/user-tokens.tsx web/src/pages/user-tokens.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): Account Password and API tokens pages with inline errors and confirmed revoke

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 13: Phase verification

**Files:** none changed unless a check fails (then fix in the owning task's files and commit as `fix(web): …` with the `Co-Authored-By` line).

- [ ] **Step 1: The whole web suite, lint, types, build**

```bash
pnpm --dir web run lint
pnpm --dir web exec tsc -b
pnpm --dir web run test:coverage
pnpm --dir web run build && git checkout -- web/dist/.gitkeep
```

Expected: all pass; `git status --short web/dist` prints nothing.

- [ ] **Step 2: The rules of this phase hold in the code**

```bash
grep -rn "window.confirm" web/src --include=*.ts --include=*.tsx
grep -rnE "(bg|text|border)-(emerald|amber|red)-[0-9]" web/src/pages/server-settings.tsx web/src/pages/settings-*.tsx web/src/pages/org-members.tsx web/src/pages/user-*.tsx web/src/components/domain-dialog.tsx web/src/components/storage-dialog.tsx web/src/components/orphans-dialog.tsx web/src/components/shared-variable-dialog.tsx web/src/components/alerts/channel*.tsx web/src/components/alerts/rule*.tsx web/src/components/alerts/override-dialog.tsx
grep -rnE '"(Shoutrrr URL|History only)"|\bOrphaned\b|\bResync\b' web/src --include=*.ts --include=*.tsx | grep -v "\.test\."
grep -rn "from \"@/components/ui/dialog\"" web/src/pages/server-settings.tsx web/src/pages/settings-*.tsx web/src/pages/org-members.tsx web/src/pages/user-*.tsx web/src/components/domain-dialog.tsx web/src/components/storage-dialog.tsx web/src/components/orphans-dialog.tsx web/src/components/shared-variable-dialog.tsx web/src/components/alerts/channel-dialog.tsx web/src/components/alerts/rule-dialog.tsx web/src/components/alerts/override-dialog.tsx
```

Expected: no output from any of the four (every confirm is a `ConfirmDialog`, status colours are tokens, the old words are gone from the settings UI, every settings dialog is a `ResponsiveDialog`).

- [ ] **Step 3: Diff coverage of the changed web lines**

```bash
go run ./tools/diffcov --base main --min 80 --lcov web/coverage/lcov.info
```

Expected: web changed lines ≥ 80% covered. If a file falls short, add the missing test to that task's test file (the uncovered lines are listed), commit, re-run.

- [ ] **Step 4: Nothing outside `web/` and `docs/` changed**

```bash
git diff --stat main -- . ':!web' ':!docs'
```

Expected: no output (no Go, no `api/openapi.yaml`).

- [ ] **Step 5: Browser check on a throwaway standalone instance**

Build and run a standalone binary against a scratch data directory (`make build`, then `bin/standalone` with `DATABASE_URL=sqlite://…` and `AGENT_DATA_DIR` in a temp dir, as `.env.example` describes), sign in, and check every Settings page at 1280, 768 and 375 px, light and dark:
- Server → General shows name, status pill, IP/CPU/memory/disk and "Running v…"; Update stays off until a version is typed.
- Server → Domains (with `INGRESS_ENABLED=true`): rows with "Automatic HTTPS"/"Own certificate"/"HTTPS off"; the dialog's three plain choices; Remove asks first; on a phone the dialog is a bottom sheet.
- Server → Storages: the type-first dialog, S3's Region/Prefix/Path-style under Advanced, inline Test; "Backups of deleted apps" from a row's menu, an app id only in a tooltip.
- Server → Docker: three sections; registry, network and volume deletes all ask first; the anonymous switch.
- Organization → General edits in place (no id shown); Members rows with "(you)", the ⋯ menu, Add member → temporary password + Copy.
- Shared variables: one list with "All servers" / "{server} only"; no "Applies to" in standalone; built-ins folded; the banners appear only when relevant (stop the agent's sync to see "Sync … failed", if practical).
- Notifications: the service grid with "Other (URL)"; Email's port/username under Advanced; a rule dialog that reads as a sentence, with Cancel.
- Password and API tokens: inline errors; Revoke asks first.
- Rows stack as cards at 375 px and every tap target is at least 40 px.

Take screenshots at the three widths for the PR description. Stop the instance and delete its scratch directory.

- [ ] **Step 6: Commit any fixes**

```bash
git status --short
```

Expected: clean (or only the fixes, committed with `fix(web): …` and the `Co-Authored-By` line).
