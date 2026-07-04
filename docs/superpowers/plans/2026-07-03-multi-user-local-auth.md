# Multi-user + Local-first Auth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Email+password auth (always on, bootstrap admin on first login of a fresh instance), optional Google, env auth deleted; admins manage org members with temp passwords and roles; admin-vs-member enforcement.

**Architecture:** New `user_credentials` table + `local` go-pkgz direct provider whose CredChecker bootstraps the admin when the users table is empty. Existing connected-accounts ClaimsUpd path resolves local logins unchanged. `requireAdmin` middleware (DB role lookup per request) gates org management and infrastructure mutations. React: reuse the existing username/password login form for the `local` provider; members page mirrors the PAT page patterns (one-time reveal).

**Tech Stack:** Go 1.25, Bun ORM, go-pkgz/auth v2 (`AddDirectProvider`), `golang.org/x/crypto/bcrypt`; React 19 + shadcn.

## Global Constraints

- Spec: `docs/superpowers/specs/2026-07-03-multi-user-local-auth-design.md` — its API shapes, error rules, and role matrix are normative.
- Emails normalized `strings.ToLower(strings.TrimSpace(email))` at every boundary.
- Bootstrap ONLY when `CountUsers() == 0`, re-checked inside the insert transaction.
- Handlers never see password hashes; hashing lives in the repository.
- Temp passwords: 16 chars, crypto/rand, PAT alphabet.
- `go test ./...` green + `pnpm --dir web run build && lint` green at every commit.

---

### Task 1: schema + models + domain types

**Files:** Create `internal/infra/db/migrations/20260703000002_user_credentials.go`; Modify `internal/infra/db/models/models.go`, `internal/domain/model/user.go`, `internal/infra/db/bootstrap.go` (RegisterModel), `go.mod` (bcrypt).

**Produces:**
- `models.UserCredentials{UserID(pk), Email(unique), PasswordHash, MustChangePassword bool, UpdatedAt}` (`bun:"table:user_credentials"`, json:"-" on hash).
- `model.Credentials{Email string; MustChangePassword bool}`; `model.Member{User; Email string; Role string; Provider string}`; errors `ErrInvalidCredentials`, `ErrLastOwner`, `ErrEmailTaken`, `ErrNotBootstrap = errors.New("users already exist")`.
- Migration follows the dialect-switch pattern of `20260703000001_pat_tokens.go`; columns per spec §Schema; index on nothing extra (email UNIQUE is the lookup).

Steps: write migration + models + `go get golang.org/x/crypto` → `go build ./... && go test ./internal/infra/db/...` → commit `feat(db): user_credentials table for local auth`.

### Task 2: repository (TDD)

**Files:** Modify `internal/domain/port/user.go`, `internal/infra/db/repository/user.go`; Test `internal/infra/db/repository/user_test.go`.

**Produces (port.UserRepository additions):**
```go
CountUsers(ctx context.Context) (int, error)
BootstrapLocalAdmin(ctx context.Context, email, password string) (model.User, error) // model.ErrNotBootstrap when users exist
VerifyLocalCredentials(ctx context.Context, email, password string) (model.User, error) // model.ErrInvalidCredentials
CreateMemberUser(ctx context.Context, orgID, name, email, role, tempPassword string) (model.User, error) // ErrEmailTaken on dup
SetPassword(ctx context.Context, userID, password string, mustChange bool) error
GetCredentials(ctx context.Context, userID string) (model.Credentials, error) // sql.ErrNoRows → model.ErrorUserNotFound
ListMembers(ctx context.Context, orgID string) ([]model.Member, error)
UpdateMemberRole(ctx context.Context, orgID, userID, role string) error // model.ErrLastOwner guard
RemoveMember(ctx context.Context, orgID, userID string) error           // deletes user row; ErrLastOwner guard
RoleOf(ctx context.Context, userID string) (string, error)
```
Implementation notes: `BootstrapLocalAdmin` runs in `db.RunInTx`: `SELECT count(*) FROM users` → if >0 return ErrNotBootstrap; then reuse the CreateUser insert sequence (user, org, owner membership, connected account provider="local" external_id=email) + credentials insert. `CreateMemberUser`: tx WITHOUT org creation — user + credentials(mustChange=true) + connected account + membership(orgID, role). `VerifyLocalCredentials`: select credentials by email → bcrypt compare → GetUser. Last-owner guard: `SELECT count(*) FROM organization_users WHERE organization_id=? AND role='owner'` and target's current role. `RemoveMember` verifies membership in orgID then `DELETE FROM users WHERE user_id=?` (cascades). bcrypt at `bcrypt.DefaultCost`.

