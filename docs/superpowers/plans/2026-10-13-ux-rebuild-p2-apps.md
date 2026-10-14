# UX rebuild, phase 2: Apps page and app page — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild the Apps page (`/server/:id`) and the app page (`/server/:id/app/:appId`) per spec §3: a one-job Apps page (header with counts, one New app, a health strip for this server, a firing-alerts line, plain app cards, a filter, an empty state) and an app page with a calm header (status, address, one contextual primary + "⋯"), banners only when relevant, and the tabs Overview · Logs · Configuration · Backups · Metrics · Settings, with History as a side sheet.

**Architecture:** Pure helpers in `web/src/lib/` (`app-summary.ts`, `app-details-save.ts`, additions to `app-status.ts` and `backups.ts`) carry the decisions and are tested directly. The app page is split into `web/src/components/app-page/` (header, overview, logs tab, history sheet, settings tab; the Configuration switch stays in the page, where phase 3 expects it) so each part has one job; `web/src/pages/app-details.tsx` only wires them. App-wide actions that several places trigger (Back up now from the header, ⌘K and Overview; Delete and Save as template from the header and Settings) live in hooks (`use-run-backup.ts`, `use-app-actions.ts`), each with one implementation. The History sheet's open state is in the URL (`?history=1`) so links and the old `?tab=history` open it.

**Tech Stack:** React 19, React Router 7, TypeScript 5.9 (strict, `verbatimModuleSyntax`, `erasableSyntaxOnly`), Vite 7, Tailwind 4, vendored shadcn/ui (Radix Sheet, DropdownMenu, AlertDialog, Collapsible; vaul Drawer), Vitest 5 + Testing Library on jsdom. No new dependency, no Go change.

**Spec:** `docs/superpowers/specs/2026-10-13-ux-rebuild-design.md` — this plan is phase 2 of §8 ("Apps page + app page (Overview, tabs, header, history sheet)"), implementing §3 "Apps page and app page" and the "Back up now" palette action of §2. The "New app" chooser of §4 (dialog with three cards: `NewAppButton`/`NewAppChoices`) is phase 3's: phase 2's Apps page has one primary **New app** link to `newAppPath(serverId)` that phase 3 swaps for its `NewAppButton`.

**Base:** branch `feat/ux-rebuild` after phase 1 (`docs/superpowers/plans/2026-10-13-ux-rebuild-p1-shell.md`) has landed. Every file this plan edits is described **as phase 1 leaves it**: when a step quotes code from `app-details.tsx`, `home.tsx` or their tests, it is the phase-1 version (phase 1 Tasks 6, 10, 12, 13 changed them; grep that plan for the exact text if in doubt). All commands run from the worktree root.

## Global Constraints

- The spec is binding. Phase 2 does **not** redesign the Configuration tab body or the editor (`AppEditorPanel`, `AppEditor`, `InstancePanel` stay as they are; phase 3), the New app chooser (`NewAppButton`/`NewAppChoices`) and the new-app forms behind `/server/:id/new` (phase 3), Monitoring/Inbox (phase 4), the Settings area pages (phase 5), or the template pages and `UpgradeInstanceDialog`'s copy (phase 6).
- Frontend only: no Go change, no API change; `api/openapi.yaml` stays untouched. No feature is removed: History, Save as template, Detach, Delete (with volumes/backups), roll back, Deploy draft, Upgrade, Back up now, restore, the KPI sparklines and the alert rules all stay reachable.
- Never delete anything under `web/src/components/ui/` (vendored shadcn/ui).
- Use phase 1's primitives, never a second copy: `PageHeader` (`@/components/page-header`), `StatusPill`/`StatusDot` (`@/components/status`) with `appStatus`/`backupStatus`/`serverStatus` (`@/lib/status`), `ConfirmDialog` (`@/components/confirm-dialog`), `ResponsiveDialog` (`@/components/responsive-dialog`), `EmptyState` (`@/components/empty-state`), `Section`/`RowList` (`@/components/section`), `Disclosure` (`@/components/disclosure`), `UnderlineTabs` + `useTabParam`, `usePageTitle`, `usePaletteActions`, and the route builders of `@/lib/routes` (`appPath`, `appSub`, `newAppPath`, `monitoringPath`, `templatePath`, `NEW_TEMPLATE_PATH`, `INBOX_PATH`, `LEGACY_APP_TABS`, `type AppTab`, `type NewAppSource`).
- Status colours are theme tokens (`bg-status-ok|warn|bad|idle`, `text-status-*`, `border-status-*/30`); no new hard-coded status colours (`bg-emerald-500`, `text-amber-700`, …) in code this plan writes.
- Type scale: page title 24/600, section/widget title 15/600 (`text-[15px] font-semibold`), body 14 (`text-sm`); 4/8 spacing; transitions `duration-150` with `motion-reduce:` off for anything that animates. The app page is full width (phase 1's `isWidePath`); the Apps page is capped at 1100px by the shell.
- Plain words (spec §1.3): "Pull latest images" (never "Update" for `control … update`), "Detach from template" (never "Unlink"), "Version N" / "Saved 2h ago" (never "Revision N" / "r7") in text this plan writes, "No public address", "Restore…", "Edit…". Exception: `UpgradeInstanceDialog` keeps its "Upgrade to rN" copy until phase 6 relabels the template pages.
- No `window.confirm`; destructive confirms (Delete app, roll back, Detach) go through `ConfirmDialog`; menus are shadcn `DropdownMenu`; internal links are router `<Link>`s; external app addresses are `<a target="_blank" rel="noreferrer">`.
- Mobile: tap targets ≥ 40px in the app header actions and row menus (`size-10` / `min-h-10` below `sm`); the app header stacks (`flex-col` → `sm:flex-row`); Overview is one column below `lg`; tabs scroll inside themselves (`UnderlineTabs`).
- The API reports no container start time: the Overview's Status widget shows the state, the containers and their problems, but no "since" time (spec §3 "since when" needs an API field → follow-up, noted in the PR).
- Agent round trips are the expensive unit: the Overview reads the app once for its backups (`getApp`) and its history once (`getRevisions`); the alert rules on the Metrics tab load only when "Alert rules for this app" is opened; nothing polls that did not poll before.
- i18n: plain English literals like every existing page (only auth strings go through i18next).
- Lint (`eslint-plugin-react-hooks` 7): no `Date.now()`/`Math.random()` during render (use `useNow()` or call helpers whose default argument reads the clock), no `ref.current` reads during render; files exporting components export only components (hooks in `use-*.ts`, helpers in `lib/`).
- TypeScript: `import type` for types, no `enum`, no unused locals/imports — when a step removes the last use of an import, remove the import.
- Tests: Vitest + Testing Library beside the code; mock contexts the way existing tests do (`vi.mock("@/context/use-servers", …)`, `AppsContext.Provider value={… as unknown as AppsContextValue}`); answer fetches with `fakeAPI` (`@/test/fake-api`); keep existing `data-testid`s where the element survives (`kpi-*`, `firing-badge`, `chart-*`, `alerts-card`).
- Coverage: changed web lines ≥ 80% (`tools/diffcov`).
- `pnpm --dir web run build` empties `web/dist`: run `git checkout -- web/dist/.gitkeep` after any build, before committing.
- Per task, before the commit: the task's Vitest files, `pnpm --dir web run lint`, `pnpm --dir web exec tsc -b`.
- Commits: Conventional Commits (`feat(web): …`, `refactor(web): …`), each message ending with the line `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Changing an app's icon, colour or description in Settings while the app is stopped, or while it has an undeployed draft.** Expected: the change is saved, but the stopped app is not started and nobody's draft is deployed (the save goes in as a draft then); a running app with nothing pending is saved and redeployed in place; a name-only change uses the rename route and redeploys nothing. Pinned in Task 12 (`app-details-save.test.ts`).
2. **A template-managed app's Settings → General.** The API answers 409 to `save-app` on such an app. Expected: the name can still be changed (rename is allowed), the icon/colour/description fields are disabled with "Icon, colour and description are set by template X.", and Save never sends `save-app`. Pinned in Task 12 (`app-settings-tab.test.tsx`).
3. **Old and emailed app links:** `?tab=history` (bookmarks), `?tab=editor`, and the Go alert e-mails' `?tab=metrics`. Expected: `?tab=history` opens Configuration with the History sheet open and the URL becomes `?tab=configuration&history=1` in one replace; closing the sheet drops `history`; `?tab=metrics` still opens Metrics; no tab parameter opens Overview. Pinned in Task 10 (`use-history-sheet.test.tsx`) and Task 14 (`app-details.test.tsx`).
4. **"Back up now" from the header menu or ⌘K on an app with no backup set up, with only a custom backup, or while a backup or restore runs.** Expected: nothing is sent; a toast says there is nothing to back up yet (and where to set one up); while anything runs the action is disabled, so a second click never queues a duplicate. Pinned in Task 8 (`use-run-backup.test.tsx`) and Task 14 (menu item disabled while busy).
5. **Moving from one app to another while the app page stays mounted** (sidebar link, ⌘K). Expected: Overview, History, Settings and Logs show only the new app's data — never the previous app's history, description or backups. Pinned in Task 14 (`key={app.id}` on every per-app part, with a test that switches apps).

## File map

Created:

| File | Responsibility |
|---|---|
| `web/src/lib/app-summary.ts` | app addresses, counts, Apps-page subtitle, the card's one warning, filter, container hints |
| `web/src/components/server-health-strip.tsx` | `ServerHealthStrip` — this server's CPU/Memory/Disk line or its offline state |
| `web/src/components/app-page/ownership.ts` | `type Ownership` (the template side of an app) |
| `web/src/components/app-page/app-logs-tab.tsx` | `AppLogsTab` |
| `web/src/components/app-page/history-panel.tsx` | `HistoryPanel` (the versions list) |
| `web/src/components/app-page/history-sheet.tsx` | `HistorySheet` |
| `web/src/components/app-page/app-settings-tab.tsx` | `AppSettingsTab` (General, Template, Danger zone) |
| `web/src/components/app-page/app-overview.tsx` | `AppOverview` (Status, Access, Resources, Backups, Recent changes) |
| `web/src/components/app-page/app-header.tsx` | `AppHeader` (identity, status, address, chips, primary + ⋯) |
| `web/src/hooks/use-run-backup.ts` | `useRunBackup`, `useBackUpNow` |
| `web/src/hooks/use-app-revisions.ts` | `useAppRevisions` |
| `web/src/hooks/use-history-sheet.ts` | `useHistorySheet` (`?history=1`) |
| `web/src/hooks/use-app-actions.ts` | `useDeleteApp`, `useSaveAsTemplate` |
| `web/src/lib/app-details-save.ts` | `saveAppDetails` (rename + metadata save, draft rule) |

Deleted: `web/src/components/server-cards.tsx`, `web/src/components/server-cards.test.tsx` (the multi-server cards leave the Apps page; spec §3).

---

### Task 1: Containers in the apps context

The status report already carries each app's containers (`AppStatus.containers` in `api/openapi.yaml`); the web drops them. The Overview's Status widget needs them.

**Files:**
- Modify: `web/src/lib/app-status.ts`, `web/src/lib/app-status.test.ts`
- Modify: `web/src/context/apps-context-base.ts`
- Modify: `web/src/context/apps-context.tsx`, `web/src/context/apps-context.test.tsx`

**Interfaces:**
- Consumes: nothing new.
- Produces:
  - `type ContainerStatusWire = { container_id: string; name: string; status_code: number; exit_code: number; error?: string }` (`@/lib/app-status`).
  - `splitStatus(apps)` now returns `{ status, stale, backups, containers: Record<string, ContainerStatusWire[]> }` (apps with at least one container only).
  - `AppsContextValue.containersByApp: Record<string, ContainerStatusWire[]>` (live, from `get-apps-status` and every `apps_status` push).

- [ ] **Step 1: Write the failing tests**

In `web/src/lib/app-status.test.ts`, in `describe("splitStatus", …)` change both `toEqual({ … backups: … })` expectations to also expect `containers: {}`:

```ts
    ).toEqual({ status: { a: 1, b: 5, c: 2, d: 1 }, stale: { a: ["SMTP_PASS"] }, backups: {}, containers: {} });
    expect(splitStatus(null)).toEqual({ status: {}, stale: {}, backups: {}, containers: {} });
```

```ts
    ).toEqual({ status: { a: 1, b: 1, c: 1 }, stale: { a: ["X"] }, backups: { a: health }, containers: {} });
```

and add inside the same `describe`:

```ts
  it("keeps the containers of apps that report some", () => {
    const web = { container_id: "c1", name: "shop-web-1", status_code: 1, exit_code: 0 };
    expect(
      splitStatus([
        { app_id: "a", status_code: 1, containers: [web] },
        { app_id: "b", status_code: 5, containers: [] },
        { app_id: "c", status_code: 0, containers: null },
      ]).containers,
    ).toEqual({ a: [web] });
  });
```

In `web/src/context/apps-context.test.tsx`, give `a1` a container in the `get-apps-status` answer of `fetchMock`:

```ts
          { app_id: "a1", status_code: 1, backups: [{ name: "db", state: "late", last_success: 1 }], containers: [{ container_id: "c1", name: "shop-web-1", status_code: 1, exit_code: 0 }] },
```

and add inside `describe("AppsProvider backups", …)`:

```ts
  it("keeps each app's containers from the status and its pushes", async () => {
    const { result } = renderHook(() => useApps(), { wrapper });
    await waitUntil(() =>
      expect(result.current.containersByApp).toEqual({
        a1: [{ container_id: "c1", name: "shop-web-1", status_code: 1, exit_code: 0 }],
      }),
    );
    const crashed = { container_id: "c2", name: "blog-db-1", status_code: 4, exit_code: 1, error: "out of memory" };
    act(() => {
      for (const h of handlers) {
        h({ type: "apps_status", payload: { server_id: "srv-1", apps: [{ app_id: "a2", status_code: 4, containers: [crashed] }] } });
      }
    });
    await waitUntil(() => expect(result.current.containersByApp).toEqual({ a2: [crashed] }));
  });
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/app-status.test.ts src/context/apps-context.test.tsx`
Expected: FAIL — `containers` is missing from `splitStatus`'s result; `containersByApp` is undefined.

- [ ] **Step 3: Implement**

In `web/src/lib/app-status.ts`, above `AppStatusWire` add:

```ts
// ContainerStatusWire is one container of an app in a status report
// (command.ContainerStatus): its compose name, its own status code (the
// AppStatusCode values), and why it stopped when it did.
export type ContainerStatusWire = {
  container_id: string;
  name: string;
  status_code: number;
  exit_code: number;
  error?: string;
};
```

add the field to `AppStatusWire` (after `backups`):

```ts
  // The app's containers (none reported: absent or null).
  containers?: ContainerStatusWire[] | null;
```

and replace `splitStatus` with:

```ts
// splitStatus turns a report into the apps context's maps: container
// status codes, the stale shared names of stale apps only, the backup
// health of apps with backups only, and the containers of apps that report
// some.
export function splitStatus(apps: AppStatusWire[] | null | undefined): {
  status: Record<string, number>;
  stale: Record<string, string[]>;
  backups: Record<string, BackupHealth[]>;
  containers: Record<string, ContainerStatusWire[]>;
} {
  const status: Record<string, number> = {};
  const stale: Record<string, string[]> = {};
  const backups: Record<string, BackupHealth[]> = {};
  const containers: Record<string, ContainerStatusWire[]> = {};
  for (const a of apps ?? []) {
    status[a.app_id] = a.status_code;
    if (a.stale_vars && a.stale_vars.length > 0) stale[a.app_id] = a.stale_vars;
    if (a.backups?.length) backups[a.app_id] = a.backups;
    if (a.containers?.length) containers[a.app_id] = a.containers;
  }
  return { status, stale, backups, containers };
}
```

In `web/src/context/apps-context-base.ts` add `import type { ContainerStatusWire } from "@/lib/app-status";` and, in `AppsContextValue` after `backupsByApp`:

```ts
  // containersByApp maps app id -> its containers in the latest report
  // (apps that report containers only; live, with the status).
  containersByApp: Record<string, ContainerStatusWire[]>;
```

In `web/src/context/apps-context.tsx`:
1. change the import to `import { splitStatus, type AppStatusWire, type ContainerStatusWire } from "@/lib/app-status";`
2. after `const [backupsByApp, setBackupsByApp] = …` add `const [containersByApp, setContainersByApp] = useState<Record<string, ContainerStatusWire[]>>({});`
3. in `loadStatus`'s reset branch add `setContainersByApp({});` after `setBackupsByApp({});`
4. in both places that call `splitStatus` (the `get-apps-status` read and the `apps_status` subscription) destructure `containers` too and add `setContainersByApp(keepIfSame(containers));` after `setBackupsByApp(keepIfSame(backups));`
5. add `containersByApp` to the `value` object (after `backupsByApp`) and to its dependency array (after `backupsByApp`).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/app-status.test.ts src/context/apps-context.test.tsx src/context/apps-context-vars.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/app-status.ts web/src/lib/app-status.test.ts web/src/context/apps-context-base.ts web/src/context/apps-context.tsx web/src/context/apps-context.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): keep each app's containers from the status report

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: App summary helpers

The Apps page and the app page share a handful of small decisions: where an app lives (its address), how many apps run, the one warning a card shows, the filter, and what to say about a broken container. They are pure and tested here.

**Files:**
- Create: `web/src/lib/app-summary.ts`, `web/src/lib/app-summary.test.ts`
- Modify: `web/src/lib/backups.ts`, `web/src/lib/backups.test.ts`

**Interfaces:**
- Consumes: `App`, `AppDomain`, `AppStatusCode` (`@/context/apps-context-base`), `ContainerStatusWire` (Task 1), `BackupHealth` (`@/lib/backups`), `AppTab` (`@/lib/routes`).
- Produces (`@/lib/app-summary`):
  - `type AppAddress = { host: string; url: string; https: boolean }`
  - `appAddresses(app: Pick<App, "domains">): AppAddress[]` — the app's routes (never its redirects), in API order.
  - `primaryAddress(app: Pick<App, "domains">): AppAddress | null`
  - `appTitle(app: Pick<App, "name">): string` — the name, or "Unnamed app" (never a raw id).
  - `type AppCounts = { running: number; problem: number; stopped: number }`; `countApps(apps: Pick<App, "id">[], statusByApp: Record<string, number>): AppCounts`
  - `appsSubtitle(serverName: string, total: number, counts: AppCounts): string` — "homelab · 3 running · 1 stopped".
  - `type AppWarning = { label: string; tab?: AppTab }`; `appWarning(health: BackupHealth[] | undefined, staleVars: string[] | undefined): AppWarning | null`
  - `FILTER_THRESHOLD = 8`; `filterApps<T extends Pick<App, "name" | "domains">>(apps: T[], query: string): T[]`
  - `containerHint(c: ContainerStatusWire): string`
- Produces (`@/lib/backups`): `runnableBackups(list: EffectiveBackup[]): EffectiveBackup[]` — on, with a storage, not custom.

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/app-summary.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import {
  appAddresses,
  appsSubtitle,
  appTitle,
  appWarning,
  containerHint,
  countApps,
  filterApps,
  primaryAddress,
} from "@/lib/app-summary";

const shop = {
  name: "Shop",
  domains: [
    { domain: "old.example.com", ssl: true, kind: "redirect" as const },
    { domain: "shop.example.com", ssl: true, kind: "route" as const },
    { domain: "shop.lan", ssl: false, kind: "route" as const },
  ],
};

describe("addresses", () => {
  it("lists the routes, never the redirects, with the scheme TLS gives", () => {
    expect(appAddresses(shop)).toEqual([
      { host: "shop.example.com", url: "https://shop.example.com", https: true },
      { host: "shop.lan", url: "http://shop.lan", https: false },
    ]);
    expect(primaryAddress(shop)?.url).toBe("https://shop.example.com");
  });

  it("has no address without routes", () => {
    expect(primaryAddress({ domains: undefined })).toBeNull();
    expect(primaryAddress({ domains: [{ domain: "x.example.com", ssl: true, kind: "redirect" }] })).toBeNull();
  });
});

describe("appTitle", () => {
  it("names an app not reconciled yet without its id", () => {
    expect(appTitle({ name: "Shop" })).toBe("Shop");
    expect(appTitle({ name: "  " })).toBe("Unnamed app");
  });
});

describe("counts", () => {
  it("counts running, problem and stopped apps, leaving unknown ones out", () => {
    const apps = ["a", "b", "c", "d", "e", "f"].map((id) => ({ id }));
    // Active, Idle, Restarting run; Problematic is a problem; Stopped; Unknown (missing).
    expect(countApps(apps, { a: 1, b: 2, c: 3, d: 4, e: 5 })).toEqual({ running: 3, problem: 1, stopped: 1 });
  });

  it("says the server and the counts, skipping zeros except running", () => {
    expect(appsSubtitle("homelab", 4, { running: 3, problem: 0, stopped: 1 })).toBe("homelab · 3 running · 1 stopped");
    expect(appsSubtitle("homelab", 2, { running: 0, problem: 2, stopped: 0 })).toBe("homelab · 0 running · 2 with problems");
    expect(appsSubtitle("homelab", 0, { running: 0, problem: 0, stopped: 0 })).toBe("homelab · no apps yet");
  });
});

describe("appWarning", () => {
  it("names the worst backup first, then shared variables to reload", () => {
    expect(appWarning([{ name: "a", state: "late" }, { name: "b", state: "failing" }], ["X"])).toEqual({
      label: "Backup failing",
      tab: "backups",
    });
    expect(appWarning([{ name: "a", state: "late" }], undefined)).toEqual({ label: "Backup late", tab: "backups" });
    expect(appWarning([{ name: "a", state: "ok" }], ["SMTP_PASS"])).toEqual({ label: "Reload needed" });
  });

  it("is quiet when all is well", () => {
    expect(appWarning([{ name: "a", state: "ok" }, { name: "b", state: "off" }], [])).toBeNull();
    expect(appWarning(undefined, undefined)).toBeNull();
  });
});

describe("filterApps", () => {
  const apps = [shop, { name: "Blog", domains: [] }, { name: "Photos", domains: undefined }];

  it("matches names and addresses, ignoring case and spaces", () => {
    expect(filterApps(apps, "  BLO ").map((a) => a.name)).toEqual(["Blog"]);
    expect(filterApps(apps, "lan").map((a) => a.name)).toEqual(["Shop"]);
  });

  it("keeps everything for an empty query", () => {
    expect(filterApps(apps, "")).toBe(apps);
  });
});

describe("containerHint", () => {
  it("says what is wrong in a few words", () => {
    const base = { container_id: "c", name: "web", exit_code: 0 };
    expect(containerHint({ ...base, status_code: 1 })).toBe("");
    expect(containerHint({ ...base, status_code: 3 })).toBe("Keeps restarting");
    expect(containerHint({ ...base, status_code: 4, exit_code: 137 })).toBe("Stopped with exit code 137");
    expect(containerHint({ ...base, status_code: 4, error: "OOMKilled\nmore detail" })).toBe("OOMKilled");
    expect(containerHint({ ...base, status_code: 4 })).toBe("Has a problem");
  });
});
```

In `web/src/lib/backups.test.ts` add (import `runnableBackups` with the other imports from `@/lib/backups`):

```ts
describe("runnableBackups", () => {
  it("keeps the backups Back up now can run: on, with a storage, not custom", () => {
    const b = (name: string, over: Partial<EffectiveBackup>): EffectiveBackup => ({
      name,
      type: "sqlite",
      enabled: true,
      storage: "disk",
      in_recipe: true,
      from_recipe: null,
      ...over,
    });
    const list = [b("db", {}), b("off", { enabled: false }), b("nowhere", { storage: "" }), b("replica", { type: "custom" })];
    expect(runnableBackups(list).map((x) => x.name)).toEqual(["db"]);
  });
});
```

