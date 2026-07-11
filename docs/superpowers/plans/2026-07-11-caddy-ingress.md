# Caddy Ingress Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Embed Caddy v2 as a library in the agent runtime so apps get UI-configured domains → host-port reverse proxying, per-domain Let's Encrypt certs, and redirects.

**Architecture:** Ingress config is an optional `ingress` section in the app's committed `config.json` (agent filesystem = source of truth, versioned/rolled back with the app). The API validates it, blocks cross-app domain conflicts via a rebuildable `app_domains` DB index, and ships the blob unchanged. On the agent, an `IngressManager` (in `internal/infra/ingress/caddy`) merges every app's fragment into one Caddy JSON config and hot-reloads the embedded Caddy after each mutating command. One app's bad fragment excludes only that app.

**Tech Stack:** Go 1.25, `github.com/caddyserver/caddy/v2` (curated module imports, NOT `modules/standard`), Bun ORM (SQLite/Postgres), chi, React 19 + Vite (`web/`).

**Spec:** `docs/superpowers/specs/2026-07-11-caddy-ingress-design.md`

## Global Constraints

- Agent filesystem is authoritative; the `app_domains` table is a rebuildable cache. Never make DB the source of truth.
- User-supplied strings (domains, targets) go through `model.Ingress.Validate()` into typed JSON fields — never string-templated config.
- Ingress failures degrade ingress only: never fail a deploy, never crash the agent, never let one app's fragment break another's domains.
- A missing `ingress` key in `config.json` means "feature untouched" — every code path must no-op (old clients, pre-feature apps).
- Caddy log level follows `LOG_LEVEL`; HTTP access logs only at `debug`.
- New env vars: `INGRESS_HTTP_PORT` (default 80), `INGRESS_HTTPS_PORT` (default 443), `INGRESS_ACME_EMAIL` (default empty).
- Run `go vet ./...` before every commit; `gofmt` formatting.
- All work happens in this worktree; commit after every task.

---

### Task 1: Ingress domain model + validation

**Files:**
- Create: `internal/domain/model/ingress.go`
- Create: `internal/domain/model/ingress_test.go`
- Modify: `internal/domain/model/app.go` (add `Ingress` field to `App`)

**Interfaces:**
- Produces: `model.Ingress{Domains []IngressDomain, Redirects []IngressRedirect}`, `model.IngressDomain{Domain string, UpstreamPort int, SSL bool}`, `model.IngressRedirect{Domain, Path, To string, Code int, SSL bool}`, `(*Ingress).Validate() error`, `(*Ingress).DomainNames() []string`, `model.ParseIngress(configBlob []byte) (*Ingress, error)`, sentinel errors `model.ErrIngressInvalid`, `model.ErrDomainTaken`, types `model.DomainClaim`, `model.AppDomainInfo`, and `model.App.Ingress *Ingress`.

- [ ] **Step 1: Write the failing test**

`internal/domain/model/ingress_test.go`:

```go
package model

import (
	"strings"
	"testing"
)

func validIngress() Ingress {
	return Ingress{
		Domains: []IngressDomain{
			{Domain: "blog.example.com", UpstreamPort: 8088, SSL: true},
			{Domain: "internal.lan", UpstreamPort: 9000, SSL: false},
		},
		Redirects: []IngressRedirect{
			{Domain: "www.example.com", To: "https://blog.example.com", Code: 301, SSL: true},
			{Domain: "blog.example.com", Path: "/old-blog/*", To: "https://blog.example.com/blog", Code: 302},
		},
	}
}

func TestValidateAccepts(t *testing.T) {
	ing := validIngress()
	if err := ing.Validate(); err != nil {
		t.Fatalf("valid ingress rejected: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Ingress)
		errHas string
	}{
		{"empty domain", func(i *Ingress) { i.Domains[0].Domain = "" }, "domain"},
		{"scheme in domain", func(i *Ingress) { i.Domains[0].Domain = "https://x.com" }, "hostname"},
		{"wildcard", func(i *Ingress) { i.Domains[0].Domain = "*.example.com" }, "hostname"},
		{"port in domain", func(i *Ingress) { i.Domains[0].Domain = "x.com:8080" }, "hostname"},
		{"trailing dot", func(i *Ingress) { i.Domains[0].Domain = "x.com." }, "hostname"},
		{"unicode", func(i *Ingress) { i.Domains[0].Domain = "bücher.de" }, "hostname"},
		{"uppercase", func(i *Ingress) { i.Domains[0].Domain = "X.COM" }, "hostname"},
		{"port zero", func(i *Ingress) { i.Domains[0].UpstreamPort = 0 }, "port"},
		{"port too big", func(i *Ingress) { i.Domains[0].UpstreamPort = 70000 }, "port"},
		{"dup within app", func(i *Ingress) { i.Domains[1].Domain = i.Domains[0].Domain }, "duplicate"},
		{"dup route vs redirect", func(i *Ingress) { i.Redirects[0].Domain = i.Domains[0].Domain }, "duplicate"},
		{"relative target", func(i *Ingress) { i.Redirects[0].To = "/nowhere" }, "absolute"},
		{"ftp target", func(i *Ingress) { i.Redirects[0].To = "ftp://x.com" }, "absolute"},
		{"bad code", func(i *Ingress) { i.Redirects[0].Code = 300 }, "code"},
		{"path rule unknown domain", func(i *Ingress) { i.Redirects[1].Domain = "other.example.com" }, "path rule"},
		{"path without leading slash", func(i *Ingress) { i.Redirects[1].Path = "old/*" }, "path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ing := validIngress()
			tc.mutate(&ing)
			err := ing.Validate()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.errHas) {
				t.Fatalf("error %q does not mention %q", err, tc.errHas)
			}
		})
	}
}

func TestValidateAcceptsSingleLabelHost(t *testing.T) {
	// Dev setups use hosts like "localhost"; ACME won't issue for them, but
	// ssl:false single labels are legitimate.
	ing := Ingress{Domains: []IngressDomain{{Domain: "localhost", UpstreamPort: 8080}}}
	if err := ing.Validate(); err != nil {
		t.Fatalf("single-label host rejected: %v", err)
	}
}

func TestDomainNames(t *testing.T) {
	ing := validIngress()
	got := ing.DomainNames()
	// Route domains + domain-level redirect sources; path rules excluded.
	want := []string{"blog.example.com", "internal.lan", "www.example.com"}
	if len(got) != len(want) {
		t.Fatalf("DomainNames = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("DomainNames = %v, want %v", got, want)
		}
	}
}

func TestParseIngress(t *testing.T) {
	ing, err := ParseIngress([]byte(`{"name":"x","ingress":{"domains":[{"domain":"a.example.com","upstream_port":81,"ssl":true}],"redirects":[]}}`))
	if err != nil || ing == nil {
		t.Fatalf("ParseIngress: %v, %v", ing, err)
	}
	if ing.Domains[0].Domain != "a.example.com" || ing.Domains[0].UpstreamPort != 81 || !ing.Domains[0].SSL {
		t.Fatalf("parsed = %+v", ing)
	}

	// Missing key => nil, nil (feature untouched).
	ing, err = ParseIngress([]byte(`{"name":"x"}`))
	if err != nil || ing != nil {
		t.Fatalf("missing key: got %v, %v; want nil, nil", ing, err)
	}

	// Broken JSON => error.
	if _, err := ParseIngress([]byte(`{`)); err == nil {
		t.Fatal("broken JSON accepted")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/domain/model/ -run 'TestValidate|TestDomainNames|TestParseIngress' -v`
Expected: FAIL — `undefined: Ingress`, `undefined: ParseIngress`.

- [ ] **Step 3: Write the implementation**

`internal/domain/model/ingress.go`:

```go
package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Ingress-related sentinel errors, matched by the HTTP layer to map failures
// to 400s with the underlying message.
var (
	ErrIngressInvalid = errors.New("invalid ingress")
	ErrDomainTaken    = errors.New("domain already in use")
)

// Ingress is the app's routing config, stored under the "ingress" key of the
// committed config.json. The agent filesystem is the source of truth; the API
// mirrors route/redirect domains into the app_domains DB index.
type Ingress struct {
	Domains   []IngressDomain   `json:"domains"`
	Redirects []IngressRedirect `json:"redirects"`
}

// IngressDomain maps a hostname to a host port the app's compose file
// publishes (ideally bound to 127.0.0.1). SSL=true means an ACME cert plus
// the automatic HTTP->HTTPS redirect; false means plain HTTP on port 80 only.
type IngressDomain struct {
	Domain       string `json:"domain"`
	UpstreamPort int    `json:"upstream_port"`
	SSL          bool   `json:"ssl"`
}

// IngressRedirect is either a domain-level redirect (Path == "", preserves the
// request URI, needs its own SSL choice because serving https://source
// requires a cert) or a path rule (Path != "", scoped to a domain the app
// already claims, redirects the matched prefix to the exact To URL).
type IngressRedirect struct {
	Domain string `json:"domain"`
	Path   string `json:"path,omitempty"`
	To     string `json:"to"`
	Code   int    `json:"code"`
	SSL    bool   `json:"ssl,omitempty"`
}

// hostnameRe is a strict lowercase RFC-1123 hostname: labels of [a-z0-9-],
// no leading/trailing hyphen, at least one label, no wildcard/port/scheme.
var hostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

func validHostname(h string) bool {
	return h != "" && len(h) <= 253 && hostnameRe.MatchString(h)
}

var validRedirectCodes = map[int]bool{301: true, 302: true, 307: true, 308: true}

// Validate enforces the shared rulebook (UI, API, and agent all rely on it).
// Strict typing here is the injection defense: every value later lands in a
// typed JSON field of the generated Caddy config.
func (i *Ingress) Validate() error {
	seen := map[string]bool{}
	claim := func(domain, what string) error {
		if !validHostname(domain) {
			return fmt.Errorf("%s: %q is not a valid lowercase hostname", what, domain)
		}
		if seen[domain] {
			return fmt.Errorf("%s: duplicate domain %q within the app", what, domain)
		}
		seen[domain] = true
		return nil
	}

	for _, d := range i.Domains {
		if err := claim(d.Domain, "domain"); err != nil {
			return err
		}
		if d.UpstreamPort < 1 || d.UpstreamPort > 65535 {
			return fmt.Errorf("domain %q: upstream port %d out of range 1-65535", d.Domain, d.UpstreamPort)
		}
	}

	for _, r := range i.Redirects {
		u, err := url.Parse(r.To)
		if err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("redirect for %q: target %q must be an absolute http(s) URL", r.Domain, r.To)
		}
		if !validRedirectCodes[r.Code] {
			return fmt.Errorf("redirect for %q: code %d not one of 301, 302, 307, 308", r.Domain, r.Code)
		}
		if r.Path == "" {
			// Domain-level redirect: claims its hostname like a route does.
			if err := claim(r.Domain, "redirect"); err != nil {
				return err
			}
			continue
		}
		// Path rule: must ride a domain this app already claims.
		if !strings.HasPrefix(r.Path, "/") {
			return fmt.Errorf("redirect path %q must start with /", r.Path)
		}
		if !seen[r.Domain] {
			return fmt.Errorf("path rule for %q: domain not claimed by this app (path rule domains must match a route or redirect domain listed above it)", r.Domain)
		}
	}
	return nil
}

// DomainNames returns the hostnames this ingress claims (routes + domain-level
// redirect sources, in declaration order). Path rules claim nothing new.
func (i *Ingress) DomainNames() []string {
	var out []string
	for _, d := range i.Domains {
		out = append(out, d.Domain)
	}
	for _, r := range i.Redirects {
		if r.Path == "" {
			out = append(out, r.Domain)
		}
	}
	return out
}

// ParseIngress extracts the optional "ingress" key from an app config blob.
// (nil, nil) when the key is absent — the feature is untouched and every
// caller must no-op.
func ParseIngress(configBlob []byte) (*Ingress, error) {
	var probe struct {
		Ingress *Ingress `json:"ingress"`
	}
	if err := json.Unmarshal(configBlob, &probe); err != nil {
		return nil, fmt.Errorf("parse app config: %w", err)
	}
	return probe.Ingress, nil
}

// DomainClaim names the app/server holding a domain — the payload of the
// cross-app conflict error and of the /domains/check endpoint.
type DomainClaim struct {
	Domain     string `json:"domain"`
	AppID      string `json:"app_id"`
	AppName    string `json:"app_name"`
	ServerID   string `json:"server_id"`
	ServerName string `json:"server_name"`
}

// AppDomainInfo is the display slice of an app_domains row (app cards, chips).
type AppDomainInfo struct {
	Domain string `json:"domain"`
	SSL    bool   `json:"ssl"`
	Kind   string `json:"kind"` // "route" | "redirect"
}
```

In `internal/domain/model/app.go`, add one field to `App` (after `Color`):

```go
type App struct {
	ID         string    `json:"id"`
	ServerID   string    `json:"server_id"`
	Version    string    `json:"version"`
	TemplateID string    `json:"template_id"`
	Name       string    `json:"name"`
	Icon       string    `json:"icon"`
	Color      string    `json:"color"`
	// Ingress is parsed straight out of the committed config.json when the
	// agent lists apps (ListApps unmarshals the raw blob into App), letting
	// the API reconcile the app_domains index without a second command.
	Ingress   *Ingress  `json:"ingress,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/domain/model/ -v && go vet ./...`
Expected: PASS (all packages still compile — the `App` field is additive).

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model/
git commit -m "feat(ingress): domain model, validation, config-blob parsing"
```

---

### Task 2: DB — `app_domains` table, repository, port

**Files:**
- Modify: `internal/infra/db/models/models.go` (append `AppDomain`)
- Create: `internal/infra/db/migrations/20260711000001_app_domains.go`
- Modify: `internal/domain/port/app.go` (append `AppDomainRepository`)
- Create: `internal/infra/db/repository/app_domain.go`
- Create: `internal/infra/db/repository/app_domain_test.go`

**Interfaces:**
- Consumes: `model.Ingress`, `model.DomainClaim`, `model.AppDomainInfo`, `model.App.Ingress` (Task 1); existing test helpers `newTestDB(t)`, `seedOrgWithServer(t, conn, orgID, serverID)` in `internal/infra/db/repository`.
- Produces:

```go
// internal/domain/port/app.go
type AppDomainRepository interface {
	// FindClaims returns rows holding any of the given domains, excluding the
	// app being saved (its own claims are not conflicts). Claims carry app and
	// server names for the error message.
	FindClaims(ctx context.Context, domains []string, excludeAppID string) ([]model.DomainClaim, error)
	// ReplaceForApp makes the index mirror the app's ingress: delete the
	// app's rows, insert the new set, one transaction. nil ingress = no-op
	// (old clients); empty ingress = rows removed.
	ReplaceForApp(ctx context.Context, appID, serverID string, ing *model.Ingress) error
	DeleteForApp(ctx context.Context, appID string) error
	// ReplaceForServer rebuilds the whole server's rows from a reconciled app
	// list (apps carry .Ingress parsed from their config blobs).
	ReplaceForServer(ctx context.Context, serverID string, apps []model.App) error
	// ListForServer returns display rows grouped by app id.
	ListForServer(ctx context.Context, serverID string) (map[string][]model.AppDomainInfo, error)
}
```

- [ ] **Step 1: Write the failing test**

`internal/infra/db/repository/app_domain_test.go`:

```go
package repository

import (
	"context"
	"testing"

	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
)

func newDomainRepo(t *testing.T) (*DbAppDomainRepository, *DbAppRepository) {
	t.Helper()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	conn := newTestDB(t)
	seedOrgWithServer(t, conn, "org-1", "srv-1")
	return NewDbAppDomainRepository(conn, log), NewDbAppRepository(conn, log)
}

func seedApp(t *testing.T, apps *DbAppRepository, id, name string) {
	t.Helper()
	if err := apps.SaveApp(context.Background(), model.App{ID: id, ServerID: "srv-1", Name: name, Icon: "i", Color: "#000000"}); err != nil {
		t.Fatal(err)
	}
}

func ingressOf(domains ...model.IngressDomain) *model.Ingress {
	return &model.Ingress{Domains: domains, Redirects: []model.IngressRedirect{}}
}

func TestReplaceAndFindClaims(t *testing.T) {
	repo, apps := newDomainRepo(t)
	ctx := context.Background()
	seedApp(t, apps, "app-1", "Ghost")
	seedApp(t, apps, "app-2", "Blog2")

	ing := &model.Ingress{
		Domains:   []model.IngressDomain{{Domain: "blog.example.com", UpstreamPort: 8088, SSL: true}},
		Redirects: []model.IngressRedirect{
			{Domain: "www.example.com", To: "https://blog.example.com", Code: 301, SSL: true},
			// Path rule: must NOT produce a row.
			{Domain: "blog.example.com", Path: "/old/*", To: "https://blog.example.com/new", Code: 302},
		},
	}
	if err := repo.ReplaceForApp(ctx, "app-1", "srv-1", ing); err != nil {
		t.Fatal(err)
	}

	// Another app asking for a held domain conflicts, with names attached.
	claims, err := repo.FindClaims(ctx, []string{"blog.example.com", "free.example.com"}, "app-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].Domain != "blog.example.com" || claims[0].AppName != "Ghost" || claims[0].ServerName == "" {
		t.Fatalf("claims = %+v", claims)
	}

	// The owner re-saving is not a conflict.
	claims, _ = repo.FindClaims(ctx, []string{"blog.example.com"}, "app-1")
	if len(claims) != 0 {
		t.Fatalf("own claims reported as conflicts: %+v", claims)
	}

	// Replace shrinks: dropping the redirect frees www.
	if err := repo.ReplaceForApp(ctx, "app-1", "srv-1", ingressOf(model.IngressDomain{Domain: "blog.example.com", UpstreamPort: 8088, SSL: true})); err != nil {
		t.Fatal(err)
	}
	claims, _ = repo.FindClaims(ctx, []string{"www.example.com"}, "app-2")
	if len(claims) != 0 {
		t.Fatalf("stale row survived replace: %+v", claims)
	}

	// nil ingress = no-op (old client edit must not wipe rows).
	if err := repo.ReplaceForApp(ctx, "app-1", "srv-1", nil); err != nil {
		t.Fatal(err)
	}
	list, err := repo.ListForServer(ctx, "srv-1")
	if err != nil || len(list["app-1"]) != 1 {
		t.Fatalf("ListForServer after nil replace = %+v, %v", list, err)
	}

	// Empty (non-nil) ingress deletes rows.
	if err := repo.ReplaceForApp(ctx, "app-1", "srv-1", &model.Ingress{}); err != nil {
		t.Fatal(err)
	}
	list, _ = repo.ListForServer(ctx, "srv-1")
	if len(list["app-1"]) != 0 {
		t.Fatalf("rows survived empty replace: %+v", list)
	}
}

func TestDeleteForAppAndReplaceForServer(t *testing.T) {
	repo, apps := newDomainRepo(t)
	ctx := context.Background()
	seedApp(t, apps, "app-1", "One")
	seedApp(t, apps, "app-2", "Two")

	_ = repo.ReplaceForApp(ctx, "app-1", "srv-1", ingressOf(model.IngressDomain{Domain: "one.example.com", UpstreamPort: 81}))
	_ = repo.ReplaceForApp(ctx, "app-2", "srv-1", ingressOf(model.IngressDomain{Domain: "two.example.com", UpstreamPort: 82}))

	if err := repo.DeleteForApp(ctx, "app-1"); err != nil {
		t.Fatal(err)
	}
	list, _ := repo.ListForServer(ctx, "srv-1")
	if len(list["app-1"]) != 0 || len(list["app-2"]) != 1 {
		t.Fatalf("after delete: %+v", list)
	}

	// Reconcile: agent now reports app-2 without ingress and app-3 with one.
	repApps := []model.App{
		{ID: "app-2", Name: "Two"},
		{ID: "app-3", Name: "Three", Ingress: ingressOf(model.IngressDomain{Domain: "three.example.com", UpstreamPort: 83})},
	}
	if err := repo.ReplaceForServer(ctx, "srv-1", repApps); err != nil {
		t.Fatal(err)
	}
	list, _ = repo.ListForServer(ctx, "srv-1")
	if len(list["app-2"]) != 0 || len(list["app-3"]) != 1 || list["app-3"][0].Domain != "three.example.com" {
		t.Fatalf("after reconcile: %+v", list)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/infra/db/repository/ -run 'TestReplaceAndFindClaims|TestDeleteForAppAndReplaceForServer' -v`
Expected: FAIL — `undefined: NewDbAppDomainRepository`.

- [ ] **Step 3: Write model, migration, repository**

Append to `internal/infra/db/models/models.go`:

```go
type AppDomain struct {
	bun.BaseModel `bun:"table:app_domains"`

	// Domain is the PK: lowercased FQDN. The PK is the global uniqueness
	// constraint that makes cross-app/cross-server conflicts impossible to
	// persist, not just impolite.
	Domain       string         `bun:"domain,pk" json:"domain"`
	AppID        string         `bun:"app_id,notnull,type:char(36)" json:"app_id"`
	ServerID     string         `bun:"server_id,notnull,type:char(36)" json:"server_id"`
	Kind         string         `bun:"kind,notnull" json:"kind"` // "route" | "redirect"
	SSL          bool           `bun:"ssl,notnull" json:"ssl"`
	UpstreamPort int            `bun:"upstream_port,notnull,default:0" json:"upstream_port"` // routes only
	Target       string         `bun:"target,notnull,default:''" json:"target"`              // redirects only
	Code         int            `bun:"code,notnull,default:0" json:"code"`                   // redirects only
	UpdatedAt    types.DateTime `bun:"updated_at,notnull" json:"updated_at"`

	App    *App    `bun:"rel:belongs-to,join:app_id=app_id"`
	Server *Server `bun:"rel:belongs-to,join:server_id=server_id"`
}
```

`internal/infra/db/migrations/20260711000001_app_domains.go`:

```go
package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		return createAppDomains(ctx, db)
	}, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS app_domains")
		return err
	})
}

