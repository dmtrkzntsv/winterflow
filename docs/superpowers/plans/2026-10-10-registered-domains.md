# Registered Domains and TLS Modes Implementation Plan (PR 5)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every ingress hostname must be covered by a domain registered on its server (exact name or `*.suffix`), and the registration's TLS mode — `auto`, `certificate` or `off` — decides how the embedded Caddy serves it; registrations are managed from a new Domains tab.

**Architecture:** On the agent, a new package `internal/infra/ingress/domains` stores registrations under `{AGENT_DATA_DIR}/domains/` and checks uploaded certificates; the Caddy manager reads them in `BuildConfig` (routes placed per mode, ACME subjects only for `auto`, `tls.certificates.load_files` for `certificate`, a catch-all 404 per registration), seeds them once from existing apps (upgrade path), and serves `domain.list|create|delete` through the dispatcher. On the API, `app_domains` becomes unique per `(server_id, domain)`, a new `server_domains` index mirrors each agent's registrations (from command results and a `domain.list` reconcile), save-app enforces coverage and per-server uniqueness, and three agent-bound routes join the table while `GET /api/v1/domains/check` gains `server_id`. On the web, Server Settings gets a Domains tab and the ingress editor gets suggestions, inline coverage errors and read-only mode badges.

**Tech Stack:** Go 1.25 (chi, bun over SQLite/Postgres, embedded Caddy v2.11 JSON config, `crypto/tls` + `crypto/x509`), React 19 + Vite + Vitest + Testing Library, vendored shadcn/ui (Radix), pnpm.

**Spec:** `docs/superpowers/specs/2026-10-10-registered-domains-design.md` (today at `/tmp/wf-ui/2026-10-10-registered-domains-design.md`; it is committed to that path before execution). Read it with this plan.

**Base:** branch `feat/remove-roles` (PR 4, HEAD `e6b9b64`). Create `feat/registered-domains` from it. PR 4 removed roles: `api.Guards` has only `AuthLimit`, there is no admin gating, and every server-addressed route only checks org ownership. Every diff below is against that base plus the earlier tasks (the code was built and the full suite run on a copy of `e6b9b64`); `New:` files are given whole. If another PR moved a hunk's context, apply the change by hand — the intent is in the step text.

## Global Constraints