**Tests (write first, verify FAIL, then implement, verify PASS):** `TestBootstrapLocalAdminCreatesOwnerOnce` (second call → ErrNotBootstrap; email normalized: login "  Admin@X.io " verifies as "admin@x.io"), `TestVerifyLocalCredentials` (ok / wrong pw → ErrInvalidCredentials / unknown → ErrInvalidCredentials), `TestCreateMemberUserJoinsOrgWithoutNewOrg` (org count unchanged; RoleOf=member; dup email → ErrEmailTaken; verify temp password works and GetCredentials shows MustChangePassword), `TestSetPasswordClearsMustChange`, `TestUpdateMemberRoleLastOwnerGuard`, `TestRemoveMemberDeletesUserAndTokens` (create PAT for member, remove, FindByToken → ErrInvalidToken), `TestRoleOf`.

Commit: `feat(db): local credentials, org members, bootstrap admin`.

### Task 3: local provider + env auth removal

**Files:** Create `internal/app/web/auth/local.go` + `local_test.go`; Delete `internal/app/web/auth/env.go`; Modify `internal/app/web/bootstrap.go`, `pkg/config` (remove GetEnvAuth + env branch of IsAuthSupported + tests), `.env.dist`.

**Produces:** `auth.AddLocalAuth(service *auth.Service, log *logger.Logger, users LocalUserStore)` where
```go
type LocalUserStore interface {
    CountUsers(ctx context.Context) (int, error)
    BootstrapLocalAdmin(ctx context.Context, email, password string) (model.User, error)
    VerifyLocalCredentials(ctx context.Context, email, password string) (model.User, error)
}
```
Checker logic (unit-test with a fake store): normalize email; empty email/password → false; CountUsers==0 → BootstrapLocalAdmin (race loser falls through to Verify); else Verify. Register with `service.AddDirectProvider("local", ...)`. In `bootstrap.go`: replace `authprvd.AddEnvAuth(...)` with `authprvd.AddLocalAuth(service, s.Logger, s.Deps.UserService)`. Keep ClaimsUpd + BasicAuthChecker untouched.

**Tests:** `TestCheckerBootstrapsOnEmpty`, `TestCheckerVerifiesWhenNotEmpty`, `TestCheckerRejectsBlank`.

Commit: `feat(auth): local email+password provider with first-login bootstrap; drop env auth`.

### Task 4: rbac middleware + endpoints (TDD)

**Files:** Create `internal/app/web/middleware/rbac/rbac.go` + test, `internal/app/web/handler/org/handler.go` + test; Modify `internal/app/web/handler/user/handler.go` + test (profile, change-password), `internal/app/web/routes.go`.

