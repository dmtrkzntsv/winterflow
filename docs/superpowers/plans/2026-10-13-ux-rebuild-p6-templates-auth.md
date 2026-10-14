# UX rebuild, phase 6: templates, sign-in, adding a server — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild the Templates list and template page (cards, `PageHeader` with a back link, tabs Editor · Apps using it · Versions, "Version N" wording, parameter rows with an expandable details area, "Update all apps" with the rollout strip, delete through `ConfirmDialog`), send a fresh instance's `/login` to a "Set up Winterflow" screen with the sign-in pages sharing one frame, and rebuild the Add server dialog as a two-step `ResponsiveDialog` without the mock organizations.

**Architecture:** Pages keep their data flow (the templates and rollouts API clients, `useRollout`, `useTemplatesApi`); only presentation and wording change, on the phase-1 primitives (`PageHeader`, `UnderlineTabs` + `useTabParam`, `ConfirmDialog`, `ResponsiveDialog`, `EmptyState`, `RowList`, `StatusPill`, `Disclosure`). Two small helpers are new and tested directly: `lib/auth-state.ts` (reads `GET /api/v1/auth/state`, whose `bootstrap` field already says "fresh, unclaimed instance") and `lib/clipboard.ts` (copy that works on plain-http LAN origins). The sign-in pages share `components/auth-screen.tsx`.

**Tech Stack:** React 19, React Router 7, TypeScript 5.9 (strict, `verbatimModuleSyntax`, `erasableSyntaxOnly`), Tailwind 4, vendored shadcn/ui (Radix, vaul), `input-otp` 1.4, Vitest 5 + Testing Library + user-event 14 on jsdom. No new dependency, no Go change, no API change.

**Spec:** `docs/superpowers/specs/2026-10-13-ux-rebuild-design.md` — this plan is phase 6 of §8 ("Templates, sign-in, add server"): §7 in full, under §1's principles (plain words, one primary action, `ConfirmDialog` for every destructive confirm, real router links, mobile friendly). It builds on phase 1 (`docs/superpowers/plans/2026-10-13-ux-rebuild-p1-shell.md`, "Interfaces for later phases"); phases 2–5 have run before it and touch other files.

**Base:** branch `feat/ux-rebuild` after phases 1–5. All commands run from the worktree root.

## Global Constraints

- The spec is binding. Frontend only: no Go change, no API change, `api/openapi.yaml` untouched. No feature removed: everything reachable before stays reachable.
- Never delete anything under `web/src/components/ui/` (vendored shadcn/ui). `web/src/components/ui/code-input.tsx` is this project's own wrapper (only `server-creation-dialog.tsx` uses it); Task 7 edits it in place — it is not deleted.
- Keep the Frost palette, Inter, the sky wash and the snowflake logo (`web/src/components/app-logo.tsx` is not edited).
- Plain words in every string this phase touches: "Version N" (never "rN" or "Revision N") in template UI, the upgrade dialog, instance rows and the template rollout lines; "Update" / "Update all apps" for moving apps to a newer template version; "Detach" for unlink; no Docker or API jargon in copy.
- One primary (filled) button per screen area; destructive confirms only through `ConfirmDialog` (`@/components/confirm-dialog`); no `window.confirm`; real router links (`<Link>`, `navigate`), no `href="#"`.
- Type scale: page title 24/600 (`PageHeader`), section title 15/600 (`text-[15px] font-semibold`), body 14 (`text-sm`); 4/8 spacing; transitions `duration-150` with `motion-reduce:transition-none`.
- Status colours only through tokens (`StatusPill`, `StatusDot`, `bg-status-*`, `text-status-*`); no new hard-coded status colours.
- Mobile: tap targets ≥ 40px where this phase adds controls (`min-h-10`, `size-10`); dialogs are `ResponsiveDialog` (bottom sheet below 640px); jsdom has no `matchMedia`, so components render their desktop form in tests unless a test calls `stubViewport` from `@/test/media`.
- Lint (`eslint-plugin-react-hooks` 7, `react-refresh`): no impure calls during render beyond what the files already do (`relativeTime` is existing practice); files exporting components export only components.
- TypeScript: `import type` for types, no `enum`, no unused locals/imports (remove an import when a step removes its last use).
- Tests: Vitest + Testing Library beside the code; answer fetches with `fakeAPI` from `@/test/fake-api`; mock `sonner` with a hoisted `toast` the way existing tests do; keep existing `data-testid`s where the element survives; update every existing test a change breaks (the tasks list them).
- Coverage: changed web lines ≥ 80% (`tools/diffcov`); `*.test.tsx`, `web/src/test/`, `web/src/main.tsx` and `web/src/components/ui/` are exempt.
- `pnpm --dir web run build` empties `web/dist`: run `git checkout -- web/dist/.gitkeep` after any build, before committing.
- Per task, before its commit: the task's Vitest files, `pnpm --dir web run lint`, and `pnpm --dir web exec tsc -b` pass.
- Commits: Conventional Commits (`feat(web): …`, `refactor(web): …`), each message ending with the line `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Old template links that name a tab by its old name** (`/templates/t1?tab=instances` and `?tab=revisions` from bookmarks, and the phase-1 redirect of `/org/templates/t1?tab=…`, which keeps the query). Expected: they open "Apps using it" and "Versions" (the URL rewritten to `?tab=apps` / `?tab=versions`), never a silent fallback to Editor. Pinned in Task 5 ("opens old tab links on their new tabs").
2. **Adding a server from a browser on plain http** (`http://192.168.1.10:8080` — the usual home setup, where `navigator.clipboard` does not exist), and a browser that refuses the copy outright. Expected: Copy still works through the selection fallback; when even that is refused, the dialog says to copy the command by hand and the command is selectable in one tap — never an uncaught error. Pinned in Task 7 (`clipboard.test.ts` fallback/refusal, dialog "says when copying isn't possible").
3. **Typing the pairing code on a phone, or pasting it in lowercase** (codes are `A–Z0–9`, `pkg/util.GenerateRandomCode`; the old input asked phones for a numeric keypad). Expected: a text keyboard with capitals, letters accepted, the code uppercased before sending; a wrong or expired code is explained in plain words and kept for correction. Pinned in Task 7 (dialog tests "adds the server", "explains a wrong code").
4. **Sign-in when the instance's state can't be read, or is already claimed.** Expected: `/login` stays usable (no redirect, no blank page, no sign-up link it can't vouch for); only `bootstrap: true` sends `/login` to the setup screen, and the setup screen offers no "Log in" link (no account exists yet). Pinned in Task 6 (`login.test.tsx`, `register.test.tsx`).
5. **Deleting a template that an app started using meanwhile** (the API answers 409). Expected: the confirm dialog stays open with the reason in a toast, nothing navigates; while apps use the template the menu item is disabled and says why. Pinned in Task 5 ("explains a refused delete", "offers Delete only while no app uses the template").

## File map

Created:

| File | Responsibility |
|---|---|
| `web/src/lib/auth-state.ts` (+ test) | `fetchAuthState()` — `{ bootstrap, registrationEnabled }` from `GET /api/v1/auth/state` |
| `web/src/components/auth-screen.tsx` (+ test) | `AuthScreen` (the sign-in frame: card, illustration hidden on phones, terms) and `AuthHeading` |
| `web/src/pages/login.test.tsx`, `web/src/pages/register.test.tsx` | sign-in page behaviour |
| `web/src/lib/clipboard.ts` (+ test) | `copyText()` with the plain-http fallback |
| `web/src/components/server-creation-dialog.test.tsx` | Add server dialog behaviour |

Modified: `web/src/lib/templates.ts` (+ test), `web/src/pages/org-templates.tsx` (+ test), `web/src/lib/rollouts.ts` (+ test), `web/src/components/rollout-strip.test.tsx`, `web/src/components/template-revisions.tsx` (+ test), `web/src/components/upgrade-instance-dialog.tsx`, `web/src/components/template-instances.tsx` (+ test), `web/src/components/template-editor.tsx` (+ test), `web/src/pages/template-page.tsx` (+ test), `web/src/components/login-form.tsx`, `web/src/pages/login.tsx`, `web/src/pages/register.tsx`, `web/src/components/server-creation-dialog.tsx`, `web/src/components/ui/code-input.tsx`.

Deleted: `web/src/components/create-organization-modal.tsx` (its only user was the mock organization picker).

Not in this phase: the app page's own template chip and "Update available" button and the instance panel on the Configuration tab (phases 2–3; Task 3 only fixes the dialog wording their tests assert), the new-app template form's version picker (phase 3), Settings pages (phase 5).

---

### Task 1: Templates list as cards

**Files:**
- Modify: `web/src/lib/templates.ts` (replace `instancesLabel`)
- Modify: `web/src/lib/templates.test.ts`
- Modify (rewrite): `web/src/pages/org-templates.tsx`
- Modify (rewrite): `web/src/pages/org-templates.test.tsx`

**Interfaces:**
- Consumes: `PageHeader` (`@/components/page-header`), `EmptyState` (`@/components/empty-state`), `NEW_TEMPLATE_PATH`, `templatePath` (`@/lib/routes`), `AppIcon` (`@/components/app-icon`), `relativeTime` (`@/lib/utils`).
- Produces: `usageLabel(t: Pick<TemplateSummary, "instances" | "behind">): string` (`@/lib/templates`) — "Not used by any app yet" / "Used by 1 app" / "Used by 12 apps · 3 on an older version". `instancesLabel` no longer exists.

Note: `TemplateSummary` (the list endpoint) carries no icon or colour, and the API may not change; the card's icon is the `AppIcon` initial tile of the template's name.

- [ ] **Step 1: Write the failing tests**

In `web/src/lib/templates.test.ts` change the import `instancesLabel,` to `usageLabel,` and replace the `describe("instancesLabel", …)` block with:

```ts
describe("usageLabel", () => {
  it("says how many apps use a template, and how many are on an older version", () => {
    expect(usageLabel({ instances: 0, behind: 0 })).toBe("Not used by any app yet");
    expect(usageLabel({ instances: 1, behind: 0 })).toBe("Used by 1 app");
    expect(usageLabel({ instances: 12, behind: 3 })).toBe("Used by 12 apps · 3 on an older version");
  });
});
```

Replace the whole of `web/src/pages/org-templates.test.tsx` with:

```tsx
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import OrgTemplatesPage from "@/pages/org-templates";
import { fakeAPI } from "@/test/fake-api";

const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.fn() } as NotificationsContextValue;

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/templates"]}>
      <NotificationsContext.Provider value={notifications}>
        <Routes>
          <Route path="/templates" element={<OrgTemplatesPage />} />
          <Route path="/templates/new" element={<p>new template</p>} />
          <Route path="/templates/:id" element={<p>template page</p>} />
        </Routes>
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("OrgTemplatesPage", () => {
  it("shows each template as a card: how many apps use it and when it changed", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2026-10-14T10:00:00Z"));
    const user = userEvent.setup();
    fakeAPI({
      "/api/v1/template/get-templates": {
        templates: [{ id: "t1", name: "twillingate", latest_revision: 5, instances: 12, behind: 3, updated_at: "2026-10-12T10:00:00Z" }],
      },
    });
    renderPage();
    expect(screen.getByRole("heading", { level: 1, name: "Templates" })).toBeInTheDocument();
    const card = await screen.findByRole("link", { name: /twillingate/ });
    expect(card).toHaveAttribute("href", "/templates/t1");
    expect(within(card).getByText("Used by 12 apps · 3 on an older version")).toBeInTheDocument();
    expect(within(card).getByText("Updated 2 days ago")).toBeInTheDocument();
    expect(screen.queryByText("r5")).not.toBeInTheDocument();
    await user.click(card);
    expect(await screen.findByText("template page")).toBeInTheDocument();
  });

  it("offers New template as a link, and says what to do when there is none", async () => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/template/get-templates": { templates: null } });
    renderPage();
    expect(await screen.findByText(/No templates yet/)).toBeInTheDocument();
    const add = screen.getByRole("link", { name: /New template/ });
    expect(add).toHaveAttribute("href", "/templates/new");
    await user.click(add);
    expect(await screen.findByText("new template")).toBeInTheDocument();
  });

  it("shows why the list failed", async () => {
    fakeAPI({ "/api/v1/template/get-templates": new Error("database down") });
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("database down");
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/templates.test.ts src/pages/org-templates.test.tsx`
Expected: FAIL — `usageLabel` is not exported; the page still renders "r5" and an "Add template" button.

- [ ] **Step 3: Implement**

In `web/src/lib/templates.ts` replace the `instancesLabel` comment and function with:

```ts
// usageLabel says how many apps use a template, in plain words: "Used by
// 12 apps · 3 on an older version" ("Not used by any app yet" without any).
export function usageLabel(t: Pick<TemplateSummary, "instances" | "behind">): string {
  if (t.instances === 0) return "Not used by any app yet";
  const used = `Used by ${t.instances} ${t.instances === 1 ? "app" : "apps"}`;
  return t.behind > 0 ? `${used} · ${t.behind} on an older version` : used;
}
```

Replace the whole of `web/src/pages/org-templates.tsx` with:

```tsx
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { LayoutTemplate, Plus } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { AppIcon } from "@/components/app-icon";
import { EmptyState } from "@/components/empty-state";
import { InlineError } from "@/components/inline-error";
import { PageHeader } from "@/components/page-header";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { NEW_TEMPLATE_PATH, templatePath } from "@/lib/routes";
import { usageLabel, type TemplateSummary } from "@/lib/templates";
import { relativeTime } from "@/lib/utils";

// OrgTemplatesPage is /templates: the organization's templates as cards
// (how many apps use each, when it last changed) and New template.
export default function OrgTemplatesPage() {
  const api = useTemplatesApi();
  const [list, setList] = useState<TemplateSummary[] | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api.listTemplates().then(setList, (e: unknown) => setError(e instanceof Error ? e.message : "Failed to load templates"));
  }, [api]);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Templates"
        subtitle="Define an app once, then add it to any server with its own settings."
        actions={
          <Button asChild>
            <Link to={NEW_TEMPLATE_PATH}>
              <Plus className="size-4" /> New template
            </Link>
          </Button>
        }
      />
      {error ? (
        <InlineError>{error}</InlineError>
      ) : list === null ? (
        <div className="flex h-24 items-center justify-center">
          <Spinner />
        </div>
      ) : list.length === 0 ? (
        <EmptyState
          icon={LayoutTemplate}
          title="No templates yet. Create one here, or open an app's Settings and save it as a template."
        />
      ) : (
        <ul aria-label="Templates" className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {list.map((t) => (
            <li key={t.id}>
              <Link
                to={templatePath(t.id)}
                className="flex h-full items-start gap-3 rounded-xl border bg-card p-4 transition-colors duration-150 hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring motion-reduce:transition-none"
              >
                <AppIcon name={t.name} className="size-10 shrink-0 text-base" />
                <div className="min-w-0 space-y-1">
                  <p className="truncate text-[15px] font-semibold">{t.name}</p>
                  <p className="text-sm text-muted-foreground">{usageLabel(t)}</p>
                  <p className="text-xs text-muted-foreground">
                    <time dateTime={t.updated_at}>Updated {relativeTime(new Date(t.updated_at))}</time>
                  </p>
                </div>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/templates.test.ts src/pages/org-templates.test.tsx`
Expected: PASS

Run: `grep -rn "instancesLabel" web/src`
Expected: no output.

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/templates.ts web/src/lib/templates.test.ts web/src/pages/org-templates.tsx web/src/pages/org-templates.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): templates list as cards with usage and last change

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: "Version N" in the rollout lines and the Versions list