// createAppDomains is the rebuildable ingress index: one row per hostname an
// app claims (routes + domain-level redirect sources; path rules ride an
// existing row). PK on domain = global cross-app/cross-server uniqueness.
func createAppDomains(ctx context.Context, db *bun.DB) error {
	stmts := []string{
		`CREATE TABLE app_domains (
			domain VARCHAR(253) NOT NULL PRIMARY KEY,
			app_id CHAR(36) NOT NULL,
			server_id CHAR(36) NOT NULL,
			kind VARCHAR(16) NOT NULL,
			ssl BOOLEAN NOT NULL DEFAULT FALSE,
			upstream_port INTEGER NOT NULL DEFAULT 0,
			target VARCHAR(2048) NOT NULL DEFAULT '',
			code INTEGER NOT NULL DEFAULT 0,
			updated_at TIMESTAMP NOT NULL
		)`,
		"CREATE INDEX idx_app_domains_app_id ON app_domains (app_id)",
		"CREATE INDEX idx_app_domains_server_id ON app_domains (server_id)",
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("app_domains migration: %w", err)
		}
	}
	return nil
}
```

Append the `AppDomainRepository` interface from the **Interfaces** block above to `internal/domain/port/app.go` (plus the `model.DomainClaim` import is already covered by the existing `model` import).

`internal/infra/db/repository/app_domain.go`:

```go
package repository

import (
	"context"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/models"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"

	"github.com/uptrace/bun"
)

func NewDbAppDomainRepository(db *db.BunConnection, log *logger.Logger) *DbAppDomainRepository {
	return &DbAppDomainRepository{db: db, log: log}
}

// DbAppDomainRepository maintains the app_domains index. The agent filesystem
// stays authoritative; every method here is cache maintenance.
type DbAppDomainRepository struct {
	db  *db.BunConnection
	log *logger.Logger
}

// rowsFor flattens an ingress into index rows: routes plus domain-level
// redirect sources. Path rules produce no rows (they cannot conflict).
func rowsFor(appID, serverID string, ing *model.Ingress) []models.AppDomain {
	if ing == nil {
		return nil
	}
	now := types.DateTime(time.Now().UTC())
	var rows []models.AppDomain
	for _, d := range ing.Domains {
		rows = append(rows, models.AppDomain{
			Domain: d.Domain, AppID: appID, ServerID: serverID,
			Kind: "route", SSL: d.SSL, UpstreamPort: d.UpstreamPort, UpdatedAt: now,
		})
	}
	for _, r := range ing.Redirects {
		if r.Path != "" {
			continue
		}
		rows = append(rows, models.AppDomain{
			Domain: r.Domain, AppID: appID, ServerID: serverID,
			Kind: "redirect", SSL: r.SSL, Target: r.To, Code: r.Code, UpdatedAt: now,
		})
	}
	return rows
}

func (r *DbAppDomainRepository) FindClaims(ctx context.Context, domains []string, excludeAppID string) ([]model.DomainClaim, error) {
	if len(domains) == 0 {
		return nil, nil
	}
	var rows []models.AppDomain
	q := r.db.GetDB().NewSelect().
		Model(&rows).
		Relation("App").
		Relation("Server").
		Where("app_domain.domain IN (?)", bun.In(domains))
	if excludeAppID != "" {
		q = q.Where("app_domain.app_id != ?", excludeAppID)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]model.DomainClaim, 0, len(rows))
	for _, row := range rows {
		claim := model.DomainClaim{Domain: row.Domain, AppID: row.AppID, ServerID: row.ServerID}
		if row.App != nil {
			claim.AppName = row.App.Name
		}
		if row.Server != nil {
			claim.ServerName = row.Server.Name
		}
		out = append(out, claim)
	}
	return out, nil
}