- Registration name: an exact lowercase RFC-1123 hostname (same rule as ingress hostnames), or `*.` + one; the `*` is the whole leftmost label; at most 253 characters (`model.ValidateDomainName`, mirrored by `domainNameError` in `web/src/lib/domains.ts`).
- TLS modes are exactly `auto` | `certificate` | `off` (`command.DomainTLS`).
- Coverage: an exact name covers only itself; `*.s` covers `<one label>.s` — not `s`, not `a.b.s`; an exact registration wins over a wildcard (`model.WinningDomain`). Comparison is case-insensitive.
- Command types are `domain.list`, `domain.create`, `domain.delete`; every request with a name has a `Validate()` that the dispatcher's generic `handle` runs; one dispatcher registration line per type.
- Agent storage: `{AGENT_DATA_DIR}/domains/<file-safe name>.json` (0644, `{name, tls}`), plus `.crt` (0644) and `.key` (0600) in certificate mode; `*` is spelled `_wildcard_` in file names.
- The certificate key arrives ECIES-encrypted (browser → server public key, as for registry passwords), is decrypted only on the agent with its private key, and never appears in any response, log or the API database.
- Certificate checks at create time: `tls.X509KeyPair` must succeed; the leaf must cover the name (same wildcard SAN for a wildcard name; exact or covering wildcard SAN for an exact name); the leaf must not be expired.
- `domain.create` on an existing name replaces it (certificate renewal). `domain.delete` is refused while an app uses a hostname the registration wins; the error names the apps.
- `INGRESS_ENABLED=false`: `domain.create`/`domain.delete` fail with `ingress is disabled on this server`; `domain.list` returns the stored list.
- Upgrade path: on the first start without `domains/`, register every hostname existing apps use as an exact registration (`ssl: true` → `auto`, else `off`), once, logged.
- A hostname is unique per server (`app_domains` primary key `(server_id, domain)` on SQLite and Postgres); the same registration name may exist on any number of servers.
- The ingress `ssl` field is ignored by the proxy from now on; old configs keep loading.
- Caddy: the rate-limit route stays first on both servers; read-header/idle timeouts unchanged; reloads after `app.save`, `app.delete`, `app.rename`, `app.rollback`, `domain.create`, `domain.delete`.
- Every server-addressed route verifies org ownership (`webutil.RequireServerAccess`); the contract matrix needs a `valid` and a `malformed` probe for each new route.
- No new env var. (Project rule: any added/renamed env var must be documented in `.env.example` in the same change — not triggered here.)
- Resource rules: nothing new is polled; domain reads happen on request; no extra process forks.
- Never delete anything under `web/src/components/ui/` (vendored shadcn).
- After `pnpm --dir web run build`, restore `web/dist/.gitkeep` (`make web-build` does it); never commit its deletion.
- Commits: conventional messages (`feat(agent): …`, `feat(api): …`, `feat(web): …`, `test(contract): …`), each ending with the line `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Coverage gates: CI requires ≥80% diff coverage per language (`make diffcov DIFFCOV_BASE=feat/remove-roles`) and the ratchet (`go 74.0`, `web 12.6`).

## Review Focus

1. An apex hostname (`twillingate.app`) used while only `*.twillingate.app` is registered: refused, and the hint says "register twillingate.app" — never "*.app" (Task 1 test `TestNotCoveredError`).
2. A certificate or key pasted with Windows CRLF line endings (copied from the Cloudflare dashboard on Windows) must validate (Task 2 test case "CRLF line endings").
3. Certificate and key pasted into each other's fields must fail with "do not match or are not PEM" and store nothing (Task 2 case "cert and key swapped"; Task 4 asserts nothing is stored on a refused create).
4. A chain pasted with the leaf first then an intermediate validates against the leaf; intermediate-first fails clearly (Task 2 cases "leaf then intermediate" / "intermediate first").
5. Renewing with a wrong certificate (`domain.create` on an existing name) must leave the working certificate in place (Task 4 test, "a failed renewal keeps the working certificate").

---

## File map

| File | Task | Change |
|---|---|---|
| `internal/domain/model/domain.go` (+ `_test`) | 1 | new: `RegisteredDomain`, `ErrDomainNotCovered`, `ValidateDomainName`, `IsWildcardDomain`, `DomainCovers`, `WinningDomain`, `NotCoveredError` |
| `internal/domain/command/domain.go` (+ `_test`), `command.go` | 1 | new: `DomainTLS`, `Domain`, List/Create/Delete req+resp with `Validate()`; 3 type constants |
| `internal/infra/ingress/domains/store.go` (+ `_test`) | 2 | new: `Store` (List/Put/Delete/Seed/Exists), `CheckCertificate`, `CertInfo` |
| `internal/infra/ingress/domains/certtest/certtest.go` | 2 | new test helper: `Issue` (throwaway CA-signed certs) |
| `internal/infra/ingress/caddy/config.go` | 3 | `BuildConfig(apps, regs, opts)`: per-mode placement, `load_files`, catch-all 404s, `skip_certificates` |
| `internal/infra/ingress/caddy/manager.go` | 3, 4 | store field, `AppIngress.Name`, `buildConfig` reads registrations; `Start` seeds |
| `internal/infra/ingress/caddy/{config_test,config_modes_test,ratelimit_test,manager_test}.go` | 3 | updated + new mode tests, real-Caddy validation and serving |
| `internal/domain/model/ingress.go` | 3 | doc: `ssl` is legacy |
| `internal/infra/ingress/caddy/domains.go` (+ `_test`) | 4 | new: `ErrIngressDisabled`, seed, `ListDomains`, `CreateDomain`, `DeleteDomain` |
| `internal/infra/ingress/domains/certtest/ecies.go` | 4 | new test helper: `AgentKey`, `Encrypt` |
| `internal/domain/port/app.go` | 5, 6, 7 | `IngressManager` domain methods; `FindClaims(serverID)`; `ServerDomainRepository` |
| `internal/app/agent/dispatcher.go` (+ tests) | 5 | three handler lines |
| `CLAUDE.md` | 5 | command surface + registered-domains paragraph |
| `internal/infra/db/migrations/20261010000002_app_domains_per_server.go` | 6 | new: PK `(server_id, domain)` (PG alter, SQLite rebuild) |
| `internal/infra/db/models/models.go` | 6, 7 | `AppDomain.ServerID` pk; `ServerDomain` |
| `internal/infra/db/repository/app_domain.go` (+ `_test`) | 6 | `FindClaims(ctx, serverID, …)` |
| `internal/domain/usecase/app/usecase.go` (+ `_test`) | 6, 7, 8 | per-server claims; `ServerDomainRepository` dep; reconcile after apps.list; coverage + `DomainCheck` |
| `internal/app/web/handler/app/{check_domain,routes,save_app,handler}.go` (+ tests) | 6, 7, 8 | check needs `server_id`; 3 routes; 400 on not covered |
| `internal/app/web/contract/*` | 6, 7, 8 | probes, per-server test, PK migration test, fake agent domain registry, domain routes, coverage 400s |
| `internal/infra/db/migrations/20261010000003_server_domains.go` | 7 | new table |
| `internal/infra/db/repository/server_domain.go` (+ `_test`) | 7 | new repo |
| `internal/infra/bootstrap/{deps,core}.go`, `internal/app/web/routes.go` | 7 | wiring |
| `internal/domain/usecase/app/domains.go` (+ `_test`), `coverage_test.go` | 7, 8 | domain usecases; coverage tests |
| `internal/app/web/handler/app/domains.go` (+ `_test`) | 7 | handlers |
| `api/openapi.yaml`, `internal/app/web/testdata/routes.golden` | 6, 7, 8 | regenerated |
| `web/src/lib/domains.ts` (+ test) | 9 | TS mirror of the coverage rule, labels, expiry grading |
| `web/src/context/use-docker.ts` (+ new test) | 9 | `listDomains`, `createDomain` (encrypts key), `deleteDomain` |
| `web/src/pages/settings-domains.tsx` (+ test), `settings.tsx` (+ new test), `src/test/setup.ts` | 9 | Domains tab |
| `web/src/components/ingress-editor.tsx` (+ new test), `app-editor.tsx` | 10 | suggestions, inline errors, mode badges |
| `docs/recipes/cloudflare-tunnel.md` | 10 | register `*.example.com` with TLS off |

## How to run things

- One Go package: `go test ./internal/infra/ingress/caddy/` — one test: add `-run TestName`.
- Full Go suite as CI runs it: `make test` (race, `-coverpkg=./...`, writes `coverage.out`).
- Contract suite on Postgres: `make test-pg` (throwaway `postgres:16` container, removed on exit; needs Docker).
- Regenerate the OpenAPI document after any route/schema change: `make openapi` (then `TestOpenAPIUpToDate` passes).
- Regenerate the routes golden after adding routes: `go test ./internal/app/web/ -run TestRoutesGolden -update`.
- Web: `pnpm --dir web run test`, `pnpm --dir web run lint`, `pnpm --dir web run build` (runs `tsc -b`), then `touch web/dist/.gitkeep`.
- Diff coverage: `make diffcov DIFFCOV_BASE=feat/remove-roles` (runs `make test` and the web coverage first).

---

### Task 1: Coverage rules and the domain command catalog

**Files:**
- Create: `internal/domain/model/domain.go`, `internal/domain/model/domain_test.go`
- Create: `internal/domain/command/domain.go`, `internal/domain/command/domain_test.go`
- Modify: `internal/domain/command/command.go` (type constants, after the volume block)

**Interfaces:**
- Consumes: `validHostname` (unexported, `internal/domain/model/ingress.go`).
- Produces:
  - `model.RegisteredDomain{Name, TLS string; NotAfter *time.Time; Issuer string}` (JSON `name`, `tls`, `not_after,omitempty`, `issuer,omitempty`)
  - `model.ErrDomainNotCovered`; `model.NotCoveredError(host string) error` (wraps it; message `"<host> is not covered by a domain registered on this server — register <host> or *.<parent>"`, the `or *.<parent>` part only when `<parent>` has a dot)
  - `model.ValidateDomainName(name string) error`, `model.IsWildcardDomain(name string) bool`, `model.DomainCovers(name, host string) bool`, `model.WinningDomain(names []string, host string) (string, bool)`
  - `command.DomainTLS` with `DomainTLSAuto|DomainTLSCertificate|DomainTLSOff` and `(DomainTLS).Valid() bool`
  - `command.Domain{Name; TLS; NotAfter *time.Time; Issuer string; Apps []string}`
  - `command.ListDomainsRequest{}`, `ListDomainsResponse{Domains []Domain}`, `CreateDomainRequest{Name; TLS; Certificate, Key string}` (+`Validate`), `CreateDomainResponse{Domain Domain}`, `DeleteDomainRequest{Name}` (+`Validate`), `DeleteDomainResponse{Name}`
  - `command.TypeDomainList = "domain.list"`, `TypeDomainCreate = "domain.create"`, `TypeDomainDelete = "domain.delete"`

- [ ] **Step 1: Write the failing model tests** — `internal/domain/model/domain_test.go`:

```go
package model

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateDomainName(t *testing.T) {
	for _, ok := range []string{"twillingate.app", "app.twillingate.app", "*.twillingate.app", "*.eu.twillingate.app", "localhost", "*.test"} {
		if err := ValidateDomainName(ok); err != nil {
			t.Errorf("%q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "*", "*.", "**.x.app", "a.*.x.app", "*x.app", "x.*", "Twillingate.app", "*.Twillingate.app",
		"-x.app", "x..app", "x.app.", "x.app:443", "https://x.app", "*.*.x.app", strings.Repeat("a", 250) + ".app"} {
		if err := ValidateDomainName(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestDomainCovers(t *testing.T) {
	cases := []struct {
		name, host string
		want       bool
	}{
		{"twillingate.app", "twillingate.app", true},
		{"twillingate.app", "a.twillingate.app", false},
		{"*.twillingate.app", "a.twillingate.app", true},
		{"*.twillingate.app", "twillingate.app", false},     // not the suffix itself
		{"*.twillingate.app", "a.b.twillingate.app", false}, // one label only
		{"*.twillingate.app", ".twillingate.app", false},    // empty label
		{"*.twillingate.app", "a.twillingate.apps", false},
		{"*.twillingate.app", "atwillingate.app", false},
		{"*.eu.twillingate.app", "x.eu.twillingate.app", true},
		{"*.Twillingate.App", "A.twillingate.app", true}, // case-insensitive
		{"TWILLINGATE.app", "twillingate.APP", true},
	}
	for _, tc := range cases {
		if got := DomainCovers(tc.name, tc.host); got != tc.want {
			t.Errorf("DomainCovers(%q, %q) = %v, want %v", tc.name, tc.host, got, tc.want)
		}
	}
}

func TestWinningDomain(t *testing.T) {
	names := []string{"*.twillingate.app", "app.twillingate.app", "twillingate.app", "*.eu.twillingate.app"}
	cases := []struct {
		host, want string
		ok         bool
	}{
		{"app.twillingate.app", "app.twillingate.app", true}, // exact wins over the wildcard
		{"x.twillingate.app", "*.twillingate.app", true},
		{"twillingate.app", "twillingate.app", true},
		{"x.eu.twillingate.app", "*.eu.twillingate.app", true},
		{"a.b.twillingate.app", "", false},
		{"example.com", "", false},
	}
	for _, tc := range cases {
		got, ok := WinningDomain(names, tc.host)
		if got != tc.want || ok != tc.ok {
			t.Errorf("WinningDomain(%q) = %q, %v; want %q, %v", tc.host, got, ok, tc.want, tc.ok)
		}
	}
	// Order does not matter: the exact name wins even listed after.
	if got, _ := WinningDomain([]string{"app.twillingate.app", "*.twillingate.app"}, "app.twillingate.app"); got != "app.twillingate.app" {
		t.Errorf("exact listed first: got %q", got)
	}
	if _, ok := WinningDomain(nil, "x.app"); ok {
		t.Error("no registrations must cover nothing")
	}
}

func TestNotCoveredError(t *testing.T) {
	err := NotCoveredError("shop.twillingate.app")
	if !errors.Is(err, ErrDomainNotCovered) {
		t.Fatalf("not wrapped: %v", err)
	}
	want := "shop.twillingate.app is not covered by a domain registered on this server — register shop.twillingate.app or *.twillingate.app"
	if err.Error() != want {
		t.Fatalf("message = %q", err)
	}
	for _, host := range []string{"localhost", "twillingate.app"} {
		// An apex never falls under *.<tld>: suggest the apex alone.
		if got := NotCoveredError(host).Error(); !strings.HasSuffix(got, "register "+host) {
			t.Fatalf("%s hint = %q", host, got)
		}
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/domain/model/`
Expected: FAIL to compile — `undefined: ValidateDomainName` (and `DomainCovers`, `WinningDomain`, `NotCoveredError`, `ErrDomainNotCovered`).

- [ ] **Step 3: Implement the rules** — `internal/domain/model/domain.go`:

```go
package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrDomainNotCovered marks a save whose ingress names a hostname no domain
// registered on the target server covers; the HTTP layer maps it to 400.
var ErrDomainNotCovered = errors.New("domain not covered")

// RegisteredDomain is one row of the API's server_domains index: a name an
// agent accepts ingress hostnames under, with its TLS mode ("auto",
// "certificate" or "off"). The agent's domains/ dir is the source of truth.
type RegisteredDomain struct {
	Name     string     `json:"name"`
	TLS      string     `json:"tls"`
	NotAfter *time.Time `json:"not_after,omitempty"`
	Issuer   string     `json:"issuer,omitempty"`
}

// IsWildcardDomain reports whether a registration name is a wildcard: "*."
// followed by the suffix it covers one label of.
func IsWildcardDomain(name string) bool { return strings.HasPrefix(name, "*.") }

// ValidateDomainName checks a registration name: an exact lowercase RFC-1123
// hostname (the ingress rule), or "*." plus one, where the "*" is the whole
// leftmost label.
func ValidateDomainName(name string) error {
	host := strings.TrimPrefix(name, "*.")
	if len(name) > 253 || !validHostname(host) {
		return fmt.Errorf("%q is not a valid domain: use a lowercase hostname like example.com, or *.example.com for every subdomain", name)
	}
	return nil
}

// DomainCovers reports whether the registration name covers host. An exact
// name covers only itself; "*.s" covers "<one label>.s" — not "s" itself and
// not "a.b.s". Comparison ignores case.
func DomainCovers(name, host string) bool {
	name, host = strings.ToLower(name), strings.ToLower(host)
	if !IsWildcardDomain(name) {
		return name == host
	}
	label, suffix, ok := strings.Cut(host, ".")
	return ok && label != "" && suffix == name[2:]
}

// WinningDomain returns the registration among names that covers host: an
// exact name wins over a wildcard. Only "*.<parent of host>" can be the
// wildcard, so two wildcards never compete for one host.
func WinningDomain(names []string, host string) (string, bool) {
	var wildcard string
	for _, n := range names {
		if !DomainCovers(n, host) {
			continue
		}
		if !IsWildcardDomain(n) {
			return n, true
		}
		wildcard = n
	}
	return wildcard, wildcard != ""
}

// notCoveredError is ErrDomainNotCovered with a message that stands on its
// own (the UI shows it as is).
type notCoveredError struct{ msg string }

func (e notCoveredError) Error() string { return e.msg }
func (e notCoveredError) Unwrap() error { return ErrDomainNotCovered }

// NotCoveredError explains how to cover host: register it, or the wildcard
// for its parent — unless that parent is a bare TLD (an apex like
// twillingate.app is never covered by *.twillingate.app, and *.app is no
// advice).
func NotCoveredError(host string) error {
	hint := host
	if _, parent, ok := strings.Cut(host, "."); ok && strings.Contains(parent, ".") {
		hint = host + " or *." + parent
	}
	return notCoveredError{fmt.Sprintf("%s is not covered by a domain registered on this server — register %s", host, hint)}
}
```

- [ ] **Step 4: Run the model tests**

Run: `go test ./internal/domain/model/`
Expected: PASS.

- [ ] **Step 5: Write the failing command tests** — `internal/domain/command/domain_test.go`:

```go
package command

import (
	"strings"
	"testing"
)

func TestCreateDomainRequestValidate(t *testing.T) {
	ok := []CreateDomainRequest{
		{Name: "twillingate.app", TLS: DomainTLSAuto},
		{Name: "*.twillingate.app", TLS: DomainTLSOff},
		{Name: "*.twillingate.app", TLS: DomainTLSCertificate, Certificate: "PEM", Key: "CIPHERTEXT"},
	}
	for _, r := range ok {
		if err := r.Validate(); err != nil {
			t.Errorf("%+v should be valid: %v", r, err)
		}
	}
	bad := map[string]CreateDomainRequest{
		"bad name":          {Name: "*.*.x.app", TLS: DomainTLSAuto},
		"uppercase":         {Name: "X.app", TLS: DomainTLSAuto},
		"no mode":           {Name: "x.app"},
		"unknown mode":      {Name: "x.app", TLS: "manual"},
		"cert without key":  {Name: "x.app", TLS: DomainTLSCertificate, Certificate: "PEM"},
		"key without cert":  {Name: "x.app", TLS: DomainTLSCertificate, Key: "C"},
		"cert in auto mode": {Name: "x.app", TLS: DomainTLSAuto, Certificate: "PEM", Key: "C"},
		"key in off mode":   {Name: "x.app", TLS: DomainTLSOff, Key: "C"},
	}
	for name, r := range bad {
		if err := r.Validate(); err == nil {
			t.Errorf("%s: %+v should be rejected", name, r)
		}
	}
	err := CreateDomainRequest{Name: "x.app", TLS: "manual"}.Validate()
	if err == nil || !strings.Contains(err.Error(), "auto, certificate or off") {
		t.Errorf("mode error = %v", err)
	}
}

func TestDeleteDomainRequestValidate(t *testing.T) {
	if err := (DeleteDomainRequest{Name: "*.x.app"}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"", "../x", "x/y", "*"} {
		if err := (DeleteDomainRequest{Name: n}).Validate(); err == nil {
			t.Errorf("%q should be rejected", n)
		}
	}
}
```

Run: `go test ./internal/domain/command/`
Expected: FAIL to compile — `undefined: CreateDomainRequest`, `DomainTLSAuto`, `DeleteDomainRequest`.

- [ ] **Step 6: Add the catalog** — `internal/domain/command/domain.go`:

```go
package command

import (
	"errors"
	"fmt"
	"time"

	"winterflow/internal/domain/model"
)

// Domain registrations: the names an agent's ingress accepts hostnames under,
// each with a TLS mode. The agent's {AGENT_DATA_DIR}/domains/ is the source of
// truth; the API mirrors it into its server_domains index.

// DomainTLS is how TLS works for hostnames under a registration.
type DomainTLS string

const (
	// DomainTLSAuto: the agent obtains one ACME (HTTP-01) cert per app host.
	DomainTLSAuto DomainTLS = "auto"
	// DomainTLSCertificate: an uploaded PEM cert + key serves every host.
	DomainTLSCertificate DomainTLS = "certificate"
	// DomainTLSOff: TLS ends in front (tunnel, proxy); plain HTTP only.
	DomainTLSOff DomainTLS = "off"
)

// Valid reports whether t is one of the three modes.
func (t DomainTLS) Valid() bool {
	return t == DomainTLSAuto || t == DomainTLSCertificate || t == DomainTLSOff
}

// Domain is one registration as the agent reports it. The key never leaves
// the agent; NotAfter and Issuer describe the stored certificate.
type Domain struct {
	Name     string     `json:"name"`
	TLS      DomainTLS  `json:"tls"`
	NotAfter *time.Time `json:"not_after,omitempty"` // certificate mode
	Issuer   string     `json:"issuer,omitempty"`    // certificate mode, CN of the issuer
	Apps     []string   `json:"apps"`                // app ids using a covered host on this agent
}

type ListDomainsRequest struct{}

type ListDomainsResponse struct {
	Domains []Domain `json:"domains"`
}

// CreateDomainRequest registers (or replaces) a name. In certificate mode
// Certificate is the PEM chain and Key the ECIES ciphertext of the PEM key,
// encrypted in the browser with the agent's public key.
type CreateDomainRequest struct {
	Name        string    `json:"name"`
	TLS         DomainTLS `json:"tls"`
	Certificate string    `json:"certificate,omitempty"`
	Key         string    `json:"key,omitempty"`
}

// Validate checks the name and mode, and that Certificate and Key are present
// exactly when the mode is certificate.
func (r CreateDomainRequest) Validate() error {
	if err := model.ValidateDomainName(r.Name); err != nil {
		return err
	}
	if !r.TLS.Valid() {
		return fmt.Errorf("tls must be auto, certificate or off, got %q", r.TLS)
	}
	if r.TLS == DomainTLSCertificate {
		if r.Certificate == "" || r.Key == "" {
			return errors.New("certificate mode needs a certificate and a key")
		}
	} else if r.Certificate != "" || r.Key != "" {
		return fmt.Errorf("a certificate and key are only accepted in certificate mode, not %q", r.TLS)
	}
	return nil
}

type CreateDomainResponse struct {
	Domain Domain `json:"domain"`
}

type DeleteDomainRequest struct {
	Name string `json:"name"`
}

func (r DeleteDomainRequest) Validate() error { return model.ValidateDomainName(r.Name) }

type DeleteDomainResponse struct {
	Name string `json:"name"`
}
```

And the type constants in `internal/domain/command/command.go`:

```diff
diff --git a/internal/domain/command/command.go b/internal/domain/command/command.go
--- a/internal/domain/command/command.go
+++ b/internal/domain/command/command.go
@@ -49,6 +49,11 @@ const (
 	TypeVolumeCreate Type = "volume.create"
 	TypeVolumeDelete Type = "volume.delete"
 
+	// Domain registrations (ingress names + TLS mode).
+	TypeDomainList   Type = "domain.list"
+	TypeDomainCreate Type = "domain.create"
+	TypeDomainDelete Type = "domain.delete"
+
 	// Container images.
 	TypeImageTags Type = "image.tags" // list available registry tags for an image
 
```

- [ ] **Step 7: Run the domain-layer tests**

Run: `go test ./internal/domain/...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/domain/model/domain.go internal/domain/model/domain_test.go \
  internal/domain/command/domain.go internal/domain/command/domain_test.go internal/domain/command/command.go
git commit -m "feat(domain): registered domain names, coverage rule and domain commands

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Agent domain store and certificate checks

**Files:**
- Create: `internal/infra/ingress/domains/store.go`
- Create: `internal/infra/ingress/domains/store_test.go`
- Create: `internal/infra/ingress/domains/certtest/certtest.go` (imported by `_test` files only)

**Interfaces:**
- Consumes: `model.ValidateDomainName`, `model.IsWildcardDomain`, `model.DomainCovers`, `command.DomainTLS*` (Task 1).
- Produces:
  - `domains.Registration{Name string; TLS command.DomainTLS; CertFile, KeyFile string}` (`CertFile`/`KeyFile` set for certificate mode only, `json:"-"`)
  - `domains.NewStore(dir string) *Store`; `(*Store).Dir() string`, `Exists() bool`, `List() ([]Registration, error)` (sorted by name, broken files skipped), `Put(name string, mode command.DomainTLS, certPEM, keyPEM []byte) error`, `Delete(name string) (existed bool, err error)`, `Seed(regs []Registration) (seeded bool, err error)`
  - `domains.CheckCertificate(name string, certPEM, keyPEM []byte, now time.Time) (*x509.Certificate, error)`
  - `domains.CertInfo(certFile string) (notAfter time.Time, issuerCN string, err error)`
  - `certtest.Issuer` (const CN) and `certtest.Issue(t testing.TB, notAfter time.Time, dnsNames ...string) (certPEM, keyPEM []byte)`

- [ ] **Step 1: Add the test-certificate helper** — `internal/infra/ingress/domains/certtest/certtest.go`:

```go
// Package certtest issues throwaway certificates for tests of the domain
// registrations (certificate mode). Only _test files import it.
package certtest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

// Issuer is the CN every certificate from Issue names as its issuer.
const Issuer = "Winterflow Test Origin CA"

// Issue returns a PEM certificate for dnsNames, valid from an hour ago until
// notAfter and signed by a fresh CA named Issuer, plus its PEM (PKCS#8) key.
func Issue(t testing.TB, notAfter time.Time, dnsNames ...string) (certPEM, keyPEM []byte) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: Issuer},
		NotBefore:             time.Now().Add(-2 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: firstOr(dnsNames, "leaf")},
		DNSNames:     dnsNames,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

func firstOr(s []string, def string) string {
	if len(s) == 0 {
		return def
	}
	return s[0]
}
```

- [ ] **Step 2: Write the failing store tests** — `internal/infra/ingress/domains/store_test.go` (the CRLF, swapped-fields and chain-order cases are the Review Focus items 2–4):

```go
package domains

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/ingress/domains/certtest"
)

func TestStorePutListDelete(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "domains")
	s := NewStore(dir)
	if s.Exists() {
		t.Fatal("fresh store must not exist yet")
	}
	if regs, err := s.List(); err != nil || regs != nil {
		t.Fatalf("missing dir: %v, %v", regs, err)
	}

	cert, key := certtest.Issue(t, time.Now().Add(24*time.Hour), "*.twillingate.app")
	if err := s.Put("*.twillingate.app", command.DomainTLSCertificate, cert, key); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("twillingate.app", command.DomainTLSAuto, nil, nil); err != nil {
		t.Fatal(err)
	}

	// Wildcards are spelled _wildcard_ on disk; the key is private.
	for file, perm := range map[string]os.FileMode{
		"_wildcard_.twillingate.app.json": 0o644,
		"_wildcard_.twillingate.app.crt":  0o644,
		"_wildcard_.twillingate.app.key":  0o600,
		"twillingate.app.json":            0o644,
	} {
		fi, err := os.Stat(filepath.Join(dir, file))
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if fi.Mode().Perm() != perm {
			t.Errorf("%s mode = %v, want %v", file, fi.Mode().Perm(), perm)
		}
	}

	regs, err := s.List()
	if err != nil || len(regs) != 2 {
		t.Fatalf("List = %+v, %v", regs, err)
	}
	if regs[0].Name != "*.twillingate.app" || regs[0].TLS != command.DomainTLSCertificate ||
		regs[0].CertFile != filepath.Join(dir, "_wildcard_.twillingate.app.crt") ||
		regs[0].KeyFile != filepath.Join(dir, "_wildcard_.twillingate.app.key") {
		t.Errorf("certificate registration = %+v", regs[0])
	}
	if regs[1].Name != "twillingate.app" || regs[1].TLS != command.DomainTLSAuto || regs[1].CertFile != "" {
		t.Errorf("auto registration = %+v", regs[1])
	}

	// Replacing with another mode drops the certificate files.
	if err := s.Put("*.twillingate.app", command.DomainTLSOff, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "_wildcard_.twillingate.app.key")); !os.IsNotExist(err) {
		t.Error("key survived the switch to off")
	}

	existed, err := s.Delete("*.twillingate.app")
	if err != nil || !existed {
		t.Fatalf("Delete = %v, %v", existed, err)
	}
	if existed, _ := s.Delete("*.twillingate.app"); existed {
		t.Error("second delete reported the registration as existing")
	}
	if regs, _ := s.List(); len(regs) != 1 {
		t.Fatalf("after delete: %+v", regs)
	}
}

func TestStoreListSkipsBrokenFiles(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Put("ok.app", command.DomainTLSOff, nil, nil); err != nil {
		t.Fatal(err)
	}
	for file, body := range map[string]string{
		"garbage.app.json": "{",
		"renamed.app.json": `{"name":"other.app","tls":"off"}`,
		"mode.app.json":    `{"name":"mode.app","tls":"manual"}`,
		"BAD.app.json":     `{"name":"BAD.app","tls":"off"}`,
		"notes.txt":        "x",
	} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	regs, err := s.List()
	if err != nil || len(regs) != 1 || regs[0].Name != "ok.app" {
		t.Fatalf("List = %+v, %v", regs, err)
	}
}

func TestStoreSeedRunsOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "domains")
	s := NewStore(dir)
	seeded, err := s.Seed([]Registration{{Name: "a.app", TLS: command.DomainTLSAuto}, {Name: "b.app", TLS: command.DomainTLSOff}})
	if err != nil || !seeded {
		t.Fatalf("first seed = %v, %v", seeded, err)
	}
	if regs, _ := s.List(); len(regs) != 2 {
		t.Fatalf("seeded = %+v", regs)
	}
	if _, err := os.Stat(dir + ".seed"); !os.IsNotExist(err) {
		t.Error("staging dir left behind")
	}
	seeded, err = s.Seed([]Registration{{Name: "c.app", TLS: command.DomainTLSAuto}})
	if err != nil || seeded {
		t.Fatalf("second seed = %v, %v", seeded, err)
	}
	if regs, _ := s.List(); len(regs) != 2 {
		t.Fatalf("second seed changed the store: %+v", regs)
	}
	// An empty install still gets the directory, so it never seeds again.
	empty := NewStore(filepath.Join(t.TempDir(), "domains"))
	if seeded, err := empty.Seed(nil); err != nil || !seeded || !empty.Exists() {
		t.Fatalf("empty seed = %v, %v", seeded, err)
	}
}

func TestCheckCertificate(t *testing.T) {
	now := time.Now()
	future := now.Add(90 * 24 * time.Hour)
	wildCert, wildKey := certtest.Issue(t, future, "*.twillingate.app", "twillingate.app")
	exactCert, exactKey := certtest.Issue(t, future, "app.twillingate.app")
	_, otherKey := certtest.Issue(t, future, "*.twillingate.app")
	oldCert, oldKey := certtest.Issue(t, now.Add(-time.Hour), "*.twillingate.app")
	interCert, _ := certtest.Issue(t, future, "intermediate.example")
	crlf := func(b []byte) []byte { return []byte(strings.ReplaceAll(string(b), "\n", "\r\n")) }
	concat := func(a, b []byte) []byte { return append(append([]byte{}, a...), b...) }

	type pair struct {
		label, name string
		cert, key   []byte
	}
	ok := []pair{
		{"wildcard cert, wildcard name", "*.twillingate.app", wildCert, wildKey},
		{"exact name under a wildcard cert", "app.twillingate.app", wildCert, wildKey},
		{"exact SAN", "twillingate.app", wildCert, wildKey},
		{"exact cert", "app.twillingate.app", exactCert, exactKey},
		// Review focus: PEM copied on Windows, and a chain with the leaf
		// first followed by an intermediate.
		{"CRLF line endings", "*.twillingate.app", crlf(wildCert), crlf(wildKey)},
		{"leaf then intermediate", "*.twillingate.app", concat(wildCert, interCert), wildKey},
	}
	for _, tc := range ok {
		leaf, err := CheckCertificate(tc.name, tc.cert, tc.key, now)
		if err != nil {
			t.Errorf("%s: %v", tc.label, err)
			continue
		}
		if leaf.Issuer.CommonName != certtest.Issuer {
			t.Errorf("%s: issuer = %q", tc.label, leaf.Issuer.CommonName)
		}
	}

	bad := []struct {
		pair
		want string
	}{
		{pair{"mismatched key", "*.twillingate.app", wildCert, otherKey}, "do not match"},
		{pair{"not PEM", "*.twillingate.app", []byte("nope"), wildKey}, "do not match"},
		{pair{"SAN not covering", "shop.example.com", wildCert, wildKey}, "does not cover shop.example.com"},
		{pair{"wildcard name, exact cert", "*.twillingate.app", exactCert, exactKey}, "does not cover *.twillingate.app (it covers app.twillingate.app)"},
		{pair{"two labels deep", "a.b.twillingate.app", wildCert, wildKey}, "does not cover"},
		{pair{"expired", "*.twillingate.app", oldCert, oldKey}, "expired on"},
		// Review focus: fields pasted the wrong way round, and a chain
		// whose leaf is not first.
		{pair{"cert and key swapped", "*.twillingate.app", wildKey, wildCert}, "do not match or are not PEM"},
		{pair{"intermediate first", "*.twillingate.app", concat(interCert, wildCert), wildKey}, "do not match"},
	}
	for _, tc := range bad {
		_, err := CheckCertificate(tc.name, tc.cert, tc.key, now)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want it to contain %q", tc.label, err, tc.want)
		}
	}
}

func TestCertInfo(t *testing.T) {
	dir := t.TempDir()
	notAfter := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	cert, _ := certtest.Issue(t, notAfter, "x.app")
	path := filepath.Join(dir, "x.crt")
	if err := os.WriteFile(path, cert, 0o644); err != nil {
		t.Fatal(err)
	}
	got, issuer, err := CertInfo(path)
	if err != nil || !got.Equal(notAfter) || issuer != certtest.Issuer {
		t.Fatalf("CertInfo = %v, %q, %v", got, issuer, err)
	}
	if err := os.WriteFile(path, []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CertInfo(path); err == nil {
		t.Error("junk must not parse")
	}
	if _, _, err := CertInfo(filepath.Join(dir, "missing.crt")); err == nil {
		t.Error("missing file must error")
	}
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `go test ./internal/infra/ingress/domains/`
Expected: FAIL to compile — `undefined: NewStore`, `CheckCertificate`, `CertInfo`, `Registration`.

- [ ] **Step 4: Implement the store** — `internal/infra/ingress/domains/store.go`:

```go
// Package domains stores the agent's domain registrations under
// {AGENT_DATA_DIR}/domains/: one <name>.json per registration ({name, tls}),
// plus <name>.crt (0644) and <name>.key (0600) in certificate mode. A
// wildcard's "*" is spelled _wildcard_ in file names. The directory is the
// source of truth the API's server_domains index mirrors.
package domains

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

// Registration is one stored registration. CertFile and KeyFile are set in
// certificate mode only.
type Registration struct {
	Name     string            `json:"name"`
	TLS      command.DomainTLS `json:"tls"`
	CertFile string            `json:"-"`
	KeyFile  string            `json:"-"`
}

// Store reads and writes the domains/ directory. Methods are safe for
// concurrent use.
type Store struct {
	dir string
	mu  sync.Mutex
}

func NewStore(dir string) *Store { return &Store{dir: dir} }

// Dir is the directory the store owns.
func (s *Store) Dir() string { return s.dir }

const wildcardToken = "_wildcard_"

// fileStem maps a validated name to its file stem ("*.x.app" ->
// "_wildcard_.x.app"); nameOf is the inverse.
func fileStem(name string) string { return strings.Replace(name, "*", wildcardToken, 1) }
func nameOf(stem string) string   { return strings.Replace(stem, wildcardToken, "*", 1) }

func (s *Store) path(name, ext string) string {
	return filepath.Join(s.dir, fileStem(name)+ext)
}

// Exists reports whether the domains/ directory exists (the upgrade path
// seeds it once, on the first start without it).
func (s *Store) Exists() bool {
	_, err := os.Stat(s.dir)
	return err == nil
}

// List returns every registration, sorted by name. A file that does not
// parse, names something else than its file name, or carries an unknown
// mode is skipped: its hosts then fall out of ingress (fail closed) instead
// of one broken file stopping all routing.
func (s *Store) List() ([]Registration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read domains: %w", err)
	}
	var out []Registration
	for _, e := range entries {
		stem, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var r Registration
		if json.Unmarshal(raw, &r) != nil || r.Name != nameOf(stem) ||
			model.ValidateDomainName(r.Name) != nil || !r.TLS.Valid() {
			continue
		}
		if r.TLS == command.DomainTLSCertificate {
			r.CertFile, r.KeyFile = s.path(r.Name, ".crt"), s.path(r.Name, ".key")
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Put writes (or replaces) a registration. In certificate mode certPEM and
// keyPEM are written next to it; switching away from certificate mode removes
// them. The .json is written last so a crash never leaves a registration
// pointing at missing files.
func (s *Store) Put(name string, mode command.DomainTLS, certPEM, keyPEM []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create domains dir: %w", err)
	}
	if mode == command.DomainTLSCertificate {
		if err := writeAtomic(s.path(name, ".crt"), certPEM, 0o644); err != nil {
			return err
		}
		if err := writeAtomic(s.path(name, ".key"), keyPEM, 0o600); err != nil {
			return err
		}
	}
	raw, _ := json.Marshal(Registration{Name: name, TLS: mode})
	if err := writeAtomic(s.path(name, ".json"), raw, 0o644); err != nil {
		return err
	}
	if mode != command.DomainTLSCertificate {
		_ = os.Remove(s.path(name, ".crt"))
		_ = os.Remove(s.path(name, ".key"))
	}
	return nil
}

// Delete removes a registration and its certificate files. It reports
// whether the registration existed.
func (s *Store) Delete(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.path(name, ".json"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("delete domain: %w", err)
	}
	_ = os.Remove(s.path(name, ".crt"))
	_ = os.Remove(s.path(name, ".key"))
	return true, nil
}

// Seed creates the domains/ directory holding regs, unless it already
// exists; it reports whether it did. The files are written to a sibling
// directory renamed into place, so a crash mid-way leaves no half-seeded
// directory that would stop the next start from seeding.
func (s *Store) Seed(regs []Registration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.dir); err == nil {
		return false, nil
	}
	tmp := s.dir + ".seed"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return false, fmt.Errorf("seed domains: %w", err)
	}
	for _, r := range regs {
		raw, _ := json.Marshal(Registration{Name: r.Name, TLS: r.TLS})
		if err := os.WriteFile(filepath.Join(tmp, fileStem(r.Name)+".json"), raw, 0o644); err != nil {
			return false, fmt.Errorf("seed domains: %w", err)
		}
	}
	if err := os.Rename(tmp, s.dir); err != nil {
		return false, fmt.Errorf("seed domains: %w", err)
	}
	return true, nil
}

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// CheckCertificate validates an uploaded pair for name at now: the key must
// match the certificate, the leaf must cover name (the same wildcard SAN for
// a wildcard name; the exact name or a wildcard SAN covering it for an exact
// name), and the leaf must not be expired. It returns the parsed leaf.
func CheckCertificate(name string, certPEM, keyPEM []byte, now time.Time) (*x509.Certificate, error) {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("the certificate and key do not match or are not PEM: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}
	if !leafCovers(leaf, name) {
		sans := strings.Join(leaf.DNSNames, ", ")
		if sans == "" {
			sans = "no DNS names"
		}
		return nil, fmt.Errorf("the certificate does not cover %s (it covers %s)", name, sans)
	}
	if now.After(leaf.NotAfter) {
		return nil, fmt.Errorf("the certificate expired on %s", leaf.NotAfter.UTC().Format(time.DateOnly))
	}
	return leaf, nil
}

func leafCovers(leaf *x509.Certificate, name string) bool {
	for _, san := range leaf.DNSNames {
		san = strings.ToLower(san)
		if san == name {
			return true
		}
		if !model.IsWildcardDomain(name) && model.IsWildcardDomain(san) && model.DomainCovers(san, name) {
			return true
		}
	}
	return false
}

// CertInfo reads the leaf of a stored PEM certificate: its expiry and its
// issuer's common name.
func CertInfo(certFile string) (time.Time, string, error) {
	raw, err := os.ReadFile(certFile)
	if err != nil {
		return time.Time{}, "", err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return time.Time{}, "", errors.New("no PEM certificate")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, "", err
	}
	return leaf.NotAfter, leaf.Issuer.CommonName, nil
}
```

- [ ] **Step 5: Run the store tests**

Run: `go test -race ./internal/infra/ingress/domains/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/infra/ingress/domains
git commit -m "feat(agent): store domain registrations and check uploaded certificates

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Caddy config per TLS mode

`BuildConfig` takes the registrations. Per app hostname it finds the winning registration; none → the host is excluded with a warning (fail closed). `auto`/`certificate` hosts go on the HTTPS server, `off` hosts on the HTTP server; only `auto` hosts are ACME subjects. Each `certificate` registration adds a `tls.certificates.load_files` pair — Caddy's automatic HTTPS skips names a loaded certificate covers (verified against real Caddy below). After all app routes, every registration gets a catch-all 404 on its mode's server; a wildcard's catch-all excludes (with a `not` matcher) the exact registrations under it, which win their host and may sit on the other server. An `auto` catch-all name that no app route serves is listed in the HTTPS server's `automatic_https.skip_certificates`, so Caddy never tries to order a wildcard certificate over HTTP-01 (impossible) or a certificate for an unused name — endless ACME retries are idle CPU on the target hardware. The throttle stays the first route on both servers.

**Files:**
- Modify: `internal/infra/ingress/caddy/config.go` (full new content below)
- Modify: `internal/infra/ingress/caddy/manager.go` (store field, app name, `buildConfig`)
- Modify: `internal/domain/model/ingress.go` (doc comments: `ssl` is legacy)
- Modify: `internal/infra/ingress/caddy/config_test.go`, `ratelimit_test.go`, `manager_test.go`
- Create: `internal/infra/ingress/caddy/config_modes_test.go`

**Interfaces:**
- Consumes: `domains.Registration`, `domains.NewStore`, `(*Store).List`, `(*Store).Put` (Task 2); `model.WinningDomain`, `model.IsWildcardDomain`, `model.DomainCovers`, `command.DomainTLS*` (Task 1); `certtest.Issue` (Task 2).
- Produces:
  - `caddy.BuildConfig(apps []AppIngress, regs []domains.Registration, opts Options) ([]byte, []string, error)`
  - `caddy.AppIngress{AppID, Name string; Ingress model.Ingress}` (`Name` is the app's display name from `config.json`)
  - `(*Manager).domains *domains.Store` rooted at `{AGENT_DATA_DIR}/domains` (used by Task 4)

- [ ] **Step 1: Write the failing mode tests** — `internal/infra/ingress/caddy/config_modes_test.go`:

```go
package caddy

import (
	"encoding/json"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/ingress/domains"
)

// view is the slice of the emitted config the mode tests read.
type view struct {
	Apps struct {
		HTTP struct {
			Servers map[string]struct {
				Routes []struct {
					Match []struct {
						Host []string `json:"host"`
						Not  []struct {
							Host []string `json:"host"`
						} `json:"not"`
					} `json:"match"`
					Handle []map[string]any `json:"handle"`
				} `json:"routes"`
				AutomaticHTTPS *struct {
					SkipCertificates []string `json:"skip_certificates"`
				} `json:"automatic_https"`
			} `json:"servers"`
		} `json:"http"`
		TLS *struct {
			Automation *struct {
				Policies []struct {
					Subjects []string `json:"subjects"`
				} `json:"policies"`
			} `json:"automation"`
			Certificates *struct {
				LoadFiles []struct {
					Certificate string `json:"certificate"`
					Key         string `json:"key"`
				} `json:"load_files"`
			} `json:"certificates"`
		} `json:"tls"`
	} `json:"apps"`
}

func decodeView(t *testing.T, raw []byte) view {
	t.Helper()
	var v view
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	return v
}

// routeKinds lists a server's routes as "<handler> <hosts>" in order, e.g.
// "reverse_proxy a.test" or "404 *.x.test !y.x.test".
func routeKinds(v view, server string) []string {
	var out []string
	for _, rt := range v.Apps.HTTP.Servers[server].Routes {
		h := rt.Handle[0]
		kind, _ := h["handler"].(string)
		if kind == "static_response" && h["status_code"] == "404" {
			kind = "404"
		}
		var hosts []string
		for _, m := range rt.Match {
			hosts = append(hosts, m.Host...)
			for _, n := range m.Not {
				for _, x := range n.Host {
					hosts = append(hosts, "!"+x)
				}
			}
		}
		out = append(out, strings.TrimSpace(kind+" "+strings.Join(hosts, " ")))
	}
	return out
}

func modeRegs() []domains.Registration {
	return []domains.Registration{
		{Name: "*.cert.test", TLS: command.DomainTLSCertificate, CertFile: "/d/_wildcard_.cert.test.crt", KeyFile: "/d/_wildcard_.cert.test.key"},
		{Name: "x.cert.test", TLS: command.DomainTLSOff}, // exact wins over the wildcard
		{Name: "auto.test", TLS: command.DomainTLSAuto},
		{Name: "*.auto.test", TLS: command.DomainTLSAuto}, // no app uses it
		{Name: "*.tunnel.test", TLS: command.DomainTLSOff},
	}
}

func modeApps() []AppIngress {
	return []AppIngress{{AppID: "app1", Ingress: model.Ingress{
		Domains: []model.IngressDomain{
			{Domain: "a.cert.test", UpstreamPort: 8081, SSL: false}, // ssl is ignored now
			{Domain: "auto.test", UpstreamPort: 8082},
			{Domain: "b.tunnel.test", UpstreamPort: 8083, SSL: true},
			{Domain: "x.cert.test", UpstreamPort: 8084},
			{Domain: "uncovered.example", UpstreamPort: 8085},
		},
		Redirects: []model.IngressRedirect{
			{Domain: "old.tunnel.test", To: "https://auto.test", Code: 301, SSL: true},
			{Domain: "auto.test", Path: "/legacy/*", To: "https://auto.test/new", Code: 302},
		},
	}}}
}

func TestBuildConfigPlacesHostsByRegistrationMode(t *testing.T) {
	raw, warnings, err := BuildConfig(modeApps(), modeRegs(), Options{RateLimitRPS: 50, RateLimitBurst: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "uncovered.example is not covered") {
		t.Fatalf("warnings = %v, want only the uncovered host", warnings)
	}
	if strings.Contains(string(raw), "uncovered.example") {
		t.Error("uncovered host leaked into the config")
	}
	v := decodeView(t, raw)

	wantHTTPS := []string{
		"winterflow_rate_limit",
		"static_response auto.test", // path rule before its domain's route
		"reverse_proxy a.cert.test",
		"reverse_proxy auto.test",
		"404 *.cert.test !x.cert.test",
		"404 auto.test",
		"404 *.auto.test",
	}
	wantHTTP := []string{
		"winterflow_rate_limit",
		"reverse_proxy b.tunnel.test",
		"reverse_proxy x.cert.test",
		"static_response old.tunnel.test",
		"404 x.cert.test",
		"404 *.tunnel.test",
	}
	if got := routeKinds(v, "https"); strings.Join(got, "\n") != strings.Join(wantHTTPS, "\n") {
		t.Errorf("https routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(wantHTTPS, "\n"))
	}
	if got := routeKinds(v, "http"); strings.Join(got, "\n") != strings.Join(wantHTTP, "\n") {
		t.Errorf("http routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(wantHTTP, "\n"))
	}

	// ACME subjects: only hosts under auto registrations.
	if v.Apps.TLS == nil || v.Apps.TLS.Automation == nil {
		t.Fatal("no tls automation for the auto host")
	}
	if subj := v.Apps.TLS.Automation.Policies[0].Subjects; strings.Join(subj, ",") != "auto.test" {
		t.Errorf("ACME subjects = %v, want [auto.test]", subj)
	}
	// The uploaded pair is loaded from disk.
	lf := v.Apps.TLS.Certificates.LoadFiles
	if len(lf) != 1 || lf[0].Certificate != "/d/_wildcard_.cert.test.crt" || lf[0].Key != "/d/_wildcard_.cert.test.key" {
		t.Errorf("load_files = %+v", lf)
	}
	// The unused auto wildcard never gets a certificate order; the served
	// auto host does.
	ah := v.Apps.HTTP.Servers["https"].AutomaticHTTPS
	if ah == nil || strings.Join(ah.SkipCertificates, ",") != "*.auto.test" {
		t.Errorf("automatic_https = %+v, want skip_certificates [*.auto.test]", ah)
	}
}

// TestBuildConfigCertificateHostsAreNotACMESubjects: a certificate-mode
// registration (wildcard or exact) never puts a host in an ACME policy, and
// with no auto host there is no automation at all.
func TestBuildConfigCertificateHostsAreNotACMESubjects(t *testing.T) {
	regs := []domains.Registration{
		{Name: "*.cert.test", TLS: command.DomainTLSCertificate, CertFile: "/c.crt", KeyFile: "/c.key"},
		{Name: "exact.test", TLS: command.DomainTLSCertificate, CertFile: "/e.crt", KeyFile: "/e.key"},
	}
	apps := []AppIngress{{AppID: "a", Ingress: model.Ingress{Domains: []model.IngressDomain{
		{Domain: "a.cert.test", UpstreamPort: 1, SSL: true},
		{Domain: "exact.test", UpstreamPort: 2, SSL: true},
	}}}}
	raw, warnings, err := BuildConfig(apps, regs, Options{})
	if err != nil || len(warnings) != 0 {
		t.Fatalf("BuildConfig: %v %v", err, warnings)
	}
	v := decodeView(t, raw)
	if v.Apps.TLS == nil || v.Apps.TLS.Automation != nil {
		t.Fatalf("tls = %+v, want certificates only, no automation", v.Apps.TLS)
	}
	if n := len(v.Apps.TLS.Certificates.LoadFiles); n != 2 {
		t.Errorf("load_files = %d, want 2", n)
	}
	if ah := v.Apps.HTTP.Servers["https"].AutomaticHTTPS; ah != nil {
		t.Errorf("certificate catch-alls need no skip list: %+v", ah)
	}
}

// TestBuildConfigWithoutRegistrationsServesNothing: after the upgrade an
// install with no registrations excludes every host (fail closed).
func TestBuildConfigWithoutRegistrationsServesNothing(t *testing.T) {
	raw, warnings, err := BuildConfig(sampleApps(), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 3 {
		t.Fatalf("warnings = %v, want one per host", warnings)
	}
	v := decodeView(t, raw)
	for name, srv := range v.Apps.HTTP.Servers {
		if len(srv.Routes) != 0 {
			t.Errorf("server %s has routes: %v", name, routeKinds(v, name))
		}
	}
	if v.Apps.TLS != nil {
		t.Error("tls app present without registrations")
	}
}
```

- [ ] **Step 2: Point the existing tests at registrations**

`config_test.go` — sample registrations covering `sampleApps`, and the new argument everywhere:

```diff
diff --git a/internal/infra/ingress/caddy/config_test.go b/internal/infra/ingress/caddy/config_test.go
--- a/internal/infra/ingress/caddy/config_test.go
+++ b/internal/infra/ingress/caddy/config_test.go
@@ -5,7 +5,9 @@ import (
 	"strings"
 	"testing"
 
+	"winterflow/internal/domain/command"
 	"winterflow/internal/domain/model"
+	"winterflow/internal/infra/ingress/domains"
 )
 
 func opts() Options {
@@ -34,6 +36,15 @@ func decode(t *testing.T, raw []byte) map[string]any {
 	return m
 }
 
+// sampleRegs covers sampleApps: the example.com hosts on auto TLS, the plain
+// host with TLS off.
+func sampleRegs() []domains.Registration {
+	return []domains.Registration{
+		{Name: "*.example.com", TLS: command.DomainTLSAuto},
+		{Name: "plain.example.com", TLS: command.DomainTLSOff},
+	}
+}
+
 func sampleApps() []AppIngress {
 	return []AppIngress{
 		{AppID: "app-b", Ingress: model.Ingress{
@@ -50,7 +61,7 @@ func sampleApps() []AppIngress {
 }
 
 func TestBuildConfigShape(t *testing.T) {
-	raw, warnings, err := BuildConfig(sampleApps(), opts())
+	raw, warnings, err := BuildConfig(sampleApps(), sampleRegs(), opts())
 	if err != nil || len(warnings) != 0 {
 		t.Fatalf("BuildConfig: %v, warnings %v", err, warnings)
 	}
@@ -107,7 +118,7 @@ func TestBuildConfigIsolatesBadFragmentsAndDups(t *testing.T) {
 			Domains: []model.IngressDomain{{Domain: "blog.example.com", UpstreamPort: 7000, SSL: true}},
 		}},
 	)
-	raw, warnings, err := BuildConfig(apps, opts())
+	raw, warnings, err := BuildConfig(apps, sampleRegs(), opts())
 	if err != nil {
 		t.Fatal(err)
 	}
@@ -125,7 +136,7 @@ func TestBuildConfigIsolatesBadFragmentsAndDups(t *testing.T) {
 
 func TestBuildConfigEmptyAndNoSSL(t *testing.T) {
 	// No apps: still a valid config with empty servers (Caddy runs, serves nothing).
-	raw, warnings, err := BuildConfig(nil, opts())
+	raw, warnings, err := BuildConfig(nil, nil, opts())
 	if err != nil || len(warnings) != 0 {
 		t.Fatalf("empty: %v %v", err, warnings)
 	}
@@ -137,11 +148,11 @@ func TestBuildConfigEmptyAndNoSSL(t *testing.T) {
 	// Debug level turns access logs on.
 	o := opts()
 	o.LogLevel = "debug"
-	raw, _, _ = BuildConfig(sampleApps(), o)
+	raw, _, _ = BuildConfig(sampleApps(), sampleRegs(), o)
 	if !strings.Contains(string(raw), `"logs"`) {
 		t.Error("debug level did not enable access logs")
 	}
-	raw, _, _ = BuildConfig(sampleApps(), opts())
+	raw, _, _ = BuildConfig(sampleApps(), sampleRegs(), opts())
 	cfg = decode(t, raw)
 	srv := dig(t, cfg, "apps", "http", "servers", "https").(map[string]any)
 	if _, ok := srv["logs"]; ok {
```

`ratelimit_test.go` — `TestBuildConfigValidatesWithCaddy` now validates all three modes with a real certificate pair on disk:

```diff
diff --git a/internal/infra/ingress/caddy/ratelimit_test.go b/internal/infra/ingress/caddy/ratelimit_test.go
--- a/internal/infra/ingress/caddy/ratelimit_test.go
+++ b/internal/infra/ingress/caddy/ratelimit_test.go
@@ -9,7 +9,10 @@ import (
 
 	caddy "github.com/caddyserver/caddy/v2"
 
+	"winterflow/internal/domain/command"
 	"winterflow/internal/domain/model"
+	"winterflow/internal/infra/ingress/domains"
+	"winterflow/internal/infra/ingress/domains/certtest"
 )
 
 // nextCounter counts how many requests pass through the middleware.
@@ -121,16 +124,45 @@ func TestRateLimitCapsVisitorMap(t *testing.T) {
 }
 
 // TestBuildConfigValidatesWithCaddy runs the full emitted config — throttle
-// module, timeouts, proxy routes — through Caddy's own validator, so a schema
-// mistake or an unregistered module fails here instead of at agent startup.
+// module, timeouts, proxy routes, all three TLS modes with an uploaded
+// certificate loaded from disk, the wildcard catch-all's "not" matcher and
+// skip_certificates — through Caddy's own validator, so a schema mistake,
+// an unloadable certificate or an unregistered module fails here instead of
+// at agent startup.
 func TestBuildConfigValidatesWithCaddy(t *testing.T) {
+	dir := t.TempDir()
+	store := domains.NewStore(dir)
+	cert, key := certtest.Issue(t, time.Now().Add(90*24*time.Hour), "*.cert.test")
+	for _, r := range []struct {
+		name      string
+		mode      command.DomainTLS
+		cert, key []byte
+	}{
+		{"*.cert.test", command.DomainTLSCertificate, cert, key},
+		{"x.cert.test", command.DomainTLSOff, nil, nil},
+		{"example.test", command.DomainTLSAuto, nil, nil},
+		{"*.auto.test", command.DomainTLSAuto, nil, nil},
+		{"*.tunnel.test", command.DomainTLSOff, nil, nil},
+	} {
+		if err := store.Put(r.name, r.mode, r.cert, r.key); err != nil {
+			t.Fatal(err)
+		}
+	}
+	regs, err := store.List()
+	if err != nil {
+		t.Fatal(err)
+	}
 	apps := []AppIngress{{
 		AppID: "app1",
 		Ingress: model.Ingress{
-			Domains: []model.IngressDomain{{Domain: "example.test", UpstreamPort: 8080}},
+			Domains: []model.IngressDomain{
+				{Domain: "example.test", UpstreamPort: 8080},
+				{Domain: "a.cert.test", UpstreamPort: 8081},
+				{Domain: "b.tunnel.test", UpstreamPort: 8082},
+			},
 		},
 	}}
-	raw, warnings, err := BuildConfig(apps, Options{
+	raw, warnings, err := BuildConfig(apps, regs, Options{
 		RateLimitRPS:   50,
 		RateLimitBurst: 100,
 		StorageDir:     t.TempDir(),
@@ -150,7 +182,7 @@ func TestBuildConfigValidatesWithCaddy(t *testing.T) {
 // TestBuildConfigIncludesThrottle fixates that the predefined throttle sits in
 // front of every route on both servers, and that connection timeouts are set.
 func TestBuildConfigIncludesThrottle(t *testing.T) {
-	raw, warnings, err := BuildConfig(nil, Options{RateLimitRPS: 50, RateLimitBurst: 100})
+	raw, warnings, err := BuildConfig(nil, nil, Options{RateLimitRPS: 50, RateLimitBurst: 100})
 	if err != nil || len(warnings) != 0 {
 		t.Fatalf("BuildConfig: err=%v warnings=%v", err, warnings)
 	}
@@ -187,7 +219,7 @@ func TestBuildConfigIncludesThrottle(t *testing.T) {
 	}
 
 	// Disabled: no throttle route.
-	raw, _, err = BuildConfig(nil, Options{})
+	raw, _, err = BuildConfig(nil, nil, Options{})
 	if err != nil {
 		t.Fatal(err)
 	}
```

`manager_test.go` — the existing serving test registers `*.test` (TLS off, so no ACME in tests), and a new real-Caddy test proves certificate mode serves the uploaded certificate and the catch-all answers 404:

```diff
diff --git a/internal/infra/ingress/caddy/manager_test.go b/internal/infra/ingress/caddy/manager_test.go
--- a/internal/infra/ingress/caddy/manager_test.go
+++ b/internal/infra/ingress/caddy/manager_test.go
@@ -2,8 +2,10 @@ package caddy
 
 import (
 	"context"
+	"crypto/tls"
 	"encoding/json"
 	"fmt"
+	"io"
 	"net"
 	"net/http"
 	"net/http/httptest"
@@ -13,6 +15,9 @@ import (
 	"testing"
 	"time"
 
+	"winterflow/internal/domain/command"
+	"winterflow/internal/infra/ingress/domains"
+	"winterflow/internal/infra/ingress/domains/certtest"
 	"winterflow/pkg/config"
 	"winterflow/pkg/logger"
 )
@@ -40,6 +45,14 @@ func writeApp(t *testing.T, dataDir, appID, configJSON string) {
 	}
 }
 
+// registerDomain stores a registration the way domain.create does.
+func registerDomain(t *testing.T, dataDir, name string, mode command.DomainTLS) {
+	t.Helper()
+	if err := domains.NewStore(filepath.Join(dataDir, "domains")).Put(name, mode, nil, nil); err != nil {
+		t.Fatal(err)
+	}
+}
+
 func getVia(t *testing.T, port int, host, path string) *http.Response {
 	t.Helper()
 	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
@@ -89,6 +102,7 @@ func TestManagerServesReloadsAndIsolates(t *testing.T) {
 		return string(raw)
 	}
 	writeApp(t, dataDir, "app-1", appCfg(upstreamPort))
+	registerDomain(t, dataDir, "*.test", command.DomainTLSOff)
 	// A second app with a broken config must not affect app-1.
 	writeApp(t, dataDir, "app-2", `{"name":"broken","ingress":{"domains":[{"domain":"BAD HOST","upstream_port":1}]}}`)
 
@@ -238,3 +252,67 @@ func unsetAPIPort(t *testing.T) {
 	t.Setenv("API_PORT", "")
 	_ = os.Unsetenv("API_PORT")
 }
+
+// TestManagerServesUploadedCertificateAndCatchAll runs real Caddy with a
+// certificate-mode wildcard and an off-mode wildcard: HTTPS for any host
+// under the wildcard presents the uploaded certificate (no ACME involved),
+// and hosts no app claims answer the catch-all 404 on both servers.
+func TestManagerServesUploadedCertificateAndCatchAll(t *testing.T) {
+	dataDir := t.TempDir()
+	httpPort, httpsPort := freePort(t), freePort(t)
+	t.Setenv("AGENT_DATA_DIR", dataDir)
+	t.Setenv("INGRESS_ENABLED", "true")
+	t.Setenv("INGRESS_HTTP_PORT", fmt.Sprint(httpPort))
+	t.Setenv("INGRESS_HTTPS_PORT", fmt.Sprint(httpsPort))
+	t.Setenv("LOG_LEVEL", "error")
+
+	cert, key := certtest.Issue(t, time.Now().Add(90*24*time.Hour), "*.cert.test")
+	store := domains.NewStore(filepath.Join(dataDir, "domains"))
+	if err := store.Put("*.cert.test", command.DomainTLSCertificate, cert, key); err != nil {
+		t.Fatal(err)
+	}
+	registerDomain(t, dataDir, "*.tunnel.test", command.DomainTLSOff)
+
+	m := NewManager(config.NewServerConfig("standalone"), logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}))
+	t.Cleanup(m.Stop)
+	ctx, cancel := context.WithCancel(context.Background())
+	defer cancel()
+	if err := m.Start(ctx); err != nil || !m.Enabled() {
+		t.Fatalf("start: %v, enabled %v", err, m.Enabled())
+	}
+
+	// Off mode: plain HTTP, unknown host under the wildcard is a clean 404.
+	resp := getVia(t, httpPort, "nope.tunnel.test", "/")
+	body, _ := io.ReadAll(resp.Body)
+	resp.Body.Close()
+	if resp.StatusCode != http.StatusNotFound || !strings.Contains(string(body), "no app is served on this hostname") {
+		t.Fatalf("off catch-all: %d %q", resp.StatusCode, body)
+	}
+
+	// Certificate mode: the TLS handshake presents the uploaded leaf.
+	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
+		ServerName:         "nope.cert.test",
+		InsecureSkipVerify: true, // the test CA is not in the system pool; the leaf is checked below
+	}}}
+	req, _ := http.NewRequest("GET", fmt.Sprintf("https://127.0.0.1:%d/", httpsPort), nil)
+	req.Host = "nope.cert.test"
+	var tlsResp *http.Response
+	var err error
+	for i := 0; i < 50; i++ {
+		if tlsResp, err = client.Do(req); err == nil {
+			break
+		}
+		time.Sleep(100 * time.Millisecond)
+	}
+	if err != nil {
+		t.Fatalf("https via caddy: %v", err)
+	}
+	defer tlsResp.Body.Close()
+	leaf := tlsResp.TLS.PeerCertificates[0]
+	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "*.cert.test" || leaf.Issuer.CommonName != certtest.Issuer {
+		t.Fatalf("served certificate = %v issued by %q", leaf.DNSNames, leaf.Issuer.CommonName)
+	}
+	if tlsResp.StatusCode != http.StatusNotFound {
+		t.Fatalf("certificate catch-all: %d", tlsResp.StatusCode)
+	}
+}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `go test ./internal/infra/ingress/caddy/`
Expected: FAIL to compile — `too many arguments in call to BuildConfig` / `unknown field Name in struct literal`.

- [ ] **Step 4: Rewrite the config builder** — `internal/infra/ingress/caddy/config.go` becomes:

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

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/ingress/domains"
)

// AppIngress is one app's committed ingress plus its display name (from the
// same config.json), used to name apps in the domain delete refusal.
type AppIngress struct {
	AppID   string
	Name    string
	Ingress model.Ingress
}

type Options struct {
	HTTPPort   int
	HTTPSPort  int
	ACMEEmail  string
	StorageDir string
	LogLevel   string
	// RateLimitRPS/RateLimitBurst configure the predefined per-client-IP
	// throttle in front of every route (0 rps disables it). A ceiling on
	// request fan-out so traffic spikes answer 429 instead of saturating the
	// host.
	RateLimitRPS   float64
	RateLimitBurst int
}

// --- minimal typed mirror of Caddy's documented JSON config ---------------
// Only the fields we emit. Values arrive via json.Marshal of typed fields —
// user strings can never alter the config structure.

type caddyConfig struct {
	Admin   *adminConfig   `json:"admin,omitempty"`
	Logging *loggingConfig `json:"logging,omitempty"`
	Storage map[string]any `json:"storage,omitempty"`
	Apps    map[string]any `json:"apps"`
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
	// AutomaticHTTPS lists catch-all names Caddy must not order certs for.
	AutomaticHTTPS *autoHTTPS `json:"automatic_https,omitempty"`
	// Connection hygiene: slow-header and idle connections must not pile up
	// on a small host. Durations are Caddy duration strings.
	ReadHeaderTimeout string `json:"read_header_timeout,omitempty"`
	IdleTimeout       string `json:"idle_timeout,omitempty"`
}

type autoHTTPS struct {
	SkipCertificates []string `json:"skip_certificates,omitempty"`
}

type route struct {
	Match  []matcher        `json:"match,omitempty"`
	Handle []map[string]any `json:"handle"`
}

type matcher struct {
	Host []string `json:"host,omitempty"`
	Path []string `json:"path,omitempty"`
	// Not excludes requests any of these matcher sets match.
	Not []matcher `json:"not,omitempty"`
}

type tlsApp struct {
	Automation   *tlsAutomation   `json:"automation,omitempty"`
	Certificates *tlsCertificates `json:"certificates,omitempty"`
}

// tlsCertificates loads uploaded (certificate-mode) PEM pairs from disk.
// Automatic HTTPS skips every name a loaded certificate covers.
type tlsCertificates struct {
	LoadFiles []certKeyFiles `json:"load_files"`
}

type certKeyFiles struct {
	Certificate string `json:"certificate"`
	Key         string `json:"key"`
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

// notFoundRoute answers 404 for every host under a registration that no app
// route claimed (it sits after all app routes on its server).
func notFoundRoute(m matcher) route {
	return route{
		Match: []matcher{m},
		Handle: []map[string]any{{
			"handler":     "static_response",
			"status_code": "404",
			"headers":     http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}},
			"body":        "404 not found: no app is served on this hostname\n",
		}},
	}
}

// BuildConfig merges per-app ingress fragments into one Caddy config, placing
// each hostname by the TLS mode of the registration that wins it (exact over
// wildcard): auto and certificate on the HTTPS server, off on the HTTP
// server; only auto hosts are ACME subjects. A hostname no registration
// covers is excluded with a warning (fail closed). Fragment validation
// failures and cross-app duplicates are warnings too, never errors: one
// app's bad routing must not take down another's. Apps are processed in
// sorted-AppID order so duplicate resolution is deterministic.
func BuildConfig(apps []AppIngress, regs []domains.Registration, opts Options) ([]byte, []string, error) {
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

	names := make([]string, len(regs))
	modes := map[string]command.DomainTLS{}
	for i, r := range regs {
		names[i] = r.Name
		modes[r.Name] = r.TLS
	}

	httpsRoutes := []route{}
	httpRoutes := []route{}
	var sslSubjects []string

	for _, a := range sorted {
		if err := a.Ingress.Validate(); err != nil {
			warnings = append(warnings, fmt.Sprintf("app %s: ingress excluded: %v", a.AppID, err))
			continue
		}
		// Claim this app's covered hostnames; on cross-app duplicates the
		// app keeps its other domains — only the contested hostname is
		// dropped. modeOf doubles as "mine": the TLS mode of each claimed
		// host's winning registration.
		modeOf := map[string]command.DomainTLS{}
		for _, d := range a.Ingress.DomainNames() {
			win, ok := model.WinningDomain(names, d)
			if !ok {
				warnings = append(warnings, fmt.Sprintf("app %s: domain %s is not covered by a registered domain, skipping", a.AppID, d))
				continue
			}
			if owner, taken := claimed[d]; taken {
				warnings = append(warnings, fmt.Sprintf("app %s: domain %s already claimed by app %s, skipping", a.AppID, d, owner))
				continue
			}
			claimed[d] = a.AppID
			modeOf[d] = modes[win]
		}
		// place puts a route on the server its host's mode selects; auto
		// hosts also become ACME subjects.
		place := func(host string, rt route, subject bool) {
			switch modeOf[host] {
			case command.DomainTLSOff:
				httpRoutes = append(httpRoutes, rt)
			case command.DomainTLSAuto:
				httpsRoutes = append(httpsRoutes, rt)
				if subject {
					sslSubjects = append(sslSubjects, host)
				}
			default: // certificate
				httpsRoutes = append(httpsRoutes, rt)
			}
		}

		// Path rules first: they must match before their domain's main route.
		for _, r := range a.Ingress.Redirects {
			if _, mine := modeOf[r.Domain]; r.Path == "" || !mine {
				continue
			}
			place(r.Domain, redirectRoute(r.Domain, r.Path, r.To, r.Code), false)
		}
		for _, d := range a.Ingress.Domains {
			if _, mine := modeOf[d.Domain]; !mine {
				continue
			}
			place(d.Domain, proxyRoute(d.Domain, d.UpstreamPort), true)
		}
		for _, r := range a.Ingress.Redirects {
			if _, mine := modeOf[r.Domain]; r.Path != "" || !mine {
				continue
			}
			// Domain-level redirect preserves the request URI. The
			// {http.request.uri} placeholder is a fixed literal appended to a
			// validated absolute URL — not user templating.
			place(r.Domain, redirectRoute(r.Domain, "", r.To+"{http.request.uri}", r.Code), true)
		}
	}

	// Catch-all 404 per registration, after every app route. A wildcard's
	// catch-all leaves out the exact registrations under it: those win
	// their host and may sit on the other server. Auto catch-alls must not
	// make Caddy order a certificate (a wildcard can't pass HTTP-01, an
	// unused name may not even point here), so their names are skipped for
	// certificates unless an app route serves that very host.
	var skipCerts []string
	var loadFiles []certKeyFiles
	for _, r := range regs {
		m := matcher{Host: []string{r.Name}}
		if model.IsWildcardDomain(r.Name) {
			var exact []string
			for _, n := range names {
				if !model.IsWildcardDomain(n) && model.DomainCovers(r.Name, n) {
					exact = append(exact, n)
				}
			}
			if len(exact) > 0 {
				m.Not = []matcher{{Host: exact}}
			}
		}
		switch r.TLS {
		case command.DomainTLSOff:
			httpRoutes = append(httpRoutes, notFoundRoute(m))
		case command.DomainTLSCertificate:
			httpsRoutes = append(httpsRoutes, notFoundRoute(m))
			loadFiles = append(loadFiles, certKeyFiles{Certificate: r.CertFile, Key: r.KeyFile})
		default: // auto
			httpsRoutes = append(httpsRoutes, notFoundRoute(m))
			if _, served := claimed[r.Name]; !served {
				skipCerts = append(skipCerts, r.Name)
			}
		}
	}

	// Predefined throttle: a non-terminal first route on both servers — the
	// middleware passes allowed requests through to the routes below.
	if opts.RateLimitRPS > 0 {
		throttle := route{Handle: []map[string]any{{
			"handler": "winterflow_rate_limit",
			"rps":     opts.RateLimitRPS,
			"burst":   opts.RateLimitBurst,
		}}}
		httpsRoutes = append([]route{throttle}, httpsRoutes...)
		httpRoutes = append([]route{throttle}, httpRoutes...)
	}

	httpsServer := &httpServer{Listen: []string{":" + strconv.Itoa(httpsPort)}, Routes: httpsRoutes}
	if len(skipCerts) > 0 {
		httpsServer.AutomaticHTTPS = &autoHTTPS{SkipCertificates: skipCerts}
	}
	// Servers listening only on the HTTP port are exempt from automatic
	// HTTPS: no certs, no redirects for their hosts. Caddy appends its own
	// HTTP->HTTPS redirects for the ssl hosts to this server.
	httpServerCfg := &httpServer{Listen: []string{":" + strconv.Itoa(httpPort)}, Routes: httpRoutes}
	for _, srv := range []*httpServer{httpsServer, httpServerCfg} {
		srv.ReadHeaderTimeout = "10s"
		srv.IdleTimeout = "5m"
	}
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

	var tlsCfg tlsApp
	if len(sslSubjects) > 0 {
		issuer := map[string]any{"module": "acme"}
		if opts.ACMEEmail != "" {
			issuer["email"] = opts.ACMEEmail
		}
		tlsCfg.Automation = &tlsAutomation{Policies: []tlsPolicy{{
			Subjects: sslSubjects,
			Issuers:  []map[string]any{issuer},
		}}}
	}
	if len(loadFiles) > 0 {
		tlsCfg.Certificates = &tlsCertificates{LoadFiles: loadFiles}
	}
	if tlsCfg.Automation != nil || tlsCfg.Certificates != nil {
		appsCfg["tls"] = tlsCfg
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

- [ ] **Step 5: Feed it from the manager** — `internal/infra/ingress/caddy/manager.go`:

```diff
diff --git a/internal/infra/ingress/caddy/manager.go b/internal/infra/ingress/caddy/manager.go
--- a/internal/infra/ingress/caddy/manager.go
+++ b/internal/infra/ingress/caddy/manager.go
@@ -2,6 +2,7 @@ package caddy
 
 import (
 	"context"
+	"encoding/json"
 	"errors"
 	"fmt"
 	"os"
@@ -11,6 +12,7 @@ import (
 	"sync"
 
 	"winterflow/internal/domain/model"
+	"winterflow/internal/infra/ingress/domains"
 	"winterflow/pkg/config"
 	"winterflow/pkg/logger"
 
@@ -34,15 +36,20 @@ var caddyMu sync.Mutex
 // Manager owns the embedded Caddy instance. Ingress failures degrade ingress
 // only: Start never fails the agent, Reload never fails a deploy.
 type Manager struct {
-	cfg *config.ServerConfig
-	log *logger.Logger
+	cfg     *config.ServerConfig
+	log     *logger.Logger
+	domains *domains.Store
 
 	mu      sync.Mutex
 	enabled bool
 }
 
 func NewManager(cfg *config.ServerConfig, log *logger.Logger) *Manager {
-	return &Manager{cfg: cfg, log: log}
+	return &Manager{
+		cfg:     cfg,
+		log:     log,
+		domains: domains.NewStore(filepath.Join(cfg.GetAgentDataDir(), "domains")),
+	}
 }
 
 func (m *Manager) options() Options {
@@ -88,15 +95,25 @@ func (m *Manager) scanApps() ([]AppIngress, []string) {
 		if ing == nil {
 			continue
 		}
-		out = append(out, AppIngress{AppID: appID, Ingress: *ing})
+		var meta struct {
+			Name string `json:"name"`
+		}
+		_ = json.Unmarshal(raw, &meta) // ParseIngress already proved it is JSON
+		out = append(out, AppIngress{AppID: appID, Name: meta.Name, Ingress: *ing})
 	}
 	return out, warnings
 }
 
 func (m *Manager) buildConfig() ([]byte, []string) {
-	apps, scanWarnings := m.scanApps()
-	cfg, buildWarnings, err := BuildConfig(apps, m.options())
-	warnings := append(scanWarnings, buildWarnings...)
+	apps, warnings := m.scanApps()
+	regs, err := m.domains.List()
+	if err != nil {
+		// Unreadable domains dir: no registration covers anything, so
+		// every host is excluded (fail closed) and the warning says why.
+		warnings = append(warnings, "ingress: "+err.Error())
+	}
+	cfg, buildWarnings, err := BuildConfig(apps, regs, m.options())
+	warnings = append(warnings, buildWarnings...)
 	if err != nil {
 		// Marshal failure of our own structs: effectively impossible, but
 		// degrade rather than propagate.
```

And mark `ssl` as legacy in `internal/domain/model/ingress.go`:

```diff
diff --git a/internal/domain/model/ingress.go b/internal/domain/model/ingress.go
--- a/internal/domain/model/ingress.go
+++ b/internal/domain/model/ingress.go
@@ -25,8 +25,11 @@ type Ingress struct {
 }
 
 // IngressDomain maps a hostname to a host port the app's compose file
-// publishes (ideally bound to 127.0.0.1). SSL=true means an ACME cert plus
-// the automatic HTTP->HTTPS redirect; false means plain HTTP on port 80 only.
+// publishes (ideally bound to 127.0.0.1). SSL no longer decides anything for
+// the proxy: TLS follows the registered domain covering the hostname (see
+// RegisteredDomain). It still loads from old configs, picks the auto/off
+// mode when the agent first registers existing hostnames, and picks the
+// link scheme on app cards.
 type IngressDomain struct {
 	Domain       string `json:"domain"`
 	UpstreamPort int    `json:"upstream_port"`
@@ -34,9 +37,10 @@ type IngressDomain struct {
 }
 
 // IngressRedirect is either a domain-level redirect (Path == "", preserves the
-// request URI, needs its own SSL choice because serving https://source
-// requires a cert) or a path rule (Path != "", scoped to a domain the app
-// already claims, redirects the matched prefix to the exact To URL).
+// request URI; its source hostname must be covered by a registered domain
+// like a route's, and SSL is legacy as on IngressDomain) or a path rule
+// (Path != "", scoped to a domain the app already claims, redirects the
+// matched prefix to the exact To URL).
 type IngressRedirect struct {
 	Domain string `json:"domain"`
 	Path   string `json:"path,omitempty"`
```

- [ ] **Step 6: Run the Caddy tests (real Caddy validation and serving included)**

Run: `go test -race ./internal/infra/ingress/caddy/`
Expected: PASS. With `-v`, Caddy logs `skipping automatic certificate management because one or more matching certificates are already loaded` for `*.cert.test` / `a.cert.test` in `TestBuildConfigValidatesWithCaddy` — the proof that certificate mode triggers no ACME.

- [ ] **Step 7: Commit**

```bash
git add internal/infra/ingress/caddy internal/domain/model/ingress.go
git commit -m "feat(ingress): route hostnames by their registered domain's TLS mode

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Manager domain operations and the upgrade path

**Files:**
- Create: `internal/infra/ingress/caddy/domains.go`
- Create: `internal/infra/ingress/caddy/domains_test.go`
- Create: `internal/infra/ingress/domains/certtest/ecies.go`
- Modify: `internal/infra/ingress/caddy/manager.go` (`Start` seeds before the enabled check)

**Interfaces:**
- Consumes: `(*Manager).domains`, `(*Manager).scanApps() ([]AppIngress, []string)`, `AppIngress.Name` (Task 3); `domains.*` (Task 2); `crypto.DecryptWithPrivateKey(keyPath, ciphertext string) (string, error)` (`pkg/crypto`); `cfg.GetAgentKeyPath()`, `cfg.IsIngressEnabled()` (`pkg/config`).
- Produces:
  - `caddy.ErrIngressDisabled` (`"ingress is disabled on this server"`)
  - `(*Manager).ListDomains(ctx) ([]command.Domain, error)`, `CreateDomain(ctx, command.CreateDomainRequest) (command.Domain, error)`, `DeleteDomain(ctx, name string) error` — exactly the methods Task 5 adds to `port.IngressManager`
  - `certtest.AgentKey(t, certDir string) *ecdsa.PublicKey` (writes `agent.key`; point `HUB_CERT_DIR` at `certDir`) and `certtest.Encrypt(t, pub, plaintext string) string` (browser-equivalent ECIES)

- [ ] **Step 1: Add the ECIES test helper** — `internal/infra/ingress/domains/certtest/ecies.go`:

```go
package certtest

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

// AgentKey writes a fresh P-256 agent.key into certDir (point HUB_CERT_DIR
// at it) and returns the public half for Encrypt.
func AgentKey(t testing.TB, certDir string) *ecdsa.PublicKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "agent.key"),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return &key.PublicKey
}

// Encrypt mirrors the browser's ECIES (ephemeral P-256 + ECDH + SHA-256 +
// AES-256-GCM), the scheme pkg/crypto decrypts.
func Encrypt(t testing.TB, pub *ecdsa.PublicKey, plaintext string) string {
	t.Helper()
	curve := elliptic.P256()
	eph, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sharedX, _ := curve.ScalarMult(pub.X, pub.Y, eph.D.Bytes()) //nolint:staticcheck // mirrors the browser
	xb := sharedX.Bytes()
	for len(xb) < 32 {
		xb = append([]byte{0}, xb...)
	}
	keyHash := sha256.Sum256(xb)
	block, _ := aes.NewCipher(keyHash[:])
	gcm, _ := cipher.NewGCM(block)
	iv := make([]byte, 12)
	if _, err := rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	ct := gcm.Seal(nil, iv, []byte(plaintext), nil)
	ephPoint := elliptic.Marshal(curve, eph.X, eph.Y) //nolint:staticcheck // mirrors the browser
	payload := append(append(append([]byte{}, ephPoint...), iv...), ct...)
	return base64.StdEncoding.EncodeToString(payload)
}
```

- [ ] **Step 2: Write the failing manager tests** — `internal/infra/ingress/caddy/domains_test.go` (the failed-renewal assertion is Review Focus item 5):

```go
package caddy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/ingress/domains/certtest"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

func testManager(t *testing.T, ingressEnabled bool) (*Manager, string) {
	t.Helper()
	dataDir := t.TempDir()
	t.Setenv("AGENT_DATA_DIR", dataDir)
	t.Setenv("INGRESS_ENABLED", map[bool]string{true: "true", false: "false"}[ingressEnabled])
	t.Setenv("LOG_LEVEL", "error")
	return NewManager(config.NewServerConfig("standalone"), logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})), dataDir
}

func TestStartSeedsDomainsFromExistingAppsOnce(t *testing.T) {
	m, dataDir := testManager(t, false) // seeding does not need the proxy
	writeApp(t, dataDir, "app-1", `{"name":"Blog","ingress":{
		"domains":[{"domain":"blog.example.com","upstream_port":1,"ssl":true},{"domain":"plain.example.com","upstream_port":2,"ssl":false}],
		"redirects":[{"domain":"www.example.com","to":"https://blog.example.com","code":301,"ssl":true},
		             {"domain":"blog.example.com","path":"/old/*","to":"https://blog.example.com/new","code":302}]}}`)
	// A later app repeating a host does not change its mode: first app wins.
	writeApp(t, dataDir, "app-2", `{"name":"Dup","ingress":{"domains":[{"domain":"blog.example.com","upstream_port":3,"ssl":false}]}}`)
	writeApp(t, dataDir, "app-3", `{"name":"Broken"`)
	writeApp(t, dataDir, "app-4", `{"name":"No ingress"}`)

	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := m.ListDomains(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	modes := map[string]command.DomainTLS{}
	for _, d := range got {
		modes[d.Name] = d.TLS
	}
	want := map[string]command.DomainTLS{
		"blog.example.com":  command.DomainTLSAuto,
		"plain.example.com": command.DomainTLSOff,
		"www.example.com":   command.DomainTLSAuto,
	}
	if len(modes) != len(want) {
		t.Fatalf("seeded = %v, want %v", modes, want)
	}
	for n, mode := range want {
		if modes[n] != mode {
			t.Errorf("%s = %q, want %q", n, modes[n], mode)
		}
	}

	// Second start: the dir exists, a new app's host is not auto-registered.
	writeApp(t, dataDir, "app-5", `{"name":"New","ingress":{"domains":[{"domain":"new.example.com","upstream_port":5,"ssl":true}]}}`)
	m2 := NewManager(config.NewServerConfig("standalone"), m.log)
	if err := m2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if again, _ := m2.ListDomains(context.Background()); len(again) != 3 {
		t.Fatalf("second start changed the registrations: %+v", again)
	}
}

func TestDomainCommandsCertificateMode(t *testing.T) {
	m, dataDir := testManager(t, true)
	certDir := t.TempDir()
	t.Setenv("HUB_CERT_DIR", certDir)
	pub := certtest.AgentKey(t, certDir)
	writeApp(t, dataDir, "app-1", `{"name":"Shop","ingress":{"domains":[{"domain":"shop.twillingate.app","upstream_port":1}]}}`)
	ctx := context.Background()

	notAfter := time.Now().Add(60 * 24 * time.Hour).Truncate(time.Second)
	cert, key := certtest.Issue(t, notAfter, "*.twillingate.app")
	d, err := m.CreateDomain(ctx, command.CreateDomainRequest{
		Name: "*.twillingate.app", TLS: command.DomainTLSCertificate,
		Certificate: string(cert), Key: certtest.Encrypt(t, pub, string(key)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.TLS != command.DomainTLSCertificate || d.NotAfter == nil || !d.NotAfter.Equal(notAfter) ||
		d.Issuer != certtest.Issuer || len(d.Apps) != 1 || d.Apps[0] != "app-1" {
		t.Fatalf("created = %+v", d)
	}
	// The decrypted key is on disk, private, and never in the response.
	stored, err := os.ReadFile(filepath.Join(dataDir, "domains", "_wildcard_.twillingate.app.key"))
	if err != nil || string(stored) != string(key) {
		t.Fatalf("stored key = %q, %v", stored, err)
	}

	bad := map[string]command.CreateDomainRequest{
		"SAN does not cover": {Name: "*.other.app", TLS: command.DomainTLSCertificate,
			Certificate: string(cert), Key: certtest.Encrypt(t, pub, string(key))},
		"key not decryptable": {Name: "*.twillingate.app", TLS: command.DomainTLSCertificate,
			Certificate: string(cert), Key: "bm90IGEgY2lwaGVydGV4dA=="},
	}
	for label, req := range bad {
		if _, err := m.CreateDomain(ctx, req); err == nil {
			t.Errorf("%s: created", label)
		}
	}
	if doms, _ := m.ListDomains(ctx); len(doms) != 1 {
		t.Fatalf("a refused create was stored: %+v", doms)
	}
	// Review focus: a failed renewal keeps the working certificate.
	wrongCert, wrongKey := certtest.Issue(t, notAfter, "*.example.com")
	if _, err := m.CreateDomain(ctx, command.CreateDomainRequest{Name: "*.twillingate.app", TLS: command.DomainTLSCertificate,
		Certificate: string(wrongCert), Key: certtest.Encrypt(t, pub, string(wrongKey))}); err == nil {
		t.Fatal("renewal with a certificate for another name succeeded")
	}
	if kept, _ := os.ReadFile(filepath.Join(dataDir, "domains", "_wildcard_.twillingate.app.crt")); string(kept) != string(cert) {
		t.Fatal("a refused renewal replaced the working certificate")
	}

	// In use: refused, naming the app.
	err = m.DeleteDomain(ctx, "*.twillingate.app")
	if err == nil || !strings.Contains(err.Error(), "Shop (app-1)") {
		t.Fatalf("delete in use = %v", err)
	}
	// An exact registration now wins shop.twillingate.app, so the wildcard
	// is free to go — and the exact one is the one in use.
	if _, err := m.CreateDomain(ctx, command.CreateDomainRequest{Name: "shop.twillingate.app", TLS: command.DomainTLSOff}); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteDomain(ctx, "*.twillingate.app"); err != nil {
		t.Fatalf("delete unused wildcard: %v", err)
	}
	if err := m.DeleteDomain(ctx, "*.twillingate.app"); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("delete missing = %v", err)
	}
}

func TestDomainMutationsNeedIngress(t *testing.T) {
	m, _ := testManager(t, false)
	ctx := context.Background()
	if _, err := m.CreateDomain(ctx, command.CreateDomainRequest{Name: "x.app", TLS: command.DomainTLSOff}); !errors.Is(err, ErrIngressDisabled) {
		t.Fatalf("create = %v", err)
	}
	if err := m.DeleteDomain(ctx, "x.app"); !errors.Is(err, ErrIngressDisabled) {
		t.Fatalf("delete = %v", err)
	}
	if doms, err := m.ListDomains(ctx); err != nil || len(doms) != 0 {
		t.Fatalf("list = %+v, %v", doms, err)
	}
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `go test ./internal/infra/ingress/caddy/ -run 'Seeds|DomainCommands|DomainMutations'`
Expected: FAIL to compile — `m.ListDomains undefined`, `undefined: ErrIngressDisabled`.

- [ ] **Step 4: Implement the operations** — `internal/infra/ingress/caddy/domains.go`:

```go
package caddy

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/ingress/domains"
	"winterflow/pkg/crypto"
)

// ErrIngressDisabled refuses domain mutations on an agent started with
// INGRESS_ENABLED=false: there is no proxy to apply them to.
var ErrIngressDisabled = errors.New("ingress is disabled on this server")

// seedDomains is the upgrade path: on the first start without a domains/
// dir, register every hostname existing apps use as an exact registration —
// auto where its ingress entry had ssl:true, off otherwise — so routing
// keeps working under the coverage rule. Creating the dir makes it run once.
func (m *Manager) seedDomains() {
	if m.domains.Exists() {
		return
	}
	apps, _ := m.scanApps()
	regs := seedRegistrations(apps)
	seeded, err := m.domains.Seed(regs)
	if err != nil {
		m.log.Warn("ingress: seed domain registrations", "error", err)
		return
	}
	if seeded {
		m.log.Info("ingress: registered the hostnames existing apps use", "count", len(regs), "dir", m.domains.Dir())
	}
}

// seedRegistrations maps the apps' hostnames (routes and domain-level
// redirect sources) to exact registrations. Apps are scanned in app-id order
// and the first entry for a host decides its mode, matching BuildConfig's
// first-claim-wins rule.
func seedRegistrations(apps []AppIngress) []domains.Registration {
	sorted := append([]AppIngress(nil), apps...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].AppID < sorted[j].AppID })
	seen := map[string]bool{}
	var out []domains.Registration
	add := func(host string, ssl bool) {
		if seen[host] || model.ValidateDomainName(host) != nil || model.IsWildcardDomain(host) {
			return
		}
		seen[host] = true
		mode := command.DomainTLSOff
		if ssl {
			mode = command.DomainTLSAuto
		}
		out = append(out, domains.Registration{Name: host, TLS: mode})
	}
	for _, a := range sorted {
		for _, d := range a.Ingress.Domains {
			add(d.Domain, d.SSL)
		}
		for _, r := range a.Ingress.Redirects {
			if r.Path == "" {
				add(r.Domain, r.SSL)
			}
		}
	}
	return out
}

// domainUsers maps each registration name to the apps (in app-id order)
// with at least one hostname that registration wins.
func domainUsers(apps []AppIngress, regs []domains.Registration) map[string][]AppIngress {
	names := make([]string, len(regs))
	for i, r := range regs {
		names[i] = r.Name
	}
	out := map[string][]AppIngress{}
	for _, a := range apps {
		used := map[string]bool{}
		for _, h := range a.Ingress.DomainNames() {
			if win, ok := model.WinningDomain(names, h); ok && !used[win] {
				used[win] = true
				out[win] = append(out[win], a)
			}
		}
	}
	return out
}

func describe(r domains.Registration, users []AppIngress) command.Domain {
	d := command.Domain{Name: r.Name, TLS: r.TLS, Apps: []string{}}
	for _, a := range users {
		d.Apps = append(d.Apps, a.AppID)
	}
	if r.TLS == command.DomainTLSCertificate {
		if notAfter, issuer, err := domains.CertInfo(r.CertFile); err == nil {
			d.NotAfter, d.Issuer = &notAfter, issuer
		}
	}
	return d
}

// ListDomains reports every registration with the apps using it. It works
// with ingress disabled too: it only reads the stored list.
func (m *Manager) ListDomains(context.Context) ([]command.Domain, error) {
	regs, err := m.domains.List()
	if err != nil {
		return nil, err
	}
	apps, _ := m.scanApps()
	users := domainUsers(apps, regs)
	out := make([]command.Domain, 0, len(regs))
	for _, r := range regs {
		out = append(out, describe(r, users[r.Name]))
	}
	return out, nil
}

// CreateDomain registers (or replaces) a name. In certificate mode the key
// arrives ECIES-encrypted; it is decrypted with the agent's private key and
// the pair must match, cover the name and not be expired before anything is
// written. The caller reloads the proxy.
func (m *Manager) CreateDomain(_ context.Context, in command.CreateDomainRequest) (command.Domain, error) {
	if !m.cfg.IsIngressEnabled() {
		return command.Domain{}, ErrIngressDisabled
	}
	var cert, key []byte
	if in.TLS == command.DomainTLSCertificate {
		plain, err := crypto.DecryptWithPrivateKey(m.cfg.GetAgentKeyPath(), in.Key)
		if err != nil {
			return command.Domain{}, fmt.Errorf("decrypt the certificate key: %w", err)
		}
		cert, key = []byte(in.Certificate), []byte(plain)
		if _, err := domains.CheckCertificate(in.Name, cert, key, time.Now()); err != nil {
			return command.Domain{}, err
		}
	}
	if err := m.domains.Put(in.Name, in.TLS, cert, key); err != nil {
		return command.Domain{}, err
	}
	doms, err := m.ListDomains(context.Background())
	if err != nil {
		return command.Domain{}, err
	}
	for _, d := range doms {
		if d.Name == in.Name {
			return d, nil
		}
	}
	return command.Domain{Name: in.Name, TLS: in.TLS, Apps: []string{}}, nil
}

// DeleteDomain removes a registration, refusing while any app uses a
// hostname this registration wins (the error names those apps).
func (m *Manager) DeleteDomain(_ context.Context, name string) error {
	if !m.cfg.IsIngressEnabled() {
		return ErrIngressDisabled
	}
	regs, err := m.domains.List()
	if err != nil {
		return err
	}
	apps, _ := m.scanApps()
	if users := domainUsers(apps, regs)[name]; len(users) > 0 {
		labels := make([]string, 0, len(users))
		for _, a := range users {
			if a.Name != "" {
				labels = append(labels, fmt.Sprintf("%s (%s)", a.Name, a.AppID))
			} else {
				labels = append(labels, a.AppID)
			}
		}
		return fmt.Errorf("%s is used by %s: remove its hostnames from those apps first", name, strings.Join(labels, ", "))
	}
	existed, err := m.domains.Delete(name)
	if err != nil {
		return err
	}
	if !existed {
		return fmt.Errorf("%s is not registered on this server", name)
	}
	return nil
}
```

Seed in `Start`, before the `INGRESS_ENABLED` check (an install that turns ingress on later still finds its hostnames registered):

```diff
diff --git a/internal/infra/ingress/caddy/manager.go b/internal/infra/ingress/caddy/manager.go
--- a/internal/infra/ingress/caddy/manager.go
+++ b/internal/infra/ingress/caddy/manager.go
@@ -132,6 +132,10 @@ func (m *Manager) Start(ctx context.Context) error {
 	m.mu.Lock()
 	defer m.mu.Unlock()
 
+	// Before the enabled check: an install that turns ingress on later
+	// still finds its old hostnames registered.
+	m.seedDomains()
+
 	if !m.cfg.IsIngressEnabled() {
 		m.log.Info("ingress disabled by config (INGRESS_ENABLED=false); its ports are left to another proxy",
 			"http_port", m.cfg.GetIngressHTTPPort(),
```

- [ ] **Step 5: Run the Caddy package**

Run: `go test -race ./internal/infra/ingress/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/infra/ingress
git commit -m "feat(agent): list, create and delete domain registrations; seed them on upgrade

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Dispatcher wiring for domain.list / domain.create / domain.delete

**Files:**
- Modify: `internal/domain/port/app.go` (`IngressManager` gains three methods)
- Modify: `internal/app/agent/dispatcher.go` (three registrations + `errNoIngress`)
- Modify: `internal/app/agent/dispatcher_test.go` (`fakeIngress` implements the new methods; malformed-payload list)
- Create: `internal/app/agent/dispatcher_domains_test.go`
- Modify: `CLAUDE.md` (command surface)

**Interfaces:**
- Consumes: `(*caddy.Manager).ListDomains/CreateDomain/DeleteDomain` (Task 4); `command.*Domain*` (Task 1); `certtest.Issue/AgentKey/Encrypt`.
- Produces: `port.IngressManager` with `ListDomains(ctx) ([]command.Domain, error)`, `CreateDomain(ctx, command.CreateDomainRequest) (command.Domain, error)`, `DeleteDomain(ctx, name string) error`. Both binaries already pass `*caddy.Manager` to `NewDispatcher` (`cmd/agent/main.go`, `internal/infra/bootstrap/standalone.go`), so no wiring changes. Successful create/delete call `d.reloadIngress(ctx)`.

- [ ] **Step 1: Write the failing dispatcher tests** — `internal/app/agent/dispatcher_domains_test.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	ingresscaddy "winterflow/internal/infra/ingress/caddy"
	"winterflow/internal/infra/ingress/domains/certtest"
	dockercompose "winterflow/internal/infra/orchestrator/docker_compose"
	"winterflow/internal/infra/transport/grpc/proto"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

func dispatchJSON(t *testing.T, d *Dispatcher, typ command.Type, req any) *proto.ResponseEnvelope {
	t.Helper()
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return d.Dispatch(context.Background(), envelope(string(typ), payload))
}

func TestDispatchDomainCommands(t *testing.T) {
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	ing := &fakeIngress{domains: []command.Domain{{Name: "*.x.app", TLS: command.DomainTLSOff, Apps: []string{"a1"}}}}
	d := NewDispatcher(dockercompose.NewRepository(config.NewServerConfig("standalone"), log), ing, log)
	ok := proto.ResponseCode_RESPONSE_CODE_SUCCESS

	resp := dispatchJSON(t, d, command.TypeDomainList, command.ListDomainsRequest{})
	var list command.ListDomainsResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &list) != nil || len(list.Domains) != 1 || list.Domains[0].Apps[0] != "a1" {
		t.Fatalf("domain.list = %+v %s", resp.Base, resp.Payload)
	}
	if ing.reloads != 0 {
		t.Fatal("a read must not reload ingress")
	}

	resp = dispatchJSON(t, d, command.TypeDomainCreate, command.CreateDomainRequest{Name: "*.x.app", TLS: command.DomainTLSAuto})
	var created command.CreateDomainResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &created) != nil || created.Domain.Name != "*.x.app" {
		t.Fatalf("domain.create = %+v %s", resp.Base, resp.Payload)
	}
	resp = dispatchJSON(t, d, command.TypeDomainDelete, command.DeleteDomainRequest{Name: "*.x.app"})
	if resp.Base.ResponseCode != ok {
		t.Fatalf("domain.delete = %+v", resp.Base)
	}
	if ing.reloads != 2 {
		t.Fatalf("reloads = %d, want one per successful mutation", ing.reloads)
	}

	// Invalid payloads never reach the manager.
	for typ, req := range map[command.Type]any{
		command.TypeDomainCreate: command.CreateDomainRequest{Name: "../etc", TLS: command.DomainTLSOff},
		command.TypeDomainDelete: command.DeleteDomainRequest{Name: "a/b"},
	} {
		resp := dispatchJSON(t, d, typ, req)
		if resp.Base.ResponseCode == ok || !strings.HasPrefix(resp.Base.Detail, "invalid payload: ") {
			t.Errorf("%s: %+v", typ, resp.Base)
		}
	}
	if len(ing.created) != 1 || len(ing.deleted) != 1 {
		t.Fatalf("manager saw invalid payloads: %+v %+v", ing.created, ing.deleted)
	}

	// A refused mutation is a correlated error and does not reload.
	ing.domainErr = errors.New("*.x.app is used by Shop (a1)")
	resp = dispatchJSON(t, d, command.TypeDomainDelete, command.DeleteDomainRequest{Name: "*.x.app"})
	if resp.Base.ResponseCode == ok || resp.Base.Detail != "*.x.app is used by Shop (a1)" || resp.RequestId != "req-1" {
		t.Fatalf("refused delete = %+v", resp)
	}
	resp = dispatchJSON(t, d, command.TypeDomainCreate, command.CreateDomainRequest{Name: "y.app", TLS: command.DomainTLSOff})
	if resp.Base.ResponseCode == ok {
		t.Fatal("refused create succeeded")
	}
	if ing.reloads != 2 {
		t.Fatalf("failed mutations reloaded: %d", ing.reloads)
	}
}

func TestDispatchDomainCommandsWithoutIngress(t *testing.T) {
	d := newTestDispatcher(t)
	for typ, req := range map[command.Type]any{
		command.TypeDomainList:   command.ListDomainsRequest{},
		command.TypeDomainCreate: command.CreateDomainRequest{Name: "x.app", TLS: command.DomainTLSOff},
		command.TypeDomainDelete: command.DeleteDomainRequest{Name: "x.app"},
	} {
		if resp := dispatchJSON(t, d, typ, req); resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS {
			t.Errorf("%s succeeded without an ingress manager", typ)
		}
	}
}

// TestDispatchDomainCreateWithRealManager runs domain.create in certificate
// mode through the real dispatcher and Caddy manager (not started, so no
// ports): the browser-encrypted key is decrypted, checked and stored, and
// domain.list reports the certificate.
func TestDispatchDomainCreateWithRealManager(t *testing.T) {
	dataDir, certDir := t.TempDir(), t.TempDir()
	t.Setenv("AGENT_DATA_DIR", dataDir)
	t.Setenv("HUB_CERT_DIR", certDir)
	t.Setenv("INGRESS_ENABLED", "true")
	pub := certtest.AgentKey(t, certDir)
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	cfg := config.NewServerConfig("standalone")
	d := NewDispatcher(dockercompose.NewRepository(cfg, log), ingresscaddy.NewManager(cfg, log), log)

	cert, key := certtest.Issue(t, time.Now().Add(30*24*time.Hour), "*.twillingate.app")
	resp := dispatchJSON(t, d, command.TypeDomainCreate, command.CreateDomainRequest{
		Name: "*.twillingate.app", TLS: command.DomainTLSCertificate,
		Certificate: string(cert), Key: certtest.Encrypt(t, pub, string(key)),
	})
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("create: %+v", resp.Base)
	}
	if strings.Contains(string(resp.Payload), "PRIVATE KEY") {
		t.Fatal("the key leaked into the response")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "domains", "_wildcard_.twillingate.app.key")); err != nil {
		t.Fatal(err)
	}
	resp = dispatchJSON(t, d, command.TypeDomainList, command.ListDomainsRequest{})
	var list command.ListDomainsResponse
	if err := json.Unmarshal(resp.Payload, &list); err != nil || len(list.Domains) != 1 ||
		list.Domains[0].NotAfter == nil || list.Domains[0].Issuer != certtest.Issuer {
		t.Fatalf("list = %s (%v)", resp.Payload, err)
	}
}
```

And extend the fake plus the malformed-payload table in `internal/app/agent/dispatcher_test.go`:

```diff
diff --git a/internal/app/agent/dispatcher_test.go b/internal/app/agent/dispatcher_test.go
--- a/internal/app/agent/dispatcher_test.go
+++ b/internal/app/agent/dispatcher_test.go
@@ -26,14 +26,34 @@ func newTestDispatcher(t *testing.T) *Dispatcher {
 }
 
 // fakeIngress is a minimal port.IngressManager test double: it counts reloads
-// and returns canned warnings, standing in for the real Caddy manager.
+// and returns canned warnings, standing in for the real Caddy manager. Its
+// domain methods record calls and answer from fields.
 type fakeIngress struct {
 	reloads  int
 	warnings []string
+
+	domains   []command.Domain
+	domainErr error // returned by CreateDomain and DeleteDomain when set
+	created   []command.CreateDomainRequest
+	deleted   []string
 }
 
 func (f *fakeIngress) Reload(_ context.Context) []string { f.reloads++; return f.warnings }
 func (f *fakeIngress) Enabled() bool                     { return true }
+func (f *fakeIngress) ListDomains(context.Context) ([]command.Domain, error) {
+	return f.domains, nil
+}
+func (f *fakeIngress) CreateDomain(_ context.Context, in command.CreateDomainRequest) (command.Domain, error) {
+	f.created = append(f.created, in)
+	if f.domainErr != nil {
+		return command.Domain{}, f.domainErr
+	}
+	return command.Domain{Name: in.Name, TLS: in.TLS, Apps: []string{}}, nil
+}
+func (f *fakeIngress) DeleteDomain(_ context.Context, name string) error {
+	f.deleted = append(f.deleted, name)
+	return f.domainErr
+}
 
 func envelope(typ string, payload []byte) *proto.RequestEnvelope {
 	return &proto.RequestEnvelope{
@@ -72,6 +92,8 @@ func TestDispatchMalformedPayloadsReturnCorrelatedErrors(t *testing.T) {
 		command.TypeVolumeCreate,
 		command.TypeVolumeDelete,
 		command.TypeAgentUpdate,
+		command.TypeDomainCreate,
+		command.TypeDomainDelete,
 	} {
 		resp := d.Dispatch(context.Background(), envelope(string(typ), bad))
 		if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS {
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/app/agent/`
Expected: FAIL — `domain.list: ... unsupported command type: domain.list` (the fake compiles; the dispatcher has no handlers yet), and `TestDispatchDomainCreateWithRealManager` fails the same way.

- [ ] **Step 3: Extend the port** — `internal/domain/port/app.go`:

```diff
diff --git a/internal/domain/port/app.go b/internal/domain/port/app.go
--- a/internal/domain/port/app.go
+++ b/internal/domain/port/app.go
@@ -2,6 +2,7 @@ package port
 
 import (
 	"context"
+	"winterflow/internal/domain/command"
 	"winterflow/internal/domain/model"
 )
 
@@ -50,4 +51,12 @@ type IngressManager interface {
 	Reload(ctx context.Context) []string
 	// Enabled reports whether the proxy bound its ports at startup.
 	Enabled() bool
+	// ListDomains reports the domain registrations with the apps using
+	// each; it works with ingress disabled.
+	ListDomains(ctx context.Context) ([]command.Domain, error)
+	// CreateDomain registers or replaces a name (certificate mode: the
+	// ECIES-encrypted key is decrypted and the pair checked first).
+	CreateDomain(ctx context.Context, in command.CreateDomainRequest) (command.Domain, error)
+	// DeleteDomain removes a registration no app uses.
+	DeleteDomain(ctx context.Context, name string) error
 }
```

- [ ] **Step 4: Register the handlers** — `internal/app/agent/dispatcher.go`:

```diff
diff --git a/internal/app/agent/dispatcher.go b/internal/app/agent/dispatcher.go
--- a/internal/app/agent/dispatcher.go
+++ b/internal/app/agent/dispatcher.go
@@ -6,6 +6,7 @@ package agent
 
 import (
 	"context"
+	"errors"
 	"fmt"
 	"strings"
 
@@ -49,6 +50,10 @@ func (d *Dispatcher) reloadIngress(ctx context.Context) []string {
 	return warnings
 }
 
+// errNoIngress answers domain commands on a dispatcher built without an
+// ingress manager (tests); production always wires one.
+var errNoIngress = errors.New("ingress is not available on this agent")
+
 // Dispatch decodes the request payload by its command type, runs the matching
 // handler, and returns the response envelope. Decode/handler errors become an
 // error ResponseEnvelope so the caller always gets a correlated reply.
@@ -210,6 +215,34 @@ func (d *Dispatcher) newHandlers() map[command.Type]handlerFunc {
 			return command.DeleteVolumeResponse{Name: in.Name}, d.orch.DeleteVolume(ctx, in.Name)
 		}),
 
+		command.TypeDomainList: handle(d, func(ctx context.Context, _ command.ListDomainsRequest) (command.ListDomainsResponse, error) {
+			if d.ingress == nil {
+				return command.ListDomainsResponse{}, errNoIngress
+			}
+			doms, err := d.ingress.ListDomains(ctx)
+			return command.ListDomainsResponse{Domains: doms}, err
+		}),
+		command.TypeDomainCreate: handle(d, func(ctx context.Context, in command.CreateDomainRequest) (command.CreateDomainResponse, error) {
+			if d.ingress == nil {
+				return command.CreateDomainResponse{}, errNoIngress
+			}
+			dom, err := d.ingress.CreateDomain(ctx, in)
+			if err == nil {
+				d.reloadIngress(ctx)
+			}
+			return command.CreateDomainResponse{Domain: dom}, err
+		}),
+		command.TypeDomainDelete: handle(d, func(ctx context.Context, in command.DeleteDomainRequest) (command.DeleteDomainResponse, error) {
+			if d.ingress == nil {
+				return command.DeleteDomainResponse{}, errNoIngress
+			}
+			err := d.ingress.DeleteDomain(ctx, in.Name)
+			if err == nil {
+				d.reloadIngress(ctx)
+			}
+			return command.DeleteDomainResponse{Name: in.Name}, err
+		}),
+
 		command.TypeImageTags: handle(d, func(ctx context.Context, in command.ImageTagsRequest) (command.ImageTagsResponse, error) {
 			tags, err := d.orch.ImageTags(ctx, in.Image)
 			return command.ImageTagsResponse{Image: in.Image, Tags: tags}, err
```

- [ ] **Step 5: Document the command surface** — `CLAUDE.md`:

```diff
diff --git a/CLAUDE.md b/CLAUDE.md
--- a/CLAUDE.md
+++ b/CLAUDE.md
@@ -42,7 +42,9 @@ This is the **v2 monorepo** that merges the v1 `winterflow-agent` and `winterflo
 
 **Single gRPC envelope + typed commands** (the deliberate simplification over v1's ~14 oneof RPC pairs): all hub↔agent traffic rides one envelope (`proto.RequestEnvelope`/`ResponseEnvelope` = a `type` string + JSON `payload`). The catalog of command types and payload structs lives in `internal/domain/command`; `internal/infra/transport/codec` is the ONLY place that (de)serializes payloads. **To add a feature:** define a command type + payload struct in `internal/domain/command`, add one registration line in the agent Dispatcher's `newHandlers` map (`internal/app/agent/dispatcher.go`) plus the orchestrator method, and add a usecase/handler + route on the API side. Routes live in typed tables: each handler package's `Routes(g api.Guards) []api.Route` (`internal/app/web/api`) is both what `routes.go` registers with chi (`api.Register`, fail-closed on nil guards) and what `internal/app/web/api/openapi` renders into `api/openapi.yaml`; `api.Guards` carries only the auth rate limiter (`g.AuthLimit`) — there is no role guard. Run `make openapi` after any route or response-type change. Response-side schemas require every non-`omitempty` field and allow `null` for nil-able kinds; a type also used in a request body gets a separate lenient `<Name>Input` component. A new SSE notification type needs an `api.EventPayload` entry in `handler/notification/routes.go`. Add a contract test in `internal/app/web/contract` (happy path, plus a `probes` entry when the route takes input).
 
-**Implemented command surface** (the v1→v2 migration is complete for these): app lifecycle — `app.save` (create/edit, new revision + redeploy), `app.get`, `apps.list` (reconcile), `apps.status`, `app.control` (start/stop/restart/update), `app.delete` (optional `delete_volumes` also removes the app's named volumes), `app.rename`, `app.logs`; Docker resources — `registry.list/create/delete`, `network.list/create/delete`, `volume.list/create/delete`; agent — `agent.update` (self-update). All are agent-bound (202 + `request_id`, result over SSE).
+**Implemented command surface** (the v1→v2 migration is complete for these): app lifecycle — `app.save` (create/edit, new revision + redeploy), `app.get`, `apps.list` (reconcile), `apps.status`, `app.control` (start/stop/restart/update), `app.delete` (optional `delete_volumes` also removes the app's named volumes), `app.rename`, `app.logs`; Docker resources — `registry.list/create/delete`, `network.list/create/delete`, `volume.list/create/delete`; ingress — `domain.list/create/delete` (registered domains, see below); agent — `agent.update` (self-update). All are agent-bound (202 + `request_id`, result over SSE).
+
+**Registered domains:** every ingress hostname must be covered by a domain registered on the same server (`{AGENT_DATA_DIR}/domains/`, mirrored into the API's `server_domains`): an exact name or `*.suffix` (one label; exact wins), each with a TLS mode — `auto` (ACME per host), `certificate` (uploaded PEM pair, key ECIES-encrypted like registry passwords, loaded via `load_files`) or `off` (plain HTTP behind a tunnel/proxy). The ingress `ssl` flag is ignored. `save-app` 400s on uncovered or per-server-duplicate hosts; the agent excludes uncovered hosts again in `BuildConfig`. The first start without `domains/` registers existing hostnames once (`ssl:true` → auto, else off).
 
 **App secrets are ECIES-encrypted end-to-end** (`pkg/crypto`, P-256 ECDH→SHA-256→AES-256-GCM): the browser encrypts a secret with the agent's public key (served by `GET /api/v1/server/get-public-key`, sourced from the `public_key` capability or the local agent cert in standalone); the agent decrypts with its mTLS private key before render. Editing sends the `<encrypted>` placeholder for unchanged secrets (the agent preserves the stored value, never returns plaintext). Registry passwords use the same scheme.
 
```

- [ ] **Step 6: Run the agent tests and the whole build**

Run: `go build ./... && go test -race ./internal/app/agent/ ./internal/infra/transport/...`
Expected: PASS (the hub e2e test still builds its dispatcher with a nil ingress manager).

- [ ] **Step 7: Commit**

```bash
git add internal/domain/port/app.go internal/app/agent CLAUDE.md
git commit -m "feat(agent): dispatch domain.list, domain.create and domain.delete

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: A hostname is unique per server

`app_domains` was keyed by the hostname alone (global uniqueness). The key becomes `(server_id, domain)`: apps on different servers may serve the same hostname (DNS round robin), one server still cannot have two. Claims are looked up on the target server only, so `GET /api/v1/domains/check` needs `server_id` — which makes it a server-addressed route with the ownership guard and a `valid` probe.

**Files:**
- Create: `internal/infra/db/migrations/20261010000002_app_domains_per_server.go`
- Modify: `internal/infra/db/models/models.go` (`AppDomain`)
- Modify: `internal/infra/db/repository/app_domain.go`, `app_domain_test.go`
- Modify: `internal/domain/port/app.go` (`FindClaims` signature)
- Modify: `internal/domain/usecase/app/usecase.go`, `usecase_test.go`
- Modify: `internal/app/web/handler/app/check_domain.go`, `check_domain_test.go`, `routes.go`
- Modify: `internal/app/web/contract/matrix_test.go`, `apps_test.go`, `migration_test.go`
- Regenerate: `api/openapi.yaml`

**Interfaces:**
- Produces:
  - `port.AppDomainRepository.FindClaims(ctx, serverID string, domains []string, excludeAppID string) ([]model.DomainClaim, error)`
  - `(*app.UseCase).CheckDomain(ctx, serverID, domain, excludeAppID string) ([]model.DomainClaim, error)` (Task 8 changes the return type)
  - `GET /api/v1/domains/check?server_id=&domain=&app_id=` — `server_id` required (400), foreign server 403
  - contract helper `(*client).registerServer(t) string` (claims one more pending server into the caller's org)

- [ ] **Step 1: Write the failing tests**

Repository — per-server claims (`internal/infra/db/repository/app_domain_test.go`):

```diff
diff --git a/internal/infra/db/repository/app_domain_test.go b/internal/infra/db/repository/app_domain_test.go
--- a/internal/infra/db/repository/app_domain_test.go
+++ b/internal/infra/db/repository/app_domain_test.go
@@ -46,7 +46,7 @@ func TestReplaceAndFindClaims(t *testing.T) {
 	}
 
 	// Another app asking for a held domain conflicts, with names attached.
-	claims, err := repo.FindClaims(ctx, []string{"blog.example.com", "free.example.com"}, "app-2")
+	claims, err := repo.FindClaims(ctx, "srv-1", []string{"blog.example.com", "free.example.com"}, "app-2")
 	if err != nil {
 		t.Fatal(err)
 	}
@@ -55,7 +55,7 @@ func TestReplaceAndFindClaims(t *testing.T) {
 	}
 
 	// The owner re-saving is not a conflict.
-	claims, _ = repo.FindClaims(ctx, []string{"blog.example.com"}, "app-1")
+	claims, _ = repo.FindClaims(ctx, "srv-1", []string{"blog.example.com"}, "app-1")
 	if len(claims) != 0 {
 		t.Fatalf("own claims reported as conflicts: %+v", claims)
 	}
@@ -64,7 +64,7 @@ func TestReplaceAndFindClaims(t *testing.T) {
 	if err := repo.ReplaceForApp(ctx, "app-1", "srv-1", ingressOf(model.IngressDomain{Domain: "blog.example.com", UpstreamPort: 8088, SSL: true})); err != nil {
 		t.Fatal(err)
 	}
-	claims, _ = repo.FindClaims(ctx, []string{"www.example.com"}, "app-2")
+	claims, _ = repo.FindClaims(ctx, "srv-1", []string{"www.example.com"}, "app-2")
 	if len(claims) != 0 {
 		t.Fatalf("stale row survived replace: %+v", claims)
 	}
@@ -153,3 +153,35 @@ func TestReplaceForServerDedupsDuplicateDomain(t *testing.T) {
 		t.Fatalf("sorted-first app did not keep the domain: %+v", list)
 	}
 }
+
+// TestHostnamesAreUniquePerServer: two servers may index the same hostname
+// (DNS round robin); claims are looked up on one server only.
+func TestHostnamesAreUniquePerServer(t *testing.T) {
+	repo, apps := newDomainRepo(t)
+	ctx := context.Background()
+	seedOrgWithServer(t, repo.db, "org-2", "srv-2")
+	seedApp(t, apps, "app-1", "One")
+	if err := apps.SaveApp(ctx, model.App{ID: "app-2", ServerID: "srv-2", Name: "Two", Icon: "i", Color: "#000000"}); err != nil {
+		t.Fatal(err)
+	}
+	shared := ingressOf(model.IngressDomain{Domain: "shop.example.com", UpstreamPort: 80})
+	if err := repo.ReplaceForApp(ctx, "app-1", "srv-1", shared); err != nil {
+		t.Fatal(err)
+	}
+	if err := repo.ReplaceForApp(ctx, "app-2", "srv-2", shared); err != nil {
+		t.Fatalf("second server could not index the same hostname: %v", err)
+	}
+	for server, owner := range map[string]string{"srv-1": "One", "srv-2": "Two"} {
+		claims, err := repo.FindClaims(ctx, server, []string{"shop.example.com"}, "app-new")
+		if err != nil || len(claims) != 1 || claims[0].AppName != owner || claims[0].ServerID != server {
+			t.Fatalf("%s claims = %+v, %v", server, claims, err)
+		}
+	}
+	// A reconcile of one server leaves the other's row alone.
+	if err := repo.ReplaceForServer(ctx, "srv-1", nil); err != nil {
+		t.Fatal(err)
+	}
+	if claims, _ := repo.FindClaims(ctx, "srv-2", []string{"shop.example.com"}, ""); len(claims) != 1 {
+		t.Fatalf("srv-2 row lost: %+v", claims)
+	}
+}
```

Usecase — claims are looked up on the target server (`internal/domain/usecase/app/usecase_test.go`):

```diff
diff --git a/internal/domain/usecase/app/usecase_test.go b/internal/domain/usecase/app/usecase_test.go
--- a/internal/domain/usecase/app/usecase_test.go
+++ b/internal/domain/usecase/app/usecase_test.go
@@ -90,11 +90,13 @@ type fakeDomainRepo struct {
 		serverID string
 		ing      *model.Ingress
 	}
-	deleted string
-	synced  string
+	deleted     string
+	synced      string
+	claimServer string // serverID of the last FindClaims
 }
 
-func (f *fakeDomainRepo) FindClaims(_ context.Context, _ []string, _ string) ([]model.DomainClaim, error) {
+func (f *fakeDomainRepo) FindClaims(_ context.Context, serverID string, _ []string, _ string) ([]model.DomainClaim, error) {
+	f.claimServer = serverID
 	return f.claims, nil
 }
 func (f *fakeDomainRepo) ReplaceForApp(_ context.Context, appID, serverID string, ing *model.Ingress) error {
@@ -147,6 +149,9 @@ func TestSaveAppRejectsTakenDomain(t *testing.T) {
 	if !errors.Is(err, model.ErrDomainTaken) {
 		t.Fatalf("err = %v, want ErrDomainTaken", err)
 	}
+	if repo.claimServer != "s" {
+		t.Fatalf("claims looked up on server %q, want the target server s", repo.claimServer)
+	}
 	for _, part := range []string{"blog.example.com", "Ghost", "hetzner-1"} {
 		if !strings.Contains(err.Error(), part) {
 			t.Fatalf("error %q missing %q", err, part)
@@ -230,12 +235,12 @@ func TestCheckDomain(t *testing.T) {
 	repo := &fakeDomainRepo{claims: []model.DomainClaim{{Domain: "x.example.com", AppName: "Other"}}}
 	uc := newTestUseCaseWithDomains(&captureDispatcher{}, repo)
 
-	claims, err := uc.CheckDomain(context.Background(), "x.example.com", "app-1")
-	if err != nil || len(claims) != 1 {
-		t.Fatalf("CheckDomain = %v, %v", claims, err)
+	claims, err := uc.CheckDomain(context.Background(), "srv-1", "x.example.com", "app-1")
+	if err != nil || len(claims) != 1 || repo.claimServer != "srv-1" {
+		t.Fatalf("CheckDomain = %v, %v (server %q)", claims, err, repo.claimServer)
 	}
 
-	if _, err := uc.CheckDomain(context.Background(), "NOT A HOST", ""); !errors.Is(err, model.ErrIngressInvalid) {
+	if _, err := uc.CheckDomain(context.Background(), "srv-1", "NOT A HOST", ""); !errors.Is(err, model.ErrIngressInvalid) {
 		t.Fatalf("err = %v, want ErrIngressInvalid", err)
 	}
 }
```

Handler — the check needs an owned server (`internal/app/web/handler/app/check_domain_test.go`):

```diff
diff --git a/internal/app/web/handler/app/check_domain_test.go b/internal/app/web/handler/app/check_domain_test.go
--- a/internal/app/web/handler/app/check_domain_test.go
+++ b/internal/app/web/handler/app/check_domain_test.go
@@ -9,7 +9,7 @@ import (
 
 func TestCheckDomainAvailable(t *testing.T) {
 	h, _, _ := newHandler(t)
-	w := do(h.CheckDomain, authed(httptest.NewRequest("GET", "/api/v1/domains/check?domain=app.example.com", nil)))
+	w := do(h.CheckDomain, authed(httptest.NewRequest("GET", "/api/v1/domains/check?server_id=s1&domain=app.example.com", nil)))
 	if w.Code != http.StatusOK {
 		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
 	}
@@ -23,3 +23,16 @@ func TestCheckDomainAvailable(t *testing.T) {
 		t.Fatalf("data = %+v, want available with no claims", body.Data)
 	}
 }
+
+func TestCheckDomainNeedsAnOwnedServer(t *testing.T) {
+	h, _, _ := newHandler(t)
+	if w := do(h.CheckDomain, authed(httptest.NewRequest("GET", "/api/v1/domains/check?domain=app.example.com", nil))); w.Code != http.StatusBadRequest {
+		t.Fatalf("missing server_id = %d, want 400", w.Code)
+	}
+	if w := do(h.CheckDomain, authed(httptest.NewRequest("GET", "/api/v1/domains/check?server_id=other&domain=app.example.com", nil))); w.Code != http.StatusForbidden {
+		t.Fatalf("foreign server = %d, want 403", w.Code)
+	}
+	if w := do(h.CheckDomain, httptest.NewRequest("GET", "/api/v1/domains/check?server_id=s1&domain=app.example.com", nil)); w.Code != http.StatusUnauthorized {
+		t.Fatalf("anonymous = %d, want 401", w.Code)
+	}
+}
```

Contract — the probe, the per-server scenario, and the migration replayed on both dialects (`matrix_test.go`, `apps_test.go`, `migration_test.go`). `TestAppDomainsPerServerKeyMigration` migrates a fresh database only up to the migration before this one (a migrator over the older subset), inserts rows under the old key, proves the old key rejects a second server, then runs the full set:

```diff
diff --git a/internal/app/web/contract/matrix_test.go b/internal/app/web/contract/matrix_test.go
--- a/internal/app/web/contract/matrix_test.go
+++ b/internal/app/web/contract/matrix_test.go
@@ -62,8 +62,9 @@ var probes = map[string]probe{
 	"POST /api/v1/app/rename-app": {valid: func(s string) call {
 		return post("/api/v1/app/rename-app", map[string]any{"server_id": s, "app_id": anApp, "name": "y"})
 	}, malformed: bad("POST", "/api/v1/app/rename-app")},
-	"GET /api/v1/domains/check": {malformed: call{Method: http.MethodGet, Path: "/api/v1/domains/check",
-		Query: url.Values{"domain": {"not a domain"}}}},
+	"GET /api/v1/domains/check": {valid: func(s string) call {
+		return get("/api/v1/domains/check", url.Values{"server_id": {s}, "domain": {"free.example.test"}})
+	}, malformed: bad("GET", "/api/v1/domains/check")},
 
 	"GET /api/v1/registry/list": {valid: func(s string) call { return get("/api/v1/registry/list", sq(s)) }, malformed: bad("GET", "/api/v1/registry/list")},
 	"POST /api/v1/registry/create": {valid: func(s string) call {
```

```diff
diff --git a/internal/app/web/contract/apps_test.go b/internal/app/web/contract/apps_test.go
--- a/internal/app/web/contract/apps_test.go
+++ b/internal/app/web/contract/apps_test.go
@@ -7,6 +7,7 @@ import (
 	"testing"
 
 	happ "winterflow/internal/app/web/handler/app"
+	"winterflow/internal/app/web/handler/server"
 	"winterflow/internal/domain/command"
 	"winterflow/internal/domain/model"
 	"winterflow/internal/infra/transport/bus"
@@ -57,15 +58,18 @@ func TestAppLifecycle(t *testing.T) {
 
 	t.Run("domain check sees the claim", func(t *testing.T) {
 		var d happ.CheckDomainResponse
-		owner.get(t, "/api/v1/domains/check", url.Values{"domain": {"web.example.test"}}).expect(t, http.StatusOK).data(t, &d)
+		owner.get(t, "/api/v1/domains/check", url.Values{"server_id": {sid}, "domain": {"web.example.test"}}).expect(t, http.StatusOK).data(t, &d)
 		if d.Available || len(d.Claims) != 1 || d.Claims[0].AppName != "web" {
 			t.Fatalf("check = %+v", d)
 		}
-		owner.get(t, "/api/v1/domains/check", url.Values{"domain": {"free.example.test"}}).expect(t, http.StatusOK).data(t, &d)
+		owner.get(t, "/api/v1/domains/check", url.Values{"server_id": {sid}, "domain": {"free.example.test"}}).expect(t, http.StatusOK).data(t, &d)
 		if !d.Available {
 			t.Fatalf("free domain = %+v", d)
 		}
 	})
+	t.Run("the check rejects a malformed hostname", func(t *testing.T) {
+		owner.get(t, "/api/v1/domains/check", url.Values{"server_id": {sid}, "domain": {"not a domain"}}).expect(t, http.StatusBadRequest)
+	})
 	t.Run("a second app cannot take the domain", func(t *testing.T) {
 		owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
 			ServerID: sid, App: model.App{Name: "dup", Icon: "box", Color: "#112233"}, Config: ingress,
@@ -132,7 +136,7 @@ func TestAppLifecycle(t *testing.T) {
 			t.Fatalf("after delete: %+v", apps)
 		}
 		var d happ.CheckDomainResponse
-		owner.get(t, "/api/v1/domains/check", url.Values{"domain": {"web.example.test"}}).expect(t, http.StatusOK).data(t, &d)
+		owner.get(t, "/api/v1/domains/check", url.Values{"server_id": {sid}, "domain": {"web.example.test"}}).expect(t, http.StatusOK).data(t, &d)
 		if !d.Available {
 			t.Fatalf("domain still claimed: %+v", d)
 		}
@@ -166,3 +170,37 @@ func TestAppAgentErrors(t *testing.T) {
 		t.Fatalf("failed commands changed the DB: %+v", apps)
 	}
 }
+
+// registerServer claims one more pending server into c's org and returns
+// its id.
+func (c *client) registerServer(t *testing.T) string {
+	t.Helper()
+	var reg server.RegisterServerResponse
+	c.post(t, "/api/v1/server/register", server.RegisterServerRequest{Code: c.e.seedPendingServer(t)}).
+		expect(t, http.StatusOK).data(t, &reg)
+	return reg.Server.ID
+}
+
+// TestHostnameUniquePerServer: apps on different servers may serve the same
+// hostname (DNS round robin); on one server it stays exclusive.
+func TestHostnameUniquePerServer(t *testing.T) {
+	t.Parallel()
+	e := newEnv(t)
+	owner := e.claim(t)
+	first := owner.serverID(t)
+	second := owner.registerServer(t)
+	shop := json.RawMessage(`{"ingress":{"domains":[{"domain":"shop.example.test","upstream_port":8080}]}}`)
+
+	saveApp(t, owner, first, "shop-a", shop)
+	saveApp(t, owner, second, "shop-b", shop)
+
+	var d happ.CheckDomainResponse
+	owner.get(t, "/api/v1/domains/check", url.Values{"server_id": {second}, "domain": {"shop.example.test"}}).
+		expect(t, http.StatusOK).data(t, &d)
+	if d.Available || len(d.Claims) != 1 || d.Claims[0].AppName != "shop-b" || d.Claims[0].ServerID != second {
+		t.Fatalf("claims on the second server = %+v", d)
+	}
+	owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
+		ServerID: first, App: model.App{Name: "shop-c", Icon: "box", Color: "#112233"}, Config: shop,
+	}}).expect(t, http.StatusBadRequest)
+}
```

```diff
diff --git a/internal/app/web/contract/migration_test.go b/internal/app/web/contract/migration_test.go
--- a/internal/app/web/contract/migration_test.go
+++ b/internal/app/web/contract/migration_test.go
@@ -3,16 +3,97 @@ package contract_test
 import (
 	"context"
 	"database/sql"
+	"strings"
 	"testing"
 
 	"github.com/uptrace/bun"
 	"github.com/uptrace/bun/dialect/pgdialect"
+	"github.com/uptrace/bun/dialect/sqlitedialect"
 	"github.com/uptrace/bun/driver/pgdriver"
+	"github.com/uptrace/bun/driver/sqliteshim"
 	"github.com/uptrace/bun/migrate"
 
 	"winterflow/internal/infra/db/migrations"
 )
 
+// openBun opens a fresh database of the run's dialect without migrating it.
+func openBun(t *testing.T) *bun.DB {
+	t.Helper()
+	dsn := freshDB(t)
+	var db *bun.DB
+	if dialect(t) == "postgres" {
+		db = bun.NewDB(sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn))), pgdialect.New())
+	} else {
+		sqldb, err := sql.Open(sqliteshim.ShimName, strings.TrimPrefix(dsn, "sqlite://"))
+		if err != nil {
+			t.Fatal(err)
+		}
+		db = bun.NewDB(sqldb, sqlitedialect.New())
+	}
+	t.Cleanup(func() { _ = db.Close() })
+	return db
+}
+
+// migrateBefore applies every migration named below name (the schema an
+// install had before it), on a migrator of its own.
+func migrateBefore(t *testing.T, ctx context.Context, db *bun.DB, name string) {
+	t.Helper()
+	older := migrate.NewMigrations()
+	for _, m := range migrations.Migrations.Sorted() {
+		if m.Name < name {
+			older.Add(m)
+		}
+	}
+	m := migrate.NewMigrator(db, older)
+	if err := m.Init(ctx); err != nil {
+		t.Fatal(err)
+	}
+	if _, err := m.Migrate(ctx); err != nil {
+		t.Fatal(err)
+	}
+}
+
+// TestAppDomainsPerServerKeyMigration upgrades an install whose app_domains
+// key is the hostname alone: every row survives, and afterwards a second
+// server may index the same hostname while one server still may not.
+func TestAppDomainsPerServerKeyMigration(t *testing.T) {
+	t.Parallel()
+	ctx := context.Background()
+	db := openBun(t)
+	migrateBefore(t, ctx, db, "20261010000002")
+
+	const srvA, srvB = "0192a3b4-c5d6-7e8f-9a0b-1c2d3e4f5a01", "0192a3b4-c5d6-7e8f-9a0b-1c2d3e4f5a02"
+	const appA, appB = "0192a3b4-c5d6-7e8f-9a0b-1c2d3e4f5a11", "0192a3b4-c5d6-7e8f-9a0b-1c2d3e4f5a12"
+	insert := func(domain, app, srv string) error {
+		_, err := db.ExecContext(ctx, `INSERT INTO app_domains (domain, app_id, server_id, kind, updated_at)
+			VALUES (?, ?, ?, 'route', '2026-10-10 12:00:00')`, domain, app, srv)
+		return err
+	}
+	for _, d := range []string{"keep.test", "also.test"} {
+		if err := insert(d, appA, srvA); err != nil {
+			t.Fatal(err)
+		}
+	}
+	if err := insert("keep.test", appB, srvB); err == nil {
+		t.Fatal("the old key accepted a hostname twice; the test does not start from the old schema")
+	}
+
+	m := migrate.NewMigrator(db, migrations.Migrations)
+	if _, err := m.Migrate(ctx); err != nil {
+		t.Fatalf("migrate: %v", err)
+	}
+	var n int
+	if err := db.NewRaw(`SELECT count(*) FROM app_domains`).Scan(ctx, &n); err != nil || n != 2 {
+		t.Fatalf("rows after migration = %d, %v; want 2", n, err)
+	}
+	if err := insert("keep.test", appB, srvB); err != nil {
+		t.Fatalf("second server cannot index the same hostname: %v", err)
+	}
+	if err := insert("keep.test", appB, srvA); err == nil {
+		t.Fatal("one server indexed a hostname twice")
+	}
+}
+
 // TestAppDomainsUUIDMigrationSurvivesJunkRows replays the app_domains UUID
 // migration over an install whose ids are CHAR(36) and include a row that is
 // not a UUID: the migration must drop it instead of aborting startup.
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go vet ./internal/... ; go test ./internal/app/web/contract/ -run 'PerServer|UniquePerServer'`
Expected: vet reports `too many arguments in call to repo.FindClaims` / `uc.CheckDomain`; once those compile, `TestAppDomainsPerServerKeyMigration` fails with `second server cannot index the same hostname: ... UNIQUE constraint failed` (SQLite) and `TestHostnameUniquePerServer` gets `status = 400` on the second server's save.

- [ ] **Step 3: Migrate the key** — `internal/infra/db/migrations/20261010000002_app_domains_per_server.go`:

```go
package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
)

func init() {
	Migrations.MustRegister(appDomainsPerServerKey, appDomainsPerServerKeyDown)
}

// appDomainsPerServerKey makes a hostname unique per server instead of
// globally: the primary key moves from (domain) to (server_id, domain), so
// apps on different servers may claim the same hostname (DNS round robin)
// while two apps on one server still cannot. Every existing row satisfies
// the new key, so all rows are kept.
func appDomainsPerServerKey(ctx context.Context, db *bun.DB) error {
	if db.Dialect().Name() == dialect.PG {
		_, err := db.ExecContext(ctx,
			`ALTER TABLE app_domains DROP CONSTRAINT app_domains_pkey, ADD PRIMARY KEY (server_id, domain)`)
		return err
	}
	// SQLite cannot alter a primary key: rebuild the table.
	stmts := []string{
		`CREATE TABLE app_domains_new (
			domain VARCHAR(253) NOT NULL,
			app_id CHAR(36) NOT NULL,
			server_id CHAR(36) NOT NULL,
			kind VARCHAR(16) NOT NULL,
			ssl BOOLEAN NOT NULL DEFAULT FALSE,
			upstream_port INTEGER NOT NULL DEFAULT 0,
			target VARCHAR(2048) NOT NULL DEFAULT '',
			code INTEGER NOT NULL DEFAULT 0,
			updated_at TIMESTAMP NOT NULL,
			PRIMARY KEY (server_id, domain)
		)`,
		`INSERT INTO app_domains_new (domain, app_id, server_id, kind, ssl, upstream_port, target, code, updated_at)
			SELECT domain, app_id, server_id, kind, ssl, upstream_port, target, code, updated_at FROM app_domains`,
		`DROP TABLE app_domains`,
		`ALTER TABLE app_domains_new RENAME TO app_domains`,
		"CREATE INDEX idx_app_domains_app_id ON app_domains (app_id)",
		"CREATE INDEX idx_app_domains_server_id ON app_domains (server_id)",
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("app_domains per-server key: %w", err)
		}
	}
	return nil
}

// appDomainsPerServerKeyDown is a no-op: the global key cannot be restored
// once two servers share a hostname, and the index is rebuildable anyway.
func appDomainsPerServerKeyDown(context.Context, *bun.DB) error { return nil }
```

Model (`internal/infra/db/models/models.go`):

```diff
diff --git a/internal/infra/db/models/models.go b/internal/infra/db/models/models.go
--- a/internal/infra/db/models/models.go
+++ b/internal/infra/db/models/models.go
@@ -160,12 +160,12 @@ type App struct {
 type AppDomain struct {
 	bun.BaseModel `bun:"table:app_domains"`
 
-	// Domain is the PK: lowercased FQDN. The PK is the global uniqueness
-	// constraint that makes cross-app/cross-server conflicts impossible to
-	// persist, not just impolite.
+	// (ServerID, Domain) is the PK: a hostname is unique per server, so two
+	// apps on one agent can never both persist it, while apps on different
+	// servers may share it (DNS round robin).
 	Domain       string         `bun:"domain,pk" json:"domain"`
 	AppID        string         `bun:"app_id,notnull,type:char(36)" json:"app_id"`
-	ServerID     string         `bun:"server_id,notnull,type:char(36)" json:"server_id"`
+	ServerID     string         `bun:"server_id,pk,type:char(36)" json:"server_id"`
 	Kind         string         `bun:"kind,notnull" json:"kind"` // "route" | "redirect"
 	SSL          bool           `bun:"ssl,notnull" json:"ssl"`
 	UpstreamPort int            `bun:"upstream_port,notnull,default:0" json:"upstream_port"` // routes only
```

- [ ] **Step 4: Claims per server** — repository, port, usecase:

```diff
diff --git a/internal/infra/db/repository/app_domain.go b/internal/infra/db/repository/app_domain.go
--- a/internal/infra/db/repository/app_domain.go
+++ b/internal/infra/db/repository/app_domain.go
@@ -51,7 +51,7 @@ func rowsFor(appID, serverID string, ing *model.Ingress) []models.AppDomain {
 	return rows
 }
 
-func (r *DbAppDomainRepository) FindClaims(ctx context.Context, domains []string, excludeAppID string) ([]model.DomainClaim, error) {
+func (r *DbAppDomainRepository) FindClaims(ctx context.Context, serverID string, domains []string, excludeAppID string) ([]model.DomainClaim, error) {
 	if len(domains) == 0 {
 		return nil, nil
 	}
@@ -60,6 +60,7 @@ func (r *DbAppDomainRepository) FindClaims(ctx context.Context, domains []string
 		Model(&rows).
 		Relation("App").
 		Relation("Server").
+		Where("app_domain.server_id = ?", serverID).
 		Where("app_domain.domain IN (?)", bun.In(domains))
 	if excludeAppID != "" {
 		q = q.Where("app_domain.app_id != ?", excludeAppID)
@@ -105,7 +106,7 @@ func (r *DbAppDomainRepository) DeleteForApp(ctx context.Context, appID string)
 }
 
 // ReplaceForServer rebuilds the whole server's index from the agent's
-// reported apps (apps.list reconcile). The `domain` column is a PK, so two
+// reported apps (apps.list reconcile). (server_id, domain) is the PK, so two
 // apps claiming the same domain would collide on INSERT and roll back the
 // whole reconcile, leaving the table stale forever with no way to self-heal.
 // The agent can legitimately reach that state for a moment (BuildConfig in
```

```diff
diff --git a/internal/domain/port/app.go b/internal/domain/port/app.go
--- a/internal/domain/port/app.go
+++ b/internal/domain/port/app.go
@@ -25,10 +25,11 @@ type AppRepository interface {
 // claims. The repository reconciles app config blobs via ReplaceForApp or
 // ReplaceForServer; external APIs query for domain conflicts via FindClaims.
 type AppDomainRepository interface {
-	// FindClaims returns rows holding any of the given domains, excluding the
-	// app being saved (its own claims are not conflicts). Claims carry app and
-	// server names for the error message.
-	FindClaims(ctx context.Context, domains []string, excludeAppID string) ([]model.DomainClaim, error)
+	// FindClaims returns rows on serverID holding any of the given domains,
+	// excluding the app being saved (its own claims are not conflicts). A
+	// hostname is unique per server, so other servers' claims never count.
+	// Claims carry app and server names for the error message.
+	FindClaims(ctx context.Context, serverID string, domains []string, excludeAppID string) ([]model.DomainClaim, error)
 	// ReplaceForApp makes the index mirror the app's ingress: delete the
 	// app's rows, insert the new set, one transaction. nil ingress = no-op
 	// (old clients); empty ingress = rows removed.
```

```diff
diff --git a/internal/domain/usecase/app/usecase.go b/internal/domain/usecase/app/usecase.go
--- a/internal/domain/usecase/app/usecase.go
+++ b/internal/domain/usecase/app/usecase.go
@@ -226,9 +226,10 @@ func (uc *UseCase) GetLogs(ctx context.Context, userID, serverID, appID string,
 	})
 }
 
-// CheckDomain reports which app (if any) already claims a hostname — the
-// live-typing availability check behind GET /api/v1/domains/check.
-func (uc *UseCase) CheckDomain(ctx context.Context, domain, excludeAppID string) ([]model.DomainClaim, error) {
+// CheckDomain reports which app (if any) on serverID already claims a
+// hostname — the live-typing availability check behind GET
+// /api/v1/domains/check. A hostname is unique per server only.
+func (uc *UseCase) CheckDomain(ctx context.Context, serverID, domain, excludeAppID string) ([]model.DomainClaim, error) {
 	probe := model.Ingress{Domains: []model.IngressDomain{{Domain: domain, UpstreamPort: 1}}}
 	if err := probe.Validate(); err != nil {
 		return nil, fmt.Errorf("%w: %v", model.ErrIngressInvalid, err)
@@ -236,7 +237,7 @@ func (uc *UseCase) CheckDomain(ctx context.Context, domain, excludeAppID string)
 	if uc.domains == nil {
 		return nil, nil
 	}
-	return uc.domains.FindClaims(ctx, []string{domain}, excludeAppID)
+	return uc.domains.FindClaims(ctx, serverID, []string{domain}, excludeAppID)
 }
 
 // SaveApp dispatches an app.save command to the server's agent and returns
@@ -274,7 +275,7 @@ func (uc *UseCase) SaveApp(ctx context.Context, userID, serverID string, app mod
 			return "", fmt.Errorf("%w: %v", model.ErrIngressInvalid, err)
 		}
 		if uc.domains != nil {
-			claims, err := uc.domains.FindClaims(ctx, ing.DomainNames(), app.ID)
+			claims, err := uc.domains.FindClaims(ctx, serverID, ing.DomainNames(), app.ID)
 			if err != nil {
 				uc.log.Error("SaveApp: find domain claims", "error", err, "app_id", app.ID)
 				return "", err
```

- [ ] **Step 5: The check route takes `server_id`** — handler and route table:

```diff
diff --git a/internal/app/web/handler/app/check_domain.go b/internal/app/web/handler/app/check_domain.go
--- a/internal/app/web/handler/app/check_domain.go
+++ b/internal/app/web/handler/app/check_domain.go
@@ -8,21 +8,31 @@ import (
 )
 
 // CheckDomainResponse is the data of GET /api/v1/domains/check. Claims lists
-// the apps already using the hostname (null when there are none).
+// the apps on the server already using the hostname (null when there are
+// none).
 type CheckDomainResponse struct {
 	Available bool                `json:"available"`
 	Claims    []model.DomainClaim `json:"claims"`
 }
 
-// CheckDomain answers "is this hostname free?" for the app editor's
-// live-typing validation. app_id (optional) excludes the app being edited so
-// its own claims don't read as conflicts.
+// CheckDomain answers "is this hostname free on this server?" for the app
+// editor's live-typing validation. app_id (optional) excludes the app being
+// edited so its own claims don't read as conflicts.
 func (h *Handler) CheckDomain(w http.ResponseWriter, r *http.Request) {
-	if _, ok := webutil.RequireUser(w, r); !ok {
+	userID, ok := webutil.RequireUser(w, r)
+	if !ok {
+		return
+	}
+	serverID := r.URL.Query().Get(serverIDKey)
+	if serverID == "" {
+		webutil.Error(w, "server_id is required", nil)
+		return
+	}
+	if !webutil.RequireServerAccess(w, r, h.servers, userID, serverID) {
 		return
 	}
 	domain := r.URL.Query().Get("domain")
-	claims, err := h.usecase.CheckDomain(r.Context(), domain, r.URL.Query().Get("app_id"))
+	claims, err := h.usecase.CheckDomain(r.Context(), serverID, domain, r.URL.Query().Get("app_id"))
 	if err != nil {
 		if errors.Is(err, model.ErrIngressInvalid) {
 			webutil.Error(w, err.Error(), nil)
```

```diff
diff --git a/internal/app/web/handler/app/routes.go b/internal/app/web/handler/app/routes.go
--- a/internal/app/web/handler/app/routes.go
+++ b/internal/app/web/handler/app/routes.go
@@ -29,8 +29,9 @@ type ImageTagsQuery struct {
 
 // CheckDomainQuery is the query of GET /api/v1/domains/check.
 type CheckDomainQuery struct {
-	Domain string `query:"domain" description:"Hostname to check."`
-	AppID  string `query:"app_id" description:"App being edited; its own claims don't count."`
+	ServerID string `query:"server_id" required:"true" description:"Server the hostname would be served on."`
+	Domain   string `query:"domain" description:"Hostname to check."`
+	AppID    string `query:"app_id" description:"App being edited; its own claims don't count."`
 }
 
 // Routes is the app handler's slice of the route table.
@@ -104,8 +105,8 @@ func (h *Handler) Routes(api.Guards) []api.Route {
 		},
 		{
 			Method: http.MethodGet, Path: "/api/v1/domains/check", Handler: h.CheckDomain, Auth: api.User,
-			Summary: "Check whether a hostname is free", Tag: "apps",
-			Query: CheckDomainQuery{}, Response: CheckDomainResponse{}, Errors: []int{http.StatusBadRequest},
+			Summary: "Check whether a hostname is free on a server", Tag: "apps",
+			Query: CheckDomainQuery{}, Response: CheckDomainResponse{}, Errors: server,
 		},
 	}
 }
```

- [ ] **Step 6: Regenerate the spec and run everything touched**

Run:
```bash
make openapi
go test ./internal/infra/db/... ./internal/domain/... ./internal/app/web/...
make test-pg
```
Expected: PASS on SQLite and on Postgres (`make test-pg` runs the contract suite, incl. `TestAppDomainsPerServerKeyMigration` and the existing `TestAppDomainsUUIDMigrationSurvivesJunkRows`, against `postgres:16` and removes the container).

- [ ] **Step 7: Commit**

```bash
git add internal/infra/db internal/domain internal/app/web api/openapi.yaml
git commit -m "feat(api)!: make an ingress hostname unique per server

GET /api/v1/domains/check now requires server_id.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: The server_domains index and the domain routes

The API keeps an index of each agent's registrations: `server_domains(server_id, name, tls, not_after, issuer, updated_at)`, PK `(server_id, name)`, FK to `servers` with cascade, index on `name`. It is refreshed from `domain.create`/`domain.delete` results and from a `domain.list` reconcile, which runs when the Domains tab loads (the route) and after every `apps.list` reconcile (so the upgrade path's seeded registrations reach the API on the next app-list load). Three agent-bound routes go into the app handler (it already has the ownership helpers and the usecase).

**Files:**
- Create: `internal/infra/db/migrations/20261010000003_server_domains.go`
- Modify: `internal/infra/db/models/models.go` (`ServerDomain`)
- Create: `internal/infra/db/repository/server_domain.go`, `server_domain_test.go`
- Modify: `internal/domain/port/app.go` (`ServerDomainRepository`)
- Modify: `internal/infra/bootstrap/deps.go`, `core.go`; `internal/app/web/routes.go`
- Create: `internal/domain/usecase/app/domains.go`, `domains_test.go`; modify `usecase.go`
- Create: `internal/app/web/handler/app/domains.go`, `domains_test.go`; modify `handler.go`, `routes.go`, `routes_test.go`
- Modify: `internal/app/web/contract/fakeagent_test.go`, `matrix_test.go`; create `internal/app/web/contract/domains_test.go`
- Regenerate: `api/openapi.yaml`, `internal/app/web/testdata/routes.golden`

**Interfaces:**
- Consumes: `command.*Domain*` (Task 1); the dispatcher answering `domain.*` (Task 5).
- Produces:
  - `port.ServerDomainRepository{ListForServer(ctx, serverID) ([]model.RegisteredDomain, error); Upsert(ctx, serverID, model.RegisteredDomain) error; Delete(ctx, serverID, name string) error; ReplaceForServer(ctx, serverID, []model.RegisteredDomain) error}`; `repository.NewDbServerDomainRepository(conn, log)`
  - `bootstrap.Deps.ServerDomainRepository`; `app.Deps.ServerDomainRepository` (usecase) and `server.Deps.ServerDomainRepository` (handler)
  - `(*app.UseCase).ListServerDomains(ctx, userID, serverID) (string, error)`, `CreateDomain(ctx, userID, serverID, command.CreateDomainRequest) (string, error)`, `DeleteDomain(ctx, userID, serverID, name string) (string, error)`
  - Routes (all `Auth: api.User`, `Query: api.ServerQuery{}`, 202 + SSE result, errors 400/403, tag `domains`): `GET /api/v1/domain/list` → `command.ListDomainsResponse`; `POST /api/v1/domain/create` body `command.CreateDomainRequest` → `command.CreateDomainResponse`; `POST /api/v1/domain/delete` body `command.DeleteDomainRequest` → `command.DeleteDomainResponse`
  - contract helpers: `(*fakeAgent).register(agentID string, d command.Domain)`, `(*env).indexed(t, serverID) map[string]string` (name → tls), `registerDomain(t, c, serverID, name, mode)`

- [ ] **Step 1: Write the failing repository tests** — `internal/infra/db/repository/server_domain_test.go`:

```go
package repository

import (
	"context"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db/models"
	"winterflow/pkg/logger"
)

func newServerDomainRepo(t *testing.T) *DbServerDomainRepository {
	t.Helper()
	conn := newTestDB(t)
	seedOrgWithServer(t, conn, "org-1", "srv-1")
	seedOrgWithServer(t, conn, "org-2", "srv-2")
	return NewDbServerDomainRepository(conn, logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}))
}

func TestServerDomainsUpsertListDelete(t *testing.T) {
	repo := newServerDomainRepo(t)
	ctx := context.Background()
	notAfter := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)

	if err := repo.Upsert(ctx, "srv-1", model.RegisteredDomain{Name: "*.twillingate.app", TLS: "auto"}); err != nil {
		t.Fatal(err)
	}
	// Replacing the certificate updates the row in place.
	if err := repo.Upsert(ctx, "srv-1", model.RegisteredDomain{Name: "*.twillingate.app", TLS: "certificate", NotAfter: &notAfter, Issuer: "CF Origin"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Upsert(ctx, "srv-1", model.RegisteredDomain{Name: "twillingate.app", TLS: "off"}); err != nil {
		t.Fatal(err)
	}
	// The same name on another server is its own row.
	if err := repo.Upsert(ctx, "srv-2", model.RegisteredDomain{Name: "*.twillingate.app", TLS: "off"}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.ListForServer(ctx, "srv-1")
	if err != nil || len(got) != 2 {
		t.Fatalf("list = %+v, %v", got, err)
	}
	if got[0].Name != "*.twillingate.app" || got[0].TLS != "certificate" || got[0].Issuer != "CF Origin" ||
		got[0].NotAfter == nil || !got[0].NotAfter.Equal(notAfter) {
		t.Errorf("certificate row = %+v", got[0])
	}
	if got[1].Name != "twillingate.app" || got[1].NotAfter != nil {
		t.Errorf("off row = %+v", got[1])
	}

	if err := repo.Delete(ctx, "srv-1", "twillingate.app"); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.ListForServer(ctx, "srv-1"); len(got) != 1 {
		t.Fatalf("after delete: %+v", got)
	}
	if got, _ := repo.ListForServer(ctx, "srv-2"); len(got) != 1 || got[0].TLS != "off" {
		t.Fatalf("other server touched: %+v", got)
	}
}

func TestServerDomainsReplaceForServer(t *testing.T) {
	repo := newServerDomainRepo(t)
	ctx := context.Background()
	_ = repo.Upsert(ctx, "srv-1", model.RegisteredDomain{Name: "old.app", TLS: "auto"})
	_ = repo.Upsert(ctx, "srv-2", model.RegisteredDomain{Name: "keep.app", TLS: "auto"})

	if err := repo.ReplaceForServer(ctx, "srv-1", []model.RegisteredDomain{
		{Name: "a.app", TLS: "auto"}, {Name: "*.b.app", TLS: "off"},
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.ListForServer(ctx, "srv-1")
	if len(got) != 2 || got[0].Name != "*.b.app" || got[1].Name != "a.app" {
		t.Fatalf("after replace: %+v", got)
	}
	if err := repo.ReplaceForServer(ctx, "srv-1", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.ListForServer(ctx, "srv-1"); len(got) != 0 {
		t.Fatalf("empty replace left %+v", got)
	}
	if got, _ := repo.ListForServer(ctx, "srv-2"); len(got) != 1 {
		t.Fatalf("other server touched: %+v", got)
	}
}

// TestServerDomainsCascadeWithServer: removing a server drops its index.
func TestServerDomainsCascadeWithServer(t *testing.T) {
	repo := newServerDomainRepo(t)
	ctx := context.Background()
	_ = repo.Upsert(ctx, "srv-1", model.RegisteredDomain{Name: "a.app", TLS: "auto"})
	if _, err := repo.db.GetDB().NewDelete().Model((*models.Server)(nil)).Where("server_id = ?", "srv-1").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.ListForServer(ctx, "srv-1"); len(got) != 0 {
		t.Fatalf("rows survived their server: %+v", got)
	}
}
```

Run: `go test ./internal/infra/db/repository/ -run ServerDomains`
Expected: FAIL to compile — `undefined: NewDbServerDomainRepository`.

- [ ] **Step 2: Table, model, repository**

`internal/infra/db/migrations/20261010000003_server_domains.go`:

```go
package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
)

func init() {
	Migrations.MustRegister(createServerDomains, func(ctx context.Context, db *bun.DB) error {
		_, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS server_domains")
		return err
	})
}

// createServerDomains is the API's index of each agent's domain
// registrations (the agent's domains/ dir is the source of truth): what
// save-app checks ingress hostnames against. Rebuilt from domain.list.
func createServerDomains(ctx context.Context, db *bun.DB) error {
	uuidType := "VARCHAR(36)"
	if db.Dialect().Name() == dialect.PG {
		uuidType = "UUID"
	}
	stmts := []string{
		fmt.Sprintf(`CREATE TABLE server_domains (
			server_id %s NOT NULL,
			name VARCHAR(253) NOT NULL,
			tls VARCHAR(16) NOT NULL,
			not_after TIMESTAMP,
			issuer VARCHAR(255) NOT NULL DEFAULT '',
			updated_at TIMESTAMP NOT NULL,
			PRIMARY KEY (server_id, name),
			FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
		)`, uuidType),
		"CREATE INDEX idx_server_domains_name ON server_domains (name)",
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("server_domains migration: %w", err)
		}
	}
	return nil
}
```

`internal/infra/db/models/models.go` (append):

```diff
diff --git a/internal/infra/db/models/models.go b/internal/infra/db/models/models.go
--- a/internal/infra/db/models/models.go
+++ b/internal/infra/db/models/models.go
@@ -176,3 +176,16 @@ type AppDomain struct {
 	App    *App    `bun:"rel:belongs-to,join:app_id=app_id"`
 	Server *Server `bun:"rel:belongs-to,join:server_id=server_id"`
 }
+
+// ServerDomain is one row of the server_domains index: a domain registered on
+// an agent, mirrored from domain.list / domain.create / domain.delete.
+type ServerDomain struct {
+	bun.BaseModel `bun:"table:server_domains"`
+
+	ServerID  string          `bun:"server_id,pk" json:"server_id"`
+	Name      string          `bun:"name,pk" json:"name"`
+	TLS       string          `bun:"tls,notnull" json:"tls"`
+	NotAfter  *types.DateTime `bun:"not_after,nullzero" json:"not_after"`
+	Issuer    string          `bun:"issuer,notnull" json:"issuer"`
+	UpdatedAt types.DateTime  `bun:"updated_at,notnull" json:"updated_at"`
+}
```

`internal/infra/db/repository/server_domain.go`:

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

func NewDbServerDomainRepository(db *db.BunConnection, log *logger.Logger) *DbServerDomainRepository {
	return &DbServerDomainRepository{db: db, log: log}
}

// DbServerDomainRepository maintains the server_domains index. The agent's
// domains/ dir stays authoritative; every method here is cache maintenance.
type DbServerDomainRepository struct {
	db  *db.BunConnection
	log *logger.Logger
}

func serverDomainRow(serverID string, d model.RegisteredDomain) models.ServerDomain {
	row := models.ServerDomain{
		ServerID: serverID, Name: d.Name, TLS: d.TLS, Issuer: d.Issuer,
		UpdatedAt: types.DateTime(time.Now().UTC()),
	}
	if d.NotAfter != nil {
		na := types.DateTime(d.NotAfter.UTC())
		row.NotAfter = &na
	}
	return row
}

// ListForServer returns the server's registrations sorted by name.
func (r *DbServerDomainRepository) ListForServer(ctx context.Context, serverID string) ([]model.RegisteredDomain, error) {
	var rows []models.ServerDomain
	if err := r.db.GetDB().NewSelect().Model(&rows).
		Where("server_id = ?", serverID).Order("name ASC").Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]model.RegisteredDomain, 0, len(rows))
	for _, row := range rows {
		d := model.RegisteredDomain{Name: row.Name, TLS: row.TLS, Issuer: row.Issuer}
		if row.NotAfter != nil {
			na := row.NotAfter.Time().UTC()
			d.NotAfter = &na
		}
		out = append(out, d)
	}
	return out, nil
}

// Upsert records one registration (domain.create confirmed by the agent).
func (r *DbServerDomainRepository) Upsert(ctx context.Context, serverID string, d model.RegisteredDomain) error {
	row := serverDomainRow(serverID, d)
	_, err := r.db.GetDB().NewInsert().Model(&row).
		On("CONFLICT (server_id, name) DO UPDATE").
		Set("tls = EXCLUDED.tls").
		Set("not_after = EXCLUDED.not_after").
		Set("issuer = EXCLUDED.issuer").
		Set("updated_at = EXCLUDED.updated_at").
		Exec(ctx)
	return err
}

// Delete drops one registration (domain.delete confirmed by the agent).
func (r *DbServerDomainRepository) Delete(ctx context.Context, serverID, name string) error {
	_, err := r.db.GetDB().NewDelete().Model((*models.ServerDomain)(nil)).
		Where("server_id = ?", serverID).Where("name = ?", name).Exec(ctx)
	return err
}

// ReplaceForServer makes the index mirror a domain.list answer.
func (r *DbServerDomainRepository) ReplaceForServer(ctx context.Context, serverID string, ds []model.RegisteredDomain) error {
	rows := make([]models.ServerDomain, 0, len(ds))
	for _, d := range ds {
		rows = append(rows, serverDomainRow(serverID, d))
	}
	return r.db.GetDB().RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewDelete().Model((*models.ServerDomain)(nil)).Where("server_id = ?", serverID).Exec(ctx); err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		_, err := tx.NewInsert().Model(&rows).Exec(ctx)
		return err
	})
}
```

Run: `go test ./internal/infra/db/repository/ -run ServerDomains`
Expected: PASS.

- [ ] **Step 3: Write the failing usecase tests** — `internal/domain/usecase/app/domains_test.go`:

```go
package app

import (
	"context"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

// recordingDispatcher keeps every dispatch (nested ones included).
type recordingDispatcher struct{ all []port.DispatchInput }

func (r *recordingDispatcher) Dispatch(_ context.Context, in port.DispatchInput) (string, error) {
	r.all = append(r.all, in)
	return "req-" + string(in.Type), nil
}

func (r *recordingDispatcher) last() port.DispatchInput { return r.all[len(r.all)-1] }

// fakeServerDomains is an in-memory port.ServerDomainRepository.
type fakeServerDomains struct {
	rows     map[string][]model.RegisteredDomain // serverID -> rows
	upserted []model.RegisteredDomain
	deleted  []string
}

func newFakeServerDomains(rows ...model.RegisteredDomain) *fakeServerDomains {
	f := &fakeServerDomains{rows: map[string][]model.RegisteredDomain{}}
	if len(rows) > 0 {
		f.rows["srv-1"] = rows
	}
	return f
}

func (f *fakeServerDomains) ListForServer(_ context.Context, serverID string) ([]model.RegisteredDomain, error) {
	return f.rows[serverID], nil
}
func (f *fakeServerDomains) Upsert(_ context.Context, serverID string, d model.RegisteredDomain) error {
	f.upserted = append(f.upserted, d)
	f.rows[serverID] = append(f.rows[serverID], d)
	return nil
}
func (f *fakeServerDomains) Delete(_ context.Context, _, name string) error {
	f.deleted = append(f.deleted, name)
	return nil
}
func (f *fakeServerDomains) ReplaceForServer(_ context.Context, serverID string, ds []model.RegisteredDomain) error {
	f.rows[serverID] = ds
	return nil
}

func newDomainsUseCase(d port.CommandDispatcher, sd port.ServerDomainRepository) *UseCase {
	return NewUseCase(&Deps{
		CommandDispatcher:      d,
		AppRepository:          noopAppRepo{},
		AppDomainRepository:    &fakeDomainRepo{},
		ServerDomainRepository: sd,
		Log:                    logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
	})
}

func TestListServerDomainsReconcilesTheIndex(t *testing.T) {
	disp := &recordingDispatcher{}
	sd := newFakeServerDomains(model.RegisteredDomain{Name: "stale.app", TLS: "auto"})
	uc := newDomainsUseCase(disp, sd)

	if _, err := uc.ListServerDomains(context.Background(), "u1", "srv-1"); err != nil {
		t.Fatal(err)
	}
	in := disp.last()
	if in.Type != command.TypeDomainList || in.AgentID != "srv-1" || in.UserID != "u1" {
		t.Fatalf("dispatch = %+v", in)
	}
	in.OnResult(port.CommandResult{Success: true, Payload: []byte(
		`{"domains":[{"name":"*.twillingate.app","tls":"certificate","not_after":"2027-01-02T03:04:05Z","issuer":"CF","apps":["a1"]}]}`)})
	got := sd.rows["srv-1"]
	if len(got) != 1 || got[0].Name != "*.twillingate.app" || got[0].TLS != "certificate" || got[0].Issuer != "CF" ||
		got[0].NotAfter == nil || !got[0].NotAfter.Equal(time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("index = %+v", got)
	}

	// A failed list leaves the index alone.
	in.OnResult(port.CommandResult{Success: false, Error: "agent offline"})
	in.OnResult(port.CommandResult{Success: true, Payload: []byte(`{not json`)})
	if len(sd.rows["srv-1"]) != 1 {
		t.Fatalf("failed results changed the index: %+v", sd.rows["srv-1"])
	}
}

func TestCreateAndDeleteDomainFollowTheAgent(t *testing.T) {
	disp := &recordingDispatcher{}
	sd := newFakeServerDomains()
	uc := newDomainsUseCase(disp, sd)
	ctx := context.Background()

	req := command.CreateDomainRequest{Name: "*.x.app", TLS: command.DomainTLSOff}
	if _, err := uc.CreateDomain(ctx, "u1", "srv-1", req); err != nil {
		t.Fatal(err)
	}
	create := disp.last()
	if create.Type != command.TypeDomainCreate || create.Payload != req {
		t.Fatalf("dispatch = %+v", create)
	}
	create.OnResult(port.CommandResult{Success: false, Error: "nope"})
	if len(sd.upserted) != 0 {
		t.Fatal("a refused create entered the index")
	}
	create.OnResult(port.CommandResult{Success: true, Payload: []byte(`{"domain":{"name":"*.x.app","tls":"off","apps":[]}}`)})
	if len(sd.upserted) != 1 || sd.upserted[0].Name != "*.x.app" || sd.upserted[0].TLS != "off" {
		t.Fatalf("upserted = %+v", sd.upserted)
	}

	if _, err := uc.DeleteDomain(ctx, "u1", "srv-1", "*.x.app"); err != nil {
		t.Fatal(err)
	}
	del := disp.last()
	if del.Type != command.TypeDomainDelete || del.Payload != (command.DeleteDomainRequest{Name: "*.x.app"}) {
		t.Fatalf("dispatch = %+v", del)
	}
	del.OnResult(port.CommandResult{Success: false, Error: "*.x.app is used by Shop (a1)"})
	if len(sd.deleted) != 0 {
		t.Fatal("a refused delete left the index")
	}
	del.OnResult(port.CommandResult{Success: true})
	if len(sd.deleted) != 1 || sd.deleted[0] != "*.x.app" {
		t.Fatalf("deleted = %v", sd.deleted)
	}
}

func TestRefreshAppsAlsoReconcilesDomains(t *testing.T) {
	disp := &recordingDispatcher{}
	uc := newDomainsUseCase(disp, newFakeServerDomains())
	if _, err := uc.RefreshApps(context.Background(), "u1", "srv-1"); err != nil {
		t.Fatal(err)
	}
	disp.last().OnResult(port.CommandResult{Success: true, Payload: []byte(`{"apps":[]}`)})
	if len(disp.all) != 2 || disp.all[1].Type != command.TypeDomainList || disp.all[1].AgentID != "srv-1" {
		t.Fatalf("dispatches = %+v", disp.all)
	}
}

func TestDomainResultsWithoutAnIndexAreNoOps(t *testing.T) {
	disp := &recordingDispatcher{}
	uc := newTestUseCase(disp) // no ServerDomainRepository
	ctx := context.Background()
	_, _ = uc.ListServerDomains(ctx, "u", "s")
	_, _ = uc.CreateDomain(ctx, "u", "s", command.CreateDomainRequest{Name: "x.app", TLS: command.DomainTLSOff})
	_, _ = uc.DeleteDomain(ctx, "u", "s", "x.app")
	for _, in := range disp.all {
		in.OnResult(port.CommandResult{Success: true, Payload: []byte(`{}`)})
	}
	_, _ = uc.RefreshApps(ctx, "u", "s")
	disp.last().OnResult(port.CommandResult{Success: true, Payload: []byte(`{"apps":[]}`)})
	if disp.last().Type != command.TypeAppsList {
		t.Fatal("refresh dispatched domain.list without an index to fill")
	}
}
```

Run: `go test ./internal/domain/usecase/app/`
Expected: FAIL to compile — `unknown field ServerDomainRepository in struct literal of type Deps`, `uc.ListServerDomains undefined`.

- [ ] **Step 4: Port, wiring, usecase**

`internal/domain/port/app.go`:

```diff
diff --git a/internal/domain/port/app.go b/internal/domain/port/app.go
--- a/internal/domain/port/app.go
+++ b/internal/domain/port/app.go
@@ -42,6 +42,20 @@ type AppDomainRepository interface {
 	ListForServer(ctx context.Context, serverID string) (map[string][]model.AppDomainInfo, error)
 }
 
+// ServerDomainRepository is the DB-backed index of each server's domain
+// registrations (the agent's domains/ dir is the source of truth): what
+// save-app checks ingress hostnames against.
+type ServerDomainRepository interface {
+	// ListForServer returns the server's registrations sorted by name.
+	ListForServer(ctx context.Context, serverID string) ([]model.RegisteredDomain, error)
+	// Upsert records a registration the agent confirmed creating.
+	Upsert(ctx context.Context, serverID string, d model.RegisteredDomain) error
+	// Delete drops a registration the agent confirmed deleting.
+	Delete(ctx context.Context, serverID, name string) error
+	// ReplaceForServer makes the index mirror a domain.list answer.
+	ReplaceForServer(ctx context.Context, serverID string, ds []model.RegisteredDomain) error
+}
+
 // IngressManager owns the embedded reverse proxy. Implementations must be
 // safe for concurrent use and must never let an ingress failure propagate as
 // a command failure.
```

`internal/infra/bootstrap/deps.go` and `core.go`:

```diff
diff --git a/internal/infra/bootstrap/deps.go b/internal/infra/bootstrap/deps.go
--- a/internal/infra/bootstrap/deps.go
+++ b/internal/infra/bootstrap/deps.go
@@ -22,7 +22,9 @@ type Deps struct {
 	ServerRepository    port.ServerRepository
 	AppRepository       port.AppRepository
 	AppDomainRepository port.AppDomainRepository
-	CommandDispatcher   port.CommandDispatcher
-	NotificationManager port.NotificationManager
-	StatusCache         *status.Cache
+	// ServerDomainRepository indexes each server's domain registrations.
+	ServerDomainRepository port.ServerDomainRepository
+	CommandDispatcher      port.CommandDispatcher
+	NotificationManager    port.NotificationManager
+	StatusCache            *status.Cache
 }
```

```diff
diff --git a/internal/infra/bootstrap/core.go b/internal/infra/bootstrap/core.go
--- a/internal/infra/bootstrap/core.go
+++ b/internal/infra/bootstrap/core.go
@@ -25,6 +25,7 @@ func WireCore(ctx context.Context, b bus.Bus, dbconn *db.BunConnection, cfg *con
 	serverRepo := repository.NewDbServerRepository(dbconn, log)
 	appRepo := repository.NewDbAppRepository(dbconn, log)
 	appDomainRepo := repository.NewDbAppDomainRepository(dbconn, log)
+	serverDomainRepo := repository.NewDbServerDomainRepository(dbconn, log)
 
 	nm := notificationsvc.NewNotificationManager(log)
 	dispatcher := dispatch.NewManager(b, nm, cfg, log)
@@ -34,14 +35,15 @@ func WireCore(ctx context.Context, b bus.Bus, dbconn *db.BunConnection, cfg *con
 	startEventsSubscriber(ctx, b, statusCache, serverRepo, nm, cfg, log)
 
 	return &Deps{
-		Log:                 log,
-		Cfg:                 cfg,
-		UserService:         userRepo,
-		ServerRepository:    serverRepo,
-		AppRepository:       appRepo,
-		AppDomainRepository: appDomainRepo,
-		CommandDispatcher:   dispatcher,
-		NotificationManager: nm,
-		StatusCache:         statusCache,
+		Log:                    log,
+		Cfg:                    cfg,
+		UserService:            userRepo,
+		ServerRepository:       serverRepo,
+		AppRepository:          appRepo,
+		AppDomainRepository:    appDomainRepo,
+		ServerDomainRepository: serverDomainRepo,
+		CommandDispatcher:      dispatcher,
+		NotificationManager:    nm,
+		StatusCache:            statusCache,
 	}, serverRepo
 }
```

`internal/domain/usecase/app/usecase.go` (the dependency, and a `domain.list` after each `apps.list` reconcile):

```diff
diff --git a/internal/domain/usecase/app/usecase.go b/internal/domain/usecase/app/usecase.go
--- a/internal/domain/usecase/app/usecase.go
+++ b/internal/domain/usecase/app/usecase.go
@@ -13,25 +13,28 @@ import (
 )
 
 type UseCase struct {
-	dispatcher port.CommandDispatcher
-	repo       port.AppRepository
-	domains    port.AppDomainRepository
-	log        *logger.Logger
+	dispatcher    port.CommandDispatcher
+	repo          port.AppRepository
+	domains       port.AppDomainRepository
+	serverDomains port.ServerDomainRepository
+	log           *logger.Logger
 }
 
 type Deps struct {
-	CommandDispatcher   port.CommandDispatcher
-	AppRepository       port.AppRepository
-	AppDomainRepository port.AppDomainRepository
-	Log                 *logger.Logger
+	CommandDispatcher      port.CommandDispatcher
+	AppRepository          port.AppRepository
+	AppDomainRepository    port.AppDomainRepository
+	ServerDomainRepository port.ServerDomainRepository
+	Log                    *logger.Logger
 }
 
 func NewUseCase(d *Deps) *UseCase {
 	return &UseCase{
-		dispatcher: d.CommandDispatcher,
-		repo:       d.AppRepository,
-		domains:    d.AppDomainRepository,
-		log:        d.Log,
+		dispatcher:    d.CommandDispatcher,
+		repo:          d.AppRepository,
+		domains:       d.AppDomainRepository,
+		serverDomains: d.ServerDomainRepository,
+		log:           d.Log,
 	}
 }
 
@@ -77,6 +80,13 @@ func (uc *UseCase) RefreshApps(ctx context.Context, userID, serverID string) (st
 					uc.log.Error("RefreshApps: sync domain rows", "error", err, "server_id", serverID)
 				}
 			}
+			// Registrations change with the apps' agent (upgrade seeding,
+			// another API replica): re-read them with every reconcile.
+			if uc.serverDomains != nil {
+				if _, err := uc.ListServerDomains(context.Background(), userID, serverID); err != nil {
+					uc.log.Error("RefreshApps: dispatch domain.list", "error", err, "server_id", serverID)
+				}
+			}
 		},
 	})
 }
```

`internal/domain/usecase/app/domains.go`:

```go
package app

import (
	"context"
	"encoding/json"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
)

// registeredDomain is the index row for an agent-reported registration.
func registeredDomain(d command.Domain) model.RegisteredDomain {
	return model.RegisteredDomain{Name: d.Name, TLS: string(d.TLS), NotAfter: d.NotAfter, Issuer: d.Issuer}
}

// ListServerDomains dispatches domain.list to the server's agent. The result
// reconciles the server_domains index and is delivered over SSE.
func (uc *UseCase) ListServerDomains(ctx context.Context, userID, serverID string) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeDomainList,
		Payload: command.ListDomainsRequest{},
		OnResult: func(res port.CommandResult) {
			if !res.Success || len(res.Payload) == 0 || uc.serverDomains == nil {
				return
			}
			var listed command.ListDomainsResponse
			if err := json.Unmarshal(res.Payload, &listed); err != nil {
				uc.log.Error("ListServerDomains: decode result", "error", err)
				return
			}
			rows := make([]model.RegisteredDomain, 0, len(listed.Domains))
			for _, d := range listed.Domains {
				rows = append(rows, registeredDomain(d))
			}
			if err := uc.serverDomains.ReplaceForServer(context.Background(), serverID, rows); err != nil {
				uc.log.Error("ListServerDomains: sync index", "error", err, "server_id", serverID)
			}
		},
	})
}

// CreateDomain dispatches domain.create (callers validate req first). On
// the agent's confirmation the registration enters the index.
func (uc *UseCase) CreateDomain(ctx context.Context, userID, serverID string, req command.CreateDomainRequest) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeDomainCreate,
		Payload: req,
		OnResult: func(res port.CommandResult) {
			if !res.Success || len(res.Payload) == 0 || uc.serverDomains == nil {
				return
			}
			var created command.CreateDomainResponse
			if err := json.Unmarshal(res.Payload, &created); err != nil {
				uc.log.Error("CreateDomain: decode result", "error", err)
				return
			}
			if err := uc.serverDomains.Upsert(context.Background(), serverID, registeredDomain(created.Domain)); err != nil {
				uc.log.Error("CreateDomain: update index", "error", err, "server_id", serverID)
			}
		},
	})
}

// DeleteDomain dispatches domain.delete. On the agent's confirmation the
// registration leaves the index; a refusal (in use) changes nothing.
func (uc *UseCase) DeleteDomain(ctx context.Context, userID, serverID, name string) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeDomainDelete,
		Payload: command.DeleteDomainRequest{Name: name},
		OnResult: func(res port.CommandResult) {
			if !res.Success || uc.serverDomains == nil {
				return
			}
			if err := uc.serverDomains.Delete(context.Background(), serverID, name); err != nil {
				uc.log.Error("DeleteDomain: update index", "error", err, "server_id", serverID)
			}
		},
	})
}
```

Run: `go test ./internal/domain/usecase/app/`
Expected: PASS.

- [ ] **Step 5: Write the failing handler tests** — `internal/app/web/handler/app/domains_test.go`, plus the route-table expectation:

```go
package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
)

func TestDomainRoutesDispatch(t *testing.T) {
	cases := []struct {
		name    string
		handler func(*Handler) http.HandlerFunc
		request *http.Request
		typ     command.Type
	}{
		{"list", func(h *Handler) http.HandlerFunc { return h.ListRegisteredDomains },
			httptest.NewRequest("GET", "/api/v1/domain/list?server_id=s1", nil), command.TypeDomainList},
		{"create", func(h *Handler) http.HandlerFunc { return h.CreateDomain },
			httptest.NewRequest("POST", "/api/v1/domain/create?server_id=s1", strings.NewReader(`{"name":"*.x.app","tls":"off"}`)), command.TypeDomainCreate},
		{"delete", func(h *Handler) http.HandlerFunc { return h.DeleteDomain },
			httptest.NewRequest("POST", "/api/v1/domain/delete?server_id=s1", strings.NewReader(`{"name":"*.x.app"}`)), command.TypeDomainDelete},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, fd, _ := newHandler(t)
			w := do(tc.handler(h), authed(tc.request))
			if w.Code != http.StatusAccepted || decodeRequestID(t, w) != "req-123" {
				t.Fatalf("status %d body %s", w.Code, w.Body.String())
			}
			if fd.last.Type != tc.typ || fd.last.AgentID != "s1" || fd.last.UserID != "u1" {
				t.Fatalf("dispatched %+v", fd.last)
			}
		})
	}
}

func TestDomainRoutesRejectBadInput(t *testing.T) {
	cases := []struct {
		name    string
		handler func(*Handler) http.HandlerFunc
		target  string
		body    string
		want    int
	}{
		{"list without server", func(h *Handler) http.HandlerFunc { return h.ListRegisteredDomains }, "/x", "", http.StatusBadRequest},
		{"list foreign server", func(h *Handler) http.HandlerFunc { return h.ListRegisteredDomains }, "/x?server_id=other", "", http.StatusForbidden},
		{"create foreign server", func(h *Handler) http.HandlerFunc { return h.CreateDomain }, "/x?server_id=other", `{"name":"x.app","tls":"off"}`, http.StatusForbidden},
		{"create bad json", func(h *Handler) http.HandlerFunc { return h.CreateDomain }, "/x?server_id=s1", `{`, http.StatusBadRequest},
		{"create bad name", func(h *Handler) http.HandlerFunc { return h.CreateDomain }, "/x?server_id=s1", `{"name":"*.*.x.app","tls":"off"}`, http.StatusBadRequest},
		{"create bad mode", func(h *Handler) http.HandlerFunc { return h.CreateDomain }, "/x?server_id=s1", `{"name":"x.app","tls":"manual"}`, http.StatusBadRequest},
		{"create cert without key", func(h *Handler) http.HandlerFunc { return h.CreateDomain }, "/x?server_id=s1", `{"name":"x.app","tls":"certificate","certificate":"PEM"}`, http.StatusBadRequest},
		{"delete foreign server", func(h *Handler) http.HandlerFunc { return h.DeleteDomain }, "/x?server_id=other", `{"name":"x.app"}`, http.StatusForbidden},
		{"delete bad name", func(h *Handler) http.HandlerFunc { return h.DeleteDomain }, "/x?server_id=s1", `{"name":"../x"}`, http.StatusBadRequest},
		{"delete bad json", func(h *Handler) http.HandlerFunc { return h.DeleteDomain }, "/x?server_id=s1", `{`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, fd, _ := newHandler(t)
			r := httptest.NewRequest("POST", tc.target, strings.NewReader(tc.body))
			if w := do(tc.handler(h), authed(r)); w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if fd.last.Type != "" {
				t.Fatalf("dispatched %+v", fd.last)
			}
		})
	}
	h, _, _ := newHandler(t)
	if w := do(h.CreateDomain, httptest.NewRequest("POST", "/x?server_id=s1", strings.NewReader(`{}`))); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", w.Code)
	}
}
```

```diff
diff --git a/internal/app/web/handler/app/routes_test.go b/internal/app/web/handler/app/routes_test.go
--- a/internal/app/web/handler/app/routes_test.go
+++ b/internal/app/web/handler/app/routes_test.go
@@ -24,6 +24,9 @@ func TestRoutes(t *testing.T) {
 		"POST /api/v1/app/control-app":    false,
 		"POST /api/v1/app/delete-app":     false,
 		"POST /api/v1/app/rename-app":     false,
+		"GET /api/v1/domain/list":         false,
+		"POST /api/v1/domain/create":      false,
+		"POST /api/v1/domain/delete":      false,
 		"GET /api/v1/domains/check":       false,
 	}
 	got := map[string]bool{}
```

Run: `go test ./internal/app/web/handler/app/`
Expected: FAIL to compile — `h.ListRegisteredDomains undefined`.

- [ ] **Step 6: Handlers, routes, router wiring**

`internal/app/web/handler/app/domains.go`:

```go
package server

import (
	"net/http"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/command"
)

// serverFromQuery resolves the caller and the server_id query parameter and
// enforces the caller's org owns that server, writing 401/400/403 itself.
func (h *Handler) serverFromQuery(w http.ResponseWriter, r *http.Request) (userID, serverID string, ok bool) {
	userID, ok = webutil.RequireUser(w, r)
	if !ok {
		return "", "", false
	}
	serverID = r.URL.Query().Get(serverIDKey)
	if serverID == "" {
		webutil.Error(w, "server_id is required", nil)
		return "", "", false
	}
	if !webutil.RequireServerAccess(w, r, h.servers, userID, serverID) {
		return "", "", false
	}
	return userID, serverID, true
}

// ListRegisteredDomains dispatches domain.list; the registrations (with the
// apps using each) arrive over SSE and refresh the server_domains index.
func (h *Handler) ListRegisteredDomains(w http.ResponseWriter, r *http.Request) {
	userID, serverID, ok := h.serverFromQuery(w, r)
	if !ok {
		return
	}
	reqID, err := h.usecase.ListServerDomains(r.Context(), userID, serverID)
	if err != nil {
		webutil.Error(w, "failed to list domains", nil)
		return
	}
	accepted(w, "list domains dispatched", reqID)
}

// CreateDomain registers a domain on the server, or replaces an existing
// registration (how a certificate is renewed).
func (h *Handler) CreateDomain(w http.ResponseWriter, r *http.Request) {
	userID, serverID, ok := h.serverFromQuery(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[command.CreateDomainRequest](w, r)
	if !ok {
		return
	}
	if err := req.Validate(); err != nil {
		webutil.Error(w, err.Error(), nil)
		return
	}
	reqID, err := h.usecase.CreateDomain(r.Context(), userID, serverID, req)
	if err != nil {
		webutil.Error(w, "failed to register domain", nil)
		return
	}
	accepted(w, "register domain dispatched", reqID)
}

// DeleteDomain removes a registration; the agent refuses while an app uses it.
func (h *Handler) DeleteDomain(w http.ResponseWriter, r *http.Request) {
	userID, serverID, ok := h.serverFromQuery(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[command.DeleteDomainRequest](w, r)
	if !ok {
		return
	}
	if err := req.Validate(); err != nil {
		webutil.Error(w, err.Error(), nil)
		return
	}
	reqID, err := h.usecase.DeleteDomain(r.Context(), userID, serverID, req.Name)
	if err != nil {
		webutil.Error(w, "failed to remove domain", nil)
		return
	}
	accepted(w, "remove domain dispatched", reqID)
}
```

`internal/app/web/handler/app/handler.go`, `routes.go`, and `internal/app/web/routes.go`:

```diff
diff --git a/internal/app/web/handler/app/handler.go b/internal/app/web/handler/app/handler.go
--- a/internal/app/web/handler/app/handler.go
+++ b/internal/app/web/handler/app/handler.go
@@ -22,7 +22,9 @@ type Deps struct {
 	CommandDispatcher   port.CommandDispatcher
 	AppRepository       port.AppRepository
 	AppDomainRepository port.AppDomainRepository
-	StatusCache         *status.Cache
+	// ServerDomainRepository indexes each server's domain registrations.
+	ServerDomainRepository port.ServerDomainRepository
+	StatusCache            *status.Cache
 	// Servers authorizes server-addressed operations: every handler that takes
 	// a server_id verifies the caller's org owns it before acting.
 	Servers webutil.ServerAccess
@@ -30,10 +32,11 @@ type Deps struct {
 
 func NewHandler(d *Deps) *Handler {
 	uc := useapp.NewUseCase(&useapp.Deps{
-		CommandDispatcher:   d.CommandDispatcher,
-		AppRepository:       d.AppRepository,
-		AppDomainRepository: d.AppDomainRepository,
-		Log:                 d.Logger,
+		CommandDispatcher:      d.CommandDispatcher,
+		AppRepository:          d.AppRepository,
+		AppDomainRepository:    d.AppDomainRepository,
+		ServerDomainRepository: d.ServerDomainRepository,
+		Log:                    d.Logger,
 	})
 	return &Handler{usecase: *uc, status: d.StatusCache, servers: d.Servers, log: d.Logger}
 }
```

```diff
diff --git a/internal/app/web/handler/app/routes.go b/internal/app/web/handler/app/routes.go
--- a/internal/app/web/handler/app/routes.go
+++ b/internal/app/web/handler/app/routes.go
@@ -103,6 +103,25 @@ func (h *Handler) Routes(api.Guards) []api.Route {
 			Summary: "Rename an app", Tag: "apps",
 			Request: RenameAppRequest{}, Accepted: true, Result: command.RenameAppResponse{}, Errors: server,
 		},
+		{
+			Method: http.MethodGet, Path: "/api/v1/domain/list", Handler: h.ListRegisteredDomains, Auth: api.User,
+			Summary: "List the domains registered on a server, with the apps using each", Tag: "domains",
+			Query: api.ServerQuery{}, Accepted: true, Result: command.ListDomainsResponse{}, Errors: server,
+		},
+		{
+			Method: http.MethodPost, Path: "/api/v1/domain/create", Handler: h.CreateDomain, Auth: api.User,
+			Summary: "Register a domain, or replace its TLS mode or certificate", Tag: "domains",
+			Description: "In certificate mode `certificate` is the PEM chain and `key` the PEM private key " +
+				"ECIES-encrypted with the server's public key (GET /api/v1/server/get-public-key).",
+			Query: api.ServerQuery{}, Request: command.CreateDomainRequest{},
+			Accepted: true, Result: command.CreateDomainResponse{}, Errors: server,
+		},
+		{
+			Method: http.MethodPost, Path: "/api/v1/domain/delete", Handler: h.DeleteDomain, Auth: api.User,
+			Summary: "Remove a registered domain no app uses", Tag: "domains",
+			Query: api.ServerQuery{}, Request: command.DeleteDomainRequest{},
+			Accepted: true, Result: command.DeleteDomainResponse{}, Errors: server,
+		},
 		{
 			Method: http.MethodGet, Path: "/api/v1/domains/check", Handler: h.CheckDomain, Auth: api.User,
 			Summary: "Check whether a hostname is free on a server", Tag: "apps",
```

```diff
diff --git a/internal/app/web/routes.go b/internal/app/web/routes.go
--- a/internal/app/web/routes.go
+++ b/internal/app/web/routes.go
@@ -68,12 +68,13 @@ func routeTable(d *bootstrap.Deps, g api.Guards) []api.Route {
 		NotificationManager: d.NotificationManager,
 	})
 	appsAPI := happ.NewHandler(&happ.Deps{
-		Logger:              d.Log,
-		CommandDispatcher:   d.CommandDispatcher,
-		AppRepository:       d.AppRepository,
-		AppDomainRepository: d.AppDomainRepository,
-		StatusCache:         d.StatusCache,
-		Servers:             d.ServerRepository,
+		Logger:                 d.Log,
+		CommandDispatcher:      d.CommandDispatcher,
+		AppRepository:          d.AppRepository,
+		AppDomainRepository:    d.AppDomainRepository,
+		ServerDomainRepository: d.ServerDomainRepository,
+		StatusCache:            d.StatusCache,
+		Servers:                d.ServerRepository,
 	})
 	dockerAPI := hdocker.NewHandler(&hdocker.Deps{
 		Logger:            d.Log,
```

Run: `go test ./internal/app/web/handler/app/`
Expected: PASS.

- [ ] **Step 7: Contract suite** — the fake agent keeps a registry per agent id so a list after a create sees it (`fakeagent_test.go`), the matrix gets both probes per route (`matrix_test.go`), and `domains_test.go` drives every route:

```diff
diff --git a/internal/app/web/contract/fakeagent_test.go b/internal/app/web/contract/fakeagent_test.go
--- a/internal/app/web/contract/fakeagent_test.go
+++ b/internal/app/web/contract/fakeagent_test.go
@@ -3,6 +3,7 @@ package contract_test
 import (
 	"context"
 	"encoding/json"
+	"sort"
 	"sync"
 	"testing"
 
@@ -38,10 +39,60 @@ type fakeAgent struct {
 	mu        sync.Mutex
 	overrides map[command.Type]script
 	calls     []received
+	// domains is each agent's registry (agentID -> name -> domain): the
+	// domain.* defaults read and write it, so a list after a create sees it.
+	domains map[string]map[string]command.Domain
 }
 
 func newFakeAgent() *fakeAgent {
-	return &fakeAgent{overrides: map[command.Type]script{}}
+	return &fakeAgent{overrides: map[command.Type]script{}, domains: map[string]map[string]command.Domain{}}
+}
+
+// register puts a domain straight into agentID's registry, the way the
+// agent's upgrade path seeds one without any API call.
+func (a *fakeAgent) register(agentID string, d command.Domain) {
+	a.mu.Lock()
+	defer a.mu.Unlock()
+	if a.domains[agentID] == nil {
+		a.domains[agentID] = map[string]command.Domain{}
+	}
+	if d.Apps == nil {
+		d.Apps = []string{}
+	}
+	a.domains[agentID][d.Name] = d
+}
+
+// domainScript answers domain.* from agentID's registry.
+func (a *fakeAgent) domainScript(typ command.Type, agentID string) (script, bool) {
+	switch typ {
+	case command.TypeDomainList:
+		return func(json.RawMessage) reply {
+			a.mu.Lock()
+			defer a.mu.Unlock()
+			out := []command.Domain{}
+			for _, d := range a.domains[agentID] {
+				out = append(out, d)
+			}
+			sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
+			return reply{Payload: command.ListDomainsResponse{Domains: out}}
+		}, true
+	case command.TypeDomainCreate:
+		return func(raw json.RawMessage) reply {
+			in := decode[command.CreateDomainRequest](raw)
+			d := command.Domain{Name: in.Name, TLS: in.TLS, Apps: []string{}}
+			a.register(agentID, d)
+			return reply{Payload: command.CreateDomainResponse{Domain: d}}
+		}, true
+	case command.TypeDomainDelete:
+		return func(raw json.RawMessage) reply {
+			in := decode[command.DeleteDomainRequest](raw)
+			a.mu.Lock()
+			delete(a.domains[agentID], in.Name)
+			a.mu.Unlock()
+			return reply{Payload: command.DeleteDomainResponse{Name: in.Name}}
+		}, true
+	}
+	return nil, false
 }
 
 // on overrides the answer for typ for the rest of the test.
@@ -89,7 +140,11 @@ func (a *fakeAgent) Dispatch(_ context.Context, req *proto.RequestEnvelope) *pro
 	s, ok := a.overrides[typ]
 	a.mu.Unlock()
 	if !ok {
-		s = defaultScript(typ)
+		if ds, isDomain := a.domainScript(typ, agentID); isDomain {
+			s = ds
+		} else {
+			s = defaultScript(typ)
+		}
 	}
 	r := s(req.Payload)
 	code, detail, body := proto.ResponseCode_RESPONSE_CODE_SUCCESS, "ok", r.Payload
@@ -219,6 +274,7 @@ func TestFakeAgentAnswersEveryCommandType(t *testing.T) {
 		command.TypeRegistryCreate, command.TypeRegistryDelete, command.TypeNetworkList,
 		command.TypeNetworkCreate, command.TypeNetworkDelete, command.TypeVolumeList,
 		command.TypeVolumeCreate, command.TypeVolumeDelete, command.TypeImageTags, command.TypeAgentUpdate,
+		command.TypeDomainList, command.TypeDomainCreate, command.TypeDomainDelete,
 	} {
 		resp := a.Dispatch(context.Background(), &proto.RequestEnvelope{Type: string(typ), RequestId: "r", Payload: []byte(`{}`)})
 		if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
```

```diff
diff --git a/internal/app/web/contract/matrix_test.go b/internal/app/web/contract/matrix_test.go
--- a/internal/app/web/contract/matrix_test.go
+++ b/internal/app/web/contract/matrix_test.go
@@ -62,6 +62,13 @@ var probes = map[string]probe{
 	"POST /api/v1/app/rename-app": {valid: func(s string) call {
 		return post("/api/v1/app/rename-app", map[string]any{"server_id": s, "app_id": anApp, "name": "y"})
 	}, malformed: bad("POST", "/api/v1/app/rename-app")},
+	"GET /api/v1/domain/list": {valid: func(s string) call { return get("/api/v1/domain/list", sq(s)) }, malformed: bad("GET", "/api/v1/domain/list")},
+	"POST /api/v1/domain/create": {valid: func(s string) call {
+		return call{Method: http.MethodPost, Path: "/api/v1/domain/create", Query: sq(s), Body: map[string]any{"name": "*.example.test", "tls": "off"}}
+	}, malformed: bad("POST", "/api/v1/domain/create")},
+	"POST /api/v1/domain/delete": {valid: func(s string) call {
+		return call{Method: http.MethodPost, Path: "/api/v1/domain/delete", Query: sq(s), Body: map[string]any{"name": "*.example.test"}}
+	}, malformed: bad("POST", "/api/v1/domain/delete")},
 	"GET /api/v1/domains/check": {valid: func(s string) call {
 		return get("/api/v1/domains/check", url.Values{"server_id": {s}, "domain": {"free.example.test"}})
 	}, malformed: bad("GET", "/api/v1/domains/check")},
```

`internal/app/web/contract/domains_test.go`:

```go
package contract_test

import (
	"net/http"
	"net/url"
	"testing"

	"winterflow/internal/domain/command"
)

// indexed is the API's server_domains index for serverID, as names.
func (e *env) indexed(t *testing.T, serverID string) map[string]string {
	t.Helper()
	rows, err := e.deps.ServerDomainRepository.ListForServer(e.ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, r := range rows {
		out[r.Name] = r.TLS
	}
	return out
}

// registerDomain registers name on serverID through the API.
func registerDomain(t *testing.T, c *client, serverID, name string, mode command.DomainTLS) {
	t.Helper()
	c.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/domain/create", Query: sq(serverID),
		Body: command.CreateDomainRequest{Name: name, TLS: mode}}, nil)
}

func TestDomainRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	q := url.Values{"server_id": {sid}}

	t.Run("create enters the index", func(t *testing.T) {
		var created command.CreateDomainResponse
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/domain/create", Query: q,
			Body: command.CreateDomainRequest{Name: "*.example.test", TLS: command.DomainTLSOff}}, &created)
		if created.Domain.Name != "*.example.test" || created.Domain.TLS != command.DomainTLSOff {
			t.Fatalf("created = %+v", created)
		}
		// Certificate mode carries the PEM chain and the encrypted key.
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/domain/create", Query: q,
			Body: command.CreateDomainRequest{Name: "secure.test", TLS: command.DomainTLSCertificate,
				Certificate: "-----BEGIN CERTIFICATE-----", Key: "BASE64CIPHERTEXT"}}, nil)
		got := e.agent.seen(command.TypeDomainCreate)
		if in := decode[command.CreateDomainRequest](got[len(got)-1].Payload); in.Key != "BASE64CIPHERTEXT" {
			t.Fatalf("the key reached the agent as %q", in.Key)
		}
		if idx := e.indexed(t, sid); idx["*.example.test"] != "off" || idx["secure.test"] != "certificate" {
			t.Fatalf("index = %v", idx)
		}
	})
	t.Run("list reports the agent's registry and reconciles", func(t *testing.T) {
		// Registered on the agent behind the API's back (upgrade seeding).
		e.agent.register(sid, command.Domain{Name: "legacy.test", TLS: command.DomainTLSAuto, Apps: []string{"a1"}})
		var listed command.ListDomainsResponse
		owner.succeed(t, call{Method: http.MethodGet, Path: "/api/v1/domain/list", Query: q}, &listed)
		if len(listed.Domains) != 3 || listed.Domains[1].Name != "legacy.test" || listed.Domains[1].Apps[0] != "a1" {
			t.Fatalf("listed = %+v", listed)
		}
		if idx := e.indexed(t, sid); len(idx) != 3 || idx["legacy.test"] != "auto" {
			t.Fatalf("index = %v", idx)
		}
	})
	t.Run("refresh-apps also reconciles domains", func(t *testing.T) {
		e.agent.register(sid, command.Domain{Name: "seeded.test", TLS: command.DomainTLSOff})
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/app/refresh-apps", Query: q}, nil)
		eventually(t, func() bool { return e.indexed(t, sid)["seeded.test"] == "off" })
	})
	t.Run("delete leaves the index", func(t *testing.T) {
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/domain/delete", Query: q,
			Body: command.DeleteDomainRequest{Name: "secure.test"}}, nil)
		if _, ok := e.indexed(t, sid)["secure.test"]; ok {
			t.Fatal("deleted registration still indexed")
		}
	})
	t.Run("a refused delete keeps the registration", func(t *testing.T) {
		e.agent.fail(command.TypeDomainDelete, "*.example.test is used by Shop (a1)")
		owner.failWith(t, call{Method: http.MethodPost, Path: "/api/v1/domain/delete", Query: q,
			Body: command.DeleteDomainRequest{Name: "*.example.test"}}, "*.example.test is used by Shop (a1)")
		if _, ok := e.indexed(t, sid)["*.example.test"]; !ok {
			t.Fatal("a refused delete left the index")
		}
	})
	t.Run("agent errors", func(t *testing.T) {
		e.agent.fail(command.TypeDomainCreate, "the certificate does not cover x.test (it covers y.test)")
		owner.failWith(t, call{Method: http.MethodPost, Path: "/api/v1/domain/create", Query: q,
			Body: command.CreateDomainRequest{Name: "x.test", TLS: command.DomainTLSCertificate, Certificate: "C", Key: "K"}},
			"the certificate does not cover x.test (it covers y.test)")
		e.agent.fail(command.TypeDomainList, "ingress is not available on this agent")
		owner.failWith(t, call{Method: http.MethodGet, Path: "/api/v1/domain/list", Query: q}, "ingress is not available on this agent")
		if _, ok := e.indexed(t, sid)["x.test"]; ok {
			t.Fatal("a refused create entered the index")
		}
	})
	t.Run("the API rejects a bad name before the agent", func(t *testing.T) {
		n := e.agent.count()
		owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/domain/create", Query: q,
			Body: command.CreateDomainRequest{Name: "*.*.example.test", TLS: command.DomainTLSOff}}).expect(t, http.StatusBadRequest)
		if e.agent.count() != n {
			t.Fatal("an invalid registration reached the agent")
		}
	})
}
```

- [ ] **Step 8: Regenerate and run**

Run:
```bash
make openapi
go test ./internal/app/web/ -run TestRoutesGolden -update
go test ./internal/...
make test-pg
```
Expected: the golden gains three lines (`GET /api/v1/domain/list`, `POST /api/v1/domain/create`, `POST /api/v1/domain/delete`); everything PASSes on SQLite and Postgres, including the coverage guard (each new route answered below 400) and `TestForeignServer` (403 on another org's server, nothing reaches the agent).

- [ ] **Step 9: Commit**

```bash
git add internal api/openapi.yaml
git commit -m "feat(api): index registered domains and add the domain routes

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Save-app requires a covering registration

`save-app` now checks every ingress hostname (routes and domain-level redirect sources) against the server's `server_domains` rows: uncovered → 400 with `model.NotCoveredError`'s message (the agent excludes such hosts again in `BuildConfig`). The check endpoint answers `{available, covered, registration?, claims}`.

**Files:**
- Modify: `internal/domain/usecase/app/usecase.go` (`DomainCheck`, `winningRegistration`, coverage in `SaveApp`)
- Create: `internal/domain/usecase/app/coverage_test.go`; modify `usecase_test.go`
- Modify: `internal/app/web/handler/app/check_domain.go` (response), `save_app.go` (400)
- Modify: `internal/app/web/contract/apps_test.go`
- Regenerate: `api/openapi.yaml`

**Interfaces:**
- Consumes: `model.NotCoveredError`, `model.ErrDomainNotCovered`, `model.WinningDomain` (Task 1); `ServerDomainRepository` and the contract helper `registerDomain` (Task 7).
- Produces:
  - `app.DomainCheck{Claims []model.DomainClaim; Registration *model.RegisteredDomain}`; `(*app.UseCase).CheckDomain(ctx, serverID, domain, excludeAppID string) (DomainCheck, error)`
  - `server.CheckDomainResponse{Available, Covered bool; Registration *model.RegisteredDomain (omitempty); Claims []model.DomainClaim}` (consumed by the web editor, Task 10)

- [ ] **Step 1: Write the failing tests**

`internal/domain/usecase/app/coverage_test.go`:

```go
package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

// ingressWith is an app config whose ingress routes host and redirects
// redirect (a domain-level redirect source) when non-empty.
func ingressWith(host, redirect string) []byte {
	cfg := `{"name":"demo","ingress":{"domains":[{"domain":"` + host + `","upstream_port":8088}],"redirects":[`
	if redirect != "" {
		cfg += `{"domain":"` + redirect + `","to":"https://` + host + `","code":301}`
	}
	return []byte(cfg + `]}}`)
}

func TestSaveAppRequiresACoveringRegistration(t *testing.T) {
	sd := newFakeServerDomains(
		model.RegisteredDomain{Name: "*.twillingate.app", TLS: "certificate"},
		model.RegisteredDomain{Name: "twillingate.app", TLS: "auto"},
	)
	ctx := context.Background()
	save := func(cfg []byte) (*recordingDispatcher, error) {
		disp := &recordingDispatcher{}
		_, err := newDomainsUseCase(disp, sd).SaveApp(ctx, "u", "srv-1", model.App{Name: "demo"}, command.AppPayload{Config: cfg}, false)
		return disp, err
	}

	for _, ok := range [][]byte{
		ingressWith("shop.twillingate.app", ""),
		ingressWith("shop.twillingate.app", "twillingate.app"),
		[]byte(`{"name":"no ingress"}`),
	} {
		if disp, err := save(ok); err != nil || len(disp.all) != 1 {
			t.Errorf("%s: err %v, dispatches %d", ok, err, len(disp.all))
		}
	}

	cases := map[string]struct {
		cfg  []byte
		host string
	}{
		"two labels deep":    {ingressWith("a.shop.twillingate.app", ""), "a.shop.twillingate.app"},
		"other domain":       {ingressWith("shop.example.com", ""), "shop.example.com"},
		"uncovered redirect": {ingressWith("shop.twillingate.app", "www.example.com"), "www.example.com"},
	}
	for name, tc := range cases {
		disp, err := save(tc.cfg)
		if !errors.Is(err, model.ErrDomainNotCovered) || !strings.Contains(err.Error(), tc.host+" is not covered") {
			t.Errorf("%s: err = %v", name, err)
		}
		if len(disp.all) != 0 {
			t.Errorf("%s: an uncovered save reached the agent", name)
		}
	}
	// The hint names the wildcard for the host's parent.
	_, err := save(ingressWith("shop.example.com", ""))
	if !strings.Contains(err.Error(), "register shop.example.com or *.example.com") {
		t.Errorf("hint = %v", err)
	}
	// Another server's registrations do not count.
	disp := &recordingDispatcher{}
	_, err = newDomainsUseCase(disp, sd).SaveApp(ctx, "u", "srv-2", model.App{Name: "demo"},
		command.AppPayload{Config: ingressWith("shop.twillingate.app", "")}, false)
	if !errors.Is(err, model.ErrDomainNotCovered) {
		t.Errorf("srv-2 save = %v, want not covered", err)
	}
}

func TestCheckDomainReportsTheWinningRegistration(t *testing.T) {
	sd := newFakeServerDomains(
		model.RegisteredDomain{Name: "*.twillingate.app", TLS: "certificate"},
		model.RegisteredDomain{Name: "app.twillingate.app", TLS: "off"},
	)
	uc := newDomainsUseCase(&recordingDispatcher{}, sd)
	ctx := context.Background()
	for host, want := range map[string]string{
		"shop.twillingate.app": "*.twillingate.app",
		"app.twillingate.app":  "app.twillingate.app", // exact wins
		"a.b.twillingate.app":  "",
	} {
		check, err := uc.CheckDomain(ctx, "srv-1", host, "")
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if check.Registration != nil {
			got = check.Registration.Name
		}
		if got != want {
			t.Errorf("%s: registration %q, want %q", host, got, want)
		}
	}
}
```

`internal/domain/usecase/app/usecase_test.go` (the check now returns a struct):

```diff
diff --git a/internal/domain/usecase/app/usecase_test.go b/internal/domain/usecase/app/usecase_test.go
--- a/internal/domain/usecase/app/usecase_test.go
+++ b/internal/domain/usecase/app/usecase_test.go
@@ -235,9 +235,9 @@ func TestCheckDomain(t *testing.T) {
 	repo := &fakeDomainRepo{claims: []model.DomainClaim{{Domain: "x.example.com", AppName: "Other"}}}
 	uc := newTestUseCaseWithDomains(&captureDispatcher{}, repo)
 
-	claims, err := uc.CheckDomain(context.Background(), "srv-1", "x.example.com", "app-1")
-	if err != nil || len(claims) != 1 || repo.claimServer != "srv-1" {
-		t.Fatalf("CheckDomain = %v, %v (server %q)", claims, err, repo.claimServer)
+	check, err := uc.CheckDomain(context.Background(), "srv-1", "x.example.com", "app-1")
+	if err != nil || len(check.Claims) != 1 || repo.claimServer != "srv-1" || check.Registration != nil {
+		t.Fatalf("CheckDomain = %+v, %v (server %q)", check, err, repo.claimServer)
 	}
 
 	if _, err := uc.CheckDomain(context.Background(), "srv-1", "NOT A HOST", ""); !errors.Is(err, model.ErrIngressInvalid) {
```

Contract (`internal/app/web/contract/apps_test.go`): an uncovered save is a 400 that never reaches the agent; the lifecycle registers `*.example.test` first; the check reports the winning registration; both servers in the per-server test register the same wildcard:

```diff
diff --git a/internal/app/web/contract/apps_test.go b/internal/app/web/contract/apps_test.go
--- a/internal/app/web/contract/apps_test.go
+++ b/internal/app/web/contract/apps_test.go
@@ -4,6 +4,7 @@ import (
 	"encoding/json"
 	"net/http"
 	"net/url"
+	"strings"
 	"testing"
 
 	happ "winterflow/internal/app/web/handler/app"
@@ -47,6 +48,20 @@ func TestAppLifecycle(t *testing.T) {
 	sid := owner.serverID(t)
 	ingress := json.RawMessage(`{"ingress":{"domains":[{"domain":"web.example.test","upstream_port":8080}]}}`)
 
+	t.Run("an uncovered hostname is refused", func(t *testing.T) {
+		r := owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
+			ServerID: sid, App: model.App{Name: "web", Icon: "box", Color: "#112233"}, Config: ingress,
+		}}).expect(t, http.StatusBadRequest)
+		const want = "web.example.test is not covered by a domain registered on this server — register web.example.test or *.example.test"
+		if !strings.Contains(string(r.Body), want) {
+			t.Fatalf("body = %s", r.Body)
+		}
+		if n := len(e.agent.seen(command.TypeAppSave)); n != 0 {
+			t.Fatalf("%d uncovered save(s) reached the agent", n)
+		}
+	})
+	registerDomain(t, owner, sid, "*.example.test", command.DomainTLSAuto)
+
 	appID := saveApp(t, owner, sid, "web", ingress)
 	apps := listApps(t, owner, sid)
 	if len(apps) != 1 || apps[0].ID != appID || apps[0].Name != "web" || len(apps[0].Domains) != 1 {
@@ -59,9 +74,15 @@ func TestAppLifecycle(t *testing.T) {
 	t.Run("domain check sees the claim", func(t *testing.T) {
 		var d happ.CheckDomainResponse
 		owner.get(t, "/api/v1/domains/check", url.Values{"server_id": {sid}, "domain": {"web.example.test"}}).expect(t, http.StatusOK).data(t, &d)
-		if d.Available || len(d.Claims) != 1 || d.Claims[0].AppName != "web" {
+		if d.Available || len(d.Claims) != 1 || d.Claims[0].AppName != "web" ||
+			!d.Covered || d.Registration == nil || d.Registration.Name != "*.example.test" || d.Registration.TLS != "auto" {
 			t.Fatalf("check = %+v", d)
 		}
+		var deep happ.CheckDomainResponse
+		owner.get(t, "/api/v1/domains/check", url.Values{"server_id": {sid}, "domain": {"a.web.example.test"}}).expect(t, http.StatusOK).data(t, &deep)
+		if !deep.Available || deep.Covered || deep.Registration != nil {
+			t.Fatalf("two labels under the wildcard = %+v", deep)
+		}
 		owner.get(t, "/api/v1/domains/check", url.Values{"server_id": {sid}, "domain": {"free.example.test"}}).expect(t, http.StatusOK).data(t, &d)
 		if !d.Available {
 			t.Fatalf("free domain = %+v", d)
@@ -190,6 +211,9 @@ func TestHostnameUniquePerServer(t *testing.T) {
 	first := owner.serverID(t)
 	second := owner.registerServer(t)
 	shop := json.RawMessage(`{"ingress":{"domains":[{"domain":"shop.example.test","upstream_port":8080}]}}`)
+	// The same registration may exist on any number of servers.
+	registerDomain(t, owner, first, "*.example.test", command.DomainTLSOff)
+	registerDomain(t, owner, second, "*.example.test", command.DomainTLSAuto)
 
 	saveApp(t, owner, first, "shop-a", shop)
 	saveApp(t, owner, second, "shop-b", shop)
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/domain/usecase/app/ ./internal/app/web/contract/`
Expected: the usecase package fails to compile (`check.Claims undefined (type []model.DomainClaim ...)`); after Step 3 compiles, before Step 4 the contract's "an uncovered hostname is refused" fails with `status = 202, want 400`.

- [ ] **Step 3: Coverage in the usecase** — `internal/domain/usecase/app/usecase.go`:

```diff
diff --git a/internal/domain/usecase/app/usecase.go b/internal/domain/usecase/app/usecase.go
--- a/internal/domain/usecase/app/usecase.go
+++ b/internal/domain/usecase/app/usecase.go
@@ -236,18 +236,57 @@ func (uc *UseCase) GetLogs(ctx context.Context, userID, serverID, appID string,
 	})
 }
 
-// CheckDomain reports which app (if any) on serverID already claims a
-// hostname — the live-typing availability check behind GET
-// /api/v1/domains/check. A hostname is unique per server only.
-func (uc *UseCase) CheckDomain(ctx context.Context, serverID, domain, excludeAppID string) ([]model.DomainClaim, error) {
+// DomainCheck answers GET /api/v1/domains/check: the apps on the server
+// already claiming the hostname, and the registration covering it (nil when
+// none does).
+type DomainCheck struct {
+	Claims       []model.DomainClaim
+	Registration *model.RegisteredDomain
+}
+
+// CheckDomain reports whether a hostname is free and covered on serverID —
+// the live-typing check behind GET /api/v1/domains/check. A hostname is
+// unique per server only.
+func (uc *UseCase) CheckDomain(ctx context.Context, serverID, domain, excludeAppID string) (DomainCheck, error) {
 	probe := model.Ingress{Domains: []model.IngressDomain{{Domain: domain, UpstreamPort: 1}}}
 	if err := probe.Validate(); err != nil {
-		return nil, fmt.Errorf("%w: %v", model.ErrIngressInvalid, err)
+		return DomainCheck{}, fmt.Errorf("%w: %v", model.ErrIngressInvalid, err)
 	}
-	if uc.domains == nil {
-		return nil, nil
+	var out DomainCheck
+	if uc.serverDomains != nil {
+		regs, err := uc.serverDomains.ListForServer(ctx, serverID)
+		if err != nil {
+			return DomainCheck{}, err
+		}
+		out.Registration = winningRegistration(regs, domain)
 	}
-	return uc.domains.FindClaims(ctx, serverID, []string{domain}, excludeAppID)
+	if uc.domains != nil {
+		claims, err := uc.domains.FindClaims(ctx, serverID, []string{domain}, excludeAppID)
+		if err != nil {
+			return DomainCheck{}, err
+		}
+		out.Claims = claims
+	}
+	return out, nil
+}
+
+// winningRegistration is the registration among regs covering host (exact
+// over wildcard), or nil.
+func winningRegistration(regs []model.RegisteredDomain, host string) *model.RegisteredDomain {
+	names := make([]string, len(regs))
+	for i, r := range regs {
+		names[i] = r.Name
+	}
+	win, ok := model.WinningDomain(names, host)
+	if !ok {
+		return nil
+	}
+	for i := range regs {
+		if regs[i].Name == win {
+			return &regs[i]
+		}
+	}
+	return nil
 }
 
 // SaveApp dispatches an app.save command to the server's agent and returns
@@ -284,6 +323,20 @@ func (uc *UseCase) SaveApp(ctx context.Context, userID, serverID string, app mod
 		if err := ing.Validate(); err != nil {
 			return "", fmt.Errorf("%w: %v", model.ErrIngressInvalid, err)
 		}
+		// Every hostname must sit under a domain registered on this
+		// server; the agent drops uncovered hosts again in BuildConfig.
+		if uc.serverDomains != nil {
+			regs, err := uc.serverDomains.ListForServer(ctx, serverID)
+			if err != nil {
+				uc.log.Error("SaveApp: list registered domains", "error", err, "server_id", serverID)
+				return "", err
+			}
+			for _, host := range ing.DomainNames() {
+				if winningRegistration(regs, host) == nil {
+					return "", model.NotCoveredError(host)
+				}
+			}
+		}
 		if uc.domains != nil {
 			claims, err := uc.domains.FindClaims(ctx, serverID, ing.DomainNames(), app.ID)
 			if err != nil {
```

- [ ] **Step 4: HTTP layer**

```diff
diff --git a/internal/app/web/handler/app/check_domain.go b/internal/app/web/handler/app/check_domain.go
--- a/internal/app/web/handler/app/check_domain.go
+++ b/internal/app/web/handler/app/check_domain.go
@@ -7,12 +7,15 @@ import (
 	"winterflow/internal/domain/model"
 )
 
-// CheckDomainResponse is the data of GET /api/v1/domains/check. Claims lists
-// the apps on the server already using the hostname (null when there are
-// none).
+// CheckDomainResponse is the data of GET /api/v1/domains/check. Available:
+// no other app on the server claims the hostname (Claims lists those that
+// do, null when none). Covered: a domain registered on the server covers it
+// (Registration is the one that wins, exact over wildcard).
 type CheckDomainResponse struct {
-	Available bool                `json:"available"`
-	Claims    []model.DomainClaim `json:"claims"`
+	Available    bool                    `json:"available"`
+	Covered      bool                    `json:"covered"`
+	Registration *model.RegisteredDomain `json:"registration,omitempty"`
+	Claims       []model.DomainClaim     `json:"claims"`
 }
 
 // CheckDomain answers "is this hostname free on this server?" for the app
@@ -32,7 +35,7 @@ func (h *Handler) CheckDomain(w http.ResponseWriter, r *http.Request) {
 		return
 	}
 	domain := r.URL.Query().Get("domain")
-	claims, err := h.usecase.CheckDomain(r.Context(), serverID, domain, r.URL.Query().Get("app_id"))
+	check, err := h.usecase.CheckDomain(r.Context(), serverID, domain, r.URL.Query().Get("app_id"))
 	if err != nil {
 		if errors.Is(err, model.ErrIngressInvalid) {
 			webutil.Error(w, err.Error(), nil)
@@ -41,5 +44,10 @@ func (h *Handler) CheckDomain(w http.ResponseWriter, r *http.Request) {
 		webutil.Error(w, "failed to check domain", nil)
 		return
 	}
-	webutil.Success(w, "", CheckDomainResponse{Available: len(claims) == 0, Claims: claims})
+	webutil.Success(w, "", CheckDomainResponse{
+		Available:    len(check.Claims) == 0,
+		Covered:      check.Registration != nil,
+		Registration: check.Registration,
+		Claims:       check.Claims,
+	})
 }
```

```diff
diff --git a/internal/app/web/handler/app/save_app.go b/internal/app/web/handler/app/save_app.go
--- a/internal/app/web/handler/app/save_app.go
+++ b/internal/app/web/handler/app/save_app.go
@@ -106,7 +106,7 @@ func (h *Handler) SaveApp(w http.ResponseWriter, r *http.Request) {
 
 	requestID, err := h.usecase.SaveApp(r.Context(), userID, req.ServerID, app, payload, req.Draft)
 	if err != nil {
-		if errors.Is(err, model.ErrIngressInvalid) || errors.Is(err, model.ErrDomainTaken) {
+		if errors.Is(err, model.ErrIngressInvalid) || errors.Is(err, model.ErrDomainTaken) || errors.Is(err, model.ErrDomainNotCovered) {
 			webutil.Error(w, err.Error(), nil)
 			return
 		}
```

- [ ] **Step 5: Regenerate and run**

Run:
```bash
make openapi
go test ./internal/...
make test-pg
```
Expected: PASS on both databases. The parity golden (`internal/app/web/contract/testdata/parity.golden.json`) is unchanged: its scenario saves apps without ingress.

- [ ] **Step 6: Commit**

```bash
git add internal api/openapi.yaml
git commit -m "feat(api): refuse ingress hostnames no registered domain covers

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Web — Domains tab in Server Settings

**Files:**
- Create: `web/src/lib/domains.ts`, `web/src/lib/domains.test.ts`
- Modify: `web/src/context/use-docker.ts`; create `web/src/context/use-docker.test.tsx`
- Create: `web/src/pages/settings-domains.tsx`, `web/src/pages/settings-domains.test.tsx`
- Modify: `web/src/pages/settings.tsx`; create `web/src/pages/settings.test.tsx`
- Modify: `web/src/test/setup.ts` (jsdom lacks `ResizeObserver`, which the Radix radio group needs)

**Interfaces:**
- Consumes: the routes of Task 7 (`/api/v1/domain/list|create|delete?server_id=`), `GET /api/v1/server/get-public-key`, `encryptSecret` (`web/src/lib/ecies.ts`).
- Produces:
  - `lib/domains.ts`: `type DomainTLS`, `type RegisteredDomain {name; tls; not_after?; issuer?; apps: string[] | null}`, `TLS_LABELS` (`Auto TLS` / `Certificate` / `TLS off`), `isWildcard`, `domainNameError`, `covers`, `winningDomain`, `certExpiry(notAfter?, now?) → "ok" | "soon" | "expired" | null` (soon = under 30 days)
  - `useDocker()` gains `listDomains(): Promise<RegisteredDomain[]>`, `createDomain(name, tls, cert?: {certificate, key}): Promise<void>` (encrypts `key`), `deleteDomain(name): Promise<unknown>`
  - `DomainsCard({ docker })`; the `domains` tab shows only when `activeServer.features.ingress` is true (`?tab=domains` falls back to registries otherwise)

- [ ] **Step 1: Write the failing helper tests** — `web/src/lib/domains.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import {
  certExpiry,
  covers,
  domainNameError,
  isWildcard,
  winningDomain,
} from "@/lib/domains";

describe("domain registrations", () => {
  it("validates names like the agent", () => {
    for (const ok of ["twillingate.app", "*.twillingate.app", "*.eu.twillingate.app", "localhost"]) {
      expect(domainNameError(ok)).toBe("");
    }
    for (const bad of ["", "*", "*.", "**.x.app", "a.*.x.app", "X.app", "x.app.", "x..app", "https://x.app", "*.*.x.app"]) {
      expect(domainNameError(bad)).not.toBe("");
    }
    expect(domainNameError(`${"a".repeat(250)}.app`)).not.toBe("");
    expect(isWildcard("*.x.app")).toBe(true);
    expect(isWildcard("x.app")).toBe(false);
  });

  it("covers one label under a wildcard, exact names only themselves", () => {
    expect(covers("twillingate.app", "twillingate.app")).toBe(true);
    expect(covers("twillingate.app", "a.twillingate.app")).toBe(false);
    expect(covers("*.twillingate.app", "a.twillingate.app")).toBe(true);
    expect(covers("*.twillingate.app", "twillingate.app")).toBe(false);
    expect(covers("*.twillingate.app", "a.b.twillingate.app")).toBe(false);
    expect(covers("*.twillingate.app", ".twillingate.app")).toBe(false);
    expect(covers("*.Twillingate.App", "A.twillingate.app")).toBe(true);
  });

  it("lets an exact name win over a wildcard", () => {
    const names = ["*.twillingate.app", "app.twillingate.app"];
    expect(winningDomain(names, "app.twillingate.app")).toBe("app.twillingate.app");
    expect(winningDomain(names, "shop.twillingate.app")).toBe("*.twillingate.app");
    expect(winningDomain(names, "a.b.twillingate.app")).toBeNull();
    expect(winningDomain([], "x.app")).toBeNull();
  });

  it("grades certificate expiry", () => {
    const now = new Date("2026-10-10T00:00:00Z");
    expect(certExpiry(undefined, now)).toBeNull();
    expect(certExpiry("garbage", now)).toBeNull();
    expect(certExpiry("2026-10-09T00:00:00Z", now)).toBe("expired");
    expect(certExpiry("2026-10-20T00:00:00Z", now)).toBe("soon");
    expect(certExpiry("2027-01-01T00:00:00Z", now)).toBe("ok");
  });
});
```

Run: `pnpm --dir web run test src/lib/domains.test.ts`
Expected: FAIL — `Failed to resolve import "@/lib/domains"`.

- [ ] **Step 2: The TS mirror of the rule** — `web/src/lib/domains.ts`:

```ts
// Registered domains: a TS mirror of internal/domain/model/domain.go (the Go
// rule is authoritative; this one drives suggestions and inline hints).

export type DomainTLS = "auto" | "certificate" | "off";

export type RegisteredDomain = {
  name: string;
  tls: DomainTLS;
  not_after?: string; // RFC 3339, certificate mode
  issuer?: string; // CN of the issuer, certificate mode
  apps: string[] | null; // app ids using a covered host
};

export const TLS_LABELS: Record<DomainTLS, string> = {
  auto: "Auto TLS",
  certificate: "Certificate",
  off: "TLS off",
};

// Same strict lowercase RFC-1123 rule as the ingress hostnames.
const HOSTNAME_RE =
  /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$/;

export function isWildcard(name: string): boolean {
  return name.startsWith("*.");
}

// domainNameError mirrors model.ValidateDomainName; "" means valid.
export function domainNameError(name: string): string {
  const host = isWildcard(name) ? name.slice(2) : name;
  if (name.length > 253 || !HOSTNAME_RE.test(host)) {
    return "Use a lowercase hostname like example.com, or *.example.com for every subdomain.";
  }
  return "";
}

// covers mirrors model.DomainCovers: an exact name covers itself; "*.s"
// covers "<one label>.s", not "s" and not "a.b.s". Case-insensitive.
export function covers(name: string, host: string): boolean {
  const n = name.toLowerCase();
  const h = host.toLowerCase();
  if (!isWildcard(n)) return n === h;
  const dot = h.indexOf(".");
  return dot > 0 && h.slice(dot + 1) === n.slice(2);
}

// winningDomain mirrors model.WinningDomain: the covering registration, an
// exact name winning over a wildcard; null when none covers host.
export function winningDomain(names: string[], host: string): string | null {
  let wildcard: string | null = null;
  for (const n of names) {
    if (!covers(n, host)) continue;
    if (!isWildcard(n)) return n;
    wildcard = n;
  }
  return wildcard;
}

export type CertExpiry = "ok" | "soon" | "expired";

// certExpiry grades a certificate's not_after: expired, under 30 days
// ("soon"), or fine. null without a date.
export function certExpiry(notAfter: string | undefined, now: Date = new Date()): CertExpiry | null {
  if (!notAfter) return null;
  const left = new Date(notAfter).getTime() - now.getTime();
  if (Number.isNaN(left)) return null;
  if (left <= 0) return "expired";
  return left < 30 * 24 * 60 * 60 * 1000 ? "soon" : "ok";
}
```

Run: `pnpm --dir web run test src/lib/domains.test.ts` — Expected: PASS.

- [ ] **Step 3: Write the failing hook test** — `web/src/context/use-docker.test.tsx` (the key is sent only as ciphertext):

```tsx
import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useDocker } from "@/context/use-docker";

vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));
const waitFor = vi.fn();
vi.mock("@/context/use-notifications", () => ({ useNotifications: () => ({ waitFor }) }));
vi.mock("@/lib/ecies", () => ({
  encryptSecret: vi.fn(async (plain: string, key: string) => `enc(${plain}|${key})`),
}));

type Sent = { url: string; method: string; body: unknown };
let sent: Sent[] = [];
const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input);
  sent.push({ url, method: init?.method ?? "GET", body: init?.body ? JSON.parse(String(init.body)) : undefined });
  if (url.includes("/api/v1/server/get-public-key")) {
    return new Response(JSON.stringify({ data: { public_key: "PUB" } }), { status: 200 });
  }
  return new Response(JSON.stringify({ data: { request_id: "r1" } }), { status: 202 });
});

describe("useDocker domain calls", () => {
  beforeEach(() => {
    sent = [];
    vi.stubGlobal("fetch", fetchMock);
    waitFor.mockResolvedValue({ status: 0, payload: {} });
  });
  afterEach(() => vi.unstubAllGlobals());

  it("encrypts the certificate key with the server's public key", async () => {
    const { result } = renderHook(() => useDocker());
    await result.current.createDomain("*.x.app", "certificate", { certificate: "CERT", key: "PLAIN-KEY" });

    const create = sent.find((s) => s.url.includes("/api/v1/domain/create"));
    expect(create?.url).toContain("server_id=srv-1");
    expect(create?.body).toEqual({ name: "*.x.app", tls: "certificate", certificate: "CERT", key: "enc(PLAIN-KEY|PUB)" });
    expect(JSON.stringify(sent)).not.toContain('"PLAIN-KEY"');
  });

  it("sends only the name and mode otherwise", async () => {
    const { result } = renderHook(() => useDocker());
    await result.current.createDomain("x.app", "off");
    expect(sent).toHaveLength(1);
    expect(sent[0].body).toEqual({ name: "x.app", tls: "off" });

    await result.current.deleteDomain("x.app");
    expect(sent[1]).toMatchObject({ method: "POST", body: { name: "x.app" } });
    expect(sent[1].url).toContain("/api/v1/domain/delete?server_id=srv-1");
  });

  it("lists domains from the result payload", async () => {
    const { result } = renderHook(() => useDocker());
    waitFor.mockResolvedValueOnce({ status: 0, payload: { domains: null } });
    expect(await result.current.listDomains()).toEqual([]);
    waitFor.mockResolvedValueOnce({ status: 0, payload: { domains: [{ name: "x.app", tls: "auto", apps: [] }] } });
    expect(await result.current.listDomains()).toEqual([{ name: "x.app", tls: "auto", apps: [] }]);
    waitFor.mockResolvedValueOnce({ status: 1, error: "ingress is disabled on this server" });
    await expect(result.current.listDomains()).rejects.toThrow("ingress is disabled on this server");
  });
});
```

Run: `pnpm --dir web run test src/context/use-docker.test.tsx`
Expected: FAIL — `result.current.createDomain is not a function`.

- [ ] **Step 4: Domain calls in the hook** — `web/src/context/use-docker.ts`:

```diff
diff --git a/web/src/context/use-docker.ts b/web/src/context/use-docker.ts
--- a/web/src/context/use-docker.ts
+++ b/web/src/context/use-docker.ts
@@ -3,6 +3,7 @@ import { useCallback, useMemo } from "react";
 import { useNotifications } from "@/context/use-notifications";
 import { useServers } from "@/context/use-servers";
 import { encryptSecret } from "@/lib/ecies";
+import type { DomainTLS, RegisteredDomain } from "@/lib/domains";
 import { apiBaseUrl } from "@/config";
 
 const base = apiBaseUrl.endsWith("/") ? apiBaseUrl.slice(0, -1) : apiBaseUrl;
@@ -150,6 +151,39 @@ export function useDocker() {
     [dispatch],
   );
 
+  const listDomains = useCallback(
+    () =>
+      dispatch<{ domains: RegisteredDomain[] | null }>("/api/v1/domain/list", {}).then(
+        (p) => p.domains ?? [],
+      ),
+    [dispatch],
+  );
+
+  // createDomain registers (or replaces) a name. In certificate mode the key
+  // is a secret: it is encrypted with the server key, like registry passwords.
+  const createDomain = useCallback(
+    async (
+      name: string,
+      tls: DomainTLS,
+      cert?: { certificate: string; key: string },
+    ) => {
+      const body: Record<string, string> = { name, tls };
+      if (tls === "certificate" && cert) {
+        const key = await getPublicKey();
+        body.certificate = cert.certificate;
+        body.key = await encryptSecret(cert.key, key);
+      }
+      await dispatch("/api/v1/domain/create", { method: "POST", body });
+    },
+    [dispatch, getPublicKey],
+  );
+
+  const deleteDomain = useCallback(
+    (name: string) =>
+      dispatch("/api/v1/domain/delete", { method: "POST", body: { name } }),
+    [dispatch],
+  );
+
   const updateAgent = useCallback(
     (version: string) =>
       dispatch("/api/v1/agent/update", { method: "POST", body: { version } }),
@@ -170,6 +204,9 @@ export function useDocker() {
       listVolumes,
       createVolume,
       deleteVolume,
+      listDomains,
+      createDomain,
+      deleteDomain,
       updateAgent,
     }),
     [
@@ -183,6 +220,9 @@ export function useDocker() {
       listVolumes,
       createVolume,
       deleteVolume,
+      listDomains,
+      createDomain,
+      deleteDomain,
       updateAgent,
     ],
   );
```

Run: `pnpm --dir web run test src/context/use-docker.test.tsx` — Expected: PASS.

- [ ] **Step 5: Write the failing card and page tests**

`web/src/pages/settings-domains.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";
import type { Docker } from "@/context/use-docker";
import type { RegisteredDomain } from "@/lib/domains";
import { DomainsCard } from "@/pages/settings-domains";

const toastError = vi.fn();
vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: (...args: unknown[]) => toastError(...args) },
}));

const day = 24 * 60 * 60 * 1000;
const inDays = (n: number) => new Date(Date.now() + n * day).toISOString();

const DOMAINS: RegisteredDomain[] = [
  { name: "*.twillingate.app", tls: "certificate", not_after: inDays(10), issuer: "CF Origin", apps: ["a1", "a2"] },
  { name: "old.example.com", tls: "certificate", not_after: inDays(-1), apps: [] },
  { name: "tunnel.example.com", tls: "off", apps: [] },
  { name: "example.com", tls: "auto", apps: ["a1"] },
];

function fakeDocker(over: Partial<Docker> = {}): Docker {
  return {
    listDomains: vi.fn().mockResolvedValue(DOMAINS),
    createDomain: vi.fn().mockResolvedValue(undefined),
    deleteDomain: vi.fn().mockResolvedValue(undefined),
    ...over,
  } as unknown as Docker;
}

function renderCard(docker: Docker) {
  const apps = {
    apps: [
      { id: "a1", name: "Shop" },
      { id: "a2", name: "Blog" },
    ],
  } as unknown as AppsContextValue;
  return render(
    <MemoryRouter>
      <AppsContext.Provider value={apps}>
        <DomainsCard docker={docker} />
      </AppsContext.Provider>
    </MemoryRouter>,
  );
}

describe("DomainsCard", () => {
  beforeEach(() => toastError.mockReset());

  it("lists names, modes, expiry and the apps using each", async () => {
    renderCard(fakeDocker());

    expect(await screen.findByText("*.twillingate.app")).toHaveClass("font-mono");
    expect(screen.getAllByText("Certificate").length).toBeGreaterThan(0);
    expect(screen.getByText("TLS off")).toBeInTheDocument();
    expect(screen.getByText("Auto TLS")).toBeInTheDocument();
    // Under 30 days is a warning; past is destructive.
    expect(screen.getByText(/^Expires /)).toHaveClass("text-amber-700");
    expect(screen.getByText(/^Expired /)).toHaveClass("text-destructive");
    expect(screen.getByText(/^Expires /)).toHaveAttribute("title", "Issued by CF Origin");
    expect(screen.getByText(/2 apps:/)).toBeInTheDocument();
    expect(screen.getAllByRole("link", { name: "Shop" })[0]).toHaveAttribute("href", "/app/a1");
    expect(screen.getByRole("link", { name: "Blog" })).toHaveAttribute("href", "/app/a2");
    expect(screen.getAllByText("Not used")).toHaveLength(2);
  });

  it("registers a domain with TLS off", async () => {
    const user = userEvent.setup();
    const docker = fakeDocker();
    renderCard(docker);

    await user.click(await screen.findByRole("button", { name: /Register domain/ }));
    const dialog = await screen.findByRole("dialog");
    const submit = within(dialog).getByRole("button", { name: "Register" });
    expect(submit).toBeDisabled();

    await user.type(within(dialog).getByLabelText("Name"), "*.*.bad");
    expect(within(dialog).getByText(/Use a lowercase hostname/)).toBeInTheDocument();
    expect(submit).toBeDisabled();

    await user.clear(within(dialog).getByLabelText("Name"));
    await user.type(within(dialog).getByLabelText("Name"), "*.Tunnel.Example.com");
    await user.click(within(dialog).getByRole("radio", { name: "TLS off" }));
    await user.click(submit);

    expect(docker.createDomain).toHaveBeenCalledWith("*.tunnel.example.com", "off", undefined);
    expect(docker.listDomains).toHaveBeenCalledTimes(2);
  });

  it("asks for a certificate and key in certificate mode", async () => {
    const user = userEvent.setup();
    const docker = fakeDocker();
    renderCard(docker);

    await user.click(await screen.findByRole("button", { name: /Register domain/ }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), "*.secure.app");
    expect(within(dialog).queryByLabelText("Certificate (PEM)")).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole("radio", { name: "Certificate" }));

    const submit = within(dialog).getByRole("button", { name: "Register" });
    await user.type(within(dialog).getByLabelText("Certificate (PEM)"), "CERT");
    expect(submit).toBeDisabled(); // the key is still missing
    await user.type(within(dialog).getByLabelText("Private key (PEM)"), "KEY");
    await user.click(submit);

    expect(docker.createDomain).toHaveBeenCalledWith("*.secure.app", "certificate", {
      certificate: "CERT",
      key: "KEY",
    });
  });

  it("replaces a certificate for an existing name", async () => {
    const user = userEvent.setup();
    const docker = fakeDocker();
    renderCard(docker);

    await user.click(await screen.findByRole("button", { name: "Replace certificate for *.twillingate.app" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByLabelText("Name")).toHaveValue("*.twillingate.app");
    expect(within(dialog).getByLabelText("Name")).toHaveAttribute("readonly");
    expect(within(dialog).getByRole("radio", { name: "Certificate" })).toBeChecked();
    await user.type(within(dialog).getByLabelText("Certificate (PEM)"), "NEW");
    await user.type(within(dialog).getByLabelText("Private key (PEM)"), "KEY");
    await user.click(within(dialog).getByRole("button", { name: "Replace" }));

    expect(docker.createDomain).toHaveBeenCalledWith("*.twillingate.app", "certificate", {
      certificate: "NEW",
      key: "KEY",
    });
  });

  it("removes a domain after confirmation and shows a refusal", async () => {
    const user = userEvent.setup();
    const docker = fakeDocker({
      deleteDomain: vi
        .fn()
        .mockResolvedValueOnce(undefined)
        .mockRejectedValueOnce(new Error("example.com is used by Shop (a1)")),
    });
    renderCard(docker);

    await user.click(await screen.findByRole("button", { name: "Remove domain tunnel.example.com" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Remove domain" }));
    expect(docker.deleteDomain).toHaveBeenCalledWith("tunnel.example.com");

    await user.click(screen.getByRole("button", { name: "Remove domain example.com" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Remove domain" }));
    expect(toastError).toHaveBeenCalledWith("Couldn't remove domain", {
      description: "example.com is used by Shop (a1)",
    });
  });

  it("says so when the list can't be loaded, or is empty", async () => {
    const { unmount } = renderCard(
      fakeDocker({ listDomains: vi.fn().mockRejectedValue(new Error("agent offline")) }),
    );
    expect(await screen.findByText("Couldn't load domains.")).toBeInTheDocument();
    unmount();

    renderCard(fakeDocker({ listDomains: vi.fn().mockResolvedValue([]) }));
    expect(await screen.findByText(/No domains yet/)).toBeInTheDocument();
  });
});
```

`web/src/pages/settings.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import SettingsPage from "@/pages/settings";

let ingress = true;

vi.mock("@/layouts/use-app-layout", () => ({ useAppBreadcrumbs: () => {} }));
vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ activeServer: { id: "s1", features: { ingress } } }),
}));
vi.mock("@/context/use-docker", () => ({
  useDocker: () => ({
    hasServer: true,
    listRegistries: vi.fn().mockResolvedValue([]),
    listNetworks: vi.fn().mockResolvedValue([]),
  }),
}));
vi.mock("@/pages/settings-domains", () => ({ DomainsCard: () => <p>domains card</p> }));
vi.mock("@/pages/settings-volumes", () => ({ VolumesCard: () => <p>volumes card</p> }));

function renderAt(url: string) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <SettingsPage />
    </MemoryRouter>,
  );
}

describe("SettingsPage", () => {
  it("offers the Domains tab on a server with ingress", () => {
    ingress = true;
    renderAt("/settings?tab=domains");
    expect(screen.getByRole("tab", { name: "Domains" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("domains card")).toBeInTheDocument();
  });

  it("hides it without ingress and falls back to registries", () => {
    ingress = false;
    renderAt("/settings?tab=domains");
    expect(screen.queryByRole("tab", { name: "Domains" })).not.toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Registries" })).toHaveAttribute("aria-selected", "true");
    expect(screen.queryByText("domains card")).not.toBeInTheDocument();
  });
});
```

`web/src/test/setup.ts` (append):

```diff
diff --git a/web/src/test/setup.ts b/web/src/test/setup.ts
--- a/web/src/test/setup.ts
+++ b/web/src/test/setup.ts
@@ -7,3 +7,11 @@ import { afterEach } from "vitest";
 afterEach(() => {
   cleanup();
 });
+
+// jsdom has no ResizeObserver; Radix primitives (radio group, …) measure
+// with it. A no-op stand-in is enough for behaviour tests.
+globalThis.ResizeObserver ??= class {
+  observe() {}
+  unobserve() {}
+  disconnect() {}
+};
```

Run: `pnpm --dir web run test src/pages`
Expected: FAIL — `Failed to resolve import "@/pages/settings-domains"`.

- [ ] **Step 6: The card** — `web/src/pages/settings-domains.tsx`:

```tsx
import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { KeyRound, Plus, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { buttonVariants } from "@/components/ui/button-variants";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Spinner } from "@/components/ui/spinner";
import { Textarea } from "@/components/ui/textarea";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { type Docker } from "@/context/use-docker";
import { useApps } from "@/context/use-apps";
import {
  certExpiry,
  domainNameError,
  isWildcard,
  TLS_LABELS,
  type DomainTLS,
  type RegisteredDomain,
} from "@/lib/domains";
import { cn } from "@/lib/utils";

const MODES: { value: DomainTLS; hint: string }[] = [
  {
    value: "auto",
    hint: "Winterflow gets a Let's Encrypt certificate for each app hostname. Use it with an A record pointing at this server (Cloudflare: DNS only).",
  },
  {
    value: "certificate",
    hint: "Upload a certificate and key, e.g. a Cloudflare Origin certificate behind the Cloudflare proxy.",
  },
  {
    value: "off",
    hint: "TLS ends in front of this server, e.g. a Cloudflare Tunnel. Apps are served over plain HTTP.",
  },
];

function TLSBadge({ tls }: { tls: DomainTLS }) {
  return (
    <Badge variant={tls === "off" ? "outline" : "secondary"} className="whitespace-nowrap">
      {TLS_LABELS[tls]}
    </Badge>
  );
}

function Expiry({ domain }: { domain: RegisteredDomain }) {
  const grade = certExpiry(domain.not_after);
  if (domain.tls !== "certificate" || !grade || !domain.not_after) {
    return <span className="text-muted-foreground">—</span>;
  }
  const day = domain.not_after.slice(0, 10);
  return (
    <span
      title={domain.issuer ? `Issued by ${domain.issuer}` : undefined}
      className={cn(
        "whitespace-nowrap",
        grade === "soon" && "font-medium text-amber-700 dark:text-amber-400",
        grade === "expired" && "font-medium text-destructive",
      )}
    >
      {grade === "expired" ? `Expired ${day}` : `Expires ${day}`}
    </span>
  );
}

function UsedBy({ ids, names }: { ids: string[]; names: Map<string, string> }) {
  if (ids.length === 0) return <span className="text-muted-foreground">Not used</span>;
  return (
    <span>
      {ids.length === 1 ? "1 app: " : `${ids.length} apps: `}
      {ids.map((id, i) => (
        <span key={id}>
          {i > 0 && ", "}
          <Link to={`/app/${id}`} className="font-medium underline-offset-4 hover:underline">
            {names.get(id) ?? id}
          </Link>
        </span>
      ))}
    </span>
  );
}

// DomainDialog adds a registration, or (replace set) swaps the TLS mode or
// certificate of an existing one — the agent replaces a name on create. The
// parent remounts it (key) per opening, so state starts from replace.
function DomainDialog({
  docker,
  open,
  replace,
  onOpenChange,
  onSaved,
}: {
  docker: Docker;
  open: boolean;
  replace: RegisteredDomain | null;
  onOpenChange: (open: boolean) => void;
  onSaved: () => Promise<void>;
}) {
  const [name, setName] = useState(replace?.name ?? "");
  const [tls, setTLS] = useState<DomainTLS>(replace ? "certificate" : "auto");
  const [certificate, setCertificate] = useState("");
  const [key, setKey] = useState("");
  const [busy, setBusy] = useState(false);

  const trimmed = name.trim();
  const nameError = trimmed ? domainNameError(trimmed) : "";
  const needsPair = tls === "certificate";
  const canSave =
    trimmed !== "" &&
    !nameError &&
    !busy &&
    (!needsPair || (certificate.trim() !== "" && key.trim() !== ""));

  const save = async () => {
    setBusy(true);
    try {
      await docker.createDomain(
        trimmed,
        tls,
        needsPair ? { certificate: certificate.trim(), key: key.trim() } : undefined,
      );
      toast.success(replace ? "Domain updated" : "Domain registered");
      onOpenChange(false);
      await onSaved();
    } catch (e) {
      toast.error(replace ? "Couldn't update domain" : "Couldn't register domain", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xl">
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (canSave) void save();
          }}
        >
          <DialogHeader>
            <DialogTitle>{replace ? "Replace certificate" : "Register domain"}</DialogTitle>
            <DialogDescription>
              Apps on this server can only use hostnames under a registered domain.
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-1.5">
            <Label htmlFor="dom-name">Name</Label>
            <Input
              id="dom-name"
              placeholder="*.example.com"
              autoComplete="off"
              spellCheck={false}
              readOnly={replace !== null}
              className={cn("font-mono", nameError && "border-destructive focus-visible:ring-destructive")}
              aria-invalid={nameError !== ""}
              aria-describedby="dom-name-hint"
              value={name}
              onChange={(e) => setName(e.target.value.toLowerCase())}
            />
            <p
              id="dom-name-hint"
              className={cn("text-xs", nameError ? "text-destructive" : "text-muted-foreground")}
            >
              {nameError || "An exact hostname, or *.example.com for every single-label subdomain."}
            </p>
          </div>
          <div className="grid gap-1.5">
            <Label>TLS</Label>
            <RadioGroup value={tls} onValueChange={(v) => setTLS(v as DomainTLS)} className="gap-3">
              {MODES.map((m) => (
                <label key={m.value} className="flex items-start gap-2.5 text-sm">
                  <RadioGroupItem value={m.value} aria-label={TLS_LABELS[m.value]} className="mt-0.5" />
                  <span>
                    <span className="font-medium">{TLS_LABELS[m.value]}</span>
                    <span className="block text-xs text-muted-foreground">{m.hint}</span>
                  </span>
                </label>
              ))}
            </RadioGroup>
            {tls === "auto" && isWildcard(trimmed) ? (
              <p className="text-xs text-muted-foreground">
                Any single label is accepted; each app hostname under it gets its own certificate.
              </p>
            ) : null}
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
                  value={certificate}
                  onChange={(e) => setCertificate(e.target.value)}
                />
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="dom-key">Private key (PEM)</Label>
                <Textarea
                  id="dom-key"
                  rows={4}
                  spellCheck={false}
                  className="font-mono text-xs"
                  placeholder="-----BEGIN PRIVATE KEY-----"
                  value={key}
                  onChange={(e) => setKey(e.target.value)}
                />
                <p className="text-xs text-muted-foreground">
                  Encrypted in your browser with this server's key; only the server can read it.
                </p>
              </div>
            </>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={!canSave}>
              {busy ? "Saving…" : replace ? "Replace" : "Register"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function DomainsCard({ docker }: { docker: Docker }) {
  const { apps } = useApps();
  const [items, setItems] = useState<RegisteredDomain[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [replace, setReplace] = useState<RegisteredDomain | null>(null);
  const [dialogKey, setDialogKey] = useState(0);
  const [toDelete, setToDelete] = useState<RegisteredDomain | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setItems(await docker.listDomains());
    } catch (e) {
      toast.error("Failed to load domains", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setLoading(false);
    }
  }, [docker]);

  useEffect(() => {
    void load();
  }, [load]);

  const remove = async (name: string) => {
    try {
      await docker.deleteDomain(name);
      toast.success("Domain removed");
    } catch (e) {
      // In use: the agent's message names the apps.
      toast.error("Couldn't remove domain", {
        description: e instanceof Error ? e.message : undefined,
      });
    }
    await load();
  };

  const names = useMemo(() => new Map(apps.map((a) => [a.id, a.name])), [apps]);
  const openDialog = (row: RegisteredDomain | null) => {
    setReplace(row);
    setDialogKey((k) => k + 1);
    setDialogOpen(true);
  };

  return (
    <Card>
      <CardHeader className="flex-row flex-wrap items-start justify-between gap-x-4 gap-y-3 space-y-0">
        <div className="min-w-0 space-y-1.5">
          <CardTitle>Domains</CardTitle>
          <CardDescription>
            The names apps on this server may use, and how their TLS works.
          </CardDescription>
        </div>
        <div className="flex items-center gap-2">
          <Button
            size="icon"
            variant="ghost"
            className="size-8 text-muted-foreground"
            aria-label="Refresh domains"
            title="Refresh domains"
            onClick={() => void load()}
            disabled={loading}
          >
            <RefreshCw className={cn("size-4", loading && "animate-spin")} />
          </Button>
          <Button size="sm" onClick={() => openDialog(null)}>
            <Plus className="size-4" /> Register domain
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        {items === null ? (
          <div className="flex h-24 items-center justify-center">
            {loading ? <Spinner /> : <p className="text-sm text-muted-foreground">Couldn't load domains.</p>}
          </div>
        ) : items.length === 0 ? (
          <p className="rounded-lg border border-dashed py-10 text-center text-sm text-muted-foreground">
            No domains yet. Register one before giving an app a hostname.
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>TLS</TableHead>
                <TableHead className="hidden sm:table-cell">Certificate</TableHead>
                <TableHead className="hidden sm:table-cell">Used by</TableHead>
                <TableHead className="w-20">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((d) => (
                <TableRow key={d.name}>
                  <TableCell className="font-mono">{d.name}</TableCell>
                  <TableCell>
                    <TLSBadge tls={d.tls} />
                  </TableCell>
                  <TableCell className="hidden sm:table-cell">
                    <Expiry domain={d} />
                  </TableCell>
                  <TableCell className="hidden sm:table-cell">
                    <UsedBy ids={d.apps ?? []} names={names} />
                  </TableCell>
                  <TableCell className="py-0.5 text-right">
                    {d.tls === "certificate" ? (
                      <Button
                        size="icon"
                        variant="ghost"
                        className="size-8 text-muted-foreground"
                        aria-label={`Replace certificate for ${d.name}`}
                        title="Replace certificate"
                        onClick={() => openDialog(d)}
                      >
                        <KeyRound className="size-4" />
                      </Button>
                    ) : null}
                    <Button
                      size="icon"
                      variant="ghost"
                      className="size-8 text-muted-foreground hover:text-destructive"
                      aria-label={`Remove domain ${d.name}`}
                      title="Remove domain"
                      onClick={() => setToDelete(d)}
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
      <DomainDialog
        key={dialogKey}
        docker={docker}
        open={dialogOpen}
        replace={replace}
        onOpenChange={setDialogOpen}
        onSaved={load}
      />
      <AlertDialog open={toDelete !== null} onOpenChange={(o) => !o && setToDelete(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove this domain?</AlertDialogTitle>
            <AlertDialogDescription>
              <code className="break-all font-mono text-foreground">{toDelete?.name}</code> will no
              longer be accepted on this server. A domain still used by an app can't be removed.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              className={buttonVariants({ variant: "destructive" })}
              onClick={() => {
                if (toDelete) void remove(toDelete.name);
              }}
            >
              Remove domain
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  );
}
```

And the tab, only for servers running the ingress — `web/src/pages/settings.tsx`:

```diff
diff --git a/web/src/pages/settings.tsx b/web/src/pages/settings.tsx
--- a/web/src/pages/settings.tsx
+++ b/web/src/pages/settings.tsx
@@ -32,13 +32,16 @@ import {
   type Network,
   type Registry,
 } from "@/context/use-docker";
+import { useServers } from "@/context/use-servers";
+import { DomainsCard } from "@/pages/settings-domains";
 import { VolumesCard } from "@/pages/settings-volumes";
 
-const TABS = ["registries", "networks", "volumes", "agent"] as const;
+const TABS = ["registries", "networks", "volumes", "domains", "agent"] as const;
 
 // SettingsPage is the server-level settings view (v1 "Server Settings"):
-// Docker registries, networks and volumes, plus agent self-update — one tab per
-// section, synced to ?tab= like the app details page.
+// Docker registries, networks and volumes, registered domains (servers with
+// ingress only), plus agent self-update — one tab per section, synced to
+// ?tab= like the app details page.
 export default function SettingsPage() {
   const breadcrumbs = useMemo(
     () => [{ label: "Server" }, { label: "Settings" }],
@@ -47,12 +50,17 @@ export default function SettingsPage() {
   useAppBreadcrumbs(breadcrumbs);
 
   const docker = useDocker();
+  const { activeServer } = useServers();
+  // Domains configure the embedded ingress: only servers running it.
+  const hasIngress = Boolean(activeServer?.features?.ingress);
   const [searchParams, setSearchParams] = useSearchParams();
 
   const tabParam = searchParams.get("tab") ?? "registries";
-  const tab = (TABS as readonly string[]).includes(tabParam)
-    ? tabParam
-    : "registries";
+  const tab =
+    (TABS as readonly string[]).includes(tabParam) &&
+    (tabParam !== "domains" || hasIngress)
+      ? tabParam
+      : "registries";
   const setTab = (next: string) => {
     setSearchParams(next === "registries" ? {} : { tab: next }, {
       replace: true,
@@ -73,6 +81,7 @@ export default function SettingsPage() {
         <TabsTrigger value="registries">Registries</TabsTrigger>
         <TabsTrigger value="networks">Networks</TabsTrigger>
         <TabsTrigger value="volumes">Volumes</TabsTrigger>
+        {hasIngress ? <TabsTrigger value="domains">Domains</TabsTrigger> : null}
         <TabsTrigger value="agent">Agent</TabsTrigger>
       </TabsList>
       <TabsContent value="registries">
@@ -84,6 +93,11 @@ export default function SettingsPage() {
       <TabsContent value="volumes">
         <VolumesCard docker={docker} />
       </TabsContent>
+      {hasIngress ? (
+        <TabsContent value="domains">
+          <DomainsCard docker={docker} />
+        </TabsContent>
+      ) : null}
       <TabsContent value="agent">
         <AgentCard docker={docker} />
       </TabsContent>
```

- [ ] **Step 7: Run the web checks**

Run:
```bash
pnpm --dir web run test
pnpm --dir web run lint
pnpm --dir web run build && touch web/dist/.gitkeep
```
Expected: all tests PASS, lint clean, build succeeds (the >500 kB chunk warning is pre-existing).

- [ ] **Step 8: Commit**

```bash
git add web/src
git commit -m "feat(web): Domains tab to register domains and their TLS mode

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Web — ingress editor suggestions, coverage hints and mode badges

The hostname inputs (routes and domain-level redirect sources) offer the server's registrations when empty: an exact name fills the field; a wildcard fills `.suffix` and puts the caret before it. The live check (now with `server_id`) shows "Not covered … Register a domain" (link to the Domains tab) or "Taken by app …". The per-host SSL switch is gone; the covering registration's mode shows as a read-only badge. New rows still carry `ssl: true`, which the proxy ignores and app cards use for the link scheme.

**Files:**
- Modify: `web/src/components/ingress-editor.tsx`; create `web/src/components/ingress-editor.test.tsx`
- Modify: `web/src/components/app-editor.tsx` (pass `serverId`)
- Modify: `docs/recipes/cloudflare-tunnel.md`

**Interfaces:**
- Consumes: `useDocker().listDomains`, `winningDomain`, `isWildcard`, `TLS_LABELS`, `RegisteredDomain` (Task 9); `GET /api/v1/domains/check` → `{available, covered, registration?, claims}` (Task 8).
- Produces: `IngressEditor` props `{state, onChange, appId?, serverId: string}`.

- [ ] **Step 1: Write the failing editor tests** — `web/src/components/ingress-editor.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { IngressEditor } from "@/components/ingress-editor";
import type { RegisteredDomain } from "@/lib/domains";
import type { AppEditorState, AppIngress } from "@/types/app-config";

const REGISTRATIONS: RegisteredDomain[] = [
  { name: "*.twillingate.app", tls: "certificate", apps: [] },
  { name: "example.com", tls: "auto", apps: [] },
  { name: "*.tunnel.example.com", tls: "off", apps: [] },
];

vi.mock("@/context/use-docker", () => ({
  useDocker: () => ({ listDomains: listDomainsMock }),
}));
const listDomainsMock = vi.fn(() => Promise.resolve(REGISTRATIONS));

// check answers GET /api/v1/domains/check per hostname.
type Check = { available: boolean; covered: boolean; claims: { app_name: string }[] | null };
let checks: Record<string, Check> = {};
const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = new URL(String(input), "http://localhost");
  const domain = url.searchParams.get("domain") ?? "";
  const data = checks[domain] ?? { available: true, covered: true, claims: null };
  return new Response(JSON.stringify({ success: true, data }), { status: 200 });
});

function Harness({ ingress }: { ingress: AppIngress }) {
  const [state, setState] = useState<AppEditorState>({
    config: { id: "app-1", name: "x", files: [], variables: [], ingress },
    files: {},
    variables: {},
  });
  return (
    <MemoryRouter>
      <IngressEditor state={state} onChange={setState} appId="app-1" serverId="srv-1" />
    </MemoryRouter>
  );
}

const oneDomain = (domain: string): AppIngress => ({
  domains: [{ id: "d1", domain, upstream_port: 8080, ssl: true }],
  redirects: [],
});

describe("IngressEditor", () => {
  beforeEach(() => {
    checks = {};
    fetchMock.mockClear();
    vi.stubGlobal("fetch", fetchMock);
  });
  afterEach(() => vi.unstubAllGlobals());

  it("suggests registrations; a wildcard pre-fills the suffix with the caret before it", async () => {
    const user = userEvent.setup();
    render(<Harness ingress={oneDomain("")} />);

    await user.click(await screen.findByRole("button", { name: "*.twillingate.app" }));
    const input = screen.getByLabelText<HTMLInputElement>("Hostname");
    expect(input).toHaveValue(".twillingate.app");
    await waitFor(() => expect(input).toHaveFocus());
    expect(input.selectionStart).toBe(0);

    await user.type(input, "shop", { skipClick: true });
    expect(input).toHaveValue("shop.twillingate.app");
    // The covering registration's mode replaces the old SSL switch.
    expect(screen.getByText("Certificate")).toBeInTheDocument();
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
  });

  it("fills an exact registration as is and checks it on this server", async () => {
    const user = userEvent.setup();
    render(<Harness ingress={oneDomain("")} />);

    await user.click(await screen.findByRole("button", { name: "example.com" }));
    expect(screen.getByLabelText("Hostname")).toHaveValue("example.com");
    expect(screen.getByText("Auto TLS")).toBeInTheDocument();
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const url = new URL(String(fetchMock.mock.calls[0][0]), "http://localhost");
    expect(url.searchParams.get("server_id")).toBe("srv-1");
    expect(url.searchParams.get("domain")).toBe("example.com");
    expect(url.searchParams.get("app_id")).toBe("app-1");
  });

  it("explains an uncovered hostname with a link to the Domains tab", async () => {
    const user = userEvent.setup();
    checks["shop.other.app"] = { available: true, covered: false, claims: null };
    render(<Harness ingress={oneDomain("")} />);

    await user.type(screen.getByLabelText("Hostname"), "shop.other.app");
    expect(await screen.findByText(/Not covered by a domain registered on this server/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Register a domain" })).toHaveAttribute("href", "/settings?tab=domains");
  });

  it("names the app that already takes the hostname", async () => {
    const user = userEvent.setup();
    checks["shop.twillingate.app"] = { available: false, covered: true, claims: [{ app_name: "Shop" }] };
    render(<Harness ingress={oneDomain("")} />);

    await user.type(screen.getByLabelText("Hostname"), "shop.twillingate.app");
    expect(await screen.findByText('Taken by app "Shop" on this server.')).toBeInTheDocument();
  });

  it("checks domain-level redirect sources and shows their mode", async () => {
    const user = userEvent.setup();
    checks["old.other.app"] = { available: true, covered: false, claims: null };
    render(
      <Harness
        ingress={{
          domains: [],
          redirects: [
            { id: "r1", domain: "", path: "", to: "https://example.com", code: 301, ssl: true },
            { id: "r2", domain: "x.tunnel.example.com", path: "", to: "https://example.com", code: 301, ssl: true },
          ],
        }}
      />,
    );
    expect(await screen.findByText("TLS off")).toBeInTheDocument();
    const [first] = screen.getAllByLabelText("Redirect hostname");
    await user.type(first, "old.other.app");
    expect(await screen.findByText(/Not covered/)).toBeInTheDocument();
  });

  it("works without registrations", async () => {
    listDomainsMock.mockRejectedValueOnce(new Error("agent offline"));
    render(<Harness ingress={oneDomain("")} />);
    await waitFor(() => expect(listDomainsMock).toHaveBeenCalled());
    expect(screen.queryByText("Use:")).not.toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run them to verify they fail**

Run: `pnpm --dir web run test src/components/ingress-editor.test.tsx`
Expected: FAIL — `Unable to find an accessible element with the role "button" and name "*.twillingate.app"` (no suggestions yet) and missing `Hostname` labels.

- [ ] **Step 3: Rework the editor** — `web/src/components/ingress-editor.tsx` becomes (the redirect `Select`, the port input and the add/remove plumbing are unchanged; the SSL `Switch` import and both switches are gone):

```tsx
import { Plus, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { EditorSection } from "@/components/editor-section";
import { apiBaseUrl } from "@/config";
import { useDocker } from "@/context/use-docker";
import {
  isWildcard,
  TLS_LABELS,
  winningDomain,
  type RegisteredDomain,
} from "@/lib/domains";
import {
  localId,
  type AppEditorState,
  type AppIngress,
  type IngressDomain,
  type IngressRedirect,
} from "@/types/app-config";

type Props = {
  state: AppEditorState;
  onChange: (next: AppEditorState) => void;
  appId?: string;
  serverId: string;
};

// Issue is the live check's verdict on one hostname: another app on this
// server claims it, or no registered domain covers it.
type Issue = { kind: "taken"; app: string } | { kind: "uncovered" };

// HostHints shows, under a hostname input, the live check's problem (with a
// way to fix it) or the TLS mode the covering registration gives the host.
function HostHints({
  host,
  issue,
  registrations,
}: {
  host: string;
  issue?: Issue;
  registrations: RegisteredDomain[];
}) {
  if (!host) return null;
  if (issue?.kind === "taken") {
    return (
      <p className="mt-2 text-xs text-destructive">
        Taken by app "{issue.app}" on this server.
      </p>
    );
  }
  if (issue?.kind === "uncovered") {
    return (
      <p className="mt-2 text-xs text-destructive">
        Not covered by a domain registered on this server.{" "}
        <Link to="/settings?tab=domains" className="font-medium underline underline-offset-4">
          Register a domain
        </Link>
      </p>
    );
  }
  const win = winningDomain(
    registrations.map((r) => r.name),
    host,
  );
  const reg = registrations.find((r) => r.name === win);
  if (!reg) return null;
  return (
    <p className="mt-2 flex items-center gap-1.5 text-xs text-muted-foreground">
      <Badge variant={reg.tls === "off" ? "outline" : "secondary"}>{TLS_LABELS[reg.tls]}</Badge>
      via <code className="font-mono">{reg.name}</code>
    </p>
  );
}

// Suggestions offers the server's registrations for an empty hostname:
// an exact name fills the field; a wildcard fills ".suffix" and puts the
// caret before it, ready for the label.
function Suggestions({
  registrations,
  onPick,
}: {
  registrations: RegisteredDomain[];
  onPick: (value: string, caretAtStart: boolean) => void;
}) {
  if (registrations.length === 0) return null;
  return (
    <div className="mt-2 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
      <span>Use:</span>
      {registrations.map((r) => (
        <Button
          key={r.name}
          type="button"
          size="sm"
          variant="outline"
          className="h-6 px-2 font-mono text-xs"
          onClick={() =>
            isWildcard(r.name) ? onPick(r.name.slice(1), true) : onPick(r.name, false)
          }
        >
          {r.name}
        </Button>
      ))}
    </div>
  );
}

const emptyIngress = (): AppIngress => ({ domains: [], redirects: [] });

const REDIRECT_CODES = [301, 302, 307, 308] as const;

// IngressEditor edits an app's domains (host -> upstream port) and redirect
// rules. Hostnames must sit under a domain registered on the server; TLS
// follows that registration's mode (shown as a badge). It matches the
// Files/Variables card idiom in app-editor.tsx: local rows keyed by localId,
// patched via map/filter over the config array.
export function IngressEditor({ state, onChange, appId, serverId }: Props) {
  // issues[domain] = the live check's verdict (absent = fine or unchecked).
  // Keyed by the domain text itself, so editing a domain to a different
  // string naturally stops showing the old error — nothing extra to clear.
  const [issues, setIssues] = useState<Record<string, Issue | undefined>>({});
  const timers = useRef<Record<string, ReturnType<typeof setTimeout>>>({});
  const inputs = useRef<Record<string, HTMLInputElement | null>>({});
  const [registrations, setRegistrations] = useState<RegisteredDomain[]>([]);
  const { listDomains } = useDocker();

  // The server's registrations drive suggestions and the TLS badges.
  useEffect(() => {
    let live = true;
    listDomains()
      .then((list) => live && setRegistrations(list))
      .catch(() => {
        // Advisory only: without them the editor just offers no suggestions.
      });
    return () => {
      live = false;
    };
  }, [listDomains, serverId]);

  // Cancel any in-flight debounce timers if the editor unmounts (e.g. the
  // user navigates away mid-check) so a stale check never calls setState.
  useEffect(() => {
    const timersAtMount = timers.current;
    return () => {
      Object.values(timersAtMount).forEach(clearTimeout);
    };
  }, []);

  const ing = state.config.ingress ?? emptyIngress();
  const setIngress = (next: AppIngress) =>
    onChange({ ...state, config: { ...state.config, ingress: next } });

  const checkDomain = (id: string, domain: string) => {
    clearTimeout(timers.current[id]);
    if (!domain) return;
    timers.current[id] = setTimeout(async () => {
      try {
        const qs = new URLSearchParams({ server_id: serverId, domain });
        if (appId) qs.set("app_id", appId);
        const res = await fetch(`${apiBaseUrl}/api/v1/domains/check?${qs}`, {
          credentials: "include",
        });
        if (!res.ok) return; // e.g. not a hostname yet: the form validation says so
        const body = (await res.json()) as {
          data?: {
            available?: boolean;
            covered?: boolean;
            claims?: { app_name: string }[] | null;
          };
        };
        const claim = body.data?.claims?.[0];
        const issue: Issue | undefined =
          body.data?.available === false && claim
            ? { kind: "taken", app: claim.app_name }
            : body.data?.covered === false
              ? { kind: "uncovered" }
              : undefined;
        setIssues((prev) => ({ ...prev, [domain]: issue }));
      } catch {
        // Advisory; the save-time check is authoritative.
      }
    }, 400);
  };

  // pick fills a hostname from a suggestion; a wildcard's ".suffix" gets
  // the caret at its start so the label can be typed straight away.
  const pick = (
    id: string,
    set: (value: string) => void,
    value: string,
    caretAtStart: boolean,
  ) => {
    set(value);
    if (!caretAtStart) checkDomain(id, value);
    requestAnimationFrame(() => {
      const el = inputs.current[id];
      if (!el) return;
      el.focus();
      if (caretAtStart) el.setSelectionRange(0, 0);
    });
  };

  // --- domains ---
  const updateDomain = (id: string, patch: Partial<IngressDomain>) =>
    setIngress({
      ...ing,
      domains: ing.domains.map((d) => (d.id === id ? { ...d, ...patch } : d)),
    });

  const addDomain = () =>
    setIngress({
      ...ing,
      domains: [
        ...ing.domains,
        { id: localId("dom"), domain: "", upstream_port: "", ssl: true },
      ],
    });

  const removeDomain = (id: string) => {
    clearTimeout(timers.current[id]);
    delete timers.current[id];
    const removed = ing.domains.find((d) => d.id === id);
    if (removed) {
      setIssues((prev) => {
        if (!(removed.domain in prev)) return prev;
        const next = { ...prev };
        delete next[removed.domain];
        return next;
      });
    }
    setIngress({ ...ing, domains: ing.domains.filter((d) => d.id !== id) });
  };

  // --- redirects ---
  const updateRedirect = (id: string, patch: Partial<IngressRedirect>) =>
    setIngress({
      ...ing,
      redirects: ing.redirects.map((r) =>
        r.id === id ? { ...r, ...patch } : r,
      ),
    });

  const addRedirect = () =>
    setIngress({
      ...ing,
      redirects: [
        ...ing.redirects,
        {
          id: localId("red"),
          domain: "",
          path: "",
          to: "",
          code: 301,
          ssl: true,
        },
      ],
    });

  const removeRedirect = (id: string) => {
    clearTimeout(timers.current[id]);
    delete timers.current[id];
    setIngress({
      ...ing,
      redirects: ing.redirects.filter((r) => r.id !== id),
    });
  };

  return (
    <EditorSection
      title="Domains & Routing"
      description="Serve the app through the built-in proxy on hostnames under the domains registered on this server. TLS follows each domain's mode."
    >
      <div className="space-y-6">
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <Label className="text-sm font-medium">Domains</Label>
            <Button size="sm" variant="outline" onClick={addDomain}>
              <Plus className="size-4" /> Add domain
            </Button>
          </div>
          {ing.domains.length === 0 ? (
            <p className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">
              Add a domain to route external traffic to this app.
            </p>
          ) : null}
          {ing.domains.map((d) => (
            <div key={d.id} className="rounded-md border p-3">
              <div className="flex items-center gap-2">
                <Input
                  ref={(el) => {
                    inputs.current[d.id] = el;
                  }}
                  value={d.domain}
                  placeholder="app.example.com"
                  aria-label="Hostname"
                  className="font-mono"
                  onChange={(e) => {
                    const domain = e.target.value.trim().toLowerCase();
                    updateDomain(d.id, { domain });
                    checkDomain(d.id, domain);
                  }}
                />
                <Input
                  value={d.upstream_port}
                  type="number"
                  placeholder="8088"
                  aria-label="Upstream port"
                  className="w-28 shrink-0"
                  onChange={(e) =>
                    updateDomain(d.id, {
                      upstream_port:
                        e.target.value === "" ? "" : Number(e.target.value),
                    })
                  }
                />
                <Button
                  size="icon"
                  variant="ghost"
                  className="size-8 shrink-0"
                  aria-label="Remove domain"
                  onClick={() => removeDomain(d.id)}
                >
                  <Trash2 className="size-4" />
                </Button>
              </div>
              {d.domain === "" ? (
                <Suggestions
                  registrations={registrations}
                  onPick={(value, caret) =>
                    pick(d.id, (domain) => updateDomain(d.id, { domain }), value, caret)
                  }
                />
              ) : (
                <HostHints host={d.domain} issue={issues[d.domain]} registrations={registrations} />
              )}
              <p className="mt-2 text-xs text-muted-foreground">
                The compose file must publish this port on 127.0.0.1, e.g.{" "}
                <code>127.0.0.1:8088:80</code>.
              </p>
            </div>
          ))}
        </div>

        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <Label className="text-sm font-medium">Redirects</Label>
            <Button size="sm" variant="outline" onClick={addRedirect}>
              <Plus className="size-4" /> Add redirect
            </Button>
          </div>
          {ing.redirects.length === 0 ? (
            <p className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">
              Redirect a domain (or a path on it) to another URL.
            </p>
          ) : null}
          {ing.redirects.map((r) => (
            <div key={r.id} className="rounded-md border p-3">
              <div className="flex flex-wrap items-center gap-2">
                <Input
                  ref={(el) => {
                    inputs.current[r.id] = el;
                  }}
                  value={r.domain}
                  placeholder="old.example.com"
                  aria-label="Redirect hostname"
                  className="font-mono sm:max-w-56"
                  onChange={(e) => {
                    const domain = e.target.value.trim().toLowerCase();
                    updateRedirect(r.id, { domain });
                    // Path rules ride a domain checked above; only a
                    // domain-level redirect claims its hostname.
                    if (r.path === "") checkDomain(r.id, domain);
                  }}
                />
                <Input
                  value={r.path}
                  placeholder="/old-path/* (empty = whole domain)"
                  className="font-mono sm:max-w-64"
                  onChange={(e) =>
                    updateRedirect(r.id, { path: e.target.value })
                  }
                />
                <Input
                  value={r.to}
                  placeholder="https://new.example.com/new-path"
                  className="font-mono flex-1"
                  onChange={(e) => updateRedirect(r.id, { to: e.target.value })}
                />
                <Select
                  value={String(r.code)}
                  onValueChange={(v) =>
                    updateRedirect(r.id, {
                      code: Number(v) as IngressRedirect["code"],
                    })
                  }
                >
                  <SelectTrigger className="h-9 w-24 shrink-0">
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
                  className="size-8 shrink-0"
                  aria-label="Remove redirect"
                  onClick={() => removeRedirect(r.id)}
                >
                  <Trash2 className="size-4" />
                </Button>
              </div>
              {r.path !== "" ? null : r.domain === "" ? (
                <Suggestions
                  registrations={registrations}
                  onPick={(value, caret) =>
                    pick(r.id, (domain) => updateRedirect(r.id, { domain }), value, caret)
                  }
                />
              ) : (
                <HostHints host={r.domain} issue={issues[r.domain]} registrations={registrations} />
              )}
            </div>
          ))}
        </div>
      </div>
    </EditorSection>
  );
}
```

`web/src/components/app-editor.tsx`:

```diff
diff --git a/web/src/components/app-editor.tsx b/web/src/components/app-editor.tsx
--- a/web/src/components/app-editor.tsx
+++ b/web/src/components/app-editor.tsx
@@ -390,6 +390,7 @@ export function AppEditor({ state, onChange }: Props) {
           state={state}
           onChange={onChange}
           appId={state.config.id || undefined}
+          serverId={activeServer.id}
         />
       ) : (
         <EditorSection
```

- [ ] **Step 4: Update the Cloudflare Tunnel recipe** — `docs/recipes/cloudflare-tunnel.md`:

```diff
diff --git a/docs/recipes/cloudflare-tunnel.md b/docs/recipes/cloudflare-tunnel.md
--- a/docs/recipes/cloudflare-tunnel.md
+++ b/docs/recipes/cloudflare-tunnel.md
@@ -61,13 +61,21 @@ routes by hostname from there. A wildcard hostname (`*.example.com`) also
 works; add the wildcard CNAME pointing at `<tunnel-id>.cfargotunnel.com`
 to your DNS zone yourself.
 
-## 4. Configure app domains in winterflow
+## 4. Register the domain with TLS off
 
-In each app's **Domains & Routing** card: add the domain, set the upstream
-port to the app's published host port, and leave **SSL off**. Cloudflare
-already terminates TLS at the edge; with no inbound 80/443, Let's Encrypt
-issuance can't complete behind a tunnel, so SSL-on domains would only
-produce ACME warnings in the agent log.
+In **Server → Settings → Domains**, register the names the tunnel serves —
+`*.example.com` covers every single-label subdomain — and pick **TLS off**.
+Cloudflare already terminates TLS at the edge; with no inbound 80/443,
+Let's Encrypt issuance can't complete behind a tunnel, so winterflow serves
+these hostnames over plain HTTP only and never asks for a certificate.
+
+## 5. Configure app domains in winterflow
+
+In each app's **Domains & Routing** card, add the hostname (pick the
+registered domain from the suggestions and type the label) and set the
+upstream port to the app's published host port. The card shows the
+**TLS off** badge it inherits from the registration. An unknown hostname
+under the wildcard answers a plain 404.
 
 ## Notes
 
```

- [ ] **Step 5: Run the web checks**

Run:
```bash
pnpm --dir web run test
pnpm --dir web run lint
pnpm --dir web run build && touch web/dist/.gitkeep
```
Expected: PASS / clean / builds.

- [ ] **Step 6: Commit**

```bash
git add web/src docs/recipes/cloudflare-tunnel.md
git commit -m "feat(web): suggest registered domains and show TLS modes in the ingress editor

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Final verification (before opening the PR)

- [ ] `gofmt -l ./internal ./pkg ./cmd` prints nothing; `go vet ./...` is clean.
- [ ] `make test` passes (race, all packages, contract suite on SQLite incl. the coverage guard).
- [ ] `make test-pg` passes (contract suite on Postgres; container removed afterwards — `docker ps -a | grep postgres:16` shows nothing new).
- [ ] `pnpm --dir web run test:coverage`, `pnpm --dir web run lint`, `make web-build` pass; `git status` shows `web/dist/.gitkeep` unchanged.
- [ ] `make diffcov DIFFCOV_BASE=feat/remove-roles` passes for Go and web (the plan was measured at about 87% Go / 95% web changed-line coverage) and the ratchet passes.
- [ ] `TestOpenAPIUpToDate` and `TestRoutesGolden` pass without `-update`.
- [ ] Manual smoke (optional, standalone with `INGRESS_ENABLED=true`, unprivileged ports, e.g. `INGRESS_HTTP_PORT=8081 INGRESS_HTTPS_PORT=8443`): an existing app with a hostname keeps serving after the upgrade (its host appears under Domains as an exact registration); register `*.example.test` with TLS off; give an app `shop.example.test`; `curl -H 'Host: shop.example.test' localhost:8081` reaches it and `curl -H 'Host: nope.example.test' localhost:8081` answers the catch-all 404.
- [ ] `.env.example` unchanged (no env var added).
