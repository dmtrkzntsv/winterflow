# x-winterflow Sub-project 4: Catalog UI Implementation Plan

**Goal:** Make the catalog discoverable and installable in New app, Templates, and the command palette; show catalog identity and upgrades on installed apps. Ship the remaining UI work as one PR.

**Spec:** [2026-10-14-x-winterflow-catalog-design.md](../specs/2026-10-14-x-winterflow-catalog-design.md), “Sub-project 4: UI”. The first three sub-projects are already merged (#27, #29, #30). Start from `1c47d48` or a later `main`.

## Scope and decisions

- Reuse the shipped org template editor, live Detected panel, Save as template, instance form, hostname fields, backup storage choices, revision comparison, and rollout flow. Do not rebuild them.
- `catalog.*` and the top-level template `name` are the current catalog identity and search data. `body.*`, `detected`, and the revision list describe immutable compose revisions. Thus a catalog detail page opened on an old revision still shows the current logo, description, license, and homepage. Never use `body.summary`, `body.description`, `body.icon`, or `body.color` as a catalog metadata fallback.
- Keep withdrawn catalog templates visible on `/templates` only when the API lists them because this org has instances. Show the withdrawal note on their detail page; disable new installs. Existing instances, revision history, and upgrades remain accessible.
- **Logos:** load the current `catalog.logo_url` directly in the browser. Missing or failed images fall back to the existing `AppIcon`. Browsers contact the catalog image host, and no image proxy route is needed. The page currently sends no Content Security Policy restricting image origins.
- Old catalog revision pruning is outside this PR. Do not delete revisions or add retention settings.
- The Templates **index** has no tabs: org templates first, Catalog below. Org template detail keeps its existing tabs; catalog detail has read-only Files, Versions, and Apps using it sections, with no edit/delete actions.

## Existing contracts and gaps

- `get-templates` returns org entries followed by active catalog entries and withdrawn catalog entries with live org instances. `TemplateSummary` already carries `source`, `instances`, `behind`, `withdrawn_at`, and current `catalog`. `get-template` carries the same current catalog metadata plus the chosen revision's `body`, `detected`, and `revisions`.
- The list lacks the latest org template's summary. Replace `useTemplateLooks` with a shared, bounded latest-org-view loader keyed by `id@latest_revision`, retaining icon/color and adding `body.summary` for picker and palette search. Fetch org views only; catalog cards and search read list metadata directly. A failed individual view keeps its name/icon fallback and does not hide the template.
- `useTemplateCount` and `NewAppChoices` currently count only org templates; change the choice to appear when either source has an installable entry. An empty catalog with no org templates keeps the existing empty state.
- `TemplateFlow` currently filters catalog entries out and renders all org cards above the form. Split selection from installation; a direct `?template=<id>` link opens the form immediately, and Change template reopens the picker. Do not clear a claimed install while a deploy is in flight.
- The app detail header already computes whether an instance is behind. Add the current catalog logo from its template view there and to the template's instance rows. The Apps grid also shows catalog identity and upgrade state; read template summaries and the active server's instances once each, then join by template ID and app ID. `behind` on a summary is an org-wide count and cannot identify a particular app.

## Tasks

### 1. Shared catalog presentation and search

- [ ] Add a pure `templateSearch` helper in `web/src/lib/templates.ts` (or a nearby module): trim and case-fold the query; match name and summary for both sources, and `catalog.keywords` plus `catalog.alternative_to` for catalog entries. Preserve input order. Treat missing optional metadata and an empty query safely. Unit-test those fields and source order.
- [ ] Build one reusable `CatalogLogo` component for cards, detail headers, palette rows, and instances. It loads `catalog.logo_url` directly and falls back to `AppIcon` on load failure or no logo. Keep useful accessible names and avoid a broken-image flash.
- [ ] Refactor `web/src/hooks/use-template-looks.ts` into the shared latest-org-view loader. Bound concurrent reads; cache by revision so picker, Templates, and palette can reuse results without loading every catalog body. Test stale result handling when the list changes.

### 2. New app picker and install header

- [ ] Update `useTemplateCount`, `NewAppChoices`, and their tests so catalog-only installations offer “From a template”; exclude withdrawn entries from new installs.
- [ ] Extract the template picker from `web/src/components/new-app/template-flow.tsx`. Show org templates first and catalog as a logo/name/summary grid, with one search field and source headings. Empty query shows both; no matches has a clear state. Selecting a card fetches that template and opens the existing install form. A direct template URL starts on the form; Change template returns to the picker while preserving the server. On phones use `Sheet` as a full viewport picker with a scrollable list and keyboard/focus behavior; the form stays on the New app page.
- [ ] Above the existing app name and variables, show the selected template's logo, current description, license, and homepage for catalog entries. Use safe external links with `rel="noreferrer"`; show org summary/description from its body. Label the primary action **Install**. Keep revision selection, hostnames for `http` exposes when domains exist, backups, secret encryption, retry/idempotency, and successful navigation intact.
- [ ] Test org-first ordering, all search fields, catalog-only choice, direct links, mobile sheet selection, current catalog metadata on an older revision, withdrawn selection refusal, and a complete catalog install payload.

### 3. Templates index and catalog detail

- [ ] Extend `web/src/pages/org-templates.tsx` into the single Templates index. Keep org cards and New template action. Add a Catalog section: used catalog templates (`instances > 0`) first, then the searchable grid of the remaining active catalog templates. Do not duplicate used entries in the grid. Used cards show count, “Upgrade available” when `behind > 0`, and Upgrade all. Show withdrawn used entries with the withdrawal note and no install action. Handle the cases of no org templates, no catalog, and a catalog-only org without making the whole page look empty.
- [ ] Wire Upgrade all through the existing rollout endpoint and confirmation/progress behavior, reusing or extracting the logic in `template-page.tsx`; the list refreshes after completion. The action is scoped to the current org and template ID, as the API already enforces.
- [ ] Branch `web/src/pages/template-page.tsx` on `view.source`. The catalog branch shows current catalog identity/links, a read-only viewer of `body.files`, the existing `TemplateRevisions`, this org's `TemplateInstances`, Upgrade all, and the withdrawn note. Omit editable metadata/files, save, delete, and live inspect. Keep the org editor branch as it is. A withdrawn catalog template with instances remains reachable; an active template can open New app on a chosen server.
- [ ] Test section order, search, used-first deduplication, counts and upgrade indicators, rollout action, read-only behavior, old-revision/current-metadata distinction, and withdrawn visibility. Preserve existing org editor tests.

### 4. Command palette and catalog instances

- [ ] Add a Templates group to `web/src/lib/palette.ts` and `web/src/components/command-palette.tsx`. Show org templates on an empty query; add active catalog templates only after text is entered. Pass summary, keywords, and alternative-to through the palette's search mechanism. Selecting a template navigates to `newAppFromTemplatePath(activeServerId, id)`. With no active server, open its template detail page, whose Create app control already offers server selection; never construct a URL with an empty server ID. Exclude withdrawn entries. Keep unique cmdk values and recent-item behavior stable.
- [ ] Render the catalog logo and explicit “Upgrade available” state in the app detail header, `TemplateInstances`, and the Apps grid for catalog instances. Compare each instance's `revision` with its template's `latest_revision`, including on the grid after joining its template and instance reads. Keep org-template and plain-app icons and status behavior intact. A changed catalog logo appears from current `catalog.*` even when the instance runs an old revision.
- [ ] Test palette visibility, keyword and alternative-to matching, active-server routing, no-server handling, catalog logo fallback, and upgrade state in each instance surface.

### 5. Verification and delivery

- [ ] Run focused Vitest files while building, then `pnpm --dir web run lint`, `pnpm --dir web run build`, and `make web-test`. Run `make openapi`, `go test ./...`, and `make test-pg` if Docker is available. Run the repo's changed-line coverage gate against `origin/main` and address any failures.
- [ ] Manually exercise a standalone instance with an active catalog: catalog-only New app, phone picker, install with values/hostname/backups, catalog detail and Upgrade all, and an instance after a newer feed revision. Capture desktop and phone screenshots for the PR.
- [ ] Ship one PR with the spec and this plan linked, motivation, the logo decision, validation commands, screenshots, and any configuration note. Keep revision pruning for a separate proposal.

## Acceptance criteria

1. A catalog-only org can discover and install a template from New app; both sources share one search box, and the phone picker fills the screen.
2. The Templates index shows org entries, then used catalog entries, then the remaining catalog grid. Catalog detail is read-only, including for a withdrawn template that still has instances.
3. The palette exposes org templates immediately and matching active catalog templates after typing, installing on the active server.
4. Catalog logos and upgrade availability appear where catalog instances are shown. All catalog identity text and logos follow current `catalog.*`; compose files, detected fields, and revision diffs follow `body.*`.
5. Browser image requests use the current catalog URL, with fallback tiles when a logo cannot be loaded. No old catalog revision is pruned by this PR.
