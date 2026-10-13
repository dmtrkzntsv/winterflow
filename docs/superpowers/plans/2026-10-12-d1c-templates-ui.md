# D1c: Templates UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The web UI for templates and instances: Org → Templates (list, editor with revisions and instances, per-instance Upgrade, Delete), Server → Add app → From template, the app page of an instance (badge, Parameters form, Upgrade, Unlink) and Settings → Save as template, on top of the D1a API as built.

**Architecture:** One framework-free client (`web/src/lib/templates.ts`) speaks the D1a routes; pure helpers hold the logic the pages share (`template-params.ts`: patterns, previews, upgrade needs, keys; `template-draft.ts`: the editor's state, recipe backup names, Save as template; `line-diff.ts`: revision diffs), each tested directly. Pages and components follow the existing ones: org pages unscoped under `/org/templates`, the From template page inside the server scope, the app page swapping its Configuration tab for an instance panel. Instance commands go without `wait` and await their result over SSE, like every other agent command.

**Tech Stack:** React 19, React Router 7, TypeScript 5.9 (strict, `verbatimModuleSyntax`, `erasableSyntaxOnly`), Vite 7, Vitest 5 + Testing Library on jsdom, Tailwind 4 with the vendored shadcn/ui components, CodeMirror 6 (`@codemirror/lang-yaml`'s Lezer parser also reads the compose recipe). No new dependency, no Go change.

**Spec:** docs/superpowers/specs/2026-10-11-templates-design.md (§UI, §API, §Errors, §Testing; D1c = "Delivery" item 3)

**Base:** branch `feat/templates-ui` at `11180c9` (stacked on `feat/templates`, PR #20, the D1a API). Every snippet below is against that base plus the earlier tasks. The plan was produced from code that was written and run in a scratch worktree, then re-applied task by task from this document's own snippets to a clean worktree: after every task `tsc -b`, `eslint .` and the task's tests passed, each task's "failing test" step failed as stated, and the final state passed the whole web suite (83 files, 614 tests), `vite build`, and `tools/diffcov` at 98.2% of changed web lines covered.

## Global Constraints

- The spec is binding. Out of scope (D1b, not built yet): **Upgrade all to rN** and the **rollout strip** (template page, server Variables tab, org Variables page). Do not add placeholders for them.
- **No backend change.** The D1a API covers the UI (see "API check" below). If a task finds a gap anyway, stop and report it instead of working around it.
- Request and response shapes are exactly `api/openapi.yaml`'s `Body`, `Parameter`, `IngressRow`, `BackupDefault`, `TemplateSummary`, `TemplateView`, `TemplateRevisionInfo`, `SavedTemplate`, `InstanceView`, `InstanceResult` and the `*InstanceRequest` bodies. Go encodes empty maps and lists as `null`: read them with `?? []` / `?? {}`.
- Secrets never travel in plaintext: a secret value is sent only as ECIES ciphertext (`encryptSecret` from `@/lib/ecies`, with the server's public key read at submit time through `sealSecrets`, never cached) or left out (the agent keeps its stored value). A template never holds a secret value: no secret files, no secret defaults.
- Instance commands are sent without `wait` and awaited over SSE with `INSTANCE_WAIT_MS` (10 minutes): never hold an HTTP request for minutes (proxies and Cloudflare tunnels cut it).
- Instance keys made in the browser come from `newKey()` (`crypto.getRandomValues`); `crypto.randomUUID` and `crypto.subtle` do not exist on plain-http installs.
- CI gate: changed lines at least 80% covered per language: `go run ./tools/diffcov --base origin/main --min 80 --go coverage.out --lcov web/coverage/lcov.info` (this branch is stacked: locally use `--base origin/feat/templates`). Every task's tests cover the lines it adds; `web/src/test/`, `*.test.tsx` and `web/src/main.tsx` are exempt.
- Never delete anything under `web/src/components/ui/` (vendored shadcn/ui, kept on purpose even when unused). Keep `web/dist/.gitkeep` committed: `vite build` empties `web/dist`, so run `git checkout -- web/dist/.gitkeep` after a build.
- Links inside a server's scope go through `useServerPath()` / `serverPath(serverId, sub)` (`/server/:serverId/...`); org pages are unscoped (`/org/templates`, `/org/templates/new`, `/org/templates/:templateId`).
- Tests: `vi.mock("@/context/use-servers", ...)` wherever a component reads servers; mock `@/layouts/use-app-layout` (breadcrumbs), `sonner` (toasts) and `@/lib/ecies` (always include `ENCRYPTED: "<encrypted>"` in that mock); wrap anything that calls `useTemplatesApi` in `NotificationsContext.Provider`; answer fetches with `fakeAPI` from `@/test/fake-api`.
- i18n: only the login/auth strings go through i18next (`web/src/locales/en.ts`, the only locale). Pages and components use plain English literals, like every existing page: add no locale keys.
- Wording and look: "Add …" for adding things (Add template, Add app, Add parameter, Add host, Add file); the existing building blocks (`EditorSection`, `Card`, `Badge`, `InlineError`, `NativeSelect`, `AlertDialog`/`Dialog`) and the muted "Frost" palette (no new colours beyond the existing amber/emerald/red status tints); incremental changes, no redesign; never touch the logo.
- Commits: Conventional Commits (`feat(web): …`, `refactor(web): …`), each ending with the line `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Per task: `pnpm --dir web exec tsc -b`, `pnpm --dir web run lint` and the task's Vitest files must pass before the commit.

## Review Focus

1. **A server without an encryption key yet** (its agent never connected; the API answers "public key unavailable for this server"), or one re-keyed while the page was open. Expected: the form says the server has no key yet and sends nothing; a re-keyed server gets ciphertext for its current key because the key is read at submit. Pinned in Task 1 (`sealSecrets` "explains a server without a key yet", one key read per submit) and Task 8 ("keeps the dialog open with the reason when the server has no key").
2. **A parameter pattern written in RE2 that JavaScript reads differently or not at all** (`[[:alpha:]]+`, `\pL`, `^a\z`, `(?P<x>a)`, `(?U)`). Expected: the browser never blocks a value Go would accept; such a pattern is checked by the API only, and the editor says so; `(?i)` still works as typed. Pinned in Task 2 (`compilePattern`, `valueError` "A pattern JavaScript can't check is the API's to check") and Task 7 (the editor's "The browser can't check this pattern").
3. **Two people save the same template.** Expected: the second save is refused, the page says "Someone saved r4 meanwhile; reload" with the actual revision, Reload loads it, nothing is overwritten. Pinned in Task 7 ("says who saved meanwhile on a concurrent edit, and reloads").
4. **Editing an app a template manages** through the plain editor or History (the app list is stale, or the instance lookup is still running). Expected: the plain editor is never offered while the lookup runs; a save-app or rollback-app refused with 409 explains itself and re-reads the list so the instance view appears. Pinned in Task 10 ("never offers the plain editor while the instance is being looked up", "re-reads the app list when a roll back is refused as managed", `app-editor-panel.test.tsx`).
5. **An instance that never reached `created`** (the deploy failed after the API accepted the create). Expected: retrying on the From template page updates the same key instead of adding a second app; the app page shows the last error, marks the app "Not created yet" and asks for every required secret again. Pinned in Task 9 ("retries a failed deploy as an update of the same key, and a refused create as a create") and Task 10 ("shows a pending instance's failure and asks for its secrets again").

## API check (D1a as built): no gaps

Checked against `internal/app/web/handler/templates/*`, `internal/domain/usecase/templates/*` and `api/openapi.yaml`:

- **List counts:** `GET /api/v1/template/get-templates` returns `instances` (pending and created) and `behind` per template, computed by the repository. Nothing to add.
- **Instances of one template across servers:** `GET /api/v1/instance/get-instances` takes `server_id` and `template_id`, both optional; without `server_id` it lists the caller's org instances on every server (org-scoped, `ListInstances(orgID, "", templateID)`), with live `status_code`s. Nothing to add.
- **Is this app an instance:** `GET /api/v1/app/get-apps` already returns `template_id` per app (`AppWithDomains` embeds `model.App`, whose `TemplateID` the save hook and the reconcile keep, and unlink's save clears). The page then needs two calls, only for such an app: `get-instances?server_id=&template_id=` (matched by `app_id`) and `get-template?id=&revision=` (the instance's revision, which also carries `latest_revision`). The config blob's `instance` marker is not needed.
- **Save as template:** `GET /api/v1/app/get-app` (agent, over SSE) already gives the files, variables (secrets masked), config blob (ingress) and the effective `backups` with `in_recipe`; everything else is built in the browser.
- **Public keys for other servers:** `GET /api/v1/server/get-public-key?server_id=` works for any server the org owns (the Instances tab upgrades instances on several servers).
- **Domain coverage preview:** `GET /api/v1/domains/check?server_id=&domain=` (used by the app editor) answers coverage and claims for a hostname.

## File structure

| File | Responsibility | Task |
|---|---|---|
| `web/src/lib/templates.ts` | API types, the client, `ApiError`, `sealSecrets`, small labels | 1 |
| `web/src/hooks/use-templates-api.ts` | the client bound to the tab's SSE `waitFor` | 1 |
| `web/src/lib/template-params.ts` | pattern checks, value errors, previews, upgrade needs, `newKey` | 2 |
| `web/src/lib/line-diff.ts` | LCS line diff and hunks | 3 |
| `web/src/pages/org-templates.tsx` | Org → Templates list | 4 |
| `web/src/components/files-section.tsx` | the app editor's file editing, shared | 5 |
| `web/src/lib/template-draft.ts` | editor state ↔ body, recipe backup names, checks, Save as template | 6, 11 |
| `web/src/components/template-editor.tsx` | the template form | 7 |
| `web/src/pages/template-page.tsx` | one template: editor, revisions, instances, delete | 7, 8 |
| `web/src/components/parameter-fields.tsx` | a form over parameters (required first, masked secrets) | 8 |
| `web/src/components/upgrade-instance-dialog.tsx` | Upgrade to rN, asking only for new values | 8 |
| `web/src/components/template-revisions.tsx` / `template-instances.tsx` | the two tabs | 8 |
| `web/src/lib/host-check.ts` | `checkHostname`, moved out of `hostname-field.tsx` | 9 |
| `web/src/pages/create-from-template.tsx` | Server → Add app → From template | 9 |
| `web/src/hooks/use-app-instance.ts` | the instance behind an app | 10 |
| `web/src/components/instance-panel.tsx` | Configuration of an instance: Parameters form, read-only files | 10 |
| `web/src/pages/app-details.tsx` (modified) | badge, Upgrade, pending error, Unlink, Save as template | 10, 11 |

---

### Task 1: Templates API client, types and the test helper

**Files:**
- Create: `web/src/lib/templates.ts`
- Create: `web/src/hooks/use-templates-api.ts`
- Modify: `web/src/test/fake-api.ts`
- Test: `web/src/lib/templates.test.ts`

**Interfaces:**
- Consumes: `apiBase` (`@/config`), `readApiError` (`@/lib/api`), `encryptSecret` (`@/lib/ecies`), `Keep` (`@/lib/backups`), `useNotifications` (`@/context/use-notifications`).
- Produces (`@/lib/templates`): types `Parameter`, `IngressRow`, `BackupDefault`, `TemplateBody`, `TemplateSummary`, `TemplateRevisionInfo`, `TemplateView`, `SavedTemplate`, `SaveTemplateBody`, `InstanceState`, `InstanceView`, `InstanceValues`, `CreateInstanceBody`, `UpdateInstanceBody`, `UpgradeInstanceBody`, `WaitFor`, `TemplatesApi`; `INSTANCE_WAIT_MS = 600000`; `class ApiError extends Error { status: number }`; `isManagedByTemplate(message: string): boolean`; `instancesLabel(t: {instances: number; behind: number}): string`; `createTemplatesApi(waitFor: WaitFor, fetchImpl?: typeof fetch): TemplatesApi` with `listTemplates()`, `getTemplate(id, revision?)`, `saveTemplate(body)`, `deleteTemplate(id)`, `listInstances({serverId?, templateId?})`, `getInstance(serverId, key)`, `createInstance(body)`, `updateInstance(body)`, `upgradeInstance(body)`, `unlinkInstance(serverId, key)` (the four commands resolve once the SSE result arrived, throw the agent's error), `publicKey(serverId)`; `sealSecrets(api: Pick<TemplatesApi, "publicKey">, serverId: string, typed: Record<string, string>): Promise<Record<string, string>>`.
- Produces (`@/hooks/use-templates-api`): `useTemplatesApi(): TemplatesApi` (bound to the tab's `waitFor`; needs `NotificationsContext`).
- Produces (`@/test/fake-api`): an `Error` value answers its `status` property (default 400); a function entry gets `(body, query: URLSearchParams)`.

Every route and shape below is the D1a API as built (`internal/app/web/handler/templates`, `api/openapi.yaml`). Go encodes an empty map or list as `null`, so list fields are typed `| null`. Instance commands are sent **without** `wait`: the 202's `request_id` is awaited over SSE for up to 10 minutes, so no HTTP request is held open through a proxy or tunnel (Cloudflare cuts at 100 s). A 200 (a replayed create of a created instance) had nothing to run.

- [ ] **Step 1: Extend the test helper (exempt from coverage) so a test can answer a 409/404 and read the query of a GET:**

In `web/src/test/fake-api.ts`:

Replace:

```ts
// fakeAPI answers fetches by path from data (a value, or a function of the
// request body) and records every call. Unknown paths answer 404.
export function fakeAPI(data: Record<string, unknown>) {
  const calls: Call[] = [];
```

with:

```ts
// fakeAPI answers fetches by path from data (a value, or a function of the
// request body and query) and records every call. Unknown paths answer
// 404; an Error answers 400, or its status when it has one (an ApiError).
export function fakeAPI(data: Record<string, unknown>) {
  const calls: Call[] = [];
```

Replace:

```ts
    }
    const entry = data[u.pathname];
    const value = typeof entry === "function" ? (entry as (b: unknown) => unknown)(body) : entry;
    if (value instanceof Error) {
      return new Response(JSON.stringify({ success: false, message: value.message }), { status: 400 });
    }
    return new Response(JSON.stringify({ success: true, message: "ok", data: value }));
```

with:

```ts
    }
    const entry = data[u.pathname];
    const value =
      typeof entry === "function" ? (entry as (b: unknown, q: URLSearchParams) => unknown)(body, u.searchParams) : entry;
    if (value instanceof Error) {
      const status = (value as Error & { status?: number }).status ?? 400;
      return new Response(JSON.stringify({ success: false, message: value.message }), { status });
    }
    return new Response(JSON.stringify({ success: true, message: "ok", data: value }));
```

- [ ] **Step 2: Write the failing test:**

Create `web/src/lib/templates.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/ecies", () => ({
  ENCRYPTED: "<encrypted>",
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));

import { fakeAPI } from "@/test/fake-api";
import {
  ApiError,
  createTemplatesApi,
  INSTANCE_WAIT_MS,
  instancesLabel,
  isManagedByTemplate,
  sealSecrets,
} from "@/lib/templates";

const ok = vi.fn(async () => ({ status: 0 }));

afterEach(() => {
  vi.unstubAllGlobals();
  ok.mockClear();
});

describe("createTemplatesApi", () => {
  it("reads templates, revisions and instances", async () => {
    const api = fakeAPI({
      "/api/v1/template/get-templates": { templates: null },
      "/api/v1/template/get-template": { id: "t1", revision: 2 },
      "/api/v1/instance/get-instances": { instances: [{ key: "k1" }] },
      "/api/v1/instance/get-instance": { key: "k1" },
    });
    const t = createTemplatesApi(ok);
    expect(await t.listTemplates()).toEqual([]);
    expect(await t.getTemplate("t1", 2)).toEqual({ id: "t1", revision: 2 });
    await t.getTemplate("t1");
    expect(await t.listInstances({ templateId: "t1" })).toEqual([{ key: "k1" }]);
    await t.listInstances({});
    expect(await t.getInstance("s1", "k1")).toEqual({ key: "k1" });
    expect(api.calls.map((c) => c.query)).toEqual([
      "",
      "?id=t1&revision=2",
      "?id=t1",
      "?template_id=t1",
      "",
      "?server_id=s1&key=k1",
    ]);
  });

  it("saves and deletes templates, keeping the status of a refusal", async () => {
    const conflict = new ApiError("someone saved the template meanwhile", 409);
    const api = fakeAPI({
      "/api/v1/template/save-template": (b: unknown) =>
        (b as { base_revision: number }).base_revision === 1 ? conflict : { id: "t1", name: "shop", revision: 3, warnings: null },
      "/api/v1/template/delete-template": null,
    });
    const t = createTemplatesApi(ok);
    const body = { files: {}, parameters: [], app_name: "x", icon: "", color: "", ingress: [], backups: {} };
    expect((await t.saveTemplate({ id: "t1", name: "shop", body, base_revision: 2 })).revision).toBe(3);
    const err = await t.saveTemplate({ id: "t1", name: "shop", body, base_revision: 1 }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(409);
    await t.deleteTemplate("t1");
    expect(api.posted("/api/v1/template/delete-template")).toEqual([{ id: "t1" }]);
  });

  it("waits for an instance command's result over SSE", async () => {
    const fetchImpl = vi.fn(async () =>
      new Response(JSON.stringify({ data: { request_id: "r1" } }), { status: 202 }),
    ) as unknown as typeof fetch;
    const waitFor = vi.fn(async () => ({ status: 0 }));
    const t = createTemplatesApi(waitFor, fetchImpl);
    await t.createInstance({ server_id: "s1", key: "k1", template_id: "t1" });
    expect(waitFor).toHaveBeenCalledWith("r1", INSTANCE_WAIT_MS);

    waitFor.mockResolvedValueOnce({ status: 1, error: "image not found" } as never);
    await expect(t.updateInstance({ server_id: "s1", key: "k1" })).rejects.toThrow("image not found");
    waitFor.mockResolvedValueOnce({ status: 1 } as never);
    await expect(t.upgradeInstance({ server_id: "s1", key: "k1", revision: 2 })).rejects.toThrow(
      "The agent reported a failure",
    );
  });

  it("returns at once when nothing ran, and refuses a 202 without a request id", async () => {
    const api = fakeAPI({ "/api/v1/instance/create-instance": { instance: { key: "k1" }, request_id: "" } });
    const waitFor = vi.fn();
    await createTemplatesApi(waitFor).createInstance({ server_id: "s1", key: "k1", template_id: "t1" });
    expect(waitFor).not.toHaveBeenCalled();
    expect(api.posted("/api/v1/instance/create-instance")).toEqual([{ server_id: "s1", key: "k1", template_id: "t1" }]);

    const bare = vi.fn(async () => new Response(JSON.stringify({ data: {} }), { status: 202 })) as unknown as typeof fetch;
    await expect(createTemplatesApi(waitFor, bare).unlinkInstance("s1", "k1")).rejects.toThrow("No request id");
  });

  it("carries the API's message on a refused instance command", async () => {
    fakeAPI({ "/api/v1/instance/unlink-instance": new ApiError("instance not found", 404) });
    const err = await createTemplatesApi(ok).unlinkInstance("s1", "k1").catch((e: unknown) => e);
    expect((err as ApiError).message).toBe("instance not found");
    expect((err as ApiError).status).toBe(404);
  });
});

describe("isManagedByTemplate", () => {
  it("spots the API's managed-app conflict", () => {
    expect(isManagedByTemplate("this app is managed by template shop: update the instance or unlink it")).toBe(true);
    expect(isManagedByTemplate("name taken")).toBe(false);
  });
});

describe("instancesLabel", () => {
  it("counts instances and the ones behind", () => {
    expect(instancesLabel({ instances: 12, behind: 3 })).toBe("12 instances · 3 behind");
    expect(instancesLabel({ instances: 1, behind: 0 })).toBe("1 instance");
  });
});

describe("sealSecrets", () => {
  it("encrypts only typed secrets, with the key read at that moment", async () => {
    const api = fakeAPI({ "/api/v1/server/get-public-key": { public_key: "K1" } });
    const t = createTemplatesApi(ok);
    expect(await sealSecrets(t, "s1", { A: "x", B: "" })).toEqual({ A: "ct(K1):x" });
    expect(await sealSecrets(t, "s1", { B: "" })).toEqual({});
    expect(api.calls.map((c) => c.query)).toEqual(["?server_id=s1"]);
  });

  it("explains a server without a key yet", async () => {
    fakeAPI({ "/api/v1/server/get-public-key": new Error("public key unavailable for this server") });
    await expect(sealSecrets(createTemplatesApi(ok), "s1", { A: "x" })).rejects.toThrow(
      "This server has no encryption key yet",
    );
  });

  it("passes other failures through", async () => {
    fakeAPI({ "/api/v1/server/get-public-key": new ApiError("forbidden", 403) });
    await expect(sealSecrets(createTemplatesApi(ok), "s1", { A: "x" })).rejects.toThrow("forbidden");
  });
});
```

- [ ] **Step 3: Run the tests**

Run: `pnpm --dir web exec vitest run src/lib/templates.test.ts`
Expected: FAIL: `Failed to resolve import "@/lib/templates"`

- [ ] **Step 4: Write the client:**

Create `web/src/lib/templates.ts`:

```ts
// Templates and their instances: types mirroring the API
// (internal/domain/template, internal/domain/usecase/templates) and the API
// client. A template is org-level; an instance is an app on one server
// rendered from a template revision. Secret parameter values travel only as
// ECIES ciphertexts for the instance's server (lib/ecies); no route ever
// returns one.
import { apiBase } from "@/config";
import { readApiError } from "@/lib/api";
import type { Keep } from "@/lib/backups";
import { encryptSecret } from "@/lib/ecies";

export type Parameter = {
  name: string;
  secret?: boolean;
  required?: boolean;
  // null/absent: no default ("" is a default, but renders the same).
  default?: string | null;
  pattern?: string; // Go RE2
  description?: string;
};

// IngressRow routes host to the host port the compose file publishes; port
// is a number or exactly "${PARAM}".
export type IngressRow = { host: string; service?: string; port: number | string };

export type BackupDefault = { enabled: boolean; storage?: string; every?: string; keep?: Keep | null };

// TemplateBody is a revision's body. Go encodes an empty map or list as
// null.
export type TemplateBody = {
  files: Record<string, string> | null;
  parameters: Parameter[] | null;
  app_name: string;
  icon: string;
  color: string;
  ingress: IngressRow[] | null;
  backups: Record<string, BackupDefault> | null;
};

export type TemplateSummary = {
  id: string;
  name: string;
  latest_revision: number;
  instances: number; // pending and created
  behind: number; // on a revision older than the latest
  updated_at: string;
};

// instancesLabel is a template's "12 instances · 3 behind".
export function instancesLabel(t: Pick<TemplateSummary, "instances" | "behind">): string {
  const n = `${t.instances} ${t.instances === 1 ? "instance" : "instances"}`;
  return t.behind > 0 ? `${n} · ${t.behind} behind` : n;
}

export type TemplateRevisionInfo = { revision: number; note: string; created_at: string; created_by: string };

export type TemplateView = {
  id: string;
  name: string;
  latest_revision: number;
  revision: number;
  body: TemplateBody;
  note: string;
  created_at: string;
  created_by: string;
  revisions: TemplateRevisionInfo[] | null; // newest first
};

export type SavedTemplate = { id: string; name: string; revision: number; warnings: string[] | null };

export type SaveTemplateBody = {
  id?: string;
  name: string;
  body: TemplateBody;
  note?: string;
  base_revision: number; // the latest revision the editor read; 0 for a new template
};

export type InstanceState = "pending" | "created" | "deleted" | "unlinked";

export type InstanceView = {
  key: string;
  server_id: string;
  app_id: string;
  template_id: string;
  template_name: string;
  revision: number;
  parameters: Record<string, string> | null; // values callers set
  secrets: Record<string, string> | null; // name -> "<encrypted>"
  state: InstanceState;
  status_code: number;
  last_error: string;
  created_at: string;
  updated_at: string;
};

// InstanceValues are what create, update and upgrade send: plain values,
// secret ciphertexts and (while pending) backup storages, by name.
export type InstanceValues = {
  parameters?: Record<string, string>;
  secrets?: Record<string, string>;
  backups?: Record<string, string>;
};

export type CreateInstanceBody = InstanceValues & {
  server_id: string;
  key: string;
  template_id: string;
  revision?: number;
};

export type UpdateInstanceBody = InstanceValues & { server_id: string; key: string };

export type UpgradeInstanceBody = UpdateInstanceBody & { revision: number };

// INSTANCE_WAIT_MS bounds the wait for an instance command's result over
// SSE: a deploy may pull an image for minutes (the API's own wait bound).
export const INSTANCE_WAIT_MS = 10 * 60_000;

// ApiError is a refused request with its HTTP status (409: a concurrent
// edit, a key in use, a template with live instances).
export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

async function refusal(res: Response): Promise<ApiError> {
  return new ApiError((await readApiError(res)).message, res.status);
}

// MANAGED_MARKER starts the API's 409 to save-app and rollback-app on an
// app a template instance manages (model.ManagedByTemplate).
const MANAGED_MARKER = "this app is managed by ";

export function isManagedByTemplate(message: string): boolean {
  return message.includes(MANAGED_MARKER);
}

export type WaitFor = (ref: string, timeoutMs?: number) => Promise<{ status?: number; error?: string }>;

export type TemplatesApi = {
  listTemplates(): Promise<TemplateSummary[]>;
  getTemplate(id: string, revision?: number): Promise<TemplateView>;
  saveTemplate(body: SaveTemplateBody): Promise<SavedTemplate>;
  deleteTemplate(id: string): Promise<void>;
  listInstances(filter: { serverId?: string; templateId?: string }): Promise<InstanceView[]>;
  getInstance(serverId: string, key: string): Promise<InstanceView>;
  // The instance commands resolve once the agent's result arrived (or at
  // once when there was nothing to run) and throw its error.
  createInstance(body: CreateInstanceBody): Promise<void>;
  updateInstance(body: UpdateInstanceBody): Promise<void>;
  upgradeInstance(body: UpgradeInstanceBody): Promise<void>;
  unlinkInstance(serverId: string, key: string): Promise<void>;
  // publicKey is the server's ECIES public key, read when a secret is about
  // to be encrypted (never cached: a re-keyed server must not get
  // ciphertexts for its old key).
  publicKey(serverId: string): Promise<string>;
};

export function createTemplatesApi(waitFor: WaitFor, fetchImpl?: typeof fetch): TemplatesApi {
  const doFetch: typeof fetch = fetchImpl ?? ((input, init) => fetch(input, init));
  async function call<T>(path: string, init?: RequestInit): Promise<T> {
    const res = await doFetch(`${apiBase}${path}`, { credentials: "include", ...init });
    if (!res.ok) throw await refusal(res);
    return ((await res.json()) as { data: T }).data;
  }
  const post = (payload: unknown): RequestInit => ({
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  // command posts an instance call without wait: a 202 names the request
  // whose result arrives over SSE; a 200 had nothing to run (a replayed
  // create of a created instance).
  async function command(path: string, payload: unknown): Promise<void> {
    const res = await doFetch(`${apiBase}${path}`, { credentials: "include", ...post(payload) });
    if (!res.ok) throw await refusal(res);
    if (res.status !== 202) return;
    const ref = ((await res.json()) as { data?: { request_id?: string } }).data?.request_id;
    if (!ref) throw new Error("No request id");
    const result = await waitFor(ref, INSTANCE_WAIT_MS);
    if (result.status) throw new Error(result.error || "The agent reported a failure");
  }
  const qs = (params: Record<string, string | undefined>) => {
    const q = new URLSearchParams();
    for (const [k, v] of Object.entries(params)) if (v) q.set(k, v);
    const s = q.toString();
    return s ? `?${s}` : "";
  };
  return {
    listTemplates: () =>
      call<{ templates: TemplateSummary[] | null }>("/api/v1/template/get-templates").then((d) => d.templates ?? []),
    getTemplate: (id, revision) =>
      call<TemplateView>(`/api/v1/template/get-template${qs({ id, revision: revision ? String(revision) : undefined })}`),
    saveTemplate: (body) => call<SavedTemplate>("/api/v1/template/save-template", post(body)),
    deleteTemplate: async (id) => {
      await call<null>("/api/v1/template/delete-template", post({ id }));
    },
    listInstances: ({ serverId, templateId }) =>
      call<{ instances: InstanceView[] | null }>(
        `/api/v1/instance/get-instances${qs({ server_id: serverId, template_id: templateId })}`,
      ).then((d) => d.instances ?? []),
    getInstance: (serverId, key) =>
      call<InstanceView>(`/api/v1/instance/get-instance${qs({ server_id: serverId, key })}`),
    createInstance: (body) => command("/api/v1/instance/create-instance", body),
    updateInstance: (body) => command("/api/v1/instance/update-instance", body),
    upgradeInstance: (body) => command("/api/v1/instance/upgrade-instance", body),
    unlinkInstance: (serverId, key) => command("/api/v1/instance/unlink-instance", { server_id: serverId, key }),
    publicKey: (serverId) =>
      call<{ public_key: string }>(`/api/v1/server/get-public-key${qs({ server_id: serverId })}`).then((d) => d.public_key),
  };
}

// NO_KEY is the API's answer for a server whose agent never reported its key.
const NO_KEY = "public key unavailable";

// sealSecrets encrypts the typed secret values ("" = not given, left out)
// for serverId with its current public key. Without any typed secret it
// reads no key. A server without a key yet gets an explanation instead of
// the API's terse refusal.
export async function sealSecrets(
  api: Pick<TemplatesApi, "publicKey">,
  serverId: string,
  typed: Record<string, string>,
): Promise<Record<string, string>> {
  const given = Object.entries(typed).filter(([, v]) => v !== "");
  if (given.length === 0) return {};
  let key: string;
  try {
    key = await api.publicKey(serverId);
  } catch (e) {
    if (e instanceof Error && e.message.includes(NO_KEY)) {
      throw new Error(
        "This server has no encryption key yet (its agent hasn't connected), so secrets can't be encrypted for it. Try again once it is online.",
      );
    }
    throw e;
  }
  const out: Record<string, string> = {};
  for (const [name, value] of given) out[name] = await encryptSecret(value, key);
  return out;
}
```

- [ ] **Step 5: Write the hook the pages use (covered by the page tests of Tasks 4-11):**

Create `web/src/hooks/use-templates-api.ts`:

```ts
import { useMemo } from "react";

import { useNotifications } from "@/context/use-notifications";
import { createTemplatesApi, type TemplatesApi } from "@/lib/templates";

// useTemplatesApi is the templates client bound to this tab's SSE stream
// (instance commands wait for their result on it).
export function useTemplatesApi(): TemplatesApi {
  const { waitFor } = useNotifications();
  return useMemo(() => createTemplatesApi(waitFor), [waitFor]);
}
```

- [ ] **Step 6: Run the tests**

Run: `pnpm --dir web exec vitest run src/lib/templates.test.ts`
Expected: PASS (10 tests)

- [ ] **Step 7: Check**

Run: `pnpm --dir web exec tsc -b && pnpm --dir web run lint`
Expected: no errors

- [ ] **Step 8: Commit**

```bash
git add web/src/lib/templates.ts \
  web/src/lib/templates.test.ts \
  web/src/hooks/use-templates-api.ts \
  web/src/test/fake-api.ts
git commit -m "feat(web): templates and instances API client" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Parameter values in the browser: patterns, previews, upgrade needs, keys

**Files:**
- Create: `web/src/lib/template-params.ts`
- Test: `web/src/lib/template-params.test.ts`

**Interfaces:**
- Consumes: `InstanceView`, `Parameter`, `TemplateBody` (Task 1).
- Produces (`@/lib/template-params`): `compilePattern(pattern?: string): RegExp | null`; `valueError(p: Parameter, value: string): string`; `fieldErrors(params, values: Record<string,string>, kept?: readonly string[]): Record<string,string>`; `requiredFirst(params): Parameter[]`; `plainValues(params, given): Record<string,string>`; `expand(s, plain): string`; `missingRefs(s, plain): string[]`; `type HostPreview = {host: string; port: string; missing: string[]}`; `previewHosts(body, plain): HostPreview[]`; `upgradeNeeds(target: Parameter[] | null | undefined, inst: InstanceView): Parameter[]`; `newKey(): string` (UUID v4).

Patterns are Go RE2. JavaScript reads most of them the same, but not POSIX classes, `\p` classes, `\z`/`\A`/`\Q`/`\E`/`\C`, `(?P<name>)`, `(?U)` or negated flags: those patterns are left to the API (`compilePattern` returns null and nothing is blocked in the browser). A leading `(?i)`/`(?m)`/`(?s)` becomes the JS flag. `crypto.randomUUID` (like `crypto.subtle`) exists only on secure contexts and self-hosted installs are often plain http, so `newKey` builds a v4 UUID from `crypto.getRandomValues`.

- [ ] **Step 1: Write the failing test:**

Create `web/src/lib/template-params.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import {
  compilePattern,
  expand,
  fieldErrors,
  newKey,
  plainValues,
  previewHosts,
  requiredFirst,
  upgradeNeeds,
  valueError,
} from "@/lib/template-params";
import type { InstanceView, Parameter, TemplateBody } from "@/lib/templates";

const NAME: Parameter = { name: "NAME", required: true, pattern: "^[a-z]{3,20}$" };
const PORT: Parameter = { name: "PORT", required: true, pattern: "^[1-9][0-9]{0,4}$" };
const MEM: Parameter = { name: "MEMORY_MAX", default: "256m" };
const KEY: Parameter = { name: "SESSION_KEY", secret: true, required: true };

describe("compilePattern", () => {
  it("compiles what JavaScript reads like RE2", () => {
    expect(compilePattern("^[a-z]{3,20}$")?.test("shop")).toBe(true);
    expect(compilePattern("(?i)^shop$")?.test("SHOP")).toBe(true);
    expect(compilePattern("(?is)^a.b$")?.test("A\nB")).toBe(true);
  });

  it("leaves RE2-only patterns, and ones JavaScript refuses, to the API", () => {
    for (const p of ["[[:alpha:]]+", "\\pL+", "^a\\z", "(?P<x>a)", "(?U)a+", "(?i-s)a", "(", ""]) {
      expect(compilePattern(p)).toBeNull();
    }
    expect(compilePattern(undefined)).toBeNull();
  });
});

describe("valueError", () => {
  it("checks a value as typed", () => {
    expect(valueError(NAME, "shop")).toBe("");
    expect(valueError(NAME, "Shop!")).toBe("Must match ^[a-z]{3,20}$");
    expect(valueError(NAME, "")).toBe("Required");
    expect(valueError({ name: "X", required: true, default: "a" }, "")).toBe("");
    expect(valueError(MEM, "${HOME}")).toBe("May not contain $");
    // A pattern JavaScript can't check is the API's to check.
    expect(valueError({ name: "X", pattern: "[[:alpha:]]+" }, "123")).toBe("");
  });
});

describe("fieldErrors", () => {
  it("collects each field's problem; a kept secret may stay blank", () => {
    expect(fieldErrors([NAME, MEM, KEY], { NAME: "X" })).toEqual({ NAME: "Must match ^[a-z]{3,20}$", SESSION_KEY: "Required" });
    expect(fieldErrors([NAME, KEY], { NAME: "shop" }, ["SESSION_KEY"])).toEqual({});
    expect(fieldErrors([{ name: "T", secret: true }], {})).toEqual({});
    expect(fieldErrors(null, {})).toEqual({});
  });
});

describe("rendering", () => {
  const body: TemplateBody = {
    files: {},
    parameters: [NAME, PORT, MEM, KEY],
    app_name: "${NAME}",
    icon: "",
    color: "",
    ingress: [{ host: "${NAME}.twillingate.app", port: "${PORT}" }, { host: "static.example.com", port: 8080 }],
    backups: null,
  };

  it("orders required parameters first", () => {
    expect(requiredFirst([MEM, NAME, KEY]).map((p) => p.name)).toEqual(["NAME", "SESSION_KEY", "MEMORY_MAX"]);
    expect(requiredFirst(null)).toEqual([]);
  });

  it("uses given values, then defaults, never secrets", () => {
    expect(plainValues(body.parameters, { NAME: "shop" })).toEqual({ NAME: "shop", PORT: "", MEMORY_MAX: "256m" });
    expect(plainValues(null, {})).toEqual({});
    expect(expand("${NAME}-${NOPE}", { NAME: "shop" })).toBe("shop-");
  });

  it("previews the hosts, naming what is still missing", () => {
    expect(previewHosts(body, plainValues(body.parameters, { NAME: "shop" }))).toEqual([
      { host: "shop.twillingate.app", port: "", missing: ["PORT"] },
      { host: "static.example.com", port: "8080", missing: [] },
    ]);
    expect(previewHosts({ ...body, ingress: null }, {})).toEqual([]);
  });
});

describe("upgradeNeeds", () => {
  const inst = {
    state: "created",
    parameters: { NAME: "shop" },
    secrets: { SESSION_KEY: "<encrypted>" },
  } as unknown as InstanceView;
  const target: Parameter[] = [NAME, MEM, KEY, { name: "REGION", required: true }, { name: "TOKEN", secret: true, required: true }];

  it("asks only for required values the instance doesn't hold", () => {
    expect(upgradeNeeds(target, inst).map((p) => p.name)).toEqual(["REGION", "TOKEN"]);
  });

  it("asks a pending instance for every required secret again", () => {
    expect(upgradeNeeds(target, { ...inst, state: "pending" }).map((p) => p.name)).toEqual([
      "SESSION_KEY",
      "REGION",
      "TOKEN",
    ]);
    expect(upgradeNeeds(null, inst)).toEqual([]);
    expect(upgradeNeeds(target, { ...inst, parameters: null, secrets: null }).map((p) => p.name)).toEqual([
      "NAME",
      "SESSION_KEY",
      "REGION",
      "TOKEN",
    ]);
  });
});

describe("newKey", () => {
  it("is a fresh UUID v4 each time", () => {
    const a = newKey();
    expect(a).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    expect(newKey()).not.toBe(a);
  });
});
```

- [ ] **Step 2: Run the tests**

Run: `pnpm --dir web exec vitest run src/lib/template-params.test.ts`
Expected: FAIL: `Failed to resolve import "@/lib/template-params"`

- [ ] **Step 3: Implement:**

Create `web/src/lib/template-params.ts`:

```ts
// Parameter values in the browser: instant feedback while a value is typed
// (the API checks everything again with Go's RE2), the preview of what a
// value renders into (app name, hosts) and which values an upgrade needs.
import type { InstanceView, Parameter, TemplateBody } from "@/lib/templates";

// RE2_ONLY matches constructs JavaScript reads differently or not at all:
// POSIX classes, \p classes, \z \A \Q \E \C, (?P<name>), ungreedy and
// negated flags. A pattern using one is left to the API.
const RE2_ONLY = /\[\[:|\\[pPzAQEC]|\(\?P<|\(\?[imsU]*(-|U)/;

// compilePattern turns a parameter's RE2 pattern into a JS RegExp, or null
// when JavaScript can't be trusted to agree (then only the API checks it).
// A leading (?i), (?m) or (?s) becomes the matching JS flag.
export function compilePattern(pattern: string | undefined): RegExp | null {
  if (!pattern || RE2_ONLY.test(pattern)) return null;
  let src = pattern;
  let flags = "";
  const lead = /^\(\?([ims]+)\)/.exec(src);
  if (lead) {
    flags = [...new Set(lead[1])].join("");
    src = src.slice(lead[0].length);
  }
  try {
    return new RegExp(src, flags);
  } catch {
    return null;
  }
}

// valueError checks a plain value as typed ("" = not given: the default
// applies). "" means fine; the API has the last word.
export function valueError(p: Parameter, value: string): string {
  if (value.includes("$")) return "May not contain $";
  if (value === "") return p.required && !p.default ? "Required" : "";
  const re = compilePattern(p.pattern);
  if (re && !re.test(value)) return `Must match ${p.pattern}`;
  return "";
}

// fieldErrors are a parameter form's problems by name: each plain value as
// valueError has it, a required secret neither typed nor kept (kept: the
// secrets the agent already holds, which a blank field leaves as they are).
export function fieldErrors(
  params: Parameter[] | null | undefined,
  values: Record<string, string>,
  kept: readonly string[] = [],
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const p of params ?? []) {
    const v = values[p.name] ?? "";
    const err = p.secret ? (p.required && v === "" && !kept.includes(p.name) ? "Required" : "") : valueError(p, v);
    if (err) out[p.name] = err;
  }
  return out;
}

// requiredFirst orders a form: required parameters first, each group in the
// template's order.
export function requiredFirst(params: Parameter[] | null | undefined): Parameter[] {
  const list = params ?? [];
  return [...list.filter((p) => p.required), ...list.filter((p) => !p.required)];
}

// plainValues are the values a render uses: each plain parameter's given
// value, else its default, else "".
export function plainValues(params: Parameter[] | null | undefined, given: Record<string, string>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const p of params ?? []) {
    if (p.secret) continue;
    out[p.name] = given[p.name] || p.default || "";
  }
  return out;
}

const REF = /\$\{([^}]*)\}/g;

// expand substitutes ${NAME} with plain values, as the API renders the app
// name, ingress hosts and ports.
export function expand(s: string, plain: Record<string, string>): string {
  return s.replace(REF, (_, name: string) => plain[name] ?? "");
}

// missingRefs are the parameters s references that have no value yet.
export function missingRefs(s: string, plain: Record<string, string>): string[] {
  return [...s.matchAll(REF)].map((m) => m[1]).filter((name) => !plain[name]);
}

export type HostPreview = { host: string; port: string; missing: string[] };

// previewHosts renders the body's ingress rows with the values so far.
export function previewHosts(body: TemplateBody, plain: Record<string, string>): HostPreview[] {
  return (body.ingress ?? []).map((row) => ({
    host: expand(row.host, plain),
    port: expand(String(row.port), plain),
    missing: [...missingRefs(row.host, plain), ...missingRefs(String(row.port), plain)],
  }));
}

// upgradeNeeds are the parameters of the target revision that need a value
// the instance doesn't hold: a required plain parameter without a default
// or a stored value, and a required secret it was never given. A pending
// instance holds no secret at all: every required secret is asked for.
export function upgradeNeeds(target: Parameter[] | null | undefined, inst: InstanceView): Parameter[] {
  const pending = inst.state === "pending";
  return (target ?? []).filter((p) => {
    if (!p.required) return false;
    if (p.secret) return pending || !(p.name in (inst.secrets ?? {}));
    return !p.default && !inst.parameters?.[p.name];
  });
}

// newKey is the key the browser gives an instance it creates (a UUID v4):
// the API replays a create with the same key instead of making a second
// app. crypto.randomUUID exists only on secure contexts, and self-hosted
// installs are often reached over plain http, so it is built from
// getRandomValues (available everywhere).
export function newKey(): string {
  const b = crypto.getRandomValues(new Uint8Array(16));
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  const h = Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}
```

- [ ] **Step 4: Run the tests**

Run: `pnpm --dir web exec vitest run src/lib/template-params.test.ts`
Expected: PASS (10 tests)

- [ ] **Step 5: Check**

Run: `pnpm --dir web exec tsc -b && pnpm --dir web run lint`
Expected: no errors

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/template-params.ts \
  web/src/lib/template-params.test.ts
git commit -m "feat(web): template parameter checks, previews and instance keys" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Line diff for revisions (no dependency)

**Files:**
- Create: `web/src/lib/line-diff.ts`
- Test: `web/src/lib/line-diff.test.ts`

**Interfaces:**
- Produces (`@/lib/line-diff`): `type DiffLine = {op: " " | "+" | "-"; text: string}`; `diffLines(before: string, after: string): DiffLine[]`; `type Hunk = {skipped: number; lines: DiffLine[]}`; `hunks(lines: DiffLine[], context = 3): Hunk[]`.

**Why no dependency:** the lockfile has no diff package (only `js-yaml` transitively), and adding `diff`/`jsdiff` means a new download, a lockfile change and a license review for ~60 lines of code. Template bodies are at most 1 MiB and files a few hundred lines, so an LCS over the lines between the common head and tail is plenty; past 2 million table cells the changed middle is shown as removed-then-added (correct, coarser).

- [ ] **Step 1: Write the failing test:**

Create `web/src/lib/line-diff.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import { diffLines, hunks } from "@/lib/line-diff";

const ops = (before: string, after: string) => diffLines(before, after).map((l) => l.op + l.text);

describe("diffLines", () => {
  it("marks changed lines between a common head and tail", () => {
    expect(ops("a\nb\nc", "a\nB\nc")).toEqual([" a", "-b", "+B", " c"]);
  });

  it("keeps the common lines of a reordered middle", () => {
    expect(ops("x\na\nb\nc\ny", "x\nb\nc\nd\ny")).toEqual([" x", "-a", " b", " c", "+d", " y"]);
  });

  it("handles empty sides and identical text", () => {
    expect(ops("", "a\nb")).toEqual(["+a", "+b"]);
    expect(ops("a", "")).toEqual(["-a"]);
    expect(ops("a\nb", "a\nb")).toEqual([" a", " b"]);
    expect(ops("", "")).toEqual([]);
  });

  it("falls back to remove-then-add past the table bound", () => {
    const big = (p: string) => Array.from({ length: 1500 }, (_, i) => `${p}${i}`).join("\n");
    const out = diffLines(big("a"), big("b"));
    expect(out.filter((l) => l.op === "-")).toHaveLength(1500);
    expect(out[0].op).toBe("-");
    expect(out[1500].op).toBe("+");
  });
});

describe("hunks", () => {
  it("keeps three lines of context and counts what it skips", () => {
    const before = Array.from({ length: 20 }, (_, i) => `l${i}`).join("\n");
    const after = before.replace("l10", "L10");
    const [h, ...rest] = hunks(diffLines(before, after));
    expect(rest).toEqual([]);
    expect(h.skipped).toBe(7);
    expect(h.lines.map((l) => l.op + l.text)).toEqual([" l7", " l8", " l9", "-l10", "+L10", " l11", " l12", " l13"]);
  });

  it("splits distant changes and has none for equal text", () => {
    const before = Array.from({ length: 30 }, (_, i) => `l${i}`).join("\n");
    const after = before.replace("l2\n", "x\n").replace("l25", "y");
    expect(hunks(diffLines(before, after)).map((h) => h.skipped)).toEqual([0, 16]);
    expect(hunks(diffLines("a", "a"))).toEqual([]);
  });
});
```

- [ ] **Step 2: Run the tests**

Run: `pnpm --dir web exec vitest run src/lib/line-diff.test.ts`
Expected: FAIL: `Failed to resolve import "@/lib/line-diff"`

- [ ] **Step 3: Implement:**

Create `web/src/lib/line-diff.ts`:

```ts
// A line diff for template revisions: a longest-common-subsequence diff of
// the lines between the common head and tail, which is all a template's
// files (at most 1 MiB, usually a few hundred lines) need. No dependency.

export type DiffLine = { op: " " | "+" | "-"; text: string };

// MAX_CELLS bounds the LCS table; past it the changed middle is shown as
// removed then added (still correct, just coarser).
const MAX_CELLS = 2_000_000;

export function diffLines(before: string, after: string): DiffLine[] {
  const a = before === "" ? [] : before.split("\n");
  const b = after === "" ? [] : after.split("\n");
  let head = 0;
  while (head < a.length && head < b.length && a[head] === b[head]) head++;
  let tail = 0;
  while (tail < a.length - head && tail < b.length - head && a[a.length - 1 - tail] === b[b.length - 1 - tail]) tail++;
  const midA = a.slice(head, a.length - tail);
  const midB = b.slice(head, b.length - tail);
  const out: DiffLine[] = a.slice(0, head).map((text) => ({ op: " ", text }));
  out.push(...middle(midA, midB));
  out.push(...a.slice(a.length - tail).map((text) => ({ op: " " as const, text })));
  return out;
}

function middle(a: string[], b: string[]): DiffLine[] {
  const removed = a.map((text) => ({ op: "-" as const, text }));
  const added = b.map((text) => ({ op: "+" as const, text }));
  if (a.length === 0 || b.length === 0 || a.length * b.length > MAX_CELLS) return [...removed, ...added];
  // lcs[i][j] is the LCS length of a[i:] and b[j:].
  const w = b.length + 1;
  const lcs = new Uint32Array((a.length + 1) * w);
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      lcs[i * w + j] = a[i] === b[j] ? lcs[(i + 1) * w + j + 1] + 1 : Math.max(lcs[(i + 1) * w + j], lcs[i * w + j + 1]);
    }
  }
  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      out.push({ op: " ", text: a[i] });
      i++;
      j++;
    } else if (lcs[(i + 1) * w + j] >= lcs[i * w + j + 1]) {
      out.push({ op: "-", text: a[i++] });
    } else {
      out.push({ op: "+", text: b[j++] });
    }
  }
  while (i < a.length) out.push({ op: "-", text: a[i++] });
  while (j < b.length) out.push({ op: "+", text: b[j++] });
  return out;
}

// Hunk is a run of changes with up to `context` unchanged lines around it;
// skipped counts the unchanged lines left out before it.
export type Hunk = { skipped: number; lines: DiffLine[] };

export function hunks(lines: DiffLine[], context = 3): Hunk[] {
  const keep = new Array<boolean>(lines.length).fill(false);
  lines.forEach((l, i) => {
    if (l.op === " ") return;
    for (let k = Math.max(0, i - context); k <= Math.min(lines.length - 1, i + context); k++) keep[k] = true;
  });
  const out: Hunk[] = [];
  let skipped = 0;
  let current: Hunk | null = null;
  lines.forEach((l, i) => {
    if (!keep[i]) {
      skipped++;
      current = null;
      return;
    }
    if (!current) {
      current = { skipped, lines: [] };
      out.push(current);
      skipped = 0;
    }
    current.lines.push(l);
  });
  return out;
}
```

- [ ] **Step 4: Run the tests**

Run: `pnpm --dir web exec vitest run src/lib/line-diff.test.ts`
Expected: PASS (6 tests)

- [ ] **Step 5: Check**

Run: `pnpm --dir web exec tsc -b && pnpm --dir web run lint`
Expected: no errors

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/line-diff.ts \
  web/src/lib/line-diff.test.ts
git commit -m "feat(web): line diff for template revisions" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Org → Templates list page, route and menu entry

**Files:**
- Create: `web/src/pages/org-templates.tsx`
- Modify: `web/src/components/nav-user.tsx`
- Modify: `web/src/main.tsx`
- Test: `web/src/pages/org-templates.test.tsx`
- Test: `web/src/components/nav-user.test.tsx`

**Interfaces:**
- Consumes: `useTemplatesApi` (Task 1), `instancesLabel`, `TemplateSummary` (Task 1).
- Produces: route `/org/templates` → `OrgTemplatesPage` (default export of `@/pages/org-templates`); the user menu item **Templates** after **Org variables**. Links it renders: `/org/templates/new` (Add template) and `/org/templates/:id` (Task 7 adds those routes).

Org pages are reached from the user menu (`components/nav-user.tsx`: Members, Alerts, Org variables). The list shows name, latest revision and "12 instances · 3 behind" from `get-templates` (no extra calls: the API computes both counts).

- [ ] **Step 1: Write the failing page test:**

Create `web/src/pages/org-templates.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import OrgTemplatesPage from "@/pages/org-templates";
import { fakeAPI } from "@/test/fake-api";

vi.mock("@/layouts/use-app-layout", () => ({ useAppBreadcrumbs: () => {} }));

const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.fn() } as NotificationsContextValue;

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/org/templates"]}>
      <NotificationsContext.Provider value={notifications}>
        <Routes>
          <Route path="/org/templates" element={<OrgTemplatesPage />} />
          <Route path="/org/templates/new" element={<p>new template</p>} />
          <Route path="/org/templates/:id" element={<p>template page</p>} />
        </Routes>
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
}

afterEach(() => vi.unstubAllGlobals());

describe("OrgTemplatesPage", () => {
  it("lists templates with their revision and instances", async () => {
    const user = userEvent.setup();
    fakeAPI({
      "/api/v1/template/get-templates": {
        templates: [{ id: "t1", name: "twillingate", latest_revision: 5, instances: 12, behind: 3, updated_at: "2026-10-12T10:00:00Z" }],
      },
    });
    renderPage();
    expect(await screen.findByText("12 instances · 3 behind")).toBeInTheDocument();
    expect(screen.getByText("r5")).toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "twillingate" }));
    expect(await screen.findByText("template page")).toBeInTheDocument();
  });

  it("offers Add template, also when there is none", async () => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/template/get-templates": { templates: null } });
    renderPage();
    expect(await screen.findByText(/No templates yet/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Add template/ }));
    expect(await screen.findByText("new template")).toBeInTheDocument();
  });

  it("shows why the list failed", async () => {
    fakeAPI({ "/api/v1/template/get-templates": new Error("database down") });
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("database down");
  });
});
```

- [ ] **Step 2: Add the menu test:**

In `web/src/components/nav-user.test.tsx`:

Replace:

```tsx
                <Route path="/org/members" element={<p>members page</p>} />
                <Route path="/org/alerts" element={<p>alerts page</p>} />
              </Routes>
            </ProfileContext.Provider>
```

with:

```tsx
                <Route path="/org/members" element={<p>members page</p>} />
                <Route path="/org/alerts" element={<p>alerts page</p>} />
                <Route path="/org/templates" element={<p>templates page</p>} />
              </Routes>
            </ProfileContext.Provider>
```

Replace:

```tsx
    expect(await screen.findByText("alerts page")).toBeInTheDocument();
  });
});
```

with:

```tsx
    expect(await screen.findByText("alerts page")).toBeInTheDocument();
  });

  it("offers Templates next to the org's variables", async () => {
    const user = userEvent.setup();
    renderNav();

    await user.click(screen.getByRole("button", { name: /Ann/ }));
    await user.click(await screen.findByRole("menuitem", { name: "Templates" }));

    expect(await screen.findByText("templates page")).toBeInTheDocument();
  });
});
```

- [ ] **Step 3: Run the tests**

Run: `pnpm --dir web exec vitest run src/pages/org-templates.test.tsx src/components/nav-user.test.tsx`
Expected: FAIL: `Failed to resolve import "@/pages/org-templates"`; the menu test fails with `Unable to find role="menuitem" and name "Templates"`

- [ ] **Step 4: Implement the page:**

Create `web/src/pages/org-templates.tsx`:

```tsx
import { useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { Plus } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Spinner } from "@/components/ui/spinner";
import { InlineError } from "@/components/inline-error";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { instancesLabel, type TemplateSummary } from "@/lib/templates";
import { relativeTime } from "@/lib/utils";

const BREADCRUMBS = [{ label: "Organization" }, { label: "Templates" }];

// OrgTemplatesPage is Org → Templates: the org's stack definitions, each
// with its latest revision and how many apps run it.
export default function OrgTemplatesPage() {
  useAppBreadcrumbs(BREADCRUMBS);
  const api = useTemplatesApi();
  const navigate = useNavigate();
  const [list, setList] = useState<TemplateSummary[] | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api.listTemplates().then(setList, (e: unknown) => setError(e instanceof Error ? e.message : "Failed to load templates"));
  }, [api]);

  return (
    <Card>
      <CardHeader className="flex-row flex-wrap items-start justify-between gap-x-4 gap-y-3 space-y-0">
        <div className="min-w-0 space-y-1.5">
          <CardTitle>Templates</CardTitle>
          <CardDescription>
            A stack defined once, added as an app on any server with its own values. Apps added from a template
            keep its files until you unlink them.
          </CardDescription>
        </div>
        <Button size="sm" onClick={() => navigate("/org/templates/new")}>
          <Plus className="size-4" /> Add template
        </Button>
      </CardHeader>
      <CardContent>
        {error ? (
          <InlineError>{error}</InlineError>
        ) : list === null ? (
          <div className="flex h-24 items-center justify-center">
            <Spinner />
          </div>
        ) : list.length === 0 ? (
          <p className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
            No templates yet. Add one here, or open an app's Settings and save it as a template.
          </p>
        ) : (
          <ul className="divide-y rounded-md border">
            {list.map((t) => (
              <li key={t.id} className="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3 text-sm">
                <Link to={`/org/templates/${t.id}`} className="font-medium hover:underline">
                  {t.name}
                </Link>
                <span className="text-muted-foreground">r{t.latest_revision}</span>
                <span className="text-muted-foreground">{instancesLabel(t)}</span>
                <time className="ml-auto text-xs text-muted-foreground" dateTime={t.updated_at}>
                  {relativeTime(new Date(t.updated_at))}
                </time>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
```

- [ ] **Step 5: Add the menu item:**

In `web/src/components/nav-user.tsx`:

Replace:

```tsx
"use client";

import { BellRing, Braces, ChevronsUpDown, KeyRound, LockKeyhole, LogOut, Users } from "lucide-react";
import { useNavigate } from "react-router-dom";
```

with:

```tsx
"use client";

import { BellRing, Braces, ChevronsUpDown, KeyRound, LayoutTemplate, LockKeyhole, LogOut, Users } from "lucide-react";
import { useNavigate } from "react-router-dom";
```

Replace:

```tsx
                Org variables
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => navigate("/user/tokens")}>
                <KeyRound />
```

with:

```tsx
                Org variables
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => navigate("/org/templates")}>
                <LayoutTemplate />
                Templates
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => navigate("/user/tokens")}>
                <KeyRound />
```

- [ ] **Step 6: Route it (`main.tsx` is exempt from coverage):**

In `web/src/main.tsx`:

Replace:

```tsx
import OrgAlertsPage from "@/pages/org-alerts"
import OrgVariablesPage from "@/pages/org-variables";
import LoginPage from "@/pages/login"
import RegisterPage from "@/pages/register"
```

with:

```tsx
import OrgAlertsPage from "@/pages/org-alerts"
import OrgVariablesPage from "@/pages/org-variables";
import OrgTemplatesPage from "@/pages/org-templates"
import LoginPage from "@/pages/login"
import RegisterPage from "@/pages/register"
```

Replace:

```tsx
            element: <OrgVariablesPage />,
          },
          { path: "*", element: <NotFound /> },
        ],
```

with:

```tsx
            element: <OrgVariablesPage />,
          },
          {
            path: "/org/templates",
            element: <OrgTemplatesPage />,
          },
          { path: "*", element: <NotFound /> },
        ],
```

- [ ] **Step 7: Run the tests**

Run: `pnpm --dir web exec vitest run src/pages/org-templates.test.tsx src/components/nav-user.test.tsx`
Expected: PASS (6 tests)

- [ ] **Step 8: Check**

Run: `pnpm --dir web exec tsc -b && pnpm --dir web run lint`
Expected: no errors

- [ ] **Step 9: Commit**

```bash
git add web/src/pages/org-templates.tsx \
  web/src/pages/org-templates.test.tsx \
  web/src/components/nav-user.tsx \
  web/src/components/nav-user.test.tsx \
  web/src/main.tsx
git commit -m "feat(web): org templates list" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Share the app editor's file editing; read-only code editor

**Files:**
- Create: `web/src/components/files-section.tsx`
- Modify: `web/src/components/app-editor.tsx`
- Modify: `web/src/components/code-editor.tsx`
- Test: `web/src/components/files-section.test.tsx`
- Test: `web/src/components/code-editor.test.tsx`
- Test: `web/src/components/app-editor.test.tsx`

**Interfaces:**
- Produces (`@/components/files-section`): `FilesSection({files: AppFileMeta[], contents: Record<string,string>, onChange: (files, contents) => void, description: ReactNode, allowSecret?: boolean = true, imageChips?: boolean = true})` and `SecretToggle({on, onChange})`.
- Produces (`@/components/code-editor`): `CodeEditor` gains `readOnly?: boolean`; `onChange` becomes optional.
- `AppEditor` renders `FilesSection` instead of its own Files section; its behaviour is unchanged.

The template editor (Task 7) must reuse the app editor's file editing, without the secret toggle (a template holds no secret) and without the image-tag chips (they ask the active server's registries; a template is org-level). Move the Files section out of `AppEditor` unchanged, with two switches. The instance page (Task 10) shows files read-only, so `CodeEditor` gets `readOnly`.

- [ ] **Step 1: Write the failing tests:**

Create `web/src/components/files-section.test.tsx`:

```tsx
import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { FilesSection } from "@/components/files-section";
import type { AppFileMeta } from "@/types/app-config";

vi.mock("@/components/code-editor", () => ({
  CodeEditor: ({ value, onChange, filename }: { value: string; onChange?: (v: string) => void; filename?: string }) => (
    <textarea aria-label={`Content of ${filename || "file"}`} value={value} onChange={(e) => onChange?.(e.target.value)} />
  ),
}));
vi.mock("@/components/image-tag-picker", () => ({
  ImageTagPicker: ({ image, onSelect }: { image: string; onSelect: (ref: string) => void }) => (
    <button onClick={() => onSelect(`${image.split(":")[0]}:2`)}>tags of {image}</button>
  ),
}));

function Harness({ allowSecret, imageChips }: { allowSecret?: boolean; imageChips?: boolean }) {
  const [files, setFiles] = useState<AppFileMeta[]>([{ id: "f1", filename: "compose.yml", is_encrypted: false }]);
  const [contents, setContents] = useState<Record<string, string>>({ f1: "services:\n  app:\n    image: nginx:1\n" });
  return (
    <>
      <FilesSection
        files={files}
        contents={contents}
        onChange={(f, c) => {
          setFiles(f);
          setContents(c);
        }}
        description="the files"
        allowSecret={allowSecret}
        imageChips={imageChips}
      />
      <output aria-label="state">{JSON.stringify({ files, contents })}</output>
    </>
  );
}

const state = () => JSON.parse(screen.getByRole("status", { name: "state" }).textContent ?? "{}");

describe("FilesSection", () => {
  it("adds, renames, marks secret, edits and removes files", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    await user.click(screen.getAllByRole("button", { name: /Add file/ })[0]);
    const names = screen.getAllByRole("textbox", { name: "File name" });
    await user.type(names[1], "app.env");
    await user.click(screen.getAllByRole("button", { name: /Secret/ })[1]);
    await user.type(screen.getByRole("textbox", { name: "Content of app.env" }), "A=1");
    expect(state().files[1]).toMatchObject({ filename: "app.env", is_encrypted: true });
    expect(Object.values(state().contents)).toContain("A=1");
    await user.click(screen.getByRole("button", { name: "Remove app.env" }));
    expect(state().files).toHaveLength(1);
    expect(Object.keys(state().contents)).toEqual(["f1"]);
  });

  it("rewrites an image reference from the tag browser", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    await user.click(screen.getByRole("button", { name: "tags of nginx:1" }));
    expect(state().contents.f1).toContain("image: nginx:2");
  });

  it("can leave out the secret toggle and the image browser", async () => {
    const user = userEvent.setup();
    render(<Harness allowSecret={false} imageChips={false} />);
    expect(screen.queryByRole("button", { name: /Secret/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /tags of/ })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Remove compose.yml" }));
    expect(screen.getByText(/Add at least a/)).toBeInTheDocument();
  });
});
```

Create `web/src/components/code-editor.test.tsx`:

```tsx
import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { CodeEditor } from "@/components/code-editor";

describe("CodeEditor", () => {
  it("can show content read-only", () => {
    const { container } = render(<CodeEditor value={"services: {}\n"} filename="compose.yml" readOnly />);
    const content = container.querySelector(".cm-content");
    expect(content).toHaveTextContent("services: {}");
    expect(content).toHaveAttribute("contenteditable", "false");
  });

  it("is editable by default", () => {
    const { container } = render(<CodeEditor value="x" onChange={() => {}} />);
    expect(container.querySelector(".cm-content")).toHaveAttribute("contenteditable", "true");
  });
});
```

- [ ] **Step 2: Pin that the app editor still edits files through the shared section:**

In `web/src/components/app-editor.test.tsx`:

Replace:

```tsx
import { render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
```

with:

```tsx
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
```

Replace:

```tsx
  });
});
```

with:

```tsx
  });
});

describe("AppEditor files", () => {
  it("edits files through the shared files section", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<AppEditor state={emptyEditorState()} onChange={onChange} />);
    await user.click(screen.getAllByRole("button", { name: /Add file/ })[0]);
    const next = onChange.mock.lastCall![0] as AppEditorState;
    expect(next.config.files).toHaveLength(2);
    expect(Object.keys(next.files)).toHaveLength(2);
  });
});
```

- [ ] **Step 3: Run the tests**

Run: `pnpm --dir web exec vitest run src/components/files-section.test.tsx src/components/code-editor.test.tsx src/components/app-editor.test.tsx`
Expected: FAIL: `Failed to resolve import "@/components/files-section"`; the read-only CodeEditor test fails (`contenteditable` is `"true"`)

- [ ] **Step 4: Create the shared section (the code moved out of `app-editor.tsx`, made prop-driven):**

Create `web/src/components/files-section.tsx`:

```tsx
import type { ReactNode } from "react";
import { FileText, Lock, LockOpen, Plus, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { CodeEditor } from "@/components/code-editor";
import { EditorSection } from "@/components/editor-section";
import { ImageTagPicker } from "@/components/image-tag-picker";
import { cn } from "@/lib/utils";
import { localId, type AppFileMeta } from "@/types/app-config";

type Props = {
  files: AppFileMeta[];
  contents: Record<string, string>; // file id -> content
  onChange: (files: AppFileMeta[], contents: Record<string, string>) => void;
  description: ReactNode;
  // allowSecret offers the per-file secret toggle (an app's files; a
  // template holds no secret).
  allowSecret?: boolean;
  // imageChips offers the image tag browser under compose files (it asks
  // the active server's registries).
  imageChips?: boolean;
};

// FilesSection is the app editor's file editing: named files, each in a
// code editor, added and removed freely. The app editor and the template
// editor share it.
export function FilesSection({ files, contents, onChange, description, allowSecret = true, imageChips = true }: Props) {
  const updateMeta = (id: string, patch: Partial<AppFileMeta>) =>
    onChange(
      files.map((f) => (f.id === id ? { ...f, ...patch } : f)),
      contents,
    );
  const updateContent = (id: string, content: string) => onChange(files, { ...contents, [id]: content });
  const add = () => {
    const id = localId("file");
    onChange([...files, { id, filename: "", is_encrypted: false }], { ...contents, [id]: "" });
  };
  const remove = (id: string) => {
    const rest = { ...contents };
    delete rest[id];
    onChange(
      files.filter((f) => f.id !== id),
      rest,
    );
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
      {files.length === 0 ? (
        <p className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">
          Add at least a <code>compose.yml</code>.
        </p>
      ) : null}
      {files.map((f) => (
        <div key={f.id} className="overflow-hidden rounded-md border">
          <div className="flex items-center gap-1 border-b bg-muted/40 py-1 pr-1 pl-1">
            <FileText className="ml-2 size-4 shrink-0 text-muted-foreground" />
            <Input
              aria-label="File name"
              value={f.filename}
              placeholder="compose.yml"
              className="h-8 border-transparent bg-transparent font-mono shadow-none focus-visible:border-input focus-visible:bg-background"
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
          <CodeEditor
            value={contents[f.id] ?? ""}
            onChange={(content) => updateContent(f.id, content)}
            filename={f.filename}
            placeholder="file contents (use ${VAR}, including org/server variables)"
            className="rounded-none border-0"
          />
          {imageChips && isComposeFilename(f.filename) ? (
            <ImageChips
              content={contents[f.id] ?? ""}
              onReplace={(oldRef, newRef) => updateContent(f.id, (contents[f.id] ?? "").split(oldRef).join(newRef))}
            />
          ) : null}
        </div>
      ))}
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

// isComposeFilename gates the image-tag chips to compose files.
function isComposeFilename(name: string): boolean {
  const n = name.trim();
  return n === "compose.yml" || n === "compose.yaml" || n === "docker-compose.yml" || n === "docker-compose.yaml";
}

const IMAGE_LINE = /^\s*image:\s*["']?([\w][\w./:@-]*)/gm;

// ImageChips lists the image references found in a compose file, each with a
// tag browser that rewrites the reference in place.
function ImageChips({ content, onReplace }: { content: string; onReplace: (oldRef: string, newRef: string) => void }) {
  const refs = Array.from(new Set(Array.from(content.matchAll(IMAGE_LINE), (m) => m[1]).filter(Boolean)));
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

- [ ] **Step 5: Use it in `AppEditor` and drop the moved code:**

In `web/src/components/app-editor.tsx`:

Replace:

```tsx
import { Eye, EyeOff, FileText, Lock, LockOpen, Plus, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
```

with:

```tsx
import { Eye, EyeOff, Lock, Plus, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
```

Replace:

```tsx
import { Switch } from "@/components/ui/switch";
import { PasswordInput } from "@/components/ui/password-input";
import { CodeEditor } from "@/components/code-editor";
import { ImageTagPicker } from "@/components/image-tag-picker";
import { IconPicker } from "@/components/icon-picker";
import { IngressEditor } from "@/components/ingress-editor";
import { EditorSection } from "@/components/editor-section";
import { ENCRYPTED } from "@/lib/ecies";
import { cn } from "@/lib/utils";
import { useState } from "react";
import { SharedVariablesHint } from "@/components/shared-variables-hint";
```

with:

```tsx
import { Switch } from "@/components/ui/switch";
import { PasswordInput } from "@/components/ui/password-input";
import { FilesSection, SecretToggle } from "@/components/files-section";
import { IconPicker } from "@/components/icon-picker";
import { IngressEditor } from "@/components/ingress-editor";
import { EditorSection } from "@/components/editor-section";
import { ENCRYPTED } from "@/lib/ecies";
import { useState } from "react";
import { SharedVariablesHint } from "@/components/shared-variables-hint";
```

Replace:

```tsx
  localId,
  type AppEditorState,
  type AppFileMeta,
  type AppVariableMeta,
  type StorageVariable,
```

with:

```tsx
  localId,
  type AppEditorState,
  type AppVariableMeta,
  type StorageVariable,
```

Replace:

```tsx
  const setConfig = (patch: Partial<AppEditorState["config"]>) =>
    onChange({ ...state, config: { ...state.config, ...patch } });

  // --- files ---
  const updateFileMeta = (id: string, patch: Partial<AppFileMeta>) =>
    setConfig({
      files: state.config.files.map((f) =>
        f.id === id ? { ...f, ...patch } : f,
      ),
    });

  const updateFileContent = (id: string, content: string) =>
    onChange({ ...state, files: { ...state.files, [id]: content } });

  const addFile = () => {
    const id = localId("file");
    onChange({
      ...state,
      config: {
        ...state.config,
        files: [
          ...state.config.files,
          { id, filename: "", is_encrypted: false },
        ],
      },
      files: { ...state.files, [id]: "" },
    });
  };

  const removeFile = (id: string) => {
    const rest = { ...state.files };
    delete rest[id];
    onChange({
      ...state,
      config: {
        ...state.config,
        files: state.config.files.filter((f) => f.id !== id),
      },
      files: rest,
    });
  };

  // --- variables ---
```

with:

```tsx
  const setConfig = (patch: Partial<AppEditorState["config"]>) =>
    onChange({ ...state, config: { ...state.config, ...patch } });

  // --- variables ---
```

Replace:

```tsx
      </EditorSection>

      <EditorSection
        title="Files"
        description={
          <>
```

with:

```tsx
      </EditorSection>

      <FilesSection
        files={state.config.files}
        contents={state.files}
        onChange={(files, contents) =>
          onChange({ ...state, config: { ...state.config, files }, files: contents })
        }
        description={
          <>
```

Replace:

```tsx
          </>
        }
        action={
          <Button size="sm" variant="outline" onClick={addFile}>
            <Plus className="size-4" /> Add file
          </Button>
        }
      >
        {state.config.files.length === 0 ? (
          <p className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">
            Add at least a <code>compose.yml</code>.
          </p>
        ) : null}
        {state.config.files.map((f) => (
          <div key={f.id} className="overflow-hidden rounded-md border">
            <div className="flex items-center gap-1 border-b bg-muted/40 py-1 pr-1 pl-1">
              <FileText className="ml-2 size-4 shrink-0 text-muted-foreground" />
              <Input
                aria-label="File name"
                value={f.filename}
                placeholder="compose.yml"
                className="h-8 border-transparent bg-transparent font-mono shadow-none focus-visible:border-input focus-visible:bg-background"
                onChange={(e) => updateFileMeta(f.id, { filename: e.target.value })}
              />
              <SecretToggle
                on={f.is_encrypted}
                onChange={(on) => updateFileMeta(f.id, { is_encrypted: on })}
              />
              <Button
                size="icon"
                variant="ghost"
                className="size-8 shrink-0 text-muted-foreground hover:text-destructive"
                aria-label={`Remove ${f.filename || "file"}`}
                onClick={() => removeFile(f.id)}
              >
                <Trash2 className="size-4" />
              </Button>
            </div>
            <CodeEditor
              value={state.files[f.id] ?? ""}
              onChange={(content) => updateFileContent(f.id, content)}
              filename={f.filename}
              placeholder="file contents (use ${VAR}, including org/server variables)"
              className="rounded-none border-0"
            />
            {isComposeFilename(f.filename) ? (
              <ImageChips
                content={state.files[f.id] ?? ""}
                onReplace={(oldRef, newRef) =>
                  updateFileContent(
                    f.id,
                    (state.files[f.id] ?? "").split(oldRef).join(newRef),
                  )
                }
              />
            ) : null}
          </div>
        ))}
      </EditorSection>

      <EditorSection
```

with:

```tsx
          </>
        }
      />

      <EditorSection
```

Replace:

```tsx
  );
}

// SecretToggle marks a file or variable as secret (encrypted end to end).
function SecretToggle({
  on,
  onChange,
}: {
  on: boolean;
  onChange: (on: boolean) => void;
}) {
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

// isComposeFilename gates the image-tag chips to compose files.
function isComposeFilename(name: string): boolean {
  const n = name.trim();
  return (
    n === "compose.yml" ||
    n === "compose.yaml" ||
    n === "docker-compose.yml" ||
    n === "docker-compose.yaml"
  );
}

const IMAGE_LINE = /^\s*image:\s*["']?([\w][\w./:@-]*)/gm;

// ImageChips lists the image references found in a compose file, each with a
// tag browser that rewrites the reference in place.
function ImageChips({
  content,
  onReplace,
}: {
  content: string;
  onReplace: (oldRef: string, newRef: string) => void;
}) {
  const refs = Array.from(
    new Set(
      Array.from(content.matchAll(IMAGE_LINE), (m) => m[1]).filter(Boolean),
    ),
  );
  if (refs.length === 0) return null;
  return (
    <div className="flex flex-wrap items-center gap-2 border-t bg-muted/20 px-3 py-2">
      <span className="text-xs text-muted-foreground">Images</span>
      {refs.map((ref) => (
        <ImageTagPicker
          key={ref}
          image={ref}
          onSelect={(newRef) => onReplace(ref, newRef)}
        />
      ))}
    </div>
  );
}
```

with:

```tsx
  );
}
```

- [ ] **Step 6: Add `readOnly`:**

In `web/src/components/code-editor.tsx`:

Replace:

```tsx
type Props = {
  value: string;
  onChange: (value: string) => void;
  filename?: string;
  placeholder?: string;
```

with:

```tsx
type Props = {
  value: string;
  onChange?: (value: string) => void;
  // readOnly shows the content without letting it change (an instance's
  // files, owned by its template).
  readOnly?: boolean;
  filename?: string;
  placeholder?: string;
```

Replace:

```tsx
  value,
  onChange,
  filename,
  placeholder,
```

with:

```tsx
  value,
  onChange,
  readOnly = false,
  filename,
  placeholder,
```

Replace:

```tsx
        value={value}
        onChange={onChange}
        extensions={extensions}
        theme={dark ? "dark" : "light"}
```

with:

```tsx
        value={value}
        onChange={onChange}
        readOnly={readOnly}
        editable={!readOnly}
        extensions={extensions}
        theme={dark ? "dark" : "light"}
```

- [ ] **Step 7: Run the tests**

Run: `pnpm --dir web exec vitest run src/components/files-section.test.tsx src/components/code-editor.test.tsx src/components/app-editor.test.tsx`
Expected: PASS (8 tests)

- [ ] **Step 8: Check**

Run: `pnpm --dir web exec tsc -b && pnpm --dir web run lint`
Expected: no errors

- [ ] **Step 9: Commit**

```bash
git add web/src/components/files-section.tsx \
  web/src/components/files-section.test.tsx \
  web/src/components/code-editor.tsx \
  web/src/components/code-editor.test.tsx \
  web/src/components/app-editor.tsx \
  web/src/components/app-editor.test.tsx
git commit -m "refactor(web): share the app editor's file editing" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Template editor state: draft ↔ body, recipe backup names, checks

**Files:**
- Create: `web/src/lib/template-draft.ts`
- Test: `web/src/lib/template-draft.test.ts`

**Interfaces:**
- Consumes: `compilePattern` (Task 2); `BackupDefault`, `Parameter`, `TemplateBody` (Task 1); `DEFAULT_COMPOSE`, `localId`, `AppFileMeta` (`@/types/app-config`); `yamlLanguage` (`@codemirror/lang-yaml`, already a dependency).
- Produces (`@/lib/template-draft`): types `ParamRow = {id, name, secret, required, default: string, pattern, description}`, `IngressDraft = {id, host, service, port: string}`, `TemplateDraft = {name, appName, icon, color, files: AppFileMeta[], contents: Record<string,string>, parameters: ParamRow[], ingress: IngressDraft[], backups: Record<string, BackupDefault>}`; `TEMPLATE_NAME: RegExp`; `emptyDraft()`, `paramRow(p?: Partial<Parameter>)`, `ingressRow(host?, port?, service?)`, `draftFromBody(name, body)`, `recipeBackupNames(d): string[]`, `bodyFromDraft(d): TemplateBody`, `draftProblem(d): string | null`.

The Backup defaults rows are "one per recipe backup", so the browser must read `x-winterflow.backups[].name` from the compose files as they are being edited. There is no YAML parser among the dependencies, but `@codemirror/lang-yaml` (already bundled for the code editor) carries the Lezer YAML parser: `yamlLanguage.parser.parse(text)` gives `Document > BlockMapping > Pair(Key, value)`, sequences of `Item`s, flow mappings and `QuotedLiteral`s, which is all the walk needs. Like compose, the first main compose file plus the first override are read. Defaults for names the recipe no longer declares are dropped on save (the API would refuse them). An empty default field means no default (it renders the same as an empty default).

- [ ] **Step 1: Write the failing test:**

Create `web/src/lib/template-draft.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import {
  bodyFromDraft,
  draftFromBody,
  draftProblem,
  emptyDraft,
  ingressRow,
  paramRow,
  recipeBackupNames,
  type TemplateDraft,
} from "@/lib/template-draft";
import type { TemplateBody } from "@/lib/templates";

const COMPOSE = `services:
  app:
    image: ghcr.io/dmtrkzntsv/twillingate:\${VERSION}
    ports: ["127.0.0.1:\${PORT}:8080"]
    volumes: [data:/data]
volumes: { data: {} }
x-winterflow:
  backups:
    - { name: db, type: sqlite, volume: data, path: twillingate.db, every: 1h }
    # a comment between items
    - name: "uploads"
      type: files
      volume: data
`;

const BODY: TemplateBody = {
  files: { "compose.yaml": COMPOSE, "Caddyfile": ":80" },
  parameters: [
    { name: "NAME", required: true, pattern: "^[a-z]{3,20}$", description: "Instance name" },
    { name: "PORT", required: true },
    { name: "MEMORY_MAX", default: "256m" },
    { name: "SESSION_KEY", secret: true, required: true },
  ],
  app_name: "${NAME}",
  icon: "chart",
  color: "#2563eb",
  ingress: [{ host: "${NAME}.twillingate.app", service: "app", port: "${PORT}" }],
  backups: { db: { enabled: true, storage: "tenants" }, gone: { enabled: true } },
};

function valid(): TemplateDraft {
  return draftFromBody("twillingate", BODY);
}

describe("draftFromBody / bodyFromDraft", () => {
  it("round-trips a body, dropping defaults of backups the compose file no longer declares", () => {
    const body = bodyFromDraft(valid());
    expect(body).toEqual({ ...BODY, backups: { db: { enabled: true, storage: "tenants" } } });
  });

  it("writes a numeric port as a number and leaves out empty fields", () => {
    const d = valid();
    d.ingress = [ingressRow("static.example.com", "8080")];
    d.parameters = [paramRow({ name: "X", default: "" })];
    const body = bodyFromDraft(d);
    expect(body.ingress).toEqual([{ host: "static.example.com", port: 8080 }]);
    expect(body.parameters).toEqual([{ name: "X" }]);
  });

  it("reads null lists from the API", () => {
    const d = draftFromBody("x", { ...BODY, files: null, parameters: null, ingress: null, backups: null });
    expect([d.files, d.parameters, d.ingress, d.backups]).toEqual([[], [], [], {}]);
  });
});

describe("recipeBackupNames", () => {
  it("reads block and flow entries, quoted names, the override file too", () => {
    const d = valid();
    expect(recipeBackupNames(d)).toEqual(["db", "uploads"]);
    const id = "override";
    d.files.push({ id, filename: "compose.override.yml", is_encrypted: false });
    d.contents[id] = "x-winterflow:\n  backups: [{ name: logs, type: files, volume: data }, { name: db, type: sqlite }]\n";
    expect(recipeBackupNames(d)).toEqual(["db", "uploads", "logs"]);
  });

  it("finds none without a compose file or a recipe", () => {
    expect(recipeBackupNames(emptyDraft())).toEqual([]);
    const d = valid();
    d.files = d.files.filter((f) => f.filename === "Caddyfile");
    expect(recipeBackupNames(d)).toEqual([]);
    const odd = valid();
    odd.contents[odd.files.find((f) => f.filename === "compose.yaml")!.id] = "x-winterflow:\n  backups: none\n";
    expect(recipeBackupNames(odd)).toEqual([]);
  });
});

describe("draftProblem", () => {
  const cases: [string, (d: TemplateDraft) => void, string][] = [
    ["name", (d) => (d.name = "Shop"), "Name: lowercase"],
    ["app name", (d) => (d.appName = " "), "App name is required."],
    ["file name", (d) => (d.files[0].filename = ""), "Every file needs a filename."],
    ["duplicate file", (d) => (d.files[0].filename = "compose.yaml"), "Two files have the same name."],
    ["compose", (d) => (d.files = d.files.filter((f) => f.filename === "Caddyfile")), "A compose file is required"],
    ["param name", (d) => (d.parameters[0].name = "name"), 'Parameter "name": uppercase'],
    ["reserved", (d) => (d.parameters[0].name = "WF_X"), "Parameter WF_X is reserved."],
    ["twice", (d) => (d.parameters[1].name = "NAME"), "Parameter NAME is declared twice."],
    ["long pattern", (d) => (d.parameters[0].pattern = "a".repeat(513)), "longer than 512 bytes"],
    ["dollar default", (d) => (d.parameters[2].default = "$X"), "the default may not contain $."],
    ["default vs pattern", (d) => (d.parameters[0].default = "AB"), "the default doesn't match the pattern."],
    ["app name ref", (d) => (d.appName = "${SESSION_KEY}"), "${SESSION_KEY} is not a plain parameter"],
    ["empty host", (d) => (d.ingress[0].host = ""), "Every ingress row needs a host."],
    ["host ref", (d) => (d.ingress[0].host = "${NOPE}.example.com"), "${NOPE} is not a plain parameter"],
    ["port", (d) => (d.ingress[0].port = "70000"), "the port is a number 1-65535"],
    ["port ref", (d) => (d.ingress[0].port = "${SESSION_KEY}"), "the port is a number 1-65535"],
  ];
  it.each(cases)("names the problem: %s", (_, change, want) => {
    const d = valid();
    change(d);
    expect(draftProblem(d)).toContain(want);
  });

  it("accepts a valid draft", () => {
    expect(draftProblem(valid())).toBeNull();
  });
});
```

- [ ] **Step 2: Run the tests**

Run: `pnpm --dir web exec vitest run src/lib/template-draft.test.ts`
Expected: FAIL: `Failed to resolve import "@/lib/template-draft"`

- [ ] **Step 3: Implement:**

Create `web/src/lib/template-draft.ts`:

```ts
// The template editor's working state (TemplateDraft) and its conversions:
// from a stored revision and to the body save-template takes. The checks
// give instant feedback; the API checks everything again
// (internal/domain/template/validate.go).
import { yamlLanguage } from "@codemirror/lang-yaml";

import { compilePattern } from "@/lib/template-params";
import type { BackupDefault, Parameter, TemplateBody } from "@/lib/templates";
import { DEFAULT_COMPOSE, localId, type AppFileMeta } from "@/types/app-config";

export type ParamRow = {
  id: string;
  name: string;
  secret: boolean;
  required: boolean;
  default: string; // "" = none (renders the same as an empty default)
  pattern: string;
  description: string;
};

export type IngressDraft = { id: string; host: string; service: string; port: string };

export type TemplateDraft = {
  name: string;
  appName: string;
  icon: string;
  color: string;
  files: AppFileMeta[]; // is_encrypted is always false: a template holds no secret
  contents: Record<string, string>; // file id -> content
  parameters: ParamRow[];
  ingress: IngressDraft[];
  backups: Record<string, BackupDefault>; // by recipe backup name
};

// Compose files in compose's load order (model.ComposeFileNames and the
// overrides).
const COMPOSE_FILES = ["compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"];
const OVERRIDE_FILES = [
  "compose.override.yml",
  "compose.override.yaml",
  "docker-compose.override.yml",
  "docker-compose.override.yaml",
];

export const TEMPLATE_NAME = /^[a-z][a-z0-9-]{0,62}$/;
const PARAM_NAME = /^[A-Z][A-Z0-9_]*$/;
const PORT = /^[1-9][0-9]{0,4}$/;
const REF = /\$\{([^}]*)\}/g;

export function emptyDraft(): TemplateDraft {
  const id = localId("file");
  return {
    name: "",
    appName: "",
    icon: "",
    color: "#64748b",
    files: [{ id, filename: "compose.yml", is_encrypted: false }],
    contents: { [id]: DEFAULT_COMPOSE },
    parameters: [],
    ingress: [],
    backups: {},
  };
}

export function paramRow(p: Partial<Parameter> = {}): ParamRow {
  return {
    id: localId("param"),
    name: p.name ?? "",
    secret: !!p.secret,
    required: !!p.required,
    default: p.default ?? "",
    pattern: p.pattern ?? "",
    description: p.description ?? "",
  };
}

export function ingressRow(host = "", port = "", service = ""): IngressDraft {
  return { id: localId("ing"), host, service, port };
}

export function draftFromBody(name: string, body: TemplateBody): TemplateDraft {
  const files: AppFileMeta[] = [];
  const contents: Record<string, string> = {};
  for (const filename of Object.keys(body.files ?? {}).sort()) {
    const id = localId("file");
    files.push({ id, filename, is_encrypted: false });
    contents[id] = body.files![filename];
  }
  return {
    name,
    appName: body.app_name,
    icon: body.icon,
    color: body.color,
    files,
    contents,
    parameters: (body.parameters ?? []).map(paramRow),
    ingress: (body.ingress ?? []).map((r) => ingressRow(r.host, String(r.port), r.service ?? "")),
    backups: { ...(body.backups ?? {}) },
  };
}

// composeContents are the draft's compose files compose would load: the
// first main file and the first override.
function composeContents(d: TemplateDraft): string[] {
  const byName = new Map(d.files.map((f) => [f.filename.trim(), d.contents[f.id] ?? ""]));
  const main = COMPOSE_FILES.find((n) => byName.has(n));
  if (!main) return [];
  const override = OVERRIDE_FILES.find((n) => byName.has(n));
  return override ? [byName.get(main)!, byName.get(override)!] : [byName.get(main)!];
}

type YamlNode = ReturnType<typeof yamlLanguage.parser.parse>["topNode"];

function scalar(src: string, n: YamlNode | null | undefined): string {
  if (!n) return "";
  const text = src.slice(n.from, n.to).trim();
  return n.name === "QuotedLiteral" ? text.slice(1, -1) : text;
}

// entries are a mapping node's key/value pairs.
function entries(src: string, n: YamlNode | null | undefined): [string, YamlNode | null][] {
  if (!n || (n.name !== "BlockMapping" && n.name !== "FlowMapping")) return [];
  const out: [string, YamlNode | null][] = [];
  for (let pair = n.firstChild; pair; pair = pair.nextSibling) {
    if (pair.name !== "Pair") continue;
    const key = pair.getChild("Key");
    let value = key?.nextSibling ?? null;
    while (value && (value.name === ":" || value.name === "Comment")) value = value.nextSibling;
    out.push([scalar(src, key?.firstChild), value]);
  }
  return out;
}

// recipeNamesIn reads the backup names one compose file declares under
// x-winterflow.backups (a list of mappings with a name).
function recipeNamesIn(src: string): string[] {
  const doc = yamlLanguage.parser.parse(src).topNode.getChild("Document");
  const root = doc?.firstChild;
  const xwf = entries(src, root).find(([k]) => k === "x-winterflow")?.[1];
  const list = entries(src, xwf).find(([k]) => k === "backups")?.[1];
  if (!list || (list.name !== "BlockSequence" && list.name !== "FlowSequence")) return [];
  const names: string[] = [];
  for (const item of list.getChildren("Item")) {
    const name = entries(src, item.firstChild).find(([k]) => k === "name")?.[1];
    const text = scalar(src, name);
    if (text) names.push(text);
  }
  return names;
}

// recipeBackupNames are the backup names the draft's compose files declare
// (the Backup defaults rows), in order, without duplicates.
export function recipeBackupNames(d: TemplateDraft): string[] {
  return [...new Set(composeContents(d).flatMap(recipeNamesIn))];
}

export function bodyFromDraft(d: TemplateDraft): TemplateBody {
  const files: Record<string, string> = {};
  for (const f of d.files) files[f.filename.trim()] = d.contents[f.id] ?? "";
  const recipe = new Set(recipeBackupNames(d));
  const backups: Record<string, BackupDefault> = {};
  for (const [name, def] of Object.entries(d.backups)) if (recipe.has(name)) backups[name] = def;
  return {
    files,
    parameters: d.parameters.map((p) => {
      const out: Parameter = { name: p.name.trim() };
      if (p.secret) out.secret = true;
      if (p.required) out.required = true;
      if (!p.secret && p.default !== "") out.default = p.default;
      if (p.pattern) out.pattern = p.pattern;
      if (p.description.trim()) out.description = p.description.trim();
      return out;
    }),
    app_name: d.appName.trim(),
    icon: d.icon,
    color: d.color,
    ingress: d.ingress.map((r) => {
      const port = r.port.trim();
      return {
        host: r.host.trim(),
        ...(r.service.trim() ? { service: r.service.trim() } : {}),
        port: PORT.test(port) ? Number(port) : port,
      };
    }),
    backups,
  };
}

// refProblem names the first ${...} in s that is not a plain parameter.
function refProblem(field: string, s: string, plain: Set<string>): string | null {
  for (const m of s.matchAll(REF)) {
    if (!plain.has(m[1])) return `${field}: \${${m[1]}} is not a plain parameter of the template.`;
  }
  return null;
}

// draftProblem is the first thing the API would refuse, worded for the
// editor; null when the draft looks saveable.
export function draftProblem(d: TemplateDraft): string | null {
  if (!TEMPLATE_NAME.test(d.name)) {
    return "Name: lowercase letters, digits and hyphens, starting with a letter (at most 63).";
  }
  if (!d.appName.trim()) return "App name is required.";
  const names = d.files.map((f) => f.filename.trim());
  if (names.some((n) => !n)) return "Every file needs a filename.";
  if (new Set(names).size !== names.length) return "Two files have the same name.";
  if (!names.some((n) => COMPOSE_FILES.includes(n))) return `A compose file is required (${COMPOSE_FILES.join(", ")}).`;
  const seen = new Set<string>();
  const plain = new Set<string>();
  for (const p of d.parameters) {
    const name = p.name.trim();
    if (!PARAM_NAME.test(name)) return `Parameter "${name}": uppercase letters, digits and _, starting with a letter.`;
    if (name === "COMPOSE_PROJECT_NAME" || name.startsWith("WF_")) return `Parameter ${name} is reserved.`;
    if (seen.has(name)) return `Parameter ${name} is declared twice.`;
    seen.add(name);
    if (new TextEncoder().encode(p.pattern).length > 512) return `Parameter ${name}: the pattern is longer than 512 bytes.`;
    if (p.secret) continue;
    plain.add(name);
    if (p.default.includes("$")) return `Parameter ${name}: the default may not contain $.`;
    const re = compilePattern(p.pattern);
    if (p.default !== "" && re && !re.test(p.default)) return `Parameter ${name}: the default doesn't match the pattern.`;
  }
  const app = refProblem("App name", d.appName, plain);
  if (app) return app;
  for (const r of d.ingress) {
    if (!r.host.trim()) return "Every ingress row needs a host.";
    const host = refProblem(`Host ${r.host}`, r.host, plain);
    if (host) return host;
    const port = r.port.trim();
    const ref = /^\$\{([^}]*)\}$/.exec(port);
    if (ref ? !plain.has(ref[1]) : !PORT.test(port) || Number(port) > 65535) {
      return `Host ${r.host}: the port is a number 1-65535 or \${PARAM} of a plain parameter.`;
    }
  }
  return null;
}
```

- [ ] **Step 4: Run the tests**

Run: `pnpm --dir web exec vitest run src/lib/template-draft.test.ts`
Expected: PASS (22 tests)

- [ ] **Step 5: Check**

Run: `pnpm --dir web exec tsc -b && pnpm --dir web run lint`
Expected: no errors

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/template-draft.ts \
  web/src/lib/template-draft.test.ts
git commit -m "feat(web): template editor state and checks" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Template editor and template page (Add template, Save with 409, Delete)

**Files:**
- Create: `web/src/components/template-editor.tsx`
- Create: `web/src/pages/template-page.tsx`
- Modify: `web/src/main.tsx`
- Test: `web/src/components/template-editor.test.tsx`
- Test: `web/src/pages/template-page.test.tsx`

**Interfaces:**
- Consumes: `FilesSection` (Task 5); `TemplateDraft`, `ParamRow`, `IngressDraft`, `paramRow`, `ingressRow`, `recipeBackupNames`, `draftFromBody`, `bodyFromDraft`, `draftProblem`, `emptyDraft` (Task 6); `compilePattern` (Task 2); `useTemplatesApi`, `ApiError`, `TemplateView`, `InstanceView` (Task 1); `IconPicker`, `EditorSection`, `InlineError`, `NotFound`.
- Produces: `TemplateEditor({draft, onChange})` (`@/components/template-editor`); default export `TemplatePage` and `export type DraftState = {draft: TemplateDraft; notes: string[]}` (`@/pages/template-page`), read from `location.state` on `/org/templates/new` (Task 11 navigates there with it); routes `/org/templates/new` and `/org/templates/:templateId`.

Save sends `base_revision` = the latest revision the page read (0 for a new template). A 409 means someone saved meanwhile: the page reads the template again to name their revision ("Someone saved r4 meanwhile; reload") and offers Reload, which drops the local changes; nothing is overwritten. Save's `warnings` (file references nothing provides) are shown as a toast. Delete is offered only while `get-instances?template_id=` lists no pending or created instance (the API refuses it with 409 otherwise; that message is shown too). The editor's labels are the selectors the tests (and screen readers) use: `Template name`, `App name`, `Parameter name`, `Default of NAME`, `Pattern of NAME`, `NAME is secret`, `NAME is required`, `Host`, `Port of HOST`, `Back up NAME`, `Storage of NAME`.

- [ ] **Step 1: Write the failing editor test:**

Create `web/src/components/template-editor.test.tsx`:

```tsx
import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { TemplateEditor } from "@/components/template-editor";
import { bodyFromDraft, draftFromBody, type TemplateDraft } from "@/lib/template-draft";

vi.mock("@/components/code-editor", () => ({
  CodeEditor: ({ value, onChange, filename }: { value: string; onChange?: (v: string) => void; filename?: string }) => (
    <textarea aria-label={`Content of ${filename || "file"}`} value={value} onChange={(e) => onChange?.(e.target.value)} />
  ),
}));

const COMPOSE = "services: {}\nx-winterflow:\n  backups:\n    - { name: db, type: sqlite, volume: data, path: a.db }\n";

const changed = vi.fn<(d: TemplateDraft) => void>();
// latest is the draft as the editor last changed it.
const latest = () => changed.mock.lastCall![0];

function Harness({ initial }: { initial: TemplateDraft }) {
  const [draft, setDraft] = useState(initial);
  return (
    <TemplateEditor
      draft={draft}
      onChange={(d) => {
        setDraft(d);
        changed(d);
      }}
    />
  );
}

function start(files: Record<string, string> = { "compose.yaml": COMPOSE }) {
  const draft = draftFromBody("shop", {
    files,
    parameters: [{ name: "NAME", default: "shop" }],
    app_name: "${NAME}",
    icon: "",
    color: "",
    ingress: null,
    backups: null,
  });
  render(<Harness initial={draft} />);
  return userEvent.setup();
}

describe("TemplateEditor", () => {
  it("edits parameters: a secret loses its default, a pattern JS can't read is flagged", async () => {
    const user = start();
    await user.click(screen.getByRole("checkbox", { name: "NAME is required" }));
    await user.click(screen.getByLabelText("Pattern of NAME"));
    await user.paste("[[:alpha:]]+");
    expect(screen.getByText(/The browser can't check this pattern/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("Description of NAME"), "Instance name");
    await user.click(screen.getByRole("checkbox", { name: "NAME is secret" }));
    expect(screen.getByLabelText("Default of NAME")).toBeDisabled();
    expect(bodyFromDraft(latest()).parameters).toEqual([
      { name: "NAME", secret: true, required: true, pattern: "[[:alpha:]]+", description: "Instance name" },
    ]);
    await user.click(screen.getByRole("button", { name: "Remove NAME" }));
    expect(screen.getByText("No parameters yet.")).toBeInTheDocument();
  });

  it("edits the app record and the files", async () => {
    const user = start();
    await user.clear(screen.getByLabelText("Template name"));
    await user.type(screen.getByLabelText("Template name"), "shop2");
    await user.clear(screen.getByLabelText("App name"));
    await user.type(screen.getByLabelText("App name"), "Shop");
    await user.click(screen.getAllByRole("button", { name: /Add file/ })[0]);
    expect(screen.queryByRole("button", { name: /Secret/ })).not.toBeInTheDocument();
    expect(latest()).toMatchObject({ name: "shop2", appName: "Shop" });
    expect(latest().files).toHaveLength(2);
  });

  it("edits ingress rows", async () => {
    const user = start();
    expect(screen.getByText("No hosts.")).toBeInTheDocument();
    await user.click(screen.getAllByRole("button", { name: /Add host/ })[0]);
    await user.click(screen.getByLabelText("Host"));
    await user.paste("${NAME}.example.com");
    await user.type(screen.getByLabelText("Port of ${NAME}.example.com"), "8080");
    await user.type(screen.getByLabelText("Service of ${NAME}.example.com"), "app");
    expect(bodyFromDraft(latest()).ingress).toEqual([{ host: "${NAME}.example.com", service: "app", port: 8080 }]);
    await user.click(screen.getByRole("button", { name: "Remove ${NAME}.example.com" }));
    expect(latest().ingress).toEqual([]);
  });

  it("offers one backup default per recipe backup", async () => {
    const user = start();
    await user.click(screen.getByRole("switch", { name: "Back up db" }));
    await user.type(screen.getByLabelText("Storage of db"), "tenants");
    expect(bodyFromDraft(latest()).backups).toEqual({ db: { enabled: true, storage: "tenants" } });
  });

  it("says when the compose file declares no backups", () => {
    start({ "compose.yaml": "services: {}\n" });
    expect(screen.getByText("The compose file declares no backups.")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Write the failing page test:**

Create `web/src/pages/template-page.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import { draftFromBody } from "@/lib/template-draft";
import { ApiError, type TemplateBody, type TemplateView } from "@/lib/templates";
import TemplatePage from "@/pages/template-page";
import { fakeAPI } from "@/test/fake-api";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
vi.mock("@/layouts/use-app-layout", () => ({ useAppBreadcrumbs: () => {} }));
vi.mock("@/components/code-editor", () => ({
  CodeEditor: ({ value, onChange, filename }: { value: string; onChange?: (v: string) => void; filename?: string }) => (
    <textarea aria-label={`Content of ${filename || "file"}`} value={value} onChange={(e) => onChange?.(e.target.value)} />
  ),
}));

const COMPOSE = `services:
  app:
    image: ghcr.io/dmtrkzntsv/twillingate:\${VERSION}
    ports: ["127.0.0.1:\${PORT}:8080"]
x-winterflow:
  backups:
    - { name: db, type: sqlite, volume: data, path: twillingate.db }
`;

const BODY: TemplateBody = {
  files: { "compose.yaml": COMPOSE },
  parameters: [
    { name: "NAME", required: true, pattern: "^[a-z]{3,20}$" },
    { name: "PORT", required: true },
    { name: "VERSION", required: true },
  ],
  app_name: "${NAME}",
  icon: "chart",
  color: "#2563eb",
  ingress: [{ host: "${NAME}.twillingate.app", port: "${PORT}" }],
  backups: { db: { enabled: true, storage: "tenants" } },
};

function view(revision: number): TemplateView {
  return {
    id: "t1",
    name: "twillingate",
    latest_revision: revision,
    revision,
    body: BODY,
    note: "",
    created_at: "2026-10-12T10:00:00Z",
    created_by: "u1",
    revisions: [{ revision, note: "", created_at: "2026-10-12T10:00:00Z", created_by: "u1" }],
  };
}

const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.fn() } as NotificationsContextValue;

function renderPage(url: string, state?: unknown) {
  return render(
    <MemoryRouter initialEntries={[{ pathname: url, state }]}>
      <NotificationsContext.Provider value={notifications}>
        <Routes>
          <Route path="/org/templates" element={<p>the list</p>} />
          <Route path="/org/templates/new" element={<TemplatePage />} />
          <Route path="/org/templates/:templateId" element={<TemplatePage />} />
        </Routes>
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  for (const f of Object.values(toast)) f.mockReset();
});
afterEach(() => vi.unstubAllGlobals());

describe("TemplatePage", () => {
  it("adds a template as revision 1, then opens it", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({
      "/api/v1/template/save-template": { id: "t9", name: "shop", revision: 1, warnings: ["SMTP_PASS is not a parameter"] },
      "/api/v1/template/get-template": { ...view(1), id: "t9", name: "shop" },
      "/api/v1/instance/get-instances": { instances: [] },
    });
    renderPage("/org/templates/new");
    await user.type(screen.getByLabelText("Template name"), "shop");
    await user.type(screen.getByLabelText("App name"), "Shop");
    await user.click(screen.getByRole("button", { name: "Save template" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Saved r1"));
    expect(toast.warning).toHaveBeenCalledWith("Check these references", { description: "SMTP_PASS is not a parameter" });
    const [sent] = api.posted("/api/v1/template/save-template") as { name: string; base_revision: number; id?: string }[];
    expect(sent).toMatchObject({ name: "shop", base_revision: 0 });
    expect(sent.id).toBeUndefined();
    expect(await screen.findByRole("heading", { name: "shop" })).toBeInTheDocument();
  });

  it("refuses to save what the API would refuse, naming it", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({});
    renderPage("/org/templates/new");
    await user.type(screen.getByLabelText("Template name"), "Shop");
    await user.click(screen.getByRole("button", { name: "Save template" }));
    expect(toast.error).toHaveBeenCalledWith(expect.stringContaining("Name: lowercase"));
    expect(api.calls).toEqual([]);
  });

  it("adds a revision on top of the one it read, with a note", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [] },
      "/api/v1/template/save-template": { id: "t1", name: "twillingate", revision: 4, warnings: null },
    });
    renderPage("/org/templates/t1");
    const save = await screen.findByRole("button", { name: "Save as r4" });
    expect(save).toBeDisabled();
    await user.click(screen.getAllByRole("button", { name: /Add parameter/ })[0]);
    await user.type(screen.getAllByLabelText("Parameter name").at(-1)!, "MEMORY_MAX");
    await user.type(screen.getByLabelText("Revision note"), "memory cap");
    await user.click(save);
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Saved r4"));
    const [sent] = api.posted("/api/v1/template/save-template") as {
      id: string;
      base_revision: number;
      note: string;
      body: TemplateBody;
    }[];
    expect(sent).toMatchObject({ id: "t1", base_revision: 3, note: "memory cap" });
    expect(sent.body.parameters?.map((p) => p.name)).toEqual(["NAME", "PORT", "VERSION", "MEMORY_MAX"]);
    expect(sent.body.backups).toEqual({ db: { enabled: true, storage: "tenants" } });
  });

  it("says who saved meanwhile on a concurrent edit, and reloads", async () => {
    const user = userEvent.setup();
    let latest = 3;
    fakeAPI({
      "/api/v1/template/get-template": () => view(latest),
      "/api/v1/instance/get-instances": { instances: [] },
      "/api/v1/template/save-template": () => {
        latest = 4;
        return new ApiError("someone saved the template meanwhile: reload it and save again", 409);
      },
    });
    renderPage("/org/templates/t1");
    await user.type(await screen.findByLabelText("App name"), "-x");
    await user.click(screen.getByRole("button", { name: "Save as r4" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Someone saved r4 meanwhile; reload");
    await user.click(screen.getByRole("button", { name: "Reload" }));
    expect(await screen.findByRole("button", { name: "Save as r5" })).toBeInTheDocument();
    expect(screen.getByLabelText("App name")).toHaveValue("${NAME}");
    expect(toast.error).not.toHaveBeenCalled();
  });

  it("reports any other failed save", async () => {
    const user = userEvent.setup();
    fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [] },
      "/api/v1/template/save-template": new Error("invalid template: files: x-winterflow.backups[0]: unknown type"),
    });
    renderPage("/org/templates/t1");
    await user.type(await screen.findByLabelText("App name"), "-x");
    await user.click(screen.getByRole("button", { name: "Save as r4" }));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Couldn't save the template", {
        description: "invalid template: files: x-winterflow.backups[0]: unknown type",
      }),
    );
  });

  it("offers Delete only without live instances", async () => {
    fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [{ key: "k1" }] },
    });
    renderPage("/org/templates/t1");
    expect(await screen.findByRole("button", { name: "Delete template" })).toBeDisabled();
    expect(screen.getByText("Revision 3 · 1 app use it")).toBeInTheDocument();
  });

  it("deletes a template no app uses", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [] },
      "/api/v1/template/delete-template": null,
    });
    renderPage("/org/templates/t1");
    await user.click(await screen.findByRole("button", { name: "Delete template" }));
    await user.click(screen.getByRole("button", { name: "Delete" }));
    expect(await screen.findByText("the list")).toBeInTheDocument();
    expect(api.posted("/api/v1/template/delete-template")).toEqual([{ id: "t1" }]);
  });

  it("explains a refused delete", async () => {
    const user = userEvent.setup();
    fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [] },
      "/api/v1/template/delete-template": new ApiError("the template has live instances: delete or unlink them first", 409),
    });
    renderPage("/org/templates/t1");
    await user.click(await screen.findByRole("button", { name: "Delete template" }));
    await user.click(screen.getByRole("button", { name: "Delete" }));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Couldn't delete the template", {
        description: "the template has live instances: delete or unlink them first",
      }),
    );
  });

  it("shows Not found for another org's template, and retries other failures", async () => {
    fakeAPI({ "/api/v1/template/get-template": new ApiError("template not found", 404), "/api/v1/instance/get-instances": { instances: [] } });
    renderPage("/org/templates/t1");
    expect(await screen.findByText("Template not found")).toBeInTheDocument();
    vi.unstubAllGlobals();

    const user = userEvent.setup();
    let fail = true;
    fakeAPI({
      "/api/v1/template/get-template": () => (fail ? new Error("database down") : view(3)),
      "/api/v1/instance/get-instances": { instances: [] },
    });
    renderPage("/org/templates/t2");
    expect(await screen.findByText("database down")).toBeInTheDocument();
    fail = false;
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findAllByRole("heading", { name: "twillingate" })).not.toHaveLength(0);
  });

  it("opens prefilled from Save as template, with its notes", async () => {
    const draft = draftFromBody("shop-front", BODY);
    renderPage("/org/templates/new", { draft, notes: ["Redirects aren't part of templates; they were left out."] });
    expect(screen.getByLabelText("Template name")).toHaveValue("shop-front");
    expect(screen.getByText("Redirects aren't part of templates; they were left out.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save template" })).toBeEnabled();
  });
});
```

- [ ] **Step 3: Run the tests**

Run: `pnpm --dir web exec vitest run src/components/template-editor.test.tsx src/pages/template-page.test.tsx`
Expected: FAIL: `Failed to resolve import "@/components/template-editor"` and `"@/pages/template-page"`

- [ ] **Step 4: Implement the editor:**

Create `web/src/components/template-editor.tsx`:

```tsx
import { Plus, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { EditorSection } from "@/components/editor-section";
import { FilesSection } from "@/components/files-section";
import { IconPicker } from "@/components/icon-picker";
import {
  ingressRow,
  paramRow,
  recipeBackupNames,
  type IngressDraft,
  type ParamRow,
  type TemplateDraft,
} from "@/lib/template-draft";
import { compilePattern } from "@/lib/template-params";

type Props = {
  draft: TemplateDraft;
  onChange: (next: TemplateDraft) => void;
};

// TemplateEditor edits a template revision: the app's files (the app
// editor's file editing, without secrets), the parameters an instance
// supplies, the app's name, icon and color, the ingress rows and the
// backup defaults.
export function TemplateEditor({ draft, onChange }: Props) {
  const set = (patch: Partial<TemplateDraft>) => onChange({ ...draft, ...patch });
  const setParam = (id: string, patch: Partial<ParamRow>) =>
    set({ parameters: draft.parameters.map((p) => (p.id === id ? { ...p, ...patch } : p)) });
  const setIngress = (id: string, patch: Partial<IngressDraft>) =>
    set({ ingress: draft.ingress.map((r) => (r.id === id ? { ...r, ...patch } : r)) });
  const backupNames = recipeBackupNames(draft);

  return (
    <div>
      <EditorSection title="Template" description="Lowercase letters, digits and hyphens; unique in the organization.">
        <div className="grid gap-2">
          <Label htmlFor="template-name">Template name</Label>
          <Input
            id="template-name"
            value={draft.name}
            placeholder="twillingate"
            className="font-mono"
            onChange={(e) => set({ name: e.target.value })}
          />
        </div>
      </EditorSection>

      <EditorSection
        title="App"
        description={
          <>
            How each app added from the template appears. The name may use <code>{"${PARAM}"}</code> of a plain
            parameter.
          </>
        }
      >
        <div className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2">
          <Label>Icon</Label>
          <Label htmlFor="template-app-name">App name</Label>
          <Label htmlFor="template-color">Color</Label>
          <IconPicker value={draft.icon} color={draft.color} name={draft.appName} onChange={(icon) => set({ icon })} />
          <Input
            id="template-app-name"
            value={draft.appName}
            placeholder="${NAME}"
            onChange={(e) => set({ appName: e.target.value })}
          />
          <Input
            id="template-color"
            type="color"
            className="h-9 w-14 cursor-pointer p-1"
            value={draft.color || "#64748b"}
            onChange={(e) => set({ color: e.target.value })}
          />
        </div>
      </EditorSection>

      <FilesSection
        files={draft.files}
        contents={draft.contents}
        onChange={(files, contents) => set({ files, contents })}
        allowSecret={false}
        imageChips={false}
        description={
          <>
            Copied unchanged into each app. <code>{"${PARAM}"}</code> takes a parameter's value at deploy; any other{" "}
            <code>{"${VAR}"}</code> must come from a shared variable on the server.
          </>
        }
      />

      <EditorSection
        title="Parameters"
        description="Each becomes an app variable of the same name. A secret's value is encrypted in the browser for the app's server; a template never holds one."
        action={
          <Button size="sm" variant="outline" onClick={() => set({ parameters: [...draft.parameters, paramRow()] })}>
            <Plus className="size-4" /> Add parameter
          </Button>
        }
      >
        {draft.parameters.length === 0 ? (
          <p className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">No parameters yet.</p>
        ) : (
          <div className="divide-y rounded-md border">
            {draft.parameters.map((p) => {
              const label = p.name || "parameter";
              const unchecked = p.pattern !== "" && compilePattern(p.pattern) === null;
              return (
                <div key={p.id} className="grid gap-2 p-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]">
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
                    placeholder={p.secret ? "Secrets have no default" : "Default (optional)"}
                    className="font-mono"
                    onChange={(e) => setParam(p.id, { default: e.target.value })}
                  />
                  <div className="flex items-center gap-4 text-sm">
                    <label className="flex items-center gap-2">
                      <Checkbox
                        aria-label={`${label} is secret`}
                        checked={p.secret}
                        onCheckedChange={(c) => setParam(p.id, { secret: c === true, default: c === true ? "" : p.default })}
                      />
                      Secret
                    </label>
                    <label className="flex items-center gap-2">
                      <Checkbox
                        aria-label={`${label} is required`}
                        checked={p.required}
                        onCheckedChange={(c) => setParam(p.id, { required: c === true })}
                      />
                      Required
                    </label>
                    <Button
                      size="icon"
                      variant="ghost"
                      className="ml-auto size-8 text-muted-foreground hover:text-destructive"
                      aria-label={`Remove ${label}`}
                      onClick={() => set({ parameters: draft.parameters.filter((x) => x.id !== p.id) })}
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </div>
                  <div className="grid gap-1">
                    <Input
                      aria-label={`Pattern of ${label}`}
                      value={p.pattern}
                      placeholder="Pattern, e.g. ^[a-z]{3,20}$ (optional)"
                      className="font-mono"
                      onChange={(e) => setParam(p.id, { pattern: e.target.value })}
                    />
                    {unchecked ? (
                      <p className="text-xs text-muted-foreground">
                        The browser can't check this pattern; values are checked when the app is saved.
                      </p>
                    ) : null}
                  </div>
                  <Input
                    aria-label={`Description of ${label}`}
                    value={p.description}
                    placeholder="Description (optional)"
                    className="sm:col-span-2"
                    onChange={(e) => setParam(p.id, { description: e.target.value })}
                  />
                </div>
              );
            })}
          </div>
        )}
      </EditorSection>

      <EditorSection
        title="Ingress"
        description={
          <>
            Hostnames routed to a port the compose file publishes on the host. Host and port may use{" "}
            <code>{"${PARAM}"}</code> of a plain parameter; the service is a note for people.
          </>
        }
        action={
          <Button size="sm" variant="outline" onClick={() => set({ ingress: [...draft.ingress, ingressRow()] })}>
            <Plus className="size-4" /> Add host
          </Button>
        }
      >
        {draft.ingress.length === 0 ? (
          <p className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">No hosts.</p>
        ) : (
          <div className="divide-y rounded-md border">
            {draft.ingress.map((r) => (
              <div key={r.id} className="flex flex-wrap items-center gap-2 p-2">
                <Input
                  aria-label="Host"
                  value={r.host}
                  placeholder="${NAME}.example.com"
                  className="min-w-48 flex-1 font-mono"
                  onChange={(e) => setIngress(r.id, { host: e.target.value })}
                />
                <Input
                  aria-label={`Port of ${r.host || "host"}`}
                  value={r.port}
                  placeholder="8080 or ${PORT}"
                  className="w-36 font-mono"
                  onChange={(e) => setIngress(r.id, { port: e.target.value })}
                />
                <Input
                  aria-label={`Service of ${r.host || "host"}`}
                  value={r.service}
                  placeholder="Service (optional)"
                  className="w-40"
                  onChange={(e) => setIngress(r.id, { service: e.target.value })}
                />
                <Button
                  size="icon"
                  variant="ghost"
                  className="size-8 text-muted-foreground hover:text-destructive"
                  aria-label={`Remove ${r.host || "host"}`}
                  onClick={() => set({ ingress: draft.ingress.filter((x) => x.id !== r.id) })}
                >
                  <Trash2 className="size-4" />
                </Button>
              </div>
            ))}
          </div>
        )}
      </EditorSection>

      <EditorSection
        title="Backup defaults"
        description={
          <>
            What each new app starts with, per backup the compose file declares under{" "}
            <code>x-winterflow.backups</code>. The storage is looked up by name on the app's server.
          </>
        }
      >
        {backupNames.length === 0 ? (
          <p className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">
            The compose file declares no backups.
          </p>
        ) : (
          <div className="divide-y rounded-md border">
            {backupNames.map((name) => {
              const def = draft.backups[name] ?? { enabled: false };
              const setDef = (patch: Partial<typeof def>) =>
                set({ backups: { ...draft.backups, [name]: { ...def, ...patch } } });
              return (
                <div key={name} className="flex flex-wrap items-center gap-3 p-2">
                  <Switch aria-label={`Back up ${name}`} checked={def.enabled} onCheckedChange={(on) => setDef({ enabled: on })} />
                  <code className="w-32 font-mono text-sm">{name}</code>
                  <Input
                    aria-label={`Storage of ${name}`}
                    value={def.storage ?? ""}
                    placeholder="Storage name"
                    className="w-48"
                    onChange={(e) => setDef({ storage: e.target.value })}
                  />
                </div>
              );
            })}
          </div>
        )}
      </EditorSection>
    </div>
  );
}
```

- [ ] **Step 5: Implement the page (Task 8 adds the Revisions and Instances tabs):**

Create `web/src/pages/template-page.tsx`:

```tsx
import { useCallback, useEffect, useMemo, useState } from "react";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { InlineError } from "@/components/inline-error";
import { NotFound } from "@/components/not-found";
import { TemplateEditor } from "@/components/template-editor";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { bodyFromDraft, draftFromBody, draftProblem, emptyDraft, type TemplateDraft } from "@/lib/template-draft";
import { ApiError, type InstanceView, type TemplateView } from "@/lib/templates";

// DraftState is what "Save as template" hands the new template's editor.
export type DraftState = { draft: TemplateDraft; notes: string[] };

// TemplatePage is one template (/org/templates/:templateId) or a new one
// (/org/templates/new, optionally prefilled by Save as template): its
// editor, where Save adds a revision.
export default function TemplatePage() {
  const { templateId } = useParams();
  const location = useLocation();
  const navigate = useNavigate();
  const api = useTemplatesApi();
  const prefill = (location.state as DraftState | null) ?? null;

  const [view, setView] = useState<TemplateView | null>(null);
  const [instances, setInstances] = useState<InstanceView[]>([]);
  const [draft, setDraft] = useState<TemplateDraft>(() => prefill?.draft ?? emptyDraft());
  const [baseline, setBaseline] = useState(() => (prefill ? "" : JSON.stringify(draft)));
  const [loadError, setLoadError] = useState<ApiError | Error | null>(null);
  const [note, setNote] = useState("");
  const [saving, setSaving] = useState(false);
  // conflict is the revision someone saved while this editor was open.
  const [conflict, setConflict] = useState<number | null>(null);

  const breadcrumbs = useMemo(
    () => [{ label: "Templates", href: "/org/templates" }, { label: view?.name ?? (templateId ? "Template" : "New template") }],
    [view?.name, templateId],
  );
  useAppBreadcrumbs(breadcrumbs);

  const load = useCallback(async () => {
    if (!templateId) return;
    setLoadError(null);
    try {
      const [v, list] = await Promise.all([api.getTemplate(templateId), api.listInstances({ templateId })]);
      const loaded = draftFromBody(v.name, v.body);
      setView(v);
      setInstances(list);
      setDraft(loaded);
      setBaseline(JSON.stringify(loaded));
      setConflict(null);
    } catch (e) {
      setLoadError(e instanceof Error ? e : new Error("Failed to load the template"));
    }
  }, [api, templateId]);

  useEffect(() => {
    void load();
  }, [load]);

  const dirty = JSON.stringify(draft) !== baseline;

  const save = async () => {
    const problem = draftProblem(draft);
    if (problem) {
      toast.error(problem);
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
      toast.success(`Saved r${saved.revision}`);
      if (saved.warnings?.length) {
        toast.warning("Check these references", { description: saved.warnings.join("\n") });
      }
      setNote("");
      if (!view) {
        navigate(`/org/templates/${saved.id}`, { replace: true });
        return;
      }
      await load();
    } catch (e) {
      if (e instanceof ApiError && e.status === 409 && view) {
        // Someone saved meanwhile: name their revision; Reload takes it.
        const latest = await api.getTemplate(view.id).catch(() => null);
        setConflict(latest?.latest_revision ?? view.latest_revision + 1);
      } else {
        toast.error("Couldn't save the template", { description: e instanceof Error ? e.message : undefined });
      }
    } finally {
      setSaving(false);
    }
  };

  const remove = async () => {
    if (!view) return;
    try {
      await api.deleteTemplate(view.id);
      toast.success(`Template ${view.name} deleted`);
      navigate("/org/templates");
    } catch (e) {
      toast.error("Couldn't delete the template", { description: e instanceof Error ? e.message : undefined });
    }
  };

  if (loadError) {
    if (loadError instanceof ApiError && loadError.status === 404) {
      return <NotFound title="Template not found" description="This template doesn't exist in your organization." />;
    }
    return (
      <div className="flex min-h-60 flex-col items-center justify-center gap-3">
        <InlineError>{loadError.message}</InlineError>
        <Button variant="outline" size="sm" onClick={() => void load()}>
          Retry
        </Button>
      </div>
    );
  }
  if (templateId && !view) {
    return (
      <div className="flex min-h-60 items-center justify-center">
        <Spinner />
      </div>
    );
  }

  const live = instances.length;

  return (
    <div className="mx-auto w-full max-w-5xl space-y-4">
      <header className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <div className="mr-auto min-w-0">
          <h1 className="truncate text-2xl font-semibold tracking-tight">{view?.name ?? "Add template"}</h1>
          <p className="text-sm text-muted-foreground">
            {view
              ? `Revision ${view.latest_revision} · ${live} ${live === 1 ? "app" : "apps"} use it`
              : "Saving adds revision 1."}
          </p>
        </div>
        {view ? (
          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button
                variant="outline"
                size="sm"
                className="text-destructive"
                disabled={live > 0}
                title={live > 0 ? "Delete or unlink its apps first" : undefined}
              >
                Delete template
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>Delete {view.name}?</AlertDialogTitle>
                <AlertDialogDescription>
                  Its revisions are deleted. No app uses it; apps unlinked from it stay as they are.
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>Cancel</AlertDialogCancel>
                <AlertDialogAction onClick={() => void remove()}>Delete</AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        ) : null}
      </header>

      {prefill?.notes.length ? (
        <ul className="list-disc space-y-1 rounded-md border bg-muted/30 py-3 pr-4 pl-8 text-sm text-muted-foreground">
          {prefill.notes.map((n) => (
            <li key={n}>{n}</li>
          ))}
        </ul>
      ) : null}

      <div className="sticky top-0 z-10 -mx-4 flex flex-wrap items-center gap-2 border-b bg-background/95 px-4 py-3 backdrop-blur supports-[backdrop-filter]:bg-background/80">
        {conflict !== null ? (
          <p role="alert" className="mr-auto flex items-center gap-2 text-sm text-destructive">
            Someone saved r{conflict} meanwhile; reload to get it (your changes here are dropped).
            <Button size="sm" variant="outline" onClick={() => void load()}>
              Reload
            </Button>
          </p>
        ) : (
          <Input
            aria-label="Revision note"
            value={note}
            placeholder="What changed (optional)"
            className="mr-auto max-w-md"
            onChange={(e) => setNote(e.target.value)}
          />
        )}
        <Button onClick={() => void save()} disabled={saving || (!dirty && !!view) || conflict !== null}>
          {saving ? "Saving…" : view ? `Save as r${view.latest_revision + 1}` : "Save template"}
        </Button>
      </div>
      <TemplateEditor draft={draft} onChange={setDraft} />
    </div>
  );
}
```

- [ ] **Step 6: Route it:**

In `web/src/main.tsx`:

Replace:

```tsx
import OrgVariablesPage from "@/pages/org-variables";
import OrgTemplatesPage from "@/pages/org-templates"
import LoginPage from "@/pages/login"
import RegisterPage from "@/pages/register"
```

with:

```tsx
import OrgVariablesPage from "@/pages/org-variables";
import OrgTemplatesPage from "@/pages/org-templates"
import TemplatePage from "@/pages/template-page"
import LoginPage from "@/pages/login"
import RegisterPage from "@/pages/register"
```

Replace:

```tsx
            element: <OrgTemplatesPage />,
          },
          { path: "*", element: <NotFound /> },
        ],
```

with:

```tsx
            element: <OrgTemplatesPage />,
          },
          {
            path: "/org/templates/new",
            element: <TemplatePage />,
          },
          {
            path: "/org/templates/:templateId",
            element: <TemplatePage />,
          },
          { path: "*", element: <NotFound /> },
        ],
```

- [ ] **Step 7: Run the tests**

Run: `pnpm --dir web exec vitest run src/components/template-editor.test.tsx src/pages/template-page.test.tsx`
Expected: PASS (15 tests)

- [ ] **Step 8: Check**

Run: `pnpm --dir web exec tsc -b && pnpm --dir web run lint`
Expected: no errors

- [ ] **Step 9: Commit**

```bash
git add web/src/components/template-editor.tsx \
  web/src/components/template-editor.test.tsx \
  web/src/pages/template-page.tsx \
  web/src/pages/template-page.test.tsx \
  web/src/main.tsx
git commit -m "feat(web): template editor and page" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Revisions with diffs, Instances with per-row Upgrade

**Files:**
- Create: `web/src/components/parameter-fields.tsx`
- Create: `web/src/components/upgrade-instance-dialog.tsx`
- Create: `web/src/components/template-revisions.tsx`
- Create: `web/src/components/template-instances.tsx`
- Modify: `web/src/pages/template-page.tsx`
- Test: `web/src/components/template-revisions.test.tsx`
- Test: `web/src/components/template-instances.test.tsx`
- Test: `web/src/pages/template-page.test.tsx`

**Interfaces:**
- Consumes: `diffLines`, `hunks` (Task 3); `fieldErrors`, `requiredFirst`, `upgradeNeeds`, `expand`, `plainValues` (Task 2); `useTemplatesApi`, `sealSecrets`, `InstanceView`, `TemplateView`, `TemplateBody` (Task 1); `useServers`, `serverPath`, `AppStatusBadge`.
- Produces: `ParameterFields({params, values, onChange: (name, value) => void, kept?: readonly string[], showRequired?: boolean})` (`@/components/parameter-fields`; each input's accessible name is the parameter name); `UpgradeInstanceDialog({instance, target: TemplateView, open, onOpenChange, onDone})` (`@/components/upgrade-instance-dialog`); `TemplateRevisions({view})`; `TemplateInstances({view, instances, onChanged})`. The template page gets tabs Editor / Revisions / Instances in `?tab=`.

A revision's diff compares each file and a "template settings" pseudo-file (everything but the files, as indented JSON) with the revision before it, three lines of context. Upgrade asks only for what the target revision needs that the instance doesn't hold (`upgradeNeeds`: a newly required value without a default; for a pending instance every required secret, since the agent holds none). Secrets are encrypted with the instance's **own** server key, read when Upgrade is pressed. Upgrade all and the rollout strip are D1b and are not built.

- [ ] **Step 1: Write the failing tests:**

Create `web/src/components/template-revisions.test.tsx`:

```tsx
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
    expect(screen.getByText("First revision")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Changes from r2" }));
    const compose = await screen.findByRole("region", { name: "Changes to compose.yaml" });
    expect(within(compose).getByText("- image: app:1", { exact: false })).toBeInTheDocument();
    expect(within(compose).getByText("+ image: app:2", { exact: false })).toBeInTheDocument();
    const settings = screen.getByRole("region", { name: "Changes to template settings" });
    expect(settings).toHaveTextContent('+ "app_name": "shop-${NAME}",');

    await user.click(screen.getByRole("button", { name: "Changes from r1" }));
    const added = await screen.findByRole("region", { name: "Changes to Caddyfile" });
    expect(added).toHaveTextContent("added");
    expect(screen.queryByRole("region", { name: "Changes to compose.yaml" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Changes from r1" }));
    expect(screen.queryByRole("region", { name: "Changes to Caddyfile" })).not.toBeInTheDocument();
  });

  it("says when a revision changed nothing, and when it can't be read", async () => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/template/get-template": { ...VIEW, body: BODIES["1"] } });
    renderRevisions();
    await user.click(screen.getByRole("button", { name: "Changes from r2" }));
    expect(await screen.findByText("No changes.")).toBeInTheDocument();
    vi.unstubAllGlobals();

    fakeAPI({ "/api/v1/template/get-template": new Error("template revision not found") });
    await user.click(screen.getByRole("button", { name: "Changes from r1" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("template revision not found");
  });
});
```

Create `web/src/components/template-instances.test.tsx`:

```tsx
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { TemplateInstances } from "@/components/template-instances";
import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import type { InstanceView, TemplateView } from "@/lib/templates";
import { fakeAPI } from "@/test/fake-api";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
vi.mock("@/lib/ecies", () => ({
  ENCRYPTED: "<encrypted>",
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));
vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ servers: [{ id: "s1", name: "alpha" }, { id: "s2", name: "beta" }] }),
}));

const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.fn() } as NotificationsContextValue;

const VIEW: TemplateView = {
  id: "t1",
  name: "twillingate",
  latest_revision: 3,
  revision: 3,
  body: {
    files: { "compose.yaml": "services: {}\n" },
    parameters: [
      { name: "NAME", required: true },
      { name: "REGION", required: true },
      { name: "SESSION_KEY", secret: true, required: true },
    ],
    app_name: "${NAME}",
    icon: "",
    color: "",
    ingress: null,
    backups: null,
  },
  note: "",
  created_at: "2026-10-12T10:00:00Z",
  created_by: "u1",
  revisions: null,
};

const inst = (over: Partial<InstanceView>): InstanceView => ({
  key: "k1",
  server_id: "s1",
  app_id: "a1",
  template_id: "t1",
  template_name: "twillingate",
  revision: 2,
  parameters: { NAME: "bakomi" },
  secrets: { SESSION_KEY: "<encrypted>" },
  state: "created",
  status_code: 1,
  last_error: "",
  created_at: "2026-10-12T10:00:00Z",
  updated_at: "2026-10-12T10:00:00Z",
  ...over,
});

const onChanged = vi.fn();

function renderInstances(instances: InstanceView[]) {
  return render(
    <MemoryRouter>
      <NotificationsContext.Provider value={notifications}>
        <TemplateInstances view={VIEW} instances={instances} onChanged={onChanged} />
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  onChanged.mockReset();
  toast.success.mockReset();
});
afterEach(() => vi.unstubAllGlobals());

describe("TemplateInstances", () => {
  it("lists instances per server with revision and status", () => {
    renderInstances([
      inst({}),
      inst({ key: "k2", server_id: "s2", app_id: "a2", revision: 3, parameters: { NAME: "shop" }, state: "pending", last_error: "pull failed" }),
    ]);
    const rows = screen.getAllByRole("listitem");
    expect(within(rows[0]).getByRole("link", { name: "bakomi" })).toHaveAttribute("href", "/server/s1/app/a1");
    expect(rows[0]).toHaveTextContent("on alpha");
    expect(rows[0]).toHaveTextContent("r2 · behind");
    expect(within(rows[0]).getByText("Running")).toBeInTheDocument();
    expect(within(rows[1]).getByText("Not created yet")).toHaveAttribute("title", "pull failed");
    expect(within(rows[1]).queryByRole("button", { name: "Upgrade" })).not.toBeInTheDocument();
  });

  it("upgrades one instance, asking only for the newly required value", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({ "/api/v1/instance/upgrade-instance": { instance: inst({ revision: 3 }), request_id: "", warnings: [] } });
    renderInstances([inst({})]);
    await user.click(screen.getByRole("button", { name: "Upgrade" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).queryByLabelText("NAME")).not.toBeInTheDocument();
    expect(within(dialog).queryByLabelText("SESSION_KEY")).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Upgrade to r3" }));
    expect(within(dialog).getByText("Required")).toBeInTheDocument();
    expect(api.calls).toEqual([]);

    await user.type(within(dialog).getByLabelText("REGION"), "eu");
    await user.click(within(dialog).getByRole("button", { name: "Upgrade to r3" }));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    expect(api.posted("/api/v1/instance/upgrade-instance")).toEqual([
      { server_id: "s1", key: "k1", revision: 3, parameters: { REGION: "eu" } },
    ]);
    expect(toast.success).toHaveBeenCalledWith("Upgraded to r3");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("asks a pending instance for its secrets again and encrypts them for its server", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({
      "/api/v1/server/get-public-key": { public_key: "K2" },
      "/api/v1/instance/upgrade-instance": { instance: inst({}), request_id: "", warnings: [] },
    });
    renderInstances([inst({ server_id: "s2", state: "pending", parameters: { NAME: "x", REGION: "eu" } })]);
    await user.click(screen.getByRole("button", { name: "Upgrade" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("SESSION_KEY"), "hunter2");
    await user.click(within(dialog).getByRole("button", { name: "Upgrade to r3" }));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    expect(api.posted("/api/v1/instance/upgrade-instance")).toEqual([
      { server_id: "s2", key: "k1", revision: 3, secrets: { SESSION_KEY: "ct(K2):hunter2" } },
    ]);
    expect(JSON.stringify(api.calls)).not.toContain('"hunter2"');
  });

  it("keeps the dialog open with the reason when the server has no key", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({ "/api/v1/server/get-public-key": new Error("public key unavailable for this server") });
    renderInstances([inst({ secrets: null, parameters: { NAME: "x", REGION: "eu" } })]);
    await user.click(screen.getByRole("button", { name: "Upgrade" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("SESSION_KEY"), "hunter2");
    await user.click(within(dialog).getByRole("button", { name: "Upgrade to r3" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("This server has no encryption key yet");
    expect(api.posted("/api/v1/instance/upgrade-instance")).toEqual([]);
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("says when no app uses the template", () => {
    renderInstances([]);
    expect(screen.getByText(/No app uses this template yet/)).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Add the tabs test to the page test (it now mocks `@/context/use-servers`, and `renderPage` takes a URL with a query):**

In `web/src/pages/template-page.test.tsx`:

Replace:

```tsx
vi.mock("sonner", () => ({ toast }));
vi.mock("@/layouts/use-app-layout", () => ({ useAppBreadcrumbs: () => {} }));
vi.mock("@/components/code-editor", () => ({
  CodeEditor: ({ value, onChange, filename }: { value: string; onChange?: (v: string) => void; filename?: string }) => (
```

with:

```tsx
vi.mock("sonner", () => ({ toast }));
vi.mock("@/layouts/use-app-layout", () => ({ useAppBreadcrumbs: () => {} }));
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ servers: [{ id: "s1", name: "alpha" }] }) }));
vi.mock("@/components/code-editor", () => ({
  CodeEditor: ({ value, onChange, filename }: { value: string; onChange?: (v: string) => void; filename?: string }) => (
```

Replace:

```tsx
function renderPage(url: string, state?: unknown) {
  return render(
    <MemoryRouter initialEntries={[{ pathname: url, state }]}>
      <NotificationsContext.Provider value={notifications}>
        <Routes>
```

with:

```tsx
function renderPage(url: string, state?: unknown) {
  return render(
    <MemoryRouter initialEntries={[state ? { pathname: url, state } : url]}>
      <NotificationsContext.Provider value={notifications}>
        <Routes>
```

Replace:

```tsx
    expect(screen.getByRole("button", { name: "Save template" })).toBeEnabled();
  });
});
```

with:

```tsx
    expect(screen.getByRole("button", { name: "Save template" })).toBeEnabled();
  });

  it("has the revisions and the instances in tabs; Upgrade refreshes the instances only", async () => {
    const user = userEvent.setup();
    const at = (revision: number) => ({
      key: "k1",
      server_id: "s1",
      app_id: "a1",
      template_id: "t1",
      template_name: "twillingate",
      revision,
      parameters: { NAME: "bakomi", PORT: "8081", VERSION: "1.0" },
      secrets: null,
      state: "created",
      status_code: 1,
      last_error: "",
      created_at: "2026-10-12T10:00:00Z",
      updated_at: "2026-10-12T10:00:00Z",
    });
    let instances = [at(2)];
    const api = fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": () => ({ instances }),
      "/api/v1/instance/upgrade-instance": () => {
        instances = [at(3)];
        return { instance: at(3), request_id: "", warnings: [] };
      },
    });
    renderPage("/org/templates/t1?tab=instances");
    await user.click(await screen.findByRole("button", { name: "Upgrade" }));
    await user.click(await screen.findByRole("button", { name: "Upgrade to r3" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Upgrade" })).not.toBeInTheDocument());
    expect(screen.getByText("r3")).toBeInTheDocument();
    expect(api.calls.filter((c) => c.path === "/api/v1/template/get-template")).toHaveLength(1);

    await user.click(screen.getByRole("tab", { name: "Revisions" }));
    expect(await screen.findByRole("button", { name: "Changes from r2" })).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "Editor" }));
    expect(screen.getByLabelText("Template name")).toHaveValue("twillingate");
  });
});
```

- [ ] **Step 3: Run the tests**

Run: `pnpm --dir web exec vitest run src/components/template-revisions.test.tsx src/components/template-instances.test.tsx src/pages/template-page.test.tsx`
Expected: FAIL: `Failed to resolve import "@/components/template-revisions"` / `"@/components/template-instances"`; the page test fails with `Unable to find role="button" and name "Upgrade"`

- [ ] **Step 4: The parameter form (also used by Tasks 9 and 10):**

Create `web/src/components/parameter-fields.tsx`:

```tsx
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { PasswordInput } from "@/components/ui/password-input";
import { fieldErrors, requiredFirst } from "@/lib/template-params";
import type { Parameter } from "@/lib/templates";

type Props = {
  params: Parameter[] | null | undefined;
  values: Record<string, string>; // plain values and typed secrets by name
  onChange: (name: string, value: string) => void;
  // kept are the secrets the agent holds: blank keeps them.
  kept?: readonly string[];
  // showRequired reveals "Required" (after a submit attempt); a value that
  // breaks its pattern shows as it is typed.
  showRequired?: boolean;
};

// ParameterFields is a form over a template's parameters: required ones
// first, each checked as typed, secrets masked (the caller encrypts them
// for the server before anything is sent).
export function ParameterFields({ params, values, onChange, kept = [], showRequired = false }: Props) {
  const errors = fieldErrors(params, values, kept);
  return (
    <div className="grid gap-4">
      {requiredFirst(params).map((p) => {
        const id = `param-${p.name}`;
        const err = errors[p.name] ?? "";
        const shown = err !== "" && (err !== "Required" || showRequired);
        const value = values[p.name] ?? "";
        return (
          <div key={p.name} className="grid gap-1.5">
            <Label htmlFor={id} className="font-mono">
              {p.name}
              {p.required ? (
                <span aria-hidden className="text-destructive">
                  *
                </span>
              ) : null}
              {p.secret ? <span className="font-sans text-xs font-normal text-muted-foreground">secret</span> : null}
            </Label>
            {p.secret ? (
              <PasswordInput
                id={id}
                aria-label={p.name}
                value={value}
                autoComplete="new-password"
                placeholder={kept.includes(p.name) ? "Stored, leave blank to keep" : "Encrypted in your browser"}
                aria-invalid={shown || undefined}
                onChange={(e) => onChange(p.name, e.target.value)}
              />
            ) : (
              <Input
                id={id}
                aria-label={p.name}
                value={value}
                className="font-mono"
                placeholder={p.default ? `Default: ${p.default}` : undefined}
                aria-invalid={shown || undefined}
                onChange={(e) => onChange(p.name, e.target.value)}
              />
            )}
            {p.description ? <p className="text-xs text-muted-foreground">{p.description}</p> : null}
            {shown ? <p className="text-xs text-destructive">{err}</p> : null}
          </div>
        );
      })}
    </div>
  );
}
```

- [ ] **Step 5: The Upgrade dialog (also used on the app page, Task 10):**

Create `web/src/components/upgrade-instance-dialog.tsx`:

```tsx
import { useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { InlineError } from "@/components/inline-error";
import { ParameterFields } from "@/components/parameter-fields";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { fieldErrors, upgradeNeeds } from "@/lib/template-params";
import { sealSecrets, type InstanceView, type TemplateView } from "@/lib/templates";

type Props = {
  instance: InstanceView;
  // target is the revision to move to (its body says what it needs).
  target: TemplateView;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onDone: () => void;
};

// UpgradeInstanceDialog moves one instance to another revision of its
// template, asking only for the values the instance doesn't hold yet
// (a newly required parameter, a pending instance's secrets).
export function UpgradeInstanceDialog({ instance, target, open, onOpenChange, onDone }: Props) {
  const api = useTemplatesApi();
  const needs = upgradeNeeds(target.body.parameters, instance);
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
      const plain: Record<string, string> = {};
      const typed: Record<string, string> = {};
      for (const p of needs) (p.secret ? typed : plain)[p.name] = values[p.name] ?? "";
      const secrets = await sealSecrets(api, instance.server_id, typed);
      await api.upgradeInstance({
        server_id: instance.server_id,
        key: instance.key,
        revision: target.revision,
        ...(Object.keys(plain).length ? { parameters: plain } : {}),
        ...(Object.keys(secrets).length ? { secrets } : {}),
      });
      toast.success(`Upgraded to r${target.revision}`);
      onOpenChange(false);
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The upgrade failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !busy && onOpenChange(o)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            Upgrade to r{target.revision}
          </DialogTitle>
          <DialogDescription>
            The app is redeployed with {target.name} r{target.revision}; its values and backup settings stay.
          </DialogDescription>
        </DialogHeader>
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
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button disabled={busy} onClick={() => void upgrade()}>
            {busy ? "Upgrading…" : `Upgrade to r${target.revision}`}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
```

Create `web/src/components/template-revisions.tsx`:

```tsx
import { useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { InlineError } from "@/components/inline-error";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { diffLines, hunks } from "@/lib/line-diff";
import type { TemplateBody, TemplateView } from "@/lib/templates";
import { cn, relativeTime } from "@/lib/utils";

// SETTINGS names the pseudo-file holding everything but the files.
const SETTINGS = "template settings";

// revisionTexts are a body's comparable texts: each file, and its other
// fields as indented JSON.
function revisionTexts(body: TemplateBody): Record<string, string> {
  const { files, ...rest } = body;
  return { ...(files ?? {}), [SETTINGS]: JSON.stringify(rest, null, 2) };
}

type FileDiff = { name: string; status: "added" | "removed" | "changed"; hunks: ReturnType<typeof hunks> };

function diffBodies(before: TemplateBody, after: TemplateBody): FileDiff[] {
  const a = revisionTexts(before);
  const b = revisionTexts(after);
  const names = [...new Set([...Object.keys(a), ...Object.keys(b)])].sort();
  const out: FileDiff[] = [];
  for (const name of names) {
    const h = hunks(diffLines(a[name] ?? "", b[name] ?? ""));
    if (h.length === 0) continue;
    out.push({ name, status: !(name in a) ? "added" : !(name in b) ? "removed" : "changed", hunks: h });
  }
  return out;
}

// TemplateRevisions lists a template's revisions, newest first; each opens
// its changes against the revision before it.
export function TemplateRevisions({ view }: { view: TemplateView }) {
  const api = useTemplatesApi();
  const [open, setOpen] = useState<number | null>(null);
  const [diffs, setDiffs] = useState<Record<number, FileDiff[] | string>>({});

  const toggle = async (revision: number) => {
    if (open === revision) {
      setOpen(null);
      return;
    }
    setOpen(revision);
    if (diffs[revision]) return;
    try {
      const [prev, cur] = await Promise.all([api.getTemplate(view.id, revision - 1), api.getTemplate(view.id, revision)]);
      setDiffs((d) => ({ ...d, [revision]: diffBodies(prev.body, cur.body) }));
    } catch (e) {
      setDiffs((d) => ({ ...d, [revision]: e instanceof Error ? e.message : "Failed to load the revisions" }));
    }
  };

  return (
    <ol className="divide-y rounded-md border">
      {(view.revisions ?? []).map((r) => {
        const diff = diffs[r.revision];
        return (
          <li key={r.revision} className="px-4 py-3 text-sm">
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <span className="font-medium">r{r.revision}</span>
              <span className="min-w-0 flex-1 truncate text-muted-foreground">{r.note || "No note"}</span>
              <time className="text-xs text-muted-foreground" dateTime={r.created_at}>
                {relativeTime(new Date(r.created_at))}
              </time>
              {r.revision > 1 ? (
                <Button size="sm" variant="ghost" onClick={() => void toggle(r.revision)}>
                  {open === r.revision ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />}
                  Changes from r{r.revision - 1}
                </Button>
              ) : (
                <span className="text-xs text-muted-foreground">First revision</span>
              )}
            </div>
            {open === r.revision ? (
              <div className="mt-3 space-y-3">
                {diff === undefined ? (
                  <Spinner />
                ) : typeof diff === "string" ? (
                  <InlineError>{diff}</InlineError>
                ) : diff.length === 0 ? (
                  <p className="text-muted-foreground">No changes.</p>
                ) : (
                  diff.map((f) => (
                    <section key={f.name} aria-label={`Changes to ${f.name}`} className="overflow-hidden rounded-md border">
                      <h3 className="border-b bg-muted/40 px-3 py-1.5 font-mono text-xs">
                        {f.name}
                        {f.status !== "changed" ? <span className="ml-2 font-sans text-muted-foreground">{f.status}</span> : null}
                      </h3>
                      <pre className="overflow-x-auto font-mono text-xs leading-5">
                        {f.hunks.map((h, i) => (
                          <div key={i}>
                            {h.skipped > 0 ? (
                              <div className="bg-muted/30 px-3 text-muted-foreground">… {h.skipped} unchanged</div>
                            ) : null}
                            {h.lines.map((l, j) => (
                              <div
                                key={j}
                                className={cn(
                                  "px-3",
                                  l.op === "+" && "bg-emerald-500/10 text-emerald-800 dark:text-emerald-300",
                                  l.op === "-" && "bg-red-500/10 text-red-800 dark:text-red-300",
                                )}
                              >
                                {l.op} {l.text}
                              </div>
                            ))}
                          </div>
                        ))}
                      </pre>
                    </section>
                  ))
                )}
              </div>
            ) : null}
          </li>
        );
      })}
    </ol>
  );
}
```

Create `web/src/components/template-instances.tsx`:

```tsx
import { useState } from "react";
import { Link } from "react-router-dom";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { AppStatusBadge } from "@/components/app-status-badge";
import { UpgradeInstanceDialog } from "@/components/upgrade-instance-dialog";
import { useServers } from "@/context/use-servers";
import { serverPath } from "@/lib/server-paths";
import { expand, plainValues } from "@/lib/template-params";
import type { InstanceView, TemplateView } from "@/lib/templates";

// TemplateInstances lists the apps a template runs as, across the org's
// servers: where, on which revision, how they are doing, and Upgrade for
// the ones behind the latest revision (view is the latest).
export function TemplateInstances({
  view,
  instances,
  onChanged,
}: {
  view: TemplateView;
  instances: InstanceView[];
  onChanged: () => void;
}) {
  const { servers } = useServers();
  const [upgrading, setUpgrading] = useState<InstanceView | null>(null);
  const serverName = (id: string) => servers.find((s) => s.id === id)?.name ?? id;

  if (instances.length === 0) {
    return (
      <p className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
        No app uses this template yet. Add one on a server: Apps → From template.
      </p>
    );
  }

  return (
    <>
      <ul className="divide-y rounded-md border">
        {instances.map((inst) => {
          const name = expand(view.body.app_name, plainValues(view.body.parameters, inst.parameters ?? {}));
          const behind = inst.revision < view.latest_revision;
          return (
            <li key={inst.key} className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3 text-sm">
              <div className="min-w-0 flex-1">
                <Link to={serverPath(inst.server_id, `/app/${inst.app_id}`)} className="font-medium hover:underline">
                  {name || inst.app_id.slice(0, 8)}
                </Link>
                <p className="text-xs text-muted-foreground">on {serverName(inst.server_id)}</p>
              </div>
              <span className={behind ? "text-amber-700 dark:text-amber-400" : "text-muted-foreground"}>
                r{inst.revision}
                {behind ? " · behind" : ""}
              </span>
              {inst.state === "pending" ? (
                <Badge variant="outline" title={inst.last_error || undefined}>
                  Not created yet
                </Badge>
              ) : (
                <AppStatusBadge status={inst.status_code} />
              )}
              {behind ? (
                <Button size="sm" variant="outline" onClick={() => setUpgrading(inst)}>
                  Upgrade
                </Button>
              ) : null}
            </li>
          );
        })}
      </ul>
      {upgrading ? (
        <UpgradeInstanceDialog
          key={upgrading.key}
          instance={upgrading}
          target={view}
          open
          onOpenChange={(o) => {
            if (!o) setUpgrading(null);
          }}
          onDone={onChanged}
        />
      ) : null}
    </>
  );
}
```

- [ ] **Step 6: Put the editor, the revisions and the instances in tabs (an upgrade re-reads the instances only, so unsaved editor changes survive):**

In `web/src/pages/template-page.tsx`:

Replace:

```tsx
import { useCallback, useEffect, useMemo, useState } from "react";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import { toast } from "sonner";
```

with:

```tsx
import { useCallback, useEffect, useMemo, useState } from "react";
import { useLocation, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { toast } from "sonner";
```

Replace:

```tsx
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import {
  AlertDialog,
```

with:

```tsx
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  AlertDialog,
```

Replace:

```tsx
import { NotFound } from "@/components/not-found";
import { TemplateEditor } from "@/components/template-editor";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { bodyFromDraft, draftFromBody, draftProblem, emptyDraft, type TemplateDraft } from "@/lib/template-draft";
import { ApiError, type InstanceView, type TemplateView } from "@/lib/templates";

// DraftState is what "Save as template" hands the new template's editor.
```

with:

```tsx
import { NotFound } from "@/components/not-found";
import { TemplateEditor } from "@/components/template-editor";
import { TemplateInstances } from "@/components/template-instances";
import { TemplateRevisions } from "@/components/template-revisions";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { bodyFromDraft, draftFromBody, draftProblem, emptyDraft, type TemplateDraft } from "@/lib/template-draft";
import { ApiError, type InstanceView, type TemplateView } from "@/lib/templates";

const TABS = ["editor", "revisions", "instances"] as const;

// DraftState is what "Save as template" hands the new template's editor.
```

Replace:

```tsx
// TemplatePage is one template (/org/templates/:templateId) or a new one
// (/org/templates/new, optionally prefilled by Save as template): its
// editor, where Save adds a revision.
export default function TemplatePage() {
  const { templateId } = useParams();
```

with:

```tsx
// TemplatePage is one template (/org/templates/:templateId) or a new one
// (/org/templates/new, optionally prefilled by Save as template): its
// editor, where Save adds a revision, its revisions with their changes and
// the apps it runs as. The tab lives in ?tab=.
export default function TemplatePage() {
  const { templateId } = useParams();
```

Replace:

```tsx
  const api = useTemplatesApi();
  const prefill = (location.state as DraftState | null) ?? null;

  const [view, setView] = useState<TemplateView | null>(null);
```

with:

```tsx
  const api = useTemplatesApi();
  const prefill = (location.state as DraftState | null) ?? null;
  const [params, setParams] = useSearchParams();
  const tab = (TABS as readonly string[]).includes(params.get("tab") ?? "") ? params.get("tab")! : "editor";

  const [view, setView] = useState<TemplateView | null>(null);
```

Replace:

```tsx
    void load();
  }, [load]);

  const dirty = JSON.stringify(draft) !== baseline;
```

with:

```tsx
    void load();
  }, [load]);

  // reloadInstances follows an upgrade without touching the editor.
  const reloadInstances = useCallback(() => {
    if (templateId) api.listInstances({ templateId }).then(setInstances, () => {});
  }, [api, templateId]);

  const dirty = JSON.stringify(draft) !== baseline;
```

Replace:

```tsx
  const live = instances.length;

  return (
```

with:

```tsx
  const live = instances.length;
  const editor = (
    <>
      <div className="sticky top-0 z-10 -mx-4 flex flex-wrap items-center gap-2 border-b bg-background/95 px-4 py-3 backdrop-blur supports-[backdrop-filter]:bg-background/80">
        {conflict !== null ? (
          <p role="alert" className="mr-auto flex items-center gap-2 text-sm text-destructive">
            Someone saved r{conflict} meanwhile; reload to get it (your changes here are dropped).
            <Button size="sm" variant="outline" onClick={() => void load()}>
              Reload
            </Button>
          </p>
        ) : (
          <Input
            aria-label="Revision note"
            value={note}
            placeholder="What changed (optional)"
            className="mr-auto max-w-md"
            onChange={(e) => setNote(e.target.value)}
          />
        )}
        <Button onClick={() => void save()} disabled={saving || (!dirty && !!view) || conflict !== null}>
          {saving ? "Saving…" : view ? `Save as r${view.latest_revision + 1}` : "Save template"}
        </Button>
      </div>
      <TemplateEditor draft={draft} onChange={setDraft} />
    </>
  );

  return (
```

Replace:

```tsx
      ) : null}

      <div className="sticky top-0 z-10 -mx-4 flex flex-wrap items-center gap-2 border-b bg-background/95 px-4 py-3 backdrop-blur supports-[backdrop-filter]:bg-background/80">
        {conflict !== null ? (
          <p role="alert" className="mr-auto flex items-center gap-2 text-sm text-destructive">
            Someone saved r{conflict} meanwhile; reload to get it (your changes here are dropped).
            <Button size="sm" variant="outline" onClick={() => void load()}>
              Reload
            </Button>
          </p>
        ) : (
          <Input
            aria-label="Revision note"
            value={note}
            placeholder="What changed (optional)"
            className="mr-auto max-w-md"
            onChange={(e) => setNote(e.target.value)}
          />
        )}
        <Button onClick={() => void save()} disabled={saving || (!dirty && !!view) || conflict !== null}>
          {saving ? "Saving…" : view ? `Save as r${view.latest_revision + 1}` : "Save template"}
        </Button>
      </div>
      <TemplateEditor draft={draft} onChange={setDraft} />
    </div>
  );
```

with:

```tsx
      ) : null}

      {view ? (
        <Tabs value={tab} onValueChange={(v) => setParams(v === "editor" ? {} : { tab: v }, { replace: true })}>
          <TabsList>
            <TabsTrigger value="editor">Editor</TabsTrigger>
            <TabsTrigger value="revisions">Revisions</TabsTrigger>
            <TabsTrigger value="instances">Instances</TabsTrigger>
          </TabsList>
          <TabsContent value="editor" className="mt-4">
            {editor}
          </TabsContent>
          <TabsContent value="revisions" className="mt-4">
            <TemplateRevisions view={view} />
          </TabsContent>
          <TabsContent value="instances" className="mt-4">
            <TemplateInstances view={view} instances={instances} onChanged={reloadInstances} />
          </TabsContent>
        </Tabs>
      ) : (
        editor
      )}
    </div>
  );
```

- [ ] **Step 7: Run the tests**

Run: `pnpm --dir web exec vitest run src/components/template-revisions.test.tsx src/components/template-instances.test.tsx src/pages/template-page.test.tsx`
Expected: PASS (18 tests)

- [ ] **Step 8: Check**

Run: `pnpm --dir web exec tsc -b && pnpm --dir web run lint`
Expected: no errors

- [ ] **Step 9: Commit**

```bash
git add web/src/components/parameter-fields.tsx \
  web/src/components/upgrade-instance-dialog.tsx \
  web/src/components/template-revisions.tsx \
  web/src/components/template-revisions.test.tsx \
  web/src/components/template-instances.tsx \
  web/src/components/template-instances.test.tsx \
  web/src/pages/template-page.tsx \
  web/src/pages/template-page.test.tsx
git commit -m "feat(web): template revisions, instances and per-instance upgrade" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Server: Add app → From template

**Files:**
- Create: `web/src/lib/host-check.ts`
- Modify: `web/src/components/hostname-field.tsx`
- Create: `web/src/pages/create-from-template.tsx`
- Modify: `web/src/pages/home.tsx`
- Modify: `web/src/main.tsx`
- Test: `web/src/pages/create-from-template.test.tsx`
- Test: `web/src/pages/home.test.tsx`

**Interfaces:**
- Consumes: `ParameterFields` (Task 8); `expand`, `fieldErrors`, `newKey`, `plainValues`, `previewHosts`, `HostPreview` (Task 2); `useTemplatesApi`, `ApiError`, `sealSecrets`, `TemplateSummary`, `TemplateView`, `UpdateInstanceBody` (Task 1); `useApps().refresh`, `useBackups().serverStorages`, `useServers().activeServerId`, `useServerPath`, `HostHints`, `validHostname`, `NativeSelect`, `EditorSection`.
- Produces: `checkHostname(serverId, appId, host): Promise<HostCheck | null>` moved to `@/lib/host-check` (unchanged; `hostname-field.tsx` imports it); default export `CreateFromTemplatePage` at `/server/:serverId/create-app/template`; a **From template** button beside **New App** on the Apps page.

The browser makes the instance key once per page (`newKey`), so a retry after a lost answer replays the same create (no second app). The create is sent without `wait` and awaited over SSE (10 min). If the API accepted the create but the deploy failed (the SSE result is an error), the instance is now *pending* under that key: the next Add app sends `update-instance` with the same key, every secret and the backup storages (a pending update renders like a create), and the template and revision selects lock. A refusal (4xx: a value, a host not covered, a missing storage) claimed nothing, so the next attempt is a create again; an `update-instance` 404 (the create never reached the API) also falls back to create. The hosts preview renders `${PARAM}` with the values typed so far (else the defaults) and runs the registered-domains check (`GET /api/v1/domains/check`, debounced) once a host is complete, showing B's coverage error inline. Storage selects list `get-storages` of this server, preselected to the template's storage; one the server lacks is flagged. Only changed storages are sent as `backups` overrides.

- [ ] **Step 1: Write the failing page test:**

Create `web/src/pages/create-from-template.test.tsx`:

```tsx
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Outlet, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import { ApiError, type TemplateView } from "@/lib/templates";
import CreateFromTemplatePage from "@/pages/create-from-template";
import { fakeAPI } from "@/test/fake-api";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
vi.mock("@/layouts/use-app-layout", () => ({ useAppBreadcrumbs: () => {} }));
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
      { name: "MEMORY_MAX", default: "256m" },
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

const waitFor_ = vi.fn();
const notifications = { connected: true, subscribe: () => () => {}, waitFor: waitFor_ } as NotificationsContextValue;

const routes = (extra: Record<string, unknown> = {}) => ({
  "/api/v1/template/get-templates": {
    templates: [{ id: "t1", name: "twillingate", latest_revision: 2, instances: 0, behind: 0, updated_at: "" }],
  },
  "/api/v1/template/get-template": VIEW,
  "/api/v1/domains/check": { available: true, covered: false, registration: null, claims: null },
  "/api/v1/server/get-public-key": { public_key: "K1" },
  "/api/v1/instance/get-instance": { app_id: "app-9", key: "k" },
  ...extra,
});

// accepted makes create-instance answer 202 (the result comes over SSE).
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

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/server/s1/create-app/template"]}>
      <NotificationsContext.Provider value={notifications}>
        <Routes>
          <Route element={<Outlet />}>
            <Route path="/server/s1/create-app/template" element={<CreateFromTemplatePage />} />
            <Route path="/server/s1/app/:appId" element={<p>the new app</p>} />
            <Route path="/org/templates" element={<p>templates</p>} />
          </Route>
        </Routes>
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  toast.success.mockReset();
  refresh.mockClear();
  waitFor_.mockReset();
});
afterEach(() => vi.unstubAllGlobals());

describe("CreateFromTemplatePage", () => {
  it("asks for required values first, checking patterns as typed", async () => {
    const user = userEvent.setup();
    fakeAPI(routes());
    renderPage();
    const fields = await screen.findAllByRole("textbox");
    expect(fields[0]).toHaveAccessibleName("NAME");
    expect(screen.getByLabelText("MEMORY_MAX")).toHaveAttribute("placeholder", "Default: 256m");
    await user.type(screen.getByLabelText("NAME"), "Shop");
    expect(screen.getByText("Must match ^[a-z]{3,20}$")).toBeInTheDocument();
    expect(screen.queryByText("Required")).not.toBeInTheDocument();
  });

  it("previews the hosts with the domain coverage check", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(routes());
    renderPage();
    expect(await screen.findByText("Fill in NAME.")).toBeInTheDocument();
    await user.type(screen.getByLabelText("NAME"), "shop");
    const hosts = screen.getByRole("list", { name: "Hosts" });
    expect(within(hosts).getByText("shop.twillingate.app")).toBeInTheDocument();
    expect(await within(hosts).findByText(/No domain on this server covers this hostname/)).toBeInTheDocument();
    const checks = api.calls.filter((c) => c.path === "/api/v1/domains/check");
    expect(checks.at(-1)?.query).toBe("?server_id=s1&domain=shop.twillingate.app");
    expect(screen.getByText("shop", { selector: "span.font-medium" })).toBeInTheDocument();
  });

  it("adds the app with encrypted secrets and a browser key, then opens it", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(routes({ "/api/v1/instance/create-instance": { request_id: "r1" } }));
    accepted(api);
    waitFor_.mockResolvedValue({ status: 0 });
    renderPage();
    await user.click(await screen.findByRole("button", { name: "Add app" }));
    expect(screen.getAllByText("Required")).toHaveLength(2);
    await user.type(screen.getByLabelText("NAME"), "shop");
    await user.type(screen.getByLabelText("SESSION_KEY"), "hunter2");
    await user.selectOptions(screen.getByLabelText("Storage for db"), "local");
    await user.click(screen.getByRole("button", { name: "Add app" }));
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
    expect(api.calls.find((c) => c.path === "/api/v1/instance/get-instance")?.query).toBe(`?server_id=s1&key=${sent.key}`);
  });

  it("retries a failed deploy as an update of the same key, and a refused create as a create", async () => {
    const user = userEvent.setup();
    let refuse = true;
    const api = fakeAPI(
      routes({
        "/api/v1/instance/create-instance": () =>
          refuse ? new ApiError("invalid template: parameters.NAME: \"abc\" is taken", 400) : { request_id: "r1" },
        "/api/v1/instance/update-instance": { request_id: "r2" },
      }),
    );
    renderPage();
    await user.type(await screen.findByLabelText("NAME"), "abc");
    await user.type(screen.getByLabelText("SESSION_KEY"), "s");
    await user.click(screen.getByRole("button", { name: "Add app" }));
    expect(await screen.findByRole("alert")).toHaveTextContent('"abc" is taken');

    refuse = false;
    accepted(api);
    waitFor_.mockResolvedValueOnce({ status: 1, error: "pull access denied" });
    await user.click(screen.getByRole("button", { name: "Add app" }));
    expect(await screen.findByText("pull access denied")).toBeInTheDocument();
    expect(screen.getByLabelText("Template")).toBeDisabled();

    accepted(api, "/api/v1/instance/update-instance");
    waitFor_.mockResolvedValueOnce({ status: 0 });
    await user.click(screen.getByRole("button", { name: "Add app" }));
    expect(await screen.findByText("the new app")).toBeInTheDocument();
    const creates = api.posted("/api/v1/instance/create-instance") as { key: string }[];
    const updates = api.posted("/api/v1/instance/update-instance") as { key: string; secrets: unknown }[];
    expect(creates).toHaveLength(2);
    expect(creates[0].key).toBe(creates[1].key);
    expect(updates).toEqual([expect.objectContaining({ key: creates[0].key, secrets: { SESSION_KEY: "ct(K1):s" } })]);
  });

  it("flags a template storage this server doesn't have", async () => {
    serverStorages.mockResolvedValueOnce(["local"]);
    fakeAPI(routes());
    renderPage();
    expect(await screen.findByText("This server has no storage named tenants.", { exact: false })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "tenants (not on this server)" })).toBeInTheDocument();
  });

  it("loads an older revision on request", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(routes());
    renderPage();
    await user.selectOptions(await screen.findByLabelText("Revision"), "1");
    await waitFor(() => expect(api.calls.at(-1)?.query).toBe("?id=t1&revision=1"));
  });

  it("points to Templates when the org has none, and shows load failures", async () => {
    fakeAPI(routes({ "/api/v1/template/get-templates": { templates: [] } }));
    renderPage();
    expect(await screen.findByRole("link", { name: "Add one" })).toHaveAttribute("href", "/org/templates");
    vi.unstubAllGlobals();

    fakeAPI(routes({ "/api/v1/template/get-template": new Error("template not found") }));
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("template not found");
  });
});
```

- [ ] **Step 2: Add the entry-point test:**

In `web/src/pages/home.test.tsx`:

Replace:

```tsx
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
```

with:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
```

Replace:

```tsx
    expect(strip.compareDocumentPosition(grid) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});
```

with:

```tsx
    expect(strip.compareDocumentPosition(grid) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("offers adding an app from a template", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <Routes>
          <Route path="/" element={<HomePage />} />
          <Route path="/create-app/template" element={<p>from template</p>} />
        </Routes>
      </MemoryRouter>,
    );
    await user.click(screen.getByRole("button", { name: /From template/ }));
    expect(await screen.findByText("from template")).toBeInTheDocument();
  });
});
```

- [ ] **Step 3: Run the tests**

Run: `pnpm --dir web exec vitest run src/pages/create-from-template.test.tsx src/pages/home.test.tsx`
Expected: FAIL: `Failed to resolve import "@/pages/create-from-template"`; the home test fails with `Unable to find role="button" and name /From template/`

- [ ] **Step 4: Move `checkHostname` out of `hostname-field.tsx` unchanged, exported:**

Create `web/src/lib/host-check.ts`:

```ts
import type { HostCheck, Issue } from "@/components/host-hints";
import { apiBaseUrl } from "@/config";
import type { RegisteredDomain } from "@/lib/domains";

// checkHostname asks the API what it thinks of a hostname on this server:
// whether another app claims it, and which registration covers it. null
// when the API refuses (the form validation explains bad input).
export async function checkHostname(
  serverId: string,
  appId: string | undefined,
  host: string,
): Promise<HostCheck | null> {
  const qs = new URLSearchParams({ server_id: serverId, domain: host });
  if (appId) qs.set("app_id", appId);
  const res = await fetch(`${apiBaseUrl}/api/v1/domains/check?${qs}`, {
    credentials: "include",
  });
  if (!res.ok) return null;
  const body = (await res.json()) as {
    data?: {
      available?: boolean;
      covered?: boolean;
      registration?: RegisteredDomain | null;
      claims?: { app_name: string }[] | null;
    };
  };
  const data = body.data;
  if (!data) return null;
  const claim = data.claims?.[0];
  const issue: Issue | undefined =
    data.available === false && claim
      ? { kind: "taken", app: claim.app_name }
      : data.covered === false
        ? { kind: "uncovered" }
        : undefined;
  return { issue, registration: data.registration ?? undefined };
}
```

- [ ] **Step 5: Import it there instead of defining it:**

In `web/src/components/hostname-field.tsx`:

Replace:

```tsx
import { Input } from "@/components/ui/input";
import { HostHints, Suggestions, type HostCheck, type Issue } from "@/components/host-hints";
import { apiBaseUrl } from "@/config";
import { validHostname, type RegisteredDomain } from "@/lib/domains";
import { cn } from "@/lib/utils";

// checkHostname asks the API what it thinks of a hostname on this server:
// whether another app claims it, and which registration covers it. null
// when the API refuses (the form validation explains bad input).
async function checkHostname(
  serverId: string,
  appId: string | undefined,
  host: string,
): Promise<HostCheck | null> {
  const qs = new URLSearchParams({ server_id: serverId, domain: host });
  if (appId) qs.set("app_id", appId);
  const res = await fetch(`${apiBaseUrl}/api/v1/domains/check?${qs}`, {
    credentials: "include",
  });
  if (!res.ok) return null;
  const body = (await res.json()) as {
    data?: {
      available?: boolean;
      covered?: boolean;
      registration?: RegisteredDomain | null;
      claims?: { app_name: string }[] | null;
    };
  };
  const data = body.data;
  if (!data) return null;
  const claim = data.claims?.[0];
  const issue: Issue | undefined =
    data.available === false && claim
      ? { kind: "taken", app: claim.app_name }
      : data.covered === false
        ? { kind: "uncovered" }
        : undefined;
  return { issue, registration: data.registration ?? undefined };
}

type Props = {
```

with:

```tsx
import { Input } from "@/components/ui/input";
import { HostHints, Suggestions, type HostCheck } from "@/components/host-hints";
import { checkHostname } from "@/lib/host-check";
import { validHostname, type RegisteredDomain } from "@/lib/domains";
import { cn } from "@/lib/utils";

type Props = {
```

- [ ] **Step 6: Implement the page:**

Create `web/src/pages/create-from-template.tsx`:

```tsx
import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import { EditorSection } from "@/components/editor-section";
import { HostHints, type HostCheck } from "@/components/host-hints";
import { InlineError } from "@/components/inline-error";
import { NativeSelect } from "@/components/native-select";
import { ParameterFields } from "@/components/parameter-fields";
import { useApps } from "@/context/use-apps";
import { useBackups } from "@/context/use-backups";
import { useServerPath } from "@/context/use-server-path";
import { useServers } from "@/context/use-servers";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { validHostname } from "@/lib/domains";
import { checkHostname } from "@/lib/host-check";
import { expand, fieldErrors, newKey, plainValues, previewHosts, type HostPreview } from "@/lib/template-params";
import { ApiError, sealSecrets, type TemplateSummary, type TemplateView, type UpdateInstanceBody } from "@/lib/templates";

const BREADCRUMBS = [{ label: "Apps", href: "/" }, { label: "From template" }];

// CreateFromTemplatePage is Add app → From template on a server: pick a
// template and revision, fill in its parameters (secrets encrypted in the
// browser for this server), pick storages for its backups, see the hosts
// the app will get, then add the app and open it.
export default function CreateFromTemplatePage() {
  useAppBreadcrumbs(BREADCRUMBS);
  const navigate = useNavigate();
  const toServer = useServerPath();
  const { activeServerId } = useServers();
  const { refresh } = useApps();
  const { serverStorages } = useBackups();
  const api = useTemplatesApi();

  const [templates, setTemplates] = useState<TemplateSummary[] | null>(null);
  const [templateId, setTemplateId] = useState("");
  const [revision, setRevision] = useState(0); // 0: the latest
  const [view, setView] = useState<TemplateView | null>(null);
  const [values, setValues] = useState<Record<string, string>>({});
  const [storages, setStorages] = useState<string[]>([]);
  const [backupStorage, setBackupStorage] = useState<Record<string, string>>({});
  const [loadError, setLoadError] = useState("");
  // key names this app to the API: a retry after a lost answer replays the
  // same create instead of adding a second app.
  const [key] = useState(newKey);
  // claimed: the API accepted the create (the key is taken), so a retry
  // after an agent failure is an update of that pending instance.
  const [claimed, setClaimed] = useState(false);
  const [tried, setTried] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    api.listTemplates().then(
      (list) => {
        setTemplates(list);
        if (list.length > 0) setTemplateId((id) => id || list[0].id);
      },
      (e: unknown) => setLoadError(e instanceof Error ? e.message : "Failed to load templates"),
    );
  }, [api]);

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
        const defaults: Record<string, string> = {};
        for (const [name, d] of Object.entries(v.body.backups ?? {})) defaults[name] = d.storage ?? "";
        setBackupStorage(defaults);
      },
      (e: unknown) => live && setLoadError(e instanceof Error ? e.message : "Failed to load the template"),
    );
    return () => {
      live = false;
    };
  }, [api, templateId, revision]);

  const params = view?.body.parameters ?? [];
  const plain = useMemo(() => plainValues(view?.body.parameters, values), [view, values]);
  const hosts = view ? previewHosts(view.body, plain) : [];
  const appName = view ? expand(view.body.app_name, plain) : "";
  const backups = Object.entries(view?.body.backups ?? {});

  // send creates the instance, or once the API holds this key (an earlier
  // attempt whose deploy failed: the instance is pending) updates it. A
  // refused create (4xx) claimed nothing.
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
      if (e instanceof ApiError) setClaimed(false);
      throw e;
    }
  };

  const create = async () => {
    if (!view || !activeServerId) return;
    setTried(true);
    if (Object.keys(fieldErrors(params, values)).length > 0) return;
    setBusy(true);
    setError("");
    try {
      const parameters: Record<string, string> = {};
      const typed: Record<string, string> = {};
      for (const p of params) {
        const v = values[p.name] ?? "";
        if (p.secret) typed[p.name] = v;
        else if (v !== "") parameters[p.name] = v;
      }
      const secrets = await sealSecrets(api, activeServerId, typed);
      const overrides: Record<string, string> = {};
      for (const [name, d] of backups) {
        if ((backupStorage[name] ?? "") !== (d.storage ?? "")) overrides[name] = backupStorage[name];
      }
      const body = {
        server_id: activeServerId,
        key,
        parameters,
        secrets,
        ...(Object.keys(overrides).length ? { backups: overrides } : {}),
      };
      await send(body, view);
      const inst = await api.getInstance(activeServerId, key);
      await refresh();
      toast.success("App added");
      navigate(toServer(`/app/${inst.app_id}`));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't add the app");
    } finally {
      setBusy(false);
    }
  };

  if (loadError) {
    return <InlineError className="mx-auto max-w-5xl">{loadError}</InlineError>;
  }
  if (templates === null || (templateId && !view)) {
    return (
      <div className="flex min-h-60 items-center justify-center">
        <Spinner />
      </div>
    );
  }
  if (templates.length === 0) {
    return (
      <p className="mx-auto max-w-5xl rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
        The organization has no templates yet.{" "}
        <Link to="/org/templates" className="underline underline-offset-4">
          Add one
        </Link>{" "}
        or save an app as a template from its Settings.
      </p>
    );
  }

  return (
    <div className="mx-auto w-full max-w-5xl">
      <div className="sticky top-0 z-10 -mx-4 mb-6 flex flex-wrap items-center gap-x-4 gap-y-2 border-b bg-background/95 px-4 py-3 backdrop-blur supports-[backdrop-filter]:bg-background/80">
        <div className="mr-auto min-w-0">
          <h1 className="text-xl font-semibold tracking-tight">Add app from template</h1>
          <p className="text-sm text-muted-foreground">The app keeps the template's files until you unlink it.</p>
        </div>
        <Button variant="ghost" onClick={() => navigate(toServer())} disabled={busy}>
          Cancel
        </Button>
        <Button onClick={() => void create()} disabled={busy || !view}>
          {busy ? "Adding…" : "Add app"}
        </Button>
      </div>

      {error ? (
        <InlineError icon className="mb-4">
          {error}
        </InlineError>
      ) : null}

      <EditorSection title="Template" description="The latest revision unless you pick an older one.">
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="grid gap-2">
            <Label htmlFor="pick-template">Template</Label>
            <NativeSelect
              id="pick-template"
              value={templateId}
              disabled={claimed}
              onChange={(e) => {
                setTemplateId(e.target.value);
                setRevision(0);
                setView(null);
              }}
            >
              {templates.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name}
                </option>
              ))}
            </NativeSelect>
          </div>
          <div className="grid gap-2">
            <Label htmlFor="pick-revision">Revision</Label>
            <NativeSelect
              id="pick-revision"
              value={String(revision)}
              disabled={claimed}
              onChange={(e) => setRevision(Number(e.target.value))}
            >
              <option value="0">Latest (r{view?.latest_revision})</option>
              {(view?.revisions ?? []).map((r) => (
                <option key={r.revision} value={String(r.revision)}>
                  r{r.revision}
                  {r.note ? ` · ${r.note}` : ""}
                </option>
              ))}
            </NativeSelect>
          </div>
        </div>
      </EditorSection>

      <EditorSection title="Parameters" description="Required ones first. Secret values are encrypted in your browser for this server.">
        {params.length === 0 ? (
          <p className="text-sm text-muted-foreground">This template takes no values.</p>
        ) : (
          <ParameterFields
            params={params}
            values={values}
            onChange={(name, v) => setValues((prev) => ({ ...prev, [name]: v }))}
            showRequired={tried}
          />
        )}
      </EditorSection>

      {backups.length > 0 ? (
        <EditorSection title="Backups" description="Where each backup the template turns on is stored on this server.">
          <div className="grid gap-3">
            {backups.map(([name, d]) => {
              const value = backupStorage[name] ?? "";
              const missing = value !== "" && !storages.includes(value);
              return (
                <div key={name} className="grid gap-1.5">
                  <Label htmlFor={`storage-${name}`}>
                    <code className="font-mono">{name}</code>
                    {d.enabled ? null : <span className="text-xs font-normal text-muted-foreground">off by default</span>}
                  </Label>
                  <NativeSelect
                    id={`storage-${name}`}
                    aria-label={`Storage for ${name}`}
                    value={value}
                    onChange={(e) => setBackupStorage((prev) => ({ ...prev, [name]: e.target.value }))}
                  >
                    <option value="">No storage</option>
                    {missing ? <option value={value}>{value} (not on this server)</option> : null}
                    {storages.map((s) => (
                      <option key={s} value={s}>
                        {s}
                      </option>
                    ))}
                  </NativeSelect>
                  {missing ? (
                    <p className="text-xs text-destructive">
                      This server has no storage named {value}.{" "}
                      <Link to={toServer("/settings?tab=storages")} className="underline underline-offset-4">
                        Add it
                      </Link>{" "}
                      or pick another.
                    </p>
                  ) : null}
                </div>
              );
            })}
          </div>
        </EditorSection>
      ) : null}

      <EditorSection title="The app" description="What the app will be called and the hosts it will answer on.">
        <p className="text-sm">
          <span className="text-muted-foreground">Name: </span>
          <span className="font-medium">{appName.trim() || "fill in the parameters"}</span>
        </p>
        {hosts.length === 0 ? (
          <p className="text-sm text-muted-foreground">No hosts.</p>
        ) : (
          <ul aria-label="Hosts" className="grid gap-2">
            {hosts.map((h, i) => (
              <HostPreviewRow key={i} serverId={activeServerId ?? ""} preview={h} />
            ))}
          </ul>
        )}
      </EditorSection>
    </div>
  );
}

// HostPreviewRow is one host the app will get, with the live check
// (another app's host, or no registered domain covering it) once its
// parameters are filled in.
function HostPreviewRow({ serverId, preview }: { serverId: string; preview: HostPreview }) {
  const [result, setResult] = useState<{ host: string; check: HostCheck } | null>(null);
  const ready = preview.missing.length === 0 && validHostname(preview.host);

  useEffect(() => {
    if (!ready || !serverId) return;
    let live = true;
    const timer = setTimeout(() => {
      checkHostname(serverId, undefined, preview.host).then(
        (check) => live && check && setResult({ host: preview.host, check }),
        () => {},
      );
    }, 400);
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [ready, serverId, preview.host]);

  const check = result?.host === preview.host ? result.check : null;
  return (
    <li className="rounded-md border p-3 text-sm">
      <span className="font-mono">{preview.host || "…"}</span>
      {preview.port ? <span className="text-muted-foreground"> → port {preview.port}</span> : null}
      {preview.missing.length > 0 ? (
        <p className="mt-1 text-xs text-muted-foreground">Fill in {preview.missing.join(", ")}.</p>
      ) : !validHostname(preview.host) ? (
        <p className="mt-1 text-xs text-destructive">Not a valid hostname.</p>
      ) : check ? (
        <HostHints id={`host-${preview.host}`} check={check} />
      ) : null}
    </li>
  );
}
```

- [ ] **Step 7: Add the entry point:**

In `web/src/pages/home.tsx`:

Replace:

```tsx
import { useMemo, useState } from "react"
import { useNavigate } from "react-router-dom"
import { Plus } from "lucide-react"

import { useAppBreadcrumbs } from "@/layouts/use-app-layout"
```

with:

```tsx
import { useMemo, useState } from "react"
import { useNavigate } from "react-router-dom"
import { LayoutTemplate, Plus } from "lucide-react"

import { useAppBreadcrumbs } from "@/layouts/use-app-layout"
```

Replace:

```tsx
  return (
    <div className="space-y-4">
      {/* The apps page: title and New App share one row. */}
      <div className="flex items-center justify-between gap-3">
        <h1 className="text-2xl font-semibold tracking-tight">Apps</h1>
        <Button size="sm" onClick={() => navigate(toServer("/create-app"))}>
          <Plus className="size-4" /> New App
```

with:

```tsx
  return (
    <div className="space-y-4">
      {/* The apps page: title, New App and From template share one row. */}
      <div className="flex items-center justify-between gap-3">
        <h1 className="mr-auto text-2xl font-semibold tracking-tight">Apps</h1>
        <Button size="sm" variant="outline" onClick={() => navigate(toServer("/create-app/template"))}>
          <LayoutTemplate className="size-4" /> From template
        </Button>
        <Button size="sm" onClick={() => navigate(toServer("/create-app"))}>
          <Plus className="size-4" /> New App
```

- [ ] **Step 8: Route it:**

In `web/src/main.tsx`:

Replace:

```tsx
import OrgTemplatesPage from "@/pages/org-templates"
import TemplatePage from "@/pages/template-page"
import LoginPage from "@/pages/login"
import RegisterPage from "@/pages/register"
```

with:

```tsx
import OrgTemplatesPage from "@/pages/org-templates"
import TemplatePage from "@/pages/template-page"
import CreateFromTemplatePage from "@/pages/create-from-template"
import LoginPage from "@/pages/login"
import RegisterPage from "@/pages/register"
```

Replace:

```tsx
              { index: true, element: <HomePage /> },
              { path: "create-app", element: <CreateAppPage /> },
              { path: "app/:appId", element: <AppDetailsPage /> },
              { path: "monitoring", element: <MonitoringPage /> },
```

with:

```tsx
              { index: true, element: <HomePage /> },
              { path: "create-app", element: <CreateAppPage /> },
              { path: "create-app/template", element: <CreateFromTemplatePage /> },
              { path: "app/:appId", element: <AppDetailsPage /> },
              { path: "monitoring", element: <MonitoringPage /> },
```

- [ ] **Step 9: Run the tests**

Run: `pnpm --dir web exec vitest run src/pages/create-from-template.test.tsx src/pages/home.test.tsx src/components/ingress-editor.test.tsx`
Expected: PASS (19 tests; `ingress-editor.test.tsx` covers the moved `checkHostname`)

- [ ] **Step 10: Check**

Run: `pnpm --dir web exec tsc -b && pnpm --dir web run lint`
Expected: no errors

- [ ] **Step 11: Commit**

```bash
git add web/src/lib/host-check.ts \
  web/src/components/hostname-field.tsx \
  web/src/pages/create-from-template.tsx \
  web/src/pages/create-from-template.test.tsx \
  web/src/pages/home.tsx \
  web/src/pages/home.test.tsx \
  web/src/main.tsx
git commit -m "feat(web): add an app from a template" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: The app page of an instance: badge, Parameters form, Upgrade, Unlink, 409s

**Files:**
- Modify: `web/src/context/apps-context-base.ts`
- Modify: `web/src/context/apps-context.tsx`
- Create: `web/src/hooks/use-app-instance.ts`
- Create: `web/src/components/instance-panel.tsx`
- Modify: `web/src/components/app-editor-panel.tsx`
- Modify: `web/src/pages/app-details.tsx`
- Test: `web/src/context/apps-context.test.tsx`
- Test: `web/src/components/app-editor-panel.test.tsx`
- Test: `web/src/pages/app-details.test.tsx`

**Interfaces:**
- Consumes: `ParameterFields`, `UpgradeInstanceDialog` (Task 8); `fieldErrors` (Task 2); `CodeEditor` `readOnly` (Task 5); `useTemplatesApi`, `sealSecrets`, `isManagedByTemplate`, `InstanceView`, `TemplateView` (Task 1).
- Produces: `App.templateId?: string` (from `get-apps`' `template_id`, which the API already returns); `useAppInstance(app?: {id, serverId, templateId?}): {loading, error, instance: InstanceView | null, current: TemplateView | null, reload}` (`@/hooks/use-app-instance`); `InstancePanel({instance, current, onSaved})` (`@/components/instance-panel`); `SettingsTab` props `instance: InstanceView | null`, `onUnlinked: () => void` (Task 11 adds Save as template to its non-instance branch).

**No API gap.** `get-apps` already returns each app's `template_id` (`model.App` embeds it, set while an instance manages the app, cleared by unlink's save), so the page knows on load whether an app is an instance; only then it reads `get-instances?server_id=&template_id=` (one DB query, matched by `app_id`) and `get-template?id=&revision=` (the instance's revision: its body for the form and files, and `latest_revision` for "r5 available"). The latest body is read only when Upgrade is pressed. The files are shown from the template revision (an instance's files are that revision's files, unchanged), so the Configuration tab needs no agent round trip.

While a templated app's instance is being looked up, the Configuration tab shows a spinner, never the plain editor (its save would be refused with 409). If a save-app or rollback-app is still refused as managed (the list was stale: an instance was created through the API meanwhile), the editor/history explain it and re-read the app list, which swaps in the instance view. History hides Roll back for an instance (rollback-app answers 409). A pending instance shows its last error and asks for every required secret again; its Save is enabled without changes (a retry). Unlink confirms with the spec's text; afterwards the list re-read clears `templateId`, so the badge goes and the editor returns.

- [ ] **Step 1: Write the failing tests. The app list maps `template_id`:**

In `web/src/context/apps-context.test.tsx`:

Replace:

```tsx
    expect(DELETE_APP_TIMEOUT_MS).toBeGreaterThanOrEqual(9 * 60_000);
  });
});
```

with:

```tsx
    expect(DELETE_APP_TIMEOUT_MS).toBeGreaterThanOrEqual(9 * 60_000);
  });
});

describe("AppsProvider apps", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("knows which apps a template manages", async () => {
    const app = { server_id: "srv-1", name: "shop", version: "1", icon: "", color: "", created_at: "" };
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) =>
        String(input).includes("/api/v1/app/get-apps?")
          ? json(200, { data: { apps: [{ ...app, id: "a1", template_id: "t1" }, { ...app, id: "a2", template_id: "" }] } })
          : json(202, { data: {} }),
      ),
    );
    const { result } = renderHook(() => useApps(), { wrapper });
    await waitUntil(() => expect(result.current.apps).toHaveLength(2));
    expect(result.current.apps.map((a) => a.templateId)).toEqual(["t1", undefined]);
  });
});
```

- [ ] **Step 2: A save refused as managed explains itself and re-reads the list:**

Create `web/src/components/app-editor-panel.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppEditorPanel } from "@/components/app-editor-panel";
import { AppsContext, type AppDetailPayload, type AppsContextValue } from "@/context/apps-context-base";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServer: null, loading: true }) }));
vi.mock("@/hooks/use-shared-names", () => ({ useSharedNames: () => [] }));
vi.mock("@/components/code-editor", () => ({ CodeEditor: () => <textarea aria-label="code" /> }));
vi.mock("@/components/image-tag-picker", () => ({ ImageTagPicker: () => null }));

const detail: AppDetailPayload = {
  app: {
    app_id: "a1",
    config: btoa(JSON.stringify({ name: "shop", files: [{ filename: "compose.yml" }], variables: [] })),
    files: [{ name: "compose.yml", content: btoa("services: {}\n") }],
    variables: [],
  },
  revision: 1,
  available_revisions: null,
};

function renderPanel(saveApp: AppsContextValue["saveApp"], refresh = vi.fn(async () => {})) {
  const value = {
    getApp: vi.fn(async () => detail),
    saveApp,
    getPublicKey: vi.fn(async () => "K"),
    refresh,
  } as unknown as AppsContextValue;
  render(
    <AppsContext.Provider value={value}>
      <AppEditorPanel appId="a1" />
    </AppsContext.Provider>,
  );
  return refresh;
}

describe("AppEditorPanel", () => {
  beforeEach(() => {
    toast.success.mockReset();
    toast.error.mockReset();
  });

  it("explains a save refused because a template took the app over, and re-reads the list", async () => {
    const user = userEvent.setup();
    const saveApp = vi.fn(async () => {
      throw new Error("this app is managed by template twillingate: update the instance or unlink it");
    });
    const refresh = renderPanel(saveApp);
    await user.type(await screen.findByLabelText("Name"), "-2");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("This app is managed by a template", {
        description: "Change it through its parameters, or unlink it in Settings first.",
      }),
    );
    expect(refresh).toHaveBeenCalled();
  });

  it("reports any other refusal as before", async () => {
    const user = userEvent.setup();
    const refresh = renderPanel(vi.fn(async () => Promise.reject(new Error("no storage nope"))));
    await user.type(await screen.findByLabelText("Name"), "-2");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Failed to update app", { description: "no storage nope" }));
    expect(refresh).not.toHaveBeenCalled();
  });
});
```

- [ ] **Step 3: The app page: the render helper now provides notifications and takes app/context overrides, and the instance cases are added:**

In `web/src/pages/app-details.test.tsx`:

Replace:

```tsx
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Outlet, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";
import AppDetailsPage from "@/pages/app-details";
import type { BackupHealth } from "@/lib/backups";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn() }));
```

with:

```tsx
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Outlet, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AppsContext, type App, type AppsContextValue } from "@/context/apps-context-base";
import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import AppDetailsPage from "@/pages/app-details";
import type { BackupHealth } from "@/lib/backups";
import type { InstanceView, TemplateView } from "@/lib/templates";
import { fakeAPI } from "@/test/fake-api";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn() }));
```

Replace:

```tsx
// notifications).
vi.mock("@/components/stale-vars-badge", () => ({ StaleVarsBadge: () => null }));
// Links point into the active server's scope (/server/:id/...).
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));

const remove = vi.fn();

function renderPage(url: string, backupsByApp: Record<string, BackupHealth[]> = {}) {
  const value = {
    apps: [{ id: "a1", serverId: "s", name: "Shop", version: "3", icon: "", color: "", createdAt: "" }],
    statusByApp: { a1: 1 },
    backupsByApp,
```

with:

```tsx
// notifications).
vi.mock("@/components/stale-vars-badge", () => ({ StaleVarsBadge: () => null }));
vi.mock("@/components/app-editor-panel", () => ({ AppEditorPanel: () => <p>the plain editor</p> }));
vi.mock("@/components/code-editor", () => ({
  CodeEditor: ({ value, filename }: { value: string; filename?: string }) => (
    <pre aria-label={`Content of ${filename}`}>{value}</pre>
  ),
}));
vi.mock("@/lib/ecies", () => ({
  ENCRYPTED: "<encrypted>",
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));
// Links point into the active server's scope (/server/:id/...).
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));

const remove = vi.fn();
const refresh = vi.fn(async () => {});
const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.fn() } as NotificationsContextValue;

function renderPage(
  url: string,
  backupsByApp: Record<string, BackupHealth[]> = {},
  over: { app?: Partial<App>; ctx?: Partial<AppsContextValue> } = {},
) {
  const value = {
    apps: [{ id: "a1", serverId: "s", name: "Shop", version: "3", icon: "", color: "", createdAt: "", ...over.app }],
    statusByApp: { a1: 1 },
    backupsByApp,
```

Replace:

```tsx
    remove,
    rename: vi.fn(),
  } as unknown as AppsContextValue;
  return render(
    <MemoryRouter initialEntries={[url]}>
      <AppsContext.Provider value={value}>
        <Routes>
          <Route element={<Outlet context={{ setBreadcrumbs: () => {} }} />}>
            <Route path="/app/:appId" element={<AppDetailsPage />} />
            <Route path="/server/srv-1" element={<p>the apps</p>} />
          </Route>
        </Routes>
      </AppsContext.Provider>
    </MemoryRouter>,
  );
```

with:

```tsx
    remove,
    rename: vi.fn(),
    refresh,
    ...over.ctx,
  } as unknown as AppsContextValue;
  return render(
    <MemoryRouter initialEntries={[url]}>
      <NotificationsContext.Provider value={notifications}>
        <AppsContext.Provider value={value}>
          <Routes>
            <Route element={<Outlet context={{ setBreadcrumbs: () => {} }} />}>
              <Route path="/app/:appId" element={<AppDetailsPage />} />
              <Route path="/server/srv-1" element={<p>the apps</p>} />
            </Route>
          </Routes>
        </AppsContext.Provider>
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
```

Replace:

```tsx
  beforeEach(() => {
    remove.mockReset();
    for (const f of Object.values(toast)) f.mockReset();
  });
```

with:

```tsx
  beforeEach(() => {
    remove.mockReset();
    refresh.mockClear();
    for (const f of Object.values(toast)) f.mockReset();
  });
```

Replace:

```tsx
  });
});
```

with:

```tsx
  });
});

const INSTANCE: InstanceView = {
  key: "k1",
  server_id: "s",
  app_id: "a1",
  template_id: "t1",
  template_name: "twillingate",
  revision: 2,
  parameters: { NAME: "bakomi" },
  secrets: { SESSION_KEY: "<encrypted>" },
  state: "created",
  status_code: 1,
  last_error: "",
  created_at: "2026-10-12T10:00:00Z",
  updated_at: "2026-10-12T10:00:00Z",
};

const template = (revision: number, extra: TemplateView["body"]["parameters"] = []): TemplateView => ({
  id: "t1",
  name: "twillingate",
  latest_revision: 3,
  revision,
  body: {
    files: { "compose.yaml": "services:\n  app:\n    image: twillingate:${VERSION}\n" },
    parameters: [{ name: "NAME", required: true }, { name: "SESSION_KEY", secret: true, required: true }, ...(extra ?? [])],
    app_name: "${NAME}",
    icon: "",
    color: "",
    ingress: null,
    backups: null,
  },
  note: "",
  created_at: "2026-10-12T10:00:00Z",
  created_by: "u1",
  revisions: null,
});

// instanceAPI answers the instance lookup: the instance, its revision and
// the latest (which adds a required REGION).
function instanceAPI(inst: InstanceView = INSTANCE, extra: Record<string, unknown> = {}) {
  return fakeAPI({
    "/api/v1/instance/get-instances": { instances: [inst] },
    "/api/v1/template/get-template": (_: unknown, q: URLSearchParams) =>
      q.get("revision") ? template(Number(q.get("revision"))) : template(3, [{ name: "REGION", required: true }]),
    "/api/v1/server/get-public-key": { public_key: "K" },
    ...extra,
  });
}

const managed = { app: { serverId: "s", templateId: "t1" } };

describe("AppDetailsPage of a template instance", () => {
  afterEach(() => vi.unstubAllGlobals());
  beforeEach(() => {
    refresh.mockClear();
    for (const f of Object.values(toast)) f.mockReset();
  });

  it("badges the template and revision, and upgrades asking for the new value", async () => {
    const user = userEvent.setup();
    const api = instanceAPI(INSTANCE, { "/api/v1/instance/upgrade-instance": { instance: INSTANCE, request_id: "", warnings: [] } });
    renderPage("/app/a1?tab=backups", {}, managed);
    expect(await screen.findByRole("link", { name: "twillingate · r2" })).toHaveAttribute("href", "/org/templates/t1");
    expect(screen.getByText("r3 available")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Upgrade to r3" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("REGION"), "eu");
    await user.click(within(dialog).getByRole("button", { name: "Upgrade to r3" }));
    await waitFor(() => expect(refresh).toHaveBeenCalled());
    expect(api.posted("/api/v1/instance/upgrade-instance")).toEqual([
      { server_id: "s", key: "k1", revision: 3, parameters: { REGION: "eu" } },
    ]);
  });

  it("shows the files read-only and a Parameters form instead of the editor", async () => {
    const user = userEvent.setup();
    const api = instanceAPI(INSTANCE, { "/api/v1/instance/update-instance": { instance: INSTANCE, request_id: "", warnings: [] } });
    renderPage("/app/a1?tab=editor", {}, managed);
    expect(await screen.findByText(/Managed by template/)).toBeInTheDocument();
    expect(screen.queryByText("the plain editor")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Content of compose.yaml")).toHaveTextContent("image: twillingate:${VERSION}");
    const save = screen.getByRole("button", { name: "Save & redeploy" });
    expect(save).toBeDisabled();
    expect(screen.getByLabelText("SESSION_KEY")).toHaveAttribute("placeholder", "Stored, leave blank to keep");
    await user.clear(screen.getByLabelText("NAME"));
    await user.type(screen.getByLabelText("NAME"), "shop");
    await user.click(save);
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Saved and redeployed"));
    expect(api.posted("/api/v1/instance/update-instance")).toEqual([{ server_id: "s", key: "k1", parameters: { NAME: "shop" } }]);
    expect(refresh).toHaveBeenCalled();
  });

  it("replaces a secret only when a new one is typed, encrypted for the server", async () => {
    const user = userEvent.setup();
    const api = instanceAPI(INSTANCE, { "/api/v1/instance/update-instance": { instance: INSTANCE, request_id: "", warnings: [] } });
    renderPage("/app/a1?tab=editor", {}, managed);
    await user.type(await screen.findByLabelText("SESSION_KEY"), "new-secret");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalled());
    expect(api.posted("/api/v1/instance/update-instance")).toEqual([
      { server_id: "s", key: "k1", secrets: { SESSION_KEY: "ct(K):new-secret" } },
    ]);
  });

  it("shows a pending instance's failure and asks for its secrets again", async () => {
    const user = userEvent.setup();
    instanceAPI({ ...INSTANCE, state: "pending", last_error: "pull access denied" });
    renderPage("/app/a1?tab=editor", {}, managed);
    expect(await screen.findByText(/last deploy from its template failed: pull access denied/)).toBeInTheDocument();
    expect(screen.getByText("Not created yet")).toBeInTheDocument();
    expect(screen.getByLabelText("SESSION_KEY")).toHaveAttribute("placeholder", "Encrypted in your browser");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    expect(screen.getByText("Required")).toBeInTheDocument();
  });

  it("never offers the plain editor while the instance is being looked up", async () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise(() => {})));
    renderPage("/app/a1?tab=editor", {}, managed);
    expect(await screen.findByRole("tab", { name: "Configuration" })).toBeInTheDocument();
    expect(screen.queryByText("the plain editor")).not.toBeInTheDocument();
  });

  it("offers Retry when the lookup fails", async () => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/instance/get-instances": new Error("database down") });
    renderPage("/app/a1?tab=editor", {}, managed);
    expect(await screen.findByText("database down")).toBeInTheDocument();
    vi.unstubAllGlobals();
    instanceAPI();
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText(/Managed by template/)).toBeInTheDocument();
  });

  it("treats an app whose instance is gone as an ordinary app", async () => {
    fakeAPI({ "/api/v1/instance/get-instances": { instances: [] } });
    renderPage("/app/a1?tab=editor", {}, managed);
    expect(await screen.findByText("the plain editor")).toBeInTheDocument();
  });

  it("unlinks from the template after confirming", async () => {
    const user = userEvent.setup();
    const api = instanceAPI(INSTANCE, { "/api/v1/instance/unlink-instance": { instance: INSTANCE, request_id: "", warnings: [] } });
    renderPage("/app/a1?tab=settings", {}, managed);
    await user.click(await screen.findByRole("button", { name: "Unlink from template" }));
    expect(
      screen.getByText("The app becomes an ordinary app you can edit freely; template upgrades no longer reach it."),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Unlink" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Unlinked from the template"));
    expect(api.posted("/api/v1/instance/unlink-instance")).toEqual([{ server_id: "s", key: "k1" }]);
    expect(refresh).toHaveBeenCalled();
  });

  it("reports a failed unlink", async () => {
    const user = userEvent.setup();
    instanceAPI(INSTANCE, { "/api/v1/instance/unlink-instance": new Error("instance not found") });
    renderPage("/app/a1?tab=settings", {}, managed);
    await user.click(await screen.findByRole("button", { name: "Unlink from template" }));
    await user.click(screen.getByRole("button", { name: "Unlink" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Unlink failed", { description: "instance not found" }));
  });

  it("offers no Roll back in History: the template owns the files", async () => {
    instanceAPI();
    const getRevisions = vi.fn(async () => ({
      current: "bbbbbbbb",
      deployed: "bbbbbbbb",
      revisions: [
        { hash: "bbbbbbbb", subject: "upgrade", timestamp: 2 },
        { hash: "aaaaaaaa", subject: "create", timestamp: 1 },
      ],
    }));
    renderPage("/app/a1?tab=history", {}, { ...managed, ctx: { getRevisions } });
    expect(await screen.findByText(/Managed by template twillingate/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Roll back/ })).not.toBeInTheDocument();
  });

  it("re-reads the app list when a roll back is refused as managed", async () => {
    const user = userEvent.setup();
    const getRevisions = vi.fn(async () => ({
      current: "bbbbbbbb",
      deployed: "bbbbbbbb",
      revisions: [
        { hash: "bbbbbbbb", subject: "edit", timestamp: 2 },
        { hash: "aaaaaaaa", subject: "create", timestamp: 1 },
      ],
    }));
    const rollback = vi.fn(async () => {
      throw new Error("this app is managed by template twillingate: update the instance or unlink it");
    });
    renderPage("/app/a1?tab=history", {}, { ctx: { getRevisions, rollback } });
    await user.click(await screen.findByRole("button", { name: /Roll back/ }));
    await user.click(screen.getByRole("button", { name: "Roll back" }));
    await waitFor(() => expect(refresh).toHaveBeenCalled());
  });
});
```

- [ ] **Step 4: Run the tests**

Run: `pnpm --dir web exec vitest run src/context/apps-context.test.tsx src/components/app-editor-panel.test.tsx src/pages/app-details.test.tsx`
Expected: FAIL (12 tests): the app list's `templateId` is undefined; the panel's toast is `Failed to update app`; the instance cases find no template badge, no Parameters form, no Unlink, and still a Roll back

- [ ] **Step 5: Carry the template id on `App`:**

In `web/src/context/apps-context-base.ts`:

Replace:

```ts
  createdAt: string;
  domains?: AppDomain[];
};
```

with:

```ts
  createdAt: string;
  domains?: AppDomain[];
  // templateId is set while a template instance manages the app (its
  // Configuration is then the instance's parameters).
  templateId?: string;
};
```

In `web/src/context/apps-context.tsx`:

Replace:

```tsx
  created_at: string;
  domains?: AppDomainResponse[] | null;
};
```

with:

```tsx
  created_at: string;
  domains?: AppDomainResponse[] | null;
  template_id?: string;
};
```

Replace:

```tsx
  createdAt: a.created_at,
  domains: a.domains ?? undefined,
});
```

with:

```tsx
  createdAt: a.created_at,
  domains: a.domains ?? undefined,
  templateId: a.template_id || undefined,
});
```

- [ ] **Step 6: Look up the instance behind an app:**

Create `web/src/hooks/use-app-instance.ts`:

```ts
import { useCallback, useEffect, useState } from "react";

import { useTemplatesApi } from "@/hooks/use-templates-api";
import type { InstanceView, TemplateView } from "@/lib/templates";

export type AppInstance = {
  // loading is true until a templated app's instance is known: the page
  // must not offer the plain editor meanwhile (its save would be refused).
  loading: boolean;
  error: string;
  instance: InstanceView | null;
  // current is the instance's revision (its latest_revision tells whether
  // it is behind).
  current: TemplateView | null;
  reload: () => Promise<void>;
};

// useAppInstance finds the template instance that manages an app (the app
// list's template_id says one does): two reads, only for such an app.
export function useAppInstance(app: { id: string; serverId: string; templateId?: string } | undefined): AppInstance {
  const api = useTemplatesApi();
  const id = app?.id;
  const serverId = app?.serverId;
  const templateId = app?.templateId;
  const [loading, setLoading] = useState(Boolean(templateId));
  const [error, setError] = useState("");
  const [instance, setInstance] = useState<InstanceView | null>(null);
  const [current, setCurrent] = useState<TemplateView | null>(null);

  const reload = useCallback(async () => {
    setError("");
    if (!id || !serverId || !templateId) {
      setInstance(null);
      setCurrent(null);
      setLoading(false);
      return;
    }
    setLoading(true);
    try {
      const list = await api.listInstances({ serverId, templateId });
      const inst = list.find((i) => i.app_id === id) ?? null;
      setInstance(inst);
      setCurrent(inst ? await api.getTemplate(inst.template_id, inst.revision) : null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load the app's template");
    } finally {
      setLoading(false);
    }
  }, [api, id, serverId, templateId]);

  useEffect(() => {
    void reload();
  }, [reload]);

  return { loading, error, instance, current, reload };
}
```

- [ ] **Step 7: The Configuration tab of an instance:**

Create `web/src/components/instance-panel.tsx`:

```tsx
import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { FileText } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { CodeEditor } from "@/components/code-editor";
import { EditorSection } from "@/components/editor-section";
import { InlineError } from "@/components/inline-error";
import { ParameterFields } from "@/components/parameter-fields";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { fieldErrors } from "@/lib/template-params";
import { sealSecrets, type InstanceView, type TemplateView } from "@/lib/templates";

// storedValues are the form's starting values: the plain values the
// instance holds (secrets start blank: blank keeps them).
function storedValues(current: TemplateView, instance: InstanceView): Record<string, string> {
  const out: Record<string, string> = {};
  for (const p of current.body.parameters ?? []) if (!p.secret) out[p.name] = instance.parameters?.[p.name] ?? "";
  return out;
}

// InstancePanel is the Configuration tab of an app a template manages: the
// template's files read-only, and in place of the editor a Parameters form
// whose Save re-renders and redeploys the app. A secret keeps its value
// unless a new one is typed; a pending instance (never created) holds none,
// so every required secret is asked for again.
export function InstancePanel({
  instance,
  current,
  onSaved,
}: {
  instance: InstanceView;
  current: TemplateView;
  onSaved: () => void;
}) {
  const api = useTemplatesApi();
  const initial = useMemo(() => storedValues(current, instance), [current, instance]);
  const [values, setValues] = useState(initial);
  const [tried, setTried] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const params = current.body.parameters ?? [];
  const kept = instance.state === "created" ? Object.keys(instance.secrets ?? {}) : [];
  const dirty = params.some((p) => (values[p.name] ?? "") !== (initial[p.name] ?? ""));

  const save = async () => {
    setTried(true);
    if (Object.keys(fieldErrors(params, values, kept)).length > 0) return;
    setSaving(true);
    setError("");
    try {
      const parameters: Record<string, string> = {};
      const typed: Record<string, string> = {};
      for (const p of params) {
        const v = values[p.name] ?? "";
        if (p.secret) typed[p.name] = v;
        else if (v !== (initial[p.name] ?? "")) parameters[p.name] = v;
      }
      const secrets = await sealSecrets(api, instance.server_id, typed);
      await api.updateInstance({
        server_id: instance.server_id,
        key: instance.key,
        ...(Object.keys(parameters).length ? { parameters } : {}),
        ...(Object.keys(secrets).length ? { secrets } : {}),
      });
      toast.success("Saved and redeployed");
      onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The save failed");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div>
      <div className="sticky top-0 z-10 -mx-4 mb-6 flex flex-wrap items-center gap-2 border-b bg-background/95 px-4 py-3 backdrop-blur supports-[backdrop-filter]:bg-background/80">
        <p className="mr-auto text-sm text-muted-foreground">
          Managed by template{" "}
          <Link to={`/org/templates/${instance.template_id}`} className="font-medium text-foreground hover:underline">
            {instance.template_name}
          </Link>{" "}
          r{instance.revision}. Change the files in the template, or unlink the app in Settings.
        </p>
        <Button onClick={() => void save()} disabled={saving || (!dirty && instance.state === "created")}>
          {saving ? "Saving…" : "Save & redeploy"}
        </Button>
      </div>
      {error ? (
        <InlineError icon className="mb-4">
          {error}
        </InlineError>
      ) : null}
      <EditorSection
        title="Parameters"
        description="The values this app was added with. Secret values are encrypted in your browser for this server."
      >
        {params.length === 0 ? (
          <p className="text-sm text-muted-foreground">The template takes no values.</p>
        ) : (
          <ParameterFields
            params={params}
            values={values}
            onChange={(name, v) => setValues((prev) => ({ ...prev, [name]: v }))}
            kept={kept}
            showRequired={tried}
          />
        )}
      </EditorSection>
      <EditorSection title="Files" description="Read-only: the template's files, as the app runs them.">
        {Object.entries(current.body.files ?? {})
          .sort(([a], [b]) => a.localeCompare(b))
          .map(([name, content]) => (
            <div key={name} className="overflow-hidden rounded-md border">
              <div className="flex items-center gap-2 border-b bg-muted/40 px-3 py-2 font-mono text-sm">
                <FileText className="size-4 text-muted-foreground" />
                {name}
              </div>
              <CodeEditor value={content} filename={name} readOnly className="rounded-none border-0" />
            </div>
          ))}
      </EditorSection>
    </div>
  );
}
```

- [ ] **Step 8: Explain a save refused as managed:**

In `web/src/components/app-editor-panel.tsx`:

Replace:

```tsx
  validateEditorState,
} from "@/lib/app-editor-io";
import type { AppEditorState } from "@/types/app-config";
```

with:

```tsx
  validateEditorState,
} from "@/lib/app-editor-io";
import { isManagedByTemplate } from "@/lib/templates";
import type { AppEditorState } from "@/types/app-config";
```

Replace:

```tsx
// saves in place (app.save with the app id → new revision + redeploy).
export function AppEditorPanel({ appId }: { appId: string }) {
  const { saveApp, getApp, getPublicKey } = useApps();
  const [state, setState] = useState<AppEditorState>(emptyEditorState);
  const [loading, setLoading] = useState(true);
```

with:

```tsx
// saves in place (app.save with the app id → new revision + redeploy).
export function AppEditorPanel({ appId }: { appId: string }) {
  const { saveApp, getApp, getPublicKey, refresh } = useApps();
  const [state, setState] = useState<AppEditorState>(emptyEditorState);
  const [loading, setLoading] = useState(true);
```

Replace:

```tsx
      await load();
    } catch (e) {
      toast.error(draft ? "Failed to save draft" : "Failed to update app", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
```

with:

```tsx
      await load();
    } catch (e) {
      const message = e instanceof Error ? e.message : "";
      if (isManagedByTemplate(message)) {
        // A template instance took the app over (the list was stale): its
        // Configuration is the instance's parameters, shown once the list
        // is read again.
        toast.error("This app is managed by a template", {
          description: "Change it through its parameters, or unlink it in Settings first.",
        });
        void refresh();
        return;
      }
      toast.error(draft ? "Failed to save draft" : "Failed to update app", {
        description: message || undefined,
      });
    } finally {
```

- [ ] **Step 9: Wire the page: badge and Upgrade in the header, the pending error, the instance panel in Configuration, no Roll back in History, Unlink in Settings:**

In `web/src/pages/app-details.tsx`:

Replace:

```tsx
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import {
  ArrowUpCircle,
```

with:

```tsx
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import {
  ArrowUpCircle,
```

Replace:

```tsx
import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
```

with:

```tsx
import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
```

Replace:

```tsx
import { AppBackups } from "@/components/app-backups";
import { AppEditorPanel } from "@/components/app-editor-panel";
import { AppStatusIndicator } from "@/components/app-status-badge";
import { DeleteAppDialog } from "@/components/delete-app-dialog";
```

with:

```tsx
import { AppBackups } from "@/components/app-backups";
import { AppEditorPanel } from "@/components/app-editor-panel";
import { InlineError } from "@/components/inline-error";
import { InstancePanel } from "@/components/instance-panel";
import { UpgradeInstanceDialog } from "@/components/upgrade-instance-dialog";
import { AppStatusIndicator } from "@/components/app-status-badge";
import { DeleteAppDialog } from "@/components/delete-app-dialog";
```

Replace:

```tsx
  type DeleteAppResult,
} from "@/context/apps-context-base";
import { readApiError } from "@/lib/api";
import { needsAttention } from "@/lib/backups";
import { deleteOutcome, type DeleteChoice } from "@/lib/delete-app";
import { appLabel } from "@/lib/kpi";
import { cn, relativeTime } from "@/lib/utils";
import { useServerPath } from "@/context/use-server-path";
```

with:

```tsx
  type DeleteAppResult,
} from "@/context/apps-context-base";
import { useAppInstance } from "@/hooks/use-app-instance";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { readApiError } from "@/lib/api";
import { needsAttention } from "@/lib/backups";
import { deleteOutcome, type DeleteChoice } from "@/lib/delete-app";
import { appLabel } from "@/lib/kpi";
import { isManagedByTemplate, type InstanceView, type TemplateView } from "@/lib/templates";
import { cn, relativeTime } from "@/lib/utils";
import { useServerPath } from "@/context/use-server-path";
```

Replace:

```tsx
  const toServer = useServerPath();
  const [searchParams, setSearchParams] = useSearchParams();
  const { apps, statusByApp, backupsByApp, loading, control, remove, rename } = useApps();

  const tabParam = searchParams.get("tab") ?? "logs";
```

with:

```tsx
  const toServer = useServerPath();
  const [searchParams, setSearchParams] = useSearchParams();
  const { apps, statusByApp, backupsByApp, loading, control, remove, rename, refresh } = useApps();
  const templatesApi = useTemplatesApi();

  const tabParam = searchParams.get("tab") ?? "logs";
```

Replace:

```tsx
  const app = useMemo(() => apps.find((a) => a.id === appId), [apps, appId]);

  const breadcrumbs = useMemo(
```

with:

```tsx
  const app = useMemo(() => apps.find((a) => a.id === appId), [apps, appId]);
  // managed is the template instance behind the app, when there is one.
  const managed = useAppInstance(app);
  const inst = managed.instance;
  const latest = managed.current?.latest_revision ?? 0;
  const behind = inst !== null && latest > inst.revision;
  const [upgradeTarget, setUpgradeTarget] = useState<TemplateView | null>(null);
  const reloadManaged = () => void Promise.all([managed.reload(), refresh()]);
  const openUpgrade = async () => {
    if (!inst) return;
    try {
      setUpgradeTarget(await templatesApi.getTemplate(inst.template_id));
    } catch (e) {
      toast.error("Couldn't load the template", { description: e instanceof Error ? e.message : undefined });
    }
  };

  const breadcrumbs = useMemo(
```

Replace:

```tsx
            <AppStatusIndicator status={status} />
            <AppAlertsBadge serverId={app.serverId} appId={app.id} />
            {app.version ? <span>Revision {app.version}</span> : null}
            {routes.slice(0, 2).map((d) => (
```

with:

```tsx
            <AppStatusIndicator status={status} />
            <AppAlertsBadge serverId={app.serverId} appId={app.id} />
            {inst ? (
              <span className="inline-flex items-center gap-2">
                <Link
                  to={`/org/templates/${inst.template_id}`}
                  className="rounded-md border px-1.5 py-0.5 text-xs font-medium text-foreground hover:bg-muted"
                >
                  {inst.template_name} · r{inst.revision}
                </Link>
                {behind ? (
                  <span className="text-xs text-amber-700 dark:text-amber-400">r{latest} available</span>
                ) : null}
                {inst.state === "pending" ? <Badge variant="outline">Not created yet</Badge> : null}
              </span>
            ) : null}
            {app.version ? <span>Revision {app.version}</span> : null}
            {routes.slice(0, 2).map((d) => (
```

Replace:

```tsx
        </div>
        <div className="flex flex-wrap gap-2">
          {canStop
            ? controlButton("stop", "Stop", Square)
```

with:

```tsx
        </div>
        <div className="flex flex-wrap gap-2">
          {behind ? (
            <Button size="sm" onClick={() => void openUpgrade()}>
              Upgrade to r{latest}
            </Button>
          ) : null}
          {canStop
            ? controlButton("stop", "Stop", Square)
```

Replace:

```tsx
        </div>
      </header>

      <StaleVarsBadge key={app.id} appId={app.id} />
```

with:

```tsx
        </div>
      </header>

      {inst?.state === "pending" && inst.last_error ? (
        <InlineError icon>
          The app's last deploy from its template failed: {inst.last_error}. Fix its values under Configuration and
          save again.
        </InlineError>
      ) : null}

      {upgradeTarget && inst ? (
        <UpgradeInstanceDialog
          instance={inst}
          target={upgradeTarget}
          open
          onOpenChange={(o) => {
            if (!o) setUpgradeTarget(null);
          }}
          onDone={reloadManaged}
        />
      ) : null}

      <StaleVarsBadge key={app.id} appId={app.id} />
```

Replace:

```tsx
        </TabsContent>
        <TabsContent value="editor" className="mt-0">
          <AppEditorPanel appId={app.id} />
        </TabsContent>
        <TabsContent value="backups" className="mt-6">
```

with:

```tsx
        </TabsContent>
        <TabsContent value="editor" className="mt-0">
          {app.templateId && managed.error ? (
            <div className="flex min-h-60 flex-col items-center justify-center gap-3">
              <InlineError>{managed.error}</InlineError>
              <Button variant="outline" size="sm" onClick={() => void managed.reload()}>
                Retry
              </Button>
            </div>
          ) : app.templateId && managed.loading ? (
            <div className="flex min-h-60 items-center justify-center">
              <Spinner />
            </div>
          ) : inst && managed.current ? (
            <InstancePanel
              key={`${inst.revision}-${inst.updated_at}`}
              instance={inst}
              current={managed.current}
              onSaved={reloadManaged}
            />
          ) : (
            <AppEditorPanel appId={app.id} />
          )}
        </TabsContent>
        <TabsContent value="backups" className="mt-6">
```

Replace:

```tsx
        </TabsContent>
        <TabsContent value="history" className="mt-6">
          <HistoryTab appId={app.id} />
        </TabsContent>
        <TabsContent value="settings" className="mt-6">
```

with:

```tsx
        </TabsContent>
        <TabsContent value="history" className="mt-6">
          <HistoryTab appId={app.id} managedBy={inst?.template_name} />
        </TabsContent>
        <TabsContent value="settings" className="mt-6">
```

Replace:

```tsx
            appId={app.id}
            name={app.name}
            onRename={rename}
            onDelete={async (opts) => {
```

with:

```tsx
            appId={app.id}
            name={app.name}
            instance={inst}
            onUnlinked={reloadManaged}
            onRename={rename}
            onDelete={async (opts) => {
```

Replace:

```tsx
}

function HistoryTab({ appId }: { appId: string }) {
  const { getRevisions, rollback, control } = useApps();
  const [data, setData] = useState<AppRevisions | null>(null);
  const [loading, setLoading] = useState(true);
```

with:

```tsx
}

function HistoryTab({ appId, managedBy }: { appId: string; managedBy?: string }) {
  const { getRevisions, rollback, control, refresh } = useApps();
  const [data, setData] = useState<AppRevisions | null>(null);
  const [loading, setLoading] = useState(true);
```

Replace:

```tsx
      toast.error("Roll back failed", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
```

with:

```tsx
      toast.error("Roll back failed", {
        description: e instanceof Error ? e.message : undefined,
      });
      // A template took the app over meanwhile: show it as managed.
      if (e instanceof Error && isManagedByTemplate(e.message)) void refresh();
    } finally {
      setBusy(false);
    }
```

Replace:

```tsx
            ? "1 revision"
            : `${data.revisions.length} revisions`}
          , newest first. Rolling back adds a new revision; nothing is lost.
        </p>
        <Button
```

with:

```tsx
            ? "1 revision"
            : `${data.revisions.length} revisions`}
          , newest first.{" "}
          {managedBy
            ? `Managed by template ${managedBy}: it changes through its parameters or an upgrade; unlink it to roll back.`
            : "Rolling back adds a new revision; nothing is lost."}
        </p>
        <Button
```

Replace:

```tsx
                    Deploy draft
                  </Button>
                ) : !isCurrent ? (
                  <AlertDialog>
                    <AlertDialogTrigger asChild>
```

with:

```tsx
                    Deploy draft
                  </Button>
                ) : !isCurrent && !managedBy ? (
                  <AlertDialog>
                    <AlertDialogTrigger asChild>
```

Replace:

```tsx
  appId,
  name,
  onRename,
  onDelete,
```

with:

```tsx
  appId,
  name,
  instance,
  onUnlinked,
  onRename,
  onDelete,
```

Replace:

```tsx
  appId: string;
  name: string;
  onRename: (appId: string, name: string) => Promise<void>;
  onDelete: (opts: { deleteVolumes: boolean; deleteBackups: boolean }) => Promise<DeleteAppResult>;
}) {
  const { backupsByApp } = useApps();
  const [newName, setNewName] = useState(name);
  const [busy, setBusy] = useState(false);
```

with:

```tsx
  appId: string;
  name: string;
  instance: InstanceView | null;
  onUnlinked: () => void;
  onRename: (appId: string, name: string) => Promise<void>;
  onDelete: (opts: { deleteVolumes: boolean; deleteBackups: boolean }) => Promise<DeleteAppResult>;
}) {
  const { backupsByApp } = useApps();
  const templatesApi = useTemplatesApi();
  const [newName, setNewName] = useState(name);
  const [busy, setBusy] = useState(false);
```

Replace:

```tsx
      toast.error("Rename failed", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
```

with:

```tsx
      toast.error("Rename failed", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  const unlink = async () => {
    if (!instance) return;
    setBusy(true);
    try {
      await templatesApi.unlinkInstance(instance.server_id, instance.key);
      toast.success("Unlinked from the template");
      onUnlinked();
    } catch (e) {
      toast.error("Unlink failed", { description: e instanceof Error ? e.message : undefined });
    } finally {
      setBusy(false);
    }
```

Replace:

```tsx
      </div>

      <div className="grid gap-4 p-5 md:grid-cols-[14rem_minmax(0,1fr)] md:gap-8">
        <div className="space-y-1">
```

with:

```tsx
      </div>

      {instance ? (
        <div className="grid gap-4 p-5 md:grid-cols-[14rem_minmax(0,1fr)] md:gap-8">
          <div className="space-y-1">
            <h2 className="text-sm font-semibold">Template</h2>
            <p className="text-sm text-muted-foreground">
              Managed by template {instance.template_name}: its files and values change through the template.
            </p>
          </div>
          <div>
            <AlertDialog>
              <AlertDialogTrigger asChild>
                <Button variant="outline" disabled={busy}>
                  Unlink from template
                </Button>
              </AlertDialogTrigger>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>Unlink from {instance.template_name}?</AlertDialogTitle>
                  <AlertDialogDescription>
                    The app becomes an ordinary app you can edit freely; template upgrades no longer reach it.
                  </AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel>Cancel</AlertDialogCancel>
                  <AlertDialogAction onClick={() => void unlink()}>Unlink</AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
          </div>
        </div>
      ) : null}

      <div className="grid gap-4 p-5 md:grid-cols-[14rem_minmax(0,1fr)] md:gap-8">
        <div className="space-y-1">
```

- [ ] **Step 10: Run the tests**

Run: `pnpm --dir web exec vitest run src/context/apps-context.test.tsx src/components/app-editor-panel.test.tsx src/pages/app-details.test.tsx`
Expected: PASS (23 tests)

- [ ] **Step 11: Check**

Run: `pnpm --dir web exec tsc -b && pnpm --dir web run lint`
Expected: no errors

- [ ] **Step 12: Commit**

```bash
git add web/src/context/apps-context-base.ts \
  web/src/context/apps-context.tsx \
  web/src/context/apps-context.test.tsx \
  web/src/hooks/use-app-instance.ts \
  web/src/components/instance-panel.tsx \
  web/src/components/app-editor-panel.tsx \
  web/src/components/app-editor-panel.test.tsx \
  web/src/pages/app-details.tsx \
  web/src/pages/app-details.test.tsx
git commit -m "feat(web): the app page of a template instance" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Existing app: Settings → Save as template; final verification

**Files:**
- Modify: `web/src/lib/template-draft.ts`
- Modify: `web/src/pages/app-details.tsx`
- Test: `web/src/lib/template-draft.test.ts`
- Test: `web/src/pages/app-details.test.tsx`

**Interfaces:**
- Consumes: `stateFromDetail` (`@/lib/app-editor-io`), `configOf` (`@/lib/backups`), `ENCRYPTED` (`@/lib/ecies`), `AppDetailPayload`, `useApps().getApp`; `paramRow`, `ingressRow`, `TemplateDraft` (Task 6); `DraftState` (Task 7).
- Produces (`@/lib/template-draft`): `slugName(appName: string): string`; `templateFromApp(appId: string, detail: AppDetailPayload): {draft: TemplateDraft; notes: string[]}`.

Save as template builds revision 1 in the browser from what the app editor holds (`getApp`, decoded by the editor's own `stateFromDetail`): files copied; each variable a parameter with its value as the default; each secret a required secret parameter without a default; the hosts literal ingress rows (`host → upstream_port`); the recipe backups' overrides (`configOf`) the backup defaults. What cannot come along is listed as notes on the new template's page: secret files (a template holds no secret), a value containing `$` (a parameter value can't), redirects, a Git source. It opens `/org/templates/new` prefilled (name: the app name as a slug, editable) and nothing is saved until the user presses Save, so a body the API would refuse is fixed in the editor rather than lost. The app stays an ordinary app.

- [ ] **Step 1: Write the failing tests:**

In `web/src/lib/template-draft.test.ts`:

Replace:

```ts
import { describe, expect, it } from "vitest";

import {
  bodyFromDraft,
```

with:

```ts
import { describe, expect, it } from "vitest";

import type { AppDetailPayload } from "@/context/apps-context-base";
import type { EffectiveBackup } from "@/lib/backups";
import {
  bodyFromDraft,
```

Replace:

```ts
  paramRow,
  recipeBackupNames,
  type TemplateDraft,
} from "@/lib/template-draft";
```

with:

```ts
  paramRow,
  recipeBackupNames,
  slugName,
  templateFromApp,
  type TemplateDraft,
} from "@/lib/template-draft";
```

Replace:

```ts
    expect(draftProblem(valid())).toBeNull();
  });
});
```

with:

```ts
    expect(draftProblem(valid())).toBeNull();
  });
});