func (r *DbAppDomainRepository) ReplaceForApp(ctx context.Context, appID, serverID string, ing *model.Ingress) error {
	if ing == nil {
		// Old client / config without the key: leave the index alone.
		return nil
	}
	rows := rowsFor(appID, serverID, ing)
	return r.db.GetDB().RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewDelete().Model((*models.AppDomain)(nil)).Where("app_id = ?", appID).Exec(ctx); err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		_, err := tx.NewInsert().Model(&rows).Exec(ctx)
		return err
	})
}

func (r *DbAppDomainRepository) DeleteForApp(ctx context.Context, appID string) error {
	_, err := r.db.GetDB().NewDelete().Model((*models.AppDomain)(nil)).Where("app_id = ?", appID).Exec(ctx)
	return err
}

func (r *DbAppDomainRepository) ReplaceForServer(ctx context.Context, serverID string, apps []model.App) error {
	var rows []models.AppDomain
	for _, a := range apps {
		rows = append(rows, rowsFor(a.ID, serverID, a.Ingress)...)
	}
	return r.db.GetDB().RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewDelete().Model((*models.AppDomain)(nil)).Where("server_id = ?", serverID).Exec(ctx); err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		_, err := tx.NewInsert().Model(&rows).Exec(ctx)
		return err
	})
}

func (r *DbAppDomainRepository) ListForServer(ctx context.Context, serverID string) (map[string][]model.AppDomainInfo, error) {
	var rows []models.AppDomain
	err := r.db.GetDB().NewSelect().
		Model(&rows).
		Where("server_id = ?", serverID).
		Order("domain ASC").
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]model.AppDomainInfo{}
	for _, row := range rows {
		out[row.AppID] = append(out[row.AppID], model.AppDomainInfo{Domain: row.Domain, SSL: row.SSL, Kind: row.Kind})
	}
	return out, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/infra/db/... -v && go vet ./...`
Expected: PASS, including all pre-existing repository tests (migration runs in `newTestDB`).

- [ ] **Step 5: Commit**

```bash
git add internal/infra/db/ internal/domain/port/app.go
git commit -m "feat(ingress): app_domains index table, repository, port"
```

---

### Task 3: Save/delete/rollback/reconcile integration (usecase + handler)

**Files:**
- Modify: `internal/domain/usecase/app/usecase.go`
- Modify: `internal/domain/usecase/app/usecase_test.go`
- Modify: `internal/app/web/handler/app/save_app.go` (error mapping)
- Modify: `internal/app/web/handler/app/handler.go` (Deps gains `AppDomainRepository`)
- Modify: `internal/infra/bootstrap/deps.go`, `internal/infra/bootstrap/core.go` (construct + expose the repo)
- Modify: `internal/app/web/routes.go` (pass it to `happ.NewHandler`)

**Interfaces:**
- Consumes: `model.ParseIngress`, `(*Ingress).Validate/DomainNames`, `model.ErrIngressInvalid`, `model.ErrDomainTaken` (Task 1); `port.AppDomainRepository` (Task 2).
- Produces: `useapp.Deps.AppDomainRepository port.AppDomainRepository`; `UseCase.SaveApp` now returns ingress validation/conflict errors wrapping the sentinels; `bootstrap.Deps.AppDomainRepository port.AppDomainRepository`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/domain/usecase/app/usecase_test.go`:

```go
type fakeDomainRepo struct {
	claims   []model.DomainClaim
	replaced struct {
		appID    string
		serverID string
		ing      *model.Ingress
	}
	deleted string
	synced  string
}

func (f *fakeDomainRepo) FindClaims(_ context.Context, _ []string, _ string) ([]model.DomainClaim, error) {
	return f.claims, nil
}
func (f *fakeDomainRepo) ReplaceForApp(_ context.Context, appID, serverID string, ing *model.Ingress) error {
	f.replaced.appID, f.replaced.serverID, f.replaced.ing = appID, serverID, ing
	return nil
}
func (f *fakeDomainRepo) DeleteForApp(_ context.Context, appID string) error {
	f.deleted = appID
	return nil
}
func (f *fakeDomainRepo) ReplaceForServer(_ context.Context, serverID string, _ []model.App) error {
	f.synced = serverID
	return nil
}
func (f *fakeDomainRepo) ListForServer(_ context.Context, _ string) (map[string][]model.AppDomainInfo, error) {
	return nil, nil
}

const ingressConfig = `{"name":"demo","ingress":{"domains":[{"domain":"blog.example.com","upstream_port":8088,"ssl":true}],"redirects":[]}}`

func newTestUseCaseWithDomains(d port.CommandDispatcher, domains port.AppDomainRepository) *UseCase {
	return NewUseCase(&Deps{
		CommandDispatcher:   d,
		AppRepository:       noopAppRepo{},
		AppDomainRepository: domains,
		Log:                 logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
	})
}

func TestSaveAppRejectsInvalidIngress(t *testing.T) {
	disp := &captureDispatcher{}
	uc := newTestUseCaseWithDomains(disp, &fakeDomainRepo{})

	bad := `{"name":"demo","ingress":{"domains":[{"domain":"NOT A HOST","upstream_port":8088}],"redirects":[]}}`
	_, err := uc.SaveApp(context.Background(), "u", "s", model.App{Name: "demo"}, command.AppPayload{Config: []byte(bad)}, false)
	if !errors.Is(err, model.ErrIngressInvalid) {
		t.Fatalf("err = %v, want ErrIngressInvalid", err)
	}
	if disp.last.Type != "" {
		t.Fatal("invalid ingress reached the bus")
	}
}

func TestSaveAppRejectsTakenDomain(t *testing.T) {
	disp := &captureDispatcher{}
	repo := &fakeDomainRepo{claims: []model.DomainClaim{{Domain: "blog.example.com", AppName: "Ghost", ServerName: "hetzner-1"}}}
	uc := newTestUseCaseWithDomains(disp, repo)

	_, err := uc.SaveApp(context.Background(), "u", "s", model.App{Name: "demo"}, command.AppPayload{Config: []byte(ingressConfig)}, false)
	if !errors.Is(err, model.ErrDomainTaken) {
		t.Fatalf("err = %v, want ErrDomainTaken", err)
	}
	for _, part := range []string{"blog.example.com", "Ghost", "hetzner-1"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("error %q missing %q", err, part)
		}
	}
}

func TestSaveAppReplacesIndexOnSuccess(t *testing.T) {
	disp := &captureDispatcher{}
	repo := &fakeDomainRepo{}
	uc := newTestUseCaseWithDomains(disp, repo)

	_, err := uc.SaveApp(context.Background(), "u", "srv-9", model.App{ID: "app-7", Name: "demo"}, command.AppPayload{Config: []byte(ingressConfig)}, false)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the agent confirming the save.
	disp.last.OnResult(port.CommandResult{Success: true, Payload: []byte(`{"app_id":"app-7","revision":"abc"}`)})
	if repo.replaced.appID != "app-7" || repo.replaced.serverID != "srv-9" || repo.replaced.ing == nil {
		t.Fatalf("ReplaceForApp not called correctly: %+v", repo.replaced)
	}
}

func TestDeleteAppRemovesDomainRows(t *testing.T) {
	disp := &captureDispatcher{}
	repo := &fakeDomainRepo{}
	uc := newTestUseCaseWithDomains(disp, repo)

	_, err := uc.DeleteApp(context.Background(), "u", "s", "app-7")
	if err != nil {
		t.Fatal(err)
	}
	disp.last.OnResult(port.CommandResult{Success: true})
	if repo.deleted != "app-7" {
		t.Fatalf("DeleteForApp = %q, want app-7", repo.deleted)
	}
}

func TestRefreshAppsSyncsDomains(t *testing.T) {
	disp := &captureDispatcher{}
	repo := &fakeDomainRepo{}
	uc := newTestUseCaseWithDomains(disp, repo)

	_, err := uc.RefreshApps(context.Background(), "u", "srv-9")
	if err != nil {
		t.Fatal(err)
	}
	disp.last.OnResult(port.CommandResult{Success: true, Payload: []byte(`{"apps":[{"id":"a1","name":"x","ingress":{"domains":[{"domain":"a.example.com","upstream_port":81}],"redirects":[]}}]}`)})
	if repo.synced != "srv-9" {
		t.Fatalf("ReplaceForServer not called (synced=%q)", repo.synced)
	}
}
```

Add `"errors"` and `"strings"` to the test file imports.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/domain/usecase/app/ -v`
Expected: FAIL — `unknown field AppDomainRepository in struct literal`.

- [ ] **Step 3: Implement usecase changes**

In `internal/domain/usecase/app/usecase.go`:

Add to imports: `"fmt"`, `"strings"`. Extend the struct and Deps:

```go
type UseCase struct {
	dispatcher port.CommandDispatcher
	repo       port.AppRepository
	domains    port.AppDomainRepository
	log        *logger.Logger
}

type Deps struct {
	CommandDispatcher   port.CommandDispatcher
	AppRepository       port.AppRepository
	AppDomainRepository port.AppDomainRepository
	Log                 *logger.Logger
}

func NewUseCase(d *Deps) *UseCase {
	return &UseCase{
		dispatcher: d.CommandDispatcher,
		repo:       d.AppRepository,
		domains:    d.AppDomainRepository,
		log:        d.Log,
	}
}
```

In `SaveApp`, after the `payload.Config` fallback block and before building `req`, insert:

```go
	// Ingress rides the config blob. Parse + validate + conflict-check here so
	// a bad config is a synchronous 400, not an async agent failure. ing==nil
	// (no "ingress" key) means the feature is untouched: skip everything.
	ing, err := model.ParseIngress(payload.Config)
	if err != nil {
		return "", fmt.Errorf("%w: %v", model.ErrIngressInvalid, err)
	}
	if ing != nil {
		if err := ing.Validate(); err != nil {
			return "", fmt.Errorf("%w: %v", model.ErrIngressInvalid, err)
		}
		if uc.domains != nil {
			claims, err := uc.domains.FindClaims(ctx, ing.DomainNames(), app.ID)
			if err != nil {
				return "", err
			}
			if len(claims) > 0 {
				var parts []string
				for _, c := range claims {
					parts = append(parts, fmt.Sprintf("%s is already used by app %q on server %q", c.Domain, c.AppName, c.ServerName))
				}
				return "", fmt.Errorf("%w: %s", model.ErrDomainTaken, strings.Join(parts, "; "))
			}
		}
	}
```

In `SaveApp`'s `OnResult`, after the existing `uc.repo.SaveApp(...)` call, append:

```go
			if uc.domains != nil && ing != nil {
				if err := uc.domains.ReplaceForApp(context.Background(), persisted.ID, serverID, ing); err != nil {
					// Index only: reconcile heals it on the next apps.list.
					uc.log.Error("SaveApp: update domain index", "error", err, "app_id", persisted.ID)
				}
			}
```

In `DeleteApp`'s `OnResult`, after the existing `uc.repo.DeleteApp(...)` call, append:

```go
			if uc.domains != nil {
				if err := uc.domains.DeleteForApp(context.Background(), appID); err != nil {
					uc.log.Error("DeleteApp: remove domain rows", "error", err, "app_id", appID)
				}
			}
```

In `RefreshApps`'s `OnResult`, after the existing `uc.repo.SyncApps(...)` call, append:

```go
			if uc.domains != nil {
				if err := uc.domains.ReplaceForServer(context.Background(), serverID, listed.Apps); err != nil {
					uc.log.Error("RefreshApps: sync domain rows", "error", err, "server_id", serverID)
				}
			}
```

Replace `RollbackApp` with a version that re-reads the restored config (rollback restores routing, so the index must follow):

```go
// RollbackApp dispatches an app.rollback: the agent restores the given commit
// as a new revision and redeploys. The result is delivered over SSE. On
// success the restored config is re-fetched so the domain index follows the
// rollback.
func (uc *UseCase) RollbackApp(ctx context.Context, userID, serverID, appID, hash string) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppRollback,
		Payload: command.RollbackAppRequest{AppID: appID, Hash: hash},
		OnResult: func(res port.CommandResult) {
			if !res.Success || uc.domains == nil {
				return
			}
			_, err := uc.dispatcher.Dispatch(context.Background(), port.DispatchInput{
				AgentID: serverID,
				UserID:  userID,
				Type:    command.TypeAppGet,
				Payload: command.GetAppRequest{AppID: appID},
				OnResult: func(got port.CommandResult) {
					if !got.Success || len(got.Payload) == 0 {
						return
					}
					var resp command.GetAppResponse
					if err := json.Unmarshal(got.Payload, &resp); err != nil {
						uc.log.Error("RollbackApp: decode app.get", "error", err)
						return
					}
					ing, err := model.ParseIngress(resp.App.Config)
					if err != nil {
						uc.log.Error("RollbackApp: parse restored config", "error", err)
						return
					}
					if ing == nil {
						return
					}
					if err := uc.domains.ReplaceForApp(context.Background(), appID, serverID, ing); err != nil {
						uc.log.Error("RollbackApp: update domain index", "error", err, "app_id", appID)
					}
				},
			})
			if err != nil {
				uc.log.Error("RollbackApp: dispatch app.get", "error", err)
			}
		},
	})
}
```

Handler error mapping — in `internal/app/web/handler/app/save_app.go`, add `"errors"` and `"winterflow/internal/domain/model"` imports (model is already imported) and change the error branch of `SaveApp`:

```go
	requestID, err := h.usecase.SaveApp(r.Context(), userID, req.ServerID, app, payload, req.Draft)
	if err != nil {
		if errors.Is(err, model.ErrIngressInvalid) || errors.Is(err, model.ErrDomainTaken) {
			webutil.Error(w, err.Error(), nil)
			return
		}
		webutil.Error(w, "failed to save app", nil)
		return
	}
