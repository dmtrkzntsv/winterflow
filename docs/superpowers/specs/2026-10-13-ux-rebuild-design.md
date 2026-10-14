# UX rebuild — design

Date: 2026-10-13. Status: approved in conversation (sections 1–5 explicitly; 6–8 delegated to
the implementer's judgement by the user).

## Goal

Make the web UI simple, modern and mobile friendly for its main audience: an individual builder
or a small team running a few apps on one or a few home/small servers. Simpler is better; power
features stay reachable but out of the default view. Frontend only: no API change, no feature
removed.

## 1. Principles

1. **One job per screen.** The primary action sits top-right and is the only filled button.
2. **Show state, not mechanics.** By default: status, address, health. Compose, ports, revisions,
   commit hashes, Docker terms live behind "Advanced", "More options", or the last tab.
3. **Plain words.** "Update" → "Pull latest images"; "Revision 7"/"r7" → "Version 7" or "Saved 2h
   ago"; "Unlink" → "Detach from template"; "Resync" → "Send again"; "Orphaned" → "Not used by any
   app"; "Shoutrrr URL" → "Other (URL)".
4. **Fields appear when they matter.** Optional fields behind a disclosure, defaults prefilled.
5. **One way to do each thing.** Shared primitives (below); no `window.confirm`; every destructive
   action confirms through one dialog; real router links everywhere (no full reloads).

### Look

Keep the Frost palette, the snowflake logo (never restyled), Inter, soft cards, the sky wash.
Modernize by reduction: fewer nested frames (flat rows with hover instead of tables in bordered
cards), tighter type scale (page title 24/600, section title 15/600, body 14), 4/8 spacing,
reading pages capped near 1100px (charts/logs full width), status colours as theme tokens
(`--status-ok|warn|bad|idle`) instead of hard-coded Tailwind colours, Light/Dark/System theme
toggle (default System), 150 ms transitions, reduced-motion respected (login snowfall included).

### Shared primitives (new, `web/src/components/`)

- `PageHeader` — title, optional subtitle, optional back link, actions slot; stacks on mobile.
- `StatusPill` / `StatusDot` — one mapping from app/server/backup state to token colour + word.
- `ConfirmDialog` — the single destructive-confirm (title, body, confirm label, pending state).
- `EmptyState` — icon, one sentence, one optional action.
- `ResponsiveDialog` — shadcn `Dialog` on ≥640px, `Drawer` (vaul) bottom sheet on phones.
- `Section` / `RowList` — flat list rows that collapse to stacked cards on mobile.
- `Disclosure` — "More options"/"Advanced" collapsible with consistent styling.
- One tab style (underline), horizontally scrollable on mobile, synced to `?tab=`.

## 2. Navigation and shell

Sidebar (desktop ≥1024px, 240px, collapsible to icons):

```
[❄ homelab        ▾]   server switcher
[⌕ Search…       ⌘K]
▣ Apps                  home (/server/:id) — replaces "Dashboard"
    ● Website           live app list, status dot
    + New app
◔ Monitoring            /server/:id/monitoring
✉ Inbox            3   /inbox — org-wide, unread count
⧉ Templates             /templates
──────────────
⚙ Settings              /server/:id/settings (server section) and /settings/* (org/account)
(AL) Alex Admin    ▾   Theme (Light/Dark/System) · Log out
```

- **Server switcher:** distributed — dropdown of servers with status dots, ⌘1…⌘9 really wired,
  "Add server". Standalone — logo + server name, no chevron, no menu. Switching keeps the page
  kind (Apps→Apps, Monitoring→Monitoring, Settings→Settings); from an app page it goes to Apps.
- **Scope:** per-server = Apps, app pages, Monitoring, Settings/Server. Org-wide = Inbox,
  Templates, Settings/Organization, Settings/Account.
- **No breadcrumb bar on desktop**; every page starts with `PageHeader`; nested pages show a
  back link ("← Website", "← Templates").
- **Mobile (<1024px):** sticky slim top bar (menu button, page title, search icon); sidebar is a
  sheet that closes on navigation; dialogs become bottom sheets on phones; tables become stacked
  rows; tap targets ≥40px; app action bar becomes primary + "⋯". Verified at 375, 768, 1280.
- **⌘K palette** (cmdk, already a dependency): jump to apps on this server, servers, every page
  and Settings section; actions New app, Add server, Theme; on an app page also Restart,
  Stop/Start, Pull latest images, View logs, Back up now. Empty query shows recent items
  (localStorage). Opened by ⌘K/Ctrl+K, the sidebar search box, the mobile search icon.

### Routes

| New route | Replaces |
|---|---|
| `/server/:id` (Apps) | same |
| `/server/:id/new` (+ `?from=template|compose|git`) | `create-app`, `create-app/template` (old paths redirect) |
| `/server/:id/app/:appId?tab=overview|logs|configuration|backups|metrics|settings` | same path; old `tab=editor`→configuration, `tab=history`→configuration + history sheet |
| `/server/:id/monitoring` | same (alert emails link here — must keep working) |
| `/server/:id/settings/{general,domains,storages,docker}` | `/server/:id/settings?tab=…` (redirects; `tab=variables`→`/settings/variables`, `tab=agent`→general, registries/networks/volumes→docker) |
| `/inbox` | `/org/alerts` (history tab); `/org/alerts?tab=rules|channels` → `/settings/notifications` |
| `/templates`, `/templates/new`, `/templates/:id` | `/org/templates…` |
| `/settings/{organization,members,variables,notifications,password,tokens}` | `/org/members`, `/org/variables`, `/user/password`, `/user/tokens` |

`/settings` alone redirects to the active server's settings general page (or organization when
there is no server). `must_change_password` still forces `/settings/password` (old
`/user/password` redirects). Alert deep links `?tab=metrics` keep working.