**Files:**
- Modify: `web/src/lib/rollouts.ts` (`rolloutLine`, `failureLine`, `skippedLine` for `template_upgrade`)
- Modify: `web/src/lib/rollouts.test.ts`, `web/src/components/rollout-strip.test.tsx`
- Modify: `web/src/components/template-revisions.tsx`
- Modify: `web/src/components/template-revisions.test.tsx`

**Interfaces:**
- Consumes: nothing new.
- Produces: same exports, new wording. Template rollout lines read "Updating 3 of 12: bakomi…", "Update to version 3 queued…", "Update to version 3 stopped at bakomi", "Update to version 3 failed", "Updated 2 apps to version 5 · 1 skipped: it needs a new value — update it on its own." (reload lines unchanged). `TemplateRevisions` rows read "Version 3", buttons "Changes from version 2", the first row "First version".

- [ ] **Step 1: Update the tests to the new wording (they fail)**

In `web/src/lib/rollouts.test.ts` and `web/src/components/rollout-strip.test.tsx`, replace these expected strings (every occurrence):

| Old | New |
|---|---|
| `"Upgrading 3 of 12: bakomi…"` | `"Updating 3 of 12: bakomi…"` |
| `"Upgrade to r3 queued…"` | `"Update to version 3 queued…"` |
| `"Upgrading 3 of 12: a1b2c3d4 (stopping after this one)"` | `"Updating 3 of 12: a1b2c3d4 (stopping after this one)"` |
| `"Upgrade to r3 stopped at bakomi"` | `"Update to version 3 stopped at bakomi"` |
| `"Upgrade to r3 failed"` | `"Update to version 3 failed"` |
| `"Upgraded 2 apps to r5 · 1 skipped: it needs a new value — upgrade it on its own."` | `"Updated 2 apps to version 5 · 1 skipped: it needs a new value — update it on its own."` |
| `"Upgraded 1 app to r5 · 3 skipped: they need a new value — upgrade them on their own."` | `"Updated 1 app to version 5 · 3 skipped: they need a new value — update them on their own."` |
| `"No app upgraded to r5 · 1 skipped: it needs a new value — upgrade it on its own."` | `"No app updated to version 5 · 1 skipped: it needs a new value — update it on its own."` |
| `"Stopped after upgrading 2 apps to r5 · 1 skipped: it needs a new value — upgrade it on its own."` | `"Stopped after updating 2 apps to version 5 · 1 skipped: it needs a new value — update it on its own."` |
| `"Stopped before upgrading any app to r5 · 2 skipped: they need a new value — upgrade them on their own."` | `"Stopped before updating any app to version 5 · 2 skipped: they need a new value — update them on their own."` |
| `"Stopped after upgrading 1 app to r5 · 2 skipped: they need a new value — upgrade them on their own."` | `"Stopped after updating 1 app to version 5 · 2 skipped: they need a new value — update them on their own."` |

(Test names that say "Upgrade all" may stay; only expectations change.)

In `web/src/components/template-revisions.test.tsx` replace every `"Changes from r2"` with `"Changes from version 2"`, every `"Changes from r1"` with `"Changes from version 1"`, and `"First revision"` with `"First version"`. In the first test, after `expect(screen.getByText("First revision")…` (now `"First version"`), add:

```tsx
    expect(screen.getByText("Version 3")).toBeInTheDocument();
    expect(screen.queryByText("r3")).not.toBeInTheDocument();
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/rollouts.test.ts src/components/rollout-strip.test.tsx src/components/template-revisions.test.tsx`
Expected: FAIL — the code still says "Upgrading", "r3", "First revision".

- [ ] **Step 3: Implement**

In `web/src/lib/rollouts.ts`:

1. In `rolloutLine` replace

```ts
  const verb = r.kind === "template_upgrade" ? "Upgrading" : "Reloading";
  if (r.state === "queued" || !r.current_app_id) {
    return r.kind === "template_upgrade" ? `Upgrade to r${r.revision} queued…` : "Reload queued…";
  }
```

with

```ts
  const verb = r.kind === "template_upgrade" ? "Updating" : "Reloading";
  if (r.state === "queued" || !r.current_app_id) {
    return r.kind === "template_upgrade" ? `Update to version ${r.revision} queued…` : "Reload queued…";
  }
```

and its comment's example `"Upgrading 3 of 12: bakomi…"` with `"Updating 3 of 12: bakomi…"`.

2. In `failureLine` replace `` `Upgrade to r${r.revision}` `` with `` `Update to version ${r.revision}` ``.

3. Replace the body of `skippedLine` after its first `if` with:

```ts
  const updated =
    r.state === "stopped"
      ? r.done > 0
        ? `Stopped after updating ${apps(r.done)} to version ${r.revision}`
        : `Stopped before updating any app to version ${r.revision}`
      : r.done > 0
        ? `Updated ${apps(r.done)} to version ${r.revision}`
        : `No app updated to version ${r.revision}`;
  const why = r.skipped === 1 ? "it needs a new value — update it on its own" : "they need a new value — update them on their own";
  return `${updated} · ${r.skipped} skipped: ${why}.`;
```

and its comment's example with `"Updated 2 apps to version 5 · 1 skipped: it needs a new value — update it on its own."`.

In `web/src/components/template-revisions.tsx`:

1. Change the comment above `TemplateRevisions` to:

```tsx
// TemplateRevisions is the Versions tab: a template's versions, newest
// first; each opens its changes against the version before it.
```

2. Replace `setFailed({ revision, message: e instanceof Error ? e.message : "Failed to load the revisions" });` with `setFailed({ revision, message: e instanceof Error ? e.message : "Failed to load the versions" });`.

3. Replace the row header block

```tsx
              <span className="font-medium">r{r.revision}</span>
```

with

```tsx
              <span className="font-medium">Version {r.revision}</span>
```

then `Changes from r{r.revision - 1}` with `Changes from version {r.revision - 1}`, and `First revision` with `First version`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/rollouts.test.ts src/components/rollout-strip.test.tsx src/components/template-revisions.test.tsx`
Expected: PASS

Run: `grep -nE 'Upgrading|Upgraded|to r\$\{|First revision|>r\{' web/src/lib/rollouts.ts web/src/components/template-revisions.tsx`
Expected: no output. (`template-page.test.tsx` still expects `"Upgrade to r3 queued…"` and fails until Task 5 updates it; the instances list and the update dialog change in Task 3.)

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/rollouts.ts web/src/lib/rollouts.test.ts web/src/components/rollout-strip.test.tsx web/src/components/template-revisions.tsx web/src/components/template-revisions.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): say "version N" in template rollouts and the versions list

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: The apps using a template, and the update dialog

**Files:**
- Modify (rewrite): `web/src/components/upgrade-instance-dialog.tsx` (on `ResponsiveDialog`, "Update … to version N")
- Modify (rewrite): `web/src/components/template-instances.tsx` (flat rows, `StatusPill`, "Update all apps", `ConfirmDialog`)
- Modify: `web/src/components/template-instances.test.tsx`
- Modify (only if they assert the dialog's old wording): the app page tests from phases 2–3 (`web/src/pages/app-details.test.tsx` and/or `web/src/components/instance-panel*.test.tsx`), and `web/src/components/instance-panel.tsx` line "r{instance.revision}" if it is still there

**Interfaces:**
- Consumes: `ResponsiveDialog` (`@/components/responsive-dialog`), `ConfirmDialog` (`@/components/confirm-dialog`), `EmptyState`, `RowList` (`@/components/section`), `StatusPill` (`@/components/status`), `appStatus` (`@/lib/status`), `appPath` (`@/lib/routes`), `AppIcon`.
- Produces: same component names and props — `UpgradeInstanceDialog({ instance, appName, target, current?, onClose, onDone })` (the app page keeps using it), `TemplateInstances({ view, instances, onChanged, onUpgradeAll?, rolloutActive? })`. Visible names: row button "Update", dialog title "Update {app} to version {N}", its button "Update to version {N}", toast "Updated to version {N}", the bar's button "Update all apps", its confirm "Update all".

- [ ] **Step 1: Update the tests (they fail)**

In `web/src/components/template-instances.test.tsx`:

1. Replace the first test (`"lists instances per server with revision and status"`) with:

```tsx
  it("lists the apps per server with their version and status", () => {
    renderInstances([
      inst({}),
      inst({ key: "k2", server_id: "s2", app_id: "a2", revision: 3, parameters: { NAME: "shop" }, state: "pending", last_error: "pull failed" }),
    ]);
    const rows = screen.getAllByRole("listitem");
    expect(within(rows[0]).getByRole("link", { name: "bakomi" })).toHaveAttribute("href", "/server/s1/app/a1");
    expect(rows[0]).toHaveTextContent("on alpha");
    expect(rows[0]).toHaveTextContent("Version 2 (latest is 3)");
    expect(rows[0]).not.toHaveTextContent(/\br2\b/);
    expect(within(rows[0]).getByText("Running").querySelector(".bg-status-ok")).not.toBeNull();
    expect(within(rows[1]).getByText("Not created yet").closest("[title]")).toHaveAttribute("title", "pull failed");
    expect(rows[1]).toHaveTextContent("Version 3");
    expect(within(rows[1]).queryByRole("button", { name: "Update" })).not.toBeInTheDocument();
  });
```

2. In every other test replace `{ name: "Upgrade" }` with `{ name: "Update" }`, `"Upgrade to r3"` with `"Update to version 3"`, `toast.success).toHaveBeenCalledWith("Upgraded to r3")` with `toast.success).toHaveBeenCalledWith("Updated to version 3")`, `toHaveTextContent("Upgrade bakomi to r3")` with `toHaveTextContent("Update bakomi to version 3")`, and `/Couldn't read r2 to compare its defaults: boom/` with `/Couldn't read version 2 to compare its defaults: boom/`.

3. Replace the test `"reports a failed delete of a pending instance"` with:

```tsx
  it("reports a failed delete of a pending instance and keeps the confirm open to retry", async () => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/instance/delete-instance": new Error("server offline") });
    renderInstances([inst({ state: "pending" })]);
    await user.click(screen.getByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Couldn't delete bakomi", { description: "server offline" }));
    expect(onChanged).not.toHaveBeenCalled();
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Delete" })).toBeEnabled();
  });
