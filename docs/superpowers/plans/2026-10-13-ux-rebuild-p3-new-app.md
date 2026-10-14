# UX rebuild, phase 3: new app flows and the Configuration tab — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the two create pages with one `/server/:id/new` page: a three-card chooser (inline, and as a dialog from the Apps page's **New app**), and three short forms (From a template, Compose file, Git repository) with messages next to the fields instead of toasts, a sticky action bar on phones, and a landing on the new app's Overview. Then reduce the app page's Configuration tab to what runs (Files, Variables, Domains, Git source; redirects and image versions under "Advanced"), and give template-managed apps an in-place "A newer version of X is available → Update".

**Architecture:** Validation becomes data: `editorErrors(state)` in `web/src/lib/app-editor-io.ts` returns every problem keyed by field (`name`, `repo_url`, `file:<id>`, `domain:<id>`, …), and each field renders its own message from that map; `focusFirstInvalid` moves focus to the first field marked `aria-invalid`, and any disclosure holding an error is forced open. The editor pieces the create forms and the Configuration tab share are split out of `AppEditor` (`FileEditors`, `VariablesEditor`, `AddressRows`, `RedirectRows`, `GitSourceFields`, `NameField`), so both screens are compositions of the same parts. The save payload is still built by the unchanged `buildSavePayload`; create forms only add `pruneBlankAddresses` (an untouched Address row is dropped, so a create without an address saves exactly as before).

**Tech Stack:** React 19, React Router 7, TypeScript 5.9 (strict, `verbatimModuleSyntax`, `erasableSyntaxOnly`), Tailwind 4, vendored shadcn/ui (Radix Popover/Collapsible, vaul), Vitest 5 + Testing Library on jsdom. No new dependency, no Go change.

**Spec:** `docs/superpowers/specs/2026-10-13-ux-rebuild-design.md` — this plan is phase 3 of §8 ("New app flows + Configuration tab"): all of §4 "Creating an app", and the Configuration bullet of §3 "App page". It builds on phase 1 (`docs/superpowers/plans/2026-10-13-ux-rebuild-p1-shell.md`, "Interfaces for later phases") and runs after phase 2 (`docs/superpowers/plans/2026-10-13-ux-rebuild-p2-apps.md`).

**Base:** branch `feat/ux-rebuild` with phases 1 and 2 committed. All commands run from the worktree root.

## Global Constraints

- The spec is binding; this phase touches only §4 and the Configuration tab. Apps page, app header, Overview, History sheet and the app Settings tab are phase 2's: do not redesign them here (Task 10 and Task 11 make the two small, named edits to phase 2 code).
- Frontend only: no Go change, no API change; `api/openapi.yaml` stays untouched. No feature is removed: secret files/variables, image tag browser, redirects, Git source, template versions and backup storages all stay reachable.
- **Save payloads stay identical.** `buildSavePayload(state, getPublicKey, appId?)` and `stateFromDetail(appId, detail)` keep their signatures and output. Git source sends `repo_url`, `branch`, `compose_path`, `auto_update`, `poll_seconds` (default 120) and an ECIES `token` (or `<encrypted>` to keep, `""` for none), and the config blob mirrors it with `token_set`. Ingress sends `{ domains: [{ domain, upstream_port, ssl }], redirects: [{ domain, path?, to, code, ssl? }] }`. Instance create/update bodies are unchanged except that a template backup without a default storage now preselects this server's first storage (the spec's "first storage preselected").
- Never delete anything under `web/src/components/ui/` (vendored shadcn/ui, kept on purpose even when unused). Do not edit vendored files.
- Plain words (spec §1.3): "Revision"/"r7" → "Version 7"; "Unlink" → "detach"; no Docker jargon outside "Advanced"; no raw ids. In particular the template form's version picker reads "Latest (version 5)" and "Version 3 · note", never "r5"/"r3" (phase 6 converts the rest of the template UI and does not touch the create form).
- One filled button per screen: **Create app** on the create forms, **Save & redeploy** on the Configuration tab. Everything else is `outline`, `ghost` or `link`.
- Validation is field-level: a refused Create/Save marks each bad field (`aria-invalid="true"`, red border, its message under it, `aria-describedby` pointing at the message), shows one line "Some fields need attention." in the action bar, opens any disclosure holding an error, and focuses the first bad field. No `toast.error` for validation. Server/agent failures stay as before (inline `ErrorText` on the create forms, toasts on the Configuration tab).
- Status colours are theme tokens (`text-status-warn`, `bg-status-ok`, …); never new hard-coded status colours.
- Type scale: page title via `PageHeader` (24/600), section titles `text-[15px] font-semibold`, body `text-sm`; 4/8 spacing; create forms are capped at `max-w-3xl`; transitions `duration-150` with `motion-reduce:transition-none`.
- Phones: the create forms' action bar is `sticky bottom-0` below 640px (static from `sm:` up), tap targets in it are at least 40px (`max-sm:h-10`).
- Real router links everywhere (`<Link>`, `navigate`); no `window.confirm`.
- Lint (`eslint-plugin-react-hooks` 7, `react-refresh`): no impure calls during render, no `ref.current` reads during render; files that export components export only components (plus types) — helpers go in `web/src/lib/*.ts`, hooks in `web/src/hooks/use-*.ts`.
- TypeScript: `import type` for types, no `enum`, no unused locals/imports — when a step removes the last use of an import, remove the import.
- Tests: Vitest + Testing Library, files beside the code; mock contexts the way existing tests do (`vi.mock("@/context/use-servers", …)`), answer fetches with `fakeAPI` from `@/test/fake-api`, mock `@/components/code-editor` with a `<textarea>`; keep existing `data-testid`s.
- Coverage: changed web lines must be ≥80% covered (`tools/diffcov`); `web/src/test/`, `*.test.tsx`, `web/src/main.tsx` and `web/src/components/ui/` are exempt.
- `pnpm --dir web run build` empties `web/dist`: run `git checkout -- web/dist/.gitkeep` after any build, before committing.
- Per task, before the commit: the task's Vitest files, `pnpm --dir web run lint` and `pnpm --dir web exec tsc -b` pass.
- Commits: Conventional Commits (`feat(web): …`, `refactor(web): …`), each message ending with the line `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Interfaces assumed from phases 1 and 2

Phase 1 (authoritative in its plan; grep it for exact code): `PageHeader({ title, subtitle?, back?: { to, label }, actions? })` (`@/components/page-header`); `Disclosure({ label = "More options", defaultOpen?, open?, onOpenChange?, className?, children })` (`@/components/disclosure`, Radix Collapsible: closed content is **not in the DOM**); `EmptyState({ title, icon?, action? })` (`@/components/empty-state`); `ResponsiveDialog({ open, onOpenChange, title, description?, children, footer? })` (`@/components/responsive-dialog`); `appsPath`, `newAppPath(serverId, from?)`, `appPath(serverId, appId, tab?)`, `appSub(appId, tab?)`, `serverSettingsPath(serverId, section?)`, `serverSettingsSub(section?)`, `templatePath(id)`, `TEMPLATES_PATH`, `type NewAppSource = "template" | "compose" | "git"` (`@/lib/routes`); `web/src/pages/new-app.tsx` (`NewAppPage`, reads `?from=`) and its route `/server/:serverId/new`; `web/src/routes.test.tsx` stubs every page. After phase 1, `create-app.tsx`/`create-from-template.tsx` call `usePageTitle("New app")` and link with `TEMPLATES_PATH` / `serverSettingsSub("storages")`.

Phase 2 (planned in parallel; this plan relies only on the following, and each task that touches phase 2 code says what to grep for):

1. **Configuration tab mount point.** The app page (`web/src/pages/app-details.tsx`, unless phase 2 moved it — find it with `grep -rn "<AppEditorPanel\|<InstancePanel" web/src --include=*.tsx | grep -v test`) renders, inside `<TabsContent value="configuration">`, a switch (today's `ConfigurationTab`) that shows `LoadError`/`PageSpinner` while a templated app's instance is read, `<InstancePanel instance current onSaved … />` for a template-managed app, else `<AppEditorPanel appId … />`. Phase 3 owns everything **inside** `AppEditorPanel` and `InstancePanel`; it does not move the switch.
2. **History button slot.** Phase 2 puts its "History" button (opens the History side sheet) into the Configuration tab by passing it to both panels as `headerActions?: ReactNode`, which each panel renders in its sticky action bar, right before its Save buttons. If phase 2 placed the button another way, Task 9/10 add the `headerActions` prop (they check first) and move phase 2's button element into it unchanged.
3. **Upgrade dialog stays on the page.** Phase 2 keeps the page-level `openUpgrade()` (reads the latest revision, mounts `UpgradeInstanceDialog`) for its header's "Update available"; Task 10 passes it to `ConfigurationTab` as `onUpgrade`.
4. **Settings tab owns name/icon/colour/description.** Phase 2's Settings → General edits them through `stateFromDetail` + `buildSavePayload` (and the rename route); Task 9 removes them from the Configuration tab. If phase 2 already removed the "Details" section from `AppEditor`, that step is a no-op.
5. **Apps page "New app".** Phase 2's Apps page has one primary **New app** control linking to `newAppPath(serverId)`; Task 11 swaps it for the `NewAppButton` (chooser dialog) built in Task 8.

## Interfaces for later phases

- `NewAppButton({ serverId, className? })` (`@/components/new-app/new-app-button`) — the primary "New app" that opens the chooser; `NewAppChoices({ serverId, onPick? })` — the three cards.
- `editorErrors(state): FieldErrors` and the key scheme below; `FormField`, `FieldMessage` (`@/components/form-field`); `invalidAttrs`, `focusFirstInvalid`, `hasErrorIn` (`@/lib/field-errors`).
- `NameField`, `AppLookPicker` (icon + colour from a name), `suggestAppLook(name)` (`@/lib/app-look`).
- `AppEditorPanel({ appId, headerActions? })`, `InstancePanel({ instance, current, onSaved, headerActions?, latestRevision?, onUpgrade? })`.

## Review Focus

1. **A bad value inside a collapsed section** — a nameless variable in the closed "Variables (1)", a redirect without a target under the closed "More options"/"Advanced", an optional template value with a `$` under the closed "More options". Expected: Create/Save opens that section, shows the message under the field and moves focus to it, instead of a silent no-op or a message pointing at nothing. Pinned in Task 5 (compose: variables), Task 7 (template: optional value), Task 9 (Configuration: Advanced redirects).
2. **The untouched Address row and a server without the built-in proxy.** Expected: creating an app without filling the Address row (or on a server whose `features.ingress` is off) sends no `ingress` key at all — the exact payload the old page sent — and a half-filled row (hostname, no port) is refused with "Enter a port from 1 to 65535." under the port. Pinned in Task 1 (`pruneBlankAddresses`) and Task 5.
3. **Two apps with the same name, or an agent answer that doesn't list the new app yet.** Expected: Create lands on the *new* app's Overview, never on the older app of the same name; when the answer doesn't contain it, the Apps page. Pinned in Task 5 (`createdApp` tests, ComposeFlow "opens the new app, not the old one of the same name").
4. **Template backups and this server's storages** — no default storage and storages present, no default and no storages, a default the server doesn't have. Expected: first storage preselected and sent; "No storage" selected and nothing sent; the missing one flagged with a link to Settings → Storages, not silently swapped. Pinned in Task 7.
5. **An app whose stored config has no name** (an app not reconciled yet) saved from the Configuration tab, which no longer has a Name field. Expected: the save is refused with "This app has no name yet. Name it in Settings first." linking to the app's Settings tab — not a dead end with focus going nowhere. Pinned in Task 9.

## Field-error keys (`editorErrors`)

| Key | Field | Messages |
|---|---|---|
| `name` | Name | "Give the app a name." |
| `repo_url` | Repository URL | "Enter the repository's URL." |
| `branch` | Branch | "Enter a branch." |
| `files` | the file list | "Add a compose file named compose.yml." |
| `file:<id>` | a file's name | "Name this file." |
| `var:<id>` | a variable's name | "Name this variable." |
| `domain:<id>` | an address's hostname | "Enter a hostname." / "Use a lowercase hostname like app.example.com." / "This hostname is already used above." |
| `port:<id>` | an address's port | "Enter a port from 1 to 65535." |
| `redirect-host:<id>` | a redirect's hostname | the hostname messages above, or "Add {host} as an address or a whole-domain redirect above this rule first." |
| `redirect-path:<id>` | a redirect's path | "The path must start with /." / "The path can't contain { or }." |
| `redirect-to:<id>` | a redirect's target | "Send it to a full address starting with http:// or https://." / "The target can't contain { or }." / "Pick 301, 302, 307 or 308." |

DOM ids: the input of key `k` is `app-name`, `src-url`, `src-branch`, `file-<id>`, `var-<id>`, `port-<id>`, `redirect-path-<id>`, `redirect-to-<id>` (hostnames keep `HostnameField`'s row id); its message has id `<input id>-error`.

## File map

Created:

| File | Responsibility |
|---|---|
| `web/src/lib/field-errors.ts` | `FieldErrors`, `errorId`, `invalidAttrs`, `hasErrorIn`, `focusFirstInvalid` |
| `web/src/components/form-field.tsx` | `FormField` (label + control + hint/error), `FieldMessage` |
| `web/src/lib/editor-rows.ts` | add/remove a file or a variable in editor state |
| `web/src/lib/compose-images.ts` | `imageRefs(content)` |
| `web/src/components/variables-editor.tsx` | `VariablesEditor` (rows + Add variable) |
| `web/src/components/git-source-fields.tsx` | `GitSourceFields` (URL, branch; token behind "Private repository?"; compose path + auto-redeploy under "Advanced") |
| `web/src/lib/app-look.ts` | `APP_COLORS`, `DEFAULT_APP_COLOR`, `suggestAppLook(name)` |
| `web/src/components/app-look-picker.tsx` | `AppLookPicker` (icon grid + colours in a popover) |
| `web/src/components/name-field.tsx` | `NameField` (name; icon + colour follow it until picked) |
| `web/src/lib/new-app.ts` | `parseNewAppSource`, `createdApp`, `composeStart`, `gitStart` |
| `web/src/hooks/use-create-app.ts` | `useCreateApp(serverId)` (save, toast, open the new app) |
| `web/src/components/new-app/create-bar.tsx` | `CreateBar` (Cancel + Create app; sticky bottom on phones) |
| `web/src/components/new-app/compose-flow.tsx` | `ComposeFlow` |
| `web/src/components/new-app/git-flow.tsx` | `GitFlow` |
| `web/src/components/new-app/template-flow.tsx` | `TemplateFlow` (cards, values, backups, summary, version) |
| `web/src/components/new-app/new-app-choices.tsx` | `NewAppChoices` (the three cards) |
| `web/src/components/new-app/new-app-button.tsx` | `NewAppButton` (primary + chooser dialog) |

Modified: `web/src/lib/app-editor-io.ts`, `web/src/types/app-config.ts`, `web/src/components/files-section.tsx`, `web/src/components/ingress-editor.tsx`, `web/src/components/hostname-field.tsx`, `web/src/hooks/use-registrations.ts`, `web/src/components/icon-picker.tsx`, `web/src/components/app-editor.tsx`, `web/src/components/app-editor-panel.tsx`, `web/src/components/instance-panel.tsx`, `web/src/pages/new-app.tsx`, `web/src/routes.tsx`, `web/src/routes.test.tsx`, the app page (phase 2), the Apps page (phase 2).

Deleted: `web/src/pages/create-app.tsx`, `web/src/pages/create-from-template.tsx`, `web/src/pages/create-from-template.test.tsx` (its cases move to `template-flow.test.tsx`).

Not touched: `web/src/components/variables-table.tsx` (shared variables, phase 5), `web/src/components/parameter-fields.tsx` (the forms pass it filtered lists), `web/src/components/upgrade-instance-dialog.tsx`, `web/src/components/image-tag-picker.tsx`.

---

### Task 1: Field-level validation model

**Files:**
- Create: `web/src/lib/field-errors.ts`, `web/src/lib/field-errors.test.ts`
- Create: `web/src/components/form-field.tsx`, `web/src/components/form-field.test.tsx`
- Modify: `web/src/lib/app-editor-io.ts` (add `editorErrors`, `newGitSource`, `newAddressRow`, `pruneBlankAddresses`; `validateEditorState` becomes a wrapper)
- Modify: `web/src/lib/app-editor-io.test.ts` (new cases)
- Modify: `web/src/types/app-config.ts` (add `isComposeFile`)

**Interfaces:**
- Consumes: nothing new.
- Produces:
  - `type FieldErrors = Record<string, string>`; `errorId(id: string): string` (= `` `${id}-error` ``); `invalidAttrs(id: string, message: string | undefined, className?: string): { "aria-invalid"?: true; "aria-describedby"?: string; className: string }`; `hasErrorIn(errors: FieldErrors, ...prefixes: string[]): boolean`; `focusFirstInvalid(root: HTMLElement | null): void` (`@/lib/field-errors`)
  - `FormField({ id, label, hint?, error?, className?, children })`, `FieldMessage({ id, message?, className? })` (`@/components/form-field`)
  - `editorErrors(state: AppEditorState): FieldErrors` (keys: table above), `newGitSource(): AppSourceConfig`, `newAddressRow(): IngressDomain`, `pruneBlankAddresses(state: AppEditorState): AppEditorState` (`@/lib/app-editor-io`); `validateEditorState(state): string | null` stays until Task 9 (first message of `editorErrors`)
  - `isComposeFile(name: string): boolean` (`@/types/app-config`)

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/field-errors.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from "vitest";

import { errorId, focusFirstInvalid, hasErrorIn, invalidAttrs } from "@/lib/field-errors";

describe("invalidAttrs", () => {
  it("marks a field with a message invalid and points at the message", () => {
    expect(invalidAttrs("app-name", "Give the app a name.", "font-mono")).toEqual({
      "aria-invalid": true,
      "aria-describedby": "app-name-error",
      className: "font-mono border-destructive focus-visible:ring-destructive",
    });
    expect(errorId("app-name")).toBe("app-name-error");
  });

  it("leaves a fine field alone", () => {
    expect(invalidAttrs("app-name", undefined, "font-mono")).toEqual({
      "aria-invalid": undefined,
      "aria-describedby": undefined,
      className: "font-mono",
    });
  });
});

describe("hasErrorIn", () => {
  it("matches a key or a key prefix", () => {
    const errors = { name: "x", "var:v1": "y" };
    expect(hasErrorIn(errors, "var:")).toBe(true);
    expect(hasErrorIn(errors, "redirect-")).toBe(false);
    expect(hasErrorIn(errors, "redirect-", "name")).toBe(true);
    expect(hasErrorIn({}, "name")).toBe(false);
  });
});

describe("focusFirstInvalid", () => {
  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("focuses the first invalid field once the frame is painted", async () => {
    document.body.innerHTML =
      '<div id="root"><input id="a" /><input id="b" aria-invalid="true" /><input id="c" aria-invalid="true" /></div>';
    const scroll = vi.fn();
    Element.prototype.scrollIntoView = scroll;
    focusFirstInvalid(document.getElementById("root"));
    await new Promise((r) => requestAnimationFrame(() => r(null)));
    expect(document.activeElement?.id).toBe("b");
    expect(scroll).toHaveBeenCalledWith({ block: "center" });
  });

  it("does nothing without an invalid field or a root", async () => {
    document.body.innerHTML = '<div id="root"><input id="a" /></div>';
    focusFirstInvalid(document.getElementById("root"));
    focusFirstInvalid(null);
    await new Promise((r) => requestAnimationFrame(() => r(null)));
    expect(document.activeElement).toBe(document.body);
  });
});
```

Create `web/src/components/form-field.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { FieldMessage, FormField } from "@/components/form-field";
import { Input } from "@/components/ui/input";
import { invalidAttrs } from "@/lib/field-errors";

describe("FormField", () => {
  it("labels its control and shows the hint while the field is fine", () => {
    render(
      <FormField id="src-branch" label="Branch" hint="Usually main.">
        <Input id="src-branch" {...invalidAttrs("src-branch", undefined)} />
      </FormField>,
    );
    expect(screen.getByLabelText("Branch")).not.toHaveAttribute("aria-invalid");
    expect(screen.getByText("Usually main.")).toBeInTheDocument();
  });

  it("replaces the hint with the field's message, which describes the control", () => {
    render(
      <FormField id="src-branch" label="Branch" hint="Usually main." error="Enter a branch.">
        <Input id="src-branch" {...invalidAttrs("src-branch", "Enter a branch.")} />
      </FormField>,
    );
    const input = screen.getByLabelText("Branch");
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(input).toHaveAccessibleDescription("Enter a branch.");
    expect(screen.queryByText("Usually main.")).not.toBeInTheDocument();
  });
});

describe("FieldMessage", () => {
  it("renders only with a message", () => {
    const { container, rerender } = render(<FieldMessage id="files" />);
    expect(container).toBeEmptyDOMElement();
    rerender(<FieldMessage id="files" message="Add a compose file named compose.yml." />);
    expect(screen.getByText("Add a compose file named compose.yml.")).toHaveAttribute("id", "files-error");
  });
});
```

Append to `web/src/lib/app-editor-io.test.ts` (add the imports to the existing import line: `editorErrors, emptyEditorState, newAddressRow, newGitSource, pruneBlankAddresses`, and `import type { AppEditorState } from "@/types/app-config";`):