## 3. Apps page and app page

### Apps page (`/server/:id`)

- `PageHeader` "Apps", subtitle "homelab · 3 running · 1 stopped". One primary **New app**.
- **Health strip:** one slim row `CPU 30% · Memory 39% · Disk 55% · Monitoring →` for this server
  (offline/never-connected state shown instead when relevant). The multi-server cards leave this
  page; IP/cores/agent version move to Settings → Server → General.
- **Firing alerts:** one line `⚠ 2 alerts firing → Inbox` (only when firing).
- **App cards:** icon, name, `StatusPill`, primary address link (or "No public address"); a small
  warning chip only for failing/late backups or stale shared variables. No raw ids.
- Filter box when there are more than 8 apps.
- Empty state "Run your first app" with *From a template* and *Compose file*.

### App page

Header: icon, name, `StatusPill`, primary address with "Open ↗"; small "From template X" chip and
"Update available" only when behind. Actions: one contextual primary (Start when stopped, Restart
when running) + "⋯" (Stop, Pull latest images, Back up now, Save as template, Delete). No
"Revision N" in the header. Stale-vars and firing-alert banners only when relevant.

Tabs: **Overview** (default) · Logs · Configuration · Backups · Metrics · Settings.

- **Overview** (2 columns desktop, 1 mobile):
  - *Status* — since when, containers with state dots, restart/problem hints; Restart.
  - *Access* — domains with HTTPS state; "Add a domain" when none (only when ingress exists).
  - *Resources* — CPU/Memory/Network/Disk with sparklines (today's KPI row); "View metrics →".
  - *Backups* — per backup: state, last copy, next run; Back up now, Restore…; or "Set up a backup".
  - *Recent changes* — last 3 history entries ("Changed compose.yml · 2h ago"), live/draft;
    "View history".
- **Logs** — full height; container filter chips only with several containers; line count,
  refresh, jump to latest.
- **Configuration** — only what runs: Files, Variables, Domains, Git source. Icon/name/colour/
  description move to Settings. "Deploy from Git" collapsed unless used; redirects and image chips
  under "Advanced". **History** button opens a side sheet (versions labelled by subject + relative
  time; hash on hover; Roll back, Deploy draft) — replaces the History tab. Template-managed apps:
  parameter form + "A newer version of X is available → Update".
- **Backups** — flat rows (name, state pill, last copy, next run, "⋯" actions). Edit dialog puts
  retention tiers, location and skipped paths under "Advanced".
- **Metrics** — range menu + 4 charts; "Alert rules for this app" collapsed below. The KPI row
  above the tabs is removed (Overview carries it).
- **Settings** — General (name, icon, colour, description, one Save — name change uses the rename
  route, the rest the existing save path), Template (Save as / Detach), Danger zone (Delete).

## 4. Creating an app

"New app" opens a chooser (dialog / bottom sheet): **From a template** (only when templates
exist), **Compose file**, **Git repository**; each goes to `/server/:id/new?from=…`.

- **Template:** template cards grid (icon, name, description) → form below: required parameters,
  optional ones under "More options" with defaults shown; backups storage picker only when
  declared (first storage preselected); address preview "Will be available at …"; version picker
  under "Advanced" (default latest). One **Create app**.
- **Compose:** Name (icon + colour auto from the name, editable via an icon button), prefilled
  compose editor, "Add another file" link, collapsed "Variables (0)", one Address row
  (hostname + port, "Add another"); description, redirects, image chips under "More options".
- **Git:** Name, Repository URL, Branch (`main`); token behind "Private repository?"; compose path
  and auto-redeploy under "Advanced".
- Field-level validation messages instead of toasts; sticky bottom action bar on phones; after
  Create land on the new app's Overview.

## 5. Monitoring and Inbox

- **Monitoring:** header (server name, data-as-of, range menu); 4 KPI tiles; default charts CPU,
  Memory, Disk usage, Network; "Apps" section with CPU by app and Memory by app; collapsed "More
  details" with Load, Disk I/O, Temperature, Docker disk usage; firing alerts as a top banner;
  "Alert rules for this server" collapsed with a link to Settings → Notifications. No env var
  names in empty states.
- **Inbox (`/inbox`, org-wide):** "Firing now" pinned on top, then events grouped by day; rows:
  red dot (fired) / green check (resolved), plain title, target, time, small delivery note; unread
  dot + "Mark all as read"; row click opens the target (app Metrics or server Monitoring); filters
  All / Firing / Resolved, server filter only with 2+ servers; empty "All quiet…"; header link
  "Notification settings →". Unread = newer than the user's last-seen timestamp stored in
  localStorage (per user id); the sidebar badge shows the count. No backend change.

## 6. Settings

One area with a sub-menu (left column desktop; list → drill-in on mobile).

- **Server · {name}**
  - *General* — name, status, IP, CPU/RAM, agent version (read-only; the API has no rename);
    Updates: "Running vX" + version field + Update.
  - *Domains* — flat rows (name, HTTPS state, expiry only when soon, apps using it). Dialog: name;
    HTTPS as plain choices "Automatic (Let's Encrypt)" / "My own certificate" (PEM fields) / "Off —
    behind Cloudflare Tunnel or another proxy". Only shown when the server has ingress.
  - *Storages* — rows; dialog type-first (Folder on this server / S3 bucket); S3 shows Endpoint,
    Bucket, Access key, Secret key, with Region, Prefix, Path-style under "Advanced"; inline Test;
    "Backups of deleted apps" link (orphans; raw id on hover only).
  - *Docker* (last) — Registries, Networks, Volumes as sections on one page; every delete confirms;
    anonymous volumes behind a toggle.
- **Organization**
  - *General* — name, icon, colour (no raw id).
  - *Members* — rows (avatar, name, email, "(you)"), "⋯" with Reset password / Remove via
    `ConfirmDialog`; Add member → temp password with Copy.
  - *Shared variables* — one page replacing the org and server variable pages: rows show name,
    value or "Secret", and scope "All servers" / "{server} only". Add dialog: Name, Value, Secret;
    "Applies to" under Advanced, hidden in standalone (org scope). Built-in `WF_*` collapsed,
    read-only. "Sync failed → Send again" and "N apps use old values → Reload" only when relevant;
    rollout strip kept.
  - *Notifications* — Channels (service icon grid, "Other (URL)", SMTP port/username under
    Advanced) and Alert rules (sentence-style dialog with Cancel) on one page.
- **Account** — *Password*, *API tokens* (revoke via `ConfirmDialog`).

## 7. Templates, sign-in, adding a server

- **Templates list:** cards (icon, name, "Used by N apps", "Updated 2 days ago"), New template.
- **Template page:** tabs Editor · Apps using it · Versions; "Version N" wording; parameters grid
  rows show Name + Default with Description/Pattern/Secret/Required in an expandable row; "Update
  all apps" keeps the rollout strip.
- **Sign-in:** a fresh (unclaimed) instance sends `/login` to the setup screen ("Set up
  Winterflow"); illustration hidden on phones; snowfall respects reduced-motion.
- **Add server** (distributed only): standard dialog, two steps (copy install command, enter the
  6-character code); remove the mock organization chips and fake create-org modal; provider links
  collapsed under "Don't have a server yet?".

## 8. Delivery and testing

Phased commits on one branch (`feat/ux-rebuild`), one PR:

1. Foundation + shell — tokens, primitives, sidebar, switcher, mobile top bar, ⌘K, theme toggle,
   route table + redirects.
2. Apps page + app page (Overview, tabs, header, history sheet).
3. New app flows + Configuration tab.
4. Monitoring + Inbox.
5. Settings area (server, organization incl. merged variables and notifications, account).
6. Templates, sign-in, add server.
7. Polish pass from browser review at 375/768/1280, light and dark.

Each phase: existing tests updated to new copy/structure (keep existing `data-testid`s where the
element survives), new tests for new primitives/pages, `pnpm --dir web run lint`, `tsc -b`,
`vitest` green, diff coverage ≥80% on changed web lines. Browser check on a throwaway standalone
instance; screenshots in the PR. `api/openapi.yaml` and Go code untouched (if `make openapi`
diff appears, something is wrong).

## Out of scope / follow-ups

- Server rename/remove, latest-agent-version lookup (need API).
- Server-side read state and a broader activity feed for the Inbox.
- Moving hard-coded strings into i18n.
