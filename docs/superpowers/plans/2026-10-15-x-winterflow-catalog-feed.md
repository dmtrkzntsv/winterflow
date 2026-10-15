# x-winterflow Sub-project 3: the Catalog Feed Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Publish the app catalog as a signed JSON feed that `cmd/catalog` builds from a winterflow-apps checkout, and have every API process sync it into read-only catalog templates (a new template per new app, a revision only when an app's files change, metadata updated in place, withdrawals for dropped apps), safely across replicas and stale mirrors; first fix the YAML alias and merge-key gap the real catalog exposes.

**Architecture:** `recipe.DecodeFiles` expands YAML aliases and merge keys once (capped at 100 000 nodes per file), so the x-winterflow parser, the backup recipe and template validation all see merged services. A pure domain package `internal/domain/catalog` holds the feed format, the files digest, ed25519 signing and `Entry.Check`, shared by the builder (`internal/app/catalog`, `cmd/catalog`) and the sync. Migration `0017_catalog_meta` adds `templates.catalog` (the metadata JSON) and `templates.catalog_feed_at`; the templates repository implements `port.CatalogRepository` with writes that are conditional on the base the sync read and on the feed's `generated_at`. `internal/infra/catalogfeed` fetches with `If-None-Match`, deadlines and size caps; `internal/domain/usecase/catalog.Syncer` verifies, applies, withdraws and stamps; `bootstrap.StartCatalog` runs it on its own goroutine in api and standalone. `get-templates` and `get-template` gain `catalog`.

**Tech Stack:** Go 1.27 (`crypto/ed25519`, `crypto/sha256`, `net/http`, `go.yaml.in/yaml/v3`), sqlc v1.31.1 over `database/sql` (SQLite `modernc.org/sqlite`, Postgres `pgx/v5`), chi route tables and the OpenAPI generator, TypeScript types in `web/` (no UI).

**Spec:** docs/superpowers/specs/2026-10-14-x-winterflow-catalog-design.md (sections "Sub-project 3: the catalog feed", "Configuration", "Invariants kept", "Testing"), with the controller's design brief `.superpowers/scratch/sp3-design-brief.md`; where this plan settles something the brief left open or had to change, "Spec decisions this plan makes" says so.

**Base:** this branch (`feat/x-winterflow-catalog`, at `9ac4a50`) is one commit behind `origin/main` (`df34c7d`, #28 the Processes tab). Run `git merge --ff-only origin/main` before Task 1: the diffs below are made against `df34c7d` (#28 touches `CLAUDE.md`, `api/openapi.yaml` and `bootstrap/standalone.go`, none of the hunks below). Every snippet was applied, task by task, to a copy of the tree and its tests run there: on `9ac4a50` the whole set (`go vet ./...`, `WF_CONTRACT_GUARD=require make test`, `make test-pg` with coverage, `make sqlc-check`, web `tsc -b`, ESLint and all 1688 Vitest tests, the merged SQLite+Postgres diff gate at 92.9% of 756 changed Go lines with no executable web lines, the ratchet at Go 87.4 against 86.8, and the standalone smoke of Task 10 on Docker); rebased on `df34c7d`, the diffs applied without conflicts and `make openapi` (no change), `go vet ./...` and `WF_CONTRACT_GUARD=require go test ./...` passed. New files are given whole; edits are unified diffs (save the block to a file and `git apply` it, or make the same edits by hand). Generated files (`internal/infra/db/sqlc/gen/**`, `api/openapi.yaml`, `internal/app/web/contract/testdata/parity.golden.json`) are regenerated with the commands given, never edited.

## Global Constraints

- Feed `format` is `1`; the JSON names are exactly the spec's: `format`, `source_commit`, `generated_at`, `apps[]` of `slug`, `name`, `summary`, `description`, `keywords`, `tags`, `alternative_to`, `license`, `homepage_url`, `repository_url`, `documentation_url`, `logo_url`, `files`, `digest`.
- `digest` is `"sha256:"` + the hex SHA-256 over the files in sorted name order, each written `<len(name)>:<name><len(content)>:<content>`.
- Keys are ed25519 in standard base64: the public key 32 bytes, the signing key the 32-byte seed (the 64-byte key also reads). `catalog.json.sig` is the standard-base64 signature over the exact `catalog.json` bytes, followed by `"\n"`.
- The compiled-in default `CATALOG_PUBLIC_KEY` is `QAyhWPQDK0IS8UvVtNA3X/vXwSisba2Lx/7bxI3CG0E=`; the private seed never enters the repository (tests generate their own keys).
- Env vars, read by api and standalone and documented in `.env.example`: `CATALOG_URL` (default `https://winterflow.io/catalog/catalog.json`; set but empty disables the sync and hides the catalog), `CATALOG_PUBLIC_KEY` (default above), `CATALOG_REFRESH` (default `24h`, minimum `5m`, an invalid value is the default, never a crash).
- The sync runs a minute after start, then every `CATALOG_REFRESH`; one sync is bounded by 5 minutes, each request by 30 s; the feed is capped at 8 MiB and the `.sig` at 4 KiB; https only, http only to a loopback host (127.0.0.0/8, ::1, localhost), redirects included; `User-Agent: winterflow`. One goroutine per API process, never the standalone bridge's, started next to `StartRollouts` (not in `WireCore`, not in the contract harness).
- An entry passes `Entry.Check`: slug `^[a-z0-9][a-z0-9_-]{0,127}$`; name 1 to 255 bytes, one line, no surrounding spaces; `keywords`, `tags`, `alternative_to` at most 50 items of 1 to 100 bytes; `license` at most 100; each URL empty or https of at most 2048; the digest the files'; the body `{files, summary, description}` passes `template.Validate` and `template.Encode` (1 MiB).
- YAML expansion: at most `recipe.MaxExpandedNodes` = 100 000 nodes per expanded file; past it, an alias inside its own anchor, or a merge of something that is not a mapping, the file is not valid YAML (a nil tree, `Compose.Unparsed`), exactly like a decode failure.
- The migration is `0017_catalog_meta.sql`, identical in `internal/infra/db/migrations/sqlite` and `.../pgsql`, ASCII only; run `make sqlc` after any SQL edit and keep `make sqlc-check` green.
- Only a digest change makes a revision; name and metadata change in place; every catalog write clears `withdrawn_at`; losing a race is a skip, never an error.
- No UI (sub-project 4): the web only gains the optional `catalog` field on its `TemplateSummary`/`TemplateView` types. Never touch `web/src/components/ui/`.
- Coverage: at least 80% of changed lines per language on the merged SQLite + Postgres profile; `make openapi` whenever a response type changes.
- Commit messages: Conventional Commits ending with a blank line and `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` (the `-m ... -m ...` form below does that).

## How to run things

- One Go package: `go test ./internal/domain/catalog/`; one test: add `-run TestName`. The usecase sync tests take about 5 s, the contract suite about 10 s on SQLite.
- Contract suite on SQLite: `WF_CONTRACT_GUARD=require go test ./internal/app/web/contract/` (a partial `-run` needs the guard unset or it fails on purpose).
- Postgres: `make test-pg` (Docker; the contract suite and `internal/infra/db/...` on a throwaway `postgres:16`, a few minutes); a subset: `make test-pg TEST_PG_ARGS='-run Catalog -v'`.
- sqlc: `make sqlc` (about 5 s; needs a C compiler), `make sqlc-check`.
- OpenAPI and goldens: `make openapi`; `go test ./internal/app/web/contract/ -run TestParity -update` (review the diff).
- Web: `CI=true pnpm --dir web install --frozen-lockfile` once; `pnpm --dir web exec tsc -b`; `pnpm --dir web run lint`; `CI=true make web-test` (all Vitest tests with coverage, about 4 minutes).
- CI's coverage numbers: `WF_CONTRACT_GUARD=require make test && make test-pg TEST_PG_ARGS='-race -covermode=atomic -coverpkg=./... -coverprofile=contract-postgres.out' && tail -n +2 contract-postgres.out >> coverage.out && CI=true make web-test`, then `go run ./tools/diffcov --base origin/main --min 80 --go coverage.out --lcov web/coverage/lcov.info` and `go run ./tools/diffcov ratchet --go coverage.out --lcov web/coverage/lcov.info`.

## Spec decisions this plan makes

- **Field name `Template.CatalogMeta`, not `Catalog`:** `model.Template` already has the method `Catalog() bool` (sub-project 2), so the brief's `Catalog *CatalogMeta` field does not compile. The API views keep the brief's JSON name: `TemplateSummary.Catalog` / `TemplateView.Catalog`, `json:"catalog"`.
- **A persistent replay guard instead of the brief's in-memory one:** with several API replicas (or after a restart) an in-memory guard does not stop a stale mirror or CDN edge from rolling a template back (replica B, fetching an hour after A, gets yesterday's feed: B sees a different digest and adds a revision with the old files; the next day A flips it again, and instances flap between "up to date" and "upgrade available"). Migration 0017 therefore adds a second column, `templates.catalog_feed_at` (the `generated_at` of the newest feed applied to the template, NULL for org rows), every sync write is conditional on `catalog_feed_at IS NULL OR catalog_feed_at <= <this feed>`, and after a sync `StampCatalog` moves every catalog template's `catalog_feed_at` forward to the feed, so a later sync of an older feed is refused as a whole (`ErrStale`, also in a fresh process). Because a stamped future date would then refuse every real feed, a feed dated more than an hour ahead of the API's clock is refused (`ErrFuture`, `MaxClockSkew`). The brief's other replica rules hold as written: an insert that loses the slug race and a bump that loses the base race are skipped.
- **Port methods:** `port.CatalogRepository` has four: `ListCatalog`, `ApplyCatalog(ctx, base *CatalogState, e CatalogEntry) (CatalogOutcome, error)` (the brief's "apply one entry in one transaction"; the sync compares with `base` first and skips unchanged entries without a write), `WithdrawCatalog(ctx, id, at, feedAt)` (one template; the sync loops) and `StampCatalog`. The fetcher's port is `port.CatalogFetcher` with `model.CatalogFetch`. `bootstrap.Deps` gains `CatalogRepository` (the same `*template.Repo`).
- **`Decode` also refuses a slug listed twice** (two entries would make the sync write one template twice, the second winning), besides the brief's unknown format and zero apps; a missing `generated_at` is refused too (the replay guard needs it).
- **The `.sig` file** is the base64 signature plus a newline (reading the brief's "plus \"\\n\"" as the file's trailing newline; the signature covers the exact `catalog.json` bytes, as the spec says); `Verify` trims surrounding whitespace.
- **`--apps` is the winterflow-apps checkout root** (it holds `apps/` and `assets/`); the slug is the folder name, and a `metadata.json` whose `slug` differs is an error of that app. A logo is copied only when `metadata.logo` has a short extension (`^\.[a-z0-9]{1,5}$`) and names a regular file under `assets/` (the path is cleaned, so `../` cannot leave it); `logos/` is replaced as a whole on every build, and an unsigned build removes a `.sig` left by an earlier signed one. The "no logo" warning is reported only for apps that pass the check.
- **A bad `CATALOG_URL` or `CATALOG_PUBLIC_KEY`** (http to a non-loopback host, not a 32-byte key) is logged at start and leaves the sync off: the API keeps serving.
- **Withdrawing everything when `CATALOG_URL` is empty** passes a far-future `feedAt` (`9999-12-31`), so no stored feed time protects a template from it; setting the URL again restores the templates at the next sync (its feed is not older than the stamped one).
- **The web type** is `catalog?: CatalogMeta | null` (optional), so the existing fixtures need no change; the gallery that reads it is sub-project 4.

## Review Focus

1. **An alias bomb or alias cycle in an org template body or a catalog compose file** (a dozen lines that expand to ten million nodes, or `a: &a [*a]`): the API, which parses org users' bodies at save and inspect and the whole feed every day, must answer 400 "not valid YAML" quickly instead of exhausting memory. Pinned by `TestDecodeFilesRefusesWhatCantExpand` and `TestValidateRules/an_alias_bomb` (Task 1) and `TestCheckRefuses/an_alias_bomb` (Task 2).
2. **A feed whose `catalog.json` fetch succeeds but whose `.sig` 404s, 502s, answers 304, is oversized or does not verify:** the whole feed must be refused and nothing written, never applied as unsigned. Pinned by `TestFetchNeedsTheSignature` (Task 6), `TestSyncRefusesABadSignature` (Task 7) and `TestCatalogSyncRefusesUnsignedFeeds` (Task 9).
3. **A slug renamed or an app dropped in the catalog:** the old template must be withdrawn (hidden, its instances untouched), never deleted, the new slug becomes a new template, and an app listed again comes back on the same revision. Pinned by `TestSyncWithdrawsAndRestores` (Task 7), `TestWithdrawAndRestoreCatalog` (Task 4) and `TestCatalogSync` (Task 9: the dropped app leaves `get-templates` and comes back, while the installed one reads `behind` 1 after its new revision).
4. **Replicas syncing at once, or one of them behind a stale mirror or restarted:** each change must be written once (one revision, not two) and an older feed must never roll a template back. Pinned by `TestApplyCatalogRace` and `TestApplyCatalogSkipsStaleWrites` (Task 4), `TestSyncRace` and `TestSyncRefusesAnOlderOrFutureFeed` (Task 7), and `TestCatalogSyncReplicas` (Task 9, with overlapping transactions under `make test-pg`).
5. **A sync that fails halfway (a locked database, a cancelled context) followed by a 304:** the ETag must be kept only after a sync fully applied, so the next tick fetches the whole feed instead of a 304 hiding entries that were never written; likewise a feed dated in the future must not be stamped and block every later feed. Pinned by `TestSyncRetriesAFailedApply` and `TestSyncRefusesAnOlderOrFutureFeed` (Task 7).

---
### Task 1: YAML aliases and merge keys are expanded once, for every reader

Six catalog apps share service definitions through anchors (`x-app: &app {...}` then `web: { <<: *app }`, `environment: *app-env`). The x-winterflow parser walks the YAML node tree, where a merge key is a `<<` key and an alias is a pointer, so their declared variables read as unused and `Render` never sets them. `recipe.DecodeFiles` (the one place every compose file is decoded, for the API and the agent alike) now returns trees with every alias replaced by a copy of its anchor and every merge key expanded, capped so a few lines of nested aliases cannot exhaust the API's memory. Template warnings stop scanning non-compose files (`$uri` in an nginx.conf is the container's business).

**Files:**
- Create: `internal/domain/backup/recipe/expand.go`
- Modify: `internal/domain/backup/recipe/compose.go:44-55` (`DecodeFiles`)
- Modify: `internal/domain/template/validate.go:97-118` (`Warnings`)
- Test: `internal/domain/backup/recipe/expand_test.go` (create)
- Test: `internal/domain/compose/xwinterflow/xwinterflow_test.go` (append `TestMergedEnvironments`)
- Test: `internal/domain/compose/xwinterflow/testdata/render/merged.yml` (create: the golden file)
- Test: `internal/domain/template/validate_test.go`

**Interfaces:**
- Consumes: `go.yaml.in/yaml/v3` (`yaml.Node`, `AliasNode`, `Node.Alias`, merge keys tagged `!!merge`), `recipe.ParseComposeNodes`, `xwinterflow.Parse`, `template.Body.composeContents`.
- Produces:
  - `recipe.MaxExpandedNodes = 100_000` (exported: CLAUDE.md names it in Task 10).
  - `recipe.DecodeFiles(files ...[]byte) []*yaml.Node`: same signature; each tree is now expanded (no `AliasNode`, no `!!merge` key, no `Anchor`, no node shared between copies, comments kept); `nil` for a file that is not valid YAML, expands past `MaxExpandedNodes`, has an alias inside its own anchor, or merges something that is not a mapping or a list of mappings.
  - `template.Warnings(b Body, spec xwinterflow.Spec, shared func(string) bool) []string`: same signature; undeclared `${VAR}` references are collected from the compose files only.

- [ ] **Step 1: Write the failing tests**

Create `internal/domain/backup/recipe/expand_test.go`:

````go
package recipe

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

// plain is a node tree as Go values, read the way the x-winterflow parser
// walks it (an alias stays "*name", a merge key stays "<<"), so only an
// expanded tree reads like its expansion.
func plain(n *yaml.Node) any {
	switch n.Kind {
	case yaml.DocumentNode:
		return plain(n.Content[0])
	case yaml.MappingNode:
		m := map[string]any{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			m[n.Content[i].Value] = plain(n.Content[i+1])
		}
		return m
	case yaml.SequenceNode:
		l := []any{}
		for _, c := range n.Content {
			l = append(l, plain(c))
		}
		return l
	case yaml.AliasNode:
		return "*" + n.Value
	}
	return n.Value
}

func TestDecodeFilesExpandsAliasesAndMergeKeys(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"an alias is its anchor's copy": {
			src:  "a: &x {k: 1}\nb: *x\n",
			want: "a: {k: 1}\nb: {k: 1}\n",
		},
		"the mapping's own keys win over merged ones": {
			src:  "base: &base {image: a, restart: always}\nsvc:\n  image: b\n  <<: *base\n",
			want: "base: {image: a, restart: always}\nsvc: {image: b, restart: always}\n",
		},
		"an earlier mapping of a merge list wins": {
			src:  "one: &one {a: 1, b: 1}\ntwo: &two {b: 2, c: 2}\nsvc:\n  <<: [*one, *two]\n",
			want: "one: {a: 1, b: 1}\ntwo: {b: 2, c: 2}\nsvc: {a: 1, b: 1, c: 2}\n",
		},
		"merges nest": {
			src:  "a: &a {x: 1}\nb: &b {<<: *a, y: 2}\nc: {<<: *b, z: 3}\n",
			want: "a: {x: 1}\nb: {x: 1, y: 2}\nc: {x: 1, y: 2, z: 3}\n",
		},
		"an inline mapping merges too": {
			src:  "svc: {<<: {a: 1}, b: 2}\n",
			want: "svc: {a: 1, b: 2}\n",
		},
		"a quoted << is an ordinary key": {
			src:  "a: &a {x: 1}\nsvc: {\"<<\": 5}\n",
			want: "a: {x: 1}\nsvc: {\"<<\": 5}\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			var want yaml.Node
			if err := yaml.Unmarshal([]byte(tc.want), &want); err != nil {
				t.Fatal(err)
			}
			got := DecodeFiles([]byte(tc.src))[0]
			if got == nil {
				t.Fatal("refused")
			}
			if !reflect.DeepEqual(plain(got), plain(&want)) {
				t.Fatalf("got %#v\nwant %#v", plain(got), plain(&want))
			}
		})
	}
}

// The expanded tree shares no node between copies (a reader that edits one
// service's copy must not edit another's) and keeps the comments inside an
// anchor; no node keeps an anchor or an alias.
func TestDecodeFilesCopiesAnchors(t *testing.T) {
	src := "x-env: &env\n  # the greeting\n  GREETING: hi\nservices:\n  a: {environment: *env}\n  b:\n    environment:\n      <<: *env\n"
	doc := DecodeFiles([]byte(src))[0]
	if doc == nil {
		t.Fatal("refused")
	}
	svcs := doc.Content[0].Content[3]
	a, b := svcs.Content[1].Content[1], svcs.Content[3].Content[1]
	if a.Kind != yaml.MappingNode || b.Kind != yaml.MappingNode || len(a.Content) != 2 || len(b.Content) != 2 {
		t.Fatalf("environments: a = %+v, b = %+v", a, b)
	}
	if a.Content[0] == b.Content[0] || a.Content[0].HeadComment != "# the greeting" || b.Content[0].HeadComment != "# the greeting" {
		t.Fatalf("a = %+v, b = %+v", a.Content[0], b.Content[0])
	}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.AliasNode || n.Anchor != "" || n.Tag == "!!merge" {
			t.Fatalf("left in the tree: %+v", n)
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(doc)
}

// A file whose aliases expand into themselves, past the cap, or merge
// something that is not a mapping reads as not valid YAML: nil, and
// Unparsed for the compose model.
func TestDecodeFilesRefusesWhatCantExpand(t *testing.T) {
	bomb := "a: &a [x, x, x, x, x, x, x, x, x, x]\n"
	for i, prev := 0, "a"; i < 6; i++ {
		name := fmt.Sprintf("l%d", i)
		bomb += name + ": &" + name + " [" + strings.Repeat("*"+prev+", ", 9) + "*" + prev + "]\n"
		prev = name
	}
	for name, src := range map[string]string{
		"a billion laughs":       bomb,
		"an alias in its anchor": "a: &a [*a]\n",
		"a merge in its anchor":  "a: &a {<<: *a}\n",
		"a merge of a scalar":    "a: &a 1\nb: {<<: *a}\n",
		"a merge list of lists":  "a: &a [1]\nb: {<<: [*a]}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got := DecodeFiles([]byte(src)); got[0] != nil {
				t.Fatal("expanded")
			}
			c, err := ParseCompose([]byte(src))
			if err != nil || !c.Unparsed {
				t.Fatalf("compose = %+v, %v", c, err)
			}
		})
	}
	// The cap counts every node of the expanded tree: the document, the
	// root, two keys, the anchored scalar and the list are 6, so a list of
	// MaxExpandedNodes-6 aliases is the most that expands.
	list := func(n int) []byte {
		return []byte("a: &a x\nk: [" + strings.TrimSuffix(strings.Repeat("*a, ", n), ", ") + "]\n")
	}
	if DecodeFiles(list(MaxExpandedNodes - 6))[0] == nil {
		t.Fatal("a tree of exactly MaxExpandedNodes was refused")
	}
	if DecodeFiles(list(MaxExpandedNodes - 5))[0] != nil {
		t.Fatal("a tree past MaxExpandedNodes expanded")
	}
}

// A service merged from an anchor carries its volumes: the backup recipe
// sees the mount.
func TestParseComposeSeesMergedServices(t *testing.T) {
	c := parse(t, "x-app: &app\n  image: example/app\n  volumes: [data:/data]\nservices:\n  web:\n    <<: *app\n  worker:\n    <<: *app\n    command: work\nvolumes: {data: {}}\n")
	if len(c.Mounts["web"]) != 1 || len(c.Mounts["worker"]) != 1 || c.Mounts["worker"][0] != c.Mounts["web"][0] {
		t.Fatalf("mounts = %+v", c.Mounts)
	}
}
````

Append to `internal/domain/compose/xwinterflow/xwinterflow_test.go` (`git apply` this diff), and create the golden file it compares with:

````diff
diff --git a/internal/domain/compose/xwinterflow/xwinterflow_test.go b/internal/domain/compose/xwinterflow/xwinterflow_test.go
index a5219c3..ac3fbbf 100644
--- a/internal/domain/compose/xwinterflow/xwinterflow_test.go
+++ b/internal/domain/compose/xwinterflow/xwinterflow_test.go
@@ -239,3 +239,49 @@ func TestInterpolatedBodyValueIsNoDefault(t *testing.T) {
 		})
 	}
 }
+
+// rails is the catalog's merge-key pattern: services share one definition
+// through an anchor (x-app: &app), and one more reuses its environment
+// alone (environment: *app-env).
+const rails = `x-app: &app
+  image: example/rails:7
+  environment: &app-env
+    # The app's public URL, e.g. https://rails.example.com
+    BASE_URL: "http://localhost:3000"
+    SECRET_KEY_BASE: "changeme"
+services:
+  web:
+    <<: *app
+    command: serve
+  worker:
+    <<: *app
+    command: work
+  cron:
+    image: example/cron:1
+    environment: *app-env
+x-winterflow:
+  expose:
+    - { container: web, port: 3000 }
+  variables:
+    BASE_URL: { required: true, pattern: "^https?://.+" }
+    SECRET_KEY_BASE: { required: true, encrypted: true }
+`
+
+// A variable in a merged or aliased environment is the services' own: no
+// "nothing uses it" warning, its default and description come from the
+// anchor, and Render sets it in every service that has it.
+func TestMergedEnvironments(t *testing.T) {
+	s := parse(t, ModeTemplate, rails)
+	if len(s.Warnings) != 0 {
+		t.Fatalf("warnings = %q", s.Warnings)
+	}
+	base, _ := s.Variable("BASE_URL")
+	if !reflect.DeepEqual(base.Services, []string{"web", "worker", "cron"}) || base.Default == nil ||
+		*base.Default != "http://localhost:3000" || base.Description != "The app's public URL, e.g. https://rails.example.com" {
+		t.Fatalf("BASE_URL = %+v", base)
+	}
+	if secret, _ := s.Variable("SECRET_KEY_BASE"); len(secret.Services) != 3 || secret.Default != nil {
+		t.Fatalf("SECRET_KEY_BASE = %+v", secret)
+	}
+	golden(t, "merged", render(t, s, []string{"BASE_URL", "SECRET_KEY_BASE"}, map[string]int{"web:3000/http": 20001}))
+}
````

Create `internal/domain/compose/xwinterflow/testdata/render/merged.yml`:

````yaml
# Generated by winterflow from x-winterflow at every deploy. Do not edit.
services:
  cron:
    environment:
      BASE_URL: ${BASE_URL}
      SECRET_KEY_BASE: ${SECRET_KEY_BASE}
  web:
    environment:
      BASE_URL: ${BASE_URL}
      SECRET_KEY_BASE: ${SECRET_KEY_BASE}
    ports:
      - "127.0.0.1:20001:3000"
  worker:
    environment:
      BASE_URL: ${BASE_URL}
      SECRET_KEY_BASE: ${SECRET_KEY_BASE}
````