```ts
function withConfig(patch: Partial<AppEditorState["config"]>): AppEditorState {
  const s = emptyEditorState();
  return { ...s, config: { ...s.config, name: "shop", ...patch } };
}

describe("editorErrors", () => {
  it("has nothing to say about a named app with a compose file", () => {
    expect(editorErrors(withConfig({}))).toEqual({});
  });

  it("names every problem by its field", () => {
    const s = withConfig({
      name: " ",
      files: [{ id: "f1", filename: "", is_encrypted: false }],
      variables: [{ id: "v1", name: "", is_encrypted: false }],
    });
    expect(editorErrors(s)).toEqual({
      name: "Give the app a name.",
      files: "Add a compose file named compose.yml.",
      "file:f1": "Name this file.",
      "var:v1": "Name this variable.",
    });
  });

  it("takes every compose file name compose itself finds", () => {
    for (const filename of ["compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"]) {
      expect(editorErrors(withConfig({ files: [{ id: "f", filename, is_encrypted: false }] }))).toEqual({});
    }
  });

  it("lets a Git-deployed app go without a compose file but asks for its repository and branch", () => {
    const s = withConfig({ files: [], source: { ...newGitSource(), branch: " " } });
    expect(editorErrors(s)).toEqual({ repo_url: "Enter the repository's URL.", branch: "Enter a branch." });
    expect(newGitSource()).toEqual({ repo_url: "", branch: "main", compose_path: "", auto_update: true, poll_seconds: 120 });
  });

  it("checks addresses: hostname, duplicates and port", () => {
    const s = withConfig({
      ingress: {
        domains: [
          { id: "d1", domain: "", upstream_port: "", ssl: true },
          { id: "d2", domain: "Shop.Example.com", upstream_port: 8080, ssl: true },
          { id: "d3", domain: "shop.example.com", upstream_port: 70000, ssl: true },
          { id: "d4", domain: "shop.example.com", upstream_port: 80, ssl: true },
        ],
        redirects: [],
      },
    });
    expect(editorErrors(s)).toEqual({
      "domain:d1": "Enter a hostname.",
      "port:d1": "Enter a port from 1 to 65535.",
      "domain:d2": "Use a lowercase hostname like app.example.com.",
      "port:d3": "Enter a port from 1 to 65535.",
      "domain:d4": "This hostname is already used above.",
    });
  });

  it("checks redirects field by field, in the order the server reads them", () => {
    const red = (id: string, patch: Partial<{ domain: string; path: string; to: string; code: 301 | 302 | 307 | 308 }>) => ({
      id,
      domain: "old.example.com",
      path: "",
      to: "https://new.example.com",
      code: 301 as const,
      ssl: true,
      ...patch,
    });
    const s = withConfig({
      ingress: {
        domains: [],
        redirects: [
          red("r1", { path: "/a", domain: "late.example.com" }),
          red("r2", { domain: "late.example.com" }),
          red("r3", { to: "http:/nohost" }),
          red("r4", { to: "https://x.example.com/{env.SECRET}" }),
          red("r5", { domain: "late.example.com", path: "no-slash" }),
          red("r6", { domain: "late.example.com", path: "/{x}" }),
          red("r7", { domain: "other.example.com", code: 303 as unknown as 301 }),
        ],
      },
    });
    expect(editorErrors(s)).toEqual({
      "redirect-host:r1": "Add late.example.com as an address or a whole-domain redirect above this rule first.",
      "redirect-to:r3": "Send it to a full address starting with http:// or https://.",
      "redirect-to:r4": "The target can't contain { or }.",
      "redirect-path:r5": "The path must start with /.",
      "redirect-path:r6": "The path can't contain { or }.",
      "redirect-to:r7": "Pick 301, 302, 307 or 308.",
    });
  });
});

describe("pruneBlankAddresses", () => {
  it("drops untouched rows and the ingress they leave empty", () => {
    const blank = { ...newAddressRow(), id: "d1" };
    const s = withConfig({ ingress: { domains: [blank], redirects: [] } });
    expect(pruneBlankAddresses(s).config).not.toHaveProperty("ingress", expect.anything());
    expect(pruneBlankAddresses(s).config.ingress).toBeUndefined();
    expect(blank).toMatchObject({ domain: "", upstream_port: "", ssl: true });
  });

  it("keeps a row with anything in it", () => {
    const s = withConfig({
      ingress: {
        domains: [
          { id: "d1", domain: "", upstream_port: "", ssl: true },
          { id: "d2", domain: "", upstream_port: 8080, ssl: true },
        ],
        redirects: [{ id: "r1", domain: "", path: "", to: "https://x.example.com", code: 301, ssl: true }],
      },
    });
    const out = pruneBlankAddresses(s).config.ingress;
    expect(out?.domains.map((d) => d.id)).toEqual(["d2"]);
    expect(out?.redirects.map((r) => r.id)).toEqual(["r1"]);
  });

  it("leaves a state without ingress as it is", () => {
    const s = withConfig({});
    expect(pruneBlankAddresses(s)).toBe(s);
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/field-errors.test.ts src/components/form-field.test.tsx src/lib/app-editor-io.test.ts`
Expected: FAIL (`@/lib/field-errors` and `@/components/form-field` don't exist; `editorErrors` is not exported).

- [ ] **Step 3: Write the implementation**

Create `web/src/lib/field-errors.ts`:

```ts
import { cn } from "@/lib/utils";

// FieldErrors are a form's problems by field key (see editorErrors for the
// app editor's keys). A key is present only with a message.
export type FieldErrors = Record<string, string>;

// errorId is the id of the message under the field with this id.
export function errorId(id: string): string {
  return `${id}-error`;
}

// invalidAttrs marks a field invalid (red border, described by its message)
// when message is set; the vendored inputs don't style aria-invalid
// themselves. Spread it last on the input: it carries the className.
export function invalidAttrs(id: string, message: string | undefined, className?: string) {
  return {
    "aria-invalid": message ? (true as const) : undefined,
    "aria-describedby": message ? errorId(id) : undefined,
    className: cn(className, message && "border-destructive focus-visible:ring-destructive"),
  };
}

// hasErrorIn says whether any error's key equals or starts with one of
// prefixes (a disclosure holding those fields must open).
export function hasErrorIn(errors: FieldErrors, ...prefixes: string[]): boolean {
  return Object.keys(errors).some((k) => prefixes.some((p) => k === p || k.startsWith(p)));
}

// focusFirstInvalid moves focus to the first field marked invalid inside
// root, one frame later: after React painted the messages and opened any
// disclosure holding one.
export function focusFirstInvalid(root: HTMLElement | null): void {
  requestAnimationFrame(() => {
    const el = root?.querySelector<HTMLElement>('[aria-invalid="true"]');
    if (!el) return;
    el.focus();
    el.scrollIntoView?.({ block: "center" });
  });
}
```

Create `web/src/components/form-field.tsx`:

```tsx
import type { ReactNode } from "react";

import { Label } from "@/components/ui/label";
import { errorId } from "@/lib/field-errors";
import { cn } from "@/lib/utils";

// FormField is one labelled field: the label, the control (children, which
// spread invalidAttrs(id, error)), and under it the field's message, or
// its hint while the field is fine.
export function FormField({
  id,
  label,
  hint,
  error,
  className,
  children,
}: {
  id: string;
  label: ReactNode;
  hint?: ReactNode;
  error?: string;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={cn("grid gap-1.5", className)}>
      <Label htmlFor={id}>{label}</Label>
      {children}
      {error ? (
        <FieldMessage id={id} message={error} />
      ) : hint ? (
        <p className="text-xs text-muted-foreground">{hint}</p>
      ) : null}
    </div>
  );
}

// FieldMessage is the message under the field with this id (nothing when
// the field is fine).
export function FieldMessage({ id, message, className }: { id: string; message?: string; className?: string }) {
  if (!message) return null;
  return (
    <p id={errorId(id)} className={cn("text-xs text-destructive", className)}>
      {message}
    </p>
  );
}
```

In `web/src/types/app-config.ts`, after `COMPOSE_FILENAMES`, add:

```ts
// isComposeFile says whether a file name is one compose finds by itself.
export function isComposeFile(name: string): boolean {
  return COMPOSE_FILENAMES.includes(name.trim());
}
```

In `web/src/lib/app-editor-io.ts`:

1. Change the imports to:

```ts
import { validHostname } from "@/lib/domains";
import { ENCRYPTED, encryptSecret } from "@/lib/ecies";
import type { FieldErrors } from "@/lib/field-errors";
import type { AppDetailPayload } from "@/context/apps-context-base";
import {
  DEFAULT_COMPOSE,
  isComposeFile,
  localId,
  type AppEditorState,
  type AppSourceConfig,
  type IngressDomain,
  type IngressRedirect,
} from "@/types/app-config";
```

2. Replace the whole `validateEditorState` function with:

```ts
// newGitSource is the Git source a newly Git-deployed app starts with.
export function newGitSource(): AppSourceConfig {
  return { repo_url: "", branch: "main", compose_path: "", auto_update: true, poll_seconds: 120 };
}

// newAddressRow is a blank address (hostname → port) row.
export function newAddressRow(): IngressDomain {
  return { id: localId("dom"), domain: "", upstream_port: "", ssl: true };
}

// pruneBlankAddresses drops the address and redirect rows a create form
// left untouched (its one Address row starts blank), and the ingress itself
// when nothing is left, so an app created without an address saves exactly
// as before (no ingress key). Only the create forms use it: an existing
// app's empty ingress is saved as it was loaded.
export function pruneBlankAddresses(state: AppEditorState): AppEditorState {
  const ing = state.config.ingress;
  if (!ing) return state;
  const domains = ing.domains.filter((d) => d.domain.trim() !== "" || d.upstream_port !== "");
  const redirects = ing.redirects.filter((r) => r.domain.trim() !== "" || r.path.trim() !== "" || r.to.trim() !== "");
  const ingress = domains.length > 0 || redirects.length > 0 ? { domains, redirects } : undefined;
  return { ...state, config: { ...state.config, ingress } };
}

const REDIRECT_CODES = new Set([301, 302, 307, 308]);

// redirectTargetError checks a redirect's target and code. Go's net/url
// leaves Host empty for forms like "http:/foo" or "http:///foo" and
// Validate() rejects them, but WHATWG new URL() normalizes them to a
// non-empty host — so the raw value must also be scheme://non-slash, and
// may not hold whitespace (WHATWG strips embedded tab/newline; Go's
// url.Parse rejects them). The brace check mirrors
// internal/domain/model/ingress.go Validate(): Caddy expands {env.*} and
// {http.request.*} placeholders in the Location header at request time, so
// braces could leak secrets to an attacker-controlled target. The Go check
// is the security boundary; this is faster feedback.
function redirectTargetError(r: IngressRedirect): string {
  const to = r.to.trim();
  let target: URL | null = null;
  try {
    target = new URL(r.to);
  } catch {
    target = null;
  }
  if (
    !target ||
    (target.protocol !== "http:" && target.protocol !== "https:") ||
    target.host === "" ||
    !/^https?:\/\/[^/]/i.test(to) ||
    /\s/.test(to)
  )
    return "Send it to a full address starting with http:// or https://.";
  if (/[{}]/.test(r.to)) return "The target can't contain { or }.";
  if (!REDIRECT_CODES.has(r.code)) return "Pick 301, 302, 307 or 308.";
  return "";
}

// editorErrors names every problem in the editor's state by field (see the
// key table in the UX rebuild phase 3 plan): name, repo_url, branch, files,
// file:<id>, var:<id>, domain:<id>, port:<id>, redirect-host:<id>,
// redirect-path:<id>, redirect-to:<id>. Ingress rows are read in the order
// the server's Validate reads them: addresses, then redirects, each
// claiming its hostname; a path rule needs a hostname claimed above it.
export function editorErrors(state: AppEditorState): FieldErrors {
  const out: FieldErrors = {};
  const { config } = state;
  if (!config.name.trim()) out.name = "Give the app a name.";
  const src = config.source;
  if (src) {
    if (!src.repo_url.trim()) out.repo_url = "Enter the repository's URL.";
    if (!src.branch.trim()) out.branch = "Enter a branch.";
  }
  // A Git-deployed app may take its compose file from the repository.
  if (!src && !config.files.some((f) => isComposeFile(f.filename))) out.files = "Add a compose file named compose.yml.";
  for (const f of config.files) if (!f.filename.trim()) out[`file:${f.id}`] = "Name this file.";
  for (const v of config.variables) if (!v.name.trim()) out[`var:${v.id}`] = "Name this variable.";

  const ing = config.ingress;
  if (!ing) return out;
  const seen = new Set<string>();
  const claim = (host: string): string => {
    if (!validHostname(host)) return host ? "Use a lowercase hostname like app.example.com." : "Enter a hostname.";
    if (seen.has(host)) return "This hostname is already used above.";
    seen.add(host);
    return "";
  };
  for (const d of ing.domains) {
    const host = claim(d.domain);
    if (host) out[`domain:${d.id}`] = host;
    const port = Number(d.upstream_port);
    if (d.upstream_port === "" || !Number.isInteger(port) || port < 1 || port > 65535)
      out[`port:${d.id}`] = "Enter a port from 1 to 65535.";
  }
  for (const r of ing.redirects) {
    const to = redirectTargetError(r);
    if (to) out[`redirect-to:${r.id}`] = to;
    if (/[{}]/.test(r.path)) out[`redirect-path:${r.id}`] = "The path can't contain { or }.";
    else if (r.path !== "" && !r.path.startsWith("/")) out[`redirect-path:${r.id}`] = "The path must start with /.";
    const host =
      r.path === ""
        ? claim(r.domain)
        : seen.has(r.domain)
          ? ""
          : `Add ${r.domain || "this hostname"} as an address or a whole-domain redirect above this rule first.`;
    if (host) out[`redirect-host:${r.id}`] = host;
  }
  return out;
}

// validateEditorState is editorErrors' first message (null when none).
// Removed once every caller shows messages by field.
export function validateEditorState(state: AppEditorState): string | null {
  return Object.values(editorErrors(state))[0] ?? null;
}
```

Note the deliberate widening: a compose file named `compose.yaml` or `docker-compose.yaml` is now accepted (the agent's `model.ComposeFileNames` finds all four); the old check refused them.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/field-errors.test.ts src/components/form-field.test.tsx src/lib/app-editor-io.test.ts`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/field-errors.ts web/src/lib/field-errors.test.ts web/src/components/form-field.tsx web/src/components/form-field.test.tsx web/src/lib/app-editor-io.ts web/src/lib/app-editor-io.test.ts web/src/types/app-config.ts
git commit -m "$(cat <<'EOF'
feat(web): field-level validation for the app editor

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Files and variables as shared pieces with field messages

**Files:**
- Create: `web/src/lib/editor-rows.ts`, `web/src/lib/editor-rows.test.ts`
- Create: `web/src/lib/compose-images.ts`, `web/src/lib/compose-images.test.ts`
- Create: `web/src/components/variables-editor.tsx`, `web/src/components/variables-editor.test.tsx`
- Modify: `web/src/components/files-section.tsx` (`FileEditors`, exported `ImageChips`, `errors`)
- Modify: `web/src/components/files-section.test.tsx` (new cases)
- Modify: `web/src/components/app-editor.tsx` (variables through `VariablesEditor`; `errors` prop passed down)

**Interfaces:**
- Consumes: `FieldErrors`, `invalidAttrs`, `FieldMessage` (Task 1); `isComposeFile` (Task 1).
- Produces:
  - `addFile(files: AppFileMeta[], contents: Record<string, string>): { files; contents }`, `removeFile(files, contents, id): { files; contents }`, `addVariable(state: AppEditorState): AppEditorState`, `removeVariable(state, id): AppEditorState` (`@/lib/editor-rows`)
  - `imageRefs(content: string): string[]` (`@/lib/compose-images`)
  - `FileEditors({ files, contents, onChange, allowSecret? = true, imageChips? = true, errors? = {}, addLabel? })` — the file blocks; with `addLabel` a link-style button under them adds a file. `FilesSection` gains `errors?` and shows `errors.files` in place of its empty note. `ImageChips({ content, onReplace })` is exported. (`@/components/files-section`)
  - `VariablesEditor({ state, onChange, shared, errors? = {}, emptyNote? = true })` — rows, then an outline "Add variable" button (`@/components/variables-editor`)
  - `AppEditor({ state, onChange, errors? })`

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/editor-rows.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import { emptyEditorState } from "@/lib/app-editor-io";
import { addFile, addVariable, removeFile, removeVariable } from "@/lib/editor-rows";

describe("editor rows", () => {
  it("adds and removes a file with its content", () => {
    const files = [{ id: "f1", filename: "compose.yml", is_encrypted: false }];
    const added = addFile(files, { f1: "x" });
    expect(added.files).toHaveLength(2);
    const id = added.files[1].id;
    expect(added.files[1]).toMatchObject({ filename: "", is_encrypted: false });
    expect(added.contents).toEqual({ f1: "x", [id]: "" });
    expect(removeFile(added.files, added.contents, "f1")).toEqual({ files: [added.files[1]], contents: { [id]: "" } });
  });

  it("adds and removes a variable with its value", () => {
    const s = addVariable(emptyEditorState());
    const [v] = s.config.variables;
    expect(v).toMatchObject({ name: "", is_encrypted: false });
    expect(s.variables).toEqual({ [v.id]: "" });
    const back = removeVariable(s, v.id);
    expect(back.config.variables).toEqual([]);
    expect(back.variables).toEqual({});
  });
});
```

Create `web/src/lib/compose-images.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import { imageRefs } from "@/lib/compose-images";

describe("imageRefs", () => {
  it("lists each image a compose file names, once", () => {
    const content = `services:
  web:
    image: nginx:alpine
  db:
    image: "postgres:16"
  again:
    image: nginx:alpine
  # image: commented:1
`;
    // A commented-out line doesn't start with whitespace then image:.
    expect(imageRefs(content)).toEqual(["nginx:alpine", "postgres:16"]);
    expect(imageRefs("services: {}\n")).toEqual([]);
  });
});
```

Create `web/src/components/variables-editor.test.tsx`:

```tsx
import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { VariablesEditor } from "@/components/variables-editor";
import { emptyEditorState } from "@/lib/app-editor-io";
import type { FieldErrors } from "@/lib/field-errors";
import type { AppEditorState } from "@/types/app-config";

function Harness({ errors = {}, emptyNote }: { errors?: FieldErrors; emptyNote?: boolean }) {
  const [state, setState] = useState<AppEditorState>(emptyEditorState);
  return (
    <>
      <VariablesEditor
        state={state}
        onChange={setState}
        shared={[{ name: "SMTP_HOST", scope: "org" }]}
        errors={errors}
        emptyNote={emptyNote}
      />
      <output aria-label="state">{JSON.stringify({ vars: state.config.variables, values: state.variables })}</output>
    </>
  );
}
const state = () => JSON.parse(screen.getByRole("status", { name: "state" }).textContent ?? "{}");

describe("VariablesEditor", () => {
  it("adds, fills, marks secret, reveals and removes a variable", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    expect(screen.getByText("No variables yet.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Add variable/ }));
    await user.type(screen.getByRole("textbox", { name: "Variable name" }), "SMTP_HOST");
    expect(screen.getByText("overrides org value")).toBeInTheDocument();
    await user.type(screen.getByRole("textbox", { name: "Value of SMTP_HOST" }), "mail");
    await user.click(screen.getByRole("button", { name: /Secret/ }));
    expect(screen.getByLabelText("Value of SMTP_HOST")).toHaveAttribute("type", "password");
    await user.click(screen.getByRole("button", { name: "Show value" }));
    expect(screen.getByLabelText("Value of SMTP_HOST")).toHaveAttribute("type", "text");
    expect(state().vars[0]).toMatchObject({ name: "SMTP_HOST", is_encrypted: true });
    expect(Object.values(state().values)).toEqual(["mail"]);
    await user.click(screen.getByRole("button", { name: "Remove SMTP_HOST" }));
    expect(state().vars).toEqual([]);
  });

  it("shows a variable's message under it", async () => {
    const user = userEvent.setup();
    const { rerender } = render(<Harness />);
    await user.click(screen.getByRole("button", { name: /Add variable/ }));
    const id = state().vars[0].id as string;
    rerender(<Harness errors={{ [`var:${id}`]: "Name this variable." }} />);
    const name = screen.getByRole("textbox", { name: "Variable name" });
    expect(name).toHaveAttribute("aria-invalid", "true");
    expect(name).toHaveAccessibleDescription("Name this variable.");
  });

  it("can leave the empty note to the caller", () => {
    render(<Harness emptyNote={false} />);
    expect(screen.queryByText("No variables yet.")).not.toBeInTheDocument();
  });
});
```

Note: the `rerender` keeps the `Harness`'s state (same component type), so the added variable survives.

Append to `web/src/components/files-section.test.tsx` (add `FileEditors` to the import from `@/components/files-section`; extend `Harness` with an `errors` prop passed through to `FilesSection`):

Change `function Harness({ allowSecret, imageChips }: { allowSecret?: boolean; imageChips?: boolean })` to `function Harness({ allowSecret, imageChips, errors }: { allowSecret?: boolean; imageChips?: boolean; errors?: Record<string, string> })` and add `errors={errors}` to the `<FilesSection …>` props. Then append:

```tsx
describe("FilesSection messages", () => {
  it("marks a file's name and says why the list is refused", () => {
    render(<Harness errors={{ "file:f1": "Name this file.", files: "Add a compose file named compose.yml." }} />);
    const name = screen.getByRole("textbox", { name: "File name" });
    expect(name).toHaveAttribute("aria-invalid", "true");
    expect(name).toHaveAccessibleDescription("Name this file.");
    expect(screen.getByText("Add a compose file named compose.yml.")).toBeInTheDocument();
  });
});

describe("FileEditors", () => {
  it("adds a file from its link and leaves out the image chips on request", async () => {
    const user = userEvent.setup();
    function Editors() {
      const [files, setFiles] = useState<AppFileMeta[]>([{ id: "f1", filename: "compose.yml", is_encrypted: false }]);
      const [contents, setContents] = useState<Record<string, string>>({ f1: "services:\n  app:\n    image: nginx:1\n" });
      return (
        <FileEditors
          files={files}
          contents={contents}
          onChange={(f, c) => {
            setFiles(f);
            setContents(c);
          }}
          imageChips={false}
          addLabel="Add another file"
        />
      );
    }
    render(<Editors />);
    expect(screen.queryByRole("button", { name: /tags of/ })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Add another file" }));
    expect(screen.getAllByRole("textbox", { name: "File name" })).toHaveLength(2);
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/editor-rows.test.ts src/lib/compose-images.test.ts src/components/variables-editor.test.tsx src/components/files-section.test.tsx`
Expected: FAIL (modules and `FileEditors` missing).

- [ ] **Step 3: Write the implementation**

Create `web/src/lib/editor-rows.ts`:

```ts
// Adding and removing files and variables in the app editor's state: a row
// and its content (keyed by the row's local id) always move together.
import { localId, type AppEditorState, type AppFileMeta } from "@/types/app-config";

type Files = { files: AppFileMeta[]; contents: Record<string, string> };

export function addFile(files: AppFileMeta[], contents: Record<string, string>): Files {
  const id = localId("file");
  return { files: [...files, { id, filename: "", is_encrypted: false }], contents: { ...contents, [id]: "" } };
}

export function removeFile(files: AppFileMeta[], contents: Record<string, string>, id: string): Files {
  const rest = { ...contents };
  delete rest[id];
  return { files: files.filter((f) => f.id !== id), contents: rest };
}

export function addVariable(state: AppEditorState): AppEditorState {
  const id = localId("var");
  return {
    ...state,
    config: { ...state.config, variables: [...state.config.variables, { id, name: "", is_encrypted: false }] },
    variables: { ...state.variables, [id]: "" },
  };
}

export function removeVariable(state: AppEditorState, id: string): AppEditorState {
  const rest = { ...state.variables };
  delete rest[id];
  return {
    ...state,
    config: { ...state.config, variables: state.config.variables.filter((v) => v.id !== id) },
    variables: rest,
  };
}
```

Create `web/src/lib/compose-images.ts`:

```ts
// IMAGE_LINE finds `image:` lines in a compose file (global: matchAll only).
const IMAGE_LINE = /^\s*image:\s*["']?([\w][\w./:@-]*)/gm;

// imageRefs are the image references a compose file names, each once, in
// file order.
export function imageRefs(content: string): string[] {
  return Array.from(new Set(Array.from(content.matchAll(IMAGE_LINE), (m) => m[1]).filter(Boolean)));
}
```

Replace `web/src/components/files-section.tsx` with:

```tsx
import type { ReactNode } from "react";
import { FileText, Lock, LockOpen, Plus, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { CodeEditor } from "@/components/code-editor";
import { EditorSection } from "@/components/editor-section";
import { FieldMessage } from "@/components/form-field";
import { ImageTagPicker } from "@/components/image-tag-picker";
import { EmptyNote } from "@/components/page-states";
import { imageRefs } from "@/lib/compose-images";
import { addFile, removeFile } from "@/lib/editor-rows";
import { invalidAttrs, type FieldErrors } from "@/lib/field-errors";
import { cn } from "@/lib/utils";
import { isComposeFile, type AppFileMeta } from "@/types/app-config";

type FileEditorsProps = {
  files: AppFileMeta[];
  contents: Record<string, string>; // file id -> content
  onChange: (files: AppFileMeta[], contents: Record<string, string>) => void;
  // allowSecret offers the per-file secret toggle (an app's files; a
  // template holds no secret).
  allowSecret?: boolean;
  // imageChips offers the image tag browser under compose files (it asks
  // the active server's registries).
  imageChips?: boolean;
  // errors holds file:<id> messages (editorErrors).
  errors?: FieldErrors;
  // addLabel shows a link-style button under the files that adds one.
  addLabel?: string;
};

// FileEditors are the app's files, each a name over a code editor. The
// files section, the compose create form and the template editor share it.
export function FileEditors({
  files,
  contents,
  onChange,
  allowSecret = true,
  imageChips = true,
  errors = {},
  addLabel,
}: FileEditorsProps) {
  const updateMeta = (id: string, patch: Partial<AppFileMeta>) =>
    onChange(
      files.map((f) => (f.id === id ? { ...f, ...patch } : f)),
      contents,
    );
  const updateContent = (id: string, content: string) => onChange(files, { ...contents, [id]: content });
  const add = () => {
    const next = addFile(files, contents);
    onChange(next.files, next.contents);
  };
  const remove = (id: string) => {
    const next = removeFile(files, contents, id);
    onChange(next.files, next.contents);
  };

  return (
    <div className="space-y-3">
      {files.map((f) => {
        const nameId = `file-${f.id}`;
        const error = errors[`file:${f.id}`];
        return (
          <div key={f.id} className="overflow-hidden rounded-md border">
            <div className="flex items-center gap-1 border-b bg-muted/40 py-1 pr-1 pl-1">
              <FileText className="ml-2 size-4 shrink-0 text-muted-foreground" />
              <Input
                id={nameId}
                aria-label="File name"
                value={f.filename}
                placeholder="compose.yml"
                {...invalidAttrs(
                  nameId,
                  error,
                  "h-8 border-transparent bg-transparent font-mono shadow-none focus-visible:border-input focus-visible:bg-background",
                )}
                onChange={(e) => updateMeta(f.id, { filename: e.target.value })}
              />
              {allowSecret ? (
                <SecretToggle on={f.is_encrypted} onChange={(on) => updateMeta(f.id, { is_encrypted: on })} />
              ) : null}
              <Button
                size="icon"
                variant="ghost"
                className="size-8 shrink-0 text-muted-foreground hover:text-destructive"
                aria-label={`Remove ${f.filename || "file"}`}
                onClick={() => remove(f.id)}
              >
                <Trash2 className="size-4" />
              </Button>
            </div>
            <FieldMessage id={nameId} message={error} className="border-b px-3 py-1.5" />
            <CodeEditor
              value={contents[f.id] ?? ""}
              onChange={(content) => updateContent(f.id, content)}
              filename={f.filename}
              placeholder="file contents (use ${VAR}, including org/server variables)"
              className="rounded-none border-0"
            />
            {imageChips && isComposeFile(f.filename) ? (
              <ImageChips
                content={contents[f.id] ?? ""}
                onReplace={(oldRef, newRef) => updateContent(f.id, (contents[f.id] ?? "").split(oldRef).join(newRef))}
              />
            ) : null}
          </div>
        );
      })}
      {addLabel ? (
        <Button type="button" variant="link" size="sm" className="h-auto px-0" onClick={add}>
          <Plus className="size-4" /> {addLabel}
        </Button>
      ) : null}
    </div>
  );
}

type Props = Omit<FileEditorsProps, "addLabel"> & { description: ReactNode };

// FilesSection is the editor row of files: an explanation, "Add file", and
// the files. errors.files (no compose file) stands in for the empty note.
export function FilesSection({ files, contents, onChange, description, allowSecret = true, imageChips = true, errors = {} }: Props) {
  const add = () => {
    const next = addFile(files, contents);
    onChange(next.files, next.contents);
  };
  return (
    <EditorSection
      title="Files"
      description={description}
      action={
        <Button size="sm" variant="outline" onClick={add}>
          <Plus className="size-4" /> Add file
        </Button>
      }
    >
      {errors.files ? (
        <FieldMessage id="files" message={errors.files} className="text-sm" />
      ) : files.length === 0 ? (
        <EmptyNote>
          Add at least a <code>compose.yml</code>.
        </EmptyNote>
      ) : null}
      {files.length > 0 ? (
        <FileEditors
          files={files}
          contents={contents}
          onChange={onChange}
          allowSecret={allowSecret}
          imageChips={imageChips}
          errors={errors}
        />
      ) : null}
    </EditorSection>
  );
}

// SecretToggle marks a file or variable as secret (encrypted end to end).
export function SecretToggle({ on, onChange }: { on: boolean; onChange: (on: boolean) => void }) {
  return (
    <Button
      type="button"
      size="sm"
      variant="ghost"
      aria-pressed={on}
      title={on ? "Secret: encrypted for this server" : "Mark as secret"}
      className={cn(
        "h-8 shrink-0 gap-1.5 px-2 text-xs",
        on
          ? "bg-amber-500/10 text-amber-700 hover:bg-amber-500/15 hover:text-amber-800 dark:text-amber-400"
          : "text-muted-foreground",
      )}
      onClick={() => onChange(!on)}
    >
      {on ? <Lock className="size-3.5" /> : <LockOpen className="size-3.5" />}
      <span className="sr-only sm:not-sr-only">Secret</span>
    </Button>
  );
}

// ImageChips lists the image references found in a compose file, each with
// a tag browser that rewrites the reference in place.
export function ImageChips({ content, onReplace }: { content: string; onReplace: (oldRef: string, newRef: string) => void }) {
  const refs = imageRefs(content);
  if (refs.length === 0) return null;
  return (
    <div className="flex flex-wrap items-center gap-2 border-t bg-muted/20 px-3 py-2">
      <span className="text-xs text-muted-foreground">Images</span>
      {refs.map((ref) => (
        <ImageTagPicker key={ref} image={ref} onSelect={(newRef) => onReplace(ref, newRef)} />
      ))}
    </div>
  );
}
```

(The `SecretToggle` amber classes are the existing ones, unchanged: a secret marker, not a status colour.)

Create `web/src/components/variables-editor.tsx`:

```tsx
import { useState } from "react";
import { Eye, EyeOff, Plus, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SecretToggle } from "@/components/files-section";
import { FieldMessage } from "@/components/form-field";
import { EmptyNote } from "@/components/page-states";
import { addVariable, removeVariable } from "@/lib/editor-rows";
import { invalidAttrs, type FieldErrors } from "@/lib/field-errors";
import { overrideNote, type SharedName } from "@/lib/shared-variables";
import type { AppEditorState, AppVariableMeta } from "@/types/app-config";

type Props = {
  state: AppEditorState;
  onChange: (next: AppEditorState) => void;
  // shared are the org/server variables an app variable may override.
  shared: SharedName[];
  // errors holds var:<id> messages (editorErrors).
  errors?: FieldErrors;
  // emptyNote says "No variables yet." when there are none.
  emptyNote?: boolean;
};

// VariablesEditor edits an app's variables: name, value (masked while
// secret), the secret toggle and removal, then "Add variable". Secret values
// are encrypted by the save, never here.
export function VariablesEditor({ state, onChange, shared, errors = {}, emptyNote = true }: Props) {
  const [revealed, setRevealed] = useState<Record<string, boolean>>({});
  const vars = state.config.variables;
  const updateMeta = (id: string, patch: Partial<AppVariableMeta>) =>
    onChange({
      ...state,
      config: { ...state.config, variables: vars.map((v) => (v.id === id ? { ...v, ...patch } : v)) },
    });
  const updateValue = (id: string, value: string) => onChange({ ...state, variables: { ...state.variables, [id]: value } });

  return (
    <div className="space-y-3">
      {vars.length === 0 ? (
        emptyNote ? (
          <EmptyNote>No variables yet.</EmptyNote>
        ) : null
      ) : (
        <div className="divide-y rounded-md border">
          {vars.map((v) => {
            const isRevealed = revealed[v.id] || !v.is_encrypted;
            const note = overrideNote(v.name, shared);
            const nameId = `var-${v.id}`;
            const error = errors[`var:${v.id}`];
            return (
              <div key={v.id} className="p-1">
                <div className="flex items-center gap-1">
                  <Input
                    id={nameId}
                    aria-label="Variable name"
                    value={v.name}
                    placeholder="VAR_NAME"
                    {...invalidAttrs(
                      nameId,
                      error,
                      "h-8 w-32 shrink-0 border-transparent font-mono shadow-none focus-visible:border-input sm:w-56",
                    )}
                    onChange={(e) => updateMeta(v.id, { name: e.target.value })}
                  />
                  {note ? <span className="shrink-0 text-xs text-muted-foreground">{note}</span> : null}
                  <Input
                    aria-label={`Value of ${v.name || "variable"}`}
                    value={state.variables[v.id] ?? ""}
                    type={isRevealed ? "text" : "password"}
                    placeholder="value"
                    className="h-8 border-transparent font-mono shadow-none focus-visible:border-input"
                    onChange={(e) => updateValue(v.id, e.target.value)}
                  />
                  {v.is_encrypted ? (
                    <Button
                      size="icon"
                      variant="ghost"
                      className="size-8 shrink-0 text-muted-foreground"
                      aria-label={isRevealed ? "Hide value" : "Show value"}
                      onClick={() => setRevealed((r) => ({ ...r, [v.id]: !r[v.id] }))}
                    >
                      {isRevealed ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                    </Button>
                  ) : null}
                  <SecretToggle on={v.is_encrypted} onChange={(on) => updateMeta(v.id, { is_encrypted: on })} />
                  <Button
                    size="icon"
                    variant="ghost"
                    className="size-8 shrink-0 text-muted-foreground hover:text-destructive"
                    aria-label={`Remove ${v.name || "variable"}`}
                    onClick={() => onChange(removeVariable(state, v.id))}
                  >
                    <Trash2 className="size-4" />
                  </Button>
                </div>
                <FieldMessage id={nameId} message={error} className="px-2 pt-1 pb-1" />
              </div>
            );
          })}
        </div>
      )}
      <Button size="sm" variant="outline" onClick={() => onChange(addVariable(state))}>
        <Plus className="size-4" /> Add variable
      </Button>
    </div>
  );
}
```

In `web/src/components/app-editor.tsx`:

1. Props: `type Props = { state: AppEditorState; onChange: (next: AppEditorState) => void; errors?: FieldErrors };` and the signature `export function AppEditor({ state, onChange, errors = {} }: Props)`; import `type FieldErrors` from `@/lib/field-errors` and `VariablesEditor` from `@/components/variables-editor`.
2. Delete `revealed` state, `updateVarMeta`, `updateVarValue`, `addVar`, `removeVar`.
3. Pass `errors={errors}` to `<FilesSection …>`.
4. Replace the Variables `EditorSection` (its `action` and the whole `state.config.variables.length === 0 ? … : …` block) with:

```tsx
      <EditorSection
        title="Variables"
        description={
          <>
            Substituted into files as <code>{"${NAME}"}</code>, including org and
            server variables. Secret values are encrypted in your browser.
          </>
        }
      >
        <VariablesEditor
          state={state}
          onChange={onChange}
          shared={shared}
          errors={errors}
          emptyNote={state.storageVariables.length === 0}
        />
        <SharedVariablesHint shared={shared} />
        <StorageVariableRows vars={state.storageVariables} />
      </EditorSection>
```

5. Remove the now-unused imports (`Eye`, `EyeOff`, `Plus`, `Trash2`, `SecretToggle`, `overrideNote`, `useState` if unused, `localId`, `AppVariableMeta`) — `tsc -b` lists them.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/editor-rows.test.ts src/lib/compose-images.test.ts src/components/variables-editor.test.tsx src/components/files-section.test.tsx src/components/app-editor.test.tsx src/components/app-editor-panel.test.tsx src/components/template-editor.test.tsx`
Expected: PASS (the app editor, panel and template editor tests pass unchanged).

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/editor-rows.ts web/src/lib/editor-rows.test.ts web/src/lib/compose-images.ts web/src/lib/compose-images.test.ts web/src/components/variables-editor.tsx web/src/components/variables-editor.test.tsx web/src/components/files-section.tsx web/src/components/files-section.test.tsx web/src/components/app-editor.tsx
git commit -m "$(cat <<'EOF'
refactor(web): files and variables editors as shared pieces with field messages

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Addresses, redirects and the Git source as shared pieces

**Files:**
- Modify: `web/src/hooks/use-registrations.ts` (`enabled` flag)
- Create: `web/src/hooks/use-registrations.test.tsx`
- Modify: `web/src/components/hostname-field.tsx` (`error`, `footer`)
- Modify: `web/src/components/ingress-editor.tsx` (replace `IngressEditor` with `AddressRows` + `RedirectRows`)
- Modify: `web/src/components/ingress-editor.test.tsx` (harness, "Port" label, new cases)
- Create: `web/src/components/git-source-fields.tsx`, `web/src/components/git-source-fields.test.tsx`
- Modify: `web/src/components/app-editor.tsx` (ingress through the new rows; Git fields through `GitSourceFields`)

**Interfaces:**
- Consumes: `newAddressRow`, `newGitSource` (Task 1); `FieldErrors`, `invalidAttrs`, `FieldMessage`, `FormField` (Task 1); `Disclosure` (phase 1).
- Produces:
  - `useRegistrations(serverId: string, enabled = true): { registrations: RegisteredDomain[]; refresh: () => void }` — no request while `enabled` is false.
  - `HostnameField` gains `error?: string` (marks the input invalid, message under the row, before the live hints) and `footer?: ReactNode` (rendered last in the row's box).
  - `type IngressRowsProps = { state; onChange; appId?; serverId; registrations: RegisteredDomain[]; onRefresh: () => void; errors?: FieldErrors; addLabel?: string; emptyNote?: string | null }`; `AddressRows(props)` (hostname + Port rows, default `addLabel` "Add hostname", default `emptyNote` "Add a hostname to send web traffic to this app."), `RedirectRows(props)` (default `addLabel` "Add redirect", default `emptyNote` "Redirect a hostname (or a path on it) to another URL.") (`@/components/ingress-editor`). `IngressEditor` is removed.
  - `GitSourceFields({ source: AppSourceConfig; token: string; onSource: (patch: Partial<AppSourceConfig>) => void; onToken: (token: string) => void; errors?: FieldErrors })` (`@/components/git-source-fields`): Repository URL (`#src-url`), Branch (`#src-branch`); "Private repository?" disclosure (open when a token is stored or typed) with Access token (`#src-token`); "Advanced" disclosure with Compose file path (`#src-compose`) and the auto-redeploy checkbox.

- [ ] **Step 1: Write the failing tests**

Create `web/src/hooks/use-registrations.test.tsx`:

```tsx
import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useRegistrations } from "@/hooks/use-registrations";
import { fakeAPI } from "@/test/fake-api";

// fakeAPI stubs fetch globally.
afterEach(() => vi.unstubAllGlobals());

describe("useRegistrations", () => {
  it("reads the server's domains", async () => {
    const api = fakeAPI({ "/api/v1/domain/get-domains": { domains: [{ name: "example.com", tls: "auto" }] } });
    const { result } = renderHook(() => useRegistrations("s1"));
    await waitFor(() => expect(result.current.registrations).toEqual([{ name: "example.com", tls: "auto" }]));
    expect(api.calls[0].query).toBe("?server_id=s1");
  });

  it("asks nothing while disabled (a server without the built-in proxy)", async () => {
    const api = fakeAPI({ "/api/v1/domain/get-domains": { domains: [] } });
    renderHook(() => useRegistrations("s1", false));
    await new Promise((r) => setTimeout(r, 20));
    expect(api.calls).toEqual([]);
  });
});
```

Create `web/src/components/git-source-fields.test.tsx`:

```tsx
import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { GitSourceFields } from "@/components/git-source-fields";
import { newGitSource } from "@/lib/app-editor-io";
import type { FieldErrors } from "@/lib/field-errors";
import type { AppSourceConfig } from "@/types/app-config";

function Harness({ initial = newGitSource(), errors }: { initial?: AppSourceConfig; errors?: FieldErrors }) {
  const [source, setSource] = useState(initial);
  const [token, setToken] = useState("");
  return (
    <>
      <GitSourceFields
        source={source}
        token={token}
        onSource={(patch) => setSource((s) => ({ ...s, ...patch }))}
        onToken={setToken}
        errors={errors}
      />
      <output aria-label="state">{JSON.stringify({ source, token })}</output>
    </>
  );
}
const state = () => JSON.parse(screen.getByRole("status", { name: "state" }).textContent ?? "{}");

describe("GitSourceFields", () => {
  it("asks for the repository and branch, the rest out of the way", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    expect(screen.getByLabelText("Branch")).toHaveValue("main");
    expect(screen.queryByLabelText("Access token")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Compose file path")).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("Repository URL"), "https://github.com/acme/shop");
    await user.click(screen.getByRole("button", { name: "Private repository?" }));
    await user.type(screen.getByLabelText("Access token"), "tok");
    await user.click(screen.getByRole("button", { name: "Advanced" }));
    await user.type(screen.getByLabelText("Compose file path"), "deploy/compose.yml");
    await user.click(screen.getByRole("checkbox", { name: /Redeploy automatically/ }));
    expect(state()).toEqual({
      source: {
        repo_url: "https://github.com/acme/shop",
        branch: "main",
        compose_path: "deploy/compose.yml",
        auto_update: false,
        poll_seconds: 120,
      },
      token: "tok",
    });
  });

  it("opens the token field when one is stored, saying blank keeps it", () => {
    render(<Harness initial={{ ...newGitSource(), token_set: true }} />);
    expect(screen.getByLabelText("Access token")).toHaveAttribute("placeholder", "Stored, leave blank to keep");
  });

  it("shows the URL and branch messages under their fields", () => {
    render(<Harness errors={{ repo_url: "Enter the repository's URL.", branch: "Enter a branch." }} />);
    expect(screen.getByLabelText("Repository URL")).toHaveAccessibleDescription("Enter the repository's URL.");
    expect(screen.getByLabelText("Branch")).toHaveAttribute("aria-invalid", "true");
  });
});
```

In `web/src/components/ingress-editor.test.tsx`:

1. Change the imports: `import { AddressRows, RedirectRows } from "@/components/ingress-editor";` (instead of `IngressEditor`), add `import { useRegistrations } from "@/hooks/use-registrations";` and `import type { FieldErrors } from "@/lib/field-errors";`.
2. Replace `function Harness({ ingress }: { ingress: AppIngress })` and its body's `<IngressEditor … />` line with:

```tsx
function Harness({ ingress, errors }: { ingress: AppIngress; errors?: FieldErrors }) {
  const [state, setState] = useState<AppEditorState>({
    config: { id: "app-1", name: "x", files: [], variables: [], ingress },
    files: {},
    variables: {},
    storageVariables: [],
  });
  const { registrations, refresh } = useRegistrations("srv-1");
  const ing = state.config.ingress;
  const rows = { state, onChange: setState, appId: "app-1", serverId: "srv-1", registrations, onRefresh: refresh, errors };
  return (
    <MemoryRouter>
      <AddressRows {...rows} />
      <RedirectRows {...rows} />
      <output data-testid="ingress">{JSON.stringify(ing)}</output>
      <output data-testid="ssl">
        {JSON.stringify([...(ing?.domains ?? []), ...(ing?.redirects ?? [])].map((r) => [r.domain, r.ssl]))}
      </output>
    </MemoryRouter>
  );
}
```

3. In "adds and edits hostnames and redirects", replace both `screen.getByLabelText("Upstream port")` with `screen.getByLabelText("Port")`.
4. Append:

```tsx
describe("ingress rows: messages", () => {
  beforeEach(() => {
    registrations = [WILDCARD, EXACT, TUNNEL];
    checks = {};
    offline = false;
    vi.stubGlobal("fetch", fetchMock);
  });
  afterEach(() => vi.unstubAllGlobals());

  it("shows a hostname's and a port's message under their row", () => {
    render(
      <Harness
        ingress={oneDomain("Bad Host")}
        errors={{ "domain:d1": "Use a lowercase hostname like app.example.com.", "port:d1": "Enter a port from 1 to 65535." }}
      />,
    );
    const host = screen.getByLabelText("Hostname");
    expect(host).toHaveAttribute("aria-invalid", "true");
    expect(host).toHaveAccessibleDescription("Use a lowercase hostname like app.example.com.");
    expect(screen.getByLabelText("Port")).toHaveAccessibleDescription("Enter a port from 1 to 65535.");
  });

  it("shows a redirect's messages under its fields", () => {
    render(
      <Harness
        ingress={{
          domains: [],
          redirects: [{ id: "r1", domain: "x.tunnel.example.com", path: "old", to: "nope", code: 301, ssl: true }],
        }}
        errors={{
          "redirect-path:r1": "The path must start with /.",
          "redirect-to:r1": "Send it to a full address starting with http:// or https://.",
        }}
      />,
    );
    expect(screen.getByLabelText("Redirect path")).toHaveAccessibleDescription("The path must start with /.");
    expect(screen.getByLabelText("Redirect to")).toHaveAccessibleDescription(
      "Send it to a full address starting with http:// or https://.",
    );
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/hooks/use-registrations.test.tsx src/components/git-source-fields.test.tsx src/components/ingress-editor.test.tsx`
Expected: FAIL (`AddressRows`, `GitSourceFields` missing; the disabled hook still fetches).

- [ ] **Step 3: Write the implementation**

In `web/src/hooks/use-registrations.ts`: change the signature to `export function useRegistrations(serverId: string, enabled = true)`, make the effect start with `if (!enabled) return;`, add `enabled` to its dependency list (`[serverId, reloads, enabled]`), and extend the comment: "enabled false (a server without the built-in proxy) asks nothing."

In `web/src/components/hostname-field.tsx`:

1. Imports: add `import { FieldMessage } from "@/components/form-field";` and `errorId` to the `@/lib/field-errors` import (new import line `import { errorId } from "@/lib/field-errors";`).
2. Props: add

```ts
  // error is the hostname's own message (editorErrors): it marks the input
  // invalid and sits under the row, before the live check's hints.
  error?: string;
  // footer goes last in the row's box (the row's other messages).
  footer?: ReactNode;
```

and destructure `error, footer` in the function signature.
3. Replace the returned JSX with:

```tsx
  const hintId = `${rowId}-hint`;
  const describedBy = [error ? errorId(rowId) : "", issue || shown?.registration ? hintId : ""].filter(Boolean).join(" ");
  return (
    <div className="rounded-md border p-3">
      <div className={rowClassName}>
        <Input
          ref={input}
          value={value}
          placeholder={placeholder}
          aria-label={label}
          aria-invalid={issue !== undefined || Boolean(error)}
          aria-describedby={describedBy || undefined}
          className={cn(
            "font-mono",
            inputClassName,
            (issue || error) && "border-destructive focus-visible:ring-destructive",
          )}
          onChange={(e) => edit(e.target.value.trim().toLowerCase())}
        />
        {children}
      </div>
      <FieldMessage id={rowId} message={error} className="mt-2" />
      {!checked ? null : value === "" ? (
        <Suggestions registrations={registrations} onPick={pick} />
      ) : shown ? (
        <HostHints id={hintId} check={shown} />
      ) : null}
      {footer}
    </div>
  );
```

Replace `web/src/components/ingress-editor.tsx` with:

```tsx
import { Plus, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { FieldMessage } from "@/components/form-field";
import { HostnameField } from "@/components/hostname-field";
import { EmptyNote } from "@/components/page-states";
import { newAddressRow } from "@/lib/app-editor-io";
import type { RegisteredDomain } from "@/lib/domains";
import { invalidAttrs, type FieldErrors } from "@/lib/field-errors";
import { localId, type AppEditorState, type AppIngress, type IngressDomain, type IngressRedirect } from "@/types/app-config";

export type IngressRowsProps = {
  state: AppEditorState;
  onChange: (next: AppEditorState) => void;
  appId?: string;
  serverId: string;
  // registrations are the server's domains (useRegistrations), offered as
  // suggestions for an empty hostname; onRefresh reads them again.
  registrations: RegisteredDomain[];
  onRefresh: () => void;
  // errors holds domain:/port:/redirect-*: messages (editorErrors).
  errors?: FieldErrors;
  addLabel?: string;
  // emptyNote stands in for no rows (null says nothing).
  emptyNote?: string | null;
};

const emptyIngress = (): AppIngress => ({ domains: [], redirects: [] });

// legacySSL is the stored "ssl" flag for a hostname under reg. The proxy
// ignores it (the registration decides HTTPS); it only picks the link
// scheme on app cards.
const legacySSL = (reg: RegisteredDomain) => reg.tls !== "off";

const REDIRECT_CODES = [301, 302, 307, 308] as const;

function ingressOf(state: AppEditorState): AppIngress {
  return state.config.ingress ?? emptyIngress();
}

// AddressRows edits where the app answers: hostname → the port the compose
// file publishes on 127.0.0.1. Hostnames must sit under a domain registered
// on the server; HTTPS follows that registration (shown under the row).
export function AddressRows({
  state,
  onChange,
  appId,
  serverId,
  registrations,
  onRefresh,
  errors = {},
  addLabel = "Add hostname",
  emptyNote = "Add a hostname to send web traffic to this app.",
}: IngressRowsProps) {
  const ing = ingressOf(state);
  const setIngress = (next: AppIngress) => onChange({ ...state, config: { ...state.config, ingress: next } });
  const update = (id: string, patch: Partial<IngressDomain>) =>
    setIngress({ ...ing, domains: ing.domains.map((d) => (d.id === id ? { ...d, ...patch } : d)) });

  return (
    <div className="space-y-3">
      {ing.domains.length === 0 && emptyNote ? <EmptyNote>{emptyNote}</EmptyNote> : null}
      {ing.domains.map((d) => {
        const portId = `port-${d.id}`;
        const portError = errors[`port:${d.id}`];
        return (
          <HostnameField
            key={d.id}
            rowId={d.id}
            value={d.domain}
            label="Hostname"
            placeholder="app.example.com"
            // Phones put the hostname on its own row: it's the long,
            // important part, and the port fits beside the button.
            rowClassName="flex flex-wrap items-center gap-2 sm:flex-nowrap"
            inputClassName="basis-full sm:basis-auto"
            checked
            serverId={serverId}
            appId={appId}
            registrations={registrations}
            error={errors[`domain:${d.id}`]}
            footer={<FieldMessage id={portId} message={portError} className="mt-2" />}
            onChange={(domain) => update(d.id, { domain })}
            onRegistration={(reg) => {
              if (d.ssl !== legacySSL(reg)) update(d.id, { ssl: legacySSL(reg) });
            }}
            onReturn={onRefresh}
          >
            <Input
              id={portId}
              value={d.upstream_port}
              type="number"
              inputMode="numeric"
              placeholder="8088"
              aria-label="Port"
              title="The port the compose file publishes on 127.0.0.1"
              {...invalidAttrs(portId, portError, "w-28 shrink-0")}
              onChange={(e) => update(d.id, { upstream_port: e.target.value === "" ? "" : Number(e.target.value) })}
            />
            <Button
              size="icon"
              variant="ghost"
              className="ml-auto size-8 shrink-0 text-muted-foreground hover:text-destructive sm:ml-0"
              aria-label={`Remove ${d.domain || "hostname"}`}
              onClick={() => setIngress({ ...ing, domains: ing.domains.filter((x) => x.id !== d.id) })}
            >
              <Trash2 className="size-4" />
            </Button>
          </HostnameField>
        );
      })}
      {ing.domains.length > 0 ? (
        <p className="text-xs text-muted-foreground">
          The compose file must publish each port on 127.0.0.1, e.g. <code>127.0.0.1:8088:80</code>.
        </p>
      ) : null}
      <Button size="sm" variant="outline" onClick={() => setIngress({ ...ing, domains: [...ing.domains, newAddressRow()] })}>
        <Plus className="size-4" /> {addLabel}
      </Button>
    </div>
  );
}

// RedirectRows edits redirect rules: a hostname (a whole-domain redirect
// claims it), or a path on a hostname claimed above, sent to another URL.
export function RedirectRows({
  state,
  onChange,
  appId,
  serverId,
  registrations,
  onRefresh,
  errors = {},
  addLabel = "Add redirect",
  emptyNote = "Redirect a hostname (or a path on it) to another URL.",
}: IngressRowsProps) {
  const ing = ingressOf(state);
  const setIngress = (next: AppIngress) => onChange({ ...state, config: { ...state.config, ingress: next } });
  const update = (id: string, patch: Partial<IngressRedirect>) =>
    setIngress({ ...ing, redirects: ing.redirects.map((r) => (r.id === id ? { ...r, ...patch } : r)) });
  const add = () =>
    setIngress({
      ...ing,
      redirects: [...ing.redirects, { id: localId("red"), domain: "", path: "", to: "", code: 301, ssl: true }],
    });

  return (
    <div className="space-y-3">
      {ing.redirects.length === 0 && emptyNote ? <EmptyNote>{emptyNote}</EmptyNote> : null}
      {ing.redirects.map((r) => {
        const pathId = `redirect-path-${r.id}`;
        const toId = `redirect-to-${r.id}`;
        const pathError = errors[`redirect-path:${r.id}`];
        const toError = errors[`redirect-to:${r.id}`];
        return (
          <HostnameField
            key={r.id}
            rowId={r.id}
            value={r.domain}
            label="Redirect hostname"
            placeholder="old.example.com"
            rowClassName="flex flex-wrap items-center gap-2"
            inputClassName="sm:max-w-56"
            // Path rules ride a domain checked above; only a domain-level
            // redirect claims its hostname.
            checked={r.path === ""}
            serverId={serverId}
            appId={appId}
            registrations={registrations}
            error={errors[`redirect-host:${r.id}`]}
            footer={
              <>
                <FieldMessage id={pathId} message={pathError} className="mt-2" />
                <FieldMessage id={toId} message={toError} className="mt-2" />
              </>
            }
            onChange={(domain) => update(r.id, { domain })}
            onRegistration={(reg) => {
              if (r.ssl !== legacySSL(reg)) update(r.id, { ssl: legacySSL(reg) });
            }}
            onReturn={onRefresh}
          >
            <Input
              id={pathId}
              value={r.path}
              placeholder="/old-path/* (empty = whole domain)"
              aria-label="Redirect path"
              {...invalidAttrs(pathId, pathError, "font-mono sm:max-w-64")}
              onChange={(e) => update(r.id, { path: e.target.value })}
            />
            <Input
              id={toId}
              value={r.to}
              placeholder="https://new.example.com/new-path"
              aria-label="Redirect to"
              {...invalidAttrs(toId, toError, "flex-1 font-mono")}
              onChange={(e) => update(r.id, { to: e.target.value })}
            />
            <Select value={String(r.code)} onValueChange={(v) => update(r.id, { code: Number(v) as IngressRedirect["code"] })}>
              <SelectTrigger className="h-9 w-24 shrink-0" aria-label="Redirect code">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {REDIRECT_CODES.map((code) => (
                  <SelectItem key={code} value={String(code)}>
                    {code}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button
              size="icon"
              variant="ghost"
              className="size-8 shrink-0 text-muted-foreground hover:text-destructive"
              aria-label="Remove redirect"
              onClick={() => setIngress({ ...ing, redirects: ing.redirects.filter((x) => x.id !== r.id) })}
            >
              <Trash2 className="size-4" />
            </Button>
          </HostnameField>
        );
      })}
      <Button size="sm" variant="outline" onClick={add}>
        <Plus className="size-4" /> {addLabel}
      </Button>
    </div>
  );
}
```

Create `web/src/components/git-source-fields.tsx`:

```tsx
import { useState } from "react";

import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { PasswordInput } from "@/components/ui/password-input";
import { Disclosure } from "@/components/disclosure";
import { FormField } from "@/components/form-field";
import { invalidAttrs, type FieldErrors } from "@/lib/field-errors";
import type { AppSourceConfig } from "@/types/app-config";

type Props = {
  source: AppSourceConfig;
  // token is a new access token typed this session ("" keeps the stored one).
  token: string;
  onSource: (patch: Partial<AppSourceConfig>) => void;
  onToken: (token: string) => void;
  // errors holds repo_url and branch messages (editorErrors).
  errors?: FieldErrors;
};

// GitSourceFields are where a Git-deployed app comes from: the repository
// and branch up front, the token behind "Private repository?", the compose
// file path and auto-redeploy under "Advanced". The token is encrypted for
// the server by the save, never here.
export function GitSourceFields({ source, token, onSource, onToken, errors = {} }: Props) {
  const [privateOpen, setPrivateOpen] = useState(Boolean(source.token_set) || token !== "");
  return (
    <div className="grid gap-4">
      <FormField id="src-url" label="Repository URL" error={errors.repo_url}>
        <Input
          id="src-url"
          value={source.repo_url}
          placeholder="https://github.com/org/app"
          autoComplete="off"
          {...invalidAttrs("src-url", errors.repo_url, "font-mono")}
          onChange={(e) => onSource({ repo_url: e.target.value })}
        />
      </FormField>
      <FormField id="src-branch" label="Branch" error={errors.branch} className="sm:max-w-xs">
        <Input
          id="src-branch"
          value={source.branch}
          placeholder="main"
          autoComplete="off"
          {...invalidAttrs("src-branch", errors.branch, "font-mono")}
          onChange={(e) => onSource({ branch: e.target.value })}
        />
      </FormField>
      <Disclosure label="Private repository?" open={privateOpen} onOpenChange={setPrivateOpen}>
        <FormField
          id="src-token"
          label="Access token"
          hint="A token that can read the repository. It is encrypted in your browser for this server."
        >
          <PasswordInput
            id="src-token"
            value={token}
            autoComplete="new-password"
            placeholder={source.token_set ? "Stored, leave blank to keep" : "Paste a token"}
            onChange={(e) => onToken(e.target.value)}
          />
        </FormField>
      </Disclosure>
      <Disclosure label="Advanced">
        <FormField
          id="src-compose"
          label="Compose file path"
          hint="Inside the repository. Empty uses the compose file at its root."
        >
          <Input
            id="src-compose"
            value={source.compose_path || ""}
            placeholder="compose.yml"
            className="font-mono"
            onChange={(e) => onSource({ compose_path: e.target.value })}
          />
        </FormField>
        <label className="flex min-h-10 items-center gap-2 text-sm">
          <Checkbox checked={source.auto_update} onCheckedChange={(c) => onSource({ auto_update: c === true })} />
          Redeploy automatically when the branch gets new commits
        </label>
      </Disclosure>
    </div>
  );
}
```

In `web/src/components/app-editor.tsx` (interim layout; Task 9 reshapes it):

1. Imports: replace `IngressEditor` with `import { AddressRows, RedirectRows } from "@/components/ingress-editor";`, add `import { GitSourceFields } from "@/components/git-source-fields";`, `import { useRegistrations } from "@/hooks/use-registrations";`, `import { newGitSource } from "@/lib/app-editor-io";`; drop `Input`, `Checkbox`, `PasswordInput`, `Label` imports that become unused (keep `Label`/`Input` while the Details section still uses them).
2. After `const shared = useSharedNames(activeServer?.id);` add:

```tsx
  const ingressOn = Boolean(activeServer?.features?.ingress);
  const { registrations, refresh } = useRegistrations(activeServer?.id ?? "", ingressOn);
```

3. In the "Deploy from Git" switch, replace the inline default object with `source ?? newGitSource()`, and replace the whole `{source ? (<div className="grid gap-4 sm:grid-cols-2"> … </div>) : null}` block with:

```tsx
        {source ? (
          <>
            <GitSourceFields
              source={source}
              token={state.sourceToken || ""}
              onSource={setSource}
              onToken={(sourceToken) => onChange({ ...state, sourceToken })}
              errors={errors}
            />
            <p className="text-xs text-muted-foreground">
              No compose file in the repository? Add a root <code>compose.yml</code> under
              Files — the clone is available at <code>./source</code>.
            </p>
          </>
        ) : null}
```

4. Replace the `<IngressEditor … />` element (keep the surrounding `serversLoading ? null : activeServer?.features?.ingress ? … : <EditorSection title="Domains & Routing" description="Ingress isn't enabled …" />` structure) with:

```tsx
        <EditorSection
          title="Domains & Routing"
          description="Serve the app on hostnames under the domains on this server. Each domain sets how HTTPS works."
        >
          <AddressRows
            state={state}
            onChange={onChange}
            appId={state.config.id || undefined}
            serverId={activeServer.id}
            registrations={registrations}
            onRefresh={refresh}
            errors={errors}
          />
          <RedirectRows
            state={state}
            onChange={onChange}
            appId={state.config.id || undefined}
            serverId={activeServer.id}
            registrations={registrations}
            onRefresh={refresh}
            errors={errors}
          />
        </EditorSection>
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/hooks/use-registrations.test.tsx src/components/git-source-fields.test.tsx src/components/ingress-editor.test.tsx src/components/app-editor.test.tsx src/components/app-editor-panel.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/hooks/use-registrations.ts web/src/hooks/use-registrations.test.tsx web/src/components/hostname-field.tsx web/src/components/ingress-editor.tsx web/src/components/ingress-editor.test.tsx web/src/components/git-source-fields.tsx web/src/components/git-source-fields.test.tsx web/src/components/app-editor.tsx
git commit -m "$(cat <<'EOF'
refactor(web): address, redirect and Git source fields as shared pieces

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Name with an icon and colour that follow it

**Files:**
- Create: `web/src/lib/app-look.ts`, `web/src/lib/app-look.test.ts`
- Modify: `web/src/components/icon-picker.tsx` (extract `IconGrid`; `IconPicker`'s props unchanged)
- Create: `web/src/components/app-look-picker.tsx`
- Create: `web/src/components/name-field.tsx`, `web/src/components/name-field.test.tsx`

**Interfaces:**
- Consumes: `FormField`, `invalidAttrs` (Task 1); `AppIcon`, `APP_ICON_NAMES`, `getAppIcon` (existing).
- Produces:
  - `APP_COLORS: readonly string[]`, `DEFAULT_APP_COLOR = "#64748b"`, `type AppLook = { icon: string; color: string }`, `suggestAppLook(name: string): AppLook` (`@/lib/app-look`)
  - `IconGrid({ value?, onPick: (name: string) => void })` (`@/components/icon-picker`; buttons named by icon, `aria-pressed` on the current one)
  - `AppLookPicker({ name, icon, color, onPick: (look: AppLook) => void, onReset: () => void })` — a 40px tile button "Change icon and colour" opening a popover (`@/components/app-look-picker`)
  - `NameField({ value: { name: string; icon: string; color: string }; onChange: (next: { name; icon; color }) => void; error?: string; id? = "app-name" })` — the name drives icon and colour until the user picks one (`@/components/name-field`)

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/app-look.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import { getAppIcon } from "@/lib/app-icons";
import { APP_COLORS, DEFAULT_APP_COLOR, suggestAppLook } from "@/lib/app-look";

describe("suggestAppLook", () => {
  it("picks an icon from a word in the name", () => {
    expect(suggestAppLook("my-blog").icon).toBe("Book");
    expect(suggestAppLook("Postgres DB").icon).toBe("Database");
    expect(suggestAppLook("shop").icon).toBe("ShoppingCart");
    expect(suggestAppLook("jellyfin").icon).toBe("Film");
  });

  it("matches word starts only (feedback is not a database)", () => {
    expect(suggestAppLook("feedback").icon).toBe("");
  });

  it("gives every name a stable colour from the palette", () => {
    const a = suggestAppLook("website").color;
    expect(APP_COLORS).toContain(a);
    expect(suggestAppLook("website").color).toBe(a);
    expect(suggestAppLook("  Website ").color).toBe(a);
  });

  it("falls back to the neutral tile for an empty name", () => {
    expect(suggestAppLook("  ")).toEqual({ icon: "", color: DEFAULT_APP_COLOR });
  });

  it("only suggests icons the picker has", () => {
    for (const name of ["blog", "shop", "db", "mail", "photos", "video", "music", "chat", "grafana", "cloud", "git", "home", "vault", "web"]) {
      const { icon } = suggestAppLook(name);
      expect(icon, name).not.toBe("");
      expect(getAppIcon(icon), name).toBeDefined();
    }
  });
});
```

Create `web/src/components/name-field.test.tsx`:

```tsx
import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { NameField } from "@/components/name-field";
import { suggestAppLook } from "@/lib/app-look";

function Harness({ error }: { error?: string }) {
  const [value, setValue] = useState({ name: "", icon: "", color: "" });
  return (
    <>
      <NameField value={value} onChange={setValue} error={error} />
      <output aria-label="look">{JSON.stringify(value)}</output>
    </>
  );
}
const look = () => JSON.parse(screen.getByRole("status", { name: "look" }).textContent ?? "{}");

describe("NameField", () => {
  it("lets the name pick the icon and colour", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    await user.type(screen.getByLabelText("Name"), "shop");
    expect(look()).toEqual({ name: "shop", ...suggestAppLook("shop") });
  });

  it("keeps a picked icon when the name changes, until reset", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    await user.type(screen.getByLabelText("Name"), "shop");
    await user.click(screen.getByRole("button", { name: "Change icon and colour" }));
    await user.click(await screen.findByRole("button", { name: "Database" }));
    await user.type(screen.getByLabelText("Name"), "-2");
    expect(look()).toMatchObject({ name: "shop-2", icon: "Database" });

    await user.click(screen.getByRole("button", { name: "Change icon and colour" }));
    await user.click(await screen.findByRole("button", { name: "Pick automatically from the name" }));
    expect(look()).toEqual({ name: "shop-2", ...suggestAppLook("shop-2") });
  });

  it("offers the palette's colours", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    await user.click(screen.getByRole("button", { name: "Change icon and colour" }));
    await user.click(await screen.findByRole("button", { name: "Colour #6366f1" }));
    expect(look().color).toBe("#6366f1");
  });

  it("shows its message", () => {
    render(<Harness error="Give the app a name." />);
    expect(screen.getByLabelText("Name")).toHaveAccessibleDescription("Give the app a name.");
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/app-look.test.ts src/components/name-field.test.tsx`
Expected: FAIL (modules missing).

- [ ] **Step 3: Write the implementation**

Create `web/src/lib/app-look.ts`:

```ts
// An app's tile (icon + colour) picked from its name while the user hasn't
// chosen one: a word in the name suggests the icon, the name's hash the
// colour, so the same name always gets the same tile.

export type AppLook = { icon: string; color: string };

// DEFAULT_APP_COLOR is the neutral tile (no name yet).
export const DEFAULT_APP_COLOR = "#64748b";

// APP_COLORS are the tile colours offered and picked from: calm hues that
// read on light and dark.
export const APP_COLORS: readonly string[] = [
  "#0ea5e9",
  "#6366f1",
  "#14b8a6",
  "#22c55e",
  "#f59e0b",
  "#ef4444",
  "#ec4899",
  "#64748b",
];

// KEYWORDS: a word of the name starting with one of these suggests the icon
// (names from APP_ICONS).
const KEYWORDS: readonly [readonly string[], string][] = [
  [["blog", "wiki", "doc", "note", "book"], "Book"],
  [["shop", "store", "cart"], "ShoppingCart"],
  [["db", "database", "postgres", "mysql", "maria", "redis", "mongo", "sql"], "Database"],
  [["mail", "smtp"], "Mail"],
  [["photo", "image", "gallery", "immich"], "Camera"],
  [["video", "jellyfin", "plex", "media", "film", "movie"], "Film"],
  [["music", "audio", "navidrome", "radio"], "Music"],
  [["chat", "matrix", "talk"], "MessageSquare"],
  [["monitor", "grafana", "metric", "stat", "analytic", "uptime"], "Activity"],
  [["cloud", "drive", "file", "nextcloud", "sync"], "Cloud"],
  [["git", "code", "dev"], "Code"],
  [["home", "assistant"], "Home"],
  [["vpn", "proxy", "auth", "vault", "password"], "Shield"],
  [["web", "site", "www", "nginx"], "Globe"],
];

// hash is FNV-1a (32 bit): stable across sessions and browsers.
function hash(s: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

export function suggestAppLook(name: string): AppLook {
  const key = name.trim().toLowerCase();
  if (!key) return { icon: "", color: DEFAULT_APP_COLOR };
  const words = key.split(/[^a-z0-9]+/).filter(Boolean);
  const icon = KEYWORDS.find(([starts]) => words.some((w) => starts.some((s) => w.startsWith(s))))?.[1] ?? "";
  return { icon, color: APP_COLORS[hash(key) % APP_COLORS.length] };
}
```

Replace the body of `web/src/components/icon-picker.tsx` (keep its imports, add `useState` already there) with:

```tsx
// IconGrid is the searchable grid of app icons.
export function IconGrid({ value, onPick }: { value?: string; onPick: (name: string) => void }) {
  const [query, setQuery] = useState("");
  const filtered = query
    ? APP_ICON_NAMES.filter((n) => n.toLowerCase().includes(query.toLowerCase()))
    : APP_ICON_NAMES;
  return (
    <div>
      <Input
        autoFocus
        placeholder="Search icons…"
        aria-label="Search icons"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        className="mb-2"
      />
      <ScrollArea className="h-56">
        <div className="grid grid-cols-6 gap-1 pr-2">
          {filtered.map((name) => {
            const Icon = getAppIcon(name)!;
            return (
              <button
                key={name}
                type="button"
                title={name}
                aria-label={name}
                aria-pressed={value === name}
                className={cn(
                  "flex aspect-square items-center justify-center rounded-md hover:bg-muted",
                  value === name && "bg-muted ring-2 ring-primary",
                )}
                onClick={() => onPick(name)}
              >
                <Icon className="size-5" />
              </button>
            );
          })}
          {filtered.length === 0 ? (
            <p className="col-span-6 py-4 text-center text-sm text-muted-foreground">No icons</p>
          ) : null}
        </div>
      </ScrollArea>
    </div>
  );
}

export function IconPicker({
  value,
  color,
  name,
  id,
  onChange,
}: {
  value?: string;
  // name and id give the tile its fallback initial (as in the header).
  name?: string;
  id?: string;
  color?: string;
  onChange: (icon: string) => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button variant="outline" className="h-12 w-12 p-0">
          <AppIcon icon={value} name={name} id={id} color={color} className="size-8" />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-72 p-2" align="start">
        <IconGrid
          value={value}
          onPick={(next) => {
            onChange(next);
            setOpen(false);
          }}
        />
      </PopoverContent>
    </Popover>
  );
}
```

(If phase 2 changed `IconPicker`'s markup, keep phase 2's version of `IconPicker` and only route its grid through `IconGrid`.)

Create `web/src/components/app-look-picker.tsx`:

```tsx
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { AppIcon } from "@/components/app-icon";
import { IconGrid } from "@/components/icon-picker";
import { APP_COLORS, DEFAULT_APP_COLOR, type AppLook } from "@/lib/app-look";
import { cn } from "@/lib/utils";

// AppLookPicker is the app's tile as a button: it opens the icon grid and
// the colours. onReset hands the choice back to the name.
export function AppLookPicker({
  name,
  icon,
  color,
  onPick,
  onReset,
}: {
  name: string;
  icon: string;
  color: string;
  onPick: (look: AppLook) => void;
  onReset: () => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="outline"
          className="size-10 shrink-0 p-0"
          aria-label="Change icon and colour"
          title="Change icon and colour"
        >
          <AppIcon name={name} icon={icon} color={color} className="size-7" />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-72 space-y-3 p-3" align="start">
        <IconGrid value={icon} onPick={(next) => onPick({ icon: next, color })} />
        <div className="space-y-1.5">
          <p className="text-xs font-medium text-muted-foreground">Colour</p>
          <div className="flex flex-wrap items-center gap-1.5">
            {APP_COLORS.map((c) => (
              <button
                key={c}
                type="button"
                aria-label={`Colour ${c}`}
                aria-pressed={color === c}
                className={cn(
                  "size-7 rounded-md ring-offset-2 ring-offset-background",
                  color === c && "ring-2 ring-primary",
                )}
                style={{ backgroundColor: c }}
                onClick={() => onPick({ icon, color: c })}
              />
            ))}
            <input
              type="color"
              aria-label="Custom colour"
              value={color || DEFAULT_APP_COLOR}
              className="h-7 w-10 cursor-pointer rounded-md border bg-transparent p-0.5"
              onChange={(e) => onPick({ icon, color: e.target.value })}
            />
          </div>
        </div>
        <Button
          type="button"
          variant="link"
          size="sm"
          className="h-auto p-0"
          onClick={() => {
            onReset();
            setOpen(false);
          }}
        >
          Pick automatically from the name
        </Button>
      </PopoverContent>
    </Popover>
  );
}
```

Create `web/src/components/name-field.tsx`:

```tsx
import { useState } from "react";

import { Input } from "@/components/ui/input";
import { AppLookPicker } from "@/components/app-look-picker";
import { FormField } from "@/components/form-field";
import { suggestAppLook } from "@/lib/app-look";
import { invalidAttrs } from "@/lib/field-errors";

type Value = { name: string; icon: string; color: string };

// NameField is a new app's name with its tile beside it. While the user
// hasn't picked an icon or colour, the name picks them (suggestAppLook);
// a pick sticks until "Pick automatically from the name".
export function NameField({
  value,
  onChange,
  error,
  id = "app-name",
}: {
  value: Value;
  onChange: (next: Value) => void;
  error?: string;
  id?: string;
}) {
  const [picked, setPicked] = useState(false);
  return (
    <FormField id={id} label="Name" error={error}>
      <div className="flex items-center gap-2">
        <AppLookPicker
          name={value.name}
          icon={value.icon}
          color={value.color}
          onPick={(look) => {
            setPicked(true);
            onChange({ ...value, ...look });
          }}
          onReset={() => {
            setPicked(false);
            onChange({ ...value, ...suggestAppLook(value.name) });
          }}
        />
        <Input
          id={id}
          value={value.name}
          placeholder="my-app"
          autoComplete="off"
          {...invalidAttrs(id, error)}
          onChange={(e) => {
            const name = e.target.value;
            onChange(picked ? { ...value, name } : { name, ...suggestAppLook(name) });
          }}
        />
      </div>
    </FormField>
  );
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/app-look.test.ts src/components/name-field.test.tsx src/components/template-editor.test.tsx src/pages/org-members.test.tsx`
Expected: PASS (the template editor and org page keep using `IconPicker` unchanged).

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/app-look.ts web/src/lib/app-look.test.ts web/src/components/icon-picker.tsx web/src/components/app-look-picker.tsx web/src/components/name-field.tsx web/src/components/name-field.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): app name field whose icon and colour follow the name

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: The Compose file form

**Files:**
- Create: `web/src/lib/new-app.ts`, `web/src/lib/new-app.test.ts`
- Create: `web/src/hooks/use-create-app.ts`
- Create: `web/src/components/new-app/create-bar.tsx`
- Create: `web/src/components/new-app/compose-flow.tsx`, `web/src/components/new-app/compose-flow.test.tsx`

**Interfaces:**
- Consumes: `editorErrors`, `pruneBlankAddresses`, `emptyEditorState`, `newAddressRow`, `newGitSource`, `buildSavePayload` (Task 1/existing); `FieldMessage`, `FormField`, `focusFirstInvalid`, `hasErrorIn` (Task 1); `FileEditors`, `ImageChips` (Task 2); `VariablesEditor` (Task 2); `AddressRows`, `RedirectRows`, `useRegistrations(serverId, enabled)` (Task 3); `NameField` (Task 4); `Disclosure` (phase 1); `appPath`, `appsPath`, `type NewAppSource` (phase 1).
- Produces:
  - `parseNewAppSource(raw: string | null): NewAppSource | null`, `createdApp(list: App[], before: ReadonlySet<string>, name: string): App | undefined`, `composeStart(): AppEditorState` (default compose.yml + one blank Address row), `gitStart(): AppEditorState` (no files, `newGitSource()`) (`@/lib/new-app`)
  - `useCreateApp(serverId: string): { create: (state: AppEditorState) => Promise<void>; busy: boolean; error: string }` (`@/hooks/use-create-app`)
  - `CreateBar({ busy: boolean; onCreate: () => void; cancelTo: string; invalid?: boolean; label? = "Create app" })` (`@/components/new-app/create-bar`)
  - `ComposeFlow({ serverId: string })` (`@/components/new-app/compose-flow`)

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/new-app.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import type { App } from "@/context/apps-context-base";
import { composeStart, createdApp, gitStart, parseNewAppSource } from "@/lib/new-app";
import { DEFAULT_COMPOSE } from "@/types/app-config";

const app = (id: string, name: string) => ({ id, name, serverId: "s1", version: "1", icon: "", color: "", createdAt: "" }) as App;

describe("parseNewAppSource", () => {
  it("reads the three sources and nothing else", () => {
    expect(parseNewAppSource("template")).toBe("template");
    expect(parseNewAppSource("compose")).toBe("compose");
    expect(parseNewAppSource("git")).toBe("git");
    expect(parseNewAppSource("GIT")).toBeNull();
    expect(parseNewAppSource("")).toBeNull();
    expect(parseNewAppSource(null)).toBeNull();
  });
});

describe("createdApp", () => {
  it("finds the new app, not an older one of the same name", () => {
    const list = [app("old", "shop"), app("new", "shop"), app("x", "blog")];
    expect(createdApp(list, new Set(["old", "x"]), "shop")?.id).toBe("new");
  });

  it("finds nothing when the answer doesn't list it yet", () => {
    expect(createdApp([app("old", "shop")], new Set(["old"]), "shop")).toBeUndefined();
  });
});

describe("starting states", () => {
  it("starts a compose app with the example file and one blank address", () => {
    const s = composeStart();
    expect(s.config.files.map((f) => f.filename)).toEqual(["compose.yml"]);
    expect(Object.values(s.files)).toEqual([DEFAULT_COMPOSE]);
    expect(s.config.ingress?.domains).toEqual([expect.objectContaining({ domain: "", upstream_port: "" })]);
    expect(s.config.source).toBeUndefined();
  });

  it("starts a Git app with no files and the default source", () => {
    const s = gitStart();
    expect(s.config.files).toEqual([]);
    expect(s.files).toEqual({});
    expect(s.config.source).toEqual({ repo_url: "", branch: "main", compose_path: "", auto_update: true, poll_seconds: 120 });
  });
});
```

Create `web/src/components/new-app/compose-flow.test.tsx`:

```tsx
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

  it("asks for a compose file when the last one is removed", async () => {
    const user = userEvent.setup();
    renderFlow(vi.fn());
    await user.type(screen.getByLabelText("Name"), "shop");
    await user.click(screen.getByRole("button", { name: "Remove compose.yml" }));
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(screen.getByText("Add a compose file named compose.yml.")).toBeInTheDocument();
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

  it("opens More options for a half-made redirect", async () => {
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

  it("keeps Create in reach at the bottom of a phone screen", () => {
    stubViewport(375);
    renderFlow(vi.fn());
    expect(screen.getByTestId("create-bar")).toHaveClass("sticky", "bottom-0", "sm:static");
    expect(screen.getByRole("button", { name: "Create app" })).toHaveClass("max-sm:h-10");
  });

  it("goes back to the Apps page on Cancel", async () => {
    const user = userEvent.setup();
    renderFlow(vi.fn());
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.getByText("the apps")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/new-app.test.ts src/components/new-app/compose-flow.test.tsx`
Expected: FAIL (modules missing).

- [ ] **Step 3: Write the implementation**

Create `web/src/lib/new-app.ts`:

```ts
import type { App } from "@/context/apps-context-base";
import { emptyEditorState, newAddressRow, newGitSource } from "@/lib/app-editor-io";
import type { NewAppSource } from "@/lib/routes";
import type { AppEditorState } from "@/types/app-config";

const SOURCES: readonly NewAppSource[] = ["template", "compose", "git"];

// parseNewAppSource reads /new?from=: one of the three sources, else null
// (the chooser).
export function parseNewAppSource(raw: string | null): NewAppSource | null {
  return raw !== null && (SOURCES as readonly string[]).includes(raw) ? (raw as NewAppSource) : null;
}

// createdApp is the app a create added: named name and absent from the list
// before (another app may carry the same name). Undefined when the agent's
// answer doesn't list it yet.
export function createdApp(list: App[], before: ReadonlySet<string>, name: string): App | undefined {
  return list.find((a) => a.name === name && !before.has(a.id));
}

// composeStart is the compose form's state: the example compose.yml and one
// blank Address row (dropped on create while blank).
export function composeStart(): AppEditorState {
  const s = emptyEditorState();
  return { ...s, config: { ...s.config, ingress: { domains: [newAddressRow()], redirects: [] } } };
}

// gitStart is the Git form's state: no files of its own (the repository's
// compose file runs) and the default source.
export function gitStart(): AppEditorState {
  const s = emptyEditorState();
  return { ...s, config: { ...s.config, files: [], source: newGitSource() }, files: {} };
}
```

Create `web/src/hooks/use-create-app.ts`:

```ts
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";

import { useApps } from "@/context/use-apps";
import { buildSavePayload } from "@/lib/app-editor-io";
import { createdApp } from "@/lib/new-app";
import { appPath, appsPath } from "@/lib/routes";
import type { AppEditorState } from "@/types/app-config";

// useCreateApp saves a new app (app.save without an id: the agent creates
// and deploys it) and opens its Overview, or the Apps page when the answer
// doesn't list it yet. A failure stays on the form as error.
export function useCreateApp(serverId: string) {
  const { apps, saveApp, getPublicKey } = useApps();
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const create = async (state: AppEditorState) => {
    const before = new Set(apps.map((a) => a.id));
    setBusy(true);
    setError("");
    try {
      const payload = await buildSavePayload(state, getPublicKey);
      const list = await saveApp(payload);
      toast.success("App created and deploying");
      const created = createdApp(list, before, state.config.name.trim());
      navigate(created ? appPath(serverId, created.id) : appsPath(serverId));
    } catch (e) {
      setError(e instanceof Error && e.message ? e.message : "Couldn't create the app");
    } finally {
      setBusy(false);
    }
  };

  return { create, busy, error };
}
```

Create `web/src/components/new-app/create-bar.tsx`:

```tsx
import { useNavigate } from "react-router-dom";

import { Button } from "@/components/ui/button";

// CreateBar ends a create form: Cancel and the one filled Create app. On a
// phone it sticks to the bottom of the screen so Create stays in reach;
// from 640px up it sits under the form. invalid adds the one-line summary
// of a refused attempt (each field says what is wrong).
export function CreateBar({
  busy,
  onCreate,
  cancelTo,
  invalid = false,
  label = "Create app",
}: {
  busy: boolean;
  onCreate: () => void;
  cancelTo: string;
  invalid?: boolean;
  label?: string;
}) {
  const navigate = useNavigate();
  return (
    <div
      data-testid="create-bar"
      className="sticky bottom-0 z-10 -mx-4 flex flex-wrap items-center justify-end gap-2 border-t bg-background/95 px-4 pt-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] backdrop-blur sm:static sm:mx-0 sm:border-t-0 sm:bg-transparent sm:px-0 sm:pt-2 sm:pb-0 sm:backdrop-blur-none"
    >
      {invalid ? (
        <p role="alert" className="mr-auto text-sm text-destructive">
          Some fields need attention.
        </p>
      ) : null}
      <Button variant="ghost" className="max-sm:h-10" disabled={busy} onClick={() => navigate(cancelTo)}>
        Cancel
      </Button>
      <Button className="max-sm:h-10 max-sm:flex-1" disabled={busy} onClick={onCreate}>
        {busy ? "Creating…" : label}
      </Button>
    </div>
  );
}
```

Create `web/src/components/new-app/compose-flow.tsx`:

```tsx
import { useMemo, useRef, useState } from "react";

import { Input } from "@/components/ui/input";
import { Disclosure } from "@/components/disclosure";
import { ErrorText } from "@/components/error-text";
import { FileEditors, ImageChips } from "@/components/files-section";
import { FieldMessage, FormField } from "@/components/form-field";
import { AddressRows, RedirectRows } from "@/components/ingress-editor";
import { NameField } from "@/components/name-field";
import { CreateBar } from "@/components/new-app/create-bar";
import { SharedVariablesHint } from "@/components/shared-variables-hint";
import { VariablesEditor } from "@/components/variables-editor";
import { useServers } from "@/context/use-servers";
import { useCreateApp } from "@/hooks/use-create-app";
import { useRegistrations } from "@/hooks/use-registrations";
import { useSharedNames } from "@/hooks/use-shared-names";
import { editorErrors, pruneBlankAddresses } from "@/lib/app-editor-io";
import { imageRefs } from "@/lib/compose-images";
import { focusFirstInvalid, hasErrorIn, type FieldErrors } from "@/lib/field-errors";
import { composeStart } from "@/lib/new-app";
import { appsPath } from "@/lib/routes";
import { isComposeFile, type AppEditorState } from "@/types/app-config";

const NO_ERRORS: FieldErrors = {};

// ComposeFlow is New app → Compose file: a name (its tile follows it), the
// compose file, variables and one address; description, redirects and
// image versions under "More options". Create deploys it at once.
export function ComposeFlow({ serverId }: { serverId: string }) {
  const { activeServer, loading: serversLoading } = useServers();
  const ingressOn = Boolean(activeServer?.features?.ingress);
  const shared = useSharedNames(serverId);
  const { registrations, refresh } = useRegistrations(serverId, ingressOn);
  const { create, busy, error } = useCreateApp(serverId);
  const [state, setState] = useState<AppEditorState>(composeStart);
  const [tried, setTried] = useState(false);
  const [varsOpen, setVarsOpen] = useState(false);
  const [moreOpen, setMoreOpen] = useState(false);
  const form = useRef<HTMLDivElement>(null);

  // ready is what Create sends: the untouched Address row dropped (and the
  // whole ingress on a server without the proxy).
  const ready = useMemo(() => {
    const pruned = pruneBlankAddresses(state);
    return ingressOn ? pruned : { ...pruned, config: { ...pruned.config, ingress: undefined } };
  }, [state, ingressOn]);
  const errors = useMemo(() => (tried ? editorErrors(ready) : NO_ERRORS), [tried, ready]);
  const invalid = Object.keys(errors).length > 0;
  const config = state.config;
  const setConfig = (patch: Partial<AppEditorState["config"]>) =>
    setState((s) => ({ ...s, config: { ...s.config, ...patch } }));
  const composeFiles = config.files.filter((f) => isComposeFile(f.filename) && imageRefs(state.files[f.id] ?? "").length > 0);
  const rows = { state, onChange: setState, serverId, registrations, onRefresh: refresh, errors };

  const submit = () => {
    setTried(true);
    if (Object.keys(editorErrors(ready)).length > 0) {
      focusFirstInvalid(form.current);
      return;
    }
    void create(ready);
  };

  return (
    <div ref={form} className="space-y-8">
      <NameField
        value={{ name: config.name, icon: config.icon ?? "", color: config.color ?? "" }}
        onChange={(look) => setConfig(look)}
        error={errors.name}
      />

      <section aria-labelledby="compose-title" className="space-y-3">
        <h2 id="compose-title" className="text-[15px] font-semibold">
          Compose file
        </h2>
        <FieldMessage id="files" message={errors.files} className="text-sm" />
        <FileEditors
          files={config.files}
          contents={state.files}
          onChange={(files, contents) => setState((s) => ({ ...s, config: { ...s.config, files }, files: contents }))}
          imageChips={false}
          errors={errors}
          addLabel="Add another file"
        />
      </section>

      <Disclosure
        label={`Variables (${config.variables.length})`}
        open={varsOpen || hasErrorIn(errors, "var:")}
        onOpenChange={setVarsOpen}
      >
        <p className="text-sm text-muted-foreground">
          Used in the files as <code>{"${NAME}"}</code>. Secret values are encrypted in your browser.
        </p>
        <VariablesEditor state={state} onChange={setState} shared={shared} errors={errors} />
        <SharedVariablesHint shared={shared} />
      </Disclosure>

      {serversLoading ? null : ingressOn ? (
        <section aria-labelledby="address-title" className="space-y-3">
          <div className="space-y-1">
            <h2 id="address-title" className="text-[15px] font-semibold">
              Address
            </h2>
            <p className="text-sm text-muted-foreground">Where people reach the app. Leave it empty to keep the app private.</p>
          </div>
          <AddressRows {...rows} addLabel="Add another" emptyNote={null} />
        </section>
      ) : (
        <p className="text-sm text-muted-foreground">
          This server has no built-in web proxy: people reach the app on the port its compose file publishes.
        </p>
      )}

      <Disclosure open={moreOpen || hasErrorIn(errors, "redirect-")} onOpenChange={setMoreOpen}>
        <FormField id="app-desc" label="Description">
          <Input
            id="app-desc"
            value={config.description ?? ""}
            placeholder="Optional"
            onChange={(e) => setConfig({ description: e.target.value })}
          />
        </FormField>
        {ingressOn ? (
          <div className="space-y-2">
            <h3 className="text-sm font-medium">Redirects</h3>
            <RedirectRows {...rows} />
          </div>
        ) : null}
        {composeFiles.map((f) => (
          <div key={f.id} className="space-y-1">
            <h3 className="text-sm font-medium">
              Image versions in <code className="font-mono">{f.filename}</code>
            </h3>
            <div className="overflow-hidden rounded-md border">
              <ImageChips
                content={state.files[f.id] ?? ""}
                onReplace={(oldRef, newRef) =>
                  setState((s) => ({ ...s, files: { ...s.files, [f.id]: (s.files[f.id] ?? "").split(oldRef).join(newRef) } }))
                }
              />
            </div>
          </div>
        ))}
      </Disclosure>

      {error ? <ErrorText alert icon text={error} /> : null}
      <CreateBar busy={busy} onCreate={submit} cancelTo={appsPath(serverId)} invalid={invalid} />
    </div>
  );
}
```

Note: `ErrorText` with `alert` also has `role="alert"`; the validation summary and an agent failure never show together (a create that reaches the agent has passed validation), so tests reading `getByRole("alert")` see one.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/new-app.test.ts src/components/new-app/compose-flow.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/lib/new-app.ts web/src/lib/new-app.test.ts web/src/hooks/use-create-app.ts web/src/components/new-app/create-bar.tsx web/src/components/new-app/compose-flow.tsx web/src/components/new-app/compose-flow.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): Compose file form for new apps

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: The Git repository form

**Files:**
- Create: `web/src/components/new-app/git-flow.tsx`, `web/src/components/new-app/git-flow.test.tsx`

**Interfaces:**
- Consumes: `gitStart`, `useCreateApp`, `CreateBar` (Task 5); `GitSourceFields` (Task 3); `NameField` (Task 4); `editorErrors`, `newGitSource` (Task 1); `focusFirstInvalid` (Task 1).
- Produces: `GitFlow({ serverId: string })` (`@/components/new-app/git-flow`).

- [ ] **Step 1: Write the failing test**

Create `web/src/components/new-app/git-flow.test.tsx`:

```tsx
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

function renderFlow(saveApp = vi.fn(async () => [app("new1", "shop")]), getPublicKey = vi.fn(async () => "K")) {
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
    expect(screen.getByLabelText("Repository URL")).toHaveAccessibleDescription("Enter the repository's URL.");
    expect(screen.getByLabelText("Branch")).toHaveAccessibleDescription("Enter a branch.");
    await waitFor(() => expect(screen.getByLabelText("Repository URL")).toHaveFocus());
    expect(saveApp).not.toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `pnpm --dir web exec vitest run src/components/new-app/git-flow.test.tsx`
Expected: FAIL (`git-flow` missing).

- [ ] **Step 3: Write the implementation**

Create `web/src/components/new-app/git-flow.tsx`:

```tsx
import { useMemo, useRef, useState } from "react";

import { ErrorText } from "@/components/error-text";
import { GitSourceFields } from "@/components/git-source-fields";
import { NameField } from "@/components/name-field";
import { CreateBar } from "@/components/new-app/create-bar";
import { useCreateApp } from "@/hooks/use-create-app";
import { editorErrors, newGitSource } from "@/lib/app-editor-io";
import { focusFirstInvalid, type FieldErrors } from "@/lib/field-errors";
import { gitStart } from "@/lib/new-app";
import { appsPath } from "@/lib/routes";
import type { AppEditorState, AppSourceConfig } from "@/types/app-config";

const NO_ERRORS: FieldErrors = {};

// GitFlow is New app → Git repository: a name, the repository and branch;
// the token behind "Private repository?", the compose path and
// auto-redeploy under "Advanced". The app runs the repository's compose
// file; addresses and variables are added afterwards under Configuration.
export function GitFlow({ serverId }: { serverId: string }) {
  const { create, busy, error } = useCreateApp(serverId);
  const [state, setState] = useState<AppEditorState>(gitStart);
  const [tried, setTried] = useState(false);
  const form = useRef<HTMLDivElement>(null);
  const errors = useMemo(() => (tried ? editorErrors(state) : NO_ERRORS), [tried, state]);
  const config = state.config;
  const source = config.source ?? newGitSource();

  const setSource = (patch: Partial<AppSourceConfig>) =>
    setState((s) => ({ ...s, config: { ...s.config, source: { ...(s.config.source ?? newGitSource()), ...patch } } }));

  const submit = () => {
    setTried(true);
    if (Object.keys(editorErrors(state)).length > 0) {
      focusFirstInvalid(form.current);
      return;
    }
    void create(state);
  };

  return (
    <div ref={form} className="space-y-6">
      <NameField
        value={{ name: config.name, icon: config.icon ?? "", color: config.color ?? "" }}
        onChange={(look) => setState((s) => ({ ...s, config: { ...s.config, ...look } }))}
        error={errors.name}
      />
      <GitSourceFields
        source={source}
        token={state.sourceToken ?? ""}
        onSource={setSource}
        onToken={(sourceToken) => setState((s) => ({ ...s, sourceToken }))}
        errors={errors}
      />
      <p className="text-sm text-muted-foreground">
        The app runs the repository's compose file. Add addresses and variables under Configuration once it is created.
      </p>
      {error ? <ErrorText alert icon text={error} /> : null}
      <CreateBar busy={busy} onCreate={submit} cancelTo={appsPath(serverId)} invalid={Object.keys(errors).length > 0} />
    </div>
  );
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `pnpm --dir web exec vitest run src/components/new-app/git-flow.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/components/new-app/git-flow.tsx web/src/components/new-app/git-flow.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): Git repository form for new apps

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: The From a template form

**Files:**
- Create: `web/src/components/new-app/template-flow.tsx`, `web/src/components/new-app/template-flow.test.tsx`

**Interfaces:**
- Consumes: `CreateBar` (Task 5); `focusFirstInvalid` (Task 1); `Disclosure`, `EmptyState` (phase 1); `appPath`, `appsPath`, `serverSettingsPath`, `TEMPLATES_PATH` (phase 1); `ParameterFields`, `HostHints`, `NativeSelect`, `ErrorText`, `LoadError`, `PageSpinner`, `AppIcon`, `useTemplatesApi`, `useBackups().serverStorages`, `useHostCheck`, `sealSecrets`, `fieldErrors`, `splitValues`, `plainValues`, `previewHosts`, `expand`, `newKey` (existing; unchanged).
- Produces: `TemplateFlow({ serverId: string })` (`@/components/new-app/template-flow`). Same create/update/retry semantics as `CreateFromTemplatePage` (one browser key per form; a retry after a claimed create is an update of the pending instance), with: template cards instead of a select (one template is picked for you; several wait for a pick); required values up front, optional ones under "More options"; a backup without a default storage preselects this server's first storage; "Will be called …" / "Will be available at …"; the version picker under "Advanced"; **Create app** in `CreateBar`.

- [ ] **Step 1: Write the failing test**

Create `web/src/components/new-app/template-flow.test.tsx` (it carries over every case of `web/src/pages/create-from-template.test.tsx`, adapted to the new copy and structure, plus the new ones):

```tsx
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { TemplateFlow } from "@/components/new-app/template-flow";
import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import { ApiError, LOAD_TIMEOUT_MS, type TemplateView } from "@/lib/templates";
import { fakeAPI } from "@/test/fake-api";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
vi.mock("@/lib/ecies", () => ({
  ENCRYPTED: "<encrypted>",
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "s1" }) }));
const refresh = vi.hoisted(() => vi.fn(async () => {}));
vi.mock("@/context/use-apps", () => ({ useApps: () => ({ refresh }) }));
const serverStorages = vi.hoisted(() => vi.fn(async () => ["tenants", "local"]));
vi.mock("@/context/use-backups", () => ({ useBackups: () => ({ serverStorages }) }));

const VIEW: TemplateView = {
  id: "t1",
  name: "twillingate",
  latest_revision: 2,
  revision: 2,
  body: {
    files: { "compose.yaml": "services: {}\n" },
    parameters: [
      { name: "MEMORY_MAX", default: "256m", pattern: "^[0-9]+m$" },
      { name: "NAME", required: true, pattern: "^[a-z]{3,20}$", description: "Instance name" },
      { name: "SESSION_KEY", secret: true, required: true },
    ],
    app_name: "${NAME}",
    icon: "chart",
    color: "",
    ingress: [{ host: "${NAME}.twillingate.app", port: 8080 }],
    backups: { db: { enabled: true, storage: "tenants" } },
  },
  note: "",
  created_at: "2026-10-12T10:00:00Z",
  created_by: "u1",
  revisions: [
    { revision: 2, note: "bump", created_at: "2026-10-12T10:00:00Z", created_by: "u1" },
    { revision: 1, note: "", created_at: "2026-10-11T10:00:00Z", created_by: "u1" },
  ],
};

const SUMMARY = { id: "t1", name: "twillingate", latest_revision: 2, instances: 0, behind: 0, updated_at: "" };

const waitFor_ = vi.fn();
const notifications = { connected: true, subscribe: () => () => {}, waitFor: waitFor_ } as NotificationsContextValue;

const routes = (extra: Record<string, unknown> = {}) => ({
  "/api/v1/template/get-templates": { templates: [SUMMARY] },
  "/api/v1/template/get-template": VIEW,
  "/api/v1/domains/check": { available: true, covered: false, registration: null, claims: null },
  "/api/v1/server/get-public-key": { public_key: "K1" },
  "/api/v1/instance/get-instance": { app_id: "app-9", key: "k" },
  ...extra,
});

// accepted makes create-instance (or path) answer 202: the result comes over SSE.
function accepted(api: ReturnType<typeof fakeAPI>, path = "/api/v1/instance/create-instance") {
  const inner = api.fetchMock;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      const res = await inner(url, init);
      if (new URL(url, "http://x").pathname !== path || !res.ok) return res;
      return new Response(JSON.stringify({ data: { request_id: "r1" } }), { status: 202 });
    }),
  );
}

function renderFlow() {
  return render(
    <MemoryRouter initialEntries={["/server/s1/new?from=template"]}>
      <NotificationsContext.Provider value={notifications}>
        <Routes>
          <Route path="/server/s1/new" element={<TemplateFlow serverId="s1" />} />
          <Route path="/server/s1/app/:appId" element={<p>the new app</p>} />
          <Route path="/templates" element={<p>templates</p>} />
        </Routes>
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
}

// fill gives the required values.
async function fill(user: ReturnType<typeof userEvent.setup>) {
  await user.type(await screen.findByLabelText("NAME"), "abc");
  await user.type(screen.getByLabelText("SESSION_KEY"), "s");
}

beforeEach(() => {
  toast.success.mockReset();
  refresh.mockClear();
  waitFor_.mockReset();
  serverStorages.mockImplementation(async () => ["tenants", "local"]);
});
afterEach(() => vi.unstubAllGlobals());

describe("TemplateFlow", () => {
  it("picks the only template and asks for its required values first", async () => {
    const user = userEvent.setup();
    fakeAPI(routes());
    renderFlow();
    expect(await screen.findByRole("button", { name: /twillingate/ })).toHaveAttribute("aria-pressed", "true");
    const fields = await screen.findAllByRole("textbox");
    expect(fields[0]).toHaveAccessibleName("NAME");
    // Optional values wait under More options, their defaults shown.
    expect(screen.queryByLabelText("MEMORY_MAX")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "More options" }));
    expect(screen.getByLabelText("MEMORY_MAX")).toHaveAttribute("placeholder", "Default: 256m");
    await user.type(screen.getByLabelText("NAME"), "Shop");
    expect(screen.getByText("Must match ^[a-z]{3,20}$")).toBeInTheDocument();
    expect(screen.getByLabelText("NAME")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByLabelText("NAME")).toHaveAccessibleDescription("Must match ^[a-z]{3,20}$");
    expect(screen.getByLabelText("MEMORY_MAX")).not.toHaveAttribute("aria-invalid");
    expect(screen.queryByText("Required")).not.toBeInTheDocument();
  });

  it("waits for a pick when there are several templates", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(
      routes({
        "/api/v1/template/get-templates": { templates: [SUMMARY, { ...SUMMARY, id: "t2", name: "blog", latest_revision: 4 }] },
        "/api/v1/template/get-template": (_: unknown, q: URLSearchParams) =>
          q.get("id") === "t2" ? { ...VIEW, id: "t2", name: "blog", latest_revision: 4, revision: 4 } : VIEW,
      }),
    );
    renderFlow();
    expect(await screen.findByText("Pick a template to continue.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /blog/ })).toHaveTextContent("Version 4");
    await user.click(screen.getByRole("button", { name: /blog/ }));
    expect(await screen.findByLabelText("NAME")).toBeInTheDocument();
    expect(api.calls.find((c) => c.path === "/api/v1/template/get-template")?.query).toBe("?id=t2");
  });

  it("previews the name and the address with the domain coverage check", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(routes());
    renderFlow();
    expect(await screen.findByText("Fill in NAME to see the address.")).toBeInTheDocument();
    await user.type(screen.getByLabelText("NAME"), "shop");
    const addresses = screen.getByRole("list", { name: "Addresses" });
    expect(within(addresses).getByText("shop.twillingate.app")).toBeInTheDocument();
    expect(within(addresses).getByText(/Will be available at/)).toBeInTheDocument();
    expect(await within(addresses).findByText(/No domain on this server covers this hostname/)).toBeInTheDocument();
    const checks = api.calls.filter((c) => c.path === "/api/v1/domains/check");
    expect(checks.at(-1)?.query).toBe("?server_id=s1&domain=shop.twillingate.app");
    expect(screen.getByText("shop", { selector: "span.font-medium" })).toBeInTheDocument();
  });

  it("creates the app with encrypted secrets and a browser key, then opens it", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(routes({ "/api/v1/instance/create-instance": { request_id: "r1" } }));
    accepted(api);
    waitFor_.mockResolvedValue({ status: 0 });
    renderFlow();
    await user.click(await screen.findByRole("button", { name: "Create app" }));
    expect(screen.getAllByText("Required")).toHaveLength(2);
    await waitFor(() => expect(screen.getByLabelText("NAME")).toHaveFocus());
    await user.type(screen.getByLabelText("NAME"), "shop");
    await user.type(screen.getByLabelText("SESSION_KEY"), "hunter2");
    await user.selectOptions(screen.getByLabelText("Storage for db"), "local");
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(await screen.findByText("the new app")).toBeInTheDocument();
    const [sent] = api.posted("/api/v1/instance/create-instance") as Record<string, unknown>[];
    expect(sent).toMatchObject({
      server_id: "s1",
      template_id: "t1",
      revision: 2,
      parameters: { NAME: "shop" },
      secrets: { SESSION_KEY: "ct(K1):hunter2" },
      backups: { db: "local" },
    });
    expect(sent.key).toMatch(/^[0-9a-f-]{36}$/);
    expect(JSON.stringify(api.calls)).not.toContain('"hunter2"');
    expect(waitFor_).toHaveBeenCalledWith("r1", 10 * 60_000);
    expect(refresh).toHaveBeenCalled();
    expect(toast.success).toHaveBeenCalledWith("App created");
    expect(api.calls.find((c) => c.path === "/api/v1/instance/get-instance")?.query).toBe(`?server_id=s1&key=${sent.key}`);
  });

  it("opens More options for an optional value that is wrong", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(routes());
    renderFlow();
    await fill(user);
    await user.click(screen.getByRole("button", { name: "More options" }));
    await user.type(screen.getByLabelText("MEMORY_MAX"), "lots");
    await user.click(screen.getByRole("button", { name: "More options" }));
    expect(screen.queryByLabelText("MEMORY_MAX")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(screen.getByLabelText("MEMORY_MAX")).toHaveAccessibleDescription("Must match ^[0-9]+m$");
    await waitFor(() => expect(screen.getByLabelText("MEMORY_MAX")).toHaveFocus());
    expect(screen.getByRole("alert")).toHaveTextContent("Some fields need attention.");
    expect(api.posted("/api/v1/instance/create-instance")).toEqual([]);
  });

  it("retries a failed deploy as an update of the same key, and a refused create as a create", async () => {
    const user = userEvent.setup();
    let refuse = true;
    const api = fakeAPI(
      routes({
        "/api/v1/instance/create-instance": () =>
          refuse ? new ApiError('invalid template: parameters.NAME: "abc" is taken', 400) : { request_id: "r1" },
        "/api/v1/instance/update-instance": { request_id: "r2" },
      }),
    );
    renderFlow();
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(await screen.findByRole("alert")).toHaveTextContent('"abc" is taken');

    refuse = false;
    accepted(api);
    waitFor_.mockResolvedValueOnce({ status: 1, error: "pull access denied" });
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(await screen.findByText("pull access denied")).toBeInTheDocument();
    // The template and version are fixed once the API holds the key.
    expect(screen.getByRole("button", { name: /twillingate/ })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Advanced" }));
    expect(screen.getByLabelText("Version")).toBeDisabled();

    accepted(api, "/api/v1/instance/update-instance");
    waitFor_.mockResolvedValueOnce({ status: 0 });
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(await screen.findByText("the new app")).toBeInTheDocument();
    const creates = api.posted("/api/v1/instance/create-instance") as { key: string }[];
    const updates = api.posted("/api/v1/instance/update-instance") as { key: string; secrets: unknown }[];
    expect(creates).toHaveLength(2);
    expect(creates[0].key).toBe(creates[1].key);
    expect(updates).toEqual([expect.objectContaining({ key: creates[0].key, secrets: { SESSION_KEY: "ct(K1):s" } })]);
  });

  it("shows an agent failure by its last line, the rest behind Details", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(routes({ "/api/v1/instance/create-instance": { request_id: "r1" } }));
    accepted(api);
    waitFor_.mockResolvedValueOnce({ status: 1, error: "Container web  Creating\nError: pull access denied\n" });
    renderFlow();
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(await screen.findByText("Error: pull access denied")).toBeVisible();
    expect(screen.getByRole("alert")).toContainElement(screen.getByText("Details"));
    expect(screen.getByText(/Container web Creating/)).not.toBeVisible();
  });

  it.each([500, 409])("retries a create the API answered %i as an update of the same key", async (status) => {
    const user = userEvent.setup();
    const api = fakeAPI(
      routes({
        "/api/v1/instance/create-instance": new ApiError("the key may be held", status),
        "/api/v1/instance/update-instance": { request_id: "r2" },
      }),
    );
    renderFlow();
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("the key may be held");

    accepted(api, "/api/v1/instance/update-instance");
    waitFor_.mockResolvedValueOnce({ status: 0 });
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(await screen.findByText("the new app")).toBeInTheDocument();
    const creates = api.posted("/api/v1/instance/create-instance") as { key: string }[];
    const updates = api.posted("/api/v1/instance/update-instance") as { key: string }[];
    expect(creates).toHaveLength(1);
    expect(updates).toEqual([expect.objectContaining({ key: creates[0].key })]);
  });

  it("keeps a template's own storage, offering No storage only for a backup without one", async () => {
    fakeAPI(routes());
    const { unmount } = renderFlow();
    const select = await screen.findByLabelText("Storage for db");
    expect(select).toHaveValue("tenants");
    expect(within(select).queryByRole("option", { name: "No storage" })).not.toBeInTheDocument();
    unmount();
    vi.unstubAllGlobals();

    const bare = { ...VIEW, body: { ...VIEW.body, backups: { db: { enabled: false } } } };
    fakeAPI(routes({ "/api/v1/template/get-template": bare }));
    renderFlow();
    const empty = await screen.findByLabelText("Storage for db");
    expect(within(empty).getByRole("option", { name: "No storage" })).toBeInTheDocument();
  });

  it("preselects this server's first storage for a backup without a default, and sends it", async () => {
    const user = userEvent.setup();
    const bare = { ...VIEW, body: { ...VIEW.body, backups: { db: { enabled: true } } } };
    const api = fakeAPI(routes({ "/api/v1/template/get-template": bare, "/api/v1/instance/create-instance": { request_id: "" } }));
    renderFlow();
    await waitFor(() => expect(screen.getByLabelText("Storage for db")).toHaveValue("tenants"));
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Create app" }));
    await screen.findByText("the new app");
    expect(api.posted("/api/v1/instance/create-instance")[0]).toMatchObject({ backups: { db: "tenants" } });
  });

  it("sends no storage when the server has none", async () => {
    const user = userEvent.setup();
    serverStorages.mockImplementation(async () => []);
    const bare = { ...VIEW, body: { ...VIEW.body, backups: { db: { enabled: true } } } };
    const api = fakeAPI(routes({ "/api/v1/template/get-template": bare, "/api/v1/instance/create-instance": { request_id: "" } }));
    renderFlow();
    expect(await screen.findByLabelText("Storage for db")).toHaveValue("");
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Create app" }));
    await screen.findByText("the new app");
    expect(api.posted("/api/v1/instance/create-instance")[0]).not.toHaveProperty("backups");
  });

  it("flags a template storage this server doesn't have, linking to Storages", async () => {
    serverStorages.mockImplementation(async () => ["local"]);
    fakeAPI(routes());
    renderFlow();
    expect(await screen.findByText("This server has no storage named tenants.", { exact: false })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "tenants (not on this server)" })).toBeInTheDocument();
    expect(screen.getByLabelText("Storage for db")).toHaveValue("tenants");
    expect(screen.getByRole("link", { name: "Add it" })).toHaveAttribute("href", "/server/s1/settings/storages");
  });

  it("loads an older version from Advanced", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(routes());
    renderFlow();
    await screen.findByLabelText("NAME");
    await user.click(screen.getByRole("button", { name: "Advanced" }));
    expect(screen.getByRole("option", { name: "Latest (version 2)" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Version 2 · bump" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Version 1" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: /\br\d/ })).not.toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("Version"), "1");
    await waitFor(() => expect(api.calls.at(-1)?.query).toBe("?id=t1&revision=1"));
  });

  it("points to Templates when the org has none, and shows load failures", async () => {
    fakeAPI(routes({ "/api/v1/template/get-templates": { templates: [] } }));
    renderFlow();
    expect(await screen.findByText("Your organization has no templates yet.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Go to Templates" })).toHaveAttribute("href", "/templates");
    vi.unstubAllGlobals();

    fakeAPI(routes({ "/api/v1/template/get-template": new Error("template not found") }));
    renderFlow();
    expect(await screen.findByRole("alert")).toHaveTextContent("template not found");
  });

  it("retries a failed load", async () => {
    const user = userEvent.setup();
    let fail = true;
    fakeAPI(routes({ "/api/v1/template/get-template": () => (fail ? new Error("bad gateway") : VIEW) }));
    renderFlow();
    expect(await screen.findByRole("alert")).toHaveTextContent("bad gateway");
    fail = false;
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByLabelText("NAME")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("gives up on a read that never answers, aborting it, and offers Retry", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
      const signals: AbortSignal[] = [];
      vi.stubGlobal(
        "fetch",
        vi.fn((_url: string, init?: RequestInit) => {
          if (init?.signal) signals.push(init.signal);
          return new Promise<Response>(() => {});
        }),
      );
      renderFlow();
      act(() => {
        vi.advanceTimersByTime(LOAD_TIMEOUT_MS + 1);
      });
      expect(await screen.findByRole("alert")).toHaveTextContent("The server didn't answer in time");
      expect(signals[0]?.aborted).toBe(true);

      fakeAPI(routes());
      await user.click(screen.getByRole("button", { name: "Retry" }));
      expect(await screen.findByLabelText("NAME")).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `pnpm --dir web exec vitest run src/components/new-app/template-flow.test.tsx`
Expected: FAIL (`template-flow` missing).

- [ ] **Step 3: Write the implementation**

Create `web/src/components/new-app/template-flow.tsx`:

```tsx
import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { LayoutTemplate } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { AppIcon } from "@/components/app-icon";
import { Disclosure } from "@/components/disclosure";
import { EmptyState } from "@/components/empty-state";
import { ErrorText } from "@/components/error-text";
import { HostHints } from "@/components/host-hints";
import { NativeSelect } from "@/components/native-select";
import { CreateBar } from "@/components/new-app/create-bar";
import { LoadError, PageSpinner } from "@/components/page-states";
import { ParameterFields } from "@/components/parameter-fields";
import { useApps } from "@/context/use-apps";
import { useBackups } from "@/context/use-backups";
import { useHostCheck } from "@/hooks/use-host-check";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { validHostname } from "@/lib/domains";
import { focusFirstInvalid } from "@/lib/field-errors";
import { appPath, appsPath, serverSettingsPath, TEMPLATES_PATH } from "@/lib/routes";
import {
  expand,
  fieldErrors,
  newKey,
  plainValues,
  previewHosts,
  splitValues,
  type HostPreview,
} from "@/lib/template-params";
import {
  ApiError,
  sealSecrets,
  type BackupDefault,
  type TemplateSummary,
  type TemplateView,
  type UpdateInstanceBody,
} from "@/lib/templates";
import { cn } from "@/lib/utils";

// TemplateFlow is New app → From a template: pick a template (cards), fill
// in its required values (optional ones under "More options", defaults
// shown), pick storages for its backups, see the name and addresses the
// app will get, then Create app. Secret values are encrypted in the
// browser for this server. The version (latest by default) is under
// "Advanced".
export function TemplateFlow({ serverId }: { serverId: string }) {
  const navigate = useNavigate();
  const { refresh } = useApps();
  const { serverStorages } = useBackups();
  const api = useTemplatesApi();

  const [templates, setTemplates] = useState<TemplateSummary[] | null>(null);
  const [templateId, setTemplateId] = useState("");
  const [revision, setRevision] = useState(0); // 0: the latest
  const [view, setView] = useState<TemplateView | null>(null);
  const [values, setValues] = useState<Record<string, string>>({});
  // storages are this server's storage names (null while read).
  const [storages, setStorages] = useState<string[] | null>(null);
  // storageOverrides are the backup storages the user picked.
  const [storageOverrides, setStorageOverrides] = useState<Record<string, string>>({});
  const [loadError, setLoadError] = useState("");
  // attempt counts Retry clicks: each one runs the reads again.
  const [attempt, setAttempt] = useState(0);
  // key names this app to the API: a retry after a lost answer replays the
  // same create instead of adding a second app.
  const [key] = useState(newKey);
  // claimed: the API accepted the create (the key is taken), so a retry
  // after an agent failure is an update of that pending instance.
  const [claimed, setClaimed] = useState(false);
  const [tried, setTried] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [moreOpen, setMoreOpen] = useState(false);
  const form = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let live = true;
    api.listTemplates().then(
      (list) => {
        if (!live) return;
        setTemplates(list);
        // One template: nothing to choose.
        if (list.length === 1) setTemplateId((id) => id || list[0].id);
      },
      (e: unknown) => live && setLoadError(e instanceof Error ? e.message : "Failed to load templates"),
    );
    return () => {
      live = false;
    };
  }, [api, attempt]);

  useEffect(() => {
    serverStorages().then(setStorages, () => setStorages([]));
  }, [serverStorages]);

  useEffect(() => {
    if (!templateId) return;
    let live = true;
    api.getTemplate(templateId, revision || undefined).then(
      (v) => {
        if (!live) return;
        setView(v);
        setStorageOverrides({});
      },
      (e: unknown) => live && setLoadError(e instanceof Error ? e.message : "Failed to load the template"),
    );
    return () => {
      live = false;
    };
  }, [api, templateId, revision, attempt]);

  const params = useMemo(() => view?.body.parameters ?? [], [view]);
  const required = params.filter((p) => p.required);
  const optional = params.filter((p) => !p.required);
  const errors = fieldErrors(params, values);
  const invalid = tried && Object.keys(errors).length > 0;
  const optionalTrouble = tried && optional.some((p) => errors[p.name]);
  const plain = useMemo(() => plainValues(params, values), [params, values]);
  const hosts = view ? previewHosts(view.body, plain) : [];
  const appName = view ? expand(view.body.app_name, plain) : "";
  const backups = Object.entries(view?.body.backups ?? {});

  // storageFor is a backup's storage: the user's pick, else the template's
  // default, else (a backup without one) this server's first storage.
  const storageFor = (name: string, d: BackupDefault) =>
    storageOverrides[name] ?? (d.storage || storages?.[0] || "");

  // send creates the instance, or once the API holds this key (an earlier
  // attempt whose deploy failed: the instance is pending) updates it. A
  // refused create (a 4xx other than 409) claimed nothing; a 409 (the key is
  // held) or a 5xx (the claim came before the dispatch failed) may have.
  const send = async (body: UpdateInstanceBody, template: TemplateView) => {
    if (claimed) {
      try {
        await api.updateInstance(body);
        return;
      } catch (e) {
        if (!(e instanceof ApiError && e.status === 404)) throw e;
      }
    }
    setClaimed(true);
    try {
      await api.createInstance({ ...body, template_id: template.id, revision: template.revision });
    } catch (e) {
      if (e instanceof ApiError && e.status >= 400 && e.status < 500 && e.status !== 409) setClaimed(false);
      throw e;
    }
  };

  const create = async () => {
    if (!view || !serverId) return;
    setTried(true);
    if (Object.keys(fieldErrors(params, values)).length > 0) {
      focusFirstInvalid(form.current);
      return;
    }
    setBusy(true);
    setError("");
    try {
      const { plain: given, typed } = splitValues(params, values);
      const parameters = Object.fromEntries(Object.entries(given).filter(([, v]) => v !== ""));
      const secrets = await sealSecrets(api, serverId, typed);
      const overrides: Record<string, string> = {};
      for (const [name, d] of backups) {
        const picked = storageFor(name, d);
        if (picked !== (d.storage ?? "")) overrides[name] = picked;
      }
      const body = {
        server_id: serverId,
        key,
        parameters,
        secrets,
        ...(Object.keys(overrides).length ? { backups: overrides } : {}),
      };
      await send(body, view);
      const [inst] = await Promise.all([api.getInstance(serverId, key), refresh()]);
      toast.success("App created");
      navigate(appPath(serverId, inst.app_id));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't create the app");
    } finally {
      setBusy(false);
    }
  };

  if (loadError) {
    return (
      <LoadError
        message={loadError}
        onRetry={() => {
          setLoadError("");
          setAttempt((n) => n + 1);
        }}
      />
    );
  }
  if (templates === null) return <PageSpinner />;
  if (templates.length === 0) {
    return (
      <EmptyState
        title="Your organization has no templates yet."
        icon={LayoutTemplate}
        action={
          <Button asChild variant="outline">
            <Link to={TEMPLATES_PATH}>Go to Templates</Link>
          </Button>
        }
      />
    );
  }

  const setValue = (name: string, v: string) => setValues((prev) => ({ ...prev, [name]: v }));

  return (
    <div ref={form} className="space-y-8">
      <div role="group" aria-label="Templates" className="grid gap-3 sm:grid-cols-2">
        {templates.map((t) => {
          const on = t.id === templateId;
          return (
            <button
              key={t.id}
              type="button"
              aria-pressed={on}
              disabled={claimed}
              onClick={() => {
                if (on) return;
                setTemplateId(t.id);
                setRevision(0);
                setView(null);
                setValues({});
              }}
              className={cn(
                "flex min-h-16 items-center gap-3 rounded-xl border bg-card p-3 text-left transition-colors duration-150 hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-60 motion-reduce:transition-none",
                on && "border-primary ring-2 ring-primary/30",
              )}
            >
              <AppIcon
                name={t.name}
                icon={on && view ? view.body.icon : undefined}
                color={on && view ? view.body.color : undefined}
                className="size-10 shrink-0 text-base"
              />
              <span className="min-w-0">
                <span className="block truncate font-medium">{t.name}</span>
                <span className="block text-xs text-muted-foreground">Version {t.latest_revision}</span>
              </span>
            </button>
          );
        })}
      </div>

      {!templateId ? (
        <p className="text-sm text-muted-foreground">Pick a template to continue.</p>
      ) : !view ? (
        <PageSpinner />
      ) : (
        <>
          <section aria-label="Values" className="space-y-4">
            {required.length > 0 ? (
              <ParameterFields params={required} values={values} onChange={setValue} showRequired={tried} />
            ) : optional.length === 0 ? (
              <p className="text-sm text-muted-foreground">This template needs no values.</p>
            ) : null}
            {optional.length > 0 ? (
              <Disclosure open={moreOpen || optionalTrouble} onOpenChange={setMoreOpen}>
                <p className="text-xs text-muted-foreground">Leave a field empty to use its default.</p>
                <ParameterFields params={optional} values={values} onChange={setValue} showRequired={tried} />
              </Disclosure>
            ) : null}
            <p className="text-xs text-muted-foreground">Secret values are encrypted in your browser for this server.</p>
          </section>

          {backups.length > 0 ? (
            <section aria-labelledby="backups-title" className="space-y-3">
              <h2 id="backups-title" className="text-[15px] font-semibold">
                Backups
              </h2>
              {backups.map(([name, d]) => {
                const value = storageFor(name, d);
                const missing = value !== "" && storages !== null && !storages.includes(value);
                return (
                  <div key={name} className="grid gap-1.5">
                    <Label htmlFor={`storage-${name}`}>
                      <span>
                        Keep backups of <code className="font-mono">{name}</code> in
                      </span>
                      {d.enabled ? null : <span className="text-xs font-normal text-muted-foreground">off by default</span>}
                    </Label>
                    <NativeSelect
                      id={`storage-${name}`}
                      aria-label={`Storage for ${name}`}
                      value={value}
                      onChange={(e) => setStorageOverrides((prev) => ({ ...prev, [name]: e.target.value }))}
                    >
                      {(d.storage ?? "") === "" ? <option value="">No storage</option> : null}
                      {missing ? <option value={value}>{value} (not on this server)</option> : null}
                      {(storages ?? []).map((s) => (
                        <option key={s} value={s}>
                          {s}
                        </option>
                      ))}
                    </NativeSelect>
                    {missing ? (
                      <p className="text-xs text-destructive">
                        This server has no storage named {value}.{" "}
                        <Link to={serverSettingsPath(serverId, "storages")} className="underline underline-offset-4">
                          Add it
                        </Link>{" "}
                        or pick another.
                      </p>
                    ) : null}
                  </div>
                );
              })}
            </section>
          ) : null}

          <section aria-label="Summary" className="space-y-2 rounded-xl border bg-muted/30 p-4 text-sm">
            <p>
              <span className="text-muted-foreground">Will be called </span>
              <span className="font-medium">{appName.trim() || "…"}</span>
            </p>
            {hosts.length > 0 ? (
              <ul aria-label="Addresses" className="space-y-2">
                {hosts.map((h, i) => (
                  <HostPreviewRow key={i} serverId={serverId} preview={h} />
                ))}
              </ul>
            ) : null}
          </section>

          <Disclosure label="Advanced">
            <div className="grid gap-1.5 sm:max-w-xs">
              <Label htmlFor="pick-version">Version</Label>
              <NativeSelect
                id="pick-version"
                value={String(revision)}
                disabled={claimed}
                onChange={(e) => setRevision(Number(e.target.value))}
              >
                <option value="0">Latest (version {view.latest_revision})</option>
                {(view.revisions ?? []).map((r) => (
                  <option key={r.revision} value={String(r.revision)}>
                    Version {r.revision}
                    {r.note ? ` · ${r.note}` : ""}
                  </option>
                ))}
              </NativeSelect>
            </div>
          </Disclosure>

          {error ? <ErrorText alert icon text={error} /> : null}
          <CreateBar busy={busy} onCreate={() => void create()} cancelTo={appsPath(serverId)} invalid={invalid} />
        </>
      )}
    </div>
  );
}

// HostPreviewRow is one address the app will get, with the live check
// (another app's host, or no registered domain covering it) once its
// values are filled in.
function HostPreviewRow({ serverId, preview }: { serverId: string; preview: HostPreview }) {
  const ready = preview.missing.length === 0 && validHostname(preview.host);
  const check = useHostCheck(serverId, undefined, preview.host, { enabled: ready && !!serverId, delay: 400 });
  return (
    <li>
      <span className="text-muted-foreground">Will be available at </span>
      <span className="font-mono">{preview.host || "…"}</span>
      {preview.missing.length > 0 ? (
        <p className="mt-1 text-xs text-muted-foreground">Fill in {preview.missing.join(", ")} to see the address.</p>
      ) : !validHostname(preview.host) ? (
        <p className="mt-1 text-xs text-destructive">This isn't a valid hostname yet.</p>
      ) : check ? (
        <HostHints id={`host-${preview.host}`} check={check} />
      ) : null}
    </li>
  );
}
```

Note: the "Will be available at" text node and the host `span` are siblings, so `within(addresses).getByText("shop.twillingate.app")` finds the span and `getByText(/Will be available at/)` finds the label.

- [ ] **Step 4: Run the test to verify it passes**

Run: `pnpm --dir web exec vitest run src/components/new-app/template-flow.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/components/new-app/template-flow.tsx web/src/components/new-app/template-flow.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): From a template form with cards, defaults and an address preview

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: The chooser and the `/server/:id/new` page

**Files:**
- Create: `web/src/components/new-app/new-app-choices.tsx`, `web/src/components/new-app/new-app-button.tsx`
- Create: `web/src/components/new-app/new-app-choices.test.tsx`
- Modify: `web/src/pages/new-app.tsx` (replace its body)
- Create: `web/src/pages/new-app.test.tsx`
- Modify: `web/src/routes.tsx` (old `create-app` → `?from=compose`)
- Modify: `web/src/routes.test.tsx` (one `new-app` stub instead of the two old pages)
- Delete: `web/src/pages/create-app.tsx`, `web/src/pages/create-from-template.tsx`, `web/src/pages/create-from-template.test.tsx`

**Interfaces:**
- Consumes: `ComposeFlow` (Task 5), `GitFlow` (Task 6), `TemplateFlow` (Task 7), `parseNewAppSource` (Task 5); `PageHeader`, `ResponsiveDialog`, `newAppPath`, `appsPath` (phase 1).
- Produces:
  - `NewAppChoices({ serverId: string; onPick?: () => void })` — a list "Ways to start" of link cards: **From a template** (only when the org has at least one template), **Compose file**, **Git repository** → `newAppPath(serverId, from)`.
  - `NewAppButton({ serverId: string; className?: string })` — the filled **New app** button; opens `ResponsiveDialog` "New app" holding `NewAppChoices` (closes on a pick).
  - `NewAppPage` (default export, unchanged route): no or unknown `?from=` → `PageHeader` "New app" (back "Apps") + the chooser; a source → `PageHeader` "New app from a template" / "New app from a compose file" / "New app from Git" (back "New app") + its form.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/new-app/new-app-choices.test.tsx`:

```tsx
import type { ReactNode } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { NewAppButton } from "@/components/new-app/new-app-button";
import { NewAppChoices } from "@/components/new-app/new-app-choices";
import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import { fakeAPI } from "@/test/fake-api";

const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.fn() } as NotificationsContextValue;
const ONE = [{ id: "t1", name: "twillingate", latest_revision: 2, instances: 0, behind: 0, updated_at: "" }];

function At() {
  const l = useLocation();
  return <p data-testid="at">{l.pathname + l.search}</p>;
}

function renderIn(ui: ReactNode) {
  return render(
    <MemoryRouter initialEntries={["/server/s1"]}>
      <NotificationsContext.Provider value={notifications}>
        <Routes>
          <Route path="*" element={<>{ui}<At /></>} />
        </Routes>
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
}

afterEach(() => vi.unstubAllGlobals());

describe("NewAppChoices", () => {
  it("offers a template only when the organization has one", async () => {
    fakeAPI({ "/api/v1/template/get-templates": { templates: ONE } });
    renderIn(<NewAppChoices serverId="s1" />);
    expect(await screen.findByRole("link", { name: /From a template/ })).toHaveAttribute("href", "/server/s1/new?from=template");
    expect(screen.getByRole("link", { name: /Compose file/ })).toHaveAttribute("href", "/server/s1/new?from=compose");
    expect(screen.getByRole("link", { name: /Git repository/ })).toHaveAttribute("href", "/server/s1/new?from=git");
  });

  it("leaves the template out without templates, or when they can't be read", async () => {
    const api = fakeAPI({ "/api/v1/template/get-templates": { templates: [] } });
    const { unmount } = renderIn(<NewAppChoices serverId="s1" />);
    await waitFor(() => expect(api.calls).toHaveLength(1));
    expect(screen.queryByRole("link", { name: /From a template/ })).not.toBeInTheDocument();
    expect(screen.getAllByRole("link")).toHaveLength(2);
    unmount();
    vi.unstubAllGlobals();

    const failing = fakeAPI({ "/api/v1/template/get-templates": new Error("down") });
    renderIn(<NewAppChoices serverId="s1" />);
    await waitFor(() => expect(failing.calls).toHaveLength(1));
    expect(screen.queryByRole("link", { name: /From a template/ })).not.toBeInTheDocument();
  });
});

describe("NewAppButton", () => {
  it("opens the chooser and closes it on a pick", async () => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/template/get-templates": { templates: ONE } });
    renderIn(<NewAppButton serverId="s1" />);
    await user.click(screen.getByRole("button", { name: "New app" }));
    const dialog = await screen.findByRole("dialog", { name: "New app" });
    expect(dialog).toHaveTextContent("How do you want to start?");
    await user.click(screen.getByRole("link", { name: /Compose file/ }));
    expect(screen.getByTestId("at")).toHaveTextContent("/server/s1/new?from=compose");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });
});
```

Create `web/src/pages/new-app.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import NewAppPage from "@/pages/new-app";
import { fakeAPI } from "@/test/fake-api";

vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "s1" }) }));
vi.mock("@/components/new-app/compose-flow", () => ({ ComposeFlow: ({ serverId }: { serverId: string }) => <p>compose form {serverId}</p> }));
vi.mock("@/components/new-app/git-flow", () => ({ GitFlow: ({ serverId }: { serverId: string }) => <p>git form {serverId}</p> }));
vi.mock("@/components/new-app/template-flow", () => ({
  TemplateFlow: ({ serverId }: { serverId: string }) => <p>template form {serverId}</p>,
}));

const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.fn() } as NotificationsContextValue;

function renderAt(url: string) {
  fakeAPI({ "/api/v1/template/get-templates": { templates: [] } });
  return render(
    <MemoryRouter initialEntries={[url]}>
      <NotificationsContext.Provider value={notifications}>
        <Routes>
          <Route path="/server/s1/new" element={<NewAppPage />} />
        </Routes>
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
}

afterEach(() => vi.unstubAllGlobals());

describe("NewAppPage", () => {
  it.each(["/server/s1/new", "/server/s1/new?from=nonsense"])("shows the chooser at %s", async (url) => {
    renderAt(url);
    expect(screen.getByRole("heading", { level: 1, name: "New app" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Apps" })).toHaveAttribute("href", "/server/s1");
    expect(await screen.findByRole("link", { name: /Compose file/ })).toBeInTheDocument();
    expect(document.title).toContain("New app");
  });

  it.each([
    ["compose", "New app from a compose file", "compose form s1"],
    ["git", "New app from Git", "git form s1"],
    ["template", "New app from a template", "template form s1"],
  ])("opens the %s form", (from, title, body) => {
    renderAt(`/server/s1/new?from=${from}`);
    expect(screen.getByRole("heading", { level: 1, name: title })).toBeInTheDocument();
    expect(screen.getByText(body)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "New app" })).toHaveAttribute("href", "/server/s1/new");
  });
});
```

In `web/src/routes.test.tsx`:

1. Replace the two lines

```tsx
vi.mock("@/pages/create-app", () => ({ default: () => <p>compose page</p> }));
vi.mock("@/pages/create-from-template", () => ({ default: () => <p>from template page</p> }));
```

with

```tsx
vi.mock("@/pages/new-app", () => ({
  default: function NewAppStub() {
    return <p>new app page {new URLSearchParams(useLocation().search).get("from") ?? "chooser"}</p>;
  },
}));
```

2. In the table of pages by path (phase 1's `["/server/s1/new", "compose page"]`, `["/server/s1/new?from=compose", "compose page"]`, `["/server/s1/new?from=template", "from template page"]`), the expectations become `"new app page chooser"`, `"new app page compose"`, `"new app page template"`; add `["/server/s1/new?from=git", "new app page git"]`.
3. In the redirect table, the two rows become:

```tsx
    ["/server/s1/create-app", "/server/s1/new?from=compose", "new app page compose"],
    ["/server/s1/create-app/template", "/server/s1/new?from=template", "new app page template"],
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/components/new-app/new-app-choices.test.tsx src/pages/new-app.test.tsx src/routes.test.tsx`
Expected: FAIL (choices/button missing; the page still renders the old forms; `create-app` still redirects to the chooser).

- [ ] **Step 3: Write the implementation**

Create `web/src/components/new-app/new-app-choices.tsx`:

```tsx
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { FileCode, GitBranch, LayoutTemplate, type LucideIcon } from "lucide-react";

import { useTemplatesApi } from "@/hooks/use-templates-api";
import { newAppPath, type NewAppSource } from "@/lib/routes";

const CHOICES: readonly { from: NewAppSource; title: string; text: string; icon: LucideIcon }[] = [
  { from: "template", title: "From a template", text: "Fill in a few values. The template brings the rest.", icon: LayoutTemplate },
  { from: "compose", title: "Compose file", text: "Write or paste a Docker Compose file.", icon: FileCode },
  { from: "git", title: "Git repository", text: "Run a repository's compose file and redeploy on new commits.", icon: GitBranch },
];

// NewAppChoices are the ways to start a new app, as link cards. "From a
// template" shows only when the organization has a template (a failed read
// counts as none).
export function NewAppChoices({ serverId, onPick }: { serverId: string; onPick?: () => void }) {
  const api = useTemplatesApi();
  const [templates, setTemplates] = useState(0);
  useEffect(() => {
    let live = true;
    api.listTemplates().then(
      (list) => live && setTemplates(list.length),
      () => live && setTemplates(0),
    );
    return () => {
      live = false;
    };
  }, [api]);

  const shown = CHOICES.filter((c) => c.from !== "template" || templates > 0);
  return (
    <ul aria-label="Ways to start" className="grid gap-3 sm:grid-cols-[repeat(auto-fit,minmax(12rem,1fr))]">
      {shown.map((c) => (
        <li key={c.from}>
          <Link
            to={newAppPath(serverId, c.from)}
            onClick={onPick}
            className="flex h-full min-h-24 flex-col gap-2 rounded-xl border bg-card p-4 transition-colors duration-150 hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none motion-reduce:transition-none"
          >
            <c.icon className="size-5 text-primary" aria-hidden />
            <span className="font-medium">{c.title}</span>
            <span className="text-sm text-muted-foreground">{c.text}</span>
          </Link>
        </li>
      ))}
    </ul>
  );
}
```

Create `web/src/components/new-app/new-app-button.tsx`:

```tsx
import { useState } from "react";
import { Plus } from "lucide-react";

import { Button } from "@/components/ui/button";
import { NewAppChoices } from "@/components/new-app/new-app-choices";
import { ResponsiveDialog } from "@/components/responsive-dialog";

// NewAppButton is the Apps page's primary action: it opens the ways to
// start (a dialog; a bottom sheet on phones), each leading to its form.
export function NewAppButton({ serverId, className }: { serverId: string; className?: string }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button className={className} onClick={() => setOpen(true)}>
        <Plus className="size-4" /> New app
      </Button>
      <ResponsiveDialog open={open} onOpenChange={setOpen} title="New app" description="How do you want to start?">
        {open ? <NewAppChoices serverId={serverId} onPick={() => setOpen(false)} /> : null}
      </ResponsiveDialog>
    </>
  );
}
```

Replace `web/src/pages/new-app.tsx` with:

```tsx
import { useSearchParams } from "react-router-dom";

import { PageHeader } from "@/components/page-header";
import { ComposeFlow } from "@/components/new-app/compose-flow";
import { GitFlow } from "@/components/new-app/git-flow";
import { NewAppChoices } from "@/components/new-app/new-app-choices";
import { TemplateFlow } from "@/components/new-app/template-flow";
import { useServers } from "@/context/use-servers";
import { parseNewAppSource } from "@/lib/new-app";
import { appsPath, newAppPath, type NewAppSource } from "@/lib/routes";

const TITLES: Record<NewAppSource, string> = {
  template: "New app from a template",
  compose: "New app from a compose file",
  git: "New app from Git",
};

// NewAppPage is /server/:id/new: ?from=template|compose|git opens that
// form; anything else shows the ways to start (the same cards the Apps
// page's New app opens).
export default function NewAppPage() {
  const [params] = useSearchParams();
  const { activeServerId } = useServers();
  const serverId = activeServerId ?? "";
  const from = parseNewAppSource(params.get("from"));
  return (
    <div className="mx-auto w-full max-w-3xl space-y-6">
      <PageHeader
        title={from ? TITLES[from] : "New app"}
        subtitle={from ? undefined : "How do you want to start?"}
        back={from ? { to: newAppPath(serverId), label: "New app" } : { to: appsPath(serverId), label: "Apps" }}
      />
      {from === "template" ? (
        <TemplateFlow serverId={serverId} />
      ) : from === "compose" ? (
        <ComposeFlow serverId={serverId} />
      ) : from === "git" ? (
        <GitFlow serverId={serverId} />
      ) : (
        <NewAppChoices serverId={serverId} />
      )}
    </div>
  );
}
```

In `web/src/routes.tsx`, change the legacy redirect `{ path: "create-app", element: <LegacyRedirect to={({ params }) => newAppPath(params.serverId ?? "")} /> }` to `newAppPath(params.serverId ?? "", "compose")` (the old address was the compose editor).

Delete the old pages:

```bash
git rm web/src/pages/create-app.tsx web/src/pages/create-from-template.tsx web/src/pages/create-from-template.test.tsx
```

Then `grep -rn "create-app\"\|create-from-template\|CreateAppPage\|CreateFromTemplatePage" web/src` must show only the legacy `path: "create-app"` entries in `web/src/routes.tsx` (and their rows in `routes.test.tsx`).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/components/new-app src/pages/new-app.test.tsx src/routes.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/components/new-app/new-app-choices.tsx web/src/components/new-app/new-app-button.tsx web/src/components/new-app/new-app-choices.test.tsx web/src/pages/new-app.tsx web/src/pages/new-app.test.tsx web/src/routes.tsx web/src/routes.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): New app chooser and one page for the three create forms

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

(`git rm` above already staged the deletions.)

---

### Task 9: The Configuration tab shows only what runs

**Files:**
- Modify: `web/src/components/app-editor.tsx` (final layout)
- Modify: `web/src/components/app-editor.test.tsx`
- Modify: `web/src/components/app-editor-panel.tsx` (field messages, `headerActions`, nameless app)
- Modify: `web/src/components/app-editor-panel.test.tsx`
- Modify: `web/src/lib/app-editor-io.ts` (remove `validateEditorState`)

**Interfaces:**
- Consumes: everything from Tasks 1–3; `Disclosure` (phase 1); `appSub` (phase 1); `useServerPath` (existing).
- Produces:
  - `AppEditor({ state, onChange, errors? })` — sections in order: **Files** (no image chips), **Variables**, **Domains** (address rows; on a server without the proxy, one sentence), **Git source** when the app has one (else a closed "Deploy from Git" disclosure holding the switch), then a closed **Advanced** disclosure: **Redirects** (proxy only) and **Image versions** (chips per compose file). No Name/Icon/Colour/Description.
  - `AppEditorPanel({ appId: string; headerActions?: ReactNode })` — `headerActions` render in the sticky bar right before Discard / Save draft / Save & redeploy.
- Step 0 (before Step 1): run `grep -n "headerActions\|title=\"Details\"" web/src/components/app-editor-panel.tsx web/src/components/app-editor.tsx`. If phase 2 already added `headerActions` to `AppEditorPanel`, keep its prop and placement and skip that part of Step 3; if phase 2 already removed the Details section, skip that part too. Find phase 2's History button: `grep -rn "History" web/src/pages/app-details.tsx`. If it is rendered next to (not inside) `<AppEditorPanel>`, move that element into `headerActions={…}` in the same commit.

- [ ] **Step 1: Write the failing tests**

Replace `web/src/components/app-editor.test.tsx` with:

```tsx
import { useState } from "react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AppEditor } from "@/components/app-editor";
import { emptyEditorState, newGitSource } from "@/lib/app-editor-io";
import type { FieldErrors } from "@/lib/field-errors";
import { fakeAPI } from "@/test/fake-api";
import type { AppEditorState } from "@/types/app-config";

let servers: { activeServer: { id: string; features?: Record<string, boolean> } | null; activeServerId?: string; loading: boolean };
vi.mock("@/context/use-servers", () => ({ useServers: () => servers }));
vi.mock("@/hooks/use-shared-names", () => ({ useSharedNames: () => [] }));
vi.mock("@/components/code-editor", () => ({ CodeEditor: () => <textarea aria-label="code" /> }));
vi.mock("@/components/image-tag-picker", () => ({
  ImageTagPicker: ({ image }: { image: string }) => <button>tags of {image}</button>,
}));

function Harness({ initial, errors }: { initial: AppEditorState; errors?: FieldErrors }) {
  const [state, setState] = useState(initial);
  return (
    <MemoryRouter>
      <AppEditor state={state} onChange={setState} errors={errors} />
      <output aria-label="state">{JSON.stringify(state.config)}</output>
    </MemoryRouter>
  );
}
const config = () => JSON.parse(screen.getByRole("status", { name: "state" }).textContent ?? "{}");

beforeEach(() => {
  servers = { activeServer: null, loading: true };
});
afterEach(() => vi.unstubAllGlobals());

describe("AppEditor storage variables", () => {
  it("lists the referenced storage variables read-only, secrets masked", () => {
    const state: AppEditorState = {
      ...emptyEditorState(),
      storageVariables: [
        { name: "WF_STORAGE_HETZNER_BUCKET", storage: "hetzner", secret: false },
        { name: "WF_STORAGE_HETZNER_SECRET_ACCESS_KEY", storage: "hetzner", secret: true },
      ],
    };
    render(<Harness initial={state} />);
    const rows = within(screen.getByRole("list", { name: "Storage variables" })).getAllByRole("listitem");
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent("WF_STORAGE_HETZNER_BUCKET");
    expect(rows[0]).toHaveTextContent("provided by storage hetzner");
    expect(rows[0]).not.toHaveTextContent("<encrypted>");
    expect(rows[1]).toHaveTextContent("<encrypted>");
    expect(within(rows[0]).queryByRole("textbox")).not.toBeInTheDocument();
    expect(screen.queryByText("No variables yet.")).not.toBeInTheDocument();
  });

  it("keeps the empty state without any variable", () => {
    render(<Harness initial={emptyEditorState()} />);
    expect(screen.getByText("No variables yet.")).toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "Storage variables" })).not.toBeInTheDocument();
  });
});

describe("AppEditor", () => {
  it("shows only what runs: no name, icon, colour or description", () => {
    render(<Harness initial={emptyEditorState()} />);
    expect(screen.getByRole("heading", { name: "Files" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Variables" })).toBeInTheDocument();
    expect(screen.queryByLabelText("Name")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Description")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Color")).not.toBeInTheDocument();
  });

  it("edits files through the shared files section", async () => {
    const user = userEvent.setup();
    render(<Harness initial={emptyEditorState()} />);
    await user.click(screen.getAllByRole("button", { name: /Add file/ })[0]);
    expect(config().files).toHaveLength(2);
  });

  it("keeps Deploy from Git closed until used, and opens it as the Git source", async () => {
    const user = userEvent.setup();
    render(<Harness initial={emptyEditorState()} />);
    expect(screen.queryByRole("switch", { name: "Deploy from Git" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Deploy from Git" }));
    await user.click(screen.getByRole("switch", { name: "Deploy from Git" }));
    expect(screen.getByRole("heading", { name: "Git source" })).toBeInTheDocument();
    expect(screen.getByLabelText("Repository URL")).toBeInTheDocument();
    expect(config().source).toEqual(newGitSource());
  });

  it("shows an app's Git source up front", () => {
    const s = emptyEditorState();
    render(<Harness initial={{ ...s, config: { ...s.config, source: { ...newGitSource(), repo_url: "https://x.test/r" } } }} />);
    expect(screen.getByLabelText("Repository URL")).toHaveValue("https://x.test/r");
    expect(screen.getByRole("switch", { name: "Deploy from Git" })).toBeChecked();
  });

  it("keeps image versions under Advanced", async () => {
    const user = userEvent.setup();
    const s = emptyEditorState();
    render(<Harness initial={s} />);
    expect(screen.queryByRole("button", { name: "tags of nginx:alpine" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Advanced" }));
    expect(screen.getByRole("heading", { name: "Image versions" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "tags of nginx:alpine" })).toBeInTheDocument();
  });

  it("says plainly when the server has no built-in proxy", () => {
    servers = { activeServer: { id: "s1", features: {} }, activeServerId: "s1", loading: false };
    render(<Harness initial={emptyEditorState()} />);
    expect(screen.getByRole("heading", { name: "Domains" })).toBeInTheDocument();
    expect(screen.getByText(/no built-in web proxy/)).toBeInTheDocument();
    expect(screen.queryByLabelText("Hostname")).not.toBeInTheDocument();
  });

  it("puts addresses under Domains and redirects under Advanced, opening it for a bad redirect", async () => {
    servers = { activeServer: { id: "s1", features: { ingress: true } }, activeServerId: "s1", loading: false };
    fakeAPI({
      "/api/v1/domain/get-domains": { domains: [] },
      "/api/v1/domains/check": { available: true, covered: true, registration: null, claims: null },
    });
    const s = emptyEditorState();
    const initial: AppEditorState = {
      ...s,
      config: {
        ...s.config,
        ingress: {
          domains: [{ id: "d1", domain: "", upstream_port: "", ssl: true }],
          redirects: [{ id: "r1", domain: "old.example.com", path: "", to: "nope", code: 301, ssl: true }],
        },
      },
    };
    const { rerender } = render(<Harness initial={initial} />);
    expect(screen.getByLabelText("Hostname")).toBeInTheDocument();
    expect(screen.queryByLabelText("Redirect to")).not.toBeInTheDocument();
    rerender(<Harness initial={initial} errors={{ "redirect-to:r1": "Send it to a full address starting with http:// or https://." }} />);
    expect(screen.getByLabelText("Redirect to")).toHaveAccessibleDescription(
      "Send it to a full address starting with http:// or https://.",
    );
  });
});
```

In `web/src/components/app-editor-panel.test.tsx`:

1. Add `MemoryRouter` (`import { MemoryRouter } from "react-router-dom";`) and wrap the rendered tree: `<MemoryRouter><AppsContext.Provider …><AppEditorPanel appId="a1" headerActions={headerActions} /></AppsContext.Provider></MemoryRouter>`; give `renderPanel` a third parameter `headerActions?: React.ReactNode` (import `type ReactNode` from `react`) and a fourth `d: AppDetailPayload = detail`, using `getApp: vi.fn(async () => d)`.
2. In the two existing tests, replace `await user.type(await screen.findByLabelText("Name"), "-2");` with:

```tsx
    await user.click(await screen.findByRole("button", { name: /Add variable/ }));
    await user.type(screen.getByRole("textbox", { name: "Variable name" }), "PORT");
```

3. Append:

```tsx
  it("refuses a nameless file next to it instead of in a toast", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn();
    renderPanel(saveApp);
    await user.click((await screen.findAllByRole("button", { name: /Add file/ }))[0]);
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    const names = screen.getAllByRole("textbox", { name: "File name" });
    expect(names[1]).toHaveAccessibleDescription("Name this file.");
    await waitFor(() => expect(names[1]).toHaveFocus());
    expect(screen.getByRole("alert")).toHaveTextContent("Some fields need attention.");
    expect(saveApp).not.toHaveBeenCalled();
    expect(toast.error).not.toHaveBeenCalled();
  });

  it("sends a nameless app to Settings to get a name", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn();
    const nameless = { ...detail, app: { ...detail.app, config: btoa(JSON.stringify({ files: [{ filename: "compose.yml" }] })) } };
    renderPanel(saveApp, vi.fn(async () => {}), undefined, nameless);
    await user.click(await screen.findByRole("button", { name: /Add variable/ }));
    await user.type(screen.getByRole("textbox", { name: "Variable name" }), "PORT");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    expect(screen.getByRole("alert")).toHaveTextContent("This app has no name yet.");
    expect(screen.getByRole("link", { name: "Name it in Settings" })).toHaveAttribute("href", "/app/a1?tab=settings");
    expect(saveApp).not.toHaveBeenCalled();
  });

  it("shows the page's actions in its bar", async () => {
    renderPanel(vi.fn(), vi.fn(async () => {}), <button>History</button>);
    expect(await screen.findByRole("button", { name: "History" })).toBeInTheDocument();
  });
```

(`useServerPath` without an active server returns the bare sub-path, so the link is `/app/a1?tab=settings` in this test; on the real page it is `/server/<id>/app/a1?tab=settings`.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/components/app-editor.test.tsx src/components/app-editor-panel.test.tsx`
Expected: FAIL (Details still shown; no "Advanced"; the panel still toasts).

- [ ] **Step 3: Write the implementation**

Replace `web/src/components/app-editor.tsx` with:

```tsx
import { useState } from "react";
import { Lock } from "lucide-react";

import { Switch } from "@/components/ui/switch";
import { Disclosure } from "@/components/disclosure";
import { EditorSection } from "@/components/editor-section";
import { FilesSection, ImageChips } from "@/components/files-section";
import { GitSourceFields } from "@/components/git-source-fields";
import { AddressRows, RedirectRows } from "@/components/ingress-editor";
import { SharedVariablesHint } from "@/components/shared-variables-hint";
import { VariablesEditor } from "@/components/variables-editor";
import { useServers } from "@/context/use-servers";
import { useRegistrations } from "@/hooks/use-registrations";
import { useSharedNames } from "@/hooks/use-shared-names";
import { newGitSource } from "@/lib/app-editor-io";
import { imageRefs } from "@/lib/compose-images";
import { ENCRYPTED } from "@/lib/ecies";
import { hasErrorIn, type FieldErrors } from "@/lib/field-errors";
import { isComposeFile, type AppEditorState, type AppSourceConfig, type StorageVariable } from "@/types/app-config";

type Props = {
  state: AppEditorState;
  onChange: (next: AppEditorState) => void;
  // errors are editorErrors' messages, shown by their fields.
  errors?: FieldErrors;
};

// AppEditor is the Configuration tab's body: only what runs — files,
// variables, domains and the Git source; redirects and image versions under
// "Advanced". The app's name, icon, colour and description live in its
// Settings. Secrets are flagged here and encrypted by the save.
export function AppEditor({ state, onChange, errors = {} }: Props) {
  const { activeServer, loading: serversLoading } = useServers();
  const serverId = activeServer?.id ?? "";
  const ingressOn = Boolean(activeServer?.features?.ingress);
  const shared = useSharedNames(activeServer?.id);
  const { registrations, refresh } = useRegistrations(serverId, ingressOn);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const appId = state.config.id || undefined;
  const rows = { state, onChange, appId, serverId, registrations, onRefresh: refresh, errors };
  const imageFiles = state.config.files.filter(
    (f) => isComposeFile(f.filename) && imageRefs(state.files[f.id] ?? "").length > 0,
  );

  return (
    <div>
      <FilesSection
        files={state.config.files}
        contents={state.files}
        onChange={(files, contents) => onChange({ ...state, config: { ...state.config, files }, files: contents })}
        description="Written into the app's folder. Mark a file secret to encrypt it for this server only."
        imageChips={false}
        errors={errors}
      />

      <EditorSection
        title="Variables"
        description={
          <>
            Used in files as <code>{"${NAME}"}</code>, including organization and server variables. Secret values are
            encrypted in your browser.
          </>
        }
      >
        <VariablesEditor
          state={state}
          onChange={onChange}
          shared={shared}
          errors={errors}
          emptyNote={state.storageVariables.length === 0}
        />
        <SharedVariablesHint shared={shared} />
        <StorageVariableRows vars={state.storageVariables} />
      </EditorSection>

      {/* activeServer is null while get-servers is in flight: render
          nothing until it resolves so a server with the proxy doesn't
          flash the "no proxy" sentence. */}
      {serversLoading ? null : (
        <EditorSection
          title="Domains"
          description={
            ingressOn
              ? "Where people reach the app. Each domain sets how HTTPS works."
              : "This server has no built-in web proxy, so domains can't be set here. People reach the app on the port its compose file publishes, or through your own proxy or tunnel."
          }
        >
          {ingressOn ? <AddressRows {...rows} /> : null}
        </EditorSection>
      )}

      <GitSource state={state} onChange={onChange} errors={errors} />

      <Disclosure
        label="Advanced"
        open={advancedOpen || hasErrorIn(errors, "redirect-")}
        onOpenChange={setAdvancedOpen}
        className="border-t pt-4"
      >
        {ingressOn ? (
          <EditorSection title="Redirects" description="Send a hostname, or a path on it, to another address.">
            <RedirectRows {...rows} />
          </EditorSection>
        ) : null}
        <EditorSection title="Image versions" description="Pick another tag for an image a compose file uses.">
          {imageFiles.length === 0 ? (
            <p className="text-sm text-muted-foreground">No images found in the compose files.</p>
          ) : (
            imageFiles.map((f) => (
              <div key={f.id} className="space-y-1">
                <p className="font-mono text-xs text-muted-foreground">{f.filename}</p>
                <div className="overflow-hidden rounded-md border">
                  <ImageChips
                    content={state.files[f.id] ?? ""}
                    onReplace={(oldRef, newRef) =>
                      onChange({ ...state, files: { ...state.files, [f.id]: (state.files[f.id] ?? "").split(oldRef).join(newRef) } })
                    }
                  />
                </div>
              </div>
            ))
          )}
        </EditorSection>
      </Disclosure>
    </div>
  );
}

// GitSource is the app's Git source when it has one; otherwise a closed
// "Deploy from Git" holding the switch that adds one.
function GitSource({ state, onChange, errors }: { state: AppEditorState; onChange: (next: AppEditorState) => void; errors: FieldErrors }) {
  const source = state.config.source;
  const setSource = (next: AppSourceConfig | undefined) => onChange({ ...state, config: { ...state.config, source: next } });
  const toggle = (
    <Switch
      aria-label="Deploy from Git"
      checked={Boolean(source)}
      onCheckedChange={(on) => setSource(on ? (source ?? newGitSource()) : undefined)}
    />
  );
  if (!source) {
    return (
      <Disclosure label="Deploy from Git" className="border-t pt-4">
        <div className="flex items-start justify-between gap-4 rounded-md border p-3">
          <p className="text-sm text-muted-foreground">
            Clone a repository and run its compose file, redeploying on new commits. While this is off, the files above
            are the whole app.
          </p>
          {toggle}
        </div>
      </Disclosure>
    );
  }
  return (
    <EditorSection
      title="Git source"
      description="The repository the app runs. Turn it off to run only the files above."
      inlineAction
      action={toggle}
    >
      <GitSourceFields
        source={source}
        token={state.sourceToken || ""}
        onSource={(patch) => setSource({ ...source, ...patch })}
        onToken={(sourceToken) => onChange({ ...state, sourceToken })}
        errors={errors}
      />
      <p className="text-xs text-muted-foreground">
        No compose file in the repository? Add a root <code>compose.yml</code> under Files — the clone is available at{" "}
        <code>./source</code>.
      </p>
    </EditorSection>
  );
}

// StorageVariableRows lists the WF_STORAGE_ variables the compose files
// reference, read-only: their values come from the server's storages
// (Settings → Storages), and a secret shows only as <encrypted>.
function StorageVariableRows({ vars }: { vars: StorageVariable[] }) {
  if (vars.length === 0) return null;
  return (
    <ul aria-label="Storage variables" className="mt-2 divide-y rounded-md border bg-muted/30">
      {vars.map((v) => (
        <li key={v.name} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-sm">
          <code className="font-mono [overflow-wrap:anywhere]">{v.name}</code>
          {v.secret ? (
            <span className="flex items-center gap-1 font-mono text-xs text-muted-foreground">
              <Lock className="size-3" /> {ENCRYPTED}
            </span>
          ) : null}
          <span className="ml-auto text-xs text-muted-foreground">
            provided by storage <code className="font-mono">{v.storage}</code>
          </span>
        </li>
      ))}
    </ul>
  );
}
```

In `web/src/components/app-editor-panel.tsx`:

1. Imports: `import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";`, `import { Link } from "react-router-dom";`, `import { useServerPath } from "@/context/use-server-path";`, `import { focusFirstInvalid, type FieldErrors } from "@/lib/field-errors";`, `import { appSub } from "@/lib/routes";`; in the `@/lib/app-editor-io` import replace `validateEditorState` with `editorErrors`.
2. Signature and new state, right after the existing `useState`s:

```tsx
// AppEditorPanel is the Configuration tab of a plain app: it loads the
// app's current version, edits what runs and saves in place (app.save with
// the app id → new version + redeploy). headerActions (the page's History
// button) sit in its bar.
export function AppEditorPanel({ appId, headerActions }: { appId: string; headerActions?: ReactNode }) {
```

```tsx
  // tried: a save was refused, so every field shows its message (and
  // clears it as it is fixed).
  const [tried, setTried] = useState(false);
  const form = useRef<HTMLDivElement>(null);
  const toServer = useServerPath();
```

3. In `load`, after `setBaseline(JSON.stringify(loaded));` add `setTried(false);`.
4. Replace the start of `handleSave` (the `validateEditorState` block) with:

```tsx
    if (Object.keys(editorErrors(state)).length > 0) {
      setTried(true);
      focusFirstInvalid(form.current);
      return;
    }
```

5. Before `if (loading) return <PageSpinner />;` add:

```tsx
  const errors: FieldErrors = tried ? editorErrors(state) : {};
  const invalid = Object.keys(errors).length > 0;
```

(Plain computation, not a hook: it runs after the early `useMemo` and before the early returns are fine either way since it calls no hook.)

6. Replace the `<p className="mr-auto text-sm text-muted-foreground">…</p>` in the bar with:

```tsx
        <p className="mr-auto text-sm text-muted-foreground">
          {errors.name ? (
            <span role="alert" className="text-destructive">
              This app has no name yet.{" "}
              <Link to={toServer(appSub(appId, "settings"))} className="font-medium underline underline-offset-4">
                Name it in Settings
              </Link>{" "}
              first.
            </span>
          ) : invalid ? (
            <span role="alert" className="text-destructive">
              Some fields need attention.
            </span>
          ) : dirty ? (
            <span className="inline-flex items-center gap-2 text-foreground">
              <span className="size-2 rounded-full bg-status-warn" aria-hidden />
              Unsaved changes
            </span>
          ) : (
            "Every save is kept in History, so you can roll back."
          )}
        </p>
        {headerActions}
```

(`bg-amber-400` becomes the `bg-status-warn` token.)

7. Replace `<AppEditor state={state} onChange={setState} />` with:

```tsx
      <div ref={form}>
        <AppEditor state={state} onChange={setState} errors={errors} />
      </div>
```

8. Change the Save draft button's `title` to `"Save a new version without redeploying"`.

In `web/src/lib/app-editor-io.ts`, delete `validateEditorState` (no caller is left: `grep -rn validateEditorState web/src` prints nothing).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/components/app-editor.test.tsx src/components/app-editor-panel.test.tsx src/lib/app-editor-io.test.ts src/pages/app-details.test.tsx`
Expected: PASS

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/components/app-editor.tsx web/src/components/app-editor.test.tsx web/src/components/app-editor-panel.tsx web/src/components/app-editor-panel.test.tsx web/src/lib/app-editor-io.ts
git commit -m "$(cat <<'EOF'
feat(web): Configuration tab shows only what runs, with field messages

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

(If Step 0 moved phase 2's History button into `headerActions`, add the app page file to this commit.)

---

### Task 10: Template-managed apps: parameters and "A newer version is available"

**Files:**
- Modify: `web/src/components/instance-panel.tsx`
- Create: `web/src/components/instance-panel.test.tsx`
- Modify: the app page's Configuration switch (phase 2; `web/src/pages/app-details.tsx` unless moved — `grep -rn "<InstancePanel" web/src --include=*.tsx | grep -v test`)
- Modify: `web/src/pages/app-details.test.tsx` (one new case)

**Interfaces:**
- Consumes: `templatePath` (phase 1); phase 2's page-level `openUpgrade()` and History button (see "Interfaces assumed").
- Produces: `InstancePanel({ instance, current, onSaved, headerActions?: ReactNode, latestRevision?: number, onUpgrade?: () => void })` — copy says "version N" (not "rN") and "detach" (not "unlink"); when `latestRevision > instance.revision` and `onUpgrade` is given, a status row "A newer version of {template} is available (version N)." with an outline **Update** button calling `onUpgrade`. The page's `ConfigurationTab` (or phase 2's equivalent) takes `onUpgrade: () => void` and passes `latestRevision={ownership.current.latest_revision}` and `onUpgrade` to `InstancePanel`.
- Step 0: `grep -n "headerActions" web/src/components/instance-panel.tsx` — if phase 2 already added it, keep it and only add `latestRevision`/`onUpgrade`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/instance-panel.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { InstancePanel } from "@/components/instance-panel";
import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import type { InstanceView, TemplateView } from "@/lib/templates";

vi.mock("@/components/code-editor", () => ({
  CodeEditor: ({ value, filename }: { value: string; filename?: string }) => <pre aria-label={`Content of ${filename}`}>{value}</pre>,
}));

const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.fn() } as NotificationsContextValue;

const INSTANCE: InstanceView = {
  key: "k1",
  server_id: "s",
  app_id: "a1",
  template_id: "t1",
  template_name: "twillingate",
  revision: 2,
  parameters: { NAME: "bakomi" },
  secrets: null,
  state: "created",
  status_code: 1,
  last_error: "",
  created_at: "",
  updated_at: "",
};

const CURRENT: TemplateView = {
  id: "t1",
  name: "twillingate",
  latest_revision: 3,
  revision: 2,
  body: { files: { "compose.yaml": "services: {}\n" }, parameters: [{ name: "NAME" }], app_name: "${NAME}", icon: "", color: "", ingress: null, backups: null },
  note: "",
  created_at: "",
  created_by: "",
  revisions: null,
};

function renderPanel(props: Partial<Parameters<typeof InstancePanel>[0]> = {}) {
  return render(
    <MemoryRouter>
      <NotificationsContext.Provider value={notifications}>
        <InstancePanel instance={INSTANCE} current={CURRENT} onSaved={vi.fn()} {...props} />
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
}

describe("InstancePanel", () => {
  it("names the template and its version in plain words, with the page's actions", () => {
    renderPanel({ headerActions: <button>History</button> });
    expect(screen.getByText(/Managed by template/)).toHaveTextContent("Managed by template twillingate (version 2).");
    expect(screen.getByRole("link", { name: "twillingate" })).toHaveAttribute("href", "/templates/t1");
    expect(screen.getByText(/detach the app/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "History" })).toBeInTheDocument();
  });

  it("offers a newer version of the template", async () => {
    const user = userEvent.setup();
    const onUpgrade = vi.fn();
    renderPanel({ latestRevision: 3, onUpgrade });
    expect(screen.getByRole("status")).toHaveTextContent("A newer version of twillingate is available (version 3).");
    await user.click(screen.getByRole("button", { name: "Update" }));
    expect(onUpgrade).toHaveBeenCalled();
  });

  it("says nothing about versions when the app is on the latest", () => {
    renderPanel({ latestRevision: 2, onUpgrade: vi.fn() });
    expect(screen.queryByRole("button", { name: "Update" })).not.toBeInTheDocument();
  });
});
```

Append to the `describe("AppDetailsPage of a template instance", …)` block of `web/src/pages/app-details.test.tsx` (helpers `instanceAPI`, `managed`, `renderPage` as they exist after phase 2; if phase 2 renamed them, use its equivalents; use the tab value phase 2 uses — `configuration`):

```tsx
  it("offers the newer template version from the Configuration tab", async () => {
    const user = userEvent.setup();
    instanceAPI();
    renderPage("/app/a1?tab=configuration", {}, managed);
    const note = await screen.findByText(/A newer version of twillingate is available/);
    expect(note).toHaveTextContent("(version 3)");
    await user.click(within(screen.getByRole("status")).getByRole("button", { name: "Update" }));
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
  });
```

(If the page renders more than one `role="status"` element, select the banner with `screen.getByText(/A newer version of/).closest('[role="status"]')` instead.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/components/instance-panel.test.tsx src/pages/app-details.test.tsx`
Expected: FAIL (old copy "r2", no banner).

- [ ] **Step 3: Write the implementation**

In `web/src/components/instance-panel.tsx`:

1. Imports: `import { useMemo, useState, type ReactNode } from "react";`, `import { ArrowUpCircle, FileText } from "lucide-react";`, and `templatePath` from `@/lib/routes` (phase 1 already uses it here).
2. Props:

```tsx
export function InstancePanel({
  instance,
  current,
  onSaved,
  headerActions,
  latestRevision,
  onUpgrade,
}: {
  instance: InstanceView;
  current: TemplateView;
  onSaved: () => void;
  // headerActions sit in the bar before Save (the page's History button).
  headerActions?: ReactNode;
  // latestRevision is the template's newest version; onUpgrade opens the
  // page's update dialog.
  latestRevision?: number;
  onUpgrade?: () => void;
}) {
```

and update the doc comment above to: "InstancePanel is the Configuration tab of an app a template manages: the template's files read-only, a form of its values whose Save re-renders and redeploys the app, and — when the template has a newer version — an Update that opens the page's update dialog."
3. After `const dirty = …` add `const newer = onUpgrade !== undefined && latestRevision !== undefined && latestRevision > instance.revision;`.
4. Replace the bar's `<p>` and add `headerActions`:

```tsx
      <StickyActionBar className="mb-6">
        <p className="mr-auto text-sm text-muted-foreground">
          Managed by template{" "}
          <Link to={templatePath(instance.template_id)} className="font-medium text-foreground hover:underline">
            {instance.template_name}
          </Link>{" "}
          (version {instance.revision}). Change its files in the template, or detach the app from it in Settings.
        </p>
        {headerActions}
        <Button onClick={() => void save()} disabled={saving || (!dirty && instance.state === "created")}>
          {saving ? "Saving…" : "Save & redeploy"}
        </Button>
      </StickyActionBar>
      {newer ? (
        <div role="status" className="mb-6 flex flex-wrap items-center gap-3 rounded-lg border bg-muted/40 px-4 py-3 text-sm">
          <ArrowUpCircle className="size-4 shrink-0 text-primary" aria-hidden />
          <span className="mr-auto">
            A newer version of {instance.template_name} is available (version {latestRevision}).
          </span>
          <Button size="sm" variant="outline" onClick={onUpgrade}>
            Update
          </Button>
        </div>
      ) : null}
```

5. The Parameters section description becomes "The values this app was created with. Secret values are encrypted in your browser for this server."; the Files section description becomes "Read-only: the template's files, as the app runs them."

In the app page's Configuration switch (today's `ConfigurationTab` in `web/src/pages/app-details.tsx`): add `onUpgrade: () => void` to its props, pass it from the page as `onUpgrade={() => void openUpgrade()}`, and on its `<InstancePanel …>` add

```tsx
        latestRevision={ownership.current.latest_revision}
        onUpgrade={onUpgrade}
```

keeping phase 2's `headerActions` on both panels.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/components/instance-panel.test.tsx src/pages/app-details.test.tsx`
Expected: PASS (the existing instance tests match `/Managed by template/`, which stays).

- [ ] **Step 5: Lint, type-check, commit**

```bash
pnpm --dir web run lint && pnpm --dir web exec tsc -b
git add web/src/components/instance-panel.tsx web/src/components/instance-panel.test.tsx web/src/pages/app-details.tsx web/src/pages/app-details.test.tsx
git commit -m "$(cat <<'EOF'
feat(web): template-managed Configuration offers the newer template version

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 11: New app opens the chooser; phase verification

**Files:**
- Modify: the Apps page (phase 2; find its New app control with `grep -rn "newAppPath\|newAppSub" web/src/pages --include=*.tsx | grep -v test`)
- Modify: that page's test

**Interfaces:**
- Consumes: `NewAppButton` (Task 8).
- Produces: nothing new.

- [ ] **Step 1: Write the failing test**

In the Apps page's test (phase 2's; today `web/src/pages/home.test.tsx`), replace the assertion that **New app** navigates to `/server/s1/new` (or add, if there is none) with:

```tsx
  it("opens the ways to start from New app", async () => {
    const user = userEvent.setup();
    // render the Apps page as the file's other tests do (same helper)
    await user.click(await screen.findByRole("button", { name: "New app" }));
    const dialog = await screen.findByRole("dialog", { name: "New app" });
    expect(within(dialog).getByRole("link", { name: /Compose file/ })).toHaveAttribute("href", "/server/s1/new?from=compose");
  });
```

The page renders `NewAppChoices`, which reads `get-templates` through `useTemplatesApi`: the test tree needs a `NotificationsContext.Provider` (as in `new-app-choices.test.tsx`) and `fakeAPI` answering `"/api/v1/template/get-templates": { templates: [] }` alongside the page's own routes. Empty-state links ("From a template" / "Compose file" in "Run your first app") stay plain links and keep their tests.

- [ ] **Step 2: Run the test to verify it fails**

Run: `pnpm --dir web exec vitest run src/pages/home.test.tsx` (or phase 2's Apps page test file)
Expected: FAIL (New app is a link, no dialog).

- [ ] **Step 3: Write the implementation**

In the Apps page, replace the primary New app control (a `Button`/`Link` to `newAppPath(serverId)` or `toServer(newAppSub())` inside `PageHeader`'s `actions`) with:

```tsx
<NewAppButton serverId={serverId} />
```

where `serverId` is the id the page already uses for its links (`activeServerId`), and import `NewAppButton` from `@/components/new-app/new-app-button`. Remove imports that become unused (`Plus`, `newAppPath`/`newAppSub`) — `tsc -b` lists them.

- [ ] **Step 4: Run the test to verify it passes**

Run: `pnpm --dir web exec vitest run src/pages/home.test.tsx`
Expected: PASS

- [ ] **Step 5: Verify the whole phase**

```bash
pnpm --dir web exec vitest run
pnpm --dir web run lint
pnpm --dir web exec tsc -b
pnpm --dir web run build && git checkout -- web/dist/.gitkeep
grep -rn "validateEditorState\|toast.error(err)\|CreateAppPage\|CreateFromTemplatePage" web/src   # expect nothing
grep -rn "Revision {\|>r{\| r{" web/src/components/instance-panel.tsx web/src/components/new-app   # expect nothing
git diff --stat main -- api/ internal/ cmd/ pkg/   # expect nothing (frontend only)
```

Diff coverage on changed web lines: `make web-test && go run ./tools/diffcov --base main --min 80 --lcov web/coverage/lcov.info` (≥80%).

Browser check on a throwaway standalone instance (`make build`, run `bin/standalone` with a temp `DATABASE_URL`/`AGENT_DATA_DIR`; or the `run` skill), at 375, 768 and 1280 px, light and dark:
1. Apps → **New app**: dialog (bottom sheet at 375) with the cards; "From a template" only once a template exists.
2. Compose file: type a name → tile changes; pick an icon → it stays when the name changes; Create with an empty name → message under Name, focus there; open Variables, add a nameless one, close it, Create → it reopens; fill and Create → lands on the new app's Overview. At 375 the action bar stays at the bottom while scrolling.
3. Git repository: Private repository? hides the token; Advanced holds path and auto-redeploy; Create → Overview.
4. From a template: cards; required first; More options shows defaults; Will be available at …; Advanced → Version; Create → Overview.
5. App → Configuration: Files, Variables, Domains, Git source/Deploy from Git, Advanced (Redirects, Image versions); no Name/Icon/Colour/Description; History button from phase 2 in the bar; a template-managed app behind its template shows "A newer version … → Update".

Save screenshots for the PR.

- [ ] **Step 6: Commit**

```bash
git add <the Apps page file> <its test file>
git commit -m "$(cat <<'EOF'
feat(web): New app on the Apps page opens the ways to start

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
)"
```

---

## Self-review

**Spec coverage (§4 and the Configuration bullet of §3):**
- Chooser (dialog/bottom sheet; template only when templates exist; each to `/server/:id/new?from=…`) → Task 8 (`NewAppChoices`, `NewAppButton`, inline on `/new` with no or invalid `from`), Task 11 (Apps page).
- Template: cards → form; required values; optional under "More options" with defaults; backups storage only when declared, first storage preselected; "Will be available at …"; version under "Advanced" (latest default); one Create app → Task 7.
- Compose: Name with auto icon + colour editable via an icon button (Task 4), prefilled compose editor, "Add another file", collapsed "Variables (0)", one Address row with "Add another", description/redirects/image chips under "More options" → Task 5.
- Git: Name, Repository URL, Branch `main`, token behind "Private repository?", compose path + auto-redeploy under "Advanced" → Tasks 3 and 6.
- Field-level messages instead of toasts → Task 1 (model), Tasks 2/3 (fields), Tasks 5–7, 9 (forms). Sticky bottom bar on phones → Task 5 (`CreateBar`). After Create → Overview (`appPath(serverId, id)` with the default tab) → Task 5 (`useCreateApp`), Task 7.
- Configuration: only Files, Variables, Domains, Git source; Deploy from Git collapsed unless used; redirects and image chips under "Advanced"; icon/name/colour/description gone → Task 9. History button slot → Task 9/10 (`headerActions`). Template-managed parameter form + "A newer version of X is available → Update" via `UpgradeInstanceDialog` → Task 10.
- Plain words → copy in Tasks 5–10 ("Version N", "detach", "Some fields need attention.").

**Placeholder scan:** every code step carries its code. Tasks 10 and 11 edit phase 2 files whose exact text this plan cannot see; each says what to grep for and the exact element to add or replace.

**Type consistency:** `FieldErrors`/`invalidAttrs`/`FieldMessage` (Task 1) are used with the same names in Tasks 2–9; `IngressRowsProps` (`registrations`, `onRefresh`) match their callers in Tasks 3, 5, 9; `useRegistrations(serverId, enabled)` matches Tasks 3, 5, 9; `NewAppSource` comes from phase 1's `@/lib/routes`; `createdApp(list, before, name)` matches `useCreateApp`; `AppEditorPanel({ appId, headerActions })` and `InstancePanel({ …, headerActions, latestRevision, onUpgrade })` match the "Interfaces assumed" section.

**Review Focus:** each of the five items has a test in its owning task (Tasks 1/5, 5, 7, 7, 9 as listed above).