describe("templateFromApp", () => {
  const b64 = (s: string) => btoa(s);
  const config = {
    name: "Shop Front",
    icon: "cart",
    color: "#111111",
    files: [{ filename: "compose.yml" }, { filename: "key.pem", is_encrypted: true }],
    variables: [{ name: "NAME" }, { name: "TOKEN", is_encrypted: true }, { name: "PRICE" }],
    ingress: {
      domains: [{ domain: "shop.example.com", upstream_port: 8080 }],
      redirects: [{ domain: "www.shop.example.com", to: "https://shop.example.com", code: 301 }],
    },
  };
  const backup = (b: Partial<EffectiveBackup>): EffectiveBackup =>
    ({ name: "db", type: "sqlite", enabled: true, storage: "tenants", in_recipe: true, from_recipe: { every: true }, ...b }) as EffectiveBackup;
  const detail: AppDetailPayload = {
    app: {
      app_id: "a1",
      config: b64(JSON.stringify(config)),
      files: [
        { name: "compose.yml", content: b64(COMPOSE) },
        { name: "key.pem", content: "<encrypted>", encrypted: true },
      ],
      variables: [
        { name: "NAME", content: b64("shop") },
        { name: "TOKEN", content: "<encrypted>", encrypted: true },
        { name: "PRICE", content: b64("$5") },
      ],
    },
    backups: [backup({}), backup({ name: "extra", in_recipe: false })],
    revision: 1,
    available_revisions: null,
  };

  it("builds revision 1 from what the editor holds", () => {
    const { draft, notes } = templateFromApp("a1", detail);
    expect(draft.name).toBe("shop-front");
    const body = bodyFromDraft(draft);
    expect(body).toEqual({
      files: { "compose.yml": COMPOSE },
      parameters: [{ name: "NAME", default: "shop" }, { name: "TOKEN", secret: true, required: true }, { name: "PRICE" }],
      app_name: "Shop Front",
      icon: "cart",
      color: "#111111",
      ingress: [{ host: "shop.example.com", port: 8080 }],
      // The backup added in the app's Backups tab has no recipe entry.
      backups: { db: { enabled: true, storage: "tenants" } },
    });
    expect(notes).toEqual([
      "key.pem is a secret file; a template holds no secrets, so it was left out.",
      "PRICE's value has a $, which a parameter can't hold; it has no default now.",
      "Redirects aren't part of templates; they were left out.",
    ]);
  });

  it("notes that a Git source does not come along", () => {
    const git = { ...config, source: { repo_url: "https://example.com/r.git", branch: "main", auto_update: false } };
    const { notes } = templateFromApp("a1", { ...detail, app: { ...detail.app, config: b64(JSON.stringify(git)) } });
    expect(notes[0]).toContain("deploys from a Git repository");
  });

  it("slugs any app name into a template name", () => {
    expect(slugName("  2 Shop!! Front ")).toBe("shop-front");
    expect(slugName("123")).toBe("template");
    expect(slugName("a".repeat(80) + "-b")).toHaveLength(63);
  });
});
```

In `web/src/pages/app-details.test.tsx`:

Replace:

```tsx
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Outlet, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
```

with:

```tsx
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Outlet, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
```

Replace:

```tsx
import type { BackupHealth } from "@/lib/backups";
import type { InstanceView, TemplateView } from "@/lib/templates";
import { fakeAPI } from "@/test/fake-api";
```

with:

```tsx
import type { BackupHealth } from "@/lib/backups";
import type { InstanceView, TemplateView } from "@/lib/templates";
import type { DraftState } from "@/pages/template-page";
import { fakeAPI } from "@/test/fake-api";
```

Replace:

```tsx
const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.fn() } as NotificationsContextValue;