(If `EffectiveBackup` is not imported in that test file yet, add `type EffectiveBackup` to its `@/lib/backups` import.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/app-summary.test.ts src/lib/backups.test.ts`
Expected: FAIL — `@/lib/app-summary` does not exist; `runnableBackups` is not exported.

- [ ] **Step 3: Implement**

Create `web/src/lib/app-summary.ts`:

```ts
import { AppStatusCode, type App, type AppDomain } from "@/context/apps-context-base";
import type { ContainerStatusWire } from "@/lib/app-status";
import type { BackupHealth } from "@/lib/backups";
import type { AppTab } from "@/lib/routes";

// What the Apps page and the app page say about an app at a glance: where
// it lives, how many apps run, the one warning a card carries.

// AppAddress is a public address of an app: the hostname and the URL that
// opens it (https when the route has TLS).
export type AppAddress = { host: string; url: string; https: boolean };

function addressOf(d: AppDomain): AppAddress {
  return { host: d.domain, url: `${d.ssl ? "https" : "http"}://${d.domain}`, https: d.ssl };
}

// appAddresses are the app's routes in the order the API lists them; a
// redirect is not where the app lives.
export function appAddresses(app: Pick<App, "domains">): AppAddress[] {
  return (app.domains ?? []).filter((d) => d.kind === "route").map(addressOf);
}

// primaryAddress is the address the UI opens ("Open ↗"), or null.
export function primaryAddress(app: Pick<App, "domains">): AppAddress | null {
  return appAddresses(app)[0] ?? null;
}

// appTitle is an app's name for the UI; an app not reconciled yet has none
// and is never shown by its raw id.
export function appTitle(app: Pick<App, "name">): string {
  return app.name?.trim() || "Unnamed app";
}

export type AppCounts = { running: number; problem: number; stopped: number };

// countApps sorts the apps by their live status: running (active, idle,
// restarting), with a problem, stopped. An app without a known status is
// in none (the UI never infers "down" from missing data).
export function countApps(apps: Pick<App, "id">[], statusByApp: Record<string, number>): AppCounts {
  const counts: AppCounts = { running: 0, problem: 0, stopped: 0 };
  for (const a of apps) {
    const code = statusByApp[a.id];
    if (code === AppStatusCode.Active || code === AppStatusCode.Idle || code === AppStatusCode.Restarting) counts.running++;
    else if (code === AppStatusCode.Problematic) counts.problem++;
    else if (code === AppStatusCode.Stopped) counts.stopped++;
  }
  return counts;
}

// appsSubtitle is the Apps page's subtitle: "homelab · 3 running · 1
// stopped" (zero counts other than running are left out).
export function appsSubtitle(serverName: string, total: number, counts: AppCounts): string {
  const parts = [serverName];
  if (total === 0) {
    parts.push("no apps yet");
  } else {
    parts.push(`${counts.running} running`);
    if (counts.problem > 0) parts.push(`${counts.problem} with problems`);
    if (counts.stopped > 0) parts.push(`${counts.stopped} stopped`);
  }
  return parts.join(" · ");
}

// AppWarning is the one small warning an app card shows, and the tab it
// leads to (none: the app page's default tab, whose banner explains it).
export type AppWarning = { label: string; tab?: AppTab };

// appWarning picks a card's warning: a failing backup, else a late one,
// else shared variables changed since the app last read them. Backup health
// never changes the app's own status.
export function appWarning(health: BackupHealth[] | undefined, staleVars: string[] | undefined): AppWarning | null {
  const states = (health ?? []).map((h) => h.state);
  if (states.includes("failing")) return { label: "Backup failing", tab: "backups" };
  if (states.includes("late")) return { label: "Backup late", tab: "backups" };
  if ((staleVars ?? []).length > 0) return { label: "Reload needed" };
  return null;
}

// FILTER_THRESHOLD is how many apps a server has before the Apps page
// offers a filter box.
export const FILTER_THRESHOLD = 8;

// filterApps keeps the apps whose name or an address contains the query
// (case-insensitive); an empty query keeps the list as it is.
export function filterApps<T extends Pick<App, "name" | "domains">>(apps: T[], query: string): T[] {
  const q = query.trim().toLowerCase();
  if (!q) return apps;
  return apps.filter(
    (a) => (a.name ?? "").toLowerCase().includes(q) || (a.domains ?? []).some((d) => d.domain.toLowerCase().includes(q)),
  );
}

// containerHint says in a few words what is wrong with a container ("" when
// nothing is): the agent's error (first line), else its exit code.
export function containerHint(c: ContainerStatusWire): string {
  if (c.status_code === AppStatusCode.Restarting) return "Keeps restarting";
  if (c.status_code !== AppStatusCode.Problematic) return "";
  const error = c.error?.trim().split("\n")[0];
  if (error) return error;
  return c.exit_code ? `Stopped with exit code ${c.exit_code}` : "Has a problem";
}
```

In `web/src/lib/backups.ts`, after `needsAttention`, add:

```ts
// runnableBackups are the backups Back up now can run: on, with a storage,
// and run by winterflow (a custom backup's own tool runs itself).
export function runnableBackups(list: EffectiveBackup[]): EffectiveBackup[] {
  return list.filter((b) => b.enabled && !!b.storage && b.type !== "custom");
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/app-summary.test.ts src/lib/backups.test.ts`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/app-summary.ts web/src/lib/app-summary.test.ts web/src/lib/backups.ts web/src/lib/backups.test.ts
git commit -m "$(cat <<'EOF'
feat(web): app summary helpers (address, counts, card warning, filter)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 3: Server health strip and the firing-alerts lines

**Files:**
- Create: `web/src/components/server-health-strip.tsx`, `web/src/components/server-health-strip.test.tsx`
- Modify: `web/src/components/alerts/active-alerts.tsx`, `web/src/components/alerts/active-alerts.test.tsx`

**Interfaces:**
- Consumes: `serverStatus` (`@/lib/status`), `StatusDot` (`@/components/status`), `monitoringPath`, `INBOX_PATH` (`@/lib/routes`), `useMetricsOverview` (`@/hooks/use-metrics`), `useActiveAlerts` (`@/hooks/use-alerts`), `formatAge` (`@/lib/kpi`), `activeText`, `sinceText` (`@/lib/alerts`).
- Produces:
  - `ServerHealthStrip({ server }: { server: Server })` — a region named "Server health": "CPU 42% · Memory 38% · Disk 55%", or "Offline · last seen 5m ago", or "Waiting for this server to connect for the first time", or "No data yet"; always a "Monitoring" link to `monitoringPath(server.id)`.
  - `FiringAlertsLine({ serverId }: { serverId: string })` — nothing while quiet; else one link to `INBOX_PATH`: "2 alerts firing · Inbox".
  - `AppAlertsBanner({ serverId, appId }: { serverId: string; appId: string })` — nothing while quiet; else a status region named "Firing alerts" listing this app's firing alerts ("CPU 93% > 90% · 12 min") and a "View metrics" link (`?tab=metrics`). `AppAlertsBadge` stays until Task 14 removes it.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/server-health-strip.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ServerHealthStrip } from "@/components/server-health-strip";
import type { Server } from "@/context/servers-context-base";
import type { ServerOverview } from "@/lib/metrics";

let statusByServer: Record<string, "online" | "unknown"> = {};
let overview: ServerOverview[] = [];
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ statusByServer }) }));
vi.mock("@/hooks/use-metrics", () => ({
  useMetricsOverview: () => ({ data: { servers: overview }, loading: false, error: null, refresh: vi.fn() }),
}));

const server = (lastSeenAt: string | null): Server => ({
  id: "s1",
  organizationId: "o",
  name: "homelab",
  createdAt: "",
  lastSeenAt,
  capabilities: {},
});

const sample = {
  server_id: "s1",
  liveness: "online" as const,
  latest: {
    at: "2026-10-13T10:00:00Z",
    server: {
      cpu: 42,
      mem_used: 3,
      mem_total: 8,
      swap_used: 0,
      swap_total: 0,
      load1: 0.4,
      load5: 0.3,
      load15: 0.2,
      disks: [
        { mount: "/", used: 55, total: 100 },
        { mount: "/mnt/a", used: 10, total: 100 },
      ],
    },
  },
  sparkline: [],
  apps: { running: 2, stopped: 0, problematic: 0 },
};

function renderStrip(s: Server) {
  return render(
    <MemoryRouter>
      <ServerHealthStrip server={s} />
    </MemoryRouter>,
  );
}

describe("ServerHealthStrip", () => {
  beforeEach(() => {
    statusByServer = { s1: "online" };
    overview = [sample];
  });

  it("shows CPU, memory and the fullest disk now, and leads to Monitoring", () => {
    renderStrip(server("2026-10-13T10:00:00Z"));
    const strip = screen.getByRole("region", { name: "Server health" });
    expect(strip).toHaveTextContent("CPU 42%");
    expect(strip).toHaveTextContent("Memory 38%");
    expect(strip).toHaveTextContent("Disk 55%");
    expect(screen.getByRole("link", { name: "Monitoring" })).toHaveAttribute("href", "/server/s1/monitoring");
  });

  it("says when there is no data yet instead of dashes", () => {
    overview = [];
    renderStrip(server("2026-10-13T10:00:00Z"));
    expect(screen.getByRole("region", { name: "Server health" })).toHaveTextContent("No data yet");
    expect(screen.queryByText(/CPU/)).not.toBeInTheDocument();
  });

  it("says the server is offline and since when", () => {
    statusByServer = {};
    renderStrip(server(new Date(Date.now() - 5 * 60_000).toISOString()));
    const strip = screen.getByRole("region", { name: "Server health" });
    expect(strip).toHaveTextContent("Offline");
    expect(strip).toHaveTextContent("last seen 5m ago");
    expect(strip).not.toHaveTextContent("CPU");
  });

  it("says a server that never connected is still awaited", () => {
    statusByServer = {};
    renderStrip(server(null));
    expect(screen.getByRole("region", { name: "Server health" })).toHaveTextContent(
      "Waiting for this server to connect for the first time",
    );
  });
});
```

In `web/src/components/alerts/active-alerts.test.tsx`, change the import to

```tsx
import { ActiveAlertsStrip, AppAlertsBadge, AppAlertsBanner, FiringAlertsLine, FiringBadge } from "@/components/alerts/active-alerts";
```

and add at the end of the file:

```tsx
describe("FiringAlertsLine", () => {
  it("counts this server's firing alerts in one line that leads to the Inbox", async () => {
    const api = fakeAPI({ "/api/v1/alerts/get-active": firing });
    render(
      <MemoryRouter>
        <FiringAlertsLine serverId="s1" />
      </MemoryRouter>,
    );
    expect(await screen.findByRole("link", { name: /2 alerts firing/ })).toHaveAttribute("href", "/inbox");
    expect(api.calls[0].query).toBe("?server_id=s1");
  });

  it("says one alert in the singular, and nothing while all is quiet", async () => {
    const api = fakeAPI({ "/api/v1/alerts/get-active": firing.slice(0, 1) });
    const { unmount } = render(
      <MemoryRouter>
        <FiringAlertsLine serverId="s1" />
      </MemoryRouter>,
    );
    expect(await screen.findByRole("link", { name: /1 alert firing/ })).toBeInTheDocument();
    unmount();
    vi.unstubAllGlobals();
    const quiet = fakeAPI({ "/api/v1/alerts/get-active": [] });
    const { container } = render(
      <MemoryRouter>
        <FiringAlertsLine serverId="s1" />
      </MemoryRouter>,
    );
    await vi.waitFor(() => expect(quiet.calls).toHaveLength(1));
    expect(container).toBeEmptyDOMElement();
    expect(api.calls).toHaveLength(1);
  });
});

describe("AppAlertsBanner", () => {
  it("lists only this app's firing alerts, with how long, and leads to its metrics", async () => {
    const api = fakeAPI({ "/api/v1/alerts/get-active": firing });
    render(
      <MemoryRouter>
        <AppAlertsBanner serverId="s1" appId="a1" />
      </MemoryRouter>,
    );
    const banner = await screen.findByRole("status", { name: "Firing alerts" });
    expect(banner).toHaveTextContent("Down");
    expect(banner).toHaveTextContent("12 min");
    // The server's own CPU alert is not this app's.
    expect(banner).not.toHaveTextContent("CPU");
    expect(within(banner).getByRole("link", { name: "View metrics" })).toHaveAttribute("href", "/?tab=metrics");
    expect(api.calls[0].query).toBe("?server_id=s1&app_id=a1");
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/components/server-health-strip.test.tsx src/components/alerts/active-alerts.test.tsx`
Expected: FAIL — the strip, `FiringAlertsLine` and `AppAlertsBanner` do not exist.

- [ ] **Step 3: Implement**

Create `web/src/components/server-health-strip.tsx`:

```tsx
import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { ArrowRight } from "lucide-react";

import { StatusDot } from "@/components/status";
import { isStandalone } from "@/config";
import type { Server } from "@/context/servers-context-base";
import { useServers } from "@/context/use-servers";
import { useMetricsOverview } from "@/hooks/use-metrics";
import { useNow } from "@/hooks/use-now";
import { formatAge } from "@/lib/kpi";
import { formatPercent, fullestDisk, percentOf } from "@/lib/metrics";
import { monitoringPath } from "@/lib/routes";
import { serverStatus } from "@/lib/status";

// ServerHealthStrip is the Apps page's one line about this server: CPU,
// memory and the fullest disk now — or why there are no figures (offline,
// never connected, no data yet) — and the way to Monitoring. The server's
// IP, cores and agent version live in Settings → Server → General.
export function ServerHealthStrip({ server }: { server: Server }) {
  const { statusByServer } = useServers();
  const { data } = useMetricsOverview();
  const now = useNow();
  const status = serverStatus({
    liveness: statusByServer[server.id],
    lastSeenAt: server.lastSeenAt,
    standalone: isStandalone,
  });
  const latest = data?.servers?.find((s) => s.server_id === server.id)?.latest?.server;

  let body: ReactNode;
  if (status.tone === "bad") {
    const age = formatAge(now - new Date(server.lastSeenAt ?? 0).getTime());
    body = (
      <span className="inline-flex items-center gap-2">
        <StatusDot tone="bad" />
        <span className="font-medium text-status-bad">Offline</span>
        <span className="text-muted-foreground">· {age === "just now" ? "last seen just now" : `last seen ${age} ago`}</span>
      </span>
    );
  } else if (status.tone === "idle") {
    body = (
      <span className="inline-flex items-center gap-2 text-muted-foreground">
        <StatusDot tone="idle" />
        Waiting for this server to connect for the first time
      </span>
    );
  } else if (!latest) {
    body = <span className="text-muted-foreground">No data yet</span>;
  } else {
    const disk = fullestDisk(latest.disks);
    const figures: [string, number | undefined][] = [
      ["CPU", latest.cpu],
      ["Memory", percentOf(latest.mem_used, latest.mem_total)],
      ["Disk", disk ? percentOf(disk.used, disk.total) : undefined],
    ];
    body = figures.map(([label, value], i) => (
      <span key={label} className="whitespace-nowrap">
        {i > 0 ? (
          <span aria-hidden className="mr-3 text-muted-foreground/60">
            ·
          </span>
        ) : null}
        <span className="text-muted-foreground">{label}</span>{" "}
        <span className="font-medium tabular-nums">{formatPercent(value)}</span>
      </span>
    ));
  }

  return (
    <div
      role="region"
      aria-label="Server health"
      className="flex min-h-10 flex-wrap items-center gap-x-3 gap-y-1 rounded-xl border bg-card px-4 py-2 text-sm"
    >
      {body}
      <Link
        to={monitoringPath(server.id)}
        className="ml-auto inline-flex min-h-8 items-center gap-1 font-medium text-muted-foreground transition-colors duration-150 hover:text-foreground"
      >
        Monitoring
        <ArrowRight className="size-4" aria-hidden />
      </Link>
    </div>
  );
}
```

In `web/src/components/alerts/active-alerts.tsx`, change the `lucide-react` import to `import { ArrowRight, BellRing, Flame, TriangleAlert } from "lucide-react";` (keep the existing `INBOX_PATH` import phase 1 added; add it from `@/lib/routes` if missing) and add after `AppAlertsBadge`:

```tsx
// FiringAlertsLine is the Apps page's one line about the alerts firing on
// this server and its apps, leading to the Inbox. Nothing while all is
// quiet.
export function FiringAlertsLine({ serverId }: { serverId: string }) {
  const { data } = useActiveAlerts(serverId);
  if (data.length === 0) return null;
  return (
    <Link
      to={INBOX_PATH}
      className="flex min-h-10 items-center gap-2 rounded-xl border border-status-bad/30 bg-status-bad/10 px-4 py-2 text-sm font-medium text-status-bad transition-colors duration-150 hover:bg-status-bad/15"
    >
      <TriangleAlert className="size-4 shrink-0" aria-hidden />
      {data.length} alert{data.length === 1 ? "" : "s"} firing
      <span className="ml-auto inline-flex items-center gap-1 text-xs">
        Inbox <ArrowRight className="size-3.5" aria-hidden />
      </span>
    </Link>
  );
}

// AppAlertsBanner is the app page's banner while alerts fire on the app:
// what fires and for how long, and the way to its metrics. Nothing while
// all is quiet.
export function AppAlertsBanner({ serverId, appId }: { serverId: string; appId: string }) {
  const { data } = useActiveAlerts(serverId, appId);
  const now = useNow();
  const mine = data.filter((a) => a.app_id === appId);
  if (mine.length === 0) return null;
  return (
    <div
      role="status"
      aria-label="Firing alerts"
      className="flex flex-wrap items-start gap-x-3 gap-y-1 rounded-xl border border-status-bad/30 bg-status-bad/10 px-4 py-2.5 text-sm"
    >
      <TriangleAlert className="mt-0.5 size-4 shrink-0 text-status-bad" aria-hidden />
      <ul className="min-w-0 flex-1 space-y-0.5">
        {mine.map((a) => (
          <li key={`${a.rule_id}-${a.app_id}`}>
            <span className="font-medium text-status-bad">{activeText(a)}</span>
            <span className="text-muted-foreground"> · {sinceText(a.since, now)}</span>
          </li>
        ))}
      </ul>
      <Link to="?tab=metrics" className="text-sm font-medium underline-offset-4 hover:underline">
        View metrics
      </Link>
    </div>
  );
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/components/server-health-strip.test.tsx src/components/alerts/active-alerts.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/components/server-health-strip.tsx web/src/components/server-health-strip.test.tsx web/src/components/alerts/active-alerts.tsx web/src/components/alerts/active-alerts.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): server health strip, firing-alerts line and app alerts banner

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: The Apps page

**Files:**
- Modify (rewrite): `web/src/components/app-grid.tsx`, `web/src/components/app-grid.test.tsx`
- Modify (rewrite): `web/src/pages/home.tsx`, `web/src/pages/home.test.tsx`
- Delete: `web/src/components/server-cards.tsx`, `web/src/components/server-cards.test.tsx`

**Interfaces:**
- Consumes: Task 2 helpers, `ServerHealthStrip`, `FiringAlertsLine` (Task 3), `PageHeader`, `EmptyState`, `StatusPill`, `appStatus`, `appSub`, `newAppSub`, `newAppPath` (phase 1), `staleVarsByApp` (apps context).
- Produces:
  - `AppGrid()` (same export) — app cards as a list named "Apps": icon, name link (to `appSub(id, warning.tab)` in the active server's scope), `StatusPill`, the primary address (an external link) or "No public address", the one warning chip; a "Filter apps" box over 8 apps; the empty state "Run your first app" with two links, "From a template" (`newAppSub("template")`) and "Compose file" (`newAppSub("compose")`), in the active server's scope.
  - `HomePage()` (default export of `@/pages/home`) — `PageHeader` "Apps" + subtitle + one primary **New app** — a router link (`Button asChild`) to `newAppPath(activeServer.id)`, which phase 3 swaps for its `NewAppButton` (the chooser); health strip, firing line, grid; with no server: "Add a server to run your apps on." + "Add server" (distributed).

- [ ] **Step 1: Write the failing tests**

Replace the whole of `web/src/components/app-grid.test.tsx` with:

```tsx
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { AppGrid } from "@/components/app-grid";
import { AppsContext, type App, type AppsContextValue } from "@/context/apps-context-base";

// Links point into the active server's scope (/server/:id/...).
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));

const app = (id: string, name: string, over: Partial<App> = {}): App => ({
  id,
  serverId: "srv-1",
  name,
  version: "1",
  icon: "",
  color: "",
  createdAt: "",
  ...over,
});

function renderGrid(over: Partial<AppsContextValue>) {
  const value = {
    apps: [app("a1", "Shop"), app("a2", "Blog")],
    statusByApp: { a1: 1, a2: 5 },
    backupsByApp: {},
    staleVarsByApp: {},
    loading: false,
    error: null,
    ...over,
  } as unknown as AppsContextValue;
  return render(
    <MemoryRouter>
      <AppsContext.Provider value={value}>
        <AppGrid />
      </AppsContext.Provider>
    </MemoryRouter>,
  );
}

const card = (name: string) => screen.getByRole("link", { name }).closest("li")!;

describe("AppGrid", () => {
  it("shows each app's status and where it lives, never its id", () => {
    renderGrid({
      apps: [
        app("a1", "Shop", { domains: [{ domain: "shop.example.com", ssl: true, kind: "route" }] }),
        app("a2", "Blog"),
        app("0123456789", ""),
      ],
      statusByApp: { a1: 1, a2: 5 },
    });
    const shop = within(card("Shop"));
    expect(shop.getByText("Running")).toBeInTheDocument();
    expect(shop.getByRole("link", { name: "shop.example.com" })).toHaveAttribute("href", "https://shop.example.com");
    expect(within(card("Blog")).getByText("Stopped")).toBeInTheDocument();
    expect(within(card("Blog")).getByText("No public address")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Unnamed app" })).toBeInTheDocument();
    expect(screen.queryByText(/01234567/)).not.toBeInTheDocument();
  });

  it("carries one warning chip, for a late or failing backup or shared variables to reload", () => {
    renderGrid({
      backupsByApp: { a1: [{ name: "db", state: "failing", last_error: "disk full" }], a2: [{ name: "db", state: "ok" }] },
      staleVarsByApp: { a2: ["SMTP_PASS"] },
    });
    expect(within(card("Shop")).getByText("Backup failing")).toBeInTheDocument();
    expect(within(card("Blog")).getByText("Reload needed")).toBeInTheDocument();
  });

  it("links a backup warning to the app's Backups tab, and the others to the app", () => {
    renderGrid({ backupsByApp: { a1: [{ name: "db", state: "late" }], a2: [{ name: "db", state: "ok" }] } });
    expect(screen.getByRole("link", { name: "Shop" })).toHaveAttribute("href", "/server/srv-1/app/a1?tab=backups");
    expect(screen.getByRole("link", { name: "Blog" })).toHaveAttribute("href", "/server/srv-1/app/a2");
  });

  it("shows no chip while all is well", () => {
    renderGrid({});
    expect(screen.queryByText(/Backup (late|failing)|Reload needed/)).not.toBeInTheDocument();
  });

  it("offers a filter only past eight apps, matching names and addresses", async () => {
    const user = userEvent.setup();
    const many = Array.from({ length: 9 }, (_, i) => app(`a${i}`, `App ${i}`));
    many[3] = app("a3", "Photos", { domains: [{ domain: "pics.lan", ssl: false, kind: "route" }] });
    const { unmount } = renderGrid({ apps: many.slice(0, 8) });
    expect(screen.queryByRole("searchbox", { name: "Filter apps" })).not.toBeInTheDocument();
    unmount();
    renderGrid({ apps: many });
    await user.type(screen.getByRole("searchbox", { name: "Filter apps" }), "pics");
    expect(within(screen.getByRole("list", { name: "Apps" })).getAllByRole("listitem")).toHaveLength(1);
    expect(screen.getByRole("link", { name: "Photos" })).toBeInTheDocument();
    await user.clear(screen.getByRole("searchbox", { name: "Filter apps" }));
    await user.type(screen.getByRole("searchbox", { name: "Filter apps" }), "nothing like it");
    expect(screen.getByText("No app matches “nothing like it”.")).toBeInTheDocument();
  });

  it("invites to run the first app from a template or a compose file", () => {
    renderGrid({ apps: [] });
    expect(screen.getByText("Run your first app")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /From a template/ })).toHaveAttribute("href", "/server/srv-1/new?from=template");
    expect(screen.getByRole("link", { name: /Compose file/ })).toHaveAttribute("href", "/server/srv-1/new?from=compose");
  });

  it("shows skeletons while the first list loads, and a read failure", () => {
    const { unmount } = renderGrid({ apps: [], loading: true });
    expect(screen.getAllByRole("status", { name: "Loading apps" })).toHaveLength(3);
    unmount();
    renderGrid({ error: "agent offline" });
    expect(screen.getByText("agent offline")).toBeInTheDocument();
  });
});
```

Replace the whole of `web/src/pages/home.test.tsx` with:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import HomePage from "@/pages/home";

const homelab = { id: "s1", organizationId: "o", name: "homelab", createdAt: "", lastSeenAt: null, capabilities: {} };
let servers: { activeServer: typeof homelab | null; loading: boolean; refresh: () => Promise<void> };
vi.mock("@/context/use-servers", () => ({ useServers: () => servers }));
vi.mock("@/context/use-apps", () => ({
  useApps: () => ({
    apps: [{ id: "a1" }, { id: "a2" }, { id: "a3" }],
    statusByApp: { a1: 1, a2: 1, a3: 5 },
  }),
}));
vi.mock("@/components/server-health-strip", () => ({ ServerHealthStrip: () => <div data-testid="strip" /> }));
vi.mock("@/components/alerts/active-alerts", () => ({ FiringAlertsLine: () => <div data-testid="firing" /> }));
vi.mock("@/components/app-grid", () => ({ AppGrid: () => <div data-testid="grid" /> }));
vi.mock("@/components/server-creation-dialog", () => ({
  ServerSelectionDialog: ({ isOpen }: { isOpen: boolean }) => (isOpen ? <p>add server dialog</p> : null),
}));

function renderHome() {
  return render(
    <MemoryRouter initialEntries={["/server/s1"]}>
      <Routes>
        <Route path="/server/s1" element={<HomePage />} />
        <Route path="/server/s1/new" element={<p>new app page</p>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("HomePage", () => {
  beforeEach(() => {
    servers = { activeServer: homelab, loading: false, refresh: vi.fn(async () => {}) };
  });

  it("is the Apps page: counts under the title, one New app, health and alerts above the apps", () => {
    renderHome();
    expect(screen.getByRole("heading", { level: 1, name: "Apps" })).toBeInTheDocument();
    expect(screen.getByText("homelab · 2 running · 1 stopped")).toBeInTheDocument();
    // One primary action: New app, a link to the new-app page.
    expect(screen.queryAllByRole("button")).toHaveLength(0);
    expect(screen.getByRole("link", { name: "New app" })).toHaveAttribute("href", "/server/s1/new");
    const strip = screen.getByTestId("strip");
    const grid = screen.getByTestId("grid");
    expect(strip.compareDocumentPosition(screen.getByTestId("firing")) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(strip.compareDocumentPosition(grid) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // The multi-server cards are gone from this page.
    expect(screen.queryByRole("region", { name: "Servers" })).not.toBeInTheDocument();
  });

  it("goes to the new-app page from New app", async () => {
    const user = userEvent.setup();
    renderHome();
    await user.click(screen.getByRole("link", { name: "New app" }));
    expect(await screen.findByText("new app page")).toBeInTheDocument();
  });

  it("asks for a server when there is none", async () => {
    const user = userEvent.setup();
    servers = { ...servers, activeServer: null };
    renderHome();
    expect(screen.getByText("Add a server to run your apps on.")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "New app" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Add server" }));
    expect(screen.getByText("add server dialog")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/components/app-grid.test.tsx src/pages/home.test.tsx`
Expected: FAIL — the grid has no pills, chips, filter or new empty state; the page still has "Add app" and "From template" buttons and the Servers section.

- [ ] **Step 3: Implement**

Replace the whole of `web/src/components/app-grid.tsx` with:

```tsx
import { useState } from "react";
import { Link } from "react-router-dom";
import { Boxes, FileCode2, LayoutTemplate, TriangleAlert } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { AppIcon } from "@/components/app-icon";
import { EmptyState } from "@/components/empty-state";
import { StatusPill } from "@/components/status";
import type { App } from "@/context/apps-context-base";
import { useApps } from "@/context/use-apps";
import { useServerPath } from "@/context/use-server-path";
import { appTitle, appWarning, filterApps, FILTER_THRESHOLD, primaryAddress, type AppWarning } from "@/lib/app-summary";
import { TILE_GRID } from "@/lib/grid";
import { appSub, newAppSub } from "@/lib/routes";
import { appStatus } from "@/lib/status";
import { cn } from "@/lib/utils";

function AppTileSkeleton() {
  return (
    <Card role="status" aria-label="Loading apps" className="flex items-center gap-3 p-3.5">
      <div className="frost size-10 shrink-0 rounded-lg" />
      <div className="flex-1 space-y-2">
        <div className="frost h-3.5 w-24 rounded" />
        <div className="frost h-3 w-32 rounded" />
      </div>
    </Card>
  );
}

// NoApps is the Apps page of a server without apps: one sentence and the
// two most common ways to start.
function NoApps() {
  const toServer = useServerPath();
  return (
    <EmptyState
      icon={Boxes}
      title="Run your first app"
      action={
        <div className="flex flex-wrap justify-center gap-2">
          <Button asChild variant="outline">
            <Link to={toServer(newAppSub("template"))}>
              <LayoutTemplate className="size-4" aria-hidden /> From a template
            </Link>
          </Button>
          <Button asChild>
            <Link to={toServer(newAppSub("compose"))}>
              <FileCode2 className="size-4" aria-hidden /> Compose file
            </Link>
          </Button>
        </div>
      }
    />
  );
}

// AppCard is one app: icon, name (the link to the app, covering the card),
// its status, the address it opens at, and at most one warning. The address
// is its own external link above the card's.
function AppCard({ app, code, warning, index }: { app: App; code: number | undefined; warning: AppWarning | null; index: number }) {
  const toServer = useServerPath();
  const address = primaryAddress(app);
  const named = Boolean(app.name?.trim());
  return (
    <li className="app-card-in relative min-w-0" style={{ animationDelay: `${index * 40}ms` }}>
      <Card className="flex h-full items-center gap-3 p-3.5 transition-colors duration-150 hover:border-primary/40">
        <AppIcon name={app.name} id={app.id} icon={app.icon} color={app.color} className="size-10 shrink-0 rounded-lg text-base" />
        <div className="min-w-0 flex-1 space-y-1">
          <div className="flex items-center justify-between gap-2">
            <Link
              to={toServer(appSub(app.id, warning?.tab))}
              className={cn(
                "truncate text-sm font-semibold tracking-tight after:absolute after:inset-0 after:rounded-xl focus-visible:outline-none focus-visible:after:ring-2 focus-visible:after:ring-ring",
                !named && "font-medium text-muted-foreground",
              )}
            >
              {appTitle(app)}
            </Link>
            <StatusPill status={appStatus(code)} />
          </div>
          <div className="flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
            {address ? (
              <a
                href={address.url}
                target="_blank"
                rel="noreferrer"
                className="relative z-10 truncate transition-colors duration-150 hover:text-foreground hover:underline"
              >
                {address.host}
              </a>
            ) : (
              <span className="truncate">No public address</span>
            )}
            {warning ? (
              <span className="ml-auto inline-flex shrink-0 items-center gap-1 rounded-full bg-status-warn/15 px-2 py-0.5 text-[11px] font-medium text-foreground">
                <TriangleAlert className="size-3 text-status-warn" aria-hidden />
                {warning.label}
              </span>
            ) : null}
          </div>
        </div>
      </Card>
    </li>
  );
}

// AppGrid is the active server's apps as cards (a filter box once there
// are more than FILTER_THRESHOLD), or the invitation to run the first one.
export function AppGrid() {
  const { apps, statusByApp, backupsByApp, staleVarsByApp, loading, error } = useApps();
  const [query, setQuery] = useState("");

  if (loading && apps.length === 0) {
    return (
      <div className={TILE_GRID}>
        <AppTileSkeleton />
        <AppTileSkeleton />
        <AppTileSkeleton />
      </div>
    );
  }
  if (error) {
    return <p className="text-sm text-destructive">{error}</p>;
  }
  if (apps.length === 0) {
    return <NoApps />;
  }

  const shown = filterApps(apps, query);
  return (
    <div className="space-y-3">
      {apps.length > FILTER_THRESHOLD ? (
        <Input
          type="search"
          aria-label="Filter apps"
          placeholder="Filter apps"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          className="max-w-xs"
        />
      ) : null}
      {shown.length === 0 ? (
        <p className="text-sm text-muted-foreground">No app matches “{query.trim()}”.</p>
      ) : (
        <ul aria-label="Apps" className={TILE_GRID}>
          {shown.map((app, index) => (
            <AppCard
              key={app.id}
              app={app}
              code={statusByApp[app.id]}
              warning={appWarning(backupsByApp?.[app.id], staleVarsByApp?.[app.id])}
              index={index}
            />
          ))}
        </ul>
      )}
    </div>
  );
}
```