```

4. Replace the last test (`"upgrades every created instance behind …"`) with:

```tsx
  it("updates every created app on an older version after a confirm, not while a rollout runs", async () => {
    const user = userEvent.setup();
    const onUpgradeAll = vi.fn();
    const list = [inst({}), inst({ key: "k2", app_id: "a2", revision: 3 }), inst({ key: "k3", app_id: "a3", state: "pending", revision: 1 })];
    const tree = (active: boolean, instances = list) => (
      <MemoryRouter>
        <NotificationsContext.Provider value={notifications}>
          <TemplateInstances view={VIEW} instances={instances} onChanged={onChanged} onUpgradeAll={onUpgradeAll} rolloutActive={active} />
        </NotificationsContext.Provider>
      </MemoryRouter>
    );
    const { rerender } = render(tree(false));
    // Only k1 counts: k2 is on version 3, k3 was never created.
    expect(screen.getByText("1 app uses an older version. The latest is version 3.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Update all apps" }));
    const dialog = screen.getByRole("alertdialog");
    expect(dialog).toHaveTextContent("Update 1 app to version 3?");
    expect(dialog).toHaveTextContent(/stopped apps will be started/i);
    await user.click(within(dialog).getByRole("button", { name: "Update all" }));
    expect(onUpgradeAll).toHaveBeenCalledTimes(1);

    rerender(tree(true));
    expect(screen.getByRole("button", { name: "Update all apps" })).toBeDisabled();
    rerender(tree(false, [inst({ revision: 3 })]));
    expect(screen.queryByRole("button", { name: /Update all/ })).not.toBeInTheDocument();
  });
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/components/template-instances.test.tsx`
Expected: FAIL — the rows still say "r2 · behind", the button "Upgrade".

- [ ] **Step 3: Implement**

Replace the whole of `web/src/components/upgrade-instance-dialog.tsx` with:

```tsx
import { useEffect, useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { InlineError } from "@/components/inline-error";
import { ParameterFields } from "@/components/parameter-fields";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { AppStatusCode } from "@/context/apps-context-base";
import { defaultChanges, fieldErrors, splitValues, upgradeNeeds } from "@/lib/template-params";
import { sealSecrets, type InstanceView, type TemplateView } from "@/lib/templates";

type Props = {
  instance: InstanceView;
  // appName is the app's name as the page shows it.
  appName: string;
  // target is the version to move to (its body says what it needs).
  target: TemplateView;
  // current is the instance's own version when the caller has it read;
  // otherwise the dialog reads it (to name the defaults that change).
  current?: TemplateView | null;
  // onClose closes the dialog (the caller mounts it only while open).
  onClose: () => void;
  onDone: () => void;
};

// UpgradeInstanceDialog updates one app to another version of its
// template, asking only for the values the app doesn't hold yet (a newly
// required parameter, a pending app's secrets), and naming the parameters
// left on their default whose default the update changes.
export function UpgradeInstanceDialog({ instance, appName, target, current, onClose, onDone }: Props) {
  const api = useTemplatesApi();
  const needs = upgradeNeeds(target.body.parameters, instance);
  const given =
    current && current.id === instance.template_id && current.revision === instance.revision ? current : null;
  const [read, setRead] = useState<{ view: TemplateView | null; error: string } | null>(null);
  useEffect(() => {
    if (given) return;
    let live = true;
    api.getRevision(instance.template_id, instance.revision).then(
      (view) => live && setRead({ view, error: "" }),
      (e: unknown) => live && setRead({ view: null, error: e instanceof Error ? e.message : "the read failed" }),
    );
    return () => {
      live = false;
    };
  }, [api, given, instance.template_id, instance.revision]);
  const from = given ?? read?.view ?? null;
  const changes = from ? defaultChanges(from.body.parameters, target.body.parameters, instance) : [];
  const starts = instance.state === "created" && instance.status_code === AppStatusCode.Stopped;
  const [values, setValues] = useState<Record<string, string>>({});
  const [tried, setTried] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const upgrade = async () => {
    setTried(true);
    if (Object.keys(fieldErrors(needs, values)).length > 0) return;
    setBusy(true);
    setError("");
    try {
      const { plain, typed } = splitValues(needs, values);
      const secrets = await sealSecrets(api, instance.server_id, typed);
      await api.upgradeInstance({
        server_id: instance.server_id,
        key: instance.key,
        revision: target.revision,
        ...(Object.keys(plain).length ? { parameters: plain } : {}),
        ...(Object.keys(secrets).length ? { secrets } : {}),
      });
      toast.success(`Updated to version ${target.revision}`);
      onClose();
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The update failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <ResponsiveDialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
      title={`Update ${appName} to version ${target.revision}`}
      description={
        <>
          The app is redeployed with {target.name} version {target.revision}; its backup settings stay. Values you set
          stay; parameters left on their default take the new default.{starts ? " The app will be started." : ""}
        </>
      }
      footer={
        <>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={busy} onClick={() => void upgrade()}>
            {busy ? "Updating…" : `Update to version ${target.revision}`}
          </Button>
        </>
      }
    >
      <div className="grid gap-4">
        {!from && !read ? (
          <p className="text-sm text-muted-foreground">Checking which defaults change…</p>
        ) : read?.error ? (
          <p className="text-sm text-muted-foreground">
            Couldn't read version {instance.revision} to compare its defaults: {read.error}
          </p>
        ) : changes.length > 0 ? (
          <ul aria-label="Defaults that change" className="grid gap-1 text-sm">
            {changes.map((c) => (
              <li key={c.name}>
                <code className="font-mono">{c.name}</code>: {c.from || "(empty)"} → {c.to || "(empty)"}{" "}
                <span className="text-muted-foreground">(follows the template default)</span>
              </li>
            ))}
          </ul>
        ) : null}
        {needs.length > 0 ? (
          <ParameterFields
            params={needs}
            values={values}
            onChange={(name, v) => setValues((prev) => ({ ...prev, [name]: v }))}
            showRequired={tried}
          />
        ) : (
          <p className="text-sm text-muted-foreground">No new values are needed.</p>
        )}
        {error ? <InlineError icon>{error}</InlineError> : null}
      </div>
    </ResponsiveDialog>
  );
}
```

Replace the whole of `web/src/components/template-instances.tsx` with:

```tsx
import { useState } from "react";
import { Link } from "react-router-dom";
import { AppWindow } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { AppIcon } from "@/components/app-icon";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { ErrorText } from "@/components/error-text";
import { RowList } from "@/components/section";
import { StatusPill } from "@/components/status";
import { UpgradeInstanceDialog } from "@/components/upgrade-instance-dialog";
import { useServers } from "@/context/use-servers";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { appPath } from "@/lib/routes";
import { appStatus } from "@/lib/status";
import { expand, plainValues } from "@/lib/template-params";
import type { InstanceView, TemplateView } from "@/lib/templates";

// instanceName is an app's name as the latest version renders it (the app
// id's prefix until it has one).
function instanceName(view: TemplateView, inst: InstanceView): string {
  return expand(view.body.app_name, plainValues(view.body.parameters, inst.parameters ?? {})) || inst.app_id.slice(0, 8);
}

// TemplateInstances is the "Apps using it" tab: the apps a template runs
// as, across the organization's servers — where, on which version, how they
// are doing, and Update for the ones on an older version (view is the
// latest). A pending app (its deploy never succeeded) shows why and offers
// Delete: no other page reaches it, and it keeps the template from being
// deleted. With onUpgradeAll, "Update all apps" (confirmed first) queues
// the update of every created app on an older version; it waits while an
// update of the template runs.
export function TemplateInstances({
  view,
  instances,
  onChanged,
  onUpgradeAll,
  rolloutActive = false,
}: {
  view: TemplateView;
  instances: InstanceView[];
  onChanged: () => void;
  onUpgradeAll?: () => void;
  rolloutActive?: boolean;
}) {
  const { servers } = useServers();
  const api = useTemplatesApi();
  const [upgrading, setUpgrading] = useState<InstanceView | null>(null);
  const [deleting, setDeleting] = useState<string | null>(null); // key
  const [confirmDelete, setConfirmDelete] = useState<InstanceView | null>(null);
  const [confirmAll, setConfirmAll] = useState(false);
  const serverName = (id: string) => servers.find((s) => s.id === id)?.name ?? id;

  // remove deletes a pending app; a failure is reported and rethrown so
  // the confirm dialog stays open for a retry.
  const remove = async (inst: InstanceView) => {
    const name = instanceName(view, inst);
    setDeleting(inst.key);
    try {
      await api.deleteInstance(inst.server_id, inst.key);
    } catch (e) {
      toast.error(`Couldn't delete ${name}`, { description: e instanceof Error ? e.message : undefined });
      throw e;
    } finally {
      setDeleting(null);
    }
    toast.success(`Deleted ${name}`);
    onChanged();
  };

  if (instances.length === 0) {
    return (
      <EmptyState
        icon={AppWindow}
        title="No app uses this template yet. Add one from a server: New app → From a template."
      />
    );
  }

  const behind = instances.filter((i) => i.state === "created" && i.revision < view.latest_revision).length;
  const latest = view.latest_revision;

  return (
    <div className="space-y-3">
      {onUpgradeAll && behind > 0 ? (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border bg-muted/30 px-4 py-3 text-sm">
          <span>
            {behind} {behind === 1 ? "app uses" : "apps use"} an older version. The latest is version {latest}.
          </span>
          <Button
            size="sm"
            disabled={rolloutActive}
            title={rolloutActive ? "An update is running" : undefined}
            onClick={() => setConfirmAll(true)}
          >
            Update all apps
          </Button>
        </div>
      ) : null}
      <RowList label="Apps using this template">
        {instances.map((inst) => {
          const name = instanceName(view, inst);
          const old = inst.revision < latest;
          const pending = inst.state === "pending";
          return (
            <li
              key={inst.key}
              className="flex flex-col gap-2 px-4 py-3 text-sm transition-colors duration-150 hover:bg-muted/40 motion-reduce:transition-none sm:flex-row sm:items-center sm:gap-4 max-sm:rounded-xl max-sm:border max-sm:bg-card"
            >
              <div className="flex min-w-0 flex-1 items-center gap-3">
                <AppIcon name={name} icon={view.body.icon} color={view.body.color} className="size-8 shrink-0 text-sm" />
                <div className="min-w-0">
                  <Link to={appPath(inst.server_id, inst.app_id)} className="font-medium hover:underline">
                    {name}
                  </Link>
                  <p className="text-xs text-muted-foreground">on {serverName(inst.server_id)}</p>
                  {pending && inst.last_error ? <ErrorText text={inst.last_error} className="text-xs" /> : null}
                </div>
              </div>
              <span className="text-xs text-muted-foreground">
                {old ? `Version ${inst.revision} (latest is ${latest})` : `Version ${inst.revision}`}
              </span>
              {pending ? (
                <span title={inst.last_error || undefined}>
                  <StatusPill status={{ tone: "warn", label: "Not created yet" }} />
                </span>
              ) : (
                <StatusPill status={appStatus(inst.status_code)} />
              )}
              {old || pending ? (
                <div className="flex items-center gap-2">
                  {old ? (
                    <Button size="sm" variant="outline" className="max-sm:min-h-10" onClick={() => setUpgrading(inst)}>
                      Update
                    </Button>
                  ) : null}
                  {pending ? (
                    <Button
                      size="sm"
                      variant="outline"
                      className="text-destructive max-sm:min-h-10"
                      disabled={deleting === inst.key}
                      onClick={() => setConfirmDelete(inst)}
                    >
                      Delete
                    </Button>
                  ) : null}
                </div>
              ) : null}
            </li>
          );
        })}
      </RowList>
      <ConfirmDialog
        open={confirmAll}
        onOpenChange={setConfirmAll}
        destructive={false}
        title={`Update ${behind} ${behind === 1 ? "app" : "apps"} to version ${latest}?`}
        description="They are updated one at a time, oldest first, each keeping its own values. Stopped apps will be started. An app that needs a new value is skipped (update it on its own); the first failure stops the rest."
        confirmLabel="Update all"
        onConfirm={() => onUpgradeAll?.()}
      />
      <ConfirmDialog
        open={confirmDelete !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmDelete(null);
        }}
        title={confirmDelete ? `Delete ${instanceName(view, confirmDelete)}?` : "Delete"}
        description={
          confirmDelete
            ? `Its deploy never succeeded. Deleting removes whatever it left on ${serverName(confirmDelete.server_id)}.`
            : undefined
        }
        confirmLabel="Delete"
        pendingLabel="Deleting…"
        onConfirm={() => (confirmDelete ? remove(confirmDelete) : undefined)}
      />
      {upgrading ? (
        <UpgradeInstanceDialog
          key={upgrading.key}
          instance={upgrading}
          appName={instanceName(view, upgrading)}
          target={view}
          onClose={() => setUpgrading(null)}
          onDone={onChanged}
        />
      ) : null}
    </div>
  );
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/components/template-instances.test.tsx`
Expected: PASS

- [ ] **Step 5: Fix the app page's assertions on the dialog, and its last "rN"**

The app page (phases 2–3) mounts `UpgradeInstanceDialog`. Find the assertions on the dialog's old wording:

```bash
grep -rnE "Upgrade [^\"]* to r[0-9]|Upgraded to r[0-9]|Couldn't read r[0-9]" web/src --include=*.test.tsx
grep -rnE "within\(dialog\)\.getByRole\(\"button\", \{ name: \"Upgrade to r[0-9]\" \}\)" web/src --include=*.test.tsx
```

In each hit **that targets the dialog** (a `within(dialog)` query, the dialog's heading, or the toast), apply: `"Upgrade to rN"` → `"Update to version N"`, `"Upgrade {App} to rN"` → `"Update {App} to version N"`, `"Upgraded to rN"` → `"Updated to version N"`, `Couldn't read rN` → `Couldn't read version N`. Leave the app page's own button that opens the dialog as phases 2–3 named it.

Then:

```bash
grep -n "r{instance.revision}" web/src/components/instance-panel.tsx
```

If it prints a line, replace `r{instance.revision}.` there with `version {instance.revision}.` (and update any test expecting the old sentence the same way).

Run: `pnpm --dir web exec vitest run src/pages src/components`
Expected: PASS, except `src/pages/template-page.test.tsx`, whose expectations of the old wording Task 5 updates.

- [ ] **Step 6: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add -A web/src/components web/src/pages
git commit -m "$(cat <<'EOF'
feat(web): apps using a template as flat rows, update dialog on ResponsiveDialog

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Template editor — parameters as rows with details

**Files:**
- Modify: `web/src/components/template-editor.tsx` (the Parameters section only)
- Modify: `web/src/components/template-editor.test.tsx`

**Interfaces:**
- Consumes: `paramRow`, `ParamRow`, `TemplateDraft` (`@/lib/template-draft`), `compilePattern` (`@/lib/template-params`).
- Produces: `TemplateEditor({ draft, onChange })` (unchanged signature). Accessible names kept: "Parameter name", "Default of {NAME}", "Pattern of {NAME}", "Description of {NAME}", "{NAME} is secret", "{NAME} is required", "Remove {NAME}"; new: the toggle button "Details of {NAME}" (`aria-expanded`). Pattern, Description, Secret and Required are rendered only while a row's details are open; a newly added parameter opens with its details shown.

- [ ] **Step 1: Write the failing tests**

In `web/src/components/template-editor.test.tsx`:

1. In the test `"edits parameters: a secret loses its default, a pattern JS can't read is flagged"`, insert after the line `expect(latest().parameters[0].default).toBe("shop2");`:

```tsx
    await user.click(screen.getByRole("button", { name: "Details of NAME" }));
```

2. Replace the test `"heads the parameter columns and uses square checkboxes"` with:

```tsx
  it("shows Name and Default, the rest behind Details", async () => {
    const user = start();
    const head = screen.getByRole("group", { name: "Parameter columns" });
    expect(within(head).getAllByText(/./).map((e) => e.textContent)).toEqual(["Name", "Default"]);
    const toggle = screen.getByRole("button", { name: "Details of NAME" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByLabelText("Pattern of NAME")).not.toBeInTheDocument();
    await user.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByLabelText("Pattern of NAME")).toBeInTheDocument();
    expect(screen.getByLabelText("Description of NAME")).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "NAME is secret" })).toHaveClass("rounded-[4px]");
    expect(screen.getByRole("checkbox", { name: "NAME is required" })).toHaveClass("rounded-[4px]");
    await user.click(toggle);
    expect(screen.queryByLabelText("Pattern of NAME")).not.toBeInTheDocument();
  });

  it("says what a closed row holds, and opens a new parameter's details", async () => {
    const draft = draftFromBody("shop", {
      files: { "compose.yaml": COMPOSE },
      parameters: [{ name: "KEY", secret: true, required: true, pattern: "^x$" }],
      app_name: "shop",
      icon: "",
      color: "",
      ingress: null,
      backups: null,
    });
    render(<Harness initial={draft} />);
    const user = userEvent.setup();
    expect(screen.getByText("Secret · Required · Pattern")).toBeInTheDocument();
    expect(screen.getByLabelText("Default of KEY")).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Details of KEY" }));
    expect(screen.queryByText("Secret · Required · Pattern")).not.toBeInTheDocument();

    await user.click(screen.getAllByRole("button", { name: /Add parameter/ })[0]);
    expect(screen.getByRole("button", { name: "Details of parameter" })).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByLabelText("Pattern of parameter")).toBeInTheDocument();
  });
```

3. Replace the test `"has placeholders that fit and say what goes there"` with:

```tsx
  it("has placeholders that fit and say what goes there", async () => {
    const user = start();
    expect(screen.getByLabelText("Template name")).toHaveAttribute("placeholder", "my-stack");
    expect(screen.getByLabelText("App name")).toHaveAttribute("placeholder", "my-app or ${NAME}");
    expect(screen.getByLabelText("Default of NAME")).toHaveAttribute("placeholder", "Default");
    await user.click(screen.getByRole("button", { name: "Details of NAME" }));
    expect(screen.getByLabelText("Pattern of NAME")).toHaveAttribute("placeholder", "e.g. ^[a-z0-9-]+$");
  });
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/components/template-editor.test.tsx`
Expected: FAIL — there is no "Details of NAME" button; the heads list six columns.

- [ ] **Step 3: Implement**

In `web/src/components/template-editor.tsx`:

1. Imports: change `import { useMemo } from "react";` to `import { useMemo, useState } from "react";` and `import { Plus, Trash2 } from "lucide-react";` to `import { ChevronDown, Plus, Trash2 } from "lucide-react";`.

2. Replace the `PARAM_GRID` constant and its comment with:

```tsx
// PARAM_COLS lays a parameter row (and the column heads) out: name,
// default and the row's two buttons on one line from sm up; on a phone the
// default goes under the name.
const PARAM_COLS = "grid-cols-[minmax(0,1fr)_4.5rem] sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_4.5rem]";

// paramFlags names the settings a closed parameter row hides.
function paramFlags(p: ParamRow): string[] {
  const flags: string[] = [];
  if (p.secret) flags.push("Secret");
  if (p.required) flags.push("Required");
  if (p.pattern !== "") flags.push("Pattern");
  return flags;
}
```

3. In `TemplateEditor`, after the `setIngress` line add:

```tsx
  // expanded holds the parameter rows showing their details; a parameter
  // just added opens with them shown.
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => new Set());
  const toggleDetails = (id: string) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (!next.delete(id)) next.add(id);
      return next;
    });
  const addParam = () => {
    const row = paramRow();
    set({ parameters: [...draft.parameters, row] });
    setExpanded((prev) => new Set(prev).add(row.id));
  };