function renderPage(
  url: string,
```

with:

```tsx
const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.fn() } as NotificationsContextValue;

// NewTemplate stands in for the template editor Save as template opens.
function NewTemplate() {
  const state = useLocation().state as DraftState;
  return <p>new template {state.draft.name} with {state.draft.parameters.length} parameters</p>;
}

function renderPage(
  url: string,
```

Replace:

```tsx
              <Route path="/app/:appId" element={<AppDetailsPage />} />
              <Route path="/server/srv-1" element={<p>the apps</p>} />
            </Route>
          </Routes>
```

with:

```tsx
              <Route path="/app/:appId" element={<AppDetailsPage />} />
              <Route path="/server/srv-1" element={<p>the apps</p>} />
              <Route path="/org/templates/new" element={<NewTemplate />} />
            </Route>
          </Routes>
```

Replace:

```tsx
    await waitFor(() => expect(refresh).toHaveBeenCalled());
  });
});
```

with:

```tsx
    await waitFor(() => expect(refresh).toHaveBeenCalled());
  });
});

describe("AppDetailsPage: Save as template", () => {
  beforeEach(() => {
    for (const f of Object.values(toast)) f.mockReset();
  });

  it("opens a new template's editor built from the app", async () => {
    const user = userEvent.setup();
    const getApp = vi.fn(async () => ({
      app: {
        app_id: "a1",
        config: btoa(JSON.stringify({ name: "Shop", files: [{ filename: "compose.yml" }], variables: [{ name: "PORT" }] })),
        files: [{ name: "compose.yml", content: btoa("services: {}\n") }],
        variables: [{ name: "PORT", content: btoa("8080") }],
      },
      revision: 1,
      available_revisions: null,
    }));
    renderPage("/app/a1?tab=settings", {}, { ctx: { getApp } });
    await user.click(await screen.findByRole("button", { name: "Save as template" }));
    expect(await screen.findByText("new template shop with 1 parameters")).toBeInTheDocument();
  });

  it("reports an app it can't read", async () => {
    const user = userEvent.setup();
    const getApp = vi.fn(async () => Promise.reject(new Error("agent offline")));
    renderPage("/app/a1?tab=settings", {}, { ctx: { getApp } });
    await user.click(await screen.findByRole("button", { name: "Save as template" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Couldn't read the app", { description: "agent offline" }));
    expect(screen.getByRole("button", { name: "Save as template" })).toBeEnabled();
  });
});
```

- [ ] **Step 2: Run the tests**

Run: `pnpm --dir web exec vitest run src/lib/template-draft.test.ts src/pages/app-details.test.tsx`
Expected: FAIL (5 tests): `TypeError: templateFromApp is not a function` (and `slugName`); `Unable to find role="button" and name "Save as template"`

- [ ] **Step 3: Build the draft from an app:**

In `web/src/lib/template-draft.ts`:

Replace:

```ts
// The template editor's working state (TemplateDraft) and its conversions:
// from a stored revision and to the body save-template takes. The checks
// give instant feedback; the API checks everything again
// (internal/domain/template/validate.go).
import { yamlLanguage } from "@codemirror/lang-yaml";

import { compilePattern } from "@/lib/template-params";
import type { BackupDefault, Parameter, TemplateBody } from "@/lib/templates";
```

with:

```ts
// The template editor's working state (TemplateDraft) and its conversions:
// from a stored revision, to the body save-template takes, and from an
// existing app ("Save as template"). The checks give instant feedback; the
// API checks everything again (internal/domain/template/validate.go).
import { yamlLanguage } from "@codemirror/lang-yaml";

import type { AppDetailPayload } from "@/context/apps-context-base";
import { stateFromDetail } from "@/lib/app-editor-io";
import { configOf } from "@/lib/backups";
import { ENCRYPTED } from "@/lib/ecies";
import { compilePattern } from "@/lib/template-params";
import type { BackupDefault, Parameter, TemplateBody } from "@/lib/templates";
```

Replace:

```ts
    }
  }
  return null;
}
```

with:

```ts
    }
  }
  return null;
}