Replace the whole of `web/src/pages/home.tsx` with:

```tsx
import { useState } from "react";
import { Link } from "react-router-dom";
import { Plus, Server } from "lucide-react";

import { Button } from "@/components/ui/button";
import { FiringAlertsLine } from "@/components/alerts/active-alerts";
import { AppGrid } from "@/components/app-grid";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { ServerSelectionDialog } from "@/components/server-creation-dialog";
import { ServerHealthStrip } from "@/components/server-health-strip";
import { isStandalone } from "@/config";
import { useApps } from "@/context/use-apps";
import { useServers } from "@/context/use-servers";
import { appsSubtitle, countApps } from "@/lib/app-summary";
import { newAppPath } from "@/lib/routes";

// HomePage is the Apps page (/server/:id): this server's apps, how many
// run, its health in one line, firing alerts in one line, and New app (a
// link to the new-app page; phase 3 turns it into the chooser). The other
// servers are one switch away in the sidebar.
export default function HomePage() {
  const { activeServer, loading, refresh } = useServers();
  const { apps, statusByApp } = useApps();
  const [addServerOpen, setAddServerOpen] = useState(false);

  if (!activeServer) {
    return (
      <div className="space-y-6">
        <PageHeader title="Apps" />
        {loading ? null : (
          <EmptyState
            icon={Server}
            title={isStandalone ? "This server isn't ready yet." : "Add a server to run your apps on."}
            action={
              isStandalone ? undefined : (
                <Button onClick={() => setAddServerOpen(true)}>
                  <Plus className="size-4" aria-hidden /> Add server
                </Button>
              )
            }
          />
        )}
        {!isStandalone ? (
          <ServerSelectionDialog
            isOpen={addServerOpen}
            onClose={() => setAddServerOpen(false)}
            onServerAdded={() => void refresh()}
          />
        ) : null}
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Apps"
        subtitle={appsSubtitle(activeServer.name, apps.length, countApps(apps, statusByApp))}
        actions={
          <Button asChild>
            <Link to={newAppPath(activeServer.id)}>
              <Plus className="size-4" aria-hidden /> New app
            </Link>
          </Button>
        }
      />
      <div className="space-y-2">
        <ServerHealthStrip server={activeServer} />
        <FiringAlertsLine serverId={activeServer.id} />
      </div>
      <AppGrid />
    </div>
  );
}
```

Delete the multi-server cards:

```bash
git rm web/src/components/server-cards.tsx web/src/components/server-cards.test.tsx
```

In `web/src/lib/grid.ts` replace the comment with `// TILE_GRID is the Apps page's card grid.`

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/components/app-grid.test.tsx src/pages/home.test.tsx`
Expected: PASS

Run: `grep -rn "server-cards\|ServerCards" web/src`
Expected: no output.

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add -A web/src/components/app-grid.tsx web/src/components/app-grid.test.tsx web/src/pages/home.tsx web/src/pages/home.test.tsx web/src/lib/grid.ts web/src/components/server-cards.tsx web/src/components/server-cards.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): Apps page with counts, health strip, plain app cards and New app

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 5: Split the app page into `components/app-page/` (no behaviour change)

`web/src/pages/app-details.tsx` holds the page and four tab bodies in one 800-line file. Move three tab bodies out unchanged so the following tasks each change one focused file. The Configuration switch (`ConfigurationTab`) **stays in `app-details.tsx`**: phase 3 owns what is inside `AppEditorPanel`/`InstancePanel` and finds the switch by grepping for `<InstancePanel` there. Nothing on screen changes in this task; the existing tests are the check.

**Files:**
- Create: `web/src/components/app-page/ownership.ts`
- Create: `web/src/components/app-page/app-logs-tab.tsx`
- Create: `web/src/components/app-page/history-panel.tsx`
- Create: `web/src/components/app-page/app-settings-tab.tsx`
- Modify: `web/src/pages/app-details.tsx`

**Interfaces:**
- Consumes: the phase-1 `app-details.tsx`.
- Produces:
  - `type Ownership = AppInstance | null` (`@/components/app-page/ownership`) — the template side of an app (null for a plain app).
  - `AppLogsTab({ appId }: { appId: string })` (was `LogsTab`).
  - `ConfigurationTab` stays a private function of `app-details.tsx` (typed with the moved `Ownership`).
  - `HistoryPanel({ appId, ownership }: { appId: string; ownership: Ownership })` (was `HistoryTab`).
  - `AppSettingsTab(props)` (was `SettingsTab`, same props: `appId`, `name`, `ownership`, `onUnlinked`, `onRename`, `onDelete`).

- [ ] **Step 1: Confirm the starting point is green**

Run: `pnpm --dir web exec vitest run src/pages/app-details.test.tsx`
Expected: PASS (phase 1's version).

- [ ] **Step 2: Move the code**

Create `web/src/components/app-page/ownership.ts`:

```ts
import type { AppInstance } from "@/hooks/use-app-instance";

// Ownership is the template side of an app a template manages, with only
// this app's own instance; null for a plain app. While the instance is
// read (or when the read failed) the app is still the template's.
export type Ownership = AppInstance | null;
```

Then cut each function below out of `web/src/pages/app-details.tsx` **verbatim** (body unchanged) into its new file, add `export`, rename where noted, and give the file exactly the imports its code uses (each is listed; `tsc -b` and `eslint` report a missing or unused one):

| New file | Moved code | Imports it needs |
|---|---|---|
| `web/src/components/app-page/app-logs-tab.tsx` | the `const base = apiBaseUrl.endsWith("/") …` line and `function LogsTab` renamed `export function AppLogsTab` | `useCallback, useEffect, useState` (react); `LogsView, type LogLine` (`@/components/logs-view`); `useNotifications` (`@/context/use-notifications`); `useServers` (`@/context/use-servers`); `apiBaseUrl` (`@/config`); `readApiError` (`@/lib/api`) |
| `web/src/components/app-page/history-panel.tsx` | `function HistoryTab` renamed `export function HistoryPanel` (with its comment) | `useCallback, useEffect, useState` (react); `History, RefreshCw` (lucide-react); `toast` (sonner); `Button`, `Spinner`, and the `AlertDialog*` parts it uses (`@/components/ui/…`); `useApps` (`@/context/use-apps`); `type AppRevisions` (`@/context/apps-context-base`); `isManagedByTemplate` (`@/lib/templates`); `cn, relativeTime` (`@/lib/utils`); `type Ownership` |
| `web/src/components/app-page/app-settings-tab.tsx` | `function SettingsTab` renamed `export function AppSettingsTab` | `useState` (react); `useNavigate` (react-router-dom); `toast` (sonner); `Button`, `Input` (`@/components/ui/…`); `ConfirmButton` (`@/components/confirm-button`); `DeleteAppDialog` (`@/components/delete-app-dialog`); `useApps` (`@/context/use-apps`); `type DeleteAppResult` (`@/context/apps-context-base`); `useTemplatesApi` (`@/hooks/use-templates-api`); `deleteOutcome, type DeleteChoice` (`@/lib/delete-app`); `appLabel` (`@/lib/kpi`); `templateFromApp` (`@/lib/template-draft`); `NEW_TEMPLATE_PATH` (`@/lib/routes`); `type DraftState` (`@/pages/template-page`); `type Ownership` |

Also delete the `type Ownership = AppInstance | null;` declaration (and its comment) from `app-details.tsx`.

In `web/src/pages/app-details.tsx`:
- add the imports

```tsx
import { AppLogsTab } from "@/components/app-page/app-logs-tab";
import { AppSettingsTab } from "@/components/app-page/app-settings-tab";
import { HistoryPanel } from "@/components/app-page/history-panel";
import type { Ownership } from "@/components/app-page/ownership";
```

- in the JSX, `<LogsTab appId={app.id} />` → `<AppLogsTab appId={app.id} />`, `<HistoryTab … />` → `<HistoryPanel … />`, `<SettingsTab` → `<AppSettingsTab` (same props);
- remove every import the page no longer uses (the moved code took them: `LogsView`, `ConfirmButton`, `DeleteAppDialog`, `Input`, the `AlertDialog*` parts, `History`, `RefreshCw`, `apiBaseUrl`, `readApiError`, `useNotifications`, `useServers`, `isManagedByTemplate`, `deleteOutcome`, `DeleteChoice`, `templateFromApp`, `DraftState`, `NEW_TEMPLATE_PATH`, `type AppRevisions`, `type DeleteAppResult`, `relativeTime`, `type AppInstance` — keep any the page itself still uses; `tsc -b` with `noUnusedLocals` lists the rest).

- [ ] **Step 3: Run the tests (unchanged) to verify nothing moved on screen**

Run: `pnpm --dir web exec vitest run src/pages/app-details.test.tsx`
Expected: PASS, with no test edited.

- [ ] **Step 4: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/components/app-page web/src/pages/app-details.tsx
git commit -m "$(cat <<'EOF'
refactor(web): move the app page's tab bodies into components/app-page

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Logs — container filter chips, line count, jump to latest

**Files:**
- Modify: `web/src/components/logs-view.tsx`
- Create: `web/src/components/logs-view.test.tsx`

**Interfaces:**
- Consumes: nothing new.
- Produces: `LogsView` (same props). With two or more containers its header has a group named "Containers" of toggle buttons ("All", then each container, `aria-pressed`) that filter the lines; with one container its name; the shown line count ("12 lines"); the lines in `role="log"` named "Logs"; a "Jump to latest" button once scrolled up.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/logs-view.test.tsx`:

```tsx
import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { LogsView, type LogLine } from "@/components/logs-view";

const line = (container: string | undefined, message: string): LogLine => ({ timestamp: 1_760_000_000, level: 0, message, container });

function renderLogs(lines: LogLine[], over: Partial<Parameters<typeof LogsView>[0]> = {}) {
  const props = { lines, loading: false, error: null, tail: 200, onTailChange: vi.fn(), onRefresh: vi.fn(), ...over };
  render(<LogsView {...props} />);
  return props;
}

describe("LogsView", () => {
  it("filters by container with chips when there are several", async () => {
    const user = userEvent.setup();
    renderLogs([line("web", "GET /"), line("db", "checkpoint"), line("web", "GET /health")]);
    const chips = screen.getByRole("group", { name: "Containers" });
    expect(within(chips).getAllByRole("button").map((b) => b.textContent)).toEqual(["All", "web", "db"]);
    expect(within(chips).getByRole("button", { name: "All" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByText("3 lines")).toBeInTheDocument();

    await user.click(within(chips).getByRole("button", { name: "web" }));
    const log = screen.getByRole("log", { name: "Logs" });
    expect(log).toHaveTextContent("GET /health");
    expect(log).not.toHaveTextContent("checkpoint");
    expect(within(chips).getByRole("button", { name: "web" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByText("2 lines")).toBeInTheDocument();

    await user.click(within(chips).getByRole("button", { name: "All" }));
    expect(screen.getByRole("log", { name: "Logs" })).toHaveTextContent("checkpoint");
  });

  it("has no chips with one container, only its name", () => {
    renderLogs([line("web", "GET /")]);
    expect(screen.queryByRole("group", { name: "Containers" })).not.toBeInTheDocument();
    expect(screen.getByText("1 line")).toBeInTheDocument();
    expect(screen.getAllByText("web").length).toBeGreaterThan(0);
  });

  it("refreshes, changes the line count asked for, and says when there is no output", async () => {
    const user = userEvent.setup();
    const props = renderLogs([]);
    expect(screen.getByText("No output yet. Start the app, then refresh.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Refresh logs" }));
    expect(props.onRefresh).toHaveBeenCalled();
  });

  it("offers Jump to latest once scrolled up, and sticks to the bottom again", async () => {
    const user = userEvent.setup();
    renderLogs([line("web", "a"), line("web", "b")]);
    const log = screen.getByRole("log", { name: "Logs" });
    Object.defineProperty(log, "scrollHeight", { configurable: true, value: 1000 });
    Object.defineProperty(log, "clientHeight", { configurable: true, value: 100 });
    log.scrollTop = 0;
    fireEvent.scroll(log);
    await user.click(await screen.findByRole("button", { name: "Jump to latest" }));
    expect(log.scrollTop).toBe(1000);
    expect(screen.queryByRole("button", { name: "Jump to latest" })).not.toBeInTheDocument();
  });

  it("shows a read failure with Try again", async () => {
    const user = userEvent.setup();
    const props = renderLogs([], { error: "agent offline" });
    expect(screen.getByText("agent offline")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(props.onRefresh).toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/components/logs-view.test.tsx`
Expected: FAIL — no chips, no line count, no `log` role, the jump button is named "Latest".

- [ ] **Step 3: Implement**

In `web/src/components/logs-view.tsx`:

1. Add, above `export function LogsView`:

```tsx
// Chip is one container filter: a toggle button in the console's palette.
function Chip({ pressed, onClick, children }: { pressed: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      aria-pressed={pressed}
      onClick={onClick}
      className={cn(
        "inline-flex min-h-7 items-center gap-1.5 rounded-full border border-console-border px-2.5 font-mono text-xs transition-colors duration-150 max-sm:min-h-10",
        pressed ? "bg-console-hover text-console-foreground" : "text-console-muted hover:text-console-foreground",
      )}
    >
      {children}
    </button>
  );
}
```

and change the React import to `import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";`.

2. In `LogsView`, after the `containers` memo, add:

```tsx
  // The container picked in the chips; it goes back to All when that
  // container has no lines any more.
  const [only, setOnly] = useState<string | null>(null);
  const active = only !== null && containers.includes(only) ? only : null;
  const shown = useMemo(() => (active ? lines.filter((l) => l.container === active) : lines), [lines, active]);
```

and change the stick-to-bottom effect's dependency from `lines` to `shown`: `}, [shown, stickToBottom]);`.

3. Replace the header's left part — the `<div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-4 gap-y-1 text-xs">…</div>` block — with:

```tsx
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5 text-xs">
          {containers.length > 1 ? (
            <div role="group" aria-label="Containers" className="flex flex-wrap gap-1.5">
              <Chip pressed={active === null} onClick={() => setOnly(null)}>
                All
              </Chip>
              {containers.map((c) => (
                <Chip key={c} pressed={active === c} onClick={() => setOnly(c)}>
                  <span className={cn("size-2 rounded-full bg-current", colorOf(c))} aria-hidden />
                  <span>{c}</span>
                </Chip>
              ))}
            </div>
          ) : (
            <span className="font-mono text-console-muted">{containers[0] ?? "Container output"}</span>
          )}
        </div>
        <span className="text-xs tabular-nums text-console-muted">
          {shown.length === 1 ? "1 line" : `${shown.length} lines`}
        </span>
```

and change that header row's class from `"flex items-center gap-3 border-b border-console-border px-3 py-2"` to `"flex flex-wrap items-center gap-2 border-b border-console-border px-3 py-2"`.

4. Render `shown` instead of `lines` in the line list, and name the list: the scroll box becomes

```tsx
        <div
          ref={scrollRef}
          role="log"
          aria-label="Logs"
          onScroll={handleScroll}
          className="flex-1 overflow-y-auto py-2 font-mono text-xs leading-5"
        >
          {shown.map((l, i) => (
```

(the row markup inside is unchanged).

5. The jump button: change its condition to `{!stickToBottom && shown.length > 0 ? (` and its content to `<ArrowDown className="size-3.5" aria-hidden /> Jump to latest`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/components/logs-view.test.tsx src/pages/app-details.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/components/logs-view.tsx web/src/components/logs-view.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): logs container filter chips, line count and Jump to latest

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: Metrics — KPI blocks for the Overview, alert rules collapsed

**Files:**
- Modify: `web/src/components/app-metrics.tsx`, `web/src/components/app-metrics.test.tsx`

**Interfaces:**
- Consumes: `Disclosure` (phase 1).
- Produces:
  - `AppMetricBlocks({ serverId, appId, layout? }: { serverId: string; appId: string; layout?: "row" | "pair" })` — `"row"` (default) is today's 2→4 column row; `"pair"` is always two columns (the Overview's half-width widget). Without any point yet it says "No data yet." instead of rendering nothing (it now sits in a titled widget that must not stay blank).
  - `AppMetricsTab` (same props) — the range menu and four charts, then a `Disclosure` "Alert rules for this app", closed: the alert rules (and their three requests) load only when it is opened.

- [ ] **Step 1: Write the failing tests**

In `web/src/components/app-metrics.test.tsx`:

- add `import { within } from "@testing-library/react";` to the existing Testing Library import (`import { render, screen, within } from "@testing-library/react";`);
- add inside `describe("AppMetricBlocks", …)`:

```tsx
  it("lays the four blocks out in pairs for a half-width widget", () => {
    render(<AppMetricBlocks serverId="s1" appId="a1" layout="pair" />);
    const grid = screen.getByTestId("kpi-CPU").parentElement!;
    expect(grid).toHaveClass("grid-cols-2");
    expect(grid).not.toHaveClass("lg:grid-cols-4");
    expect(within(grid).getAllByTestId(/^kpi-/)).toHaveLength(4);
  });
```

- replace the test "renders nothing without data and explains unavailable containers" with:

```tsx
  it("says there is no data yet, and explains unavailable containers", () => {
    data = { interval_seconds: 60, points: [] };
    const { rerender } = render(<AppMetricBlocks serverId="s1" appId="a1" />);
    expect(screen.getByText("No data yet.")).toBeInTheDocument();
    expect(screen.queryByTestId("kpi-CPU")).not.toBeInTheDocument();
    data = { interval_seconds: 60, points: [{ t: 0, unavailable: 1, unavailable_max: 1 }] };
    rerender(<AppMetricBlocks serverId="s1" appId="a2" />);
    expect(screen.getByText("Container metrics are unavailable on this host")).toBeInTheDocument();
    expect(screen.queryByTestId("kpi-CPU")).not.toBeInTheDocument();
  });
```

- in `it("charts the app over the chosen range", …)` replace the line `expect(screen.getByTestId("alerts-card")).toHaveTextContent("s1/a1");` with:

```tsx
    // The alert rules are folded away until asked for.
    expect(screen.queryByTestId("alerts-card")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Alert rules for this app" }));
    expect(screen.getByTestId("alerts-card")).toHaveTextContent("s1/a1");
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/components/app-metrics.test.tsx`
Expected: FAIL — `layout` is ignored; nothing renders without data; the alerts card is always shown.

- [ ] **Step 3: Implement**

In `web/src/components/app-metrics.tsx`:

1. Add `import { Disclosure } from "@/components/disclosure";`.
2. Replace `const KPI_GRID = "grid grid-cols-2 gap-3 lg:grid-cols-4";` with:

```tsx
// The KPI blocks as a row under a header (2 columns, 4 on a wide screen),
// or in pairs inside a half-width widget.
const KPI_GRID = { row: "grid grid-cols-2 gap-3 lg:grid-cols-4", pair: "grid grid-cols-2 gap-3" } as const;
```

3. Change `AppMetricBlocks`'s signature and comment to:

```tsx
// AppMetricBlocks is an app's KPI blocks: CPU now, memory against its
// limit, disk and network rate, each from the newest 1-minute point with
// the last hour as a sparkline (the Overview's Resources widget).
export function AppMetricBlocks({
  serverId,
  appId,
  layout = "row",
}: {
  serverId: string;
  appId: string;
  layout?: "row" | "pair";
}) {
```

and use `KPI_GRID[layout]` in both places that used `KPI_GRID`. Replace `if (!last) return null;` with `if (!last) return <p className="text-sm text-muted-foreground">No data yet.</p>;`.

4. In `AppMetricsTab`, replace `<TargetAlertsCard serverId={serverId} appId={appId} targetLabel={appName || "this app"} />` with:

```tsx
      <Disclosure label="Alert rules for this app">
        <TargetAlertsCard serverId={serverId} appId={appId} targetLabel={appName || "this app"} />
      </Disclosure>
```

and update its comment to `// AppMetricsTab is the app page's Metrics tab: CPU, memory, network and disk over the selected range, the app's alert rules folded below.`

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/components/app-metrics.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/components/app-metrics.tsx web/src/components/app-metrics.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): app KPI blocks in pairs, alert rules folded on the Metrics tab

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 8: Back up now in one place, and the Backups tab as flat rows

"Back up now" is triggered from the Backups tab, the Overview, the header's "⋯" and ⌘K. One hook runs it (with the activity tracking and held health the tab already has); the tab's rows become flat rows with every action in "⋯".

**Files:**
- Create: `web/src/hooks/use-run-backup.ts`, `web/src/hooks/use-run-backup.test.tsx`
- Modify: `web/src/components/app-backups.tsx`, `web/src/components/app-backups.test.tsx`
- Modify: `web/src/lib/status.ts`, `web/src/lib/status.test.ts` (phase 1 files: one constant)

**Interfaces:**
- Consumes: `trackActivity`, `holdHealth`, `shownHealth`, `useOverlays`, `useActivity` (`@/lib/backup-activity`), `useBackups` (`@/context/use-backups`), `runnableBackups` (Task 2), `RowList` (phase 1), `StatusPill`, `backupStatus`, `type StatusInfo` (phase 1).
- Produces (`@/hooks/use-run-backup`):
  - `useRunBackup(appId: string): (name: string) => Promise<void>` — runs one backup now: marks it running (survives leaving the tab), holds the outcome as its health until a report shows it, toasts "`name` backed up" / "`name`: nothing changed since the last copy" / "Couldn't back up `name`". Never throws.
  - `useBackUpNow(appId: string): { run: () => Promise<void>; busy: boolean }` — reads the app's backups and runs every runnable one; with none, toasts "Nothing to back up yet" (description "Set up a backup on the Backups tab first.") and sends nothing; `busy` while any backup of the app runs or is restored.
- Produces (`@/lib/status`): `RESTORING: StatusInfo = { tone: "warn", label: "Restoring", pulse: true }` — a backup's pill while it is restored (the Overview uses it too).
- Produces (`AppBackups`, same props): rows in a list named "Backups"; each row: name (mono), its detail line, a `StatusPill` (or "Restoring"), "Last copy 4 minutes ago", the next-copy note, an inline "Set up…" button only without a storage, and a "⋯" menu named "Actions for `name`" with "Set up…" (no storage), "Back up now" and "Restore…" (built-in backups), "Edit…".

- [ ] **Step 1: Write the failing tests**

In `web/src/lib/status.test.ts` add (import `RESTORING` with the other names from `@/lib/status`):

```ts
describe("RESTORING", () => {
  it("is a moving warning", () => {
    expect(RESTORING).toEqual({ tone: "warn", label: "Restoring", pulse: true });
  });
});
```

Create `web/src/hooks/use-run-backup.test.tsx`:

```tsx
import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";
import { useBackUpNow, useRunBackup } from "@/hooks/use-run-backup";
import { resetHeldHealth, trackActivity } from "@/lib/backup-activity";
import { AgentError, type EffectiveBackup } from "@/lib/backups";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
const backups = vi.hoisted(() => ({ runBackup: vi.fn() }));
vi.mock("@/context/use-backups", () => ({ useBackups: () => backups }));

const b = (name: string, over: Partial<EffectiveBackup> = {}): EffectiveBackup => ({
  name,
  type: "sqlite",
  enabled: true,
  storage: "disk",
  in_recipe: true,
  from_recipe: null,
  ...over,
});

function wrap(getApp: AppsContextValue["getApp"]) {
  const value = { getApp, backupsByApp: { a1: [{ name: "database", state: "ok", last_success: 10 }] } } as unknown as AppsContextValue;
  return ({ children }: { children: ReactNode }) => <AppsContext.Provider value={value}>{children}</AppsContext.Provider>;
}

describe("useRunBackup", () => {
  beforeEach(() => {
    resetHeldHealth();
    backups.runBackup.mockReset();
    for (const f of Object.values(toast)) f.mockReset();
  });

  it("backs one backup up and says how it went", async () => {
    backups.runBackup.mockResolvedValueOnce({ unchanged: true, point: null, warnings: [] });
    const { result } = renderHook(() => useRunBackup("a1"), { wrapper: wrap(vi.fn()) });
    await act(() => result.current("database"));
    expect(backups.runBackup).toHaveBeenCalledWith("a1", "database");
    expect(toast.success).toHaveBeenCalledWith("database: nothing changed since the last copy", { description: undefined });
    backups.runBackup.mockRejectedValueOnce(new AgentError("upload: permission denied"));
    await act(() => result.current("database"));
    expect(toast.error).toHaveBeenCalledWith("Couldn't back up database", { description: "upload: permission denied" });
  });
});

describe("useBackUpNow", () => {
  beforeEach(() => {
    resetHeldHealth();
    backups.runBackup.mockReset();
    for (const f of Object.values(toast)) f.mockReset();
  });

  it("runs every backup it can: on, with a storage, not custom", async () => {
    backups.runBackup.mockResolvedValue({ unchanged: false, point: { id: "p" }, warnings: [] });
    const getApp = vi.fn(async () => ({
      backups: [b("database"), b("media", { enabled: false }), b("nowhere", { storage: "" }), b("replica", { type: "custom" }), b("uploads", { type: "files" })],
    }));
    const { result } = renderHook(() => useBackUpNow("a1"), { wrapper: wrap(getApp as unknown as AppsContextValue["getApp"]) });
    await act(() => result.current.run());
    expect(backups.runBackup.mock.calls).toEqual([
      ["a1", "database"],
      ["a1", "uploads"],
    ]);
  });

  it("sends nothing and says so when no backup is set up", async () => {
    const getApp = vi.fn(async () => ({ backups: [b("media", { enabled: false }), b("replica", { type: "custom" })] }));
    const { result } = renderHook(() => useBackUpNow("a1"), { wrapper: wrap(getApp as unknown as AppsContextValue["getApp"]) });
    await act(() => result.current.run());
    expect(backups.runBackup).not.toHaveBeenCalled();
    expect(toast.info).toHaveBeenCalledWith("Nothing to back up yet", { description: "Set up a backup on the Backups tab first." });
  });

  it("reports an app it can't read", async () => {
    const getApp = vi.fn(async () => Promise.reject(new Error("agent offline")));
    const { result } = renderHook(() => useBackUpNow("a1"), { wrapper: wrap(getApp as unknown as AppsContextValue["getApp"]) });
    await act(() => result.current.run());
    expect(toast.error).toHaveBeenCalledWith("Couldn't read the app's backups", { description: "agent offline" });
  });

  it("is busy while any backup of the app runs or is restored", async () => {
    const { result } = renderHook(() => useBackUpNow("a1"), { wrapper: wrap(vi.fn()) });
    expect(result.current.busy).toBe(false);
    let end = () => {};
    act(() => {
      end = trackActivity("a1", "database", "restore");
    });
    expect(result.current.busy).toBe(true);
    act(() => end());
    await waitFor(() => expect(result.current.busy).toBe(false));
  });
});
```