```

4. In the Parameters `EditorSection`, change the action's `onClick={() => set({ parameters: [...draft.parameters, paramRow()] })}` to `onClick={addParam}`, and replace everything from `{draft.parameters.length === 0 ? (` to the matching `)}` that closes the section's body with:

```tsx
        {draft.parameters.length === 0 ? (
          <EmptyNote>No parameters yet.</EmptyNote>
        ) : (
          <div className="divide-y rounded-md border">
            <div
              role="group"
              aria-label="Parameter columns"
              className={cn(PARAM_COLS, "hidden gap-x-2 px-3 py-2 text-xs font-medium text-muted-foreground sm:grid")}
            >
              <span>Name</span>
              <span>Default</span>
            </div>
            {draft.parameters.map((p) => {
              const label = p.name || "parameter";
              const open = expanded.has(p.id);
              const detailsId = `param-details-${p.id}`;
              const unchecked = p.pattern !== "" && compilePattern(p.pattern) === null;
              const flags = paramFlags(p);
              return (
                <div key={p.id} className={cn(PARAM_COLS, "grid items-center gap-2 p-3")}>
                  <Input
                    aria-label="Parameter name"
                    value={p.name}
                    placeholder="NAME"
                    className="font-mono"
                    onChange={(e) => setParam(p.id, { name: e.target.value })}
                  />
                  <Input
                    aria-label={`Default of ${label}`}
                    value={p.secret ? "" : p.default}
                    disabled={p.secret}
                    placeholder={p.secret ? "No default (secret)" : "Default"}
                    className="font-mono max-sm:order-1 max-sm:col-span-2"
                    onChange={(e) => setParam(p.id, { default: e.target.value })}
                  />
                  <div className="flex items-center justify-end gap-1">
                    <Button
                      size="icon"
                      variant="ghost"
                      className="size-8 text-muted-foreground"
                      aria-label={`Details of ${label}`}
                      aria-expanded={open}
                      aria-controls={open ? detailsId : undefined}
                      onClick={() => toggleDetails(p.id)}
                    >
                      <ChevronDown
                        className={cn(
                          "size-4 transition-transform duration-150 motion-reduce:transition-none",
                          open && "rotate-180",
                        )}
                      />
                    </Button>
                    <Button
                      size="icon"
                      variant="ghost"
                      className="size-8 text-muted-foreground hover:text-destructive"
                      aria-label={`Remove ${label}`}
                      onClick={() => set({ parameters: removeById(draft.parameters, p.id) })}
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </div>
                  {!open && flags.length > 0 ? (
                    <p className="order-2 col-span-full text-xs text-muted-foreground">{flags.join(" · ")}</p>
                  ) : null}
                  {open ? (
                    <div id={detailsId} className="order-2 col-span-full grid gap-3 rounded-md bg-muted/30 p-3 sm:grid-cols-2">
                      <div className="grid gap-1.5">
                        <Label htmlFor={`${detailsId}-description`}>Description</Label>
                        <Input
                          id={`${detailsId}-description`}
                          aria-label={`Description of ${label}`}
                          value={p.description}
                          placeholder="Shown next to the field when adding an app"
                          onChange={(e) => setParam(p.id, { description: e.target.value })}
                        />
                      </div>
                      <div className="grid gap-1.5">
                        <Label htmlFor={`${detailsId}-pattern`}>Pattern</Label>
                        <Input
                          id={`${detailsId}-pattern`}
                          aria-label={`Pattern of ${label}`}
                          value={p.pattern}
                          placeholder="e.g. ^[a-z0-9-]+$"
                          className="font-mono"
                          onChange={(e) => setParam(p.id, { pattern: e.target.value })}
                        />
                      </div>
                      <div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-sm sm:col-span-2">
                        <label className="flex min-h-10 items-center gap-2">
                          <Checkbox
                            aria-label={`${label} is secret`}
                            className="rounded-[4px]"
                            checked={p.secret}
                            onCheckedChange={(c) =>
                              setParam(p.id, { secret: c === true, default: c === true ? "" : p.default })
                            }
                          />
                          Secret (encrypted for the app's server, no default)
                        </label>
                        <label className="flex min-h-10 items-center gap-2">
                          <Checkbox
                            aria-label={`${label} is required`}
                            className="rounded-[4px]"
                            checked={p.required}
                            onCheckedChange={(c) => setParam(p.id, { required: c === true })}
                          />
                          Required
                        </label>
                      </div>
                      {unchecked ? (
                        <p className="text-xs text-muted-foreground sm:col-span-2">
                          The browser can't check this pattern; values are checked when the app is saved.
                        </p>
                      ) : null}
                    </div>
                  ) : null}
                </div>
              );
            })}
          </div>
        )}
```

(`Label` and `Checkbox` stay imported; `PARAM_GRID` is gone, so nothing else refers to it.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/components/template-editor.test.tsx`
Expected: PASS. (`template-page.test.tsx` still expects the old wording of Tasks 2–3 and is brought up to date in Task 5; the parameter it adds goes through "Parameter name", which this task keeps.)

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/components/template-editor.tsx web/src/components/template-editor.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): template parameters as Name + Default rows with expandable details

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Template page — header, tabs, versions, delete

**Files:**
- Modify (rewrite): `web/src/pages/template-page.tsx`
- Modify: `web/src/pages/template-page.test.tsx`