// slugName makes a template name from an app name.
export function slugName(appName: string): string {
  const slug = appName
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/^[^a-z]+/, "")
    .replace(/-+$/, "")
    .slice(0, 63)
    .replace(/-+$/, "");
  return slug || "template";
}

// templateFromApp builds revision 1 of a template from an app as the editor
// holds it (Save as template): files copied, each variable a parameter with
// its value as the default, each secret a required secret parameter, the
// app's hosts literal ingress rows, its backup overrides the defaults.
// notes say what could not come along.
export function templateFromApp(appId: string, detail: AppDetailPayload): { draft: TemplateDraft; notes: string[] } {
  const state = stateFromDetail(appId, detail);
  const notes: string[] = [];
  if (state.config.source) {
    notes.push("The app deploys from a Git repository; a template holds files only, so the repository is not part of it.");
  }
  const files: AppFileMeta[] = [];
  const contents: Record<string, string> = {};
  for (const f of state.config.files) {
    if (f.is_encrypted || state.files[f.id] === ENCRYPTED) {
      notes.push(`${f.filename} is a secret file; a template holds no secrets, so it was left out.`);
      continue;
    }
    files.push({ ...f, is_encrypted: false });
    contents[f.id] = state.files[f.id] ?? "";
  }
  const parameters = state.config.variables.map((v) => {
    if (v.is_encrypted) return paramRow({ name: v.name, secret: true, required: true });
    const value = state.variables[v.id] ?? "";
    if (value.includes("$")) {
      notes.push(`${v.name}'s value has a $, which a parameter can't hold; it has no default now.`);
      return paramRow({ name: v.name });
    }
    return paramRow({ name: v.name, default: value });
  });
  const ingress = (state.config.ingress?.domains ?? []).map((dom) => ingressRow(dom.domain, String(dom.upstream_port)));
  if ((state.config.ingress?.redirects.length ?? 0) > 0) notes.push("Redirects aren't part of templates; they were left out.");
  const backups: Record<string, BackupDefault> = {};
  for (const b of detail.backups ?? []) {
    if (!b.in_recipe) continue;
    const c = configOf(b);
    backups[b.name] = {
      enabled: !!c.enabled,
      ...(c.storage ? { storage: c.storage } : {}),
      ...(c.every ? { every: c.every } : {}),
      ...(c.keep ? { keep: c.keep } : {}),
    };
  }
  return {
    draft: {
      name: slugName(state.config.name),
      appName: state.config.name,
      icon: state.config.icon ?? "",
      color: state.config.color ?? "#64748b",
      files,
      contents,
      parameters,
      ingress,
      backups,
    },
    notes,
  };
}
```

- [ ] **Step 4: Offer it in Settings for apps no template manages:**

In `web/src/pages/app-details.tsx`:

Replace:

```tsx
import { deleteOutcome, type DeleteChoice } from "@/lib/delete-app";
import { appLabel } from "@/lib/kpi";
import { isManagedByTemplate, type InstanceView, type TemplateView } from "@/lib/templates";
import { cn, relativeTime } from "@/lib/utils";
import { useServerPath } from "@/context/use-server-path";
```

with:

```tsx
import { deleteOutcome, type DeleteChoice } from "@/lib/delete-app";
import { appLabel } from "@/lib/kpi";
import { templateFromApp } from "@/lib/template-draft";
import { isManagedByTemplate, type InstanceView, type TemplateView } from "@/lib/templates";
import type { DraftState } from "@/pages/template-page";
import { cn, relativeTime } from "@/lib/utils";
import { useServerPath } from "@/context/use-server-path";
```

Replace:

```tsx
  onDelete: (opts: { deleteVolumes: boolean; deleteBackups: boolean }) => Promise<DeleteAppResult>;
}) {
  const { backupsByApp } = useApps();
  const templatesApi = useTemplatesApi();
  const [newName, setNewName] = useState(name);
```

with:

```tsx
  onDelete: (opts: { deleteVolumes: boolean; deleteBackups: boolean }) => Promise<DeleteAppResult>;
}) {
  const { backupsByApp, getApp } = useApps();
  const navigate = useNavigate();
  const templatesApi = useTemplatesApi();
  const [newName, setNewName] = useState(name);
```

Replace:

```tsx
      toast.error("Unlink failed", { description: e instanceof Error ? e.message : undefined });
    } finally {
      setBusy(false);
    }