In `web/src/components/app-backups.test.tsx`, make these edits (the component's rows move from a table with per-row text buttons to flat list rows whose actions are in a "⋯" menu):

1. Replace the `row` helper and add two menu helpers right below it:

```tsx
const row = (name: string) => screen.getByText(name, { selector: "span.font-mono" }).closest("li")!;

// menuItem opens a backup's ⋯ menu and returns its item named `item`.
async function menuItem(user: ReturnType<typeof userEvent.setup>, name: string, item: string) {
  await user.click(await screen.findByRole("button", { name: `Actions for ${name}` }));
  return screen.findByRole("menuitem", { name: item });
}

// pick runs an item of a backup's ⋯ menu.
async function pick(user: ReturnType<typeof userEvent.setup>, name: string, item: string) {
  await user.click(await menuItem(user, name, item));
}
```

2. In `flip`, replace `await user.click(await screen.findByRole("button", { name: `Edit: ${name}` }));` with `await pick(user, name, "Edit…");`.

3. Apply these replacements in every test (the action, then the same assertion it was followed by):

| Was | Becomes |
|---|---|
| `.then((e) => e.closest("tr")!)` | `.then((e) => e.closest("li")!)` |
| `await user.click(await screen.findByRole("button", { name: "Back up now: X" }))` and `await user.click(screen.getByRole("button", { name: "Back up now: X" }))` | `await pick(user, "X", "Back up now")` |
| clicking `"Copies & restore: X"` | `await pick(user, "X", "Restore…")` |
| clicking `"Edit: X"` | `await pick(user, "X", "Edit…")` |
| `expect(…getByRole("button", { name: "Back up now: X" })).toBeDisabled()` (and `findByRole`) | `expect(await menuItem(user, "X", "Back up now")).toHaveAttribute("aria-disabled", "true"); await user.keyboard("{Escape}");` |
| `…toBeEnabled()` on `"Back up now: X"` | `expect(await menuItem(user, "X", "Back up now")).not.toHaveAttribute("aria-disabled"); await user.keyboard("{Escape}");` (inside `waitFor` where it was) |
| `expect(screen.getByRole("button", { name: "Edit: X" })).toBeDisabled()` | `expect(await menuItem(user, "X", "Edit…")).toHaveAttribute("aria-disabled", "true"); await user.keyboard("{Escape}");` |
| `toHaveFocus()` on `"Edit: X"`, `"Copies & restore: X"` | `toHaveFocus()` on `screen.getByRole("button", { name: "Actions for X" })` |
| `"· 4 minutes ago"`, `"· 10 minutes ago"`, `/^· (now\|\d+ seconds? ago)$/`, `"· 1 hour ago"` | `"Last copy 4 minutes ago"`, `"Last copy 10 minutes ago"`, `/^Last copy (now\|\d+ seconds? ago)$/`, `"Last copy 1 hour ago"` |
| `expect(await screen.findAllByRole("button", { name: "Edit: database" })).not.toHaveLength(0)` | `expect(await screen.findByRole("button", { name: "Actions for database" })).toBeInTheDocument()` |

`"Set up X"` stays an inline button: its queries do not change. The one test that checks a disabled state without a `user` becomes:

```tsx
  it("takes the agent's word that a backup is running", async () => {
    const user = userEvent.setup({ delay: null });
    renderBackups({}, undefined, [{ name: "database", state: "running" }]);
    expect(await menuItem(user, "database", "Back up now")).toHaveAttribute("aria-disabled", "true");
  });
```

4. In "lists each backup with its storage, schedule, location and health": replace the lines from `expect(media.getByRole("button", { name: "Back up now: media" })).toBeDisabled();` to the end of the test with:

```tsx
    const replica = within(row("replica"));
    expect(replica.getByText("Custom · service litestream → s3 · Its own tool")).toBeInTheDocument();
    expect(replica.getByText("OK")).toBeInTheDocument();
    // Flat rows, no table.
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    expect(screen.getByRole("list", { name: "Backups" })).toBeInTheDocument();
```

5. Replace the whole test "has the actions as text buttons, and the same ones in a menu for phones" with:

```tsx
  it("keeps every action of a row in its ⋯ menu, with the same rules", async () => {
    const user = userEvent.setup({ delay: null });
    backups.listRestorePoints.mockResolvedValue([]);
    renderBackups();
    const db = within(await screen.findByText("database", { selector: "span.font-mono" }).then((e) => e.closest("li")!));
    expect(db.queryByRole("button", { name: /Back up now|Restore|Edit/ })).not.toBeInTheDocument();

    await user.click(db.getByRole("button", { name: "Actions for database" }));
    expect((await screen.findAllByRole("menuitem")).map((m) => m.textContent)).toEqual(["Back up now", "Restore…", "Edit…"]);
    backups.runBackup.mockResolvedValueOnce({ unchanged: false, point: { id: "p" }, warnings: [] });
    await user.click(screen.getByRole("menuitem", { name: "Back up now" }));
    await waitFor(() => expect(backups.runBackup).toHaveBeenCalledWith("a1", "database"));
    await pick(user, "database", "Restore…");
    expect(await screen.findByText("No copies yet. Back up now to make one.")).toBeInTheDocument();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(db.getByRole("button", { name: "Actions for database" })).toHaveFocus());
    await pick(user, "database", "Edit…");
    expect(await screen.findByRole("heading", { name: "Edit backup database" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Cancel" }));

    // Without a storage: Set up inline and first in the menu; nothing runs yet.
    expect(within(row("media")).getByRole("button", { name: "Set up media" })).toBeInTheDocument();
    await user.click(within(row("media")).getByRole("button", { name: "Actions for media" }));
    expect((await screen.findAllByRole("menuitem")).map((m) => m.textContent)).toEqual(["Set up…", "Back up now", "Restore…", "Edit…"]);
    expect(screen.getByRole("menuitem", { name: "Back up now" })).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByRole("menuitem", { name: "Restore…" })).toHaveAttribute("aria-disabled", "true");
    await user.click(screen.getByRole("menuitem", { name: "Set up…" }));
    expect(await screen.findByRole("heading", { name: "Turn on media" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Cancel" }));

    // A custom backup's menu has only Edit.
    await user.click(within(row("replica")).getByRole("button", { name: "Actions for replica" }));
    expect((await screen.findAllByRole("menuitem")).map((m) => m.textContent)).toEqual(["Edit…"]);
  });
```

6. In "won't start a second run while one is under way, and shows a restore": after `await pick(user, "database", "Back up now");` assert

```tsx
    expect(await menuItem(user, "database", "Back up now")).toHaveAttribute("aria-disabled", "true");
    await user.keyboard("{Escape}");
    expect(within(row("database")).getByText("Running")).toBeInTheDocument();
```

and, after the restore starts, replace `expect(screen.getByRole("button", { name: "Edit: database" })).toBeDisabled();` with `expect(await menuItem(user, "database", "Edit…")).toHaveAttribute("aria-disabled", "true"); await user.keyboard("{Escape}");`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/status.test.ts src/hooks/use-run-backup.test.tsx src/components/app-backups.test.tsx`
Expected: FAIL — `RESTORING` and the hook do not exist; the rows are still table rows with text buttons.

- [ ] **Step 3: Implement**

In `web/src/lib/status.ts`, after `backupStatus`, add:

```ts
// RESTORING is a backup's state while it is being restored (the app is
// stopped meanwhile).
export const RESTORING: StatusInfo = { tone: "warn", label: "Restoring", pulse: true };
```

Create `web/src/hooks/use-run-backup.ts`:

```ts
import { useCallback } from "react";
import { toast } from "sonner";

import { useApps } from "@/context/use-apps";
import { useBackups } from "@/context/use-backups";
import { holdHealth, shownHealth, trackActivity, useActivity, useOverlays } from "@/lib/backup-activity";
import { AgentError, isRunRefusal, runnableBackups, type EffectiveBackup } from "@/lib/backups";

// useRunBackup is Back up now for one backup of an app. The run is marked
// as running in the shared activity store (it outlives the tab that started
// it), and its outcome is held as the backup's health until an agent report
// shows it. It toasts the result and never throws.
export function useRunBackup(appId: string): (name: string) => Promise<void> {
  const { backupsByApp } = useApps();
  const { runBackup } = useBackups();
  const overlays = useOverlays(appId);
  const reported = backupsByApp?.[appId];
  return useCallback(
    async (name: string) => {
      const end = trackActivity(appId, name, "backup");
      const prior = shownHealth(overlays[name], reported?.find((h) => h.name === name));
      try {
        const res = await runBackup(appId, name);
        holdHealth(appId, name, { kind: "backed-up", unchanged: res.unchanged }, prior);
        const title = res.unchanged ? `${name}: nothing changed since the last copy` : `${name} backed up`;
        toast.success(title, { description: res.warnings.join("\n") || undefined });
      } catch (e) {
        const message = e instanceof Error ? e.message : "";
        // The run itself failed (not refused before or while another runs):
        // the row says so now, not when the next report comes.
        if (e instanceof AgentError && message && !isRunRefusal(message)) {
          holdHealth(appId, name, { kind: "failed", error: message }, prior);
        }
        toast.error(`Couldn't back up ${name}`, { description: message || undefined });
      } finally {
        end();
      }
    },
    [appId, runBackup, overlays, reported],
  );
}

// useBackUpNow is the app-wide Back up now (the page's ⋯ menu, ⌘K): every
// backup winterflow can run for the app, at once (the agent runs them one
// after the other). With none set up it says so and sends nothing. busy
// while any backup of the app runs or is being restored, so a second click
// never queues a duplicate.
export function useBackUpNow(appId: string): { run: () => Promise<void>; busy: boolean } {
  const { getApp } = useApps();
  const runOne = useRunBackup(appId);
  const activity = useActivity(appId);
  const busy = Object.keys(activity).length > 0;
  const run = useCallback(async () => {
    let list: EffectiveBackup[];
    try {
      list = (await getApp(appId)).backups ?? [];
    } catch (e) {
      toast.error("Couldn't read the app's backups", { description: e instanceof Error ? e.message : undefined });
      return;
    }
    const ready = runnableBackups(list);
    if (ready.length === 0) {
      toast.info("Nothing to back up yet", { description: "Set up a backup on the Backups tab first." });
      return;
    }
    await Promise.all(ready.map((b) => runOne(b.name)));
  }, [appId, getApp, runOne]);
  return { run, busy };
}
```

In `web/src/components/app-backups.tsx`:

1. Imports: remove `Badge`, the `Table…` import, `trackActivity` (from `@/lib/backup-activity`), and `AgentError`, `isRunRefusal`, `STATE_LABELS` (from `@/lib/backups`); add

```tsx
import { RowList } from "@/components/section";
import { StatusPill } from "@/components/status";
import { useRunBackup } from "@/hooks/use-run-backup";
import { backupStatus, RESTORING } from "@/lib/status";
```

2. Delete the `STATE_STYLE` constant.

3. Replace the whole `const runNow = async (name: string) => { … };` function with `const runNow = useRunBackup(appId);` — and move that line up to just after `const backups = useBackups();` (hooks run before the early returns).

4. Replace

```tsx
        <Table>
          <TableBody>
            {list.map((b) => (
```

with

```tsx
        <RowList label="Backups">
            {list.map((b) => (
```

and the closing `          </TableBody>\n        </Table>` with `        </RowList>`.

5. Replace the whole `BackupRow` function (and its comment) with:

```tsx
// BackupRow is one backup as a flat row: its name with a muted line (type,
// location, storage, schedule), its state with the last and next copy, and
// its actions in a ⋯ menu — Set up (no storage yet; also inline), Back up
// now and Restore (built-in backups only: winterflow neither runs nor lists
// a custom backup's copies), Edit (on/off is there).
function BackupRow({
  backup: b,
  health,
  busy,
  activity,
  onSetUp,
  onRun,
  onPoints,
  onEdit,
}: {
  backup: EffectiveBackup;
  health: BackupHealth | undefined;
  busy: boolean;
  activity: readonly Activity[] | undefined;
  onSetUp: (trigger: HTMLElement) => void;
  onRun: () => void;
  onPoints: (trigger: HTMLElement) => void;
  onEdit: (trigger: HTMLElement) => void;
}) {
  const menuRef = useRef<HTMLButtonElement>(null);
  const builtin = b.type !== "custom";
  const kinds = activity ?? [];
  const restoringRow = kinds.includes("restore");
  const state: BackupState = kinds.includes("backup") ? "running" : (health?.state ?? (b.enabled ? "never" : "off"));
  // A run is under way here, or the agent reports one (a page reloaded, or
  // another browser, started it): a second request would be refused.
  const running = kinds.includes("backup") || health?.state === "running";
  const note = restoringRow || running || !builtin ? "" : nextCopyNote(b, state, health?.last_success);
  const where = [TYPE_LABELS[b.type], locationLabel(b)].filter(Boolean).join(" · ");
  const detail = [b.storage ? `${where} → ${b.storage}` : where, builtin ? scheduleLabel(b.every) : "Its own tool"].join(" · ");
  const runDisabled = !b.enabled || running || busy;
  const copiesTitle = b.storage ? undefined : "Set up the backup to see its copies: they are stored in its storage";
  return (
    <li className="flex flex-col gap-2 px-4 py-3 transition-colors duration-150 hover:bg-muted/40 sm:flex-row sm:items-center sm:gap-4 max-sm:rounded-xl max-sm:border max-sm:bg-card">
      <div className="min-w-0 flex-1">
        <span className="font-mono text-sm font-medium">{b.name}</span>
        <div className="mt-0.5 text-xs text-muted-foreground [overflow-wrap:anywhere]">{detail}</div>
        {state === "failing" && health?.last_error ? <RowError error={health.last_error} /> : null}
      </div>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 sm:w-60 sm:justify-end">
        <span title={health?.last_error}>
          <StatusPill status={restoringRow ? RESTORING : backupStatus(state)} />
        </span>
        {health?.last_success ? (
          <span className="text-xs text-muted-foreground">Last copy {relativeTime(new Date(health.last_success * 1000))}</span>
        ) : null}
        {note ? <span className="w-full text-xs text-muted-foreground sm:text-right">{note}</span> : null}
      </div>
      <div className="flex shrink-0 items-center justify-end gap-1">
        {b.storage ? null : (
          <Button size="sm" variant="outline" disabled={busy} aria-label={`Set up ${b.name}`} onClick={(e) => onSetUp(e.currentTarget)}>
            Set up…
          </Button>
        )}
        <DropdownMenu modal={false}>
          <DropdownMenuTrigger asChild>
            <Button
              ref={menuRef}
              size="icon"
              variant="ghost"
              className="size-10 text-muted-foreground sm:size-8"
              aria-label={`Actions for ${b.name}`}
            >
              <MoreHorizontal className="size-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {b.storage ? null : (
              <DropdownMenuItem disabled={busy} onSelect={() => menuRef.current && onSetUp(menuRef.current)}>
                Set up…
              </DropdownMenuItem>
            )}
            {builtin ? (
              <>
                <DropdownMenuItem disabled={runDisabled} onSelect={onRun}>
                  Back up now
                </DropdownMenuItem>
                <DropdownMenuItem
                  disabled={!b.storage}
                  title={copiesTitle}
                  onSelect={() => menuRef.current && onPoints(menuRef.current)}
                >
                  Restore…
                </DropdownMenuItem>
              </>
            ) : null}
            <DropdownMenuItem disabled={busy} onSelect={() => menuRef.current && onEdit(menuRef.current)}>
              Edit…
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </li>
  );
}
```

6. In the component's comment, replace "Back up now; its copies (restore, delete); an editor for on/off, …" wording by "its actions in a ⋯ menu (Back up now, Restore, Edit)"; and in the "can't be read" error text replace "or roll back in History" with "or roll back in History (Configuration → History)".

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/status.test.ts src/hooks/use-run-backup.test.tsx src/components/app-backups.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/status.ts web/src/lib/status.test.ts web/src/hooks/use-run-backup.ts web/src/hooks/use-run-backup.test.tsx web/src/components/app-backups.tsx web/src/components/app-backups.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): one Back up now hook; backups as flat rows with a ⋯ menu

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 9: Backup edit dialog — retention, location and skipped paths under "Advanced"

**Files:**
- Modify: `web/src/components/backup-edit-dialog.tsx`, `web/src/components/backup-edit-dialog.test.tsx`

**Interfaces:**
- Consumes: `Disclosure` (phase 1).
- Produces: `BackupEditDialog` (same props). Visible: the on/off switch, the storage, the schedule. Under a `Disclosure` "Advanced" (closed): Keep (retention tiers), Location (with the skipped paths). It opens by itself when a stored retention or location is invalid, so the reason Save is disabled can be fixed.

- [ ] **Step 1: Write the failing tests**

In `web/src/components/backup-edit-dialog.test.tsx`:

1. Change the Testing Library import to `import { cleanup, fireEvent, render, screen } from "@testing-library/react";`.
2. Give `renderDialog` a `closed` option and open "Advanced" unless it is set (every existing test edits retention or location, so they keep working unchanged):

```tsx
function renderDialog(
  backup: EffectiveBackup,
  opts: { days?: number; error?: string; onRemove?: () => void; onChangeStorage?: () => void; closed?: boolean } = {},
) {
  const onSave = vi.fn();
  render(
    <BackupEditDialog
      backup={backup}
      retentionDays={opts.days ?? 0}
      targets={targets}
      open
      busy={false}
      error={opts.error}
      onOpenChange={() => {}}
      onSave={onSave}
      onRemove={opts.onRemove}
      onChangeStorage={opts.onChangeStorage}
    />,
  );
  // Retention and location sit under Advanced (a custom backup has none).
  const advanced = screen.queryByRole("button", { name: "Advanced" });
  if (advanced && !opts.closed && advanced.getAttribute("aria-expanded") !== "true") fireEvent.click(advanced);
  return onSave;
}
```

3. Add inside `describe("BackupEditDialog", …)`:

```tsx
  it("keeps retention, location and skipped paths under Advanced, closed", async () => {
    const user = userEvent.setup({ delay: null });
    renderDialog({ ...database, type: "files", path: undefined, paths: ["uploads"], exclude: ["cache"] }, { closed: true });
    expect(screen.getByLabelText("Schedule")).toBeInTheDocument();
    expect(screen.queryByLabelText("Every copy for")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Location kind")).not.toBeInTheDocument();
    expect(screen.queryByText("cache")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Advanced" }));
    expect(screen.getByLabelText("Every copy for")).toBeInTheDocument();
    expect(screen.getByLabelText("Location kind")).toBeInTheDocument();
    expect(screen.getByText("cache")).toBeInTheDocument();
  });

  it("opens Advanced by itself when a stored retention needs fixing", () => {
    renderDialog({ ...database, keep: { recent: "24h0m0s", daily: -1, monthly: 6 } }, { closed: true });
    expect(screen.getByLabelText("Daily copies")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("has no Advanced for a custom backup", () => {
    renderDialog({ ...database, type: "custom", service: "litestream" }, { closed: true });
    expect(screen.queryByRole("button", { name: "Advanced" })).not.toBeInTheDocument();
  });
```

(`keepError` refuses a daily count that is not a whole number from 0 to 3650, so `-1` is a stored retention the form cannot save.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/components/backup-edit-dialog.test.tsx`
Expected: FAIL — there is no "Advanced"; retention and location are always shown.

- [ ] **Step 3: Implement**

In `web/src/components/backup-edit-dialog.tsx`:

1. Add `import { Disclosure } from "@/components/disclosure";`.
2. Move the line `const custom = b.type === "custom";` (with its comment) up to right after `const b = backup;`, and delete it from its old place.
3. After the `marks` state, add:

```tsx
  // Retention and location are Advanced. A stored value the form refuses
  // opens them at once: Save is disabled until it is fixed.
  const [advancedOpen, setAdvancedOpen] = useState(
    () => !custom && !!(keepError(recent, daily, monthly) || locationError(b.type, where, target, pathsFrom(pathsText))),
  );
```

4. Wrap the two `section("retention", …)` and `section("location", …)` calls (inside the `custom ? null : (<fieldset …>…</fieldset>)` block, after the schedule section) in the disclosure:

```tsx
            <Disclosure label="Advanced" open={advancedOpen} onOpenChange={setAdvancedOpen}>
              {section(
                "retention",
                "Keep",
                /* unchanged */
              )}
              {section(
                "location",
                "Location",
                /* unchanged, including the "Skipped" paragraph */
              )}
            </Disclosure>
```

(the two `section(...)` calls move inside verbatim; nothing else in them changes).

5. Update the component comment's first sentence to: `// BackupEditDialog edits a sqlite or files backup's schedule and, under Advanced, its retention and location.`

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/components/backup-edit-dialog.test.tsx src/components/app-backups.test.tsx`
Expected: PASS (`app-backups.test.tsx` only changes the schedule through Edit, which stays visible).

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/components/backup-edit-dialog.tsx web/src/components/backup-edit-dialog.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): backup retention, location and skipped paths under Advanced

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 10: History as a side sheet

History stops being a tab: a "History" button in the Configuration tab's action bar opens a side sheet (and the Overview's "View history" opens it in Task 13). Its open state is `?history=1`, so the old `?tab=history` lands on Configuration with the sheet open.

**Files:**
- Create: `web/src/hooks/use-app-revisions.ts`, `web/src/hooks/use-app-revisions.test.tsx`
- Create: `web/src/hooks/use-history-sheet.ts`, `web/src/hooks/use-history-sheet.test.tsx`
- Create: `web/src/components/app-page/history-sheet.tsx`, `web/src/components/app-page/history-sheet.test.tsx`
- Modify (rewrite): `web/src/components/app-page/history-panel.tsx`
- Modify: `web/src/components/app-editor-panel.tsx`, `web/src/components/app-editor-panel.test.tsx` (`headerActions`)
- Modify: `web/src/components/instance-panel.tsx` (`headerActions`)
- Modify: `web/src/lib/routes.ts`, `web/src/lib/routes.test.ts` (`LEGACY_APP_TABS.history`)
- Modify: `web/src/pages/app-details.tsx`, `web/src/pages/app-details.test.tsx`

**Interfaces:**
- Consumes: `ConfirmDialog` (phase 1), `Sheet*` (`@/components/ui/sheet`), `useTabParam`, `LEGACY_APP_TABS` (phase 1), `type Ownership` (Task 5).
- Produces:
  - `useAppRevisions(appId: string, enabled?: boolean): { data: AppRevisions | null; loading: boolean; error: string | null; reload: () => Promise<void> }` (`@/hooks/use-app-revisions`) — reads `getRevisions` when enabled; a superseded read is dropped.
  - `useHistorySheet(): [open: boolean, setOpen: (open: boolean) => void]` (`@/hooks/use-history-sheet`) — `?history=1`; rewrites an old `?tab=history` to `?tab=configuration&history=1` in one replace. Call it **after** `useTabParam` on the page (its rewrite must be the last one).
  - `HistorySheet({ appId, ownership, open, onOpenChange })` (`@/components/app-page/history-sheet`) — a right side sheet titled "History" around `HistoryPanel`.
  - `HistoryPanel({ appId, ownership })` — "N versions"; each version: what changed (its commit hash on hover: `title="Commit 1a2b3c4d"`), when, "Live" / "Draft, not deployed"; "Deploy draft"; "Roll back" through `ConfirmDialog` (none for a template-managed app).
  - `AppEditorPanel({ appId, headerActions? }: { appId: string; headerActions?: ReactNode })` and `InstancePanel({ instance, current, onSaved, headerActions? })` — `headerActions` render in the panel's sticky action bar right before its Save buttons (phase 3 keeps this prop).
  - `LEGACY_APP_TABS = { editor: "configuration", history: "configuration" }`.
  - On the page: the History button (`variant="outline"`, text "History") is passed to both panels as `headerActions`; the page's tabs no longer include `history`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/hooks/use-app-revisions.test.tsx`:

```tsx
import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { AppsContext, type AppRevisions, type AppsContextValue } from "@/context/apps-context-base";
import { useAppRevisions } from "@/hooks/use-app-revisions";

const revs = (hash: string): AppRevisions => ({ current: hash, deployed: hash, revisions: [{ hash, subject: `at ${hash}`, timestamp: 1 }] });

function wrap(getRevisions: AppsContextValue["getRevisions"]) {
  const value = { getRevisions } as unknown as AppsContextValue;
  return ({ children }: { children: ReactNode }) => <AppsContext.Provider value={value}>{children}</AppsContext.Provider>;
}

describe("useAppRevisions", () => {
  it("reads the history, and again on reload", async () => {
    const getRevisions = vi.fn(async (id: string) => revs(id));
    const { result } = renderHook(() => useAppRevisions("a1"), { wrapper: wrap(getRevisions) });
    await waitFor(() => expect(result.current.data?.current).toBe("a1"));
    await act(() => result.current.reload());
    expect(getRevisions).toHaveBeenCalledTimes(2);
  });

  it("reads nothing until enabled, and reports a failed read", async () => {
    const getRevisions = vi.fn(async () => Promise.reject(new Error("agent offline")));
    const { result, rerender } = renderHook(({ on }) => useAppRevisions("a1", on), {
      wrapper: wrap(getRevisions as unknown as AppsContextValue["getRevisions"]),
      initialProps: { on: false },
    });
    expect(getRevisions).not.toHaveBeenCalled();
    rerender({ on: true });
    await waitFor(() => expect(result.current.error).toBe("agent offline"));
    expect(result.current.loading).toBe(false);
  });

  it("drops a late answer for the app it no longer shows", async () => {
    let first: (v: AppRevisions) => void = () => {};
    const getRevisions = vi.fn((id: string) =>
      id === "a1" ? new Promise<AppRevisions>((resolve) => (first = resolve)) : Promise.resolve(revs(id)),
    );
    const { result, rerender } = renderHook(({ id }) => useAppRevisions(id), {
      wrapper: wrap(getRevisions as unknown as AppsContextValue["getRevisions"]),
      initialProps: { id: "a1" },
    });
    rerender({ id: "a2" });
    await waitFor(() => expect(result.current.data?.current).toBe("a2"));
    await act(async () => first(revs("a1")));
    expect(result.current.data?.current).toBe("a2");
  });
});
```

Create `web/src/hooks/use-history-sheet.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { useHistorySheet } from "@/hooks/use-history-sheet";
import { useTabParam } from "@/hooks/use-tab-param";
import { LEGACY_APP_TABS } from "@/lib/routes";

const TABS = ["overview", "logs", "configuration", "backups", "metrics", "settings"] as const;

// Probe uses the two hooks in the page's order.
function Probe() {
  const [tab] = useTabParam(TABS, "overview", { aliases: LEGACY_APP_TABS });
  const [open, setOpen] = useHistorySheet();
  const { search } = useLocation();
  return (
    <div>
      <span data-testid="tab">{tab}</span>
      <span data-testid="open">{String(open)}</span>
      <span data-testid="search">{search}</span>
      <button onClick={() => setOpen(true)}>open</button>
      <button onClick={() => setOpen(false)}>close</button>
    </div>
  );
}

function renderAt(url: string) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <Probe />
    </MemoryRouter>,
  );
}

describe("useHistorySheet", () => {
  it("opens Configuration with the sheet from an old ?tab=history link, rewriting it once", async () => {
    renderAt("/app?tab=history&x=1");
    expect(screen.getByTestId("tab")).toHaveTextContent("configuration");
    expect(screen.getByTestId("open")).toHaveTextContent("true");
    expect(await screen.findByText("?tab=configuration&x=1&history=1")).toBeInTheDocument();
    expect(screen.getByTestId("open")).toHaveTextContent("true");
  });

  it("keeps the sheet in ?history=1 and drops it on close", async () => {
    const user = userEvent.setup();
    renderAt("/app?tab=logs");
    expect(screen.getByTestId("open")).toHaveTextContent("false");
    await user.click(screen.getByRole("button", { name: "open" }));
    expect(screen.getByTestId("search")).toHaveTextContent("?tab=logs&history=1");
    expect(screen.getByTestId("open")).toHaveTextContent("true");
    await user.click(screen.getByRole("button", { name: "close" }));
    expect(screen.getByTestId("search")).toHaveTextContent(/^\?tab=logs$/);
  });
});
```

Create `web/src/components/app-page/history-sheet.test.tsx`:

```tsx
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { HistorySheet } from "@/components/app-page/history-sheet";
import type { Ownership } from "@/components/app-page/ownership";
import { AppsContext, type AppRevisions, type AppsContextValue } from "@/context/apps-context-base";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

const now = Math.floor(Date.now() / 1000);
const HISTORY: AppRevisions = {
  current: "cccccccc11",
  deployed: "bbbbbbbb11",
  revisions: [
    { hash: "cccccccc11", subject: "Changed compose.yml", timestamp: now - 60 },
    { hash: "bbbbbbbb11", subject: "Changed .env", timestamp: now - 7200 },
    { hash: "aaaaaaaa11", subject: "create", timestamp: now - 86400 },
  ],
};

const ctx = { getRevisions: vi.fn(), rollback: vi.fn(), control: vi.fn(), refresh: vi.fn() };

function renderSheet(ownership: Ownership = null, open = true) {
  const onOpenChange = vi.fn();
  render(
    <AppsContext.Provider value={ctx as unknown as AppsContextValue}>
      <HistorySheet appId="a1" ownership={ownership} open={open} onOpenChange={onOpenChange} />
    </AppsContext.Provider>,
  );
  return onOpenChange;
}

describe("HistorySheet", () => {
  beforeEach(() => {
    for (const f of Object.values(ctx)) f.mockReset();
    for (const f of Object.values(toast)) f.mockReset();
    ctx.getRevisions.mockResolvedValue(HISTORY);
    ctx.rollback.mockResolvedValue(undefined);
    ctx.control.mockResolvedValue(undefined);
  });

  it("lists the versions by what changed and when, with the commit on hover", async () => {
    renderSheet();
    const sheet = await screen.findByRole("dialog", { name: "History" });
    expect(await within(sheet).findByText("3 versions.", { exact: false })).toBeInTheDocument();
    expect(within(sheet).getByText("Changed compose.yml")).toHaveAttribute("title", "Commit cccccccc");
    expect(within(sheet).getByText("Draft, not deployed")).toBeInTheDocument();
    expect(within(sheet).getByText("Live")).toBeInTheDocument();
    expect(within(sheet).getByText("2 hours ago")).toBeInTheDocument();
    expect(sheet).not.toHaveTextContent(/Revision/);
  });

  it("reads nothing while closed", () => {
    renderSheet(null, false);
    expect(ctx.getRevisions).not.toHaveBeenCalled();
  });

  it("rolls back after confirming, and reads the history again", async () => {
    const user = userEvent.setup();
    renderSheet();
    const sheet = await screen.findByRole("dialog", { name: "History" });
    const older = (await within(sheet).findByText("create")).closest("li")!;
    await user.click(within(older).getByRole("button", { name: /Roll back/ }));
    const confirm = await screen.findByRole("alertdialog", { name: "Roll back to “create”?" });
    await user.click(within(confirm).getByRole("button", { name: "Roll back" }));
    await waitFor(() => expect(ctx.rollback).toHaveBeenCalledWith("a1", "aaaaaaaa11"));
    expect(toast.success).toHaveBeenCalledWith("Rolled back and redeployed");
    await waitFor(() => expect(ctx.getRevisions).toHaveBeenCalledTimes(2));
  });

  it("deploys the draft", async () => {
    const user = userEvent.setup();
    renderSheet();
    await user.click(await screen.findByRole("button", { name: "Deploy draft" }));
    await waitFor(() => expect(ctx.control).toHaveBeenCalledWith("a1", "start"));
    expect(toast.success).toHaveBeenCalledWith("Draft deployed");
  });

  it("offers no roll back for an app a template manages", async () => {
    renderSheet({ loading: false, error: "", instance: null, current: null, reload: vi.fn(async () => {}) });
    const sheet = await screen.findByRole("dialog", { name: "History" });
    expect(await within(sheet).findByText(/Managed by a template/)).toBeInTheDocument();
    expect(within(sheet).queryByRole("button", { name: /Roll back/ })).not.toBeInTheDocument();
  });

  it("shows a failed read with Try again", async () => {
    const user = userEvent.setup();
    ctx.getRevisions.mockRejectedValueOnce(new Error("agent offline"));
    renderSheet();
    expect(await screen.findByText("agent offline")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Changed compose.yml")).toBeInTheDocument();
  });
});
```

In `web/src/components/app-editor-panel.test.tsx` add inside its `describe`:

```tsx
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
```

In `web/src/lib/routes.test.ts`, next to `expect(LEGACY_APP_TABS.editor).toBe("configuration");` add `expect(LEGACY_APP_TABS.history).toBe("configuration");`.

In `web/src/pages/app-details.test.tsx`:
- in `renderPage`'s default context value add `getRevisions: vi.fn(async () => ({ current: "", deployed: "", revisions: [] })),` (the History sheet reads it when opened);
- add inside `describe("AppDetailsPage", …)`:

```tsx
  it("opens History from Configuration's bar; History is no tab any more", async () => {
    const user = userEvent.setup();
    const getRevisions = vi.fn(async () => ({
      current: "bbbbbbbb",
      deployed: "bbbbbbbb",
      revisions: [{ hash: "bbbbbbbb", subject: "Changed compose.yml", timestamp: 2 }],
    }));
    renderPage("/app/a1?tab=configuration", {}, { ctx: { getRevisions } });
    expect(screen.queryByRole("tab", { name: "History" })).not.toBeInTheDocument();
    await user.click(await screen.findByRole("button", { name: "History" }));
    const sheet = await screen.findByRole("dialog", { name: "History" });
    expect(await within(sheet).findByText("Changed compose.yml")).toBeInTheDocument();
  });
```

Also change the mock of `@/components/app-editor-panel` at the top of the file so the History button can be found in the plain editor's bar:

```tsx
vi.mock("@/components/app-editor-panel", () => ({
  AppEditorPanel: ({ headerActions }: { headerActions?: React.ReactNode }) => (
    <div>
      {headerActions}
      <p>the plain editor</p>
    </div>
  ),
}));
```

(add `import type React from "react";` if the file has no React type import). The three existing tests that open `?tab=history` ("offers no Roll back in History…", "re-reads the app list when a roll back is refused as managed", "offers no Roll back while a templated app's instance is still being looked up") stay as they are: they now open Configuration with the sheet.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/hooks/use-app-revisions.test.tsx src/hooks/use-history-sheet.test.tsx src/components/app-page/history-sheet.test.tsx src/components/app-editor-panel.test.tsx src/lib/routes.test.ts src/pages/app-details.test.tsx`
Expected: FAIL — the hooks and the sheet do not exist; `headerActions` is ignored; History is still a tab.

- [ ] **Step 3: Implement**

Create `web/src/hooks/use-app-revisions.ts`:

```ts
import { useCallback, useEffect, useRef, useState } from "react";

import type { AppRevisions } from "@/context/apps-context-base";
import { useApps } from "@/context/use-apps";

export type AppRevisionsState = {
  data: AppRevisions | null;
  loading: boolean;
  error: string | null;
  reload: () => Promise<void>;
};

// useAppRevisions reads an app's history from the agent (one round trip)
// while enabled. A read that is no longer the latest (another app, or a
// reload) is dropped.
export function useAppRevisions(appId: string, enabled = true): AppRevisionsState {
  const { getRevisions } = useApps();
  const [data, setData] = useState<AppRevisions | null>(null);
  const [loading, setLoading] = useState(enabled);
  const [error, setError] = useState<string | null>(null);
  const latest = useRef(0);

  const reload = useCallback(async () => {
    const n = ++latest.current;
    setLoading(true);
    setError(null);
    try {
      const next = await getRevisions(appId);
      if (n === latest.current) setData(next);
    } catch (e) {
      if (n === latest.current) setError(e instanceof Error ? e.message : "Failed to load history");
    } finally {
      if (n === latest.current) setLoading(false);
    }
  }, [appId, getRevisions]);

  useEffect(() => {
    if (enabled) void reload();
  }, [enabled, reload]);

  return { data, loading, error, reload };
}
```

Create `web/src/hooks/use-history-sheet.ts`:

```ts
import { useCallback, useEffect } from "react";
import { useSearchParams } from "react-router-dom";

// useHistorySheet keeps the app page's History sheet in the URL
// (?history=1), so a link or a reload opens it. An old ?tab=history link
// (History was a tab) becomes ?tab=configuration&history=1. Call it after
// useTabParam: both rewrite the URL on the first render, and the last
// rewrite wins, so this one must come last.
export function useHistorySheet(): [boolean, (open: boolean) => void] {
  const [params, setParams] = useSearchParams();
  const legacy = params.get("tab") === "history";
  const open = legacy || params.get("history") === "1";

  useEffect(() => {
    if (!legacy) return;
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.set("tab", "configuration");
        next.set("history", "1");
        return next;
      },
      { replace: true },
    );
  }, [legacy, setParams]);

  const setOpen = useCallback(
    (next: boolean) => {
      setParams(
        (prev) => {
          const out = new URLSearchParams(prev);
          if (out.get("tab") === "history") out.set("tab", "configuration");
          if (next) out.set("history", "1");
          else out.delete("history");
          return out;
        },
        { replace: true },
      );
    },
    [setParams],
  );

  return [open, setOpen];
}
```

Replace the whole of `web/src/components/app-page/history-panel.tsx` with:

```tsx
import { useState } from "react";
import { History, RefreshCw } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import type { Ownership } from "@/components/app-page/ownership";
import { ConfirmDialog } from "@/components/confirm-dialog";
import type { AppRevision } from "@/context/apps-context-base";
import { useApps } from "@/context/use-apps";
import { useAppRevisions } from "@/hooks/use-app-revisions";
import { isManagedByTemplate } from "@/lib/templates";
import { cn, relativeTime } from "@/lib/utils";

// HistoryPanel lists an app's versions, newest first: what changed and
// when (the commit hash on hover), which one is live, an undeployed draft
// with Deploy draft, and Roll back for an older one — not for an app a
// template manages: its template owns the files (its name may still be
// loading).
export function HistoryPanel({ appId, ownership }: { appId: string; ownership: Ownership }) {
  const templated = ownership !== null;
  const managedBy = ownership?.instance?.template_name;
  const { rollback, control, refresh } = useApps();
  const { data, loading, error, reload } = useAppRevisions(appId);
  const [busy, setBusy] = useState(false);
  const [target, setTarget] = useState<AppRevision | null>(null);

  const doRollback = async (hash: string) => {
    setBusy(true);
    try {
      await rollback(appId, hash);
      toast.success("Rolled back and redeployed");
      await reload();
    } catch (e) {
      toast.error("Roll back failed", { description: e instanceof Error ? e.message : undefined });
      // A template took the app over meanwhile: show it as managed.
      if (e instanceof Error && isManagedByTemplate(e.message)) void refresh();
    } finally {
      setBusy(false);
    }
  };

  // Deploying the current draft is a plain start: the worktree already holds
  // HEAD, so materialize + compose up brings the draft live.
  const doDeploy = async () => {
    setBusy(true);
    try {
      await control(appId, "start");
      toast.success("Draft deployed");
      await reload();
    } catch (e) {
      toast.error("Deploy failed", { description: e instanceof Error ? e.message : undefined });
    } finally {
      setBusy(false);
    }
  };

  if (loading && !data) {
    return (
      <div className="flex h-40 items-center justify-center">
        <Spinner />
      </div>
    );
  }
  if (error) {
    return (
      <div className="flex h-40 flex-col items-center justify-center gap-3">
        <p className="text-sm text-destructive">{error}</p>
        <Button size="sm" variant="outline" onClick={() => void reload()}>
          Try again
        </Button>
      </div>
    );
  }
  if (!data || data.revisions.length === 0) {
    return <p className="py-10 text-center text-sm text-muted-foreground">No history yet. Every saved change shows up here.</p>;
  }

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          {data.revisions.length === 1 ? "1 version" : `${data.revisions.length} versions`}.{" "}
          {templated
            ? `Managed by ${managedBy ? `template ${managedBy}` : "a template"}: it changes through its values or an update; detach it to roll back.`
            : "Rolling back adds a new version; nothing is lost."}
        </p>
        <Button
          size="icon"
          variant="ghost"
          className="size-8 shrink-0 text-muted-foreground"
          aria-label="Refresh history"
          title="Refresh history"
          onClick={() => void reload()}
          disabled={loading}
        >
          <RefreshCw className={cn("size-4", loading && "animate-spin motion-reduce:animate-none")} />
        </Button>
      </div>
      <ol aria-label="Versions">
        {data.revisions.map((rev, i) => {
          const isCurrent = rev.hash === data.current;
          const isDeployed = data.deployed !== "" && rev.hash === data.deployed;
          const isUndeployedDraft = isCurrent && data.deployed !== "" && !isDeployed;
          const isLast = i === data.revisions.length - 1;
          const when = new Date(rev.timestamp * 1000);
          return (
            <li key={rev.hash} className="relative flex gap-3 pb-5 last:pb-0">
              {!isLast ? <span className="absolute top-4 bottom-0 left-[6px] w-px bg-border" aria-hidden /> : null}
              <span
                className={cn(
                  "relative mt-1 size-[13px] shrink-0 rounded-full border-2 bg-background",
                  isDeployed
                    ? "border-status-ok bg-status-ok ring-4 ring-status-ok/15"
                    : isUndeployedDraft
                      ? "border-status-warn"
                      : "border-muted-foreground/40",
                )}
                aria-hidden
              />
              <div className="flex min-w-0 flex-1 flex-wrap items-start gap-x-3 gap-y-2">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="truncate text-sm font-medium" title={`Commit ${rev.hash.slice(0, 8)}`}>
                      {rev.subject}
                    </span>
                    {isDeployed ? <span className="text-xs font-medium text-status-ok">Live</span> : null}
                    {isUndeployedDraft ? <span className="text-xs font-medium text-status-warn">Draft, not deployed</span> : null}
                  </div>
                  <time className="text-xs text-muted-foreground" dateTime={when.toISOString()} title={when.toLocaleString()}>
                    {relativeTime(when)}
                  </time>
                </div>
                {isUndeployedDraft ? (
                  <Button size="sm" onClick={() => void doDeploy()} disabled={busy}>
                    Deploy draft
                  </Button>
                ) : !isCurrent && !templated ? (
                  <Button size="sm" variant="ghost" disabled={busy} onClick={() => setTarget(rev)}>
                    <History className="size-4" aria-hidden /> Roll back
                  </Button>
                ) : null}
              </div>
            </li>
          );
        })}
      </ol>
      <ConfirmDialog
        open={target !== null}
        onOpenChange={(open) => {
          if (!open) setTarget(null);
        }}
        title={target ? `Roll back to “${target.subject}”?` : "Roll back?"}
        description="The app's files and variables go back to this version as a new entry in the history, and the app is redeployed. Nothing is lost: you can roll forward again."
        confirmLabel="Roll back"
        pendingLabel="Rolling back…"
        destructive={false}
        onConfirm={async () => {
          if (target) await doRollback(target.hash);
        }}
      />
    </div>
  );
}
```

Create `web/src/components/app-page/history-sheet.tsx`:

```tsx
import { HistoryPanel } from "@/components/app-page/history-panel";
import type { Ownership } from "@/components/app-page/ownership";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";

// HistorySheet is the app's history in a side sheet (full width on a
// phone), opened from Configuration's bar or the Overview. Nothing is read
// while it is closed.
export function HistorySheet({
  appId,
  ownership,
  open,
  onOpenChange,
}: {
  appId: string;
  ownership: Ownership;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="flex w-full flex-col gap-0 p-0 sm:max-w-md">
        <SheetHeader className="border-b px-5 py-4 text-left">
          <SheetTitle>History</SheetTitle>
          <SheetDescription>Every saved change, newest first.</SheetDescription>
        </SheetHeader>
        <div className="flex-1 overflow-y-auto px-5 py-4">{open ? <HistoryPanel appId={appId} ownership={ownership} /> : null}</div>
      </SheetContent>
    </Sheet>
  );
}
```

In `web/src/components/app-editor-panel.tsx`:
- change the React import to `import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";`;
- change the signature to `export function AppEditorPanel({ appId, headerActions }: { appId: string; headerActions?: ReactNode }) {` and add to its comment: `headerActions (the page's History button) sit in the action bar before the Save buttons.`;
- in the `StickyActionBar`, insert `{headerActions}` right after the closing `</p>` and before the "Discard" button.

In `web/src/components/instance-panel.tsx`:
- add `import type { ReactNode } from "react";` (or add `type ReactNode` to its React import);
- add `headerActions` to the destructured props and `headerActions?: ReactNode;` (comment: `// headerActions sit in the bar before Save (the page's History button).`) to the props type;
- in its `StickyActionBar`, insert `{headerActions}` right after the closing `</p>` and before the Save `<Button>`.

In `web/src/lib/routes.ts` change the constant to:

```ts
// LEGACY_APP_TABS maps an app page ?tab= value of before the rebuild to
// its new name (the URL is rewritten in place). History is a side sheet
// now: useHistorySheet opens it for an old ?tab=history.
export const LEGACY_APP_TABS = { editor: "configuration", history: "configuration" } as const satisfies Record<string, AppTab>;
```

In `web/src/pages/app-details.tsx`:
1. Imports: add

```tsx
import type { ReactNode } from "react";
import { History } from "lucide-react";
import { HistorySheet } from "@/components/app-page/history-sheet";
import { useHistorySheet } from "@/hooks/use-history-sheet";
```

(merge `ReactNode` and `History` into the existing `react` / `lucide-react` imports) and remove `import { HistoryPanel } from "@/components/app-page/history-panel";`.
2. `TABS` loses `"history"`: `const TABS = ["logs", "metrics", "configuration", "backups", "settings"] as const;` (delete the "History stays a tab until phase 2" comment), and the `{ value: "history", label: "History" }` item and the `<TabsContent value="history">…</TabsContent>` block are deleted.
3. Right after the `useTabParam` line add `const [historyOpen, setHistoryOpen] = useHistorySheet();`.
4. `ConfigurationTab` takes the bar's extra actions and passes them to both panels:

```tsx
// ConfigurationTab is the app's editor, or for an app a template manages
// its instance's parameters (never the plain editor, whose save would be
// refused). headerActions (the History button) go in either panel's bar.
function ConfigurationTab({
  appId,
  ownership,
  onSaved,
  headerActions,
}: {
  appId: string;
  ownership: Ownership;
  onSaved: () => void;
  headerActions?: ReactNode;
}) {
  if (ownership?.error) return <LoadError message={ownership.error} onRetry={() => void ownership.reload()} />;
  if (ownership?.loading) return <PageSpinner />;
  if (ownership?.instance && ownership.current) {
    const inst = ownership.instance;
    return (
      <InstancePanel
        key={`${inst.revision}-${inst.updated_at}`}
        instance={inst}
        current={ownership.current}
        onSaved={onSaved}
        headerActions={headerActions}
      />
    );
  }
  return <AppEditorPanel appId={appId} headerActions={headerActions} />;
}
```

5. The Configuration panel's mount becomes:

```tsx
        <TabsContent value="configuration" className="mt-0">
          <ConfigurationTab
            appId={app.id}
            ownership={ownership}
            onSaved={reloadManaged}
            headerActions={
              <Button variant="outline" onClick={() => setHistoryOpen(true)}>
                <History className="size-4" aria-hidden /> History
              </Button>
            }
          />
        </TabsContent>
```

6. As the last child of the page's root `<div>`, add `<HistorySheet key={app.id} appId={app.id} ownership={ownership} open={historyOpen} onOpenChange={setHistoryOpen} />`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/hooks/use-app-revisions.test.tsx src/hooks/use-history-sheet.test.tsx src/components/app-page/history-sheet.test.tsx src/components/app-editor-panel.test.tsx src/lib/routes.test.ts src/pages/app-details.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/hooks/use-app-revisions.ts web/src/hooks/use-app-revisions.test.tsx web/src/hooks/use-history-sheet.ts web/src/hooks/use-history-sheet.test.tsx web/src/components/app-page/history-sheet.tsx web/src/components/app-page/history-sheet.test.tsx web/src/components/app-page/history-panel.tsx web/src/components/app-editor-panel.tsx web/src/components/app-editor-panel.test.tsx web/src/components/instance-panel.tsx web/src/lib/routes.ts web/src/lib/routes.test.ts web/src/pages/app-details.tsx web/src/pages/app-details.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): History as a side sheet opened from Configuration (?history=1)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 11: Delete and Save as template — one dialog, one implementation each

The header's "⋯" (Task 14) and the Settings tab both offer Delete and Save as template. Each gets one hook; Delete gets one controlled dialog on `ConfirmDialog`, mounted once on the page.

**Files:**
- Create: `web/src/hooks/use-app-actions.ts`
- Modify (rewrite): `web/src/components/delete-app-dialog.tsx`, `web/src/components/delete-app-dialog.test.tsx`
- Modify: `web/src/components/app-page/app-settings-tab.tsx`
- Modify: `web/src/pages/app-details.tsx`, `web/src/pages/app-details.test.tsx`

**Interfaces:**
- Consumes: `ConfirmDialog` (phase 1), `deleteOutcome`, `type DeleteChoice` (`@/lib/delete-app`), `templateFromApp` (`@/lib/template-draft`), `NEW_TEMPLATE_PATH` (phase 1), `useServerPath`.
- Produces:
  - `useDeleteApp(appId: string): (choice: DeleteChoice) => Promise<void>` (`@/hooks/use-app-actions`) — deletes, leaves for the Apps list, toasts the outcome (`deleteOutcome`); on failure toasts "Delete failed" and **rethrows** (the dialog stays open for a retry).
  - `useSaveAsTemplate(appId: string, name: string): { run: () => Promise<void>; busy: boolean }` — reads the app, opens `NEW_TEMPLATE_PATH` with the draft state; "Couldn't read the app" on failure.
  - `DeleteAppDialog({ name, open, onOpenChange, onConfirm }: { name: string; open: boolean; onOpenChange: (open: boolean) => void; onConfirm: (choice: DeleteChoice) => Promise<void> })` — `ConfirmDialog` "Delete `name`?" with the two opt-ins (unticked at every opening), the action named by the choice ("Delete app", "Delete app and volumes", …), pending label "Deleting `name`…" and a status line while it runs.
  - `AppSettingsTab` props become `{ appId, name, ownership, onUnlinked, onRename, onDelete: () => void, onSaveAsTemplate: () => void, savingTemplate: boolean }` (Task 12 replaces the tab).

- [ ] **Step 1: Write the failing tests**

Replace the whole of `web/src/components/delete-app-dialog.test.tsx` with:

```tsx
import { useState } from "react";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { DeleteAppDialog } from "@/components/delete-app-dialog";
import type { DeleteChoice } from "@/lib/delete-app";

function Harness({ onConfirm }: { onConfirm: (c: DeleteChoice) => Promise<void> }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        open
      </button>
      <DeleteAppDialog name="Shop" open={open} onOpenChange={setOpen} onConfirm={onConfirm} />
    </>
  );
}

describe("DeleteAppDialog", () => {
  it("keeps volumes and backups unless ticked", async () => {
    const user = userEvent.setup({ delay: null });
    const onConfirm = vi.fn(async () => {});
    render(<Harness onConfirm={onConfirm} />);
    await user.click(screen.getByRole("button", { name: "open" }));
    expect(screen.getByRole("alertdialog", { name: "Delete Shop?" })).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "Also delete its volumes" })).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "Also delete its backups" })).not.toBeChecked();
    expect(screen.getAllByRole("checkbox").map((c) => c.id)).toEqual(["delete-volumes", "delete-backups"]);
    await user.click(screen.getByRole("button", { name: "Delete app" }));
    expect(onConfirm).toHaveBeenLastCalledWith({ withVolumes: false, withBackups: false });
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
  });

  it("names what it deletes once ticked, and starts unticked at every opening", async () => {
    const user = userEvent.setup({ delay: null });
    const onConfirm = vi.fn(async () => {});
    render(<Harness onConfirm={onConfirm} />);
    await user.click(screen.getByRole("button", { name: "open" }));
    await user.click(screen.getByRole("checkbox", { name: "Also delete its backups" }));
    expect(screen.getByRole("button", { name: "Delete app and backups" })).toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: "Also delete its volumes" }));
    await user.click(screen.getByRole("button", { name: "Delete app, volumes and backups" }));
    expect(onConfirm).toHaveBeenLastCalledWith({ withVolumes: true, withBackups: true });
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());

    await user.click(screen.getByRole("button", { name: "open" }));
    expect(screen.getByRole("checkbox", { name: "Also delete its volumes" })).not.toBeChecked();
    await user.click(screen.getByRole("checkbox", { name: "Also delete its volumes" }));
    await user.click(screen.getByRole("button", { name: "Delete app and volumes" }));
    expect(onConfirm).toHaveBeenLastCalledWith({ withVolumes: true, withBackups: false });
  });

  it("shows progress while the delete runs, and can't be sent again", async () => {
    const user = userEvent.setup({ delay: null });
    let finish: () => void = () => {};
    render(<Harness onConfirm={() => new Promise<void>((resolve) => (finish = resolve))} />);
    await user.click(screen.getByRole("button", { name: "open" }));
    await user.click(screen.getByRole("button", { name: "Delete app" }));
    expect(await screen.findByRole("button", { name: "Deleting Shop…" })).toBeDisabled();
    expect(screen.getByRole("status")).toHaveTextContent("This can take a few minutes");
    await act(async () => finish());
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
  });

  it("stays open after a failed delete, for a retry", async () => {
    const user = userEvent.setup({ delay: null });
    const onConfirm = vi.fn(async () => Promise.reject(new Error("timed out")));
    render(<Harness onConfirm={onConfirm} />);
    await user.click(screen.getByRole("button", { name: "open" }));
    await user.click(screen.getByRole("button", { name: "Delete app" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Delete app" })).toBeEnabled());
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });
});
```

In `web/src/pages/app-details.test.tsx`, in `describe("AppDetailsPage", …)`:
- in "shows progress while an app is deleted, and where its backups stayed" replace the two clicks with

```tsx
    await user.click(await screen.findByRole("button", { name: "Delete app…" }));
    await user.click(screen.getByRole("button", { name: "Delete app" }));
```

and `screen.findByRole("button", { name: /Deleting Shop/ })` stays;
- replace "reports a failed delete and a delete with warnings" with:

```tsx
  it("keeps the dialog after a failed delete, then reports a delete with warnings", async () => {
    const user = userEvent.setup({ delay: null });
    remove.mockRejectedValueOnce(new Error("timed out"));
    renderPage("/app/a1?tab=settings");
    await user.click(await screen.findByRole("button", { name: "Delete app…" }));
    await user.click(screen.getByRole("button", { name: "Delete app" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Delete failed", { description: "timed out" }));
    remove.mockResolvedValueOnce({ warnings: ["volume x is in use"], volumesDeleted: false, backupsDeleted: false });
    // The dialog is still open: confirm again.
    await user.click(await screen.findByRole("button", { name: "Delete app" }));
    await waitFor(() =>
      expect(toast.warning).toHaveBeenCalledWith("App deleted with cleanup warnings", { description: "volume x is in use" }),
    );
    expect(await screen.findByText("the apps")).toBeInTheDocument();
  });
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/components/delete-app-dialog.test.tsx src/pages/app-details.test.tsx`
Expected: FAIL — the dialog is not controlled; the Settings tab has no "Delete app…" button.

- [ ] **Step 3: Implement**

Create `web/src/hooks/use-app-actions.ts`:

```ts
import { useCallback, useState } from "react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";

import type { DeleteAppResult } from "@/context/apps-context-base";
import { useApps } from "@/context/use-apps";
import { useServerPath } from "@/context/use-server-path";
import { deleteOutcome, type DeleteChoice } from "@/lib/delete-app";
import { appLabel } from "@/lib/kpi";
import { NEW_TEMPLATE_PATH } from "@/lib/routes";
import { templateFromApp } from "@/lib/template-draft";
import type { DraftState } from "@/pages/template-page";

// useDeleteApp deletes an app and leaves its page for the Apps list,
// telling what was kept (volumes, backups) or why not everything went. A
// failure is toasted and thrown again, so the confirm dialog stays open for
// a retry.
export function useDeleteApp(appId: string): (choice: DeleteChoice) => Promise<void> {
  const { remove, backupsByApp } = useApps();
  const navigate = useNavigate();
  const toServer = useServerPath();
  return useCallback(
    async (choice: DeleteChoice) => {
      const hadBackups = (backupsByApp?.[appId]?.length ?? 0) > 0;
      let res: DeleteAppResult;
      try {
        res = await remove(appId, { deleteVolumes: choice.withVolumes, deleteBackups: choice.withBackups });
      } catch (e) {
        toast.error("Delete failed", { description: e instanceof Error ? e.message : undefined });
        throw e;
      }
      navigate(toServer());
      const out = deleteOutcome(choice, res, { hadBackups });
      if (out.level === "warning") toast.warning(out.title, { description: out.description });
      else toast.success(out.title, { description: out.description });
    },
    [appId, backupsByApp, navigate, remove, toServer],
  );
}

// useSaveAsTemplate opens a new template's editor prefilled from what the
// app's editor holds (its files, its variables as parameters, its hosts and
// backup settings); nothing is saved until the template is. busy while the
// app is read.
export function useSaveAsTemplate(appId: string, name: string): { run: () => Promise<void>; busy: boolean } {
  const { getApp } = useApps();
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const run = useCallback(async () => {
    setBusy(true);
    try {
      const state: DraftState = { ...templateFromApp(appId, await getApp(appId)), appName: appLabel(name, appId) };
      navigate(NEW_TEMPLATE_PATH, { state });
    } catch (e) {
      toast.error("Couldn't read the app", { description: e instanceof Error ? e.message : undefined });
      setBusy(false);
    }
  }, [appId, getApp, name, navigate]);
  return { run, busy };
}
```

Replace the whole of `web/src/components/delete-app-dialog.tsx` with:

```tsx
import { useState } from "react";

import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { ConfirmDialog } from "@/components/confirm-dialog";
import type { DeleteChoice } from "@/lib/delete-app";
import { cn } from "@/lib/utils";

// DeleteAppDialog confirms deleting an app and, opt-in, its volumes and its
// backups. Every opening starts with the safe default (unticked). onConfirm
// runs the delete: the dialog shows it pending (it can take minutes), closes
// when it resolves and stays open when it rejects.
export function DeleteAppDialog({
  name,
  open,
  onOpenChange,
  onConfirm,
}: {
  name: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onConfirm: (choice: DeleteChoice) => Promise<void>;
}) {
  const [withVolumes, setWithVolumes] = useState(false);
  const [withBackups, setWithBackups] = useState(false);
  const [deleting, setDeleting] = useState(false);
  // A new opening resets the choices (adjusted during render).
  const [wasOpen, setWasOpen] = useState(open);
  if (wasOpen !== open) {
    setWasOpen(open);
    if (open) {
      setWithVolumes(false);
      setWithBackups(false);
    }
  }
  const label =
    withVolumes && withBackups
      ? "Delete app, volumes and backups"
      : withVolumes
        ? "Delete app and volumes"
        : withBackups
          ? "Delete app and backups"
          : "Delete app";
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={`Delete ${name}?`}
      description="Its containers are stopped and removed, along with the deployment and its whole history. This cannot be undone."
      confirmLabel={label}
      pendingLabel={`Deleting ${name}…`}
      onConfirm={async () => {
        setDeleting(true);
        try {
          await onConfirm({ withVolumes, withBackups });
        } finally {
          setDeleting(false);
        }
      }}
    >
      <Choice
        id="delete-volumes"
        checked={withVolumes}
        disabled={deleting}
        onChange={setWithVolumes}
        label="Also delete its volumes"
        hint="Permanently erases the data in this app's Docker volumes. Unticked, the volumes are kept; you can delete them later in Settings → Docker."
      />
      <Choice
        id="delete-backups"
        checked={withBackups}
        disabled={deleting}
        onChange={setWithBackups}
        label="Also delete its backups"
        hint="Permanently deletes every copy of this app in its backup storages. Unticked, the copies stay in the storage and expire per the server's retention settings; you can delete them sooner in Settings → Storages."
      />
      {deleting ? (
        <p role="status" className="text-sm text-muted-foreground">
          Deleting the app. This can take a few minutes, longer when its backups are deleted from a storage. Keep this page open.
        </p>
      ) : null}
    </ConfirmDialog>
  );
}

// Choice is one opt-in: neutral until ticked, then unmistakably
// destructive — the safe choice (keep the data) is the default.
function Choice({
  id,
  checked,
  disabled,
  onChange,
  label,
  hint,
}: {
  id: string;
  checked: boolean;
  disabled: boolean;
  onChange: (v: boolean) => void;
  label: string;
  hint: string;
}) {
  return (
    <div
      className={cn(
        "flex items-start gap-3 rounded-md border p-3 transition-colors duration-150",
        checked && "border-destructive/50 bg-destructive/5",
      )}
    >
      <Checkbox
        id={id}
        className="mt-px data-[state=checked]:border-destructive data-[state=checked]:bg-destructive data-[state=checked]:text-destructive-foreground"
        aria-describedby={`${id}-hint`}
        checked={checked}
        disabled={disabled}
        onCheckedChange={(v) => onChange(v === true)}
      />
      <div className="grid gap-1">
        <Label htmlFor={id} className={cn("cursor-pointer leading-snug", checked && "text-destructive")}>
          {label}
        </Label>
        <p id={`${id}-hint`} className="text-sm leading-snug text-muted-foreground">
          {hint}
        </p>
      </div>
    </div>
  );
}
```

In `web/src/components/app-page/app-settings-tab.tsx` (the old Settings tab moved in Task 5):
- props: replace `onDelete: (opts: { deleteVolumes: boolean; deleteBackups: boolean }) => Promise<DeleteAppResult>;` with `onDelete: () => void;` and add `onSaveAsTemplate: () => void; savingTemplate: boolean;` (destructure them too);
- delete the `deleting` state, the `saveAsTemplate` and `del` functions, `navigate`, and `backupsByApp`/`getApp` (drop `useApps` if nothing else reads it);
- the Save as template button becomes `<Button variant="outline" disabled={busy || savingTemplate} onClick={onSaveAsTemplate}>Save as template</Button>`;
- replace the `<DeleteAppDialog … />` element with

```tsx
          <Button
            variant="outline"
            className="border-destructive/40 text-destructive hover:bg-destructive/10 hover:text-destructive"
            disabled={busy}
            onClick={onDelete}
          >
            <Trash2 className="size-4" aria-hidden /> Delete app…
          </Button>
```

(add `import { Trash2 } from "lucide-react";`) and remove the imports that are now unused (`DeleteAppDialog`, `deleteOutcome`, `DeleteChoice`, `DeleteAppResult`, `templateFromApp`, `NEW_TEMPLATE_PATH`, `DraftState`, `appLabel`, `useNavigate`).

In `web/src/pages/app-details.tsx`:
1. Add imports `import { DeleteAppDialog } from "@/components/delete-app-dialog";` and `import { useDeleteApp, useSaveAsTemplate } from "@/hooks/use-app-actions";`.
2. Before the `if (!app)` early return add:

```tsx
  const [deleteOpen, setDeleteOpen] = useState(false);
  const deleteApp = useDeleteApp(appId ?? "");
  const saveAsTemplate = useSaveAsTemplate(appId ?? "", app?.name ?? "");
```

3. On `<AppSettingsTab …>` replace the `onDelete={async (opts) => { … }}` prop with

```tsx
            onDelete={() => setDeleteOpen(true)}
            onSaveAsTemplate={() => void saveAsTemplate.run()}
            savingTemplate={saveAsTemplate.busy}
```

4. Next to `<HistorySheet …/>` add `<DeleteAppDialog name={appLabel(app.name, app.id)} open={deleteOpen} onOpenChange={setDeleteOpen} onConfirm={deleteApp} />`.
5. Remove `remove` from the `useApps()` destructuring and `navigate`/`useNavigate`/`toServer`/`useServerPath` if nothing else on the page uses them (`tsc -b` says).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/components/delete-app-dialog.test.tsx src/pages/app-details.test.tsx`
Expected: PASS (the "Save as template" tests pass unchanged: the button still says "Save as template" and now runs the hook).

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/hooks/use-app-actions.ts web/src/components/delete-app-dialog.tsx web/src/components/delete-app-dialog.test.tsx web/src/components/app-page/app-settings-tab.tsx web/src/pages/app-details.tsx web/src/pages/app-details.test.tsx
git commit -m "$(cat <<'EOF'
refactor(web): one Delete dialog on ConfirmDialog, Delete and Save as template hooks

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 12: The Settings tab — General, Template, Danger zone

Settings becomes three sections. **General** edits name, icon, colour and description with one Save: a new name goes through the rename route (it commits without redeploying), the icon, colour and description through the app's save (`getApp` → `stateFromDetail` → change only what changed → `buildSavePayload` → `saveApp`). That save redeploys only a running app with nothing undeployed; for a stopped app, or one with an undeployed draft, it is saved as a draft so a colour never starts an app or deploys someone's draft. A template-managed app (the API answers 409 to `save-app`) can change its name only. **Template** has Save as template, or Detach (was "Unlink"). **Danger zone** has Delete.

The Configuration editor keeps its "Details" section (name/icon/colour/description) in this phase: phase 3 Task 9 removes it from the Configuration view when it redesigns the editor.

**Files:**
- Create: `web/src/lib/app-details-save.ts`, `web/src/lib/app-details-save.test.ts`
- Modify (rewrite): `web/src/components/app-page/app-settings-tab.tsx`
- Create: `web/src/components/app-page/app-settings-tab.test.tsx`
- Modify: `web/src/components/icon-picker.tsx` (an accessible name, `disabled`)
- Modify: `web/src/pages/app-details.tsx`, `web/src/pages/app-details.test.tsx`

**Interfaces:**
- Consumes: `stateFromDetail`, `buildSavePayload` (`@/lib/app-editor-io`), `hasRunningContainers` (`@/lib/app-status`), `Section`, `ConfirmDialog` (phase 1), `useTemplatesApi().unlinkInstance`, `isManagedByTemplate`.
- Produces:
  - `type AppDetailsInput = { name: string; icon: string; color: string; description: string }`, `type AppDetailsDeps = Pick<AppsContextValue, "rename" | "getApp" | "getRevisions" | "getPublicKey" | "saveApp">`, `saveAppDetails(deps, appId, before, after, running): Promise<{ renamed: boolean; saved: "none" | "deployed" | "draft" }>` (`@/lib/app-details-save`).
  - `AppSettingsTab({ app, ownership, running, onDetached, onSaveAsTemplate, savingTemplate, onDelete }: { app: App; ownership: Ownership; running: boolean; onDetached: () => void; onSaveAsTemplate: () => void; savingTemplate: boolean; onDelete: () => void })`.
  - `IconPicker({ …, label?: string = "Icon", disabled?: boolean })` — the trigger is named by `label`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/app-details-save.test.ts`:

```ts
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { AppDetailPayload } from "@/context/apps-context-base";
import { saveAppDetails, type AppDetailsDeps } from "@/lib/app-details-save";

const detail: AppDetailPayload = {
  app: {
    app_id: "a1",
    config: btoa(
      JSON.stringify({ name: "Shop", description: "Old", icon: "store", color: "#112233", files: [{ filename: "compose.yml" }], variables: [] }),
    ),
    files: [{ name: "compose.yml", content: btoa("services: {}\n") }],
    variables: [],
  },
  revision: 1,
  available_revisions: null,
};
const before = { name: "Shop", icon: "store", color: "#112233", description: "Old" };

let deps: AppDetailsDeps & Record<string, ReturnType<typeof vi.fn>>;
function makeDeps(revisions = { current: "bbb", deployed: "bbb", revisions: [] }) {
  deps = {
    rename: vi.fn(async () => {}),
    getApp: vi.fn(async () => detail),
    getRevisions: vi.fn(async () => revisions),
    getPublicKey: vi.fn(async () => "K"),
    saveApp: vi.fn(async () => []),
  } as unknown as typeof deps;
}

const saved = () => deps.saveApp.mock.calls[0][0] as { draft: boolean; config: Record<string, unknown>; files: { name: string }[] };

describe("saveAppDetails", () => {
  beforeEach(() => makeDeps());

  it("renames through the rename route alone when only the name changed", async () => {
    expect(await saveAppDetails(deps, "a1", before, { ...before, name: "  Store " }, true)).toEqual({ renamed: true, saved: "none" });
    expect(deps.rename).toHaveBeenCalledWith("a1", "Store");
    expect(deps.getApp).not.toHaveBeenCalled();
    expect(deps.saveApp).not.toHaveBeenCalled();
  });

  it("saves a running app's new colour with its files, and redeploys it", async () => {
    expect(await saveAppDetails(deps, "a1", before, { ...before, color: "#ff0000" }, true)).toEqual({ renamed: false, saved: "deployed" });
    expect(deps.rename).not.toHaveBeenCalled();
    expect(saved().draft).toBe(false);
    expect(saved().config).toMatchObject({ name: "Shop", color: "#ff0000", icon: "store", description: "Old" });
    expect(saved().files.map((f) => f.name)).toEqual(["compose.yml"]);
  });

  it("keeps the change as a draft for a stopped app: a colour never starts it", async () => {
    expect((await saveAppDetails(deps, "a1", before, { ...before, description: "New" }, false)).saved).toBe("draft");
    expect(saved().draft).toBe(true);
    expect(saved().config).toMatchObject({ description: "New" });
  });

  it("keeps the change as a draft beside undeployed changes: it never deploys them", async () => {
    makeDeps({ current: "ccc", deployed: "bbb", revisions: [] });
    expect((await saveAppDetails(deps, "a1", before, { ...before, icon: "cart" }, true)).saved).toBe("draft");
    expect(saved().draft).toBe(true);
  });

  it("renames first, then saves only what changed", async () => {
    await saveAppDetails(deps, "a1", before, { ...before, name: "Store", icon: "cart" }, true);
    expect(deps.rename.mock.invocationCallOrder[0]).toBeLessThan(deps.getApp.mock.invocationCallOrder[0]);
    // The stored description is kept as it was read, never as the form had it.
    expect(saved().config).toMatchObject({ icon: "cart", description: "Old", color: "#112233" });
  });
});
```

Create `web/src/components/app-page/app-settings-tab.test.tsx`:

```tsx
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppSettingsTab } from "@/components/app-page/app-settings-tab";
import type { Ownership } from "@/components/app-page/ownership";
import { AppsContext, type App, type AppsContextValue } from "@/context/apps-context-base";
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

function renderTab(over: { ownership?: Ownership; running?: boolean } = {}) {
  const props = { onDetached: vi.fn(), onSaveAsTemplate: vi.fn(), onDelete: vi.fn() };
  render(
    <sse.Provider>
      <AppsContext.Provider value={ctx as unknown as AppsContextValue}>
        <AppSettingsTab app={APP} ownership={over.ownership ?? null} running={over.running ?? true} savingTemplate={false} {...props} />
      </AppsContext.Provider>
    </sse.Provider>,
  );
  return props;
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

  it("saves a new colour and description through the app's save, redeploying a running app", async () => {
    const user = userEvent.setup();
    renderTab();
    const description = await screen.findByDisplayValue("Old");
    fireEvent.change(screen.getByLabelText("Colour"), { target: { value: "#ff0000" } });
    await user.clear(description);
    await user.type(description, "A small shop");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Saved"));
    const body = ctx.saveApp.mock.calls[0][0] as { draft: boolean; config: Record<string, unknown> };
    expect(body.draft).toBe(false);
    expect(body.config).toMatchObject({ color: "#ff0000", description: "A small shop" });
    expect(ctx.rename).not.toHaveBeenCalled();
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
    expect(screen.getByLabelText("Colour")).toBeDisabled();
    expect(screen.getByLabelText("Description")).toBeDisabled();
    expect(screen.getByRole("button", { name: "Icon" })).toBeDisabled();
    await user.type(screen.getByRole("textbox", { name: "App name" }), "!");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(ctx.rename).toHaveBeenCalledWith("a1", "Shop!"));
    expect(ctx.saveApp).not.toHaveBeenCalled();
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
```

In `web/src/pages/app-details.test.tsx`:
- in `renderPage`'s default context add

```tsx
    getApp: vi.fn(async () => ({
      app: { app_id: "a1", config: btoa(JSON.stringify({ name: "Shop", description: "" })), files: [], variables: [] },
      revision: 1,
      available_revisions: null,
    })),
```

  (tests that pass their own `getApp` keep overriding it);
- rename the template-instance Settings expectations to the new words:
  - "does not offer Save as template for an app a template manages": `{ name: "Unlink from template" }` → `{ name: "Detach from template" }`;
  - "unlinks from the template after confirming" → title "detaches from the template after confirming"; click `"Detach from template"`; expect the text `"The app becomes an ordinary app you can edit freely; template updates no longer reach it."`; click `"Detach"`; expect `toast.success` with `"Detached from the template"` (the posted body and `refresh` assertions stay);
  - "offers no Unlink while the instance is pending (the API refuses it)" → title "offers no Detach while …"; `findByText(/Detach becomes available once/)`; `queryByRole("button", { name: "Detach from template" })`;
  - "reports a failed unlink" → title "reports a failed detach"; click `"Detach from template"` then `"Detach"`; expect `toast.error` with `"Detach failed", { description: "instance not found" }`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/app-details-save.test.ts src/components/app-page/app-settings-tab.test.tsx src/pages/app-details.test.tsx`
Expected: FAIL — `saveAppDetails` does not exist; the Settings tab has no General form, says "Unlink".

- [ ] **Step 3: Implement**

Create `web/src/lib/app-details-save.ts`:

```ts
import type { AppsContextValue } from "@/context/apps-context-base";
import { buildSavePayload, stateFromDetail } from "@/lib/app-editor-io";

// AppDetailsInput is what Settings → General edits.
export type AppDetailsInput = { name: string; icon: string; color: string; description: string };

export type AppDetailsDeps = Pick<AppsContextValue, "rename" | "getApp" | "getRevisions" | "getPublicKey" | "saveApp">;

// saveAppDetails saves Settings → General. A new name goes through the
// rename route: a commit that redeploys nothing. The icon, colour and
// description go through the app's own save (a new version, with the files
// and variables as stored; only the fields that changed are replaced). That
// save redeploys only a running app with nothing undeployed; for a stopped
// app, or one with an undeployed draft, it is kept as a draft, so a colour
// never starts an app or deploys someone's draft — it goes live with the
// app's next deploy.
export async function saveAppDetails(
  deps: AppDetailsDeps,
  appId: string,
  before: AppDetailsInput,
  after: AppDetailsInput,
  running: boolean,
): Promise<{ renamed: boolean; saved: "none" | "deployed" | "draft" }> {
  const name = after.name.trim();
  const renamed = name !== before.name.trim();
  if (renamed) await deps.rename(appId, name);

  const icon = after.icon !== before.icon;
  const color = after.color !== before.color;
  const description = after.description !== before.description;
  if (!icon && !color && !description) return { renamed, saved: "none" };

  const state = stateFromDetail(appId, await deps.getApp(appId));
  state.config = {
    ...state.config,
    ...(icon ? { icon: after.icon } : {}),
    ...(color ? { color: after.color } : {}),
    ...(description ? { description: after.description } : {}),
  };
  const revisions = await deps.getRevisions(appId);
  const hasDraft = revisions.deployed !== "" && revisions.current !== revisions.deployed;
  const draft = !running || hasDraft;
  const payload = await buildSavePayload(state, deps.getPublicKey, appId);
  await deps.saveApp({ ...payload, draft });
  return { renamed, saved: draft ? "draft" : "deployed" };
}
```

In `web/src/components/icon-picker.tsx`, add to the props `label = "Icon"` and `disabled = false` (types `label?: string; disabled?: boolean;`), and on the trigger `<Button variant="outline" className="h-12 w-12 p-0">` add `aria-label={label} disabled={disabled}`.

Replace the whole of `web/src/components/app-page/app-settings-tab.tsx` with:

```tsx
import { useCallback, useEffect, useState } from "react";
import { Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { Ownership } from "@/components/app-page/ownership";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { IconPicker } from "@/components/icon-picker";
import { InlineError } from "@/components/inline-error";
import { Section } from "@/components/section";
import type { App } from "@/context/apps-context-base";
import { useApps } from "@/context/use-apps";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { saveAppDetails } from "@/lib/app-details-save";
import { stateFromDetail } from "@/lib/app-editor-io";
import { isManagedByTemplate } from "@/lib/templates";

// The colour an app without one shows (the editor's default too).
const DEFAULT_COLOR = "#64748b";

// GeneralSection edits how the app appears: name, icon, colour and
// description, with one Save (see saveAppDetails for what each part does).
// Name, icon and colour come from the app list at once; the description is
// read from the app. A template-managed app changes its name only: the rest
// is its template's (the API refuses its save).
function GeneralSection({ app, ownership, running }: { app: App; ownership: Ownership; running: boolean }) {
  const deps = useApps();
  const { getApp, refresh } = deps;
  const managed = ownership !== null;
  const templateName = ownership?.instance?.template_name;
  const shown = { name: app.name, icon: app.icon ?? "", color: app.color || DEFAULT_COLOR };
  const [form, setForm] = useState({ ...shown, description: "" });
  // The stored description: null until read.
  const [description, setDescription] = useState<string | null>(null);
  const [readError, setReadError] = useState("");
  const [saving, setSaving] = useState(false);

  const readDescription = useCallback(async () => {
    setReadError("");
    try {
      const stored = stateFromDetail(app.id, await getApp(app.id)).config.description ?? "";
      setDescription(stored);
      setForm((f) => ({ ...f, description: stored }));
    } catch (e) {
      setReadError(e instanceof Error ? e.message : "the agent didn't answer");
    }
  }, [app.id, getApp]);

  useEffect(() => {
    void readDescription();
  }, [readDescription]);

  const before = { ...shown, description: description ?? "" };
  const after = { ...form, description: description === null ? "" : form.description };
  const dirty =
    after.name !== before.name || after.icon !== before.icon || after.color !== before.color || after.description !== before.description;
  const canSave = dirty && form.name.trim() !== "" && !saving;

  const save = async () => {
    setSaving(true);
    try {
      const result = await saveAppDetails(deps, app.id, before, after, running);
      if (result.saved === "none") toast.success("App renamed");
      else if (result.saved === "deployed") toast.success("Saved");
      else toast.success("Saved", { description: "It goes live with the app's next deploy." });
      if (result.saved !== "none") void readDescription();
    } catch (e) {
      const message = e instanceof Error ? e.message : "";
      if (isManagedByTemplate(message)) {
        toast.error("This app is managed by a template", {
          description: "Its icon, colour and description come from the template.",
        });
        void refresh();
      } else {
        toast.error("Couldn't save", { description: message || undefined });
      }
    } finally {
      setSaving(false);
    }
  };

  return (
    <Section title="General" description="How the app appears in the sidebar and the app list.">
      <form
        className="grid gap-4 rounded-xl border bg-card p-4"
        onSubmit={(e) => {
          e.preventDefault();
          if (canSave) void save();
        }}
      >
        <div className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-end gap-3">
          <div className="grid gap-1.5">
            <Label>Icon</Label>
            <IconPicker
              label="Icon"
              value={form.icon}
              color={form.color}
              name={form.name}
              id={app.id}
              disabled={managed || saving}
              onChange={(icon) => setForm((f) => ({ ...f, icon }))}
            />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="app-name">Name</Label>
            <Input
              id="app-name"
              aria-label="App name"
              value={form.name}
              disabled={saving}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="app-colour">Colour</Label>
            <Input
              id="app-colour"
              type="color"
              className="h-9 w-14 cursor-pointer p-1"
              value={form.color}
              disabled={managed || saving}
              onChange={(e) => setForm((f) => ({ ...f, color: e.target.value }))}
            />
          </div>
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor="app-description">Description</Label>
          <Input
            id="app-description"
            value={form.description}
            placeholder={description === null && !readError ? "Loading…" : "Optional"}
            disabled={managed || saving || description === null}
            onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
          />
        </div>
        {readError ? (
          <InlineError>
            Couldn't read the description: {readError}.{" "}
            <button type="button" className="underline underline-offset-4" onClick={() => void readDescription()}>
              Retry
            </button>
          </InlineError>
        ) : null}
        {managed ? (
          <p className="text-xs text-muted-foreground">
            Icon, colour and description are set by {templateName ? `template ${templateName}` : "its template"}.
          </p>
        ) : null}
        <div className="flex justify-end">
          <Button type="submit" disabled={!canSave}>
            {saving ? "Saving…" : "Save"}
          </Button>
        </div>
      </form>
    </Section>
  );
}

// TemplateSection starts a template from a plain app, or detaches a
// template-managed one (once its first deploy from the template went
// through: the API detaches only a created instance). Nothing while the
// instance is still read.
function TemplateSection({
  ownership,
  onDetached,
  onSaveAsTemplate,
  savingTemplate,
}: {
  ownership: Ownership;
  onDetached: () => void;
  onSaveAsTemplate: () => void;
  savingTemplate: boolean;
}) {
  const templatesApi = useTemplatesApi();
  const [confirm, setConfirm] = useState(false);
  const instance = ownership?.instance ?? null;
  if (ownership && !instance) return null;

  if (!instance) {
    return (
      <Section
        title="Template"
        description="Start a template from this app: its files, its variables as parameters, its addresses and backup settings. The app stays as it is."
      >
        <div>
          <Button variant="outline" disabled={savingTemplate} onClick={onSaveAsTemplate}>
            Save as template
          </Button>
        </div>
      </Section>
    );
  }

  const detach = async () => {
    try {
      await templatesApi.unlinkInstance(instance.server_id, instance.key);
      toast.success("Detached from the template");
      onDetached();
    } catch (e) {
      toast.error("Detach failed", { description: e instanceof Error ? e.message : undefined });
    }
  };

  return (
    <Section
      title="Template"
      description={`Managed by template ${instance.template_name}: its files and values change through the template.`}
    >
      {instance.state !== "created" ? (
        <p className="text-sm text-muted-foreground">Detach becomes available once its first deploy from the template succeeds.</p>
      ) : (
        <div>
          <Button variant="outline" onClick={() => setConfirm(true)}>
            Detach from template
          </Button>
        </div>
      )}
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title={`Detach from ${instance.template_name}?`}
        description="The app becomes an ordinary app you can edit freely; template updates no longer reach it."
        confirmLabel="Detach"
        pendingLabel="Detaching…"
        destructive={false}
        onConfirm={detach}
      />
    </Section>
  );
}

// AppSettingsTab is the app page's Settings tab: General (name, icon,
// colour, description), Template (Save as / Detach) and the Danger zone
// (Delete, through the page's one Delete dialog).
export function AppSettingsTab({
  app,
  ownership,
  running,
  onDetached,
  onSaveAsTemplate,
  savingTemplate,
  onDelete,
}: {
  app: App;
  ownership: Ownership;
  running: boolean;
  onDetached: () => void;
  onSaveAsTemplate: () => void;
  savingTemplate: boolean;
  onDelete: () => void;
}) {
  return (
    <div className="max-w-3xl space-y-10">
      <GeneralSection app={app} ownership={ownership} running={running} />
      <TemplateSection
        ownership={ownership}
        onDetached={onDetached}
        onSaveAsTemplate={onSaveAsTemplate}
        savingTemplate={savingTemplate}
      />
      <Section
        title="Danger zone"
        description="Stops the containers and removes the deployment and its history. Volumes and backups are kept unless you choose otherwise."
      >
        <div>
          <Button
            variant="outline"
            className="border-destructive/40 text-destructive hover:bg-destructive/10 hover:text-destructive"
            onClick={onDelete}
          >
            <Trash2 className="size-4" aria-hidden /> Delete app…
          </Button>
        </div>
      </Section>
    </div>
  );
}
```

In `web/src/pages/app-details.tsx`:
- add `import { hasRunningContainers } from "@/lib/app-status";`;
- replace the `<AppSettingsTab …/>` element with

```tsx
          <AppSettingsTab
            key={app.id}
            app={app}
            ownership={ownership}
            running={hasRunningContainers(status)}
            onDetached={reloadManaged}
            onSaveAsTemplate={() => void saveAsTemplate.run()}
            savingTemplate={saveAsTemplate.busy}
            onDelete={() => setDeleteOpen(true)}
          />
```

- remove `rename` from the `useApps()` destructuring.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/app-details-save.test.ts src/components/app-page/app-settings-tab.test.tsx src/pages/app-details.test.tsx src/components/app-editor.test.tsx`
Expected: PASS (`app-editor.test.tsx` still passes: the editor's icon button now has the name "Icon", which it did not query).

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/app-details-save.ts web/src/lib/app-details-save.test.ts web/src/components/app-page/app-settings-tab.tsx web/src/components/app-page/app-settings-tab.test.tsx web/src/components/icon-picker.tsx web/src/pages/app-details.tsx web/src/pages/app-details.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): app Settings — General (name, icon, colour, description), Template, Danger zone

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 13: The Overview tab

**Files:**
- Create: `web/src/components/app-page/app-overview.tsx`, `web/src/components/app-page/app-overview.test.tsx`

**Interfaces:**
- Consumes: `containersByApp` (Task 1), `appAddresses`, `containerHint` (Task 2), `AppMetricBlocks` with `layout="pair"` (Task 7), `useRunBackup` (Task 8), `RESTORING` (Task 8), `useAppRevisions` (Task 10), `RestorePointsDialog`, `useBackups`, `shownHealth`/`useOverlays`/`useActivity`/`restoringBackup` (`@/lib/backup-activity`), `nextCopyNote`, `appSub`, `StatusPill`/`StatusDot`, `appStatus`/`backupStatus`.
- Produces: `AppOverview({ app, status, pending, onControl, onOpenHistory }: { app: App; status: number | undefined; pending: ControlAction | null; onControl: (action: ControlAction) => void; onOpenHistory: () => void })` — a grid (one column below `lg`, two from `lg`) of regions:
  - **Status** — the app's pill, each container with a state dot and what is wrong with it ("Keeps restarting", "Stopped with exit code 137"); Restart (Start when not running).
  - **Access** — each address (external link, "HTTPS" / "HTTP only"); without one "No public address." and, when the server has the built-in proxy, "Add a domain" (to Configuration).
  - **Resources** — `AppMetricBlocks layout="pair"`; "View metrics" (to Metrics).
  - **Backups** — each set-up backup: pill, last copy, next copy, "Back up now" and "Restore…" (built-in ones); none set up: "Set up a backup" (to Backups); "Manage backups" otherwise. One `getApp` read.
  - **Recent changes** — the last three versions (what changed · when, Live/Draft); "View history" calls `onOpenHistory`. One `getRevisions` read.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/app-page/app-overview.test.tsx`:

```tsx
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppOverview } from "@/components/app-page/app-overview";
import { AppsContext, type App, type AppsContextValue } from "@/context/apps-context-base";
import { resetHeldHealth } from "@/lib/backup-activity";
import type { BackupHealth, EffectiveBackup } from "@/lib/backups";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
const backups = vi.hoisted(() => ({ runBackup: vi.fn() }));
vi.mock("@/context/use-backups", () => ({ useBackups: () => backups }));
let ingress = true;
vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ activeServerId: "srv-1", activeServer: { id: "srv-1", features: { ingress } } }),
}));
vi.mock("@/components/app-metrics", () => ({
  AppMetricBlocks: ({ layout }: { layout?: string }) => <div data-testid="kpi-blocks">{layout}</div>,
}));
vi.mock("@/components/restore-points-dialog", () => ({
  RestorePointsDialog: ({ backup }: { backup: { name: string } }) => <div role="dialog" aria-label="Copies">copies of {backup.name}</div>,
}));