Modify `internal/domain/template/validate_test.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/template/validate_test.go b/internal/domain/template/validate_test.go
index 3be6cec..c1c16e6 100644
--- a/internal/domain/template/validate_test.go
+++ b/internal/domain/template/validate_test.go
@@ -1,6 +1,7 @@
 package template
 
 import (
+	"fmt"
 	"strings"
 	"testing"
 
@@ -36,6 +37,7 @@ func TestValidateRules(t *testing.T) {
 		"a long color":         {func(b *Body) { b.Color = strings.Repeat("c", 33) }, "color"},
 		"not YAML":             {func(b *Body) { b.Files["compose.yaml"] = "services: [\n" }, "not valid YAML"},
 		"an unknown block key": {func(b *Body) { b.Files["compose.yaml"] += "  volumes: {}\n" }, "x-winterflow.volumes"},
+		"an alias bomb":        {func(b *Body) { b.Files["compose.yaml"] = aliasBomb }, "not valid YAML"},
 		"a default off pattern": {func(b *Body) {
 			b.Files["compose.yaml"] = strings.Replace(notesCompose, `LANGUAGE: "en"`, `LANGUAGE: "english"`, 1)
 		}, "x-winterflow.variables.LANGUAGE.pattern"},
@@ -56,6 +58,18 @@ func TestValidateRules(t *testing.T) {
 	}
 }
 
+// aliasBomb is a dozen lines whose aliases would expand to ten million
+// nodes: refused as not valid YAML before it costs the API its memory.
+var aliasBomb = func() string {
+	out := "services: {app: {image: x}}\nx-a: &a [x, x, x, x, x, x, x, x, x, x]\n"
+	for i, prev := 0, "a"; i < 6; i++ {
+		name := fmt.Sprintf("x-l%d", i)
+		out += name + ": &" + name + " [" + strings.Repeat("*"+prev+", ", 9) + "*" + prev + "]\n"
+		prev = name
+	}
+	return out
+}()
+
 func TestValidateName(t *testing.T) {
 	if err := ValidateName("notes-2"); err != nil {
 		t.Fatal(err)
@@ -76,6 +90,9 @@ func TestWarnings(t *testing.T) {
 	b := notes()
 	b.Files["compose.yaml"] = strings.Replace(notesCompose, "    DB_PASSWORD: { encrypted: true }\n", "    DB_PASSWORD: { encrypted: true }\n    UNUSED: {}\n", 1) +
 		"# ${WF_APP_ID} ${ORG_SMTP}\n"
+	// compose interpolates only compose files: a reference in another file
+	// is the container's business (an nginx $uri, an envsubst template).
+	b.Files["nginx.conf"] = "location / { try_files $uri ${FRONT_URL}; }\n"
 	s, err := Validate(b)
 	if err != nil {
 		t.Fatal(err)
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/domain/backup/recipe/ ./internal/domain/compose/xwinterflow/ ./internal/domain/template/`
Expected: FAIL. `recipe` does not build (`undefined: MaxExpandedNodes`); `TestMergedEnvironments` fails with `warnings = ["x-winterflow.variables.BASE_URL: no service environment and no override uses it" ...]`; `TestValidateRules/an_alias_bomb` fails with `want a 400 naming not valid YAML, got <nil>`; `TestWarnings` fails (the nginx.conf's `${FRONT_URL}` is reported).

- [ ] **Step 3: Write the expansion**

Create `internal/domain/backup/recipe/expand.go`:

````go
package recipe

import (
	"errors"

	yaml "go.yaml.in/yaml/v3"
)

// MaxExpandedNodes caps a compose file's YAML tree once its aliases and
// merge keys are expanded. A file past it reads as not valid YAML: a few
// lines of nested aliases (a "billion laughs") would otherwise expand into
// more nodes than the API, which parses org users' bodies and the catalog
// feed, has memory for.
const MaxExpandedNodes = 100_000

// errExpand marks a tree that can't be expanded: past MaxExpandedNodes,
// an alias inside its own anchor, or a merge key whose value is not a
// mapping or a list of mappings.
var errExpand = errors.New("yaml: aliases can't be expanded")

// expand returns a copy of doc with every alias replaced by a copy of its
// anchor and every merge key (<<: *anchor, or <<: [*a, *b]) replaced by the
// merged mappings' keys, as compose reads the file: a mapping's own keys win
// over merged ones, an earlier mapping of a merge list wins over a later
// one, and merges nest. Comments stay on the copied nodes (a variable's
// description can come from a comment inside an anchor); anchors are
// cleared, so no copy shares a node with another.
func expand(doc *yaml.Node) (*yaml.Node, error) {
	x := expander{active: map[*yaml.Node]bool{}}
	return x.node(doc)
}

type expander struct {
	nodes int
	// active are the nodes being copied: an alias to one of them is a cycle.
	active map[*yaml.Node]bool
}

func (x *expander) node(n *yaml.Node) (*yaml.Node, error) {
	if n.Kind == yaml.AliasNode {
		if n.Alias == nil {
			return nil, errExpand
		}
		return x.node(n.Alias)
	}
	if x.active[n] {
		return nil, errExpand
	}
	if x.nodes++; x.nodes > MaxExpandedNodes {
		return nil, errExpand
	}
	out := *n
	out.Anchor, out.Alias, out.Content = "", nil, nil
	if len(n.Content) == 0 {
		return &out, nil
	}
	x.active[n] = true
	defer delete(x.active, n)
	var err error
	if n.Kind == yaml.MappingNode {
		out.Content, err = x.mapping(n)
	} else {
		out.Content, err = x.list(n.Content)
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (x *expander) list(in []*yaml.Node) ([]*yaml.Node, error) {
	out := make([]*yaml.Node, 0, len(in))
	for _, c := range in {
		cc, err := x.node(c)
		if err != nil {
			return nil, err
		}
		out = append(out, cc)
	}
	return out, nil
}

// mapping copies a mapping's pairs in order; each merge key is replaced, in
// place, by the merged mappings' pairs whose keys the mapping does not set
// itself and no earlier merged mapping set.
func (x *expander) mapping(n *yaml.Node) ([]*yaml.Node, error) {
	pairs, err := x.list(n.Content)
	if err != nil {
		return nil, err
	}
	taken := map[string]bool{}
	for i := 0; i+1 < len(pairs); i += 2 {
		if !isMerge(n.Content[i]) && pairs[i].Kind == yaml.ScalarNode {
			taken[pairs[i].Value] = true
		}
	}
	out := make([]*yaml.Node, 0, len(pairs))
	for i := 0; i+1 < len(pairs); i += 2 {
		if !isMerge(n.Content[i]) {
			out = append(out, pairs[i], pairs[i+1])
			continue
		}
		sources := []*yaml.Node{pairs[i+1]}
		if pairs[i+1].Kind == yaml.SequenceNode {
			sources = pairs[i+1].Content
		}
		for _, src := range sources {
			if src.Kind != yaml.MappingNode {
				return nil, errExpand
			}
			for j := 0; j+1 < len(src.Content); j += 2 {
				k := src.Content[j]
				if k.Kind == yaml.ScalarNode {
					if taken[k.Value] {
						continue
					}
					taken[k.Value] = true
				}
				out = append(out, k, src.Content[j+1])
			}
		}
	}
	return out, nil
}

// isMerge reports whether k is a merge key: a plain <<, not a quoted "<<".
func isMerge(k *yaml.Node) bool {
	return k.Kind == yaml.ScalarNode && k.Tag == "!!merge"
}
````

Modify `internal/domain/backup/recipe/compose.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/backup/recipe/compose.go b/internal/domain/backup/recipe/compose.go
index b66a5a6..c9b1658 100644
--- a/internal/domain/backup/recipe/compose.go
+++ b/internal/domain/backup/recipe/compose.go
@@ -41,14 +41,19 @@ func ParseCompose(files ...[]byte) (Compose, error) {
 	return ParseComposeNodes(DecodeFiles(files...)...)
 }
 
-// DecodeFiles decodes each compose file once into its YAML node tree; a
-// file that is not valid YAML is nil.
+// DecodeFiles decodes each compose file once into its YAML node tree, with
+// its aliases and merge keys expanded (expand): every reader of the tree
+// sees a service's merged keys as its own. A file that is not valid YAML,
+// or whose aliases expand past MaxExpandedNodes or into themselves, is nil.
 func DecodeFiles(files ...[]byte) []*yaml.Node {
 	out := make([]*yaml.Node, len(files))
 	for i, raw := range files {
 		var n yaml.Node
-		if yaml.Unmarshal(raw, &n) == nil {
-			out[i] = &n
+		if yaml.Unmarshal(raw, &n) != nil {
+			continue
+		}
+		if x, err := expand(&n); err == nil {
+			out[i] = x
 		}
 	}
 	return out
````

Modify `internal/domain/template/validate.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/template/validate.go b/internal/domain/template/validate.go
index eff560f..2584af0 100644
--- a/internal/domain/template/validate.go
+++ b/internal/domain/template/validate.go
@@ -95,12 +95,14 @@ func validFileName(name string) bool {
 }
 
 // Warnings are what a save should point out without refusing it: the
-// parser's (a variable nothing uses), then each ${VAR} the files reference
-// that nothing provides: not a declared variable, not a WF_ or
+// parser's (a variable nothing uses), then each ${VAR} the compose files
+// reference that nothing provides: not a declared variable, not a WF_ or
 // COMPOSE_PROJECT_NAME name, not an org shared variable (shared reports
-// those), and without a fallback of its own. A compose file that is not
-// valid YAML (spec.Compose.Unparsed) declares nothing, so the references
-// are left out: every one would read as undeclared.
+// those), and without a fallback of its own. Only compose files count:
+// compose interpolates nothing else, so a $uri in an nginx.conf is the
+// container's. A compose file that is not valid YAML
+// (spec.Compose.Unparsed) declares nothing, so the references are left
+// out: every one would read as undeclared.
 func Warnings(b Body, spec xwinterflow.Spec, shared func(name string) bool) []string {
 	if shared == nil {
 		shared = func(string) bool { return false }
@@ -110,8 +112,8 @@ func Warnings(b Body, spec xwinterflow.Spec, shared func(name string) bool) []st
 		return out
 	}
 	refs := map[string]bool{}
-	for _, name := range slices.Sorted(maps.Keys(b.Files)) {
-		varref.Undefaulted([]byte(b.Files[name]), refs)
+	for _, raw := range b.composeContents() {
+		varref.Undefaulted(raw, refs)
 	}
 	for _, name := range slices.Sorted(maps.Keys(refs)) {
 		if _, ok := spec.Variable(name); ok || model.ReservedVariableName(name) || shared(name) {
````

- [ ] **Step 4: Run the tests to verify they pass, then the packages that read compose trees**

Run: `go test ./internal/domain/backup/recipe/ ./internal/domain/compose/xwinterflow/ ./internal/domain/template/`
Expected: PASS (`TestDecodeFilesRefusesWhatCantExpand` builds a 400 KB file with 99 994 aliases: about 0.2 s).

Run: `go vet ./... && go test ./internal/domain/... ./internal/infra/orchestrator/... ./internal/app/web/contract/`
Expected: PASS (the agent's orchestrator and the contract suite read the same trees; nothing in them used aliases).

- [ ] **Step 5: Commit**

```bash
git add internal/domain/backup/recipe internal/domain/compose/xwinterflow internal/domain/template
git commit -m "fix: expand YAML aliases and merge keys before reading x-winterflow" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 2: The feed format: digest, signature, decode and the entry check

A pure domain package both the builder (Task 3) and the sync (Task 7) use, so the build refuses exactly what an install would skip.

**Files:**
- Create: `internal/domain/catalog/feed.go` (format, `Decode`, `Encode`)
- Create: `internal/domain/catalog/digest.go`
- Create: `internal/domain/catalog/sign.go`
- Create: `internal/domain/catalog/check.go` (`Entry.Check`)
- Create: `internal/domain/model/catalog.go` (`CatalogMeta`)
- Test: `internal/domain/catalog/catalog_test.go`
- Test: `internal/domain/model/catalog_test.go`

**Interfaces:**
- Consumes: `template.Body`, `template.Validate`, `template.Encode`, `template.MaxBodyBytes` (Task 1's expansion makes `Validate` refuse an alias bomb).
- Produces (package `winterflow/internal/domain/catalog`, imported as `feed` by Tasks 3, 7, 8, 9 to keep it apart from the usecase package of the same name):
  - `const FormatVersion = 1`; `var ErrInvalid` (a feed or entry that can't be used), `var ErrSignature`.
  - `type Feed struct { Format int; SourceCommit string; GeneratedAt time.Time; Apps []Entry }` (JSON `format`, `source_commit`, `generated_at`, `apps`).
  - `type Entry struct { Slug, Name, Summary, Description string; Keywords, Tags, AlternativeTo []string; License, HomepageURL, RepositoryURL, DocumentationURL, LogoURL string; Files map[string]string; Digest string }` (JSON names as the spec).
  - `func Decode(data []byte) (Feed, error)`; `func Encode(f Feed) ([]byte, error)`.
  - `func Digest(files map[string]string) string`.
  - `func ParsePublicKey(s string) (ed25519.PublicKey, error)`; `func ParsePrivateKey(s string) (ed25519.PrivateKey, error)`; `func GenerateKey(random io.Reader) (public, seed string, err error)`; `func Sign(key ed25519.PrivateKey, feed []byte) []byte`; `func Verify(key ed25519.PublicKey, feed, sig []byte) error`.
  - `func (e Entry) Check() (template.Body, model.CatalogMeta, error)`.
  - `model.CatalogMeta{Summary, Description string; Keywords, Tags, AlternativeTo []string; License, HomepageURL, RepositoryURL, DocumentationURL, LogoURL string}` (JSON `summary`, `description`, `keywords`, `tags`, `alternative_to`, `license`, `homepage_url`, `repository_url`, `documentation_url`, `logo_url`), `func (m CatalogMeta) Equal(o CatalogMeta) bool`, `func (m CatalogMeta) Normalized() CatalogMeta` (nil lists become empty).

- [ ] **Step 1: Write the failing tests**

Create `internal/domain/catalog/catalog_test.go`:

````go
package catalog

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/template"
)

const budgetCompose = `services:
  app:
    image: actualbudget/actual-server:latest
    environment:
      # The public URL, e.g. https://budget.example.com
      ACTUAL_URL: "http://localhost:5006"
    volumes:
      - data:/data
volumes:
  data:
x-winterflow:
  expose:
    - { container: app, port: 5006 }
  variables:
    ACTUAL_URL: { required: true, pattern: "^https?://.+" }
`

// entry is a valid feed entry.
func entry() Entry {
	files := map[string]string{"compose.yml": budgetCompose}
	return Entry{
		Slug: "actual-budget", Name: "Actual Budget",
		Summary: "A local-first personal finance app", Description: "Envelope budgeting.",
		Keywords: []string{"budget"}, AlternativeTo: []string{"YNAB"}, License: "MIT",
		HomepageURL: "https://actualbudget.org", LogoURL: "https://winterflow.io/catalog/logos/actual-budget.png",
		Files: files, Digest: Digest(files),
	}
}

func TestDigest(t *testing.T) {
	a := Digest(map[string]string{"compose.yml": "x", "b.conf": "y"})
	if a != Digest(map[string]string{"b.conf": "y", "compose.yml": "x"}) || !strings.HasPrefix(a, "sha256:") || len(a) != len("sha256:")+64 {
		t.Fatalf("digest = %q", a)
	}
	// A name and its content never shift into each other.
	if Digest(map[string]string{"ab": "c"}) == Digest(map[string]string{"a": "bc"}) {
		t.Fatal("ab/c and a/bc share a digest")
	}
	if Digest(map[string]string{"compose.yml": "x"}) == Digest(map[string]string{"compose.yml": "x "}) {
		t.Fatal("a changed file kept the digest")
	}
	if Digest(nil) != Digest(map[string]string{}) {
		t.Fatal("no files")
	}
}

func TestSignAndVerify(t *testing.T) {
	pub, seed, err := GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := ParsePublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	sk, err := ParsePrivateKey(seed)
	if err != nil {
		t.Fatal(err)
	}
	feed := []byte(`{"format":1}`)
	sig := Sign(sk, feed)
	if !bytes.HasSuffix(sig, []byte("\n")) {
		t.Fatalf("sig = %q", sig)
	}
	if err := Verify(pk, feed, sig); err != nil {
		t.Fatal(err)
	}
	if err := Verify(pk, feed, bytes.TrimSpace(sig)); err != nil {
		t.Fatalf("without the newline: %v", err)
	}
	// The 64-byte private key reads too.
	full, err := ParsePrivateKey(base64.StdEncoding.EncodeToString(sk))
	if err != nil || !full.Equal(sk) {
		t.Fatalf("64-byte key = %v", err)
	}
	if _, _, err := GenerateKey(iotest.ErrReader(errors.New("no entropy"))); err == nil {
		t.Fatal("a key from a failing reader")
	}
	other, _, _ := GenerateKey(rand.Reader)
	otherKey, _ := ParsePublicKey(other)
	for name, c := range map[string]struct {
		key       ed25519.PublicKey
		feed, sig []byte
	}{
		"another key":       {otherKey, feed, sig},
		"a changed feed":    {pk, []byte(`{"format":1} `), sig},
		"not base64":        {pk, feed, []byte("not a signature")},
		"a short signature": {pk, feed, []byte(base64.StdEncoding.EncodeToString([]byte("short")))},
		"an empty .sig":     {pk, feed, nil},
	} {
		if err := Verify(c.key, c.feed, c.sig); !errors.Is(err, ErrSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestParseKeysRefuse(t *testing.T) {
	for _, s := range []string{"", "not base64!", base64.StdEncoding.EncodeToString(make([]byte, 31))} {
		if _, err := ParsePublicKey(s); err == nil {
			t.Errorf("public key %q accepted", s)
		}
		if _, err := ParsePrivateKey(s); err == nil {
			t.Errorf("private key %q accepted", s)
		}
	}
	// A 64-byte key whose public half is not its seed's.
	bad := make([]byte, ed25519.PrivateKeySize)
	if _, err := ParsePrivateKey(base64.StdEncoding.EncodeToString(bad)); err == nil {
		t.Error("an inconsistent 64-byte key was accepted")
	}
}

func TestDecode(t *testing.T) {
	at := time.Date(2026, 10, 14, 20, 5, 43, 0, time.UTC)
	raw, err := Encode(Feed{Format: FormatVersion, SourceCommit: "49bf28d", GeneratedAt: at, Apps: []Entry{entry()}})
	if err != nil {
		t.Fatal(err)
	}
	f, err := Decode(raw)
	if err != nil || f.SourceCommit != "49bf28d" || !f.GeneratedAt.Equal(at) || len(f.Apps) != 1 || f.Apps[0].Files["compose.yml"] != budgetCompose {
		t.Fatalf("decoded %+v, %v", f, err)
	}
	// A later format's new fields are ignored.
	if _, err := Decode([]byte(`{"format":1,"generated_at":"2026-10-14T20:05:43Z","apps":[{"slug":"a","stars":5}],"signed_by":"x"}`)); err != nil {
		t.Fatalf("unknown fields: %v", err)
	}
	for name, src := range map[string]string{
		"not JSON":          `{"format":`,
		"format 2":          `{"format":2,"generated_at":"2026-10-14T20:05:43Z","apps":[{"slug":"a"}]}`,
		"no format":         `{"generated_at":"2026-10-14T20:05:43Z","apps":[{"slug":"a"}]}`,
		"no generated_at":   `{"format":1,"apps":[{"slug":"a"}]}`,
		"no apps":           `{"format":1,"generated_at":"2026-10-14T20:05:43Z","apps":[]}`,
		"a slug twice":      `{"format":1,"generated_at":"2026-10-14T20:05:43Z","apps":[{"slug":"a"},{"slug":"a"}]}`,
		"apps is no list":   `{"format":1,"generated_at":"2026-10-14T20:05:43Z","apps":{}}`,
		"generated_at text": `{"format":1,"generated_at":"yesterday","apps":[{"slug":"a"}]}`,
	} {
		if _, err := Decode([]byte(src)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCheck(t *testing.T) {
	body, meta, err := entry().Check()
	if err != nil {
		t.Fatal(err)
	}
	if body.Files["compose.yml"] != budgetCompose || body.Summary != "A local-first personal finance app" || body.Icon != "" {
		t.Fatalf("body = %+v", body)
	}
	want := model.CatalogMeta{
		Summary: "A local-first personal finance app", Description: "Envelope budgeting.",
		Keywords: []string{"budget"}, Tags: []string{}, AlternativeTo: []string{"YNAB"}, License: "MIT",
		HomepageURL: "https://actualbudget.org", LogoURL: "https://winterflow.io/catalog/logos/actual-budget.png",
	}
	if !meta.Equal(want) || meta.Tags == nil {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestCheckRefuses(t *testing.T) {
	for name, c := range map[string]struct {
		edit func(*Entry)
		want string
	}{
		"an upper-case slug":  {func(e *Entry) { e.Slug = "Actual" }, "slug"},
		"a slug with a slash": {func(e *Entry) { e.Slug = "a/b" }, "slug"},
		"a long slug":         {func(e *Entry) { e.Slug = strings.Repeat("a", 129) }, "slug"},
		"no name":             {func(e *Entry) { e.Name = "" }, "name"},
		"a padded name":       {func(e *Entry) { e.Name = " Actual" }, "name"},
		"a two-line name":     {func(e *Entry) { e.Name = "a\nb" }, "name"},
		"a long name":         {func(e *Entry) { e.Name = strings.Repeat("n", 256) }, "name"},
		"51 keywords":         {func(e *Entry) { e.Keywords = make([]string, 51) }, "keywords"},
		"an empty tag":        {func(e *Entry) { e.Tags = []string{""} }, "tags[0]"},
		"a long alternative":  {func(e *Entry) { e.AlternativeTo = []string{strings.Repeat("y", 101)} }, "alternative_to[0]"},
		"a long license":      {func(e *Entry) { e.License = strings.Repeat("l", 101) }, "license"},
		"an http homepage":    {func(e *Entry) { e.HomepageURL = "http://actualbudget.org" }, "homepage_url"},
		"a javascript logo":   {func(e *Entry) { e.LogoURL = "javascript:alert(1)" }, "logo_url"},
		"a host-less URL":     {func(e *Entry) { e.RepositoryURL = "https:///x" }, "repository_url"},
		"a long URL":          {func(e *Entry) { e.DocumentationURL = "https://x.test/" + strings.Repeat("p", 2048) }, "documentation_url"},
		"a stale digest":      {func(e *Entry) { e.Files["compose.yml"] += "# edited\n" }, "digest"},
		"no compose file":     {func(e *Entry) { e.Files = map[string]string{"x.conf": ""}; e.Digest = Digest(e.Files) }, "a compose file is required"},
		"a long summary":      {func(e *Entry) { e.Summary = strings.Repeat("s", 201) }, "summary"},
		"a shared volume name": {func(e *Entry) {
			setCompose(e, strings.Replace(budgetCompose, "  data:\nx-", "  data: { name: budget }\nx-", 1))
		}, "would be shared"},
		"an alias bomb": {func(e *Entry) { setCompose(e, aliasBomb()) }, "not valid YAML"},
		"a body over 1 MiB": {func(e *Entry) {
			e.Files["big.txt"] = strings.Repeat("x", template.MaxBodyBytes)
			e.Digest = Digest(e.Files)
		}, "larger than"},
	} {
		t.Run(name, func(t *testing.T) {
			e := entry()
			c.edit(&e)
			_, _, err := e.Check()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to name %q", err, c.want)
			}
		})
	}
}

// setCompose replaces the entry's compose file and its digest.
func setCompose(e *Entry, compose string) {
	e.Files = map[string]string{"compose.yml": compose}
	e.Digest = Digest(e.Files)
}

// aliasBomb is a compose file whose aliases would expand to ten million
// nodes.
func aliasBomb() string {
	out := "services: {app: {image: x}}\nx-a: &a [x, x, x, x, x, x, x, x, x, x]\n"
	prev := "a"
	for _, name := range []string{"b", "c", "d", "e", "f", "g"} {
		out += "x-" + name + ": &" + name + " [" + strings.Repeat("*"+prev+", ", 9) + "*" + prev + "]\n"
		prev = name
	}
	return out
}
````

Create `internal/domain/model/catalog_test.go`:

````go
package model

import "testing"

func TestCatalogMetaEqualAndNormalized(t *testing.T) {
	a := CatalogMeta{Summary: "s", Keywords: []string{"k"}}
	n := a.Normalized()
	if n.Tags == nil || n.AlternativeTo == nil || len(n.Keywords) != 1 || a.Tags != nil {
		t.Fatalf("normalized = %+v (from %+v)", n, a)
	}
	if !a.Equal(n) {
		t.Fatal("a nil list differs from an empty one")
	}
	for _, b := range []CatalogMeta{
		{Summary: "t", Keywords: []string{"k"}},
		{Summary: "s", Keywords: []string{"k", "l"}},
		{Summary: "s", Keywords: []string{"k"}, LogoURL: "https://x.test/l.png"},
	} {
		if a.Equal(b) {
			t.Errorf("%+v equals %+v", a, b)
		}
	}
}
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/domain/catalog/ ./internal/domain/model/`
Expected: FAIL to build: `undefined: CatalogMeta` (model) and `undefined: Entry`, `undefined: Digest` (catalog).

- [ ] **Step 3: Write the metadata type and the package**

Create `internal/domain/model/catalog.go`:

````go
package model

import "slices"

// CatalogMeta is what the catalog says about a catalog template besides
// its name and files: the gallery's text, search words and links. The
// sync updates it in place (a fixed description never makes a revision).
type CatalogMeta struct {
	Summary          string   `json:"summary"`
	Description      string   `json:"description"`
	Keywords         []string `json:"keywords"`
	Tags             []string `json:"tags"`
	AlternativeTo    []string `json:"alternative_to" description:"Apps this one replaces, as search words."`
	License          string   `json:"license"`
	HomepageURL      string   `json:"homepage_url"`
	RepositoryURL    string   `json:"repository_url"`
	DocumentationURL string   `json:"documentation_url"`
	LogoURL          string   `json:"logo_url"`
}

// Equal reports whether m and o say the same (a nil list equals an empty
// one).
func (m CatalogMeta) Equal(o CatalogMeta) bool {
	return m.Summary == o.Summary && m.Description == o.Description &&
		slices.Equal(m.Keywords, o.Keywords) && slices.Equal(m.Tags, o.Tags) &&
		slices.Equal(m.AlternativeTo, o.AlternativeTo) && m.License == o.License &&
		m.HomepageURL == o.HomepageURL && m.RepositoryURL == o.RepositoryURL &&
		m.DocumentationURL == o.DocumentationURL && m.LogoURL == o.LogoURL
}

// Normalized is m with every nil list empty, as the API answers it.
func (m CatalogMeta) Normalized() CatalogMeta {
	for _, l := range []*[]string{&m.Keywords, &m.Tags, &m.AlternativeTo} {
		if *l == nil {
			*l = []string{}
		}
	}
	return m
}
````

Create `internal/domain/catalog/feed.go`:

````go
// Package catalog is the catalog feed: the signed JSON document the
// catalog builder (cmd/catalog) publishes and the API's sync reads. It
// holds the format, the files digest, the ed25519 signature and the check
// every entry must pass, shared by the builder and the sync, so the build
// refuses exactly what an install would skip. No I/O.
package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// FormatVersion is the feed format this build reads and writes.
const FormatVersion = 1

// ErrInvalid marks a feed or an entry that can't be used; the message
// names the field.
var ErrInvalid = errors.New("invalid catalog")

func invalid(field, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalid, field, fmt.Sprintf(format, args...))
}

// Feed is catalog.json.
type Feed struct {
	Format       int       `json:"format"`
	SourceCommit string    `json:"source_commit"`
	GeneratedAt  time.Time `json:"generated_at"`
	Apps         []Entry   `json:"apps"`
}

// Entry is one app of the feed: a catalog template's name, metadata and
// files, with the digest of the files.
type Entry struct {
	Slug             string            `json:"slug"`
	Name             string            `json:"name"`
	Summary          string            `json:"summary"`
	Description      string            `json:"description"`
	Keywords         []string          `json:"keywords"`
	Tags             []string          `json:"tags"`
	AlternativeTo    []string          `json:"alternative_to"`
	License          string            `json:"license"`
	HomepageURL      string            `json:"homepage_url"`
	RepositoryURL    string            `json:"repository_url"`
	DocumentationURL string            `json:"documentation_url"`
	LogoURL          string            `json:"logo_url"`
	Files            map[string]string `json:"files"`
	Digest           string            `json:"digest"`
}

// Decode reads catalog.json. Unknown fields are ignored (a later format
// may add some); an unknown format, a missing generated_at, no apps at all
// (a signed empty feed would withdraw every catalog template) or a slug
// listed twice refuse the whole feed. Entries are checked one by one
// (Entry.Check): one bad entry does not refuse the others.
func Decode(data []byte) (Feed, error) {
	var f Feed
	if err := json.Unmarshal(data, &f); err != nil {
		return Feed{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if f.Format != FormatVersion {
		return Feed{}, invalid("format", "%d is not a format this build reads (%d)", f.Format, FormatVersion)
	}
	if f.GeneratedAt.IsZero() {
		return Feed{}, invalid("generated_at", "missing")
	}
	if len(f.Apps) == 0 {
		return Feed{}, invalid("apps", "the feed lists no app")
	}
	seen := make(map[string]bool, len(f.Apps))
	for _, e := range f.Apps {
		if seen[e.Slug] {
			return Feed{}, invalid("apps", "%q is listed twice", e.Slug)
		}
		seen[e.Slug] = true
	}
	return f, nil
}

// Encode is catalog.json as the builder writes it: compact, apps in the
// order given (the builder sorts them by slug), so one apps tree always
// builds the same bytes apart from generated_at.
func Encode(f Feed) ([]byte, error) {
	return json.Marshal(f)
}
````

Create `internal/domain/catalog/digest.go`:

````go
package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"slices"
	"strconv"
)

// Digest identifies a set of files: "sha256:" and the hex SHA-256 over the
// files in name order, each written as <len(name)>:<name><len(content)>:<content>,
// so no split of a name and its content reads as another file set. Only
// the files count: a new revision of a catalog template is made when its
// digest changes, never for a fixed description.
func Digest(files map[string]string) string {
	h := sha256.New()
	for _, name := range slices.Sorted(maps.Keys(files)) {
		content := files[name]
		h.Write([]byte(strconv.Itoa(len(name)) + ":" + name + strconv.Itoa(len(content)) + ":"))
		h.Write([]byte(content))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
````

Create `internal/domain/catalog/sign.go`:

````go
package catalog

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// ErrSignature marks a feed whose signature does not verify: the sync
// refuses the whole feed.
var ErrSignature = errors.New("the catalog signature does not verify")

// ParsePublicKey reads a public key: 32 bytes, standard base64.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("catalog public key: want %d bytes of standard base64", ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(b), nil
}

// ParsePrivateKey reads a signing key, standard base64: the 32-byte seed
// (what keygen prints) or the 64-byte private key.
func ParsePrivateKey(s string) (ed25519.PrivateKey, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	switch {
	case err != nil:
	case len(b) == ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(b), nil
	case len(b) == ed25519.PrivateKeySize:
		k := ed25519.PrivateKey(b)
		if bytes.Equal(ed25519.NewKeyFromSeed(k.Seed()), k) {
			return k, nil
		}
	}
	return nil, fmt.Errorf("catalog signing key: want a %d-byte seed or a %d-byte key, standard base64", ed25519.SeedSize, ed25519.PrivateKeySize)
}

// GenerateKey makes a key pair from random: the public key and the seed,
// each standard base64.
func GenerateKey(random io.Reader) (public, seed string, err error) {
	pub, priv, err := ed25519.GenerateKey(random)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(priv.Seed()), nil
}

// Sign is catalog.json.sig for feed, the exact bytes of catalog.json: the
// ed25519 signature in standard base64, then a newline.
func Sign(key ed25519.PrivateKey, feed []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, feed)) + "\n")
}

// Verify checks sig (a .sig file: surrounding whitespace is ignored)
// against the exact bytes of feed.
func Verify(key ed25519.PublicKey, feed, sig []byte) error {
	raw, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize || !ed25519.Verify(key, feed, raw) {
		return ErrSignature
	}
	return nil
}
````

Create `internal/domain/catalog/check.go`:

````go
package catalog

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/template"
)

// Limits of an entry's fields.
const (
	MaxName     = 255
	MaxListLen  = 50
	MaxListItem = 100
	MaxLicense  = 100
	MaxURL      = 2048
)

// slugRE is a catalog slug (the apps folder name).
var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

// Check validates e as the builder and the sync both require and returns
// the revision body (files, summary, description) and the metadata a
// catalog template stores. The body must pass the template validation
// (x-winterflow with the template rules, the 1 MiB cap); the digest must
// be the files'.
func (e Entry) Check() (template.Body, model.CatalogMeta, error) {
	if !slugRE.MatchString(e.Slug) {
		return template.Body{}, model.CatalogMeta{}, invalid("slug", "%q is not lowercase letters, digits, - and _, at most 128", e.Slug)
	}
	if e.Name == "" || len(e.Name) > MaxName || strings.TrimSpace(e.Name) != e.Name || strings.ContainsAny(e.Name, "\r\n") {
		return template.Body{}, model.CatalogMeta{}, invalid("name", "one line of 1 to %d characters, no surrounding spaces", MaxName)
	}
	meta := model.CatalogMeta{
		Summary: e.Summary, Description: e.Description,
		Keywords: e.Keywords, Tags: e.Tags, AlternativeTo: e.AlternativeTo, License: e.License,
		HomepageURL: e.HomepageURL, RepositoryURL: e.RepositoryURL, DocumentationURL: e.DocumentationURL, LogoURL: e.LogoURL,
	}.Normalized()
	for _, l := range []struct {
		field string
		list  []string
	}{{"keywords", meta.Keywords}, {"tags", meta.Tags}, {"alternative_to", meta.AlternativeTo}} {
		if err := checkList(l.field, l.list); err != nil {
			return template.Body{}, model.CatalogMeta{}, err
		}
	}
	if len(e.License) > MaxLicense || strings.ContainsAny(e.License, "\r\n") {
		return template.Body{}, model.CatalogMeta{}, invalid("license", "one line of at most %d characters", MaxLicense)
	}
	for _, u := range [][2]string{
		{"homepage_url", e.HomepageURL}, {"repository_url", e.RepositoryURL},
		{"documentation_url", e.DocumentationURL}, {"logo_url", e.LogoURL},
	} {
		if err := checkURL(u[0], u[1]); err != nil {
			return template.Body{}, model.CatalogMeta{}, err
		}
	}
	if got := Digest(e.Files); e.Digest != got {
		return template.Body{}, model.CatalogMeta{}, invalid("digest", "%q is not the files' digest (%s)", e.Digest, got)
	}
	body := template.Body{Files: e.Files, Summary: e.Summary, Description: e.Description}
	if _, err := template.Validate(body); err != nil {
		return template.Body{}, model.CatalogMeta{}, err
	}
	if _, err := template.Encode(body); err != nil {
		return template.Body{}, model.CatalogMeta{}, err
	}
	return body, meta, nil
}

func checkList(field string, list []string) error {
	if len(list) > MaxListLen {
		return invalid(field, "at most %d entries", MaxListLen)
	}
	for i, s := range list {
		if s == "" || len(s) > MaxListItem || strings.ContainsAny(s, "\r\n") {
			return invalid(fmt.Sprintf("%s[%d]", field, i), "one line of 1 to %d characters", MaxListItem)
		}
	}
	return nil
}

// checkURL accepts "" or an https URL of at most MaxURL bytes.
func checkURL(field, s string) error {
	if s == "" {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || len(s) > MaxURL || u.Scheme != "https" || u.Host == "" {
		return invalid(field, "%q is not an https URL of at most %d characters", s, MaxURL)
	}
	return nil
}
````

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/domain/catalog/ ./internal/domain/model/ && go test -cover ./internal/domain/catalog/ ./internal/domain/model/`
Expected: PASS, `internal/domain/catalog` at 100.0% of statements.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/catalog internal/domain/model/catalog.go internal/domain/model/catalog_test.go
git commit -m "feat(catalog): the feed format, its digest, signature and entry check" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 3: The builder, `cmd/catalog build` and `cmd/catalog keygen`

The winterflow.io deploy runs it with the signing key; winterflow-apps CI runs it unsigned on every PR as its validation. It is tested against a fixture apps tree in miniature (two installable apps, one per kind of skip, and the real catalog's quirks: merge keys, extra files in a subfolder, a file without extension, a missing logo).

**Files:**
- Create: `internal/app/catalog/build.go` (`Build`)
- Create: `internal/app/catalog/cli.go` (`Main`)
- Create: `cmd/catalog/main.go` (thin; `cmd/*/main.go` is exempt from the coverage gate)
- Test: `internal/app/catalog/build_test.go`
- Test: `internal/app/catalog/testdata/winterflow-apps/apps/actual-budget/compose/compose.yml`
- Test: `internal/app/catalog/testdata/winterflow-apps/apps/actual-budget/metadata.json`
- Test: `internal/app/catalog/testdata/winterflow-apps/apps/coming-soon/compose/compose.yml`
- Test: `internal/app/catalog/testdata/winterflow-apps/apps/coming-soon/metadata.json`
- Test: `internal/app/catalog/testdata/winterflow-apps/apps/gitea/compose/compose.yml`
- Test: `internal/app/catalog/testdata/winterflow-apps/apps/no-compose/metadata.json`
- Test: `internal/app/catalog/testdata/winterflow-apps/apps/plausible/compose/Caddyfile`
- Test: `internal/app/catalog/testdata/winterflow-apps/apps/plausible/compose/clickhouse/logs.xml`
- Test: `internal/app/catalog/testdata/winterflow-apps/apps/plausible/compose/compose.yml`
- Test: `internal/app/catalog/testdata/winterflow-apps/apps/plausible/metadata.json`
- Test: `internal/app/catalog/testdata/winterflow-apps/apps/traefik-ingress/compose/compose.yml`
- Test: `internal/app/catalog/testdata/winterflow-apps/apps/traefik-ingress/metadata.json`
- Test: `internal/app/catalog/testdata/winterflow-apps/assets/actual-budget/actual-budget.svg`

**Interfaces:**
- Consumes: Task 2's `feed.Entry`, `feed.Digest`, `feed.Encode`, `feed.Sign`, `feed.ParsePrivateKey`, `feed.GenerateKey`, `Entry.Check`; `xwinterflow.Parse(xwinterflow.ModeTemplate, compose)` for `Spec.Files`; `template.Parse`, `template.Warnings`.
- Produces (package `winterflow/internal/app/catalog`):
  - `const FeedFile = "catalog.json"`, `SigFile = "catalog.json.sig"`, `LogoDir = "logos"`.
  - `type Options struct { Apps, Out, BaseURL string; SigningKey ed25519.PrivateKey; SourceCommit string; Now func() time.Time }`.
  - `type Skip struct { Slug, Reason string }`; `type Report struct { Apps []string; Skipped []Skip; Warnings []string }`; `type BuildError struct { Problems []string }` (`Error()` lists them).
  - `func Build(o Options) (Report, error)`.
  - `func Main(args []string, stdout, stderr io.Writer, getenv func(string) string) int` (0 done, 1 failed, 2 usage).
  - CLI: `catalog build --apps DIR --out DIR --base-url URL [--sign-key-env NAME] [--source-commit SHA]`; `catalog keygen` prints `CATALOG_PUBLIC_KEY=<b64>` and `CATALOG_SIGNING_KEY=<b64 seed>`.

- [ ] **Step 1: Write the fixture apps tree**

Create each file below under `internal/app/catalog/testdata/winterflow-apps/` exactly as shown (they are text; the logo is a one-line SVG).

Create `internal/app/catalog/testdata/winterflow-apps/apps/actual-budget/compose/compose.yml`:

````yaml
services:
  app:
    image: actualbudget/actual-server:25.10.0
    restart: unless-stopped
    environment:
      # The public URL the browser uses, e.g. https://budget.example.com
      ACTUAL_URL: "http://localhost:5006"
    volumes:
      - data:/data
volumes:
  data:
x-winterflow:
  expose:
    - { container: app, port: 5006 }
  variables:
    ACTUAL_URL: { required: true, pattern: "^https?://.+" }
  backups:
    - { name: data, type: files, volume: data }
````

Create `internal/app/catalog/testdata/winterflow-apps/apps/actual-budget/metadata.json`:

````json
{
  "id": "6c0d3c1e-actual-budget",
  "slug": "actual-budget",
  "keywords": ["budget", "finance"],
  "tags": ["finance"],
  "alternative_to": ["YNAB", "Mint"],
  "waiting_list": false,
  "name": "Actual Budget",
  "logo": "actual-budget/actual-budget.svg",
  "screenshots": [],
  "summary": "A local-first personal finance app",
  "description": "Envelope budgeting that keeps your data on your server.",
  "license": "MIT",
  "license_url": "https://github.com/actualbudget/actual/blob/master/LICENSE.txt",
  "repository_url": "https://github.com/actualbudget/actual",
  "stars": 15000,
  "last_updated": "2026-09-30",
  "update_ttl_days": 30,
  "registry_url": "https://hub.docker.com/r/actualbudget/actual-server",
  "documentation_url": "https://actualbudget.org/docs",
  "sponsorship": "https://opencollective.com/actual",
  "homepage_url": "https://actualbudget.org"
}
````

Create `internal/app/catalog/testdata/winterflow-apps/apps/coming-soon/compose/compose.yml`:

````yaml
services: { app: { image: example/soon } }
````

Create `internal/app/catalog/testdata/winterflow-apps/apps/coming-soon/metadata.json`:

````json
{ "slug": "coming-soon", "name": "Coming Soon", "summary": "Not ready yet", "waiting_list": true }
````

Create `internal/app/catalog/testdata/winterflow-apps/apps/gitea/compose/compose.yml`:

````yaml
services: { gitea: { image: gitea/gitea:1 } }
````

Create `internal/app/catalog/testdata/winterflow-apps/apps/no-compose/metadata.json`:

````json
{ "slug": "no-compose", "name": "No Compose", "summary": "Metadata only", "waiting_list": false }
````

Create `internal/app/catalog/testdata/winterflow-apps/apps/plausible/compose/Caddyfile`:

````text
:80 {
	# {$UPSTREAM} and ${UPSTREAM} here are Caddy's, not compose's
	reverse_proxy plausible:8000
}
````

Create `internal/app/catalog/testdata/winterflow-apps/apps/plausible/compose/clickhouse/logs.xml`:

````xml
<clickhouse><logger><level>warning</level></logger></clickhouse>
````

Create `internal/app/catalog/testdata/winterflow-apps/apps/plausible/compose/compose.yml`:

````yaml
x-env: &env
  # Signs sessions. Generate with: openssl rand -base64 48
  SECRET_KEY_BASE: "changeme"
  CLICKHOUSE_DATABASE_URL: http://events:8123/plausible
services:
  plausible: &plausible
    image: ghcr.io/plausible/community-edition:v2.1.4
    environment:
      <<: *env
      # The public URL, e.g. https://plausible.example.com
      BASE_URL: "http://localhost:8000"
  migrate:
    <<: *plausible
    command: /entrypoint.sh db migrate
  caddy:
    image: caddy:2
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
  events:
    image: clickhouse/clickhouse-server:24.3
    volumes:
      - events:/var/lib/clickhouse
      - ./clickhouse/logs.xml:/etc/clickhouse-server/config.d/logs.xml:ro
volumes:
  events:
x-winterflow:
  expose:
    - { container: caddy, port: 80 }
  variables:
    BASE_URL: { required: true, pattern: "^https?://.+" }
    SECRET_KEY_BASE: { required: true, encrypted: true }
  files:
    - Caddyfile
    - clickhouse/logs.xml
````

Create `internal/app/catalog/testdata/winterflow-apps/apps/plausible/metadata.json`:

````json
{
  "slug": "plausible",
  "name": "Plausible Analytics",
  "logo": "plausible/plausible.png",
  "summary": "Simple, privacy-friendly web analytics",
  "description": "Lightweight analytics without cookies.",
  "keywords": ["analytics"],
  "tags": ["analytics"],
  "alternative_to": ["Google Analytics"],
  "license": "AGPL-3.0",
  "repository_url": "https://github.com/plausible/analytics",
  "documentation_url": "https://plausible.io/docs",
  "waiting_list": false
}
````

Create `internal/app/catalog/testdata/winterflow-apps/apps/traefik-ingress/compose/compose.yml`:

````yaml
services: { traefik: { image: traefik:3, ports: ["80:80", "443:443"] } }
````

Create `internal/app/catalog/testdata/winterflow-apps/apps/traefik-ingress/metadata.json`:

````json
{ "slug": "traefik-ingress", "name": "Traefik", "summary": "An ingress", "waiting_list": false, "extension": "expose" }
````

Create `internal/app/catalog/testdata/winterflow-apps/assets/actual-budget/actual-budget.svg`:

````xml
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><rect width="1" height="1"/></svg>
````

- [ ] **Step 2: Write the failing tests**

Create `internal/app/catalog/build_test.go`:

````go
package catalog

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	feed "winterflow/internal/domain/catalog"
)

// fixture is a winterflow-apps checkout in miniature: two installable
// apps (actual-budget with a logo; plausible with merge keys, extra files
// and no logo) and one folder per kind of skip.
const fixture = "testdata/winterflow-apps"

var at = time.Date(2026, 10, 14, 20, 5, 43, 0, time.UTC)

func options(t *testing.T, apps string) Options {
	t.Helper()
	return Options{
		Apps: apps, Out: t.TempDir(), BaseURL: "https://winterflow.io/catalog/",
		SourceCommit: "49bf28d", Now: func() time.Time { return at.Add(time.Second / 2) },
	}
}

// copyFixture is a writable copy of the fixture.
func copyFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(fixture)); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readFeed(t *testing.T, out string) (feed.Feed, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(out, FeedFile))
	if err != nil {
		t.Fatal(err)
	}
	f, err := feed.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return f, raw
}

func TestBuildTheFixture(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	o := options(t, fixture)
	o.SigningKey = key
	rep, err := Build(o)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rep.Apps, []string{"actual-budget", "plausible"}) {
		t.Fatalf("apps = %v", rep.Apps)
	}
	wantSkips := []Skip{
		{"coming-soon", "on the waiting list"},
		{"gitea", "no metadata.json"},
		{"no-compose", "no compose/compose.yml"},
		{"traefik-ingress", "an agent extension (expose), not an app"},
	}
	if !reflect.DeepEqual(rep.Skipped, wantSkips) {
		t.Fatalf("skipped = %+v", rep.Skipped)
	}
	// Only the missing logo: the merged environments declare their
	// variables, and the Caddyfile's ${UPSTREAM} is not compose's.
	if !reflect.DeepEqual(rep.Warnings, []string{"plausible: no logo"}) {
		t.Fatalf("warnings = %q", rep.Warnings)
	}

	f, raw := readFeed(t, o.Out)
	if f.Format != feed.FormatVersion || f.SourceCommit != "49bf28d" || !f.GeneratedAt.Equal(at) || len(f.Apps) != 2 {
		t.Fatalf("feed = %+v", f)
	}
	sig, err := os.ReadFile(filepath.Join(o.Out, SigFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := feed.Verify(pub, raw, sig); err != nil {
		t.Fatal(err)
	}
	budget, plausible := f.Apps[0], f.Apps[1]
	if budget.Slug != "actual-budget" || budget.Name != "Actual Budget" || budget.License != "MIT" ||
		budget.LogoURL != "https://winterflow.io/catalog/logos/actual-budget.svg" ||
		!reflect.DeepEqual(budget.AlternativeTo, []string{"YNAB", "Mint"}) || budget.HomepageURL != "https://actualbudget.org" {
		t.Fatalf("actual-budget = %+v", budget)
	}
	if plausible.LogoURL != "" || len(plausible.Files) != 3 ||
		!strings.Contains(plausible.Files["clickhouse/logs.xml"], "<level>warning</level>") || plausible.Files["Caddyfile"] == "" {
		t.Fatalf("plausible = %+v", plausible)
	}
	for _, e := range f.Apps {
		if _, _, err := e.Check(); err != nil {
			t.Fatalf("%s does not pass the sync's check: %v", e.Slug, err)
		}
	}
	logo, err := os.ReadFile(filepath.Join(o.Out, LogoDir, "actual-budget.svg"))
	if err != nil || !bytes.HasPrefix(logo, []byte("<svg")) {
		t.Fatalf("logo = %q, %v", logo, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(o.Out, LogoDir)); len(entries) != 1 {
		t.Fatalf("logos = %v", entries)
	}

	// The same tree builds the same bytes.
	again := o
	again.Out = t.TempDir()
	if _, err := Build(again); err != nil {
		t.Fatal(err)
	}
	if _, raw2 := readFeed(t, again.Out); !bytes.Equal(raw, raw2) {
		t.Fatal("two builds of one tree differ")
	}
}

// Without a key no .sig is written, and one left from an earlier signed
// build is removed with the logos it no longer lists.
func TestBuildUnsigned(t *testing.T) {
	o := options(t, fixture)
	for _, f := range []string{SigFile, LogoDir + "/gone.png"} {
		p := filepath.Join(o.Out, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Build(o); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{SigFile, LogoDir + "/gone.png"} {
		if _, err := os.Stat(filepath.Join(o.Out, filepath.FromSlash(f))); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s: %v", f, err)
		}
	}
	readFeed(t, o.Out)
}

// Every invalid app is listed, and nothing is written.
func TestBuildListsEveryBadApp(t *testing.T) {
	apps := copyFixture(t)
	if err := os.Remove(filepath.Join(apps, "apps/plausible/compose/Caddyfile")); err != nil {
		t.Fatal(err)
	}
	budget := filepath.Join(apps, "apps/actual-budget/compose/compose.yml")
	raw, _ := os.ReadFile(budget)
	if err := os.WriteFile(budget, bytes.Replace(raw, []byte("volumes:\n  data:\n"), []byte("volumes:\n  data: { name: budget }\n"), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(apps, "apps/renamed/compose"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"apps/README.md":                     "not an app folder",
		"apps/insecure/metadata.json":        `{"name": "Insecure", "homepage_url": "http://insecure.test"}`,
		"apps/insecure/compose/compose.yml":  "services: {app: {image: x}}\n",
		"apps/warned/metadata.json":          `{"name": "Warned"}`,
		"apps/warned/compose/compose.yml":    "services: {app: {image: x}}\nx-winterflow: {variables: {UNUSED: {}}}\n",
		"apps/renamed/metadata.json":         `{"slug": "other", "name": "Renamed"}`,
		"apps/renamed/compose/compose.yml":   "services: {app: {image: x}}\n",
		"apps/traefik-ingress/metadata.json": `{"name": "Traefik", "extension": "expose"`,
	} {
		p := filepath.Join(apps, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	o := options(t, apps)
	rep, err := Build(o)
	var be *BuildError
	if !errors.As(err, &be) || len(be.Problems) != 5 {
		t.Fatalf("err = %v", err)
	}
	for i, want := range []string{
		"actual-budget: invalid x-winterflow: volumes.data: the name \"budget\" would be shared",
		"insecure: invalid catalog: homepage_url: \"http://insecure.test\" is not an https URL",
		"plausible: x-winterflow.files: Caddyfile: no such file or directory",
		"renamed: metadata.json: slug \"other\" is not the folder name",
		"traefik-ingress: metadata.json: unexpected end of JSON input",
	} {
		if !strings.HasPrefix(be.Problems[i], want) {
			t.Errorf("problem %d = %q, want %q", i, be.Problems[i], want)
		}
	}
	if !strings.HasPrefix(err.Error(), "5 apps are invalid:\n  actual-budget: ") {
		t.Fatalf("message = %q", err)
	}
	// The valid apps' warnings are still reported.
	if !reflect.DeepEqual(rep.Warnings, []string{"warned: no logo", "warned: x-winterflow.variables.UNUSED: no service environment and no override uses it"}) {
		t.Fatalf("warnings = %q", rep.Warnings)
	}
	if entries, _ := os.ReadDir(o.Out); len(entries) != 0 {
		t.Fatalf("a failed build wrote %v", entries)
	}
}

func TestBuildRefuses(t *testing.T) {
	o := options(t, fixture)
	o.BaseURL = "http://winterflow.io/catalog/"
	if _, err := Build(o); err == nil || !strings.Contains(err.Error(), "--base-url") {
		t.Fatalf("an http base URL: %v", err)
	}
	o = options(t, t.TempDir())
	if _, err := Build(o); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("no apps folder: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(o.Apps, "apps", "gitea"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(o); err == nil || err.Error() != "no installable app" {
		t.Fatalf("nothing installable: %v", err)
	}
}

func TestCLI(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	seed := base64.StdEncoding.EncodeToString(key.Seed())
	env := map[string]string{"CATALOG_SIGNING_KEY": seed, "BAD_KEY": "not base64"}
	run := func(args ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := Main(args, &stdout, &stderr, func(k string) string { return env[k] })
		return code, stdout.String(), stderr.String()
	}
	out := t.TempDir()
	code, stdout, stderr := run("build", "--apps", fixture, "--out", out, "--base-url", "https://winterflow.io/catalog", "--sign-key-env", "CATALOG_SIGNING_KEY")
	if code != 0 || !strings.Contains(stdout, "skipped gitea: no metadata.json\n") || !strings.Contains(stdout, "warning: plausible: no logo\n") ||
		!strings.HasSuffix(stdout, "wrote catalog.json: 2 apps, signed\n") || stderr != "" {
		t.Fatalf("build = %d\n%s\n%s", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(out, SigFile)); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args []string
		code int
		err  string
	}{
		{[]string{"build", "--apps", fixture, "--out", out, "--base-url", "https://x.test", "--sign-key-env", "UNSET"}, 1, "UNSET is not set"},
		{[]string{"build", "--apps", fixture, "--out", out, "--base-url", "https://x.test", "--sign-key-env", "BAD_KEY"}, 1, "BAD_KEY: catalog signing key"},
		{[]string{"build", "--apps", t.TempDir(), "--out", out, "--base-url", "https://x.test"}, 1, "catalog build: open"},
		{[]string{"build", "--apps", fixture}, 2, "usage:"},
		{[]string{"build", "--nope"}, 2, "flag provided but not defined"},
		{nil, 2, "usage:"},
		{[]string{"publish"}, 2, "usage:"},
	} {
		code, _, stderr := run(c.args...)
		if code != c.code || !strings.Contains(stderr, c.err) {
			t.Errorf("%v = %d, %q", c.args, code, stderr)
		}
	}
	code, stdout, _ = run("keygen")
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if code != 0 || len(lines) != 2 || !strings.HasPrefix(lines[0], "CATALOG_PUBLIC_KEY=") || !strings.HasPrefix(lines[1], "CATALOG_SIGNING_KEY=") {
		t.Fatalf("keygen = %d %q", code, stdout)
	}
	pub, err := feed.ParsePublicKey(strings.TrimPrefix(lines[0], "CATALOG_PUBLIC_KEY="))
	if err != nil {
		t.Fatal(err)
	}
	priv, err := feed.ParsePrivateKey(strings.TrimPrefix(lines[1], "CATALOG_SIGNING_KEY="))
	if err != nil || !pub.Equal(priv.Public()) {
		t.Fatalf("keygen pair: %v", err)
	}
}
````

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/app/catalog/`
Expected: FAIL to build: `undefined: Options`, `undefined: Build`, `undefined: FeedFile`.

- [ ] **Step 4: Write the builder and the command**

Create `internal/app/catalog/build.go`:

````go
// Package catalog builds the catalog feed from a winterflow-apps checkout
// (cmd/catalog): catalog.json, its signature and the logos. Every entry
// passes the same check the API's sync applies (feed.Entry.Check), so a
// feed that builds installs.
package catalog

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	feed "winterflow/internal/domain/catalog"
	"winterflow/internal/domain/compose/xwinterflow"
	"winterflow/internal/domain/template"
)

// Output file names under Options.Out.
const (
	FeedFile = "catalog.json"
	SigFile  = "catalog.json.sig"
	LogoDir  = "logos"
)

// Options is one build.
type Options struct {
	// Apps is the winterflow-apps checkout: apps/<slug>/ and assets/.
	Apps string
	// Out receives catalog.json, catalog.json.sig and logos/.
	Out string
	// BaseURL is where Out is published (https); logo URLs are under it.
	BaseURL string
	// SigningKey signs the feed; nil writes no .sig (a CI check).
	SigningKey ed25519.PrivateKey
	// SourceCommit is the apps checkout's commit ("" when unknown).
	SourceCommit string
	// Now stamps generated_at (nil: time.Now).
	Now func() time.Time
}

// Skip is an apps folder the feed leaves out, and why.
type Skip struct {
	Slug   string
	Reason string
}

// Report is what a build found.
type Report struct {
	// Apps are the slugs in the feed, sorted.
	Apps []string
	// Skipped are the folders that are not installable apps.
	Skipped []Skip
	// Warnings are the template warnings and the missing logos, each
	// prefixed with its slug: printed, never fatal.
	Warnings []string
}

// BuildError lists every app that failed the check: the build writes
// nothing then.
type BuildError struct {
	Problems []string
}

func (e *BuildError) Error() string {
	return fmt.Sprintf("%d apps are invalid:\n  %s", len(e.Problems), strings.Join(e.Problems, "\n  "))
}

// metadata is what the builder reads of apps/<slug>/metadata.json.
type metadata struct {
	Slug             string   `json:"slug"`
	Name             string   `json:"name"`
	Summary          string   `json:"summary"`
	Description      string   `json:"description"`
	Keywords         []string `json:"keywords"`
	Tags             []string `json:"tags"`
	AlternativeTo    []string `json:"alternative_to"`
	License          string   `json:"license"`
	HomepageURL      string   `json:"homepage_url"`
	RepositoryURL    string   `json:"repository_url"`
	DocumentationURL string   `json:"documentation_url"`
	Logo             string   `json:"logo"`
	WaitingList      bool     `json:"waiting_list"`
	Extension        string   `json:"extension"`
}

// logoExtRE is the extension a copied logo keeps.
var logoExtRE = regexp.MustCompile(`^\.[a-z0-9]{1,5}$`)

// Build reads every apps/<slug>/ of o.Apps and writes the feed. An
// installable app has metadata.json and compose/compose.yml, is not on the
// waiting list and is not an agent extension; any other folder is skipped
// with its reason. Any invalid app fails the build (a *BuildError naming
// every one) before anything is written.
func Build(o Options) (Report, error) {
	base, err := url.Parse(o.BaseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" {
		return Report{}, fmt.Errorf("--base-url %q is not an https URL", o.BaseURL)
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	dirs, err := os.ReadDir(filepath.Join(o.Apps, "apps"))
	if err != nil {
		return Report{}, err
	}
	var (
		rep      Report
		problems []string
		entries  []feed.Entry
		logos    = map[string]string{} // output name -> source path
	)
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		slug := d.Name()
		e, logo, skip, err := readApp(o.Apps, slug)
		switch {
		case err != nil:
			problems = append(problems, slug+": "+err.Error())
			continue
		case skip != "":
			rep.Skipped = append(rep.Skipped, Skip{Slug: slug, Reason: skip})
			continue
		}
		logoName := ""
		if logo != "" {
			logoName = slug + strings.ToLower(filepath.Ext(logo))
			e.LogoURL = base.JoinPath(LogoDir, logoName).String()
		}
		e.Digest = feed.Digest(e.Files)
		body, _, err := e.Check()
		if err != nil {
			problems = append(problems, slug+": "+err.Error())
			continue
		}
		if logoName == "" {
			rep.Warnings = append(rep.Warnings, slug+": no logo")
		} else {
			logos[logoName] = logo
		}
		spec, _ := template.Parse(body) // Check validated it
		for _, w := range template.Warnings(body, spec, nil) {
			rep.Warnings = append(rep.Warnings, slug+": "+w)
		}
		entries = append(entries, e)
		rep.Apps = append(rep.Apps, slug)
	}
	if len(problems) > 0 {
		return rep, &BuildError{Problems: problems}
	}
	if len(entries) == 0 {
		return rep, errors.New("no installable app")
	}
	slices.SortFunc(entries, func(a, b feed.Entry) int { return strings.Compare(a.Slug, b.Slug) })
	slices.Sort(rep.Apps)
	raw, err := feed.Encode(feed.Feed{
		Format: feed.FormatVersion, SourceCommit: o.SourceCommit,
		GeneratedAt: now().UTC().Truncate(time.Second), Apps: entries,
	})
	if err != nil {
		return rep, err
	}
	return rep, write(o, raw, logos)
}

// readApp reads one apps folder: its entry (without digest and logo URL)
// and its logo's path ("" when it has none), or why it is skipped.
func readApp(root, slug string) (e feed.Entry, logo, skip string, err error) {
	dir := filepath.Join(root, "apps", slug)
	rawMeta, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return feed.Entry{}, "", "no metadata.json", nil
	}
	if err != nil {
		return feed.Entry{}, "", "", err
	}
	composeDir := filepath.Join(dir, "compose")
	compose, err := os.ReadFile(filepath.Join(composeDir, "compose.yml"))
	if errors.Is(err, fs.ErrNotExist) {
		return feed.Entry{}, "", "no compose/compose.yml", nil
	}
	if err != nil {
		return feed.Entry{}, "", "", err
	}
	var m metadata
	if err := json.Unmarshal(rawMeta, &m); err != nil {
		return feed.Entry{}, "", "", fmt.Errorf("metadata.json: %v", err)
	}
	switch {
	case m.WaitingList:
		return feed.Entry{}, "", "on the waiting list", nil
	case m.Extension != "":
		return feed.Entry{}, "", fmt.Sprintf("an agent extension (%s), not an app", m.Extension), nil
	case m.Slug != "" && m.Slug != slug:
		return feed.Entry{}, "", "", fmt.Errorf("metadata.json: slug %q is not the folder name", m.Slug)
	}
	files := map[string]string{"compose.yml": string(compose)}
	spec, err := xwinterflow.Parse(xwinterflow.ModeTemplate, compose)
	if err != nil {
		return feed.Entry{}, "", "", err
	}
	for _, f := range spec.Files {
		b, err := os.ReadFile(filepath.Join(composeDir, filepath.FromSlash(f)))
		if err != nil {
			return feed.Entry{}, "", "", fmt.Errorf("x-winterflow.files: %s: %v", f, errors.Unwrap(err))
		}
		files[f] = string(b)
	}
	if m.Logo != "" && logoExtRE.MatchString(strings.ToLower(path.Ext(m.Logo))) {
		p := filepath.Join(root, "assets", filepath.FromSlash(path.Clean("/"+m.Logo)))
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			logo = p
		}
	}
	return feed.Entry{
		Slug: slug, Name: m.Name, Summary: m.Summary, Description: m.Description,
		Keywords: m.Keywords, Tags: m.Tags, AlternativeTo: m.AlternativeTo, License: m.License,
		HomepageURL: m.HomepageURL, RepositoryURL: m.RepositoryURL, DocumentationURL: m.DocumentationURL,
		Files: files,
	}, logo, "", nil
}

// write puts the feed, its signature and the logos under o.Out; logos/ is
// replaced as a whole, so a dropped app's logo goes with it.
func write(o Options, raw []byte, logos map[string]string) error {
	logoDir := filepath.Join(o.Out, LogoDir)
	if err := os.RemoveAll(logoDir); err != nil {
		return err
	}
	if err := os.MkdirAll(logoDir, 0o755); err != nil {
		return err
	}
	for name, src := range logos {
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(logoDir, name), b, 0o644); err != nil {
			return err
		}
	}
	sigPath := filepath.Join(o.Out, SigFile)
	if o.SigningKey != nil {
		if err := os.WriteFile(sigPath, feed.Sign(o.SigningKey, raw), 0o644); err != nil {
			return err
		}
	} else if err := os.Remove(sigPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.WriteFile(filepath.Join(o.Out, FeedFile), raw, 0o644)
}
````

Create `internal/app/catalog/cli.go`:

````go
package catalog

import (
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"

	feed "winterflow/internal/domain/catalog"
)

const usage = `usage:
  catalog build --apps DIR --out DIR --base-url URL [--sign-key-env NAME] [--source-commit SHA]
  catalog keygen
`

// Main runs cmd/catalog with args (the program name left out) and returns
// its exit code: 0 done, 1 failed, 2 a usage error.
func Main(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "build":
		return build(args[1:], stdout, stderr, getenv)
	case "keygen":
		pub, seed, err := feed.GenerateKey(rand.Reader)
		if err != nil {
			fmt.Fprintln(stderr, "catalog keygen:", err)
			return 1
		}
		fmt.Fprintf(stdout, "CATALOG_PUBLIC_KEY=%s\nCATALOG_SIGNING_KEY=%s\n", pub, seed)
		return 0
	}
	fmt.Fprint(stderr, usage)
	return 2
}

func build(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("catalog build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o Options
	var keyEnv string
	fs.StringVar(&o.Apps, "apps", "", "the winterflow-apps checkout (apps/ and assets/)")
	fs.StringVar(&o.Out, "out", "", "the output folder")
	fs.StringVar(&o.BaseURL, "base-url", "", "where the output folder is published (https)")
	fs.StringVar(&keyEnv, "sign-key-env", "", "the environment variable holding the signing key (base64 seed)")
	fs.StringVar(&o.SourceCommit, "source-commit", "", "the apps checkout's commit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if o.Apps == "" || o.Out == "" || o.BaseURL == "" || fs.NArg() > 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if keyEnv != "" {
		v := getenv(keyEnv)
		if v == "" {
			fmt.Fprintf(stderr, "catalog build: %s is not set\n", keyEnv)
			return 1
		}
		key, err := feed.ParsePrivateKey(v)
		if err != nil {
			fmt.Fprintf(stderr, "catalog build: %s: %v\n", keyEnv, err)
			return 1
		}
		o.SigningKey = key
	}
	rep, err := Build(o)
	for _, s := range rep.Skipped {
		fmt.Fprintf(stdout, "skipped %s: %s\n", s.Slug, s.Reason)
	}
	for _, w := range rep.Warnings {
		fmt.Fprintf(stdout, "warning: %s\n", w)
	}
	if err != nil {
		var be *BuildError
		if !errors.As(err, &be) {
			err = fmt.Errorf("catalog build: %w", err)
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	signed := "unsigned"
	if o.SigningKey != nil {
		signed = "signed"
	}
	fmt.Fprintf(stdout, "wrote %s: %d apps, %s\n", FeedFile, len(rep.Apps), signed)
	return 0
}
````

Create `cmd/catalog/main.go`:

````go
// Command catalog builds the catalog feed from a winterflow-apps checkout
// (`catalog build`) and makes its signing key pair (`catalog keygen`); see
// internal/app/catalog.
package main

import (
	"os"

	"winterflow/internal/app/catalog"
)

func main() {
	os.Exit(catalog.Main(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}
````

- [ ] **Step 5: Run the tests, then the command against the fixture**

Run: `go vet ./internal/app/catalog/ ./cmd/catalog/ && go test -cover ./internal/app/catalog/`
Expected: PASS, about 93% of statements (the rest are I/O errors).

Run:

```bash
eval "$(go run ./cmd/catalog keygen | sed 's/^/export /')"
go run ./cmd/catalog build --apps internal/app/catalog/testdata/winterflow-apps --out /tmp/catalog-out \
  --base-url https://winterflow.io/catalog/ --sign-key-env CATALOG_SIGNING_KEY --source-commit 49bf28d
ls -R /tmp/catalog-out
```

Expected:

```text
skipped coming-soon: on the waiting list
skipped gitea: no metadata.json
skipped no-compose: no compose/compose.yml
skipped traefik-ingress: an agent extension (expose), not an app
warning: plausible: no logo
wrote catalog.json: 2 apps, signed
/tmp/catalog-out:
catalog.json
catalog.json.sig
logos

/tmp/catalog-out/logos:
actual-budget.svg
```

- [ ] **Step 6: Commit**

```bash
git add internal/app/catalog cmd/catalog
git commit -m "feat(catalog): cmd/catalog builds and signs the feed from a winterflow-apps checkout" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 4: Migration 0017, sqlc and the repository's catalog writes

A catalog template keeps what the catalog says about it (`templates.catalog`, the `model.CatalogMeta` JSON) and the `generated_at` of the newest feed applied to it (`templates.catalog_feed_at`). Every sync write is conditional on the base the sync read and on that feed time, so two replicas never add two revisions and an older feed never rolls a template back.

**Files:**
- Create: `internal/infra/db/migrations/sqlite/0017_catalog_meta.sql`
- Create: `internal/infra/db/migrations/pgsql/0017_catalog_meta.sql`
- Modify: `internal/infra/db/sqlc/query/sqlite/templates.sql`
- Modify: `internal/infra/db/sqlc/query/pgsql/templates.sql`
- Regenerate: `internal/infra/db/sqlc/gen/{sqlite,pgsql}/{models.go,querier.go,templates.sql.go}` (`make sqlc`; never edited by hand)
- Modify: `internal/domain/model/catalog.go` (`CatalogState`, `CatalogEntry`, `CatalogOutcome`)
- Modify: `internal/domain/model/template.go` (`Template.CatalogMeta`)
- Create: `internal/domain/port/catalog.go` (`CatalogRepository`)
- Create: `internal/infra/db/repository/template/catalog.go`
- Modify: `internal/infra/db/repository/template/repo.go`, `sqlite.go`, `pgsql.go`
- Test: `internal/infra/db/repository/template/catalog_sync_test.go` (create)
- Test: `internal/infra/db/repository/timestamps_test.go` (writes the new TIMESTAMP column)
- Test: `internal/infra/db/sqlc/shapes_test.go` (the new row and parameter types)

**Interfaces:**
- Consumes: Task 2's `model.CatalogMeta` (`Normalized`, JSON tags); the existing `InsertTemplate`, `insertRevision`, `hydrateTemplate`, `util.GenerateID`, `types.DateTime`.
- Produces:
  - Columns `templates.catalog TEXT NOT NULL DEFAULT ''` and `templates.catalog_feed_at TIMESTAMP` (NULL for org rows and for catalog rows no sync wrote).
  - `model.Template.CatalogMeta *CatalogMeta` (nil for an org template; non-nil, lists never nil, for a catalog one) on `GetTemplate` and `ListTemplates`; `CreateTemplate` stores it for a catalog template.
  - `model.CatalogState{ID, Slug, Name string; Meta CatalogMeta; LatestRevision int64; Digest string; WithdrawnAt, FeedAt *time.Time}`; `model.CatalogEntry{Slug, Name string; Meta CatalogMeta; Body []byte; Digest string; FeedAt time.Time}`; `type CatalogOutcome string` with `CatalogInserted`, `CatalogRevised`, `CatalogUpdated`, `CatalogSkipped`.
  - `port.CatalogRepository` (implemented by `*template.Repo`):
    - `ListCatalog(ctx context.Context) ([]model.CatalogState, error)` (by slug, with the latest revision's digest)
    - `ApplyCatalog(ctx context.Context, base *model.CatalogState, e model.CatalogEntry) (model.CatalogOutcome, error)`
    - `WithdrawCatalog(ctx context.Context, id string, at, feedAt time.Time) (bool, error)`
    - `StampCatalog(ctx context.Context, feedAt time.Time) error`
  - sqlc queries `ListCatalogTemplates`, `BumpCatalogTemplate`, `UpdateCatalogTemplate`, `WithdrawCatalogTemplate`, `StampCatalogTemplates`; `InsertTemplate` and `ListTemplates` gain `catalog` (and `InsertTemplate` `catalog_feed_at`).

- [ ] **Step 1: Write the failing tests**

Create `internal/infra/db/repository/template/catalog_sync_test.go`:

````go
package template

import (
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db/dbtest"
)

var (
	feed1 = time.Date(2026, 10, 14, 20, 0, 0, 0, time.UTC)
	feed2 = feed1.Add(24 * time.Hour)
)

// catalogEntry is a feed entry for slug with digest d.
func catalogEntry(slug, name, d string, at time.Time) model.CatalogEntry {
	return model.CatalogEntry{
		Slug: slug, Name: name, Digest: d, FeedAt: at, Body: []byte(`{"files":{"compose.yml":"` + d + `"}}`),
		Meta: model.CatalogMeta{Summary: "About " + name, Keywords: []string{"k"}, LogoURL: "https://winterflow.io/catalog/logos/" + slug + ".png"},
	}
}

// state is the sync state of slug (fails the test when there is none).
func state(t *testing.T, r *Repo, slug string) *model.CatalogState {
	t.Helper()
	list, err := r.ListCatalog(dbtest.Ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	for i := range list {
		if list[i].Slug == slug {
			return &list[i]
		}
	}
	t.Fatalf("no catalog template %q in %+v", slug, list)
	return nil
}

func apply(t *testing.T, r *Repo, base *model.CatalogState, e model.CatalogEntry) model.CatalogOutcome {
	t.Helper()
	out, err := r.ApplyCatalog(dbtest.Ctx(t), base, e)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A new slug inserts revision 1; a digest change adds a revision; a name
// or metadata change is made in place; the metadata reads back on the
// template every org sees.
func TestApplyCatalog(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	if got := apply(t, r, nil, catalogEntry("budget", "Budget", "sha256:a", feed1)); got != model.CatalogInserted {
		t.Fatalf("insert = %s", got)
	}
	st := state(t, r, "budget")
	if st.Name != "Budget" || st.LatestRevision != 1 || st.Digest != "sha256:a" || st.WithdrawnAt != nil ||
		st.FeedAt == nil || !st.FeedAt.Equal(feed1) || st.Meta.Summary != "About Budget" || st.Meta.Tags == nil {
		t.Fatalf("state = %+v", st)
	}
	tpl, err := r.GetTemplate(ctx, org2, st.ID)
	if err != nil || !tpl.Catalog() || tpl.CatalogMeta == nil || tpl.CatalogMeta.LogoURL != "https://winterflow.io/catalog/logos/budget.png" {
		t.Fatalf("template = %+v, %v", tpl, err)
	}
	if list, _ := r.ListTemplates(ctx, org1); len(list) != 1 || list[0].CatalogMeta == nil || list[0].CatalogMeta.Summary != "About Budget" {
		t.Fatalf("list = %+v", list)
	}

	renamed := catalogEntry("budget", "Actual Budget", "sha256:a", feed2)
	renamed.Meta.Description = "Fixed."
	if got := apply(t, r, st, renamed); got != model.CatalogUpdated {
		t.Fatalf("rename = %s", got)
	}
	st = state(t, r, "budget")
	if st.Name != "Actual Budget" || st.LatestRevision != 1 || st.Meta.Description != "Fixed." || !st.FeedAt.Equal(feed2) {
		t.Fatalf("after the rename = %+v", st)
	}

	if got := apply(t, r, st, catalogEntry("budget", "Actual Budget", "sha256:b", feed2)); got != model.CatalogRevised {
		t.Fatalf("revise = %s", got)
	}
	st = state(t, r, "budget")
	rev, err := r.GetRevision(ctx, st.ID, 2)
	if err != nil || st.LatestRevision != 2 || st.Digest != "sha256:b" || rev.Digest != "sha256:b" || string(rev.Body) != `{"files":{"compose.yml":"sha256:b"}}` {
		t.Fatalf("after the revision = %+v, %+v, %v", st, rev, err)
	}
	if revs, _ := r.ListRevisions(ctx, st.ID); len(revs) != 2 {
		t.Fatalf("revisions = %+v", revs)
	}
}

// A withdrawn template leaves the lists of orgs without instances; the
// next feed that lists it again brings it back.
func TestWithdrawAndRestoreCatalog(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	apply(t, r, nil, catalogEntry("budget", "Budget", "sha256:a", feed1))
	st := state(t, r, "budget")
	at := feed2.Add(time.Minute)
	if ok, err := r.WithdrawCatalog(ctx, st.ID, at, feed2); err != nil || !ok {
		t.Fatalf("withdraw = %v, %v", ok, err)
	}
	if ok, err := r.WithdrawCatalog(ctx, st.ID, at.Add(time.Hour), feed2); err != nil || ok {
		t.Fatalf("withdraw twice = %v, %v", ok, err)
	}
	st = state(t, r, "budget")
	if st.WithdrawnAt == nil || !st.WithdrawnAt.Equal(at) {
		t.Fatalf("withdrawn = %+v", st)
	}
	if list, _ := r.ListTemplates(ctx, org1); len(list) != 0 {
		t.Fatalf("a withdrawn template is listed: %+v", list)
	}
	if got := apply(t, r, st, catalogEntry("budget", "Budget", "sha256:a", feed2)); got != model.CatalogUpdated {
		t.Fatalf("restore = %s", got)
	}
	if st = state(t, r, "budget"); st.WithdrawnAt != nil || st.LatestRevision != 1 {
		t.Fatalf("restored = %+v", st)
	}
}

// A write from an older feed than the one that wrote the template, or
// from a stale base, is skipped and changes nothing: a stale mirror never
// rolls a template back, and two replicas never add two revisions.
func TestApplyCatalogSkipsStaleWrites(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	apply(t, r, nil, catalogEntry("budget", "Budget", "sha256:a", feed1))
	base := state(t, r, "budget")
	apply(t, r, base, catalogEntry("budget", "Budget", "sha256:b", feed2))

	for name, c := range map[string]struct {
		base *model.CatalogState
		e    model.CatalogEntry
	}{
		"the slug inserted meanwhile":    {nil, catalogEntry("budget", "Budget", "sha256:c", feed2)},
		"a stale base":                   {base, catalogEntry("budget", "Budget", "sha256:c", feed2)},
		"an older feed's files":          {state(t, r, "budget"), catalogEntry("budget", "Budget", "sha256:a", feed1)},
		"an older feed's name":           {state(t, r, "budget"), catalogEntry("budget", "Old name", "sha256:b", feed1)},
		"an older feed's slug, inserted": {nil, catalogEntry("budget", "Budget", "sha256:a", feed1)},
	} {
		if got := apply(t, r, c.base, c.e); got != model.CatalogSkipped {
			t.Errorf("%s: %s", name, got)
		}
	}
	if st := state(t, r, "budget"); st.LatestRevision != 2 || st.Digest != "sha256:b" || st.Name != "Budget" {
		t.Fatalf("after the stale writes = %+v", st)
	}
	if ok, err := r.WithdrawCatalog(ctx, base.ID, feed2, feed1); err != nil || ok {
		t.Fatalf("an older feed withdrew it: %v, %v", ok, err)
	}
}

// StampCatalog moves every catalog template's feed forward, never back;
// a template no sync wrote (inserted by CreateTemplate) gets one too.
func TestStampCatalog(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCatalog(t, r, cat1, "manual", "Manual", nil)
	apply(t, r, nil, catalogEntry("budget", "Budget", "sha256:a", feed2))
	if err := r.StampCatalog(ctx, feed1); err != nil {
		t.Fatal(err)
	}
	if st := state(t, r, "budget"); !st.FeedAt.Equal(feed2) {
		t.Fatalf("stamped back to %v", st.FeedAt)
	}
	if st := state(t, r, "manual"); st.FeedAt == nil || !st.FeedAt.Equal(feed1) || st.Meta.Keywords == nil {
		t.Fatalf("manual = %+v", st)
	}
	if err := r.StampCatalog(ctx, feed2.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if st := state(t, r, "budget"); !st.FeedAt.Equal(feed2.Add(time.Hour)) {
		t.Fatalf("not stamped forward: %v", st.FeedAt)
	}
}

// Replicas applying the same feed at once: one write wins each step, the
// others are skipped, and no revision is added twice (on Postgres the
// transactions really overlap).
func TestApplyCatalogRace(t *testing.T) {
	r, _ := newRepo(t)
	race := func(base *model.CatalogState, e model.CatalogEntry) map[model.CatalogOutcome]int {
		var (
			wg  sync.WaitGroup
			mu  sync.Mutex
			got = map[model.CatalogOutcome]int{}
		)
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out, err := r.ApplyCatalog(dbtest.Ctx(t), base, e)
				if err != nil {
					t.Error(err)
				}
				mu.Lock()
				got[out]++
				mu.Unlock()
			}()
		}
		wg.Wait()
		return got
	}
	if got := race(nil, catalogEntry("budget", "Budget", "sha256:a", feed1)); got[model.CatalogInserted] != 1 || got[model.CatalogSkipped] != 3 {
		t.Fatalf("inserts = %v", got)
	}
	if got := race(state(t, r, "budget"), catalogEntry("budget", "Budget", "sha256:b", feed2)); got[model.CatalogRevised] != 1 || got[model.CatalogSkipped] != 3 {
		t.Fatalf("revisions = %v", got)
	}
	if st := state(t, r, "budget"); st.LatestRevision != 2 || st.Digest != "sha256:b" {
		t.Fatalf("after the race = %+v", st)
	}
}
````

Modify `internal/infra/db/repository/timestamps_test.go` (`git apply` this diff):

````diff
diff --git a/internal/infra/db/repository/timestamps_test.go b/internal/infra/db/repository/timestamps_test.go
index 25c619c..39a2292 100644
--- a/internal/infra/db/repository/timestamps_test.go
+++ b/internal/infra/db/repository/timestamps_test.go
@@ -147,6 +147,10 @@ func TestSQLiteTimestampsAreDateTimeText(t *testing.T) {
 		model.TemplateRevision{Body: []byte(`{}`)}); err != nil {
 		t.Fatal(err)
 	}
+	// catalog_feed_at: a catalog template the sync wrote.
+	if _, err := templates.ApplyCatalog(ctx, nil, model.CatalogEntry{Slug: "fed", Name: "Fed", Body: []byte(`{}`), Digest: "sha256:x", FeedAt: at}); err != nil {
+		t.Fatal(err)
+	}
 	if _, _, err := templates.ClaimInstance(ctx, model.Instance{
 		ID: dbtest.ID(502), OrgID: orgID, Key: "k", AppID: appID, ServerID: srv.ID, TemplateID: tplID, Revision: 1, RequestHash: "h",
 	}); err != nil {
````

Modify `internal/infra/db/sqlc/shapes_test.go` (`git apply` this diff):

````diff
diff --git a/internal/infra/db/sqlc/shapes_test.go b/internal/infra/db/sqlc/shapes_test.go
index e3a25c1..f33f8de 100644
--- a/internal/infra/db/sqlc/shapes_test.go
+++ b/internal/infra/db/sqlc/shapes_test.go
@@ -258,6 +258,14 @@ var (
 	_ = pgsqlgen.GetLiveTemplateInstanceForAppParams(sqlitegen.GetLiveTemplateInstanceForAppParams{})
 	_ = sqlitegen.RetireTemplateInstancesForAppParams(pgsqlgen.RetireTemplateInstancesForAppParams{})
 	_ = pgsqlgen.RetireTemplateInstancesForAppParams(sqlitegen.RetireTemplateInstancesForAppParams{})
+	_ = sqlitegen.ListCatalogTemplatesRow(pgsqlgen.ListCatalogTemplatesRow{})
+	_ = pgsqlgen.ListCatalogTemplatesRow(sqlitegen.ListCatalogTemplatesRow{})
+	_ = sqlitegen.BumpCatalogTemplateParams(pgsqlgen.BumpCatalogTemplateParams{})
+	_ = pgsqlgen.BumpCatalogTemplateParams(sqlitegen.BumpCatalogTemplateParams{})
+	_ = sqlitegen.UpdateCatalogTemplateParams(pgsqlgen.UpdateCatalogTemplateParams{})
+	_ = pgsqlgen.UpdateCatalogTemplateParams(sqlitegen.UpdateCatalogTemplateParams{})
+	_ = sqlitegen.WithdrawCatalogTemplateParams(pgsqlgen.WithdrawCatalogTemplateParams{})
+	_ = pgsqlgen.WithdrawCatalogTemplateParams(sqlitegen.WithdrawCatalogTemplateParams{})
 	_ = sqlitegen.ListUpgradableInstancesParams(pgsqlgen.ListUpgradableInstancesParams{})
 	_ = pgsqlgen.ListUpgradableInstancesParams(sqlitegen.ListUpgradableInstancesParams{})
 	_ = sqlitegen.ServerInOrganizationParams(pgsqlgen.ServerInOrganizationParams{})
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/infra/db/repository/... ./internal/infra/db/sqlc/`
Expected: FAIL to build: `undefined: model.CatalogEntry`, `r.ListCatalog undefined`, `templates.ApplyCatalog undefined`, and in `sqlc` `undefined: sqlitegen.ListCatalogTemplatesRow`.

- [ ] **Step 3: Write the model types and the port**

Modify `internal/domain/model/catalog.go`, `internal/domain/model/template.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/model/catalog.go b/internal/domain/model/catalog.go
index fae3f40..5ad1da1 100644
--- a/internal/domain/model/catalog.go
+++ b/internal/domain/model/catalog.go
@@ -1,6 +1,9 @@
 package model
 
-import "slices"
+import (
+	"slices"
+	"time"
+)
 
 // CatalogMeta is what the catalog says about a catalog template besides
 // its name and files: the gallery's text, search words and links. The
@@ -37,3 +40,45 @@ func (m CatalogMeta) Normalized() CatalogMeta {
 	}
 	return m
 }
+
+// CatalogState is what the catalog sync reads of one catalog template.
+type CatalogState struct {
+	ID             string
+	Slug           string
+	Name           string
+	Meta           CatalogMeta
+	LatestRevision int64
+	// Digest is the latest revision's.
+	Digest      string
+	WithdrawnAt *time.Time
+	// FeedAt is the generated_at of the newest feed the sync applied to
+	// the template; nil when no sync wrote it.
+	FeedAt *time.Time
+}
+
+// CatalogEntry is one valid feed entry as the sync applies it.
+type CatalogEntry struct {
+	Slug string
+	Name string
+	Meta CatalogMeta
+	// Body is the encoded revision body (files, summary, description).
+	Body   []byte
+	Digest string
+	// FeedAt is the feed's generated_at.
+	FeedAt time.Time
+}
+
+// CatalogOutcome is what applying one entry did.
+type CatalogOutcome string
+
+const (
+	// CatalogInserted: a new catalog template, revision 1.
+	CatalogInserted CatalogOutcome = "inserted"
+	// CatalogRevised: the files changed, a new revision.
+	CatalogRevised CatalogOutcome = "revised"
+	// CatalogUpdated: the name or metadata changed in place, or a
+	// withdrawn template came back.
+	CatalogUpdated CatalogOutcome = "updated"
+	// CatalogSkipped: another replica, or a newer feed, wrote it first.
+	CatalogSkipped CatalogOutcome = "skipped"
+)
diff --git a/internal/domain/model/template.go b/internal/domain/model/template.go
index cb45360..023ce79 100644
--- a/internal/domain/model/template.go
+++ b/internal/domain/model/template.go
@@ -43,7 +43,8 @@ func ManagedByTemplate(name string) error {
 
 // Template is a stack definition; its body lives in revisions. An org
 // template (Source TemplateSourceOrg) belongs to OrgID; a catalog template
-// (TemplateSourceCatalog) has no OrgID, a Slug, and WithdrawnAt once the
+// (TemplateSourceCatalog) has no OrgID, a Slug, CatalogMeta (what the
+// catalog says about it; nil for an org template) and WithdrawnAt once the
 // catalog dropped it.
 type Template struct {
 	ID             string
@@ -53,6 +54,7 @@ type Template struct {
 	Name           string
 	LatestRevision int64
 	WithdrawnAt    *time.Time
+	CatalogMeta    *CatalogMeta
 	CreatedAt      time.Time
 	CreatedBy      string
 	UpdatedAt      time.Time
````

Create `internal/domain/port/catalog.go` (Task 6 appends `CatalogFetcher` to it):

````go
package port

import (
	"context"
	"time"

	"winterflow/internal/domain/model"
)

// CatalogRepository is what the catalog sync needs of the templates
// repository. Every write names the generated_at of the feed it comes from
// (feedAt) and is refused for a template a newer feed already wrote, so an
// older feed, on another replica or after a restart, never rolls a
// template back.
type CatalogRepository interface {
	// ListCatalog returns every catalog template's sync state, by slug.
	ListCatalog(ctx context.Context) ([]model.CatalogState, error)
	// ApplyCatalog brings the catalog template of e.Slug to e in one
	// transaction, from base, the state the sync read (nil: the slug is
	// new): a new template with revision 1; a new revision when the digest
	// differs (latest_revision bumped from base's); else the name and
	// metadata in place. Every path clears withdrawn_at. Losing a race (the
	// slug inserted, the revision bumped, or the row written by a newer
	// feed meanwhile) is model.CatalogSkipped, not an error.
	ApplyCatalog(ctx context.Context, base *model.CatalogState, e model.CatalogEntry) (model.CatalogOutcome, error)
	// WithdrawCatalog sets the template's withdrawn_at to at, unless it is
	// withdrawn already or a feed newer than feedAt wrote it, and reports
	// whether it did.
	WithdrawCatalog(ctx context.Context, id string, at, feedAt time.Time) (bool, error)
	// StampCatalog records feedAt as the feed every catalog template
	// reflects (only ever moving it forward).
	StampCatalog(ctx context.Context, feedAt time.Time) error
}
````

- [ ] **Step 4: Write the migration and the queries, then regenerate**

The same file in both engines (comment-only differences are not needed: both engines take the same two statements):

Create `internal/infra/db/migrations/sqlite/0017_catalog_meta.sql` and, with identical content, `internal/infra/db/migrations/pgsql/0017_catalog_meta.sql`:

````sql
-- The catalog feed (sub-project 3). templates.catalog is what the catalog
-- says about a catalog template besides its name and files (the JSON of
-- model.CatalogMeta: summary, description, keywords, tags, alternative_to,
-- license, URLs, logo_url), updated in place by the sync; '' for an org
-- template. templates.catalog_feed_at is the generated_at of the newest
-- feed the sync applied to the template (NULL for an org template and for
-- a catalog row no sync wrote): every sync write is conditional on it, so
-- an older feed (a stale mirror on another replica, or after a restart)
-- never rolls a template back.
ALTER TABLE templates ADD COLUMN catalog TEXT NOT NULL DEFAULT '';
ALTER TABLE templates ADD COLUMN catalog_feed_at TIMESTAMP;
````

Modify `internal/infra/db/sqlc/query/sqlite/templates.sql`, `internal/infra/db/sqlc/query/pgsql/templates.sql` (`git apply` this diff):

````diff
diff --git a/internal/infra/db/sqlc/query/pgsql/templates.sql b/internal/infra/db/sqlc/query/pgsql/templates.sql
index 8dddc80..ef99738 100644
--- a/internal/infra/db/sqlc/query/pgsql/templates.sql
+++ b/internal/infra/db/sqlc/query/pgsql/templates.sql
@@ -5,9 +5,10 @@
 
 -- name: InsertTemplate :execrows
 -- A taken org name or catalog slug inserts nothing (0 rows): the caller
--- answers 400.
-INSERT INTO templates (id, source, org_id, slug, name, latest_revision, withdrawn_at, created_at, created_by, updated_at)
-VALUES ($1, $2, $3, $4, $5, 1, $6, $7, $8, $9)
+-- answers 400 (save-template) or skips the entry (the catalog sync lost
+-- the race to another replica).
+INSERT INTO templates (id, source, org_id, slug, name, latest_revision, withdrawn_at, catalog, catalog_feed_at, created_at, created_by, updated_at)
+VALUES ($1, $2, $3, $4, $5, 1, $6, $7, $8, $9, $10, $11)
 ON CONFLICT DO NOTHING;
 
 -- name: BumpTemplateRevision :execrows
@@ -29,13 +30,13 @@ SELECT id FROM templates WHERE source = 'org' AND org_id = $1 AND name = $2;
 -- org has live instances of it), each by name. One pass over the org's
 -- live instances: instances counts them, behind those on a revision older
 -- than the latest.
-SELECT t.id, t.source, t.org_id, t.slug, t.name, t.latest_revision, t.withdrawn_at, t.created_at, t.created_by, t.updated_at,
+SELECT t.id, t.source, t.org_id, t.slug, t.name, t.latest_revision, t.withdrawn_at, t.catalog, t.created_at, t.created_by, t.updated_at,
     COUNT(i.id) AS instances,
     CAST(COALESCE(SUM(CASE WHEN i.revision < t.latest_revision THEN 1 ELSE 0 END), 0) AS BIGINT) AS behind
 FROM templates AS t
 LEFT JOIN template_instances AS i ON i.template_id = t.id AND i.org_id = $1 AND i.state IN ('pending', 'created')
 WHERE t.org_id = $2 OR t.source = 'catalog'
-GROUP BY t.id, t.source, t.org_id, t.slug, t.name, t.latest_revision, t.withdrawn_at, t.created_at, t.created_by, t.updated_at
+GROUP BY t.id, t.source, t.org_id, t.slug, t.name, t.latest_revision, t.withdrawn_at, t.catalog, t.created_at, t.created_by, t.updated_at
 HAVING t.source = 'org' OR t.withdrawn_at IS NULL OR COUNT(i.id) > 0
 ORDER BY t.source DESC, t.name ASC, t.id ASC;
 
@@ -56,6 +57,49 @@ WHERE templates.id = $1 AND templates.source = 'org' AND templates.org_id = $2
 -- A deleted template's upgrade rollouts (scope key: <org_id>:<template_id>).
 DELETE FROM rollouts WHERE kind = 'template_upgrade' AND scope_key = $1;
 
+-- name: ListCatalogTemplates :many
+-- Every catalog template with its latest revision's digest: what the
+-- catalog sync compares a feed with.
+SELECT t.id, t.slug, t.name, t.catalog, t.latest_revision, t.withdrawn_at, t.catalog_feed_at, r.digest
+FROM templates AS t
+JOIN template_revisions AS r ON r.template_id = t.id AND r.revision = t.latest_revision
+WHERE t.source = 'catalog'
+ORDER BY t.slug;
+
+-- name: BumpCatalogTemplate :execrows
+-- A catalog template's next revision from the base the sync read, its
+-- name and metadata, withdrawn_at cleared: 0 rows when another replica
+-- moved latest_revision, or a newer feed wrote the template.
+UPDATE templates
+SET latest_revision = latest_revision + 1, name = sqlc.arg(name), catalog = sqlc.arg(catalog), withdrawn_at = NULL,
+    catalog_feed_at = sqlc.arg(feed_at), updated_at = sqlc.arg(updated_at)
+WHERE id = sqlc.arg(id) AND source = 'catalog' AND latest_revision = sqlc.arg(latest_revision)
+  AND (catalog_feed_at IS NULL OR catalog_feed_at <= sqlc.arg(feed_at));
+
+-- name: UpdateCatalogTemplate :execrows
+-- A catalog template's name and metadata in place, withdrawn_at cleared:
+-- 0 rows when a newer feed wrote it.
+UPDATE templates
+SET name = sqlc.arg(name), catalog = sqlc.arg(catalog), withdrawn_at = NULL,
+    catalog_feed_at = sqlc.arg(feed_at), updated_at = sqlc.arg(updated_at)
+WHERE id = sqlc.arg(id) AND source = 'catalog'
+  AND (catalog_feed_at IS NULL OR catalog_feed_at <= sqlc.arg(feed_at));
+
+-- name: WithdrawCatalogTemplate :execrows
+-- A catalog template the feed no longer lists: 0 rows when it is withdrawn
+-- already or a newer feed wrote it.
+UPDATE templates
+SET withdrawn_at = sqlc.arg(withdrawn_at), updated_at = sqlc.arg(updated_at)
+WHERE id = sqlc.arg(id) AND source = 'catalog' AND withdrawn_at IS NULL
+  AND (catalog_feed_at IS NULL OR catalog_feed_at <= sqlc.arg(feed_at));
+
+-- name: StampCatalogTemplates :exec
+-- The feed every catalog template now reflects: a later sync of an older
+-- feed is refused as a whole.
+UPDATE templates
+SET catalog_feed_at = sqlc.arg(feed_at)
+WHERE source = 'catalog' AND (catalog_feed_at IS NULL OR catalog_feed_at < sqlc.arg(feed_at));
+
 -- name: InsertTemplateRevision :exec
 INSERT INTO template_revisions (template_id, revision, body, note, digest, created_at, created_by)
 VALUES ($1, $2, $3, $4, $5, $6, $7);
diff --git a/internal/infra/db/sqlc/query/sqlite/templates.sql b/internal/infra/db/sqlc/query/sqlite/templates.sql
index 17b6257..46c1e51 100644
--- a/internal/infra/db/sqlc/query/sqlite/templates.sql
+++ b/internal/infra/db/sqlc/query/sqlite/templates.sql
@@ -5,9 +5,10 @@
 
 -- name: InsertTemplate :execrows
 -- A taken org name or catalog slug inserts nothing (0 rows): the caller
--- answers 400.
-INSERT INTO templates (id, source, org_id, slug, name, latest_revision, withdrawn_at, created_at, created_by, updated_at)
-VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?, ?)
+-- answers 400 (save-template) or skips the entry (the catalog sync lost
+-- the race to another replica).
+INSERT INTO templates (id, source, org_id, slug, name, latest_revision, withdrawn_at, catalog, catalog_feed_at, created_at, created_by, updated_at)
+VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?)
 ON CONFLICT DO NOTHING;
 
 -- name: BumpTemplateRevision :execrows
@@ -29,13 +30,13 @@ SELECT id FROM templates WHERE source = 'org' AND org_id = ? AND name = ?;
 -- org has live instances of it), each by name. One pass over the org's
 -- live instances: instances counts them, behind those on a revision older
 -- than the latest.
-SELECT t.id, t.source, t.org_id, t.slug, t.name, t.latest_revision, t.withdrawn_at, t.created_at, t.created_by, t.updated_at,
+SELECT t.id, t.source, t.org_id, t.slug, t.name, t.latest_revision, t.withdrawn_at, t.catalog, t.created_at, t.created_by, t.updated_at,
     COUNT(i.id) AS instances,
     CAST(COALESCE(SUM(CASE WHEN i.revision < t.latest_revision THEN 1 ELSE 0 END), 0) AS INTEGER) AS behind
 FROM templates AS t
 LEFT JOIN template_instances AS i ON i.template_id = t.id AND i.org_id = ? AND i.state IN ('pending', 'created')
 WHERE t.org_id = ? OR t.source = 'catalog'
-GROUP BY t.id, t.source, t.org_id, t.slug, t.name, t.latest_revision, t.withdrawn_at, t.created_at, t.created_by, t.updated_at
+GROUP BY t.id, t.source, t.org_id, t.slug, t.name, t.latest_revision, t.withdrawn_at, t.catalog, t.created_at, t.created_by, t.updated_at
 HAVING t.source = 'org' OR t.withdrawn_at IS NULL OR COUNT(i.id) > 0
 ORDER BY t.source DESC, t.name ASC, t.id ASC;
 
@@ -56,6 +57,49 @@ WHERE templates.id = ? AND templates.source = 'org' AND templates.org_id = ?
 -- A deleted template's upgrade rollouts (scope key: <org_id>:<template_id>).
 DELETE FROM rollouts WHERE kind = 'template_upgrade' AND scope_key = ?;
 
+-- name: ListCatalogTemplates :many
+-- Every catalog template with its latest revision's digest: what the
+-- catalog sync compares a feed with.
+SELECT t.id, t.slug, t.name, t.catalog, t.latest_revision, t.withdrawn_at, t.catalog_feed_at, r.digest
+FROM templates AS t
+JOIN template_revisions AS r ON r.template_id = t.id AND r.revision = t.latest_revision
+WHERE t.source = 'catalog'
+ORDER BY t.slug;
+
+-- name: BumpCatalogTemplate :execrows
+-- A catalog template's next revision from the base the sync read, its
+-- name and metadata, withdrawn_at cleared: 0 rows when another replica
+-- moved latest_revision, or a newer feed wrote the template.
+UPDATE templates
+SET latest_revision = latest_revision + 1, name = sqlc.arg(name), catalog = sqlc.arg(catalog), withdrawn_at = NULL,
+    catalog_feed_at = sqlc.arg(feed_at), updated_at = sqlc.arg(updated_at)
+WHERE id = sqlc.arg(id) AND source = 'catalog' AND latest_revision = sqlc.arg(latest_revision)
+  AND (catalog_feed_at IS NULL OR catalog_feed_at <= sqlc.arg(feed_at));
+
+-- name: UpdateCatalogTemplate :execrows
+-- A catalog template's name and metadata in place, withdrawn_at cleared:
+-- 0 rows when a newer feed wrote it.
+UPDATE templates
+SET name = sqlc.arg(name), catalog = sqlc.arg(catalog), withdrawn_at = NULL,
+    catalog_feed_at = sqlc.arg(feed_at), updated_at = sqlc.arg(updated_at)
+WHERE id = sqlc.arg(id) AND source = 'catalog'
+  AND (catalog_feed_at IS NULL OR catalog_feed_at <= sqlc.arg(feed_at));
+
+-- name: WithdrawCatalogTemplate :execrows
+-- A catalog template the feed no longer lists: 0 rows when it is withdrawn
+-- already or a newer feed wrote it.
+UPDATE templates
+SET withdrawn_at = sqlc.arg(withdrawn_at), updated_at = sqlc.arg(updated_at)
+WHERE id = sqlc.arg(id) AND source = 'catalog' AND withdrawn_at IS NULL
+  AND (catalog_feed_at IS NULL OR catalog_feed_at <= sqlc.arg(feed_at));
+
+-- name: StampCatalogTemplates :exec
+-- The feed every catalog template now reflects: a later sync of an older
+-- feed is refused as a whole.
+UPDATE templates
+SET catalog_feed_at = sqlc.arg(feed_at)
+WHERE source = 'catalog' AND (catalog_feed_at IS NULL OR catalog_feed_at < sqlc.arg(feed_at));
+
 -- name: InsertTemplateRevision :exec
 INSERT INTO template_revisions (template_id, revision, body, note, digest, created_at, created_by)
 VALUES (?, ?, ?, ?, ?, ?, ?);
````

Run: `make sqlc`
Expected: only `internal/infra/db/sqlc/gen/{sqlite,pgsql}/{models.go,querier.go,templates.sql.go}` change; both engines get `BumpCatalogTemplateParams{Name, Catalog string; FeedAt *types.DateTime; UpdatedAt types.DateTime; ID string; LatestRevision int64}`, `UpdateCatalogTemplateParams`, `WithdrawCatalogTemplateParams{WithdrawnAt *types.DateTime; UpdatedAt types.DateTime; ID string; FeedAt *types.DateTime}`, `ListCatalogTemplatesRow{ID, Slug, Name, Catalog string; LatestRevision int64; WithdrawnAt, CatalogFeedAt *types.DateTime; Digest string}`, `StampCatalogTemplates(ctx, feedAt *types.DateTime) error`, and `InsertTemplateParams` gains `Catalog string` and `CatalogFeedAt *types.DateTime`. (A repeated `sqlc.arg(feed_at)` is one parameter: SQLite reuses `?3`, Postgres `$1`.)

- [ ] **Step 5: Write the repository methods**

Create `internal/infra/db/repository/template/catalog.go`:

````go
package template

import (
	"context"
	"encoding/json"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/util"
)

// ListCatalog returns every catalog template's sync state, by slug.
func (r *Repo) ListCatalog(ctx context.Context) ([]model.CatalogState, error) {
	rows, err := r.q.ListCatalogTemplates(ctx, r.tx.Querier(ctx))
	if err != nil {
		return nil, err
	}
	out := make([]model.CatalogState, 0, len(rows))
	for _, row := range rows {
		st := model.CatalogState{
			ID: row.ID, Slug: row.Slug, Name: row.Name, Meta: decodeMeta(row.Catalog),
			LatestRevision: row.LatestRevision, Digest: row.Digest,
		}
		if row.WithdrawnAt != nil {
			at := row.WithdrawnAt.Time()
			st.WithdrawnAt = &at
		}
		if row.CatalogFeedAt != nil {
			at := row.CatalogFeedAt.Time()
			st.FeedAt = &at
		}
		out = append(out, st)
	}
	return out, nil
}

// ApplyCatalog inserts, revises or updates one catalog template in one
// transaction; every write is conditional (the slug free, latest_revision
// still base's, no newer feed), and a refused one is model.CatalogSkipped.
func (r *Repo) ApplyCatalog(ctx context.Context, base *model.CatalogState, e model.CatalogEntry) (model.CatalogOutcome, error) {
	meta, err := encodeMeta(&e.Meta)
	if err != nil {
		return "", err
	}
	now := types.NewDateTime()
	feedAt := types.DateTime(e.FeedAt.UTC())
	out := model.CatalogSkipped
	err = r.tx.WithTx(ctx, func(ctx context.Context) error {
		q := r.tx.Querier(ctx)
		switch {
		case base == nil:
			id := util.GenerateID()
			n, err := r.q.InsertTemplate(ctx, q, insertParams{
				ID: id, Source: model.TemplateSourceCatalog, Slug: e.Slug, Name: e.Name,
				Catalog: meta, CatalogFeedAt: &feedAt, CreatedAt: now, UpdatedAt: now,
			})
			if err != nil || n == 0 {
				return err
			}
			out = model.CatalogInserted
			return r.insertRevision(ctx, model.TemplateRevision{TemplateID: id, Revision: 1, Body: e.Body, Digest: e.Digest}, now)
		case base.Digest != e.Digest:
			n, err := r.q.BumpCatalogTemplate(ctx, q, catBumpParams{
				Name: e.Name, Catalog: meta, FeedAt: &feedAt, UpdatedAt: now, ID: base.ID, LatestRevision: base.LatestRevision,
			})
			if err != nil || n == 0 {
				return err
			}
			out = model.CatalogRevised
			return r.insertRevision(ctx, model.TemplateRevision{
				TemplateID: base.ID, Revision: base.LatestRevision + 1, Body: e.Body, Digest: e.Digest,
			}, now)
		default:
			n, err := r.q.UpdateCatalogTemplate(ctx, q, catUpdParams{
				Name: e.Name, Catalog: meta, FeedAt: &feedAt, UpdatedAt: now, ID: base.ID,
			})
			if err != nil || n == 0 {
				return err
			}
			out = model.CatalogUpdated
			return nil
		}
	})
	if err != nil {
		return "", err
	}
	return out, nil
}

// WithdrawCatalog marks a catalog template the feed dropped.
func (r *Repo) WithdrawCatalog(ctx context.Context, id string, at, feedAt time.Time) (bool, error) {
	withdrawn, feed := types.DateTime(at.UTC()), types.DateTime(feedAt.UTC())
	n, err := r.q.WithdrawCatalogTemplate(ctx, r.tx.Querier(ctx), withdrawParams{
		WithdrawnAt: &withdrawn, UpdatedAt: withdrawn, ID: id, FeedAt: &feed,
	})
	return n > 0, err
}

// StampCatalog moves every catalog template's feed forward to feedAt.
func (r *Repo) StampCatalog(ctx context.Context, feedAt time.Time) error {
	at := types.DateTime(feedAt.UTC())
	return r.q.StampCatalogTemplates(ctx, r.tx.Querier(ctx), &at)
}

// encodeMeta is the stored form of a catalog template's metadata ("" for
// none).
func encodeMeta(m *model.CatalogMeta) (string, error) {
	if m == nil {
		return "", nil
	}
	b, err := json.Marshal(m.Normalized())
	return string(b), err
}

// decodeMeta reads stored metadata; "" (a row no sync wrote) or a value
// that does not decode reads as empty, never failing a template read.
func decodeMeta(s string) model.CatalogMeta {
	var m model.CatalogMeta
	_ = json.Unmarshal([]byte(s), &m)
	return m.Normalized()
}
````

Modify `internal/infra/db/repository/template/repo.go`, `internal/infra/db/repository/template/sqlite.go`, `internal/infra/db/repository/template/pgsql.go` (`git apply` this diff):

````diff
diff --git a/internal/infra/db/repository/template/pgsql.go b/internal/infra/db/repository/template/pgsql.go
index 430e413..a6fd6a1 100644
--- a/internal/infra/db/repository/template/pgsql.go
+++ b/internal/infra/db/repository/template/pgsql.go
@@ -5,6 +5,7 @@ import (
 
 	"winterflow/internal/infra/db/backend"
 	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
+	"winterflow/internal/infra/db/types"
 )
 
 type pgsqlQuerier struct{}
@@ -137,3 +138,31 @@ func (pgsqlQuerier) GetLiveTemplateInstanceForApp(ctx context.Context, db backen
 func (pgsqlQuerier) RetireTemplateInstancesForApp(ctx context.Context, db backend.DBTX, p retireParams) error {
 	return pgsqlgen.New(db).RetireTemplateInstancesForApp(ctx, pgsqlgen.RetireTemplateInstancesForAppParams(p))
 }
+
+func (pgsqlQuerier) ListCatalogTemplates(ctx context.Context, db backend.DBTX) ([]catalogRow, error) {
+	rows, err := pgsqlgen.New(db).ListCatalogTemplates(ctx)
+	if err != nil {
+		return nil, err
+	}
+	out := make([]catalogRow, len(rows))
+	for i, row := range rows {
+		out[i] = catalogRow(row)
+	}
+	return out, nil
+}
+
+func (pgsqlQuerier) BumpCatalogTemplate(ctx context.Context, db backend.DBTX, p catBumpParams) (int64, error) {
+	return pgsqlgen.New(db).BumpCatalogTemplate(ctx, pgsqlgen.BumpCatalogTemplateParams(p))
+}
+
+func (pgsqlQuerier) UpdateCatalogTemplate(ctx context.Context, db backend.DBTX, p catUpdParams) (int64, error) {
+	return pgsqlgen.New(db).UpdateCatalogTemplate(ctx, pgsqlgen.UpdateCatalogTemplateParams(p))
+}
+
+func (pgsqlQuerier) WithdrawCatalogTemplate(ctx context.Context, db backend.DBTX, p withdrawParams) (int64, error) {
+	return pgsqlgen.New(db).WithdrawCatalogTemplate(ctx, pgsqlgen.WithdrawCatalogTemplateParams(p))
+}
+
+func (pgsqlQuerier) StampCatalogTemplates(ctx context.Context, db backend.DBTX, feedAt *types.DateTime) error {
+	return pgsqlgen.New(db).StampCatalogTemplates(ctx, feedAt)
+}
diff --git a/internal/infra/db/repository/template/repo.go b/internal/infra/db/repository/template/repo.go
index 64855ee..690f916 100644
--- a/internal/infra/db/repository/template/repo.go
+++ b/internal/infra/db/repository/template/repo.go
@@ -45,6 +45,10 @@ type (
 	retireParams   = sqlitegen.RetireTemplateInstancesForAppParams
 	upgradeParams  = sqlitegen.ListUpgradableInstancesParams
 	listParams     = sqlitegen.ListTemplatesParams
+	catalogRow     = sqlitegen.ListCatalogTemplatesRow
+	catBumpParams  = sqlitegen.BumpCatalogTemplateParams
+	catUpdParams   = sqlitegen.UpdateCatalogTemplateParams
+	withdrawParams = sqlitegen.WithdrawCatalogTemplateParams
 )
 
 type querier interface {
@@ -70,6 +74,11 @@ type querier interface {
 	GetTemplateInstanceByID(ctx context.Context, db backend.DBTX, id string) (instanceRow, error)
 	GetLiveTemplateInstanceForApp(ctx context.Context, db backend.DBTX, p forAppParams) (string, error)
 	RetireTemplateInstancesForApp(ctx context.Context, db backend.DBTX, p retireParams) error
+	ListCatalogTemplates(ctx context.Context, db backend.DBTX) ([]catalogRow, error)
+	BumpCatalogTemplate(ctx context.Context, db backend.DBTX, p catBumpParams) (int64, error)
+	UpdateCatalogTemplate(ctx context.Context, db backend.DBTX, p catUpdParams) (int64, error)
+	WithdrawCatalogTemplate(ctx context.Context, db backend.DBTX, p withdrawParams) (int64, error)
+	StampCatalogTemplates(ctx context.Context, db backend.DBTX, feedAt *types.DateTime) error
 }
 
 // Repo is the templates repository.
@@ -79,7 +88,10 @@ type Repo struct {
 	log *logger.Logger
 }
 
-var _ port.TemplateRepository = (*Repo)(nil)
+var (
+	_ port.TemplateRepository = (*Repo)(nil)
+	_ port.CatalogRepository  = (*Repo)(nil)
+)
 
 // New builds the repository for driver (backend.SQLite or backend.Postgres).
 func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error) {
@@ -106,6 +118,11 @@ func (r *Repo) CreateTemplate(ctx context.Context, t model.Template, rev model.T
 			at := types.DateTime(t.WithdrawnAt.UTC())
 			p.WithdrawnAt = &at
 		}
+		meta, err := encodeMeta(t.CatalogMeta)
+		if err != nil {
+			return err
+		}
+		p.Catalog = meta
 	default:
 		return fmt.Errorf("template %s: unknown source %q", t.ID, t.Source)
 	}
@@ -192,7 +209,7 @@ func (r *Repo) ListTemplates(ctx context.Context, orgID string) ([]model.Templat
 		out = append(out, model.TemplateSummary{
 			Template: hydrateTemplate(templateRow{
 				ID: row.ID, Source: row.Source, OrgID: row.OrgID, Slug: row.Slug, Name: row.Name,
-				LatestRevision: row.LatestRevision, WithdrawnAt: row.WithdrawnAt,
+				LatestRevision: row.LatestRevision, WithdrawnAt: row.WithdrawnAt, Catalog: row.Catalog,
 				CreatedAt: row.CreatedAt, CreatedBy: row.CreatedBy, UpdatedAt: row.UpdatedAt,
 			}),
 			Instances: row.Instances,
@@ -457,6 +474,10 @@ func hydrateTemplate(row templateRow) model.Template {
 		at := row.WithdrawnAt.Time()
 		t.WithdrawnAt = &at
 	}
+	if t.Catalog() {
+		meta := decodeMeta(row.Catalog)
+		t.CatalogMeta = &meta
+	}
 	return t
 }
 
diff --git a/internal/infra/db/repository/template/sqlite.go b/internal/infra/db/repository/template/sqlite.go
index 804674f..f8dee12 100644
--- a/internal/infra/db/repository/template/sqlite.go
+++ b/internal/infra/db/repository/template/sqlite.go
@@ -5,6 +5,7 @@ import (
 
 	"winterflow/internal/infra/db/backend"
 	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
+	"winterflow/internal/infra/db/types"
 )
 
 type sqliteQuerier struct{}
@@ -98,3 +99,23 @@ func (sqliteQuerier) GetLiveTemplateInstanceForApp(ctx context.Context, db backe
 func (sqliteQuerier) RetireTemplateInstancesForApp(ctx context.Context, db backend.DBTX, p retireParams) error {
 	return sqlitegen.New(db).RetireTemplateInstancesForApp(ctx, p)
 }
+
+func (sqliteQuerier) ListCatalogTemplates(ctx context.Context, db backend.DBTX) ([]catalogRow, error) {
+	return sqlitegen.New(db).ListCatalogTemplates(ctx)
+}
+
+func (sqliteQuerier) BumpCatalogTemplate(ctx context.Context, db backend.DBTX, p catBumpParams) (int64, error) {
+	return sqlitegen.New(db).BumpCatalogTemplate(ctx, p)
+}
+
+func (sqliteQuerier) UpdateCatalogTemplate(ctx context.Context, db backend.DBTX, p catUpdParams) (int64, error) {
+	return sqlitegen.New(db).UpdateCatalogTemplate(ctx, p)
+}
+
+func (sqliteQuerier) WithdrawCatalogTemplate(ctx context.Context, db backend.DBTX, p withdrawParams) (int64, error) {
+	return sqlitegen.New(db).WithdrawCatalogTemplate(ctx, p)
+}
+
+func (sqliteQuerier) StampCatalogTemplates(ctx context.Context, db backend.DBTX, feedAt *types.DateTime) error {
+	return sqlitegen.New(db).StampCatalogTemplates(ctx, feedAt)
+}
````

- [ ] **Step 6: Run the tests on both engines**

Run: `go vet ./... && go test ./internal/infra/db/... ./internal/domain/... && make sqlc-check`
Expected: PASS; `make sqlc-check` prints nothing after its command line.

Run: `make test-pg TEST_PG_ARGS='-run "Catalog|Timestamps|Migrat" -v'`
Expected: PASS on `postgres:16` (`TestApplyCatalogRace` there runs four overlapping transactions: one `inserted`/`revised`, three `skipped`).

- [ ] **Step 7: Commit**

```bash
git add internal/infra/db internal/domain/model internal/domain/port/catalog.go
git commit -m "feat(catalog): migration 0017 and conditional catalog writes in the templates repository" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 5: get-templates and get-template answer `catalog`

The gallery of sub-project 4 needs the catalog's text, search words, links and logo; the API carries them now (null for an org template). The web gets the type only.

**Files:**
- Modify: `internal/domain/usecase/templates/views.go`
- Modify: `internal/domain/usecase/templates/templates.go` (`ListTemplates`, `GetTemplate`)
- Regenerate: `api/openapi.yaml` (`make openapi`)
- Regenerate: `internal/app/web/contract/testdata/parity.golden.json` (`-update`)
- Modify: `web/src/lib/templates.ts`
- Test: `internal/domain/usecase/templates/templates_test.go`
- Test: `internal/app/web/contract/templates_test.go` (`catalogTemplate` stores metadata; assertions on `catalog`)

**Interfaces:**
- Consumes: Task 4's `model.Template.CatalogMeta`.
- Produces: `TemplateSummary.Catalog *model.CatalogMeta` and `TemplateView.Catalog *model.CatalogMeta`, JSON `catalog` (required, nullable: the OpenAPI component `CatalogMeta`, `anyOf: [null, CatalogMeta]`); web `type CatalogMeta` and `catalog?: CatalogMeta | null` on `TemplateSummary` and `TemplateView`.

- [ ] **Step 1: Write the failing tests**

Modify `internal/domain/usecase/templates/templates_test.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/usecase/templates/templates_test.go b/internal/domain/usecase/templates/templates_test.go
index 1351421..13876d2 100644
--- a/internal/domain/usecase/templates/templates_test.go
+++ b/internal/domain/usecase/templates/templates_test.go
@@ -112,11 +112,12 @@ func TestCatalogTemplatesAreReadOnly(t *testing.T) {
 	}
 	for _, org := range []string{org1, org2} {
 		v, err := f.uc.GetTemplate(ctx, org, cat, 0)
-		if err != nil || v.Source != model.TemplateSourceCatalog || v.Slug != "shop" || len(v.Detected.Variables) != 3 {
+		if err != nil || v.Source != model.TemplateSourceCatalog || v.Slug != "shop" || len(v.Detected.Variables) != 3 || v.Catalog == nil {
 			t.Fatalf("%s reads %+v, %v", org, v, err)
 		}
 	}
-	if list, _ := f.uc.ListTemplates(ctx, org1); len(list.Templates) != 2 || list.Templates[1].ID != cat {
+	if list, _ := f.uc.ListTemplates(ctx, org1); len(list.Templates) != 2 || list.Templates[1].ID != cat ||
+		list.Templates[0].Catalog != nil || list.Templates[1].Catalog == nil || list.Templates[1].Catalog.Keywords == nil {
 		t.Fatalf("org1 list = %+v", list)
 	}
 	_, err = f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{ID: cat, Name: "shop", Body: body(), BaseRevision: 1})
````

Modify `internal/app/web/contract/templates_test.go` (`git apply` this diff):

````diff
diff --git a/internal/app/web/contract/templates_test.go b/internal/app/web/contract/templates_test.go
index 94c746e..c19d52d 100644
--- a/internal/app/web/contract/templates_test.go
+++ b/internal/app/web/contract/templates_test.go
@@ -51,15 +51,16 @@ func saveTemplate(t *testing.T, c *client, req htpl.SaveTemplateRequest) tuc.Sav
 	return out
 }
 
-// catalogTemplate inserts a catalog template with body (nothing in the API
-// creates one: the catalog sync will).
+// catalogTemplate inserts a catalog template with body, as the catalog
+// sync would (catalog_test.go drives the sync itself).
 func catalogTemplate(t *testing.T, e *env, id, slug string, body template.Body) {
 	t.Helper()
 	raw, err := template.Encode(body)
 	if err != nil {
 		t.Fatal(err)
 	}
-	if err := e.deps.TemplateRepository.CreateTemplate(e.dbCtx(t), model.Template{ID: id, Source: model.TemplateSourceCatalog, Slug: slug, Name: "Shop"},
+	meta := &model.CatalogMeta{Summary: body.Summary, Keywords: []string{"store"}, LogoURL: "https://winterflow.io/catalog/logos/" + slug + ".png"}
+	if err := e.deps.TemplateRepository.CreateTemplate(e.dbCtx(t), model.Template{ID: id, Source: model.TemplateSourceCatalog, Slug: slug, Name: "Shop", CatalogMeta: meta},
 		model.TemplateRevision{Body: raw, Digest: "sha256:" + slug}); err != nil {
 		t.Fatal(err)
 	}
@@ -113,12 +114,13 @@ func TestTemplateRoutes(t *testing.T) {
 	t.Run("list and read with what the compose file declares", func(t *testing.T) {
 		var list tuc.TemplateList
 		owner.get(t, "/api/v1/template/get-templates", nil).expect(t, http.StatusOK).data(t, &list)
-		if len(list.Templates) != 1 || list.Templates[0].LatestRevision != 2 || list.Templates[0].Instances != 0 || list.Templates[0].Source != "org" {
+		if len(list.Templates) != 1 || list.Templates[0].LatestRevision != 2 || list.Templates[0].Instances != 0 || list.Templates[0].Source != "org" ||
+			list.Templates[0].Catalog != nil {
 			t.Fatalf("list = %+v", list)
 		}
 		var view tuc.TemplateView
 		owner.get(t, "/api/v1/template/get-template", url.Values{"id": {saved.ID}, "revision": {"1"}}).expect(t, http.StatusOK).data(t, &view)
-		if view.Revision != 1 || view.Note != "first" || view.Body.Summary != "A shop" || len(view.Revisions) != 2 {
+		if view.Revision != 1 || view.Note != "first" || view.Body.Summary != "A shop" || len(view.Revisions) != 2 || view.Catalog != nil {
 			t.Fatalf("view = %+v", view)
 		}
 		d := view.Detected
@@ -165,12 +167,14 @@ func TestCatalogTemplatesAreReadOnly(t *testing.T) {
 	for _, c := range []*client{owner, mallory} {
 		var list tuc.TemplateList
 		c.get(t, "/api/v1/template/get-templates", nil).expect(t, http.StatusOK).data(t, &list)
-		if len(list.Templates) != 1 || list.Templates[0].ID != cat || list.Templates[0].Source != "catalog" || list.Templates[0].Slug != "shop" {
+		if len(list.Templates) != 1 || list.Templates[0].ID != cat || list.Templates[0].Source != "catalog" || list.Templates[0].Slug != "shop" ||
+			list.Templates[0].Catalog == nil || list.Templates[0].Catalog.LogoURL != "https://winterflow.io/catalog/logos/shop.png" {
 			t.Fatalf("list = %+v", list)
 		}
 		var view tuc.TemplateView
 		c.get(t, "/api/v1/template/get-template", url.Values{"id": {cat}}).expect(t, http.StatusOK).data(t, &view)
-		if view.Source != "catalog" || len(view.Detected.Variables) != 3 {
+		if view.Source != "catalog" || len(view.Detected.Variables) != 3 || view.Catalog == nil || view.Catalog.Summary != "A shop" ||
+			len(view.Catalog.Keywords) != 1 || view.Catalog.Tags == nil {
 			t.Fatalf("view = %+v", view)
 		}
 	}
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/domain/usecase/templates/ ./internal/app/web/contract/`
Expected: FAIL to build: `v.Catalog undefined (type templates.TemplateView has no field or method Catalog)`, `list.Templates[0].Catalog undefined`.

- [ ] **Step 3: Add the field**

Modify `internal/domain/usecase/templates/views.go`, `internal/domain/usecase/templates/templates.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/usecase/templates/templates.go b/internal/domain/usecase/templates/templates.go
index 7d55f71..8a6405b 100644
--- a/internal/domain/usecase/templates/templates.go
+++ b/internal/domain/usecase/templates/templates.go
@@ -151,7 +151,7 @@ func (uc *UseCase) ListTemplates(ctx context.Context, orgID string) (TemplateLis
 	for _, t := range list {
 		out.Templates = append(out.Templates, TemplateSummary{
 			ID: t.ID, Source: t.Source, Slug: t.Slug, Name: t.Name, LatestRevision: t.LatestRevision,
-			Instances: t.Instances, Behind: t.Behind, WithdrawnAt: t.WithdrawnAt, UpdatedAt: t.UpdatedAt,
+			Instances: t.Instances, Behind: t.Behind, WithdrawnAt: t.WithdrawnAt, Catalog: t.CatalogMeta, UpdatedAt: t.UpdatedAt,
 		})
 	}
 	return out, nil
@@ -189,7 +189,7 @@ func (uc *UseCase) GetTemplate(ctx context.Context, orgID, id string, revision i
 	}
 	out := TemplateView{
 		ID: t.ID, Source: t.Source, Slug: t.Slug, Name: t.Name, LatestRevision: t.LatestRevision, WithdrawnAt: t.WithdrawnAt,
-		Revision: rev.Revision, Body: body, Detected: detected, Note: rev.Note, CreatedAt: rev.CreatedAt, CreatedBy: rev.CreatedBy,
+		Catalog: t.CatalogMeta, Revision: rev.Revision, Body: body, Detected: detected, Note: rev.Note, CreatedAt: rev.CreatedAt, CreatedBy: rev.CreatedBy,
 		Revisions: make([]TemplateRevisionInfo, 0, len(revs)),
 	}
 	for _, r := range revs {
diff --git a/internal/domain/usecase/templates/views.go b/internal/domain/usecase/templates/views.go
index 7d6868b..915639a 100644
--- a/internal/domain/usecase/templates/views.go
+++ b/internal/domain/usecase/templates/views.go
@@ -3,6 +3,7 @@ package templates
 import (
 	"time"
 
+	"winterflow/internal/domain/model"
 	"winterflow/internal/domain/template"
 )
 
@@ -16,7 +17,10 @@ type TemplateSummary struct {
 	Instances      int64      `json:"instances" description:"The org's pending and created instances."`
 	Behind         int64      `json:"behind" description:"Of those, the ones on a revision older than the latest."`
 	WithdrawnAt    *time.Time `json:"withdrawn_at" description:"When the catalog dropped the template (listed only while the org has instances of it); null otherwise."`
-	UpdatedAt      time.Time  `json:"updated_at"`
+	// Catalog is what the catalog says about a catalog template; null for
+	// an org template.
+	Catalog   *model.CatalogMeta `json:"catalog" description:"What the catalog says about a catalog template (summary, search words, links, logo); null for an org template."`
+	UpdatedAt time.Time          `json:"updated_at"`
 }
 
 // TemplateList is the data of GET get-templates.
@@ -41,6 +45,7 @@ type TemplateView struct {
 	Name           string                 `json:"name"`
 	LatestRevision int64                  `json:"latest_revision"`
 	WithdrawnAt    *time.Time             `json:"withdrawn_at" description:"When the catalog dropped the template (it takes no new installs); null otherwise."`
+	Catalog        *model.CatalogMeta     `json:"catalog" description:"What the catalog says about a catalog template; null for an org template."`
 	Revision       int64                  `json:"revision"`
 	Body           template.Body          `json:"body"`
 	Detected       template.Detected      `json:"detected" description:"What the revision's compose files declare under x-winterflow."`
````

Modify `web/src/lib/templates.ts` (`git apply` this diff):

````diff
diff --git a/web/src/lib/templates.ts b/web/src/lib/templates.ts
index ba8712e..dc00dc4 100644
--- a/web/src/lib/templates.ts
+++ b/web/src/lib/templates.ts
@@ -47,6 +47,22 @@ export type Inspection = { detected: Detected; problem: string };
 
 export type TemplateSource = "org" | "catalog";
 
+// CatalogMeta is what the catalog says about a catalog template besides its
+// name and files (null for an org template). Go encodes an empty list as
+// null.
+export type CatalogMeta = {
+  summary: string;
+  description: string;
+  keywords: string[] | null;
+  tags: string[] | null;
+  alternative_to: string[] | null; // apps this one replaces, as search words
+  license: string;
+  homepage_url: string;
+  repository_url: string;
+  documentation_url: string;
+  logo_url: string;
+};
+
 export type TemplateSummary = {
   id: string;
   source: TemplateSource;
@@ -56,6 +72,7 @@ export type TemplateSummary = {
   instances: number; // the org's pending and created ones
   behind: number; // of those, on a revision older than the latest
   withdrawn_at: string | null; // when the catalog dropped it
+  catalog?: CatalogMeta | null; // a catalog template's metadata; null for an org one
   updated_at: string;
 };
 
@@ -82,6 +99,7 @@ export type TemplateView = {
   name: string;
   latest_revision: number;
   withdrawn_at: string | null;
+  catalog?: CatalogMeta | null;
   revision: number;
   body: TemplateBody;
   detected: Detected;
````

- [ ] **Step 4: Regenerate the OpenAPI document and the parity golden, run the suites**

Run: `make openapi && go test ./internal/app/web/contract/ -run TestParity -update && git diff --stat api/openapi.yaml internal/app/web/contract/testdata/parity.golden.json`
Expected: `api/openapi.yaml` gains the `CatalogMeta` component (ten required fields, the three lists nullable arrays) and a required `catalog` (`anyOf: [{type: "null"}, {$ref: CatalogMeta}]`) on `TemplateSummary` and `TemplateView`; the parity golden gains two `"catalog": null` lines (the parity transcript's template is an org template).

Run: `go test ./internal/domain/usecase/templates/ ./internal/app/web/... && pnpm --dir web exec tsc -b && pnpm --dir web run lint`
Expected: PASS (every contract response is validated against the new schema); tsc and ESLint clean.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/usecase/templates api/openapi.yaml internal/app/web/contract web/src/lib/templates.ts
git commit -m "feat(catalog): get-templates and get-template answer the catalog metadata" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 6: The fetcher: one conditional GET, then the signature, bounded

**Files:**
- Create: `internal/infra/catalogfeed/fetcher.go`
- Modify: `internal/domain/model/catalog.go` (`CatalogFetch`)
- Modify: `internal/domain/port/catalog.go` (`CatalogFetcher`)
- Test: `internal/infra/catalogfeed/fetcher_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks but the files it extends.
- Produces:
  - `model.CatalogFetch{NotModified bool; Feed, Sig []byte; ETag string}`.
  - `port.CatalogFetcher` with `FetchCatalog(ctx context.Context, etag string) (model.CatalogFetch, error)`.
  - Package `winterflow/internal/infra/catalogfeed`: `const RequestTimeout = 30 * time.Second`, `MaxFeedBytes = 8 << 20`, `MaxSigBytes = 4 << 10`; `func New(feedURL string) (*Fetcher, error)` (refuses anything but https, or http to localhost/127.0.0.0/8/::1); `(*Fetcher).FetchCatalog` (GET the feed with `If-None-Match: etag` and `User-Agent: winterflow`; a 304 only when an ETag was sent; then GET `<path>.sig`; any other status, a body over its cap, a redirect to a refused URL or more than 5 redirects, or a request past 30 s is an error).

- [ ] **Step 1: Write the failing tests**

Create `internal/infra/catalogfeed/fetcher_test.go`:

````go
package catalogfeed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// feedServer serves catalog.json (ETag "v1") and catalog.json.sig, and
// records the requests it got.
type feedServer struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []*http.Request
	sig  func(w http.ResponseWriter) // nil: the signature
}

func newFeedServer(t *testing.T) *feedServer {
	t.Helper()
	s := &feedServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.reqs = append(s.reqs, r)
		sig := s.sig
		s.mu.Unlock()
		switch r.URL.Path {
		case "/catalog/catalog.json":
			if r.Header.Get("If-None-Match") == `"v1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", `"v1"`)
			_, _ = w.Write([]byte(`{"format":1}`))
		case "/catalog/catalog.json.sig":
			if sig != nil {
				sig(w)
				return
			}
			_, _ = w.Write([]byte("c2lnbmF0dXJl\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *feedServer) requests() []*http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*http.Request{}, s.reqs...)
}

func fetcher(t *testing.T, url string) *Fetcher {
	t.Helper()
	f, err := New(url)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestFetchAndNotModified(t *testing.T) {
	s := newFeedServer(t)
	f := fetcher(t, s.URL+"/catalog/catalog.json")
	ctx := context.Background()
	got, err := f.FetchCatalog(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.NotModified || string(got.Feed) != `{"format":1}` || string(got.Sig) != "c2lnbmF0dXJl\n" || got.ETag != `"v1"` {
		t.Fatalf("fetch = %+v", got)
	}
	again, err := f.FetchCatalog(ctx, got.ETag)
	if err != nil || !again.NotModified || again.Feed != nil || again.Sig != nil || again.ETag != `"v1"` {
		t.Fatalf("second fetch = %+v, %v", again, err)
	}
	reqs := s.requests()
	if len(reqs) != 3 || reqs[0].Header.Get("User-Agent") != "winterflow" || reqs[0].Header.Get("If-None-Match") != "" ||
		reqs[1].URL.Path != "/catalog/catalog.json.sig" || reqs[2].Header.Get("If-None-Match") != `"v1"` {
		t.Fatalf("requests = %+v", reqs)
	}
}

// A feed whose catalog.json answers but whose .sig does not is an error,
// never a feed without a signature.
func TestFetchNeedsTheSignature(t *testing.T) {
	for name, c := range map[string]struct {
		sig  func(w http.ResponseWriter)
		want string
	}{
		"a missing .sig": {func(w http.ResponseWriter) { w.WriteHeader(http.StatusNotFound) }, "catalog.json.sig: 404 Not Found"},
		"a failing .sig": {func(w http.ResponseWriter) { w.WriteHeader(http.StatusBadGateway) }, "502 Bad Gateway"},
		"a 304 .sig":     {func(w http.ResponseWriter) { w.WriteHeader(http.StatusNotModified) }, "304 Not Modified"},
		"a huge .sig":    {func(w http.ResponseWriter) { _, _ = w.Write(make([]byte, MaxSigBytes+1)) }, "larger than 4096 bytes"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newFeedServer(t)
			s.sig = c.sig
			got, err := fetcher(t, s.URL+"/catalog/catalog.json").FetchCatalog(context.Background(), "")
			if err == nil || !strings.Contains(err.Error(), c.want) || got.Feed != nil {
				t.Fatalf("fetch = %+v, %v; want an error naming %q", got, err, c.want)
			}
		})
	}
}

func TestFetchRefuses(t *testing.T) {
	s := newFeedServer(t)
	for name, c := range map[string]struct {
		handler http.HandlerFunc
		want    string
	}{
		"a server error": {func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }, "500 Internal Server Error"},
		"a 304 unasked":  {func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotModified) }, "304 Not Modified"},
		"a feed over the cap": {func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(make([]byte, MaxFeedBytes+1))
		}, "larger than 8388608 bytes"},
		"a redirect to plain http": {func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://catalog.example.test/catalog.json", http.StatusFound)
		}, "http only for a loopback host"},
		"endless redirects": {func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, r.URL.Path+"x", http.StatusFound)
		}, "too many redirects"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(c.handler)
			defer srv.Close()
			if _, err := fetcher(t, srv.URL+"/catalog.json").FetchCatalog(context.Background(), ""); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
	// A loopback redirect is followed.
	redirect := httptest.NewServer(http.RedirectHandler(s.URL+"/catalog/catalog.json", http.StatusMovedPermanently))
	defer redirect.Close()
	if got, err := fetcher(t, redirect.URL+"/catalog/catalog.json").FetchCatalog(context.Background(), ""); err != nil || string(got.Feed) != `{"format":1}` {
		t.Fatalf("through a redirect = %+v, %v", got, err)
	}
}

// A server that never answers fails the fetch at the deadline.
func TestFetchHasADeadline(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)
	f := fetcher(t, srv.URL+"/catalog.json")
	f.timeout = 100 * time.Millisecond
	start := time.Now()
	if _, err := f.FetchCatalog(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("the deadline did not hold")
	}
}

func TestNew(t *testing.T) {
	for _, ok := range []string{
		"https://winterflow.io/catalog/catalog.json",
		"http://127.0.0.1:8080/catalog.json",
		"http://[::1]:8080/catalog.json",
		"http://localhost/catalog.json",
	} {
		if _, err := New(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"http://winterflow.io/catalog/catalog.json",
		"http://10.0.0.1/catalog.json",
		"ftp://winterflow.io/catalog.json",
		"https:///catalog.json",
		"catalog.json",
		"://x",
	} {
		if _, err := New(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	f := fetcher(t, "https://winterflow.io/catalog/catalog.json?v=2")
	if f.sig.String() != "https://winterflow.io/catalog/catalog.json.sig?v=2" {
		t.Fatalf("sig URL = %s", f.sig)
	}
}
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/infra/catalogfeed/`
Expected: FAIL to build: `undefined: Fetcher`, `undefined: New`, `undefined: MaxSigBytes`.

- [ ] **Step 3: Write the types and the fetcher**

Modify `internal/domain/model/catalog.go`, `internal/domain/port/catalog.go` (`git apply` this diff):

````diff
diff --git a/internal/domain/model/catalog.go b/internal/domain/model/catalog.go
index 5ad1da1..f0246af 100644
--- a/internal/domain/model/catalog.go
+++ b/internal/domain/model/catalog.go
@@ -82,3 +82,14 @@ const (
 	// CatalogSkipped: another replica, or a newer feed, wrote it first.
 	CatalogSkipped CatalogOutcome = "skipped"
 )
+
+// CatalogFetch is one fetch of the feed and its signature.
+type CatalogFetch struct {
+	// NotModified: the feed still has the ETag asked with (a 304); Feed
+	// and Sig are nil then.
+	NotModified bool
+	// Feed is catalog.json's exact bytes, Sig catalog.json.sig's.
+	Feed, Sig []byte
+	// ETag is the feed's, to ask with next time ("" when it sent none).
+	ETag string
+}
diff --git a/internal/domain/port/catalog.go b/internal/domain/port/catalog.go
index 9472dcc..26d22c5 100644
--- a/internal/domain/port/catalog.go
+++ b/internal/domain/port/catalog.go
@@ -31,3 +31,10 @@ type CatalogRepository interface {
 	// reflects (only ever moving it forward).
 	StampCatalog(ctx context.Context, feedAt time.Time) error
 }
+
+// CatalogFetcher gets the signed catalog feed (internal/infra/catalogfeed).
+type CatalogFetcher interface {
+	// FetchCatalog GETs the feed with If-None-Match: etag ("" asks for it
+	// whatever it is), then its signature.
+	FetchCatalog(ctx context.Context, etag string) (model.CatalogFetch, error)
+}
````

Create `internal/infra/catalogfeed/fetcher.go`:

````go
// Package catalogfeed fetches the catalog feed over HTTP: catalog.json
// with If-None-Match, then catalog.json.sig, each bounded in time and size.
// Only https is fetched, except from a loopback host (tests, a feed served
// on the same box), redirects included.
package catalogfeed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
)

// Bounds of one fetch.
const (
	// RequestTimeout bounds each request, body included.
	RequestTimeout = 30 * time.Second
	// MaxFeedBytes caps catalog.json; MaxSigBytes catalog.json.sig.
	MaxFeedBytes = 8 << 20
	MaxSigBytes  = 4 << 10
	maxRedirects = 5
)

// Fetcher gets one feed URL and its signature.
type Fetcher struct {
	feed, sig *url.URL
	client    *http.Client
	timeout   time.Duration
}

var _ port.CatalogFetcher = (*Fetcher)(nil)

// New makes the fetcher of feedURL (CATALOG_URL); its signature is
// feedURL's path plus ".sig".
func New(feedURL string) (*Fetcher, error) {
	u, err := url.Parse(feedURL)
	if err != nil {
		return nil, fmt.Errorf("catalog URL: %w", err)
	}
	if err := allowed(u); err != nil {
		return nil, fmt.Errorf("catalog URL: %w", err)
	}
	sig := *u
	sig.Path += ".sig"
	sig.RawPath = ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return &Fetcher{
		feed: u, sig: &sig, timeout: RequestTimeout,
		client: &http.Client{Transport: transport, CheckRedirect: checkRedirect},
	}, nil
}

// FetchCatalog GETs the feed (304 when etag still matches), then its
// signature; any other answer, a body over its cap or a request past
// RequestTimeout is an error.
func (f *Fetcher) FetchCatalog(ctx context.Context, etag string) (model.CatalogFetch, error) {
	feed, newETag, notModified, err := f.get(ctx, f.feed, etag, MaxFeedBytes)
	if err != nil {
		return model.CatalogFetch{}, err
	}
	if notModified {
		return model.CatalogFetch{NotModified: true, ETag: etag}, nil
	}
	sig, _, _, err := f.get(ctx, f.sig, "", MaxSigBytes)
	if err != nil {
		return model.CatalogFetch{}, err
	}
	return model.CatalogFetch{Feed: feed, Sig: sig, ETag: newETag}, nil
}

// get GETs u; a 304 counts only when it was asked for (etag set).
func (f *Fetcher) get(ctx context.Context, u *url.URL, etag string, limit int64) (body []byte, newETag string, notModified bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", false, err
	}
	req.Header.Set("User-Agent", "winterflow")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, "", false, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotModified && etag != "":
		return nil, "", true, nil
	case resp.StatusCode != http.StatusOK:
		return nil, "", false, fmt.Errorf("GET %s: %s", u.Redacted(), resp.Status)
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", false, fmt.Errorf("GET %s: %w", u.Redacted(), err)
	}
	if int64(len(body)) > limit {
		return nil, "", false, fmt.Errorf("GET %s: larger than %d bytes", u.Redacted(), limit)
	}
	return body, resp.Header.Get("ETag"), false, nil
}

// checkRedirect follows at most maxRedirects redirects, each to an
// allowed URL.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errors.New("too many redirects")
	}
	return allowed(req.URL)
}

// allowed accepts https, and http to a loopback host only.
func allowed(u *url.URL) error {
	switch {
	case u.Host == "":
		return fmt.Errorf("%q has no host", u.Redacted())
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && loopback(u.Hostname()):
		return nil
	case u.Scheme == "http":
		return fmt.Errorf("%s: http only for a loopback host; use https", u.Redacted())
	}
	return fmt.Errorf("%s: not an https URL", u.Redacted())
}

func loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
````

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/infra/catalogfeed/ && go test -race -cover ./internal/infra/catalogfeed/`
Expected: PASS in about 1 s (the deadline test waits 100 ms), about 96% of statements.

- [ ] **Step 5: Commit**

```bash
git add internal/infra/catalogfeed internal/domain/model/catalog.go internal/domain/port/catalog.go
git commit -m "feat(catalog): fetch the feed and its signature with deadlines and size caps" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 7: The sync: verify, apply, withdraw, stamp

**Files:**
- Create: `internal/domain/usecase/catalog/sync.go`
- Test: `internal/domain/usecase/catalog/sync_test.go` (the real SQLite templates repository through `dbtest`, a fake fetcher)

**Interfaces:**
- Consumes: Task 2's `feed.Verify`, `feed.Decode`, `Entry.Check`, `feed.ErrSignature`, `feed.ErrInvalid`; Task 4's `port.CatalogRepository` and model types; Task 6's `port.CatalogFetcher`, `model.CatalogFetch`; `template.Encode`.
- Produces (package `winterflow/internal/domain/usecase/catalog`, imported as `catalogsync` where the domain package is also used):
  - `const DefaultFirst = time.Minute`, `DefaultEvery = 24 * time.Hour`, `DefaultTimeout = 5 * time.Minute`, `MaxClockSkew = time.Hour`; `var ErrStale`, `var ErrFuture`.
  - `type Deps struct { Repo port.CatalogRepository; Fetcher port.CatalogFetcher; Key ed25519.PublicKey; Log *logger.Logger; Now func() time.Time }`; `func New(d Deps) *Syncer`.
  - `type Result struct { NotModified bool; Inserted, Revised, Updated, Unchanged, Skipped, Invalid, Withdrawn int }`; `func (s *Syncer) Sync(ctx context.Context) (Result, error)`.
  - `type Schedule struct { First, Every, Timeout time.Duration }`; `func (s *Syncer) Run(ctx context.Context, sch Schedule)`.
  - `func WithdrawAll(ctx context.Context, repo port.CatalogRepository, now time.Time) (int, error)`.

- [ ] **Step 1: Write the failing tests**

Create `internal/domain/usecase/catalog/sync_test.go`:

````go
package catalog_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	feed "winterflow/internal/domain/catalog"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/usecase/catalog"
	"winterflow/internal/infra/db/dbtest"
	templaterepo "winterflow/internal/infra/db/repository/template"
)

var (
	g1  = time.Date(2026, 10, 14, 20, 0, 0, 0, time.UTC)
	g2  = g1.Add(24 * time.Hour)
	g3  = g2.Add(24 * time.Hour)
	now = g3.Add(time.Hour)
)

// app is a valid feed entry whose compose file runs image.
func app(slug, name, image string) feed.Entry {
	files := map[string]string{"compose.yml": "services:\n  app:\n    image: " + image + "\n    environment:\n      GREETING: hello\n" +
		"x-winterflow:\n  expose:\n    - { container: app, port: 80 }\n  variables:\n    GREETING: {}\n"}
	return feed.Entry{
		Slug: slug, Name: name, Summary: name + " in a box", Keywords: []string{slug},
		LogoURL: "https://winterflow.io/catalog/logos/" + slug + ".png", Files: files, Digest: feed.Digest(files),
	}
}

// fetcher serves the feed published last, as a signed HTTP feed would:
// a 304 when asked with its ETag.
type fetcher struct {
	mu        sync.Mutex
	feed, sig []byte
	etag      string
	err       error
	asked     []string // the ETag of every fetch
}

func (f *fetcher) FetchCatalog(_ context.Context, etag string) (model.CatalogFetch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, etag)
	switch {
	case f.err != nil:
		return model.CatalogFetch{}, f.err
	case etag != "" && etag == f.etag:
		return model.CatalogFetch{NotModified: true, ETag: etag}, nil
	}
	return model.CatalogFetch{Feed: f.feed, Sig: f.sig, ETag: f.etag}, nil
}

// publish signs a feed of apps generated at with key.
func (f *fetcher) publish(t *testing.T, key ed25519.PrivateKey, at time.Time, apps ...feed.Entry) {
	t.Helper()
	raw, err := feed.Encode(feed.Feed{Format: feed.FormatVersion, GeneratedAt: at, Apps: apps})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.feed, f.sig, f.etag = raw, feed.Sign(key, raw), `"`+at.Format(time.RFC3339)+`"`
}

func (f *fetcher) etags() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.asked...)
}

type env struct {
	repo *templaterepo.Repo
	key  ed25519.PrivateKey
	feed *fetcher
	sync *catalog.Syncer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := dbtest.Open(t)
	repo, err := templaterepo.New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{repo: repo, key: key, feed: &fetcher{}}
	e.sync = e.syncer(repo, pub)
	return e
}

// syncer is another process's Syncer (its own ETag) over repo.
func (e *env) syncer(repo port.CatalogRepository, pub ed25519.PublicKey) *catalog.Syncer {
	return catalog.New(catalog.Deps{Repo: repo, Fetcher: e.feed, Key: pub, Log: dbtest.Log, Now: func() time.Time { return now }})
}

func (e *env) run(t *testing.T) catalog.Result {
	t.Helper()
	res, err := e.sync.Sync(dbtest.Ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// states are the catalog templates by slug.
func (e *env) states(t *testing.T) map[string]model.CatalogState {
	t.Helper()
	list, err := e.repo.ListCatalog(dbtest.Ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]model.CatalogState{}
	for _, st := range list {
		out[st.Slug] = st
	}
	return out
}

func TestSyncInsertsThenReadsNotModified(t *testing.T) {
	e := newEnv(t)
	e.feed.publish(t, e.key, g1, app("budget", "Actual Budget", "actual:1"), app("notes", "Notes", "notes:1"))
	if res := e.run(t); res.Inserted != 2 || res.NotModified {
		t.Fatalf("first sync = %+v", res)
	}
	st := e.states(t)
	if len(st) != 2 || st["budget"].Name != "Actual Budget" || st["budget"].LatestRevision != 1 ||
		st["budget"].Meta.Summary != "Actual Budget in a box" || st["notes"].FeedAt == nil || !st["notes"].FeedAt.Equal(g1) {
		t.Fatalf("states = %+v", st)
	}
	if res := e.run(t); !res.NotModified {
		t.Fatalf("second sync = %+v", res)
	}
	if got := e.feed.etags(); len(got) != 2 || got[0] != "" || got[1] != `"2026-10-14T20:00:00Z"` {
		t.Fatalf("asked with %q", got)
	}
}

// A feed whose signature does not verify is refused as a whole: nothing
// is written and the next sync fetches it again.
func TestSyncRefusesABadSignature(t *testing.T) {
	e := newEnv(t)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	e.feed.publish(t, other, g1, app("budget", "Budget", "actual:1"))
	if _, err := e.sync.Sync(dbtest.Ctx(t)); !errors.Is(err, feed.ErrSignature) {
		t.Fatalf("another key: %v", err)
	}
	e.feed.publish(t, e.key, g1, app("budget", "Budget", "actual:1"))
	e.feed.mu.Lock()
	e.feed.feed = append(e.feed.feed[:len(e.feed.feed)-1:len(e.feed.feed)-1], ' ', '}')
	e.feed.mu.Unlock()
	if _, err := e.sync.Sync(dbtest.Ctx(t)); !errors.Is(err, feed.ErrSignature) {
		t.Fatalf("a changed feed: %v", err)
	}
	if st := e.states(t); len(st) != 0 {
		t.Fatalf("a refused feed wrote %+v", st)
	}
	if got := e.feed.etags(); got[1] != "" {
		t.Fatalf("a refused feed's ETag was kept: %q", got)
	}
}

// An unknown format or a feed without apps is refused as a whole: no
// template is withdrawn.
func TestSyncRefusesAnUnusableFeed(t *testing.T) {
	e := newEnv(t)
	e.feed.publish(t, e.key, g1, app("budget", "Budget", "actual:1"))
	e.run(t)
	for name, raw := range map[string]string{
		"format 2": `{"format":2,"generated_at":"2026-10-15T20:00:00Z","apps":[{"slug":"x"}]}`,
		"no apps":  `{"format":1,"generated_at":"2026-10-15T20:00:00Z","apps":[]}`,
	} {
		e.feed.mu.Lock()
		e.feed.feed, e.feed.sig, e.feed.etag = []byte(raw), feed.Sign(e.key, []byte(raw)), `"`+name+`"`
		e.feed.mu.Unlock()
		if _, err := e.sync.Sync(dbtest.Ctx(t)); !errors.Is(err, feed.ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if st := e.states(t)["budget"]; st.WithdrawnAt != nil {
		t.Fatalf("withdrawn by a refused feed: %+v", st)
	}
}

// An invalid entry is skipped: a new one is not inserted, an existing one
// keeps its last good revision and is not withdrawn.
func TestSyncSkipsAnInvalidEntry(t *testing.T) {
	e := newEnv(t)
	e.feed.publish(t, e.key, g1, app("budget", "Budget", "actual:1"))
	e.run(t)
	broken := app("budget", "Budget", "actual:2")
	broken.Digest = "sha256:stale"
	bad := app("Bad Slug", "Bad", "x:1")
	e.feed.publish(t, e.key, g2, broken, bad, app("notes", "Notes", "notes:1"))
	if res := e.run(t); res.Invalid != 2 || res.Inserted != 1 || res.Withdrawn != 0 {
		t.Fatalf("sync = %+v", res)
	}
	st := e.states(t)
	if len(st) != 2 || st["budget"].LatestRevision != 1 || st["budget"].WithdrawnAt != nil || st["budget"].Digest != app("budget", "", "actual:1").Digest {
		t.Fatalf("states = %+v", st)
	}
}

// Only a change of files makes a revision; a name or metadata change is
// made in place (no "upgrade available" for a fixed description).
func TestSyncRevisesOnlyWhenFilesChange(t *testing.T) {
	e := newEnv(t)
	e.feed.publish(t, e.key, g1, app("budget", "Budget", "actual:1"))
	e.run(t)
	fixed := app("budget", "Actual Budget", "actual:1")
	fixed.Description = "Envelope budgeting."
	e.feed.publish(t, e.key, g2, fixed)
	if res := e.run(t); res.Updated != 1 || res.Revised != 0 {
		t.Fatalf("metadata sync = %+v", res)
	}
	if st := e.states(t)["budget"]; st.LatestRevision != 1 || st.Name != "Actual Budget" || st.Meta.Description != "Envelope budgeting." {
		t.Fatalf("after metadata = %+v", st)
	}
	e.feed.publish(t, e.key, g3, app("budget", "Actual Budget", "actual:2"))
	if res := e.run(t); res.Revised != 1 {
		t.Fatalf("files sync = %+v", res)
	}
	if st := e.states(t)["budget"]; st.LatestRevision != 2 || st.Digest != app("budget", "", "actual:2").Digest {
		t.Fatalf("after files = %+v", st)
	}
}

// A template the feed drops is withdrawn; when a later feed lists it
// again it comes back, on the same revision. A renamed slug is a new
// template and the old one is withdrawn.
func TestSyncWithdrawsAndRestores(t *testing.T) {
	e := newEnv(t)
	e.feed.publish(t, e.key, g1, app("budget", "Budget", "actual:1"), app("notes", "Notes", "notes:1"))
	e.run(t)
	e.feed.publish(t, e.key, g2, app("budget", "Budget", "actual:1"), app("notes-app", "Notes", "notes:1"))
	if res := e.run(t); res.Withdrawn != 1 || res.Inserted != 1 || res.Unchanged != 1 {
		t.Fatalf("drop = %+v", res)
	}
	st := e.states(t)
	if st["notes"].WithdrawnAt == nil || !st["notes"].WithdrawnAt.Equal(now) || st["notes-app"].WithdrawnAt != nil || st["notes-app"].ID == st["notes"].ID {
		t.Fatalf("after the rename = %+v", st)
	}
	e.feed.publish(t, e.key, g3, app("budget", "Budget", "actual:1"), app("notes", "Notes", "notes:1"), app("notes-app", "Notes", "notes:1"))
	if res := e.run(t); res.Updated != 1 || res.Withdrawn != 0 {
		t.Fatalf("restore = %+v", res)
	}
	if st := e.states(t)["notes"]; st.WithdrawnAt != nil || st.LatestRevision != 1 {
		t.Fatalf("restored = %+v", st)
	}
}

// A feed older than one already applied is refused, also by a process
// that never saw the newer one (a restart, another replica): a stale
// mirror never rolls templates back. A feed dated in the future is
// refused too: stamping it would refuse every real feed after it.
func TestSyncRefusesAnOlderOrFutureFeed(t *testing.T) {
	e := newEnv(t)
	e.feed.publish(t, e.key, g2, app("budget", "Budget", "actual:2"))
	e.run(t)
	e.feed.publish(t, e.key, g1, app("budget", "Budget", "actual:1"), app("notes", "Notes", "notes:1"))
	if _, err := e.sync.Sync(dbtest.Ctx(t)); !errors.Is(err, catalog.ErrStale) {
		t.Fatalf("an older feed: %v", err)
	}
	restarted := e.syncer(e.repo, e.key.Public().(ed25519.PublicKey))
	if _, err := restarted.Sync(dbtest.Ctx(t)); !errors.Is(err, catalog.ErrStale) {
		t.Fatalf("an older feed after a restart: %v", err)
	}
	if st := e.states(t); len(st) != 1 || st["budget"].Digest != app("budget", "", "actual:2").Digest {
		t.Fatalf("states = %+v", st)
	}
	e.feed.publish(t, e.key, now.Add(catalog.MaxClockSkew+time.Minute), app("budget", "Budget", "actual:3"))
	if _, err := e.sync.Sync(dbtest.Ctx(t)); !errors.Is(err, catalog.ErrFuture) {
		t.Fatalf("a future feed: %v", err)
	}
	if st := e.states(t)["budget"]; !st.FeedAt.Equal(g2) {
		t.Fatalf("a refused feed was stamped: %v", st.FeedAt)
	}
}

// failingRepo fails ApplyCatalog while fail is set.
type failingRepo struct {
	port.CatalogRepository
	fail atomic.Bool
}

func (r *failingRepo) ApplyCatalog(ctx context.Context, base *model.CatalogState, e model.CatalogEntry) (model.CatalogOutcome, error) {
	if r.fail.Load() {
		return "", errors.New("database is locked")
	}
	return r.CatalogRepository.ApplyCatalog(ctx, base, e)
}

// A sync that failed midway keeps no ETag: the next one fetches the whole
// feed again instead of a 304 hiding what was never applied.
func TestSyncRetriesAFailedApply(t *testing.T) {
	e := newEnv(t)
	repo := &failingRepo{CatalogRepository: e.repo}
	repo.fail.Store(true)
	s := e.syncer(repo, e.key.Public().(ed25519.PublicKey))
	e.feed.publish(t, e.key, g1, app("budget", "Budget", "actual:1"))
	if _, err := s.Sync(dbtest.Ctx(t)); err == nil {
		t.Fatal("a failed apply reported success")
	}
	repo.fail.Store(false)
	res, err := s.Sync(dbtest.Ctx(t))
	if err != nil || res.NotModified || res.Inserted != 1 {
		t.Fatalf("retry = %+v, %v", res, err)
	}
	if got := e.feed.etags(); len(got) != 2 || got[1] != "" {
		t.Fatalf("asked with %q", got)
	}
}

// Replicas syncing one feed at once: each template is written once, and
// every replica ends without an error.
func TestSyncRace(t *testing.T) {
	e := newEnv(t)
	pub := e.key.Public().(ed25519.PublicKey)
	e.feed.publish(t, e.key, g1, app("budget", "Budget", "actual:1"), app("notes", "Notes", "notes:1"))
	var wg sync.WaitGroup
	results := make([]catalog.Result, 4)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := e.syncer(e.repo, pub).Sync(dbtest.Ctx(t))
			if err != nil {
				t.Error(err)
			}
			results[i] = res
		}()
	}
	wg.Wait()
	inserted := 0
	for _, r := range results {
		inserted += r.Inserted
	}
	if inserted != 2 {
		t.Fatalf("results = %+v", results)
	}
	if st := e.states(t); len(st) != 2 || st["budget"].LatestRevision != 1 {
		t.Fatalf("states = %+v", st)
	}
}

// With the catalog switched off every catalog template is withdrawn once;
// switched on again, the next sync restores them.
func TestWithdrawAll(t *testing.T) {
	e := newEnv(t)
	e.feed.publish(t, e.key, g1, app("budget", "Budget", "actual:1"), app("notes", "Notes", "notes:1"))
	e.run(t)
	ctx := dbtest.Ctx(t)
	if n, err := catalog.WithdrawAll(ctx, e.repo, now); err != nil || n != 2 {
		t.Fatalf("withdraw all = %d, %v", n, err)
	}
	if n, err := catalog.WithdrawAll(ctx, e.repo, now); err != nil || n != 0 {
		t.Fatalf("again = %d, %v", n, err)
	}
	restarted := e.syncer(e.repo, e.key.Public().(ed25519.PublicKey))
	if res, err := restarted.Sync(ctx); err != nil || res.Updated != 2 {
		t.Fatalf("restore = %+v, %v", res, err)
	}
	for slug, st := range e.states(t) {
		if st.WithdrawnAt != nil {
			t.Fatalf("%s still withdrawn", slug)
		}
	}
}

// Run syncs after First, then every Every, and a failure is not fatal.
func TestRun(t *testing.T) {
	e := newEnv(t)
	e.feed.err = errors.New("connection refused")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		e.sync.Run(ctx, catalog.Schedule{First: time.Millisecond, Every: 5 * time.Millisecond, Timeout: time.Second})
		close(done)
	}()
	deadline := time.After(10 * time.Second)
	for len(e.feed.etags()) < 3 {
		select {
		case <-deadline:
			t.Fatal("Run did not keep syncing after a failure")
		case <-time.After(time.Millisecond):
		}
	}
	e.feed.mu.Lock()
	e.feed.err = nil
	e.feed.mu.Unlock()
	e.feed.publish(t, e.key, g1, app("budget", "Budget", "actual:1"))
	for len(e.states(t)) == 0 {
		select {
		case <-deadline:
			t.Fatal("Run never applied the feed")
		case <-time.After(time.Millisecond):
		}
	}
	// The next tick reads a 304.
	for n := len(e.feed.etags()); len(e.feed.etags()) < n+2; {
		select {
		case <-deadline:
			t.Fatal("Run stopped after applying the feed")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	<-done
}

// A zero Schedule takes the defaults (the first sync a minute away): a
// cancelled Run returns without syncing.
func TestRunDefaults(t *testing.T) {
	e := newEnv(t)
	s := catalog.New(catalog.Deps{Repo: e.repo, Fetcher: e.feed, Log: dbtest.Log})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.Run(ctx, catalog.Schedule{})
	if got := e.feed.etags(); len(got) != 0 {
		t.Fatalf("synced %d times", len(got))
	}
}
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/domain/usecase/catalog/`
Expected: FAIL: `no non-test Go files in .../internal/domain/usecase/catalog`.

- [ ] **Step 3: Write the Syncer**

Create `internal/domain/usecase/catalog/sync.go`:

````go
// Package catalog is the API's catalog sync: it fetches the signed feed,
// checks it, and brings the catalog templates to it (new templates, new
// revisions when files change, metadata in place, withdrawals). One
// goroutine per API process runs it (Run), off the standalone bridge's
// goroutine; every write is conditional, so replicas syncing at once are
// harmless.
package catalog

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"sync"
	"time"

	feed "winterflow/internal/domain/catalog"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/template"
	"winterflow/pkg/logger"
)

// Defaults of Schedule.
const (
	DefaultFirst   = time.Minute
	DefaultEvery   = 24 * time.Hour
	DefaultTimeout = 5 * time.Minute
	// MaxClockSkew is how far in the future a feed's generated_at may be:
	// a feed stamped further ahead (a builder with a wrong clock) would
	// make every later feed read as older, so it is refused.
	MaxClockSkew = time.Hour
)

// Errors that refuse a whole feed (besides feed.ErrSignature and
// feed.ErrInvalid).
var (
	// ErrStale: the feed is older than one already applied, by this
	// process or another replica.
	ErrStale = errors.New("the catalog feed is older than the one applied")
	// ErrFuture: the feed's generated_at is past MaxClockSkew ahead.
	ErrFuture = errors.New("the catalog feed is dated in the future")
)

// anyFeed is the feedAt of a withdrawal no feed can outrank (the catalog
// switched off).
var anyFeed = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// Deps wires a Syncer. Now may be nil (time.Now).
type Deps struct {
	Repo    port.CatalogRepository
	Fetcher port.CatalogFetcher
	Key     ed25519.PublicKey
	Log     *logger.Logger
	Now     func() time.Time
}

// Syncer brings the catalog templates to the feed.
type Syncer struct {
	repo    port.CatalogRepository
	fetcher port.CatalogFetcher
	key     ed25519.PublicKey
	log     *logger.Logger
	now     func() time.Time

	mu sync.Mutex // one sync at a time
	// etag is the last fully applied feed's: kept only after a sync
	// succeeded, so a failed one fetches the whole feed again instead of
	// reading a 304.
	etag string
}

// New builds a Syncer.
func New(d Deps) *Syncer {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Syncer{repo: d.Repo, fetcher: d.Fetcher, key: d.Key, log: d.Log, now: now}
}

// Result is what one sync did.
type Result struct {
	NotModified bool
	Inserted    int
	Revised     int
	Updated     int
	Unchanged   int
	// Skipped entries another replica (or a newer feed) wrote first.
	Skipped int
	// Invalid entries failed the check: logged and left alone (an existing
	// template keeps its last good revision and is not withdrawn).
	Invalid   int
	Withdrawn int
}

// Sync fetches the feed (with the last applied ETag) and applies it. The
// whole feed is refused when its signature does not verify against the
// key, its format is unknown, it lists no app, or it is older than a feed
// already applied (ErrStale) or dated in the future (ErrFuture). Each
// valid entry is applied; templates the feed no longer lists are
// withdrawn; the feed's generated_at is then stamped on every catalog
// template.
func (s *Syncer) Sync(ctx context.Context) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.fetcher.FetchCatalog(ctx, s.etag)
	if err != nil {
		return Result{}, err
	}
	if got.NotModified {
		return Result{NotModified: true}, nil
	}
	if err := feed.Verify(s.key, got.Feed, got.Sig); err != nil {
		return Result{}, err
	}
	f, err := feed.Decode(got.Feed)
	if err != nil {
		return Result{}, err
	}
	now := s.now()
	if f.GeneratedAt.After(now.Add(MaxClockSkew)) {
		return Result{}, fmt.Errorf("%w: generated_at %s", ErrFuture, f.GeneratedAt.UTC().Format(time.RFC3339))
	}
	states, err := s.repo.ListCatalog(ctx)
	if err != nil {
		return Result{}, err
	}
	bySlug := make(map[string]*model.CatalogState, len(states))
	for i := range states {
		st := &states[i]
		bySlug[st.Slug] = st
		if st.FeedAt != nil && f.GeneratedAt.Before(*st.FeedAt) {
			return Result{}, fmt.Errorf("%w: generated %s, applied %s", ErrStale,
				f.GeneratedAt.UTC().Format(time.RFC3339), st.FeedAt.UTC().Format(time.RFC3339))
		}
	}
	var res Result
	listed := make(map[string]bool, len(f.Apps))
	for _, e := range f.Apps {
		listed[e.Slug] = true
		if err := s.apply(ctx, e, bySlug[e.Slug], f.GeneratedAt, &res); err != nil {
			return res, err
		}
	}
	for _, st := range states {
		if listed[st.Slug] || st.WithdrawnAt != nil {
			continue
		}
		ok, err := s.repo.WithdrawCatalog(ctx, st.ID, now, f.GeneratedAt)
		if err != nil {
			return res, fmt.Errorf("catalog %s: %w", st.Slug, err)
		}
		if ok {
			res.Withdrawn++
		}
	}
	if err := s.repo.StampCatalog(ctx, f.GeneratedAt); err != nil {
		return res, err
	}
	s.etag = got.ETag
	return res, nil
}

// apply checks one entry and writes it unless the template already says
// the same.
func (s *Syncer) apply(ctx context.Context, e feed.Entry, base *model.CatalogState, feedAt time.Time, res *Result) error {
	body, meta, err := e.Check()
	if err != nil {
		res.Invalid++
		s.log.Warn("catalog: skipping an invalid entry", "slug", e.Slug, "error", err)
		return nil
	}
	if base != nil && base.Digest == e.Digest && base.Name == e.Name && base.Meta.Equal(meta) && base.WithdrawnAt == nil {
		res.Unchanged++
		return nil
	}
	raw, err := template.Encode(body)
	if err != nil {
		return err
	}
	out, err := s.repo.ApplyCatalog(ctx, base, model.CatalogEntry{
		Slug: e.Slug, Name: e.Name, Meta: meta, Body: raw, Digest: e.Digest, FeedAt: feedAt,
	})
	if err != nil {
		return fmt.Errorf("catalog %s: %w", e.Slug, err)
	}
	switch out {
	case model.CatalogInserted:
		res.Inserted++
	case model.CatalogRevised:
		res.Revised++
	case model.CatalogUpdated:
		res.Updated++
	default:
		res.Skipped++
	}
	return nil
}

// Schedule is when Run syncs; zero fields take the defaults.
type Schedule struct {
	// First is the wait before the first sync (DefaultFirst).
	First time.Duration
	// Every is the wait between syncs (DefaultEvery; CATALOG_REFRESH).
	Every time.Duration
	// Timeout bounds one sync (DefaultTimeout).
	Timeout time.Duration
}

// Run syncs after sch.First, then every sch.Every, until ctx ends. A
// failed sync is logged and retried at the next tick; nothing is fatal.
func (s *Syncer) Run(ctx context.Context, sch Schedule) {
	if sch.First <= 0 {
		sch.First = DefaultFirst
	}
	if sch.Every <= 0 {
		sch.Every = DefaultEvery
	}
	if sch.Timeout <= 0 {
		sch.Timeout = DefaultTimeout
	}
	timer := time.NewTimer(sch.First)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		s.runOnce(ctx, sch.Timeout)
		timer.Reset(sch.Every)
	}
}

func (s *Syncer) runOnce(ctx context.Context, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := s.Sync(ctx)
	switch {
	case err != nil:
		s.log.Error("catalog sync failed", "error", err)
	case res.NotModified:
		s.log.Debug("catalog: the feed has not changed")
	default:
		s.log.Info("catalog synced", "inserted", res.Inserted, "revised", res.Revised, "updated", res.Updated,
			"unchanged", res.Unchanged, "skipped", res.Skipped, "invalid", res.Invalid, "withdrawn", res.Withdrawn)
	}
}

// WithdrawAll withdraws every catalog template: CATALOG_URL is empty, so
// the catalog is hidden (instances keep working; setting the URL again
// restores the templates at the next sync). It returns how many it
// withdrew.
func WithdrawAll(ctx context.Context, repo port.CatalogRepository, now time.Time) (int, error) {
	states, err := repo.ListCatalog(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, st := range states {
		if st.WithdrawnAt != nil {
			continue
		}
		ok, err := repo.WithdrawCatalog(ctx, st.ID, now, anyFeed)
		if err != nil {
			return n, err
		}
		if ok {
			n++
		}
	}
	return n, nil
}
````

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/domain/usecase/catalog/ && go test -race -cover ./internal/domain/usecase/catalog/`
Expected: PASS in about 5 s, about 94% of statements.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/usecase/catalog
git commit -m "feat(catalog): the sync verifies the feed and brings the catalog templates to it" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 8: Configuration and `StartCatalog` in api and standalone

**Files:**
- Modify: `pkg/config/config.go` (`DefaultCatalogURL`, `DefaultCatalogPublicKey`, `GetCatalogURL`, `GetCatalogPublicKey`, `GetCatalogRefresh`)
- Modify: `.env.example` (a Catalog section)
- Create: `internal/infra/bootstrap/catalog.go` (`StartCatalog`)
- Modify: `internal/infra/bootstrap/deps.go` (`Deps.CatalogRepository`), `core.go` (wires it), `api.go` and `standalone.go` (call `StartCatalog` after `StartRollouts`)
- Test: `pkg/config/config_test.go`
- Test: `internal/infra/bootstrap/catalog_test.go`

**Interfaces:**
- Consumes: Task 2's `feed.ParsePublicKey`; Task 4's `port.CatalogRepository` (the `*template.Repo` WireCore already builds); Task 6's `catalogfeed.New`; Task 7's `catalogsync.New`, `Schedule`, `WithdrawAll`, `DefaultTimeout`.
- Produces:
  - `config.DefaultCatalogURL = "https://winterflow.io/catalog/catalog.json"`, `config.DefaultCatalogPublicKey = "QAyhWPQDK0IS8UvVtNA3X/vXwSisba2Lx/7bxI3CG0E="`; `(*ServerConfig).GetCatalogURL() string` (`LookupEnv`: set but empty is ""), `GetCatalogPublicKey() string`, `GetCatalogRefresh() time.Duration` (24h default, 5m floor, invalid is 24h).
  - `Deps.CatalogRepository port.CatalogRepository`.
  - `func StartCatalog(ctx context.Context, d *Deps)`; unexported `startCatalog(ctx, d, sch catalogsync.Schedule) *catalogsync.Syncer` (nil when the sync is off).

- [ ] **Step 1: Write the failing tests**

Modify `pkg/config/config_test.go` (`git apply` this diff):

````diff
diff --git a/pkg/config/config_test.go b/pkg/config/config_test.go
index 7676a04..87c68a8 100644
--- a/pkg/config/config_test.go
+++ b/pkg/config/config_test.go
@@ -494,3 +494,32 @@ func TestAllowsPrivateAlertTargets(t *testing.T) {
 		t.Error("an unknown value must keep the topology default")
 	}
 }
+
+func TestCatalogConfig(t *testing.T) {
+	c := NewServerConfig("distributed")
+	unsetenv(t, "CATALOG_URL")
+	unsetenv(t, "CATALOG_PUBLIC_KEY")
+	unsetenv(t, "CATALOG_REFRESH")
+	if c.GetCatalogURL() != DefaultCatalogURL || c.GetCatalogPublicKey() != DefaultCatalogPublicKey || c.GetCatalogRefresh() != 24*time.Hour {
+		t.Fatalf("defaults = %q %q %v", c.GetCatalogURL(), c.GetCatalogPublicKey(), c.GetCatalogRefresh())
+	}
+	// Set but empty switches the catalog off; an empty key is the default.
+	t.Setenv("CATALOG_URL", "")
+	t.Setenv("CATALOG_PUBLIC_KEY", " ")
+	if c.GetCatalogURL() != "" || c.GetCatalogPublicKey() != DefaultCatalogPublicKey {
+		t.Fatalf("empty = %q %q", c.GetCatalogURL(), c.GetCatalogPublicKey())
+	}
+	t.Setenv("CATALOG_URL", " http://127.0.0.1:8080/catalog.json ")
+	t.Setenv("CATALOG_PUBLIC_KEY", "a2V5")
+	if c.GetCatalogURL() != "http://127.0.0.1:8080/catalog.json" || c.GetCatalogPublicKey() != "a2V5" {
+		t.Fatalf("set = %q %q", c.GetCatalogURL(), c.GetCatalogPublicKey())
+	}
+	for v, want := range map[string]time.Duration{
+		"6h": 6 * time.Hour, "1m": 5 * time.Minute, "soon": 24 * time.Hour, "-1h": 24 * time.Hour, "0": 24 * time.Hour,
+	} {
+		t.Setenv("CATALOG_REFRESH", v)
+		if got := c.GetCatalogRefresh(); got != want {
+			t.Errorf("CATALOG_REFRESH=%s: %v, want %v", v, got, want)
+		}
+	}
+}
````

Create `internal/infra/bootstrap/catalog_test.go`:

````go
package bootstrap

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	feed "winterflow/internal/domain/catalog"
	"winterflow/internal/domain/model"
	catalogsync "winterflow/internal/domain/usecase/catalog"
	"winterflow/internal/infra/db"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// catalogDeps is WireCore over a fresh SQLite database.
func catalogDeps(t *testing.T) (*Deps, context.Context) {
	t.Helper()
	t.Setenv("DATABASE_URL", "sqlite://"+filepath.Join(t.TempDir(), "wf.sqlite"))
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	cfg := config.NewServerConfig("standalone")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	conn, err := db.Open(log, cfg.GetDbURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Shutdown() })
	deps, _ := WireCore(ctx, membus.NewBus(log), conn, cfg, log)
	return deps, ctx
}

// waitCatalog waits until cond holds for the catalog templates.
func waitCatalog(t *testing.T, d *Deps, ctx context.Context, cond func([]model.CatalogState) bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		list, err := d.CatalogRepository.ListCatalog(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if cond(list) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("catalog = %+v", list)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTheDefaultCatalogKeyParses(t *testing.T) {
	if _, err := feed.ParsePublicKey(config.DefaultCatalogPublicKey); err != nil {
		t.Fatal(err)
	}
}

// StartCatalog's sync fetches CATALOG_URL, verifies it with
// CATALOG_PUBLIC_KEY and fills the catalog.
func TestStartCatalogSyncs(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	files := map[string]string{"compose.yml": "services:\n  app:\n    image: example/notes:1\n"}
	raw, err := feed.Encode(feed.Feed{Format: feed.FormatVersion, GeneratedAt: time.Now().UTC().Truncate(time.Second), Apps: []feed.Entry{
		{Slug: "notes", Name: "Notes", Files: files, Digest: feed.Digest(files)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/catalog.json":
			_, _ = w.Write(raw)
		case "/catalog.json.sig":
			_, _ = w.Write(feed.Sign(key, raw))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("CATALOG_URL", srv.URL+"/catalog.json")
	t.Setenv("CATALOG_PUBLIC_KEY", base64.StdEncoding.EncodeToString(pub))
	d, ctx := catalogDeps(t)
	if s := startCatalog(ctx, d, catalogsync.Schedule{First: time.Millisecond}); s == nil {
		t.Fatal("the sync did not start")
	}
	waitCatalog(t, d, ctx, func(l []model.CatalogState) bool { return len(l) == 1 && l[0].Slug == "notes" })
}

// CATALOG_URL empty withdraws every catalog template once; a URL or key
// that can't be used leaves the sync off.
func TestStartCatalogOff(t *testing.T) {
	t.Setenv("CATALOG_URL", "")
	d, ctx := catalogDeps(t)
	if err := d.TemplateRepository.CreateTemplate(ctx, model.Template{ID: "00000000-0000-7000-8000-000000000001", Source: model.TemplateSourceCatalog, Slug: "notes", Name: "Notes"},
		model.TemplateRevision{Body: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	StartCatalog(ctx, d)
	waitCatalog(t, d, ctx, func(l []model.CatalogState) bool { return len(l) == 1 && l[0].WithdrawnAt != nil })

	for name, env := range map[string][2]string{
		"an http URL": {"http://winterflow.io/catalog/catalog.json", ""},
		"a bad key":   {config.DefaultCatalogURL, "not a key"},
		"a short key": {config.DefaultCatalogURL, base64.StdEncoding.EncodeToString([]byte("short"))},
		"not a URL":   {"https://[bad", ""},
	} {
		t.Setenv("CATALOG_URL", env[0])
		t.Setenv("CATALOG_PUBLIC_KEY", env[1])
		if s := startCatalog(ctx, d, catalogsync.Schedule{}); s != nil {
			t.Errorf("%s: the sync started", name)
		}
	}
}
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./pkg/config/ ./internal/infra/bootstrap/`
Expected: FAIL to build: `c.GetCatalogURL undefined`, `undefined: DefaultCatalogURL`, and in bootstrap `undefined: startCatalog`, `d.CatalogRepository undefined`.

- [ ] **Step 3: Write the configuration and the wiring**

Modify `pkg/config/config.go` (`git apply` this diff):

````diff
diff --git a/pkg/config/config.go b/pkg/config/config.go
index 1c6015e..e15a574 100644
--- a/pkg/config/config.go
+++ b/pkg/config/config.go
@@ -425,6 +425,47 @@ func (c *ServerConfig) IsStandalone() bool {
 	return c.mode == "standalone"
 }
 
+// DefaultCatalogURL is where winterflow.io publishes the catalog feed.
+const DefaultCatalogURL = "https://winterflow.io/catalog/catalog.json"
+
+// DefaultCatalogPublicKey verifies the published catalog feed: the ed25519
+// public key (standard base64) whose private half signs winterflow.io's
+// build of the feed.
+const DefaultCatalogPublicKey = "QAyhWPQDK0IS8UvVtNA3X/vXwSisba2Lx/7bxI3CG0E="
+
+// minCatalogRefresh keeps CATALOG_REFRESH from polling the feed more than
+// every 5 minutes.
+const minCatalogRefresh = 5 * time.Minute
+
+// GetCatalogURL is the catalog feed the API syncs (api, standalone).
+// Unset is DefaultCatalogURL; set but empty switches the catalog off.
+func (c *ServerConfig) GetCatalogURL() string {
+	if v, ok := os.LookupEnv("CATALOG_URL"); ok {
+		return strings.TrimSpace(v)
+	}
+	return DefaultCatalogURL
+}
+
+// GetCatalogPublicKey is the key the feed's signature must verify against
+// (api, standalone; default DefaultCatalogPublicKey).
+func (c *ServerConfig) GetCatalogPublicKey() string {
+	if v := strings.TrimSpace(os.Getenv("CATALOG_PUBLIC_KEY")); v != "" {
+		return v
+	}
+	return DefaultCatalogPublicKey
+}
+
+// GetCatalogRefresh is how often the API syncs the catalog feed (api,
+// standalone): a Go duration, default 24h; an unparsable or non-positive
+// value is 24h, anything under 5m is raised to 5m.
+func (c *ServerConfig) GetCatalogRefresh() time.Duration {
+	d, err := time.ParseDuration(strings.TrimSpace(os.Getenv("CATALOG_REFRESH")))
+	if err != nil || d <= 0 {
+		return 24 * time.Hour
+	}
+	return max(d, minCatalogRefresh)
+}
+
 // GetGitHubReleasesURL is the base URL self-update binaries are downloaded
 // from. The expected layout is {base}/{vYYMMDD.N}/winterflow-{component}-{os}-{arch}
 // where component is "standalone" or "agent" — the asset naming produced by
````

Create `internal/infra/bootstrap/catalog.go`:

````go
package bootstrap

import (
	"context"
	"time"

	feed "winterflow/internal/domain/catalog"
	catalogsync "winterflow/internal/domain/usecase/catalog"
	"winterflow/internal/infra/catalogfeed"
)

// StartCatalog starts the catalog sync (api, standalone): one goroutine per
// process, never the standalone bridge's, syncing a minute after start and
// then every CATALOG_REFRESH (each sync bounded by 5 minutes). CATALOG_URL
// empty switches the catalog off: every catalog template is withdrawn once
// (hidden from lists and new installs; instances keep working; setting the
// URL again restores them at the next sync). A CATALOG_URL or
// CATALOG_PUBLIC_KEY that can't be used is logged and leaves the sync off:
// the API runs without it.
func StartCatalog(ctx context.Context, d *Deps) {
	startCatalog(ctx, d, catalogsync.Schedule{Every: d.Cfg.GetCatalogRefresh()})
}

// startCatalog is StartCatalog with its schedule (tests); it returns the
// running Syncer, nil when the sync is off.
func startCatalog(ctx context.Context, d *Deps, sch catalogsync.Schedule) *catalogsync.Syncer {
	url := d.Cfg.GetCatalogURL()
	if url == "" {
		go func() {
			ctx, cancel := context.WithTimeout(ctx, catalogsync.DefaultTimeout)
			defer cancel()
			n, err := catalogsync.WithdrawAll(ctx, d.CatalogRepository, time.Now())
			if err != nil {
				d.Log.Error("catalog off: withdrawing the catalog templates failed", "error", err)
				return
			}
			if n > 0 {
				d.Log.Info("catalog off (CATALOG_URL is empty): withdrew the catalog templates", "count", n)
			}
		}()
		return nil
	}
	key, err := feed.ParsePublicKey(d.Cfg.GetCatalogPublicKey())
	if err != nil {
		d.Log.Error("catalog sync off: CATALOG_PUBLIC_KEY", "error", err)
		return nil
	}
	fetcher, err := catalogfeed.New(url)
	if err != nil {
		d.Log.Error("catalog sync off: CATALOG_URL", "error", err)
		return nil
	}
	s := catalogsync.New(catalogsync.Deps{Repo: d.CatalogRepository, Fetcher: fetcher, Key: key, Log: d.Log})
	go s.Run(ctx, sch)
	return s
}
````

Modify `internal/infra/bootstrap/deps.go`, `internal/infra/bootstrap/core.go`, `internal/infra/bootstrap/api.go`, `internal/infra/bootstrap/standalone.go` (`git apply` this diff):

````diff
diff --git a/internal/infra/bootstrap/api.go b/internal/infra/bootstrap/api.go
index f295597..8bbe48d 100644
--- a/internal/infra/bootstrap/api.go
+++ b/internal/infra/bootstrap/api.go
@@ -37,6 +37,7 @@ func BootstrapAPI(ctx context.Context, log *logger.Logger, cfg *config.ServerCon
 	// WireCore has started the response subscriber, so replies reach the
 	// waiting steps; the Hub consumes the request queue independently.
 	StartRollouts(ctx, deps)
+	StartCatalog(ctx, deps)
 	return deps
 }
 
diff --git a/internal/infra/bootstrap/core.go b/internal/infra/bootstrap/core.go
index df204ae..39618d0 100644
--- a/internal/infra/bootstrap/core.go
+++ b/internal/infra/bootstrap/core.go
@@ -98,6 +98,7 @@ func WireCore(ctx context.Context, b bus.Bus, conn *db.DB, cfg *config.ServerCon
 		ServerStorageRepository:  r.serverStorage,
 		SharedVariableRepository: r.sharedVar,
 		TemplateRepository:       r.template,
+		CatalogRepository:        r.template,
 		Instances:                instances,
 		Rollouts:                 queue,
 		CommandDispatcher:        dispatcher,
diff --git a/internal/infra/bootstrap/deps.go b/internal/infra/bootstrap/deps.go
index 63ae78c..d55d0e8 100644
--- a/internal/infra/bootstrap/deps.go
+++ b/internal/infra/bootstrap/deps.go
@@ -35,6 +35,9 @@ type Deps struct {
 	SharedVariableRepository port.SharedVariableRepository
 	// TemplateRepository stores templates, their revisions and instances.
 	TemplateRepository port.TemplateRepository
+	// CatalogRepository is the same repository as the catalog sync sees
+	// it (StartCatalog).
+	CatalogRepository port.CatalogRepository
 	// Instances is the templates usecase the instance routes and the
 	// rollout queue's upgrade steps share.
 	Instances *templates.UseCase
diff --git a/internal/infra/bootstrap/standalone.go b/internal/infra/bootstrap/standalone.go
index d332cf2..05da37f 100644
--- a/internal/infra/bootstrap/standalone.go
+++ b/internal/infra/bootstrap/standalone.go
@@ -88,6 +88,7 @@ func BootstrapStandalone(ctx context.Context, log *logger.Logger, cfg *config.Se
 	// Only now can a dispatched command be consumed (membus drops messages
 	// without a subscriber), so the rollout worker starts after the bridge.
 	StartRollouts(ctx, deps)
+	StartCatalog(ctx, deps)
 
 	// Report the embedded server's capabilities (specs, IP, version, public
 	// key) through the same events path a distributed agent's registration
````

Document the three variables (CLAUDE.md's `.env.example` rule) in `.env.example` (`git apply` this diff):

````diff
diff --git a/.env.example b/.env.example
index 15eb6ea..19baeb4 100644
--- a/.env.example
+++ b/.env.example
@@ -171,6 +171,28 @@ HUB_CERT_EXT_PATH=data/ext.cnf
 # login), redirects included; validating a channel URL never connects.
 #ALERTS_ALLOW_PRIVATE_TARGETS=
 
+# --- Catalog (API/standalone) --------------------------------------------------
+
+# The template catalog: a signed feed (catalog.json and catalog.json.sig) the
+# API fetches a minute after start and then every CATALOG_REFRESH, with
+# If-None-Match (one conditional GET a day), a 30 s deadline per request and an
+# 8 MiB cap; https only (http only to a loopback host). Each app in it is a
+# read-only catalog template every organization can install. Set it empty
+# (CATALOG_URL=) to switch the catalog off: catalog templates are hidden from
+# lists and new installs (instances keep working) until it is set again.
+#CATALOG_URL=https://winterflow.io/catalog/catalog.json
+
+# The ed25519 public key (standard base64) the feed's signature must verify
+# against; a feed that does not verify is refused as a whole. Default: the key
+# of winterflow.io's published feed. Set it only to sync a feed you build and
+# sign yourself (`go run ./cmd/catalog keygen`, then `catalog build
+# --sign-key-env CATALOG_SIGNING_KEY`).
+#CATALOG_PUBLIC_KEY=QAyhWPQDK0IS8UvVtNA3X/vXwSisba2Lx/7bxI3CG0E=
+
+# How often the catalog feed is synced: a Go duration, default 24h; values
+# under 5m are raised to 5m.
+#CATALOG_REFRESH=24h
+
 # --- Ingress (embedded Caddy; agent/standalone only) ----------------------------
 
 # Master switch for the embedded reverse proxy. Default OFF (opt-in) — many
````

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./... && go test ./pkg/config/ ./internal/infra/bootstrap/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/config .env.example internal/infra/bootstrap
git commit -m "feat(catalog): CATALOG_URL, CATALOG_PUBLIC_KEY, CATALOG_REFRESH and the sync goroutine" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 9: Contract tests: the sync over HTTP, on SQLite and Postgres

The spec asks for the sync against an httptest server (ETag, bad signature, invalid entry skipped, withdraw and restore, two replicas racing) on both databases, including what `get-templates` then answers. The contract suite runs under `make test-pg`, so these tests run on Postgres there, with overlapping transactions. They exercise code from Tasks 4 to 8 and pass at once; a failure here is a bug in that code, to be fixed in its own package with a test there first.

**Files:**
- Test: `internal/app/web/contract/catalog_test.go` (create)

**Interfaces:**
- Consumes: `newEnv`, `newInstanceEnv`, `e.claim`, `e.foreignClient`, `e.dbCtx`, `shopCompose`, `testLog` (contract harness); `e.deps.CatalogRepository` (Task 8); `catalogfeed.New` (Task 6); `catalogsync.New`, `Result`, `ErrStale` (Task 7); `feed.Encode`, `feed.Sign`, `feed.Digest`, `feed.ErrSignature` (Task 2); `tuc.TemplateSummary.Catalog` (Task 5).
- Produces: nothing new for code (no route is added, so the `TestMain` route guard and the parity golden are unchanged).

- [ ] **Step 1: Write the tests**

Create `internal/app/web/contract/catalog_test.go`:

````go
package contract_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	feed "winterflow/internal/domain/catalog"
	catalogsync "winterflow/internal/domain/usecase/catalog"
	tuc "winterflow/internal/domain/usecase/templates"
	"winterflow/internal/infra/catalogfeed"
)

var (
	feedG1 = time.Date(2026, 1, 10, 20, 0, 0, 0, time.UTC) // in the past: a feed from the future is refused
	feedG2 = feedG1.Add(24 * time.Hour)
	feedG3 = feedG2.Add(24 * time.Hour)
)

// feedServer serves a signed catalog feed on loopback the way
// winterflow.io does: catalog.json with an ETag (304 on If-None-Match)
// and catalog.json.sig.
type feedServer struct {
	*httptest.Server
	key ed25519.PrivateKey
	pub ed25519.PublicKey

	mu          sync.Mutex
	raw, sig    []byte
	etag        string
	notModified int
}

func newFeedServer(t *testing.T) *feedServer {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s := &feedServer{key: key, pub: pub}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch r.URL.Path {
		case "/catalog/catalog.json":
			if s.etag != "" && r.Header.Get("If-None-Match") == s.etag {
				s.notModified++
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", s.etag)
			_, _ = w.Write(s.raw)
		case "/catalog/catalog.json.sig":
			if s.sig == nil {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(s.sig)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// publish replaces the feed with apps, generated at.
func (s *feedServer) publish(t *testing.T, at time.Time, apps ...feed.Entry) {
	t.Helper()
	raw, err := feed.Encode(feed.Feed{Format: feed.FormatVersion, SourceCommit: "49bf28d", GeneratedAt: at, Apps: apps})
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.raw, s.sig, s.etag = raw, feed.Sign(s.key, raw), `"`+at.Format(time.RFC3339)+`"`
}

// replica is one API process's sync of this feed over e's database.
func (s *feedServer) replica(t *testing.T, e *env) *catalogsync.Syncer {
	t.Helper()
	f, err := catalogfeed.New(s.URL + "/catalog/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	return catalogsync.New(catalogsync.Deps{Repo: e.deps.CatalogRepository, Fetcher: f, Key: s.pub, Log: testLog})
}

// catalogApp is a feed entry of compose (compose.yml).
func catalogApp(slug, name, compose string) feed.Entry {
	files := map[string]string{"compose.yml": compose}
	return feed.Entry{
		Slug: slug, Name: name, Summary: name + " for everyone", Keywords: []string{slug}, AlternativeTo: []string{"Other " + name},
		License: "MIT", HomepageURL: "https://" + slug + ".example.org",
		LogoURL: "https://winterflow.io/catalog/logos/" + slug + ".png", Files: files, Digest: feed.Digest(files),
	}
}

const notesCompose = "services:\n  app:\n    image: example/notes:1\n"

func syncOK(t *testing.T, s *catalogsync.Syncer, e *env) catalogsync.Result {
	t.Helper()
	res, err := s.Sync(e.dbCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// catalogBySlug is get-templates' catalog templates by slug.
func catalogBySlug(t *testing.T, c *client) map[string]tuc.TemplateSummary {
	t.Helper()
	var list tuc.TemplateList
	c.get(t, "/api/v1/template/get-templates", nil).expect(t, http.StatusOK).data(t, &list)
	out := map[string]tuc.TemplateSummary{}
	for _, s := range list.Templates {
		if s.Source == "catalog" {
			out[s.Slug] = s
		}
	}
	return out
}

// The sync fills the catalog every org reads; a 304 changes nothing; a
// new revision reads "upgrade available" on the org's instances; a
// dropped app is withdrawn and comes back when the feed lists it again.
func TestCatalogSync(t *testing.T) {
	t.Parallel()
	ie := newInstanceEnv(t)
	e, owner := ie.e, ie.owner
	fs := newFeedServer(t)
	s := fs.replica(t, e)
	broken := catalogApp("broken", "Broken", notesCompose)
	broken.Digest = "sha256:stale"
	fs.publish(t, feedG1, catalogApp("shop", "Shop", shopCompose), catalogApp("notes", "Notes", notesCompose), broken)
	if res := syncOK(t, s, e); res.Inserted != 2 || res.Invalid != 1 {
		t.Fatalf("first sync = %+v", res)
	}

	mallory, _ := e.foreignClient(t)
	for _, c := range []*client{owner, mallory} {
		got := catalogBySlug(t, c)
		if len(got) != 2 || got["shop"].Name != "Shop" || got["shop"].Catalog == nil ||
			got["shop"].Catalog.LogoURL != "https://winterflow.io/catalog/logos/shop.png" || got["notes"].Catalog.AlternativeTo[0] != "Other Notes" {
			t.Fatalf("catalog = %+v", got)
		}
	}
	shopID := catalogBySlug(t, owner)["shop"].ID
	var view tuc.TemplateView
	owner.get(t, "/api/v1/template/get-template", url.Values{"id": {shopID}}).expect(t, http.StatusOK).data(t, &view)
	if view.Source != "catalog" || view.Body.Files["compose.yml"] != shopCompose || view.Body.Summary != "Shop for everyone" ||
		len(view.Detected.Variables) != 3 || view.Catalog == nil || view.Catalog.License != "MIT" {
		t.Fatalf("view = %+v", view)
	}
	if res := syncOK(t, s, e); !res.NotModified {
		t.Fatalf("second sync = %+v", res)
	}
	fs.mu.Lock()
	notModified := fs.notModified
	fs.mu.Unlock()
	if notModified != 1 {
		t.Fatalf("304s = %d", notModified)
	}

	// An instance of the catalog's shop, then the catalog changes shop's
	// files and drops notes.
	in := ie.create("cat-1", "cata")
	in.TemplateID = shopID
	owner.post(t, "/api/v1/instance/create-instance", in).expect(t, http.StatusOK)
	shop2 := catalogApp("shop", "Shop", strings.Replace(shopCompose, "example/shop:1", "example/shop:2", 1))
	fs.publish(t, feedG2, shop2)
	if res := syncOK(t, s, e); res.Revised != 1 || res.Withdrawn != 1 {
		t.Fatalf("change = %+v", res)
	}
	got := catalogBySlug(t, owner)
	if len(got) != 1 || got["shop"].LatestRevision != 2 || got["shop"].Instances != 1 || got["shop"].Behind != 1 {
		t.Fatalf("after the change = %+v", got)
	}
	fs.publish(t, feedG3, shop2, catalogApp("notes", "Notes", notesCompose))
	if res := syncOK(t, s, e); res.Updated != 1 || res.Unchanged != 1 {
		t.Fatalf("restore = %+v", res)
	}
	if got := catalogBySlug(t, owner); len(got) != 2 || got["notes"].WithdrawnAt != nil || got["notes"].LatestRevision != 1 {
		t.Fatalf("after the restore = %+v", got)
	}
}

// A feed whose signature does not verify, or whose .sig can't be fetched,
// writes nothing.
func TestCatalogSyncRefusesUnsignedFeeds(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	fs := newFeedServer(t)
	s := fs.replica(t, e)
	fs.publish(t, feedG1, catalogApp("notes", "Notes", notesCompose))
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	fs.mu.Lock()
	fs.sig = feed.Sign(other, fs.raw)
	fs.mu.Unlock()
	if _, err := s.Sync(e.dbCtx(t)); !errors.Is(err, feed.ErrSignature) {
		t.Fatalf("another key: %v", err)
	}
	fs.mu.Lock()
	fs.sig = nil
	fs.mu.Unlock()
	if _, err := s.Sync(e.dbCtx(t)); err == nil || !strings.Contains(err.Error(), "catalog.json.sig: 404") {
		t.Fatalf("no .sig: %v", err)
	}
	if got := catalogBySlug(t, owner); len(got) != 0 {
		t.Fatalf("catalog = %+v", got)
	}
}

// Replicas syncing at once (separate processes over one database; on
// Postgres their transactions overlap) write each change once; a replica
// behind a stale mirror is refused and rolls nothing back.
func TestCatalogSyncReplicas(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	fs := newFeedServer(t)
	replicas := []*catalogsync.Syncer{fs.replica(t, e), fs.replica(t, e), fs.replica(t, e)}
	race := func() catalogsync.Result {
		var (
			wg  sync.WaitGroup
			mu  sync.Mutex
			sum catalogsync.Result
		)
		for _, s := range replicas {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res, err := s.Sync(e.dbCtx(t))
				if err != nil {
					t.Error(err)
				}
				mu.Lock()
				sum.Inserted += res.Inserted
				sum.Revised += res.Revised
				mu.Unlock()
			}()
		}
		wg.Wait()
		return sum
	}
	fs.publish(t, feedG1, catalogApp("shop", "Shop", shopCompose), catalogApp("notes", "Notes", notesCompose))
	if sum := race(); sum.Inserted != 2 {
		t.Fatalf("inserted %d times", sum.Inserted)
	}
	shop2 := catalogApp("shop", "Shop", strings.Replace(shopCompose, "example/shop:1", "example/shop:2", 1))
	fs.publish(t, feedG2, shop2, catalogApp("notes", "Notes", notesCompose))
	if sum := race(); sum.Revised != 1 {
		t.Fatalf("revised %d times", sum.Revised)
	}
	got := catalogBySlug(t, owner)
	if len(got) != 2 || got["shop"].LatestRevision != 2 || got["notes"].LatestRevision != 1 {
		t.Fatalf("catalog = %+v", got)
	}

	stale := newFeedServer(t)
	stale.key, stale.pub = fs.key, fs.pub
	stale.publish(t, feedG1, catalogApp("shop", "Shop", shopCompose))
	if _, err := stale.replica(t, e).Sync(e.dbCtx(t)); !errors.Is(err, catalogsync.ErrStale) {
		t.Fatalf("a stale mirror: %v", err)
	}
	if got := catalogBySlug(t, owner); len(got) != 2 || got["shop"].LatestRevision != 2 || got["notes"].WithdrawnAt != nil {
		t.Fatalf("after the stale mirror = %+v", got)
	}
	var revs tuc.TemplateView
	owner.get(t, "/api/v1/template/get-template", url.Values{"id": {got["shop"].ID}}).expect(t, http.StatusOK).data(t, &revs)
	if len(revs.Revisions) != 2 {
		t.Fatalf("revisions = %+v", revs.Revisions)
	}
}
````

- [ ] **Step 2: Run them on SQLite, then on Postgres**

Run: `go test -race -run 'TestCatalogSync' -v ./internal/app/web/contract/`
Expected: PASS (`TestCatalogSync`, `TestCatalogSyncRefusesUnsignedFeeds`, `TestCatalogSyncReplicas`), about 5 s.

Run: `make test-pg TEST_PG_ARGS='-race -run Catalog -count=3'`
Expected: PASS three times for `internal/app/web/contract` and `internal/infra/db/repository/template`.

Run: `WF_CONTRACT_GUARD=require go test ./internal/app/web/contract/`
Expected: PASS (the route guard still sees every route answered).

- [ ] **Step 3: Commit**

```bash
git add internal/app/web/contract/catalog_test.go
git commit -m "test(catalog): sync over HTTP, replicas and stale mirrors in the contract suite" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 10: Docs, the whole suite, coverage, and a standalone smoke

Describe the catalog in CLAUDE.md, then prove the branch: every suite, both coverage gates, and an end-to-end run on a standalone instance with Docker (a feed built by `cmd/catalog`, served on loopback, synced, installed, upgraded after a feed change, then switched off).

**Files:**
- Modify: `CLAUDE.md` (a Commands line, a **Catalog** paragraph before **Rollouts**, the Templates paragraph, the x-winterflow paragraph)
- Modify (only if the ratchet asks): `coverage-baseline.json`

**Interfaces:**
- Consumes: everything above.
- Produces: nothing new for code.

- [ ] **Step 1: Update CLAUDE.md**

Modify `CLAUDE.md` (`git apply` this diff):

````diff
diff --git a/CLAUDE.md b/CLAUDE.md
index efb0e5e..3e3d8c5 100644
--- a/CLAUDE.md
+++ b/CLAUDE.md
@@ -15,6 +15,7 @@ Backend (Go 1.27):
 - `make generate-hub-certs` / `make generate-agent-certs` — bootstrap local mTLS material (agent certs depend on hub certs existing first).
 - `make grpc` — regenerate Go from `internal/infra/transport/grpc/proto/hub.proto` (installs `protoc-gen-go`/`protoc-gen-go-grpc`).
 - `make sqlc` — regenerate `internal/infra/db/sqlc/gen` (packages `sqlitegen`, `pgsqlgen`) from the per-engine queries and migrations; sqlc is pinned and run through `go run` (needs a C compiler). `make sqlc-check` fails when the committed code is stale (CI's `sqlc` job).
+- `go run ./cmd/catalog build --apps <winterflow-apps checkout> --out <dir> --base-url <https url> [--sign-key-env NAME] [--source-commit SHA]` — build the catalog feed (see **Catalog**); `go run ./cmd/catalog keygen` prints a signing key pair.
 - `make openapi` — regenerate `api/openapi.yaml` from the route tables (`go run ./cmd/openapi`); `TestOpenAPIUpToDate` fails until it is committed.
 
 Frontend (`web/`, pnpm + Vite + React 19):
@@ -55,7 +56,9 @@ This is the **v2 monorepo** that merges the v1 `winterflow-agent` and `winterflo
 **Backups:** a compose file declares what can be backed up under `x-winterflow.backups` (the recipe: `sqlite`, `files`, `custom`; parsed and validated by `internal/domain/backup/recipe`, shared by the API's save-app 400s and the agent); per-deployment settings (on/off, storage, schedule, paths, UI-added backups) are overrides in the committed `.winterflow/config.json` → `backups`, replaced as a whole by `backup.update` and kept across app-editor saves (`keepBackups`). `${WF_BACKUP_<NAME>_<FIELD>}` resolves to the backup's storage (PREFIX narrowed to `<app-id>/<backup>/`, empty while off); an enabled `custom` backup activates the compose profile `wf-backup-<name>` (passed with the user's `COMPOSE_PROFILES` as `--profile`; a disabled one's service is removed with `rm -fs`, since `up --remove-orphans` leaves it). The agent runs `sqlite`/`files` backups itself: the scheduler (`internal/app/agent/backup`, one goroutine, one timer, one run at a time, `{AGENT_DATA_DIR}/backups/state.json`) calls `RunBackup`, which mounts the app's own volumes/binds (resolved from one cached `docker compose config`) into a capped `alpine:3.20` helper running this binary's hidden `backup dump` subcommand (the binary must be static, `CGO_ENABLED=0` as `make build`, `scripts/install.sh` and goreleaser build it: alpine has no glibc loader, and the agent refuses to run a backup from a dynamic ELF; `internal/app/backup/cli`, exit 3 unchanged / 4 missing), zstd-compresses its stdout to the storage with a manifest written last (`internal/infra/backup/layout`), then prunes by tiers under `BACKUP_RETENTION_DAYS`. `backup.run` is a detached command: the in-process bridge runs it off its single goroutine, and the API awaits its result for 2h (`DispatchInput.Wait`; the agent bounds the wait the same, and a stopped scheduler fails it at once). Health rides on `apps.status` (`AppStatus.Backups`). `backup.restore` is detached too and runs on the same scheduler (never beside a backup): `RestoreBackup` downloads the point to `backups/tmp` and checks its sha256, stops a running app, saves the current data as a `pre-restore` point (a run; its failure aborts and starts the app again), then a helper with `CHOWN`/`FOWNER` runs `backup restore` — sqlite replaces the file (quick_check, owner and mode kept; the old `-wal`/`-shm`/`-journal` are set aside as `*.restore-old`), files extracts into `.wf-restore-<id>/` at each mount root (free space checked, `..`/absolute/outside/through-symlink entries refused) and swaps only the backup's paths. Only a clean helper failure (exit 1, docker's 125-127), or a files helper stopped from outside before its `{"phase":"swapping"}` stderr line (only when its container is confirmed removed, the docker CLI ended by itself with its output read in full, and no `.wf-restore-<id>.old` is on a bind mount), leaves the data as it was and starts the app again (unless the scheduler's `Forget` cancelled it with `ErrAppDeleted`); any other end (a crash, a kill, exit 5, a swap that failed, an agent stop) leaves the app `compose down` (never `-v`) and the error names the pre-restore point, for sqlite too. A manifest records the copy's `entries`; a copy of a location the backup no longer has is refused. The restore's time is checked against the copy's raw size before the app is stopped and again after the pre-restore copy. `backups/restoring.json` records restores in progress: after an agent stop (it is kept past a restore a plain cancel left in doubt, and that restore's error is also stored as `last_error`), the next start turns it into the backup's `last_error`, and `agent.update` is refused while a restore runs. A restore (or run) that outlives its deadline gets `RestoreStopBudget` (derived from the engine's own cleanup bounds) before the scheduler abandons it, and its final outcome is logged and kept as `last_error`. The scheduler nudges the status reporter (`Changed()`) when a run ends or the plan is reread, so health reaches browsers at once. A per-app file lock (`lockApp`, reference counted, held for file work and one commit only) makes `backup.update`'s base check and commit one step, shared with save, rename, rollback and the source poll's commit; clone/fetch of a git-sourced app's upstream runs under a lock of its own (`lockSource`), before a save takes the app lock. Deleting an app marks its backup folders `<app-id>/.deleted` (or purges them with `delete_backups`); the scheduler's daily sweep applies `BACKUP_RETENTION_DAYS` to marked folders only, and `storage.orphans`/`delete-orphan` list and delete the folders of apps not on this server.
  Two per-app locks exist: the **operation lock** (`applock.go`: `lockForMutation`, held for a public mutator's whole run — save, start/stop/restart/update, delete, rollback, rename, `backup.update`, storage redeploys, the source poll — giving up after 30s with `ErrAppBusy` so the standalone bridge never wedges; the reload worker waits ctx-bounded) and the file lock above. Order: operation lock → `beginOp`/`beginRestore` → `lockApp` → `lockSource` → `sharedMu` (a save or source poll releases `lockSource` before taking `lockApp`; only a rollback holds both). A restore never takes the operation lock; `beginRestore`/`beginOp` exclude it from the mutators and reloads.
 
-**Templates and instances (API only; the agent never sees a template):** a template is an org's own (`templates.source` `org`, editable) or the catalog's (`catalog`: `org_id` NULL, a unique `slug`, `withdrawn_at` once the feed drops it; read-only to every org, so save-template and delete-template answer 403 `model.ErrCatalogReadOnly`, class `model.ErrForbidden`; until the catalog sync lands only tests insert them, through `CreateTemplate`), with immutable revisions (`template_revisions`, a catalog one with its `digest`). Every read is scoped: an org reads its own templates and every catalog template (`get-templates` lists its own first; a withdrawn one only while the org has live instances of it, and it takes no new installs, `model.ErrTemplateWithdrawn`). A body is `files` plus `summary`, `description`, `icon`, `color`; what an instance fills in, which ports it exposes and which backups it can turn on come only from the compose files' `x-winterflow` block (`template.Parse`, `ModeTemplate`: a non-external volume with a literal `name:` is an error). `template.Validate` checks a save (a compose file, file paths, the metadata limits, the shared parser, the backup recipe; the body ≤1 MiB; undeclared `${VAR}`s in files only warn); `get-template` returns what the revision declares as `detected` (variables, expose entries with their targets, backups, warnings), and `POST /api/v1/template/inspect-template` reads a body the same way without storing it (the editor's Detected panel; a body a save would refuse answers 200 with its `problem`). An instance (`template_instances`) is an ordinary app rendered by `template.Render` into the `app.save` payload the editor would send (files unchanged; one app variable per given value, so a declared variable without one keeps the compose file's literal; secrets as the caller's ECIES ciphertext or the `<encrypted>` keep placeholder; a config blob with the app name, the instance's `hosts` as its ingress, `template_id` and the `instance` marker, and no `ports`, so the agent keeps the app's host ports) and dispatched through the app usecase's `CheckSave` + `DispatchSave`, so every save-app check applies before anything is claimed. `template.Resolve` checks the values (each a declared variable, plain or secret as declared, no `$` in a plain value, a secret in the ECIES wire format's shape, then `CheckValues`; a given `""` clears a stored value); `template.CheckHosts` the hostnames (`[{host, expose}]`, each on an http expose entry). The app name is the create's `name` (default: the template's), stored on the instance; later renders use the app's current name from the API's app list (a rename sticks). The caller's `key` is unique per org among instances not unlinked (partial unique index); a create claims it only after the checks pass, a replay with the same request hash (server, template, requested revision, name, values, secret *names*, hosts, backups) is looked up before any check: it answers the created instance as stored (a later revision or a withdrawn template changes nothing) or re-dispatches a pending one at the revision and with the name it was claimed with (a withdrawn template refuses that), another request is 409, a deleted instance's key stays retired (409), unlink frees it. Updating or upgrading a pending instance renders like a create (every secret resent, the whole backups list sent: the request's `backups`, accepted only while pending, replace the instance's stored ones, else the create's stored `backups` are sent, less those the revision dropped); `hosts` on an update or upgrade replaces the stored ones. An upgrade re-renders with the stored values and hosts and sends no backups list (the agent keeps the app's settings; a backup the revision adds starts off); a newly required value missing, a stored value the revision refuses or a stored host whose expose entry the revision dropped is `templates.ErrNeedsValues` (a 400, skipped by "Upgrade all"). Instance routes take `wait: true`: the handler holds only its own request on a buffered channel fed by the dispatch result observer (which records the outcome either way; an observed dispatch keeps its result for `appuc.ObservedResultTTL`, 15 minutes, beyond the 10-minute wait), at most 10 minutes, then answers 202; a failed command is 502 and stays as `last_error`. An instance's app is changed through its instance: save-app and rollback-app on an app a pending or created instance manages answer 409 (`port.InstanceLinks`, served by the templates repository and given to the app usecase; rename is allowed), and delete-app's success retires its instance's key like delete-instance. `POST /api/v1/template/upgrade-instances` ("Upgrade all") queues the org's `template_upgrade` rollout of the template; it also starts instances the user had stopped (an upgrade is a save, which redeploys).
+**Templates and instances (API only; the agent never sees a template):** a template is an org's own (`templates.source` `org`, editable) or the catalog's (`catalog`: `org_id` NULL, a unique `slug`, `withdrawn_at` once the feed drops it; read-only to every org, so save-template and delete-template answer 403 `model.ErrCatalogReadOnly`, class `model.ErrForbidden`; written only by the catalog sync, see **Catalog**), with immutable revisions (`template_revisions`, a catalog one with its `digest`). Every read is scoped: an org reads its own templates and every catalog template (`get-templates` lists its own first; a withdrawn one only while the org has live instances of it, and it takes no new installs, `model.ErrTemplateWithdrawn`). A body is `files` plus `summary`, `description`, `icon`, `color`; what an instance fills in, which ports it exposes and which backups it can turn on come only from the compose files' `x-winterflow` block (`template.Parse`, `ModeTemplate`: a non-external volume with a literal `name:` is an error). `template.Validate` checks a save (a compose file, file paths, the metadata limits, the shared parser, the backup recipe; the body ≤1 MiB; undeclared `${VAR}`s in the compose files only warn: compose interpolates nothing else); `get-template` returns what the revision declares as `detected` (variables, expose entries with their targets, backups, warnings), and `POST /api/v1/template/inspect-template` reads a body the same way without storing it (the editor's Detected panel; a body a save would refuse answers 200 with its `problem`). An instance (`template_instances`) is an ordinary app rendered by `template.Render` into the `app.save` payload the editor would send (files unchanged; one app variable per given value, so a declared variable without one keeps the compose file's literal; secrets as the caller's ECIES ciphertext or the `<encrypted>` keep placeholder; a config blob with the app name, the instance's `hosts` as its ingress, `template_id` and the `instance` marker, and no `ports`, so the agent keeps the app's host ports) and dispatched through the app usecase's `CheckSave` + `DispatchSave`, so every save-app check applies before anything is claimed. `template.Resolve` checks the values (each a declared variable, plain or secret as declared, no `$` in a plain value, a secret in the ECIES wire format's shape, then `CheckValues`; a given `""` clears a stored value); `template.CheckHosts` the hostnames (`[{host, expose}]`, each on an http expose entry). The app name is the create's `name` (default: the template's), stored on the instance; later renders use the app's current name from the API's app list (a rename sticks). The caller's `key` is unique per org among instances not unlinked (partial unique index); a create claims it only after the checks pass, a replay with the same request hash (server, template, requested revision, name, values, secret *names*, hosts, backups) is looked up before any check: it answers the created instance as stored (a later revision or a withdrawn template changes nothing) or re-dispatches a pending one at the revision and with the name it was claimed with (a withdrawn template refuses that), another request is 409, a deleted instance's key stays retired (409), unlink frees it. Updating or upgrading a pending instance renders like a create (every secret resent, the whole backups list sent: the request's `backups`, accepted only while pending, replace the instance's stored ones, else the create's stored `backups` are sent, less those the revision dropped); `hosts` on an update or upgrade replaces the stored ones. An upgrade re-renders with the stored values and hosts and sends no backups list (the agent keeps the app's settings; a backup the revision adds starts off); a newly required value missing, a stored value the revision refuses or a stored host whose expose entry the revision dropped is `templates.ErrNeedsValues` (a 400, skipped by "Upgrade all"). Instance routes take `wait: true`: the handler holds only its own request on a buffered channel fed by the dispatch result observer (which records the outcome either way; an observed dispatch keeps its result for `appuc.ObservedResultTTL`, 15 minutes, beyond the 10-minute wait), at most 10 minutes, then answers 202; a failed command is 502 and stays as `last_error`. An instance's app is changed through its instance: save-app and rollback-app on an app a pending or created instance manages answer 409 (`port.InstanceLinks`, served by the templates repository and given to the app usecase; rename is allowed), and delete-app's success retires its instance's key like delete-instance. `POST /api/v1/template/upgrade-instances` ("Upgrade all") queues the org's `template_upgrade` rollout of the template; it also starts instances the user had stopped (an upgrade is a save, which redeploys).
+
+**Catalog:** the catalog templates come from a signed JSON feed. `internal/domain/catalog` is its format (`Feed`/`Entry`, `FormatVersion` 1; `Digest` = `sha256:` over the files in name order, each `<len(name)>:<name><len(content)>:<content>`; ed25519 `Sign`/`Verify` over the exact `catalog.json` bytes, the `.sig` being the base64 signature and a newline) and `Entry.Check`, the one check the builder and the sync share (slug `^[a-z0-9][a-z0-9_-]{0,127}$`, name, list/license/URL limits, https URLs, the digest, then `template.Validate` and the 1 MiB cap). The builder (`cmd/catalog build`, logic in `internal/app/catalog`) reads a winterflow-apps checkout: each `apps/<slug>/` with `metadata.json` and `compose/compose.yml`, not `waiting_list` and not an agent `extension`, becomes an entry with `compose.yml` plus its `x-winterflow.files` (relative to the compose folder); every other folder is skipped and listed; any invalid app fails the build, listing every one, before anything is written; it writes `catalog.json` (apps by slug, compact), `catalog.json.sig` (with `--sign-key-env`) and `logos/<slug>.<ext>` copied from `assets/` (a missing logo is a warning). The API syncs it (`internal/domain/usecase/catalog`, started by `bootstrap.StartCatalog` next to `StartRollouts`, one goroutine per process, never the standalone bridge's): a minute after start, then every `CATALOG_REFRESH` (24h, at least 5m), each sync bounded by 5 minutes; `internal/infra/catalogfeed` GETs `CATALOG_URL` with `If-None-Match`, then the `.sig` (30 s per request, 8 MiB / 4 KiB caps, https only, http only to a loopback host, redirects included). The whole feed is refused on a signature that does not verify against `CATALOG_PUBLIC_KEY` (compiled-in default `config.DefaultCatalogPublicKey`), an unknown format, no apps, a slug listed twice, a `generated_at` older than a feed already applied (`ErrStale`: `templates.catalog_feed_at` is stamped on every catalog template after a sync, so this holds across replicas and restarts) or more than an hour ahead (`ErrFuture`); an invalid entry is logged and skipped (an existing template keeps its last good revision and is not withdrawn). Each changed entry is applied in one transaction (`port.CatalogRepository.ApplyCatalog`): a new template with revision 1, a new revision only when the digest changes, else the name and `model.CatalogMeta` (`templates.catalog`; `catalog` in get-templates and get-template, null for an org template) in place; every write is conditional (the slug free, `latest_revision` still the one read, no newer `catalog_feed_at`), so a replica that loses a race, or a stale mirror, skips instead of adding a revision or rolling back. A catalog template the feed no longer lists gets `withdrawn_at` (hidden from lists and new installs, instances unaffected) and comes back when a feed lists it again; a renamed slug is a new template. The ETag is kept only after a sync fully applied, so a failed one refetches instead of reading a 304. `CATALOG_URL` empty switches the catalog off: every catalog template is withdrawn once at start.
 
 **Rollouts:** the queue of one-app-at-a-time jobs (`rollouts`, `internal/domain/usecase/rollouts`; routes `/api/v1/template/upgrade-instances`, `/api/v1/rollout/get-rollouts|stop-rollout`; an SSE `rollout` event, a `model.RolloutView`, to the org on every change and only then; it is published by the worker's process only, so with several API replicas the step events reach browsers on the worker's replica, and pages on the others catch up through `useRollout`'s re-read of `get-rollouts` every 15 s while the shown rollout is active and the page visible). Kinds: `template_upgrade` (scope key `<org_id>:<template_id>`, `model.UpgradeScopeKey`, so two orgs upgrading one catalog template don't block each other; the API names the scope by the template id in `get-rollouts`' `scope_key` and the view; the org's oldest `created` instance below the target revision is re-rendered with its stored values through the instance update path; one whose values the revision refuses, `templates.ErrNeedsValues`, or that went away is skipped; one whose save went through but was not recorded, still below the target, counts as done and joins `skip_ids`; deleting a template deletes the org's upgrade rollouts of it in the same transaction) and `vars_reload` (scope key the org id; scope `model.ReloadScope`: names × server_ids, all_stale servers, app_ids, plus each server's `revisions` and `settle_by`; the next app is a running app whose `stale_vars` in the API's status cache the scope wants, on a server whose latest report reached the scope's revision; until `settle_by`, 5 minutes, it waits for a server that has not, then leaves it out; a step sends the single-app `vars.reload`; an app deleted while its reload was queued or in flight, gone from the API's catalog, is skipped). One active rollout per (kind, scope_key) (partial unique index): a second upgrade of a template by one org is 409, a reload extends the active one (scope merged in Go, written by one conditional UPDATE on `scope_rev`; the worker completes a rollout only at the `scope_rev` it evaluated); extending a reload whose stop was requested is refused with 409 "a reload is stopping". One worker goroutine per API process, started by `bootstrap.StartRollouts` once commands can flow (standalone: after the in-process bridge subscribed; API: after the response subscriber `WireCore` starts), not inside `WireCore` (`Deps.Rollouts` is the one value whose `Wake` reaches it): a pass on every wake and once a minute (one indexed query when idle); a 2-minute lease claimed in one statement and renewed every 30 s, every worker write naming its owner (a lost lease cancels the step in flight; another process resumes after expiry; lease comparisons are `<=`); a step picks the next app fresh, records `current_app_id`, dispatches and awaits the result (10.5 minutes), then waits (5 minutes) for the app to read healthy on two status reports received after it whose receive times are at least `healthSettle` (20 s) apart (one is not enough: a crash-looping container reads running between restarts) and counts it, skips it (`skip_ids`, never picked again) or fails the rollout naming the app. Healthy means at least one Active container, none Restarting or Problematic, the rest Active or Stopped (a finished one-shot); a report showing a Restarting or Problematic container fails the step at once, naming the container, and any other unhealthy report starts the count again; at the bound no containers, no report, a single healthy report, or an app reading Stopped or unknown fails the step (an app reading Idle for other reasons, such as a paused container beside running ones, passes); a Docker healthcheck's `unhealthy` is not seen: such a container reads running. A `vars_reload` that has no app yet but waits for a server to report (`settle_by` not reached) parks itself (no owner, `lease_until` now + 5 s) and yields the worker, so one quiet server holds neither other rollouts nor the claimable list; `Run` looks again every 5 s while one waits. `Stop` ends a rollout nobody drives (queued, parked, or an expired lease) at once; otherwise `stop_requested` stops it after the step in flight. No transaction is held while a step waits. Ended rollouts beyond the newest 20 per scope are pruned when one ends. The status cache keeps each report's receive time and `vars_revision` (`status.Cache.LatestReport`).
 
@@ -71,7 +74,7 @@ This is the **v2 monorepo** that merges the v1 `winterflow-agent` and `winterflo
 
 **Orchestration:** the agent deploys apps via the `docker compose` CLI (not the Docker SDK), in `internal/infra/orchestrator/docker_compose`. Each app lives in `{AGENT_DATA_DIR}/apps-data/{appID}/` — a git repository (go-git) that IS the deployment: compose runs in it directly, interpolating `${VAR}` from the committed `.env` (+ gitignored `.env.secrets`, materialized from the ECIES-encrypted committed `secrets.json` at deploy). `{AGENT_DATA_DIR}/apps/` holds human-readable `{slug}` symlinks. Every save/rename is a commit; rollback restores an old tree as a new commit and redeploys. History is unlimited. Git-sourced apps additionally clone their upstream into a gitignored `source/` (SHA pinned in the committed `source.lock`, so rollback restores the source position too), run compose against the repo's compose file, and can auto-update via an agent-side poller.
 
-**x-winterflow:** the compose block every app may carry, parsed once by `internal/domain/compose/xwinterflow` (`Parse(ModeApp|ModeTemplate, files...)`, reading the same YAML trees as `recipe.ParseComposeNodes`): `expose` (`container`, `port`, `protocol` http|tcp|udp, id `<container>:<port>/<protocol>`), `variables` (`required`, `encrypted`, `pattern` RE2 <=512 B matched whole, `description` <=1000 B or the YAML comment above the first `environment:` key; default = that key's body literal, never for a secret; names `^[A-Z][A-Z0-9_]*$`, `WF_*` and `COMPOSE_PROJECT_NAME` reserved), `overrides.services`, `files`, `backups`; unknown keys are errors; a non-external volume with a literal `name:` warns in an app and fails a template. Save-app 400s on an invalid block, a missing required value (not for a draft: `CheckDraftValues`), a plain value failing its pattern or holding `$`, a secret variable sent plain, two entries pinned to one port (a pin for an entry the compose file no longer has is dropped, by the API and the agent alike), and an ingress `expose` that is not an http entry (a git-sourced app's block is checked by the agent). Host ports: allocated by the agent (`syncPorts`, `ports.go`) at save (drafts too), rollback and source update from `EXPOSE_PORT_RANGE` (default 20000-29999) under one server-wide lock with an in-memory index (built from every config.json on first use, which `ensurePortIndex` forces before a save or rollback rewrites one; the first app to hold a port keeps it when two claim it, with a warning; pruned in `forgetApp`), stored in the committed `.winterflow/config.json` under `ports` (`{"app:8080/http":{"host":20013,"pinned":false}}`); a save's config `ports` carries the pins (no key keeps them); ports the app holds are never probed, new ones get a listen probe; a pin another app holds is `ErrPortTaken`. At every deploy (`composeUp`, `renderDeployEnv`) the agent checks the values again (secrets after decryption) and writes the gitignored `.winterflow/compose.winterflow.yml` (`xwinterflow.Render`: `NAME: ${NAME}` in each service whose environment has a declared variable with a value; overrides with lists under `!override`, environment merged; expose ports `127.0.0.1:<host>:<port>` or `<host>:<port>/<proto>`), passed as the last `-f` by `composeArgsFor` and `run.sh` but never to `compose down`; every other commit (a draft save, a rollback, a source update) re-renders it best-effort (`refreshComposeOverride`), and a value error writes it without the variables' lines, so it never names a service the compose files no longer have. `IngressDomain` takes `expose` (resolved by `BuildConfig` to `127.0.0.1:<host>`) or `upstream_port`, exactly one; a source update (the poller's `AfterSourceUpdate`, `app.control` update) reloads the ingress, since it can move expose ports. `.env.secrets` quotes every value with `$` escaped. `app.get` returns `declared_variables` and `expose` (host port, bind, pinned) for the editor's variables form and Ports panel; the form blocks a save only for a missing required value (never a draft) or a `$` in a plain one, while a pattern mismatch is a hint (the server alone checks the pattern).
+**x-winterflow:** the compose block every app may carry, parsed once by `internal/domain/compose/xwinterflow` (`Parse(ModeApp|ModeTemplate, files...)`, reading the same YAML trees as `recipe.ParseComposeNodes`): `expose` (`container`, `port`, `protocol` http|tcp|udp, id `<container>:<port>/<protocol>`), `variables` (`required`, `encrypted`, `pattern` RE2 <=512 B matched whole, `description` <=1000 B or the YAML comment above the first `environment:` key; default = that key's body literal, never for a secret; names `^[A-Z][A-Z0-9_]*$`, `WF_*` and `COMPOSE_PROJECT_NAME` reserved), `overrides.services`, `files`, `backups`; unknown keys are errors; YAML aliases and merge keys (`<<: *anchor`, `<<: [*a, *b]`) are expanded once by `recipe.DecodeFiles` (a mapping's own keys win, an earlier merged mapping wins; capped at `recipe.MaxExpandedNodes`, 100 000 nodes per file: past it, or an alias inside its own anchor, the file reads as not valid YAML), so merged environments declare their variables like literal ones; a non-external volume with a literal `name:` warns in an app and fails a template. Save-app 400s on an invalid block, a missing required value (not for a draft: `CheckDraftValues`), a plain value failing its pattern or holding `$`, a secret variable sent plain, two entries pinned to one port (a pin for an entry the compose file no longer has is dropped, by the API and the agent alike), and an ingress `expose` that is not an http entry (a git-sourced app's block is checked by the agent). Host ports: allocated by the agent (`syncPorts`, `ports.go`) at save (drafts too), rollback and source update from `EXPOSE_PORT_RANGE` (default 20000-29999) under one server-wide lock with an in-memory index (built from every config.json on first use, which `ensurePortIndex` forces before a save or rollback rewrites one; the first app to hold a port keeps it when two claim it, with a warning; pruned in `forgetApp`), stored in the committed `.winterflow/config.json` under `ports` (`{"app:8080/http":{"host":20013,"pinned":false}}`); a save's config `ports` carries the pins (no key keeps them); ports the app holds are never probed, new ones get a listen probe; a pin another app holds is `ErrPortTaken`. At every deploy (`composeUp`, `renderDeployEnv`) the agent checks the values again (secrets after decryption) and writes the gitignored `.winterflow/compose.winterflow.yml` (`xwinterflow.Render`: `NAME: ${NAME}` in each service whose environment has a declared variable with a value; overrides with lists under `!override`, environment merged; expose ports `127.0.0.1:<host>:<port>` or `<host>:<port>/<proto>`), passed as the last `-f` by `composeArgsFor` and `run.sh` but never to `compose down`; every other commit (a draft save, a rollback, a source update) re-renders it best-effort (`refreshComposeOverride`), and a value error writes it without the variables' lines, so it never names a service the compose files no longer have. `IngressDomain` takes `expose` (resolved by `BuildConfig` to `127.0.0.1:<host>`) or `upstream_port`, exactly one; a source update (the poller's `AfterSourceUpdate`, `app.control` update) reloads the ingress, since it can move expose ports. `.env.secrets` quotes every value with `$` escaped. `app.get` returns `declared_variables` and `expose` (host port, bind, pinned) for the editor's variables form and Ports panel; the form blocks a save only for a missing required value (never a draft) or a `$` in a plain one, while a pattern mismatch is a hint (the server alone checks the pattern).
 
 **Monitoring (metrics):** the agent/standalone runs `appagent.StartMetrics`: a `server.metrics` event every 60 s built from procfs/sysfs/cgroupfs reads (`internal/infra/sysmetrics`, no forks; containers come from the status reporter's `StatusSnapshot`, never a second `docker ps`) plus an hourly `ScanDisk` (`docker system df -v`, 5-min deadline). The model, tiers and rollup math live in `internal/domain/metrics`. The API's events sink writes tier-1 rows into `metric_samples` (insert-or-ignore; app rows only for apps the API knows on that server) and keeps the latest host sample in memory (`LatestStore`); `runMetricsMaintenance` (wired in `WireCore`) rolls up 1→10→20→120→480 every 10 min and prunes per tier hourly. Charts are pulled (`/api/v1/metrics/*`), never pushed over SSE. `METRICS_ENABLED=false` switches the whole pipeline off.
 
````

- [ ] **Step 2: Static checks and generated files**

Run: `gofmt -l $(git ls-files '*.go') && go vet ./... && make sqlc-check && make openapi && git diff --exit-code api/openapi.yaml`
Expected: no file listed by gofmt, vet clean, `make sqlc-check` prints only its command, `api/openapi.yaml` unchanged.

Run: `pnpm --dir web exec tsc -b && pnpm --dir web run lint`
Expected: clean.

- [ ] **Step 3: Every suite, on both databases, and the coverage gates**

Run:

```bash
WF_CONTRACT_GUARD=require make test
WF_CONTRACT_GUARD=require make test-pg TEST_PG_ARGS='-race -covermode=atomic -coverpkg=./... -coverprofile=contract-postgres.out'
tail -n +2 contract-postgres.out >> coverage.out
CI=true make web-test
go run ./tools/diffcov --base origin/main --min 80 --go coverage.out --lcov web/coverage/lcov.info
go run ./tools/diffcov ratchet --go coverage.out --lcov web/coverage/lcov.info
```

Expected: `make test` and `make test-pg` pass (no `FAIL` line; `TestParity` matches on both databases); Vitest reports `Tests  1688 passed` (the web change is types only); the diff gate prints `go  ... changed lines covered (about 93%)  PASS` and `web  no executable changed lines  PASS`; the ratchet prints `PASS` for both (Go about 87.4 against 86.8). If the ratchet prints `::notice::raise coverage-baseline.json <lang> to <N>`, set that language's value in `coverage-baseline.json` to `<N>` and keep the change for the commit below.

- [ ] **Step 4: Standalone smoke** (Docker and the `nginx:alpine` image; `jq`, `openssl`, `python3` and `curl` on the PATH; it uses ports 18977, 18978 and 28600-28650 on 127.0.0.1, and takes about three minutes: each start's first sync is a minute away)

Save this script as `/tmp/wf-catalog-smoke.sh` (not in the repository) and run it from the repository root. It builds the standalone binary (API only, no web bundle), builds a one-app feed (an nginx page whose compose file declares a required `GREETING` with a pattern, an optional `SUFFIX`, an http expose entry and an extra file) with a fresh key pair, serves it with `python3 -m http.server` on loopback, starts standalone with `CATALOG_URL` and `CATALOG_PUBLIC_KEY` pointing at it, claims it, waits for the sync, installs the catalog template, reads the page on its host port, publishes version 2, restarts (so the next sync is a minute away instead of `CATALOG_REFRESH`), upgrades, restarts once more with `CATALOG_URL=` (the catalog switched off), and deletes the instance.

````bash
#!/usr/bin/env bash
# Standalone smoke for the catalog feed: build a feed from a one-app apps
# tree with cmd/catalog, serve it on loopback, let a standalone instance
# sync it, install the catalog app, publish a changed feed and upgrade, then
# switch the catalog off. Needs Docker, the nginx:alpine image, jq, openssl
# and python3. Run from the repo root; SMOKE is a scratch directory.
set -euo pipefail
SMOKE=${SMOKE:-/tmp/wf-sp3-smoke}
PORT=${PORT:-18977}
FEED_PORT=${FEED_PORT:-18978}
BASE=http://127.0.0.1:$PORT
for p in "$PORT" "$FEED_PORT"; do
  if curl -s -o /dev/null "http://127.0.0.1:$p/"; then echo "port $p is in use" >&2; exit 1; fi
done
rm -rf "$SMOKE" && mkdir -p "$SMOKE/apps/apps/greeter/compose" "$SMOKE/apps/assets/greeter" "$SMOKE/feed"
eval "$(go run ./cmd/catalog keygen | sed 's/^/export /')"
go build -o "$SMOKE/standalone" ./cmd/standalone

publish() { # $1: what the page says after the greeting
  cat > "$SMOKE/apps/apps/greeter/metadata.json" <<'JSON'
{"slug": "greeter", "name": "Greeter", "summary": "A greeting page", "description": "Says hello.",
 "keywords": ["hello"], "logo": "greeter/greeter.svg", "license": "MIT",
 "homepage_url": "https://greeter.example.org", "waiting_list": false}
JSON
  cat > "$SMOKE/apps/apps/greeter/compose/compose.yml" <<YAML
services:
  web:
    image: nginx:alpine
    environment:
      # The greeting the page answers with
      GREETING: "hello"
      SUFFIX: "$1"
    volumes:
      - ./default.conf.template:/etc/nginx/templates/default.conf.template:ro
x-winterflow:
  expose:
    - { container: web, port: 80 }
  variables:
    GREETING: { required: true, pattern: "^[a-z ]+\$" }
    SUFFIX: {}
  files:
    - default.conf.template
YAML
  cat > "$SMOKE/apps/apps/greeter/compose/default.conf.template" <<'CONF'
server {
  listen 80;
  location / { default_type text/plain; return 200 "${GREETING}${SUFFIX}\n"; }
}
CONF
  echo '<svg xmlns="http://www.w3.org/2000/svg"/>' > "$SMOKE/apps/assets/greeter/greeter.svg"
  sleep 1 # a later generated_at than the last build
  go run ./cmd/catalog build --apps "$SMOKE/apps" --out "$SMOKE/feed" --base-url https://winterflow.io/catalog/ \
    --sign-key-env CATALOG_SIGNING_KEY --source-commit smoke
}

python3 -I -m http.server "$FEED_PORT" --bind 127.0.0.1 --directory "$SMOKE/feed" > "$SMOKE/feed.log" 2>&1 &
FEED_PID=$!
start() { # $1: CATALOG_URL
  cat > "$SMOKE/.env" <<ENV
API_PORT=$PORT
WEB_URL=$BASE
JWT_SECRET=${JWT_SECRET:=$(openssl rand -hex 32)}
REGION=smoke
DATABASE_URL=sqlite://$SMOKE/wf.sqlite
AVATARS_STORAGE_PATH=$SMOKE/avatars
AGENT_DATA_DIR=$SMOKE/data
EXPOSE_PORT_RANGE=28600-28650
METRICS_ENABLED=false
CATALOG_URL=$1
CATALOG_PUBLIC_KEY=$CATALOG_PUBLIC_KEY
HUB_CERT_DIR=$SMOKE/hub-certs
HUB_CERT_EXT_PATH=$SMOKE/ext.cnf
HUB_CA_SUBJECT="/C=CA/O=WinterFlow.io/OU=CA/CN=WinterFlow.io CA/emailAddress=info@winterflow.io"
HUB_SERVER_SUBJECT="/C=CA/O=WinterFlow.io/OU=SERVER/CN=WinterFlow.io/emailAddress=info@winterflow.io"
ENV
  "$SMOKE/standalone" serve -env-file "$SMOKE/.env" >> "$SMOKE/standalone.log" 2>&1 &
  echo $! > "$SMOKE/pid"
  for _ in $(seq 1 60); do curl -fs "$BASE/_/health" >/dev/null && return; sleep 0.5; done
  echo "standalone did not start" >&2; exit 1
}
stop() { kill "$(cat "$SMOKE/pid")" 2>/dev/null || true; while curl -fs "$BASE/_/health" >/dev/null; do sleep 0.5; done; }
trap 'stop; kill $FEED_PID 2>/dev/null || true' EXIT

echo "== build and serve the feed (version 1)"
publish "!"
start "http://127.0.0.1:$FEED_PORT/catalog.json"
J="$SMOKE/cookies"
curl -fs -c "$J" -b "$J" -H 'Content-Type: application/json' -d '{"name":"Owner","email":"owner@example.test","password":"correct horse battery"}' "$BASE/api/v1/auth/register" >/dev/null
curl -fs -c "$J" -b "$J" -H 'Content-Type: application/json' -d '{"user":"owner@example.test","passwd":"correct horse battery"}' "$BASE/auth/local/login" >/dev/null
TOKEN=$(curl -fs -c "$J" -b "$J" -H 'Content-Type: application/json' -d '{"name":"smoke"}' "$BASE/api/v1/user/create-token" | jq -r .data.token)
api() { curl -fsS -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' "$@"; }
for _ in $(seq 1 60); do SID=$(api "$BASE/api/v1/server/get-servers" | jq -r '.data.servers[0].id // empty'); [ -n "$SID" ] && break; sleep 0.5; done
greeter() { api "$BASE/api/v1/template/get-templates" | jq -c '[.data.templates[] | select(.slug == "greeter")][0] // empty'; }
wait_for() { # $1: a jq condition on the greeter template
  for _ in $(seq 1 150); do
    if [ -n "$(greeter)" ] && greeter | jq -e "$1" >/dev/null; then return; fi
    sleep 1
  done
  echo "timed out waiting for $1" >&2; exit 1
}

echo "== the first sync (a minute after start) lists the catalog template"
wait_for '.latest_revision == 1'
greeter | jq -c '{source, slug, name, latest_revision, catalog: {summary: .catalog.summary, logo_url: .catalog.logo_url}}'
CAT=$(greeter | jq -r .id)

echo "== create-instance of the catalog template (wait)"
api -d "{\"server_id\":\"$SID\",\"key\":\"smoke-1\",\"template_id\":\"$CAT\",\"values\":{\"GREETING\":\"good morning\"},\"wait\":true}" \
  "$BASE/api/v1/instance/create-instance" | jq -c '.data.instance | {state, revision, name}'
APP=$(api "$BASE/api/v1/instance/get-instance?server_id=$SID&key=smoke-1" | jq -r .data.app_id)
HOSTPORT=$(docker ps --filter "label=com.docker.compose.project=wf-$APP" --format '{{.Ports}}' | sed -E 's/.*127\.0\.0\.1:([0-9]+)->80.*/\1/')
page() { for _ in $(seq 1 20); do curl -fs "http://127.0.0.1:$HOSTPORT/" && return; sleep 0.5; done; echo "no answer on $HOSTPORT" >&2; return 1; }
page

echo "== publish version 2, restart (the next sync is a minute after start)"
publish ", from catalog version 2"
stop
start "http://127.0.0.1:$FEED_PORT/catalog.json"
wait_for '.latest_revision == 2'
greeter | jq -c '{latest_revision, instances, behind}'
api -d "{\"server_id\":\"$SID\",\"key\":\"smoke-1\",\"revision\":2,\"wait\":true}" "$BASE/api/v1/instance/upgrade-instance" | jq -c '.data.instance | {state, revision}'
page

echo "== CATALOG_URL empty: the catalog is withdrawn at start, the instance keeps working"
stop
start ""
wait_for '.withdrawn_at != null'
greeter | jq -c '{instances, withdrawn: (.withdrawn_at != null)}'
page

echo "== delete-instance (wait)"
api -d "{\"server_id\":\"$SID\",\"key\":\"smoke-1\",\"delete_volumes\":true,\"wait\":true}" "$BASE/api/v1/instance/delete-instance" | jq -c '.data.instance.state'
````

Run:

```bash
docker image inspect nginx:alpine >/dev/null 2>&1 || docker pull nginx:alpine
bash /tmp/wf-catalog-smoke.sh
```

Expected output (the build lines may interleave with Go's download messages on a cold cache):

```text
== build and serve the feed (version 1)
wrote catalog.json: 1 apps, signed
== the first sync (a minute after start) lists the catalog template
{"source":"catalog","slug":"greeter","name":"Greeter","latest_revision":1,"catalog":{"summary":"A greeting page","logo_url":"https://winterflow.io/catalog/logos/greeter.svg"}}
== create-instance of the catalog template (wait)
{"state":"created","revision":1,"name":"Greeter"}
good morning!
== publish version 2, restart (the next sync is a minute after start)
wrote catalog.json: 1 apps, signed
{"latest_revision":2,"instances":1,"behind":1}
{"state":"created","revision":2}
good morning, from catalog version 2
== CATALOG_URL empty: the catalog is withdrawn at start, the instance keeps working
{"instances":1,"withdrawn":true}
good morning, from catalog version 2
== delete-instance (wait)
"deleted"
```

Afterwards `pgrep -f wf-sp3-smoke/standalone` and `pgrep -f "http.server 18978"` print nothing, and no container of the smoke is left (`docker ps -a --format '{{.Names}}'` lists none whose project is the deleted app). Remove `/tmp/wf-sp3-smoke`.

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md coverage-baseline.json
git commit -m "docs: the catalog feed, its builder and sync" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

(`git add` of an unchanged `coverage-baseline.json` is a no-op.)