```

with:

```tsx
      toast.error("Unlink failed", { description: e instanceof Error ? e.message : undefined });
    } finally {
      setBusy(false);
    }
  };

  // saveAsTemplate opens a new template's editor prefilled from what the
  // app's editor holds; nothing is saved until the template is.
  const saveAsTemplate = async () => {
    setBusy(true);
    try {
      const state: DraftState = templateFromApp(appId, await getApp(appId));
      navigate("/org/templates/new", { state });
    } catch (e) {
      toast.error("Couldn't read the app", { description: e instanceof Error ? e.message : undefined });
      setBusy(false);
    }
```

Replace:

```tsx
          </div>
        </div>
      ) : null}

      <div className="grid gap-4 p-5 md:grid-cols-[14rem_minmax(0,1fr)] md:gap-8">
```

with:

```tsx
          </div>
        </div>
      ) : (
        <div className="grid gap-4 p-5 md:grid-cols-[14rem_minmax(0,1fr)] md:gap-8">
          <div className="space-y-1">
            <h2 className="text-sm font-semibold">Save as template</h2>
            <p className="text-sm text-muted-foreground">
              Start a template from this app: its files, its variables as parameters, its hosts and backup settings.
              The app stays as it is.
            </p>
          </div>
          <div>
            <Button variant="outline" disabled={busy} onClick={() => void saveAsTemplate()}>
              Save as template
            </Button>
          </div>
        </div>
      )}

      <div className="grid gap-4 p-5 md:grid-cols-[14rem_minmax(0,1fr)] md:gap-8">
