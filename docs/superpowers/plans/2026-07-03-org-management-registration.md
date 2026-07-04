# Org Management + Registration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Explicit `/register` flow (env-toggleable) replacing login-bootstrap; org name/icon/color editable by admins; one model across topologies (standalone = single org via the claim-step rule).

**Architecture:** `POST /api/v1/auth/register` picks `BootstrapLocalAdmin` (standalone claim) or new `RegisterLocalUser` (distributed, own org). `registrationOpen(cfg, users)` helper is the single policy point shared by the endpoint, `auth/state`, and ClaimsUpd's find-vs-find-or-create switch. Org identity = two new columns + get/update endpoints + an Organization card on the members page reusing the app icon picker.

**Tech Stack:** unchanged (Go/Bun/go-pkgz; React/shadcn).

## Global Constraints

- Spec: `docs/superpowers/specs/2026-07-03-org-management-registration-design.md` (normative shapes/rules).
- Claim step (zero users) ALWAYS allows registration regardless of REGISTRATION_ENABLED.
- Registration never issues a session.
- `util.GetUserID` loses the `User.ID` fallback; all auth paths must set the `user_id` attr.
- TDD; full Go suite + web build/lint green per commit.

---

### Task 1: config + repo + migration

**Files:** Modify `pkg/config/config.go` (+test), `internal/domain/model/organization.go`, `internal/infra/db/models/models.go` (Organization icon/color), `internal/domain/port/user.go`, `internal/infra/db/repository/user.go` (+test); Create `internal/infra/db/migrations/20260703000003_org_identity.go`.

**Produces:**
```go
// config
IsRegistrationEnabled() bool // REGISTRATION_ENABLED: ""|"true"|"1" → true; "false"|"0" → false
// model
type Organization struct { ID, Name, Icon, Color string }
// port additions
RegisterLocalUser(ctx, name, email, password string) (model.User, error) // own org, owner; ErrEmailTaken
GetOrganization(ctx, orgID string) (model.Organization, error)
UpdateOrganization(ctx, orgID, name, icon, color string) error
```
Migration: `ALTER TABLE organizations ADD COLUMN icon VARCHAR(64) NOT NULL DEFAULT ''` + same for `color VARCHAR(7)`; down = no-op (SQLite can't drop columns portably; acceptable).
`RegisterLocalUser` mirrors `BootstrapLocalAdmin` minus the zero-check, org name `<name>'s org`, user name = provided name.

**Tests (first):** `TestIsRegistrationEnabled` (unset/true/1/false/0), `TestRegisterLocalUserCreatesOwnOrg` (role owner, distinct orgs for two registrants, dup email → ErrEmailTaken, login works), `TestGetUpdateOrganization` (roundtrip name/icon/color; unknown org → error).

Commit: `feat(db,config): registration toggle, RegisterLocalUser, org icon/color`.

### Task 2: auth policy + register endpoint

**Files:** Modify `internal/app/web/auth/local.go` (+test — remove bootstrap branch), `internal/app/web/util/user.go` (drop fallback), `internal/app/web/bootstrap.go` (ClaimsUpd find-only when closed), `internal/app/web/routes.go`; Create `internal/app/web/handler/auth/handler.go` (+test).

**Produces:**
- `handler/auth` package: `Deps{Logger, Cfg *config.ServerConfig, Users RegistrationStore}`;
  `RegistrationStore = interface{ CountUsers; BootstrapLocalAdmin; RegisterLocalUser }`.
  `Register(w,r)` per spec validation; policy: `open := users==0 || (!cfg.IsStandalone() && cfg.IsRegistrationEnabled())`; standalone+zero → BootstrapLocalAdmin; distributed → RegisterLocalUser (zero-user distributed also RegisterLocalUser). Closed → `util.Error` "registration is disabled".
  `State(w,r)` → `{bootstrap, registration_enabled}` (registration_enabled = the same `open` computation).
- Routes: `POST /api/v1/auth/register`, `GET /api/v1/auth/state` (replaces the inline closure) — both public.
- `localCredChecker(users)`: verify-only (delete Count/Bootstrap branch; store interface shrinks to `VerifyLocalCredentials`).
- ClaimsUpd: compute `open` (CountUsers + cfg) per login; when open → FindOrCreateUser (unchanged); when closed → `GetByConnectedAccount`; on miss, log + return claims WITHOUT setting `user_id` (and skip auto-claim).
- `GetUserID`: return `model`-agnostic error when attr missing (no fallback).

**Tests (first):** auth handler — open/closed matrix (standalone zero→ok, standalone nonzero→400, distributed toggle off nonzero→400, distributed toggle off ZERO→ok), validation 400s, dup email 400, response has no Set-Cookie; local_test rewrite (verify-only, blank rejects); GetUserID test (attr present ok, absent error) in a new `util` test file.

Commit: `feat(auth): explicit registration endpoint + REGISTRATION_ENABLED policy; login is verify-only`.

### Task 3: org endpoints

**Files:** Modify `internal/app/web/handler/org/handler.go` (+test), `internal/app/web/routes.go`.

- `GetOrganization` (member-visible: `With(authMW)` only) → `{org_id, name, icon, color}` via callerOrg + repo.
- `UpdateOrganization` (`With(authMW, adminMW)`): `{name, icon, color}`; name required ≤64, icon ≤64, color ≤7 (`#rrggbb` or empty).
- OrgStore interface += GetOrganization/UpdateOrganization.

**Tests (first):** get shape; update validation (empty name 400, long color 400); fake-store passthrough.

Commit: `feat(api): organization get/update (name, icon, color)`.

### Task 4: web

**Files:** Create `web/src/pages/register.tsx`; Modify `web/src/pages/login.tsx` (+LoginForm: register link + email prefill via router state), `web/src/pages/org-members.tsx` (Organization card + edit dialog), `web/src/main.tsx` (route `/register`), `web/src/locales/en.ts`.

- register page: name/email/password/confirm; on mount fetch `auth/state`; closed → message card; submit → `POST auth/register` → toast → `navigate("/login", {state:{email}})`.
- login: replace bootstrapHint prop usage with a "Create an account" link under the form shown when `state.registration_enabled`; prefill email input from `location.state?.email` (make the email input controlled with defaultValue).
- members page: Organization card (icon via the app icon rendering used by `icon-picker`/`app-icon`, name, color dot); admin Edit dialog: name Input, `IconPicker`, color input (`type="color"` like the app editor uses — check and mirror `app-editor.tsx`'s color control).

Verify `pnpm --dir web run build && lint`. Commit: `feat(web): register page, login prefill+link, organization identity card`.

### Task 5: E2E + docs

Scratch standalone :18096 `/tmp/wf-org`:
1. `auth/state` → `{bootstrap:true, registration_enabled:true}`
2. `POST auth/register` admin → 200, no Set-Cookie; state → `{bootstrap:false, registration_enabled:false}` (standalone, users>0)
3. second register → 400 "registration is disabled"
4. login admin → ok; old bootstrap-on-login is gone (login with unknown email → 403)
5. `get-organization` → default name; `update-organization` {name:"Homelab", icon:"server", color:"#3b82f6"} → get reflects
6. create member; member reads get-organization 200; member update-organization → 403
7. restart with REGISTRATION_ENABLED=false on a FRESH db → register still works (claim step)
Docs: `.env.dist` REGISTRATION_ENABLED line; MIGRATION.md (auth section: registration replaces bootstrap-on-login; org endpoints; /register page); CLAUDE.md Auth line tweak. Full suite + push v2.