**Produces:**
- `rbac.RequireAdmin(roles interface{ RoleOf(ctx, string) (string, error) }) func(http.Handler) http.Handler` — 403 `util` JSON unless role ∈ {owner, admin}. (401 if no user in ctx.)
- org handler `Deps{Logger, Users port-slice}` with `CreateUser/GetMembers/UpdateMember/RemoveMember/ResetMemberPassword` per spec shapes; temp password via shared `pat`-alphabet helper `randomPassword(16)`; role must be `admin|member`; self-removal → 400; org resolved via `PrimaryOrganizationID(caller)`.
- user handler additions: `GetProfile` (`{user_id,name,email,role,must_change_password}`; email empty for no-credentials users), `ChangePassword` (min 8; verify current via `VerifyLocalCredentials(profile.email, current)`; Google-only → 400).
- `GET /api/v1/auth/state` — tiny public handler in routes.go closure: `{bootstrap: CountUsers()==0}`.
- Routes: `adminMW := rbac.RequireAdmin(s.Deps.UserService)`; org routes `With(authMW, adminMW)`; add adminMW to `server/register`, `agent/update`, `registry/create|delete`, `network/create|delete`.

**Tests:** middleware (member 403 / admin pass / owner pass); org handler with fake store: create-user returns temp password + dup email 400 + bad role 400; update/remove pass through ErrLastOwner → 400; self-remove 400; user handler: profile shape, change-password wrong-current 400 / short 400 / success clears flag (fake). Note bootstrap-state flip covered in E2E.

Commit: `feat(api): org member management + admin/member enforcement + auth state`.

### Task 5: web UI

**Files:** Modify `web/src/components/login-form.tsx` (+ its i18n strings if keyed), `web/src/context/auth-context.tsx` (login → provider "local"), `web/src/components/nav-user.tsx`, `web/src/main.tsx`; Create `web/src/hooks/use-profile.ts`, `web/src/pages/org-members.tsx`, `web/src/pages/user-password.tsx`; Modify `web/src/layouts/app-layout.tsx` (must-change redirect guard).

Key points:
- login: `env` provider references → `local`; email input (`type="email"`); fetch `GET /api/v1/auth/state` on mount, show bootstrap hint ("No accounts yet — your first login creates the admin account"); Google button stays provider-list-driven.
- `use-profile`: fetch `/api/v1/user/get-profile` once authenticated; expose `{profile, refresh}`; used by (a) app-layout guard: `if profile?.must_change_password && location.pathname !== "/user/password"` → `<Navigate to="/user/password" />`, (b) nav-user: show "Members" item when role owner/admin.
- `/org/members`: table + Add-user dialog (name, email, role select admin/member) → one-time temp-password reveal (reuse copyText pattern from user-tokens); row actions: role toggle, reset password (reveal dialog), remove (confirm). Same `api<T>` envelope helper style as use-tokens.
- `/user/password`: current/new/confirm; on success toast + refresh profile + navigate home.

Verify: `pnpm --dir web run build && pnpm --dir web run lint`. Commit: `feat(web): local login + bootstrap hint, forced password change, org members page`.

### Task 6: E2E smoke + docs

Scratch standalone (:18097, /tmp/wf-mu, NO AUTH_ENV vars — they no longer exist):
1. `auth/state` → bootstrap:true
2. `POST /auth/local/login?user=admin@example.com&passwd=SuperSecret1` → 200, JWT cookie; `auth/state` → bootstrap:false; profile role=owner; server auto-claimed (`get-servers` non-empty)
3. second login with wrong password → 403/401
4. admin `org/create-user` {bob, bob@x.io, member} → temp password
5. bob logs in with temp password; profile must_change_password:true; `change-password` (wrong current → 400; correct → 200); old temp rejected afterwards, new works, flag cleared
6. bob `get-servers` → sees admin's server; bob `org/create-user` → 403; bob `registry/create` → 403
7. admin `reset-member-password` → bob's new-old password rejected, reset one works (must_change true again)
8. admin `update-member` bob→admin → bob passes `org/get-members`; admin self-demote (last owner) → 400
9. `remove-member` bob → bob's login rejected
Kill scratch separately. Docs: MIGRATION.md (auth section + routes), CLAUDE.md **Auth** line, `.env.dist` (drop AUTH_ENV_*, comment Google as optional). Full `go test ./...` + web lint → commit `docs: local-first multi-user auth` → `git push origin v2`.