```

Wiring: in `internal/infra/bootstrap/deps.go` add `AppDomainRepository port.AppDomainRepository` to `Deps`; in `internal/infra/bootstrap/core.go` construct `repository.NewDbAppDomainRepository(dbconn, log)` next to the existing repository constructions and set it on the returned `Deps`. In `internal/app/web/handler/app/handler.go` add `AppDomainRepository port.AppDomainRepository` to `happ.Deps` and pass it through `useapp.Deps`. In `internal/app/web/routes.go` add `AppDomainRepository: s.Deps.AppDomainRepository` (match the surrounding field style at line ~54) to the `happ.NewHandler(&happ.Deps{...})` literal. Read each file first and follow its exact existing field-passing idiom.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/domain/usecase/app/ ./internal/app/web/... -v && go vet ./... && go build ./...`
Expected: PASS; whole tree builds.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/usecase/app/ internal/app/web/ internal/infra/bootstrap/
git commit -m "feat(ingress): validate + conflict-check ingress on save, keep app_domains in sync"
```

---

### Task 4: `GET /api/v1/domains/check` endpoint

**Files:**
- Create: `internal/app/web/handler/app/check_domain.go`
- Modify: `internal/domain/usecase/app/usecase.go` (add `CheckDomain`)
- Modify: `internal/app/web/routes.go` (route)
- Test: extend `internal/domain/usecase/app/usecase_test.go`

**Interfaces:**
- Consumes: `port.AppDomainRepository.FindClaims`, `model.DomainClaim` (Tasks 1–3).
- Produces: `UseCase.CheckDomain(ctx, domain, excludeAppID string) ([]model.DomainClaim, error)`; HTTP `GET /api/v1/domains/check?domain=<host>&app_id=<optional>` → `{"success":true,"data":{"available":bool,"claims":[DomainClaim...]}}` (webutil.Success envelope). Invalid hostname → 400.

- [ ] **Step 1: Write the failing test**

Append to `internal/domain/usecase/app/usecase_test.go`:

```go
func TestCheckDomain(t *testing.T) {
	repo := &fakeDomainRepo{claims: []model.DomainClaim{{Domain: "x.example.com", AppName: "Other"}}}
	uc := newTestUseCaseWithDomains(&captureDispatcher{}, repo)

	claims, err := uc.CheckDomain(context.Background(), "x.example.com", "app-1")
	if err != nil || len(claims) != 1 {
		t.Fatalf("CheckDomain = %v, %v", claims, err)
	}

	if _, err := uc.CheckDomain(context.Background(), "NOT A HOST", ""); !errors.Is(err, model.ErrIngressInvalid) {
		t.Fatalf("err = %v, want ErrIngressInvalid", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/domain/usecase/app/ -run TestCheckDomain -v`
Expected: FAIL — `uc.CheckDomain undefined`.

- [ ] **Step 3: Implement**

Append to `internal/domain/usecase/app/usecase.go`:

```go
// CheckDomain reports which app (if any) already claims a hostname — the
// live-typing availability check behind GET /api/v1/domains/check.
func (uc *UseCase) CheckDomain(ctx context.Context, domain, excludeAppID string) ([]model.DomainClaim, error) {
	probe := model.Ingress{Domains: []model.IngressDomain{{Domain: domain, UpstreamPort: 1}}}
	if err := probe.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", model.ErrIngressInvalid, err)
	}
	if uc.domains == nil {
		return nil, nil
	}
	return uc.domains.FindClaims(ctx, []string{domain}, excludeAppID)
}
```

`internal/app/web/handler/app/check_domain.go`:

```go
package server

import (
	"errors"
	"net/http"
	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
)

// CheckDomain answers "is this hostname free?" for the app editor's
// live-typing validation. app_id (optional) excludes the app being edited so
// its own claims don't read as conflicts.
func (h *Handler) CheckDomain(w http.ResponseWriter, r *http.Request) {
	if _, ok := webutil.RequireUser(w, r); !ok {
		return
	}
	domain := r.URL.Query().Get("domain")
	claims, err := h.usecase.CheckDomain(r.Context(), domain, r.URL.Query().Get("app_id"))
	if err != nil {
		if errors.Is(err, model.ErrIngressInvalid) {
			webutil.Error(w, err.Error(), nil)
			return
		}
		webutil.Error(w, "failed to check domain", nil)
		return
	}
	webutil.Success(w, "", struct {
		Available bool                `json:"available"`
		Claims    []model.DomainClaim `json:"claims"`
	}{Available: len(claims) == 0, Claims: claims})
}
```

Route in `internal/app/web/routes.go`, next to the other app routes:

```go
	s.Router.With(authMW).Get("/api/v1/domains/check", appsAPI.CheckDomain)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/domain/usecase/app/ ./internal/app/web/... -v && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/usecase/app/ internal/app/web/
git commit -m "feat(ingress): domain availability check endpoint"
```

---

### Task 5: Caddy config builder (pure function, golden-tested)

**Files:**
- Create: `internal/infra/ingress/caddy/config.go`
- Create: `internal/infra/ingress/caddy/config_test.go`
- Modify: `go.mod` / `go.sum` (add Caddy)

**Interfaces:**
- Consumes: `model.Ingress` (Task 1).
- Produces:

```go
package caddy // import "winterflow/internal/infra/ingress/caddy"

// AppIngress is one app's parsed fragment, keyed for deterministic merging.
type AppIngress struct {
	AppID   string
	Ingress model.Ingress
}

// Options carries everything environment-specific, so BuildConfig is pure.
type Options struct {
	HTTPPort   int    // 0 = 80
	HTTPSPort  int    // 0 = 443
	ACMEEmail  string // "" = none
	StorageDir string // cert/ACME state root
	LogLevel   string // winterflow LOG_LEVEL: debug|info|warn|error
}

// BuildConfig merges per-app fragments into one Caddy JSON config.
// Invalid fragments and cross-app duplicate domains are EXCLUDED with a
// warning, never fatal — one bad app must not break the others.
func BuildConfig(apps []AppIngress, opts Options) (cfg []byte, warnings []string, err error)
```

**Config shape produced** (Caddy's documented JSON structure, built from our own typed structs — no caddy imports needed here, no text templating):

- `admin.disabled: true`
- `logging.logs.default: {level: <mapped LOG_LEVEL>}`
- `storage: {module: "file_system", root: opts.StorageDir}`
- `apps.http.http_port/https_port`
- `apps.http.servers.https`: `listen [":<httpsPort>"]`, routes for `ssl:true` route domains (host matcher → `reverse_proxy` to `127.0.0.1:<port>`) and `ssl:true` domain-level redirects (`static_response` with `Location` header `<to>{http.request.uri}`); each domain's path rules ordered BEFORE its main route. Access logs (`servers.https.logs`) only when `LogLevel == "debug"`.
- `apps.http.servers.http`: `listen [":<httpPort>"]`, same route construction for `ssl:false` entries. Caddy auto-disables automatic HTTPS for servers listening only on the HTTP port, and appends its HTTP→HTTPS redirects for the ssl domains to this server.
- `apps.tls.automation.policies[0]`: `subjects` = exactly the `ssl:true` hostnames, `issuers: [{module: "acme", email: opts.ACMEEmail}]` (omit `email` key when empty). Omit the whole `tls` app when there are no ssl domains.

- [ ] **Step 1: Write the failing test**

`internal/infra/ingress/caddy/config_test.go`:

```go
package caddy

import (
	"encoding/json"
	"strings"
	"testing"

	"winterflow/internal/domain/model"
)

func opts() Options {
	return Options{HTTPPort: 80, HTTPSPort: 443, ACMEEmail: "ops@example.com", StorageDir: "/data/caddy", LogLevel: "info"}
}

// dig walks a decoded JSON tree: dig(t, cfg, "apps", "http", "servers").
func dig(t *testing.T, v any, path ...string) any {
	t.Helper()
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok || m[p] == nil {
			t.Fatalf("missing %q in %v", strings.Join(path, "."), v)
		}
		v = m[p]
	}
	return v
}

func decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("config is not valid JSON: %v\n%s", err, raw)
	}
	return m
}

func sampleApps() []AppIngress {
	return []AppIngress{
		{AppID: "app-b", Ingress: model.Ingress{
			Domains: []model.IngressDomain{{Domain: "plain.example.com", UpstreamPort: 9000, SSL: false}},
		}},
		{AppID: "app-a", Ingress: model.Ingress{
			Domains: []model.IngressDomain{{Domain: "blog.example.com", UpstreamPort: 8088, SSL: true}},
			Redirects: []model.IngressRedirect{
				{Domain: "www.example.com", To: "https://blog.example.com", Code: 301, SSL: true},
				{Domain: "blog.example.com", Path: "/old/*", To: "https://blog.example.com/new", Code: 302},
			},
		}},
	}
}