```

- [ ] **Step 5: Run the tests**

Run: `pnpm --dir web exec vitest run src/lib/template-draft.test.ts src/pages/app-details.test.tsx`
Expected: PASS (42 tests)

- [ ] **Step 6: Check**

Run: `pnpm --dir web exec tsc -b && pnpm --dir web run lint`
Expected: no errors

- [ ] **Step 7: Commit**

```bash
git add web/src/lib/template-draft.ts \
  web/src/lib/template-draft.test.ts \
  web/src/pages/app-details.tsx \
  web/src/pages/app-details.test.tsx
git commit -m "feat(web): save an app as a template" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 8: Check**

Run: `pnpm --dir web run test:coverage && pnpm --dir web run build && git checkout -- web/dist/.gitkeep`
Expected: every test file passes; the build succeeds (its chunk-size warning is pre-existing); `web/dist/.gitkeep` is restored (vite empties `web/dist`)

- [ ] **Step 9: Check**

Run: `go run ./tools/diffcov --base origin/feat/templates --min 80 --lcov web/coverage/lcov.info`
Expected: web: changed lines ≥ 80% covered (the scratch run of this plan measured 98%). No Go file changes, so there is no Go side to gate; CI runs both with the PR's base.

---

## Self-review

**Spec coverage (§UI):**