const now = Math.floor(Date.now() / 1000);
const APP: App = { id: "a1", serverId: "srv-1", name: "Shop", version: "3", icon: "", color: "", createdAt: "" };
const LIST: EffectiveBackup[] = [
  { name: "database", type: "sqlite", enabled: true, storage: "disk", every: "1h", in_recipe: true, from_recipe: null },
  { name: "media", type: "files", enabled: false, storage: "", in_recipe: true, from_recipe: null },
  { name: "replica", type: "custom", enabled: true, storage: "s3", in_recipe: true, from_recipe: null },
];
const HEALTH: BackupHealth[] = [
  { name: "database", state: "ok", last_success: now - 600 },
  { name: "replica", state: "ok" },
];

const ctx = {
  getApp: vi.fn(),
  getRevisions: vi.fn(),
  containersByApp: {} as AppsContextValue["containersByApp"],
  backupsByApp: { a1: HEALTH } as AppsContextValue["backupsByApp"],
};

function renderOverview(over: { app?: Partial<App>; status?: number } = {}) {
  const props = { onControl: vi.fn(), onOpenHistory: vi.fn() };
  render(
    <MemoryRouter>
      <AppsContext.Provider value={ctx as unknown as AppsContextValue}>
        <AppOverview app={{ ...APP, ...over.app }} status={over.status ?? 1} pending={null} {...props} />
      </AppsContext.Provider>
    </MemoryRouter>,
  );
  return props;
}

