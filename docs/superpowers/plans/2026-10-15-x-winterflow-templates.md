# Templates use x-winterflow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make templates speak the native `x-winterflow` format: a revision body is the app's files plus `summary`/`description`/`icon`/`color`, and everything an instance fills in (variables), exposes (ports, hostnames) or turns on (backups) is derived from the compose file's `x-winterflow` block by the shared parser. Templates are an org's own or the catalog's (read-only, same for every org; rows inserted by tests until the catalog sync of sub-project 3), instances are created with values, secrets, an app name, hostnames and backup choices, upgrades re-render with the stored choices, and the existing web UI (template editor, install flow, instance panel, update dialog) keeps working on the new model.

**Architecture:** Migration `0016_templates_x_winterflow` drops and recreates `templates` (+`source`, nullable `org_id`, `slug`, `withdrawn_at`, partial uniques), `template_revisions` (+`digest`) and `template_instances` (+`name`, `hosts`) and clears `template_upgrade` rollouts; the repository scopes every read to "the org's own plus every catalog template" and every write to the org's own. `internal/domain/template` is rewritten around `xwinterflow.Parse(ModeTemplate, ...)` (`Validate`, `Detect`, `Resolve`, `CheckHosts`, `CreateBackups`, `Render`, `RequestHash`); the templates usecase renders an instance into the same `app.save` payload the app editor sends and dispatches it through `CheckSave` + `DispatchSave`. A new `POST /api/v1/template/inspect-template` (no write) and `get-template`'s `detected` feed the editor's Detected panel, so the browser carries no YAML parser. `template_upgrade` rollouts are keyed `<org_id>:<template_id>`. `instance.backup_defaults` and its agent-side merge are deleted: a backup a revision adds starts off.

**Tech Stack:** Go 1.27 (chi, sqlc v1.31.1 over `database/sql`, SQLite `modernc.org/sqlite` and Postgres `pgx/v5`), `go.yaml.in/yaml/v3` through `internal/domain/compose/xwinterflow`, React 19 + Vite + Vitest + Testing Library, vendored shadcn/ui, pnpm.

**Spec:** docs/superpowers/specs/2026-10-14-x-winterflow-catalog-design.md (Sub-project 2; the web parts the controller scoped in, see "Spec decisions")

**Base:** every snippet below was applied, task by task, to a copy of `main` at `dab8df8` (sub-project 1 merged) and its tests run there: Go `go test ./...`, `go vet ./...`, `WF_CONTRACT_GUARD=require` contract suite, `make test-pg` (contract suite and `internal/infra/db/...` on `postgres:16`), `make sqlc-check`; web `tsc -b`, ESLint and all 1683 Vitest tests; merged SQLite+Postgres diff coverage of changed lines 94.0% Go / 99.4% web; the standalone smoke of Task 11 installed and upgraded a template on Docker. New files are given whole; a file rewritten past half of its lines is given whole as "Replace the whole of"; other edits are unified diffs against the previous task's tree (save the block to a file and `git apply` it, or make the same edits by hand). Generated files (`internal/infra/db/sqlc/gen/**`, `api/openapi.yaml`, `internal/app/web/testdata/routes.golden`, `internal/app/web/contract/testdata/parity.golden.json`) are regenerated with the commands given, never edited.

## Global Constraints

- No backward compatibility: no data migration (0016 drops the three template tables), no reading of the old body fields, no `instance.backup_defaults` anywhere (API or agent).
- Migration name exactly `0016_templates_x_winterflow.sql`, in both `internal/infra/db/migrations/sqlite` and `.../pgsql`; ASCII only; after editing SQL run `make sqlc` and commit the regenerated `internal/infra/db/sqlc/gen` (`make sqlc-check` must pass).
- `templates.source` is `org` or `catalog`; CHECK: an org row has `org_id`, `slug = ''`, `withdrawn_at` NULL; a catalog row has no `org_id` and a non-empty `slug`. Unique `(org_id, name) WHERE source = 'org'`; unique `(slug) WHERE source = 'catalog'`.
- Every template read is scoped to the caller's org: its own templates plus every catalog template; another org's template is 404. Save-template and delete-template on a catalog template answer 403 (`model.ErrCatalogReadOnly`, class `model.ErrForbidden`).
- A template body is exactly `{files, summary, description, icon, color}`; `summary` one line <= 200 bytes, `description` <= 4000, `icon` <= 64, `color` <= 32, the encoded body <= 1 MiB (`template.MaxBodyBytes`); the org template name rule stays `^[a-z][a-z0-9-]{0,62}$`.
- Parameters, exposed ports and backups are only ever derived from `xwinterflow.Parse(xwinterflow.ModeTemplate, ...)`; a non-external volume with a literal `name:` is an error in a template.
- Instance create body: `server_id`, `key` (unchanged replay semantics), `template_id`, `revision` (0 = latest), `name` (default: the template's), `values`, `secrets` (ECIES), `hosts` (`[{host, expose}]`, http expose targets only), `backups` (`{name: {enabled, storage}}`, declared backups only; enabled needs a storage).
- A plain value may not contain `$`; a secret must pass `crypto.CheckCiphertext`; a secret value never leaves the browser unencrypted and never appears in any API answer (`<encrypted>` only).
- A render never sends `ports` in the config (the agent keeps the app's host ports and pins) and sends a `backups` list only for a create or a pending instance.
- `template_upgrade` rollout scope key is `<org_id>:<template_id>` (`model.UpgradeScopeKey`); the API's `get-rollouts` `scope_key` query and the rollout view's `scope_key` stay the template id.
- Fail closed: every instance route keeps `serverCaller` (org ownership of the server and the org the call acts for); the new `inspect-template` route is `api.User` and stores nothing.
- Never touch `web/src/components/ui/` (vendored shadcn). After `pnpm --dir web run build`, `web/dist/.gitkeep` must still exist (`touch web/dist/.gitkeep`).
- Tasks 2 to 5 change one Go API in steps: after Task 2 only `internal/domain/template` builds and passes, after Task 3 the tree builds but the contract tests (`internal/app/web/contract`) compile again only in Task 5. Run each task's own commands; Task 5 ends with the whole tree green. On the web side `pnpm --dir web exec tsc -b` is green again at Task 10 (Vitest does not type-check, so every task's Vitest runs pass).
- Coverage gate: >= 80% of changed lines per language (merged SQLite + Postgres profile, see "How to run things"); `api/openapi.yaml` regenerated with `make openapi` whenever a route, request or response type changes.
- Commit messages: Conventional Commits, ending with a blank line and `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` (the `-m ... -m ...` form below does that).

## How to run things

- One Go package: `go test ./internal/domain/template/`; one test: add `-run TestName`. The usecase packages take 1-10 s (`internal/domain/usecase/rollouts` about 8 s), the orchestrator package about a minute.
- Contract suite on SQLite: `WF_CONTRACT_GUARD=require go test ./internal/app/web/contract/` (about 10 s; a partial `-run` needs the guard unset or it fails on purpose).
- Postgres: `make test-pg` (Docker; the contract suite and `internal/infra/db/...` on a throwaway `postgres:16`, a few minutes); one test there: `make test-pg TEST_PG_ARGS='-run TestTemplatesXWinterflowMigration -v'`.
- sqlc: `make sqlc` (about 5 s; needs a C compiler), `make sqlc-check`.
- OpenAPI and goldens: `make openapi`; `go test ./internal/app/web/ -run TestRoutesGolden -update`; `go test ./internal/app/web/contract/ -run TestParity -update` (review the diff).
- Web: `CI=true pnpm --dir web install --frozen-lockfile` once; one file: `CI=true pnpm --dir web exec vitest run src/lib/template-values.test.ts`; all: `CI=true pnpm --dir web run test` (about 4 min); `pnpm --dir web exec tsc -b`; `pnpm --dir web run lint`.
- CI's coverage numbers: `make test && make test-pg TEST_PG_ARGS='-race -covermode=atomic -coverpkg=./... -coverprofile=contract-postgres.out' && tail -n +2 contract-postgres.out >> coverage.out && make web-test`, then `go run ./tools/diffcov --base origin/main --min 80 --go coverage.out --lcov web/coverage/lcov.info` and `go run ./tools/diffcov ratchet --go coverage.out --lcov web/coverage/lcov.info`.

## Spec decisions this plan makes

- **Scope (controller ruling, 2026-10-15):** deleting the old body would break today's template editor and install flow, so this sub-project also adapts the existing web UI: (a) the org template editor becomes files plus metadata (name, summary, description, icon, colour) with a read-only Detected panel, and "Save as template" copies an app's files; (b) New app -> From a template renders the declared variables, an optional app name, a hostname per http expose entry (when the server has registered domains) and backup storages; (c) the instance panel and the update dialog use the new values model. The catalog gallery, the catalog section of the Templates page, the ⌘K Templates group and catalog logos stay in sub-project 4; the catalog builder and sync in sub-project 3. Until sub-project 4 the web lists only the org's own templates (`orgTemplates`).
- **Detected panel source:** `get-template` returns `detected` for a stored revision and a new `POST /api/v1/template/inspect-template` reads an unsaved body the same way (no write; a body a save would refuse answers 200 with `problem`). The editor asks it 500 ms after the draft last changed, only while the draft differs from the saved version (or is new); no YAML parser is added to the web bundle (the old `lang-yaml` walk in `template-draft.ts` goes).
- **App name:** stored on the instance (`template_instances.name`, the create's `name` or the template's name); every later render (update, upgrade, unlink) uses the app's current name from the API's app list when it has one, so a rename through rename-app sticks; a pending instance uses the stored name.
- **Hostnames:** stored on the instance (`template_instances.hosts`) and rendered into every save as the app's ingress (`{domain, expose}`); update and upgrade take an optional `hosts` that replaces them. A stored hostname whose expose entry a new revision dropped makes the upgrade `ErrNeedsValues` (Upgrade all skips it; a single upgrade can send new `hosts`).
- **Values:** only values given are sent as app variables, so a declared variable without one keeps the compose file's literal; a given `""` clears a stored value; "required" means a non-empty value even when the compose file has a literal (sub-project 1's `CheckValues`). A stored value of a variable the revision dropped, or one that changed between plain and secret, is dropped.
- **Backups:** the create's `backups` is `{name: {enabled, storage}}` for declared backups only (an enabled one needs a storage); an update or upgrade of a created instance sends no backups list, so the app keeps its settings and a backup the revision adds starts off; `instance.backup_defaults` is removed from the API and the agent (`applyBackupDefaults`).
- **Request hash:** server, template, requested revision, name, values, secret names, hosts and backups.
- **Catalog rows:** `slug` is `NOT NULL DEFAULT ''` (the CHECK forces it non-empty for catalog rows) rather than nullable; `name` widens to `VARCHAR(255)` (catalog display names), the org name rule is unchanged. A withdrawn catalog template stays readable, is listed only to an org with live instances of it, refuses new installs (`model.ErrTemplateWithdrawn`, 409) and still updates and upgrades.
- **403:** a new error class `model.ErrForbidden`, mapped by `webutil.StatusFor`/`webutil.Fail`; save-template and delete-template look the template up first (a catalog one is 403 before any validation).
- **Rollout scope key:** stored as `<org_id>:<template_id>` (73 characters, so Postgres widens `rollouts.scope_key` to `VARCHAR(80)`; SQLite does not enforce lengths, its migration says so in a comment); the API keeps exposing the template id, so `useRollout` is unchanged.
- **A stored body the parser now refuses:** `get-template` still answers, with the parse error as `detected.warnings`; creating, updating or upgrading from it fails with that error.
- **Example compose of a new template:** `traefik/whoami` with one exposed port and one declared variable, so a new template shows a filled Detected panel at once.

## Review Focus

1. **A catalog template changed through an org's id** (save-template with a catalog `id`, delete-template, `AddRevision` on a catalog row) — pinned by `TestCatalogTemplatesAreReadByEveryOrg` (Task 1), `TestCatalogTemplatesAreReadOnly` in `internal/domain/usecase/templates` (Task 3) and `TestCatalogTemplatesAreReadOnly` in the contract suite (Task 5).
2. **An upgrade or update that quietly loses what the instance chose** (the app's renamed name, its hostnames, its backup settings re-sent or wiped, a newly required value rendered empty) — pinned by `TestUpdateKeepsTheNameAndHosts` and `TestUpgradeNeedsValuesAndSendsNoBackups` (Task 3) and `TestInstanceUpdateAndUpgrade` (Task 5).
3. **Two orgs upgrading one catalog template blocking each other, or reading each other's rollouts** — pinned by `TestUpgradeOfACatalogTemplateIsPerOrg` (Task 4) and `TestCatalogInstances` (Task 5).
4. **A secret or a `$` value going where it must not** (a secret sent plain, a plaintext in `secrets`, a plain `$` reaching `.env`, a secret in an answer) — pinned by `TestResolveRefuses` and `TestRenderIsWhatTheEditorWouldSend` (Task 2), `TestInstanceUpdatePending` (Task 5, every answer through `noSecrets`).
5. **The migration leaving old rows, an unenforced CHECK, or a scope key too long for Postgres** — pinned by `TestTemplatesXWinterflowMigration` (Task 1; run it on Postgres with `make test-pg`).


---
### Task 1: Migration 0016, sqlc and the templates repository: org and catalog templates

The tables hold org and catalog templates side by side; every read is scoped to "the org's own plus every catalog template", every write to the org's own. Instances gain the app name the create asked for and their hostnames. Old template data is dropped, not migrated.

**Files:**
- Modify: `internal/domain/model/errors.go`
- Modify: `internal/domain/model/template.go`
- Modify: `internal/domain/port/template.go`
- Modify: `internal/domain/usecase/rollouts/upgrade_test.go`
- Modify: `internal/domain/usecase/templates/instances.go`
- Modify: `internal/domain/usecase/templates/templates.go`
- Create: `internal/infra/db/migrate/templates_test.go`
- Create: `internal/infra/db/migrations/pgsql/0016_templates_x_winterflow.sql`
- Create: `internal/infra/db/migrations/sqlite/0016_templates_x_winterflow.sql`
- Create: `internal/infra/db/repository/template/catalog_test.go`
- Modify: `internal/infra/db/repository/template/pgsql.go`
- Modify: `internal/infra/db/repository/template/repo.go`
- Modify: `internal/infra/db/repository/template/repo_test.go`
- Modify: `internal/infra/db/repository/template/sqlite.go`
- Modify: `internal/infra/db/repository/timestamps_test.go`
- Modify: `internal/infra/db/sqlc/query/pgsql/templates.sql`
- Modify: `internal/infra/db/sqlc/query/sqlite/templates.sql`
- Modify: `internal/infra/db/sqlc/shapes_test.go`
- Regenerate: `internal/infra/db/sqlc/gen/pgsql/models.go` (never edited by hand)
- Regenerate: `internal/infra/db/sqlc/gen/pgsql/querier.go` (never edited by hand)
- Regenerate: `internal/infra/db/sqlc/gen/pgsql/templates.sql.go` (never edited by hand)
- Regenerate: `internal/infra/db/sqlc/gen/sqlite/models.go` (never edited by hand)
- Regenerate: `internal/infra/db/sqlc/gen/sqlite/querier.go` (never edited by hand)
- Regenerate: `internal/infra/db/sqlc/gen/sqlite/templates.sql.go` (never edited by hand)

**Interfaces:**
- Consumes: `backend.TxManager`, `types.DateTime`, `util.RefString`/`util.DerefString`, sqlc `sqlitegen`/`pgsqlgen` (regenerated).
- Produces:
  - Migration `0016_templates_x_winterflow` (both engines): drops and recreates `templates` (`source`, nullable `org_id`, `slug`, `withdrawn_at`, a CHECK tying them together, partial uniques `idx_templates_org_name` / `idx_templates_catalog_slug`), `template_revisions` (+ `digest`), `template_instances` (+ `name`, `hosts`); deletes `template_upgrade` rollouts; Postgres widens `rollouts.scope_key` to `VARCHAR(80)`.
  - `model.Template{Source, Slug, WithdrawnAt *time.Time}` and `(Template) Catalog() bool`; `model.TemplateSourceOrg = "org"`, `model.TemplateSourceCatalog = "catalog"`; `model.TemplateRevision.Digest`; `model.Instance{Name, Hosts []model.InstanceHost}`; `model.InstanceHost{Host, Expose}` (JSON `host`, `expose`).
  - `model.ErrForbidden` (a new error class), `model.ErrCatalogReadOnly` (matches `ErrForbidden`), `model.ErrTemplateWithdrawn` (matches `ErrConflict`), `model.UpgradeScopeKey(orgID, templateID string) string` (`<org_id>:<template_id>`).
  - `port.TemplateRepository.MarkSaved(ctx, id, revision, params, secretNames, hosts []model.InstanceHost) error` (one parameter more); `CreateTemplate` takes either source; `GetTemplate`/`ListTemplates`/`ListTemplateNames` read the org's own plus every catalog template (`ListTemplates`: own first, a withdrawn catalog template only while the org has live instances of it); `AddRevision`/`DeleteTemplate` touch only the org's own (a catalog id is `ErrTemplateNotFound` there); `DeleteTemplate` deletes rollouts keyed `UpgradeScopeKey(orgID, templateID)`.

- [ ] **Step 1: Write the failing tests**

Modify `internal/domain/usecase/rollouts/upgrade_test.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/usecase/rollouts/upgrade_test.go b/internal/domain/usecase/rollouts/upgrade_test.go
--- a/internal/domain/usecase/rollouts/upgrade_test.go
+++ b/internal/domain/usecase/rollouts/upgrade_test.go
@@ -194,11 +194,11 @@ type markFails struct {
 	on atomic.Bool
 }
 
-func (m *markFails) MarkSaved(ctx context.Context, id string, revision int64, params map[string]string, secretNames []string) error {
+func (m *markFails) MarkSaved(ctx context.Context, id string, revision int64, params map[string]string, secretNames []string, hosts []model.InstanceHost) error {
 	if m.on.Load() {
 		return errors.New("database is locked")
 	}
-	return m.TemplateRepository.MarkSaved(ctx, id, revision, params, secretNames)
+	return m.TemplateRepository.MarkSaved(ctx, id, revision, params, secretNames, hosts)
 }
 
 // A step that went through but left the instance below the target (its
````

Create `internal/infra/db/migrate/templates_test.go`:

````go
package migrate

import "testing"

// 0016_templates_x_winterflow drops the old templates and their instances
// (nothing is migrated), clears the template_upgrade rollouts but keeps the
// reloads, and the new tables hold org and catalog templates apart.
func TestTemplatesXWinterflowMigration(t *testing.T) {
	const (
		org    = "00000000-0000-7000-8000-000000000001"
		server = "00000000-0000-7000-8000-000000000011"
		tpl    = "00000000-0000-7000-8000-000000000021"
		cat    = "00000000-0000-7000-8000-000000000022"
		now    = "2026-10-14 12:00:00"
	)
	for _, d := range engines(t) {
		t.Run(d, func(t *testing.T) {
			ctx := tctx(t)
			db := openEmpty(t, d)
			if err := MigrateTo(ctx, db, d, 15); err != nil {
				t.Fatal(err)
			}
			mustExec(t, db, d, `INSERT INTO organizations (organization_id, name, created_at) VALUES (?, 'o', ?)`, org, now)
			mustExec(t, db, d, `INSERT INTO servers (server_id, organization_id, name, created_at) VALUES (?, ?, 'box', ?)`, server, org, now)
			mustExec(t, db, d, `INSERT INTO templates (id, org_id, name, latest_revision, created_at, updated_at) VALUES (?, ?, 'shop', 1, ?, ?)`, tpl, org, now, now)
			mustExec(t, db, d, `INSERT INTO template_revisions (template_id, revision, body, created_at) VALUES (?, 1, '{}', ?)`, tpl, now)
			mustExec(t, db, d, `INSERT INTO template_instances (id, org_id, instance_key, app_id, server_id, template_id, revision, params, secret_names, request_hash, state, created_at, updated_at)
				VALUES (?, ?, 'k', ?, ?, ?, 1, '{}', '[]', 'h', 'created', ?, ?)`, "00000000-0000-7000-8000-000000000031", org, "00000000-0000-7000-8000-000000000041", server, tpl, now, now)
			for id, kind := range map[string]string{
				"00000000-0000-7000-8000-000000000050": "template_upgrade",
				"00000000-0000-7000-8000-000000000051": "vars_reload",
			} {
				mustExec(t, db, d, `INSERT INTO rollouts (id, org_id, kind, scope, scope_key, state, created_at) VALUES (?, ?, ?, '{}', ?, 'done', ?)`,
					id, org, kind, tpl, now)
			}

			if err := Migrate(ctx, db, d); err != nil {
				t.Fatal(err)
			}
			for _, table := range []string{"templates", "template_revisions", "template_instances"} {
				if n := count(t, db, d, `SELECT count(*) FROM `+table); n != 0 {
					t.Fatalf("%s kept %d rows", table, n)
				}
			}
			if n := count(t, db, d, `SELECT count(*) FROM rollouts WHERE kind = 'template_upgrade'`); n != 0 {
				t.Fatalf("%d upgrade rollouts kept", n)
			}
			if n := count(t, db, d, `SELECT count(*) FROM rollouts WHERE kind = 'vars_reload'`); n != 1 {
				t.Fatalf("%d reload rollouts kept, want 1", n)
			}

			// An org template needs its org; a catalog one a slug and no org.
			mustExec(t, db, d, `INSERT INTO templates (id, source, org_id, name, latest_revision, created_at, updated_at) VALUES (?, 'org', ?, 'shop', 1, ?, ?)`, tpl, org, now, now)
			mustExec(t, db, d, `INSERT INTO templates (id, source, slug, name, latest_revision, created_at, updated_at) VALUES (?, 'catalog', 'shop', 'Shop', 1, ?, ?)`, cat, now, now)
			for _, bad := range []string{
				`INSERT INTO templates (id, source, name, latest_revision, created_at, updated_at) VALUES ('00000000-0000-7000-8000-000000000023', 'org', 'x', 1, '` + now + `', '` + now + `')`,
				`INSERT INTO templates (id, source, org_id, slug, name, latest_revision, created_at, updated_at) VALUES ('00000000-0000-7000-8000-000000000024', 'catalog', '` + org + `', 's', 'x', 1, '` + now + `', '` + now + `')`,
				`INSERT INTO templates (id, source, name, latest_revision, created_at, updated_at) VALUES ('00000000-0000-7000-8000-000000000025', 'catalog', 'x', 1, '` + now + `', '` + now + `')`,
				`INSERT INTO templates (id, source, slug, name, latest_revision, created_at, updated_at) VALUES ('00000000-0000-7000-8000-000000000026', 'catalog', 'shop', 'y', 1, '` + now + `', '` + now + `')`,
			} {
				if _, err := db.ExecContext(ctx, bad); err == nil {
					t.Fatalf("accepted: %s", bad)
				}
			}
			// A per-org upgrade scope key fits.
			mustExec(t, db, d, `INSERT INTO rollouts (id, org_id, kind, scope, scope_key, state, created_at) VALUES ('00000000-0000-7000-8000-000000000060', ?, 'template_upgrade', '{}', ?, 'queued', ?)`,
				org, org+":"+cat, now)
			if got := count(t, db, d, `SELECT length(scope_key) FROM rollouts WHERE kind = 'template_upgrade'`); got != len(org+":"+cat) {
				t.Fatalf("scope key length %d", got)
			}
		})
	}
}
````

Create `internal/infra/db/repository/template/catalog_test.go`:

````go
package template

import (
	"errors"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db/dbtest"
)

var cat1, cat2 = dbtest.ID(31), dbtest.ID(32)

// mustCatalog inserts a catalog template (revision 1 with digest d).
func mustCatalog(t *testing.T, r *Repo, id, slug, name string, withdrawn *time.Time) {
	t.Helper()
	err := r.CreateTemplate(dbtest.Ctx(t), model.Template{ID: id, Source: model.TemplateSourceCatalog, Slug: slug, Name: name, WithdrawnAt: withdrawn},
		model.TemplateRevision{Body: []byte(`{"files":{}}`), Digest: "sha256:" + slug})
	if err != nil {
		t.Fatal(err)
	}
}

// Every org reads every catalog template; only its own templates change.
func TestCatalogTemplatesAreReadByEveryOrg(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCreate(t, r, org1, tpl1, "zeta")
	mustCatalog(t, r, cat1, "actual-budget", "Actual Budget", nil)
	for _, org := range []string{org1, org2} {
		got, err := r.GetTemplate(ctx, org, cat1)
		if err != nil || !got.Catalog() || got.OrgID != "" || got.Slug != "actual-budget" || got.WithdrawnAt != nil {
			t.Fatalf("%s reads %+v, %v", org, got, err)
		}
	}
	list, err := r.ListTemplates(ctx, org1)
	if err != nil || len(list) != 2 || list[0].ID != tpl1 || list[1].ID != cat1 || list[1].Source != model.TemplateSourceCatalog {
		t.Fatalf("org1 list (its own first) = %+v, %v", list, err)
	}
	names, err := r.ListTemplateNames(ctx, org2)
	if err != nil || len(names) != 1 || names[cat1] != "Actual Budget" {
		t.Fatalf("org2 names = %+v, %v", names, err)
	}
	rev, err := r.GetRevision(ctx, cat1, 1)
	if err != nil || rev.Digest != "sha256:actual-budget" {
		t.Fatalf("revision = %+v, %v", rev, err)
	}
	if _, err := r.AddRevision(ctx, org1, cat1, "Actual Budget", 1, model.TemplateRevision{Body: []byte(`{}`)}); !errors.Is(err, model.ErrTemplateNotFound) {
		t.Fatalf("an org adds a catalog revision: %v", err)
	}
	if err := r.DeleteTemplate(ctx, org1, cat1); !errors.Is(err, model.ErrTemplateNotFound) {
		t.Fatalf("an org deletes a catalog template: %v", err)
	}
	err = r.CreateTemplate(ctx, model.Template{ID: cat2, Source: model.TemplateSourceCatalog, Slug: "actual-budget", Name: "Other"},
		model.TemplateRevision{Body: []byte(`{}`)})
	if !errors.Is(err, model.ErrTemplateNameTaken) {
		t.Fatalf("a taken slug: %v", err)
	}
	if err := r.CreateTemplate(ctx, model.Template{ID: cat2, Source: "mine", Name: "x"}, model.TemplateRevision{}); err == nil {
		t.Fatal("an unknown source was stored")
	}
}

// A withdrawn catalog template is listed only to an org that still has
// live instances of it; it stays readable to every org.
func TestWithdrawnCatalogTemplatesListOnlyWithInstances(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	mustCatalog(t, r, cat1, "gone", "Gone", &at)
	inst := instance(1, "k")
	inst.TemplateID = cat1
	if _, _, err := r.ClaimInstance(ctx, inst); err != nil {
		t.Fatal(err)
	}
	if list, _ := r.ListTemplates(ctx, org1); len(list) != 1 || list[0].Instances != 1 || list[0].WithdrawnAt == nil || !list[0].WithdrawnAt.Equal(at) {
		t.Fatalf("org1 (an instance) list = %+v", list)
	}
	if list, _ := r.ListTemplates(ctx, org2); len(list) != 0 {
		t.Fatalf("org2 (no instance) list = %+v", list)
	}
	if got, err := r.GetTemplate(ctx, org2, cat1); err != nil || got.WithdrawnAt == nil {
		t.Fatalf("org2 reads %+v, %v", got, err)
	}
}

// An instance's name and hosts round-trip through the claim; a confirmed
// save replaces the hosts.
func TestInstanceNameAndHostsRoundTrip(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCreate(t, r, org1, tpl1, "shop")
	inst := instance(1, "k")
	inst.Name = "My shop"
	inst.Hosts = []model.InstanceHost{{Host: "a.example.test", Expose: "app:8080"}}
	got, _, err := r.ClaimInstance(ctx, inst)
	if err != nil || got.Name != "My shop" || len(got.Hosts) != 1 || got.Hosts[0] != inst.Hosts[0] {
		t.Fatalf("claimed = %+v, %v", got, err)
	}
	if err := r.MarkSaved(ctx, got.ID, 1, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.GetInstance(ctx, org1, "k"); got.Hosts == nil || len(got.Hosts) != 0 {
		t.Fatalf("after a save without hosts = %+v", got.Hosts)
	}
}
````

Modify `internal/infra/db/repository/template/repo_test.go` (`git apply` this diff):

````diff
diff --git a/internal/infra/db/repository/template/repo_test.go b/internal/infra/db/repository/template/repo_test.go
--- a/internal/infra/db/repository/template/repo_test.go
+++ b/internal/infra/db/repository/template/repo_test.go
@@ -31,7 +31,7 @@ func newRepo(t *testing.T) (*Repo, *db.DB) {
 
 func mustCreate(t *testing.T, r *Repo, orgID, id, name string) {
 	t.Helper()
-	err := r.CreateTemplate(dbtest.Ctx(t), model.Template{ID: id, OrgID: orgID, Name: name, CreatedBy: "u"},
+	err := r.CreateTemplate(dbtest.Ctx(t), model.Template{ID: id, Source: model.TemplateSourceOrg, OrgID: orgID, Name: name, CreatedBy: "u"},
 		model.TemplateRevision{Body: []byte(`{"files":{}}`), Note: "first", CreatedBy: "u"})
 	if err != nil {
 		t.Fatal(err)
@@ -56,7 +56,7 @@ func TestTemplateNamesAreUniquePerOrg(t *testing.T) {
 	r, _ := newRepo(t)
 	ctx := dbtest.Ctx(t)
 	mustCreate(t, r, org1, tpl1, "shop")
-	err := r.CreateTemplate(ctx, model.Template{ID: tpl2, OrgID: org1, Name: "shop"}, model.TemplateRevision{Body: []byte(`{}`)})
+	err := r.CreateTemplate(ctx, model.Template{ID: tpl2, Source: model.TemplateSourceOrg, OrgID: org1, Name: "shop"}, model.TemplateRevision{Body: []byte(`{}`)})
 	if !errors.Is(err, model.ErrTemplateNameTaken) || !errors.Is(err, model.ErrRejected) {
 		t.Fatalf("same name in the org: %v", err)
 	}
@@ -170,7 +170,7 @@ func TestUnlinkFreesTheKeyAndDeleteRetiresIt(t *testing.T) {
 	ctx := dbtest.Ctx(t)
 	mustCreate(t, r, org1, tpl1, "shop")
 	first, _, _ := r.ClaimInstance(ctx, instance(1, "k"))
-	if err := r.MarkSaved(ctx, first.ID, 2, map[string]string{"NAME": "b"}, nil); err != nil {
+	if err := r.MarkSaved(ctx, first.ID, 2, map[string]string{"NAME": "b"}, nil, nil); err != nil {
 		t.Fatal(err)
 	}
 	saved, err := r.GetInstance(ctx, org1, "k")
@@ -194,7 +194,7 @@ func TestUnlinkFreesTheKeyAndDeleteRetiresIt(t *testing.T) {
 		t.Fatal("a deleted instance's key was claimed again")
 	}
 	// A late save confirmation does not revive a deleted instance.
-	if err := r.MarkSaved(ctx, second.ID, 3, nil, nil); err != nil {
+	if err := r.MarkSaved(ctx, second.ID, 3, nil, nil, nil); err != nil {
 		t.Fatal(err)
 	}
 	if got, _ := r.GetInstance(ctx, org1, "k"); got.State != model.InstanceDeleted {
@@ -254,7 +254,7 @@ func TestListInstancesAndBehind(t *testing.T) {
 	if _, err := r.AddRevision(ctx, org1, tpl1, "shop", 1, model.TemplateRevision{Body: []byte(`{}`)}); err != nil {
 		t.Fatal(err)
 	}
-	if err := r.MarkSaved(ctx, a.ID, 2, nil, nil); err != nil {
+	if err := r.MarkSaved(ctx, a.ID, 2, nil, nil, nil); err != nil {
 		t.Fatal(err)
 	}
 	list, _ := r.ListTemplates(ctx, org1)
@@ -283,7 +283,7 @@ func TestListUpgradableInstances(t *testing.T) {
 		if _, _, err := r.ClaimInstance(ctx, inst); err != nil {
 			t.Fatal(err)
 		}
-		if err := r.MarkSaved(ctx, inst.ID, rev, inst.Params, inst.SecretNames); err != nil {
+		if err := r.MarkSaved(ctx, inst.ID, rev, inst.Params, inst.SecretNames, nil); err != nil {
 			t.Fatal(err)
 		}
 	}
@@ -369,9 +369,9 @@ func TestInstanceLinksForApp(t *testing.T) {
 	}
 }
 
-// Deleting a template deletes its upgrade rollouts (scope key: the template
-// id), not another template's nor the org's reloads; a refused delete
-// keeps them.
+// Deleting a template deletes the org's upgrade rollouts of it (scope key:
+// <org_id>:<template_id>), not another template's nor the org's reloads; a
+// refused delete keeps them.
 func TestDeleteTemplateDeletesItsRollouts(t *testing.T) {
 	r, d := newRepo(t)
 	ctx := dbtest.Ctx(t)
@@ -382,8 +382,8 @@ func TestDeleteTemplateDeletesItsRollouts(t *testing.T) {
 	mustCreate(t, r, org1, tpl1, "shop")
 	mustCreate(t, r, org1, tpl2, "blog")
 	for i, ro := range []model.Rollout{
-		{Kind: model.RolloutTemplateUpgrade, ScopeKey: tpl1},
-		{Kind: model.RolloutTemplateUpgrade, ScopeKey: tpl2},
+		{Kind: model.RolloutTemplateUpgrade, ScopeKey: model.UpgradeScopeKey(org1, tpl1)},
+		{Kind: model.RolloutTemplateUpgrade, ScopeKey: model.UpgradeScopeKey(org1, tpl2)},
 		{Kind: model.RolloutVarsReload, ScopeKey: org1},
 	} {
 		ro.ID, ro.OrgID, ro.Scope = dbtest.ID(300+i), org1, []byte(`{}`)
@@ -404,7 +404,7 @@ func TestDeleteTemplateDeletesItsRollouts(t *testing.T) {
 	if err := r.DeleteTemplate(ctx, org1, tpl1); err != nil {
 		t.Fatal(err)
 	}
-	if n := dbtest.QueryString(t, d, `SELECT COUNT(*) FROM rollouts WHERE scope_key = ?`, tpl1); n != "0" || count() != "2" {
+	if n := dbtest.QueryString(t, d, `SELECT COUNT(*) FROM rollouts WHERE scope_key = ?`, model.UpgradeScopeKey(org1, tpl1)); n != "0" || count() != "2" {
 		t.Fatalf("rollouts of the deleted template: %s, all: %s", n, count())
 	}
 }
````

Modify `internal/infra/db/repository/timestamps_test.go` (`git apply` this diff):

````diff
diff --git a/internal/infra/db/repository/timestamps_test.go b/internal/infra/db/repository/timestamps_test.go
--- a/internal/infra/db/repository/timestamps_test.go
+++ b/internal/infra/db/repository/timestamps_test.go
@@ -138,7 +138,12 @@ func TestSQLiteTimestampsAreDateTimeText(t *testing.T) {
 	}
 
 	tplID := dbtest.ID(501)
-	if err := templates.CreateTemplate(ctx, model.Template{ID: tplID, OrgID: orgID, Name: "shop"},
+	if err := templates.CreateTemplate(ctx, model.Template{ID: tplID, Source: model.TemplateSourceOrg, OrgID: orgID, Name: "shop"},
+		model.TemplateRevision{Body: []byte(`{}`)}); err != nil {
+		t.Fatal(err)
+	}
+	// withdrawn_at: a catalog template the feed dropped.
+	if err := templates.CreateTemplate(ctx, model.Template{ID: dbtest.ID(503), Source: model.TemplateSourceCatalog, Slug: "gone", Name: "Gone", WithdrawnAt: &at},
 		model.TemplateRevision{Body: []byte(`{}`)}); err != nil {
 		t.Fatal(err)
 	}
````

Modify `internal/infra/db/sqlc/shapes_test.go` (`git apply` this diff):

````diff
diff --git a/internal/infra/db/sqlc/shapes_test.go b/internal/infra/db/sqlc/shapes_test.go
--- a/internal/infra/db/sqlc/shapes_test.go
+++ b/internal/infra/db/sqlc/shapes_test.go
@@ -228,6 +228,8 @@ var (
 	_ = pgsqlgen.GetTemplateParams(sqlitegen.GetTemplateParams{})
 	_ = sqlitegen.GetTemplateIDByNameParams(pgsqlgen.GetTemplateIDByNameParams{})
 	_ = pgsqlgen.GetTemplateIDByNameParams(sqlitegen.GetTemplateIDByNameParams{})
+	_ = sqlitegen.ListTemplatesParams(pgsqlgen.ListTemplatesParams{})
+	_ = pgsqlgen.ListTemplatesParams(sqlitegen.ListTemplatesParams{})
 	_ = sqlitegen.ListTemplatesRow(pgsqlgen.ListTemplatesRow{})
 	_ = pgsqlgen.ListTemplatesRow(sqlitegen.ListTemplatesRow{})
 	_ = sqlitegen.ListTemplateNamesRow(pgsqlgen.ListTemplateNamesRow{})
````

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/infra/db/... ./internal/domain/usecase/rollouts/`
Expected: FAIL — the tests use `model.TemplateSourceOrg`, `Hosts`, `Name`, `UpgradeScopeKey` and the six-argument `MarkSaved`, which do not exist yet (build failure).

- [ ] **Step 3: Implement**

Modify `internal/domain/model/errors.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/model/errors.go b/internal/domain/model/errors.go
--- a/internal/domain/model/errors.go
+++ b/internal/domain/model/errors.go
@@ -8,10 +8,12 @@ import "errors"
 // unresolved or reserved storage variable. ErrNotFound marks an unknown
 // template, revision or instance key, or one of another org (404).
 // ErrConflict marks a request the current state refuses (409).
+// ErrForbidden marks a change to something the caller may only read (403).
 var (
-	ErrRejected = errors.New("rejected")
-	ErrNotFound = errors.New("not found")
-	ErrConflict = errors.New("conflict")
+	ErrRejected  = errors.New("rejected")
+	ErrNotFound  = errors.New("not found")
+	ErrConflict  = errors.New("conflict")
+	ErrForbidden = errors.New("forbidden")
 )
 
 // classified is a sentinel error that also matches its class.
````

Modify `internal/domain/model/template.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/model/template.go b/internal/domain/model/template.go
--- a/internal/domain/model/template.go
+++ b/internal/domain/model/template.go
@@ -3,7 +3,7 @@ package model
 import "time"
 
 // Template errors. ErrTemplateNameTaken matches ErrRejected (400 naming
-// the field).
+// the field); ErrCatalogReadOnly matches ErrForbidden (403).
 var (
 	ErrTemplateNotFound     = &classified{"template not found", ErrNotFound}
 	ErrTemplateRevisionGone = &classified{"template revision not found", ErrNotFound}
@@ -14,8 +14,22 @@ var (
 	ErrKeyInUse             = &classified{"key in use with different values: update the instance instead", ErrConflict}
 	ErrKeyRetired           = &classified{"this key belonged to a deleted instance", ErrConflict}
 	ErrInstanceNotCreated   = &classified{"the instance was never created: retry the create or delete it", ErrConflict}
+	ErrCatalogReadOnly      = &classified{"catalog templates are read-only", ErrForbidden}
+	ErrTemplateWithdrawn    = &classified{"this template was withdrawn from the catalog: it takes no new installs", ErrConflict}
 )
 
+// Template sources: an org's own template, or one of the catalog (the same
+// for every org, read-only to all of them).
+const (
+	TemplateSourceOrg     = "org"
+	TemplateSourceCatalog = "catalog"
+)
+
+// UpgradeScopeKey is a template_upgrade rollout's scope key: one active
+// upgrade per org and template, so two orgs upgrading the same catalog
+// template don't block each other.
+func UpgradeScopeKey(orgID, templateID string) string { return orgID + ":" + templateID }
+
 // ManagedByTemplate refuses save-app or rollback-app on an app a live
 // (pending or created) instance manages: its changes go through the
 // instance. name is the template's ("" when it is gone).
@@ -27,12 +41,18 @@ func ManagedByTemplate(name string) error {
 	return &classified{"this app is managed by " + by + ": update the instance or unlink it", ErrConflict}
 }
 
-// Template is an org's stack definition; its body lives in revisions.
+// Template is a stack definition; its body lives in revisions. An org
+// template (Source TemplateSourceOrg) belongs to OrgID; a catalog template
+// (TemplateSourceCatalog) has no OrgID, a Slug, and WithdrawnAt once the
+// catalog dropped it.
 type Template struct {
 	ID             string
+	Source         string
 	OrgID          string
+	Slug           string
 	Name           string
 	LatestRevision int64
+	WithdrawnAt    *time.Time
 	CreatedAt      time.Time
 	CreatedBy      string
 	UpdatedAt      time.Time
@@ -47,12 +67,17 @@ type TemplateSummary struct {
 	Behind    int64
 }
 
-// TemplateRevision is one immutable revision: Body is the JSON body.
+// Catalog reports whether t is a catalog template.
+func (t Template) Catalog() bool { return t.Source == TemplateSourceCatalog }
+
+// TemplateRevision is one immutable revision: Body is the JSON body;
+// Digest identifies a catalog revision's files ("" for an org's).
 type TemplateRevision struct {
 	TemplateID string
 	Revision   int64
 	Body       []byte
 	Note       string
+	Digest     string
 	CreatedAt  time.Time
 	CreatedBy  string
 }
@@ -81,13 +106,24 @@ type Instance struct {
 	// TemplateID is "" once the template is gone.
 	TemplateID string
 	Revision   int64
-	// Params are the plain parameter values; SecretNames the secret
-	// parameters a value was given for (never a value).
+	// Name is the app name the create asked for.
+	Name string
+	// Params are the plain variable values given; SecretNames the secret
+	// variables a value was given for (never a value).
 	Params      map[string]string
 	SecretNames []string
+	// Hosts are the instance's ingress hostnames.
+	Hosts       []InstanceHost
 	RequestHash string
 	State       InstanceState
 	LastError   string
 	CreatedAt   time.Time
 	UpdatedAt   time.Time
 }
+
+// InstanceHost is one ingress hostname of an instance: Host routed to the
+// http expose entry Expose (<service>:<port>) of the template's compose.
+type InstanceHost struct {
+	Host   string `json:"host"`
+	Expose string `json:"expose"`
+}
````

Modify `internal/domain/port/template.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/port/template.go b/internal/domain/port/template.go
--- a/internal/domain/port/template.go
+++ b/internal/domain/port/template.go
@@ -7,31 +7,41 @@ import (
 )
 
 // TemplateRepository persists templates, their revisions and instances.
-// Every template read is scoped to an org: another org's id is
-// model.ErrTemplateNotFound.
+// Every template read is scoped to an org, which reads its own templates
+// and every catalog template: another org's id is
+// model.ErrTemplateNotFound. Only an org's own templates change through
+// AddRevision and DeleteTemplate.
 type TemplateRepository interface {
 	InstanceLinks
-	// CreateTemplate inserts the template and its revision 1 in one
-	// transaction; model.ErrTemplateNameTaken when the org has the name.
+	// CreateTemplate inserts the template (t.Source says whose: an org
+	// template needs OrgID, a catalog one Slug) and its revision 1 in one
+	// transaction; model.ErrTemplateNameTaken when the org has the name or
+	// the catalog the slug.
 	CreateTemplate(ctx context.Context, t model.Template, rev model.TemplateRevision) error
-	// AddRevision adds revision base+1 (renaming the template to name) in
-	// one transaction and returns it: model.ErrRevisionConflict when the
-	// latest revision is not base, model.ErrTemplateNameTaken when another
-	// template of the org has name, model.ErrTemplateNotFound when unknown.
+	// AddRevision adds revision base+1 of the org's template (renaming it
+	// to name) in one transaction and returns it: model.ErrRevisionConflict
+	// when the latest revision is not base, model.ErrTemplateNameTaken when
+	// another template of the org has name, model.ErrTemplateNotFound when
+	// the org has no such template (a catalog one included).
 	AddRevision(ctx context.Context, orgID, templateID, name string, base int64, rev model.TemplateRevision) (int64, error)
+	// GetTemplate returns a template the org may read.
 	GetTemplate(ctx context.Context, orgID, templateID string) (model.Template, error)
+	// ListTemplates returns the org's templates, then the catalog's (a
+	// withdrawn one only while the org has live instances of it), with the
+	// org's instance counts.
 	ListTemplates(ctx context.Context, orgID string) ([]model.TemplateSummary, error)
-	// ListTemplateNames returns the org's template names by id.
+	// ListTemplateNames returns the names of the templates the org may
+	// read, by id.
 	ListTemplateNames(ctx context.Context, orgID string) (map[string]string, error)
 	// GetRevision returns one revision; model.ErrTemplateRevisionGone when
 	// the template has no such revision.
 	GetRevision(ctx context.Context, templateID string, revision int64) (model.TemplateRevision, error)
 	// ListRevisions returns the revisions newest first, without bodies.
 	ListRevisions(ctx context.Context, templateID string) ([]model.TemplateRevision, error)
-	// DeleteTemplate removes the template, its revisions and its upgrade
-	// rollouts:
-	// model.ErrTemplateInUse while an instance is pending or created,
-	// model.ErrTemplateNotFound when unknown.
+	// DeleteTemplate removes the org's template, its revisions and the
+	// org's upgrade rollouts of it: model.ErrTemplateInUse while an
+	// instance is pending or created, model.ErrTemplateNotFound when the org
+	// has no such template (a catalog one included).
 	DeleteTemplate(ctx context.Context, orgID, templateID string) error
 
 	// ClaimInstance inserts inst as pending unless the org already has a
@@ -51,10 +61,10 @@ type TemplateRepository interface {
 	// templateID below revision, oldest first, without an instance whose
 	// server is not the org's: what an upgrade rollout of the org may touch.
 	ListUpgradableInstances(ctx context.Context, orgID, templateID string, revision int64) ([]model.Instance, error)
-	// MarkSaved records a save the agent confirmed: revision, values and
-	// state created, last error cleared. A deleted or unlinked instance is
-	// left alone.
-	MarkSaved(ctx context.Context, id string, revision int64, params map[string]string, secretNames []string) error
+	// MarkSaved records a save the agent confirmed: revision, values,
+	// hosts and state created, last error cleared. A deleted or unlinked
+	// instance is left alone.
+	MarkSaved(ctx context.Context, id string, revision int64, params map[string]string, secretNames []string, hosts []model.InstanceHost) error
 	// SetState moves a pending or created instance to state (deleted,
 	// unlinked), clearing its last error.
 	SetState(ctx context.Context, id string, state model.InstanceState) error
````

Modify `internal/domain/usecase/templates/instances.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/usecase/templates/instances.go b/internal/domain/usecase/templates/instances.go
--- a/internal/domain/usecase/templates/instances.go
+++ b/internal/domain/usecase/templates/instances.go
@@ -134,7 +134,7 @@ func (uc *UseCase) dispatchSave(ctx context.Context, userID string, inst model.I
 	// it records, not the secret ciphertexts.
 	id, stored := inst.ID, values.Stored
 	observer, done := uc.observe(inst, func(ctx context.Context) error {
-		return uc.repo.MarkSaved(ctx, id, revision, stored.Params, stored.SecretNames)
+		return uc.repo.MarkSaved(ctx, id, revision, stored.Params, stored.SecretNames, nil)
 	})
 	requestID, err := uc.apps.DispatchSave(ctx, userID, checked, observer)
 	return outcome(requestID, err, done, uc.reread(inst))
````

Modify `internal/domain/usecase/templates/templates.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/usecase/templates/templates.go b/internal/domain/usecase/templates/templates.go
--- a/internal/domain/usecase/templates/templates.go
+++ b/internal/domain/usecase/templates/templates.go
@@ -52,7 +52,7 @@ func (uc *UseCase) SaveTemplate(ctx context.Context, userID, orgID string, in Sa
 			return SavedTemplate{}, template.Invalid("base_revision", "a new template has none (0)")
 		}
 		out.ID, out.Revision = util.GenerateID(), 1
-		err = uc.repo.CreateTemplate(ctx, model.Template{ID: out.ID, OrgID: orgID, Name: in.Name, CreatedBy: userID}, rev)
+		err = uc.repo.CreateTemplate(ctx, model.Template{ID: out.ID, Source: model.TemplateSourceOrg, OrgID: orgID, Name: in.Name, CreatedBy: userID}, rev)
 	} else {
 		out.Revision, err = uc.repo.AddRevision(ctx, orgID, in.ID, in.Name, in.BaseRevision, rev)
 	}
````

Create `internal/infra/db/migrations/pgsql/0016_templates_x_winterflow.sql`:

````sql
-- Templates use x-winterflow (sub-project 2): a revision's body is the
-- app's files plus summary, description, icon and color; parameters,
-- exposed ports and backups come from the compose file's x-winterflow
-- block. Nothing is migrated (no backward compatibility): the three tables
-- are recreated empty and the upgrade rollouts that named their rows go.
--
-- templates.source is org (an org's own, editable) or catalog (the same
-- for every org, read-only; org_id NULL, slug set, withdrawn_at once the
-- feed drops it). template_instances.name is the app name the create
-- asked for (renders use the app's current name once it has one) and hosts
-- the instance's ingress hostnames ([{host, expose}]), rendered into every
-- save.
--
-- A template_upgrade rollout's scope key becomes <org_id>:<template_id>
-- (73 characters): rollouts.scope_key is widened to hold it.

DROP TABLE template_instances;
DROP TABLE template_revisions;
DROP TABLE templates;
DELETE FROM rollouts WHERE kind = 'template_upgrade';
ALTER TABLE rollouts ALTER COLUMN scope_key TYPE VARCHAR(80);

CREATE TABLE templates (
    id UUID PRIMARY KEY,
    source VARCHAR(16) NOT NULL,
    org_id UUID,
    slug VARCHAR(128) NOT NULL DEFAULT '',
    name VARCHAR(255) NOT NULL,
    latest_revision INTEGER NOT NULL,
    withdrawn_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL,
    CHECK ((source = 'org' AND org_id IS NOT NULL AND slug = '' AND withdrawn_at IS NULL)
        OR (source = 'catalog' AND org_id IS NULL AND slug <> '')),
    FOREIGN KEY (org_id) REFERENCES organizations (organization_id) ON DELETE CASCADE
);

-- An org's template names are unique in the org; a catalog slug is unique.
CREATE UNIQUE INDEX idx_templates_org_name ON templates (org_id, name) WHERE source = 'org';
CREATE UNIQUE INDEX idx_templates_catalog_slug ON templates (slug) WHERE source = 'catalog';

CREATE TABLE template_revisions (
    template_id UUID NOT NULL,
    revision INTEGER NOT NULL,
    body TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    digest VARCHAR(80) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    PRIMARY KEY (template_id, revision),
    FOREIGN KEY (template_id) REFERENCES templates (id) ON DELETE CASCADE
);

-- template_id is NULL once the template is gone (only deleted and unlinked
-- rows can outlive it: a template with live instances can't be deleted).
CREATE TABLE template_instances (
    id UUID PRIMARY KEY,
    org_id UUID NOT NULL,
    instance_key VARCHAR(128) NOT NULL,
    app_id UUID NOT NULL,
    server_id UUID NOT NULL,
    template_id UUID,
    revision INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    params TEXT NOT NULL,
    secret_names TEXT NOT NULL,
    hosts TEXT NOT NULL,
    request_hash VARCHAR(64) NOT NULL,
    state VARCHAR(16) NOT NULL,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    FOREIGN KEY (org_id) REFERENCES organizations (organization_id) ON DELETE CASCADE,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE,
    FOREIGN KEY (template_id) REFERENCES templates (id) ON DELETE SET NULL
);

-- One live key per org: an unlinked instance frees its key.
CREATE UNIQUE INDEX idx_template_instances_key ON template_instances (org_id, instance_key) WHERE state <> 'unlinked';
-- The live instances of a template (its counts, the delete guard).
CREATE INDEX idx_template_instances_template ON template_instances (template_id) WHERE state IN ('pending', 'created');
-- The org's live instances in list order.
CREATE INDEX idx_template_instances_live ON template_instances (org_id, created_at, id) WHERE state IN ('pending', 'created');
-- An app's instance (the save-app guard, delete-app's retire). Not partial:
-- its leading column also serves the servers foreign key's cascade.
CREATE INDEX idx_template_instances_app ON template_instances (server_id, app_id);
````

Create `internal/infra/db/migrations/sqlite/0016_templates_x_winterflow.sql`:

````sql
-- Templates use x-winterflow (sub-project 2): a revision's body is the
-- app's files plus summary, description, icon and color; parameters,
-- exposed ports and backups come from the compose file's x-winterflow
-- block. Nothing is migrated (no backward compatibility): the three tables
-- are recreated empty and the upgrade rollouts that named their rows go.
--
-- templates.source is org (an org's own, editable) or catalog (the same
-- for every org, read-only; org_id NULL, slug set, withdrawn_at once the
-- feed drops it). template_instances.name is the app name the create
-- asked for (renders use the app's current name once it has one) and hosts
-- the instance's ingress hostnames ([{host, expose}]), rendered into every
-- save.
--
-- A template_upgrade rollout's scope key becomes <org_id>:<template_id>
-- (73 characters): SQLite does not enforce VARCHAR lengths, so only the
-- Postgres migration widens rollouts.scope_key.

DROP TABLE template_instances;
DROP TABLE template_revisions;
DROP TABLE templates;
DELETE FROM rollouts WHERE kind = 'template_upgrade';

CREATE TABLE templates (
    id VARCHAR(36) PRIMARY KEY,
    source VARCHAR(16) NOT NULL,
    org_id VARCHAR(36),
    slug VARCHAR(128) NOT NULL DEFAULT '',
    name VARCHAR(255) NOT NULL,
    latest_revision INTEGER NOT NULL,
    withdrawn_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL,
    CHECK ((source = 'org' AND org_id IS NOT NULL AND slug = '' AND withdrawn_at IS NULL)
        OR (source = 'catalog' AND org_id IS NULL AND slug <> '')),
    FOREIGN KEY (org_id) REFERENCES organizations (organization_id) ON DELETE CASCADE
);

-- An org's template names are unique in the org; a catalog slug is unique.
CREATE UNIQUE INDEX idx_templates_org_name ON templates (org_id, name) WHERE source = 'org';
CREATE UNIQUE INDEX idx_templates_catalog_slug ON templates (slug) WHERE source = 'catalog';

CREATE TABLE template_revisions (
    template_id VARCHAR(36) NOT NULL,
    revision INTEGER NOT NULL,
    body TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    digest VARCHAR(80) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    PRIMARY KEY (template_id, revision),
    FOREIGN KEY (template_id) REFERENCES templates (id) ON DELETE CASCADE
);

-- template_id is NULL once the template is gone (only deleted and unlinked
-- rows can outlive it: a template with live instances can't be deleted).
CREATE TABLE template_instances (
    id VARCHAR(36) PRIMARY KEY,
    org_id VARCHAR(36) NOT NULL,
    instance_key VARCHAR(128) NOT NULL,
    app_id VARCHAR(36) NOT NULL,
    server_id VARCHAR(36) NOT NULL,
    template_id VARCHAR(36),
    revision INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    params TEXT NOT NULL,
    secret_names TEXT NOT NULL,
    hosts TEXT NOT NULL,
    request_hash VARCHAR(64) NOT NULL,
    state VARCHAR(16) NOT NULL,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    FOREIGN KEY (org_id) REFERENCES organizations (organization_id) ON DELETE CASCADE,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE,
    FOREIGN KEY (template_id) REFERENCES templates (id) ON DELETE SET NULL
);

-- One live key per org: an unlinked instance frees its key.
CREATE UNIQUE INDEX idx_template_instances_key ON template_instances (org_id, instance_key) WHERE state <> 'unlinked';
-- The live instances of a template (its counts, the delete guard).
CREATE INDEX idx_template_instances_template ON template_instances (template_id) WHERE state IN ('pending', 'created');
-- The org's live instances in list order.
CREATE INDEX idx_template_instances_live ON template_instances (org_id, created_at, id) WHERE state IN ('pending', 'created');
-- An app's instance (the save-app guard, delete-app's retire). Not partial:
-- its leading column also serves the servers foreign key's cascade.
CREATE INDEX idx_template_instances_app ON template_instances (server_id, app_id);
````

Modify `internal/infra/db/repository/template/pgsql.go` (`git apply` this diff):

````diff
diff --git a/internal/infra/db/repository/template/pgsql.go b/internal/infra/db/repository/template/pgsql.go
--- a/internal/infra/db/repository/template/pgsql.go
+++ b/internal/infra/db/repository/template/pgsql.go
@@ -28,8 +28,8 @@ func (pgsqlQuerier) GetTemplateIDByName(ctx context.Context, db backend.DBTX, p
 	return pgsqlgen.New(db).GetTemplateIDByName(ctx, pgsqlgen.GetTemplateIDByNameParams(p))
 }
 
-func (pgsqlQuerier) ListTemplates(ctx context.Context, db backend.DBTX, orgID string) ([]summaryRow, error) {
-	rows, err := pgsqlgen.New(db).ListTemplates(ctx, orgID)
+func (pgsqlQuerier) ListTemplates(ctx context.Context, db backend.DBTX, p listParams) ([]summaryRow, error) {
+	rows, err := pgsqlgen.New(db).ListTemplates(ctx, pgsqlgen.ListTemplatesParams(p))
 	if err != nil {
 		return nil, err
 	}
@@ -40,7 +40,7 @@ func (pgsqlQuerier) ListTemplates(ctx context.Context, db backend.DBTX, orgID st
 	return out, nil
 }
 
-func (pgsqlQuerier) ListTemplateNames(ctx context.Context, db backend.DBTX, orgID string) ([]nameRow, error) {
+func (pgsqlQuerier) ListTemplateNames(ctx context.Context, db backend.DBTX, orgID *string) ([]nameRow, error) {
 	rows, err := pgsqlgen.New(db).ListTemplateNames(ctx, orgID)
 	if err != nil {
 		return nil, err
````

Modify `internal/infra/db/repository/template/repo.go` (`git apply` this diff):

````diff
diff --git a/internal/infra/db/repository/template/repo.go b/internal/infra/db/repository/template/repo.go
--- a/internal/infra/db/repository/template/repo.go
+++ b/internal/infra/db/repository/template/repo.go
@@ -43,6 +43,7 @@ type (
 	forAppParams   = sqlitegen.GetLiveTemplateInstanceForAppParams
 	retireParams   = sqlitegen.RetireTemplateInstancesForAppParams
 	upgradeParams  = sqlitegen.ListUpgradableInstancesParams
+	listParams     = sqlitegen.ListTemplatesParams
 )
 
 type querier interface {
@@ -50,8 +51,8 @@ type querier interface {
 	BumpTemplateRevision(ctx context.Context, db backend.DBTX, p bumpParams) (int64, error)
 	GetTemplate(ctx context.Context, db backend.DBTX, p getParams) (templateRow, error)
 	GetTemplateIDByName(ctx context.Context, db backend.DBTX, p byNameParams) (string, error)
-	ListTemplates(ctx context.Context, db backend.DBTX, orgID string) ([]summaryRow, error)
-	ListTemplateNames(ctx context.Context, db backend.DBTX, orgID string) ([]nameRow, error)
+	ListTemplates(ctx context.Context, db backend.DBTX, p listParams) ([]summaryRow, error)
+	ListTemplateNames(ctx context.Context, db backend.DBTX, orgID *string) ([]nameRow, error)
 	DeleteTemplate(ctx context.Context, db backend.DBTX, p deleteParams) (int64, error)
 	DeleteTemplateRollouts(ctx context.Context, db backend.DBTX, templateID string) error
 	InsertTemplateRevision(ctx context.Context, db backend.DBTX, p insertRevision) error
@@ -90,12 +91,24 @@ func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error
 }
 
 // CreateTemplate inserts the template (latest_revision 1) and revision 1.
+// An org template keeps no slug or withdrawal; a catalog one no org.
 func (r *Repo) CreateTemplate(ctx context.Context, t model.Template, rev model.TemplateRevision) error {
 	now := types.NewDateTime()
+	p := insertParams{ID: t.ID, Source: t.Source, Name: t.Name, CreatedAt: now, CreatedBy: t.CreatedBy, UpdatedAt: now}
+	switch t.Source {
+	case model.TemplateSourceOrg:
+		p.OrgID = &t.OrgID
+	case model.TemplateSourceCatalog:
+		p.Slug = t.Slug
+		if t.WithdrawnAt != nil {
+			at := types.DateTime(t.WithdrawnAt.UTC())
+			p.WithdrawnAt = &at
+		}
+	default:
+		return fmt.Errorf("template %s: unknown source %q", t.ID, t.Source)
+	}
 	return r.tx.WithTx(ctx, func(ctx context.Context) error {
-		n, err := r.q.InsertTemplate(ctx, r.tx.Querier(ctx), insertParams{
-			ID: t.ID, OrgID: t.OrgID, Name: t.Name, CreatedAt: now, CreatedBy: t.CreatedBy, UpdatedAt: now,
-		})
+		n, err := r.q.InsertTemplate(ctx, r.tx.Querier(ctx), p)
 		if err != nil {
 			return err
 		}
@@ -113,15 +126,15 @@ func (r *Repo) AddRevision(ctx context.Context, orgID, templateID, name string,
 	now := types.NewDateTime()
 	next := base + 1
 	err := r.tx.WithTx(ctx, func(ctx context.Context) error {
-		cur, err := r.q.GetTemplate(ctx, r.tx.Querier(ctx), getParams{ID: templateID, OrgID: orgID})
-		if errors.Is(err, sql.ErrNoRows) {
+		cur, err := r.q.GetTemplate(ctx, r.tx.Querier(ctx), getParams{ID: templateID, OrgID: &orgID})
+		if errors.Is(err, sql.ErrNoRows) || (err == nil && cur.Source != model.TemplateSourceOrg) {
 			return model.ErrTemplateNotFound
 		}
 		if err != nil {
 			return err
 		}
 		if cur.Name != name {
-			switch other, err := r.q.GetTemplateIDByName(ctx, r.tx.Querier(ctx), byNameParams{OrgID: orgID, Name: name}); {
+			switch other, err := r.q.GetTemplateIDByName(ctx, r.tx.Querier(ctx), byNameParams{OrgID: &orgID, Name: name}); {
 			case err == nil && other != templateID:
 				return model.ErrTemplateNameTaken
 			case err != nil && !errors.Is(err, sql.ErrNoRows):
@@ -129,7 +142,7 @@ func (r *Repo) AddRevision(ctx context.Context, orgID, templateID, name string,
 			}
 		}
 		n, err := r.q.BumpTemplateRevision(ctx, r.tx.Querier(ctx), bumpParams{
-			Name: name, UpdatedAt: now, ID: templateID, OrgID: orgID, LatestRevision: base,
+			Name: name, UpdatedAt: now, ID: templateID, OrgID: &orgID, LatestRevision: base,
 		})
 		if err != nil {
 			return err
@@ -149,13 +162,13 @@ func (r *Repo) AddRevision(ctx context.Context, orgID, templateID, name string,
 func (r *Repo) insertRevision(ctx context.Context, rev model.TemplateRevision, now types.DateTime) error {
 	return r.q.InsertTemplateRevision(ctx, r.tx.Querier(ctx), insertRevision{
 		TemplateID: rev.TemplateID, Revision: rev.Revision, Body: string(rev.Body), Note: rev.Note,
-		CreatedAt: now, CreatedBy: rev.CreatedBy,
+		Digest: rev.Digest, CreatedAt: now, CreatedBy: rev.CreatedBy,
 	})
 }
 
-// GetTemplate returns the org's template.
+// GetTemplate returns the org's template or a catalog one.
 func (r *Repo) GetTemplate(ctx context.Context, orgID, templateID string) (model.Template, error) {
-	row, err := r.q.GetTemplate(ctx, r.tx.Querier(ctx), getParams{ID: templateID, OrgID: orgID})
+	row, err := r.q.GetTemplate(ctx, r.tx.Querier(ctx), getParams{ID: templateID, OrgID: &orgID})
 	if errors.Is(err, sql.ErrNoRows) {
 		return model.Template{}, model.ErrTemplateNotFound
 	}
@@ -165,9 +178,10 @@ func (r *Repo) GetTemplate(ctx context.Context, orgID, templateID string) (model
 	return hydrateTemplate(row), nil
 }
 
-// ListTemplates returns the org's templates by name with instance counts.
+// ListTemplates returns the org's templates, then the catalog's, each by
+// name, with the org's instance counts.
 func (r *Repo) ListTemplates(ctx context.Context, orgID string) ([]model.TemplateSummary, error) {
-	rows, err := r.q.ListTemplates(ctx, r.tx.Querier(ctx), orgID)
+	rows, err := r.q.ListTemplates(ctx, r.tx.Querier(ctx), listParams{OrgID: orgID, OrgID_2: &orgID})
 	if err != nil {
 		return nil, err
 	}
@@ -175,7 +189,8 @@ func (r *Repo) ListTemplates(ctx context.Context, orgID string) ([]model.Templat
 	for _, row := range rows {
 		out = append(out, model.TemplateSummary{
 			Template: hydrateTemplate(templateRow{
-				ID: row.ID, OrgID: row.OrgID, Name: row.Name, LatestRevision: row.LatestRevision,
+				ID: row.ID, Source: row.Source, OrgID: row.OrgID, Slug: row.Slug, Name: row.Name,
+				LatestRevision: row.LatestRevision, WithdrawnAt: row.WithdrawnAt,
 				CreatedAt: row.CreatedAt, CreatedBy: row.CreatedBy, UpdatedAt: row.UpdatedAt,
 			}),
 			Instances: row.Instances,
@@ -185,9 +200,9 @@ func (r *Repo) ListTemplates(ctx context.Context, orgID string) ([]model.Templat
 	return out, nil
 }
 
-// ListTemplateNames returns the org's template names by id.
+// ListTemplateNames returns the names of the templates the org may read.
 func (r *Repo) ListTemplateNames(ctx context.Context, orgID string) (map[string]string, error) {
-	rows, err := r.q.ListTemplateNames(ctx, r.tx.Querier(ctx), orgID)
+	rows, err := r.q.ListTemplateNames(ctx, r.tx.Querier(ctx), &orgID)
 	if err != nil {
 		return nil, err
 	}
@@ -209,7 +224,7 @@ func (r *Repo) GetRevision(ctx context.Context, templateID string, revision int6
 	}
 	return model.TemplateRevision{
 		TemplateID: row.TemplateID, Revision: row.Revision, Body: []byte(row.Body), Note: row.Note,
-		CreatedAt: row.CreatedAt.Time(), CreatedBy: row.CreatedBy,
+		Digest: row.Digest, CreatedAt: row.CreatedAt.Time(), CreatedBy: row.CreatedBy,
 	}, nil
 }
 
@@ -229,20 +244,21 @@ func (r *Repo) ListRevisions(ctx context.Context, templateID string) ([]model.Te
 	return out, nil
 }
 
-// DeleteTemplate deletes the template unless an instance is live: one
-// guarded statement, then a read to tell "in use" from "unknown". Its
-// upgrade rollouts go in the same transaction.
+// DeleteTemplate deletes the org's template unless an instance is live:
+// one guarded statement, then a read to tell "in use" from "unknown" (a
+// catalog template is unknown here). The org's upgrade rollouts of it go
+// in the same transaction.
 func (r *Repo) DeleteTemplate(ctx context.Context, orgID, templateID string) error {
 	return r.tx.WithTx(ctx, func(ctx context.Context) error {
-		n, err := r.q.DeleteTemplate(ctx, r.tx.Querier(ctx), deleteParams{ID: templateID, OrgID: orgID})
+		n, err := r.q.DeleteTemplate(ctx, r.tx.Querier(ctx), deleteParams{ID: templateID, OrgID: &orgID})
 		if err != nil {
 			return err
 		}
 		if n > 0 {
-			return r.q.DeleteTemplateRollouts(ctx, r.tx.Querier(ctx), templateID)
+			return r.q.DeleteTemplateRollouts(ctx, r.tx.Querier(ctx), model.UpgradeScopeKey(orgID, templateID))
 		}
-		_, err = r.q.GetTemplate(ctx, r.tx.Querier(ctx), getParams{ID: templateID, OrgID: orgID})
-		if errors.Is(err, sql.ErrNoRows) {
+		cur, err := r.q.GetTemplate(ctx, r.tx.Querier(ctx), getParams{ID: templateID, OrgID: &orgID})
+		if errors.Is(err, sql.ErrNoRows) || (err == nil && cur.Source != model.TemplateSourceOrg) {
 			return model.ErrTemplateNotFound
 		}
 		if err != nil {
@@ -255,7 +271,7 @@ func (r *Repo) DeleteTemplate(ctx context.Context, orgID, templateID string) err
 // ClaimInstance inserts inst as pending, or returns the live instance that
 // holds its key (the partial unique index makes the insert a no-op).
 func (r *Repo) ClaimInstance(ctx context.Context, inst model.Instance) (model.Instance, bool, error) {
-	params, names, err := encodeValues(inst.Params, inst.SecretNames)
+	params, names, hosts, err := encodeValues(inst.Params, inst.SecretNames, inst.Hosts)
 	if err != nil {
 		return model.Instance{}, false, err
 	}
@@ -267,8 +283,8 @@ func (r *Repo) ClaimInstance(ctx context.Context, inst model.Instance) (model.In
 	err = r.tx.WithTx(ctx, func(ctx context.Context) error {
 		p := claimParams{
 			ID: inst.ID, OrgID: inst.OrgID, InstanceKey: inst.Key, AppID: inst.AppID, ServerID: inst.ServerID,
-			TemplateID: util.RefString(inst.TemplateID), Revision: inst.Revision, Params: params, SecretNames: names,
-			RequestHash: inst.RequestHash, CreatedAt: now, UpdatedAt: now,
+			TemplateID: util.RefString(inst.TemplateID), Revision: inst.Revision, Name: inst.Name, Params: params,
+			SecretNames: names, Hosts: hosts, RequestHash: inst.RequestHash, CreatedAt: now, UpdatedAt: now,
 		}
 		n, err := r.q.ClaimTemplateInstance(ctx, r.tx.Querier(ctx), p)
 		if err != nil {
@@ -358,13 +374,13 @@ func hydrateInstances(rows []instanceRow, err error) ([]model.Instance, error) {
 }
 
 // MarkSaved records a confirmed save.
-func (r *Repo) MarkSaved(ctx context.Context, id string, revision int64, params map[string]string, secretNames []string) error {
-	p, names, err := encodeValues(params, secretNames)
+func (r *Repo) MarkSaved(ctx context.Context, id string, revision int64, params map[string]string, secretNames []string, hosts []model.InstanceHost) error {
+	p, names, h, err := encodeValues(params, secretNames, hosts)
 	if err != nil {
 		return err
 	}
 	return r.q.MarkTemplateInstanceSaved(ctx, r.tx.Querier(ctx), savedParams{
-		Revision: revision, Params: p, SecretNames: names, UpdatedAt: types.NewDateTime(), ID: id,
+		Revision: revision, Params: p, SecretNames: names, Hosts: h, UpdatedAt: types.NewDateTime(), ID: id,
 	})
 }
 
@@ -382,36 +398,46 @@ func (r *Repo) SetError(ctx context.Context, id, msg string) error {
 	})
 }
 
-// encodeValues serializes the plain values (an object; nil is {}) and the
-// secret names (sorted; nil is []).
-func encodeValues(params map[string]string, secretNames []string) (string, string, error) {
+// encodeValues serializes the plain values (an object; nil is {}), the
+// secret names (sorted; nil is []) and the hosts (in order; nil is []).
+func encodeValues(params map[string]string, secretNames []string, hosts []model.InstanceHost) (string, string, string, error) {
 	if params == nil {
 		params = map[string]string{}
 	}
 	p, err := json.Marshal(params)
 	if err != nil {
-		return "", "", err
+		return "", "", "", err
 	}
 	names := append([]string{}, secretNames...)
 	sort.Strings(names)
 	n, err := json.Marshal(names)
 	if err != nil {
-		return "", "", err
+		return "", "", "", err
 	}
-	return string(p), string(n), nil
+	h, err := json.Marshal(append([]model.InstanceHost{}, hosts...))
+	if err != nil {
+		return "", "", "", err
+	}
+	return string(p), string(n), string(h), nil
 }
 
 func hydrateTemplate(row templateRow) model.Template {
-	return model.Template{
-		ID: row.ID, OrgID: row.OrgID, Name: row.Name, LatestRevision: row.LatestRevision,
-		CreatedAt: row.CreatedAt.Time(), CreatedBy: row.CreatedBy, UpdatedAt: row.UpdatedAt.Time(),
+	t := model.Template{
+		ID: row.ID, Source: row.Source, OrgID: util.DerefString(row.OrgID), Slug: row.Slug, Name: row.Name,
+		LatestRevision: row.LatestRevision, CreatedAt: row.CreatedAt.Time(), CreatedBy: row.CreatedBy,
+		UpdatedAt: row.UpdatedAt.Time(),
+	}
+	if row.WithdrawnAt != nil {
+		at := row.WithdrawnAt.Time()
+		t.WithdrawnAt = &at
 	}
+	return t
 }
 
 func hydrateInstance(row instanceRow) (model.Instance, error) {
 	inst := model.Instance{
 		ID: row.ID, OrgID: row.OrgID, Key: row.InstanceKey, AppID: row.AppID, ServerID: row.ServerID,
-		TemplateID: util.DerefString(row.TemplateID), Revision: row.Revision, RequestHash: row.RequestHash,
+		TemplateID: util.DerefString(row.TemplateID), Revision: row.Revision, Name: row.Name, RequestHash: row.RequestHash,
 		State: model.InstanceState(row.State), LastError: row.LastError,
 		CreatedAt: row.CreatedAt.Time(), UpdatedAt: row.UpdatedAt.Time(),
 	}
@@ -421,5 +447,8 @@ func hydrateInstance(row instanceRow) (model.Instance, error) {
 	if err := json.Unmarshal([]byte(row.SecretNames), &inst.SecretNames); err != nil {
 		return model.Instance{}, fmt.Errorf("instance %s: secret_names: %w", row.ID, err)
 	}
+	if err := json.Unmarshal([]byte(row.Hosts), &inst.Hosts); err != nil {
+		return model.Instance{}, fmt.Errorf("instance %s: hosts: %w", row.ID, err)
+	}
 	return inst, nil
 }
````

Modify `internal/infra/db/repository/template/sqlite.go` (`git apply` this diff):

````diff
diff --git a/internal/infra/db/repository/template/sqlite.go b/internal/infra/db/repository/template/sqlite.go
--- a/internal/infra/db/repository/template/sqlite.go
+++ b/internal/infra/db/repository/template/sqlite.go
@@ -27,11 +27,11 @@ func (sqliteQuerier) GetTemplateIDByName(ctx context.Context, db backend.DBTX, p
 	return sqlitegen.New(db).GetTemplateIDByName(ctx, p)
 }
 
-func (sqliteQuerier) ListTemplates(ctx context.Context, db backend.DBTX, orgID string) ([]summaryRow, error) {
-	return sqlitegen.New(db).ListTemplates(ctx, orgID)
+func (sqliteQuerier) ListTemplates(ctx context.Context, db backend.DBTX, p listParams) ([]summaryRow, error) {
+	return sqlitegen.New(db).ListTemplates(ctx, p)
 }
 
-func (sqliteQuerier) ListTemplateNames(ctx context.Context, db backend.DBTX, orgID string) ([]nameRow, error) {
+func (sqliteQuerier) ListTemplateNames(ctx context.Context, db backend.DBTX, orgID *string) ([]nameRow, error) {
 	return sqlitegen.New(db).ListTemplateNames(ctx, orgID)
 }
 
````

Modify `internal/infra/db/sqlc/query/pgsql/templates.sql` (`git apply` this diff):

````diff
diff --git a/internal/infra/db/sqlc/query/pgsql/templates.sql b/internal/infra/db/sqlc/query/pgsql/templates.sql
--- a/internal/infra/db/sqlc/query/pgsql/templates.sql
+++ b/internal/infra/db/sqlc/query/pgsql/templates.sql
@@ -1,54 +1,64 @@
--- Templates, their immutable revisions and their instances.
+-- Templates, their immutable revisions and their instances. A template is
+-- an org's own (source org, org_id set) or the catalog's (source catalog,
+-- org_id NULL): an org reads its own and every catalog template, and
+-- changes only its own.
 
 -- name: InsertTemplate :execrows
--- A taken name inserts nothing (0 rows): the caller answers 400.
-INSERT INTO templates (id, org_id, name, latest_revision, created_at, created_by, updated_at)
-VALUES ($1, $2, $3, 1, $4, $5, $6)
+-- A taken org name or catalog slug inserts nothing (0 rows): the caller
+-- answers 400.
+INSERT INTO templates (id, source, org_id, slug, name, latest_revision, withdrawn_at, created_at, created_by, updated_at)
+VALUES ($1, $2, $3, $4, $5, 1, $6, $7, $8, $9)
 ON CONFLICT DO NOTHING;
 
 -- name: BumpTemplateRevision :execrows
--- Optimistic: 0 rows when latest_revision moved past the caller's base.
+-- An org template's next revision. Optimistic: 0 rows when latest_revision
+-- moved past the caller's base.
 UPDATE templates
 SET latest_revision = latest_revision + 1, name = $1, updated_at = $2
-WHERE id = $3 AND org_id = $4 AND latest_revision = $5;
+WHERE id = $3 AND source = 'org' AND org_id = $4 AND latest_revision = $5;
 
 -- name: GetTemplate :one
-SELECT * FROM templates WHERE id = $1 AND org_id = $2;
+-- A template the org may read: its own or a catalog one.
+SELECT * FROM templates WHERE id = $1 AND (org_id = $2 OR source = 'catalog');
 
 -- name: GetTemplateIDByName :one
-SELECT id FROM templates WHERE org_id = $1 AND name = $2;
+SELECT id FROM templates WHERE source = 'org' AND org_id = $1 AND name = $2;
 
 -- name: ListTemplates :many
--- One pass over the live instances: instances counts them, behind those on
--- a revision older than the latest.
-SELECT t.id, t.org_id, t.name, t.latest_revision, t.created_at, t.created_by, t.updated_at,
+-- The org's templates, then the catalog's (a withdrawn one only while the
+-- org has live instances of it), each by name. One pass over the org's
+-- live instances: instances counts them, behind those on a revision older
+-- than the latest.
+SELECT t.id, t.source, t.org_id, t.slug, t.name, t.latest_revision, t.withdrawn_at, t.created_at, t.created_by, t.updated_at,
     COUNT(i.id) AS instances,
     CAST(COALESCE(SUM(CASE WHEN i.revision < t.latest_revision THEN 1 ELSE 0 END), 0) AS BIGINT) AS behind
 FROM templates AS t
-LEFT JOIN template_instances AS i ON i.template_id = t.id AND i.state IN ('pending', 'created')
-WHERE t.org_id = $1
-GROUP BY t.id, t.org_id, t.name, t.latest_revision, t.created_at, t.created_by, t.updated_at
-ORDER BY t.name ASC;
+LEFT JOIN template_instances AS i ON i.template_id = t.id AND i.org_id = $1 AND i.state IN ('pending', 'created')
+WHERE t.org_id = $2 OR t.source = 'catalog'
+GROUP BY t.id, t.source, t.org_id, t.slug, t.name, t.latest_revision, t.withdrawn_at, t.created_at, t.created_by, t.updated_at
+HAVING t.source = 'org' OR t.withdrawn_at IS NULL OR COUNT(i.id) > 0
+ORDER BY t.source DESC, t.name ASC, t.id ASC;
 
 -- name: ListTemplateNames :many
--- Names only (the instance list shows them).
-SELECT id, name FROM templates WHERE org_id = $1;
+-- The names of the templates the org may read (the instance list shows
+-- them).
+SELECT id, name FROM templates WHERE org_id = $1 OR source = 'catalog';
 
 -- name: DeleteTemplate :execrows
--- Refused (0 rows) while an instance is pending or created.
+-- An org template; refused (0 rows) while an instance is pending or created.
 DELETE FROM templates
-WHERE templates.id = $1 AND templates.org_id = $2
+WHERE templates.id = $1 AND templates.source = 'org' AND templates.org_id = $2
   AND NOT EXISTS (
     SELECT 1 FROM template_instances AS i
     WHERE i.template_id = templates.id AND i.state IN ('pending', 'created'));
 
 -- name: DeleteTemplateRollouts :exec
--- A deleted template's upgrade rollouts (scope key: the template id).
+-- A deleted template's upgrade rollouts (scope key: <org_id>:<template_id>).
 DELETE FROM rollouts WHERE kind = 'template_upgrade' AND scope_key = $1;
 
 -- name: InsertTemplateRevision :exec
-INSERT INTO template_revisions (template_id, revision, body, note, created_at, created_by)
-VALUES ($1, $2, $3, $4, $5, $6);
+INSERT INTO template_revisions (template_id, revision, body, note, digest, created_at, created_by)
+VALUES ($1, $2, $3, $4, $5, $6, $7);
 
 -- name: GetTemplateRevision :one
 SELECT * FROM template_revisions WHERE template_id = $1 AND revision = $2;
@@ -62,8 +72,8 @@ ORDER BY revision DESC;
 -- name: ClaimTemplateInstance :execrows
 -- A live key (the partial unique index) inserts nothing (0 rows).
 INSERT INTO template_instances (id, org_id, instance_key, app_id, server_id, template_id, revision,
-    params, secret_names, request_hash, state, last_error, created_at, updated_at)
-VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'pending', '', $11, $12)
+    name, params, secret_names, hosts, request_hash, state, last_error, created_at, updated_at)
+VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'pending', '', $13, $14)
 ON CONFLICT DO NOTHING;
 
 -- name: GetLiveTemplateInstance :one
@@ -86,8 +96,8 @@ ORDER BY i.created_at ASC, i.id ASC;
 -- name: MarkTemplateInstanceSaved :exec
 -- A save the agent confirmed; a deleted or unlinked row stays as it is.
 UPDATE template_instances
-SET revision = $1, params = $2, secret_names = $3, state = 'created', last_error = '', updated_at = $4
-WHERE id = $5 AND state IN ('pending', 'created');
+SET revision = $1, params = $2, secret_names = $3, hosts = $4, state = 'created', last_error = '', updated_at = $5
+WHERE id = $6 AND state IN ('pending', 'created');
 
 -- name: SetTemplateInstanceState :exec
 UPDATE template_instances
````

Modify `internal/infra/db/sqlc/query/sqlite/templates.sql` (`git apply` this diff):

````diff
diff --git a/internal/infra/db/sqlc/query/sqlite/templates.sql b/internal/infra/db/sqlc/query/sqlite/templates.sql
--- a/internal/infra/db/sqlc/query/sqlite/templates.sql
+++ b/internal/infra/db/sqlc/query/sqlite/templates.sql
@@ -1,54 +1,64 @@
--- Templates, their immutable revisions and their instances.
+-- Templates, their immutable revisions and their instances. A template is
+-- an org's own (source org, org_id set) or the catalog's (source catalog,
+-- org_id NULL): an org reads its own and every catalog template, and
+-- changes only its own.
 
 -- name: InsertTemplate :execrows
--- A taken name inserts nothing (0 rows): the caller answers 400.
-INSERT INTO templates (id, org_id, name, latest_revision, created_at, created_by, updated_at)
-VALUES (?, ?, ?, 1, ?, ?, ?)
+-- A taken org name or catalog slug inserts nothing (0 rows): the caller
+-- answers 400.
+INSERT INTO templates (id, source, org_id, slug, name, latest_revision, withdrawn_at, created_at, created_by, updated_at)
+VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?, ?)
 ON CONFLICT DO NOTHING;
 
 -- name: BumpTemplateRevision :execrows
--- Optimistic: 0 rows when latest_revision moved past the caller's base.
+-- An org template's next revision. Optimistic: 0 rows when latest_revision
+-- moved past the caller's base.
 UPDATE templates
 SET latest_revision = latest_revision + 1, name = ?, updated_at = ?
-WHERE id = ? AND org_id = ? AND latest_revision = ?;
+WHERE id = ? AND source = 'org' AND org_id = ? AND latest_revision = ?;
 
 -- name: GetTemplate :one
-SELECT * FROM templates WHERE id = ? AND org_id = ?;
+-- A template the org may read: its own or a catalog one.
+SELECT * FROM templates WHERE id = ? AND (org_id = ? OR source = 'catalog');
 
 -- name: GetTemplateIDByName :one
-SELECT id FROM templates WHERE org_id = ? AND name = ?;
+SELECT id FROM templates WHERE source = 'org' AND org_id = ? AND name = ?;
 
 -- name: ListTemplates :many
--- One pass over the live instances: instances counts them, behind those on
--- a revision older than the latest.
-SELECT t.id, t.org_id, t.name, t.latest_revision, t.created_at, t.created_by, t.updated_at,
+-- The org's templates, then the catalog's (a withdrawn one only while the
+-- org has live instances of it), each by name. One pass over the org's
+-- live instances: instances counts them, behind those on a revision older
+-- than the latest.
+SELECT t.id, t.source, t.org_id, t.slug, t.name, t.latest_revision, t.withdrawn_at, t.created_at, t.created_by, t.updated_at,
     COUNT(i.id) AS instances,
     CAST(COALESCE(SUM(CASE WHEN i.revision < t.latest_revision THEN 1 ELSE 0 END), 0) AS INTEGER) AS behind
 FROM templates AS t
-LEFT JOIN template_instances AS i ON i.template_id = t.id AND i.state IN ('pending', 'created')
-WHERE t.org_id = ?
-GROUP BY t.id, t.org_id, t.name, t.latest_revision, t.created_at, t.created_by, t.updated_at
-ORDER BY t.name ASC;
+LEFT JOIN template_instances AS i ON i.template_id = t.id AND i.org_id = ? AND i.state IN ('pending', 'created')
+WHERE t.org_id = ? OR t.source = 'catalog'
+GROUP BY t.id, t.source, t.org_id, t.slug, t.name, t.latest_revision, t.withdrawn_at, t.created_at, t.created_by, t.updated_at
+HAVING t.source = 'org' OR t.withdrawn_at IS NULL OR COUNT(i.id) > 0
+ORDER BY t.source DESC, t.name ASC, t.id ASC;
 
 -- name: ListTemplateNames :many
--- Names only (the instance list shows them).
-SELECT id, name FROM templates WHERE org_id = ?;
+-- The names of the templates the org may read (the instance list shows
+-- them).
+SELECT id, name FROM templates WHERE org_id = ? OR source = 'catalog';
 
 -- name: DeleteTemplate :execrows
--- Refused (0 rows) while an instance is pending or created.
+-- An org template; refused (0 rows) while an instance is pending or created.
 DELETE FROM templates
-WHERE templates.id = ? AND templates.org_id = ?
+WHERE templates.id = ? AND templates.source = 'org' AND templates.org_id = ?
   AND NOT EXISTS (
     SELECT 1 FROM template_instances AS i
     WHERE i.template_id = templates.id AND i.state IN ('pending', 'created'));
 
 -- name: DeleteTemplateRollouts :exec
--- A deleted template's upgrade rollouts (scope key: the template id).
+-- A deleted template's upgrade rollouts (scope key: <org_id>:<template_id>).
 DELETE FROM rollouts WHERE kind = 'template_upgrade' AND scope_key = ?;
 
 -- name: InsertTemplateRevision :exec
-INSERT INTO template_revisions (template_id, revision, body, note, created_at, created_by)
-VALUES (?, ?, ?, ?, ?, ?);
+INSERT INTO template_revisions (template_id, revision, body, note, digest, created_at, created_by)
+VALUES (?, ?, ?, ?, ?, ?, ?);
 
 -- name: GetTemplateRevision :one
 SELECT * FROM template_revisions WHERE template_id = ? AND revision = ?;
@@ -62,8 +72,8 @@ ORDER BY revision DESC;
 -- name: ClaimTemplateInstance :execrows
 -- A live key (the partial unique index) inserts nothing (0 rows).
 INSERT INTO template_instances (id, org_id, instance_key, app_id, server_id, template_id, revision,
-    params, secret_names, request_hash, state, last_error, created_at, updated_at)
-VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', '', ?, ?)
+    name, params, secret_names, hosts, request_hash, state, last_error, created_at, updated_at)
+VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', '', ?, ?)
 ON CONFLICT DO NOTHING;
 
 -- name: GetLiveTemplateInstance :one
@@ -86,7 +96,7 @@ ORDER BY i.created_at ASC, i.id ASC;
 -- name: MarkTemplateInstanceSaved :exec
 -- A save the agent confirmed; a deleted or unlinked row stays as it is.
 UPDATE template_instances
-SET revision = ?, params = ?, secret_names = ?, state = 'created', last_error = '', updated_at = ?
+SET revision = ?, params = ?, secret_names = ?, hosts = ?, state = 'created', last_error = '', updated_at = ?
 WHERE id = ? AND state IN ('pending', 'created');
 
 -- name: SetTemplateInstanceState :exec
````

- [ ] **Step 4: Regenerate**

```bash
make sqlc
```

- [ ] **Step 5: Run them and see them pass**

```bash
go test ./internal/infra/db/... ./internal/domain/... ./internal/app/web/...
go vet ./...
```

Expected: PASS — every package passes; the templates usecase only had to set `Source` on create and pass `nil` hosts to `MarkSaved`, so nothing else changes behaviour yet.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat(db): templates hold org and catalog templates (0016_templates_x_winterflow)" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 2: The template body is files plus metadata; x-winterflow declares the rest

`internal/domain/template` is rewritten around the shared x-winterflow parser: a body is `files` + `summary`/`description`/`icon`/`color`; variables, exposed ports and backups come from `template.Parse` (`xwinterflow.ModeTemplate`). The old `Parameters`, `AppName`, `Ingress`, `Backups`, `Port`, `IngressRow`, `BackupDefault`, `NewBackupDefaults` and `Marker.BackupDefaults` are deleted.

**Files:**
- Modify: `internal/domain/template/body.go`
- Create: `internal/domain/template/detect.go`
- Modify: `internal/domain/template/hash.go`
- Delete: `internal/domain/template/ingress.go`
- Delete: `internal/domain/template/ingress_test.go`
- Create: `internal/domain/template/instance.go`
- Create: `internal/domain/template/instance_test.go`
- Modify: `internal/domain/template/render.go`
- Modify: `internal/domain/template/render_test.go`
- Create: `internal/domain/template/template_test.go`
- Modify: `internal/domain/template/validate.go`
- Modify: `internal/domain/template/validate_test.go`
- Modify: `internal/domain/template/values.go`
- Create: `internal/domain/template/values_test.go`

**Interfaces:**
- Consumes: `xwinterflow.Parse`, `xwinterflow.Spec` (`Variable`, `HTTPTargets`, `CheckValues`, `Expose.ID/Target`, `Compose.Recipe`, `Compose.Unparsed`, `Warnings`), `recipe.Validate`, `varref.Undefaulted`, `crypto.CheckCiphertext`, `model.Ingress.Validate`, `model.ValidateStorageName`, `model.DetectComposeFiles`.
- Produces (package `template`):
  - `type Body struct { Files map[string]string; Summary, Description, Icon, Color string }` (JSON `files`, `summary`, `description`, `icon`, `color`); `MaxBodyBytes`; `ErrInvalid`; `Invalid(field, format, args...)`; `Encode(Body) ([]byte, error)`.
  - `Parse(Body) (xwinterflow.Spec, error)`; `Validate(Body) (xwinterflow.Spec, error)`; `ValidateName(string) error`; `Warnings(Body, xwinterflow.Spec, shared func(string) bool) []string`.
  - `type Detected struct { Variables []xwinterflow.DeclaredVariable; Expose []DetectedExpose; Backups []DetectedBackup; Warnings []string }`, `DetectedExpose{ID, Target, Container, Port, Protocol}`, `DetectedBackup{Name, Type}`, `Detect(xwinterflow.Spec, []string) Detected` (lists never nil).
  - `Given{Values, Secrets}`, `Stored{Values, SecretNames}`, `Values{Plain, Secrets, Stored}`, `Resolve(xwinterflow.Spec, Stored, Given) (Values, error)`.
  - `CheckName(string) error`, `CheckHosts(xwinterflow.Spec, []model.InstanceHost) error`, `BackupChoice{Enabled, Storage}` (JSON `enabled`, `storage,omitempty`), `CreateBackups(xwinterflow.Spec, map[string]BackupChoice) ([]recipe.BackupConfig, error)` (never nil).
  - `Marker{TemplateID, TemplateName, Revision, Key}`, `Input{AppID, Name, Values, Hosts, Marker, Backups}`, `Render(Body, Input) (model.App, command.AppPayload, error)` (config always has `ingress`, never `ports`).
  - `HashInput{ServerID, TemplateID, Revision, Name, Values, SecretNames, Hosts, Backups}`, `RequestHash(HashInput) string`.

- [ ] **Step 1: Write the failing tests**

Delete:

```bash
git rm -q internal/domain/template/ingress_test.go
```

Create `internal/domain/template/instance_test.go`:

````go
package template

import (
	"reflect"
	"strings"
	"testing"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/model"
)

func TestCheckName(t *testing.T) {
	if err := CheckName("My notes"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "  ", " padded", strings.Repeat("n", 101)} {
		wantInvalid(t, CheckName(bad), "name")
	}
}

func TestCheckHosts(t *testing.T) {
	s := spec(t)
	if err := CheckHosts(s, []model.InstanceHost{{Host: "notes.example.test", Expose: "app:8080"}}); err != nil {
		t.Fatal(err)
	}
	if err := CheckHosts(s, nil); err != nil {
		t.Fatal(err)
	}
	wantInvalid(t, CheckHosts(s, []model.InstanceHost{{Host: "db.example.test", Expose: "db:5432"}}), "hosts[0].expose")
	wantInvalid(t, CheckHosts(s, []model.InstanceHost{{Host: "a.test", Expose: "app:9090"}}), `"app:9090" is not an http expose entry`)
	wantInvalid(t, CheckHosts(s, []model.InstanceHost{{Host: "Not A Host", Expose: "app:8080"}}), "hosts")
	twice := []model.InstanceHost{{Host: "a.test", Expose: "app:8080"}, {Host: "a.test", Expose: "app:8080"}}
	wantInvalid(t, CheckHosts(s, twice), "hosts")
	many := make([]model.InstanceHost, 51)
	wantInvalid(t, CheckHosts(s, many), "at most 50")
}

func TestCreateBackups(t *testing.T) {
	s := spec(t)
	got, err := CreateBackups(s, map[string]BackupChoice{"db": {Enabled: true, Storage: "nas"}})
	if err != nil || !reflect.DeepEqual(got, []recipe.BackupConfig{{Name: "db", Enabled: true, Storage: "nas"}}) {
		t.Fatalf("backups = %+v, %v", got, err)
	}
	if got, err := CreateBackups(s, nil); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("no choice = %#v, %v", got, err)
	}
	_, err = CreateBackups(s, map[string]BackupChoice{"media": {Enabled: true, Storage: "nas"}})
	wantInvalid(t, err, "backups.media")
	_, err = CreateBackups(s, map[string]BackupChoice{"db": {Enabled: true}})
	wantInvalid(t, err, "backups.db.storage: an enabled backup needs a storage")
	_, err = CreateBackups(s, map[string]BackupChoice{"db": {Storage: "../x"}})
	wantInvalid(t, err, "backups.db.storage")
}
````

Replace the whole of `internal/domain/template/render_test.go` with:

````go
package template

import (
	"encoding/json"
	"testing"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/pkg/crypto/cryptotest"
)

func TestRenderIsWhatTheEditorWouldSend(t *testing.T) {
	ct := cryptotest.Ciphertext(t, "s3cret")
	app, p, err := Render(notes(), Input{
		AppID: "app-1",
		Name:  "My notes",
		Values: Values{
			Plain:   map[string]string{"BASE_URL": "https://notes.example.test"},
			Secrets: map[string]string{"SESSION_SECRET": ct, "DB_PASSWORD": command.EncryptedPlaceholder},
		},
		Hosts:   []model.InstanceHost{{Host: "notes.example.test", Expose: "app:8080"}},
		Marker:  &Marker{TemplateID: "tpl-1", TemplateName: "notes", Revision: 2, Key: "k"},
		Backups: []recipe.BackupConfig{{Name: "db", Enabled: true, Storage: "nas"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if app.ID != "app-1" || app.Name != "My notes" || app.Icon != "note" || app.Color != "#112233" || app.TemplateID != "tpl-1" {
		t.Fatalf("app = %+v", app)
	}
	if len(p.Files) != 1 || p.Files[0].Name != "compose.yaml" || string(p.Files[0].Content) != notesCompose {
		t.Fatalf("files = %+v", p.Files)
	}
	want := []command.ContentItem{
		{Name: "BASE_URL", Content: []byte("https://notes.example.test")},
		{Name: "DB_PASSWORD", Content: []byte(command.EncryptedPlaceholder), Encrypted: true},
		{Name: "SESSION_SECRET", Content: []byte(ct), Encrypted: true},
	}
	if len(p.Variables) != len(want) {
		t.Fatalf("variables = %+v", p.Variables)
	}
	for i, v := range want {
		if p.Variables[i].Name != v.Name || string(p.Variables[i].Content) != string(v.Content) || p.Variables[i].Encrypted != v.Encrypted {
			t.Fatalf("variable %d = %+v, want %+v", i, p.Variables[i], v)
		}
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(p.Config, &cfg); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"name":        `"My notes"`,
		"template_id": `"tpl-1"`,
		"ingress":     `{"domains":[{"domain":"notes.example.test","expose":"app:8080","ssl":false}],"redirects":[]}`,
		"instance":    `{"template_id":"tpl-1","template_name":"notes","revision":2,"key":"k"}`,
		"backups":     `[{"name":"db","enabled":true,"storage":"nas"}]`,
		"variables":   `[{"name":"BASE_URL","is_encrypted":false},{"name":"DB_PASSWORD","is_encrypted":true},{"name":"SESSION_SECRET","is_encrypted":true}]`,
	} {
		if string(cfg[key]) != want {
			t.Fatalf("config %s = %s, want %s", key, cfg[key], want)
		}
	}
	if _, ok := cfg["ports"]; ok {
		t.Fatal("a render sent ports: the agent would drop the app's pins")
	}
}

// Without a marker (unlink) or backups (an update) the config says
// nothing of them; without hosts the ingress is empty, not absent.
func TestRenderWithoutMarkerOrBackups(t *testing.T) {
	_, p, err := Render(notes(), Input{AppID: "a", Name: "notes"})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]json.RawMessage
	_ = json.Unmarshal(p.Config, &cfg)
	if _, ok := cfg["instance"]; ok {
		t.Fatalf("config = %s", p.Config)
	}
	if _, ok := cfg["backups"]; ok {
		t.Fatalf("config = %s", p.Config)
	}
	if _, ok := cfg["template_id"]; ok || string(cfg["ingress"]) != `{"domains":[],"redirects":[]}` {
		t.Fatalf("config = %s", p.Config)
	}
	_, _, err = Render(notes(), Input{AppID: "a", Name: ""})
	wantInvalid(t, err, "name")
}

func TestRequestHash(t *testing.T) {
	in := HashInput{ServerID: "s", TemplateID: "t", Name: "n", Values: map[string]string{"A": "1"}, SecretNames: []string{"B", "A"},
		Hosts: []model.InstanceHost{{Host: "a.test", Expose: "app:80"}}, Backups: map[string]BackupChoice{"db": {Enabled: true, Storage: "nas"}}}
	same := in
	same.SecretNames = []string{"A", "B"}
	if RequestHash(in) != RequestHash(same) {
		t.Fatal("the order of secret names changed the hash")
	}
	for name, edit := range map[string]func(*HashInput){
		"name":     func(h *HashInput) { h.Name = "m" },
		"value":    func(h *HashInput) { h.Values = map[string]string{"A": "2"} },
		"hosts":    func(h *HashInput) { h.Hosts = nil },
		"backups":  func(h *HashInput) { h.Backups = map[string]BackupChoice{"db": {Storage: "nas"}} },
		"revision": func(h *HashInput) { h.Revision = 2 },
	} {
		other := in
		edit(&other)
		if RequestHash(other) == RequestHash(in) {
			t.Fatalf("a different %s hashed the same", name)
		}
	}
	if RequestHash(HashInput{}) != RequestHash(HashInput{Values: map[string]string{}, Hosts: []model.InstanceHost{}}) {
		t.Fatal("nil and empty hash differently")
	}
}
````

Create `internal/domain/template/template_test.go`:

````go
package template

import (
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/compose/xwinterflow"
	"winterflow/internal/domain/model"
)

// notesCompose is the spec's example: a required URL with a pattern, a
// required secret, an optional language with a default, a secret only the
// overrides use, an http and a tcp entry, and a files backup.
const notesCompose = `services:
  app:
    image: ghcr.io/example/notes:${NOTES_TAG:-latest}
    environment:
      # Public URL the browser uses
      BASE_URL: "http://localhost:8080"
      SESSION_SECRET: "changeme"
      LANGUAGE: "en"
      SMTP_HOST: ${SMTP_HOST}
    volumes:
      - data:/var/lib/notes
  db:
    image: postgres:16
    environment:
      POSTGRES_PASSWORD: notes
    volumes:
      - pg:/var/lib/postgresql/data
volumes:
  data:
  pg:
x-winterflow:
  expose:
    - { container: app, port: 8080 }
    - { container: db, port: 5432, protocol: tcp }
  variables:
    BASE_URL: { required: true, pattern: "^https?://.+" }
    SESSION_SECRET: { required: true, encrypted: true }
    LANGUAGE: { pattern: "^[a-z]{2}$" }
    DB_PASSWORD: { encrypted: true }
  overrides:
    services:
      db:
        environment:
          POSTGRES_PASSWORD: ${DB_PASSWORD}
  backups:
    - { name: db, type: files, volume: pg }
`

// notes is a body with notesCompose.
func notes() Body {
	return Body{Files: map[string]string{"compose.yaml": notesCompose}, Summary: "Notes", Icon: "note", Color: "#112233"}
}

// spec is notes' parsed block.
func spec(t *testing.T) xwinterflow.Spec {
	t.Helper()
	s, err := Validate(notes())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func wantInvalid(t *testing.T, err error, field string) {
	t.Helper()
	if !errors.Is(err, model.ErrRejected) {
		t.Fatalf("want a 400 naming %s, got %v", field, err)
	}
	if !strings.Contains(err.Error(), field) {
		t.Fatalf("error %q does not name %s", err, field)
	}
}
````

Replace the whole of `internal/domain/template/validate_test.go` with:

````go
package template

import (
	"strings"
	"testing"

	"winterflow/internal/domain/compose/xwinterflow"
)

func TestValidateAcceptsTheExample(t *testing.T) {
	s := spec(t)
	if len(s.Variables) != 4 || len(s.Expose) != 2 || len(s.Compose.Recipe) != 1 {
		t.Fatalf("spec = %+v", s)
	}
	if v, _ := s.Variable("LANGUAGE"); v.Default == nil || *v.Default != "en" {
		t.Fatalf("LANGUAGE = %+v", v)
	}
	if v, _ := s.Variable("SESSION_SECRET"); v.Default != nil {
		t.Fatalf("a secret has a default: %+v", v)
	}
}

func TestValidateRules(t *testing.T) {
	cases := map[string]struct {
		edit  func(*Body)
		field string
	}{
		"no compose file":      {func(b *Body) { b.Files = map[string]string{"config.yml": "x"} }, "a compose file is required"},
		"a reserved file":      {func(b *Body) { b.Files[".env"] = "A=1" }, `".env" is not a usable file path`},
		"a path outside":       {func(b *Body) { b.Files["../x"] = "" }, `"../x" is not a usable file path`},
		"under .winterflow":    {func(b *Body) { b.Files[".winterflow/config.json"] = "{}" }, "is not a usable file path"},
		"a two-line summary":   {func(b *Body) { b.Summary = "a\nb" }, "summary"},
		"a long summary":       {func(b *Body) { b.Summary = strings.Repeat("s", 201) }, "summary"},
		"a long description":   {func(b *Body) { b.Description = strings.Repeat("d", 4001) }, "description"},
		"a padded icon":        {func(b *Body) { b.Icon = " note" }, "icon"},
		"a long color":         {func(b *Body) { b.Color = strings.Repeat("c", 33) }, "color"},
		"not YAML":             {func(b *Body) { b.Files["compose.yaml"] = "services: [\n" }, "not valid YAML"},
		"an unknown block key": {func(b *Body) { b.Files["compose.yaml"] += "  volumes: {}\n" }, "x-winterflow.volumes"},
		"a default off pattern": {func(b *Body) {
			b.Files["compose.yaml"] = strings.Replace(notesCompose, `LANGUAGE: "en"`, `LANGUAGE: "english"`, 1)
		}, "x-winterflow.variables.LANGUAGE.pattern"},
		"a literal volume name": {func(b *Body) {
			b.Files["compose.yaml"] = strings.Replace(notesCompose, "  pg:\nx-", "  pg: { name: shared-pg }\nx-", 1)
		}, "would be shared by every install"},
		"a bad recipe": {func(b *Body) {
			b.Files["compose.yaml"] = strings.Replace(notesCompose, "volume: pg", "volume: nope", 1)
		}, "nope"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			b := notes()
			c.edit(&b)
			_, err := Validate(b)
			wantInvalid(t, err, c.field)
		})
	}
}

func TestValidateName(t *testing.T) {
	if err := ValidateName("notes-2"); err != nil {
		t.Fatal(err)
	}
	wantInvalid(t, ValidateName("Notes"), "name")
}

func TestEncodeCapsTheBody(t *testing.T) {
	b := notes()
	b.Files["big.txt"] = strings.Repeat("x", MaxBodyBytes)
	_, err := Encode(b)
	wantInvalid(t, err, "body")
}

// Warnings name the parser's (a variable nothing uses) and each reference
// nothing provides; declared, reserved, shared and defaulted ones are not.
func TestWarnings(t *testing.T) {
	b := notes()
	b.Files["compose.yaml"] = strings.Replace(notesCompose, "    DB_PASSWORD: { encrypted: true }\n", "    DB_PASSWORD: { encrypted: true }\n    UNUSED: {}\n", 1) +
		"# ${WF_APP_ID} ${ORG_SMTP}\n"
	s, err := Validate(b)
	if err != nil {
		t.Fatal(err)
	}
	got := Warnings(b, s, func(name string) bool { return name == "ORG_SMTP" })
	if len(got) != 2 || !strings.Contains(got[0], "UNUSED") || !strings.HasPrefix(got[1], "SMTP_HOST is not a declared variable") {
		t.Fatalf("warnings = %q", got)
	}
	if got := Warnings(b, s, nil); len(got) != 3 {
		t.Fatalf("without shared names = %q", got)
	}
}

func TestDetect(t *testing.T) {
	d := Detect(spec(t), []string{"w"})
	if len(d.Variables) != 4 || d.Variables[0].Name != "BASE_URL" || !d.Variables[0].Required {
		t.Fatalf("variables = %+v", d.Variables)
	}
	if len(d.Expose) != 2 || d.Expose[0].ID != "app:8080/http" || d.Expose[0].Target != "app:8080" || d.Expose[1].Protocol != "tcp" {
		t.Fatalf("expose = %+v", d.Expose)
	}
	if len(d.Backups) != 1 || d.Backups[0].Name != "db" || d.Backups[0].Type != "files" || len(d.Warnings) != 1 {
		t.Fatalf("detected = %+v", d)
	}
	empty := Detect(xwinterflow.Spec{}, nil)
	if empty.Variables == nil || empty.Expose == nil || empty.Backups == nil || empty.Warnings == nil {
		t.Fatalf("an empty detection has nil lists: %+v", empty)
	}
}
````

Create `internal/domain/template/values_test.go`:

````go
package template

import (
	"reflect"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/pkg/crypto/cryptotest"
)

func TestResolveMergesOverTheStoredValues(t *testing.T) {
	s := spec(t)
	ct := cryptotest.Ciphertext(t, "s3cret")
	v, err := Resolve(s, Stored{}, Given{
		Values:  map[string]string{"BASE_URL": "https://notes.example.test", "LANGUAGE": ""},
		Secrets: map[string]string{"SESSION_SECRET": ct},
	})
	if err != nil {
		t.Fatal(err)
	}
	// LANGUAGE given empty: the compose file's own "en" stands, nothing is sent.
	if !reflect.DeepEqual(v.Plain, map[string]string{"BASE_URL": "https://notes.example.test"}) ||
		!reflect.DeepEqual(v.Secrets, map[string]string{"SESSION_SECRET": ct}) ||
		!reflect.DeepEqual(v.Stored, Stored{Values: map[string]string{"BASE_URL": "https://notes.example.test"}, SecretNames: []string{"SESSION_SECRET"}}) {
		t.Fatalf("values = %+v", v)
	}

	// An update: the stored secret is kept, a new plain value merges, a
	// given "" clears a stored one.
	next, err := Resolve(s, Stored{Values: map[string]string{"BASE_URL": "https://a.test", "LANGUAGE": "de", "GONE": "x"}, SecretNames: []string{"SESSION_SECRET", "OLD"}},
		Given{Values: map[string]string{"LANGUAGE": ""}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(next.Plain, map[string]string{"BASE_URL": "https://a.test"}) ||
		next.Secrets["SESSION_SECRET"] != command.EncryptedPlaceholder || len(next.Secrets) != 1 ||
		!reflect.DeepEqual(next.Stored.SecretNames, []string{"SESSION_SECRET"}) {
		t.Fatalf("update = %+v", next)
	}
}

func TestResolveRefuses(t *testing.T) {
	s := spec(t)
	ct := cryptotest.Ciphertext(t, "s3cret")
	ok := map[string]string{"BASE_URL": "https://a.test"}
	sec := map[string]string{"SESSION_SECRET": ct}
	cases := map[string]struct {
		stored Stored
		given  Given
		field  string
	}{
		"an undeclared value":            {given: Given{Values: map[string]string{"NOPE": "1"}, Secrets: sec}, field: "values.NOPE"},
		"a secret sent plain":            {given: Given{Values: map[string]string{"SESSION_SECRET": "x"}}, field: "values.SESSION_SECRET: SESSION_SECRET is a secret"},
		"a dollar":                       {given: Given{Values: map[string]string{"BASE_URL": "https://$x"}, Secrets: sec}, field: "values.BASE_URL: may not contain $"},
		"an undeclared secret":           {given: Given{Values: ok, Secrets: map[string]string{"NOPE": ct}}, field: "secrets.NOPE"},
		"a plain value as secret":        {given: Given{Values: ok, Secrets: map[string]string{"LANGUAGE": ct}}, field: "secrets.LANGUAGE: LANGUAGE is not a secret"},
		"the keep placeholder":           {given: Given{Values: ok, Secrets: map[string]string{"SESSION_SECRET": command.EncryptedPlaceholder}}, field: "a ciphertext is required"},
		"a plaintext secret":             {given: Given{Values: ok, Secrets: map[string]string{"SESSION_SECRET": "s3cret"}}, field: "secrets.SESSION_SECRET: not a ciphertext"},
		"a missing required value":       {given: Given{Secrets: sec}, field: "variable BASE_URL: a value is required"},
		"a missing required secret":      {given: Given{Values: ok}, field: "variable SESSION_SECRET: a value is required"},
		"a pattern mismatch":             {given: Given{Values: map[string]string{"BASE_URL": "ftp://a"}, Secrets: sec}, field: "variable BASE_URL"},
		"a stored value off the pattern": {stored: Stored{Values: map[string]string{"BASE_URL": "https://a.test", "LANGUAGE": "deu"}, SecretNames: []string{"SESSION_SECRET"}}, field: "variable LANGUAGE"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Resolve(s, c.stored, c.given)
			wantInvalid(t, err, c.field)
		})
	}
}
````

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/domain/template/`
Expected: FAIL — the new tests call `Validate` with two results, `Resolve` on a spec, `Detect`, `CheckHosts`, `CreateBackups` with choices (build failure).

- [ ] **Step 3: Implement**

Delete:

```bash
git rm -q internal/domain/template/ingress.go
```

Replace the whole of `internal/domain/template/body.go` with:

````go
// Package template is a template revision's body: its shape, its
// validation at save, what its compose files declare under x-winterflow
// (the variables an instance fills in, the exposed ports its hostnames
// target, the backups it can turn on), the check of an instance's values,
// and its rendering into an ordinary app.save payload. The agent never sees
// a template; it gets the rendered app, and applies x-winterflow itself.
package template

import (
	"encoding/json"
	"fmt"
	"strings"

	"winterflow/internal/domain/compose/xwinterflow"
	"winterflow/internal/domain/model"
)

// MaxBodyBytes caps a revision's body (its JSON encoding).
const MaxBodyBytes = 1 << 20

// ErrInvalid marks a body, value or request the caller can fix; the
// message names the field (files, values.NAME, hosts[0].expose). It
// matches model.ErrRejected: the API answers it with a 400.
var ErrInvalid = model.Rejected("invalid template")

// Invalid is an ErrInvalid naming field: "invalid template: field: msg".
func Invalid(field, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalid, field, fmt.Sprintf(format, args...))
}

// Body is a revision's body: the app's files and how the template reads in
// a list. What an instance fills in, which ports it exposes and which
// backups it can turn on come from the compose files' x-winterflow block
// (Parse), never from fields of their own.
type Body struct {
	// Files are the app's files by relative path: what an app save
	// carries, never a secret. A compose file is required.
	Files       map[string]string `json:"files" required:"true" description:"The app's files by relative path (a compose file is required); never a secret."`
	Summary     string            `json:"summary" description:"One line about the app, at most 200 characters."`
	Description string            `json:"description" description:"At most 4000 characters."`
	Icon        string            `json:"icon"`
	Color       string            `json:"color"`
}

// Encode is the stored form of a validated body.
func Encode(b Body) ([]byte, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxBodyBytes {
		return nil, Invalid("body", "larger than %d bytes", MaxBodyBytes)
	}
	return raw, nil
}

// composeContents are the body's compose files in compose's load order.
func (b Body) composeContents() [][]byte {
	var out [][]byte
	for _, name := range model.DetectComposeFiles(func(n string) bool { _, ok := b.Files[n]; return ok }) {
		out = append(out, []byte(b.Files[name]))
	}
	return out
}

// Parse reads the body's x-winterflow block with the template rules (a
// volume with a literal name is an error). A stored body passed Validate
// when it was saved; Parse alone is what reading it again needs.
func Parse(b Body) (xwinterflow.Spec, error) {
	return xwinterflow.Parse(xwinterflow.ModeTemplate, b.composeContents()...)
}

// trimmed reports whether s has no leading or trailing space.
func trimmed(s string) bool { return strings.TrimSpace(s) == s }
````

Create `internal/domain/template/detect.go`:

````go
package template

import "winterflow/internal/domain/compose/xwinterflow"

// Detected is what a body's compose files declare under x-winterflow, as
// the editor's Detected panel and the install form show it.
type Detected struct {
	// Variables are what an instance fills in (required ones need a value;
	// encrypted ones are secrets, sent encrypted for the server).
	Variables []xwinterflow.DeclaredVariable `json:"variables"`
	// Expose are the container ports published on the host; an http one
	// can take hostnames.
	Expose []DetectedExpose `json:"expose"`
	// Backups are the backups an instance can turn on.
	Backups []DetectedBackup `json:"backups"`
	// Warnings are what the parser and the save point out.
	Warnings []string `json:"warnings"`
}

// DetectedExpose is one x-winterflow.expose entry.
type DetectedExpose struct {
	ID        string `json:"id" description:"<container>:<port>/<protocol>"`
	Target    string `json:"target" description:"<container>:<port>: what a hostname's expose names (http entries only)."`
	Container string `json:"container"`
	Port      int    `json:"port"`
	Protocol  string `json:"protocol" enum:"http,tcp,udp"`
}

// DetectedBackup is one x-winterflow.backups entry.
type DetectedBackup struct {
	Name string `json:"name"`
	Type string `json:"type" enum:"sqlite,files,custom"`
}

// Detect lists what spec declares, with warnings.
func Detect(spec xwinterflow.Spec, warnings []string) Detected {
	out := Detected{
		Variables: append([]xwinterflow.DeclaredVariable{}, spec.Variables...),
		Expose:    []DetectedExpose{},
		Backups:   []DetectedBackup{},
		Warnings:  append([]string{}, warnings...),
	}
	for _, e := range spec.Expose {
		out.Expose = append(out.Expose, DetectedExpose{ID: e.ID(), Target: e.Target(), Container: e.Container, Port: e.Port, Protocol: e.Protocol})
	}
	for _, r := range spec.Compose.Recipe {
		out.Backups = append(out.Backups, DetectedBackup{Name: r.Name, Type: string(r.Type)})
	}
	return out
}
````

Replace the whole of `internal/domain/template/hash.go` with:

````go
package template

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"winterflow/internal/domain/model"
)

// HashInput is what a create's replay check compares: the secrets by name
// only (ECIES ciphertexts differ on every encryption).
type HashInput struct {
	ServerID   string
	TemplateID string
	// Revision as requested: 0 is "the latest".
	Revision    int64
	Name        string
	Values      map[string]string
	SecretNames []string
	Hosts       []model.InstanceHost
	Backups     map[string]BackupChoice
}

// RequestHash is the hex SHA-256 of the input's canonical JSON (maps
// marshal with sorted keys; secret names are sorted; hosts keep their
// order).
func RequestHash(in HashInput) string {
	names := append([]string{}, in.SecretNames...)
	sort.Strings(names)
	values, backups, hosts := in.Values, in.Backups, in.Hosts
	if values == nil {
		values = map[string]string{}
	}
	if backups == nil {
		backups = map[string]BackupChoice{}
	}
	if hosts == nil {
		hosts = []model.InstanceHost{}
	}
	raw, _ := json.Marshal(struct {
		ServerID    string                  `json:"server_id"`
		TemplateID  string                  `json:"template_id"`
		Revision    int64                   `json:"revision"`
		Name        string                  `json:"name"`
		Values      map[string]string       `json:"values"`
		SecretNames []string                `json:"secret_names"`
		Hosts       []model.InstanceHost    `json:"hosts"`
		Backups     map[string]BackupChoice `json:"backups"`
	}{in.ServerID, in.TemplateID, in.Revision, in.Name, values, names, hosts, backups})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
````

Create `internal/domain/template/instance.go`:

````go
package template

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/compose/xwinterflow"
	"winterflow/internal/domain/model"
)

// Limits of an instance's own choices.
const (
	maxHosts   = 50
	maxAppName = 100
)

// CheckName checks the app name an instance is created with.
func CheckName(name string) error {
	if strings.TrimSpace(name) == "" || len(name) > maxAppName || !trimmed(name) {
		return Invalid("name", "the app name must be 1-%d characters, no surrounding spaces", maxAppName)
	}
	return nil
}

// CheckHosts checks an instance's hostnames: each a valid hostname, at most
// once, routed to an http expose entry of the revision (tcp and udp entries
// never take hostnames). Whether a registered domain covers each, and that
// no other app holds it, is save-app's check.
func CheckHosts(spec xwinterflow.Spec, hosts []model.InstanceHost) error {
	if len(hosts) > maxHosts {
		return Invalid("hosts", "at most %d", maxHosts)
	}
	targets := spec.HTTPTargets()
	for i, h := range hosts {
		if !targets[h.Expose] {
			return Invalid(fmt.Sprintf("hosts[%d].expose", i), "%q is not an http expose entry of the template", h.Expose)
		}
	}
	if err := ingressOf(hosts).Validate(); err != nil {
		return Invalid("hosts", "%v", err)
	}
	return nil
}

// ingressOf is the app ingress the hosts render into.
func ingressOf(hosts []model.InstanceHost) *model.Ingress {
	ing := &model.Ingress{Domains: []model.IngressDomain{}, Redirects: []model.IngressRedirect{}}
	for _, h := range hosts {
		ing.Domains = append(ing.Domains, model.IngressDomain{Domain: h.Host, Expose: h.Expose})
	}
	return ing
}

// BackupChoice is an instance's setting for one backup its compose file
// declares.
type BackupChoice struct {
	Enabled bool   `json:"enabled"`
	Storage string `json:"storage,omitempty" description:"A storage name on the instance's server; required when enabled."`
}

// CreateBackups is the backups list a new instance starts with: one entry
// per choice, by name. A choice must name a backup the revision declares;
// a backup without one starts off.
func CreateBackups(spec xwinterflow.Spec, choices map[string]BackupChoice) ([]recipe.BackupConfig, error) {
	declared := map[string]bool{}
	for _, r := range spec.Compose.Recipe {
		declared[r.Name] = true
	}
	out := []recipe.BackupConfig{}
	for _, name := range slices.Sorted(maps.Keys(choices)) {
		c := choices[name]
		if !declared[name] {
			return nil, Invalid("backups."+name, "the template declares no backup %q", name)
		}
		if c.Storage != "" {
			if err := model.ValidateStorageName(c.Storage); err != nil {
				return nil, Invalid("backups."+name+".storage", "%v", err)
			}
		} else if c.Enabled {
			return nil, Invalid("backups."+name+".storage", "an enabled backup needs a storage")
		}
		out = append(out, recipe.BackupConfig{Name: name, Enabled: c.Enabled, Storage: c.Storage})
	}
	return out, nil
}
````

Replace the whole of `internal/domain/template/render.go` with:

````go
package template

import (
	"encoding/json"
	"maps"
	"slices"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

// Marker is the "instance" key of an instance's config blob: the editor
// and the app page know from it that the app is an instance.
type Marker struct {
	TemplateID   string `json:"template_id"`
	TemplateName string `json:"template_name"`
	Revision     int64  `json:"revision"`
	Key          string `json:"key"`
}

// Input is one render.
type Input struct {
	AppID string
	// Name is the app's name (checked with CheckName).
	Name   string
	Values Values
	// Hosts are the ingress hostnames (checked with CheckHosts).
	Hosts []model.InstanceHost
	// Marker is nil when the app stops being an instance (unlink).
	Marker *Marker
	// Backups, when non-nil, is the whole backups list to store (a create);
	// nil leaves the app's stored list as it is.
	Backups []recipe.BackupConfig
}

// configFile and configVar are the config blob's item metadata (the app
// editor's shape).
type configFile struct {
	Filename    string `json:"filename"`
	IsEncrypted bool   `json:"is_encrypted"`
}

type configVar struct {
	Name        string `json:"name"`
	IsEncrypted bool   `json:"is_encrypted"`
}

// config is the rendered config blob. It has no ports: the agent keeps the
// app's host ports (and pins) when a save carries none.
type config struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Icon        string                 `json:"icon"`
	Color       string                 `json:"color"`
	Files       []configFile           `json:"files"`
	Variables   []configVar            `json:"variables"`
	Ingress     *model.Ingress         `json:"ingress"`
	TemplateID  string                 `json:"template_id,omitempty"`
	Instance    *Marker                `json:"instance,omitempty"`
	Backups     *[]recipe.BackupConfig `json:"backups,omitempty"`
}

// Render turns a body and an instance's choices into the catalog record and
// the app.save payload, exactly what the app editor would send: the files
// unchanged, one app variable per value (secrets as the caller's ciphertext
// or the keep placeholder; a variable without a value sends nothing, so the
// compose file's own value stands), and a config blob with the hosts as the
// ingress, the template id (the apps.list reconcile keeps apps.template_id
// from it) and the marker. The agent applies x-winterflow itself.
func Render(b Body, in Input) (model.App, command.AppPayload, error) {
	if err := CheckName(in.Name); err != nil {
		return model.App{}, command.AppPayload{}, err
	}
	app := model.App{ID: in.AppID, Name: in.Name, Icon: b.Icon, Color: b.Color}
	cfg := config{Name: in.Name, Icon: b.Icon, Color: b.Color, Files: []configFile{}, Variables: []configVar{}, Ingress: ingressOf(in.Hosts)}
	payload := command.AppPayload{AppID: in.AppID, Files: []command.ContentItem{}, Variables: []command.ContentItem{}}

	for _, name := range slices.Sorted(maps.Keys(b.Files)) {
		payload.Files = append(payload.Files, command.ContentItem{Name: name, Content: []byte(b.Files[name])})
		cfg.Files = append(cfg.Files, configFile{Filename: name})
	}
	for _, name := range slices.Sorted(maps.Keys(in.Values.Plain)) {
		payload.Variables = append(payload.Variables, command.ContentItem{Name: name, Content: []byte(in.Values.Plain[name])})
		cfg.Variables = append(cfg.Variables, configVar{Name: name})
	}
	for _, name := range slices.Sorted(maps.Keys(in.Values.Secrets)) {
		payload.Variables = append(payload.Variables, command.ContentItem{Name: name, Content: []byte(in.Values.Secrets[name]), Encrypted: true})
		cfg.Variables = append(cfg.Variables, configVar{Name: name, IsEncrypted: true})
	}
	if in.Marker != nil {
		app.TemplateID = in.Marker.TemplateID
		cfg.TemplateID = in.Marker.TemplateID
		cfg.Instance = in.Marker
	}
	if in.Backups != nil {
		cfg.Backups = &in.Backups
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return model.App{}, command.AppPayload{}, err
	}
	payload.Config = raw
	return app, payload, nil
}
````

Replace the whole of `internal/domain/template/validate.go` with:

````go
package template

import (
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/compose/xwinterflow"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/varref"
)

// Limits of a body's parts.
const (
	maxFileName    = 255
	maxSummary     = 200
	maxDescription = 4000
)

// nameRE is a template name.
var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// reservedFiles are written by the agent; a template can't carry them.
var reservedFiles = map[string]bool{
	".env": true, ".env.secrets": true, ".env.shared": true, ".env.shared.secrets": true, ".gitignore": true,
}

// ValidateName checks an org template's name.
func ValidateName(name string) error {
	if !nameRE.MatchString(name) {
		return Invalid("name", "%q is not a valid template name: lowercase letters, digits and hyphens, starting with a letter, at most 63", name)
	}
	return nil
}

// Validate checks every rule a body must meet at save and returns what its
// compose files declare. A body error names the field; an x-winterflow or
// backup recipe error is the shared parser's (it names the key).
func Validate(b Body) (xwinterflow.Spec, error) {
	if err := validateFiles(b.Files); err != nil {
		return xwinterflow.Spec{}, err
	}
	switch {
	case len(b.Summary) > maxSummary || !trimmed(b.Summary) || strings.ContainsAny(b.Summary, "\r\n"):
		return xwinterflow.Spec{}, Invalid("summary", "one line of at most %d characters, no surrounding spaces", maxSummary)
	case len(b.Description) > maxDescription:
		return xwinterflow.Spec{}, Invalid("description", "at most %d characters", maxDescription)
	case len(b.Icon) > 64 || !trimmed(b.Icon):
		return xwinterflow.Spec{}, Invalid("icon", "at most 64 characters, no surrounding spaces")
	case len(b.Color) > 32 || !trimmed(b.Color):
		return xwinterflow.Spec{}, Invalid("color", "at most 32 characters, no surrounding spaces")
	}
	spec, err := Parse(b)
	if err != nil {
		return xwinterflow.Spec{}, err
	}
	if spec.Compose.Unparsed {
		return xwinterflow.Spec{}, Invalid("files", "a compose file is not valid YAML")
	}
	if err := recipe.Validate(spec.Compose); err != nil {
		return xwinterflow.Spec{}, err
	}
	return spec, nil
}

func validateFiles(files map[string]string) error {
	if len(model.DetectComposeFiles(func(n string) bool { _, ok := files[n]; return ok })) == 0 {
		return Invalid("files", "a compose file is required (%s)", strings.Join(model.ComposeFileNames, ", "))
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		if !validFileName(name) {
			return Invalid("files", "%q is not a usable file path", name)
		}
	}
	return nil
}

// validFileName mirrors the agent's file path rule (a relative path inside
// the app's folder, not under .git or .winterflow) and refuses the files
// the agent writes itself.
func validFileName(name string) bool {
	if name == "" || len(name) > maxFileName || path.Clean(name) != name || path.IsAbs(name) || strings.Contains(name, `\`) {
		return false
	}
	first, _, _ := strings.Cut(name, "/")
	switch first {
	case ".", "..", ".git", ".winterflow":
		return false
	}
	return !reservedFiles[name]
}

// Warnings are what a save should point out without refusing it: the
// parser's (a variable nothing uses), then each ${VAR} the files reference
// that nothing provides: not a declared variable, not a WF_ or
// COMPOSE_PROJECT_NAME name, not an org shared variable (shared reports
// those), and without a fallback of its own.
func Warnings(b Body, spec xwinterflow.Spec, shared func(name string) bool) []string {
	if shared == nil {
		shared = func(string) bool { return false }
	}
	out := append([]string{}, spec.Warnings...)
	refs := map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(b.Files)) {
		varref.Undefaulted([]byte(b.Files[name]), refs)
	}
	for _, name := range slices.Sorted(maps.Keys(refs)) {
		if _, ok := spec.Variable(name); ok || model.ReservedVariableName(name) || shared(name) {
			continue
		}
		out = append(out, fmt.Sprintf("%s is not a declared variable; it must come from a shared variable on the server", name))
	}
	return out
}
````

Replace the whole of `internal/domain/template/values.go` with:

````go
package template

import (
	"maps"
	"slices"
	"strings"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/compose/xwinterflow"
	"winterflow/pkg/crypto"
)

// Given are the values one call sends for the declared variables: plain
// values by name, and secret ciphertexts (ECIES for the instance's server)
// by name.
type Given struct {
	Values  map[string]string
	Secrets map[string]string
}

// Stored are an instance's recorded values: the plain values its callers
// set (never "") and the names of the secrets it was given.
type Stored struct {
	Values      map[string]string
	SecretNames []string
}

// Values are what a render sends: each plain variable that has a value
// (one without keeps the compose file's own) and, per secret variable that
// has one, the ciphertext or command.EncryptedPlaceholder (the agent keeps
// its stored value).
type Values struct {
	Plain   map[string]string
	Secrets map[string]string
	// Stored is what the instance records once the save is confirmed.
	Stored Stored
}

// Resolve checks a call's values against the declared variables and merges
// them over the stored ones: a given "" clears a stored plain value (the
// compose file's own value stands again); a stored value of a variable the
// revision no longer declares, or that changed between plain and secret,
// is dropped. The error names the variable: undeclared, a secret sent as a
// plain value or the other way round, a plain value with a $ (compose
// would interpolate it from the app's .env), a secret that is not an ECIES
// ciphertext (the agent would commit a plaintext to the app's history);
// then the declared rules (xwinterflow CheckValues: a required one
// missing, a plain value failing its pattern).
func Resolve(spec xwinterflow.Spec, stored Stored, given Given) (Values, error) {
	for _, name := range slices.Sorted(maps.Keys(given.Values)) {
		v, ok := spec.Variable(name)
		switch {
		case !ok:
			return Values{}, Invalid("values."+name, "the template declares no variable %s", name)
		case v.Encrypted:
			return Values{}, Invalid("values."+name, "%s is a secret: send it in secrets, encrypted for the server", name)
		case strings.Contains(given.Values[name], "$"):
			return Values{}, Invalid("values."+name, "may not contain $")
		}
	}
	for _, name := range slices.Sorted(maps.Keys(given.Secrets)) {
		v, ok := spec.Variable(name)
		switch {
		case !ok:
			return Values{}, Invalid("secrets."+name, "the template declares no variable %s", name)
		case !v.Encrypted:
			return Values{}, Invalid("secrets."+name, "%s is not a secret: send it in values", name)
		case given.Secrets[name] == "" || given.Secrets[name] == command.EncryptedPlaceholder:
			return Values{}, Invalid("secrets."+name, "a ciphertext is required (leave the secret out to keep its value)")
		case crypto.CheckCiphertext(given.Secrets[name]) != nil:
			return Values{}, Invalid("secrets."+name, "not a ciphertext: encrypt the value with the server's public key")
		}
	}
	had := map[string]bool{}
	for _, n := range stored.SecretNames {
		had[n] = true
	}
	out := Values{Plain: map[string]string{}, Secrets: map[string]string{}, Stored: Stored{Values: map[string]string{}, SecretNames: []string{}}}
	secret := map[string]bool{}
	for _, v := range spec.Variables {
		if v.Encrypted {
			switch ct, ok := given.Secrets[v.Name]; {
			case ok:
				out.Secrets[v.Name] = ct
			case had[v.Name]:
				out.Secrets[v.Name] = command.EncryptedPlaceholder
			default:
				continue
			}
			secret[v.Name] = true
			out.Stored.SecretNames = append(out.Stored.SecretNames, v.Name)
			continue
		}
		val, set := given.Values[v.Name]
		if !set {
			val = stored.Values[v.Name]
		}
		if val != "" {
			out.Plain[v.Name] = val
			out.Stored.Values[v.Name] = val
		}
	}
	if err := spec.CheckValues(out.Plain, secret); err != nil {
		return Values{}, err
	}
	slices.Sort(out.Stored.SecretNames)
	return out, nil
}
````

- [ ] **Step 4: Run them and see them pass**

```bash
go test -cover ./internal/domain/template/
go vet ./internal/domain/template/
```

Expected: PASS — the package passes (about 97% coverage). Its consumers (`internal/domain/usecase/templates`, `internal/domain/usecase/rollouts` tests, `internal/app/web/handler/templates`, the contract tests) no longer compile: Task 3 moves them over and Task 5 the contract tests. Run only this package until then.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat(template): a template body is files plus metadata; x-winterflow declares the rest" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 3: Templates usecase: instances fill in declared variables, hostnames and backups

Save, read and inspect templates; create, update, upgrade and unlink instances on the new body. Catalog templates are read-only (403), a withdrawn one takes no new installs, an instance stores its app name and hostnames, and an upgrade that its stored choices can't render is `ErrNeedsValues`. The handler request types move to the new fields so the tree builds again.

**Files:**
- Modify: `internal/app/web/handler/templates/instances.go`
- Modify: `internal/app/web/handler/templates/update.go`
- Modify: `internal/domain/usecase/rollouts/upgrade_test.go`
- Modify: `internal/domain/usecase/templates/fixture_test.go`
- Modify: `internal/domain/usecase/templates/instance_views.go`
- Modify: `internal/domain/usecase/templates/instances.go`
- Modify: `internal/domain/usecase/templates/instances_test.go`
- Modify: `internal/domain/usecase/templates/lifecycle.go`
- Modify: `internal/domain/usecase/templates/templates.go`
- Modify: `internal/domain/usecase/templates/templates_test.go`
- Modify: `internal/domain/usecase/templates/update.go`
- Modify: `internal/domain/usecase/templates/update_test.go`
- Modify: `internal/domain/usecase/templates/views.go`

**Interfaces:**
- Consumes: Task 1's repository and model, Task 2's `template` package, `appuc.UseCase.CheckSave`/`DispatchSave`/`GetApps`.
- Produces (package `templates`):
  - `SaveTemplate` refuses a catalog id with `model.ErrCatalogReadOnly`; `DeleteTemplate` likewise; `SavedTemplate.Warnings` now include the parser's.
  - `InspectTemplate(ctx, orgID string, b template.Body) (Inspection, error)`; `Inspection{Detected template.Detected; Problem string}` (JSON `detected`, `problem`).
  - `TemplateSummary{..., Source, Slug, WithdrawnAt *time.Time}`; `TemplateView{..., Source, Slug, WithdrawnAt, Detected template.Detected}`.
  - `CreateInput{ServerID, Key, TemplateID, Revision, Name, Values, Secrets, Hosts []model.InstanceHost, Backups map[string]template.BackupChoice}`.
  - `UpdateInput{ServerID, Key, Revision, Values, Secrets, Hosts *[]model.InstanceHost, Backups map[string]template.BackupChoice}`.
  - `InstanceView{..., Name, Values (JSON values), Secrets, Hosts}` (the old `parameters` is gone).
  - Handler: `CreateInstanceRequest{..., Name, Values, Secrets, Hosts, Backups}`, `UpdateInstanceRequest`/`UpgradeInstanceRequest{..., Values, Secrets, Hosts *[]model.InstanceHost, Backups}`.

- [ ] **Step 1: Write the failing tests**

Modify `internal/domain/usecase/rollouts/upgrade_test.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/usecase/rollouts/upgrade_test.go b/internal/domain/usecase/rollouts/upgrade_test.go
--- a/internal/domain/usecase/rollouts/upgrade_test.go
+++ b/internal/domain/usecase/rollouts/upgrade_test.go
@@ -18,15 +18,11 @@ import (
 	apprepo "winterflow/internal/infra/db/repository/app"
 )
 
-const shopCompose = "services:\n  app:\n    image: example/shop:${VERSION}\n"
-
+// shopBody is template "shop" declaring a required NAME with pattern.
 func shopBody(pattern string) template.Body {
-	def := "1"
-	return template.Body{
-		Files:      map[string]string{"compose.yaml": shopCompose},
-		Parameters: []template.Parameter{{Name: "NAME", Required: true, Pattern: pattern}, {Name: "VERSION", Default: &def}},
-		AppName:    "shop-${NAME}",
-	}
+	compose := "services:\n  app:\n    image: example/shop:1\n    environment:\n      - NAME\n" +
+		"x-winterflow:\n  variables:\n    NAME: { required: true, pattern: \"" + pattern + "\" }\n"
+	return template.Body{Files: map[string]string{"compose.yaml": compose}}
 }
 
 // shop saves template "shop" (r1) with created instances named names on
@@ -40,7 +36,9 @@ func (e *env) shop(t *testing.T, tu *templates.UseCase, names ...string) (templa
 	}
 	apps := map[string]string{}
 	for _, n := range names {
-		out, err := tu.CreateInstance(ctx, "u1", org1, templates.CreateInput{ServerID: srv1, Key: n, TemplateID: tpl.ID, Params: map[string]string{"NAME": n}})
+		out, err := tu.CreateInstance(ctx, "u1", org1, templates.CreateInput{
+			ServerID: srv1, Key: n, TemplateID: tpl.ID, Name: "shop-" + n, Values: map[string]string{"NAME": n},
+		})
 		if err != nil {
 			t.Fatal(err)
 		}
````

Modify `internal/domain/usecase/templates/fixture_test.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/usecase/templates/fixture_test.go b/internal/domain/usecase/templates/fixture_test.go
--- a/internal/domain/usecase/templates/fixture_test.go
+++ b/internal/domain/usecase/templates/fixture_test.go
@@ -77,6 +77,8 @@ func (noStorages) ReplaceForServer(context.Context, string, []model.StorageSumma
 
 type fixture struct {
 	uc   *templates.UseCase
+	repo *templaterepo.Repo
+	apps *apprepo.Repo
 	disp *dispatcher
 	tpl  templates.SavedTemplate
 	// secret is the KEY ciphertext create sends.
@@ -85,31 +87,34 @@ type fixture struct {
 
 const compose = `services:
   app:
-    image: example/shop:${VERSION}
+    image: example/shop:1
+    environment:
+      NAME: "shop"
+      VERSION: "1"
+      KEY: "changeme"
     volumes: [data:/data]
 volumes: { data: {} }
 x-winterflow:
+  expose:
+    - { container: app, port: 8080 }
+  variables:
+    NAME: { required: true, pattern: "^[a-z]+$" }
+    VERSION: {}
+    KEY: { required: true, encrypted: true }
   backups:
     - { name: db, type: sqlite, volume: data, path: shop.db }
 `
 
-func strp(s string) *string { return &s }
-
+// body is template "shop": NAME required (lowercase), VERSION optional
+// (the compose file's "1"), KEY a required secret, app:8080 exposed, a
+// sqlite backup "db".
 func body() template.Body {
-	return template.Body{
-		Files: map[string]string{"compose.yaml": compose},
-		Parameters: []template.Parameter{
-			{Name: "NAME", Required: true, Pattern: "^[a-z]+$"},
-			{Name: "VERSION", Default: strp("1")},
-			{Name: "KEY", Secret: true, Required: true},
-		},
-		AppName: "shop-${NAME}",
-		Backups: map[string]template.BackupDefault{"db": {Enabled: false}},
-	}
+	return template.Body{Files: map[string]string{"compose.yaml": compose}, Summary: "A shop", Icon: "cart"}
 }
 
 // newFixture is the usecase over a real database (org1 owns srv1, org2
-// srv2) and a recording dispatcher, with template "shop" saved in org1.
+// srv2) and a recording dispatcher, with template "shop" (body) saved in
+// org1.
 // storages, when set, is the server storage index save-app checks.
 func newFixture(t *testing.T, storages port.ServerStorageRepository) fixture {
 	t.Helper()
@@ -140,5 +145,5 @@ func newFixtureWith(t *testing.T, storages port.ServerStorageRepository, status
 	if err != nil {
 		t.Fatal(err)
 	}
-	return fixture{uc: uc, disp: disp, tpl: tpl, secret: cryptotest.Ciphertext(t, "key")}
+	return fixture{uc: uc, repo: repo, apps: apps, disp: disp, tpl: tpl, secret: cryptotest.Ciphertext(t, "key")}
 }
````

Modify `internal/domain/usecase/templates/instances_test.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/usecase/templates/instances_test.go b/internal/domain/usecase/templates/instances_test.go
--- a/internal/domain/usecase/templates/instances_test.go
+++ b/internal/domain/usecase/templates/instances_test.go
@@ -17,8 +17,8 @@ import (
 )
 
 // create is a create-instance of key k1 on srv1 with the given values.
-func (f fixture) create(params map[string]string) templates.CreateInput {
-	return templates.CreateInput{ServerID: srv1, Key: "k1", TemplateID: f.tpl.ID, Params: params, Secrets: map[string]string{"KEY": f.secret}}
+func (f fixture) create(values map[string]string) templates.CreateInput {
+	return templates.CreateInput{ServerID: srv1, Key: "k1", TemplateID: f.tpl.ID, Values: values, Secrets: map[string]string{"KEY": f.secret}}
 }
 
 // received waits for an outcome's result.
@@ -58,7 +58,8 @@ func TestCreateClaimsDispatchesAndRecords(t *testing.T) {
 		t.Fatalf("result = %+v", res)
 	}
 	got, err := out.Final(ctx)
-	if err != nil || got.State != "created" || got.Parameters["NAME"] != "acme" || got.Secrets["KEY"] != command.EncryptedPlaceholder {
+	if err != nil || got.State != "created" || got.Values["NAME"] != "acme" || got.Secrets["KEY"] != command.EncryptedPlaceholder ||
+		got.Name != "shop" || len(got.Hosts) != 0 {
 		t.Fatalf("after the result = %+v, %v", got, err)
 	}
 	list, err := f.uc.ListInstances(ctx, org1, srv1, f.tpl.ID)
@@ -106,17 +107,41 @@ func TestCreateReplays(t *testing.T) {
 	}
 }
 
+// A create renders the app the caller asked for: its name, a hostname per
+// http expose entry, the backups it turned on, one variable per value.
+func TestCreateRendersNameHostsAndBackups(t *testing.T) {
+	f := newFixture(t, nil)
+	ctx := dbtest.Ctx(t)
+	in := f.create(map[string]string{"NAME": "acme", "VERSION": ""})
+	in.Name = "Acme shop"
+	in.Hosts = []model.InstanceHost{{Host: "acme.example.test", Expose: "app:8080"}}
+	in.Backups = map[string]template.BackupChoice{"db": {Enabled: true, Storage: "disk"}}
+	out, err := f.uc.CreateInstance(ctx, "u1", org1, in)
+	if err != nil {
+		t.Fatal(err)
+	}
+	vars, cfg := saved(t, f)
+	if len(vars) != 2 || string(vars["NAME"].Content) != "acme" || string(vars["KEY"].Content) != f.secret || !vars["KEY"].Encrypted {
+		t.Fatalf("variables = %+v", vars)
+	}
+	if cfg.Name != "Acme shop" || len(cfg.Ingress.Domains) != 1 || cfg.Ingress.Domains[0].Expose != "app:8080" ||
+		len(cfg.Backups) != 1 || cfg.Backups[0].Storage != "disk" || cfg.Instance.Key != "k1" {
+		t.Fatalf("config = %+v", cfg)
+	}
+	f.disp.reply(t, port.CommandResult{Success: true})
+	received(t, out.Done)
+	if v, _ := out.Final(ctx); v.Name != "Acme shop" || len(v.Hosts) != 1 || v.Hosts[0].Host != "acme.example.test" {
+		t.Fatalf("created = %+v", v)
+	}
+}
+
 // A create whose rendered app fails a save-app check claims nothing: the
 // caller can fix the request and use the same key.
 func TestCreateRefusedBeforeTheClaim(t *testing.T) {
 	f := newFixture(t, noStorages{})
 	ctx := dbtest.Ctx(t)
-	b := body()
-	b.Backups["db"] = template.BackupDefault{Enabled: true, Storage: "nowhere"}
-	if _, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{ID: f.tpl.ID, Name: "shop", Body: b, BaseRevision: 1}); err != nil {
-		t.Fatal(err)
-	}
 	in := f.create(map[string]string{"NAME": "acme"})
+	in.Backups = map[string]template.BackupChoice{"db": {Enabled: true, Storage: "nowhere"}}
 	_, err := f.uc.CreateInstance(ctx, "u1", org1, in)
 	if !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), `no storage "nowhere"`) {
 		t.Fatalf("err = %v", err)
@@ -124,7 +149,7 @@ func TestCreateRefusedBeforeTheClaim(t *testing.T) {
 	if _, err := f.uc.GetInstance(ctx, org1, srv1, "k1"); !errors.Is(err, model.ErrInstanceNotFound) || f.disp.count() != 0 {
 		t.Fatalf("a refused create claimed the key: %v", err)
 	}
-	in.Revision = 1 // its backup is off
+	in.Backups = nil // the backup starts off
 	if _, err := f.uc.CreateInstance(ctx, "u1", org1, in); err != nil {
 		t.Fatalf("the fixed request: %v", err)
 	}
@@ -144,15 +169,25 @@ func TestCreateErrors(t *testing.T) {
 	if _, err := f.uc.CreateInstance(ctx, "u1", org1, f.create(map[string]string{"NAME": "UPPER"})); !errors.Is(err, model.ErrRejected) {
 		t.Fatalf("off-pattern value: %v", err)
 	}
+	named := f.create(map[string]string{"NAME": "acme"})
+	named.Name = " padded"
+	if _, err := f.uc.CreateInstance(ctx, "u1", org1, named); !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), "name") {
+		t.Fatalf("a bad app name: %v", err)
+	}
+	hosted := f.create(map[string]string{"NAME": "acme"})
+	hosted.Hosts = []model.InstanceHost{{Host: "a.example.test", Expose: "app:9999"}}
+	if _, err := f.uc.CreateInstance(ctx, "u1", org1, hosted); !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), "hosts[0].expose") {
+		t.Fatalf("a host on no http entry: %v", err)
+	}
 	missing := f.create(map[string]string{"NAME": "acme"})
 	missing.Revision = 7
 	if _, err := f.uc.CreateInstance(ctx, "u1", org1, missing); !errors.Is(err, model.ErrNotFound) {
 		t.Fatalf("missing revision: %v", err)
 	}
 	backups := f.create(map[string]string{"NAME": "acme"})
-	backups.Backups = map[string]string{"media": "disk"}
+	backups.Backups = map[string]template.BackupChoice{"media": {Storage: "disk"}}
 	if _, err := f.uc.CreateInstance(ctx, "u1", org1, backups); !errors.Is(err, model.ErrRejected) {
-		t.Fatalf("an override of an unknown backup: %v", err)
+		t.Fatalf("a setting of an unknown backup: %v", err)
 	}
 	out, _ := f.uc.CreateInstance(ctx, "u1", org1, f.create(map[string]string{"NAME": "acme"}))
 	f.disp.reply(t, port.CommandResult{Error: "pull failed"})
@@ -239,3 +274,40 @@ func TestViewsCarryTheAppStatus(t *testing.T) {
 		}
 	}
 }
+
+// A catalog template is installed like an org's own; one the catalog
+// withdrew takes no new installs.
+func TestCreateFromTheCatalog(t *testing.T) {
+	f := newFixture(t, nil)
+	ctx := dbtest.Ctx(t)
+	raw, err := template.Encode(body())
+	if err != nil {
+		t.Fatal(err)
+	}
+	catID, goneID := dbtest.ID(41), dbtest.ID(42)
+	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
+	for _, c := range []model.Template{
+		{ID: catID, Source: model.TemplateSourceCatalog, Slug: "shop", Name: "Shop"},
+		{ID: goneID, Source: model.TemplateSourceCatalog, Slug: "gone", Name: "Gone", WithdrawnAt: &at},
+	} {
+		if err := f.repo.CreateTemplate(ctx, c, model.TemplateRevision{Body: raw, Digest: "sha256:x"}); err != nil {
+			t.Fatal(err)
+		}
+	}
+	in := f.create(map[string]string{"NAME": "acme"})
+	in.TemplateID = catID
+	if _, err := f.uc.CreateInstance(ctx, "u1", org1, in); err != nil {
+		t.Fatal(err)
+	}
+	if _, cfg := saved(t, f); cfg.Name != "Shop" || cfg.Instance.TemplateID != catID {
+		t.Fatalf("config = %+v", cfg)
+	}
+	in.Key, in.TemplateID = "k2", goneID
+	if _, err := f.uc.CreateInstance(ctx, "u1", org1, in); !errors.Is(err, model.ErrTemplateWithdrawn) || !errors.Is(err, model.ErrConflict) {
+		t.Fatalf("a withdrawn template: %v", err)
+	}
+	list, err := f.uc.ListInstances(ctx, org2, "", catID)
+	if err != nil || len(list.Instances) != 0 {
+		t.Fatalf("another org's instances of the catalog template = %+v, %v", list, err)
+	}
+}
````

Modify `internal/domain/usecase/templates/templates_test.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/usecase/templates/templates_test.go b/internal/domain/usecase/templates/templates_test.go
--- a/internal/domain/usecase/templates/templates_test.go
+++ b/internal/domain/usecase/templates/templates_test.go
@@ -7,6 +7,7 @@ import (
 	"testing"
 
 	"winterflow/internal/domain/model"
+	"winterflow/internal/domain/template"
 	"winterflow/internal/domain/usecase/templates"
 	"winterflow/internal/infra/db/dbtest"
 	templaterepo "winterflow/internal/infra/db/repository/template"
@@ -30,13 +31,17 @@ func TestSaveTemplateConflictsAndWarnings(t *testing.T) {
 	b := body()
 	b.Files["compose.yaml"] += "# ${UNSET}\n"
 	saved, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{ID: f.tpl.ID, Name: "shop", Body: b, BaseRevision: 1, Note: "two"})
-	if err != nil || saved.Revision != 2 || len(saved.Warnings) != 1 || !strings.HasPrefix(saved.Warnings[0], "UNSET ") {
+	if err != nil || saved.Revision != 2 || len(saved.Warnings) != 1 || !strings.HasPrefix(saved.Warnings[0], "UNSET is not a declared variable") {
 		t.Fatalf("saved = %+v, %v", saved, err)
 	}
 	view, err := f.uc.GetTemplate(ctx, org1, f.tpl.ID, 1)
-	if err != nil || view.Revision != 1 || view.LatestRevision != 2 || len(view.Revisions) != 2 || view.Revisions[0].Note != "two" {
+	if err != nil || view.Revision != 1 || view.LatestRevision != 2 || len(view.Revisions) != 2 || view.Revisions[0].Note != "two" ||
+		view.Source != model.TemplateSourceOrg || view.Body.Summary != "A shop" {
 		t.Fatalf("view = %+v, %v", view, err)
 	}
+	if d := view.Detected; len(d.Variables) != 3 || len(d.Expose) != 1 || d.Expose[0].Target != "app:8080" || len(d.Backups) != 1 {
+		t.Fatalf("detected = %+v", d)
+	}
 	if latest, _ := f.uc.GetTemplate(ctx, org1, f.tpl.ID, 0); latest.Revision != 2 || latest.Note != "two" {
 		t.Fatalf("latest = %+v", latest)
 	}
@@ -44,7 +49,7 @@ func TestSaveTemplateConflictsAndWarnings(t *testing.T) {
 		t.Fatalf("missing revision: %v", err)
 	}
 	list, err := f.uc.ListTemplates(ctx, org1)
-	if err != nil || len(list.Templates) != 1 || list.Templates[0].LatestRevision != 2 {
+	if err != nil || len(list.Templates) != 1 || list.Templates[0].LatestRevision != 2 || list.Templates[0].Source != model.TemplateSourceOrg {
 		t.Fatalf("list = %+v, %v", list, err)
 	}
 	if err := f.uc.DeleteTemplate(ctx, org2, f.tpl.ID); !errors.Is(err, model.ErrTemplateNotFound) {
@@ -91,3 +96,88 @@ func TestSaveTemplateWarningsBeforeTheWrite(t *testing.T) {
 		t.Fatalf("the failed save stored %+v, %v", list, err)
 	}
 }
+
+// Every org reads a catalog template; none may save or delete it.
+func TestCatalogTemplatesAreReadOnly(t *testing.T) {
+	f := newFixture(t, nil)
+	ctx := dbtest.Ctx(t)
+	raw, err := template.Encode(body())
+	if err != nil {
+		t.Fatal(err)
+	}
+	cat := dbtest.ID(41)
+	if err := f.repo.CreateTemplate(ctx, model.Template{ID: cat, Source: model.TemplateSourceCatalog, Slug: "shop", Name: "Shop"},
+		model.TemplateRevision{Body: raw, Digest: "sha256:x"}); err != nil {
+		t.Fatal(err)
+	}
+	for _, org := range []string{org1, org2} {
+		v, err := f.uc.GetTemplate(ctx, org, cat, 0)
+		if err != nil || v.Source != model.TemplateSourceCatalog || v.Slug != "shop" || len(v.Detected.Variables) != 3 {
+			t.Fatalf("%s reads %+v, %v", org, v, err)
+		}
+	}
+	if list, _ := f.uc.ListTemplates(ctx, org1); len(list.Templates) != 2 || list.Templates[1].ID != cat {
+		t.Fatalf("org1 list = %+v", list)
+	}
+	_, err = f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{ID: cat, Name: "shop", Body: body(), BaseRevision: 1})
+	if !errors.Is(err, model.ErrCatalogReadOnly) || !errors.Is(err, model.ErrForbidden) {
+		t.Fatalf("save a catalog template: %v", err)
+	}
+	if err := f.uc.DeleteTemplate(ctx, org2, cat); !errors.Is(err, model.ErrForbidden) {
+		t.Fatalf("delete a catalog template: %v", err)
+	}
+	if _, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{ID: dbtest.ID(99), Name: "shop", Body: body(), BaseRevision: 1}); !errors.Is(err, model.ErrTemplateNotFound) {
+		t.Fatalf("save an unknown template: %v", err)
+	}
+}
+
+// Inspect reads a body as a save would, without storing it: what it
+// declares as far as it parses, and the first problem.
+func TestInspectTemplate(t *testing.T) {
+	f := newFixture(t, nil)
+	ctx := dbtest.Ctx(t)
+	got, err := f.uc.InspectTemplate(ctx, org1, body())
+	if err != nil || got.Problem != "" || len(got.Detected.Variables) != 3 || len(got.Detected.Warnings) != 0 {
+		t.Fatalf("a good body = %+v, %v", got, err)
+	}
+	b := body()
+	b.Summary = "two\nlines"
+	got, err = f.uc.InspectTemplate(ctx, org1, b)
+	if err != nil || !strings.Contains(got.Problem, "summary") || len(got.Detected.Variables) != 3 {
+		t.Fatalf("a bad summary still shows the block: %+v, %v", got, err)
+	}
+	b = body()
+	b.Files["compose.yaml"] += "  bogus: 1\n"
+	got, err = f.uc.InspectTemplate(ctx, org1, b)
+	if err != nil || !strings.Contains(got.Problem, "x-winterflow.bogus") || len(got.Detected.Variables) != 0 {
+		t.Fatalf("a bad block = %+v, %v", got, err)
+	}
+	if list, _ := f.uc.ListTemplates(ctx, org1); len(list.Templates) != 1 {
+		t.Fatalf("inspect stored something: %+v", list)
+	}
+	if _, err := templates.New(templates.Deps{Repo: f.repo, Shared: failingShared{}, Log: dbtest.Log}).InspectTemplate(ctx, org1, body()); err == nil {
+		t.Fatal("a failed shared variables read was not reported")
+	}
+}
+
+// A stored body a later parser refuses still reads (its problem shown as a
+// warning), but nothing is rendered from it.
+func TestARevisionThatNoLongerParses(t *testing.T) {
+	f := newFixture(t, nil)
+	ctx := dbtest.Ctx(t)
+	cat := dbtest.ID(41)
+	raw := []byte(`{"files":{"compose.yaml":"services: {app: {image: x}}\nx-winterflow: {bogus: 1}\n"}}`)
+	if err := f.repo.CreateTemplate(ctx, model.Template{ID: cat, Source: model.TemplateSourceCatalog, Slug: "old", Name: "Old"},
+		model.TemplateRevision{Body: raw}); err != nil {
+		t.Fatal(err)
+	}
+	v, err := f.uc.GetTemplate(ctx, org1, cat, 0)
+	if err != nil || len(v.Detected.Warnings) != 1 || !strings.Contains(v.Detected.Warnings[0], "x-winterflow.bogus") {
+		t.Fatalf("view = %+v, %v", v, err)
+	}
+	in := f.create(nil)
+	in.TemplateID = cat
+	if _, err := f.uc.CreateInstance(ctx, "u1", org1, in); err == nil || !strings.Contains(err.Error(), "x-winterflow.bogus") || f.disp.count() != 0 {
+		t.Fatalf("a create from it: %v", err)
+	}
+}
````

Modify `internal/domain/usecase/templates/update_test.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/usecase/templates/update_test.go b/internal/domain/usecase/templates/update_test.go
--- a/internal/domain/usecase/templates/update_test.go
+++ b/internal/domain/usecase/templates/update_test.go
@@ -45,10 +45,10 @@ func TestUpdateKeepsValuesAndSecrets(t *testing.T) {
 	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv2, Key: "k1"}); !errors.Is(err, model.ErrInstanceNotFound) {
 		t.Fatalf("on another server: %v", err)
 	}
-	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Params: map[string]string{"NOPE": "x"}}); !errors.Is(err, model.ErrRejected) {
-		t.Fatalf("unknown parameter: %v", err)
+	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Values: map[string]string{"NOPE": "x"}}); !errors.Is(err, model.ErrRejected) {
+		t.Fatalf("an undeclared variable: %v", err)
 	}
-	out, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Params: map[string]string{"VERSION": "2"}})
+	out, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Values: map[string]string{"VERSION": "2"}})
 	if err != nil {
 		t.Fatal(err)
 	}
@@ -63,31 +63,74 @@ func TestUpdateKeepsValuesAndSecrets(t *testing.T) {
 	}
 	f.disp.reply(t, port.CommandResult{Success: true})
 	received(t, out.Done)
-	if v, _ := out.Final(ctx); v.Parameters["VERSION"] != "2" || v.Parameters["NAME"] != "acme" || v.Revision != 1 {
+	if v, _ := out.Final(ctx); v.Values["VERSION"] != "2" || v.Values["NAME"] != "acme" || v.Revision != 1 {
 		t.Fatalf("after update = %+v", v)
 	}
 }
 
-func TestUpgradeSendsNewBackupDefaults(t *testing.T) {
+// An update renders the app under its current name (a rename since the
+// create counts) and keeps the hostnames unless new ones are given.
+func TestUpdateKeepsTheNameAndHosts(t *testing.T) {
+	f := newFixture(t, nil)
+	ctx := dbtest.Ctx(t)
+	in := f.create(map[string]string{"NAME": "acme"})
+	in.Name = "Acme"
+	in.Hosts = []model.InstanceHost{{Host: "acme.example.test", Expose: "app:8080"}}
+	out, err := f.uc.CreateInstance(ctx, "u1", org1, in)
+	if err != nil {
+		t.Fatal(err)
+	}
+	f.disp.reply(t, port.CommandResult{Success: true})
+	received(t, out.Done)
+	appID := f.disp.last(t).Payload.(command.SaveAppRequest).App.AppID
+	if err := f.apps.RenameApp(ctx, appID, "Renamed"); err != nil {
+		t.Fatal(err)
+	}
+	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1"}); err != nil {
+		t.Fatal(err)
+	}
+	if _, cfg := saved(t, f); cfg.Name != "Renamed" || len(cfg.Ingress.Domains) != 1 || cfg.Ingress.Domains[0].Domain != "acme.example.test" {
+		t.Fatalf("an update's config = %+v", cfg)
+	}
+	none := []model.InstanceHost{}
+	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Hosts: &none}); err != nil {
+		t.Fatal(err)
+	}
+	if _, cfg := saved(t, f); len(cfg.Ingress.Domains) != 0 {
+		t.Fatalf("hosts not replaced: %+v", cfg.Ingress)
+	}
+	bad := []model.InstanceHost{{Host: "x.example.test", Expose: "db:1"}}
+	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Hosts: &bad}); !errors.Is(err, model.ErrRejected) || errors.Is(err, templates.ErrNeedsValues) {
+		t.Fatalf("a given host on no http entry: %v", err)
+	}
+}
+
+// An upgrade re-renders with the stored values: a newly required variable
+// without one is ErrNeedsValues (as is a stored hostname whose expose entry
+// the revision dropped), and it sends no backups list, so a backup the
+// revision adds starts off.
+func TestUpgradeNeedsValuesAndSendsNoBackups(t *testing.T) {
 	f := newFixture(t, nil)
 	ctx := dbtest.Ctx(t)
 	created(t, f)
 
 	b := body()
-	b.Files["compose.yaml"] = strings.Replace(compose, "path: shop.db }\n", "path: shop.db }\n    - { name: media, type: files, volume: data }\n", 1)
-	b.Backups["media"] = template.BackupDefault{Every: "6h"}
-	b.Parameters = append(b.Parameters, template.Parameter{Name: "REGION", Required: true})
+	b.Files["compose.yaml"] = strings.NewReplacer(
+		"path: shop.db }\n", "path: shop.db }\n    - { name: media, type: files, volume: data }\n",
+		"      KEY: \"changeme\"\n", "      KEY: \"changeme\"\n      REGION: \"\"\n",
+		"    VERSION: {}\n", "    VERSION: {}\n    REGION: { required: true }\n",
+	).Replace(compose)
 	if _, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{ID: f.tpl.ID, Name: "shop", Body: b, BaseRevision: 1}); err != nil {
 		t.Fatal(err)
 	}
 	_, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Revision: 2})
-	if !errors.Is(err, model.ErrRejected) || !errors.Is(err, templates.ErrNeedsValues) || !strings.Contains(err.Error(), "parameters.REGION: required") {
+	if !errors.Is(err, model.ErrRejected) || !errors.Is(err, templates.ErrNeedsValues) || !strings.Contains(err.Error(), "variable REGION: a value is required") {
 		t.Fatalf("a newly required value missing: %v", err)
 	}
 	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Revision: 5}); !errors.Is(err, model.ErrNotFound) {
 		t.Fatalf("an unknown revision: %v", err)
 	}
-	up, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Revision: 2, Params: map[string]string{"REGION": "eu"}})
+	up, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Revision: 2, Values: map[string]string{"REGION": "eu"}})
 	if err != nil {
 		t.Fatal(err)
 	}
@@ -96,25 +139,50 @@ func TestUpgradeSendsNewBackupDefaults(t *testing.T) {
 		Instance template.Marker `json:"instance"`
 	}
 	_ = json.Unmarshal(f.disp.last(t).Payload.(command.SaveAppRequest).App.Config, &cfg)
-	if cfg.Backups != nil || cfg.Instance.Revision != 2 || len(cfg.Instance.BackupDefaults) != 1 || cfg.Instance.BackupDefaults[0].Name != "media" {
+	if cfg.Backups != nil || cfg.Instance.Revision != 2 {
 		t.Fatalf("upgrade config: backups %s marker %+v", cfg.Backups, cfg.Instance)
 	}
 	f.disp.reply(t, port.CommandResult{Success: true})
 	received(t, up.Done)
-	if v, _ := up.Final(ctx); v.Revision != 2 || v.Parameters["REGION"] != "eu" {
+	if v, _ := up.Final(ctx); v.Revision != 2 || v.Values["REGION"] != "eu" {
 		t.Fatalf("upgraded = %+v", v)
 	}
 	list, err := f.uc.ListTemplates(ctx, org1)
 	if err != nil || list.Templates[0].Instances != 1 || list.Templates[0].Behind != 0 {
 		t.Fatalf("templates = %+v, %v", list, err)
 	}
+
+	// Revision 3 no longer exposes app:8080: the stored hostname can't
+	// follow, so Upgrade all would skip the instance.
+	hosted := f.create(map[string]string{"NAME": "acme"})
+	hosted.Key, hosted.Revision = "k2", 1
+	hosted.Hosts = []model.InstanceHost{{Host: "k2.example.test", Expose: "app:8080"}}
+	out, err := f.uc.CreateInstance(ctx, "u1", org1, hosted)
+	if err != nil {
+		t.Fatal(err)
+	}
+	f.disp.reply(t, port.CommandResult{Success: true})
+	received(t, out.Done)
+	b.Files["compose.yaml"] = strings.Replace(b.Files["compose.yaml"], "    - { container: app, port: 8080 }\n", "    - { container: app, port: 9090 }\n", 1)
+	if _, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{ID: f.tpl.ID, Name: "shop", Body: b, BaseRevision: 2}); err != nil {
+		t.Fatal(err)
+	}
+	_, err = f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k2", Revision: 3, Values: map[string]string{"REGION": "eu"}})
+	if !errors.Is(err, templates.ErrNeedsValues) || !strings.Contains(err.Error(), "hosts[0].expose") {
+		t.Fatalf("a stored host the revision can't route: %v", err)
+	}
 }
 
-// saved is the last dispatched save's variable by name and its config.
-func saved(t *testing.T, f fixture) (vars map[string]command.ContentItem, cfg struct {
+// savedConfig is what the tests read of a dispatched save's config.
+type savedConfig struct {
+	Name     string
+	Ingress  model.Ingress
 	Backups  []struct{ Name, Storage string }
 	Instance template.Marker
-}) {
+}
+
+// saved is the last dispatched save's variable by name and its config.
+func saved(t *testing.T, f fixture) (vars map[string]command.ContentItem, cfg savedConfig) {
 	t.Helper()
 	req := f.disp.last(t).Payload.(command.SaveAppRequest)
 	vars = map[string]command.ContentItem{}
@@ -135,8 +203,8 @@ func TestUpdatePendingSendsSecretsAndBackups(t *testing.T) {
 	ctx := dbtest.Ctx(t)
 	pendingInstance(t, f)
 
-	in := templates.UpdateInput{ServerID: srv1, Key: "k1", Params: map[string]string{"VERSION": "2"}}
-	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, in); !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), "secrets.KEY") {
+	in := templates.UpdateInput{ServerID: srv1, Key: "k1", Values: map[string]string{"VERSION": "2"}}
+	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, in); !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), "variable KEY: a value is required") {
 		t.Fatalf("a pending update without the required secret: %v", err)
 	}
 	if f.disp.count() != 1 {
@@ -153,12 +221,12 @@ func TestUpdatePendingSendsSecretsAndBackups(t *testing.T) {
 	if k := vars["KEY"]; string(k.Content) != again || !k.Encrypted {
 		t.Fatalf("the secret = %+v", k)
 	}
-	if len(cfg.Backups) != 1 || cfg.Backups[0].Name != "db" || len(cfg.Instance.BackupDefaults) != 0 {
-		t.Fatalf("config: backups %+v marker %+v", cfg.Backups, cfg.Instance)
+	if cfg.Backups == nil || len(cfg.Backups) != 0 {
+		t.Fatalf("config: backups %+v (want the whole, empty list)", cfg.Backups)
 	}
 	f.disp.reply(t, port.CommandResult{Success: true})
 	received(t, out.Done)
-	if v, _ := out.Final(ctx); v.State != "created" || v.Parameters["VERSION"] != "2" || v.Parameters["NAME"] != "acme" || v.Secrets["KEY"] != command.EncryptedPlaceholder {
+	if v, _ := out.Final(ctx); v.State != "created" || v.Values["VERSION"] != "2" || v.Values["NAME"] != "acme" || v.Secrets["KEY"] != command.EncryptedPlaceholder {
 		t.Fatalf("after the update = %+v", v)
 	}
 }
@@ -168,11 +236,11 @@ func TestUpdatePendingBackupsOverride(t *testing.T) {
 	ctx := dbtest.Ctx(t)
 	pendingInstance(t, f)
 
-	in := templates.UpdateInput{ServerID: srv1, Key: "k1", Secrets: map[string]string{"KEY": f.secret}, Backups: map[string]string{"nope": "disk"}}
+	in := templates.UpdateInput{ServerID: srv1, Key: "k1", Secrets: map[string]string{"KEY": f.secret}, Backups: map[string]template.BackupChoice{"nope": {Storage: "disk"}}}
 	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, in); !errors.Is(err, model.ErrRejected) {
-		t.Fatalf("an override of an unknown backup: %v", err)
+		t.Fatalf("a setting of an unknown backup: %v", err)
 	}
-	in.Backups = map[string]string{"db": "disk"}
+	in.Backups = map[string]template.BackupChoice{"db": {Storage: "disk"}}
 	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, in); err != nil {
 		t.Fatal(err)
 	}
@@ -188,17 +256,15 @@ func TestUpgradePendingSendsTheWholeBackupsList(t *testing.T) {
 
 	b := body()
 	b.Files["compose.yaml"] = strings.Replace(compose, "path: shop.db }\n", "path: shop.db }\n    - { name: media, type: files, volume: data }\n", 1)
-	b.Backups["media"] = template.BackupDefault{Every: "6h"}
 	if _, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{ID: f.tpl.ID, Name: "shop", Body: b, BaseRevision: 1}); err != nil {
 		t.Fatal(err)
 	}
 	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Revision: 2,
-		Secrets: map[string]string{"KEY": f.secret}, Backups: map[string]string{"media": "disk"}}); err != nil {
+		Secrets: map[string]string{"KEY": f.secret}, Backups: map[string]template.BackupChoice{"media": {Storage: "disk"}}}); err != nil {
 		t.Fatal(err)
 	}
 	_, cfg := saved(t, f)
-	if len(cfg.Backups) != 2 || cfg.Backups[1].Name != "media" || cfg.Backups[1].Storage != "disk" ||
-		cfg.Instance.Revision != 2 || len(cfg.Instance.BackupDefaults) != 0 {
+	if len(cfg.Backups) != 1 || cfg.Backups[0].Name != "media" || cfg.Backups[0].Storage != "disk" || cfg.Instance.Revision != 2 {
 		t.Fatalf("config: backups %+v marker %+v", cfg.Backups, cfg.Instance)
 	}
 }
@@ -209,7 +275,7 @@ func TestUpdateCreatedRefusesBackups(t *testing.T) {
 	f := newFixture(t, nil)
 	created(t, f)
 	sent := f.disp.count()
-	_, err := f.uc.UpdateInstance(dbtest.Ctx(t), "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Backups: map[string]string{"db": "disk"}})
+	_, err := f.uc.UpdateInstance(dbtest.Ctx(t), "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Backups: map[string]template.BackupChoice{"db": {Storage: "disk"}}})
 	if !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), "backups can only be set while the instance is pending") {
 		t.Fatalf("err = %v", err)
 	}
````

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/domain/usecase/templates/ ./internal/domain/usecase/rollouts/ ./internal/app/web/handler/templates/`
Expected: FAIL — the tests use `Values`, `Hosts`, `Name`, `InspectTemplate`, `TemplateView.Detected` and catalog rows (build failure).

- [ ] **Step 3: Implement**

Modify `internal/app/web/handler/templates/instances.go` (`git apply` this diff):

````diff
diff --git a/internal/app/web/handler/templates/instances.go b/internal/app/web/handler/templates/instances.go
--- a/internal/app/web/handler/templates/instances.go
+++ b/internal/app/web/handler/templates/instances.go
@@ -4,19 +4,23 @@ import (
 	"net/http"
 
 	webutil "winterflow/internal/app/web/util"
+	"winterflow/internal/domain/model"
+	"winterflow/internal/domain/template"
 	tuc "winterflow/internal/domain/usecase/templates"
 )
 
 // CreateInstanceRequest is the body of POST create-instance.
 type CreateInstanceRequest struct {
-	ServerID   string            `json:"server_id" required:"true"`
-	Key        string            `json:"key" required:"true" description:"The caller's id for the instance, unique in the org among instances not unlinked; a retried create with the same key and values makes no second app."`
-	TemplateID string            `json:"template_id" required:"true"`
-	Revision   int64             `json:"revision,omitempty" description:"Absent or 0: the latest."`
-	Parameters map[string]string `json:"parameters,omitempty" description:"Plain parameter values by name; a value may not contain $."`
-	Secrets    map[string]string `json:"secrets,omitempty" description:"Secret parameter values by name, ECIES-encrypted with the server's public key."`
-	Backups    map[string]string `json:"backups,omitempty" description:"Backup name -> storage name, replacing the template default's storage."`
-	Wait       bool              `json:"wait,omitempty" description:"Hold the request for the outcome (at most 10 minutes)."`
+	ServerID   string                           `json:"server_id" required:"true"`
+	Key        string                           `json:"key" required:"true" description:"The caller's id for the instance, unique in the org among instances not unlinked; a retried create with the same key and values makes no second app."`
+	TemplateID string                           `json:"template_id" required:"true"`
+	Revision   int64                            `json:"revision,omitempty" description:"Absent or 0: the latest."`
+	Name       string                           `json:"name,omitempty" description:"The app's name; absent: the template's name."`
+	Values     map[string]string                `json:"values,omitempty" description:"Plain values of the variables the compose file declares, by name; a value may not contain $. A variable left out keeps the compose file's own value."`
+	Secrets    map[string]string                `json:"secrets,omitempty" description:"Values of the encrypted variables by name, ECIES-encrypted with the server's public key."`
+	Hosts      []model.InstanceHost             `json:"hosts,omitempty" description:"Hostnames, each routed to an http expose entry (<service>:<port>) of the template; each must be covered by a domain registered on the server."`
+	Backups    map[string]template.BackupChoice `json:"backups,omitempty" description:"Settings of the backups the compose file declares, by name; a backup left out starts off."`
+	Wait       bool                             `json:"wait,omitempty" description:"Hold the request for the outcome (at most 10 minutes)."`
 }
 
 // InstanceQuery is the query of GET get-instance.
@@ -45,7 +49,7 @@ func (h *Handler) CreateInstance(w http.ResponseWriter, r *http.Request) {
 	}
 	out, err := h.uc.CreateInstance(r.Context(), userID, orgID, tuc.CreateInput{
 		ServerID: req.ServerID, Key: req.Key, TemplateID: req.TemplateID, Revision: req.Revision,
-		Params: req.Parameters, Secrets: req.Secrets, Backups: req.Backups,
+		Name: req.Name, Values: req.Values, Secrets: req.Secrets, Hosts: req.Hosts, Backups: req.Backups,
 	})
 	if err != nil {
 		h.fail(w, err, "create the instance")
````

Modify `internal/app/web/handler/templates/update.go` (`git apply` this diff):

````diff
diff --git a/internal/app/web/handler/templates/update.go b/internal/app/web/handler/templates/update.go
--- a/internal/app/web/handler/templates/update.go
+++ b/internal/app/web/handler/templates/update.go
@@ -4,28 +4,32 @@ import (
 	"net/http"
 
 	webutil "winterflow/internal/app/web/util"
+	"winterflow/internal/domain/model"
+	"winterflow/internal/domain/template"
 	tuc "winterflow/internal/domain/usecase/templates"
 )
 
 // UpdateInstanceRequest is the body of POST update-instance.
 type UpdateInstanceRequest struct {
-	ServerID   string            `json:"server_id" required:"true"`
-	Key        string            `json:"key" required:"true"`
-	Parameters map[string]string `json:"parameters,omitempty" description:"New plain values, merged over the stored ones; a value may not contain $."`
-	Secrets    map[string]string `json:"secrets,omitempty" description:"New secret values; a secret left out keeps its value (a pending instance holds none: send every required secret again)."`
-	Backups    map[string]string `json:"backups,omitempty" description:"Backup name -> storage name, replacing the template default's storage. Accepted only while the instance is pending; afterwards change backups in the app's Backups tab. A pending instance keeps none of the create's overrides: send them again."`
-	Wait       bool              `json:"wait,omitempty"`
+	ServerID string                           `json:"server_id" required:"true"`
+	Key      string                           `json:"key" required:"true"`
+	Values   map[string]string                `json:"values,omitempty" description:"New plain values, merged over the stored ones; an empty value clears one (the compose file's own value stands). A value may not contain $."`
+	Secrets  map[string]string                `json:"secrets,omitempty" description:"New secret values; a secret left out keeps its value (a pending instance holds none: send every required secret again)."`
+	Hosts    *[]model.InstanceHost            `json:"hosts,omitempty" description:"Present: the instance's hostnames from now on (an empty list removes them); absent: kept."`
+	Backups  map[string]template.BackupChoice `json:"backups,omitempty" description:"Backup settings by name. Accepted only while the instance is pending (send them all again); afterwards change backups in the app's Backups tab."`
+	Wait     bool                             `json:"wait,omitempty"`
 }
 
 // UpgradeInstanceRequest is the body of POST upgrade-instance.
 type UpgradeInstanceRequest struct {
-	ServerID   string            `json:"server_id" required:"true"`
-	Key        string            `json:"key" required:"true"`
-	Revision   int64             `json:"revision" required:"true" minimum:"1"`
-	Parameters map[string]string `json:"parameters,omitempty" description:"Values for parameters the revision adds or changes; a value may not contain $."`
-	Secrets    map[string]string `json:"secrets,omitempty" description:"Secret values for the revision; a secret left out keeps its value (a pending instance holds none: send every required secret again)."`
-	Backups    map[string]string `json:"backups,omitempty" description:"Backup name -> storage name, replacing the template default's storage. Accepted only while the instance is pending; afterwards change backups in the app's Backups tab. A pending instance keeps none of the create's overrides: send them again."`
-	Wait       bool              `json:"wait,omitempty"`
+	ServerID string                           `json:"server_id" required:"true"`
+	Key      string                           `json:"key" required:"true"`
+	Revision int64                            `json:"revision" required:"true" minimum:"1"`
+	Values   map[string]string                `json:"values,omitempty" description:"Values for variables the revision adds or changes; a value may not contain $."`
+	Secrets  map[string]string                `json:"secrets,omitempty" description:"Secret values for the revision; a secret left out keeps its value (a pending instance holds none: send every required secret again)."`
+	Hosts    *[]model.InstanceHost            `json:"hosts,omitempty" description:"Present: the instance's hostnames from now on; absent: kept (each must still be an http expose entry of the revision)."`
+	Backups  map[string]template.BackupChoice `json:"backups,omitempty" description:"Backup settings by name. Accepted only while the instance is pending; backups the revision adds start off."`
+	Wait     bool                             `json:"wait,omitempty"`
 }
 
 func (h *Handler) UpdateInstance(w http.ResponseWriter, r *http.Request) {
@@ -38,7 +42,7 @@ func (h *Handler) UpdateInstance(w http.ResponseWriter, r *http.Request) {
 		return
 	}
 	h.update(w, r, userID, orgID, tuc.UpdateInput{
-		ServerID: req.ServerID, Key: req.Key, Params: req.Parameters, Secrets: req.Secrets, Backups: req.Backups,
+		ServerID: req.ServerID, Key: req.Key, Values: req.Values, Secrets: req.Secrets, Hosts: req.Hosts, Backups: req.Backups,
 	}, req.Wait, "update the instance")
 }
 
@@ -56,7 +60,7 @@ func (h *Handler) UpgradeInstance(w http.ResponseWriter, r *http.Request) {
 		return
 	}
 	h.update(w, r, userID, orgID, tuc.UpdateInput{
-		ServerID: req.ServerID, Key: req.Key, Revision: req.Revision, Params: req.Parameters, Secrets: req.Secrets, Backups: req.Backups,
+		ServerID: req.ServerID, Key: req.Key, Revision: req.Revision, Values: req.Values, Secrets: req.Secrets, Hosts: req.Hosts, Backups: req.Backups,
 	}, req.Wait, "upgrade the instance")
 }
 
````

Modify `internal/domain/usecase/templates/instance_views.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/usecase/templates/instance_views.go b/internal/domain/usecase/templates/instance_views.go
--- a/internal/domain/usecase/templates/instance_views.go
+++ b/internal/domain/usecase/templates/instance_views.go
@@ -15,11 +15,13 @@ type InstanceView struct {
 	TemplateID   string `json:"template_id" description:"Empty once the template is deleted."`
 	TemplateName string `json:"template_name"`
 	Revision     int64  `json:"revision"`
-	// Parameters are the values callers set; parameters left out use the
-	// revision's default.
-	Parameters map[string]string `json:"parameters" description:"Values the callers set; the others use the revision's default."`
-	Secrets    map[string]string `json:"secrets" description:"Each secret given, as \"<encrypted>\": values are never returned."`
-	State      string            `json:"state" description:"pending, created, deleted or unlinked (only an unlink's own answer shows unlinked: the key then finds nothing)."`
+	Name         string `json:"name" description:"The app name the create asked for (a later rename shows in the app list)."`
+	// Values are the plain values callers set; a variable left out keeps
+	// the compose file's own value.
+	Values  map[string]string    `json:"values" description:"Plain values the callers set; a variable left out keeps the compose file's own value."`
+	Secrets map[string]string    `json:"secrets" description:"Each secret given, as \"<encrypted>\": values are never returned."`
+	Hosts   []model.InstanceHost `json:"hosts" description:"Hostnames routed to the app's http expose entries."`
+	State   string               `json:"state" description:"pending, created, deleted or unlinked (only an unlink's own answer shows unlinked: the key then finds nothing)."`
 	// StatusCode is the app's latest container status (0 unknown).
 	StatusCode command.ContainerStatusCode `json:"status_code"`
 	LastError  string                      `json:"last_error" description:"The last failed command's error; empty after a success."`
@@ -56,13 +58,17 @@ func buildView(inst model.Instance, templateName string, codes map[string]comman
 	for _, n := range inst.SecretNames {
 		secrets[n] = command.EncryptedPlaceholder
 	}
-	params := inst.Params
-	if params == nil {
-		params = map[string]string{}
+	values := inst.Params
+	if values == nil {
+		values = map[string]string{}
+	}
+	hosts := inst.Hosts
+	if hosts == nil {
+		hosts = []model.InstanceHost{}
 	}
 	return InstanceView{
 		Key: inst.Key, ServerID: inst.ServerID, AppID: inst.AppID, TemplateID: inst.TemplateID,
-		TemplateName: templateName, Revision: inst.Revision, Parameters: params, Secrets: secrets,
+		TemplateName: templateName, Revision: inst.Revision, Name: inst.Name, Values: values, Secrets: secrets, Hosts: hosts,
 		State: string(inst.State), StatusCode: codes[inst.AppID], LastError: inst.LastError,
 		CreatedAt: inst.CreatedAt, UpdatedAt: inst.UpdatedAt,
 	}
````

Modify `internal/domain/usecase/templates/instances.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/usecase/templates/instances.go b/internal/domain/usecase/templates/instances.go
--- a/internal/domain/usecase/templates/instances.go
+++ b/internal/domain/usecase/templates/instances.go
@@ -32,10 +32,14 @@ type CreateInput struct {
 	TemplateID string
 	// Revision 0 is the latest.
 	Revision int64
-	Params   map[string]string
-	Secrets  map[string]string
-	// Backups overrides the storage of template backup defaults by name.
-	Backups map[string]string
+	// Name is the app's name; "" is the template's name.
+	Name    string
+	Values  map[string]string
+	Secrets map[string]string
+	Hosts   []model.InstanceHost
+	// Backups are the settings of the backups the compose file declares,
+	// by name; a backup without one starts off.
+	Backups map[string]template.BackupChoice
 }
 
 // CreateInstance creates the instance, or replays a create the key already
@@ -43,7 +47,8 @@ type CreateInput struct {
 // dispatched) or dispatches a pending one's save again; a different
 // request is model.ErrKeyInUse, a deleted instance's key
 // model.ErrKeyRetired. The key is claimed only once the rendered app passes
-// every save-app check.
+// every save-app check. A withdrawn catalog template takes no new installs
+// (model.ErrTemplateWithdrawn).
 func (uc *UseCase) CreateInstance(ctx context.Context, userID, orgID string, in CreateInput) (Outcome, error) {
 	if err := ValidateKey(in.Key); err != nil {
 		return Outcome{}, err
@@ -52,29 +57,42 @@ func (uc *UseCase) CreateInstance(ctx context.Context, userID, orgID string, in
 	if err != nil {
 		return Outcome{}, err
 	}
+	if tpl.WithdrawnAt != nil {
+		return Outcome{}, model.ErrTemplateWithdrawn
+	}
 	revision := in.Revision
 	if revision == 0 {
 		revision = tpl.LatestRevision
 	}
-	body, err := uc.revision(ctx, tpl.ID, revision)
+	body, spec, err := uc.revision(ctx, tpl.ID, revision)
 	if err != nil {
 		return Outcome{}, err
 	}
-	values, err := template.Resolve(body, template.Stored{}, template.Given{Params: in.Params, Secrets: in.Secrets})
+	name := in.Name
+	if name == "" {
+		name = tpl.Name
+	}
+	if err := template.CheckName(name); err != nil {
+		return Outcome{}, err
+	}
+	if err := template.CheckHosts(spec, in.Hosts); err != nil {
+		return Outcome{}, err
+	}
+	values, err := template.Resolve(spec, template.Stored{}, template.Given{Values: in.Values, Secrets: in.Secrets})
 	if err != nil {
 		return Outcome{}, err
 	}
-	backups, err := template.CreateBackups(body, in.Backups)
+	backups, err := template.CreateBackups(spec, in.Backups)
 	if err != nil {
 		return Outcome{}, err
 	}
 	hash := template.RequestHash(template.HashInput{
-		ServerID: in.ServerID, TemplateID: tpl.ID, Revision: in.Revision,
-		Params: in.Params, SecretNames: slices.Collect(maps.Keys(in.Secrets)), Backups: in.Backups,
+		ServerID: in.ServerID, TemplateID: tpl.ID, Revision: in.Revision, Name: in.Name,
+		Values: in.Values, SecretNames: slices.Collect(maps.Keys(in.Secrets)), Hosts: in.Hosts, Backups: in.Backups,
 	})
 	render := func(appID string) (appuc.CheckedSave, error) {
 		app, payload, err := template.Render(body, template.Input{
-			AppID: appID, Values: values, Backups: backups,
+			AppID: appID, Name: name, Values: values, Hosts: in.Hosts, Backups: backups,
 			Marker: &template.Marker{TemplateID: tpl.ID, TemplateName: tpl.Name, Revision: revision, Key: in.Key},
 		})
 		if err != nil {
@@ -96,8 +114,8 @@ func (uc *UseCase) CreateInstance(ctx context.Context, userID, orgID string, in
 	}
 	inst, inserted, err := uc.repo.ClaimInstance(ctx, model.Instance{
 		ID: util.GenerateID(), OrgID: orgID, Key: in.Key, AppID: checked.AppID(), ServerID: in.ServerID,
-		TemplateID: tpl.ID, Revision: revision, Params: values.Stored.Params, SecretNames: values.Stored.SecretNames,
-		RequestHash: hash,
+		TemplateID: tpl.ID, Revision: revision, Name: name, Params: values.Stored.Values, SecretNames: values.Stored.SecretNames,
+		Hosts: in.Hosts, RequestHash: hash,
 	})
 	if err != nil {
 		return Outcome{}, err
@@ -106,7 +124,7 @@ func (uc *UseCase) CreateInstance(ctx context.Context, userID, orgID string, in
 		// Another create claimed the key in between.
 		return uc.replay(ctx, userID, inst, tpl.Name, hash, revision, values, render)
 	}
-	return uc.dispatchSave(ctx, userID, inst, checked, revision, values)
+	return uc.dispatchSave(ctx, userID, inst, checked, revision, values, in.Hosts)
 }
 
 // replay answers a create whose key is taken.
@@ -124,17 +142,17 @@ func (uc *UseCase) replay(ctx context.Context, userID string, inst model.Instanc
 	if err != nil {
 		return Outcome{}, err
 	}
-	return uc.dispatchSave(ctx, userID, inst, checked, revision, values)
+	return uc.dispatchSave(ctx, userID, inst, checked, revision, values, inst.Hosts)
 }
 
 // dispatchSave dispatches a rendered save; its confirmation records the
-// revision and values (state created), a failure the error.
-func (uc *UseCase) dispatchSave(ctx context.Context, userID string, inst model.Instance, checked appuc.CheckedSave, revision int64, values template.Values) (Outcome, error) {
+// revision, values and hosts (state created), a failure the error.
+func (uc *UseCase) dispatchSave(ctx context.Context, userID string, inst model.Instance, checked appuc.CheckedSave, revision int64, values template.Values, hosts []model.InstanceHost) (Outcome, error) {
 	// The hook lives as long as the result is awaited: it keeps only what
 	// it records, not the secret ciphertexts.
 	id, stored := inst.ID, values.Stored
 	observer, done := uc.observe(inst, func(ctx context.Context) error {
-		return uc.repo.MarkSaved(ctx, id, revision, stored.Params, stored.SecretNames, nil)
+		return uc.repo.MarkSaved(ctx, id, revision, stored.Values, stored.SecretNames, hosts)
 	})
 	requestID, err := uc.apps.DispatchSave(ctx, userID, checked, observer)
 	return outcome(requestID, err, done, uc.reread(inst))
@@ -147,6 +165,25 @@ func (uc *UseCase) reread(inst model.Instance) func(ctx context.Context) (Instan
 	}
 }
 
+// appName is the name a render of inst's app uses: the app's current name
+// in the API's app list (a rename since the create counts), else the name
+// the create asked for, else fallback (the template's).
+func (uc *UseCase) appName(ctx context.Context, inst model.Instance, fallback string) string {
+	if apps, err := uc.apps.GetApps(ctx, inst.ServerID); err == nil {
+		for _, a := range apps {
+			if a.ID == inst.AppID && a.Name != "" {
+				return a.Name
+			}
+		}
+	} else {
+		uc.log.Error("instance: read the app's name", "error", err, "app_id", inst.AppID)
+	}
+	if inst.Name != "" {
+		return inst.Name
+	}
+	return fallback
+}
+
 // GetInstance returns the instance with key on serverID (a deleted one
 // too: its key stays retired).
 func (uc *UseCase) GetInstance(ctx context.Context, orgID, serverID, key string) (InstanceView, error) {
@@ -161,8 +198,8 @@ func (uc *UseCase) GetInstance(ctx context.Context, orgID, serverID, key string)
 }
 
 // ListInstances lists the org's pending and created instances, narrowed
-// to a server and/or a template when given (another org's template is
-// model.ErrTemplateNotFound).
+// to a server and/or a template when given (a template the org can't read
+// is model.ErrTemplateNotFound).
 func (uc *UseCase) ListInstances(ctx context.Context, orgID, serverID, templateID string) (InstanceList, error) {
 	names, err := uc.repo.ListTemplateNames(ctx, orgID)
 	if err != nil {
````

Modify `internal/domain/usecase/templates/lifecycle.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/usecase/templates/lifecycle.go b/internal/domain/usecase/templates/lifecycle.go
--- a/internal/domain/usecase/templates/lifecycle.go
+++ b/internal/domain/usecase/templates/lifecycle.go
@@ -46,7 +46,8 @@ func (uc *UseCase) DeleteInstance(ctx context.Context, userID, orgID, serverID,
 }
 
 // UnlinkInstance saves the app as an ordinary app (no marker, no template
-// id) at its current revision and values; on success the key is free.
+// id) at its current revision, values and hostnames; on success the key is
+// free.
 func (uc *UseCase) UnlinkInstance(ctx context.Context, userID, orgID, serverID, key string) (Outcome, error) {
 	inst, err := uc.live(ctx, orgID, serverID, key)
 	if err != nil {
@@ -55,15 +56,18 @@ func (uc *UseCase) UnlinkInstance(ctx context.Context, userID, orgID, serverID,
 	if inst.State != model.InstanceCreated {
 		return Outcome{}, model.ErrInstanceNotCreated
 	}
-	body, err := uc.revision(ctx, inst.TemplateID, inst.Revision)
+	body, spec, err := uc.revision(ctx, inst.TemplateID, inst.Revision)
 	if err != nil {
 		return Outcome{}, err
 	}
-	values, err := template.Resolve(body, template.Stored{Params: inst.Params, SecretNames: inst.SecretNames}, template.Given{})
+	values, err := template.Resolve(spec, template.Stored{Values: inst.Params, SecretNames: inst.SecretNames}, template.Given{})
 	if err != nil {
 		return Outcome{}, err
 	}
-	app, payload, err := template.Render(body, template.Input{AppID: inst.AppID, Values: values})
+	name := uc.templateName(ctx, orgID, inst.TemplateID)
+	app, payload, err := template.Render(body, template.Input{
+		AppID: inst.AppID, Name: uc.appName(ctx, inst, name), Values: values, Hosts: inst.Hosts,
+	})
 	if err != nil {
 		return Outcome{}, err
 	}
@@ -79,7 +83,6 @@ func (uc *UseCase) UnlinkInstance(ctx context.Context, userID, orgID, serverID,
 	if err != nil {
 		return Outcome{}, err
 	}
-	name := uc.templateName(ctx, orgID, inst.TemplateID)
 	final := func(ctx context.Context) (InstanceView, error) {
 		// The key no longer finds an unlinked instance: read it by id.
 		got, err := uc.repo.GetInstanceByID(ctx, id)
````

Modify `internal/domain/usecase/templates/templates.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/usecase/templates/templates.go b/internal/domain/usecase/templates/templates.go
--- a/internal/domain/usecase/templates/templates.go
+++ b/internal/domain/usecase/templates/templates.go
@@ -3,8 +3,10 @@ package templates
 import (
 	"context"
 	"encoding/json"
+	"errors"
 	"fmt"
 
+	"winterflow/internal/domain/compose/xwinterflow"
 	"winterflow/internal/domain/model"
 	"winterflow/internal/domain/template"
 	"winterflow/pkg/util"
@@ -23,30 +25,36 @@ type SaveInput struct {
 // maxNote caps a revision note.
 const maxNote = 1000
 
-// SaveTemplate validates the body and stores it as a new revision. The
-// warnings name file references nothing provides; they are computed before
-// the write, so an error never follows a stored revision.
+// SaveTemplate validates the body and stores it as a new revision of an
+// org template (a catalog one is model.ErrCatalogReadOnly). The warnings
+// are computed before the write, so an error never follows a stored
+// revision.
 func (uc *UseCase) SaveTemplate(ctx context.Context, userID, orgID string, in SaveInput) (SavedTemplate, error) {
+	if in.ID != "" {
+		if err := uc.requireOwn(ctx, orgID, in.ID); err != nil {
+			return SavedTemplate{}, err
+		}
+	}
 	if err := template.ValidateName(in.Name); err != nil {
 		return SavedTemplate{}, err
 	}
 	if len(in.Note) > maxNote {
 		return SavedTemplate{}, template.Invalid("note", "at most %d characters", maxNote)
 	}
-	if err := template.Validate(in.Body); err != nil {
+	spec, err := template.Validate(in.Body)
+	if err != nil {
 		return SavedTemplate{}, err
 	}
 	raw, err := template.Encode(in.Body)
 	if err != nil {
 		return SavedTemplate{}, err
 	}
-	shared, err := uc.sharedNames(ctx, orgID)
+	warnings, err := uc.warnings(ctx, orgID, in.Body, spec)
 	if err != nil {
 		return SavedTemplate{}, err
 	}
 	rev := model.TemplateRevision{Body: raw, Note: in.Note, CreatedBy: userID}
-	out := SavedTemplate{ID: in.ID, Name: in.Name}
-	out.Warnings = template.Warnings(in.Body, func(name string) bool { return shared[name] })
+	out := SavedTemplate{ID: in.ID, Name: in.Name, Warnings: warnings}
 	if in.ID == "" {
 		if in.BaseRevision != 0 {
 			return SavedTemplate{}, template.Invalid("base_revision", "a new template has none (0)")
@@ -62,6 +70,57 @@ func (uc *UseCase) SaveTemplate(ctx context.Context, userID, orgID string, in Sa
 	return out, nil
 }
 
+// InspectTemplate reads a body as save-template would, without storing
+// anything: what its compose files declare (as far as they parse) and the
+// first problem a save would refuse ("" when none). The editor's Detected
+// panel follows the files with it.
+func (uc *UseCase) InspectTemplate(ctx context.Context, orgID string, b template.Body) (Inspection, error) {
+	spec, err := template.Validate(b)
+	if err == nil {
+		_, err = template.Encode(b)
+	}
+	out := Inspection{}
+	if err != nil {
+		if !errors.Is(err, model.ErrRejected) {
+			return Inspection{}, err
+		}
+		out.Problem = err.Error()
+		// What parses still shows: a bad summary leaves the block readable.
+		if spec, err = template.Parse(b); err != nil {
+			spec = xwinterflow.Spec{}
+		}
+	}
+	warnings, err := uc.warnings(ctx, orgID, b, spec)
+	if err != nil {
+		return Inspection{}, err
+	}
+	out.Detected = template.Detect(spec, warnings)
+	return out, nil
+}
+
+// warnings are what a save of b points out (template.Warnings, the org's
+// shared variables counting as provided).
+func (uc *UseCase) warnings(ctx context.Context, orgID string, b template.Body, spec xwinterflow.Spec) ([]string, error) {
+	shared, err := uc.sharedNames(ctx, orgID)
+	if err != nil {
+		return nil, err
+	}
+	return template.Warnings(b, spec, func(name string) bool { return shared[name] }), nil
+}
+
+// requireOwn refuses a change to a catalog template (model.ErrCatalogReadOnly,
+// a 403); one the org can't read is model.ErrTemplateNotFound.
+func (uc *UseCase) requireOwn(ctx context.Context, orgID, id string) error {
+	t, err := uc.repo.GetTemplate(ctx, orgID, id)
+	if err != nil {
+		return err
+	}
+	if t.Catalog() {
+		return model.ErrCatalogReadOnly
+	}
+	return nil
+}
+
 // sharedNames are the org's shared variable names.
 func (uc *UseCase) sharedNames(ctx context.Context, orgID string) (map[string]bool, error) {
 	out := map[string]bool{}
@@ -78,7 +137,8 @@ func (uc *UseCase) sharedNames(ctx context.Context, orgID string) (map[string]bo
 	return out, nil
 }
 
-// ListTemplates lists the org's templates with instance counts.
+// ListTemplates lists the org's templates, then the catalog's, with the
+// org's instance counts.
 func (uc *UseCase) ListTemplates(ctx context.Context, orgID string) (TemplateList, error) {
 	list, err := uc.repo.ListTemplates(ctx, orgID)
 	if err != nil {
@@ -87,13 +147,15 @@ func (uc *UseCase) ListTemplates(ctx context.Context, orgID string) (TemplateLis
 	out := TemplateList{Templates: make([]TemplateSummary, 0, len(list))}
 	for _, t := range list {
 		out.Templates = append(out.Templates, TemplateSummary{
-			ID: t.ID, Name: t.Name, LatestRevision: t.LatestRevision, Instances: t.Instances, Behind: t.Behind, UpdatedAt: t.UpdatedAt,
+			ID: t.ID, Source: t.Source, Slug: t.Slug, Name: t.Name, LatestRevision: t.LatestRevision,
+			Instances: t.Instances, Behind: t.Behind, WithdrawnAt: t.WithdrawnAt, UpdatedAt: t.UpdatedAt,
 		})
 	}
 	return out, nil
 }
 
-// GetTemplate returns a revision (0: the latest) with the revision list.
+// GetTemplate returns a revision (0: the latest) with what its compose
+// files declare and the revision list.
 func (uc *UseCase) GetTemplate(ctx context.Context, orgID, id string, revision int64) (TemplateView, error) {
 	t, err := uc.repo.GetTemplate(ctx, orgID, id)
 	if err != nil {
@@ -110,13 +172,22 @@ func (uc *UseCase) GetTemplate(ctx context.Context, orgID, id string, revision i
 	if err != nil {
 		return TemplateView{}, err
 	}
+	// A stored body passed the checks of its day; one a later parser
+	// refuses still reads, its problem shown as a warning.
+	detected := template.Detect(xwinterflow.Spec{}, nil)
+	if spec, err := template.Parse(body); err != nil {
+		detected.Warnings = []string{err.Error()}
+	} else {
+		detected = template.Detect(spec, spec.Warnings)
+	}
 	revs, err := uc.repo.ListRevisions(ctx, id)
 	if err != nil {
 		return TemplateView{}, err
 	}
 	out := TemplateView{
-		ID: t.ID, Name: t.Name, LatestRevision: t.LatestRevision, Revision: rev.Revision, Body: body,
-		Note: rev.Note, CreatedAt: rev.CreatedAt, CreatedBy: rev.CreatedBy, Revisions: make([]TemplateRevisionInfo, 0, len(revs)),
+		ID: t.ID, Source: t.Source, Slug: t.Slug, Name: t.Name, LatestRevision: t.LatestRevision, WithdrawnAt: t.WithdrawnAt,
+		Revision: rev.Revision, Body: body, Detected: detected, Note: rev.Note, CreatedAt: rev.CreatedAt, CreatedBy: rev.CreatedBy,
+		Revisions: make([]TemplateRevisionInfo, 0, len(revs)),
 	}
 	for _, r := range revs {
 		out.Revisions = append(out.Revisions, TemplateRevisionInfo{Revision: r.Revision, Note: r.Note, CreatedAt: r.CreatedAt, CreatedBy: r.CreatedBy})
@@ -124,13 +195,22 @@ func (uc *UseCase) GetTemplate(ctx context.Context, orgID, id string, revision i
 	return out, nil
 }
 
-// revision loads and decodes one stored revision's body.
-func (uc *UseCase) revision(ctx context.Context, templateID string, revision int64) (template.Body, error) {
+// revision loads one stored revision's body and what its compose files
+// declare.
+func (uc *UseCase) revision(ctx context.Context, templateID string, revision int64) (template.Body, xwinterflow.Spec, error) {
 	rev, err := uc.repo.GetRevision(ctx, templateID, revision)
 	if err != nil {
-		return template.Body{}, err
+		return template.Body{}, xwinterflow.Spec{}, err
+	}
+	body, err := decodeRevision(rev)
+	if err != nil {
+		return template.Body{}, xwinterflow.Spec{}, err
+	}
+	spec, err := template.Parse(body)
+	if err != nil {
+		return template.Body{}, xwinterflow.Spec{}, fmt.Errorf("template %s r%d: %w", templateID, revision, err)
 	}
-	return decodeRevision(rev)
+	return body, spec, nil
 }
 
 // decodeRevision decodes a stored revision's body.
@@ -142,7 +222,11 @@ func decodeRevision(rev model.TemplateRevision) (template.Body, error) {
 	return body, nil
 }
 
-// DeleteTemplate deletes a template without live instances.
+// DeleteTemplate deletes an org template without live instances (a
+// catalog one is model.ErrCatalogReadOnly).
 func (uc *UseCase) DeleteTemplate(ctx context.Context, orgID, id string) error {
+	if err := uc.requireOwn(ctx, orgID, id); err != nil {
+		return err
+	}
 	return uc.repo.DeleteTemplate(ctx, orgID, id)
 }
````

Modify `internal/domain/usecase/templates/update.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/usecase/templates/update.go b/internal/domain/usecase/templates/update.go
--- a/internal/domain/usecase/templates/update.go
+++ b/internal/domain/usecase/templates/update.go
@@ -8,14 +8,15 @@ import (
 	"winterflow/internal/domain/template"
 )
 
-// ErrBackupsNotPending is a backups override sent for an instance the
-// agent already confirmed: its backups belong to the app's Backups tab.
+// ErrBackupsNotPending is a backups setting sent for an instance the agent
+// already confirmed: its backups belong to the app's Backups tab.
 var ErrBackupsNotPending = model.Rejected("backups can only be set while the instance is pending; change them in the app's Backups tab")
 
-// ErrNeedsValues marks an update or upgrade the values at hand can't
-// render (a newly required parameter without a default, a stored value the
-// revision's pattern refuses): an "Upgrade all" rollout skips such an
-// instance. The error's message is the validation's.
+// ErrNeedsValues marks an update or upgrade the instance's stored choices
+// can't render: a newly required variable without a value, a stored value
+// the revision's pattern refuses, a hostname whose expose entry the
+// revision no longer has. An "Upgrade all" rollout skips such an instance.
+// The error's message is the validation's.
 var ErrNeedsValues = errors.New("the instance needs new values for this revision")
 
 // needsValues is a values error that also matches ErrNeedsValues.
@@ -29,11 +30,13 @@ type UpdateInput struct {
 	ServerID string
 	Key      string
 	Revision int64
-	Params   map[string]string
+	Values   map[string]string
 	Secrets  map[string]string
-	// Backups overrides the storage of template backup defaults by name;
-	// accepted only while the instance is pending.
-	Backups map[string]string
+	// Hosts, when non-nil, replace the instance's hostnames.
+	Hosts *[]model.InstanceHost
+	// Backups are backup settings by name; accepted only while the
+	// instance is pending.
+	Backups map[string]template.BackupChoice
 }
 
 // live returns the org's instance with key on serverID: model.ErrInstanceNotFound
@@ -54,14 +57,15 @@ func (uc *UseCase) live(ctx context.Context, orgID, serverID, key string) (model
 }
 
 // UpdateInstance re-renders the instance's revision with new values (merged
-// over the stored ones) and redeploys; with in.Revision set it moves to
-// that revision (an upgrade), adding the backup defaults of backups the
-// new revision brings.
+// over the stored ones) and hostnames, and redeploys; with in.Revision set
+// it moves to that revision (an upgrade). An upgrade sends no backups
+// list: the app keeps its settings, and a backup the revision adds starts
+// off.
 //
 // A pending instance was never confirmed by the agent: the agent holds none
 // of its secrets and no backups list. Its render therefore takes every
 // secret from the caller (none is kept) and sends the whole backups list
-// (the template defaults plus in.Backups) instead of marker defaults.
+// (in.Backups).
 func (uc *UseCase) UpdateInstance(ctx context.Context, userID, orgID string, in UpdateInput) (Outcome, error) {
 	inst, err := uc.live(ctx, orgID, in.ServerID, in.Key)
 	if err != nil {
@@ -76,34 +80,35 @@ func (uc *UseCase) UpdateInstance(ctx context.Context, userID, orgID string, in
 		return Outcome{}, err
 	}
 	revision := inst.Revision
-	upgrade := in.Revision != 0 && in.Revision != inst.Revision
-	if upgrade {
+	if in.Revision != 0 {
 		revision = in.Revision
 	}
-	body, err := uc.revision(ctx, tpl.ID, revision)
+	body, spec, err := uc.revision(ctx, tpl.ID, revision)
 	if err != nil {
 		return Outcome{}, err
 	}
-	marker := &template.Marker{TemplateID: tpl.ID, TemplateName: tpl.Name, Revision: revision, Key: inst.Key}
-	if upgrade && !pending {
-		// The current revision matters only here: which backups are new.
-		current, err := uc.revision(ctx, tpl.ID, inst.Revision)
-		if err != nil {
-			return Outcome{}, err
-		}
-		if marker.BackupDefaults, err = template.NewBackupDefaults(current, body); err != nil {
-			return Outcome{}, err
+	hosts := inst.Hosts
+	if in.Hosts != nil {
+		hosts = *in.Hosts
+	}
+	if err := template.CheckHosts(spec, hosts); err != nil {
+		if in.Hosts == nil {
+			return Outcome{}, needsValues{err} // the stored hosts no longer fit
 		}
+		return Outcome{}, err
+	}
+	stored := template.Stored{Values: inst.Params, SecretNames: inst.SecretNames}
+	input := template.Input{
+		AppID: inst.AppID, Name: uc.appName(ctx, inst, tpl.Name), Hosts: hosts,
+		Marker: &template.Marker{TemplateID: tpl.ID, TemplateName: tpl.Name, Revision: revision, Key: inst.Key},
 	}
-	stored := template.Stored{Params: inst.Params, SecretNames: inst.SecretNames}
-	input := template.Input{AppID: inst.AppID, Marker: marker}
 	if pending {
 		stored.SecretNames = nil
-		if input.Backups, err = template.CreateBackups(body, in.Backups); err != nil {
+		if input.Backups, err = template.CreateBackups(spec, in.Backups); err != nil {
 			return Outcome{}, err
 		}
 	}
-	values, err := template.Resolve(body, stored, template.Given{Params: in.Params, Secrets: in.Secrets})
+	values, err := template.Resolve(spec, stored, template.Given{Values: in.Values, Secrets: in.Secrets})
 	if err != nil {
 		return Outcome{}, needsValues{err}
 	}
@@ -116,5 +121,5 @@ func (uc *UseCase) UpdateInstance(ctx context.Context, userID, orgID string, in
 	if err != nil {
 		return Outcome{}, err
 	}
-	return uc.dispatchSave(ctx, userID, inst, checked, revision, values)
+	return uc.dispatchSave(ctx, userID, inst, checked, revision, values, hosts)
 }
````

Modify `internal/domain/usecase/templates/views.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/usecase/templates/views.go b/internal/domain/usecase/templates/views.go
--- a/internal/domain/usecase/templates/views.go
+++ b/internal/domain/usecase/templates/views.go
@@ -8,12 +8,15 @@ import (
 
 // TemplateSummary is one row of GET get-templates.
 type TemplateSummary struct {
-	ID             string    `json:"id"`
-	Name           string    `json:"name"`
-	LatestRevision int64     `json:"latest_revision"`
-	Instances      int64     `json:"instances" description:"Pending and created instances."`
-	Behind         int64     `json:"behind" description:"Instances on a revision older than the latest."`
-	UpdatedAt      time.Time `json:"updated_at"`
+	ID             string     `json:"id"`
+	Source         string     `json:"source" enum:"org,catalog" description:"org: the organization's own (editable); catalog: the same for every organization (read-only)."`
+	Slug           string     `json:"slug" description:"A catalog template's id in the catalog; empty for an org template."`
+	Name           string     `json:"name"`
+	LatestRevision int64      `json:"latest_revision"`
+	Instances      int64      `json:"instances" description:"The org's pending and created instances."`
+	Behind         int64      `json:"behind" description:"Of those, the ones on a revision older than the latest."`
+	WithdrawnAt    *time.Time `json:"withdrawn_at" description:"When the catalog dropped the template (listed only while the org has instances of it); null otherwise."`
+	UpdatedAt      time.Time  `json:"updated_at"`
 }
 
 // TemplateList is the data of GET get-templates.
@@ -29,14 +32,18 @@ type TemplateRevisionInfo struct {
 	CreatedBy string    `json:"created_by"`
 }
 
-// TemplateView is the data of GET get-template: one revision's body and
-// the revision list, newest first.
+// TemplateView is the data of GET get-template: one revision's body, what
+// its compose files declare, and the revision list, newest first.
 type TemplateView struct {
 	ID             string                 `json:"id"`
+	Source         string                 `json:"source" enum:"org,catalog"`
+	Slug           string                 `json:"slug"`
 	Name           string                 `json:"name"`
 	LatestRevision int64                  `json:"latest_revision"`
+	WithdrawnAt    *time.Time             `json:"withdrawn_at" description:"When the catalog dropped the template (it takes no new installs); null otherwise."`
 	Revision       int64                  `json:"revision"`
 	Body           template.Body          `json:"body"`
+	Detected       template.Detected      `json:"detected" description:"What the revision's compose files declare under x-winterflow."`
 	Note           string                 `json:"note"`
 	CreatedAt      time.Time              `json:"created_at"`
 	CreatedBy      string                 `json:"created_by"`
@@ -48,7 +55,14 @@ type SavedTemplate struct {
 	ID       string `json:"id"`
 	Name     string `json:"name"`
 	Revision int64  `json:"revision"`
-	// Warnings name file references nothing provides (a typo, or a
-	// shared variable the servers must have).
+	// Warnings are what the save points out: a variable nothing uses, a
+	// file reference nothing provides (a typo, or a shared variable the
+	// servers must have).
 	Warnings []string `json:"warnings"`
 }
+
+// Inspection is the data of POST inspect-template.
+type Inspection struct {
+	Detected template.Detected `json:"detected" description:"What the compose files declare, as far as they parse."`
+	Problem  string            `json:"problem" description:"The first thing save-template would refuse; empty when none."`
+}
````

- [ ] **Step 4: Run them and see them pass**

```bash
go test ./internal/domain/usecase/templates/ ./internal/domain/usecase/rollouts/ ./internal/app/web/handler/templates/
go build ./...
```

Expected: PASS — the packages pass and the tree builds. `go vet ./...` still fails in `internal/app/web/contract` (its tests use the old request fields) until Task 5.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat(templates): instances fill in x-winterflow variables, hostnames and backups" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 4: Rollouts: an upgrade's scope is the org and the template

A `template_upgrade` rollout is stored under `<org_id>:<template_id>`, so two orgs upgrading the same catalog template don't block each other. The API keeps naming the scope by the template id (`get-rollouts`' `scope_key` query and the view's `scope_key`), so the web needs no change.

**Files:**
- Modify: `internal/domain/usecase/rollouts/upgrade.go`
- Modify: `internal/domain/usecase/rollouts/upgrade_test.go`
- Modify: `internal/domain/usecase/rollouts/usecase.go`

**Interfaces:**
- Consumes: `model.UpgradeScopeKey` (Task 1).
- Produces: `QueueUpgrade` stores `ScopeKey: model.UpgradeScopeKey(orgID, tpl.ID)`; `List(ctx, orgID, model.RolloutTemplateUpgrade, templateID)` reads the stored key; `viewOf` reports a template upgrade's `ScopeKey` as its scope's `TemplateID`.

- [ ] **Step 1: Write the failing tests**

Modify `internal/domain/usecase/rollouts/upgrade_test.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/usecase/rollouts/upgrade_test.go b/internal/domain/usecase/rollouts/upgrade_test.go
--- a/internal/domain/usecase/rollouts/upgrade_test.go
+++ b/internal/domain/usecase/rollouts/upgrade_test.go
@@ -93,6 +93,9 @@ func TestQueueUpgradeChecksTheTemplateAndTheScope(t *testing.T) {
 	if err != nil || v.State != "queued" || v.Kind != model.RolloutTemplateUpgrade || v.ScopeKey != tpl.ID || v.Revision != 1 || v.CreatedBy != "u1" {
 		t.Fatalf("queued = %+v, %v", v, err)
 	}
+	if stored := e.get(t, v.ID); stored.ScopeKey != model.UpgradeScopeKey(org1, tpl.ID) {
+		t.Fatalf("stored scope key = %q", stored.ScopeKey)
+	}
 	if _, err := e.uc.QueueUpgrade(ctx, "u1", org1, tpl.ID, 0); !errors.Is(err, model.ErrRolloutActive) || !errors.Is(err, model.ErrConflict) {
 		t.Fatalf("a second upgrade: %v", err)
 	}
@@ -233,3 +236,45 @@ func TestUpgradeDoesNotRepickAnInstanceItCouldNotRecord(t *testing.T) {
 		t.Fatalf("commands = %v: the instance was saved again", got)
 	}
 }
+
+// Two orgs upgrade the same catalog template at once: each rollout is the
+// org's own (scope key <org_id>:<template_id>) and moves only its
+// instances.
+func TestUpgradeOfACatalogTemplateIsPerOrg(t *testing.T) {
+	e := newEnv(t)
+	_, tu := e.withAgent(t)
+	ctx := dbtest.Ctx(t)
+	raw, err := template.Encode(shopBody("^[a-z]+$"))
+	if err != nil {
+		t.Fatal(err)
+	}
+	cat := dbtest.ID(41)
+	if err := e.tpl.CreateTemplate(ctx, model.Template{ID: cat, Source: model.TemplateSourceCatalog, Slug: "shop", Name: "Shop"},
+		model.TemplateRevision{Body: raw}); err != nil {
+		t.Fatal(err)
+	}
+	for _, c := range []struct{ org, srv string }{{org1, srv1}, {org2, srv2}} {
+		out, err := tu.CreateInstance(ctx, "u1", c.org, templates.CreateInput{ServerID: c.srv, Key: "k", TemplateID: cat, Values: map[string]string{"NAME": "acme"}})
+		if err != nil {
+			t.Fatal(err)
+		}
+		<-out.Done
+	}
+	views := map[string]model.RolloutView{}
+	for _, org := range []string{org1, org2} {
+		v, err := e.uc.QueueUpgrade(ctx, "u1", org, cat, 0)
+		if err != nil || v.ScopeKey != cat {
+			t.Fatalf("%s queues = %+v, %v", org, v, err)
+		}
+		views[org] = v
+	}
+	if _, err := e.uc.QueueUpgrade(ctx, "u1", org1, cat, 0); !errors.Is(err, model.ErrRolloutActive) {
+		t.Fatalf("org1's second upgrade: %v", err)
+	}
+	for _, org := range []string{org1, org2} {
+		list, err := e.uc.List(ctx, org, model.RolloutTemplateUpgrade, cat)
+		if err != nil || len(list) != 1 || list[0].ID != views[org].ID {
+			t.Fatalf("%s list = %+v, %v", org, list, err)
+		}
+	}
+}
````

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/domain/usecase/rollouts/ -run "TestQueueUpgradeChecksTheTemplateAndTheScope|TestUpgradeOfACatalogTemplateIsPerOrg"`
Expected: FAIL — the stored scope key is the bare template id, and org2's upgrade of the catalog template is refused as active.

- [ ] **Step 3: Implement**

Modify `internal/domain/usecase/rollouts/upgrade.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/usecase/rollouts/upgrade.go b/internal/domain/usecase/rollouts/upgrade.go
--- a/internal/domain/usecase/rollouts/upgrade.go
+++ b/internal/domain/usecase/rollouts/upgrade.go
@@ -11,10 +11,13 @@ import (
 	"winterflow/pkg/util"
 )
 
-// QueueUpgrade queues "Upgrade all": the template's created instances on a
-// revision below revision (0: the latest) move to it, oldest first, one at
-// a time. Another org's template or an unknown revision is not found; a
-// template with an upgrade still active is model.ErrRolloutActive.
+// QueueUpgrade queues "Upgrade all": the org's created instances of the
+// template on a revision below revision (0: the latest) move to it, oldest
+// first, one at a time. The rollout's scope is the org and the template
+// (model.UpgradeScopeKey), so two orgs upgrading the same catalog template
+// don't block each other. A template the org can't read or an unknown
+// revision is not found; a template the org is still upgrading is
+// model.ErrRolloutActive.
 func (uc *UseCase) QueueUpgrade(ctx context.Context, userID, orgID, templateID string, revision int64) (model.RolloutView, error) {
 	tpl, err := uc.d.Templates.GetTemplate(ctx, orgID, templateID)
 	if err != nil {
@@ -31,7 +34,8 @@ func (uc *UseCase) QueueUpgrade(ctx context.Context, userID, orgID, templateID s
 		return model.RolloutView{}, err
 	}
 	v, ok, err := uc.queue(ctx, model.Rollout{
-		ID: util.GenerateID(), OrgID: orgID, Kind: model.RolloutTemplateUpgrade, Scope: scope, ScopeKey: tpl.ID, CreatedBy: userID,
+		ID: util.GenerateID(), OrgID: orgID, Kind: model.RolloutTemplateUpgrade, Scope: scope,
+		ScopeKey: model.UpgradeScopeKey(orgID, tpl.ID), CreatedBy: userID,
 	})
 	if err == nil && !ok {
 		err = model.ErrRolloutActive
````

Modify `internal/domain/usecase/rollouts/usecase.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/usecase/rollouts/usecase.go b/internal/domain/usecase/rollouts/usecase.go
--- a/internal/domain/usecase/rollouts/usecase.go
+++ b/internal/domain/usecase/rollouts/usecase.go
@@ -171,8 +171,9 @@ func (uc *UseCase) queue(ctx context.Context, r model.Rollout) (model.RolloutVie
 }
 
 // List returns an org's rollouts of kind: the active ones first, then the
-// ended ones newest first (scopeKey narrows to one scope). Another org's
-// template, or another org as a vars_reload scope, is not found.
+// ended ones newest first (scopeKey narrows to one scope: a template id for
+// template_upgrade, the org id for vars_reload). A template the org can't
+// read, or another org as a vars_reload scope, is not found.
 func (uc *UseCase) List(ctx context.Context, orgID, kind, scopeKey string) ([]model.RolloutView, error) {
 	switch {
 	case kind != model.RolloutTemplateUpgrade && kind != model.RolloutVarsReload:
@@ -183,6 +184,7 @@ func (uc *UseCase) List(ctx context.Context, orgID, kind, scopeKey string) ([]mo
 		if _, err := uc.d.Templates.GetTemplate(ctx, orgID, scopeKey); err != nil {
 			return nil, err
 		}
+		scopeKey = model.UpgradeScopeKey(orgID, scopeKey)
 	}
 	list, err := uc.d.Repo.ListRollouts(ctx, orgID, kind, scopeKey)
 	if err != nil {
@@ -247,6 +249,9 @@ func viewOf(r model.Rollout, name string) model.RolloutView {
 	}
 	if r.Kind == model.RolloutTemplateUpgrade {
 		if s, err := upgradeScope(r); err == nil {
+			// The API names an upgrade's scope by its template; the stored
+			// key also holds the org.
+			v.ScopeKey = s.TemplateID
 			v.Revision = s.Revision
 		}
 	}
````

- [ ] **Step 4: Run them and see them pass**

```bash
go test ./internal/domain/usecase/rollouts/
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat(rollouts): an upgrade's scope is the org and the template" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 5: API: inspect-template, 403 on catalog templates, contract tests, OpenAPI

`POST /api/v1/template/inspect-template` feeds the editor's Detected panel; `model.ErrForbidden` answers 403; the contract suite drives the new bodies, values, hostnames and backups end to end, catalog templates included; `api/openapi.yaml`, the routes golden and the parity golden are regenerated.

**Files:**
- Modify: `internal/app/web/contract/instances_test.go`
- Modify: `internal/app/web/contract/matrix_test.go`
- Modify: `internal/app/web/contract/parity_test.go`
- Modify: `internal/app/web/contract/rollouts_test.go`
- Modify: `internal/app/web/contract/templates_test.go`
- Modify: `internal/app/web/handler/templates/routes.go`
- Modify: `internal/app/web/handler/templates/templates.go`
- Modify: `internal/app/web/util/response.go`
- Create: `internal/app/web/util/response_test.go`
- Regenerate: `api/openapi.yaml` (never edited by hand)
- Regenerate: `internal/app/web/contract/testdata/parity.golden.json` (never edited by hand)
- Regenerate: `internal/app/web/testdata/routes.golden` (never edited by hand)

**Interfaces:**
- Consumes: `tuc.UseCase.InspectTemplate` (Task 3), `model.ErrForbidden` (Task 1).
- Produces: route `POST /api/v1/template/inspect-template` (`InspectTemplateRequest{Body template.Body}` -> `tuc.Inspection`, errors 400); save-template and delete-template document 403; `webutil.StatusFor` maps `model.ErrForbidden` to 403 and `webutil.Fail` answers it with its message.

- [ ] **Step 1: Write the failing tests**

Modify `internal/app/web/contract/instances_test.go` (`git apply` this diff):

````diff
diff --git a/internal/app/web/contract/instances_test.go b/internal/app/web/contract/instances_test.go
--- a/internal/app/web/contract/instances_test.go
+++ b/internal/app/web/contract/instances_test.go
@@ -50,19 +50,22 @@ func newInstanceEnv(t *testing.T) instanceEnv {
 	return instanceEnv{e: e, owner: owner, sid: sid, tplID: saved.ID, secret: cryptotest.Ciphertext(t, "s3cret")}
 }
 
-// create is a waited create of key with NAME and PORT.
-func (ie instanceEnv) create(key, name, port string) htpl.CreateInstanceRequest {
+// create is a waited create of key with NAME, the app name shop-<name>,
+// the hostname <name>.example.test on app:8080 and backup db on "disk".
+func (ie instanceEnv) create(key, name string) htpl.CreateInstanceRequest {
 	return htpl.CreateInstanceRequest{
-		ServerID: ie.sid, Key: key, TemplateID: ie.tplID, Parameters: map[string]string{"NAME": name, "PORT": port},
-		Secrets: map[string]string{"SECRET_KEY": ie.secret}, Wait: true,
+		ServerID: ie.sid, Key: key, TemplateID: ie.tplID, Name: "shop-" + name, Values: map[string]string{"NAME": name},
+		Secrets: map[string]string{"SECRET_KEY": ie.secret},
+		Hosts:   []model.InstanceHost{{Host: name + ".example.test", Expose: "app:8080"}},
+		Backups: map[string]template.BackupChoice{"db": {Enabled: true, Storage: "disk"}}, Wait: true,
 	}
 }
 
 // created creates key and returns the waited result.
-func (ie instanceEnv) created(t *testing.T, key, name, port string) htpl.InstanceResult {
+func (ie instanceEnv) created(t *testing.T, key, name string) htpl.InstanceResult {
 	t.Helper()
 	var res htpl.InstanceResult
-	r := ie.owner.post(t, "/api/v1/instance/create-instance", ie.create(key, name, port)).expect(t, http.StatusOK)
+	r := ie.owner.post(t, "/api/v1/instance/create-instance", ie.create(key, name)).expect(t, http.StatusOK)
 	noSecrets(t, string(r.Body))
 	r.data(t, &res)
 	return res
@@ -83,20 +86,26 @@ func TestInstanceCreate(t *testing.T) {
 	ie := newInstanceEnv(t)
 	e, owner, sid := ie.e, ie.owner, ie.sid
 
-	res := ie.created(t, "tenant-1", "acme", "9001")
+	res := ie.created(t, "tenant-1", "acme")
 	appID := res.Instance.AppID
 	t.Run("create with wait answers the instance", func(t *testing.T) {
 		if res.Instance.State != "created" || res.Instance.Revision != 1 || res.Instance.Secrets["SECRET_KEY"] != command.EncryptedPlaceholder ||
-			res.Instance.Parameters["NAME"] != "acme" || res.Instance.TemplateName != "shop" || res.RequestID == "" {
+			res.Instance.Values["NAME"] != "acme" || res.Instance.TemplateName != "shop" || res.Instance.Name != "shop-acme" ||
+			len(res.Instance.Hosts) != 1 || res.RequestID == "" {
 			t.Fatalf("result = %+v", res)
 		}
 		req := lastSave(t, e)
 		var cfg map[string]json.RawMessage
 		_ = json.Unmarshal(req.App.Config, &cfg)
 		if req.App.AppID != appID || string(cfg["template_id"]) != `"`+ie.tplID+`"` ||
-			string(cfg["backups"]) != `[{"name":"db","enabled":true,"storage":"disk"}]` {
+			string(cfg["backups"]) != `[{"name":"db","enabled":true,"storage":"disk"}]` ||
+			string(cfg["ingress"]) != `{"domains":[{"domain":"acme.example.test","expose":"app:8080","ssl":false}],"redirects":[]}` {
 			t.Fatalf("dispatched config = %s", req.App.Config)
 		}
+		// One app variable per value; VERSION was left to the compose file.
+		if len(req.App.Variables) != 2 {
+			t.Fatalf("variables = %+v", req.App.Variables)
+		}
 		apps := listApps(t, owner, sid)
 		if len(apps) != 1 || apps[0].ID != appID || apps[0].Name != "shop-acme" || apps[0].TemplateID != ie.tplID || len(apps[0].Domains) != 1 {
 			t.Fatalf("apps = %+v", apps)
@@ -110,20 +119,22 @@ func TestInstanceCreate(t *testing.T) {
 	t.Run("a replay makes no second app; other values are a conflict", func(t *testing.T) {
 		n := len(e.agent.seen(command.TypeAppSave))
 		var again htpl.InstanceResult
-		replay := ie.create("tenant-1", "acme", "9001")
+		replay := ie.create("tenant-1", "acme")
 		replay.Secrets = map[string]string{"SECRET_KEY": cryptotest.Ciphertext(t, "s3cret")} // new bytes, the same secret
 		owner.post(t, "/api/v1/instance/create-instance", replay).expect(t, http.StatusOK).data(t, &again)
 		if again.Instance.AppID != appID || again.RequestID != "" || len(e.agent.seen(command.TypeAppSave)) != n {
 			t.Fatalf("replay = %+v", again)
 		}
-		owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-1", "acme", "9002")).expect(t, http.StatusConflict)
+		other := ie.create("tenant-1", "acme")
+		other.Values["VERSION"] = "2.0"
+		owner.post(t, "/api/v1/instance/create-instance", other).expect(t, http.StatusConflict)
 	})
 	t.Run("a host another app holds is refused before the key is claimed", func(t *testing.T) {
-		owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-x", "acme", "9009")).expect(t, http.StatusBadRequest)
+		owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-x", "acme")).expect(t, http.StatusBadRequest)
 		owner.get(t, "/api/v1/instance/get-instance", url.Values{"server_id": {sid}, "key": {"tenant-x"}}).expect(t, http.StatusNotFound)
 	})
 	t.Run("without wait the result arrives over SSE", func(t *testing.T) {
-		in := ie.create("tenant-2", "bravo", "9002")
+		in := ie.create("tenant-2", "bravo")
 		in.Wait = false
 		var saved command.SaveAppResponse
 		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/instance/create-instance", Body: in}, &saved)
@@ -133,7 +144,7 @@ func TestInstanceCreate(t *testing.T) {
 	})
 	t.Run("a failed deploy answers 502; the same request retries it", func(t *testing.T) {
 		e.agent.fail(command.TypeAppSave, "pull access denied")
-		r := owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-3", "charlie", "9003")).expect(t, http.StatusBadGateway)
+		r := owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-3", "charlie")).expect(t, http.StatusBadGateway)
 		if !strings.Contains(string(r.Body), "pull access denied") {
 			t.Fatalf("502 body = %s", r.Body)
 		}
@@ -142,7 +153,7 @@ func TestInstanceCreate(t *testing.T) {
 		}
 		e.agent.on(command.TypeAppSave, defaultScript(command.TypeAppSave))
 		var retried htpl.InstanceResult
-		owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-3", "charlie", "9003")).expect(t, http.StatusOK).data(t, &retried)
+		owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-3", "charlie")).expect(t, http.StatusOK).data(t, &retried)
 		if retried.Instance.State != "created" || retried.Instance.LastError != "" {
 			t.Fatalf("retried = %+v", retried.Instance)
 		}
@@ -163,15 +174,25 @@ func TestInstanceCreate(t *testing.T) {
 			t.Fatalf("templates = %+v", tpls)
 		}
 	})
-	t.Run("parameters are checked", func(t *testing.T) {
-		bad := ie.create("tenant-4", "UPPER", "9004")
+	t.Run("values and hostnames are checked", func(t *testing.T) {
+		bad := ie.create("tenant-4", "UPPER")
+		bad.Hosts = nil
 		r := owner.post(t, "/api/v1/instance/create-instance", bad).expect(t, http.StatusBadRequest)
-		if !strings.Contains(string(r.Body), "parameters.NAME") {
+		if !strings.Contains(string(r.Body), "variable NAME") {
 			t.Fatalf("body = %s", r.Body)
 		}
-		bad = ie.create("tenant-4", "delta", "9004")
+		bad = ie.create("tenant-4", "delta")
 		bad.Secrets = nil
 		owner.post(t, "/api/v1/instance/create-instance", bad).expect(t, http.StatusBadRequest)
+		bad = ie.create("tenant-4", "delta")
+		bad.Hosts = []model.InstanceHost{{Host: "delta.example.test", Expose: "app:9999"}}
+		r = owner.post(t, "/api/v1/instance/create-instance", bad).expect(t, http.StatusBadRequest)
+		if !strings.Contains(string(r.Body), "hosts[0].expose") {
+			t.Fatalf("body = %s", r.Body)
+		}
+		bad = ie.create("tenant-4", "delta")
+		bad.Hosts = []model.InstanceHost{{Host: "delta.elsewhere.test", Expose: "app:8080"}}
+		owner.post(t, "/api/v1/instance/create-instance", bad).expect(t, http.StatusBadRequest)
 	})
 }
 
@@ -179,9 +200,9 @@ func TestInstanceCreate(t *testing.T) {
 func TestInstancesOfAnotherOrgAreNotFound(t *testing.T) {
 	t.Parallel()
 	ie := newInstanceEnv(t)
-	ie.created(t, "tenant-1", "acme", "9001")
+	ie.created(t, "tenant-1", "acme")
 	mallory, foreign := ie.e.foreignClient(t)
-	in := ie.create("k", "acme", "9001")
+	in := ie.create("k", "acme")
 	in.ServerID = foreign
 	mallory.post(t, "/api/v1/instance/create-instance", in).expect(t, http.StatusNotFound)
 	mallory.get(t, "/api/v1/instance/get-instances", url.Values{"template_id": {ie.tplID}}).expect(t, http.StatusNotFound)
@@ -198,13 +219,13 @@ func TestInstanceUpdateAndUpgrade(t *testing.T) {
 	t.Parallel()
 	ie := newInstanceEnv(t)
 	e, owner, sid := ie.e, ie.owner, ie.sid
-	ie.created(t, "tenant-1", "acme", "9001")
+	ie.created(t, "tenant-1", "acme")
 
-	t.Run("update without wait answers 202 and keeps the secret", func(t *testing.T) {
+	t.Run("update without wait answers 202 and keeps the secret and the hosts", func(t *testing.T) {
 		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/instance/update-instance",
-			Body: htpl.UpdateInstanceRequest{ServerID: sid, Key: "tenant-1", Parameters: map[string]string{"VERSION": "2.0"}}}, nil)
+			Body: htpl.UpdateInstanceRequest{ServerID: sid, Key: "tenant-1", Values: map[string]string{"VERSION": "2.0"}}}, nil)
 		got := instanceOf(t, owner, sid, "tenant-1")
-		if got.Parameters["VERSION"] != "2.0" || got.Parameters["NAME"] != "acme" {
+		if got.Values["VERSION"] != "2.0" || got.Values["NAME"] != "acme" || len(got.Hosts) != 1 {
 			t.Fatalf("after update = %+v", got)
 		}
 		save := lastSave(t, e)
@@ -213,36 +234,43 @@ func TestInstanceUpdateAndUpgrade(t *testing.T) {
 				t.Fatalf("an update re-sent the secret: %q", v.Content)
 			}
 		}
-		if strings.Contains(string(save.App.Config), `"backups"`) {
-			t.Fatalf("an update replaced the backups list: %s", save.App.Config)
+		if strings.Contains(string(save.App.Config), `"backups"`) || !strings.Contains(string(save.App.Config), `"acme.example.test"`) {
+			t.Fatalf("an update's config: %s", save.App.Config)
+		}
+	})
+	t.Run("update replaces the hosts when given", func(t *testing.T) {
+		hosts := []model.InstanceHost{{Host: "shop.example.test", Expose: "app:8080"}}
+		var res htpl.InstanceResult
+		owner.post(t, "/api/v1/instance/update-instance", htpl.UpdateInstanceRequest{ServerID: sid, Key: "tenant-1", Hosts: &hosts, Wait: true}).
+			expect(t, http.StatusOK).data(t, &res)
+		if len(res.Instance.Hosts) != 1 || res.Instance.Hosts[0].Host != "shop.example.test" {
+			t.Fatalf("hosts = %+v", res.Instance.Hosts)
 		}
 	})
 	t.Run("update checks the values", func(t *testing.T) {
 		owner.post(t, "/api/v1/instance/update-instance", htpl.UpdateInstanceRequest{ServerID: sid, Key: "tenant-1",
-			Parameters: map[string]string{"NOPE": "1"}}).expect(t, http.StatusBadRequest)
+			Values: map[string]string{"NOPE": "1"}}).expect(t, http.StatusBadRequest)
 		owner.post(t, "/api/v1/instance/update-instance", htpl.UpdateInstanceRequest{ServerID: sid, Key: "nope"}).
 			expect(t, http.StatusNotFound)
 	})
 	t.Run("a created instance's backups are changed in the Backups tab", func(t *testing.T) {
 		for _, path := range []string{"/api/v1/instance/update-instance", "/api/v1/instance/upgrade-instance"} {
 			r := owner.post(t, path, map[string]any{"server_id": sid, "key": "tenant-1", "revision": 1,
-				"backups": map[string]string{"db": "disk"}}).expect(t, http.StatusBadRequest)
+				"backups": map[string]any{"db": map[string]any{"enabled": true, "storage": "disk"}}}).expect(t, http.StatusBadRequest)
 			if !strings.Contains(string(r.Body), "backups can only be set while the instance is pending") {
 				t.Fatalf("%s body = %s", path, r.Body)
 			}
 		}
 	})
-	t.Run("upgrade adds the defaults of new backups only", func(t *testing.T) {
-		next := shopBody(strings.Replace(shopCompose, "path: shop.db }\n", "path: shop.db }\n    - { name: media, type: files, volume: data }\n", 1))
-		next.Backups["media"] = template.BackupDefault{Enabled: true, Storage: "disk", Every: "6h"}
-		next.Backups["db"] = template.BackupDefault{Enabled: true, Storage: "elsewhere"}
-		if saved := saveTemplate(t, owner, htpl.SaveTemplateRequest{ID: ie.tplID, Name: "shop", Body: next, BaseRevision: 1}); saved.Revision != 2 {
+	t.Run("upgrade keeps the values and sends no backups list", func(t *testing.T) {
+		next := strings.Replace(shopCompose, "path: shop.db }\n", "path: shop.db }\n    - { name: media, type: files, volume: data }\n", 1)
+		if saved := saveTemplate(t, owner, htpl.SaveTemplateRequest{ID: ie.tplID, Name: "shop", Body: shopBody(next), BaseRevision: 1}); saved.Revision != 2 {
 			t.Fatalf("saved = %+v", saved)
 		}
 		var res htpl.InstanceResult
 		owner.post(t, "/api/v1/instance/upgrade-instance", htpl.UpgradeInstanceRequest{ServerID: sid, Key: "tenant-1", Revision: 2, Wait: true}).
 			expect(t, http.StatusOK).data(t, &res)
-		if res.Instance.Revision != 2 || res.Instance.Parameters["VERSION"] != "2.0" {
+		if res.Instance.Revision != 2 || res.Instance.Values["VERSION"] != "2.0" {
 			t.Fatalf("upgraded = %+v", res.Instance)
 		}
 		var cfg struct {
@@ -250,19 +278,23 @@ func TestInstanceUpdateAndUpgrade(t *testing.T) {
 			Instance template.Marker `json:"instance"`
 		}
 		_ = json.Unmarshal(lastSave(t, e).App.Config, &cfg)
-		if cfg.Backups != nil || len(cfg.Instance.BackupDefaults) != 1 || cfg.Instance.BackupDefaults[0].Name != "media" || cfg.Instance.Revision != 2 {
+		if cfg.Backups != nil || cfg.Instance.Revision != 2 {
 			t.Fatalf("upgrade config = %s", lastSave(t, e).App.Config)
 		}
 	})
-	t.Run("an upgrade checks the new defaults' storages and revision", func(t *testing.T) {
-		next := shopBody(strings.Replace(shopCompose, "path: shop.db }\n", "path: shop.db }\n    - { name: logs, type: files, volume: data }\n", 1))
-		next.Backups["logs"] = template.BackupDefault{Enabled: true, Storage: "nowhere"}
-		saveTemplate(t, owner, htpl.SaveTemplateRequest{ID: ie.tplID, Name: "shop", Body: next, BaseRevision: 2})
+	t.Run("an upgrade a newly required value is missing for is refused", func(t *testing.T) {
+		next := strings.NewReplacer(
+			"      VERSION: \"1.0\"\n", "      VERSION: \"1.0\"\n      REGION: \"\"\n",
+			"    VERSION: {}\n", "    VERSION: {}\n    REGION: { required: true }\n",
+		).Replace(shopCompose)
+		saveTemplate(t, owner, htpl.SaveTemplateRequest{ID: ie.tplID, Name: "shop", Body: shopBody(next), BaseRevision: 2})
 		r := owner.post(t, "/api/v1/instance/upgrade-instance", htpl.UpgradeInstanceRequest{ServerID: sid, Key: "tenant-1", Revision: 3}).
 			expect(t, http.StatusBadRequest)
-		if !strings.Contains(string(r.Body), "there is no storage") || !strings.Contains(string(r.Body), "nowhere") {
+		if !strings.Contains(string(r.Body), "variable REGION: a value is required") {
 			t.Fatalf("body = %s", r.Body)
 		}
+		owner.post(t, "/api/v1/instance/upgrade-instance", htpl.UpgradeInstanceRequest{ServerID: sid, Key: "tenant-1", Revision: 3,
+			Values: map[string]string{"REGION": "eu"}, Wait: true}).expect(t, http.StatusOK)
 		owner.post(t, "/api/v1/instance/upgrade-instance", htpl.UpgradeInstanceRequest{ServerID: sid, Key: "tenant-1", Revision: 9}).
 			expect(t, http.StatusNotFound)
 	})
@@ -276,15 +308,15 @@ func TestInstanceUpdatePending(t *testing.T) {
 	e, owner, sid := ie.e, ie.owner, ie.sid
 
 	e.agent.fail(command.TypeAppSave, "pull access denied")
-	owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-p", "pending", "9100")).expect(t, http.StatusBadGateway)
-	if got := instanceOf(t, owner, sid, "tenant-p"); got.State != "pending" {
+	owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-p", "pending")).expect(t, http.StatusBadGateway)
+	if got := instanceOf(t, owner, sid, "tenant-p"); got.State != "pending" || got.Name != "shop-pending" {
 		t.Fatalf("tenant-p = %+v", got)
 	}
 	e.agent.on(command.TypeAppSave, defaultScript(command.TypeAppSave))
 
 	r := owner.post(t, "/api/v1/instance/update-instance", htpl.UpdateInstanceRequest{ServerID: sid, Key: "tenant-p",
-		Parameters: map[string]string{"VERSION": "3.0"}}).expect(t, http.StatusBadRequest)
-	if !strings.Contains(string(r.Body), "secrets.SECRET_KEY") {
+		Values: map[string]string{"VERSION": "3.0"}}).expect(t, http.StatusBadRequest)
+	if !strings.Contains(string(r.Body), "variable SECRET_KEY: a value is required") {
 		t.Fatalf("a pending update without the secret: %s", r.Body)
 	}
 	r = owner.post(t, "/api/v1/instance/update-instance", htpl.UpdateInstanceRequest{ServerID: sid, Key: "tenant-p",
@@ -295,9 +327,9 @@ func TestInstanceUpdatePending(t *testing.T) {
 	again := cryptotest.Ciphertext(t, "s3cret")
 	var res htpl.InstanceResult
 	owner.post(t, "/api/v1/instance/update-instance", htpl.UpdateInstanceRequest{ServerID: sid, Key: "tenant-p",
-		Parameters: map[string]string{"VERSION": "3.0"}, Secrets: map[string]string{"SECRET_KEY": again},
-		Backups: map[string]string{"db": "disk"}, Wait: true}).expect(t, http.StatusOK).data(t, &res)
-	if res.Instance.State != "created" || res.Instance.Parameters["VERSION"] != "3.0" {
+		Values: map[string]string{"VERSION": "3.0"}, Secrets: map[string]string{"SECRET_KEY": again},
+		Backups: map[string]template.BackupChoice{"db": {Enabled: true, Storage: "disk"}}, Wait: true}).expect(t, http.StatusOK).data(t, &res)
+	if res.Instance.State != "created" || res.Instance.Values["VERSION"] != "3.0" {
 		t.Fatalf("updated = %+v", res.Instance)
 	}
 	save := lastSave(t, e)
@@ -307,11 +339,45 @@ func TestInstanceUpdatePending(t *testing.T) {
 		}
 	}
 	var cfg struct {
+		Name    string                           `json:"name"`
 		Backups []struct{ Name, Storage string } `json:"backups"`
 	}
 	_ = json.Unmarshal(save.App.Config, &cfg)
-	if len(cfg.Backups) != 1 || cfg.Backups[0].Name != "db" || cfg.Backups[0].Storage != "disk" {
-		t.Fatalf("a pending update's backups = %s", save.App.Config)
+	if cfg.Name != "shop-pending" || len(cfg.Backups) != 1 || cfg.Backups[0].Name != "db" || cfg.Backups[0].Storage != "disk" {
+		t.Fatalf("a pending update's config = %s", save.App.Config)
+	}
+}
+
+// A catalog template is installed in the caller's org; two orgs upgrading
+// it don't block each other.
+func TestCatalogInstances(t *testing.T) {
+	t.Parallel()
+	ie := newInstanceEnv(t)
+	e, owner, sid := ie.e, ie.owner, ie.sid
+	cat := dbtest.ID(902)
+	catalogTemplate(t, e, cat, "shop", shopBody(shopCompose))
+	in := ie.create("cat-1", "cata")
+	in.TemplateID, in.Name = cat, ""
+	var res htpl.InstanceResult
+	owner.post(t, "/api/v1/instance/create-instance", in).expect(t, http.StatusOK).data(t, &res)
+	if res.Instance.TemplateID != cat || res.Instance.Name != "Shop" {
+		t.Fatalf("created = %+v", res.Instance)
+	}
+	mallory, foreign := e.foreignClient(t)
+	mine := htpl.CreateInstanceRequest{ServerID: foreign, Key: "cat-1", TemplateID: cat, Values: map[string]string{"NAME": "mall"},
+		Secrets: map[string]string{"SECRET_KEY": ie.secret}, Wait: true}
+	mallory.post(t, "/api/v1/instance/create-instance", mine).expect(t, http.StatusOK)
+	for _, c := range []*client{owner, mallory} {
+		var v model.RolloutView
+		c.post(t, "/api/v1/template/upgrade-instances", htpl.UpgradeInstancesRequest{ID: cat}).expect(t, http.StatusOK).data(t, &v)
+		if v.ScopeKey != cat {
+			t.Fatalf("queued = %+v", v)
+		}
+	}
+	var list tuc.InstanceList
+	owner.get(t, "/api/v1/instance/get-instances", url.Values{"template_id": {cat}}).expect(t, http.StatusOK).data(t, &list)
+	if len(list.Instances) != 1 || list.Instances[0].ServerID != sid {
+		t.Fatalf("the owner's instances of the catalog template = %+v", list)
 	}
 }
 
@@ -319,7 +385,7 @@ func TestInstanceLifecycle(t *testing.T) {
 	t.Parallel()
 	ie := newInstanceEnv(t)
 	e, owner, sid := ie.e, ie.owner, ie.sid
-	appID := ie.created(t, "tenant-1", "acme", "9001").Instance.AppID
+	appID := ie.created(t, "tenant-1", "acme").Instance.AppID
 
 	t.Run("control", func(t *testing.T) {
 		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/instance/control-instance",
@@ -353,7 +419,7 @@ func TestInstanceLifecycle(t *testing.T) {
 		}
 	})
 	t.Run("a freed key makes a new app; delete retires it", func(t *testing.T) {
-		again := ie.created(t, "tenant-1", "bravo", "9002")
+		again := ie.created(t, "tenant-1", "bravo")
 		if again.Instance.AppID == appID || again.RequestID == "" {
 			t.Fatalf("a freed key took over the unlinked app: %+v", again)
 		}
@@ -367,7 +433,7 @@ func TestInstanceLifecycle(t *testing.T) {
 		if req := decode[command.DeleteAppRequest](dels[len(dels)-1].Payload); req.AppID != again.Instance.AppID || !req.DeleteVolumes {
 			t.Fatalf("app.delete = %+v", req)
 		}
-		owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-1", "bravo", "9002")).expect(t, http.StatusConflict)
+		owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-1", "bravo")).expect(t, http.StatusConflict)
 		n := len(e.agent.seen(command.TypeAppDelete))
 		owner.post(t, "/api/v1/instance/delete-instance", htpl.DeleteInstanceRequest{ServerID: sid, Key: "tenant-1"}).expect(t, http.StatusOK)
 		if len(e.agent.seen(command.TypeAppDelete)) != n {
@@ -387,7 +453,7 @@ func TestInstanceAppThroughTheAppRoutes(t *testing.T) {
 	t.Parallel()
 	ie := newInstanceEnv(t)
 	e, owner, sid := ie.e, ie.owner, ie.sid
-	appID := ie.created(t, "tenant-1", "acme", "9001").Instance.AppID
+	appID := ie.created(t, "tenant-1", "acme").Instance.AppID
 	saves := len(e.agent.seen(command.TypeAppSave))
 
 	r := owner.post(t, "/api/v1/app/save-app", happ.SaveAppRequest{ServerID: sid, App: model.App{ID: appID, Name: "hijack"},
@@ -406,7 +472,7 @@ func TestInstanceAppThroughTheAppRoutes(t *testing.T) {
 	owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/app/delete-app",
 		Body: happ.DeleteAppRequest{ServerID: sid, AppID: appID}}, nil)
 	eventually(t, func() bool { return instanceOf(t, owner, sid, "tenant-1").State == "deleted" })
-	r = owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-1", "acme", "9001")).expect(t, http.StatusConflict)
+	r = owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-1", "acme")).expect(t, http.StatusConflict)
 	if !strings.Contains(string(r.Body), "this key belonged to a deleted instance") {
 		t.Fatalf("replay body = %s", r.Body)
 	}
````

Modify `internal/app/web/contract/matrix_test.go` (`git apply` this diff):

````diff
diff --git a/internal/app/web/contract/matrix_test.go b/internal/app/web/contract/matrix_test.go
--- a/internal/app/web/contract/matrix_test.go
+++ b/internal/app/web/contract/matrix_test.go
@@ -193,6 +193,7 @@ var probes = map[string]probe{
 	"GET /api/v1/template/get-template":       {malformed: bad("GET", "/api/v1/template/get-template")},
 	"POST /api/v1/template/save-template":     {malformed: bad("POST", "/api/v1/template/save-template")},
 	"POST /api/v1/template/delete-template":   {malformed: bad("POST", "/api/v1/template/delete-template")},
+	"POST /api/v1/template/inspect-template":  {malformed: bad("POST", "/api/v1/template/inspect-template")},
 	"POST /api/v1/template/upgrade-instances": {malformed: bad("POST", "/api/v1/template/upgrade-instances")},
 	"GET /api/v1/rollout/get-rollouts":        {malformed: bad("GET", "/api/v1/rollout/get-rollouts")},
 	"POST /api/v1/rollout/stop-rollout":       {malformed: bad("POST", "/api/v1/rollout/stop-rollout")},
````

Modify `internal/app/web/contract/parity_test.go` (`git apply` this diff):

````diff
diff --git a/internal/app/web/contract/parity_test.go b/internal/app/web/contract/parity_test.go
--- a/internal/app/web/contract/parity_test.go
+++ b/internal/app/web/contract/parity_test.go
@@ -137,8 +137,9 @@ func TestParity(t *testing.T) {
 
 	var tpl tuc.SavedTemplate
 	rec("save template", p.post(t, "/api/v1/template/save-template", htpl.SaveTemplateRequest{
-		Name: "parity", Body: template.Body{Files: map[string]string{"compose.yaml": "services: {app: {image: x}}\n"}, AppName: "parity-app"},
+		Name: "parity", Body: template.Body{Files: map[string]string{"compose.yaml": "services: {app: {image: x}}\n"}, Summary: "Parity"},
 	})).expect(t, http.StatusOK).data(t, &tpl)
+	rec("read template", p.get(t, "/api/v1/template/get-template", url.Values{"id": {tpl.ID}})).expect(t, http.StatusOK)
 	rec("create instance (wait)", p.post(t, "/api/v1/instance/create-instance", htpl.CreateInstanceRequest{
 		ServerID: sid, Key: "parity-1", TemplateID: tpl.ID, Wait: true,
 	})).expect(t, http.StatusOK)
````

Modify `internal/app/web/contract/rollouts_test.go` (`git apply` this diff):

````diff
diff --git a/internal/app/web/contract/rollouts_test.go b/internal/app/web/contract/rollouts_test.go
--- a/internal/app/web/contract/rollouts_test.go
+++ b/internal/app/web/contract/rollouts_test.go
@@ -84,8 +84,8 @@ func TestUpgradeAllRollout(t *testing.T) {
 	t.Parallel()
 	ie := newInstanceEnv(t)
 	e, owner, sid := ie.e, ie.owner, ie.sid
-	a := ie.created(t, "tenant-1", "acme", "9001").Instance.AppID
-	b := ie.created(t, "tenant-2", "bolt", "9002").Instance.AppID
+	a := ie.created(t, "tenant-1", "acme").Instance.AppID
+	b := ie.created(t, "tenant-2", "bolt").Instance.AppID
 	reportRunning(t, e, sid, fixed(a, b), nil, nil)
 	saveTemplate(t, owner, htpl.SaveTemplateRequest{ID: ie.tplID, Name: "shop", Body: shopBody(shopCompose), BaseRevision: 1})
 
@@ -131,8 +131,8 @@ func TestStopRollout(t *testing.T) {
 	t.Parallel()
 	ie := newInstanceEnv(t)
 	e, owner, sid := ie.e, ie.owner, ie.sid
-	a := ie.created(t, "tenant-1", "acme", "9001").Instance.AppID
-	b := ie.created(t, "tenant-2", "bolt", "9002").Instance.AppID
+	a := ie.created(t, "tenant-1", "acme").Instance.AppID
+	b := ie.created(t, "tenant-2", "bolt").Instance.AppID
 	reportRunning(t, e, sid, fixed(a, b), nil, nil)
 	saveTemplate(t, owner, htpl.SaveTemplateRequest{ID: ie.tplID, Name: "shop", Body: shopBody(shopCompose), BaseRevision: 1})
 
````

Replace the whole of `internal/app/web/contract/templates_test.go` with:

````go
package contract_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	htpl "winterflow/internal/app/web/handler/templates"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/template"
	tuc "winterflow/internal/domain/usecase/templates"
	"winterflow/internal/infra/db/dbtest"
)

// shopCompose declares, under x-winterflow, an http port, a required NAME
// with a pattern, an optional VERSION (the compose file's "1.0"), a
// required secret, and a sqlite backup; it also references a variable
// nothing declares (SMTP_PASS), which a save warns about.
const shopCompose = `services:
  app:
    image: example/shop:1
    environment:
      NAME: "shop"
      VERSION: "1.0"
      SECRET_KEY: "changeme"
      SMTP_PASS: ${SMTP_PASS}
    volumes: [data:/data]
volumes: { data: {} }
x-winterflow:
  expose:
    - { container: app, port: 8080 }
  variables:
    NAME: { required: true, pattern: "^[a-z]{3,20}$" }
    VERSION: {}
    SECRET_KEY: { required: true, encrypted: true }
  backups:
    - { name: db, type: sqlite, volume: data, path: shop.db }
`

// shopBody is a template body with the compose file and its look.
func shopBody(compose string) template.Body {
	return template.Body{Files: map[string]string{"compose.yaml": compose}, Summary: "A shop", Icon: "cart", Color: "#112233"}
}

// saveTemplate saves a template revision through the API.
func saveTemplate(t *testing.T, c *client, req htpl.SaveTemplateRequest) tuc.SavedTemplate {
	t.Helper()
	var out tuc.SavedTemplate
	c.post(t, "/api/v1/template/save-template", req).expect(t, http.StatusOK).data(t, &out)
	return out
}

// catalogTemplate inserts a catalog template with body (nothing in the API
// creates one: the catalog sync will).
func catalogTemplate(t *testing.T, e *env, id, slug string, body template.Body) {
	t.Helper()
	raw, err := template.Encode(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.deps.TemplateRepository.CreateTemplate(e.dbCtx(t), model.Template{ID: id, Source: model.TemplateSourceCatalog, Slug: slug, Name: "Shop"},
		model.TemplateRevision{Body: raw, Digest: "sha256:" + slug}); err != nil {
		t.Fatal(err)
	}
}

// foreignClient is another org's member (logged in, subscribed) and that
// org's server.
func (e *env) foreignClient(t *testing.T) (*client, string) {
	t.Helper()
	srv := e.foreignServer(t)
	m := e.newClient()
	m.userID, m.email = "", "mallory@other.test"
	m.login(t, m.email, "password")
	m.subscribe(t)
	return m, srv
}

func TestTemplateRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)

	saved := saveTemplate(t, owner, htpl.SaveTemplateRequest{Name: "shop", Body: shopBody(shopCompose), Note: "first"})
	if saved.Revision != 1 || saved.ID == "" || len(saved.Warnings) != 1 || !strings.HasPrefix(saved.Warnings[0], "SMTP_PASS is not a declared variable") {
		t.Fatalf("saved = %+v", saved)
	}
	t.Run("the name is unique in the org", func(t *testing.T) {
		owner.post(t, "/api/v1/template/save-template", htpl.SaveTemplateRequest{Name: "shop", Body: shopBody(shopCompose)}).
			expect(t, http.StatusBadRequest)
	})
	t.Run("an invalid block names the key", func(t *testing.T) {
		bad := strings.Replace(shopCompose, "    VERSION: {}\n", "    VERSION: { default: \"2\" }\n", 1)
		r := owner.post(t, "/api/v1/template/save-template", htpl.SaveTemplateRequest{Name: "other", Body: shopBody(bad)}).expect(t, http.StatusBadRequest)
		if !strings.Contains(string(r.Body), "x-winterflow.variables.VERSION.default") {
			t.Fatalf("body = %s", r.Body)
		}
		shared := strings.Replace(shopCompose, "volumes: { data: {} }", "volumes: { data: { name: shop-data } }", 1)
		r = owner.post(t, "/api/v1/template/save-template", htpl.SaveTemplateRequest{Name: "other", Body: shopBody(shared)}).expect(t, http.StatusBadRequest)
		if !strings.Contains(string(r.Body), "would be shared by every install") {
			t.Fatalf("body = %s", r.Body)
		}
	})
	t.Run("a new revision needs the latest base", func(t *testing.T) {
		next := saveTemplate(t, owner, htpl.SaveTemplateRequest{ID: saved.ID, Name: "shop", Body: shopBody(shopCompose), BaseRevision: 1, Note: "second"})
		if next.Revision != 2 {
			t.Fatalf("next = %+v", next)
		}
		owner.post(t, "/api/v1/template/save-template", htpl.SaveTemplateRequest{ID: saved.ID, Name: "shop", Body: shopBody(shopCompose), BaseRevision: 1}).
			expect(t, http.StatusConflict)
	})
	t.Run("list and read with what the compose file declares", func(t *testing.T) {
		var list tuc.TemplateList
		owner.get(t, "/api/v1/template/get-templates", nil).expect(t, http.StatusOK).data(t, &list)
		if len(list.Templates) != 1 || list.Templates[0].LatestRevision != 2 || list.Templates[0].Instances != 0 || list.Templates[0].Source != "org" {
			t.Fatalf("list = %+v", list)
		}
		var view tuc.TemplateView
		owner.get(t, "/api/v1/template/get-template", url.Values{"id": {saved.ID}, "revision": {"1"}}).expect(t, http.StatusOK).data(t, &view)
		if view.Revision != 1 || view.Note != "first" || view.Body.Summary != "A shop" || len(view.Revisions) != 2 {
			t.Fatalf("view = %+v", view)
		}
		d := view.Detected
		if len(d.Variables) != 3 || !d.Variables[2].Encrypted || len(d.Expose) != 1 || d.Expose[0].Target != "app:8080" || len(d.Backups) != 1 {
			t.Fatalf("detected = %+v", d)
		}
		owner.get(t, "/api/v1/template/get-template", url.Values{"id": {saved.ID}, "revision": {"9"}}).expect(t, http.StatusNotFound)
	})
	t.Run("inspect reads a body without saving it", func(t *testing.T) {
		var got tuc.Inspection
		owner.post(t, "/api/v1/template/inspect-template", htpl.InspectTemplateRequest{Body: shopBody(shopCompose)}).expect(t, http.StatusOK).data(t, &got)
		if got.Problem != "" || len(got.Detected.Variables) != 3 || len(got.Detected.Warnings) != 1 {
			t.Fatalf("inspect = %+v", got)
		}
		owner.post(t, "/api/v1/template/inspect-template", htpl.InspectTemplateRequest{Body: shopBody("services: [\n")}).expect(t, http.StatusOK).data(t, &got)
		if !strings.Contains(got.Problem, "not valid YAML") {
			t.Fatalf("inspect = %+v", got)
		}
	})
	t.Run("another org's template is unknown", func(t *testing.T) {
		mallory, _ := e.foreignClient(t)
		mallory.get(t, "/api/v1/template/get-template", url.Values{"id": {saved.ID}}).expect(t, http.StatusNotFound)
		mallory.post(t, "/api/v1/template/delete-template", htpl.TemplateIDRequest{ID: saved.ID}).expect(t, http.StatusNotFound)
		var list tuc.TemplateList
		mallory.get(t, "/api/v1/template/get-templates", nil).expect(t, http.StatusOK).data(t, &list)
		if len(list.Templates) != 0 {
			t.Fatalf("mallory sees %+v", list)
		}
	})
	t.Run("delete", func(t *testing.T) {
		owner.post(t, "/api/v1/template/delete-template", htpl.TemplateIDRequest{ID: saved.ID}).expect(t, http.StatusOK)
		owner.get(t, "/api/v1/template/get-template", url.Values{"id": {saved.ID}}).expect(t, http.StatusNotFound)
	})
}

// Every org reads a catalog template; saving or deleting one is 403.
func TestCatalogTemplatesAreReadOnly(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	cat := dbtest.ID(901)
	catalogTemplate(t, e, cat, "shop", shopBody(shopCompose))
	mallory, _ := e.foreignClient(t)
	for _, c := range []*client{owner, mallory} {
		var list tuc.TemplateList
		c.get(t, "/api/v1/template/get-templates", nil).expect(t, http.StatusOK).data(t, &list)
		if len(list.Templates) != 1 || list.Templates[0].ID != cat || list.Templates[0].Source != "catalog" || list.Templates[0].Slug != "shop" {
			t.Fatalf("list = %+v", list)
		}
		var view tuc.TemplateView
		c.get(t, "/api/v1/template/get-template", url.Values{"id": {cat}}).expect(t, http.StatusOK).data(t, &view)
		if view.Source != "catalog" || len(view.Detected.Variables) != 3 {
			t.Fatalf("view = %+v", view)
		}
	}
	r := owner.post(t, "/api/v1/template/save-template", htpl.SaveTemplateRequest{ID: cat, Name: "shop", Body: shopBody(shopCompose), BaseRevision: 1}).
		expect(t, http.StatusForbidden)
	if !strings.Contains(string(r.Body), "catalog templates are read-only") {
		t.Fatalf("save body = %s", r.Body)
	}
	mallory.post(t, "/api/v1/template/delete-template", htpl.TemplateIDRequest{ID: cat}).expect(t, http.StatusForbidden)
}
````

Create `internal/app/web/util/response_test.go`:

````go
package util

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"winterflow/internal/domain/model"
)

func TestStatusFor(t *testing.T) {
	for err, want := range map[error]int{
		model.Rejected("bad"):                               http.StatusBadRequest,
		model.ErrTemplateNotFound:                           http.StatusNotFound,
		model.ErrKeyInUse:                                   http.StatusConflict,
		fmt.Errorf("wrapped: %w", model.ErrCatalogReadOnly): http.StatusForbidden,
		errors.New("unclassified"):                          http.StatusBadRequest,
	} {
		if got := StatusFor(err); got != want {
			t.Errorf("%v: %d, want %d", err, got, want)
		}
	}
}
````

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/app/web/util/ ./internal/app/web/contract/`
Expected: FAIL — `StatusFor` answers 400 for `ErrCatalogReadOnly`; the contract tests post to `inspect-template`, which has no route.

- [ ] **Step 3: Implement**

Modify `internal/app/web/handler/templates/routes.go` (`git apply` this diff):

````diff
diff --git a/internal/app/web/handler/templates/routes.go b/internal/app/web/handler/templates/routes.go
--- a/internal/app/web/handler/templates/routes.go
+++ b/internal/app/web/handler/templates/routes.go
@@ -16,29 +16,39 @@ func (h *Handler) Routes(api.Guards) []api.Route {
 // templateRoutes are the org-scoped template routes.
 func (h *Handler) templateRoutes() []api.Route {
 	org := []int{http.StatusBadRequest, http.StatusNotFound}
+	own := []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict}
 	orgConflict := []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict}
 	return []api.Route{
 		{
 			Method: http.MethodGet, Path: "/api/v1/template/get-templates", Handler: h.ListTemplates, Auth: api.User,
-			Summary: "The org's templates with their instance counts", Tag: "templates",
+			Summary: "The org's templates, then the catalog's, with the org's instance counts", Tag: "templates",
 			Response: tuc.TemplateList{}, Errors: []int{http.StatusBadRequest},
 		},
 		{
 			Method: http.MethodGet, Path: "/api/v1/template/get-template", Handler: h.GetTemplate, Auth: api.User,
-			Summary: "A template revision's body (the latest by default) and the revision list", Tag: "templates",
+			Summary: "A template revision's body (the latest by default), what its compose files declare, and the revision list", Tag: "templates",
 			Query: TemplateQuery{}, Response: tuc.TemplateView{}, Errors: org,
 		},
 		{
 			Method: http.MethodPost, Path: "/api/v1/template/save-template", Handler: h.SaveTemplate, Auth: api.User,
-			Summary: "Create a template or add a revision", Tag: "templates",
-			Description: "base_revision must be the latest revision (409 otherwise: someone saved meanwhile). " +
-				"warnings name file references nothing provides.",
-			Request: SaveTemplateRequest{}, Response: tuc.SavedTemplate{}, Errors: orgConflict,
+			Summary: "Create an org template or add a revision", Tag: "templates",
+			Description: "The compose files' x-winterflow block declares the variables, exposed ports and backups. " +
+				"base_revision must be the latest revision (409 otherwise: someone saved meanwhile). " +
+				"warnings name a variable nothing uses and file references nothing provides. A catalog template is read-only (403).",
+			Request: SaveTemplateRequest{}, Response: tuc.SavedTemplate{}, Errors: own,
+		},
+		{
+			Method: http.MethodPost, Path: "/api/v1/template/inspect-template", Handler: h.InspectTemplate, Auth: api.User,
+			Summary: "What a template body declares and the first problem a save would refuse", Tag: "templates",
+			Description: "Stores nothing: the editor's Detected panel follows the files with it. A body a save would refuse " +
+				"answers 200 with the problem.",
+			Request: InspectTemplateRequest{}, Response: tuc.Inspection{}, Errors: []int{http.StatusBadRequest},
 		},
 		{
 			Method: http.MethodPost, Path: "/api/v1/template/delete-template", Handler: h.DeleteTemplate, Auth: api.User,
-			Summary: "Delete a template without live instances", Tag: "templates",
-			Request: TemplateIDRequest{}, Errors: orgConflict,
+			Summary: "Delete an org template without live instances", Tag: "templates",
+			Description: "A catalog template is read-only (403).",
+			Request:     TemplateIDRequest{}, Errors: own,
 		},
 		{
 			Method: http.MethodPost, Path: "/api/v1/template/upgrade-instances", Handler: h.UpgradeInstances, Auth: api.User,
````

Modify `internal/app/web/handler/templates/templates.go` (`git apply` this diff):

````diff
diff --git a/internal/app/web/handler/templates/templates.go b/internal/app/web/handler/templates/templates.go
--- a/internal/app/web/handler/templates/templates.go
+++ b/internal/app/web/handler/templates/templates.go
@@ -28,6 +28,11 @@ type SaveTemplateRequest struct {
 	BaseRevision int64         `json:"base_revision" description:"The latest revision the editor read (0 for a new template); another one is a 409."`
 }
 
+// InspectTemplateRequest is the body of POST inspect-template.
+type InspectTemplateRequest struct {
+	Body template.Body `json:"body" required:"true"`
+}
+
 // TemplateIDRequest is the body of POST delete-template.
 type TemplateIDRequest struct {
 	ID string `json:"id" required:"true"`
@@ -99,6 +104,24 @@ func (h *Handler) SaveTemplate(w http.ResponseWriter, r *http.Request) {
 	webutil.Success(w, "template saved", out)
 }
 
+func (h *Handler) InspectTemplate(w http.ResponseWriter, r *http.Request) {
+	_, orgID, ok := h.callerOrg(w, r)
+	if !ok {
+		return
+	}
+	r.Body = http.MaxBytesReader(w, r.Body, maxSaveBytes)
+	req, ok := webutil.DecodeBody[InspectTemplateRequest](w, r)
+	if !ok {
+		return
+	}
+	out, err := h.uc.InspectTemplate(r.Context(), orgID, req.Body)
+	if err != nil {
+		h.fail(w, err, "inspect the template")
+		return
+	}
+	webutil.Success(w, "", out)
+}
+
 func (h *Handler) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
 	_, orgID, ok := h.callerOrg(w, r)
 	if !ok {
````

Modify `internal/app/web/util/response.go` (`git apply` this diff):

````diff
diff --git a/internal/app/web/util/response.go b/internal/app/web/util/response.go
--- a/internal/app/web/util/response.go
+++ b/internal/app/web/util/response.go
@@ -46,8 +46,8 @@ func ErrorStatus(w http.ResponseWriter, status int, message string) {
 }
 
 // StatusFor is the status an error class answers with: model.ErrNotFound
-// 404, model.ErrConflict 409, anything else (model.ErrRejected included)
-// 400.
+// 404, model.ErrConflict 409, model.ErrForbidden 403, anything else
+// (model.ErrRejected included) 400.
 func StatusFor(err error) int {
 	switch {
 	case errors.Is(err, model.ErrRejected):
@@ -56,16 +56,18 @@ func StatusFor(err error) int {
 		return http.StatusNotFound
 	case errors.Is(err, model.ErrConflict):
 		return http.StatusConflict
+	case errors.Is(err, model.ErrForbidden):
+		return http.StatusForbidden
 	}
 	return http.StatusBadRequest
 }
 
 // Fail answers a usecase error: a classified one (model.ErrNotFound,
-// ErrConflict, ErrRejected) with its status (StatusFor) and message,
-// anything else logged ("<area>: <what>") and answered with a generic 400
-// "failed to <what>".
+// ErrConflict, ErrForbidden, ErrRejected) with its status (StatusFor) and
+// message, anything else logged ("<area>: <what>") and answered with a
+// generic 400 "failed to <what>".
 func Fail(w http.ResponseWriter, log *logger.Logger, area string, err error, what string) {
-	if errors.Is(err, model.ErrNotFound) || errors.Is(err, model.ErrConflict) || errors.Is(err, model.ErrRejected) {
+	if errors.Is(err, model.ErrNotFound) || errors.Is(err, model.ErrConflict) || errors.Is(err, model.ErrForbidden) || errors.Is(err, model.ErrRejected) {
 		ErrorStatus(w, StatusFor(err), err.Error())
 		return
 	}
````

- [ ] **Step 4: Regenerate**

```bash
make openapi
go test ./internal/app/web/ -run TestRoutesGolden -update
go test ./internal/app/web/contract/ -run TestParity -update
```

Review the parity golden's diff: `read template` is new, an instance shows `name`, `values`, `hosts` instead of `parameters`, a template summary `source`, `slug`, `withdrawn_at`.

- [ ] **Step 5: Run them and see them pass**

```bash
go vet ./...
go test ./...
WF_CONTRACT_GUARD=require go test ./internal/app/web/contract/
make test-pg
```

Expected: PASS — the whole tree is green again on SQLite and Postgres.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat(api): inspect-template, 403 on catalog templates, contract tests for x-winterflow templates" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 6: Retire instance.backup_defaults on the agent and in save-app

An upgrade no longer sends backup defaults: backups a revision adds start off (the agent keeps the app's stored list when a save has none). `applyBackupDefaults` and its constants go from the orchestrator, and save-app stops reading `instance.backup_defaults`.

**Files:**
- Modify: `internal/domain/usecase/app/backups.go`
- Modify: `internal/domain/usecase/app/observe_test.go`
- Modify: `internal/infra/orchestrator/docker_compose/backupconfig.go`
- Modify: `internal/infra/orchestrator/docker_compose/backupconfig_test.go`
- Modify: `internal/infra/orchestrator/docker_compose/store.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `store.go` writes `keepBackups(app.Config, prevConfig)`; `applyBackupDefaults`, `instanceKey`, `backupDefaultsKey` are deleted; `appuc.checkBackups` reads only `backups`.

- [ ] **Step 1: Delete the tests of the removed merge and add a guard test**

Modify `internal/domain/usecase/app/observe_test.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/usecase/app/observe_test.go b/internal/domain/usecase/app/observe_test.go
--- a/internal/domain/usecase/app/observe_test.go
+++ b/internal/domain/usecase/app/observe_test.go
@@ -2,13 +2,11 @@ package app
 
 import (
 	"context"
-	"encoding/json"
 	"errors"
 	"strings"
 	"testing"
 	"time"
 
-	"winterflow/internal/domain/backup/recipe"
 	"winterflow/internal/domain/command"
 	"winterflow/internal/domain/model"
 	"winterflow/internal/domain/port"
@@ -80,37 +78,6 @@ func TestObserversRunAfterTheDB(t *testing.T) {
 	}
 }
 
-// An upgrade's backup defaults need their storage on the server like a
-// backups list does.
-func TestSaveChecksInstanceBackupDefaults(t *testing.T) {
-	ss := newFakeServerStorages(model.StorageSummary{Name: "disk", Type: model.StorageLocal, Location: "/mnt"})
-	uc := newStoragesUseCase(&recordingDispatcher{}, ss)
-	compose := "services: {app: {image: x, volumes: [data:/data]}}\nvolumes: {data: {}}\nx-winterflow:\n  backups:\n" +
-		"    - {name: db, type: sqlite, volume: data, path: a.db}\n    - {name: media, type: files, volume: data}\n"
-	save := func(defaults []recipe.BackupConfig) error {
-		cfg := `{"instance":{"template_id":"t","template_name":"s","revision":2,"key":"k","backup_defaults":` + mustJSON(t, defaults) + `}}`
-		_, err := uc.CheckSave(context.Background(), "srv-1", model.App{ID: "a1", Name: "x"}, command.AppPayload{
-			Config: []byte(cfg), Files: []command.ContentItem{{Name: "compose.yml", Content: []byte(compose)}},
-		}, false)
-		return err
-	}
-	if err := save([]recipe.BackupConfig{{Name: "media", Enabled: true, Storage: "disk"}, {Name: "db", Storage: "gone"}}); err != nil {
-		t.Fatalf("a known storage (and a disabled default's): %v", err)
-	}
-	if err := save([]recipe.BackupConfig{{Name: "media", Enabled: true, Storage: "gone"}}); !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), `no storage "gone"`) {
-		t.Fatalf("an unknown storage: %v", err)
-	}
-}
-
-func mustJSON(t *testing.T, v any) string {
-	t.Helper()
-	raw, err := json.Marshal(v)
-	if err != nil {
-		t.Fatal(err)
-	}
-	return string(raw)
-}
-
 // A command with an observer keeps its result past the dispatcher's default
 // (an instance call may be waited on for the handler's bound); one without
 // (the web UI's) is left at the dispatcher's default.
````

Modify `internal/infra/orchestrator/docker_compose/backupconfig_test.go` (`git apply` this diff):

````diff
diff --git a/internal/infra/orchestrator/docker_compose/backupconfig_test.go b/internal/infra/orchestrator/docker_compose/backupconfig_test.go
--- a/internal/infra/orchestrator/docker_compose/backupconfig_test.go
+++ b/internal/infra/orchestrator/docker_compose/backupconfig_test.go
@@ -593,66 +593,23 @@ func TestRenameAppOnNullConfig(t *testing.T) {
 	}
 }
 
-func TestApplyBackupDefaults(t *testing.T) {
-	const marker = `"instance":{"template_id":"t","template_name":"shop","revision":2,"key":"k"`
-	// The rewritten blob is re-encoded from maps: keys come out sorted.
-	const stored = `"instance":{"key":"k","revision":2,"template_id":"t","template_name":"shop"}`
-	cases := []struct{ name, in, want string }{
-		{"adds a default with no entry",
-			`{"name":"a",` + marker + `,"backup_defaults":[{"name":"media","enabled":true,"storage":"s3"}]},"backups":[{"name":"database","storage":"disk"}]}`,
-			`{"backups":[{"name":"database","storage":"disk"},{"name":"media","enabled":true,"storage":"s3"}],` + stored + `,"name":"a"}`},
-		{"a stored entry wins",
-			`{` + marker + `,"backup_defaults":[{"name":"database","enabled":true,"storage":"s3"}]},"backups":[{"name":"database"}]}`,
-			`{"backups":[{"name":"database"}],` + stored + `}`},
-		{"no backups list yet",
-			`{` + marker + `,"backup_defaults":[{"name":"media","enabled":true,"storage":"s3"}]}}`,
-			`{"backups":[{"name":"media","enabled":true,"storage":"s3"}],` + stored + `}`},
-	}
-	for _, tc := range cases {
-		t.Run(tc.name, func(t *testing.T) {
-			if got := applyBackupDefaults([]byte(tc.in)); string(got) != tc.want {
-				t.Fatalf("got  %s\nwant %s", got, tc.want)
-			}
-		})
-	}
-	for _, same := range []string{`{"name":"plain"}`, `not json`, `{"instance":{"key":"k"}}`, `{"instance":{"backup_defaults":"x"}}`} {
-		if got := applyBackupDefaults([]byte(same)); string(got) != same {
-			t.Errorf("%s -> %s", same, got)
-		}
-	}
-}
-
-// An upgrade's defaults reach the stored backups through a save that, like
-// every instance update, carries no backups list: the stored settings are
-// kept and only the new backup is added; the marker keeps no defaults.
-func TestSaveAppliesInstanceBackupDefaults(t *testing.T) {
+// An instance's save, like every instance update, carries no backups
+// list: the stored settings are kept, and a backup the compose file adds
+// starts off (it has no entry).
+func TestInstanceSaveKeepsTheBackups(t *testing.T) {
 	r := newTestRepo(t)
 	if _, err := saveConfig(t, r, "app-1", backupCompose, withBackups(`[{"name":"database","enabled":true,"storage":"hetzner"}]`)); err != nil {
 		t.Fatal(err)
 	}
-	upgrade := `{"name":"demo","files":[{"filename":"compose.yml"}],"instance":{"template_id":"t","template_name":"shop","revision":2,"key":"k",` +
-		`"backup_defaults":[{"name":"database","enabled":true,"storage":"other"},{"name":"media","enabled":true,"storage":"disk","every":"6h"}]}}`
+	upgrade := `{"name":"demo","files":[{"filename":"compose.yml"}],"instance":{"template_id":"t","template_name":"shop","revision":2,"key":"k"}}`
 	if _, err := saveConfig(t, r, "app-1", backupCompose, upgrade); err != nil {
 		t.Fatal(err)
 	}
-	dir := r.appDataDir("app-1")
-	got, err := r.readOverrides(dir)
+	got, err := r.readOverrides(r.appDataDir("app-1"))
 	if err != nil {
 		t.Fatal(err)
 	}
-	want := []recipe.BackupConfig{
-		{Name: "database", Enabled: true, Storage: "hetzner"},
-		{Name: "media", Enabled: true, Storage: "disk", Every: "6h"},
-	}
-	if !reflect.DeepEqual(got, want) {
+	if want := []recipe.BackupConfig{{Name: "database", Enabled: true, Storage: "hetzner"}}; !reflect.DeepEqual(got, want) {
 		t.Fatalf("overrides = %+v", got)
 	}
-	cfg, _, err := r.readAppConfig(dir)
-	if err != nil {
-		t.Fatal(err)
-	}
-	inst, _ := cfg["instance"].(map[string]any)
-	if inst == nil || inst["key"] != "k" || inst["backup_defaults"] != nil {
-		t.Fatalf("stored marker = %v", cfg["instance"])
-	}
 }
````

- [ ] **Step 2: Run the new guard test before the removal**

Run: `go test ./internal/infra/orchestrator/docker_compose/ -run TestInstanceSaveKeepsTheBackups`
Expected: PASS already — it pins what the removal must keep (an instance save without a backups list keeps the stored settings). The deleted tests are the ones that exercised `backup_defaults`.

- [ ] **Step 3: Implement**

Modify `internal/domain/usecase/app/backups.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/usecase/app/backups.go b/internal/domain/usecase/app/backups.go
--- a/internal/domain/usecase/app/backups.go
+++ b/internal/domain/usecase/app/backups.go
@@ -28,10 +28,7 @@ const backupRunWait = command.BackupRunWait + 5*time.Minute
 // agent.
 func (uc *UseCase) checkBackups(ctx context.Context, serverID string, comp recipe.Compose, config []byte, isNew bool) (func(string) bool, error) {
 	var cfg struct {
-		Backups  *[]recipe.BackupConfig `json:"backups"`
-		Instance *struct {
-			BackupDefaults []recipe.BackupConfig `json:"backup_defaults"`
-		} `json:"instance"`
+		Backups *[]recipe.BackupConfig `json:"backups"`
 	}
 	if len(config) > 0 {
 		if err := json.Unmarshal(config, &cfg); err != nil {
@@ -49,19 +46,6 @@ func (uc *UseCase) checkBackups(ctx context.Context, serverID string, comp recip
 	if err := uc.requireBackupStorages(ctx, serverID, backups); err != nil {
 		return nil, err
 	}
-	// A template upgrade's backup defaults are added by the agent where
-	// the app has no entry yet; an enabled one needs its storage too.
-	if cfg.Instance != nil {
-		var enabled []recipe.EffectiveBackup
-		for _, d := range cfg.Instance.BackupDefaults {
-			if d.Enabled {
-				enabled = append(enabled, recipe.EffectiveBackup{Name: d.Name, Enabled: true, Storage: d.Storage})
-			}
-		}
-		if err := uc.requireBackupStorages(ctx, serverID, enabled); err != nil {
-			return nil, err
-		}
-	}
 	declared := recipe.Declared(backups)
 	known := cfg.Backups != nil || isNew
 	return func(name string) bool {
````

Modify `internal/infra/orchestrator/docker_compose/backupconfig.go` (`git apply` this diff):

````diff
diff --git a/internal/infra/orchestrator/docker_compose/backupconfig.go b/internal/infra/orchestrator/docker_compose/backupconfig.go
--- a/internal/infra/orchestrator/docker_compose/backupconfig.go
+++ b/internal/infra/orchestrator/docker_compose/backupconfig.go
@@ -1,7 +1,6 @@
 package dockercompose
 
 import (
-	"bytes"
 	"context"
 	"crypto/sha256"
 	"encoding/hex"
@@ -149,68 +148,6 @@ func keepBackups(next, prev []byte) []byte {
 	return out
 }
 
-// instanceKey is the key of a template instance's marker in config.json;
-// backupDefaultsKey the marker's list of backups to add (the API sends it
-// on an upgrade that brings backups new to the app).
-const (
-	instanceKey       = "instance"
-	backupDefaultsKey = "backup_defaults"
-)
-
-// applyBackupDefaults adds each instance.backup_defaults entry to the
-// backups list unless the list already has that name (a stored setting,
-// Backups-tab changes included, always wins), and drops backup_defaults
-// from the marker: it is an instruction for this save, not state. A blob
-// without defaults, or one that doesn't parse, comes back unchanged.
-func applyBackupDefaults(cfg []byte) []byte {
-	if !bytes.Contains(cfg, []byte(backupDefaultsKey)) {
-		return cfg // nearly every save: skip decoding the blob
-	}
-	var top map[string]json.RawMessage
-	if json.Unmarshal(cfg, &top) != nil || top == nil {
-		return cfg
-	}
-	var marker map[string]json.RawMessage
-	if json.Unmarshal(top[instanceKey], &marker) != nil || marker[backupDefaultsKey] == nil {
-		return cfg
-	}
-	var defaults, list []recipe.BackupConfig
-	if json.Unmarshal(marker[backupDefaultsKey], &defaults) != nil {
-		return cfg
-	}
-	if raw, ok := top[backupsKey]; ok && json.Unmarshal(raw, &list) != nil {
-		return cfg
-	}
-	have := map[string]bool{}
-	for _, b := range list {
-		have[b.Name] = true
-	}
-	for _, d := range defaults {
-		if !have[d.Name] {
-			list = append(list, d)
-			have[d.Name] = true
-		}
-	}
-	delete(marker, backupDefaultsKey)
-	rawMarker, err := json.Marshal(marker)
-	if err != nil {
-		return cfg
-	}
-	top[instanceKey] = rawMarker
-	if len(list) > 0 {
-		rawList, err := json.Marshal(list)
-		if err != nil {
-			return cfg
-		}
-		top[backupsKey] = rawList
-	}
-	out, err := json.Marshal(top)
-	if err != nil {
-		return cfg
-	}
-	return out
-}
-
 // cleanBackups validates the app's x-winterflow block and backups after a
 // save wrote its files and drops overrides of backups the recipe no longer
 // has (warnings, with the block's own). The spec it parsed is returned for
````

Modify `internal/infra/orchestrator/docker_compose/store.go` (`git apply` this diff):

````diff
diff --git a/internal/infra/orchestrator/docker_compose/store.go b/internal/infra/orchestrator/docker_compose/store.go
--- a/internal/infra/orchestrator/docker_compose/store.go
+++ b/internal/infra/orchestrator/docker_compose/store.go
@@ -204,7 +204,7 @@ func (r *Repository) writeAppStore(dir string, app command.AppPayload) (secretSt
 		return secretStore{}, err
 	}
 	prevConfig, _ := os.ReadFile(filepath.Join(dir, configRel))
-	if err := fsutil.WriteFile(filepath.Join(dir, configRel), applyBackupDefaults(keepBackups(app.Config, prevConfig)), 0o644); err != nil {
+	if err := fsutil.WriteFile(filepath.Join(dir, configRel), keepBackups(app.Config, prevConfig), 0o644); err != nil {
 		return secretStore{}, err
 	}
 	secretsJSON, err := json.MarshalIndent(next, "", "  ")
````

- [ ] **Step 4: Run them and see them pass**

```bash
go test ./internal/infra/orchestrator/docker_compose/ ./internal/domain/usecase/app/
go vet ./internal/infra/orchestrator/docker_compose/ ./internal/domain/usecase/app/
grep -rn "backup_defaults\|BackupDefaults\|applyBackupDefaults" --include=*.go internal
```

Expected: PASS — the packages pass (the orchestrator package takes about a minute) and the grep prints nothing.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: retire instance.backup_defaults on the agent and in save-app" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 7: Web: template types and the values helpers

`lib/templates.ts` mirrors the new API (body, `detected`, `source`, instance `name`/`values`/`hosts`, `BackupChoice`, `inspectTemplate`, `orgTemplates`); `lib/template-values.ts` holds the instance form's checks over declared variables (it reuses `lib/declared-variables` from sub-project 1). `lib/template-params.ts` stays until Task 10 removes its last importer.

**Files:**
- Create: `web/src/lib/template-values.test.ts`
- Create: `web/src/lib/template-values.ts`
- Modify: `web/src/lib/templates.test.ts`
- Modify: `web/src/lib/templates.ts`

**Interfaces:**
- Consumes: `DeclaredVariable` (`types/app-config`), `declaredValueError` (`lib/declared-variables`).
- Produces:
  - `lib/templates.ts`: `TemplateBody`, `DetectedExpose`, `DetectedBackup`, `Detected`, `Inspection`, `TemplateSource`, `TemplateSummary` (+`source`, `slug`, `withdrawn_at`), `orgTemplates(list)`, `TemplateView` (+`source`, `slug`, `withdrawn_at`, `detected`), `InstanceHost`, `BackupChoice`, `InstanceView` (+`name`, `values`, `hosts`; no `parameters`), `InstanceValues{values, secrets, hosts, backups}`, `CreateInstanceBody` (+`name`), `TemplatesApi.inspectTemplate(body)`.
  - `lib/template-values.ts`: `variableErrors(vars, values, kept?)`, `splitValues(vars, values)`, `requiredFirst(vars)`, `upgradeNeeds(target, inst)`, `defaultChanges(from, to, inst)`, `DefaultChange`, `newKey()`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/template-values.test.ts`:

````ts
import { describe, expect, it } from "vitest";

import { defaultChanges, newKey, requiredFirst, splitValues, upgradeNeeds, variableErrors } from "@/lib/template-values";
import type { InstanceView } from "@/lib/templates";
import type { DeclaredVariable } from "@/types/app-config";

const v = (name: string, extra: Partial<DeclaredVariable> = {}): DeclaredVariable => ({
  name,
  required: false,
  encrypted: false,
  default: null,
  services: ["app"],
  ...extra,
});

const VARS = [v("LANGUAGE", { default: "en" }), v("BASE_URL", { required: true }), v("SECRET", { required: true, encrypted: true })];

const inst = (extra: Partial<InstanceView> = {}): InstanceView => ({
  key: "k1",
  server_id: "s",
  app_id: "a",
  template_id: "t",
  template_name: "notes",
  revision: 1,
  name: "notes",
  values: { BASE_URL: "https://a.test" },
  secrets: { SECRET: "<encrypted>" },
  hosts: [],
  state: "created",
  status_code: 0,
  last_error: "",
  created_at: "",
  updated_at: "",
  ...extra,
});

describe("variableErrors", () => {
  it("blocks a missing required value and a plain $; a kept secret may stay blank", () => {
    expect(variableErrors(VARS, { LANGUAGE: "$x" })).toEqual({
      LANGUAGE: "A plain value can't contain $.",
      BASE_URL: "Enter a value: this variable is required.",
      SECRET: "Enter a value: this variable is required.",
    });
    expect(variableErrors(VARS, { BASE_URL: "https://a.test" }, ["SECRET"])).toEqual({});
    expect(variableErrors(null, {})).toEqual({});
  });
});

describe("splitValues and requiredFirst", () => {
  it("splits plain values from typed secrets, blank as empty", () => {
    expect(splitValues(VARS, { BASE_URL: "u", SECRET: "s" })).toEqual({
      plain: { LANGUAGE: "", BASE_URL: "u" },
      typed: { SECRET: "s" },
    });
    expect(splitValues(undefined, {})).toEqual({ plain: {}, typed: {} });
  });
  it("orders required variables first", () => {
    expect(requiredFirst(VARS).map((x) => x.name)).toEqual(["BASE_URL", "SECRET", "LANGUAGE"]);
    expect(requiredFirst(null)).toEqual([]);
  });
});

describe("upgradeNeeds", () => {
  const target = [...VARS, v("REGION", { required: true }), v("TOKEN", { required: true, encrypted: true })];
  it("asks only for required values the instance doesn't hold", () => {
    expect(upgradeNeeds(target, inst()).map((x) => x.name)).toEqual(["REGION", "TOKEN"]);
  });
  it("asks a pending instance for every required secret again", () => {
    expect(upgradeNeeds(target, inst({ state: "pending", values: null })).map((x) => x.name)).toEqual([
      "BASE_URL",
      "SECRET",
      "REGION",
      "TOKEN",
    ]);
    expect(upgradeNeeds(null, inst())).toEqual([]);
  });
});

describe("defaultChanges", () => {
  it("names the plain variables the instance leaves to the compose file whose value changes", () => {
    const to = [v("LANGUAGE", { default: "de" }), v("BASE_URL", { required: true, default: "x" }), v("NEW", { default: "1" })];
    expect(defaultChanges(VARS, to, inst())).toEqual([{ name: "LANGUAGE", from: "en", to: "de" }]);
    expect(defaultChanges(undefined, undefined, inst())).toEqual([]);
  });
});

describe("newKey", () => {
  it("is a fresh UUID v4 each time", () => {
    const a = newKey();
    expect(a).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    expect(newKey()).not.toBe(a);
  });
});
````

Modify `web/src/lib/templates.test.ts` (`git apply` this diff):

````diff
diff --git a/web/src/lib/templates.test.ts b/web/src/lib/templates.test.ts
--- a/web/src/lib/templates.test.ts
+++ b/web/src/lib/templates.test.ts
@@ -13,6 +13,7 @@ import {
   INSTANCE_WAIT_MS,
   usageLabel,
   isManagedByTemplate,
+  orgTemplates,
   LOAD_TIMEOUT_MS,
   sealSecrets,
   TOO_LONG,
@@ -99,7 +100,7 @@ describe("createTemplatesApi", () => {
       "/api/v1/template/delete-template": null,
     });
     const t = createTemplatesApi(ok);
-    const body = { files: {}, parameters: [], app_name: "x", icon: "", color: "", ingress: [], backups: {} };
+    const body = { files: {}, summary: "", description: "", icon: "", color: "" };
     expect((await t.saveTemplate({ id: "t1", name: "shop", body, base_revision: 2 })).revision).toBe(3);
     const err = await t.saveTemplate({ id: "t1", name: "shop", body, base_revision: 1 }).catch((e: unknown) => e);
     expect(err).toBeInstanceOf(ApiError);
@@ -206,6 +207,22 @@ describe("createTemplatesApi", () => {
   });
 });
 
+describe("inspectTemplate", () => {
+  it("posts the body and answers what it declares", async () => {
+    const inspection = { detected: { variables: [], expose: [], backups: [], warnings: [] }, problem: "files: a compose file is required" };
+    const api = fakeAPI({ "/api/v1/template/inspect-template": inspection });
+    const body = { files: {}, summary: "", description: "", icon: "", color: "" };
+    expect(await createTemplatesApi(ok).inspectTemplate(body)).toEqual(inspection);
+    expect(api.posted("/api/v1/template/inspect-template")).toEqual([{ body }]);
+  });
+});
+
+describe("orgTemplates", () => {
+  it("keeps the org's own templates", () => {
+    expect(orgTemplates([{ source: "org", id: "a" }, { source: "catalog", id: "b" }])).toEqual([{ source: "org", id: "a" }]);
+  });
+});
+
 describe("isManagedByTemplate", () => {
   it("spots the API's managed-app conflict", () => {
     expect(isManagedByTemplate("this app is managed by template shop: update the instance or unlink it")).toBe(true);
````

- [ ] **Step 2: Run them and see them fail**

Run: `CI=true pnpm --dir web exec vitest run src/lib/template-values.test.ts src/lib/templates.test.ts`
Expected: FAIL — `@/lib/template-values` does not exist and `inspectTemplate`/`orgTemplates` are missing.

- [ ] **Step 3: Implement**

Create `web/src/lib/template-values.ts`:

````ts
// An instance's values in the browser: the form's checks over the
// variables a template's compose files declare (x-winterflow), mirroring
// the API's (internal/domain/template Resolve, xwinterflow CheckValues).
// A required variable needs a value and a plain value can't hold $: those
// block a save. A pattern mismatch is only a hint (lib/declared-variables):
// the API (a plain value) and the agent (a secret) decide.
import { declaredValueError } from "@/lib/declared-variables";
import type { InstanceView } from "@/lib/templates";
import type { DeclaredVariable } from "@/types/app-config";

// variableErrors are a values form's blocking problems by name. kept are
// the secrets the agent holds: a blank field keeps them.
export function variableErrors(
  vars: DeclaredVariable[] | null | undefined,
  values: Record<string, string>,
  kept: readonly string[] = [],
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const v of vars ?? []) {
    const err = declaredValueError(v, values[v.name] ?? "", v.encrypted && kept.includes(v.name));
    if (err) out[v.name] = err;
  }
  return out;
}

// splitValues splits a values form by name: plain values and the typed
// secrets (to seal), each "" when blank.
export function splitValues(
  vars: DeclaredVariable[] | null | undefined,
  values: Record<string, string>,
): { plain: Record<string, string>; typed: Record<string, string> } {
  const plain: Record<string, string> = {};
  const typed: Record<string, string> = {};
  for (const v of vars ?? []) (v.encrypted ? typed : plain)[v.name] = values[v.name] ?? "";
  return { plain, typed };
}

// requiredFirst orders a form: required variables first, each group in the
// compose file's order.
export function requiredFirst(vars: DeclaredVariable[] | null | undefined): DeclaredVariable[] {
  const list = vars ?? [];
  return [...list.filter((v) => v.required), ...list.filter((v) => !v.required)];
}

// upgradeNeeds are the variables of the target revision that need a value
// the instance doesn't hold: a required plain one without a stored value,
// a required secret it was never given. A pending instance holds no secret
// at all: every required secret is asked for.
export function upgradeNeeds(target: DeclaredVariable[] | null | undefined, inst: InstanceView): DeclaredVariable[] {
  const pending = inst.state === "pending";
  return (target ?? []).filter((v) => {
    if (!v.required) return false;
    if (v.encrypted) return pending || !(v.name in (inst.secrets ?? {}));
    return !inst.values?.[v.name];
  });
}

export type DefaultChange = { name: string; from: string; to: string };

// defaultChanges are the plain variables an upgrade changes without anyone
// typing a value: ones the instance holds no value for whose compose file
// value differs between the revisions (none reads as ""). A variable new in
// the target is not a change.
export function defaultChanges(
  from: DeclaredVariable[] | null | undefined,
  to: DeclaredVariable[] | null | undefined,
  inst: Pick<InstanceView, "values">,
): DefaultChange[] {
  const before = new Map((from ?? []).filter((v) => !v.encrypted).map((v) => [v.name, v.default ?? ""]));
  const held = inst.values ?? {};
  const out: DefaultChange[] = [];
  for (const v of to ?? []) {
    const was = before.get(v.name);
    if (v.encrypted || was === undefined || held[v.name]) continue;
    const now = v.default ?? "";
    if (now !== was) out.push({ name: v.name, from: was, to: now });
  }
  return out;
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
````

Modify `web/src/lib/templates.ts` (`git apply` this diff):

````diff
diff --git a/web/src/lib/templates.ts b/web/src/lib/templates.ts
--- a/web/src/lib/templates.ts
+++ b/web/src/lib/templates.ts
@@ -1,52 +1,70 @@
 // Templates and their instances: types mirroring the API
 // (internal/domain/template, internal/domain/usecase/templates) and the API
-// client. A template is org-level; an instance is an app on one server
-// rendered from a template revision. Secret parameter values travel only as
-// ECIES ciphertexts for the instance's server (lib/ecies); no route ever
-// returns one.
+// client. A template is an org's own (editable) or the catalog's (the same
+// for every org, read-only); an instance is an app on one server rendered
+// from a template revision. What an instance fills in, which ports it
+// exposes and which backups it can turn on come from the compose files'
+// x-winterflow block (detected). Secret values travel only as ECIES
+// ciphertexts for the instance's server (lib/ecies); no route ever returns
+// one.
 import { apiBase } from "@/config";
 import { WaitTimeoutError } from "@/context/notifications-context-base";
 import { createJsonClient, jsonPost } from "@/lib/api";
-import type { Keep } from "@/lib/backups";
 import { encryptSecret } from "@/lib/ecies";
+import type { DeclaredVariable } from "@/types/app-config";
 
-export type Parameter = {
-  name: string;
-  secret?: boolean;
-  required?: boolean;
-  // null/absent: no default ("" is a default, but renders the same).
-  default?: string | null;
-  pattern?: string; // Go RE2
-  description?: string;
-};
-
-// IngressRow routes host to the host port the compose file publishes; port
-// is a number or exactly "${PARAM}".
-export type IngressRow = { host: string; service?: string; port: number | string };
-
-export type BackupDefault = { enabled: boolean; storage?: string; every?: string; keep?: Keep | null };
-
-// TemplateBody is a revision's body. Go encodes an empty map or list as
-// null.
+// TemplateBody is a revision's body: the app's files and how the template
+// reads in a list. Go encodes an empty map as null.
 export type TemplateBody = {
   files: Record<string, string> | null;
-  parameters: Parameter[] | null;
-  app_name: string;
+  summary: string;
+  description: string;
   icon: string;
   color: string;
-  ingress: IngressRow[] | null;
-  backups: Record<string, BackupDefault> | null;
 };
 
+export type DetectedExpose = {
+  id: string; // <container>:<port>/<protocol>
+  target: string; // <container>:<port>: what a hostname's expose names
+  container: string;
+  port: number;
+  protocol: "http" | "tcp" | "udp";
+};
+
+export type DetectedBackup = { name: string; type: "sqlite" | "files" | "custom" };
+
+// Detected is what a body's compose files declare under x-winterflow.
+export type Detected = {
+  variables: DeclaredVariable[] | null;
+  expose: DetectedExpose[] | null;
+  backups: DetectedBackup[] | null;
+  warnings: string[] | null;
+};
+
+// Inspection is inspect-template's answer: what a body declares (as far
+// as it parses) and the first problem a save would refuse ("" when none).
+export type Inspection = { detected: Detected; problem: string };
+
+export type TemplateSource = "org" | "catalog";
+
 export type TemplateSummary = {
   id: string;
+  source: TemplateSource;
+  slug: string; // a catalog template's id in the catalog
   name: string;
   latest_revision: number;
-  instances: number; // pending and created
-  behind: number; // on a revision older than the latest
+  instances: number; // the org's pending and created ones
+  behind: number; // of those, on a revision older than the latest
+  withdrawn_at: string | null; // when the catalog dropped it
   updated_at: string;
 };
 
+// orgTemplates are the org's own templates of a list (the catalog's are
+// listed after them).
+export function orgTemplates<T extends Pick<TemplateSummary, "source">>(list: T[]): T[] {
+  return list.filter((t) => t.source === "org");
+}
+
 // usageLabel says how many apps use a template, in plain words: "Used by
 // 12 apps · 3 on an older version" ("Not used by any app yet" without any).
 export function usageLabel(t: Pick<TemplateSummary, "instances" | "behind">): string {
@@ -59,10 +77,14 @@ export type TemplateRevisionInfo = { revision: number; note: string; created_at:
 
 export type TemplateView = {
   id: string;
+  source: TemplateSource;
+  slug: string;
   name: string;
   latest_revision: number;
+  withdrawn_at: string | null;
   revision: number;
   body: TemplateBody;
+  detected: Detected;
   note: string;
   created_at: string;
   created_by: string;
@@ -81,6 +103,13 @@ export type SaveTemplateBody = {
 
 export type InstanceState = "pending" | "created" | "deleted" | "unlinked";
 
+// InstanceHost routes a hostname to an http expose entry (its target).
+export type InstanceHost = { host: string; expose: string };
+
+// BackupChoice is an instance's setting for one backup its compose file
+// declares; a backup without one starts off.
+export type BackupChoice = { enabled: boolean; storage?: string };
+
 export type InstanceView = {
   key: string;
   server_id: string;
@@ -88,8 +117,10 @@ export type InstanceView = {
   template_id: string;
   template_name: string;
   revision: number;
-  parameters: Record<string, string> | null; // values callers set
+  name: string; // the app name the create asked for
+  values: Record<string, string> | null; // plain values callers set
   secrets: Record<string, string> | null; // name -> "<encrypted>"
+  hosts: InstanceHost[] | null;
   state: InstanceState;
   status_code: number;
   last_error: string;
@@ -98,11 +129,12 @@ export type InstanceView = {
 };
 
 // InstanceValues are what create, update and upgrade send: plain values,
-// secret ciphertexts and (while pending) backup storages, by name.
+// secret ciphertexts, hostnames and (while pending) backup settings.
 export type InstanceValues = {
-  parameters?: Record<string, string>;
+  values?: Record<string, string>;
   secrets?: Record<string, string>;
-  backups?: Record<string, string>;
+  hosts?: InstanceHost[];
+  backups?: Record<string, BackupChoice>;
 };
 
 export type CreateInstanceBody = InstanceValues & {
@@ -110,6 +142,7 @@ export type CreateInstanceBody = InstanceValues & {
   key: string;
   template_id: string;
   revision?: number;
+  name?: string;
 };
 
 export type UpdateInstanceBody = InstanceValues & { server_id: string; key: string };
@@ -152,6 +185,8 @@ export type TemplatesApi = {
   // client reads each one once (the rest of the view is as of that read).
   getRevision(id: string, revision: number): Promise<TemplateView>;
   saveTemplate(body: SaveTemplateBody): Promise<SavedTemplate>;
+  // inspectTemplate reads a body as a save would, storing nothing.
+  inspectTemplate(body: TemplateBody): Promise<Inspection>;
   deleteTemplate(id: string): Promise<void>;
   listInstances(filter: { serverId?: string; templateId?: string }): Promise<InstanceView[]>;
   getInstance(serverId: string, key: string): Promise<InstanceView>;
@@ -259,6 +294,7 @@ export function createTemplatesApi(waitFor: WaitFor, fetchImpl?: typeof fetch):
     getTemplate,
     getRevision,
     saveTemplate: (body) => call<SavedTemplate>("/api/v1/template/save-template", jsonPost(body)),
+    inspectTemplate: (body) => call<Inspection>("/api/v1/template/inspect-template", jsonPost({ body })),
     deleteTemplate: async (id) => {
       await call<null>("/api/v1/template/delete-template", jsonPost({ id }));
     },
````

- [ ] **Step 4: Run them and see them pass**

```bash
CI=true pnpm --dir web exec vitest run src/lib/template-values.test.ts src/lib/templates.test.ts
CI=true pnpm --dir web run test
```

Expected: PASS — the two files pass and so does the whole suite (Vitest does not type-check: the components still reading the old fields compile again by Task 10, when `pnpm --dir web exec tsc -b` is green).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat(web): template types follow x-winterflow; values helpers" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 8: Web: instance panel, update dialog and instance list on declared variables

A `VariableFields` form over `DeclaredVariable`s (description label, compose value as placeholder, secrets masked, `$` and missing required values blocking, a pattern mismatch only a hint) replaces `ParameterFields` in the instance panel and the update dialog. The panel sends only changed values (a cleared one goes back to the compose file's) and lists the hostnames; the instance list names an app by its created name.

**Files:**
- Modify: `web/src/components/instance-panel.test.tsx`
- Modify: `web/src/components/instance-panel.tsx`
- Modify: `web/src/components/template-instances.test.tsx`
- Modify: `web/src/components/template-instances.tsx`
- Modify: `web/src/components/upgrade-instance-dialog.tsx`
- Create: `web/src/components/variable-fields.test.tsx`
- Create: `web/src/components/variable-fields.tsx`
- Modify: `web/src/pages/app-details.test.tsx`
- Modify: `web/src/pages/template-page.test.tsx`

**Interfaces:**
- Consumes: Task 7's `lib/template-values.ts` and types; `declaredPatternHint` (`lib/declared-variables`); `invalidAttrs`, `FieldMessage`.
- Produces: `components/variable-fields.tsx` `VariableFields({ vars, values, onChange, kept?, showRequired? })`; `InstancePanel` and `UpgradeInstanceDialog` read `TemplateView.detected.variables` and send `values`; `TemplateInstances` names an app `inst.name || view.name`.

- [ ] **Step 1: Write the failing tests**

Modify `web/src/components/instance-panel.test.tsx` (`git apply` this diff):

````diff
diff --git a/web/src/components/instance-panel.test.tsx b/web/src/components/instance-panel.test.tsx
--- a/web/src/components/instance-panel.test.tsx
+++ b/web/src/components/instance-panel.test.tsx
@@ -16,8 +16,10 @@ const instance: InstanceView = {
   template_id: "t1",
   template_name: "twillingate",
   revision: 2,
-  parameters: { NAME: "bakomi" },
+  name: "bakomi",
+  values: { NAME: "bakomi" },
   secrets: {},
+  hosts: [{ host: "bakomi.example.com", expose: "app:8080" }],
   state: "created",
   status_code: 1,
   last_error: "",
@@ -25,20 +27,19 @@ const instance: InstanceView = {
   updated_at: "2026-10-12T10:00:00Z",
 };
 
+const NAME = { name: "NAME", required: true, encrypted: false, default: null, services: ["app"] };
+const LANG = { name: "LANG", required: false, encrypted: false, default: "en", services: ["app"] };
+
 const current: TemplateView = {
   id: "t1",
+  source: "org",
+  slug: "",
   name: "twillingate",
   latest_revision: 2,
+  withdrawn_at: null,
   revision: 2,
-  body: {
-    files: { "compose.yaml": "services: {}\n" },
-    parameters: [{ name: "NAME", required: true }],
-    app_name: "${NAME}",
-    icon: "",
-    color: "",
-    ingress: null,
-    backups: null,
-  },
+  body: { files: { "compose.yaml": "services: {}\n" }, summary: "", description: "", icon: "", color: "" },
+  detected: { variables: [NAME], expose: [], backups: [], warnings: [] },
   note: "",
   created_at: "2026-10-12T10:00:00Z",
   created_by: "u1",
@@ -104,13 +105,7 @@ describe("InstancePanel", () => {
     api.updateInstance = updateInstance;
     const withSecret: TemplateView = {
       ...current,
-      body: {
-        ...current.body,
-        parameters: [
-          { name: "NAME", required: true },
-          { name: "TOKEN", required: true, secret: true },
-        ],
-      },
+      detected: { ...current.detected, variables: [NAME, { name: "TOKEN", required: true, encrypted: true, default: null, services: ["app"] }] },
     };
     renderPanel({ instance: { ...instance, state: "pending", secrets: {} }, current: withSecret });
     await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
@@ -125,6 +120,29 @@ describe("InstancePanel", () => {
     expect(screen.getByText("Unsaved changes")).toBeInTheDocument();
   });
 
+  it("sends only the values that changed; a cleared one goes back to the compose file's", async () => {
+    const user = userEvent.setup();
+    const updateInstance = vi.fn(async () => {});
+    api.updateInstance = updateInstance;
+    const onSaved = vi.fn();
+    renderPanel({
+      instance: { ...instance, values: { NAME: "bakomi", LANG: "de" } },
+      current: { ...current, detected: { ...current.detected, variables: [NAME, LANG] } },
+      onSaved,
+    });
+    expect(screen.getByLabelText("LANG")).toHaveAttribute("placeholder", "Default: en");
+    await user.clear(screen.getByLabelText("LANG"));
+    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
+    await waitFor(() => expect(onSaved).toHaveBeenCalled());
+    expect(updateInstance).toHaveBeenCalledWith({ server_id: "s", key: "k1", values: { LANG: "" } });
+  });
+
+  it("shows the hostnames the app was created with", () => {
+    renderPanel();
+    expect(screen.getByText("bakomi.example.com")).toBeInTheDocument();
+    expect(screen.getByText("→ app:8080")).toBeInTheDocument();
+  });
+
   it("keeps the bar to Save and a ⋯ menu on a phone once edited", async () => {
     vi.stubGlobal("matchMedia", () => ({ matches: true, addEventListener: () => {}, removeEventListener: () => {} }));
     const user = userEvent.setup();
````

Modify `web/src/components/template-instances.test.tsx` (`git apply` this diff):

````diff
diff --git a/web/src/components/template-instances.test.tsx b/web/src/components/template-instances.test.tsx
--- a/web/src/components/template-instances.test.tsx
+++ b/web/src/components/template-instances.test.tsx
@@ -7,6 +7,7 @@ import { TemplateInstances } from "@/components/template-instances";
 import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
 import type { InstanceView, TemplateView } from "@/lib/templates";
 import { fakeAPI } from "@/test/fake-api";
+import type { DeclaredVariable } from "@/types/app-config";
 
 const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
 vi.mock("sonner", () => ({ toast }));
@@ -20,24 +21,29 @@ vi.mock("@/context/use-servers", () => ({
 
 const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.fn() } as NotificationsContextValue;
 
+const v = (name: string, extra: Partial<DeclaredVariable> = {}): DeclaredVariable => ({
+  name,
+  required: false,
+  encrypted: false,
+  default: null,
+  services: ["app"],
+  ...extra,
+});
+
 const VIEW: TemplateView = {
   id: "t1",
+  source: "org",
+  slug: "",
   name: "twillingate",
   latest_revision: 3,
+  withdrawn_at: null,
   revision: 3,
-  body: {
-    files: { "compose.yaml": "services: {}\n" },
-    parameters: [
-      { name: "NAME", required: true },
-      { name: "REGION", required: true },
-      { name: "SESSION_KEY", secret: true, required: true },
-      { name: "GREETING", default: "Hiya" },
-    ],
-    app_name: "${NAME}",
-    icon: "",
-    color: "",
-    ingress: null,
-    backups: null,
+  body: { files: { "compose.yaml": "services: {}\n" }, summary: "", description: "", icon: "", color: "" },
+  detected: {
+    variables: [v("NAME", { required: true }), v("REGION", { required: true }), v("SESSION_KEY", { required: true, encrypted: true }), v("GREETING", { default: "Hiya" })],
+    expose: [],
+    backups: [],
+    warnings: [],
   },
   note: "",
   created_at: "2026-10-12T10:00:00Z",
@@ -45,13 +51,13 @@ const VIEW: TemplateView = {
   revisions: null,
 };
 
-// R2 is the revision the instances run: GREETING's default changes in r3.
+// R2 is the revision the instances run: GREETING's compose value changes in r3.
 const R2: TemplateView = {
   ...VIEW,
   revision: 2,
-  body: {
-    ...VIEW.body,
-    parameters: [{ name: "NAME", required: true }, { name: "SESSION_KEY", secret: true, required: true }, { name: "GREETING", default: "Howdy" }],
+  detected: {
+    ...VIEW.detected,
+    variables: [v("NAME", { required: true }), v("SESSION_KEY", { required: true, encrypted: true }), v("GREETING", { default: "Howdy" })],
   },
 };
 const R2_ROUTE = { "/api/v1/template/get-template": R2 };
@@ -63,8 +69,10 @@ const inst = (over: Partial<InstanceView>): InstanceView => ({
   template_id: "t1",
   template_name: "twillingate",
   revision: 2,
-  parameters: { NAME: "bakomi" },
+  name: "bakomi",
+  values: { NAME: "bakomi" },
   secrets: { SESSION_KEY: "<encrypted>" },
+  hosts: [],
   state: "created",
   status_code: 1,
   last_error: "",
@@ -96,7 +104,7 @@ describe("TemplateInstances", () => {
   it("lists the apps per server with their version and status", () => {
     renderInstances([
       inst({}),
-      inst({ key: "k2", server_id: "s2", app_id: "a2", revision: 3, parameters: { NAME: "shop" }, state: "pending", last_error: "pull failed" }),
+      inst({ key: "k2", server_id: "s2", app_id: "a2", revision: 3, name: "shop", values: { NAME: "shop" }, state: "pending", last_error: "pull failed" }),
     ]);
     const rows = screen.getAllByRole("listitem");
     expect(within(rows[0]).getByRole("link", { name: "bakomi" })).toHaveAttribute("href", "/server/s1/app/a1");
@@ -109,12 +117,12 @@ describe("TemplateInstances", () => {
     expect(within(rows[1]).queryByRole("button", { name: "Update" })).not.toBeInTheDocument();
   });
 
-  it("shows a renamed app by its current name, the rendered one for an app no longer listed", async () => {
+  it("shows a renamed app by its current name, the created one for an app no longer listed", async () => {
     fakeAPI({
       "/api/v1/app/get-apps": (_: unknown, q: URLSearchParams) =>
         q.get("server_id") === "s1" ? { apps: [{ id: "a1", name: "Family Notes" }] } : { apps: [] },
     });
-    renderInstances([inst({}), inst({ key: "k2", server_id: "s2", app_id: "gone", parameters: { NAME: "shop" } })]);
+    renderInstances([inst({}), inst({ key: "k2", server_id: "s2", app_id: "gone", name: "shop", values: { NAME: "shop" } })]);
     expect(await screen.findByRole("link", { name: "Family Notes" })).toHaveAttribute("href", "/server/s1/app/a1");
     expect(screen.queryByRole("link", { name: "bakomi" })).not.toBeInTheDocument();
     expect(screen.getByRole("link", { name: "shop" })).toBeInTheDocument();
@@ -138,29 +146,30 @@ describe("TemplateInstances", () => {
     expect(within(dialog).queryByLabelText("NAME")).not.toBeInTheDocument();
     expect(within(dialog).queryByLabelText("SESSION_KEY")).not.toBeInTheDocument();
     await user.click(within(dialog).getByRole("button", { name: "Update to version 3" }));
-    expect(within(dialog).getByText("Required")).toBeInTheDocument();
+    expect(within(dialog).getByText("Enter a value: this variable is required.")).toBeInTheDocument();
     expect(api.posted("/api/v1/instance/upgrade-instance")).toEqual([]);
 
     await user.type(within(dialog).getByLabelText("REGION"), "eu");
     await user.click(within(dialog).getByRole("button", { name: "Update to version 3" }));
     await waitFor(() => expect(onChanged).toHaveBeenCalled());
     expect(api.posted("/api/v1/instance/upgrade-instance")).toEqual([
-      { server_id: "s1", key: "k1", revision: 3, parameters: { REGION: "eu" } },
+      { server_id: "s1", key: "k1", revision: 3, values: { REGION: "eu" } },
     ]);
     expect(toast.success).toHaveBeenCalledWith("Updated to version 3");
     expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
   });
 
-  it("names the parameters left on their default whose value the upgrade changes", async () => {
+  it("names the variables left to the compose file whose value the upgrade changes", async () => {
     const user = userEvent.setup();
     const api = fakeAPI(R2_ROUTE);
     renderInstances([inst({})]);
     await user.click(screen.getByRole("button", { name: "Update" }));
     const dialog = await screen.findByRole("dialog");
     expect(within(dialog).getByRole("heading")).toHaveTextContent("Update bakomi to version 3");
-    expect(dialog).toHaveTextContent("Values you set stay; parameters left on their default take the new default.");
+    expect(dialog).toHaveTextContent("Values you set stay; variables left to the compose file take its new value.");
+    expect(dialog).toHaveTextContent("a backup the version adds starts off");
     const changes = await within(dialog).findByRole("list", { name: "Defaults that change" });
-    expect(changes).toHaveTextContent("GREETING: Howdy → Hiya (follows the template default)");
+    expect(changes).toHaveTextContent("GREETING: Howdy → Hiya (follows the compose file)");
     expect(api.calls.find((c) => c.path === "/api/v1/template/get-template")?.query).toBe("?id=t1&revision=2");
     expect(dialog).not.toHaveTextContent("The app will be started.");
   });
@@ -168,7 +177,7 @@ describe("TemplateInstances", () => {
   it("leaves out a default the instance overrides and says a stopped app starts", async () => {
     const user = userEvent.setup();
     fakeAPI(R2_ROUTE);
-    renderInstances([inst({ status_code: 5, parameters: { NAME: "bakomi", GREETING: "Yo" } })]);
+    renderInstances([inst({ status_code: 5, values: { NAME: "bakomi", GREETING: "Yo" } })]);
     await user.click(screen.getByRole("button", { name: "Update" }));
     const dialog = await screen.findByRole("dialog");
     expect(dialog).toHaveTextContent("The app will be started.");
@@ -192,7 +201,7 @@ describe("TemplateInstances", () => {
       "/api/v1/server/get-public-key": { public_key: "K2" },
       "/api/v1/instance/upgrade-instance": { instance: inst({}), request_id: "", warnings: [] },
     });
-    renderInstances([inst({ server_id: "s2", state: "pending", parameters: { NAME: "x", REGION: "eu" } })]);
+    renderInstances([inst({ server_id: "s2", state: "pending", values: { NAME: "x", REGION: "eu" } })]);
     await user.click(screen.getByRole("button", { name: "Update" }));
     const dialog = await screen.findByRole("dialog");
     await user.type(within(dialog).getByLabelText("SESSION_KEY"), "hunter2");
@@ -207,7 +216,7 @@ describe("TemplateInstances", () => {
   it("keeps the dialog open with the reason when the server has no key", async () => {
     const user = userEvent.setup();
     const api = fakeAPI({ ...R2_ROUTE, "/api/v1/server/get-public-key": new Error("public key unavailable for this server") });
-    renderInstances([inst({ secrets: null, parameters: { NAME: "x", REGION: "eu" } })]);
+    renderInstances([inst({ secrets: null, values: { NAME: "x", REGION: "eu" } })]);
     await user.click(screen.getByRole("button", { name: "Update" }));
     const dialog = await screen.findByRole("dialog");
     await user.type(within(dialog).getByLabelText("SESSION_KEY"), "hunter2");
````

Create `web/src/components/variable-fields.test.tsx`:

````tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { VariableFields } from "@/components/variable-fields";
import type { DeclaredVariable } from "@/types/app-config";

const v = (name: string, extra: Partial<DeclaredVariable> = {}): DeclaredVariable => ({
  name,
  required: false,
  encrypted: false,
  default: null,
  services: ["app"],
  ...extra,
});

describe("VariableFields", () => {
  it("labels a field by its description, its name small beside it, and tags a secret", () => {
    render(
      <VariableFields
        vars={[v("TITLE", { default: "Notes" }), v("BASE_URL", { required: true, description: "Public URL" }), v("TOKEN", { encrypted: true })]}
        values={{}}
        onChange={vi.fn()}
      />,
    );
    const url = screen.getByLabelText("Public URL (BASE_URL)");
    const label = document.querySelector(`label[for="${url.id}"]`)!;
    expect(label).toHaveTextContent("Public URL*BASE_URL");
    const secret = document.querySelector(`label[for="${screen.getByLabelText("TOKEN").id}"]`)!;
    expect(secret).toHaveTextContent("TOKENSecret");
    expect(screen.getByLabelText("TITLE")).toHaveAttribute("placeholder", "Default: Notes");
    // Required first.
    expect(screen.getAllByRole("textbox")[0]).toBe(url);
  });

  it("flags a $ as typed, a missing value once tried, and hints a pattern mismatch", async () => {
    const values: Record<string, string> = { NAME: "$x", LANG: "english" };
    const vars = [v("NAME"), v("REQ", { required: true }), v("LANG", { pattern: "[a-z]{2}" }), v("KEY", { required: true, encrypted: true })];
    const { rerender } = render(<VariableFields vars={vars} values={values} onChange={vi.fn()} kept={["KEY"]} />);
    expect(screen.getByLabelText("NAME")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByText("A plain value can't contain $.")).toBeInTheDocument();
    expect(screen.queryByText("Enter a value: this variable is required.")).not.toBeInTheDocument();
    expect(screen.getByText("May not match [a-z]{2}; the server checks it on save.")).toBeInTheDocument();
    expect(screen.getByLabelText("KEY")).toHaveAttribute("placeholder", "Stored, leave blank to keep");
    rerender(<VariableFields vars={vars} values={values} onChange={vi.fn()} kept={["KEY"]} showRequired />);
    // The kept secret may stay blank; REQ may not.
    expect(screen.getAllByText("Enter a value: this variable is required.")).toHaveLength(1);
  });

  it("reports each change by name", async () => {
    const onChange = vi.fn();
    render(<VariableFields vars={[v("NAME")]} values={{}} onChange={onChange} />);
    await userEvent.setup().type(screen.getByLabelText("NAME"), "a");
    expect(onChange).toHaveBeenCalledWith("NAME", "a");
  });
});
````

Modify `web/src/pages/app-details.test.tsx` (`git apply` this diff):

````diff
diff --git a/web/src/pages/app-details.test.tsx b/web/src/pages/app-details.test.tsx
--- a/web/src/pages/app-details.test.tsx
+++ b/web/src/pages/app-details.test.tsx
@@ -11,6 +11,7 @@ import AppDetailsPage from "@/pages/app-details";
 import type { BackupHealth } from "@/lib/backups";
 import type { InstanceView, TemplateView } from "@/lib/templates";
 import type { DraftState } from "@/lib/template-draft";
+import type { DeclaredVariable } from "@/types/app-config";
 import { trackActivity } from "@/lib/backup-activity";
 import { fakeAPI } from "@/test/fake-api";
 import { flushPending } from "@/test/flush";
@@ -722,8 +723,10 @@ const INSTANCE: InstanceView = {
   template_id: "t1",
   template_name: "twillingate",
   revision: 2,
-  parameters: { NAME: "bakomi" },
+  name: "bakomi",
+  values: { NAME: "bakomi" },
   secrets: { SESSION_KEY: "<encrypted>" },
+  hosts: [],
   state: "created",
   status_code: 1,
   last_error: "",
@@ -731,19 +734,35 @@ const INSTANCE: InstanceView = {
   updated_at: "2026-10-12T10:00:00Z",
 };
 
-const template = (revision: number, extra: TemplateView["body"]["parameters"] = []): TemplateView => ({
+const variable = (name: string, extra: Partial<DeclaredVariable> = {}): DeclaredVariable => ({
+  name,
+  required: false,
+  encrypted: false,
+  default: null,
+  services: ["app"],
+  ...extra,
+});
+
+const template = (revision: number, extra: DeclaredVariable[] = []): TemplateView => ({
   id: "t1",
+  source: "org",
+  slug: "",
   name: "twillingate",
   latest_revision: 3,
+  withdrawn_at: null,
   revision,
   body: {
     files: { "compose.yaml": "services:\n  app:\n    image: twillingate:${VERSION}\n" },
-    parameters: [{ name: "NAME", required: true }, { name: "SESSION_KEY", secret: true, required: true }, ...(extra ?? [])],
-    app_name: "${NAME}",
+    summary: "",
+    description: "",
     icon: "",
     color: "",
-    ingress: null,
-    backups: null,
+  },
+  detected: {
+    variables: [variable("NAME", { required: true }), variable("SESSION_KEY", { required: true, encrypted: true }), ...extra],
+    expose: [],
+    backups: [],
+    warnings: [],
   },
   note: "",
   created_at: "2026-10-12T10:00:00Z",
@@ -757,7 +776,7 @@ function instanceAPI(inst: InstanceView = INSTANCE, extra: Record<string, unknow
   return fakeAPI({
     "/api/v1/instance/get-instances": { instances: [inst] },
     "/api/v1/template/get-template": (_: unknown, q: URLSearchParams) =>
-      q.get("revision") ? template(Number(q.get("revision"))) : template(3, [{ name: "REGION", required: true }]),
+      q.get("revision") ? template(Number(q.get("revision"))) : template(3, [variable("REGION", { required: true })]),
     "/api/v1/server/get-public-key": { public_key: "K" },
     ...extra,
   });
@@ -784,7 +803,7 @@ describe("AppDetailsPage of a template instance", () => {
     await user.click(within(dialog).getByRole("button", { name: "Update to version 3" }));
     await waitFor(() => expect(refresh).toHaveBeenCalled());
     expect(api.posted("/api/v1/instance/upgrade-instance")).toEqual([
-      { server_id: "s", key: "k1", revision: 3, parameters: { REGION: "eu" } },
+      { server_id: "s", key: "k1", revision: 3, values: { REGION: "eu" } },
     ]);
   });
 
@@ -824,7 +843,7 @@ describe("AppDetailsPage of a template instance", () => {
     expect(within(dialog).getByRole("heading")).toHaveTextContent("Update Shop to version 3");
   });
 
-  it("shows the files read-only and a Parameters form instead of the editor", async () => {
+  it("shows the files read-only and a Variables form instead of the editor", async () => {
     const user = userEvent.setup();
     const api = instanceAPI(INSTANCE, { "/api/v1/instance/update-instance": { instance: INSTANCE, request_id: "", warnings: [] } });
     renderPage("/app/a1?tab=editor", {}, managed);
@@ -838,15 +857,15 @@ describe("AppDetailsPage of a template instance", () => {
     await user.type(screen.getByLabelText("NAME"), "shop");
     await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
     await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Saved and redeployed"));
-    expect(api.posted("/api/v1/instance/update-instance")).toEqual([{ server_id: "s", key: "k1", parameters: { NAME: "shop" } }]);
+    expect(api.posted("/api/v1/instance/update-instance")).toEqual([{ server_id: "s", key: "k1", values: { NAME: "shop" } }]);
     expect(refresh).toHaveBeenCalled();
   });
 
-  it("sends a cleared value's default (the API would keep an empty value), else the empty value", async () => {
+  it("sends a cleared value as empty: the compose file's own value stands again", async () => {
     const user = userEvent.setup();
-    const extra = [{ name: "GREETING", default: "Hello" }, { name: "MOTTO" }];
+    const extra = [variable("GREETING", { default: "Hello" }), variable("MOTTO")];
     const api = instanceAPI(
-      { ...INSTANCE, parameters: { NAME: "bakomi", GREETING: "Hi", MOTTO: "carpe diem" } },
+      { ...INSTANCE, values: { NAME: "bakomi", GREETING: "Hi", MOTTO: "carpe diem" } },
       {
         "/api/v1/template/get-template": (_: unknown, q: URLSearchParams) => template(Number(q.get("revision") || 3), extra),
         "/api/v1/instance/update-instance": { instance: INSTANCE, request_id: "", warnings: [] },
@@ -858,7 +877,7 @@ describe("AppDetailsPage of a template instance", () => {
     await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
     await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Saved and redeployed"));
     expect(api.posted("/api/v1/instance/update-instance")).toEqual([
-      { server_id: "s", key: "k1", parameters: { GREETING: "Hello", MOTTO: "" } },
+      { server_id: "s", key: "k1", values: { GREETING: "", MOTTO: "" } },
     ]);
   });
 
@@ -894,7 +913,7 @@ describe("AppDetailsPage of a template instance", () => {
     expect(screen.getAllByText("Failed").length).toBeGreaterThan(0);
     expect(screen.getByLabelText("SESSION_KEY")).toHaveAttribute("placeholder", "Encrypted in your browser");
     await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
-    expect(screen.getByText("Required")).toBeInTheDocument();
+    expect(screen.getByText("Enter a value: this variable is required.")).toBeInTheDocument();
   });
 
   it("reads Running for a pending instance whose app a report shows running, keeping the failure's banner", async () => {
@@ -1045,7 +1064,7 @@ describe("AppDetailsPage of a template instance", () => {
     expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
   });
 
-  it("shows a failed save of the parameters", async () => {
+  it("shows a failed save of the values", async () => {
     const user = userEvent.setup();
     instanceAPI(INSTANCE, { "/api/v1/instance/update-instance": new Error("render failed") });
     renderPage("/app/a1?tab=editor", {}, managed);
````

Modify `web/src/pages/template-page.test.tsx` (`git apply` this diff):

````diff
diff --git a/web/src/pages/template-page.test.tsx b/web/src/pages/template-page.test.tsx
--- a/web/src/pages/template-page.test.tsx
+++ b/web/src/pages/template-page.test.tsx
@@ -52,13 +52,25 @@ const BODY: TemplateBody = {
   backups: { db: { enabled: true, storage: "tenants" } },
 };
 
+// DETECTED is what the compose file declares (the update dialog reads it).
+const DETECTED = {
+  variables: [{ name: "NAME", required: true, encrypted: false, default: null, services: ["app"] }],
+  expose: [],
+  backups: [],
+  warnings: [],
+};
+
 function view(revision: number): TemplateView {
   return {
     id: "t1",
+    source: "org",
+    slug: "",
     name: "twillingate",
     latest_revision: revision,
+    withdrawn_at: null,
     revision,
     body: BODY,
+    detected: DETECTED,
     note: "",
     created_at: "2026-10-12T10:00:00Z",
     created_by: "u1",
@@ -423,8 +435,10 @@ describe("TemplatePage", () => {
       template_id: "t1",
       template_name: "twillingate",
       revision,
-      parameters: { NAME: "bakomi", PORT: "8081", VERSION: "1.0" },
+      name: "bakomi",
+      values: { NAME: "bakomi" },
       secrets: null,
+      hosts: [],
       state: "created",
       status_code: 1,
       last_error: "",
@@ -459,7 +473,7 @@ describe("TemplatePage", () => {
   it("updates all apps behind after a confirm, and shows the rollout", async () => {
     const user = userEvent.setup();
     const behind = { key: "k1", server_id: "s1", app_id: "a1", template_id: "t1", template_name: "twillingate", revision: 2,
-      parameters: { NAME: "bakomi" }, secrets: null, state: "created", status_code: 1, last_error: "",
+      name: "bakomi", values: { NAME: "bakomi" }, secrets: null, hosts: [], state: "created", status_code: 1, last_error: "",
       created_at: "2026-10-12T10:00:00Z", updated_at: "2026-10-12T10:00:00Z" };
     const api = fakeAPI({
       "/api/v1/template/get-template": view(3),
@@ -486,7 +500,7 @@ describe("TemplatePage", () => {
     const user = userEvent.setup();
     fakeAPI({
       "/api/v1/template/get-template": view(3),
-      "/api/v1/instance/get-instances": { instances: [{ key: "k1", server_id: "s1", app_id: "a1", revision: 1, state: "created", parameters: {} }] },
+      "/api/v1/instance/get-instances": { instances: [{ key: "k1", server_id: "s1", app_id: "a1", revision: 1, state: "created", values: {} }] },
       "/api/v1/template/upgrade-instances": Object.assign(new Error("an upgrade of this template is already running"), { status: 409 }),
     });
     renderPage("/templates/t1?tab=apps");
````

- [ ] **Step 2: Run them and see them fail**

Run: `CI=true pnpm --dir web exec vitest run src/components/variable-fields.test.tsx src/components/instance-panel.test.tsx src/components/template-instances.test.tsx src/pages/app-details.test.tsx src/pages/template-page.test.tsx`
Expected: FAIL — `@/components/variable-fields` does not exist; the panel and dialog still read `body.parameters` and send `parameters`.

- [ ] **Step 3: Implement**

Modify `web/src/components/instance-panel.tsx` (`git apply` this diff):

````diff
diff --git a/web/src/components/instance-panel.tsx b/web/src/components/instance-panel.tsx
--- a/web/src/components/instance-panel.tsx
+++ b/web/src/components/instance-panel.tsx
@@ -8,10 +8,10 @@ import { CodeEditor } from "@/components/code-editor";
 import { ConfigBar } from "@/components/config-bar";
 import { EditorSection } from "@/components/editor-section";
 import { InlineError } from "@/components/inline-error";
-import { ParameterFields } from "@/components/parameter-fields";
+import { VariableFields } from "@/components/variable-fields";
 import { useTemplatesApi } from "@/hooks/use-templates-api";
 import { focusFirstInvalid } from "@/lib/field-errors";
-import { fieldErrors, splitValues } from "@/lib/template-params";
+import { splitValues, variableErrors } from "@/lib/template-values";
 import { sealSecrets, type InstanceView, type TemplateView } from "@/lib/templates";
 import { templatePath } from "@/lib/routes";
 
@@ -19,16 +19,18 @@ import { templatePath } from "@/lib/routes";
 // instance holds (secrets start blank: blank keeps them).
 function storedValues(current: TemplateView, instance: InstanceView): Record<string, string> {
   const out: Record<string, string> = {};
-  for (const p of current.body.parameters ?? []) if (!p.secret) out[p.name] = instance.parameters?.[p.name] ?? "";
+  for (const v of current.detected.variables ?? []) if (!v.encrypted) out[v.name] = instance.values?.[v.name] ?? "";
   return out;
 }
 
 // InstancePanel is the Configuration tab of an app a template manages: the
-// template's files read-only, and in place of the editor a Parameters form
-// whose Save re-renders and redeploys the app. A secret keeps its value
-// unless a new one is typed; a pending instance (never created) holds none,
-// so every required secret is asked for again. When the template has a
-// newer version, an Update opens the page's update dialog.
+// template's files read-only, and in place of the editor the form over the
+// variables its compose files declare, whose Save re-renders and redeploys
+// the app. A value cleared goes back to the compose file's own. A secret
+// keeps its value unless a new one is typed; a pending instance (never
+// created) holds none, so every required secret is asked for again. When
+// the template has a newer version, an Update opens the page's update
+// dialog.
 export function InstancePanel({
   instance,
   current,
@@ -58,14 +60,15 @@ export function InstancePanel({
   const [saving, setSaving] = useState(false);
   const [error, setError] = useState("");
   const root = useRef<HTMLDivElement>(null);
-  const params = current.body.parameters ?? [];
+  const vars = current.detected.variables ?? [];
+  const hosts = instance.hosts ?? [];
   const kept = instance.state === "created" ? Object.keys(instance.secrets ?? {}) : [];
-  const dirty = params.some((p) => (values[p.name] ?? "") !== (initial[p.name] ?? ""));
+  const dirty = vars.some((v) => (values[v.name] ?? "") !== (initial[v.name] ?? ""));
   useEffect(() => {
     onDirtyChange?.(dirty);
   }, [dirty, onDirtyChange]);
   useEffect(() => () => onDirtyChange?.(false), [onDirtyChange]);
-  const refused = tried && Object.keys(fieldErrors(params, values, kept)).length > 0;
+  const refused = tried && Object.keys(variableErrors(vars, values, kept)).length > 0;
   const newer = onUpgrade !== undefined && latestRevision !== undefined && latestRevision > instance.revision;
   const note = (
     <>
@@ -79,28 +82,23 @@ export function InstancePanel({
 
   const save = async () => {
     setTried(true);
-    if (Object.keys(fieldErrors(params, values, kept)).length > 0) {
-      // ParameterFields marks each bad field once tried is set.
+    if (Object.keys(variableErrors(vars, values, kept)).length > 0) {
+      // VariableFields marks each bad field once tried is set.
       focusFirstInvalid(root.current);
       return;
     }
     setSaving(true);
     setError("");
     try {
-      const { plain, typed } = splitValues(params, values);
-      const parameters: Record<string, string> = {};
-      for (const p of params) {
-        const v = plain[p.name];
-        if (v === undefined || v === (initial[p.name] ?? "")) continue;
-        // A cleared value means "the default" here (the form says so), but
-        // the API keeps a given "" as set: send the default itself.
-        parameters[p.name] = v === "" && p.default ? p.default : v;
-      }
+      const { plain, typed } = splitValues(vars, values);
+      // Only what changed; a cleared value ("") goes back to the compose
+      // file's own.
+      const changed = Object.fromEntries(Object.entries(plain).filter(([name, v]) => v !== (initial[name] ?? "")));
       const secrets = await sealSecrets(api, instance.server_id, typed);
       await api.updateInstance({
         server_id: instance.server_id,
         key: instance.key,
-        ...(Object.keys(parameters).length ? { parameters } : {}),
+        ...(Object.keys(changed).length ? { values: changed } : {}),
         ...(Object.keys(secrets).length ? { secrets } : {}),
       });
       toast.success("Saved and redeployed");
@@ -155,14 +153,14 @@ export function InstancePanel({
         </InlineError>
       ) : null}
       <EditorSection
-        title="Parameters"
-        description="The values this app was created with. Secret values are encrypted in your browser for this server."
+        title="Variables"
+        description="The values this app was created with; a field left empty keeps the compose file's own. Secret values are encrypted in your browser for this server."
       >
-        {params.length === 0 ? (
+        {vars.length === 0 ? (
           <p className="text-sm text-muted-foreground">The template takes no values.</p>
         ) : (
-          <ParameterFields
-            params={params}
+          <VariableFields
+            vars={vars}
             values={values}
             onChange={(name, v) => setValues((prev) => ({ ...prev, [name]: v }))}
             kept={kept}
@@ -170,6 +168,18 @@ export function InstancePanel({
           />
         )}
       </EditorSection>
+      {hosts.length > 0 ? (
+        <EditorSection title="Hostnames" description="Set when the app was created from the template.">
+          <ul className="space-y-1 text-sm">
+            {hosts.map((h) => (
+              <li key={h.host}>
+                <span className="font-mono">{h.host}</span>
+                <span className="text-muted-foreground"> → {h.expose}</span>
+              </li>
+            ))}
+          </ul>
+        </EditorSection>
+      ) : null}
       <EditorSection title="Files" description="Read-only: the template's files, as the app runs them.">
         {Object.entries(current.body.files ?? {})
           .sort(([a], [b]) => a.localeCompare(b))
````

Modify `web/src/components/template-instances.tsx` (`git apply` this diff):

````diff
diff --git a/web/src/components/template-instances.tsx b/web/src/components/template-instances.tsx
--- a/web/src/components/template-instances.tsx
+++ b/web/src/components/template-instances.tsx
@@ -17,12 +17,12 @@ import { useTemplatesApi } from "@/hooks/use-templates-api";
 import { appLabel } from "@/lib/kpi";
 import { appPath } from "@/lib/routes";
 import { appStatus } from "@/lib/status";
-import { expand, plainValues } from "@/lib/template-params";
 import type { InstanceView, TemplateView } from "@/lib/templates";
 
-// instanceName is an app's name as the latest version renders it.
+// instanceName is the name an app was created with (the template's when it
+// asked for none).
 function instanceName(view: TemplateView, inst: InstanceView): string {
-  return appLabel(expand(view.body.app_name, plainValues(view.body.parameters, inst.parameters ?? {})));
+  return appLabel(inst.name || view.name);
 }
 
 // TemplateInstances is the "Apps using it" tab: the apps a template runs
@@ -54,7 +54,7 @@ export function TemplateInstances({
   const [confirmAll, setConfirmAll] = useState(false);
   const serverName = (id: string) => servers.find((s) => s.id === id)?.name ?? "a server";
   // An app renamed since it was created goes by its current name; the
-  // rendered one stands in for an app the catalog doesn't list.
+  // created one stands in for an app the catalog doesn't list.
   const current = useAppNames(instances.map((i) => i.server_id));
   const nameOf = (inst: InstanceView) => {
     const live = current.get(inst.app_id);
@@ -157,7 +157,7 @@ export function TemplateInstances({
         onOpenChange={setConfirmAll}
         destructive={false}
         title={`Update ${behind} ${behind === 1 ? "app" : "apps"} to version ${latest}?`}
-        description="They are updated one at a time, oldest first, each keeping its own values. Stopped apps will be started. An app that needs a new value is skipped (update it on its own); the first failure stops the rest."
+        description="They are updated one at a time, oldest first, each keeping its own values and hostnames. Stopped apps will be started. An app that needs a new value is skipped (update it on its own); the first failure stops the rest."
         confirmLabel="Update all"
         onConfirm={() => onUpgradeAll?.()}
       />
````

Modify `web/src/components/upgrade-instance-dialog.tsx` (`git apply` this diff):

````diff
diff --git a/web/src/components/upgrade-instance-dialog.tsx b/web/src/components/upgrade-instance-dialog.tsx
--- a/web/src/components/upgrade-instance-dialog.tsx
+++ b/web/src/components/upgrade-instance-dialog.tsx
@@ -3,11 +3,11 @@ import { toast } from "sonner";
 
 import { Button } from "@/components/ui/button";
 import { InlineError } from "@/components/inline-error";
-import { ParameterFields } from "@/components/parameter-fields";
 import { ResponsiveDialog } from "@/components/responsive-dialog";
+import { VariableFields } from "@/components/variable-fields";
 import { useTemplatesApi } from "@/hooks/use-templates-api";
 import { AppStatusCode } from "@/context/apps-context-base";
-import { defaultChanges, fieldErrors, splitValues, upgradeNeeds } from "@/lib/template-params";
+import { defaultChanges, splitValues, upgradeNeeds, variableErrors } from "@/lib/template-values";
 import { sealSecrets, type InstanceView, type TemplateView } from "@/lib/templates";
 
 type Props = {
@@ -26,11 +26,11 @@ type Props = {
 
 // UpgradeInstanceDialog updates one app to another version of its template,
 // asking only for the values the app doesn't hold yet (a newly required
-// parameter, a pending app's secrets), and naming the parameters left on
-// their default whose default the update changes.
+// variable, a pending app's secrets), and naming the variables left to the
+// compose file whose value the update changes.
 export function UpgradeInstanceDialog({ instance, appName, target, current, onClose, onDone }: Props) {
   const api = useTemplatesApi();
-  const needs = upgradeNeeds(target.body.parameters, instance);
+  const needs = upgradeNeeds(target.detected.variables, instance);
   const given =
     current && current.id === instance.template_id && current.revision === instance.revision ? current : null;
   const [read, setRead] = useState<{ view: TemplateView | null; error: string } | null>(null);
@@ -46,7 +46,7 @@ export function UpgradeInstanceDialog({ instance, appName, target, current, onCl
     };
   }, [api, given, instance.template_id, instance.revision]);
   const from = given ?? read?.view ?? null;
-  const changes = from ? defaultChanges(from.body.parameters, target.body.parameters, instance) : [];
+  const changes = from ? defaultChanges(from.detected.variables, target.detected.variables, instance) : [];
   const starts = instance.state === "created" && instance.status_code === AppStatusCode.Stopped;
   const [values, setValues] = useState<Record<string, string>>({});
   const [tried, setTried] = useState(false);
@@ -55,7 +55,7 @@ export function UpgradeInstanceDialog({ instance, appName, target, current, onCl
 
   const upgrade = async () => {
     setTried(true);
-    if (Object.keys(fieldErrors(needs, values)).length > 0) return;
+    if (Object.keys(variableErrors(needs, values)).length > 0) return;
     setBusy(true);
     setError("");
     try {
@@ -65,7 +65,7 @@ export function UpgradeInstanceDialog({ instance, appName, target, current, onCl
         server_id: instance.server_id,
         key: instance.key,
         revision: target.revision,
-        ...(Object.keys(plain).length ? { parameters: plain } : {}),
+        ...(Object.keys(plain).length ? { values: plain } : {}),
         ...(Object.keys(secrets).length ? { secrets } : {}),
       });
       toast.success(`Updated to version ${target.revision}`);
@@ -87,8 +87,9 @@ export function UpgradeInstanceDialog({ instance, appName, target, current, onCl
       title={`Update ${appName} to version ${target.revision}`}
       description={
         <>
-          The app is redeployed with {target.name} version {target.revision}; its backup settings stay. Values you set
-          stay; parameters left on their default take the new default.{starts ? " The app will be started." : ""}
+          The app is redeployed with {target.name} version {target.revision}; its backup settings and hostnames stay (a
+          backup the version adds starts off). Values you set stay; variables left to the compose file take its new
+          value.{starts ? " The app will be started." : ""}
         </>
       }
       footer={
@@ -114,14 +115,14 @@ export function UpgradeInstanceDialog({ instance, appName, target, current, onCl
             {changes.map((c) => (
               <li key={c.name}>
                 <code className="font-mono">{c.name}</code>: {c.from || "(empty)"} → {c.to || "(empty)"}{" "}
-                <span className="text-muted-foreground">(follows the template default)</span>
+                <span className="text-muted-foreground">(follows the compose file)</span>
               </li>
             ))}
           </ul>
         ) : null}
         {needs.length > 0 ? (
-          <ParameterFields
-            params={needs}
+          <VariableFields
+            vars={needs}
             values={values}
             onChange={(name, v) => setValues((prev) => ({ ...prev, [name]: v }))}
             showRequired={tried}
````

Create `web/src/components/variable-fields.tsx`:

````tsx
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { PasswordInput } from "@/components/ui/password-input";
import { FieldMessage } from "@/components/form-field";
import { declaredPatternHint } from "@/lib/declared-variables";
import { invalidAttrs } from "@/lib/field-errors";
import { requiredFirst, variableErrors } from "@/lib/template-values";
import { cn } from "@/lib/utils";
import type { DeclaredVariable } from "@/types/app-config";

// nameOf is a field's accessible name: its description and variable name,
// or the name alone.
const nameOf = (v: DeclaredVariable) => (v.description ? `${v.description} (${v.name})` : v.name);

const REQUIRED = "Enter a value: this variable is required.";

type Props = {
  vars: DeclaredVariable[] | null | undefined;
  values: Record<string, string>; // plain values and typed secrets by name
  onChange: (name: string, value: string) => void;
  // kept are the secrets the agent holds: blank keeps them.
  kept?: readonly string[];
  // showRequired reveals "required" (after a submit attempt); a $ in a
  // plain value shows as it is typed.
  showRequired?: boolean;
};

// VariableFields is a form over the variables a template's compose files
// declare under x-winterflow: required ones first, the compose file's own
// value as the placeholder (a field left empty keeps it), secrets masked
// (the caller encrypts them for the server before anything is sent). A
// field is labelled by the variable's description when it has one (its name
// small beside it), else by its name; a secret carries a Secret tag. A
// value that may not match the pattern gets a hint: the server decides.
export function VariableFields({ vars, values, onChange, kept = [], showRequired = false }: Props) {
  const errors = variableErrors(vars, values, kept);
  return (
    <div className="grid gap-4">
      {requiredFirst(vars).map((v) => {
        const id = `var-${v.name}`;
        const err = errors[v.name] ?? "";
        const shown = err !== "" && (err !== REQUIRED || showRequired) ? err : undefined;
        const value = values[v.name] ?? "";
        const held = v.encrypted && kept.includes(v.name);
        const hint = shown ? "" : declaredPatternHint(v, value, held);
        const attrs = invalidAttrs(id, shown, v.encrypted ? undefined : "font-mono");
        return (
          <div key={v.name} className="grid gap-1.5">
            <Label htmlFor={id} className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
              <span className={cn(!v.description && "font-mono")}>
                {v.description || v.name}
                {v.required ? (
                  <span aria-hidden className="text-destructive">
                    *
                  </span>
                ) : null}
              </span>
              {v.encrypted ? (
                <Badge variant="secondary" className="px-1.5 py-0 text-[11px] font-normal">
                  Secret
                </Badge>
              ) : null}
              {v.description ? (
                <code className="ml-auto font-mono text-xs font-normal text-muted-foreground">{v.name}</code>
              ) : null}
            </Label>
            {v.encrypted ? (
              <PasswordInput
                id={id}
                aria-label={nameOf(v)}
                value={value}
                autoComplete="new-password"
                placeholder={held ? "Stored, leave blank to keep" : "Encrypted in your browser"}
                {...attrs}
                onChange={(e) => onChange(v.name, e.target.value)}
              />
            ) : (
              <Input
                id={id}
                aria-label={nameOf(v)}
                value={value}
                placeholder={v.default ? `Default: ${v.default}` : undefined}
                {...attrs}
                onChange={(e) => onChange(v.name, e.target.value)}
              />
            )}
            <FieldMessage id={id} message={shown} />
            {hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
          </div>
        );
      })}
    </div>
  );
}
````

- [ ] **Step 4: Run them and see them pass**

```bash
CI=true pnpm --dir web exec vitest run src/components/variable-fields.test.tsx src/components/instance-panel.test.tsx src/components/template-instances.test.tsx src/pages/app-details.test.tsx src/pages/template-page.test.tsx
CI=true pnpm --dir web run test
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat(web): instance panel, update dialog and instance list use the declared variables" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 9: Web: the template editor is files plus metadata, with a Detected panel

The editor edits the name, the look (icon, colour, summary, description) and the files; a read-only Detected panel shows what the compose file declares, read by the API (`inspect-template`, debounced, only while the draft differs from the saved version; `get-template`'s `detected` otherwise). "Save as template" copies an app's files and notes what stays with the app. The templates page lists the org's own templates only.

**Files:**
- Create: `web/src/components/template-detected.test.tsx`
- Create: `web/src/components/template-detected.tsx`
- Modify: `web/src/components/template-editor.test.tsx`
- Modify: `web/src/components/template-editor.tsx`
- Modify: `web/src/components/template-revisions.test.tsx`
- Create: `web/src/hooks/use-template-inspection.test.tsx`
- Create: `web/src/hooks/use-template-inspection.ts`
- Modify: `web/src/lib/template-draft.test.ts`
- Modify: `web/src/lib/template-draft.ts`
- Modify: `web/src/pages/app-details.test.tsx`
- Modify: `web/src/pages/org-templates.test.tsx`
- Modify: `web/src/pages/org-templates.tsx`
- Modify: `web/src/pages/template-page.test.tsx`
- Modify: `web/src/pages/template-page.tsx`

**Interfaces:**
- Consumes: `TemplatesApi.inspectTemplate`, `orgTemplates`, `publishedPorts` (`lib/host-ports`), `stateFromDetail`.
- Produces:
  - `lib/template-draft.ts`: `TemplateDraft{name, summary, description, icon, color, files, contents}`, `DraftState`, `TEMPLATE_NAME`, `EXAMPLE_COMPOSE`, `emptyDraft()`, `draftFromBody`, `bodyFromDraft`, `draftProblem`, `slugName`, `templateFromApp(appId, detail)` (same signatures; `paramRow`, `ingressRow`, `composeContents`, `backupNamesIn`, `recipeBackupNames` are gone).
  - `hooks/use-template-inspection.ts`: `useTemplateInspection(api, body, enabled, delay = 500): { inspection, error, checking }`.
  - `components/template-detected.tsx`: `TemplateDetected({ detected, problem?, error?, checking? })`.
  - `components/template-editor.tsx`: `TemplateEditor({ draft, onChange, detected? })`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/template-detected.test.tsx`:

````tsx
import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { TemplateDetected } from "@/components/template-detected";
import type { Detected } from "@/lib/templates";

const DETECTED: Detected = {
  variables: [
    { name: "BASE_URL", required: true, encrypted: false, pattern: "^https?://", description: "Public URL", default: "http://localhost", services: ["app"] },
    { name: "SECRET", required: true, encrypted: true, default: null, services: ["app"] },
    { name: "EMPTY", required: false, encrypted: false, default: "", services: ["app"] },
  ],
  expose: [
    { id: "app:8080/http", target: "app:8080", container: "app", port: 8080, protocol: "http" },
    { id: "db:5432/tcp", target: "db:5432", container: "db", port: 5432, protocol: "tcp" },
  ],
  backups: [{ name: "db", type: "files" }],
  warnings: ["x-winterflow.variables.UNUSED: no service environment and no override uses it"],
};

describe("TemplateDetected", () => {
  it("lists the variables, exposed ports, backups and warnings the compose file declares", () => {
    render(<TemplateDetected detected={DETECTED} />);
    const vars = within(screen.getByRole("region", { name: "Variables" }));
    expect(vars.getByText("BASE_URL")).toBeInTheDocument();
    expect(vars.getByText("default http://localhost")).toBeInTheDocument();
    expect(vars.getByText("Public URL")).toBeInTheDocument();
    expect(vars.getByText("^https?://")).toBeInTheDocument();
    expect(vars.getAllByText("Required")).toHaveLength(2);
    expect(vars.getByText("Secret")).toBeInTheDocument();
    expect(vars.getByText('default ""')).toBeInTheDocument();
    const ports = within(screen.getByRole("region", { name: "Exposed ports" }));
    expect(ports.getByText("takes hostnames")).toBeInTheDocument();
    expect(ports.getByText("reachable on the LAN")).toBeInTheDocument();
    expect(within(screen.getByRole("region", { name: "Backups" })).getByText("files")).toBeInTheDocument();
    expect(screen.getByRole("list", { name: "Warnings" })).toHaveTextContent("UNUSED");
  });

  it("says when nothing is declared, and shows a problem, a read error and a check in progress", () => {
    const { rerender } = render(<TemplateDetected detected={{ variables: null, expose: null, backups: null, warnings: null }} />);
    expect(screen.getByText(/No variables, exposed ports or backups/)).toBeInTheDocument();
    rerender(<TemplateDetected detected={null} problem="files: a compose file is required" error="offline" checking />);
    const alerts = screen.getAllByRole("alert");
    expect(alerts[0]).toHaveTextContent("A save would be refused: files: a compose file is required");
    expect(alerts[1]).toHaveTextContent("Couldn't read the files: offline");
    expect(screen.getByText("Checking…")).toBeInTheDocument();
    expect(screen.queryByText(/No variables/)).not.toBeInTheDocument();
  });
});
````

Replace the whole of `web/src/components/template-editor.test.tsx` with:

````tsx
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
      detected={<p>the detected panel</p>}
    />
  );
}

function start() {
  const draft = draftFromBody("shop", {
    files: { "compose.yaml": "services: {}\n" },
    summary: "A shop",
    description: "",
    icon: "",
    color: "",
  });
  render(<Harness initial={draft} />);
  return userEvent.setup();
}

describe("TemplateEditor", () => {
  it("edits the name, the look and the files", async () => {
    const user = start();
    await user.clear(screen.getByLabelText("Template name"));
    await user.type(screen.getByLabelText("Template name"), "shop2");
    await user.type(screen.getByLabelText("Summary"), " for teams");
    await user.type(screen.getByLabelText("Description"), "Sells things.");
    await user.click(screen.getAllByRole("button", { name: /Add file/ })[0]);
    // A template holds no secret: files have no secret toggle.
    expect(screen.queryByRole("button", { name: /Secret/ })).not.toBeInTheDocument();
    expect(latest()).toMatchObject({ name: "shop2", summary: "A shop for teams", description: "Sells things." });
    expect(latest().files).toHaveLength(2);
    await user.clear(screen.getByLabelText("Content of compose.yaml"));
    await user.click(screen.getByLabelText("Content of compose.yaml"));
    await user.paste("services: { app: { image: x } }");
    expect(bodyFromDraft(latest()).files?.["compose.yaml"]).toBe("services: { app: { image: x } }");
  });

  it("has placeholders that say what goes there, and shows the Detected panel after the files", () => {
    start();
    expect(screen.getByLabelText("Template name")).toHaveAttribute("placeholder", "my-stack");
    expect(screen.getByLabelText("Summary")).toHaveAttribute("placeholder", "One line about the app");
    // British spelling, like the rest of the UI.
    expect(screen.getByText("Colour")).toBeInTheDocument();
    const files = screen.getByLabelText("Content of compose.yaml");
    const panel = screen.getByText("the detected panel");
    expect(files.compareDocumentPosition(panel) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});
````

Modify `web/src/components/template-revisions.test.tsx` (`git apply` this diff):

````diff
diff --git a/web/src/components/template-revisions.test.tsx b/web/src/components/template-revisions.test.tsx
--- a/web/src/components/template-revisions.test.tsx
+++ b/web/src/components/template-revisions.test.tsx
@@ -11,26 +11,28 @@ const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.
 
 const body = (image: string, extra: Record<string, string> = {}): TemplateBody => ({
   files: { "compose.yaml": `services:\n  app:\n    image: ${image}\n`, ...extra },
-  parameters: [{ name: "NAME", required: true }],
-  app_name: "${NAME}",
+  summary: "A shop",
+  description: "",
   icon: "",
   color: "",
-  ingress: null,
-  backups: null,
 });
 
 const BODIES: Record<string, TemplateBody> = {
   "1": body("app:1"),
   "2": body("app:1", { Caddyfile: ":80" }),
-  "3": { ...body("app:2", { Caddyfile: ":80" }), app_name: "shop-${NAME}" },
+  "3": { ...body("app:2", { Caddyfile: ":80" }), summary: "A shop for teams" },
 };
 
 const VIEW: TemplateView = {
   id: "t1",
+  source: "org",
+  slug: "",
   name: "shop",
   latest_revision: 3,
+  withdrawn_at: null,
   revision: 3,
   body: BODIES["3"],
+  detected: { variables: [], expose: [], backups: [], warnings: [] },
   note: "",
   created_at: "2026-10-12T10:00:00Z",
   created_by: "u1",
@@ -71,7 +73,7 @@ describe("TemplateRevisions", () => {
     expect(within(compose).getByText("- image: app:1", { exact: false })).toBeInTheDocument();
     expect(within(compose).getByText("+ image: app:2", { exact: false })).toBeInTheDocument();
     const settings = screen.getByRole("region", { name: "Changes to template settings" });
-    expect(settings).toHaveTextContent('+ "app_name": "shop-${NAME}",');
+    expect(settings).toHaveTextContent('+ "summary": "A shop for teams"');
 
     await user.click(screen.getByRole("button", { name: "Changes from version 1" }));
     const added = await screen.findByRole("region", { name: "Changes to Caddyfile" });
````

Create `web/src/hooks/use-template-inspection.test.tsx`:

````tsx
import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useTemplateInspection } from "@/hooks/use-template-inspection";
import type { Inspection, TemplateBody } from "@/lib/templates";

const body = (compose: string): TemplateBody => ({ files: { "compose.yaml": compose }, summary: "", description: "", icon: "", color: "" });
const answer = (problem: string): Inspection => ({ detected: { variables: [], expose: [], backups: [], warnings: [] }, problem });

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe("useTemplateInspection", () => {
  it("reads a body once it stopped changing, keeping the last answer while the next is read", async () => {
    const inspectTemplate = vi.fn(async (b: TemplateBody) => answer(b.files!["compose.yaml"]));
    const api = { inspectTemplate };
    const { result, rerender } = renderHook(({ b }) => useTemplateInspection(api, b, true, 500), { initialProps: { b: body("a") } });
    expect(result.current).toEqual({ inspection: null, error: "", checking: true });
    rerender({ b: body("ab") });
    await act(() => vi.advanceTimersByTimeAsync(500));
    // Only the body that stood still for the delay was read.
    expect(inspectTemplate).toHaveBeenCalledTimes(1);
    expect(result.current).toEqual({ inspection: answer("ab"), error: "", checking: false });
    rerender({ b: body("abc") });
    expect(result.current).toEqual({ inspection: answer("ab"), error: "", checking: true });
  });

  it("reports a failed read, keeping the last good answer", async () => {
    const inspectTemplate = vi.fn(async (b: TemplateBody) => {
      if (b.files!["compose.yaml"] === "bad") throw new Error("offline");
      return answer("");
    });
    const { result, rerender } = renderHook(({ b }) => useTemplateInspection({ inspectTemplate }, b, true, 10), {
      initialProps: { b: body("good") },
    });
    await act(() => vi.advanceTimersByTimeAsync(10));
    rerender({ b: body("bad") });
    await act(() => vi.advanceTimersByTimeAsync(10));
    expect(result.current).toEqual({ inspection: answer(""), error: "offline", checking: false });
  });

  it("asks nothing while disabled", async () => {
    const inspectTemplate = vi.fn(async () => answer(""));
    const { result } = renderHook(() => useTemplateInspection({ inspectTemplate }, body("a"), false, 10));
    await act(() => vi.advanceTimersByTimeAsync(50));
    expect(inspectTemplate).not.toHaveBeenCalled();
    expect(result.current).toEqual({ inspection: null, error: "", checking: false });
  });
});
````

Replace the whole of `web/src/lib/template-draft.test.ts` with:

````ts
import { describe, expect, it } from "vitest";

import type { AppDetailPayload } from "@/context/apps-context-base";
import type { EffectiveBackup } from "@/lib/backups";
import {
  bodyFromDraft,
  draftFromBody,
  draftProblem,
  emptyDraft,
  EXAMPLE_COMPOSE,
  slugName,
  templateFromApp,
  type TemplateDraft,
} from "@/lib/template-draft";
import type { TemplateBody } from "@/lib/templates";

const COMPOSE = `services:
  app:
    image: ghcr.io/example/notes:1
    environment:
      BASE_URL: "http://localhost:8080"
x-winterflow:
  expose:
    - { container: app, port: 8080 }
  variables:
    BASE_URL: { required: true }
`;

const BODY: TemplateBody = {
  files: { "compose.yaml": COMPOSE, Caddyfile: ":80" },
  summary: "Notes for teams",
  description: "A longer text.",
  icon: "note",
  color: "#2563eb",
};

function valid(): TemplateDraft {
  return draftFromBody("notes", BODY);
}

describe("draftFromBody / bodyFromDraft", () => {
  it("round-trips a body", () => {
    expect(bodyFromDraft(valid())).toEqual(BODY);
  });

  it("trims the summary and description, and reads null files from the API", () => {
    const d = { ...valid(), summary: "  Notes  ", description: " text \n" };
    expect(bodyFromDraft(d)).toMatchObject({ summary: "Notes", description: "text" });
    expect(draftFromBody("x", { ...BODY, files: null }).files).toEqual([]);
  });
});

describe("draftProblem", () => {
  const cases: [string, (d: TemplateDraft) => TemplateDraft, string][] = [
    ["a bad name", (d) => ({ ...d, name: "Notes" }), "Name:"],
    ["a two-line summary", (d) => ({ ...d, summary: "a\nb" }), "Summary:"],
    ["a long summary", (d) => ({ ...d, summary: "s".repeat(201) }), "Summary:"],
    ["a long description", (d) => ({ ...d, description: "d".repeat(4001) }), "Description:"],
    ["a file without a name", (d) => ({ ...d, files: [...d.files, { id: "x", filename: " ", is_encrypted: false }] }), "Every file needs a filename."],
    ["two files of one name", (d) => ({ ...d, files: [...d.files, { ...d.files[0], id: "y" }] }), "Two files have the same name."],
    ["no compose file", (d) => ({ ...d, files: d.files.filter((f) => f.filename !== "compose.yaml") }), "A compose file is required"],
  ];
  for (const [name, edit, want] of cases) {
    it(`refuses ${name}`, () => {
      expect(draftProblem(edit(valid()))).toContain(want);
    });
  }

  it("accepts a valid draft", () => {
    expect(draftProblem(valid())).toBeNull();
  });
});

describe("a new template", () => {
  it("starts from the example compose file, its port exposed and a variable declared", () => {
    const body = bodyFromDraft({ ...emptyDraft(), name: "site" });
    expect(body.files).toEqual({ "compose.yml": EXAMPLE_COMPOSE });
    expect(EXAMPLE_COMPOSE).toContain("x-winterflow:");
    expect(draftProblem({ ...emptyDraft(), name: "site" })).toBeNull();
  });
});

describe("templateFromApp", () => {
  const b64 = (s: string) => btoa(s);
  const compose = `services:
  web:
    image: nginx
    ports:
      - "8090:80"
`;
  const config = {
    name: "Shop Front",
    icon: "cart",
    color: "#111111",
    files: [{ filename: "compose.yml" }, { filename: "key.pem", is_encrypted: true }, { filename: "notes.txt" }],
    variables: [{ name: "NAME" }],
    ingress: {
      domains: [{ domain: "shop.example.com", upstream_port: 8090 }],
      redirects: [],
    },
  };
  const backup = (b: Partial<EffectiveBackup>): EffectiveBackup =>
    ({ name: "db", type: "sqlite", enabled: true, storage: "tenants", in_recipe: true, from_recipe: { every: true }, ...b }) as EffectiveBackup;
  const detail: AppDetailPayload = {
    app: {
      app_id: "a1",
      config: b64(JSON.stringify(config)),
      files: [
        { name: "compose.yml", content: b64(compose) },
        { name: "key.pem", content: "<encrypted>", encrypted: true },
        { name: "notes.txt", content: b64("8090:80 stays") },
      ],
      variables: [{ name: "NAME", content: b64("shop") }],
    },
    backups: [backup({}), backup({ name: "extra", in_recipe: false })],
    revision: 1,
    available_revisions: null,
  };

  it("copies the files and the look, and notes what stays with the app", () => {
    const { draft, notes } = templateFromApp("a1", detail);
    expect(draft.name).toBe("shop-front");
    expect(bodyFromDraft(draft)).toEqual({
      files: { "compose.yml": compose, "notes.txt": "8090:80 stays" },
      summary: "",
      description: "",
      icon: "cart",
      color: "#111111",
    });
    expect(notes).toEqual([
      "key.pem is a secret file; a template holds no secrets, so it was left out.",
      "The app's variable values stay with the app: declare what each app fills in under x-winterflow.variables.",
      "The compose file publishes host port 8090 itself; two apps from the template would both claim it. List the container port under x-winterflow.expose instead, and each app gets a host port of its own.",
      "Hostnames and redirects stay with the app: each app made from the template picks its own.",
      "The backup extra was added in the app; add it to the compose file's x-winterflow.backups to make it part of the template.",
    ]);
  });

  it("notes nothing for an app that is already a good template", () => {
    const plain = { name: "Notes", files: [{ filename: "compose.yml" }], variables: [] };
    const { draft, notes } = templateFromApp("a1", {
      ...detail,
      app: { ...detail.app, config: b64(JSON.stringify(plain)), files: [{ name: "compose.yml", content: b64(COMPOSE) }], variables: [] },
      backups: [],
    });
    expect(notes).toEqual([]);
    expect(draft.color).toBe("#64748b");
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
    expect(slugName("a".repeat(62) + "-bbb--")).toBe("a".repeat(62));
  });
});
````

Modify `web/src/pages/app-details.test.tsx` (`git apply` this diff):

````diff
diff --git a/web/src/pages/app-details.test.tsx b/web/src/pages/app-details.test.tsx
--- a/web/src/pages/app-details.test.tsx
+++ b/web/src/pages/app-details.test.tsx
@@ -113,7 +113,7 @@ function NewTemplate() {
   const state = useLocation().state as DraftState;
   return (
     <p>
-      new template {state.draft.name} with {state.draft.parameters.length} parameters from {state.appName}
+      new template {state.draft.name} with {state.draft.files.length} files from {state.appName}: {state.notes.join(" ")}
     </p>
   );
 }
@@ -1136,7 +1136,11 @@ describe("AppDetailsPage: Save as template", () => {
     }));
     renderPage("/app/a1?tab=settings", {}, { ctx: { getApp } });
     await user.click(await screen.findByRole("button", { name: "Save as template" }));
-    expect(await screen.findByText("new template shop with 1 parameters from Shop")).toBeInTheDocument();
+    expect(
+      await screen.findByText(
+        "new template shop with 1 files from Shop: The app's variable values stay with the app: declare what each app fills in under x-winterflow.variables.",
+      ),
+    ).toBeInTheDocument();
   });
 
   it("reports an app it can't read", async () => {
````

Modify `web/src/pages/org-templates.test.tsx` (`git apply` this diff):

````diff
diff --git a/web/src/pages/org-templates.test.tsx b/web/src/pages/org-templates.test.tsx
--- a/web/src/pages/org-templates.test.tsx
+++ b/web/src/pages/org-templates.test.tsx
@@ -33,13 +33,13 @@ describe("OrgTemplatesPage", () => {
     fakeAPI({
       "/api/v1/template/get-templates": {
         templates: [
-          { id: "t1", name: "notes", latest_revision: 2, instances: 0, behind: 0, updated_at: "2026-10-12T10:00:00Z" },
-          { id: "t2", name: "plain", latest_revision: 1, instances: 0, behind: 0, updated_at: "2026-10-12T10:00:00Z" },
+          { id: "t1", source: "org", name: "notes", latest_revision: 2, instances: 0, behind: 0, updated_at: "2026-10-12T10:00:00Z" },
+          { id: "t2", source: "org", name: "plain", latest_revision: 1, instances: 0, behind: 0, updated_at: "2026-10-12T10:00:00Z" },
         ],
       },
       "/api/v1/template/get-template": (_: unknown, q: URLSearchParams) =>
         q.get("id") === "t1"
-          ? { id: "t1", name: "notes", revision: 2, latest_revision: 2, body: { icon: "book", color: "#0d9488" } }
+          ? { id: "t1", source: "org", name: "notes", revision: 2, latest_revision: 2, body: { icon: "book", color: "#0d9488" } }
           : new Error("gone"),
     });
     renderPage();
@@ -57,7 +57,7 @@ describe("OrgTemplatesPage", () => {
     const user = userEvent.setup();
     fakeAPI({
       "/api/v1/template/get-templates": {
-        templates: [{ id: "t1", name: "twillingate", latest_revision: 5, instances: 12, behind: 3, updated_at: "2026-10-12T10:00:00Z" }],
+        templates: [{ id: "t1", source: "org", name: "twillingate", latest_revision: 5, instances: 12, behind: 3, updated_at: "2026-10-12T10:00:00Z" }],
       },
     });
     renderPage();
@@ -71,6 +71,20 @@ describe("OrgTemplatesPage", () => {
     expect(await screen.findByText("template page")).toBeInTheDocument();
   });
 
+  it("lists the org's own templates only (the catalog's come with the gallery)", async () => {
+    fakeAPI({
+      "/api/v1/template/get-templates": {
+        templates: [
+          { id: "t1", source: "org", name: "mine", latest_revision: 1, instances: 0, behind: 0, updated_at: "2026-10-12T10:00:00Z" },
+          { id: "c1", source: "catalog", slug: "actual", name: "Actual Budget", latest_revision: 1, instances: 0, behind: 0, updated_at: "2026-10-12T10:00:00Z" },
+        ],
+      },
+    });
+    renderPage();
+    expect(await screen.findByRole("link", { name: /mine/ })).toBeInTheDocument();
+    expect(screen.queryByText("Actual Budget")).not.toBeInTheDocument();
+  });
+
   it("offers New template as a link, and says what to do when there is none", async () => {
     const user = userEvent.setup();
     fakeAPI({ "/api/v1/template/get-templates": { templates: null } });
````

Modify `web/src/pages/template-page.test.tsx` (`git apply` this diff):

````diff
diff --git a/web/src/pages/template-page.test.tsx b/web/src/pages/template-page.test.tsx
--- a/web/src/pages/template-page.test.tsx
+++ b/web/src/pages/template-page.test.tsx
@@ -6,7 +6,7 @@ import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
 import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
 import { NotificationsProvider } from "@/context/notifications-context";
 import { draftFromBody } from "@/lib/template-draft";
-import { ApiError, type TemplateBody, type TemplateView } from "@/lib/templates";
+import { ApiError, type Detected, type TemplateBody, type TemplateView } from "@/lib/templates";
 import TemplatePage from "@/pages/template-page";
 import { fakeAPI } from "@/test/fake-api";
 import { rollout } from "@/test/rollouts";
@@ -31,31 +31,27 @@ vi.mock("@/components/code-editor", () => ({
 
 const COMPOSE = `services:
   app:
-    image: ghcr.io/dmtrkzntsv/twillingate:\${VERSION}
-    ports: ["127.0.0.1:\${PORT}:8080"]
+    image: ghcr.io/dmtrkzntsv/twillingate:1
+    environment:
+      NAME: "twillingate"
 x-winterflow:
-  backups:
-    - { name: db, type: sqlite, volume: data, path: twillingate.db }
+  expose:
+    - { container: app, port: 8080 }
+  variables:
+    NAME: { required: true, pattern: "^[a-z]{3,20}$" }
 `;
 
 const BODY: TemplateBody = {
   files: { "compose.yaml": COMPOSE },
-  parameters: [
-    { name: "NAME", required: true, pattern: "^[a-z]{3,20}$" },
-    { name: "PORT", required: true },
-    { name: "VERSION", required: true },
-  ],
-  app_name: "${NAME}",
+  summary: "Analytics for one tenant",
+  description: "",
   icon: "chart",
   color: "#2563eb",
-  ingress: [{ host: "${NAME}.twillingate.app", port: "${PORT}" }],
-  backups: { db: { enabled: true, storage: "tenants" } },
 };
 
-// DETECTED is what the compose file declares (the update dialog reads it).
-const DETECTED = {
-  variables: [{ name: "NAME", required: true, encrypted: false, default: null, services: ["app"] }],
-  expose: [],
+const DETECTED: Detected = {
+  variables: [{ name: "NAME", required: true, encrypted: false, pattern: "^[a-z]{3,20}$", default: "twillingate", services: ["app"] }],
+  expose: [{ id: "app:8080/http", target: "app:8080", container: "app", port: 8080, protocol: "http" }],
   backups: [],
   warnings: [],
 };
@@ -78,6 +74,9 @@ function view(revision: number): TemplateView {
   };
 }
 
+// inspected is inspect-template's answer for any body.
+const inspected = { detected: DETECTED, problem: "" };
+
 type Listener = Parameters<NotificationsContextValue["subscribe"]>[0];
 const listeners = new Set<Listener>();
 const notifications = {
@@ -120,40 +119,44 @@ describe("TemplatePage", () => {
   it("adds a template as version 1, then opens it", async () => {
     const user = userEvent.setup();
     const api = fakeAPI({
-      "/api/v1/template/save-template": { id: "t9", name: "shop", revision: 1, warnings: ["SMTP_PASS is not a parameter"] },
+      "/api/v1/template/save-template": { id: "t9", name: "shop", revision: 1, warnings: ["SMTP_PASS is not a declared variable"] },
       "/api/v1/template/get-template": { ...view(1), id: "t9", name: "shop" },
+      "/api/v1/template/inspect-template": inspected,
       "/api/v1/instance/get-instances": { instances: [] },
     });
     renderPage("/templates/new");
+    // A new template is read as it is typed: the Detected panel fills in.
+    expect(await screen.findByText("takes hostnames")).toBeInTheDocument();
     await user.type(screen.getByLabelText("Template name"), "shop");
-    await user.type(screen.getByLabelText("App name"), "Shop");
+    await user.type(screen.getByLabelText("Summary"), "Shop");
     await user.click(screen.getByRole("button", { name: "Save template" }));
     await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Saved version 1"));
-    expect(toast.warning).toHaveBeenCalledWith("Check these references", { description: "SMTP_PASS is not a parameter" });
+    expect(toast.warning).toHaveBeenCalledWith("Check these warnings", { description: "SMTP_PASS is not a declared variable" });
     const [sent] = api.posted("/api/v1/template/save-template") as { name: string; base_revision: number; id?: string }[];
-    expect(sent).toMatchObject({ name: "shop", base_revision: 0 });
+    expect(sent).toMatchObject({ name: "shop", base_revision: 0, body: { summary: "Shop" } });
     expect(sent.id).toBeUndefined();
     expect(await screen.findByRole("heading", { name: "shop" })).toBeInTheDocument();
   });
 
   it("refuses to save what the API would refuse, naming it", async () => {
     const user = userEvent.setup();
-    const api = fakeAPI({});
+    const api = fakeAPI({ "/api/v1/template/inspect-template": inspected });
     renderPage("/templates/new");
     expect(pageTitle).toHaveBeenLastCalledWith("New template");
     // A new template saves as version 1: there is nothing to note yet.
     expect(screen.queryByLabelText("Version note")).not.toBeInTheDocument();
     await user.type(screen.getByLabelText("Template name"), "Shop");
-    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
+    expect(screen.queryByText(/^Name: lowercase/)).not.toBeInTheDocument();
     await user.click(screen.getByRole("button", { name: "Save template" }));
-    expect(screen.getByRole("alert")).toHaveTextContent("Name: lowercase");
+    expect(screen.getByText(/^Name: lowercase/)).toBeInTheDocument();
     expect(toast.error).not.toHaveBeenCalled();
-    expect(api.calls).toEqual([]);
+    expect(api.posted("/api/v1/template/save-template")).toEqual([]);
     // The message follows the draft: the next problem, then none.
     await user.clear(screen.getByLabelText("Template name"));
     await user.type(screen.getByLabelText("Template name"), "shop");
-    expect(screen.getByRole("alert")).toHaveTextContent("App name is required.");
-    await user.type(screen.getByLabelText("App name"), "Shop");
+    await user.type(screen.getByLabelText("Summary"), "a{Enter}b");
+    expect(screen.queryByText(/^Name: lowercase/)).not.toBeInTheDocument();
+    await user.clear(screen.getByLabelText("Summary"));
     expect(screen.queryByRole("alert")).not.toBeInTheDocument();
   });
 
@@ -161,16 +164,18 @@ describe("TemplatePage", () => {
     const user = userEvent.setup();
     const api = fakeAPI({
       "/api/v1/template/get-template": view(3),
+      "/api/v1/template/inspect-template": inspected,
       "/api/v1/instance/get-instances": { instances: [] },
       "/api/v1/template/save-template": { id: "t1", name: "twillingate", revision: 4, warnings: null },
     });
     renderPage("/templates/t1");
-    // Clean, the save bar waits for an edit.
-    await screen.findByLabelText("App name");
+    // Clean, the save bar waits for an edit; the Detected panel is the
+    // saved version's, read with it (no inspect call).
+    await screen.findByLabelText("Summary");
+    expect(screen.getByText("takes hostnames")).toBeInTheDocument();
     expect(screen.queryByRole("button", { name: "Save as version 4" })).not.toBeInTheDocument();
     expect(screen.queryByLabelText("Version note")).not.toBeInTheDocument();
-    await user.click(screen.getAllByRole("button", { name: /Add parameter/ })[0]);
-    await user.type(screen.getAllByLabelText("Parameter name").at(-1)!, "MEMORY_MAX");
+    await user.type(screen.getByLabelText("Summary"), ", with memory cap");
     await user.type(screen.getByLabelText("Version note"), "memory cap");
     await user.click(screen.getByRole("button", { name: "Save as version 4" }));
     await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Saved version 4"));
@@ -181,8 +186,7 @@ describe("TemplatePage", () => {
       body: TemplateBody;
     }[];
     expect(sent).toMatchObject({ id: "t1", base_revision: 3, note: "memory cap" });
-    expect(sent.body.parameters?.map((p) => p.name)).toEqual(["NAME", "PORT", "VERSION", "MEMORY_MAX"]);
-    expect(sent.body.backups).toEqual({ db: { enabled: true, storage: "tenants" } });
+    expect(sent.body).toEqual({ ...BODY, summary: "Analytics for one tenant, with memory cap" });
     // The save re-reads the template only: its instances didn't change.
     await waitFor(() => expect(api.calls.filter((c) => c.path === "/api/v1/template/get-template")).toHaveLength(2));
     expect(api.calls.filter((c) => c.path === "/api/v1/instance/get-instances")).toHaveLength(1);
@@ -213,16 +217,35 @@ describe("TemplatePage", () => {
     }
   });
 
+  it("reads an edited draft back from the API: the Detected panel says what a save would refuse", async () => {
+    const user = userEvent.setup();
+    const api = fakeAPI({
+      "/api/v1/template/get-template": view(3),
+      "/api/v1/template/inspect-template": {
+        detected: { ...DETECTED, expose: [] },
+        problem: "invalid x-winterflow: x-winterflow.bogus: unknown key",
+      },
+      "/api/v1/instance/get-instances": { instances: [] },
+    });
+    renderPage("/templates/t1");
+    await user.type(await screen.findByLabelText("Summary"), "!");
+    expect(await screen.findByText(/A save would be refused: invalid x-winterflow: x-winterflow.bogus/)).toBeInTheDocument();
+    expect(screen.queryByText("takes hostnames")).not.toBeInTheDocument();
+    const [sent] = api.posted("/api/v1/template/inspect-template") as { body: TemplateBody }[];
+    expect(sent.body.summary).toBe("Analytics for one tenant!");
+  });
+
   it("is clean again when an edit is taken back", async () => {
     const user = userEvent.setup();
     fakeAPI({
       "/api/v1/template/get-template": view(3),
+      "/api/v1/template/inspect-template": inspected,
       "/api/v1/instance/get-instances": { instances: [] },
     });
     renderPage("/templates/t1");
-    await user.type(await screen.findByLabelText("App name"), "x");
+    await user.type(await screen.findByLabelText("Summary"), "x");
     expect(screen.getByRole("button", { name: "Save as version 4" })).toBeEnabled();
-    await user.type(screen.getByLabelText("App name"), "{Backspace}");
+    await user.type(screen.getByLabelText("Summary"), "{Backspace}");
     expect(screen.queryByRole("button", { name: "Save as version 4" })).not.toBeInTheDocument();
   });
 
@@ -231,6 +254,7 @@ describe("TemplatePage", () => {
     let latest = 3;
     fakeAPI({
       "/api/v1/template/get-template": () => view(latest),
+      "/api/v1/template/inspect-template": inspected,
       "/api/v1/instance/get-instances": { instances: [] },
       "/api/v1/template/save-template": () => {
         latest = 4;
@@ -238,12 +262,12 @@ describe("TemplatePage", () => {
       },
     });
     renderPage("/templates/t1");
-    await user.type(await screen.findByLabelText("App name"), "-x");
+    await user.type(await screen.findByLabelText("Summary"), "-x");
     await user.click(screen.getByRole("button", { name: "Save as version 4" }));
-    expect(await screen.findByRole("alert")).toHaveTextContent("Someone saved version 4 meanwhile; reload");
+    expect(await screen.findByText(/Someone saved version 4 meanwhile; reload/)).toBeInTheDocument();
     await user.click(screen.getByRole("button", { name: "Reload" }));
     expect(await screen.findByText(/^Version 4 · /)).toBeInTheDocument();
-    expect(screen.getByLabelText("App name")).toHaveValue("${NAME}");
+    expect(screen.getByLabelText("Summary")).toHaveValue("Analytics for one tenant");
     expect(toast.error).not.toHaveBeenCalled();
   });
 
@@ -251,11 +275,12 @@ describe("TemplatePage", () => {
     const user = userEvent.setup();
     fakeAPI({
       "/api/v1/template/get-template": view(3),
+      "/api/v1/template/inspect-template": inspected,
       "/api/v1/instance/get-instances": { instances: [] },
       "/api/v1/template/save-template": new Error("invalid template: files: x-winterflow.backups[0]: unknown type"),
     });
     renderPage("/templates/t1");
-    await user.type(await screen.findByLabelText("App name"), "-x");
+    await user.type(await screen.findByLabelText("Summary"), "-x");
     await user.click(screen.getByRole("button", { name: "Save as version 4" }));
     await waitFor(() =>
       expect(toast.error).toHaveBeenCalledWith("Couldn't save the template", {
@@ -281,6 +306,7 @@ describe("TemplatePage", () => {
     const user = userEvent.setup();
     const api = fakeAPI({
       "/api/v1/template/get-template": view(3),
+      "/api/v1/template/inspect-template": inspected,
       "/api/v1/instance/get-instances": { instances: [] },
     });
     render(
@@ -293,12 +319,12 @@ describe("TemplatePage", () => {
       </MemoryRouter>,
     );
     act(() => streams[0].onopen?.());
-    await user.type(await screen.findByLabelText("App name"), "-x");
+    await user.type(await screen.findByLabelText("Summary"), "-x");
     for (const step of [() => streams[0].onerror?.(), () => streams[0].onopen?.()]) {
       act(step);
       await flushPending();
     }
-    expect(screen.getByLabelText("App name")).toHaveValue("${NAME}-x");
+    expect(screen.getByLabelText("Summary")).toHaveValue("Analytics for one tenant-x");
     expect(screen.getByRole("button", { name: "Save as version 4" })).toBeEnabled();
     expect(api.calls.filter((c) => c.path === "/api/v1/template/get-template")).toHaveLength(1);
   });
@@ -382,7 +408,7 @@ describe("TemplatePage", () => {
   });
 
   it("names a new template and says what saving does", () => {
-    fakeAPI({});
+    fakeAPI({ "/api/v1/template/inspect-template": inspected });
     renderPage("/templates/new");
     expect(screen.getByRole("heading", { level: 1, name: "New template" })).toBeInTheDocument();
     expect(screen.getByText("Saving creates version 1.")).toBeInTheDocument();
@@ -410,6 +436,7 @@ describe("TemplatePage", () => {
   });
 
   it("opens prefilled from Save as template, with its notes", async () => {
+    fakeAPI({ "/api/v1/template/inspect-template": inspected });
     const draft = draftFromBody("shop-front", BODY);
     renderPage("/templates/new", {
       draft,
@@ -419,7 +446,7 @@ describe("TemplatePage", () => {
     expect(screen.getByLabelText("Template name")).toHaveValue("shop-front");
     const banner = screen.getByText((_, el) => el?.tagName === "P" && !!el.textContent?.startsWith("Built from Shop front."));
     expect(banner).toHaveTextContent(
-      "Built from Shop front. Replace what else differs per app, such as hostnames, with ${PARAM} parameters before saving.",
+      "Built from Shop front. Before saving, declare under x-winterflow what differs per app: the variables each app fills in and the ports it exposes.",
     );
     const note = screen.getByText("Redirects aren't part of templates; they were left out.");
     expect(banner.compareDocumentPosition(note) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
````

- [ ] **Step 2: Run them and see them fail**

Run: `CI=true pnpm --dir web exec vitest run src/lib/template-draft.test.ts src/components/template-editor.test.tsx src/components/template-detected.test.tsx src/hooks/use-template-inspection.test.tsx src/components/template-revisions.test.tsx src/pages/template-page.test.tsx src/pages/org-templates.test.tsx src/pages/app-details.test.tsx`
Expected: FAIL — the new modules do not exist, the draft still has parameters and the page has no Summary field or Detected panel.

- [ ] **Step 3: Implement**

Create `web/src/components/template-detected.tsx`:

````tsx
import { Lock } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { EditorSection } from "@/components/editor-section";
import { InlineError } from "@/components/inline-error";
import { EmptyNote } from "@/components/page-states";
import type { Detected } from "@/lib/templates";

type Props = {
  detected: Detected | null;
  // problem is the first thing a save would refuse ("" when none).
  problem?: string;
  // error says the files could not be read just now.
  error?: string;
  checking?: boolean;
};

// TemplateDetected is the template editor's read-only Detected panel: what
// the compose files declare under x-winterflow, as the API reads it — the
// variables each app fills in, the ports it exposes (an http one takes
// hostnames), the backups it can turn on — and what a save would refuse or
// point out. It changes only through the files.
export function TemplateDetected({ detected, problem = "", error = "", checking = false }: Props) {
  const variables = detected?.variables ?? [];
  const expose = detected?.expose ?? [];
  const backups = detected?.backups ?? [];
  const warnings = detected?.warnings ?? [];
  const empty = variables.length === 0 && expose.length === 0 && backups.length === 0;
  return (
    <EditorSection
      title="Detected"
      description={
        <>
          Read from the compose file's <code>x-winterflow</code> block. Edit the files to change it.
          {checking ? <span className="block pt-1 text-xs">Checking…</span> : null}
        </>
      }
    >
      {problem ? <InlineError icon>A save would be refused: {problem}</InlineError> : null}
      {error ? <InlineError icon>Couldn't read the files: {error}</InlineError> : null}
      {detected === null ? null : empty ? (
        <EmptyNote>No variables, exposed ports or backups: each app takes no values and publishes nothing.</EmptyNote>
      ) : (
        <div className="grid gap-4 text-sm">
          {variables.length > 0 ? (
            <section aria-label="Variables" className="space-y-1.5">
              <h3 className="text-xs font-medium text-muted-foreground">Variables each app fills in</h3>
              <ul className="divide-y rounded-md border">
                {variables.map((v) => (
                  <li key={v.name} className="space-y-1 px-3 py-2">
                    <div className="flex flex-wrap items-center gap-2">
                      <code className="font-mono">{v.name}</code>
                      {v.required ? <Badge variant="secondary">Required</Badge> : null}
                      {v.encrypted ? (
                        <Badge variant="secondary">
                          <Lock className="size-3" aria-hidden /> Secret
                        </Badge>
                      ) : null}
                      <span className="ml-auto text-xs text-muted-foreground">
                        {v.encrypted ? "no default" : v.default !== null ? `default ${v.default || '""'}` : "no default"}
                      </span>
                    </div>
                    {v.description ? <p className="text-xs text-muted-foreground">{v.description}</p> : null}
                    {v.pattern ? (
                      <p className="text-xs text-muted-foreground">
                        Must match <code className="font-mono">{v.pattern}</code>
                      </p>
                    ) : null}
                  </li>
                ))}
              </ul>
            </section>
          ) : null}
          {expose.length > 0 ? (
            <section aria-label="Exposed ports" className="space-y-1.5">
              <h3 className="text-xs font-medium text-muted-foreground">Exposed ports (each app gets a host port of its own)</h3>
              <ul className="divide-y rounded-md border">
                {expose.map((e) => (
                  <li key={e.id} className="flex flex-wrap items-center gap-2 px-3 py-2">
                    <code className="font-mono">{e.target}</code>
                    <Badge variant="secondary">{e.protocol}</Badge>
                    <span className="ml-auto text-xs text-muted-foreground">
                      {e.protocol === "http" ? "takes hostnames" : "reachable on the LAN"}
                    </span>
                  </li>
                ))}
              </ul>
            </section>
          ) : null}
          {backups.length > 0 ? (
            <section aria-label="Backups" className="space-y-1.5">
              <h3 className="text-xs font-medium text-muted-foreground">Backups each app can turn on</h3>
              <ul className="divide-y rounded-md border">
                {backups.map((b) => (
                  <li key={b.name} className="flex items-center gap-2 px-3 py-2">
                    <code className="font-mono">{b.name}</code>
                    <span className="text-xs text-muted-foreground">{b.type}</span>
                  </li>
                ))}
              </ul>
            </section>
          ) : null}
        </div>
      )}
      {warnings.length > 0 ? (
        <ul aria-label="Warnings" className="list-disc space-y-1 pl-4 text-xs text-amber-800 dark:text-amber-300">
          {warnings.map((w) => (
            <li key={w}>{w}</li>
          ))}
        </ul>
      ) : null}
    </EditorSection>
  );
}
````

Replace the whole of `web/src/components/template-editor.tsx` with:

````tsx
import type { ReactNode } from "react";

import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { EditorSection } from "@/components/editor-section";
import { FilesSection } from "@/components/files-section";
import { IconPicker } from "@/components/icon-picker";
import type { TemplateDraft } from "@/lib/template-draft";

type Props = {
  draft: TemplateDraft;
  onChange: (next: TemplateDraft) => void;
  // detected is the Detected panel (TemplateDetected), shown after the
  // files it is read from.
  detected?: ReactNode;
};

// TemplateEditor edits a template revision: its name, how it reads in a
// list (icon, colour, summary, description) and the app's files (the app
// editor's file editing, without secrets). What each app fills in, which
// ports it exposes and which backups it can turn on are declared in the
// compose file's x-winterflow block; detected shows them, read-only.
export function TemplateEditor({ draft, onChange, detected }: Props) {
  const set = (patch: Partial<TemplateDraft>) => onChange({ ...draft, ...patch });
  return (
    <div>
      <EditorSection title="Template" description="Lowercase letters, digits and hyphens; unique in the organization.">
        <div className="grid gap-2">
          <Label htmlFor="template-name">Template name</Label>
          <Input
            id="template-name"
            value={draft.name}
            placeholder="my-stack"
            className="font-mono"
            onChange={(e) => set({ name: e.target.value })}
          />
        </div>
      </EditorSection>

      <EditorSection title="Look" description="How the template reads in lists; each app made from it starts with its icon and colour.">
        <div className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2">
          <Label>Icon</Label>
          <Label htmlFor="template-summary">Summary</Label>
          <Label htmlFor="template-color">Colour</Label>
          <IconPicker value={draft.icon} color={draft.color} name={draft.name} onChange={(icon) => set({ icon })} />
          <Input
            id="template-summary"
            value={draft.summary}
            placeholder="One line about the app"
            onChange={(e) => set({ summary: e.target.value })}
          />
          <Input
            id="template-color"
            type="color"
            className="h-9 w-14 cursor-pointer p-1"
            value={draft.color || "#64748b"}
            onChange={(e) => set({ color: e.target.value })}
          />
        </div>
        <div className="grid gap-2">
          <Label htmlFor="template-description">Description</Label>
          <Textarea
            id="template-description"
            value={draft.description}
            placeholder="What the app is for and what to know before adding it (optional)"
            rows={3}
            onChange={(e) => set({ description: e.target.value })}
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
            Copied unchanged into each app. Declare under <code>x-winterflow</code> what each app fills in (
            <code>variables</code>), the ports it exposes (<code>expose</code>) and its <code>backups</code>; any other{" "}
            <code>{"${VAR}"}</code> must come from a shared variable on the server.
          </>
        }
      />

      {detected}
    </div>
  );
}
````

Create `web/src/hooks/use-template-inspection.ts`:

````ts
import { useEffect, useState } from "react";

import type { Inspection, TemplateBody, TemplatesApi } from "@/lib/templates";

export type TemplateInspection = {
  // inspection is the latest answer (for an earlier body while checking).
  inspection: Inspection | null;
  // error is why the latest body could not be read ("" when it was).
  error: string;
  // checking: the body changed since the latest answer.
  checking: boolean;
};

type Answer = { key: string; inspection: Inspection | null; error: string };

// useTemplateInspection reads what a template body declares
// (inspect-template) delay ms after it last changed, while enabled: the
// editor's Detected panel follows the files without a YAML parser in the
// browser. An answer is kept with the body it is for, so a late one for an
// older body never reads as current; the panel keeps the last good
// inspection while the next is read.
export function useTemplateInspection(
  api: Pick<TemplatesApi, "inspectTemplate">,
  body: TemplateBody,
  enabled: boolean,
  delay = 500,
): TemplateInspection {
  const key = enabled ? JSON.stringify(body) : "";
  const [answer, setAnswer] = useState<Answer | null>(null);

  useEffect(() => {
    if (!key) return;
    let live = true;
    const timer = setTimeout(() => {
      api.inspectTemplate(JSON.parse(key) as TemplateBody).then(
        (inspection) => live && setAnswer({ key, inspection, error: "" }),
        (e: unknown) =>
          live &&
          setAnswer((prev) => ({
            key,
            inspection: prev?.inspection ?? null,
            error: e instanceof Error ? e.message : "The compose file could not be read",
          })),
      );
    }, delay);
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [api, key, delay]);

  const fresh = answer?.key === key;
  return { inspection: answer?.inspection ?? null, error: fresh ? answer.error : "", checking: key !== "" && !fresh };
}
````

Replace the whole of `web/src/lib/template-draft.ts` with:

````ts
// The template editor's working state (TemplateDraft) and its conversions:
// from a stored revision, to the body save-template takes, and from an
// existing app ("Save as template"). A template is its files plus how it
// reads in a list: what an app fills in, which ports it exposes and which
// backups it can turn on are declared in the compose file's x-winterflow
// block, read by the API (inspect-template), never here. The checks give
// instant feedback; the API checks everything again.
import type { AppDetailPayload } from "@/context/apps-context-base";
import { stateFromDetail } from "@/lib/app-editor-io";
import { ENCRYPTED } from "@/lib/ecies";
import { publishedPorts } from "@/lib/host-ports";
import type { TemplateBody } from "@/lib/templates";
import { COMPOSE_FILENAMES, localId, type AppFileMeta } from "@/types/app-config";

// DraftState is what "Save as template" hands the new template's editor:
// the draft, what was left out of it, and the app it was built from.
export type DraftState = { draft: TemplateDraft; notes: string[]; appName: string };

export type TemplateDraft = {
  name: string;
  summary: string;
  description: string;
  icon: string;
  color: string;
  files: AppFileMeta[]; // is_encrypted is always false: a template holds no secret
  contents: Record<string, string>; // file id -> content
};

export const TEMPLATE_NAME = /^[a-z][a-z0-9-]{0,62}$/;
const MAX_SUMMARY = 200;
const MAX_DESCRIPTION = 4000;

// EXAMPLE_COMPOSE is a new template's compose file: one service, its port
// exposed (each app gets a host port of its own) and a variable each app
// fills in.
export const EXAMPLE_COMPOSE = `services:
  app:
    image: traefik/whoami
    environment:
      # The name the page answers with
      WHOAMI_NAME: "whoami"

x-winterflow:
  expose:
    - container: app
      port: 80
  variables:
    WHOAMI_NAME: {}
`;

// emptyDraft is a new template with the example compose file.
export function emptyDraft(): TemplateDraft {
  const id = localId("file");
  return {
    name: "",
    summary: "",
    description: "",
    icon: "",
    color: "#64748b",
    files: [{ id, filename: "compose.yml", is_encrypted: false }],
    contents: { [id]: EXAMPLE_COMPOSE },
  };
}

export function draftFromBody(name: string, body: TemplateBody): TemplateDraft {
  const files: AppFileMeta[] = [];
  const contents: Record<string, string> = {};
  for (const filename of Object.keys(body.files ?? {}).sort()) {
    const id = localId("file");
    files.push({ id, filename, is_encrypted: false });
    contents[id] = body.files![filename];
  }
  return { name, summary: body.summary, description: body.description, icon: body.icon, color: body.color, files, contents };
}

export function bodyFromDraft(d: TemplateDraft): TemplateBody {
  const files: Record<string, string> = {};
  for (const f of d.files) files[f.filename.trim()] = d.contents[f.id] ?? "";
  return { files, summary: d.summary.trim(), description: d.description.trim(), icon: d.icon, color: d.color };
}

// draftProblem is the first thing the API would refuse of the draft's own
// fields, worded for the editor; null when they look saveable. What the
// compose files declare is the Detected panel's (the API reads it).
export function draftProblem(d: TemplateDraft): string | null {
  if (!TEMPLATE_NAME.test(d.name)) {
    return "Name: lowercase letters, digits and hyphens, starting with a letter (at most 63).";
  }
  const summary = d.summary.trim();
  if (summary.length > MAX_SUMMARY || /[\r\n]/.test(summary)) return `Summary: one line of at most ${MAX_SUMMARY} characters.`;
  if (d.description.trim().length > MAX_DESCRIPTION) return `Description: at most ${MAX_DESCRIPTION} characters.`;
  const names = d.files.map((f) => f.filename.trim());
  if (names.some((n) => !n)) return "Every file needs a filename.";
  if (new Set(names).size !== names.length) return "Two files have the same name.";
  if (!names.some((n) => COMPOSE_FILENAMES.includes(n))) {
    return `A compose file is required (${COMPOSE_FILENAMES.join(", ")}).`;
  }
  return null;
}

// slugName makes a template name from an app name.
export function slugName(appName: string): string {
  const slug = appName
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/^[^a-z]+/, "")
    .slice(0, 63)
    .replace(/-+$/, "");
  return slug || "template";
}

// templateFromApp builds revision 1 of a template from an app as the editor
// holds it (Save as template): its files (secret ones left out), name, icon
// and colour. What stays with the app is noted: its variable values,
// hostnames and redirects (each app made from the template has its own), a
// Git source, backups added in the app, and host ports its compose files
// publish themselves (two apps from the template would both claim them).
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
  if (state.config.variables.length > 0) {
    notes.push(
      "The app's variable values stay with the app: declare what each app fills in under x-winterflow.variables.",
    );
  }
  const ports = new Set<number>();
  for (const f of files) {
    if (!COMPOSE_FILENAMES.includes(f.filename.trim())) continue;
    for (const p of publishedPorts(contents[f.id])) ports.add(p.port);
  }
  if (ports.size > 0) {
    const list = [...ports].sort((a, b) => a - b).join(", ");
    notes.push(
      `The compose file publishes host port ${list} itself; two apps from the template would both claim it. List the container port under x-winterflow.expose instead, and each app gets a host port of its own.`,
    );
  }
  if ((state.config.ingress?.domains.length ?? 0) > 0 || (state.config.ingress?.redirects.length ?? 0) > 0) {
    notes.push("Hostnames and redirects stay with the app: each app made from the template picks its own.");
  }
  for (const b of detail.backups ?? []) {
    if (!b.in_recipe) {
      notes.push(
        `The backup ${b.name} was added in the app; add it to the compose file's x-winterflow.backups to make it part of the template.`,
      );
    }
  }
  return {
    draft: {
      name: slugName(state.config.name),
      summary: "",
      description: "",
      icon: state.config.icon ?? "",
      color: state.config.color ?? "#64748b",
      files,
      contents,
    },
    notes,
  };
}
````

Modify `web/src/pages/org-templates.tsx` (`git apply` this diff):

````diff
diff --git a/web/src/pages/org-templates.tsx b/web/src/pages/org-templates.tsx
--- a/web/src/pages/org-templates.tsx
+++ b/web/src/pages/org-templates.tsx
@@ -11,11 +11,13 @@ import { PageHeader } from "@/components/page-header";
 import { useTemplateLooks } from "@/hooks/use-template-looks";
 import { useTemplatesApi } from "@/hooks/use-templates-api";
 import { NEW_TEMPLATE_PATH, templatePath } from "@/lib/routes";
-import { usageLabel, type TemplateSummary } from "@/lib/templates";
+import { orgTemplates, usageLabel, type TemplateSummary } from "@/lib/templates";
 import { relativeTime } from "@/lib/utils";
 
-// OrgTemplatesPage is /templates: the organization's templates as cards
-// (how many apps use each, when it last changed) and New template.
+// OrgTemplatesPage is /templates: the organization's own templates as cards
+// (how many apps use each, when it last changed) and New template. (The
+// catalog's templates, listed after them by the API, get their section with
+// the catalog gallery.)
 export default function OrgTemplatesPage() {
   const api = useTemplatesApi();
   const [list, setList] = useState<TemplateSummary[] | null>(null);
@@ -23,7 +25,10 @@ export default function OrgTemplatesPage() {
   const looks = useTemplateLooks(api, list);
 
   useEffect(() => {
-    api.listTemplates().then(setList, (e: unknown) => setError(e instanceof Error ? e.message : "Failed to load templates"));
+    api.listTemplates().then(
+      (all) => setList(orgTemplates(all)),
+      (e: unknown) => setError(e instanceof Error ? e.message : "Failed to load templates"),
+    );
   }, [api]);
 
   return (
````

Modify `web/src/pages/template-page.tsx` (`git apply` this diff):

````diff
diff --git a/web/src/pages/template-page.tsx b/web/src/pages/template-page.tsx
--- a/web/src/pages/template-page.tsx
+++ b/web/src/pages/template-page.tsx
@@ -1,4 +1,4 @@
-import { useCallback, useEffect, useRef, useState } from "react";
+import { useCallback, useEffect, useMemo, useRef, useState } from "react";
 import { Link, useLocation, useNavigate, useParams } from "react-router-dom";
 import { Ellipsis, Plus, Trash2 } from "lucide-react";
 import { toast } from "sonner";
@@ -19,6 +19,7 @@ import { NotFound } from "@/components/not-found";
 import { PageHeader } from "@/components/page-header";
 import { LoadError, PageSpinner } from "@/components/page-states";
 import { RolloutStrip } from "@/components/rollout-strip";
+import { TemplateDetected } from "@/components/template-detected";
 import { TemplateEditor } from "@/components/template-editor";
 import { TemplateInstances } from "@/components/template-instances";
 import { TemplateRevisions } from "@/components/template-revisions";
@@ -27,6 +28,7 @@ import { useServers } from "@/context/use-servers";
 import { useRollout } from "@/hooks/use-rollout";
 import { useRolloutsApi } from "@/hooks/use-rollouts-api";
 import { useTabParam } from "@/hooks/use-tab-param";
+import { useTemplateInspection } from "@/hooks/use-template-inspection";
 import { useTemplatesApi } from "@/hooks/use-templates-api";
 import { isActive } from "@/lib/rollouts";
 import { bodyFromDraft, draftFromBody, draftProblem, emptyDraft, type DraftState, type TemplateDraft } from "@/lib/template-draft";
@@ -179,6 +181,21 @@ export default function TemplatePage() {
   };
 
   const dirty = baseline === null || !sameDraft(draft, baseline);
+  // The Detected panel: a saved version's own reading until the draft
+  // changes, else the API's reading of the draft (inspect-template).
+  const body = useMemo(() => bodyFromDraft(draft), [draft]);
+  const reading = dirty || !view;
+  const inspected = useTemplateInspection(api, body, reading);
+  const detectedPanel = reading ? (
+    <TemplateDetected
+      detected={inspected.inspection?.detected ?? null}
+      problem={inspected.checking ? "" : (inspected.inspection?.problem ?? "")}
+      error={inspected.error}
+      checking={inspected.checking}
+    />
+  ) : (
+    <TemplateDetected detected={view?.detected ?? null} />
+  );
 
   const problem = tried ? draftProblem(draft) : null;
 
@@ -192,13 +209,13 @@ export default function TemplatePage() {
       const saved = await api.saveTemplate({
         ...(view ? { id: view.id } : {}),
         name: draft.name,
-        body: bodyFromDraft(draft),
+        body,
         ...(note.trim() ? { note: note.trim() } : {}),
         base_revision: view?.latest_revision ?? 0,
       });
       toast.success(`Saved version ${saved.revision}`);
       if (saved.warnings?.length) {
-        toast.warning("Check these references", { description: saved.warnings.join("\n") });
+        toast.warning("Check these warnings", { description: saved.warnings.join("\n") });
       }
       setNote("");
       if (!view) {
@@ -292,7 +309,7 @@ export default function TemplatePage() {
           </Button>
         </StickyActionBar>
       ) : null}
-      <TemplateEditor draft={draft} onChange={setDraft} />
+      <TemplateEditor draft={draft} onChange={setDraft} detected={detectedPanel} />
     </>
   );
 
@@ -343,8 +360,9 @@ export default function TemplatePage() {
       {prefill ? (
         <div className="space-y-2 rounded-md border bg-muted/30 px-4 py-3 text-sm text-muted-foreground">
           <p>
-            Built from <span className="font-medium text-foreground">{prefill.appName}</span>. Replace what else differs
-            per app, such as hostnames, with <code className="font-mono">{"${PARAM}"}</code> parameters before saving.
+            Built from <span className="font-medium text-foreground">{prefill.appName}</span>. Before saving, declare under{" "}
+            <code className="font-mono">x-winterflow</code> what differs per app: the variables each app fills in and the
+            ports it exposes.
           </p>
           {prefill.notes.length ? (
             <ul className="list-disc space-y-1 pl-4">
````

- [ ] **Step 4: Run them and see them pass**

```bash
CI=true pnpm --dir web exec vitest run src/lib/template-draft.test.ts src/components/template-editor.test.tsx src/components/template-detected.test.tsx src/hooks/use-template-inspection.test.tsx src/components/template-revisions.test.tsx src/pages/template-page.test.tsx src/pages/org-templates.test.tsx src/pages/app-details.test.tsx
CI=true pnpm --dir web run test
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat(web): template editor is files plus metadata, with a Detected panel" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 10: Web: install a template with its declared variables, hostnames and backups

New app -> From a template offers the org's own templates, an app name (the template's, made unique), the declared variables (required first), a hostname per http expose entry when the server routes hostnames (`HostnameField` with the live coverage check), a switch and storage per declared backup (off by default), and the version under Advanced; the create sends `name`, `values`, `secrets`, `hosts`, `backups`. `lib/template-params.ts` and `ParameterFields` lose their last importers and go.

**Files:**
- Modify: `web/src/components/new-app/new-app-choices.test.tsx`
- Modify: `web/src/components/new-app/template-flow.test.tsx`
- Modify: `web/src/components/new-app/template-flow.tsx`
- Delete: `web/src/components/parameter-fields.test.tsx`
- Delete: `web/src/components/parameter-fields.tsx`
- Modify: `web/src/hooks/use-template-count.ts`
- Delete: `web/src/lib/template-params.test.ts`
- Delete: `web/src/lib/template-params.ts`

**Interfaces:**
- Consumes: `VariableFields` (Task 8), `useRegistrations`, `HostnameField`, `validHostname`, `orgTemplates`, `newKey`/`splitValues`/`variableErrors`.
- Produces: `TemplateFlow({ serverId, initialTemplate? })` (same props); `useTemplateCount` counts `orgTemplates`; deleted `lib/template-params.ts`, `components/parameter-fields.tsx` and their tests.

- [ ] **Step 1: Write the failing tests**

Delete:

```bash
git rm -q web/src/components/parameter-fields.test.tsx web/src/lib/template-params.test.ts
```

Modify `web/src/components/new-app/new-app-choices.test.tsx` (`git apply` this diff):

````diff
diff --git a/web/src/components/new-app/new-app-choices.test.tsx b/web/src/components/new-app/new-app-choices.test.tsx
--- a/web/src/components/new-app/new-app-choices.test.tsx
+++ b/web/src/components/new-app/new-app-choices.test.tsx
@@ -10,7 +10,11 @@ import { NotificationsContext, type NotificationsContextValue } from "@/context/
 import { fakeAPI } from "@/test/fake-api";
 
 const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.fn() } as NotificationsContextValue;
-const ONE = [{ id: "t1", name: "twillingate", latest_revision: 2, instances: 0, behind: 0, updated_at: "" }];
+const ONE = [
+  { id: "t1", source: "org", name: "twillingate", latest_revision: 2, instances: 0, behind: 0, updated_at: "" },
+  // A catalog template is not the org's own: it doesn't count.
+  { id: "c1", source: "catalog", name: "Actual Budget", latest_revision: 1, instances: 0, behind: 0, updated_at: "" },
+];
 
 function At() {
   const l = useLocation();
@@ -89,6 +93,16 @@ describe("NewAppButton", () => {
     expect(api.calls).toHaveLength(1);
   });
 
+  it("leaves the template out when the organization has only catalog templates", async () => {
+    const user = userEvent.setup();
+    const api = fakeAPI({ "/api/v1/template/get-templates": { templates: ONE.slice(1) } });
+    renderIn(<NewAppButton serverId="s1" />);
+    await user.click(screen.getByRole("button", { name: "New app" }));
+    await waitFor(() => expect(api.calls).toHaveLength(1));
+    await waitFor(() => expect(screen.queryByTestId("template-choice-skeleton")).not.toBeInTheDocument());
+    expect(screen.queryByRole("link", { name: /From a template/ })).not.toBeInTheDocument();
+  });
+
   it("leaves the template out when the templates can't be read", async () => {
     const user = userEvent.setup();
     const api = fakeAPI({ "/api/v1/template/get-templates": new Error("down") });
````

Modify `web/src/components/new-app/template-flow.test.tsx` (`git apply` this diff):

````diff
diff --git a/web/src/components/new-app/template-flow.test.tsx b/web/src/components/new-app/template-flow.test.tsx
--- a/web/src/components/new-app/template-flow.test.tsx
+++ b/web/src/components/new-app/template-flow.test.tsx
@@ -1,4 +1,4 @@
-import { act, render, screen, waitFor, within } from "@testing-library/react";
+import { act, render, screen, waitFor } from "@testing-library/react";
 import userEvent from "@testing-library/user-event";
 import { MemoryRouter, Route, Routes } from "react-router-dom";
 import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
@@ -7,6 +7,7 @@ import { TemplateFlow } from "@/components/new-app/template-flow";
 import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
 import { ApiError, LOAD_TIMEOUT_MS, type TemplateView } from "@/lib/templates";
 import { fakeAPI } from "@/test/fake-api";
+import type { DeclaredVariable } from "@/types/app-config";
 
 const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
 vi.mock("sonner", () => ({ toast }));
@@ -14,35 +15,51 @@ vi.mock("@/lib/ecies", () => ({
   ENCRYPTED: "<encrypted>",
   encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
 }));
-vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "s1" }) }));
+// ingress says whether server s1 runs the built-in proxy (hostnames).
+const ingress = vi.hoisted(() => ({ on: false }));
+vi.mock("@/context/use-servers", () => ({
+  useServers: () => ({ activeServerId: "s1", servers: [{ id: "s1", name: "alpha", features: { ingress: ingress.on } }] }),
+}));
 const refresh = vi.hoisted(() => vi.fn(async () => {}));
 const reconcile = vi.hoisted(() => vi.fn(async (): Promise<{ id: string }[]> => []));
 const noteDeployFailure = vi.hoisted(() => vi.fn());
-const rename = vi.hoisted(() => vi.fn(async () => {}));
 const apps = vi.hoisted(() => ({ list: [] as { id: string; name: string; serverId: string }[] }));
 vi.mock("@/context/use-apps", () => ({
-  useApps: () => ({ apps: apps.list, refresh, reconcile, noteDeployFailure, rename }),
+  useApps: () => ({ apps: apps.list, refresh, reconcile, noteDeployFailure }),
 }));
 const serverStorages = vi.hoisted(() => vi.fn(async () => ["tenants", "local"]));
 vi.mock("@/context/use-backups", () => ({ useBackups: () => ({ serverStorages }) }));
 
+const v = (name: string, extra: Partial<DeclaredVariable> = {}): DeclaredVariable => ({
+  name,
+  required: false,
+  encrypted: false,
+  default: null,
+  services: ["app"],
+  ...extra,
+});
+
 const VIEW: TemplateView = {
   id: "t1",
+  source: "org",
+  slug: "",
   name: "twillingate",
   latest_revision: 2,
+  withdrawn_at: null,
   revision: 2,
-  body: {
-    files: { "compose.yaml": "services: {}\n" },
-    parameters: [
-      { name: "MEMORY_MAX", default: "256m", pattern: "^[0-9]+m$" },
-      { name: "NAME", required: true, pattern: "^[a-z]{3,20}$", description: "Instance name" },
-      { name: "SESSION_KEY", secret: true, required: true },
+  body: { files: { "compose.yaml": "services: {}\n" }, summary: "Analytics for one tenant", description: "", icon: "chart", color: "" },
+  detected: {
+    variables: [
+      v("MEMORY_MAX", { default: "256m" }),
+      v("NAME", { required: true, pattern: "^[a-z]{3,20}$", description: "Instance name" }),
+      v("SESSION_KEY", { required: true, encrypted: true }),
     ],
-    app_name: "${NAME}",
-    icon: "chart",
-    color: "",
-    ingress: [{ host: "${NAME}.twillingate.app", port: 8080 }],
-    backups: { db: { enabled: true, storage: "tenants" } },
+    expose: [
+      { id: "app:8080/http", target: "app:8080", container: "app", port: 8080, protocol: "http" },
+      { id: "app:9000/tcp", target: "app:9000", container: "app", port: 9000, protocol: "tcp" },
+    ],
+    backups: [{ name: "db", type: "sqlite" }],
+    warnings: [],
   },
   note: "",
   created_at: "2026-10-12T10:00:00Z",
@@ -53,7 +70,7 @@ const VIEW: TemplateView = {
   ],
 };
 
-const SUMMARY = { id: "t1", name: "twillingate", latest_revision: 2, instances: 0, behind: 0, updated_at: "" };
+const SUMMARY = { id: "t1", source: "org", slug: "", name: "twillingate", latest_revision: 2, instances: 0, behind: 0, withdrawn_at: null, updated_at: "" };
 
 const waitFor_ = vi.fn();
 const notifications = { connected: true, subscribe: () => () => {}, waitFor: waitFor_ } as NotificationsContextValue;
@@ -64,6 +81,7 @@ const routes = (extra: Record<string, unknown> = {}) => ({
   "/api/v1/domains/check": { available: true, covered: false, registration: null, claims: null },
   "/api/v1/server/get-public-key": { public_key: "K1" },
   "/api/v1/instance/get-instance": { app_id: "app-9", key: "k" },
+  "/api/v1/domain/get-domains": { domains: [{ name: "*.example.com", tls: "auto" }] },
   ...extra,
 });
 
@@ -124,7 +142,7 @@ beforeEach(() => {
   reconcile.mockReset();
   reconcile.mockImplementation(async () => []);
   noteDeployFailure.mockReset();
-  rename.mockReset();
+  ingress.on = false;
   apps.list = [];
   waitFor_.mockReset();
   serverStorages.mockImplementation(async () => ["tenants", "local"]);
@@ -137,32 +155,37 @@ describe("TemplateFlow", () => {
     fakeAPI(routes());
     renderFlow();
     expect(await screen.findByRole("button", { name: /twillingate/ })).toHaveAttribute("aria-pressed", "true");
+    expect(await screen.findByText("Analytics for one tenant")).toBeInTheDocument();
     const fields = await screen.findAllByRole("textbox");
     expect(fields[0]).toHaveAccessibleName("App name");
     expect(fields[1]).toHaveAccessibleName("Instance name (NAME)");
-    // Optional values wait under More options, their defaults shown.
+    // Optional values wait under More options, the compose file's shown.
     expect(screen.queryByLabelText("MEMORY_MAX")).not.toBeInTheDocument();
     await user.click(screen.getByRole("button", { name: "More options" }));
     expect(screen.getByLabelText("MEMORY_MAX")).toHaveAttribute("placeholder", "Default: 256m");
+    // A pattern mismatch is a hint (the server decides); a $ is refused.
     await user.type(screen.getByLabelText("Instance name (NAME)"), "Shop");
-    expect(screen.getByText("Must match ^[a-z]{3,20}$")).toBeInTheDocument();
-    expect(screen.getByLabelText("Instance name (NAME)")).toHaveAttribute("aria-invalid", "true");
-    expect(screen.getByLabelText("Instance name (NAME)")).toHaveAccessibleDescription("Must match ^[a-z]{3,20}$");
-    expect(screen.getByLabelText("MEMORY_MAX")).not.toHaveAttribute("aria-invalid");
-    expect(screen.queryByText("Required")).not.toBeInTheDocument();
+    expect(screen.getByText("May not match ^[a-z]{3,20}$; the server checks it on save.")).toBeInTheDocument();
+    expect(screen.getByLabelText("Instance name (NAME)")).not.toHaveAttribute("aria-invalid");
+    await user.type(screen.getByLabelText("MEMORY_MAX"), "$x");
+    expect(screen.getByLabelText("MEMORY_MAX")).toHaveAttribute("aria-invalid", "true");
+    expect(screen.queryByText("Enter a value: this variable is required.")).not.toBeInTheDocument();
   });
 
-  it("waits for a pick when there are several templates", async () => {
+  it("lists the org's own templates only, and waits for a pick when there are several", async () => {
     const user = userEvent.setup();
     const api = fakeAPI(
       routes({
-        "/api/v1/template/get-templates": { templates: [SUMMARY, { ...SUMMARY, id: "t2", name: "blog", latest_revision: 4 }] },
+        "/api/v1/template/get-templates": {
+          templates: [SUMMARY, { ...SUMMARY, id: "t2", name: "blog", latest_revision: 4 }, { ...SUMMARY, id: "c1", source: "catalog", name: "Catalog app" }],
+        },
         "/api/v1/template/get-template": (_: unknown, q: URLSearchParams) =>
           q.get("id") === "t2" ? { ...VIEW, id: "t2", name: "blog", latest_revision: 4, revision: 4 } : VIEW,
       }),
     );
     renderFlow();
     expect(await screen.findByText("Pick a template to continue.")).toBeInTheDocument();
+    expect(screen.queryByRole("button", { name: /Catalog app/ })).not.toBeInTheDocument();
     expect(screen.getByRole("button", { name: /blog/ })).toHaveTextContent("Version 4");
     await user.click(screen.getByRole("button", { name: /blog/ }));
     expect(await screen.findByLabelText("Instance name (NAME)")).toBeInTheDocument();
@@ -191,43 +214,59 @@ describe("TemplateFlow", () => {
     expect(screen.queryByText("Pick a template to continue.")).not.toBeInTheDocument();
   });
 
-  it("previews the name and the address with the domain coverage check", async () => {
+  it("offers a hostname per http port on a server that routes them, checking its coverage", async () => {
     const user = userEvent.setup();
-    const api = fakeAPI(routes());
+    ingress.on = true;
+    const api = fakeAPI(routes({ "/api/v1/instance/create-instance": { request_id: "" } }));
     renderFlow();
-    expect(await screen.findByText("Fill in NAME to see the address.")).toBeInTheDocument();
-    await user.type(screen.getByLabelText("Instance name (NAME)"), "shop");
-    const addresses = screen.getByRole("list", { name: "Addresses" });
-    expect(within(addresses).getByText("shop.twillingate.app")).toBeInTheDocument();
-    expect(within(addresses).getByText(/Will be available at/)).toBeInTheDocument();
-    expect(await within(addresses).findByText(/No domain on this server covers this hostname/)).toBeInTheDocument();
-    const checks = api.calls.filter((c) => c.path === "/api/v1/domains/check");
-    expect(checks.at(-1)?.query).toBe("?server_id=s1&domain=shop.twillingate.app");
-    expect(screen.getByLabelText("App name")).toHaveValue("shop");
+    const host = await screen.findByLabelText("Hostname for app:8080");
+    // The tcp port takes no hostname.
+    expect(screen.queryByLabelText("Hostname for app:9000")).not.toBeInTheDocument();
+    await user.type(host, "shop.twillingate.app");
+    expect(await screen.findByText(/No domain on this server covers this hostname/)).toBeInTheDocument();
+    expect(api.calls.filter((c) => c.path === "/api/v1/domains/check").at(-1)?.query).toBe("?server_id=s1&domain=shop.twillingate.app");
+    await user.clear(host);
+    await user.type(host, "bad_host!.com");
+    await fill(user);
+    await user.click(screen.getByRole("button", { name: "Create app" }));
+    expect(host).toHaveAccessibleDescription(/This isn't a valid hostname./);
+    expect(api.posted("/api/v1/instance/create-instance")).toEqual([]);
+    await user.clear(host);
+    await user.type(host, "shop.example.com");
+    await user.click(screen.getByRole("button", { name: "Create app" }));
+    await screen.findByText("the new app");
+    expect(api.posted("/api/v1/instance/create-instance")[0]).toMatchObject({ hosts: [{ host: "shop.example.com", expose: "app:8080" }] });
+  });
+
+  it("asks for no hostname on a server without the built-in proxy", async () => {
+    fakeAPI(routes());
+    renderFlow();
+    await screen.findByLabelText("Instance name (NAME)");
+    expect(screen.queryByRole("heading", { name: "Hostnames" })).not.toBeInTheDocument();
   });
 
-  it("creates the app with encrypted secrets and a browser key, then opens it", async () => {
+  it("creates the app with encrypted secrets, its name and a browser key, then opens it", async () => {
     const user = userEvent.setup();
     const api = fakeAPI(routes({ "/api/v1/instance/create-instance": { request_id: "r1" } }));
     accepted(api);
     waitFor_.mockResolvedValue({ status: 0 });
     renderFlow();
     await user.click(await screen.findByRole("button", { name: "Create app" }));
-    expect(screen.getAllByText("Required")).toHaveLength(2);
+    expect(screen.getAllByText("Enter a value: this variable is required.")).toHaveLength(2);
     await waitFor(() => expect(screen.getByLabelText("Instance name (NAME)")).toHaveFocus());
     await user.type(screen.getByLabelText("Instance name (NAME)"), "shop");
     await user.type(screen.getByLabelText("SESSION_KEY"), "hunter2");
-    await user.selectOptions(screen.getByLabelText("Storage for db"), "local");
     await user.click(screen.getByRole("button", { name: "Create app" }));
     expect(await screen.findByText("the new app")).toBeInTheDocument();
     const [sent] = api.posted("/api/v1/instance/create-instance") as Record<string, unknown>[];
-    expect(sent).toMatchObject({
+    expect(sent).toEqual({
       server_id: "s1",
+      key: sent.key,
       template_id: "t1",
       revision: 2,
-      parameters: { NAME: "shop" },
+      name: "twillingate",
+      values: { NAME: "shop" },
       secrets: { SESSION_KEY: "ct(K1):hunter2" },
-      backups: { db: "local" },
     });
     expect(sent.key).toMatch(/^[0-9a-f-]{36}$/);
     expect(JSON.stringify(api.calls)).not.toContain('"hunter2"');
@@ -235,23 +274,18 @@ describe("TemplateFlow", () => {
     expect(refresh).toHaveBeenCalled();
     expect(toast.success).toHaveBeenCalledWith("App created");
     expect(api.calls.find((c) => c.path === "/api/v1/instance/get-instance")?.query).toBe(`?server_id=s1&key=${sent.key}`);
-    // The name is the template's: nothing to rename.
-    expect(rename).not.toHaveBeenCalled();
   });
 
   it("names the app after the template, numbered when the server has one of that name", async () => {
     const user = userEvent.setup();
-    apps.list = [{ id: "x", name: "shop", serverId: "s1" }, { id: "y", name: "shop 2", serverId: "other" }];
-    const api = fakeAPI(routes({ "/api/v1/instance/create-instance": { request_id: "r1" } }));
-    accepted(api);
-    waitFor_.mockResolvedValue({ status: 0 });
+    apps.list = [{ id: "x", name: "twillingate", serverId: "s1" }, { id: "y", name: "twillingate 2", serverId: "other" }];
+    const api = fakeAPI(routes({ "/api/v1/instance/create-instance": { request_id: "" } }));
     renderFlow();
-    await user.type(await screen.findByLabelText("Instance name (NAME)"), "shop");
-    expect(screen.getByLabelText("App name")).toHaveValue("shop 2");
-    await user.type(screen.getByLabelText("SESSION_KEY"), "s");
+    await fill(user);
+    expect(screen.getByLabelText("App name")).toHaveValue("twillingate 2");
     await user.click(screen.getByRole("button", { name: "Create app" }));
     expect(await screen.findByText("the new app")).toBeInTheDocument();
-    expect(rename).toHaveBeenCalledWith("app-9", "shop 2");
+    expect(api.posted("/api/v1/instance/create-instance")[0]).toMatchObject({ name: "twillingate 2" });
   });
 
   it("warns about a name already used, and asks for one when it is cleared", async () => {
@@ -276,11 +310,11 @@ describe("TemplateFlow", () => {
     renderFlow();
     await fill(user);
     await user.click(screen.getByRole("button", { name: "More options" }));
-    await user.type(screen.getByLabelText("MEMORY_MAX"), "lots");
+    await user.type(screen.getByLabelText("MEMORY_MAX"), "lo$ts");
     await user.click(screen.getByRole("button", { name: "More options" }));
     expect(screen.queryByLabelText("MEMORY_MAX")).not.toBeInTheDocument();
     await user.click(screen.getByRole("button", { name: "Create app" }));
-    expect(screen.getByLabelText("MEMORY_MAX")).toHaveAccessibleDescription("Must match ^[0-9]+m$");
+    expect(screen.getByLabelText("MEMORY_MAX")).toHaveAccessibleDescription("A plain value can't contain $.");
     await waitFor(() => expect(screen.getByLabelText("MEMORY_MAX")).toHaveFocus());
     expect(screen.getByRole("alert")).toHaveTextContent("Some fields need attention.");
     expect(api.posted("/api/v1/instance/create-instance")).toEqual([]);
@@ -297,7 +331,7 @@ describe("TemplateFlow", () => {
     const api = fakeAPI(
       routes({
         "/api/v1/instance/create-instance": () =>
-          refuse ? new ApiError('invalid template: parameters.NAME: "abc" is taken', 400) : { request_id: "r1" },
+          refuse ? new ApiError('invalid variable value: variable NAME: "abc" is taken', 400) : { request_id: "r1" },
         "/api/v1/instance/update-instance": { request_id: "r2" },
       }),
     );
@@ -394,22 +428,6 @@ describe("TemplateFlow", () => {
     expect(updates).toEqual([expect.objectContaining({ key: creates[0].key })]);
   });
 
-  it("keeps a template's own storage, offering No storage only for a backup without one", async () => {
-    fakeAPI(routes());
-    const { unmount } = renderFlow();
-    const select = await screen.findByLabelText("Storage for db");
-    expect(select).toHaveValue("tenants");
-    expect(within(select).queryByRole("option", { name: "No storage" })).not.toBeInTheDocument();
-    unmount();
-    vi.unstubAllGlobals();
-
-    const bare = { ...VIEW, body: { ...VIEW.body, backups: { db: { enabled: false } } } };
-    fakeAPI(routes({ "/api/v1/template/get-template": bare }));
-    renderFlow();
-    const empty = await screen.findByLabelText("Storage for db");
-    expect(within(empty).getByRole("option", { name: "No storage" })).toBeInTheDocument();
-  });
-
   it("holds the cards' place with a skeleton while the templates are read", async () => {
     const api = fakeAPI(routes());
     const release = held(api, "/api/v1/template/get-templates");
@@ -421,31 +439,49 @@ describe("TemplateFlow", () => {
   });
 
   it("keeps Create off until the server's storages are read", async () => {
-    const user = userEvent.setup();
     let resolve: (names: string[]) => void = () => {};
     serverStorages.mockImplementation(() => new Promise<string[]>((r) => (resolve = r)));
-    const bare = { ...VIEW, body: { ...VIEW.body, backups: { db: { enabled: true } } } };
-    const api = fakeAPI(routes({ "/api/v1/template/get-template": bare, "/api/v1/instance/create-instance": { request_id: "" } }));
+    fakeAPI(routes());
     renderFlow();
     await screen.findByLabelText("Instance name (NAME)");
     expect(screen.getByRole("button", { name: "Create app" })).toBeDisabled();
     await act(async () => resolve(["tenants"]));
     expect(screen.getByRole("button", { name: "Create app" })).toBeEnabled();
+  });
+
+  it("turns on a declared backup with a storage of this server; the rest start off", async () => {
+    const user = userEvent.setup();
+    const api = fakeAPI(routes({ "/api/v1/instance/create-instance": { request_id: "" } }));
+    renderFlow();
+    const storage = await screen.findByLabelText("Storage for db");
+    expect(screen.getByRole("switch", { name: "Back up db" })).not.toBeChecked();
+    expect(storage).toBeDisabled();
+    await user.click(screen.getByRole("switch", { name: "Back up db" }));
+    expect(storage).toHaveValue("tenants");
+    await user.selectOptions(storage, "local");
     await fill(user);
     await user.click(screen.getByRole("button", { name: "Create app" }));
     await screen.findByText("the new app");
-    expect(api.posted("/api/v1/instance/create-instance")[0]).toMatchObject({ backups: { db: "tenants" } });
+    expect(api.posted("/api/v1/instance/create-instance")[0]).toMatchObject({ backups: { db: { enabled: true, storage: "local" } } });
+  });
+
+  it("refuses a backup turned on without a storage, linking to Storages", async () => {
+    const user = userEvent.setup();
+    serverStorages.mockImplementation(async () => []);
+    const api = fakeAPI(routes());
+    renderFlow();
+    await screen.findByLabelText("Storage for db");
+    expect(screen.getByRole("link", { name: "Add one" })).toHaveAttribute("href", "/server/s1/settings/storages");
+    await user.click(screen.getByRole("switch", { name: "Back up db" }));
+    await fill(user);
+    await user.click(screen.getByRole("button", { name: "Create app" }));
+    expect(screen.getByLabelText("Storage for db")).toHaveAccessibleDescription("Pick a storage to keep its backups in.");
+    expect(api.posted("/api/v1/instance/create-instance")).toEqual([]);
   });
 
   it("starts the next template untried and folded when the pick changes", async () => {
     const user = userEvent.setup();
-    const optionalRequired = {
-      ...VIEW,
-      body: {
-        ...VIEW.body,
-        parameters: [...(VIEW.body.parameters ?? []), { name: "LABEL", pattern: "^x+$" }],
-      },
-    };
+    const optionalRequired = { ...VIEW, detected: { ...VIEW.detected, variables: [...(VIEW.detected.variables ?? []), v("LABEL")] } };
     fakeAPI(
       routes({
         "/api/v1/template/get-templates": { templates: [SUMMARY, { ...SUMMARY, id: "t2", name: "blog" }] },
@@ -458,7 +494,7 @@ describe("TemplateFlow", () => {
     await user.type(await screen.findByLabelText("Instance name (NAME)"), "abc");
     await user.type(screen.getByLabelText("SESSION_KEY"), "s");
     await user.click(screen.getByRole("button", { name: "More options" }));
-    await user.type(screen.getByLabelText("LABEL"), "no");
+    await user.type(screen.getByLabelText("LABEL"), "$no");
     await user.click(screen.getByRole("button", { name: "Create app" }));
     expect(screen.getByRole("alert")).toHaveTextContent("Some fields need attention.");
 
@@ -466,42 +502,7 @@ describe("TemplateFlow", () => {
     await screen.findByLabelText("Instance name (NAME)");
     expect(screen.queryByRole("alert")).not.toBeInTheDocument();
     expect(screen.queryByLabelText("LABEL")).not.toBeInTheDocument();
-    expect(screen.queryByText("Required")).not.toBeInTheDocument();
-  });
-
-  it("preselects this server's first storage for a backup without a default, and sends it", async () => {
-    const user = userEvent.setup();
-    const bare = { ...VIEW, body: { ...VIEW.body, backups: { db: { enabled: true } } } };
-    const api = fakeAPI(routes({ "/api/v1/template/get-template": bare, "/api/v1/instance/create-instance": { request_id: "" } }));
-    renderFlow();
-    await waitFor(() => expect(screen.getByLabelText("Storage for db")).toHaveValue("tenants"));
-    await fill(user);
-    await user.click(screen.getByRole("button", { name: "Create app" }));
-    await screen.findByText("the new app");
-    expect(api.posted("/api/v1/instance/create-instance")[0]).toMatchObject({ backups: { db: "tenants" } });
-  });
-
-  it("sends no storage when the server has none", async () => {
-    const user = userEvent.setup();
-    serverStorages.mockImplementation(async () => []);
-    const bare = { ...VIEW, body: { ...VIEW.body, backups: { db: { enabled: true } } } };
-    const api = fakeAPI(routes({ "/api/v1/template/get-template": bare, "/api/v1/instance/create-instance": { request_id: "" } }));
-    renderFlow();
-    expect(await screen.findByLabelText("Storage for db")).toHaveValue("");
-    await fill(user);
-    await user.click(screen.getByRole("button", { name: "Create app" }));
-    await screen.findByText("the new app");
-    expect(api.posted("/api/v1/instance/create-instance")[0]).not.toHaveProperty("backups");
-  });
-
-  it("flags a template storage this server doesn't have, linking to Storages", async () => {
-    serverStorages.mockImplementation(async () => ["local"]);
-    fakeAPI(routes());
-    renderFlow();
-    expect(await screen.findByText("This server has no storage named tenants.", { exact: false })).toBeInTheDocument();
-    expect(screen.getByRole("option", { name: "tenants (not on this server)" })).toBeInTheDocument();
-    expect(screen.getByLabelText("Storage for db")).toHaveValue("tenants");
-    expect(screen.getByRole("link", { name: "Add it" })).toHaveAttribute("href", "/server/s1/settings/storages");
+    expect(screen.queryByText("Enter a value: this variable is required.")).not.toBeInTheDocument();
   });
 
   it("loads an older version from Advanced", async () => {
````

- [ ] **Step 2: Run them and see them fail**

Run: `CI=true pnpm --dir web exec vitest run src/components/new-app/template-flow.test.tsx src/components/new-app/new-app-choices.test.tsx`
Expected: FAIL — the flow still reads `body.parameters`, offers no hostnames or backup switches and counts catalog templates.

- [ ] **Step 3: Implement**

Delete:

```bash
git rm -q web/src/components/parameter-fields.tsx web/src/lib/template-params.ts
```

Replace the whole of `web/src/components/new-app/template-flow.tsx` with:

````tsx
import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { LayoutTemplate } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { AppIcon } from "@/components/app-icon";
import { Disclosure } from "@/components/disclosure";
import { EmptyState } from "@/components/empty-state";
import { FieldMessage, FormField } from "@/components/form-field";
import { HostnameField } from "@/components/hostname-field";
import { NativeSelect } from "@/components/native-select";
import { CreateBar } from "@/components/new-app/create-bar";
import { LoadError, PageSpinner } from "@/components/page-states";
import { VariableFields } from "@/components/variable-fields";
import { useApps } from "@/context/use-apps";
import { useBackups } from "@/context/use-backups";
import { useServers } from "@/context/use-servers";
import { useRegistrations } from "@/hooks/use-registrations";
import { useTemplateLooks } from "@/hooks/use-template-looks";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { validHostname } from "@/lib/domains";
import { errorHeadline } from "@/lib/errors";
import { focusFirstInvalid, invalidAttrs } from "@/lib/field-errors";
import { uniqueAppName } from "@/lib/new-app";
import { appPath, appsPath, serverSettingsPath, TEMPLATES_PATH } from "@/lib/routes";
import { newKey, splitValues, variableErrors } from "@/lib/template-values";
import {
  ApiError,
  orgTemplates,
  sealSecrets,
  type BackupChoice,
  type InstanceHost,
  type TemplateSummary,
  type TemplateView,
  type UpdateInstanceBody,
} from "@/lib/templates";
import { cn } from "@/lib/utils";

// TemplateFlow is New app → From a template: pick one of the org's
// templates (cards), name the app (the template's name by default), fill in
// the variables its compose file declares (required ones first, optional
// ones under "More options", the compose file's own values shown), give
// each http port it exposes a hostname when the server has registered
// domains, turn on the backups it declares with a storage each, then
// Create app. Secret values are encrypted in the browser for this server.
// The version (latest by default) is under "Advanced". initialTemplate is
// picked to begin with (a template page's Create app).
export function TemplateFlow({ serverId, initialTemplate = "" }: { serverId: string; initialTemplate?: string }) {
  const navigate = useNavigate();
  const { apps, refresh, reconcile, noteDeployFailure } = useApps();
  const { serverStorages } = useBackups();
  const { servers } = useServers();
  const ingressOn = Boolean(servers.find((s) => s.id === serverId)?.features?.ingress);
  const { registrations, refresh: refreshDomains } = useRegistrations(serverId, ingressOn);
  const api = useTemplatesApi();

  const [templates, setTemplates] = useState<TemplateSummary[] | null>(null);
  const looks = useTemplateLooks(api, templates);
  const [templateId, setTemplateId] = useState(initialTemplate);
  const [revision, setRevision] = useState(0); // 0: the latest
  const [view, setView] = useState<TemplateView | null>(null);
  const [values, setValues] = useState<Record<string, string>>({});
  // hosts are the hostnames typed per http expose target.
  const [hosts, setHosts] = useState<Record<string, string>>({});
  // storages are this server's storage names (null while read).
  const [storages, setStorages] = useState<string[] | null>(null);
  // backups are the backups turned on, with their storage, by name.
  const [backups, setBackups] = useState<Record<string, BackupChoice>>({});
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
  // name is the app name the user typed (null: the template's, made unique
  // on this server).
  const [name, setName] = useState<string | null>(null);
  const form = useRef<HTMLDivElement>(null);
  // sending is the synchronous guard against a second Create while one runs.
  const sending = useRef(false);

  useEffect(() => {
    let live = true;
    api.listTemplates().then(
      (all) => {
        if (!live) return;
        const list = orgTemplates(all);
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
      (v) => live && setView(v),
      (e: unknown) => live && setLoadError(e instanceof Error ? e.message : "Failed to load the template"),
    );
    return () => {
      live = false;
    };
  }, [api, templateId, revision, attempt]);

  const vars = useMemo(() => view?.detected.variables ?? [], [view]);
  const required = vars.filter((v) => v.required);
  const optional = vars.filter((v) => !v.required);
  const errors = variableErrors(vars, values);
  // Hostnames go to the http ports the compose file exposes, and only on a
  // server that routes them (a registered domain covers each).
  const targets = registrations.length > 0 ? (view?.detected.expose ?? []).filter((e) => e.protocol === "http") : [];
  const hostErrors: Record<string, string> = {};
  for (const t of targets) {
    const h = (hosts[t.target] ?? "").trim();
    if (h && !validHostname(h)) hostErrors[t.target] = "This isn't a valid hostname.";
  }
  const declaredBackups = view?.detected.backups ?? [];
  const backupErrors: Record<string, string> = {};
  for (const b of declaredBackups) {
    const c = backups[b.name];
    if (c?.enabled && !c.storage) backupErrors[b.name] = "Pick a storage to keep its backups in.";
  }
  const otherNames = apps.filter((a) => a.serverId === serverId).map((a) => a.name);
  const chosenName = name ?? uniqueAppName(view?.name ?? "", otherNames);
  const nameTaken =
    chosenName.trim() !== "" && otherNames.some((n) => n.trim().toLowerCase() === chosenName.trim().toLowerCase());
  const nameError = tried && !chosenName.trim() ? "Give the app a name." : undefined;
  const problems = Object.keys(errors).length + Object.keys(hostErrors).length + Object.keys(backupErrors).length;
  const invalid = tried && (problems > 0 || nameError !== undefined);

  // send creates the instance, or once the API holds this key (an earlier
  // attempt whose deploy failed: the instance is pending) updates it. A
  // refused create (a 4xx other than 409) claimed nothing; a 409 (the key is
  // held) or a 5xx (the claim came before the dispatch failed) may have.
  const send = async (body: UpdateInstanceBody, template: TemplateView, appName: string) => {
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
      await api.createInstance({ ...body, template_id: template.id, revision: template.revision, name: appName });
    } catch (e) {
      if (e instanceof ApiError && e.status >= 400 && e.status < 500 && e.status !== 409) setClaimed(false);
      throw e;
    }
  };

  const create = async () => {
    if (!view || !serverId) return;
    setTried(true);
    const found = variableErrors(vars, values);
    if (problems > 0 || !chosenName.trim()) {
      // Opened for good, so fixing the value doesn't fold it away under the cursor.
      if (optional.some((v) => found[v.name])) setMoreOpen(true);
      focusFirstInvalid(form.current);
      return;
    }
    if (sending.current) return;
    sending.current = true;
    setBusy(true);
    setError("");
    try {
      const { plain, typed } = splitValues(vars, values);
      const given = Object.fromEntries(Object.entries(plain).filter(([, v]) => v !== ""));
      const secrets = await sealSecrets(api, serverId, typed);
      const hostList: InstanceHost[] = targets
        .map((t) => ({ host: (hosts[t.target] ?? "").trim(), expose: t.target }))
        .filter((h) => h.host !== "");
      const on = Object.fromEntries(Object.entries(backups).filter(([, c]) => c.enabled));
      const body: UpdateInstanceBody = {
        server_id: serverId,
        key,
        ...(Object.keys(given).length ? { values: given } : {}),
        ...(Object.keys(secrets).length ? { secrets } : {}),
        ...(hostList.length ? { hosts: hostList } : {}),
        ...(Object.keys(on).length ? { backups: on } : {}),
      };
      try {
        await send(body, view, chosenName.trim());
      } catch (e) {
        // A deploy that failed may have left the app behind: open it.
        if (await openLeftApp(e)) return;
        throw e;
      }
      const [inst] = await Promise.all([api.getInstance(serverId, key), refresh()]);
      toast.success("App created");
      navigate(appPath(serverId, inst.app_id));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't create the app");
    } finally {
      sending.current = false;
      setBusy(false);
    }
  };

  // openLeftApp opens the app a failed create left behind (the API holds
  // the instance, pending, and the agent's list has its app), noting the
  // failure so the app reads Failed and says why. False when there is none.
  const openLeftApp = async (e: unknown): Promise<boolean> => {
    if (e instanceof ApiError) return false; // refused: nothing was dispatched
    const inst = await api.getInstance(serverId, key).catch(() => null);
    if (!inst?.app_id) return false;
    const list = await reconcile().catch(() => []);
    if (!list.some((a) => a.id === inst.app_id)) return false;
    const message = inst.last_error || (e instanceof Error ? e.message : "");
    if (message) noteDeployFailure(inst.app_id, message);
    toast.error("The app was created but couldn't start", { description: message ? errorHeadline(message) : undefined });
    navigate(appPath(serverId, inst.app_id));
    return true;
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
  if (templates === null) {
    // The cards' place is held, so the form doesn't jump when they arrive.
    return (
      <div role="status" aria-label="Loading templates" className="grid gap-3 sm:grid-cols-2">
        <Skeleton className="h-16 rounded-xl" />
        <Skeleton className="h-16 rounded-xl" />
      </div>
    );
  }
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
  const setBackup = (name: string, patch: Partial<BackupChoice>) =>
    setBackups((prev) => {
      const cur = prev[name] ?? { enabled: false, storage: storages?.[0] ?? "" };
      return { ...prev, [name]: { ...cur, ...patch } };
    });

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
                setHosts({});
                setBackups({});
                setName(null);
                // The next template's form starts untried and folded.
                setTried(false);
                setMoreOpen(false);
                setError("");
              }}
              className={cn(
                "flex min-h-16 items-center gap-3 rounded-xl border bg-card p-3 text-left transition-colors duration-150 hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-60 motion-reduce:transition-none",
                on && "border-primary ring-2 ring-primary/30",
              )}
            >
              <AppIcon
                name={t.name}
                icon={on && view ? view.body.icon : looks[t.id]?.icon}
                color={on && view ? view.body.color : looks[t.id]?.color}
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
          {view.body.summary || view.body.description ? (
            <div className="space-y-1 text-sm">
              {view.body.summary ? <p className="font-medium">{view.body.summary}</p> : null}
              {view.body.description ? <p className="whitespace-pre-line text-muted-foreground">{view.body.description}</p> : null}
            </div>
          ) : null}

          <FormField
            id="instance-name"
            label="App name"
            error={nameError}
            hint={nameTaken ? "Another app on this server already has this name." : undefined}
            className="sm:max-w-sm"
          >
            <Input
              id="instance-name"
              value={chosenName}
              placeholder={view.name}
              {...invalidAttrs("instance-name", nameError)}
              onChange={(e) => setName(e.target.value)}
            />
          </FormField>

          <section aria-label="Values" className="space-y-4">
            {required.length > 0 ? (
              <VariableFields vars={required} values={values} onChange={setValue} showRequired={tried} />
            ) : optional.length === 0 ? (
              <p className="text-sm text-muted-foreground">This template needs no values.</p>
            ) : null}
            {optional.length > 0 ? (
              <Disclosure open={moreOpen} onOpenChange={setMoreOpen}>
                <p className="text-xs text-muted-foreground">Leave a field empty to keep the compose file's value.</p>
                <VariableFields vars={optional} values={values} onChange={setValue} showRequired={tried} />
              </Disclosure>
            ) : null}
            <p className="text-xs text-muted-foreground">Secret values are encrypted in your browser for this server.</p>
          </section>

          {targets.length > 0 ? (
            <section aria-labelledby="hosts-title" className="space-y-3">
              <h2 id="hosts-title" className="text-[15px] font-semibold">
                Hostnames
              </h2>
              <p className="text-xs text-muted-foreground">Optional: a hostname for each web port the app exposes.</p>
              {targets.map((t) => (
                <HostnameField
                  key={t.id}
                  rowId={`host-${t.container}-${t.port}`}
                  value={hosts[t.target] ?? ""}
                  label={`Hostname for ${t.target}`}
                  placeholder="app.example.com"
                  rowClassName="flex flex-wrap items-center gap-2 sm:flex-nowrap"
                  inputClassName="basis-full sm:basis-auto"
                  checked
                  serverId={serverId}
                  registrations={registrations}
                  error={tried ? hostErrors[t.target] : undefined}
                  onChange={(v) => setHosts((prev) => ({ ...prev, [t.target]: v }))}
                  onRegistration={() => {}}
                  onReturn={refreshDomains}
                >
                  <span className="shrink-0 font-mono text-xs text-muted-foreground">→ {t.target}</span>
                </HostnameField>
              ))}
            </section>
          ) : null}

          {declaredBackups.length > 0 ? (
            <section aria-labelledby="backups-title" className="space-y-3">
              <h2 id="backups-title" className="text-[15px] font-semibold">
                Backups
              </h2>
              {declaredBackups.map((b) => {
                const c = backups[b.name] ?? { enabled: false, storage: storages?.[0] ?? "" };
                const id = `storage-${b.name}`;
                const message = tried ? backupErrors[b.name] : undefined;
                return (
                  <div key={b.name} className="grid gap-1.5">
                    <div className="flex flex-wrap items-center gap-3">
                      <Switch
                        aria-label={`Back up ${b.name}`}
                        checked={c.enabled}
                        onCheckedChange={(enabled) => setBackup(b.name, { enabled })}
                      />
                      <Label htmlFor={id}>
                        Keep backups of <code className="font-mono">{b.name}</code> in
                      </Label>
                      <NativeSelect
                        id={id}
                        aria-label={`Storage for ${b.name}`}
                        value={c.storage ?? ""}
                        disabled={!c.enabled}
                        {...invalidAttrs(id, message)}
                        onChange={(e) => setBackup(b.name, { storage: e.target.value })}
                      >
                        {(storages ?? []).length === 0 ? <option value="">No storage</option> : null}
                        {(storages ?? []).map((s) => (
                          <option key={s} value={s}>
                            {s}
                          </option>
                        ))}
                      </NativeSelect>
                    </div>
                    <FieldMessage id={id} message={message} />
                    {storages !== null && storages.length === 0 ? (
                      <p className="text-xs text-muted-foreground">
                        This server has no storage yet.{" "}
                        <Link to={serverSettingsPath(serverId, "storages")} className="underline underline-offset-4">
                          Add one
                        </Link>{" "}
                        to keep backups.
                      </p>
                    ) : null}
                  </div>
                );
              })}
            </section>
          ) : null}

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

          <CreateBar
            error={error}
            busy={busy}
            waiting={storages === null}
            onCreate={() => void create()}
            cancelTo={appsPath(serverId)}
            invalid={invalid}
          />
        </>
      )}
    </div>
  );
}
````

Modify `web/src/hooks/use-template-count.ts` (`git apply` this diff):

````diff
diff --git a/web/src/hooks/use-template-count.ts b/web/src/hooks/use-template-count.ts
--- a/web/src/hooks/use-template-count.ts
+++ b/web/src/hooks/use-template-count.ts
@@ -1,8 +1,10 @@
 import { useEffect, useRef, useState } from "react";
 
 import { useTemplatesApi } from "@/hooks/use-templates-api";
+import { orgTemplates } from "@/lib/templates";
 
-// useTemplateCount is how many templates the organization has, read once
+// useTemplateCount is how many templates of its own the organization has
+// (what New app → From a template offers), read once
 // (the first time enabled is true) and kept for as long as the caller lives,
 // so reopening a dialog doesn't ask again. null while unknown; a failed read
 // counts as none.
@@ -14,7 +16,7 @@ export function useTemplateCount(enabled = true): number | null {
     if (!enabled || started.current) return;
     started.current = true;
     api.listTemplates().then(
-      (list) => setCount(list.length),
+      (list) => setCount(orgTemplates(list).length),
       () => setCount(0),
     );
   }, [enabled, api]);
````

- [ ] **Step 4: Run them and see them pass**

```bash
CI=true pnpm --dir web exec vitest run src/components/new-app/template-flow.test.tsx src/components/new-app/new-app-choices.test.tsx
pnpm --dir web exec tsc -b
pnpm --dir web run lint
CI=true pnpm --dir web run test
```

Expected: PASS — the type check is green again and the whole suite passes.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat(web): install a template with its declared variables, hostnames and backups" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 11: Docs and final verification, with a standalone smoke

Describe the new model in CLAUDE.md, then prove the whole branch: every suite, the coverage gates, and a real install and upgrade on a standalone instance with Docker.

**Files:**
- Modify: `CLAUDE.md` (the "Templates and instances" paragraph and the `template_upgrade` part of "Rollouts")
- Modify (only if the ratchet asks): `coverage-baseline.json`

**Interfaces:**
- Consumes: everything above.
- Produces: nothing new for code.

- [ ] **Step 1: Update CLAUDE.md**

Modify `CLAUDE.md` (`git apply` this diff):

````diff
diff --git a/CLAUDE.md b/CLAUDE.md
--- a/CLAUDE.md
+++ b/CLAUDE.md
@@ -55,9 +55,9 @@ This is the **v2 monorepo** that merges the v1 `winterflow-agent` and `winterflo
 **Backups:** a compose file declares what can be backed up under `x-winterflow.backups` (the recipe: `sqlite`, `files`, `custom`; parsed and validated by `internal/domain/backup/recipe`, shared by the API's save-app 400s and the agent); per-deployment settings (on/off, storage, schedule, paths, UI-added backups) are overrides in the committed `.winterflow/config.json` → `backups`, replaced as a whole by `backup.update` and kept across app-editor saves (`keepBackups`). `${WF_BACKUP_<NAME>_<FIELD>}` resolves to the backup's storage (PREFIX narrowed to `<app-id>/<backup>/`, empty while off); an enabled `custom` backup activates the compose profile `wf-backup-<name>` (passed with the user's `COMPOSE_PROFILES` as `--profile`; a disabled one's service is removed with `rm -fs`, since `up --remove-orphans` leaves it). The agent runs `sqlite`/`files` backups itself: the scheduler (`internal/app/agent/backup`, one goroutine, one timer, one run at a time, `{AGENT_DATA_DIR}/backups/state.json`) calls `RunBackup`, which mounts the app's own volumes/binds (resolved from one cached `docker compose config`) into a capped `alpine:3.20` helper running this binary's hidden `backup dump` subcommand (the binary must be static, `CGO_ENABLED=0` as `make build`, `scripts/install.sh` and goreleaser build it: alpine has no glibc loader, and the agent refuses to run a backup from a dynamic ELF; `internal/app/backup/cli`, exit 3 unchanged / 4 missing), zstd-compresses its stdout to the storage with a manifest written last (`internal/infra/backup/layout`), then prunes by tiers under `BACKUP_RETENTION_DAYS`. `backup.run` is a detached command: the in-process bridge runs it off its single goroutine, and the API awaits its result for 2h (`DispatchInput.Wait`; the agent bounds the wait the same, and a stopped scheduler fails it at once). Health rides on `apps.status` (`AppStatus.Backups`). `backup.restore` is detached too and runs on the same scheduler (never beside a backup): `RestoreBackup` downloads the point to `backups/tmp` and checks its sha256, stops a running app, saves the current data as a `pre-restore` point (a run; its failure aborts and starts the app again), then a helper with `CHOWN`/`FOWNER` runs `backup restore` — sqlite replaces the file (quick_check, owner and mode kept; the old `-wal`/`-shm`/`-journal` are set aside as `*.restore-old`), files extracts into `.wf-restore-<id>/` at each mount root (free space checked, `..`/absolute/outside/through-symlink entries refused) and swaps only the backup's paths. Only a clean helper failure (exit 1, docker's 125-127), or a files helper stopped from outside before its `{"phase":"swapping"}` stderr line (only when its container is confirmed removed, the docker CLI ended by itself with its output read in full, and no `.wf-restore-<id>.old` is on a bind mount), leaves the data as it was and starts the app again (unless the scheduler's `Forget` cancelled it with `ErrAppDeleted`); any other end (a crash, a kill, exit 5, a swap that failed, an agent stop) leaves the app `compose down` (never `-v`) and the error names the pre-restore point, for sqlite too. A manifest records the copy's `entries`; a copy of a location the backup no longer has is refused. The restore's time is checked against the copy's raw size before the app is stopped and again after the pre-restore copy. `backups/restoring.json` records restores in progress: after an agent stop (it is kept past a restore a plain cancel left in doubt, and that restore's error is also stored as `last_error`), the next start turns it into the backup's `last_error`, and `agent.update` is refused while a restore runs. A restore (or run) that outlives its deadline gets `RestoreStopBudget` (derived from the engine's own cleanup bounds) before the scheduler abandons it, and its final outcome is logged and kept as `last_error`. The scheduler nudges the status reporter (`Changed()`) when a run ends or the plan is reread, so health reaches browsers at once. A per-app file lock (`lockApp`, reference counted, held for file work and one commit only) makes `backup.update`'s base check and commit one step, shared with save, rename, rollback and the source poll's commit; clone/fetch of a git-sourced app's upstream runs under a lock of its own (`lockSource`), before a save takes the app lock. Deleting an app marks its backup folders `<app-id>/.deleted` (or purges them with `delete_backups`); the scheduler's daily sweep applies `BACKUP_RETENTION_DAYS` to marked folders only, and `storage.orphans`/`delete-orphan` list and delete the folders of apps not on this server.
  Two per-app locks exist: the **operation lock** (`applock.go`: `lockForMutation`, held for a public mutator's whole run — save, start/stop/restart/update, delete, rollback, rename, `backup.update`, storage redeploys, the source poll — giving up after 30s with `ErrAppBusy` so the standalone bridge never wedges; the reload worker waits ctx-bounded) and the file lock above. Order: operation lock → `beginOp`/`beginRestore` → `lockApp` → `lockSource` → `sharedMu` (a save or source poll releases `lockSource` before taking `lockApp`; only a rollback holds both). A restore never takes the operation lock; `beginRestore`/`beginOp` exclude it from the mutators and reloads.
 
-**Templates and instances (API only; the agent never sees a template):** a template is an org's stack definition with immutable revisions (`templates`, `template_revisions`; body = files, parameters, app name/icon/color, ingress rows, backup defaults, validated by `internal/domain/template`: ≤1 MiB, RE2 patterns ≤512 B, no default on a secret parameter, undeclared `${VAR}`s in files only warn). An instance (`template_instances`) is an ordinary app rendered from a revision and the caller's values by `template.Render` into the `app.save` payload the editor would send (files unchanged, one variable per parameter, secrets as the caller's ECIES ciphertext or the `<encrypted>` keep placeholder, config blob with the rendered ingress, `template_id` and the `instance` marker) and dispatched through the app usecase's `CheckSave` + `DispatchSave`, so every save-app check applies before anything is claimed; a secret must have the ECIES wire format's shape (`crypto.CheckCiphertext`) and a plain value (or default) may not contain `$` (compose interpolates `.env`). An ingress row's `port` is a number or `${PARAM}` of a plain parameter and its `service` is informational (the ingress proxies a hostname to `127.0.0.1:<port>` on the host, so each instance publishes its own host port, e.g. `127.0.0.1:${PORT}:8080`). The caller's `key` is unique per org among instances not unlinked (partial unique index); a create claims it only after the checks pass, a replay with the same request hash (server, template, requested revision, plain values, secret *names*, backup overrides) answers the created instance or re-dispatches a pending one, another request is 409, a deleted instance's key stays retired (409), unlink frees it. Updating or upgrading a pending instance renders like a create (every secret resent, the full backups list sent, a `backups` override accepted only while pending). Instance routes take `wait: true`: the handler holds only its own request on a buffered channel fed by the dispatch result observer (which records the outcome either way; an observed dispatch keeps its result for `appuc.ObservedResultTTL`, 15 minutes, beyond the 10-minute wait), at most 10 minutes, then answers 202; a failed command is 502 and stays as `last_error`. An upgrade sends `instance.backup_defaults` for backups new in the revision, which the agent's save (`applyBackupDefaults`) adds where the app has no entry and then drops. An instance's app is changed through its instance: save-app and rollback-app on an app a pending or created instance manages answer 409 (`port.InstanceLinks`, served by the templates repository and given to the app usecase; rename is allowed), and delete-app's success retires its instance's key like delete-instance. `POST /api/v1/template/upgrade-instances` ("Upgrade all") queues the template's `template_upgrade` rollout; it also starts instances the user had stopped (an upgrade is a save, which redeploys).
+**Templates and instances (API only; the agent never sees a template):** a template is an org's own (`templates.source` `org`, editable) or the catalog's (`catalog`: `org_id` NULL, a unique `slug`, `withdrawn_at` once the feed drops it; read-only to every org, so save-template and delete-template answer 403 `model.ErrCatalogReadOnly`; until the catalog sync lands only tests insert them, through `CreateTemplate`), with immutable revisions (`template_revisions`, a catalog one with its `digest`). Every read is scoped: an org reads its own templates and every catalog template (`get-templates` lists its own first; a withdrawn one only while the org has live instances of it, and it takes no new installs, `model.ErrTemplateWithdrawn`). A body is `files` plus `summary`, `description`, `icon`, `color`; what an instance fills in, which ports it exposes and which backups it can turn on come only from the compose files' `x-winterflow` block (`template.Parse`, `ModeTemplate`: a non-external volume with a literal `name:` is an error). `template.Validate` checks a save (a compose file, file paths, the metadata limits, the shared parser, the backup recipe; the body ≤1 MiB; undeclared `${VAR}`s in files only warn); `get-template` returns what the revision declares as `detected` (variables, expose entries with their targets, backups, warnings), and `POST /api/v1/template/inspect-template` reads a body the same way without storing it (the editor's Detected panel; a body a save would refuse answers 200 with its `problem`). An instance (`template_instances`) is an ordinary app rendered by `template.Render` into the `app.save` payload the editor would send (files unchanged; one app variable per given value, so a declared variable without one keeps the compose file's literal; secrets as the caller's ECIES ciphertext or the `<encrypted>` keep placeholder; a config blob with the app name, the instance's `hosts` as its ingress, `template_id` and the `instance` marker, and no `ports`, so the agent keeps the app's host ports) and dispatched through the app usecase's `CheckSave` + `DispatchSave`, so every save-app check applies before anything is claimed. `template.Resolve` checks the values (each a declared variable, plain or secret as declared, no `$` in a plain value, a secret in the ECIES wire format's shape, then `CheckValues`; a given `""` clears a stored value); `template.CheckHosts` the hostnames (`[{host, expose}]`, each on an http expose entry). The app name is the create's `name` (default: the template's), stored on the instance; later renders use the app's current name from the API's app list (a rename sticks). The caller's `key` is unique per org among instances not unlinked (partial unique index); a create claims it only after the checks pass, a replay with the same request hash (server, template, requested revision, name, values, secret *names*, hosts, backups) answers the created instance or re-dispatches a pending one, another request is 409, a deleted instance's key stays retired (409), unlink frees it. Updating or upgrading a pending instance renders like a create (every secret resent, the whole backups list sent, `backups` accepted only while pending); `hosts` on an update or upgrade replaces the stored ones. An upgrade re-renders with the stored values and hosts and sends no backups list (the agent keeps the app's settings; a backup the revision adds starts off); a newly required value missing, a stored value the revision refuses or a stored host whose expose entry the revision dropped is `templates.ErrNeedsValues` (a 400, skipped by "Upgrade all"). Instance routes take `wait: true`: the handler holds only its own request on a buffered channel fed by the dispatch result observer (which records the outcome either way; an observed dispatch keeps its result for `appuc.ObservedResultTTL`, 15 minutes, beyond the 10-minute wait), at most 10 minutes, then answers 202; a failed command is 502 and stays as `last_error`. An instance's app is changed through its instance: save-app and rollback-app on an app a pending or created instance manages answer 409 (`port.InstanceLinks`, served by the templates repository and given to the app usecase; rename is allowed), and delete-app's success retires its instance's key like delete-instance. `POST /api/v1/template/upgrade-instances` ("Upgrade all") queues the org's `template_upgrade` rollout of the template; it also starts instances the user had stopped (an upgrade is a save, which redeploys).
 
-**Rollouts:** the queue of one-app-at-a-time jobs (`rollouts`, `internal/domain/usecase/rollouts`; routes `/api/v1/template/upgrade-instances`, `/api/v1/rollout/get-rollouts|stop-rollout`; an SSE `rollout` event, a `model.RolloutView`, to the org on every change and only then; it is published by the worker's process only, so with several API replicas the step events reach browsers on the worker's replica, and pages on the others catch up through `useRollout`'s re-read of `get-rollouts` every 15 s while the shown rollout is active and the page visible). Kinds: `template_upgrade` (scope key the template id; the oldest `created` instance below the target revision is re-rendered with its stored values through the instance update path; one whose values the revision refuses, `templates.ErrNeedsValues`, or that went away is skipped; one whose save went through but was not recorded, still below the target, counts as done and joins `skip_ids`; deleting a template deletes its upgrade rollouts in the same transaction) and `vars_reload` (scope key the org id; scope `model.ReloadScope`: names × server_ids, all_stale servers, app_ids, plus each server's `revisions` and `settle_by`; the next app is a running app whose `stale_vars` in the API's status cache the scope wants, on a server whose latest report reached the scope's revision; until `settle_by`, 5 minutes, it waits for a server that has not, then leaves it out; a step sends the single-app `vars.reload`; an app deleted while its reload was queued or in flight, gone from the API's catalog, is skipped). One active rollout per (kind, scope_key) (partial unique index): a second upgrade of a template is 409, a reload extends the active one (scope merged in Go, written by one conditional UPDATE on `scope_rev`; the worker completes a rollout only at the `scope_rev` it evaluated); extending a reload whose stop was requested is refused with 409 "a reload is stopping". One worker goroutine per API process, started by `bootstrap.StartRollouts` once commands can flow (standalone: after the in-process bridge subscribed; API: after the response subscriber `WireCore` starts), not inside `WireCore` (`Deps.Rollouts` is the one value whose `Wake` reaches it): a pass on every wake and once a minute (one indexed query when idle); a 2-minute lease claimed in one statement and renewed every 30 s, every worker write naming its owner (a lost lease cancels the step in flight; another process resumes after expiry; lease comparisons are `<=`); a step picks the next app fresh, records `current_app_id`, dispatches and awaits the result (10.5 minutes), then waits (5 minutes) for the app to read healthy on two status reports received after it whose receive times are at least `healthSettle` (20 s) apart (one is not enough: a crash-looping container reads running between restarts) and counts it, skips it (`skip_ids`, never picked again) or fails the rollout naming the app. Healthy means at least one Active container, none Restarting or Problematic, the rest Active or Stopped (a finished one-shot); a report showing a Restarting or Problematic container fails the step at once, naming the container, and any other unhealthy report starts the count again; at the bound no containers, no report, a single healthy report, or an app reading Stopped or unknown fails the step (an app reading Idle for other reasons, such as a paused container beside running ones, passes); a Docker healthcheck's `unhealthy` is not seen: such a container reads running. A `vars_reload` that has no app yet but waits for a server to report (`settle_by` not reached) parks itself (no owner, `lease_until` now + 5 s) and yields the worker, so one quiet server holds neither other rollouts nor the claimable list; `Run` looks again every 5 s while one waits. `Stop` ends a rollout nobody drives (queued, parked, or an expired lease) at once; otherwise `stop_requested` stops it after the step in flight. No transaction is held while a step waits. Ended rollouts beyond the newest 20 per scope are pruned when one ends. The status cache keeps each report's receive time and `vars_revision` (`status.Cache.LatestReport`).
+**Rollouts:** the queue of one-app-at-a-time jobs (`rollouts`, `internal/domain/usecase/rollouts`; routes `/api/v1/template/upgrade-instances`, `/api/v1/rollout/get-rollouts|stop-rollout`; an SSE `rollout` event, a `model.RolloutView`, to the org on every change and only then; it is published by the worker's process only, so with several API replicas the step events reach browsers on the worker's replica, and pages on the others catch up through `useRollout`'s re-read of `get-rollouts` every 15 s while the shown rollout is active and the page visible). Kinds: `template_upgrade` (scope key `<org_id>:<template_id>`, `model.UpgradeScopeKey`, so two orgs upgrading one catalog template don't block each other; the API names the scope by the template id in `get-rollouts`' `scope_key` and the view; the org's oldest `created` instance below the target revision is re-rendered with its stored values through the instance update path; one whose values the revision refuses, `templates.ErrNeedsValues`, or that went away is skipped; one whose save went through but was not recorded, still below the target, counts as done and joins `skip_ids`; deleting a template deletes the org's upgrade rollouts of it in the same transaction) and `vars_reload` (scope key the org id; scope `model.ReloadScope`: names × server_ids, all_stale servers, app_ids, plus each server's `revisions` and `settle_by`; the next app is a running app whose `stale_vars` in the API's status cache the scope wants, on a server whose latest report reached the scope's revision; until `settle_by`, 5 minutes, it waits for a server that has not, then leaves it out; a step sends the single-app `vars.reload`; an app deleted while its reload was queued or in flight, gone from the API's catalog, is skipped). One active rollout per (kind, scope_key) (partial unique index): a second upgrade of a template is 409, a reload extends the active one (scope merged in Go, written by one conditional UPDATE on `scope_rev`; the worker completes a rollout only at the `scope_rev` it evaluated); extending a reload whose stop was requested is refused with 409 "a reload is stopping". One worker goroutine per API process, started by `bootstrap.StartRollouts` once commands can flow (standalone: after the in-process bridge subscribed; API: after the response subscriber `WireCore` starts), not inside `WireCore` (`Deps.Rollouts` is the one value whose `Wake` reaches it): a pass on every wake and once a minute (one indexed query when idle); a 2-minute lease claimed in one statement and renewed every 30 s, every worker write naming its owner (a lost lease cancels the step in flight; another process resumes after expiry; lease comparisons are `<=`); a step picks the next app fresh, records `current_app_id`, dispatches and awaits the result (10.5 minutes), then waits (5 minutes) for the app to read healthy on two status reports received after it whose receive times are at least `healthSettle` (20 s) apart (one is not enough: a crash-looping container reads running between restarts) and counts it, skips it (`skip_ids`, never picked again) or fails the rollout naming the app. Healthy means at least one Active container, none Restarting or Problematic, the rest Active or Stopped (a finished one-shot); a report showing a Restarting or Problematic container fails the step at once, naming the container, and any other unhealthy report starts the count again; at the bound no containers, no report, a single healthy report, or an app reading Stopped or unknown fails the step (an app reading Idle for other reasons, such as a paused container beside running ones, passes); a Docker healthcheck's `unhealthy` is not seen: such a container reads running. A `vars_reload` that has no app yet but waits for a server to report (`settle_by` not reached) parks itself (no owner, `lease_until` now + 5 s) and yields the worker, so one quiet server holds neither other rollouts nor the claimable list; `Run` looks again every 5 s while one waits. `Stop` ends a rollout nobody drives (queued, parked, or an expired lease) at once; otherwise `stop_requested` stops it after the step in flight. No transaction is held while a step waits. Ended rollouts beyond the newest 20 per scope are pruned when one ends. The status cache keeps each report's receive time and `vars_revision` (`status.Cache.LatestReport`).
 
 **App secrets are ECIES-encrypted end-to-end** (`pkg/crypto`, P-256 ECDH→SHA-256→AES-256-GCM): the browser encrypts a secret with the agent's public key (served by `GET /api/v1/server/get-public-key`, sourced from the `public_key` capability or the local agent cert in standalone); the agent decrypts with its mTLS private key before render. Editing sends the `<encrypted>` placeholder for unchanged secrets (the agent preserves the stored value, never returns plaintext). Registry passwords use the same scheme.
 
````

- [ ] **Step 2: Run every suite**

From the repository root:

```bash
gofmt -l ./cmd ./internal ./pkg ./tools          # expect no output
go vet ./...
make sqlc-check                                   # expect no diff
make openapi && git diff --exit-code api/openapi.yaml
WF_CONTRACT_GUARD=require go test ./internal/app/web/contract/
pnpm --dir web exec tsc -b
pnpm --dir web run lint
CI=true pnpm --dir web run build && touch web/dist/.gitkeep && git status --short web/dist   # expect nothing to commit
```

Expected: every command succeeds with no output beyond the tools' own.

- [ ] **Step 3: The coverage gates on CI's merged profile** (Docker for the Postgres half)

```bash
make test && make test-pg TEST_PG_ARGS='-race -covermode=atomic -coverpkg=./... -coverprofile=contract-postgres.out' \
  && tail -n +2 contract-postgres.out >> coverage.out && make web-test
go run ./tools/diffcov --base origin/main --min 80 --go coverage.out --lcov web/coverage/lcov.info
go run ./tools/diffcov ratchet --go coverage.out --lcov web/coverage/lcov.info
```

Expected: `make test` and `make test-pg` pass (`TestParity` matches on both databases, `TestTemplatesXWinterflowMigration` runs on Postgres too); the diff gate prints `go ... PASS` and `web ... PASS` (about 94% and 99%); the ratchet prints `PASS` for both. If the ratchet prints `::notice::raise coverage-baseline.json <lang> to <N>`, set that language's value in `coverage-baseline.json` to `<N>` (one decimal) and keep the change for the commit below.

- [ ] **Step 4: Standalone smoke** (Docker and the `nginx:alpine` image; `jq` and `openssl` on the PATH; it uses ports 18977 and 28600-28650 on 127.0.0.1)

Save this script as `/tmp/wf-template-smoke.sh` (not in the repository). It builds nothing: run it after `make build`. It starts the standalone binary on a scratch data directory, claims it, and through the API inspects a template body, saves an org template whose compose file declares a required `GREETING` (with a pattern), an optional `SUFFIX` and an http expose entry, creates an instance with a value and an app name, reads the page on the allocated host port, saves version 2 (which changes `SUFFIX`'s compose value), upgrades the instance and reads the page again, then deletes the instance with its volumes.

````bash
#!/usr/bin/env bash
# Standalone smoke for templates on x-winterflow: an org template from a
# compose file with an x-winterflow block, installed and upgraded through
# the API. Needs Docker and the nginx:alpine image. Run from the repo root
# after `make build`; SMOKE is a scratch directory.
set -euo pipefail
SMOKE=${SMOKE:-/tmp/wf-sp2-smoke}
PORT=${PORT:-18977}
BASE=http://127.0.0.1:$PORT
if curl -fs "$BASE/_/health" >/dev/null; then echo "port $PORT is in use" >&2; exit 1; fi
rm -rf "$SMOKE" && mkdir -p "$SMOKE"
cat > "$SMOKE/.env" <<ENV
API_PORT=$PORT
WEB_URL=$BASE
JWT_SECRET=$(openssl rand -hex 32)
REGION=smoke
DATABASE_URL=sqlite://$SMOKE/wf.sqlite
AVATARS_STORAGE_PATH=$SMOKE/avatars
AGENT_DATA_DIR=$SMOKE/data
EXPOSE_PORT_RANGE=28600-28650
METRICS_ENABLED=false
HUB_CERT_DIR=$SMOKE/hub-certs
HUB_CERT_EXT_PATH=$SMOKE/ext.cnf
HUB_CA_SUBJECT="/C=CA/O=WinterFlow.io/OU=CA/CN=WinterFlow.io CA/emailAddress=info@winterflow.io"
HUB_SERVER_SUBJECT="/C=CA/O=WinterFlow.io/OU=SERVER/CN=WinterFlow.io/emailAddress=info@winterflow.io"
ENV
BIN=$(pwd)/bin/standalone
"$BIN" serve -env-file "$SMOKE/.env" > "$SMOKE/standalone.log" 2>&1 &
echo $! > "$SMOKE/pid"
stop() { kill "$(cat "$SMOKE/pid")" 2>/dev/null || true; while curl -fs "$BASE/_/health" >/dev/null; do sleep 0.5; done; }
trap stop EXIT
for _ in $(seq 1 60); do curl -fs "$BASE/_/health" >/dev/null && break; sleep 0.5; done

J="$SMOKE/cookies"
curl -fs -c "$J" -b "$J" -H 'Content-Type: application/json' -d '{"name":"Owner","email":"owner@example.test","password":"correct horse battery"}' "$BASE/api/v1/auth/register" >/dev/null
curl -fs -c "$J" -b "$J" -H 'Content-Type: application/json' -d '{"user":"owner@example.test","passwd":"correct horse battery"}' "$BASE/auth/local/login" >/dev/null
TOKEN=$(curl -fs -c "$J" -b "$J" -H 'Content-Type: application/json' -d '{"name":"smoke"}' "$BASE/api/v1/user/create-token" | jq -r .data.token)
api() { curl -fsS -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' "$@"; }
for _ in $(seq 1 60); do SID=$(api "$BASE/api/v1/server/get-servers" | jq -r '.data.servers[0].id // empty'); [ -n "$SID" ] && break; sleep 0.5; done

body() { # $1: SUFFIX's value in the compose file (what the page says after the greeting)
  jq -n --arg suffix "$1" '{
    files: {
      "compose.yml": ("services:\n  web:\n    image: nginx:alpine\n    environment:\n      # The greeting the page answers with\n      GREETING: \"hello\"\n      SUFFIX: \"" + $suffix + "\"\n    volumes:\n      - ./default.conf.template:/etc/nginx/templates/default.conf.template:ro\nx-winterflow:\n  expose:\n    - { container: web, port: 80 }\n  variables:\n    GREETING: { required: true, pattern: \"^[a-z ]+$\" }\n    SUFFIX: {}\n"),
      "default.conf.template": "server {\n  listen 80;\n  location / { default_type text/plain; return 200 \"${GREETING}${SUFFIX}\\n\"; }\n}\n"
    },
    summary: "A greeting page", description: "", icon: "", color: "#2563eb"
  }'
}
echo "== inspect-template"
api -d "{\"body\": $(body '!')}" "$BASE/api/v1/template/inspect-template" | jq -c '.data | {problem, variables: [.detected.variables[].name], expose: [.detected.expose[].id]}'
echo "== save-template (version 1)"
TPL=$(api -d "{\"name\":\"greeter\",\"body\": $(body '!')}" "$BASE/api/v1/template/save-template" | jq -r .data.id)
echo "== create-instance (wait)"
api -d "{\"server_id\":\"$SID\",\"key\":\"smoke-1\",\"template_id\":\"$TPL\",\"name\":\"Greeter\",\"values\":{\"GREETING\":\"good morning\"},\"wait\":true}" \
  "$BASE/api/v1/instance/create-instance" | jq -c '.data.instance | {state, revision, name, values}'
APP=$(api "$BASE/api/v1/instance/get-instance?server_id=$SID&key=smoke-1" | jq -r .data.app_id)
HOSTPORT=$(docker ps --filter "label=com.docker.compose.project=wf-$APP" --format '{{.Ports}}' | sed -E 's/.*127\.0\.0\.1:([0-9]+)->80.*/\1/')
page() { for _ in $(seq 1 20); do curl -fs "http://127.0.0.1:$HOSTPORT/" && return; sleep 0.5; done; echo "no answer on $HOSTPORT" >&2; return 1; }
echo "== the app answers on 127.0.0.1:$HOSTPORT"
page
echo "== save-template (version 2), upgrade-instance (wait)"
api -d "{\"id\":\"$TPL\",\"name\":\"greeter\",\"base_revision\":1,\"body\": $(body ', from version 2')}" "$BASE/api/v1/template/save-template" | jq -c '.data | {revision, warnings}'
api -d "{\"server_id\":\"$SID\",\"key\":\"smoke-1\",\"revision\":2,\"wait\":true}" "$BASE/api/v1/instance/upgrade-instance" | jq -c '.data.instance | {state, revision, values}'
docker ps --filter "label=com.docker.compose.project=wf-$APP" --format '{{.Ports}}'
page
echo "== delete-instance (wait)"
api -d "{\"server_id\":\"$SID\",\"key\":\"smoke-1\",\"delete_volumes\":true,\"wait\":true}" "$BASE/api/v1/instance/delete-instance" | jq -c '.data.instance.state'
````

Run:

```bash
make build
docker image inspect nginx:alpine >/dev/null 2>&1 || docker pull nginx:alpine
bash /tmp/wf-template-smoke.sh
```

Expected output (the host port may differ inside 28600-28650):

```text
== inspect-template
{"problem":"","variables":["GREETING","SUFFIX"],"expose":["web:80/http"]}
== save-template (version 1)
== create-instance (wait)
{"state":"created","revision":1,"name":"Greeter","values":{"GREETING":"good morning"}}
== the app answers on 127.0.0.1:28600
good morning!
== save-template (version 2), upgrade-instance (wait)
{"revision":2,"warnings":[]}
{"state":"created","revision":2,"values":{"GREETING":"good morning"}}
127.0.0.1:28600->80/tcp
good morning, from version 2
== delete-instance (wait)
"deleted"
```

Afterwards `pgrep -f "standalone serve"` prints nothing and `docker ps -a --format '{{.Names}}' | grep '^wf-'` lists no container of the smoke (the delete removed it). Then, with the binary started the same way (or `make standalone`), open the web UI once: Templates -> New template shows the example compose file with a filled Detected panel (WHOAMI_NAME, `app:80 http takes hostnames`); save it as `whoami`, then New app -> From a template shows it with the App name `whoami`.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "docs: templates use x-winterflow" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