| Spec item | Task |
|---|---|
| `/org/templates`, linked next to Members and Variables; list with latest revision, "12 instances · 3 behind", Add template | 4 |
| Template editor: the app editor's file editing; Parameters table (name, secret, required, default, pattern, description); app name, icon, color; Ingress rows; Backup defaults (on/off, storage name, one row per recipe backup) | 5, 6, 7 |
| Save adds a revision with an optional note; concurrent edit shows "someone saved r4 meanwhile; reload" | 7 |
| Revisions list with a diff against the previous one | 3, 8 |
| Instances: server, revision, status, per-row Upgrade | 8 |
| Upgrade all to rN, rollout strip with Stop | D1b, not in this plan |
| Delete only without live instances | 7 |
| Add app → From template: template and revision (latest default); parameters required first, pattern checked as typed, secrets masked and encrypted for the server; storage select per backup default, preselected; hosts preview with the coverage error inline; browser-generated key; waits, then opens the app | 2, 8, 9 |
| App page of an instance: header badge "twillingate · r3" linking to the template, "r5 available" | 10 |
| Configuration: files read-only, "Managed by template twillingate", Parameters form whose Save re-renders and redeploys, secrets kept unless replaced | 5, 10 |
| Upgrade to rN when behind, asking for newly required values | 8, 10 |
| Settings → Unlink from template with the spec's confirm text; editor back, badge gone | 10 |
| Everything else unchanged; delete retires the key (API side, already in D1a) | — |
| Existing app: Settings → Save as template (files, variables as parameters with defaults, secrets as required secret parameters, hosts as literal ingress rows, backup overrides as defaults), opens the new template's editor; the app stays ordinary | 11 |
| Save-app / rollback-app 409 on an instance's app handled gracefully | 10 |