describe("AppOverview", () => {
  beforeEach(() => {
    resetHeldHealth();
    ingress = true;
    backups.runBackup.mockReset();
    ctx.getApp.mockReset().mockResolvedValue({ backups: LIST });
    ctx.getRevisions.mockReset().mockResolvedValue({
      current: "dddd",
      deployed: "cccc",
      revisions: [
        { hash: "dddd", subject: "Changed .env", timestamp: now - 60 },
        { hash: "cccc", subject: "Changed compose.yml", timestamp: now - 7200 },
        { hash: "bbbb", subject: "rename to Shop", timestamp: now - 86400 },
        { hash: "aaaa", subject: "create", timestamp: now - 2 * 86400 },
      ],
    });
    ctx.containersByApp = {
      a1: [
        { container_id: "c1", name: "shop-web-1", status_code: 1, exit_code: 0 },
        { container_id: "c2", name: "shop-worker-1", status_code: 3, exit_code: 0 },
        { container_id: "c3", name: "shop-db-1", status_code: 4, exit_code: 137 },
      ],
    };
  });

  it("shows the status with each container and what is wrong with it, and restarts", async () => {
    const user = userEvent.setup();
    const props = renderOverview();
    const status = screen.getByRole("region", { name: "Status" });
    expect(within(status).getByText("Running")).toBeInTheDocument();
    const containers = within(status).getByRole("list", { name: "Containers" });
    expect(within(containers).getAllByRole("listitem")).toHaveLength(3);
    expect(containers).toHaveTextContent("shop-worker-1Keeps restarting");
    expect(containers).toHaveTextContent("shop-db-1Stopped with exit code 137");
    await user.click(within(status).getByRole("button", { name: "Restart" }));
    expect(props.onControl).toHaveBeenCalledWith("restart");
  });

  it("offers Start for a stopped app, and says when no container reported yet", async () => {
    const user = userEvent.setup();
    ctx.containersByApp = {};
    const props = renderOverview({ status: 5 });
    const status = screen.getByRole("region", { name: "Status" });
    expect(within(status).getByText("No containers reported yet.")).toBeInTheDocument();
    await user.click(within(status).getByRole("button", { name: "Start" }));
    expect(props.onControl).toHaveBeenCalledWith("start");
  });

  it("lists the addresses with their HTTPS state", () => {
    renderOverview({
      app: {
        domains: [
          { domain: "shop.example.com", ssl: true, kind: "route" },
          { domain: "shop.lan", ssl: false, kind: "route" },
        ],
      },
    });
    const access = screen.getByRole("region", { name: "Access" });
    expect(within(access).getByRole("link", { name: /shop\.example\.com/ })).toHaveAttribute("href", "https://shop.example.com");
    expect(access).toHaveTextContent("HTTPS");
    expect(access).toHaveTextContent("HTTP only");
    expect(within(access).queryByRole("link", { name: "Add a domain" })).not.toBeInTheDocument();
  });

  it("offers Add a domain without an address, only with the built-in proxy", () => {
    renderOverview();
    const access = screen.getByRole("region", { name: "Access" });
    expect(access).toHaveTextContent("No public address.");
    expect(within(access).getByRole("link", { name: "Add a domain" })).toHaveAttribute("href", "/server/srv-1/app/a1?tab=configuration");
  });

  it("has no Add a domain on a server without the proxy", () => {
    ingress = false;
    renderOverview();
    expect(within(screen.getByRole("region", { name: "Access" })).queryByRole("link", { name: "Add a domain" })).not.toBeInTheDocument();
  });

  it("shows the resource blocks in pairs and leads to the metrics", () => {
    renderOverview();
    const resources = screen.getByRole("region", { name: "Resources" });
    expect(within(resources).getByTestId("kpi-blocks")).toHaveTextContent("pair");
    expect(within(resources).getByRole("link", { name: /View metrics/ })).toHaveAttribute("href", "/server/srv-1/app/a1?tab=metrics");
  });

  it("shows each set-up backup with its state, last and next copy; backs up and restores", async () => {
    const user = userEvent.setup();
    backups.runBackup.mockResolvedValue({ unchanged: false, point: { id: "p" }, warnings: [] });
    renderOverview();
    const region = screen.getByRole("region", { name: "Backups" });
    const list = await within(region).findByRole("list", { name: "Backups" });
    // media has no storage: not listed here.
    expect(within(list).getAllByRole("listitem")).toHaveLength(2);
    const db = within(list).getByText("database").closest("li")!;
    expect(db).toHaveTextContent("OK");
    expect(db).toHaveTextContent("Last copy 10 minutes ago");
    expect(db).toHaveTextContent(/next /);
    // A custom backup runs its own tool: no actions.
    expect(within(within(list).getByText("replica").closest("li")!).queryByRole("button")).not.toBeInTheDocument();
    await user.click(within(db).getByRole("button", { name: "Back up now: database" }));
    await waitFor(() => expect(backups.runBackup).toHaveBeenCalledWith("a1", "database"));
    await user.click(within(db).getByRole("button", { name: "Restore database" }));
    expect(await screen.findByRole("dialog", { name: "Copies" })).toHaveTextContent("copies of database");
    expect(within(region).getByRole("link", { name: /Manage backups/ })).toHaveAttribute("href", "/server/srv-1/app/a1?tab=backups");
  });

  it("invites to set up a backup when none is", async () => {
    ctx.getApp.mockResolvedValue({ backups: [LIST[1]] });
    renderOverview();
    const region = screen.getByRole("region", { name: "Backups" });
    expect(await within(region).findByRole("link", { name: "Set up a backup" })).toHaveAttribute(
      "href",
      "/server/srv-1/app/a1?tab=backups",
    );
  });

  it("shows the last three changes, and opens the history", async () => {
    const user = userEvent.setup();
    const props = renderOverview();
    const region = screen.getByRole("region", { name: "Recent changes" });
    const list = await within(region).findByRole("list", { name: "Recent changes" });
    expect(within(list).getAllByRole("listitem").map((li) => li.textContent)).toEqual([
      expect.stringContaining("Changed .env"),
      expect.stringContaining("Changed compose.yml"),
      expect.stringContaining("rename to Shop"),
    ]);
    expect(within(list).getAllByRole("listitem")[0]).toHaveTextContent("Draft");
    expect(within(list).getAllByRole("listitem")[1]).toHaveTextContent("Live");
    await user.click(within(region).getByRole("button", { name: "View history" }));
    expect(props.onOpenHistory).toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/components/app-page/app-overview.test.tsx`
Expected: FAIL — `@/components/app-page/app-overview` does not exist.

- [ ] **Step 3: Implement**

Create `web/src/components/app-page/app-overview.tsx`:

```tsx
import { useCallback, useEffect, useId, useRef, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { ArrowRight, ExternalLink } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { AppMetricBlocks } from "@/components/app-metrics";
import { RestorePointsDialog } from "@/components/restore-points-dialog";
import { StatusDot, StatusPill } from "@/components/status";
import type { App, ControlAction } from "@/context/apps-context-base";
import { useApps } from "@/context/use-apps";
import { useBackups } from "@/context/use-backups";
import { useServerPath } from "@/context/use-server-path";
import { useServers } from "@/context/use-servers";
import { useAppRevisions } from "@/hooks/use-app-revisions";
import { useRunBackup } from "@/hooks/use-run-backup";
import { hasRunningContainers } from "@/lib/app-status";
import { appAddresses, containerHint } from "@/lib/app-summary";
import { restoringBackup, shownHealth, useActivity, useOverlays } from "@/lib/backup-activity";
import { nextCopyNote, type BackupState, type EffectiveBackup } from "@/lib/backups";
import { appSub } from "@/lib/routes";
import { appStatus, backupStatus, RESTORING } from "@/lib/status";
import { relativeTime } from "@/lib/utils";

const LINK =
  "inline-flex min-h-8 items-center gap-1 text-sm font-medium text-muted-foreground transition-colors duration-150 hover:text-foreground";

// Widget is one Overview card: a region named by its title, with an
// optional action top-right.
function Widget({ title, action, children }: { title: string; action?: ReactNode; children: ReactNode }) {
  const id = useId();
  return (
    <section aria-labelledby={id} className="flex min-w-0 flex-col gap-3 rounded-xl border bg-card p-4">
      <div className="flex items-center justify-between gap-2">
        <h2 id={id} className="text-[15px] font-semibold">
          {title}
        </h2>
        {action}
      </div>
      {children}
    </section>
  );
}

// StatusWidget is the app's state, its containers with what is wrong with
// them, and Restart (Start when it doesn't run). The API reports no start
// time, so there is no "since".
function StatusWidget({
  app,
  status,
  pending,
  onControl,
}: {
  app: App;
  status: number | undefined;
  pending: ControlAction | null;
  onControl: (action: ControlAction) => void;
}) {
  const { containersByApp } = useApps();
  const containers = containersByApp?.[app.id] ?? [];
  const running = hasRunningContainers(status);
  const action: ControlAction = running ? "restart" : "start";
  return (
    <Widget
      title="Status"
      action={
        <Button size="sm" variant="outline" disabled={pending !== null} onClick={() => onControl(action)}>
          {pending === action ? <Spinner className="size-4" aria-hidden /> : null}
          {running ? "Restart" : "Start"}
        </Button>
      }
    >
      <div>
        <StatusPill status={appStatus(status)} />
      </div>
      {containers.length === 0 ? (
        <p className="text-sm text-muted-foreground">No containers reported yet.</p>
      ) : (
        <ul aria-label="Containers" className="space-y-1.5">
          {containers.map((c) => {
            const info = appStatus(c.status_code);
            const hint = containerHint(c);
            return (
              <li key={c.container_id || c.name} className="flex min-w-0 items-start gap-2">
                <StatusDot tone={info.tone} pulse={info.pulse} label={info.label} className="mt-1.5" />
                <span className="min-w-0">
                  <span className="block truncate font-mono text-xs">{c.name}</span>
                  {hint ? (
                    <span className={info.tone === "bad" ? "block text-xs text-status-bad" : "block text-xs text-muted-foreground"}>
                      {hint}
                    </span>
                  ) : null}
                </span>
              </li>
            );
          })}
        </ul>
      )}
    </Widget>
  );
}

// AccessWidget is where the app is reachable: each address with its HTTPS
// state; without one, the way to add a domain (only when this server runs
// the built-in proxy).
function AccessWidget({ app }: { app: App }) {
  const { activeServer } = useServers();
  const toServer = useServerPath();
  const addresses = appAddresses(app);
  return (
    <Widget title="Access">
      {addresses.length === 0 ? (
        <div className="space-y-2">
          <p className="text-sm text-muted-foreground">No public address.</p>
          {activeServer?.features?.ingress ? (
            <Button asChild size="sm" variant="outline">
              <Link to={toServer(appSub(app.id, "configuration"))}>Add a domain</Link>
            </Button>
          ) : null}
        </div>
      ) : (
        <ul className="space-y-1.5">
          {addresses.map((a) => (
            <li key={a.host} className="flex min-w-0 items-center justify-between gap-3 text-sm">
              <a
                href={a.url}
                target="_blank"
                rel="noreferrer"
                className="inline-flex min-w-0 items-center gap-1 font-medium hover:underline"
              >
                <span className="truncate">{a.host}</span>
                <ExternalLink className="size-3.5 shrink-0" aria-hidden />
              </a>
              <span className={a.https ? "shrink-0 text-xs text-status-ok" : "shrink-0 text-xs text-muted-foreground"}>
                {a.https ? "HTTPS" : "HTTP only"}
              </span>
            </li>
          ))}
        </ul>
      )}
    </Widget>
  );
}

// ResourcesWidget is the app's CPU, memory, disk and network now, with the
// last hour as sparklines.
function ResourcesWidget({ app }: { app: App }) {
  const toServer = useServerPath();
  return (
    <Widget
      title="Resources"
      action={
        <Link to={toServer(appSub(app.id, "metrics"))} className={LINK}>
          View metrics <ArrowRight className="size-4" aria-hidden />
        </Link>
      }
    >
      <AppMetricBlocks serverId={app.serverId} appId={app.id} layout="pair" />
    </Widget>
  );
}

// BackupsWidget is each set-up backup's health (last copy, next one) with
// Back up now and Restore for the built-in ones; without any, the way to
// set one up. The list (and when each runs) is read from the app once.
function BackupsWidget({ app }: { app: App }) {
  const { getApp, backupsByApp } = useApps();
  const backups = useBackups();
  const runOne = useRunBackup(app.id);
  const activity = useActivity(app.id);
  const overlays = useOverlays(app.id);
  const toServer = useServerPath();
  const [list, setList] = useState<EffectiveBackup[] | null>(null);
  const [error, setError] = useState("");
  const [points, setPoints] = useState<EffectiveBackup | null>(null);
  const opener = useRef<HTMLElement | null>(null);

  const load = useCallback(async () => {
    setError("");
    try {
      setList((await getApp(app.id)).backups ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't read the backups");
    }
  }, [app.id, getApp]);

  useEffect(() => {
    void load();
  }, [load]);

  const backupsTab = toServer(appSub(app.id, "backups"));
  const reported = backupsByApp?.[app.id] ?? [];
  const restoring = restoringBackup(activity);
  const set = (list ?? []).filter((b) => b.storage);

  let body: ReactNode;
  if (error) {
    body = (
      <p className="text-sm text-destructive">
        {error}{" "}
        <button type="button" className="underline underline-offset-4" onClick={() => void load()}>
          Retry
        </button>
      </p>
    );
  } else if (list === null) {
    body = <Spinner aria-label="Loading backups" />;
  } else if (set.length === 0) {
    body = (
      <div className="space-y-2">
        <p className="text-sm text-muted-foreground">No backup is set up yet.</p>
        <Button asChild size="sm" variant="outline">
          <Link to={backupsTab}>Set up a backup</Link>
        </Button>
      </div>
    );
  } else {
    body = (
      <ul aria-label="Backups" className="divide-y">
        {set.map((b) => {
          const health = shownHealth(overlays[b.name], reported.find((h) => h.name === b.name));
          const kinds = activity[b.name] ?? [];
          const state: BackupState = kinds.includes("backup") ? "running" : (health?.state ?? (b.enabled ? "never" : "off"));
          const builtin = b.type !== "custom";
          const meta = [
            health?.last_success ? `Last copy ${relativeTime(new Date(health.last_success * 1000))}` : "",
            builtin ? nextCopyNote(b, state, health?.last_success) : "",
          ]
            .filter(Boolean)
            .join(" · ");
          return (
            <li key={b.name} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2 first:pt-0 last:pb-0">
              <span className="font-mono text-sm">{b.name}</span>
              <StatusPill status={kinds.includes("restore") ? RESTORING : backupStatus(state)} />
              {meta ? <span className="text-xs text-muted-foreground">{meta}</span> : null}
              {builtin ? (
                <span className="ml-auto flex gap-1">
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-label={`Back up now: ${b.name}`}
                    disabled={!b.enabled || state === "running" || !!restoring}
                    onClick={() => void runOne(b.name)}
                  >
                    Back up now
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-label={`Restore ${b.name}`}
                    disabled={!!restoring}
                    onClick={(e) => {
                      opener.current = e.currentTarget;
                      setPoints(b);
                    }}
                  >
                    Restore…
                  </Button>
                </span>
              ) : null}
            </li>
          );
        })}
      </ul>
    );
  }

  return (
    <Widget
      title="Backups"
      action={
        set.length > 0 ? (
          <Link to={backupsTab} className={LINK}>
            Manage backups <ArrowRight className="size-4" aria-hidden />
          </Link>
        ) : undefined
      }
    >
      {body}
      {points ? (
        <RestorePointsDialog
          appId={app.id}
          backup={points}
          backups={backups}
          open
          running={reported.find((h) => h.name === points.name)?.state === "running"}
          returnFocus={opener}
          onOpenChange={(open) => {
            if (!open) setPoints(null);
          }}
          onRestored={() => void load()}
        />
      ) : null}
    </Widget>
  );
}

// RecentChangesWidget is the app's last three versions: what changed and
// when, which is live and which is a draft; the rest is in History.
function RecentChangesWidget({ appId, onOpenHistory }: { appId: string; onOpenHistory: () => void }) {
  const { data, loading, error, reload } = useAppRevisions(appId);
  const top = data?.revisions.slice(0, 3) ?? [];
  let body: ReactNode;
  if (error) {
    body = (
      <p className="text-sm text-destructive">
        {error}{" "}
        <button type="button" className="underline underline-offset-4" onClick={() => void reload()}>
          Retry
        </button>
      </p>
    );
  } else if (loading && !data) {
    body = <Spinner aria-label="Loading changes" />;
  } else if (!data || top.length === 0) {
    body = <p className="text-sm text-muted-foreground">No changes yet.</p>;
  } else {
    body = (
      <ul aria-label="Recent changes" className="space-y-2">
        {top.map((rev) => {
          const live = data.deployed !== "" && rev.hash === data.deployed;
          const draft = rev.hash === data.current && data.deployed !== "" && !live;
          return (
            <li key={rev.hash} className="flex min-w-0 items-baseline gap-2 text-sm">
              <span className="truncate" title={`Commit ${rev.hash.slice(0, 8)}`}>
                {rev.subject}
              </span>
              <span className="shrink-0 text-xs text-muted-foreground">· {relativeTime(new Date(rev.timestamp * 1000))}</span>
              {live ? <span className="shrink-0 text-xs font-medium text-status-ok">Live</span> : null}
              {draft ? <span className="shrink-0 text-xs font-medium text-status-warn">Draft</span> : null}
            </li>
          );
        })}
      </ul>
    );
  }
  return (
    <Widget
      title="Recent changes"
      action={
        <Button size="sm" variant="ghost" onClick={onOpenHistory}>
          View history
        </Button>
      }
    >
      {body}
    </Widget>
  );
}

// AppOverview is the app page's default tab: status, access, resources,
// backups and recent changes at a glance (two columns from lg, one below).
export function AppOverview({
  app,
  status,
  pending,
  onControl,
  onOpenHistory,
}: {
  app: App;
  status: number | undefined;
  pending: ControlAction | null;
  onControl: (action: ControlAction) => void;
  onOpenHistory: () => void;
}) {
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <StatusWidget app={app} status={status} pending={pending} onControl={onControl} />
      <AccessWidget app={app} />
      <ResourcesWidget app={app} />
      <BackupsWidget app={app} />
      <RecentChangesWidget appId={app.id} onOpenHistory={onOpenHistory} />
    </div>
  );
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/components/app-page/app-overview.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/components/app-page/app-overview.tsx web/src/components/app-page/app-overview.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): app Overview (status, access, resources, backups, recent changes)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---
### Task 14: The app page — header, banners, Overview first, Back up now in ⌘K

**Files:**
- Create: `web/src/components/app-page/app-header.tsx`
- Modify (rewrite): `web/src/pages/app-details.tsx`
- Modify: `web/src/pages/app-details.test.tsx`
- Modify: `web/src/components/alerts/active-alerts.tsx`, `web/src/components/alerts/active-alerts.test.tsx` (remove `AppAlertsBadge`)

**Interfaces:**
- Consumes: everything above; `UnderlineTabs`, `useTabParam`, `usePageTitle`, `usePaletteActions`, `type PaletteAction` (phase 1).
- Produces:
  - `AppHeader(props: AppHeaderProps)` (`@/components/app-page/app-header`) with `AppHeaderProps = { app: App; status: number | undefined; instance: InstanceView | null; behind: boolean; pending: ControlAction | null; onControl: (action: ControlAction) => void; onUpdateTemplate: () => void; backUpBusy: boolean; onBackUpNow: () => void; canSaveAsTemplate: boolean; savingTemplate: boolean; onSaveAsTemplate: () => void; onDelete: () => void }` — icon, name (h1), `StatusPill`, the primary address with "Open ↗", "From template X" (link) and "Not created yet" / "Update available" chips; one filled primary (Restart while running, else Start) and a "More actions" ⋯ menu: Stop (running only), Pull latest images, Back up now, Save as template (`canSaveAsTemplate`), Delete…. Stacks on a phone.
  - `AppDetailsPage` — tabs `overview` (default) · `logs` · `configuration` · `backups` · `metrics` · `settings`; banners (pending template deploy failure, stale shared variables, firing alerts) only when relevant; no KPI row and no "Revision N" above the tabs; every per-app part keyed by `app.id`; ⌘K group: Restart, Stop/Start, Pull latest images, View logs, Back up now. The page keeps `openUpgrade()` (header's "Update available"; phase 3 passes it to Configuration).

- [ ] **Step 1: Write the failing tests**

In `web/src/pages/app-details.test.tsx`:

1. Mocks and helpers — add next to the other `vi.mock` calls:

```tsx
// The Overview has its own tests; here it shows whose it is and offers
// View history.
vi.mock("@/components/app-page/app-overview", () => ({
  AppOverview: ({ app, onOpenHistory }: { app: { name: string }; onOpenHistory: () => void }) => (
    <div>
      <p>the overview of {app.name}</p>
      <button type="button" onClick={onOpenHistory}>
        View history
      </button>
    </div>
  ),
}));
// The alerts banner has its own tests (it polls).
vi.mock("@/components/alerts/active-alerts", () => ({ AppAlertsBanner: () => null }));
const backupsApi = vi.hoisted(() => ({ runBackup: vi.fn() }));
vi.mock("@/context/use-backups", () => ({ useBackups: () => backupsApi }));
```

add `info: vi.fn()` to the hoisted `toast` object, and add `import { trackActivity } from "@/lib/backup-activity";`.

2. In `renderPage`'s route wrapper, next to `<Link to="/app/a2?tab=editor">go to a2</Link>`, add `<Link to="/app/a2?tab=settings">open a2 settings</Link>`.

3. Update the template-instance tests to the new header:
   - "badges the template and revision, and upgrades asking for the new value" → title "chips the template, and updates asking for the new value"; expect `await screen.findByRole("link", { name: "From template twillingate" })` with `href` `"/templates/t1"`; replace `expect(screen.getByText("r3 available")).toBeInTheDocument();` with `expect(screen.queryByText(/r3/)).not.toBeInTheDocument();`; the first click becomes `await user.click(screen.getByRole("button", { name: "Update available" }));` (the dialog's own "Upgrade to r3" button and the posted body stay — the dialog's copy is phase 6's);
   - "titles the upgrade with the app, …": first click `await user.click(await screen.findByRole("button", { name: "Update available" }));`;
   - "hides the app's own revision for an app a template manages": `findByRole("link", { name: "From template twillingate" })`;
   - replace "shows the app's own revision for a plain app" with:

```tsx
  it("never shows a revision number in the header", async () => {
    renderPage("/app/a1?tab=backups");
    expect(await screen.findByRole("heading", { level: 1, name: "Shop" })).toBeInTheDocument();
    expect(screen.queryByText(/Revision/)).not.toBeInTheDocument();
  });
```

   - "reports a template that can't be loaded for an upgrade": first click `await user.click(await screen.findByRole("button", { name: "Update available" }));`;
   - "never shows the first app's instance on the app the user moved to": `queryByRole("link", { name: "twillingate · r2" })` → `queryByRole("link", { name: "From template twillingate" })`.

4. The ⌘K test (phase 1's "offers its actions in the ⌘K palette") expects the list to end with Back up now:

```tsx
    expect(within(list).getAllByRole("button").map((b) => b.textContent)).toEqual([
      "Shop: Restart",
      "Shop: Stop",
      "Shop: Pull latest images",
      "Shop: View logs",
      "Shop: Back up now",
    ]);
```

5. Add inside `describe("AppDetailsPage", …)`:

```tsx
  it("opens Overview by default; old and e-mailed links still open their tab", async () => {
    const { unmount } = renderPage("/app/a1");
    expect(await screen.findByRole("tab", { name: "Overview" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("the overview of Shop")).toBeInTheDocument();
    expect(screen.getAllByRole("tab").map((t) => t.textContent)).toEqual([
      "Overview",
      "Logs",
      "Configuration",
      "Backups",
      "Metrics",
      "Settings",
    ]);
    unmount();
    renderPage("/app/a1?tab=metrics");
    expect(await screen.findByRole("tab", { name: "Metrics" })).toHaveAttribute("aria-selected", "true");
  });

  it("shows where the app lives, and the header stacks on a phone", async () => {
    renderPage("/app/a1?tab=backups", {}, { app: { domains: [{ domain: "shop.example.com", ssl: true, kind: "route" }] } });
    expect(await screen.findByRole("link", { name: /Open/ })).toHaveAttribute("href", "https://shop.example.com");
    expect(screen.getByRole("heading", { level: 1, name: "Shop" }).closest("header")).toHaveClass("flex-col", "sm:flex-row");
  });

  it("puts Restart first while the app runs and the rest in ⋯", async () => {
    const user = userEvent.setup();
    const control = vi.fn(async () => {});
    renderPage("/app/a1?tab=backups", {}, { ctx: { control } });
    await user.click(await screen.findByRole("button", { name: "Restart" }));
    expect(control).toHaveBeenCalledWith("a1", "restart");
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("App restarted"));
    await user.click(screen.getByRole("button", { name: "More actions" }));
    expect((await screen.findAllByRole("menuitem")).map((m) => m.textContent)).toEqual([
      "Stop",
      "Pull latest images",
      "Back up now",
      "Save as template",
      "Delete…",
    ]);
    await user.click(screen.getByRole("menuitem", { name: "Pull latest images" }));
    expect(control).toHaveBeenCalledWith("a1", "update");
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Latest images pulled"));
  });

  it("offers Start while the app is stopped, and no Stop", async () => {
    const user = userEvent.setup();
    renderPage("/app/a1?tab=backups", {}, { ctx: { statusByApp: { a1: 5 } } });
    expect(await screen.findByRole("button", { name: "Start" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "More actions" }));
    expect(screen.queryByRole("menuitem", { name: "Stop" })).not.toBeInTheDocument();
  });

  it("backs up now from ⋯, and from ⌘K says when there is nothing to back up", async () => {
    const user = userEvent.setup();
    backupsApi.runBackup.mockReset().mockResolvedValue({ unchanged: false, point: { id: "p" }, warnings: [] });
    const getApp = vi.fn(async () => ({
      backups: [{ name: "db", type: "sqlite", enabled: true, storage: "disk", in_recipe: true, from_recipe: null }],
    }));
    renderPage("/app/a1?tab=backups", {}, { ctx: { getApp } as unknown as Partial<AppsContextValue> });
    await user.click(await screen.findByRole("button", { name: "More actions" }));
    await user.click(await screen.findByRole("menuitem", { name: "Back up now" }));
    await waitFor(() => expect(backupsApi.runBackup).toHaveBeenCalledWith("a1", "db"));

    getApp.mockResolvedValueOnce({ backups: [] } as never);
    await user.click(screen.getByRole("button", { name: "Shop: Back up now" }));
    await waitFor(() =>
      expect(toast.info).toHaveBeenCalledWith("Nothing to back up yet", { description: "Set up a backup on the Backups tab first." }),
    );
  });

  it("disables Back up now in ⋯ while a backup of the app runs or is restored", async () => {
    const user = userEvent.setup();
    let end = () => {};
    act(() => {
      end = trackActivity("a1", "db", "restore");
    });
    renderPage("/app/a1?tab=backups");
    await user.click(await screen.findByRole("button", { name: "More actions" }));
    expect(await screen.findByRole("menuitem", { name: "Back up now" })).toHaveAttribute("aria-disabled", "true");
    await user.keyboard("{Escape}");
    act(() => end());
    await user.click(screen.getByRole("button", { name: "More actions" }));
    expect(await screen.findByRole("menuitem", { name: "Back up now" })).not.toHaveAttribute("aria-disabled");
  });

  it("deletes from ⋯ through the one Delete dialog", async () => {
    const user = userEvent.setup();
    renderPage("/app/a1?tab=backups");
    await user.click(await screen.findByRole("button", { name: "More actions" }));
    await user.click(await screen.findByRole("menuitem", { name: "Delete…" }));
    expect(await screen.findByRole("alertdialog", { name: "Delete Shop?" })).toBeInTheDocument();
  });

  it("opens History from the Overview", async () => {
    const user = userEvent.setup();
    renderPage("/app/a1");
    await user.click(await screen.findByRole("button", { name: "View history" }));
    expect(await screen.findByRole("dialog", { name: "History" })).toBeInTheDocument();
  });

  it("shows only the new app's settings after moving to another app", async () => {
    const user = userEvent.setup();
    const getApp = vi.fn(async (id: string) => ({
      app: { app_id: id, config: btoa(JSON.stringify({ name: id, description: `${id} notes` })), files: [], variables: [] },
      revision: 1,
      available_revisions: null,
    }));
    const apps = [
      { id: "a1", serverId: "s", name: "Shop", version: "3", icon: "", color: "", createdAt: "" },
      { id: "a2", serverId: "s", name: "Blog", version: "1", icon: "", color: "", createdAt: "" },
    ];
    renderPage("/app/a1?tab=settings", {}, { ctx: { apps, getApp } as unknown as Partial<AppsContextValue> });
    expect(await screen.findByDisplayValue("a1 notes")).toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "open a2 settings" }));
    expect(await screen.findByDisplayValue("a2 notes")).toBeInTheDocument();
    expect(screen.queryByDisplayValue("a1 notes")).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 1, name: "Blog" })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "App name" })).toHaveValue("Blog");
  });
```

In `web/src/components/alerts/active-alerts.test.tsx`, remove `AppAlertsBadge` from the import and delete the test "asks for one app's alerts" (the `AppAlertsBanner` tests of Task 3 cover the app's alerts).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/pages/app-details.test.tsx src/components/alerts/active-alerts.test.tsx`
Expected: FAIL — no Overview tab, no "More actions", "Update available" or "From template" chip; ⌘K has no Back up now.

- [ ] **Step 3: Implement**

Create `web/src/components/app-page/app-header.tsx`:

```tsx
import { Link } from "react-router-dom";
import { ArrowUpCircle, DatabaseBackup, LayoutTemplate, MoreHorizontal, Play, RotateCw, Square, Trash2 } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Spinner } from "@/components/ui/spinner";
import { AppIcon } from "@/components/app-icon";
import { StatusPill } from "@/components/status";
import type { App, ControlAction } from "@/context/apps-context-base";
import { hasRunningContainers } from "@/lib/app-status";
import { appTitle, primaryAddress } from "@/lib/app-summary";
import { templatePath } from "@/lib/routes";
import { appStatus } from "@/lib/status";
import type { InstanceView } from "@/lib/templates";
import { cn } from "@/lib/utils";

export type AppHeaderProps = {
  app: App;
  status: number | undefined;
  // instance is the template instance managing the app (null: a plain app,
  // or not read yet).
  instance: InstanceView | null;
  // behind: the template has a newer version than the app runs.
  behind: boolean;
  pending: ControlAction | null;
  onControl: (action: ControlAction) => void;
  onUpdateTemplate: () => void;
  backUpBusy: boolean;
  onBackUpNow: () => void;
  canSaveAsTemplate: boolean;
  savingTemplate: boolean;
  onSaveAsTemplate: () => void;
  onDelete: () => void;
};

// AppHeader is the app page's header: who the app is (icon, name, status,
// where it opens, its template) and what to do now — one filled primary
// (Restart while it runs, else Start; an unknown status offers Start, which
// is harmless on a running stack) and the rest in ⋯. On a phone the actions
// go under the name.
export function AppHeader(p: AppHeaderProps) {
  const { app } = p;
  const running = hasRunningContainers(p.status);
  const primary: ControlAction = running ? "restart" : "start";
  const PrimaryIcon = running ? RotateCw : Play;
  const address = primaryAddress(app);
  const named = Boolean(app.name?.trim());
  return (
    <header className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
      <div className="flex min-w-0 items-center gap-4">
        <AppIcon name={app.name} id={app.id} icon={app.icon} color={app.color} className="size-14 shrink-0 rounded-xl text-xl" />
        <div className="min-w-0 space-y-1.5">
          <h1 className={cn("truncate text-2xl font-semibold tracking-tight", !named && "text-muted-foreground")}>{appTitle(app)}</h1>
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 text-sm">
            <StatusPill status={appStatus(p.status)} />
            {address ? (
              <a
                href={address.url}
                target="_blank"
                rel="noreferrer"
                className="inline-flex min-w-0 items-center gap-1.5 text-muted-foreground transition-colors duration-150 hover:text-foreground"
              >
                <span className="truncate">{address.host}</span>
                <span className="font-medium text-foreground">Open ↗</span>
              </a>
            ) : null}
            {p.instance ? (
              <Link
                to={templatePath(p.instance.template_id)}
                className="rounded-full border px-2 py-0.5 text-xs font-medium transition-colors duration-150 hover:bg-muted"
              >
                From template {p.instance.template_name}
              </Link>
            ) : null}
            {p.instance?.state === "pending" ? <Badge variant="outline">Not created yet</Badge> : null}
            {p.behind ? (
              <Button
                size="sm"
                variant="outline"
                className="h-7 rounded-full border-status-warn/50 px-2.5 text-xs"
                onClick={p.onUpdateTemplate}
              >
                Update available
              </Button>
            ) : null}
          </div>
        </div>
      </div>
      <div className="flex shrink-0 items-center gap-2">
        <Button className="max-sm:h-10" disabled={p.pending !== null} onClick={() => p.onControl(primary)}>
          {p.pending === primary ? <Spinner className="size-4" aria-hidden /> : <PrimaryIcon className="size-4" aria-hidden />}
          {running ? "Restart" : "Start"}
        </Button>
        <DropdownMenu modal={false}>
          <DropdownMenuTrigger asChild>
            <Button variant="outline" size="icon" className="size-10 sm:size-9" aria-label="More actions">
              <MoreHorizontal className="size-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {running ? (
              <DropdownMenuItem disabled={p.pending !== null} onSelect={() => p.onControl("stop")}>
                <Square aria-hidden />
                Stop
              </DropdownMenuItem>
            ) : null}
            <DropdownMenuItem disabled={p.pending !== null} onSelect={() => p.onControl("update")}>
              <ArrowUpCircle aria-hidden />
              Pull latest images
            </DropdownMenuItem>
            <DropdownMenuItem disabled={p.backUpBusy} onSelect={p.onBackUpNow}>
              <DatabaseBackup aria-hidden />
              Back up now
            </DropdownMenuItem>
            {p.canSaveAsTemplate ? (
              <DropdownMenuItem disabled={p.savingTemplate} onSelect={p.onSaveAsTemplate}>
                <LayoutTemplate aria-hidden />
                Save as template
              </DropdownMenuItem>
            ) : null}
            <DropdownMenuSeparator />
            <DropdownMenuItem className="text-destructive focus:text-destructive" onSelect={p.onDelete}>
              <Trash2 aria-hidden />
              Delete…
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  );
}
```

Replace the whole of `web/src/pages/app-details.tsx` with:

```tsx
import { useMemo, useState, type ReactNode } from "react";
import { useParams } from "react-router-dom";
import { ArrowUpCircle, DatabaseBackup, History, Play, RotateCw, ScrollText, Square, TriangleAlert } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { TabsContent } from "@/components/ui/tabs";
import { AppAlertsBanner } from "@/components/alerts/active-alerts";
import { AppBackups } from "@/components/app-backups";
import { AppEditorPanel } from "@/components/app-editor-panel";
import { AppMetricsTab } from "@/components/app-metrics";
import { AppHeader } from "@/components/app-page/app-header";
import { AppLogsTab } from "@/components/app-page/app-logs-tab";
import { AppOverview } from "@/components/app-page/app-overview";
import { AppSettingsTab } from "@/components/app-page/app-settings-tab";
import { HistorySheet } from "@/components/app-page/history-sheet";
import type { Ownership } from "@/components/app-page/ownership";
import { DeleteAppDialog } from "@/components/delete-app-dialog";
import { ErrorText } from "@/components/error-text";
import { InstancePanel } from "@/components/instance-panel";
import { NotFound } from "@/components/not-found";
import { LoadError, PageSpinner } from "@/components/page-states";
import { StaleVarsBadge } from "@/components/stale-vars-badge";
import { UnderlineTabs } from "@/components/underline-tabs";
import { UpgradeInstanceDialog } from "@/components/upgrade-instance-dialog";
import type { ControlAction } from "@/context/apps-context-base";
import type { PaletteAction } from "@/context/palette-context-base";
import { useApps } from "@/context/use-apps";
import { usePaletteActions } from "@/context/use-palette";
import { useDeleteApp, useSaveAsTemplate } from "@/hooks/use-app-actions";
import { useAppInstance } from "@/hooks/use-app-instance";
import { useHistorySheet } from "@/hooks/use-history-sheet";
import { useBackUpNow } from "@/hooks/use-run-backup";
import { useTabParam } from "@/hooks/use-tab-param";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { usePageTitle } from "@/layouts/use-page-title";
import { hasRunningContainers } from "@/lib/app-status";
import { needsAttention } from "@/lib/backups";
import { appLabel } from "@/lib/kpi";
import { LEGACY_APP_TABS, type AppTab } from "@/lib/routes";
import type { TemplateView } from "@/lib/templates";

const TABS = ["overview", "logs", "configuration", "backups", "metrics", "settings"] as const satisfies readonly AppTab[];
type Tab = (typeof TABS)[number];

const CONTROL_DONE: Record<ControlAction, string> = {
  start: "App started",
  stop: "App stopped",
  restart: "App restarted",
  update: "Latest images pulled",
};

const CONTROL_FAILED: Record<ControlAction, string> = {
  start: "Couldn't start the app",
  stop: "Couldn't stop the app",
  restart: "Couldn't restart the app",
  update: "Couldn't pull the latest images",
};

// AppDetailsPage is /server/:id/app/:appId: the header, the banners that
// matter now, and the tabs (Overview first). History is a side sheet
// (?history=1). The page stays mounted from app to app, so every per-app
// part is keyed by the app's id.
export default function AppDetailsPage() {
  const { appId = "" } = useParams();
  const { apps, statusByApp, backupsByApp, loading, control, refresh } = useApps();
  const templatesApi = useTemplatesApi();
  const [tab, setTab] = useTabParam<Tab>(TABS, "overview", { aliases: LEGACY_APP_TABS });
  // After useTabParam: see useHistorySheet.
  const [historyOpen, setHistoryOpen] = useHistorySheet();

  const app = useMemo(() => apps.find((a) => a.id === appId), [apps, appId]);
  const lookup = useAppInstance(app);
  // ownership is the template side of the app (null for a plain app). Only
  // this app's own instance is ever used.
  const ownership: Ownership = app?.templateId
    ? { ...lookup, instance: lookup.instance?.app_id === app.id ? lookup.instance : null }
    : null;
  const inst = ownership?.instance ?? null;
  const latest = ownership?.current?.latest_revision ?? 0;
  const behind = inst !== null && latest > inst.revision;
  const [upgrade, setUpgrade] = useState<{ appId: string; target: TemplateView } | null>(null);
  const upgradeTarget = upgrade && upgrade.appId === app?.id ? upgrade.target : null;
  const reloadManaged = () => void Promise.all([lookup.reload(), refresh()]);
  // openUpgrade reads the template's latest version and opens the update
  // dialog (the header's "Update available"; phase 3's Configuration reuses
  // it).
  const openUpgrade = async () => {
    if (!inst) return;
    try {
      setUpgrade({ appId: inst.app_id, target: await templatesApi.getTemplate(inst.template_id) });
    } catch (e) {
      toast.error("Couldn't load the template", { description: e instanceof Error ? e.message : undefined });
    }
  };

  const [pending, setPending] = useState<ControlAction | null>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const deleteApp = useDeleteApp(appId);
  const saveAsTemplate = useSaveAsTemplate(appId, app?.name ?? "");
  const backUpNow = useBackUpNow(appId);
  const title = appId ? appLabel(app?.name, appId) : "App";
  usePageTitle(title);

  const runControl = async (action: ControlAction) => {
    if (!appId) return;
    setPending(action);
    try {
      await control(appId, action);
      toast.success(CONTROL_DONE[action]);
    } catch (e) {
      toast.error(CONTROL_FAILED[action], { description: e instanceof Error ? e.message : undefined });
    } finally {
      setPending(null);
    }
  };

  const status = statusByApp[appId];
  const running = hasRunningContainers(status);
  // ⌘K: this app's actions while its page is open, under its name.
  const paletteActions: PaletteAction[] = app
    ? [
        { id: "restart", label: "Restart", icon: RotateCw, run: () => void runControl("restart") },
        running
          ? { id: "stop", label: "Stop", icon: Square, run: () => void runControl("stop") }
          : { id: "start", label: "Start", icon: Play, run: () => void runControl("start") },
        {
          id: "update",
          label: "Pull latest images",
          icon: ArrowUpCircle,
          keywords: ["update", "upgrade", "images"],
          run: () => void runControl("update"),
        },
        { id: "logs", label: "View logs", icon: ScrollText, run: () => setTab("logs") },
        {
          id: "backup",
          label: "Back up now",
          icon: DatabaseBackup,
          keywords: ["backup", "copy"],
          disabled: backUpNow.busy,
          run: () => void backUpNow.run(),
        },
      ]
    : [];
  usePaletteActions(title, paletteActions);

  if (!app) {
    if (loading) return null;
    return <NotFound title="App not found" description="This app doesn't exist on this server." />;
  }

  const name = appLabel(app.name, app.id);
  const historyButton = (
    <Button variant="outline" onClick={() => setHistoryOpen(true)}>
      <History className="size-4" aria-hidden /> History
    </Button>
  );

  return (
    <div className="space-y-6">
      <AppHeader
        app={app}
        status={status}
        instance={inst}
        behind={behind}
        pending={pending}
        onControl={(action) => void runControl(action)}
        onUpdateTemplate={() => void openUpgrade()}
        backUpBusy={backUpNow.busy}
        onBackUpNow={() => void backUpNow.run()}
        canSaveAsTemplate={ownership === null}
        savingTemplate={saveAsTemplate.busy}
        onSaveAsTemplate={() => void saveAsTemplate.run()}
        onDelete={() => setDeleteOpen(true)}
      />

      {inst?.state === "pending" && inst.last_error ? (
        <ErrorText
          alert
          icon
          text={inst.last_error}
          prefix="The app's last deploy from its template failed: "
          suffix=". Fix its values under Configuration and save again."
        />
      ) : null}
      <StaleVarsBadge key={app.id} appId={app.id} />
      <AppAlertsBanner key={`alerts-${app.id}`} serverId={app.serverId} appId={app.id} />

      {upgradeTarget && inst ? (
        <UpgradeInstanceDialog
          // The page's live status says whether the app is stopped now.
          instance={{ ...inst, status_code: status ?? inst.status_code }}
          appName={name}
          current={ownership?.current ?? null}
          target={upgradeTarget}
          onClose={() => setUpgrade(null)}
          onDone={reloadManaged}
        />
      ) : null}

      <UnderlineTabs<Tab>
        value={tab}
        onValueChange={setTab}
        label="App sections"
        items={[
          { value: "overview", label: "Overview" },
          { value: "logs", label: "Logs" },
          { value: "configuration", label: "Configuration" },
          {
            value: "backups",
            label: (
              <>
                Backups
                {needsAttention(backupsByApp?.[app.id]) ? (
                  <TriangleAlert
                    role="img"
                    aria-label="A backup is late or failing"
                    className="ml-1.5 inline size-3.5 align-text-bottom text-status-warn"
                  />
                ) : null}
              </>
            ),
          },
          { value: "metrics", label: "Metrics" },
          { value: "settings", label: "Settings" },
        ]}
      >
        <TabsContent value="overview" className="mt-6">
          <AppOverview
            key={app.id}
            app={app}
            status={status}
            pending={pending}
            onControl={(action) => void runControl(action)}
            onOpenHistory={() => setHistoryOpen(true)}
          />
        </TabsContent>
        <TabsContent value="logs" className="mt-6">
          <AppLogsTab key={app.id} appId={app.id} />
        </TabsContent>
        <TabsContent value="configuration" className="mt-0">
          <ConfigurationTab
            key={app.id}
            appId={app.id}
            ownership={ownership}
            onSaved={reloadManaged}
            headerActions={historyButton}
          />
        </TabsContent>
        <TabsContent value="backups" className="mt-6">
          <AppBackups key={app.id} appId={app.id} />
        </TabsContent>
        <TabsContent value="metrics" className="mt-6">
          <AppMetricsTab key={app.id} serverId={app.serverId} appId={app.id} appName={name} />
        </TabsContent>
        <TabsContent value="settings" className="mt-6">
          <AppSettingsTab
            key={app.id}
            app={app}
            ownership={ownership}
            running={running}
            onDetached={reloadManaged}
            onSaveAsTemplate={() => void saveAsTemplate.run()}
            savingTemplate={saveAsTemplate.busy}
            onDelete={() => setDeleteOpen(true)}
          />
        </TabsContent>
      </UnderlineTabs>

      <HistorySheet key={app.id} appId={app.id} ownership={ownership} open={historyOpen} onOpenChange={setHistoryOpen} />
      <DeleteAppDialog name={name} open={deleteOpen} onOpenChange={setDeleteOpen} onConfirm={deleteApp} />
    </div>
  );
}

// ConfigurationTab is the app's editor, or for an app a template manages
// its instance's parameters (never the plain editor, whose save would be
// refused). headerActions (the History button) go in either panel's bar.
// Phase 3 owns what is inside both panels.
function ConfigurationTab({
  appId,
  ownership,
  onSaved,
  headerActions,
}: {
  appId: string;
  ownership: Ownership;
  onSaved: () => void;
  headerActions?: ReactNode;
}) {
  if (ownership?.error) return <LoadError message={ownership.error} onRetry={() => void ownership.reload()} />;
  if (ownership?.loading) return <PageSpinner />;
  if (ownership?.instance && ownership.current) {
    const inst = ownership.instance;
    return (
      <InstancePanel
        key={`${inst.revision}-${inst.updated_at}`}
        instance={inst}
        current={ownership.current}
        onSaved={onSaved}
        headerActions={headerActions}
      />
    );
  }
  return <AppEditorPanel appId={appId} headerActions={headerActions} />;
}
```

In `web/src/components/alerts/active-alerts.tsx` delete `AppAlertsBadge` (and its comment); nothing else uses it.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/pages/app-details.test.tsx src/components/alerts/active-alerts.test.tsx`
Expected: PASS

Run: `grep -rn "AppAlertsBadge\|AppMetricBlocks serverId={app.serverId} appId={app.id} />\|Revision {app.version}" web/src`
Expected: no output (no badge, no KPI row above the tabs, no "Revision N").

- [ ] **Step 5: Lint, type-check, the whole web suite, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b && pnpm --dir web exec vitest run
git add web/src/components/app-page/app-header.tsx web/src/pages/app-details.tsx web/src/pages/app-details.test.tsx web/src/components/alerts/active-alerts.tsx web/src/components/alerts/active-alerts.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): app page header with one primary and ⋯, Overview first, Back up now in ⌘K

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 15: Phase verification

**Files:** none changed unless a check fails (then fix in the owning task's files and commit as `fix(web): …`).

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

Expected: changed web lines ≥ 80% covered. If a file falls short, add the missing test to that task's test file (the uncovered lines are listed), commit, re-run.

- [ ] **Step 3: Nothing outside `web/` and `docs/` changed, and no old wording is left**

```bash
git diff --stat main -- . ':!web' ':!docs'
grep -rn '"Unlink\|Unlink from\|>Update<\|Revision {' web/src --include=*.tsx | grep -v '\.test\.tsx' | grep -v 'upgrade-instance-dialog\|template-instances\|template-page'
```

Expected: no output from either (the template pages and `UpgradeInstanceDialog` are phase 6's).

- [ ] **Step 4: Browser check on a throwaway standalone instance**

Build and run a standalone binary against a scratch data directory (`make build`, then `bin/standalone` with `DATABASE_URL=sqlite://…` and `AGENT_DATA_DIR` in a temp dir), sign in, create two apps (one with a domain if ingress is on, one stopped, one with a backup set up), and check at 1280, 768 and 375 px, light and dark:
- Apps page: "Apps", "homelab · 1 running · 1 stopped", one New app (goes to the new-app page), the health strip (CPU · Memory · Disk · Monitoring →), cards with status pills, address or "No public address"; no server cards; empty server shows "Run your first app".
- App page: header (icon, name, pill, "Open ↗"), Restart/Start + ⋯ (Stop, Pull latest images, Back up now, Save as template, Delete…); tabs Overview · Logs · Configuration · Backups · Metrics · Settings, scrolling inside themselves at 375; Overview two columns at 1280, one at 768/375.
- Configuration → History opens the side sheet; `?tab=history` in the address bar lands there too; Roll back asks first.
- Backups: flat rows, ⋯ menu; Edit → Advanced folded; Metrics: four charts, "Alert rules for this app" folded.
- Settings: change the colour of the stopped app → it stays stopped, History shows a draft; rename the running one → no redeploy; Delete… asks with the two opt-ins.
- ⌘K on an app page lists Back up now.

Take screenshots at the three widths for the PR description. Stop the instance and delete its scratch directory.

- [ ] **Step 5: Commit any fixes**

```bash
git status --short
```

Expected: clean (or only the fixes, committed with `fix(web): …` and the `Co-Authored-By` line).

---

## Interfaces for later phases

**Phase 3 (new app flows + Configuration tab):**
- **Configuration switch:** `ConfigurationTab` stays a private function in `web/src/pages/app-details.tsx`, rendered inside `<TabsContent value="configuration" className="mt-0">`: `LoadError`/`PageSpinner` while a templated app's instance is read, `<InstancePanel instance current onSaved headerActions />` for a template-managed app, else `<AppEditorPanel appId headerActions />`. Phase 3 owns everything inside both panels.
- **History button slot:** both panels take `headerActions?: ReactNode` and render it in their sticky action bar right before their Save buttons (Task 10). The page passes `<Button variant="outline"><History/> History</Button>`, which calls `setHistoryOpen(true)` from `useHistorySheet()` (`@/hooks/use-history-sheet`, `?history=1`). `HistorySheet` and `useAppRevisions(appId, enabled?)` are available.
- **Upgrade:** the page keeps `openUpgrade()` (reads the latest template version, mounts `UpgradeInstanceDialog`) for the header's "Update available"; phase 3 adds `onUpgrade={() => void openUpgrade()}` to `ConfigurationTab` and `latestRevision`/`onUpgrade` to `InstancePanel`.
- **Details section:** Settings → General now edits name, icon, colour and description (`saveAppDetails` in `@/lib/app-details-save`: rename route + `stateFromDetail`/`buildSavePayload`/`saveApp`, draft for a stopped app or beside a draft). Phase 2 **leaves** the "Details" section of `AppEditor` in place; phase 3 Task 9 removes it from the Configuration view.
- **New app:** the Apps page's one primary **New app** is `<Button asChild><Link to={newAppPath(activeServer.id)}>` in `web/src/pages/home.tsx` (test: `home.test.tsx` "goes to the new-app page from New app"); phase 3 swaps it for `NewAppButton`. The empty state "Run your first app" links to `newAppSub("template")` and `newAppSub("compose")` in `web/src/components/app-grid.tsx`.
- **IconPicker** gained `label` (accessible name, default "Icon") and `disabled`; keep them when routing its grid through `IconGrid`.

**Phase 4 (Monitoring + Inbox):** `FiringAlertsLine({ serverId })`, `AppAlertsBanner({ serverId, appId })` (`@/components/alerts/active-alerts`); `ActiveAlertsStrip` and `FiringBadge` are still exported (no longer on the Apps page) for the Monitoring banner. `ServerHealthStrip({ server })` links to `monitoringPath`. `AppMetricsTab` folds `TargetAlertsCard` under `Disclosure` "Alert rules for this app" — the same pattern fits "Alert rules for this server".

**Phase 5 (Settings):** the server's IP, cores and agent version left the Apps page with `ServerCards` (deleted): Settings → Server → General shows them. `DeleteAppDialog`'s hints point at "Settings → Docker" and "Settings → Storages".

**Shared:** `containersByApp` (apps context), `appAddresses`/`primaryAddress`/`appTitle`/`appWarning`/`countApps`/`appsSubtitle`/`filterApps`/`containerHint` (`@/lib/app-summary`), `runnableBackups` (`@/lib/backups`), `RESTORING` (`@/lib/status`), `useRunBackup`/`useBackUpNow` (`@/hooks/use-run-backup`), `useDeleteApp`/`useSaveAsTemplate` (`@/hooks/use-app-actions`), `AppHeader`, `AppOverview`, `AppSettingsTab`, `HistoryPanel`, `HistorySheet` (`@/components/app-page/…`).