**Interfaces:**
- Consumes: `PageHeader`, `UnderlineTabs` (`@/components/underline-tabs`) + `TabsContent` (`@/components/ui/tabs`), `useTabParam` (`@/hooks/use-tab-param`), `ConfirmDialog`, `TEMPLATES_PATH`, `templatePath` (`@/lib/routes`), `TemplateEditor` (Task 4), `TemplateInstances` (Task 3), `TemplateRevisions` (Task 2), `RolloutStrip`, `useRollout`, `useRolloutsApi`.
- Produces: default export `TemplatePage` (routes unchanged: `/templates/new`, `/templates/:templateId`), `export type DraftState` (unchanged; the app page's "Save as template" passes it). Tabs `?tab=` values: `editor` (default, not in the URL), `apps`, `versions`; old `instances` / `revisions` are aliases. Visible names: back link "Templates", "More actions" menu with "Delete template", tabs "Editor", "Apps using it", "Versions", note field "Version note", Save "Save as version N" / "Save template".

- [ ] **Step 1: Update the tests (they fail)**

In `web/src/pages/template-page.test.tsx` (as phase 1 left it: routes and URLs under `/templates`, `usePageTitle` mocked as `pageTitle`; change any `/org/templates` still there to `/templates`):

1. Change the first import line to `import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";`.

2. Replace these strings everywhere in the file:

| Old | New |
|---|---|
| `toHaveBeenLastCalledWith("Add template")` | `toHaveBeenLastCalledWith("New template")` |
| `"Saved r1"` | `"Saved version 1"` |
| `"Saved r4"` | `"Saved version 4"` |
| `"Save as r4"` | `"Save as version 4"` |
| `"Save as r5"` | `"Save as version 5"` |
| `"Revision note"` | `"Version note"` |
| `"Someone saved r4 meanwhile; reload"` | `"Someone saved version 4 meanwhile; reload"` |
| `{ name: "Upgrade" }` | `{ name: "Update" }` |
| `"Upgrade to r3"` | `"Update to version 3"` |
| `screen.getByText("r3")` | `screen.getByText("Version 3")` |
| `{ name: "Revisions" }` | `{ name: "Versions" }` |
| `"Changes from r2"` | `"Changes from version 2"` |
| `"Upgrade all to r3"` | `"Update all apps"` |
| `{ name: "Upgrade all" }` | `{ name: "Update all" }` |
| `"Upgrade to r3 queued…"` | `"Update to version 3 queued…"` |
| `"Couldn't start the upgrade"` | `"Couldn't start the update"` |

3. Replace the three delete tests (`"offers Delete only without live instances"`, `"deletes a template no app uses"`, `"explains a refused delete"`) with:

```tsx
  it("offers Delete only while no app uses the template, saying why", async () => {
    const user = userEvent.setup();
    fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [{ key: "k1" }] },
    });
    renderPage("/templates/t1");
    expect(await screen.findByText("Version 3 · used by 1 app")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "More actions" }));
    const item = await screen.findByRole("menuitem", { name: /Delete template/ });
    expect(item).toHaveAttribute("aria-disabled", "true");
    expect(item).toHaveTextContent("Delete its apps or detach them from it first");
  });

  it("deletes a template no app uses after a confirm", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [] },
      "/api/v1/template/delete-template": null,
    });
    renderPage("/templates/t1");
    await user.click(await screen.findByRole("button", { name: "More actions" }));
    await user.click(await screen.findByRole("menuitem", { name: /Delete template/ }));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("Delete twillingate?");
    expect(api.posted("/api/v1/template/delete-template")).toEqual([]);
    await user.click(within(dialog).getByRole("button", { name: "Delete template" }));
    expect(await screen.findByText("the list")).toBeInTheDocument();
    expect(api.posted("/api/v1/template/delete-template")).toEqual([{ id: "t1" }]);
  });

  it("explains a refused delete and keeps the confirm open", async () => {
    const user = userEvent.setup();
    fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [] },
      "/api/v1/template/delete-template": new ApiError("the template has live instances: delete or unlink them first", 409),
    });
    renderPage("/templates/t1");
    await user.click(await screen.findByRole("button", { name: "More actions" }));
    await user.click(await screen.findByRole("menuitem", { name: /Delete template/ }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Delete template" }));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Couldn't delete the template", {
        description: "the template has live instances: delete or unlink them first",
      }),
    );
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(screen.queryByText("the list")).not.toBeInTheDocument();
  });

  it("starts with a way back to Templates and opens old tab links on their new tabs", async () => {
    fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [] },
    });
    renderPage("/templates/t1?tab=revisions");
    expect(await screen.findByRole("tab", { name: "Versions" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("link", { name: "Templates" })).toHaveAttribute("href", "/templates");
    expect(screen.getByRole("heading", { level: 1, name: "twillingate" })).toBeInTheDocument();
    expect(screen.getByText("Version 3 · not used by any app yet")).toBeInTheDocument();
    cleanup();
    vi.unstubAllGlobals();

    fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [] },
    });
    renderPage("/templates/t1?tab=instances");
    expect(await screen.findByRole("tab", { name: "Apps using it" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText(/No app uses this template yet/)).toBeInTheDocument();
  });

  it("names a new template and says what saving does", () => {
    fakeAPI({});
    renderPage("/templates/new");
    expect(screen.getByRole("heading", { level: 1, name: "New template" })).toBeInTheDocument();
    expect(screen.getByText("Saving creates version 1.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "More actions" })).not.toBeInTheDocument();
    expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
  });
```

(`cleanup()` unmounts the first tree before the second `renderPage`, so each query sees one page.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/pages/template-page.test.tsx`
Expected: FAIL — no "More actions" button, tabs still "Revisions"/"Instances", Save says "Save as r4".

- [ ] **Step 3: Implement**

Replace the whole of `web/src/pages/template-page.tsx` with:

```tsx
import { useCallback, useEffect, useRef, useState } from "react";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import { Ellipsis, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { TabsContent } from "@/components/ui/tabs";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { StickyActionBar } from "@/components/editor-section";
import { InlineError } from "@/components/inline-error";
import { NotFound } from "@/components/not-found";
import { PageHeader } from "@/components/page-header";
import { LoadError, PageSpinner } from "@/components/page-states";
import { RolloutStrip } from "@/components/rollout-strip";
import { TemplateEditor } from "@/components/template-editor";
import { TemplateInstances } from "@/components/template-instances";
import { TemplateRevisions } from "@/components/template-revisions";
import { UnderlineTabs } from "@/components/underline-tabs";
import { useServers } from "@/context/use-servers";
import { useRollout } from "@/hooks/use-rollout";
import { useRolloutsApi } from "@/hooks/use-rollouts-api";
import { useTabParam } from "@/hooks/use-tab-param";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { isActive } from "@/lib/rollouts";
import { TEMPLATES_PATH, templatePath } from "@/lib/routes";
import { bodyFromDraft, draftFromBody, draftProblem, emptyDraft, type TemplateDraft } from "@/lib/template-draft";
import { ApiError, type InstanceView, type TemplateView } from "@/lib/templates";

const TABS = ["editor", "apps", "versions"] as const;
type Tab = (typeof TABS)[number];
// TAB_ALIASES opens links from before the rebuild (?tab=instances,
// ?tab=revisions) on the tabs' new names.
const TAB_ALIASES: Readonly<Record<string, Tab>> = { instances: "apps", revisions: "versions" };

const BACK = { to: TEMPLATES_PATH, label: "Templates" };

// sameDraft says whether d still holds what base held. A field the editor
// didn't touch keeps its reference; only the others are compared by value
// (typing a change and taking it back is clean again).
function sameDraft(d: TemplateDraft, base: TemplateDraft): boolean {
  return (Object.keys(d) as (keyof TemplateDraft)[]).every(
    (k) => d[k] === base[k] || JSON.stringify(d[k]) === JSON.stringify(base[k]),
  );
}

// usedBy says how many apps a template runs as, in plain words.
function usedBy(n: number): string {
  return n === 0 ? "not used by any app yet" : `used by ${n} ${n === 1 ? "app" : "apps"}`;
}

// DraftState is what "Save as template" hands the new template's editor:
// the draft, what was left out of it, and the app it was built from.
export type DraftState = { draft: TemplateDraft; notes: string[]; appName: string };

// TemplatePage is one template (/templates/:templateId) or a new one
// (/templates/new, optionally prefilled by Save as template): its editor,
// where Save adds a version, the apps using it (with Update all apps and
// the update's progress) and its versions with their changes. The tab
// lives in ?tab=.
export default function TemplatePage() {
  const { templateId } = useParams();
  const location = useLocation();
  const navigate = useNavigate();
  const api = useTemplatesApi();
  const rolloutsApi = useRolloutsApi();
  const { servers } = useServers();
  const prefill = (location.state as DraftState | null) ?? null;
  const [tab, setTab] = useTabParam<Tab>(TABS, "editor", { aliases: TAB_ALIASES });

  const [view, setView] = useState<TemplateView | null>(null);
  const [instances, setInstances] = useState<InstanceView[]>([]);
  const [draft, setDraft] = useState<TemplateDraft>(() => prefill?.draft ?? emptyDraft());
  // baseline is the draft as loaded (null: a prefilled draft, unsaved).
  const [baseline, setBaseline] = useState<TemplateDraft | null>(() => (prefill ? null : draft));
  const [loadError, setLoadError] = useState<ApiError | Error | null>(null);
  const [note, setNote] = useState("");
  const [saving, setSaving] = useState(false);
  // tried: a save was refused in the browser, so the draft's problem shows
  // (and follows the edits) beside Save.
  const [tried, setTried] = useState(false);
  // conflict is the version someone saved while this editor was open.
  const [conflict, setConflict] = useState<number | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);

  // load reads the template into the editor, and its apps unless only the
  // template changed (a save).
  const load = useCallback(
    async (withInstances = true) => {
      if (!templateId) return;
      setLoadError(null);
      try {
        const [v, list] = await Promise.all([
          api.getTemplate(templateId),
          withInstances ? api.listInstances({ templateId }) : null,
        ]);
        const loaded = draftFromBody(v.name, v.body);
        setView(v);
        if (list) setInstances(list);
        setDraft(loaded);
        setBaseline(loaded);
        setConflict(null);
      } catch (e) {
        setLoadError(e instanceof Error ? e : new Error("Failed to load the template"));
      }
    },
    [api, templateId],
  );

  useEffect(() => {
    void load();
  }, [load]);

  // reloadInstances follows an update without touching the editor.
  const reloadInstances = useCallback(() => {
    if (templateId) api.listInstances({ templateId }).then(setInstances, () => {});
  }, [api, templateId]);

  // The template's "Update all apps" rollout: each step it takes (and its
  // end) moves an app, so the list follows. The first progress seen (the
  // rollout's first read, or one just queued) moved nothing the page's own
  // load did not read: only a change after it refreshes the list.
  const { rollout, set: setRollout, stop } = useRollout(rolloutsApi, "template_upgrade", templateId ?? null);
  const progress = rollout ? `${rollout.id}:${rollout.done}:${rollout.skipped}:${rollout.state}` : "";
  const lastProgress = useRef("");
  useEffect(() => {
    const prev = lastProgress.current;
    lastProgress.current = progress;
    if (prev && progress && prev !== progress) reloadInstances();
  }, [progress, reloadInstances]);
  const serverName = (id: string) => servers.find((s) => s.id === id)?.name ?? id;

  const upgradeAll = async () => {
    if (!view) return;
    try {
      setRollout(await rolloutsApi.upgradeAll(view.id, view.latest_revision));
    } catch (e) {
      toast.error("Couldn't start the update", { description: e instanceof Error ? e.message : undefined });
    }
  };

  const dirty = baseline === null || !sameDraft(draft, baseline);

  const problem = tried ? draftProblem(draft) : null;

  const save = async () => {
    if (draftProblem(draft)) {
      setTried(true);
      return;
    }
    setSaving(true);
    try {
      const saved = await api.saveTemplate({
        ...(view ? { id: view.id } : {}),
        name: draft.name,
        body: bodyFromDraft(draft),
        ...(note.trim() ? { note: note.trim() } : {}),
        base_revision: view?.latest_revision ?? 0,
      });
      toast.success(`Saved version ${saved.revision}`);
      if (saved.warnings?.length) {
        toast.warning("Check these references", { description: saved.warnings.join("\n") });
      }
      setNote("");
      if (!view) {
        navigate(templatePath(saved.id), { replace: true });
        return;
      }
      await load(false);
    } catch (e) {
      if (e instanceof ApiError && e.status === 409 && view) {
        // Someone saved meanwhile: name their version; Reload takes it.
        const latest = await api.getTemplate(view.id).catch(() => null);
        setConflict(latest?.latest_revision ?? view.latest_revision + 1);
      } else {
        toast.error("Couldn't save the template", { description: e instanceof Error ? e.message : undefined });
      }
    } finally {
      setSaving(false);
    }
  };

  // remove deletes the template; a refusal is reported and rethrown so the
  // confirm dialog stays open.
  const remove = async () => {
    if (!view) return;
    try {
      await api.deleteTemplate(view.id);
    } catch (e) {
      toast.error("Couldn't delete the template", { description: e instanceof Error ? e.message : undefined });
      throw e;
    }
    toast.success(`Deleted ${view.name}`);
    navigate(TEMPLATES_PATH);
  };

  const title = view?.name ?? (templateId ? "Template" : "New template");

  if (loadError) {
    if (loadError instanceof ApiError && loadError.status === 404) {
      return <NotFound title="Template not found" description="This template doesn't exist in your organization." />;
    }
    return (
      <div className="space-y-6">
        <PageHeader title={title} back={BACK} />
        <LoadError message={loadError.message} onRetry={() => void load()} />
      </div>
    );
  }
  if (templateId && !view) {
    return (
      <div className="space-y-6">
        <PageHeader title={title} back={BACK} />
        <PageSpinner />
      </div>
    );
  }

  const live = instances.length;
  const editor = (
    <>
      {/* Below 1024px the shell's top bar is sticky at the top: sit under it. */}
      <StickyActionBar className="max-lg:top-14">
        {conflict !== null ? (
          <p role="alert" className="mr-auto flex flex-wrap items-center gap-2 text-sm text-destructive">
            Someone saved version {conflict} meanwhile; reload to get it (your changes here are dropped).
            <Button size="sm" variant="outline" onClick={() => void load()}>
              Reload
            </Button>
          </p>
        ) : view ? (
          <Input
            aria-label="Version note"
            value={note}
            placeholder="What changed (optional)"
            className="mr-auto max-w-md"
            onChange={(e) => setNote(e.target.value)}
          />
        ) : (
          // Version 1 has nothing to note.
          <span className="mr-auto" />
        )}
        {problem ? (
          <InlineError icon className="max-w-md">
            {problem}
          </InlineError>
        ) : null}
        <Button onClick={() => void save()} disabled={saving || (!dirty && !!view) || conflict !== null}>
          {saving ? "Saving…" : view ? `Save as version ${view.latest_revision + 1}` : "Save template"}
        </Button>
      </StickyActionBar>
      <TemplateEditor draft={draft} onChange={setDraft} />
    </>
  );

  return (
    <div className="space-y-6">
      <PageHeader
        title={title}
        subtitle={view ? `Version ${view.latest_revision} · ${usedBy(live)}` : "Saving creates version 1."}
        back={BACK}
        actions={
          view ? (
            // Not modal: the confirm dialog it opens takes the focus.
            <DropdownMenu modal={false}>
              <DropdownMenuTrigger asChild>
                <Button variant="outline" size="icon" className="size-10 sm:size-9" aria-label="More actions">
                  <Ellipsis className="size-4" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="max-w-72">
                <DropdownMenuItem
                  disabled={live > 0}
                  className="flex-col items-start gap-0.5 text-destructive focus:text-destructive"
                  onSelect={() => setConfirmDelete(true)}
                >
                  <span className="flex items-center gap-2">
                    <Trash2 className="size-4" /> Delete template
                  </span>
                  {live > 0 ? (
                    <span className="text-xs text-muted-foreground">Delete its apps or detach them from it first</span>
                  ) : null}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          ) : null
        }
      />

      <RolloutStrip rollout={rollout} serverName={serverName} onStop={stop} />

      {prefill ? (
        <div className="space-y-2 rounded-md border bg-muted/30 px-4 py-3 text-sm text-muted-foreground">
          <p>
            Built from <span className="font-medium text-foreground">{prefill.appName}</span>. Replace the parts that
            differ per app — hosts, ports — with <code className="font-mono">{"${PARAM}"}</code> parameters before saving.
          </p>
          {prefill.notes.length ? (
            <ul className="list-disc space-y-1 pl-4">
              {prefill.notes.map((n) => (
                <li key={n}>{n}</li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}

      {view ? (
        <UnderlineTabs<Tab>
          value={tab}
          onValueChange={setTab}
          label="Template sections"
          items={[
            { value: "editor", label: "Editor" },
            { value: "apps", label: "Apps using it" },
            { value: "versions", label: "Versions" },
          ]}
        >
          <TabsContent value="editor" className="mt-4">
            {editor}
          </TabsContent>
          <TabsContent value="apps" className="mt-4">
            <TemplateInstances
              view={view}
              instances={instances}
              onChanged={reloadInstances}
              onUpgradeAll={() => void upgradeAll()}
              rolloutActive={!!rollout && isActive(rollout)}
            />
          </TabsContent>
          <TabsContent value="versions" className="mt-4">
            <TemplateRevisions view={view} />
          </TabsContent>
        </UnderlineTabs>
      ) : (
        editor
      )}

      {view ? (
        <ConfirmDialog
          open={confirmDelete}
          onOpenChange={setConfirmDelete}
          title={`Delete ${view.name}?`}
          description="Its versions are deleted too. Apps detached from it stay as they are."
          confirmLabel="Delete template"
          pendingLabel="Deleting…"
          onConfirm={remove}
        />
      ) : null}
    </div>
  );
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/pages/template-page.test.tsx src/routes.test.tsx`
Expected: PASS (`routes.test.tsx` mounts the page by route and keeps passing).

Run: `grep -rnE "Revision note|Save as r|Upgrade all|Add template|r\{" web/src/pages/template-page.tsx web/src/pages/org-templates.tsx web/src/components/template-*.tsx web/src/components/upgrade-instance-dialog.tsx`
Expected: no output.

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/pages/template-page.tsx web/src/pages/template-page.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): template page with a back link, Editor/Apps using it/Versions tabs and a confirmed delete

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Sign-in — setup screen for a fresh instance, one frame for every sign-in page

**Files:**
- Create: `web/src/lib/auth-state.ts`, `web/src/lib/auth-state.test.ts`
- Create: `web/src/components/auth-screen.tsx`, `web/src/components/auth-screen.test.tsx`
- Modify (rewrite): `web/src/components/login-form.tsx` (the form only)
- Modify (rewrite): `web/src/pages/login.tsx`
- Modify (rewrite): `web/src/pages/register.tsx`
- Create: `web/src/pages/login.test.tsx`, `web/src/pages/register.test.tsx`
- Check (edit only if missing): `web/src/index.css` reduced-motion rule for `.login-snowflake`

**Interfaces:**
- Consumes: `GET /api/v1/auth/state` → `{ data: { bootstrap: boolean, registration_enabled: boolean } }` (`api/openapi.yaml` `StateResponse`; `bootstrap` is true on a fresh, unclaimed instance), `POST /api/v1/auth/register`, `useAuth().login`, `LoginIllustration`, i18n keys `login.*`.
- Produces:
  - `type AuthState = { bootstrap: boolean; registrationEnabled: boolean }`, `fetchAuthState(signal?: AbortSignal): Promise<AuthState>` (`@/lib/auth-state`) — throws on a non-2xx answer.
  - `AuthScreen({ children })`, `AuthHeading({ title, subtitle })` (`@/components/auth-screen`) — the illustration carries `data-testid="auth-illustration"` and is hidden below 768px (`hidden md:block`).
  - `LoginForm({ onSubmitCredentials?, isSubmitting?, error?, availableProviders?, providersLoading?, registerEnabled?, defaultEmail?, className? })` — renders only the `<form>` now.
  - `/login` on a fresh instance replaces itself with `/register`, which shows "Set up Winterflow".

Snowfall and reduced motion: phase 1 Task 2 added, inside `@media (prefers-reduced-motion: reduce)` in `web/src/index.css`, a rule hiding `.login-snowflake`. This task does not duplicate it; Step 4 only checks it is there.

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/auth-state.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from "vitest";

import { fetchAuthState } from "@/lib/auth-state";
import { fakeAPI } from "@/test/fake-api";

afterEach(() => vi.unstubAllGlobals());

describe("fetchAuthState", () => {
  it("reads whether the instance is fresh and whether sign-up is open", async () => {
    fakeAPI({ "/api/v1/auth/state": { bootstrap: true, registration_enabled: true } });
    expect(await fetchAuthState()).toEqual({ bootstrap: true, registrationEnabled: true });
  });

  it("reads a missing field as false", async () => {
    fakeAPI({ "/api/v1/auth/state": {} });
    expect(await fetchAuthState()).toEqual({ bootstrap: false, registrationEnabled: false });
  });

  it("fails when the server refuses", async () => {
    fakeAPI({ "/api/v1/auth/state": new Error("failed to read auth state") });
    await expect(fetchAuthState()).rejects.toThrow();
  });
});
```

Create `web/src/components/auth-screen.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import "@/lib/i18n";
import { AuthHeading, AuthScreen } from "@/components/auth-screen";

describe("AuthScreen", () => {
  it("frames the form beside the snowfall, which phones don't show", () => {
    render(
      <AuthScreen>
        <p>the form</p>
      </AuthScreen>,
    );
    expect(screen.getByText("the form")).toBeInTheDocument();
    expect(screen.getByTestId("auth-illustration")).toHaveClass("hidden", "md:block");
    expect(screen.getByRole("link", { name: "Terms of Service" })).toHaveAttribute("rel", "noopener noreferrer");
  });

  it("titles a sign-in page at the page-title size", () => {
    render(<AuthHeading title="Set up Winterflow" subtitle="Create the first account." />);
    expect(screen.getByRole("heading", { level: 1, name: "Set up Winterflow" })).toHaveClass("text-2xl", "font-semibold");
    expect(screen.getByText("Create the first account.")).toBeInTheDocument();
  });
});
```

Create `web/src/pages/login.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import "@/lib/i18n";
import LoginPage from "@/pages/login";
import { fakeAPI } from "@/test/fake-api";

const auth = vi.hoisted(() => ({ isAuthenticated: false, checked: true, login: vi.fn(), logout: vi.fn() }));
vi.mock("@/context/use-auth", () => ({ useAuth: () => auth }));

function renderLogin() {
  return render(
    <MemoryRouter initialEntries={["/login"]}>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/register" element={<p>setup screen</p>} />
        <Route path="/" element={<p>home</p>} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  auth.login.mockReset();
  // The provider list (/auth/list) isn't answered here; the page logs that.
  vi.spyOn(console, "error").mockImplementation(() => {});
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("LoginPage", () => {
  it("sends a fresh instance to the setup screen", async () => {
    fakeAPI({ "/api/v1/auth/state": { bootstrap: true, registration_enabled: true } });
    renderLogin();
    expect(await screen.findByText("setup screen")).toBeInTheDocument();
  });

  it("stays on the login form once set up, offering sign-up only when it is open", async () => {
    fakeAPI({ "/api/v1/auth/state": { bootstrap: false, registration_enabled: true } });
    renderLogin();
    expect(await screen.findByRole("link", { name: "Create an account" })).toHaveAttribute("href", "/register");
    expect(screen.getByRole("heading", { level: 1, name: "Welcome back" })).toBeInTheDocument();
    expect(screen.queryByText("setup screen")).not.toBeInTheDocument();
  });

  it("still lets people log in when the instance's state can't be read", async () => {
    const api = fakeAPI({ "/api/v1/auth/state": new Error("down") });
    renderLogin();
    await waitFor(() => expect(api.calls.some((c) => c.path === "/api/v1/auth/state")).toBe(true));
    expect(screen.getByRole("heading", { level: 1, name: "Welcome back" })).toBeInTheDocument();
    expect(screen.queryByText("setup screen")).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Create an account" })).not.toBeInTheDocument();
  });

  it("logs in and goes home", async () => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/auth/state": { bootstrap: false, registration_enabled: false } });
    auth.login.mockResolvedValue(undefined);
    renderLogin();
    await user.type(screen.getByLabelText("Email"), "ann@example.test");
    await user.type(screen.getByLabelText("Password"), "secret");
    await user.click(screen.getByRole("button", { name: "Login" }));
    expect(auth.login).toHaveBeenCalledWith({ username: "ann@example.test", password: "secret" });
    expect(await screen.findByText("home")).toBeInTheDocument();
  });
});
```

Create `web/src/pages/register.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import "@/lib/i18n";
import RegisterPage from "@/pages/register";
import { fakeAPI } from "@/test/fake-api";

const auth = vi.hoisted(() => ({ isAuthenticated: false, checked: true, login: vi.fn(), logout: vi.fn() }));
vi.mock("@/context/use-auth", () => ({ useAuth: () => auth }));
const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

function renderRegister() {
  return render(
    <MemoryRouter initialEntries={["/register"]}>
      <Routes>
        <Route path="/register" element={<RegisterPage />} />
        <Route path="/login" element={<p>login page</p>} />
        <Route path="/" element={<p>home</p>} />
      </Routes>
    </MemoryRouter>,
  );
}

async function fill(user: ReturnType<typeof userEvent.setup>, password = "hunter22", confirm = password) {
  await user.type(screen.getByLabelText("Name"), "Ann");
  await user.type(screen.getByLabelText("Email"), "ann@example.test");
  await user.type(screen.getByLabelText("Password"), password);
  await user.type(screen.getByLabelText("Confirm password"), confirm);
}

beforeEach(() => {
  auth.login.mockReset();
  toast.success.mockReset();
});
afterEach(() => vi.unstubAllGlobals());

describe("RegisterPage", () => {
  it("sets up a fresh instance: the first account, signed in at once", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({
      "/api/v1/auth/state": { bootstrap: true, registration_enabled: true },
      "/api/v1/auth/register": { user_id: "u1" },
    });
    auth.login.mockResolvedValue(undefined);
    renderRegister();
    expect(await screen.findByRole("heading", { level: 1, name: "Set up Winterflow" })).toBeInTheDocument();
    // Nobody can log in yet: no account exists.
    expect(screen.queryByRole("link", { name: "Log in" })).not.toBeInTheDocument();
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Set up Winterflow" }));
    expect(await screen.findByText("home")).toBeInTheDocument();
    expect(api.posted("/api/v1/auth/register")).toEqual([{ name: "Ann", email: "ann@example.test", password: "hunter22" }]);
    expect(auth.login).toHaveBeenCalledWith({ username: "ann@example.test", password: "hunter22" });
  });

  it("is an ordinary sign-up on a set-up instance with sign-up open", async () => {
    fakeAPI({ "/api/v1/auth/state": { bootstrap: false, registration_enabled: true } });
    renderRegister();
    expect(await screen.findByRole("heading", { level: 1, name: "Create an account" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Log in" })).toHaveAttribute("href", "/login");
  });

  it("says sign-up is closed when it is", async () => {
    fakeAPI({ "/api/v1/auth/state": { bootstrap: false, registration_enabled: false } });
    renderRegister();
    expect(await screen.findByRole("heading", { level: 1, name: "Registration is closed" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Back to log in" })).toHaveAttribute("href", "/login");
  });

  it("offers the form when the state can't be read (the server decides on submit)", async () => {
    fakeAPI({ "/api/v1/auth/state": new Error("down") });
    renderRegister();
    expect(await screen.findByRole("heading", { level: 1, name: "Create an account" })).toBeInTheDocument();
  });

  it("points at the field that is wrong instead of sending", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({ "/api/v1/auth/state": { bootstrap: false, registration_enabled: true } });
    renderRegister();
    await screen.findByRole("heading", { level: 1, name: "Create an account" });
    await fill(user, "hunter22", "hunter23");
    await user.click(screen.getByRole("button", { name: "Create account" }));
    expect(screen.getByRole("alert")).toHaveTextContent("The passwords don't match.");
    expect(screen.getByLabelText("Confirm password")).toHaveAttribute("aria-invalid", "true");
    expect(api.posted("/api/v1/auth/register")).toEqual([]);

    await user.clear(screen.getByLabelText("Password"));
    await user.type(screen.getByLabelText("Password"), "abc");
    await user.click(screen.getByRole("button", { name: "Create account" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Use at least 4 characters.");
    expect(screen.getByLabelText("Password")).toHaveAttribute("aria-invalid", "true");
  });

  it("shows the server's refusal on the form", async () => {
    const user = userEvent.setup();
    fakeAPI({
      "/api/v1/auth/state": { bootstrap: false, registration_enabled: true },
      "/api/v1/auth/register": new Error("email already registered"),
    });
    renderRegister();
    await screen.findByRole("heading", { level: 1, name: "Create an account" });
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Create account" }));
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("email already registered"));
    expect(auth.login).not.toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/auth-state.test.ts src/components/auth-screen.test.tsx src/pages/login.test.tsx src/pages/register.test.tsx`
Expected: FAIL — `@/lib/auth-state` and `@/components/auth-screen` do not exist; login does not redirect; register says "Create the admin account" and reports errors as toasts.

- [ ] **Step 3: Implement**

Create `web/src/lib/auth-state.ts`:

```ts
import { apiBase } from "@/config";

// AuthState is what GET /api/v1/auth/state tells the sign-in pages:
// bootstrap — a fresh (unclaimed) instance, whose first account sets it
// up; registrationEnabled — whether a sign-up would be accepted.
export type AuthState = { bootstrap: boolean; registrationEnabled: boolean };

// fetchAuthState reads the instance's auth state (no session needed). It
// throws when the server doesn't answer with it.
export async function fetchAuthState(signal?: AbortSignal): Promise<AuthState> {
  const res = await fetch(`${apiBase}/api/v1/auth/state`, { signal });
  if (!res.ok) throw new Error(`Request failed: ${res.status}`);
  const body = (await res.json()) as { data?: { bootstrap?: unknown; registration_enabled?: unknown } } | null;
  return {
    bootstrap: body?.data?.bootstrap === true,
    registrationEnabled: body?.data?.registration_enabled === true,
  };
}
```

Create `web/src/components/auth-screen.tsx`:

```tsx
import type { ReactNode } from "react";
import { Trans } from "react-i18next";

import { Card, CardContent } from "@/components/ui/card";
import { FieldDescription } from "@/components/ui/field";
import { LoginIllustration } from "@/components/login-illustration";

const linkClass = "underline-offset-2 hover:underline";

// AuthScreen is the frame of every sign-in page (log in, set up, sign up):
// the form beside the snowfall on wider screens, the form alone on phones
// (the illustration is decoration), and the terms line under the card.
export function AuthScreen({ children }: { children: ReactNode }) {
  return (
    <div className="bg-muted flex min-h-svh flex-col items-center justify-center p-4 sm:p-6 md:p-10">
      <div className="flex w-full max-w-sm flex-col gap-6 md:max-w-4xl">
        <Card className="overflow-hidden p-0">
          <CardContent className="grid p-0 md:grid-cols-2">
            <div className="p-6 md:p-8">{children}</div>
            <LoginIllustration data-testid="auth-illustration" className="hidden h-full md:block" />
          </CardContent>
        </Card>
        <FieldDescription className="px-6 text-center">
          <Trans
            i18nKey="login.terms"
            components={{
              WinterflowLink: <a href="https://winterflow.io/" className={linkClass} target="_blank" rel="noopener noreferrer" />,
              TermsLink: (
                <a
                  href="https://winterflow.io/legal/terms-of-service/"
                  className={linkClass}
                  target="_blank"
                  rel="noopener noreferrer"
                />
              ),
              PrivacyLink: (
                <a
                  href="https://winterflow.io/legal/privacy-policy/"
                  className={linkClass}
                  target="_blank"
                  rel="noopener noreferrer"
                />
              ),
            }}
          />
        </FieldDescription>
      </div>
    </div>
  );
}

// AuthHeading is a sign-in page's title (24/600) and one line under it.
export function AuthHeading({ title, subtitle }: { title: string; subtitle: ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-2 text-center">
      <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
      <p className="text-muted-foreground text-balance">{subtitle}</p>
    </div>
  );
}
```

Replace the whole of `web/src/components/login-form.tsx` with:

```tsx
import type { FormEvent } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";

import { cn } from "@/lib/utils";
import { apiBaseUrl, appBaseUrl } from "@/config";
import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldGroup, FieldLabel, FieldSeparator } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { PasswordInput } from "@/components/ui/password-input";
import { AuthHeading } from "@/components/auth-screen";

type LoginFormValues = {
  username: string;
  password: string;
};

type LoginFormProps = {
  onSubmitCredentials?: (values: LoginFormValues) => void | Promise<void>;
  isSubmitting?: boolean;
  error?: string | null;
  availableProviders?: string[];
  providersLoading?: boolean;
  // registerEnabled: show the "create an account" link.
  registerEnabled?: boolean;
  defaultEmail?: string;
  className?: string;
};

const socialProviders = [
  {
    id: "google",
    icon: (
      <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">
        <path
          d="M12.48 10.92v3.28h7.84c-.24 1.84-.853 3.187-1.787 4.133-1.147 1.147-2.933 2.4-6.053 2.4-4.827 0-8.6-3.893-8.6-8.72s3.773-8.72 8.6-8.72c2.6 0 4.507 1.027 5.907 2.347l2.307-2.307C18.747 1.44 16.133 0 12.48 0 5.867 0 .307 5.387.307 12s5.56 12 12.173 12c3.573 0 6.267-1.173 8.373-3.36 2.16-2.16 2.84-5.213 2.84-7.667 0-.76-.053-1.467-.173-2.053H12.48z"
          fill="currentColor"
        />
      </svg>
    ),
    labelKey: "login.providers.google",
  },
] as const;

// LoginForm is the log-in form (email + password, the sign-up link, any
// SSO buttons); the page puts it in an AuthScreen.
export function LoginForm({
  onSubmitCredentials,
  isSubmitting = false,
  error,
  availableProviders = ["local"],
  providersLoading = false,
  registerEnabled = false,
  defaultEmail = "",
  className,
}: LoginFormProps) {
  const { t } = useTranslation();
  const providerSet = new Set(availableProviders);

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!onSubmitCredentials) return;
    const formData = new FormData(event.currentTarget);
    void onSubmitCredentials({
      username: String(formData.get("username") ?? ""),
      password: String(formData.get("password") ?? ""),
    });
  };

  return (
    <form className={cn(className)} onSubmit={handleSubmit}>
      <FieldGroup>
        <AuthHeading title={t("login.title")} subtitle={t("login.subtitle")} />
        <Field>
          <FieldLabel htmlFor="username">{t("login.usernameLabel")}</FieldLabel>
          <Input
            id="username"
            name="username"
            type="email"
            autoComplete="email"
            placeholder={t("login.usernamePlaceholder")}
            defaultValue={defaultEmail}
            required
          />
        </Field>
        <Field>
          <FieldLabel htmlFor="password">{t("login.passwordLabel")}</FieldLabel>
          <PasswordInput
            id="password"
            name="password"
            autoComplete="current-password"
            required
            toggleLabel={t("login.showPassword")}
            hideLabel={t("login.hidePassword")}
          />
        </Field>
        <Field>
          <Button type="submit" disabled={isSubmitting} className="w-full">
            {isSubmitting ? t("login.submitting") : t("login.submit")}
          </Button>
        </Field>
        {registerEnabled ? (
          <FieldDescription className="text-center text-sm">
            {t("login.noAccount")}{" "}
            <Link to="/register" className="underline underline-offset-2">
              {t("login.registerLink")}
            </Link>
          </FieldDescription>
        ) : null}
        {error ? (
          <FieldDescription className="text-destructive text-sm" role="status" aria-live="assertive">
            {error}
          </FieldDescription>
        ) : null}
        {/* Hide the SSO block entirely when no social provider is
            configured — a permanently disabled button is just noise. */}
        {socialProviders.some((p) => providerSet.has(p.id)) ? (
          <>
            <FieldSeparator className="*:data-[slot=field-separator-content]:bg-card">{t("login.ssoLabel")}</FieldSeparator>
            <div className="flex justify-center gap-4">
              {socialProviders.map((provider) => {
                const isAvailable = providerSet.has(provider.id) && !providersLoading;
                return (
                  <span key={provider.id} className="inline-flex">
                    <Button
                      variant="outline"
                      type="button"
                      disabled={!isAvailable}
                      className="min-h-10 min-w-16"
                      onClick={() => {
                        if (!isAvailable) return;
                        const siteUrl = window.location.origin;
                        let siteHost = siteUrl;
                        try {
                          siteHost = new URL(siteUrl).hostname;
                        } catch {
                          try {
                            siteHost = new URL(appBaseUrl).hostname;
                          } catch {
                            siteHost = siteUrl;
                          }
                        }
                        window.location.href = `${apiBaseUrl}/auth/${provider.id}/login?site=${encodeURIComponent(
                          siteHost,
                        )}&from=${encodeURIComponent(appBaseUrl)}`;
                      }}
                    >
                      {provider.icon}
                      <span className="sr-only">{t(provider.labelKey)}</span>
                    </Button>
                  </span>
                );
              })}
            </div>
          </>
        ) : null}
      </FieldGroup>
    </form>
  );
}
```

Replace the whole of `web/src/pages/login.tsx` with:

```tsx
import { useEffect, useMemo, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";

import { AuthScreen } from "@/components/auth-screen";
import { LoginForm } from "@/components/login-form";
import { apiBaseUrl } from "@/config";
import { useAuth } from "@/context/use-auth";
import { fetchAuthState } from "@/lib/auth-state";

type LoginState = { from?: { pathname?: string }; email?: string } | undefined;

// LoginPage is /login. A fresh (unclaimed) instance has no account to log
// in to, so it goes to the setup screen (/register) instead; when the
// instance's state can't be read the form stays (and offers no sign-up).
export default function LoginPage() {
  const { login, isAuthenticated } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const { t } = useTranslation();
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [providers, setProviders] = useState<string[]>([]);
  const [providersLoading, setProvidersLoading] = useState(true);
  const [registerEnabled, setRegisterEnabled] = useState(false);
  const state = location.state as LoginState;
  const redirectTo = state?.from?.pathname ?? "/";

  useEffect(() => {
    const ctl = new AbortController();
    fetchAuthState(ctl.signal).then(
      (s) => {
        if (s.bootstrap) {
          navigate("/register", { replace: true });
          return;
        }
        setRegisterEnabled(s.registrationEnabled);
      },
      () => {
        // Unknown state: keep the form, without a sign-up link.
      },
    );
    return () => ctl.abort();
  }, [navigate]);

  useEffect(() => {
    let cancelled = false;
    const fetchProviders = async () => {
      try {
        const response = await fetch(`${apiBaseUrl}/auth/list`);
        if (!response.ok) {
          throw new Error(`Failed to load providers: ${response.status}`);
        }
        const data = (await response.json()) as unknown;
        if (!Array.isArray(data)) {
          throw new Error("Invalid provider response");
        }
        const parsed = data.filter((provider): provider is string => typeof provider === "string");
        if (!cancelled) setProviders(parsed);
      } catch (fetchError) {
        console.error("Failed to fetch providers", fetchError);
        if (!cancelled) setProviders(["local"]);
      } finally {
        if (!cancelled) setProvidersLoading(false);
      }
    };
    void fetchProviders();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (isAuthenticated) navigate(redirectTo, { replace: true });
  }, [isAuthenticated, navigate, redirectTo]);

  const handleSubmit = async (values: { username: string; password: string }) => {
    setIsSubmitting(true);
    setError(null);
    try {
      await login(values);
      navigate(redirectTo, { replace: true });
    } catch (err) {
      console.error("login failed", err);
      setError(t("login.errorGeneric"));
    } finally {
      setIsSubmitting(false);
    }
  };

  const effectiveProviders = useMemo(() => (providers.length === 0 ? ["local"] : providers), [providers]);

  return (
    <AuthScreen>
      <LoginForm
        onSubmitCredentials={handleSubmit}
        isSubmitting={isSubmitting}
        error={error}
        availableProviders={effectiveProviders}
        providersLoading={providersLoading}
        registerEnabled={registerEnabled}
        defaultEmail={state?.email ?? ""}
      />
    </AuthScreen>
  );
}
```

Replace the whole of `web/src/pages/register.tsx` with:

```tsx
import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { toast } from "sonner";

import { apiBase } from "@/config";
import { useAuth } from "@/context/use-auth";
import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { PasswordInput } from "@/components/ui/password-input";
import { Spinner } from "@/components/ui/spinner";
import { AuthHeading, AuthScreen } from "@/components/auth-screen";
import { fetchAuthState, type AuthState } from "@/lib/auth-state";

type Errors = { password?: string; confirm?: string; form?: string };

// RegisterPage is /register: on a fresh instance the setup screen ("Set up
// Winterflow" — the first account claims the instance and its
// organization), afterwards the sign-up form when the server's policy
// (REGISTRATION_ENABLED, the single-org rule) allows one. When the state
// can't be read the form is offered: the API enforces the policy.
export default function RegisterPage() {
  const navigate = useNavigate();
  const { login } = useAuth();
  const [state, setState] = useState<AuthState | null>(null);
  const [busy, setBusy] = useState(false);
  const [errors, setErrors] = useState<Errors>({});

  useEffect(() => {
    const ctl = new AbortController();
    fetchAuthState(ctl.signal).then(setState, () => {
      if (!ctl.signal.aborted) setState({ bootstrap: false, registrationEnabled: true });
    });
    return () => ctl.abort();
  }, []);

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const name = String(form.get("name") ?? "").trim();
    const email = String(form.get("email") ?? "").trim();
    const password = String(form.get("password") ?? "");
    const confirm = String(form.get("confirm") ?? "");
    const next: Errors = {};
    if (password.length < 4) next.password = "Use at least 4 characters.";
    else if (password !== confirm) next.confirm = "The passwords don't match.";
    setErrors(next);
    if (next.password || next.confirm) return;
    setBusy(true);
    try {
      const res = await fetch(`${apiBase}/api/v1/auth/register`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name, email, password }),
      });
      const body = (await res.json().catch(() => null)) as { success?: boolean; message?: string } | null;
      if (!res.ok || !body?.success) {
        throw new Error(body?.message ?? `Request failed: ${res.status}`);
      }
      // Sign the fresh account in right away; if that somehow fails, fall
      // back to the login page with the email prefilled.
      try {
        await login({ username: email, password });
        toast.success("Welcome to Winterflow!");
        navigate("/", { replace: true });
      } catch {
        toast.success("Account created — log in to continue");
        navigate("/login", { state: { email } });
      }
    } catch (e) {
      setErrors({ form: e instanceof Error ? e.message : "Registration failed" });
    } finally {
      setBusy(false);
    }
  };

  if (state === null) {
    return (
      <AuthScreen>
        <div className="flex min-h-64 items-center justify-center">
          <Spinner />
        </div>
      </AuthScreen>
    );
  }

  const bootstrap = state.bootstrap;
  if (!bootstrap && !state.registrationEnabled) {
    return (
      <AuthScreen>
        <div className="flex min-h-64 flex-col items-center justify-center gap-6">
          <AuthHeading title="Registration is closed" subtitle="Ask someone in your organization to create an account for you." />
          <Button asChild variant="outline" className="min-h-10">
            <Link to="/login">Back to log in</Link>
          </Button>
        </div>
      </AuthScreen>
    );
  }

  return (
    <AuthScreen>
      <form onSubmit={(e) => void handleSubmit(e)}>
        <FieldGroup>
          <AuthHeading
            title={bootstrap ? "Set up Winterflow" : "Create an account"}
            subtitle={
              bootstrap
                ? "Create the first account. It manages this Winterflow and can add other people later."
                : "Sign up for your Winterflow account."
            }
          />
          <Field>
            <FieldLabel htmlFor="reg-name">Name</FieldLabel>
            <Input id="reg-name" name="name" autoComplete="name" maxLength={64} required />
          </Field>
          <Field>
            <FieldLabel htmlFor="reg-email">Email</FieldLabel>
            <Input id="reg-email" name="email" type="email" autoComplete="email" placeholder="you@example.com" required />
          </Field>
          <Field data-invalid={errors.password ? true : undefined}>
            <FieldLabel htmlFor="reg-password">Password</FieldLabel>
            <PasswordInput
              id="reg-password"
              name="password"
              autoComplete="new-password"
              required
              aria-invalid={errors.password ? true : undefined}
              aria-describedby={errors.password ? "reg-password-error" : undefined}
              toggleLabel="Show password"
              hideLabel="Hide password"
            />
            {errors.password ? <FieldError id="reg-password-error">{errors.password}</FieldError> : null}
          </Field>
          <Field data-invalid={errors.confirm ? true : undefined}>
            <FieldLabel htmlFor="reg-confirm">Confirm password</FieldLabel>
            <PasswordInput
              id="reg-confirm"
              name="confirm"
              autoComplete="new-password"
              required
              aria-invalid={errors.confirm ? true : undefined}
              aria-describedby={errors.confirm ? "reg-confirm-error" : undefined}
              toggleLabel="Show password"
              hideLabel="Hide password"
            />
            {errors.confirm ? <FieldError id="reg-confirm-error">{errors.confirm}</FieldError> : null}
          </Field>
          {errors.form ? <FieldError>{errors.form}</FieldError> : null}
          <Field>
            <Button type="submit" className="w-full" disabled={busy}>
              {busy ? (bootstrap ? "Setting up…" : "Creating…") : bootstrap ? "Set up Winterflow" : "Create account"}
            </Button>
          </Field>
          {bootstrap ? null : (
            <FieldDescription className="text-center text-sm">
              Already have an account?{" "}
              <Link to="/login" className="underline underline-offset-2">
                Log in
              </Link>
            </FieldDescription>
          )}
        </FieldGroup>
      </form>
    </AuthScreen>
  );
}
```

- [ ] **Step 4: Run the tests to verify they pass, and check the snowfall rule**

Run: `pnpm --dir web exec vitest run src/lib/auth-state.test.ts src/components/auth-screen.test.tsx src/pages/login.test.tsx src/pages/register.test.tsx src/routes.test.tsx`
Expected: PASS

Run: `grep -n -A8 "prefers-reduced-motion: reduce" web/src/index.css`
Expected: the block contains `.login-snowflake` with `display: none;` (phase 1 Task 2). If it does not, add inside that block:

```css
    /* The login page's snowfall is decoration: gone for less motion. */
    .login-snowflake {
        display: none;
    }
```

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/auth-state.ts web/src/lib/auth-state.test.ts web/src/components/auth-screen.tsx web/src/components/auth-screen.test.tsx web/src/components/login-form.tsx web/src/pages/login.tsx web/src/pages/register.tsx web/src/pages/login.test.tsx web/src/pages/register.test.tsx web/src/index.css
git commit -m "$(cat <<'EOF'
feat(web): fresh instance opens "Set up Winterflow"; one frame for every sign-in page

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: Add server — two steps in a standard dialog

**Files:**
- Create (unless phase 5 already did): `web/src/lib/clipboard.ts`, `web/src/lib/clipboard.test.ts`
- Modify: `web/src/components/ui/code-input.tsx` (letters on phones, phone-sized slots, `id`, no password-manager polling)
- Modify (rewrite): `web/src/components/server-creation-dialog.tsx`
- Create: `web/src/components/server-creation-dialog.test.tsx`
- Delete: `web/src/components/create-organization-modal.tsx`

**Interfaces:**
- Consumes: `ResponsiveDialog`, `Disclosure` (`@/components/disclosure`), `InlineError`, `createJsonClient`, `jsonPost` (`@/lib/api`), `apiBase` (`@/config`), `POST /api/v1/server/register` with `{ code }` → `{ server: { id, name, … } }` (the API claims into the caller's own organization; `organization_id` is optional and no longer sent).
- Produces:
  - `copyText(text: string): Promise<void>` (`@/lib/clipboard`) — clipboard API where offered, else the selection copy; throws when both are refused.
  - `CodeInput({ length?, value, onChange, className?, disabled?, autoFocus?, id? })` — accepts `A–Z0–9`, asks phones for a text keyboard with capitals, uppercases.
  - `ServerSelectionDialog({ isOpen, onClose, onServerAdded? })` — same export and props (the shell from phase 1 mounts it; standalone never does). Step 1 "Install Winterflow on the server" (command + Copy, providers under "Don't have a server yet?"), step 2 "Connect it" (the code, "Add server").

- [ ] **Step 1: Write the failing tests**

First check: `ls web/src/lib/clipboard.ts 2>/dev/null`. If it exists and exports `copyText(text: string): Promise<void>` that throws on a refused copy, keep it and only add the test cases below that its own test lacks; otherwise create both files as given.

Create `web/src/lib/clipboard.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from "vitest";

import { copyText } from "@/lib/clipboard";

afterEach(() => vi.unstubAllGlobals());

describe("copyText", () => {
  it("uses the clipboard API where the browser offers it", async () => {
    const writeText = vi.fn(async () => {});
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    await copyText("hello");
    expect(writeText).toHaveBeenCalledWith("hello");
  });

  it("falls back to a selection copy on a plain-http origin", async () => {
    vi.stubGlobal("navigator", {});
    const exec = vi.fn(() => true);
    Object.defineProperty(document, "execCommand", { value: exec, configurable: true });
    await copyText("hello");
    expect(exec).toHaveBeenCalledWith("copy");
    expect(document.querySelector("textarea")).toBeNull();
  });

  it("fails when the browser refuses both", async () => {
    vi.stubGlobal("navigator", {});
    Object.defineProperty(document, "execCommand", { value: () => false, configurable: true });
    await expect(copyText("hello")).rejects.toThrow("copy rejected");
    expect(document.querySelector("textarea")).toBeNull();
  });
});
```

Create `web/src/components/server-creation-dialog.test.tsx`:

```tsx
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ServerSelectionDialog } from "@/components/server-creation-dialog";
import { fakeAPI } from "@/test/fake-api";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

const COMMAND = "curl -fsSL https://get.winterflow.io/agent | sudo bash";

function renderDialog(isOpen = true) {
  const onClose = vi.fn();
  const onServerAdded = vi.fn();
  const view = render(<ServerSelectionDialog isOpen={isOpen} onClose={onClose} onServerAdded={onServerAdded} />);
  const reopen = (open: boolean) =>
    view.rerender(<ServerSelectionDialog isOpen={open} onClose={onClose} onServerAdded={onServerAdded} />);
  return { onClose, onServerAdded, reopen };
}

// The code input is input-otp's transparent <input> over the slots; its
// container turns pointer events off, which user-event would refuse.
const setup = () => userEvent.setup({ pointerEventsCheck: 0 });

beforeEach(() => {
  toast.success.mockReset();
});
afterEach(() => vi.unstubAllGlobals());

describe("ServerSelectionDialog", () => {
  it("installs, then connects the server with the code, in two steps", async () => {
    const user = setup();
    const api = fakeAPI({ "/api/v1/server/register": { server: { id: "s9", name: "nas" } } });
    const { onClose, onServerAdded } = renderDialog();
    const dialog = screen.getByRole("dialog", { name: "Add a server" });
    expect(dialog).toHaveTextContent("Step 1 of 2");
    expect(screen.getByTestId("install-command")).toHaveTextContent(COMMAND);
    await user.click(within(dialog).getByRole("button", { name: "Copy" }));
    expect(await navigator.clipboard.readText()).toBe(COMMAND);
    expect(within(dialog).getByRole("button", { name: "Copied" })).toBeInTheDocument();

    await user.click(within(dialog).getByRole("button", { name: "Next: enter the code" }));
    expect(dialog).toHaveTextContent("Step 2 of 2");
    const add = within(dialog).getByRole("button", { name: "Add server" });
    expect(add).toBeDisabled();
    const code = within(dialog).getByLabelText("Connection code");
    // Codes have letters: phones get a text keyboard with capitals.
    expect(code).toHaveAttribute("inputmode", "text");
    expect(code).toHaveAttribute("autocapitalize", "characters");
    await user.click(code);
    await user.paste("ab12cd");
    expect(add).toBeEnabled();
    await user.click(add);
    await waitFor(() => expect(onServerAdded).toHaveBeenCalled());
    expect(api.posted("/api/v1/server/register")).toEqual([{ code: "AB12CD" }]);
    expect(onClose).toHaveBeenCalled();
    expect(toast.success).toHaveBeenCalledWith("Added nas");
  });

  it("explains a wrong or expired code in plain words and keeps it for a fix", async () => {
    const user = setup();
    fakeAPI({ "/api/v1/server/register": new Error("invalid registration code") });
    const { onClose } = renderDialog();
    await user.click(screen.getByRole("button", { name: "Next: enter the code" }));
    const code = screen.getByLabelText("Connection code");
    await user.click(code);
    await user.paste("AB12CD");
    await user.click(screen.getByRole("button", { name: "Add server" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("That code doesn't match a server waiting to connect");
    expect(code).toHaveValue("AB12CD");
    expect(onClose).not.toHaveBeenCalled();
    vi.unstubAllGlobals();

    fakeAPI({ "/api/v1/server/register": new Error("registration code has expired") });
    await user.click(screen.getByRole("button", { name: "Add server" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("That code has expired");
  });

  it("says when copying isn't possible, so the command is copied by hand", async () => {
    const user = setup();
    renderDialog();
    vi.spyOn(navigator.clipboard, "writeText").mockRejectedValue(new Error("denied"));
    await user.click(screen.getByRole("button", { name: "Copy" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Select the command and copy it yourself");
    expect(screen.getByTestId("install-command")).toHaveClass("select-all");
  });

  it("keeps provider links behind “Don't have a server yet?”", async () => {
    const user = setup();
    renderDialog();
    expect(screen.queryByRole("link", { name: /Hetzner/ })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Don't have a server yet?" }));
    const link = screen.getByRole("link", { name: /Hetzner/ });
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
  });

  it("never asks for an organization", async () => {
    const user = setup();
    renderDialog();
    expect(screen.getByRole("dialog")).not.toHaveTextContent(/organization/i);
    await user.click(screen.getByRole("button", { name: "Next: enter the code" }));
    expect(screen.getByRole("dialog")).not.toHaveTextContent(/organization/i);
  });

  it("starts over when closed and opened again", async () => {
    const user = setup();
    const { onClose, reopen } = renderDialog();
    await user.click(screen.getByRole("button", { name: "Next: enter the code" }));
    await user.click(screen.getByLabelText("Connection code"));
    await user.paste("AB1");
    await user.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalled();
    reopen(false);
    reopen(true);
    expect(await screen.findByRole("dialog")).toHaveTextContent("Step 1 of 2");
    await user.click(screen.getByRole("button", { name: "Next: enter the code" }));
    expect(screen.getByLabelText("Connection code")).toHaveValue("");
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/clipboard.test.ts src/components/server-creation-dialog.test.tsx`
Expected: FAIL — `@/lib/clipboard` does not exist; the dialog has no "Add a server" title, no steps, and shows organization chips.

- [ ] **Step 3: Implement**

Create `web/src/lib/clipboard.ts` (skip if Step 1's check found one):

```ts
// copyText puts text on the clipboard. Browsers offer navigator.clipboard
// only on secure origins (https, localhost); a home server reached over
// plain http on the LAN gets the legacy selection-based copy instead, which
// throws when the browser refuses that too.
export async function copyText(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.setAttribute("readonly", "");
  ta.style.position = "fixed";
  ta.style.opacity = "0";
  document.body.appendChild(ta);
  ta.select();
  try {
    if (typeof document.execCommand !== "function" || !document.execCommand("copy")) {
      throw new Error("copy rejected");
    }
  } finally {
    ta.remove();
  }
}
```

Replace the whole of `web/src/components/ui/code-input.tsx` with (this is the project's own wrapper, not a shadcn component; only the Add server dialog uses it):

```tsx
import { REGEXP_ONLY_DIGITS_AND_CHARS } from "input-otp"

import {
  InputOTP,
  InputOTPGroup,
  InputOTPSeparator,
  InputOTPSlot,
} from "@/components/ui/input-otp"
import { cn } from "@/lib/utils"

type CodeInputProps = {
  length?: number
  value: string
  onChange: (value: string) => void
  className?: string
  disabled?: boolean
  autoFocus?: boolean
  // id names the input for a <label htmlFor>.
  id?: string
}

// CodeInput is a pairing-code field: letters and digits (codes are A–Z0–9),
// a text keyboard with capitals on phones, the value uppercased, split
// 3 + 3 into slots that fit a 375px phone.
export function CodeInput({
  length = 6,
  value,
  onChange,
  className,
  disabled = false,
  autoFocus = false,
  id,
}: CodeInputProps) {
  const normalizedValue = value.toUpperCase()
  const handleChange = (nextValue: string) => {
    onChange(nextValue.toUpperCase())
  }
  const firstSegmentLength = Math.min(3, length)
  const secondSegmentLength = Math.max(length - firstSegmentLength, 0)
  const slotClassName = (slotIndex: number) =>
    cn(
      "h-12 w-10 border border-input bg-background text-xl font-semibold tracking-widest sm:h-16 sm:w-14 sm:text-2xl",
      slotIndex === 0 || slotIndex === length - 1
        ? "rounded-xl sm:rounded-2xl"
        : "rounded-lg sm:rounded-xl"
    )

  return (
    <InputOTP
      id={id}
      maxLength={length}
      value={normalizedValue}
      onChange={handleChange}
      pattern={REGEXP_ONLY_DIGITS_AND_CHARS}
      inputMode="text"
      autoCapitalize="characters"
      autoCorrect="off"
      spellCheck={false}
      // A pairing code is no password: skip input-otp's password-manager
      // badge handling (it polls the layout every second while mounted).
      pushPasswordManagerStrategy="none"
      disabled={disabled}
      autoFocus={autoFocus}
      containerClassName={cn(
        "w-full flex items-center justify-center gap-2 sm:gap-4",
        className
      )}
    >
      <InputOTPGroup className="flex gap-2 sm:gap-3 sm:px-2">
        {Array.from({ length: firstSegmentLength }).map((_, index) => (
          <InputOTPSlot
            key={`code-slot-first-${index}`}
            index={index}
            className={slotClassName(index)}
          />
        ))}
      </InputOTPGroup>

      {secondSegmentLength > 0 && (
        <>
          <InputOTPSeparator className="text-2xl font-bold text-muted-foreground sm:text-3xl">
            -
          </InputOTPSeparator>

          <InputOTPGroup className="flex gap-2 sm:gap-3 sm:px-2">
            {Array.from({ length: secondSegmentLength }).map((_, index) => {
              const overallIndex = firstSegmentLength + index
              return (
                <InputOTPSlot
                  key={`code-slot-second-${index}`}
                  index={overallIndex}
                  className={slotClassName(overallIndex)}
                />
              )
            })}
          </InputOTPGroup>
        </>
      )}
    </InputOTP>
  )
}
```

(The vendored `InputOTPSeparator` draws its own `Minus` icon whatever its children are; the `-` child is kept as before so the separator renders exactly as it did — only its size classes change.)

Replace the whole of `web/src/components/server-creation-dialog.tsx` with:

```tsx
import { useState, type FormEvent } from "react";
import { Check, Copy, ExternalLink } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { CodeInput } from "@/components/ui/code-input";
import { Label } from "@/components/ui/label";
import { Disclosure } from "@/components/disclosure";
import { InlineError } from "@/components/inline-error";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { apiBase } from "@/config";
import { createJsonClient, jsonPost } from "@/lib/api";
import { copyText } from "@/lib/clipboard";

// INSTALL_COMMAND installs Winterflow on a new server; when it finishes it
// shows the 6-character code that connects the server here.
const INSTALL_COMMAND = "curl -fsSL https://get.winterflow.io/agent | sudo bash";
const CODE_LENGTH = 6;

type Provider = { name: string; url: string; logo: string; note: string };

// PROVIDERS are places to rent a server, for someone who has none yet.
const PROVIDERS: Provider[] = [
  { name: "Hetzner", url: "https://hetzner.cloud/?ref=qo1lNfZb6s3O", logo: "/partners/hetzner.svg", note: "affordable servers in Europe and the US" },
  { name: "DigitalOcean", url: "https://m.do.co/c/73a0e59acb28", logo: "/partners/digitalocean.svg", note: "simple pricing, easy to start" },
  { name: "Vultr", url: "https://www.vultr.com/?ref=9077581", logo: "/partners/vultr.svg", note: "many locations worldwide" },
  { name: "AWS", url: "https://aws.amazon.com", logo: "/partners/aws.svg", note: "Amazon's cloud, for larger setups" },
  { name: "Azure", url: "https://azure.microsoft.com", logo: "/partners/azure.svg", note: "Microsoft's cloud" },
];

// codeProblem turns the API's refusal of a code into plain words.
function codeProblem(message: string): string {
  if (message === "invalid registration code") {
    return "That code doesn't match a server waiting to connect. Check it and try again.";
  }
  if (message === "registration code has expired") {
    return "That code has expired. Get a new code from the server and try again.";
  }
  return message;
}

type Props = {
  isOpen: boolean;
  onClose: () => void;
  onServerAdded?: () => void;
};

// ServerSelectionDialog adds a server (distributed only: the standalone
// build runs one embedded server and never mounts it). Step 1 installs
// Winterflow on the machine with one command; step 2 enters the code the
// install shows. The API adds the server to the caller's own organization.
export function ServerSelectionDialog({ isOpen, onClose, onServerAdded }: Props) {
  const [step, setStep] = useState<1 | 2>(1);
  const [code, setCode] = useState("");
  const [copied, setCopied] = useState<"" | "yes" | "failed">("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  // reset puts the dialog back at step 1 for the next time it opens.
  const reset = () => {
    setStep(1);
    setCode("");
    setCopied("");
    setError("");
  };

  const close = () => {
    if (busy) return;
    reset();
    onClose();
  };

  const copy = async () => {
    try {
      await copyText(INSTALL_COMMAND);
      setCopied("yes");
    } catch {
      setCopied("failed");
    }
  };

  const submit = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (code.length !== CODE_LENGTH || busy) return;
    setBusy(true);
    setError("");
    try {
      const { server } = await createJsonClient(apiBase).call<{ server: { id: string; name: string } }>(
        "/api/v1/server/register",
        jsonPost({ code }),
      );
      toast.success(server.name ? `Added ${server.name}` : "Server added");
      reset();
      onServerAdded?.();
      onClose();
    } catch (err) {
      setError(codeProblem(err instanceof Error ? err.message : "Couldn't add the server"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <ResponsiveDialog
      open={isOpen}
      onOpenChange={(open) => {
        if (!open) close();
      }}
      title="Add a server"
      description={step === 1 ? "Step 1 of 2 · Install Winterflow on the server" : "Step 2 of 2 · Connect it"}
      className="sm:max-w-lg"
      footer={
        step === 1 ? (
          <>
            <Button variant="outline" className="max-sm:min-h-10" onClick={close}>
              Cancel
            </Button>
            <Button className="max-sm:min-h-10" onClick={() => setStep(2)}>
              Next: enter the code
            </Button>
          </>
        ) : (
          <>
            <Button
              variant="outline"
              className="max-sm:min-h-10"
              disabled={busy}
              onClick={() => {
                setError("");
                setStep(1);
              }}
            >
              Back
            </Button>
            <Button
              type="submit"
              form="add-server-form"
              className="max-sm:min-h-10"
              disabled={busy || code.length !== CODE_LENGTH}
            >
              {busy ? "Adding…" : "Add server"}
            </Button>
          </>
        )
      }
    >
      {step === 1 ? (
        <div className="grid gap-4 text-sm">
          <p className="text-muted-foreground">
            Run this command on the machine you want to add. It works on Ubuntu 22.04 or newer and Debian 12 or newer,
            with at least 1 CPU and 2 GB of memory.
          </p>
          <div className="flex items-start gap-2 rounded-lg border bg-muted/50 p-2">
            <pre
              data-testid="install-command"
              className="min-w-0 flex-1 select-all whitespace-pre-wrap break-all p-1 font-mono text-xs"
            >
              {INSTALL_COMMAND}
            </pre>
            <Button variant="ghost" size="sm" className="min-h-10 shrink-0 sm:min-h-8" onClick={() => void copy()}>
              {copied === "yes" ? <Check className="size-4" /> : <Copy className="size-4" />}
              {copied === "yes" ? "Copied" : "Copy"}
            </Button>
          </div>
          {copied === "failed" ? (
            <p role="status" className="text-muted-foreground">
              Couldn't copy it here. Select the command and copy it yourself.
            </p>
          ) : null}
          <p className="text-muted-foreground">
            When it finishes, it shows a 6-character code. You enter that code in the next step.
          </p>
          <Disclosure label="Don't have a server yet?">
            <p className="text-muted-foreground">Rent a small one from a provider, then run the command on it.</p>
            <ul className="grid gap-1">
              {PROVIDERS.map((p) => (
                <li key={p.name}>
                  <a
                    href={p.url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="flex min-h-10 items-center gap-3 rounded-md px-2 py-1.5 transition-colors duration-150 hover:bg-muted motion-reduce:transition-none"
                  >
                    <img src={p.logo} alt="" className="size-5 shrink-0 object-contain" />
                    <span className="min-w-0 flex-1">
                      <span className="font-medium">{p.name}</span>{" "}
                      <span className="text-muted-foreground">· {p.note}</span>
                    </span>
                    <ExternalLink className="size-4 shrink-0 text-muted-foreground" aria-hidden />
                  </a>
                </li>
              ))}
            </ul>
          </Disclosure>
        </div>
      ) : (
        <form id="add-server-form" className="grid gap-4 text-sm" onSubmit={(e) => void submit(e)}>
          <p className="text-muted-foreground">Enter the 6-character code the install command showed on the server.</p>
          <div className="grid gap-2">
            <Label htmlFor="server-code">Connection code</Label>
            <CodeInput
              id="server-code"
              length={CODE_LENGTH}
              value={code}
              onChange={(v) => {
                setCode(v);
                setError("");
              }}
              autoFocus
              disabled={busy}
            />
          </div>
          {error ? <InlineError icon>{error}</InlineError> : null}
        </form>
      )}
    </ResponsiveDialog>
  );
}
```

Delete the mock organization modal:

```bash
git rm web/src/components/create-organization-modal.tsx
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/clipboard.test.ts src/components/server-creation-dialog.test.tsx src/layouts src/pages/home.test.tsx`
Expected: PASS (the shell and the Apps page mount or mock the dialog by the same name and props).

Run: `grep -rn "create-organization-modal\|CreateOrganizationModal\|OrganizationPreview\|isStandalone" web/src/components/server-creation-dialog.tsx; grep -rn "create-organization-modal" web/src`
Expected: no output.

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/clipboard.ts web/src/lib/clipboard.test.ts web/src/components/ui/code-input.tsx web/src/components/server-creation-dialog.tsx web/src/components/server-creation-dialog.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): Add server as a two-step dialog without the mock organizations

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: Phase verification

**Files:** none changed unless a check fails (then fix in the owning task's files and commit as `fix(web): …` with the `Co-Authored-By` line).

- [ ] **Step 1: The whole web suite, lint, types, build**

```bash
pnpm --dir web run lint
pnpm --dir web exec tsc -b
pnpm --dir web run test:coverage
pnpm --dir web run build && git checkout -- web/dist/.gitkeep
```

Expected: all pass; `git status --short web/dist` prints nothing.

- [ ] **Step 2: Diff coverage of the changed web lines**

```bash
go run ./tools/diffcov --base main --min 80 --lcov web/coverage/lcov.info
```

Expected: web changed lines ≥ 80% covered. If a file falls short, add the missing case to that task's test file (the uncovered lines are listed), commit, re-run.

- [ ] **Step 3: No old wording left in the template UI, nothing outside `web/` and `docs/`**

```bash
grep -rnE 'r\{[a-z]|`r\$\{|Revision note|Upgrade all|Upgrade to|Upgraded|First revision|"Add template"' \
  web/src/pages/org-templates.tsx web/src/pages/template-page.tsx web/src/components/template-*.tsx \
  web/src/components/upgrade-instance-dialog.tsx web/src/components/rollout-strip.tsx web/src/lib/rollouts.ts
git diff --stat main -- . ':!web' ':!docs'
```

Expected: no output from either.

- [ ] **Step 4: Browser check on a throwaway instance**

Standalone (sign-in and templates): build and run `bin/standalone` against a scratch data directory (`make build`; `DATABASE_URL=sqlite://…`, `AGENT_DATA_DIR` in a temp dir). Check at 1280, 768 and 375 px, light and dark:
- A fresh instance: opening `/` lands on "Set up Winterflow" (no "Log in" link); after creating the account you are signed in. Log out: `/login` stays on "Welcome back". At 375px no illustration; with the OS set to reduce motion, no falling snowflakes at 1280px.
- `/templates`: cards with "Used by N apps" / "Updated …"; New template opens the editor with "← Templates".
- A template with two parameters: rows show Name + Default; Details opens Description / Pattern / Secret / Required; a closed row says "Secret · Required" when set; at 375px the default sits under the name and nothing scrolls sideways.
- Save → "Saved version 2"; tabs Editor · Apps using it · Versions; `/templates/<id>?tab=instances` opens Apps using it; an app on version 1 shows "Version 1 (latest is 2)" with Update; "Update all apps" confirms and shows the progress strip; ⋯ → Delete template is disabled while apps use it.

Distributed (Add server): run `bin/api` + `bin/hub` + an agent as in `README`/`.env.example`, sign in, and open Add server from the server switcher (and from ⌘K) at 1280 and 375 px: step 1 shows the command with Copy (on `http://<LAN IP>` too), providers only after "Don't have a server yet?"; step 2 takes the agent's code typed in lowercase on the phone keyboard; a wrong code reads "That code doesn't match…"; the right one adds the server and closes. At 375px it is a bottom sheet and the six code slots fit.

Take screenshots of the Templates list, the template page (each tab), the setup screen and both Add server steps at 1280 and 375 px for the PR description. Stop the instances and delete the scratch directories.

- [ ] **Step 5: Commit any fixes**

```bash
git status --short
```

Expected: clean (or only the fixes, committed with `fix(web): …` and the `Co-Authored-By` line).