§Testing (web): template editor (parameters table, ingress, backup defaults, concurrent-edit message) → Task 7; From template form (pattern, secrets encrypted, hosts preview) → Task 9; instance app page (read-only files, Parameters form, Upgrade, Unlink) → Task 10; Save as template body → Task 11. Rollout strip and Stop → D1b.

**Placeholder scan:** every code step carries the full file or exact replace snippets; no TBD/TODO; each "Run" names its command and expected result.

**Type consistency:** `TemplateView`, `InstanceView`, `TemplateBody`, `Parameter` (Task 1) are the only shapes later tasks read; `fieldErrors(params, values, kept)` (Task 2) is called by `ParameterFields` (Task 8), `UpgradeInstanceDialog` (Task 8), `CreateFromTemplatePage` (Task 9) and `InstancePanel` (Task 10) with the same signature; `DraftState` (Task 7) is what Task 11 passes as `location.state`; `useAppInstance` returns `{loading, error, instance, current, reload}` and `app-details.tsx` reads exactly those.

**Decisions worth a second look:**

- Save as template opens a prefilled, **unsaved** editor rather than storing revision 1 at once: an app's variables can hold what a parameter can't (a `$`, a lowercase name), and the user is expected to edit hosts into `${NAME}` before the first revision anyway.
- The instance page shows the template revision's files, not the agent's copy: they are identical by construction (files are copied unchanged), and this keeps the tab free of an agent round trip.
- An empty "Default" field is stored as no default: at render time an absent and an empty default are the same value.