func TestBuildConfigShape(t *testing.T) {
	raw, warnings, err := BuildConfig(sampleApps(), opts())
	if err != nil || len(warnings) != 0 {
		t.Fatalf("BuildConfig: %v, warnings %v", err, warnings)
	}
	cfg := decode(t, raw)

	if dig(t, cfg, "admin", "disabled") != true {
		t.Error("admin API not disabled")
	}
	if dig(t, cfg, "logging", "logs", "default", "level") != "INFO" {
		t.Error("log level not mapped")
	}
	if dig(t, cfg, "storage", "root") != "/data/caddy" {
		t.Error("storage root missing")
	}

	// TLS automation: exactly the ssl hostnames.
	subjects := dig(t, cfg, "apps", "tls", "automation", "policies").([]any)[0].(map[string]any)["subjects"].([]any)
	got := map[string]bool{}
	for _, s := range subjects {
		got[s.(string)] = true
	}
	if len(got) != 2 || !got["blog.example.com"] || !got["www.example.com"] {
		t.Errorf("subjects = %v", subjects)
	}

	s := string(raw)
	for _, want := range []string{
		`"127.0.0.1:8088"`, `"127.0.0.1:9000"`, // upstreams
		`"blog.example.com"`, `"plain.example.com"`, `"www.example.com"`,
		`https://blog.example.com{http.request.uri}`, // domain-level redirect preserves URI
		`"/old/*"`,                       // path matcher
		`"https://blog.example.com/new"`, // path rule = exact target
		`"ops@example.com"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("config missing %s\n%s", want, s)
		}
	}

	// The path rule must be ordered before its domain's main route.
	if pathIdx, routeIdx := strings.Index(s, `"/old/*"`), strings.Index(s, `"127.0.0.1:8088"`); pathIdx > routeIdx {
		t.Error("path rule ordered after its domain's route")
	}
}

func TestBuildConfigIsolatesBadFragmentsAndDups(t *testing.T) {
	apps := append(sampleApps(),
		// Invalid fragment: whole app excluded, others unaffected.
		AppIngress{AppID: "app-bad", Ingress: model.Ingress{
			Domains: []model.IngressDomain{{Domain: "NOT A HOST", UpstreamPort: 1}},
		}},
		// Duplicate across apps: sorted-appID order, first claim wins.
		AppIngress{AppID: "app-z", Ingress: model.Ingress{
			Domains: []model.IngressDomain{{Domain: "blog.example.com", UpstreamPort: 7000, SSL: true}},
		}},
	)
	raw, warnings, err := BuildConfig(apps, opts())
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want 2 (bad fragment + dup)", warnings)
	}
	s := string(raw)
	if strings.Contains(s, "NOT A HOST") {
		t.Error("invalid fragment leaked into config")
	}
	if !strings.Contains(s, `"127.0.0.1:8088"`) || strings.Contains(s, `"127.0.0.1:7000"`) {
		t.Error("dup resolution wrong: app-a (sorted first) must win blog.example.com")
	}
}

func TestBuildConfigEmptyAndNoSSL(t *testing.T) {
	// No apps: still a valid config with empty servers (Caddy runs, serves nothing).
	raw, warnings, err := BuildConfig(nil, opts())
	if err != nil || len(warnings) != 0 {
		t.Fatalf("empty: %v %v", err, warnings)
	}
	cfg := decode(t, raw)
	if _, hasTLS := dig(t, cfg, "apps").(map[string]any)["tls"]; hasTLS {
		t.Error("tls app present with zero ssl domains")
	}

	// Debug level turns access logs on.
	o := opts()
	o.LogLevel = "debug"
	raw, _, _ = BuildConfig(sampleApps(), o)
	if !strings.Contains(string(raw), `"logs"`) {
		t.Error("debug level did not enable access logs")
	}
	raw, _, _ = BuildConfig(sampleApps(), opts())
	cfg = decode(t, raw)
	srv := dig(t, cfg, "apps", "http", "servers", "https").(map[string]any)
	if _, ok := srv["logs"]; ok {
		t.Error("access logs enabled at info level")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/infra/ingress/caddy/ -v`
Expected: FAIL — package does not exist yet.

- [ ] **Step 3: Implement the builder**

`internal/infra/ingress/caddy/config.go`:

```go
// Package caddy embeds Caddy v2 as the agent's ingress: per-app domains from
// each app's committed config.json are merged into one Caddy JSON config.
// BuildConfig is a pure function so the whole translation is golden-testable.
package caddy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"winterflow/internal/domain/model"
)

type AppIngress struct {
	AppID   string
	Ingress model.Ingress
}

type Options struct {
	HTTPPort   int
	HTTPSPort  int
	ACMEEmail  string
	StorageDir string
	LogLevel   string
}

// --- minimal typed mirror of Caddy's documented JSON config ---------------
// Only the fields we emit. Values arrive via json.Marshal of typed fields —
// user strings can never alter the config structure.

type caddyConfig struct {
	Admin   *adminConfig       `json:"admin,omitempty"`
	Logging *loggingConfig     `json:"logging,omitempty"`
	Storage map[string]any     `json:"storage,omitempty"`
	Apps    map[string]any     `json:"apps"`
}

type adminConfig struct {
	Disabled bool `json:"disabled"`
}

type loggingConfig struct {
	Logs map[string]logConfig `json:"logs"`
}

type logConfig struct {
	Level string `json:"level"`
}

type httpApp struct {
	HTTPPort  int                    `json:"http_port,omitempty"`
	HTTPSPort int                    `json:"https_port,omitempty"`
	Servers   map[string]*httpServer `json:"servers"`
}

type httpServer struct {
	Listen []string        `json:"listen"`
	Routes []route         `json:"routes"`
	Logs   *map[string]any `json:"logs,omitempty"` // presence toggles access logs
}

type route struct {
	Match  []matcher        `json:"match,omitempty"`
	Handle []map[string]any `json:"handle"`
}

type matcher struct {
	Host []string `json:"host,omitempty"`
	Path []string `json:"path,omitempty"`
}

type tlsApp struct {
	Automation tlsAutomation `json:"automation"`
}

type tlsAutomation struct {
	Policies []tlsPolicy `json:"policies"`
}

type tlsPolicy struct {
	Subjects []string         `json:"subjects"`
	Issuers  []map[string]any `json:"issuers"`
}

// ---------------------------------------------------------------------------

// mapLogLevel translates winterflow's LOG_LEVEL vocabulary to zap's.
func mapLogLevel(l string) string {
	switch strings.ToLower(l) {
	case "debug":
		return "DEBUG"
	case "warn", "warning":
		return "WARN"
	case "error":
		return "ERROR"
	default:
		return "INFO"
	}
}

func proxyRoute(host string, port int) route {
	return route{
		Match: []matcher{{Host: []string{host}}},
		Handle: []map[string]any{{
			"handler":   "reverse_proxy",
			"upstreams": []map[string]any{{"dial": "127.0.0.1:" + strconv.Itoa(port)}},
		}},
	}
}

func redirectRoute(host, path, location string, code int) route {
	m := matcher{Host: []string{host}}
	if path != "" {
		m.Path = []string{path}
	}
	return route{
		Match: []matcher{m},
		Handle: []map[string]any{{
			"handler":     "static_response",
			"status_code": strconv.Itoa(code),
			"headers":     http.Header{"Location": []string{location}},
		}},
	}
}

// BuildConfig merges per-app ingress fragments into one Caddy config.
// Fragment validation failures and cross-app duplicate domains produce
// warnings and exclusions, never errors: one app's bad routing must not take
// down another's. Apps are processed in sorted-AppID order so duplicate
// resolution is deterministic.
func BuildConfig(apps []AppIngress, opts Options) ([]byte, []string, error) {
	httpPort, httpsPort := opts.HTTPPort, opts.HTTPSPort
	if httpPort == 0 {
		httpPort = 80
	}
	if httpsPort == 0 {
		httpsPort = 443
	}

	sorted := make([]AppIngress, len(apps))
	copy(sorted, apps)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].AppID < sorted[j].AppID })

	var warnings []string
	claimed := map[string]string{} // domain -> appID that won it

	httpsRoutes := []route{}
	httpRoutes := []route{}
	var sslSubjects []string
	var skip []string

	for _, a := range sorted {
		if err := a.Ingress.Validate(); err != nil {
			warnings = append(warnings, fmt.Sprintf("app %s: ingress excluded: %v", a.AppID, err))
			continue
		}
		// Claim this app's hostnames; on cross-app duplicates the app keeps
		// its other domains — only the contested hostname is dropped.
		mine := map[string]bool{}
		for _, d := range a.Ingress.DomainNames() {
			if owner, taken := claimed[d]; taken {
				warnings = append(warnings, fmt.Sprintf("app %s: domain %s already claimed by app %s, skipping", a.AppID, d, owner))
				continue
			}
			claimed[d] = a.AppID
			mine[d] = true
		}

		// Path rules first (must match before their domain's main route);
		// group them per target list depending on the parent domain's SSL.
		sslOf := map[string]bool{}
		for _, d := range a.Ingress.Domains {
			sslOf[d.Domain] = d.SSL
		}
		for _, r := range a.Ingress.Redirects {
			if r.Path == "" {
				sslOf[r.Domain] = r.SSL
				continue
			}
		}
		for _, r := range a.Ingress.Redirects {
			if r.Path == "" || !mine[r.Domain] {
				continue
			}
			rt := redirectRoute(r.Domain, r.Path, r.To, r.Code)
			if sslOf[r.Domain] {
				httpsRoutes = append(httpsRoutes, rt)
			} else {
				httpRoutes = append(httpRoutes, rt)
			}
		}
		for _, d := range a.Ingress.Domains {
			if !mine[d.Domain] {
				continue
			}
			rt := proxyRoute(d.Domain, d.UpstreamPort)
			if d.SSL {
				httpsRoutes = append(httpsRoutes, rt)
				sslSubjects = append(sslSubjects, d.Domain)
			} else {
				httpRoutes = append(httpRoutes, rt)
				skip = append(skip, d.Domain)
			}
		}
		for _, r := range a.Ingress.Redirects {
			if r.Path != "" || !mine[r.Domain] {
				continue
			}
			// Domain-level redirect preserves the request URI. The
			// {http.request.uri} placeholder is a fixed literal appended to a
			// validated absolute URL — not user templating.
			rt := redirectRoute(r.Domain, "", r.To+"{http.request.uri}", r.Code)
			if r.SSL {
				httpsRoutes = append(httpsRoutes, rt)
				sslSubjects = append(sslSubjects, r.Domain)
			} else {
				httpRoutes = append(httpRoutes, rt)
				skip = append(skip, r.Domain)
			}
		}
	}

	httpsServer := &httpServer{Listen: []string{":" + strconv.Itoa(httpsPort)}, Routes: httpsRoutes}
	// Servers listening only on the HTTP port are exempt from automatic
	// HTTPS: no certs, no redirects for their hosts. Caddy appends its own
	// HTTP->HTTPS redirects for the ssl hosts to this server.
	httpServerCfg := &httpServer{Listen: []string{":" + strconv.Itoa(httpPort)}, Routes: httpRoutes}
	if strings.EqualFold(opts.LogLevel, "debug") {
		access := map[string]any{}
		httpsServer.Logs = &access
		httpServerCfg.Logs = &access
	}

	appsCfg := map[string]any{
		"http": httpApp{
			HTTPPort:  httpPort,
			HTTPSPort: httpsPort,
			Servers:   map[string]*httpServer{"https": httpsServer, "http": httpServerCfg},
		},
	}
	_ = skip // hosts on the :80-only server need no explicit skip; kept for clarity

	if len(sslSubjects) > 0 {
		issuer := map[string]any{"module": "acme"}
		if opts.ACMEEmail != "" {
			issuer["email"] = opts.ACMEEmail
		}
		appsCfg["tls"] = tlsApp{Automation: tlsAutomation{Policies: []tlsPolicy{{
			Subjects: sslSubjects,
			Issuers:  []map[string]any{issuer},
		}}}}
	}

	cfg := caddyConfig{
		Admin:   &adminConfig{Disabled: true},
		Logging: &loggingConfig{Logs: map[string]logConfig{"default": {Level: mapLogLevel(opts.LogLevel)}}},
		Storage: map[string]any{"module": "file_system", "root": opts.StorageDir},
		Apps:    appsCfg,
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, warnings, err
	}
	return raw, warnings, nil
}
```

Note: this file has **no Caddy import** — it emits Caddy's documented JSON config format from local structs. The Caddy dependency arrives in Task 6 with the manager. If during Task 6's integration test any field name proves off (Caddy rejects the config with a precise "unknown field" error), fix it HERE and extend the golden test — do not patch JSON at load time.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/infra/ingress/caddy/ -v && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/infra/ingress/
git commit -m "feat(ingress): pure Caddy JSON config builder with per-app isolation"
```

---

### Task 6: IngressManager — embedded Caddy lifecycle

**Files:**
- Modify: `internal/domain/port/app.go` (append `IngressManager`)
- Create: `internal/infra/ingress/caddy/manager.go`
- Create: `internal/infra/ingress/caddy/manager_test.go`
- Modify: `pkg/config/config.go` (ingress env accessors)
- Modify: `pkg/config/config_test.go`
- Modify: `go.mod`/`go.sum`

**Interfaces:**
- Consumes: `BuildConfig`, `AppIngress`, `Options` (Task 5); `model.ParseIngress` (Task 1); `cfg.GetAppsDataDir()`, `cfg.GetAgentDataDir()`, `cfg.GetLogLevel()`.
- Produces:

```go
// internal/domain/port/app.go
// IngressManager owns the embedded reverse proxy. Implementations must be
// safe for concurrent use and must never let an ingress failure propagate as
// a command failure.
type IngressManager interface {
	// Reload rebuilds the merged config from the apps on disk and hot-swaps
	// it. Returned strings are warnings for the triggering command's
	// response; a load failure keeps the previous config serving.
	Reload(ctx context.Context) []string
	// Enabled reports whether the proxy bound its ports at startup.
	Enabled() bool
}
```

```go
// internal/infra/ingress/caddy
func NewManager(cfg *config.ServerConfig, log *logger.Logger) *Manager
// Start builds the initial config and starts Caddy synchronously (so the
// caller can read Enabled() for the features map). Bind failure => logged,
// Enabled()==false, nil error — the agent runs on without ingress.
// Caddy stops when ctx is cancelled.
func (m *Manager) Start(ctx context.Context) error
func (m *Manager) Reload(ctx context.Context) []string
func (m *Manager) Enabled() bool
```

New config accessors in `pkg/config/config.go`:

```go
// GetIngressHTTPPort is the embedded proxy's HTTP port (default 80).
func (c *ServerConfig) GetIngressHTTPPort() int {
	if v, err := strconv.Atoi(os.Getenv("INGRESS_HTTP_PORT")); err == nil && v > 0 {
		return v
	}
	return 80
}

// GetIngressHTTPSPort is the embedded proxy's HTTPS port (default 443).
func (c *ServerConfig) GetIngressHTTPSPort() int {
	if v, err := strconv.Atoi(os.Getenv("INGRESS_HTTPS_PORT")); err == nil && v > 0 {
		return v
	}
	return 443
}

// GetIngressACMEEmail is the optional ACME account email.
func (c *ServerConfig) GetIngressACMEEmail() string {
	return os.Getenv("INGRESS_ACME_EMAIL")
}
```

- [ ] **Step 1: Add the Caddy dependency**

```bash
go get github.com/caddyserver/caddy/v2@latest
go mod tidy
```

- [ ] **Step 2: Write the failing integration test**

`internal/infra/ingress/caddy/manager_test.go`:

```go
package caddy

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// freePort grabs an ephemeral port. ssl:false only — no ACME in tests.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// writeApp writes a minimal committed app dir the scanner reads.
func writeApp(t *testing.T, dataDir, appID, configJSON string) {
	t.Helper()
	dir := filepath.Join(dataDir, "apps-data", appID, ".winterflow")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(configJSON), 0o644); err != nil {
		t.Fatal(err)
	}
}

func getVia(t *testing.T, port int, host, path string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	req.Host = host
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var resp *http.Response
	var err error
	for i := 0; i < 50; i++ { // caddy start is fast but async-ish; retry briefly
		resp, err = client.Do(req)
		if err == nil {
			return resp
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("request via caddy never succeeded: %v", err)
	return nil
}

func TestManagerServesReloadsAndIsolates(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello-from-app")
	}))
	defer upstream.Close()
	upstreamPort := upstream.Listener.Addr().(*net.TCPAddr).Port

	dataDir := t.TempDir()
	httpPort := freePort(t)
	httpsPort := freePort(t)
	t.Setenv("AGENT_DATA_DIR", dataDir)
	t.Setenv("INGRESS_HTTP_PORT", fmt.Sprint(httpPort))
	t.Setenv("INGRESS_HTTPS_PORT", fmt.Sprint(httpsPort))
	t.Setenv("LOG_LEVEL", "error")

	appCfg := func(port int) string {
		ing := map[string]any{
			"name": "t",
			"ingress": map[string]any{
				"domains": []map[string]any{{"domain": "app.test", "upstream_port": port, "ssl": false}},
				"redirects": []map[string]any{
					{"domain": "old.test", "to": "http://app.test/landed", "code": 301, "ssl": false},
					{"domain": "app.test", "path": "/legacy/*", "to": "http://app.test/new", "code": 302},
				},
			},
		}
		raw, _ := json.Marshal(ing)
		return string(raw)
	}
	writeApp(t, dataDir, "app-1", appCfg(upstreamPort))
	// A second app with a broken config must not affect app-1.
	writeApp(t, dataDir, "app-2", `{"name":"broken","ingress":{"domains":[{"domain":"BAD HOST","upstream_port":1}]}}`)

	cfg := config.NewServerConfig("standalone")
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	m := NewManager(cfg, log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if !m.Enabled() {
		t.Fatal("manager not enabled after successful start")
	}

	// Proxying.
	resp := getVia(t, httpPort, "app.test", "/")
	body := make([]byte, 32)
	n, _ := resp.Body.Read(body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body[:n]), "hello-from-app") {
		t.Fatalf("proxy: status %d body %q", resp.StatusCode, body[:n])
	}

	// Domain-level redirect preserves the URI.
	resp = getVia(t, httpPort, "old.test", "/some/path")
	resp.Body.Close()
	if resp.StatusCode != 301 || resp.Header.Get("Location") != "http://app.test/landed/some/path" {
		t.Fatalf("domain redirect: %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Path rule redirects to the exact target and wins over the proxy route.
	resp = getVia(t, httpPort, "app.test", "/legacy/x")
	resp.Body.Close()
	if resp.StatusCode != 302 || resp.Header.Get("Location") != "http://app.test/new" {
		t.Fatalf("path rule: %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Hot reload: change the app's domain, Reload, old gone / new serving.
	newCfg := strings.ReplaceAll(appCfg(upstreamPort), "app.test", "app2.test")
	writeApp(t, dataDir, "app-1", newCfg)
	if warnings := m.Reload(ctx); len(warnings) != 1 { // app-2's bad fragment still warns
		t.Fatalf("reload warnings = %v, want exactly the app-2 exclusion", warnings)
	}
	resp = getVia(t, httpPort, "app2.test", "/")
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("reloaded domain not serving: %d", resp.StatusCode)
	}
}

func TestManagerDisabledOnBindFailure(t *testing.T) {
	// Occupy the HTTP port so caddy cannot bind it.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	taken := l.Addr().(*net.TCPAddr).Port

	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	t.Setenv("INGRESS_HTTP_PORT", fmt.Sprint(taken))
	t.Setenv("INGRESS_HTTPS_PORT", fmt.Sprint(freePort(t)))
	t.Setenv("LOG_LEVEL", "error")

	cfg := config.NewServerConfig("standalone")
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	m := NewManager(cfg, log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.Start(ctx); err != nil {
		t.Fatalf("bind failure must degrade, not error: %v", err)
	}
	if m.Enabled() {
		t.Fatal("manager claims enabled despite bind failure")
	}
	if w := m.Reload(ctx); w != nil {
		t.Fatalf("disabled manager Reload must no-op, got %v", w)
	}
}
```

Note: `GetAgentDataDir` reads `AGENT_DATA_DIR` (verify the exact env var name in `pkg/config/config.go:142-146` before running; adjust `t.Setenv` if it differs).

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/infra/ingress/caddy/ -run TestManager -v`
Expected: FAIL — `undefined: NewManager`.

- [ ] **Step 4: Implement the manager**

`internal/infra/ingress/caddy/manager.go`:

```go
package caddy

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"winterflow/internal/domain/model"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	caddy "github.com/caddyserver/caddy/v2"
	// Curated module set (NOT modules/standard): http server + static_response,
	// reverse proxy, TLS/ACME, file_system cert storage.
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
	_ "github.com/caddyserver/caddy/v2/modules/caddytls"
	_ "github.com/caddyserver/caddy/v2/modules/caddytls/standardstek"
	_ "github.com/caddyserver/caddy/v2/modules/filestorage"
)

// Manager owns the embedded Caddy instance. Ingress failures degrade ingress
// only: Start never fails the agent, Reload never fails a deploy.
type Manager struct {
	cfg *config.ServerConfig
	log *logger.Logger

	mu      sync.Mutex
	enabled bool
}

func NewManager(cfg *config.ServerConfig, log *logger.Logger) *Manager {
	return &Manager{cfg: cfg, log: log}
}

func (m *Manager) options() Options {
	return Options{
		HTTPPort:   m.cfg.GetIngressHTTPPort(),
		HTTPSPort:  m.cfg.GetIngressHTTPSPort(),
		ACMEEmail:  m.cfg.GetIngressACMEEmail(),
		StorageDir: filepath.Join(m.cfg.GetAgentDataDir(), "caddy"),
		LogLevel:   m.cfg.GetLogLevel(),
	}
}

// scanApps reads every committed config.json under apps-data and returns the
// parsed ingress fragments. Unreadable/unparseable apps are skipped with a
// warning — one broken app must not stop the scan.
func (m *Manager) scanApps() ([]AppIngress, []string) {
	appsDir := m.cfg.GetAppsDataDir()
	entries, err := os.ReadDir(appsDir)
	if err != nil {
		if !os.IsNotExist(err) {
			m.log.Warn("ingress: scan apps-data", "error", err)
		}
		return nil, nil
	}
	var out []AppIngress
	var warnings []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		appID := e.Name()
		raw, err := os.ReadFile(filepath.Join(appsDir, appID, ".winterflow", "config.json"))
		if err != nil {
			continue // app without a committed config yet
		}
		ing, err := model.ParseIngress(raw)
		if err != nil {
			warnings = append(warnings, "app "+appID+": unreadable config, ingress skipped: "+err.Error())
			continue
		}
		if ing == nil {
			continue
		}
		out = append(out, AppIngress{AppID: appID, Ingress: *ing})
	}
	return out, warnings
}

func (m *Manager) buildConfig() ([]byte, []string) {
	apps, scanWarnings := m.scanApps()
	cfg, buildWarnings, err := BuildConfig(apps, m.options())
	warnings := append(scanWarnings, buildWarnings...)
	if err != nil {
		// Marshal failure of our own structs: effectively impossible, but
		// degrade rather than propagate.
		warnings = append(warnings, "ingress: build config: "+err.Error())
		return nil, warnings
	}
	return cfg, warnings
}

// Start builds the initial config and starts Caddy synchronously so callers
// can read Enabled() for the agent's feature map. A start failure (typically
// binding 80/443 without CAP_NET_BIND_SERVICE) logs, leaves the manager
// disabled, and returns nil: the agent runs on without ingress.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, warnings := m.buildConfig()
	for _, w := range warnings {
		m.log.Warn("ingress", "warning", w)
	}
	if cfg == nil {
		return nil
	}
	if err := caddy.Load(cfg, true); err != nil {
		m.log.Warn("ingress disabled: caddy failed to start (need CAP_NET_BIND_SERVICE for ports 80/443?)", "error", err)
		return nil
	}
	m.enabled = true
	m.log.Info("ingress started",
		"http_port", m.cfg.GetIngressHTTPPort(),
		"https_port", m.cfg.GetIngressHTTPSPort())

	go func() {
		<-ctx.Done()
		if err := caddy.Stop(); err != nil {
			m.log.Warn("ingress: caddy stop", "error", err)
		}
	}()
	return nil
}

// Reload rebuilds from disk and hot-swaps the config. On a load failure the
// previous config keeps serving and the error is returned as a warning for
// the triggering command's response.
func (m *Manager) Reload(ctx context.Context) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.enabled {
		return nil
	}
	cfg, warnings := m.buildConfig()
	if cfg == nil {
		return warnings
	}
	if err := caddy.Load(cfg, false); err != nil {
		m.log.Error("ingress: reload failed, previous config keeps serving", "error", err)
		warnings = append(warnings, "ingress reload failed (previous routing still active): "+err.Error())
	}
	return warnings
}

func (m *Manager) Enabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.enabled
}
```

Append the `IngressManager` interface (from **Interfaces** above) to `internal/domain/port/app.go`.

Add the three env accessors (from **Interfaces** above) to `pkg/config/config.go`, and table-test them in `pkg/config/config_test.go` following that file's existing style (`t.Setenv` for `INGRESS_HTTP_PORT` unset/`"8080"`/`"junk"` → 80/8080/80).

Implementation notes:
- `caddy.Load(cfg, true)` both starts and reconfigures — there is no separate Run needed when loading a full config; if the caddy version's API differs (e.g. `caddy.Run` required first), check `go doc github.com/caddyserver/caddy/v2 Load` and adapt — the behavioral contract (sync start, hot reload, degrade on failure) is what the tests pin.
- If `modules/filestorage` or `modules/caddytls/standardstek` don't exist under those paths in the resolved version, find the right registration imports with `go doc` / `grep -r RegisterModule $(go env GOMODCACHE)/github.com/caddyserver/caddy` — the test failing with `unknown module` tells you exactly which id is missing.
- If a bind failure turns out to surface asynchronously rather than from `caddy.Load`, verify with the `TestManagerDisabledOnBindFailure` test and adapt (e.g. probe-listen the ports first, then release and load).

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/infra/ingress/... ./pkg/config/ -v && go vet ./...`
Expected: PASS (integration test really proxies through embedded Caddy).

- [ ] **Step 6: Commit**

```bash
git add internal/infra/ingress/ internal/domain/port/app.go pkg/config/ go.mod go.sum
git commit -m "feat(ingress): embedded Caddy manager with hot reload and graceful degradation"
```

---

### Task 7: Agent wiring — dispatcher reload, save warnings, features

**Files:**
- Modify: `internal/domain/command/app.go` (`SaveAppResponse.Warnings`)
- Modify: `internal/app/agent/dispatcher.go` (ingress param + reload hooks)
- Modify: `internal/app/agent/dispatcher_test.go`
- Modify: `cmd/agent/main.go` (manager + feature flag)
- Modify: `internal/infra/bootstrap/standalone.go` (manager + feature flag)

**Interfaces:**
- Consumes: `port.IngressManager`, `ingresscaddy.NewManager(cfg, log)`, `(*Manager).Start/Reload/Enabled` (Task 6).
- Produces: `command.SaveAppResponse.Warnings []string`; `appagent.NewDispatcher(orch *dockercompose.Repository, ingress port.IngressManager, log *logger.Logger) *Dispatcher` (nil ingress = no reloads, used by existing tests); agent feature `"ingress": <bool>`.

- [ ] **Step 1: Write the failing test**

Read `internal/app/agent/dispatcher_test.go` first and follow its envelope-building idiom. Add:

```go
type fakeIngress struct {
	reloads  int
	warnings []string
}

func (f *fakeIngress) Reload(_ context.Context) []string { f.reloads++; return f.warnings }
func (f *fakeIngress) Enabled() bool                     { return true }
```

Test cases (build request envelopes the same way the file's existing tests do):

1. `TestDispatcherReloadsIngressAfterMutations` — a dispatcher built with a `fakeIngress`; dispatch `app.save`, `app.delete`, `app.rename`, `app.rollback` against a temp-dir orchestrator (the calls may fail at the compose level — that's fine, reload must happen only on success, so use `app.save` with a valid minimal payload as the success case and assert `reloads == 1` after it; assert `reloads` does NOT increment after a failed `app.delete` of a nonexistent app).
2. `TestSaveResponseCarriesIngressWarnings` — `fakeIngress{warnings: []string{"w1"}}`; dispatch a successful `app.save`; decode the `ResponseEnvelope` payload into `command.SaveAppResponse` and assert `Warnings == ["w1"]`.
3. `TestDispatcherNilIngress` — dispatcher built with `nil` ingress dispatches `app.save` without panicking.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/app/agent/ -v`
Expected: FAIL — `NewDispatcher` signature / `fakeIngress` unused / `Warnings` undefined.

- [ ] **Step 3: Implement**

In `internal/domain/command/app.go`:

```go
type SaveAppResponse struct {
	AppID    string `json:"app_id"`
	Revision string `json:"revision"` // git commit hash of the save
	// Warnings are non-fatal ingress problems (config excluded, reload
	// failed): the save itself succeeded.
	Warnings []string `json:"warnings,omitempty"`
}
```

In `internal/app/agent/dispatcher.go`:

```go
type Dispatcher struct {
	orch     *dockercompose.Repository
	ingress  port.IngressManager
	log      *logger.Logger
	handlers map[command.Type]handlerFunc
}

func NewDispatcher(orch *dockercompose.Repository, ingress port.IngressManager, log *logger.Logger) *Dispatcher {
	d := &Dispatcher{orch: orch, ingress: ingress, log: log}
	d.handlers = d.newHandlers()
	return d
}

// reloadIngress rebuilds the proxy config after a mutation. Warnings are
// returned for responses that carry them (app.save) and logged otherwise.
// A nil manager (tests, ingress disabled) is a no-op.
func (d *Dispatcher) reloadIngress(ctx context.Context) []string {
	if d.ingress == nil {
		return nil
	}
	warnings := d.ingress.Reload(ctx)
	for _, w := range warnings {
		d.log.Warn("ingress", "warning", w)
	}
	return warnings
}
```

(add `"winterflow/internal/domain/port"` to imports). Update the mutating handlers in `newHandlers`:

```go
		command.TypeAppSave: handle(d, func(ctx context.Context, in command.SaveAppRequest) (command.SaveAppResponse, error) {
			save := d.orch.SaveApp
			if in.Draft {
				save = d.orch.SaveAppDraft
			}
			hash, err := save(ctx, in.App)
			resp := command.SaveAppResponse{AppID: in.App.AppID, Revision: hash}
			if err == nil {
				resp.Warnings = d.reloadIngress(ctx)
			}
			return resp, err
		}),
```

For `TypeAppDelete`, `TypeAppRename`, and `TypeAppRollback`, reload after success (warnings logged inside `reloadIngress`, responses unchanged), e.g.:

```go
		command.TypeAppDelete: handle(d, func(ctx context.Context, in command.DeleteAppRequest) (command.DeleteAppResponse, error) {
			if err := d.orch.DeleteApp(ctx, in.AppID); err != nil {
				return command.DeleteAppResponse{AppID: in.AppID}, err
			}
			d.reloadIngress(ctx)
			return command.DeleteAppResponse{AppID: in.AppID}, nil
		}),
		command.TypeAppRename: handle(d, func(ctx context.Context, in command.RenameAppRequest) (command.RenameAppResponse, error) {
			if err := d.orch.RenameApp(ctx, in.AppID, in.Name); err != nil {
				return command.RenameAppResponse{AppID: in.AppID, Name: in.Name}, err
			}
			d.reloadIngress(ctx)
			return command.RenameAppResponse{AppID: in.AppID, Name: in.Name}, nil
		}),
		command.TypeAppRollback: handle(d, func(ctx context.Context, in command.RollbackAppRequest) (command.RollbackAppResponse, error) {
			newHead, err := d.orch.Rollback(ctx, in.AppID, in.Hash)
			if err != nil {
				return command.RollbackAppResponse{AppID: in.AppID, Revision: newHead}, err
			}
			d.reloadIngress(ctx)
			return command.RollbackAppResponse{AppID: in.AppID, Revision: newHead}, nil
		}),
```

Wire in `cmd/agent/main.go` (around line 79, before the features map is sent):

```go
	orchestrator := dockercompose.NewRepository(cfg, log)
	ingressManager := ingresscaddy.NewManager(cfg, log)
	if err := ingressManager.Start(ctx); err != nil {
		log.Warn("ingress manager start", "error", err)
	}
	agent.SetDispatcher(appagent.NewDispatcher(orchestrator, ingressManager, log))
```

(import `ingresscaddy "winterflow/internal/infra/ingress/caddy"`), and add to the existing `features` map:

```go
	features["ingress"] = ingressManager.Enabled()
```

(match however the map literal is written there — read the surrounding lines first).

Wire in `internal/infra/bootstrap/standalone.go` (lines 44–46):

```go
	orchestrator := dockercompose.NewRepository(cfg, log)
	ingressManager := ingresscaddy.NewManager(cfg, log)
	if err := ingressManager.Start(ctx); err != nil {
		log.Warn("ingress manager start", "error", err)
	}
	agentDispatcher := appagent.NewDispatcher(orchestrator, ingressManager, log)
```

and thread the flag into `publishEmbeddedCapabilities`: change its signature to `publishEmbeddedCapabilities(ctx, b, serverRepo, cfg, log, ingressManager.Enabled())` and set `"ingress": ingressEnabled` in its `features` map. Update the existing call site.

Fix any other `NewDispatcher` call sites (tests, `inprocess_test.go`) by passing `nil` for ingress: `grep -rn "NewDispatcher(" --include='*.go'`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/app/agent/ ./internal/infra/bootstrap/ -v && go build ./... && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/command/ internal/app/agent/ cmd/agent/ internal/infra/bootstrap/
git commit -m "feat(ingress): reload proxy after mutations, save warnings, ingress feature flag"
```

---

### Task 8: Expose server features to the web

**Files:**
- Modify: `internal/domain/model/server.go` (add `Features`)
- Modify: `internal/infra/db/repository/server.go` (`GetServers` loads features; `toDomainServer` maps them)
- Test: extend `internal/infra/db/repository/server_test.go`

**Interfaces:**
- Consumes: existing `models.Server.Features []ServerFeature` relation, `SaveCapabilities` (already persists features).
- Produces: `model.Server.Features map[string]bool `json:"features,omitempty"`` — reaches the browser through the untouched `GetServers` handler.

- [ ] **Step 1: Write the failing test**

Read `internal/infra/db/repository/server_test.go` for the existing seeding idiom, then add a test that: seeds an org+server+user membership, calls `SaveCapabilities(ctx, serverID, map[string]string{...}, map[string]bool{"ingress": true})`, then `GetServers(ctx, userID)` and asserts `servers[0].Features["ingress"] == true`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/infra/db/repository/ -run TestGetServersIncludesFeatures -v`
Expected: FAIL — `servers[0].Features undefined`.

- [ ] **Step 3: Implement**

`internal/domain/model/server.go` — add to `Server`:

```go
	// Features are agent-advertised booleans (can_install, ingress, ...);
	// the UI gates capability-dependent panels on them.
	Features map[string]bool `json:"features,omitempty"`
```

`internal/infra/db/repository/server.go` — in `GetServers`, add `.Relation("Features")` next to `.Relation("Capabilities")`; in `toDomainServer`, map:

```go
	if len(s.Features) > 0 {
		srv.Features = make(map[string]bool, len(s.Features))
		for _, f := range s.Features {
			srv.Features[f.Name] = f.IsEnabled
		}
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/infra/db/repository/ -v && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model/server.go internal/infra/db/repository/
git commit -m "feat(server): expose agent feature flags in get-servers"
```

---

### Task 9: Web — ingress types, editor IO, validation

**Files:**
- Modify: `web/src/types/app-config.ts`
- Modify: `web/src/lib/app-editor-io.ts`

**Interfaces:**
- Consumes: JSON wire shapes from Task 1 (`ingress.domains[].{domain,upstream_port,ssl}`, `ingress.redirects[].{domain,path?,to,code,ssl?}`).
- Produces:

```ts
// web/src/types/app-config.ts
export type IngressDomain = {
  id: string; // localId, editor-only — stripped on save
  domain: string;
  upstream_port: number | ""; // "" while the field is empty in the editor
  ssl: boolean;
};
export type IngressRedirect = {
  id: string;
  domain: string;
  path: string; // "" = whole-domain redirect
  to: string;
  code: 301 | 302 | 307 | 308;
  ssl: boolean; // only meaningful when path === ""
};
export type AppIngress = {
  domains: IngressDomain[];
  redirects: IngressRedirect[];
};
// AppConfig gains: ingress?: AppIngress;
```

and in `app-editor-io.ts`: `loadEditorState` populates `config.ingress` (with fresh `localId`s), `validateEditorState` returns a message for invalid ingress (mirror of Go `Validate()`), `buildSavePayload`'s `config` object includes an `ingress` key (ids stripped, `upstream_port` as number) **whenever `state.config.ingress` is set** — an empty `{domains:[],redirects:[]}` is intentionally sent so the agent/API clear old rows.

- [ ] **Step 1: Add the types**

Apply the `IngressDomain`/`IngressRedirect`/`AppIngress` types above to `web/src/types/app-config.ts` and add `ingress?: AppIngress;` to `AppConfig`.

- [ ] **Step 2: Extend editor IO**

In `web/src/lib/app-editor-io.ts`:

1. Where `loadEditorState` builds `config` from the fetched blob (around line 89, next to the `source` extraction), add:

```ts
  const cfgIngress = (cfg as { ingress?: { domains?: unknown[]; redirects?: unknown[] } }).ingress;
  const ingress = cfgIngress
    ? {
        domains: (cfgIngress.domains ?? []).map((d) => {
          const v = d as { domain?: string; upstream_port?: number; ssl?: boolean };
          return {
            id: localId("dom"),
            domain: v.domain ?? "",
            upstream_port: v.upstream_port ?? ("" as const),
            ssl: v.ssl ?? false,
          };
        }),
        redirects: (cfgIngress.redirects ?? []).map((r) => {
          const v = r as { domain?: string; path?: string; to?: string; code?: number; ssl?: boolean };
          return {
            id: localId("red"),
            domain: v.domain ?? "",
            path: v.path ?? "",
            to: v.to ?? "",
            code: (v.code ?? 301) as 301 | 302 | 307 | 308,
            ssl: v.ssl ?? false,
          };
        }),
      }
    : undefined;
```

and include `ingress` in the returned `config` object.

2. In `validateEditorState` (the function around line 108 returning a message string or null), append ingress checks that mirror Go's `Validate()`:

```ts
  const hostnameRe = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$/;
  const ing = state.config.ingress;
  if (ing) {
    const seen = new Set<string>();
    for (const d of ing.domains) {
      if (!hostnameRe.test(d.domain)) return `"${d.domain}" is not a valid lowercase hostname.`;
      if (seen.has(d.domain)) return `Duplicate domain "${d.domain}".`;
      seen.add(d.domain);
      const port = Number(d.upstream_port);
      if (!Number.isInteger(port) || port < 1 || port > 65535)
        return `Domain "${d.domain}": upstream port must be 1-65535.`;
    }
    for (const r of ing.redirects) {
      let target: URL;
      try {
        target = new URL(r.to);
      } catch {
        return `Redirect for "${r.domain}": target must be an absolute http(s) URL.`;
      }
      if (target.protocol !== "http:" && target.protocol !== "https:")
        return `Redirect for "${r.domain}": target must be an absolute http(s) URL.`;
      if (r.path === "") {
        if (!hostnameRe.test(r.domain)) return `"${r.domain}" is not a valid lowercase hostname.`;
        if (seen.has(r.domain)) return `Duplicate domain "${r.domain}".`;
        seen.add(r.domain);
      } else {
        if (!r.path.startsWith("/")) return `Redirect path "${r.path}" must start with /.`;
        if (!seen.has(r.domain))
          return `Path rule for "${r.domain}": add that domain as a route or redirect first.`;
      }
    }
  }
```

3. In `buildSavePayload`, inside the `config` object literal (after `variables:`), add:

```ts
    ...(state.config.ingress
      ? {
          ingress: {
            domains: state.config.ingress.domains.map((d) => ({
              domain: d.domain.trim(),
              upstream_port: Number(d.upstream_port),
              ssl: d.ssl,
            })),
            redirects: state.config.ingress.redirects.map((r) => ({
              domain: r.domain.trim(),
              ...(r.path ? { path: r.path.trim() } : {}),
              to: r.to.trim(),
              code: r.code,
              ...(r.path ? {} : { ssl: r.ssl }),
            })),
          },
        }
      : {}),
```

- [ ] **Step 3: Verify**

Run: `pnpm --dir web run build && pnpm --dir web run lint`
Expected: clean `tsc -b` + ESLint (there is no JS test runner in this repo; the type-checker and Task 12's E2E are the verification).

- [ ] **Step 4: Commit**

```bash
git add web/src/types/app-config.ts web/src/lib/app-editor-io.ts
git commit -m "feat(web): ingress config types, load/save/validate plumbing"
```

---

### Task 10: Web — Domains & Routing editor card

**Files:**
- Create: `web/src/components/ingress-editor.tsx`
- Modify: `web/src/components/app-editor.tsx` (render the card)
- Modify: `web/src/locales/en.ts` (strings)

**Interfaces:**
- Consumes: `AppEditorState`, `IngressDomain`, `IngressRedirect`, `localId` (Task 9); `GET /api/v1/domains/check?domain=&app_id=` → `{data: {available, claims: [{domain, app_name, server_name}]}}` (Task 4); server features gating happens in Task 11 alongside the other server-context changes.
- Produces: `<IngressEditor state={state} onChange={onChange} appId={appId?} />` — a Card matching the Files/Variables idiom in `app-editor.tsx` (Card/CardHeader/CardTitle/CardContent, add/remove rows with `Plus`/`Trash2` buttons, `Switch` for booleans).

- [ ] **Step 1: Build the component**

`web/src/components/ingress-editor.tsx` — follow `app-editor.tsx`'s row/handler style exactly (read it first). Structure:

```tsx
import { Plus, Trash2 } from "lucide-react";
import { useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Switch } from "@/components/ui/switch";
import { apiBaseUrl } from "@/config";
import { localId, type AppEditorState, type AppIngress } from "@/types/app-config";

type Props = {
  state: AppEditorState;
  onChange: (next: AppEditorState) => void;
  appId?: string;
};

const emptyIngress = (): AppIngress => ({ domains: [], redirects: [] });

export function IngressEditor({ state, onChange, appId }: Props) {
  // taken[domain] = "used by app X on server Y" from the live check.
  const [taken, setTaken] = useState<Record<string, string>>({});
  const timers = useRef<Record<string, ReturnType<typeof setTimeout>>>({});

  const ing = state.config.ingress ?? emptyIngress();
  const setIngress = (next: AppIngress) =>
    onChange({ ...state, config: { ...state.config, ingress: next } });

  const checkDomain = (id: string, domain: string) => {
    clearTimeout(timers.current[id]);
    if (!domain) return;
    timers.current[id] = setTimeout(async () => {
      try {
        const qs = new URLSearchParams({ domain });
        if (appId) qs.set("app_id", appId);
        const res = await fetch(`${apiBaseUrl}/api/v1/domains/check?${qs}`, { credentials: "include" });
        const body = (await res.json()) as {
          data?: { available?: boolean; claims?: { app_name: string; server_name: string }[] };
        };
        const claim = body.data?.claims?.[0];
        setTaken((prev) => ({
          ...prev,
          [domain]: body.data?.available === false && claim
            ? `Already used by app "${claim.app_name}" on server "${claim.server_name}".`
            : "",
        }));
      } catch {
        // Availability is advisory; the save-time check is authoritative.
      }
    }, 400);
  };

  // ... addDomain/updateDomain/removeDomain and addRedirect/updateRedirect/
  // removeRedirect follow app-editor.tsx's map/filter idiom over ing.domains
  // and ing.redirects, each ending in setIngress(...).

  return (
    <Card>
      <CardHeader><CardTitle>Domains & Routing</CardTitle></CardHeader>
      <CardContent className="space-y-6">
        {/* Domain rows: Input(domain onChange -> updateDomain + checkDomain)
            · Input(type=number, upstream port) · Switch("HTTPS via Let's Encrypt")
            · Trash2 button; under the row, taken[d.domain] error text when set,
            and the hint: "The compose file must publish this port on 127.0.0.1,
            e.g. \"127.0.0.1:8088:80\"." */}
        {/* Redirect rows: Input(domain) · Input(path, placeholder "/old-path/* (empty = whole domain)")
            · Input(target URL) · native <select> for 301/302/307/308
            · Switch(SSL) rendered ONLY when r.path === "" · Trash2 */}
        {/* Two "Add" buttons with Plus icons, matching addFile's style */}
      </CardContent>
    </Card>
  );
}
```

Fill in the elided row handlers/JSX completely (they are mechanical repeats of `app-editor.tsx`'s file/variable rows — same Tailwind classes, same Button variants). Use the `select` element styling already used elsewhere in the web app if one exists (`grep -rn "<select" web/src/components/`); otherwise a minimal styled `<select>` is fine.

In `app-editor.tsx`, render `<IngressEditor state={state} onChange={onChange} appId={state.config.id || undefined} />` after the Variables card. If UI strings in this repo go through `web/src/locales/en.ts` (check how existing components reference it), route the new strings the same way; if components inline English strings, inline them.

- [ ] **Step 2: Verify**

Run: `pnpm --dir web run build && pnpm --dir web run lint`
Expected: clean build. Then `make standalone` + `make web`, open the app editor, and confirm: rows add/remove, port hint shows, a taken domain (save one app with it first) flags inline after ~400ms.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/ web/src/locales/
git commit -m "feat(web): Domains & Routing card in the app editor"
```

---

### Task 11: Domain chips on app cards + feature gating

**Files:**
- Modify: `internal/app/web/handler/app/get_apps.go` (attach domains)
- Modify: `internal/domain/usecase/app/usecase.go` (`ListDomains` passthrough)
- Modify: `web/src/context/apps-context.tsx`, `web/src/context/apps-context-base.ts` (App type gains `domains`)
- Modify: `web/src/context/servers-context.tsx`, `web/src/context/servers-context-base.ts` (Server type gains `features`)
- Modify: `web/src/components/app-grid.tsx` (chips)
- Modify: the component from Task 10's integration point (gate the card on `features.ingress`)

**Interfaces:**
- Consumes: `port.AppDomainRepository.ListForServer` (Task 2), `model.AppDomainInfo` (Task 1), `model.Server.Features` (Task 8).
- Produces: `GET /api/v1/app/get-apps` items gain `domains?: [{domain, ssl, kind}]`; web `Server.features?: Record<string, boolean>`; app cards render each `kind === "route"` domain as an external link (`https://` when ssl, else `http://`).

- [ ] **Step 1: API side**

Usecase passthrough in `internal/domain/usecase/app/usecase.go`:

```go
// ListDomains returns the server's indexed domains grouped by app id, for
// decorating the DB-backed app listing without an agent round-trip.
func (uc *UseCase) ListDomains(ctx context.Context, serverID string) (map[string][]model.AppDomainInfo, error) {
	if uc.domains == nil {
		return nil, nil
	}
	return uc.domains.ListForServer(ctx, serverID)
}
```

`internal/app/web/handler/app/get_apps.go` — decorate the response:

```go
func (h *Handler) GetApps(w http.ResponseWriter, r *http.Request) {
	serverID := r.URL.Query().Get(serverIDKey)

	// @todo check ownership

	apps, err := h.usecase.GetApps(r.Context(), serverID)
	if err != nil {
		util.Error(w, "failed to load servers", nil)
		return
	}
	domains, err := h.usecase.ListDomains(r.Context(), serverID)
	if err != nil {
		// Chips are decoration; the listing must not fail over the index.
		domains = nil
	}
	type appWithDomains struct {
		model.App
		Domains []model.AppDomainInfo `json:"domains,omitempty"`
	}
	out := make([]appWithDomains, 0, len(apps))
	for _, a := range apps {
		out = append(out, appWithDomains{App: a, Domains: domains[a.ID]})
	}
	util.Success(w, "", struct {
		Apps []appWithDomains `json:"apps"`
	}{Apps: out})
}
```

Extend the usecase test file with `TestListDomainsNilRepo` (nil repo → `nil, nil`).

- [ ] **Step 2: Web side**

- `apps-context-base.ts`: add `domains?: { domain: string; ssl: boolean; kind: "route" | "redirect" }[]` to the `App` type; `apps-context.tsx`: add the same field to `AppResponse` and copy it through the mapping.
- `servers-context-base.ts`: add `features?: Record<string, boolean>` to `Server`; `servers-context.tsx`: map it from the response (`features` key, present since Task 8).
- `app-grid.tsx`: under each app card's name row, render route-kind domains as links:

```tsx
{app.domains?.filter((d) => d.kind === "route").map((d) => (
  <a
    key={d.domain}
    href={`${d.ssl ? "https" : "http"}://${d.domain}`}
    target="_blank"
    rel="noreferrer"
    onClick={(e) => e.stopPropagation()}
    className="text-xs text-muted-foreground hover:text-foreground hover:underline"
  >
    {d.domain}
  </a>
))}
```

(match the card's existing layout — read `app-grid.tsx` first and place the chips where the version/status metadata already sits).
- Gating: where Task 10 rendered `<IngressEditor .../>`, wrap with the active server's features (via `useServers()`):

```tsx
{activeServer?.features?.ingress ? (
  <IngressEditor state={state} onChange={onChange} appId={...} />
) : (
  <Card>
    <CardHeader><CardTitle>Domains & Routing</CardTitle></CardHeader>
    <CardContent>
      <p className="text-sm text-muted-foreground">
        This server's agent doesn't support ingress. Update the agent to configure domains.
      </p>
    </CardContent>
  </Card>
)}
```

The editor pages already know the server (create-app/app-details) — thread `activeServer` from `useServers()` the same way those pages access server data (read them first; if the editor component doesn't receive the server today, gate at the page level instead of inside `AppEditor`).

- [ ] **Step 3: Verify**

Run: `go test ./... && go vet ./... && pnpm --dir web run build && pnpm --dir web run lint`
Expected: all green.

- [ ] **Step 4: Commit**

```bash
git add internal/ web/
git commit -m "feat(ingress): domain chips on app cards, feature-gated editor card"
```

---

### Task 12: End-to-end verification + env docs

**Files:**
- Modify: `.env.dist` (document the new vars)
- No other code — this task is verification.

- [ ] **Step 1: Document env vars**

Append to `.env.dist` (match its existing comment style — read it first):

```
# Embedded ingress (Caddy) — agent/standalone only.
# Ports 80/443 need root or CAP_NET_BIND_SERVICE; override for dev:
#INGRESS_HTTP_PORT=8080
#INGRESS_HTTPS_PORT=8443
#INGRESS_ACME_EMAIL=you@example.com
```

- [ ] **Step 2: Full test + lint sweep**

Run: `go test ./... && go vet ./... && pnpm --dir web run build && pnpm --dir web run lint`
Expected: everything passes.

- [ ] **Step 3: E2E through the standalone binary**

```bash
export INGRESS_HTTP_PORT=8080 INGRESS_HTTPS_PORT=8443
make standalone   # in the background; watch for "ingress started"
```

Then in the UI (`make web`): create an app from the default compose (nginx on host port 8088 — the editor default publishes it), add domain `test.localhost` → port `8088`, SSL off, plus a redirect `old.localhost` → `http://test.localhost`, code 301. Save, wait for the deploy toast, then:

```bash
curl -s -H 'Host: test.localhost' http://127.0.0.1:8080/ | head -1
# Expected: nginx welcome-page HTML
curl -s -o /dev/null -w '%{http_code} %{redirect_url}\n' -H 'Host: old.localhost' http://127.0.0.1:8080/x
# Expected: 301 http://test.localhost/x
```

Also verify degradation: save the app again with the domain edited to one already claimed by a second app → the editor flags it inline and the save returns a 400 naming the owner.

- [ ] **Step 4: Commit**

```bash
git add .env.dist
git commit -m "docs: ingress env vars in .env.dist"
```

---

## Self-review notes (already applied)

- **Spec coverage:** data model (T1), DB index + conflict (T2, T3), check endpoint (T4), pure builder + isolation + dedup + log mapping (T5), embedded lifecycle + degrade-on-bind-failure + storage dir (T6), reload hooks + Warnings + feature flag both topologies (T7), feature exposure (T8), UI types/validation (T9), editor card + live check (T10), chips + gating + reconcile read path (T11), env docs + E2E (T12). Rollback index-follow and apps.list reconcile are in T3 (usecase `RollbackApp`/`RefreshApps`); the agent side needs no change for reconcile because `model.App.Ingress` (T1) rides the existing raw-config unmarshal in `ListApps`.
- **Deliberate scope note:** ACME issuance failures surface in agent logs only (spec: v1 non-goal for UI cert status). `apps.status` warning surfacing from the spec's dedup note is covered by the save-path `Warnings` + logs; a per-status warning field was cut as YAGNI — the spec names logs + save warnings as the v1 surfaces.
- **Type consistency check:** `port.IngressManager.Reload(ctx) []string` matches `Manager.Reload` and `fakeIngress.Reload`; `NewDispatcher(orch, ingress, log)` matches both call sites and tests; `AppDomainRepository` method set matches `DbAppDomainRepository` and `fakeDomainRepo`; web `AppIngress` field names match the Go JSON tags (`upstream_port`, `ssl`, `path`, `to`, `code`).
- **Known API risk (called out in T6):** exact Caddy embedding calls (`caddy.Load` semantics, module import paths) may differ by version; the integration test pins the behavior and the task says how to adapt without changing the contract.
