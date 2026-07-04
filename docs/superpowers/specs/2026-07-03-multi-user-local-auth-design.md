# Multi-user + local-first authentication — Design

**Date:** 2026-07-03
**Status:** Approved

## Goal

Multiple users share one WinterFlow instance. Email+password ("local") is the
always-on default authentication; Google OAuth is optional; env auth is
removed. The first login on a fresh instance creates the admin account.

## Decisions (user-approved)

1. **Local email+password is the default and cannot be turned off.**
2. **Google OAuth is optional** — enabled iff `AUTH_GOOGLE_CLIENT_ID/SECRET`
   are set (mechanism already exists; login page already adapts via
   go-pkgz `/auth/list`).
3. **Env auth is deleted** (provider, `AUTH_ENV_USERNAME/PASSWORD` config,
   `.env.dist` lines, docs). Upgrade note: an instance whose only user came
   from env auth loses that login; v2 is unreleased so only dev DBs are
   affected.
4. **Bootstrap:** if the `users` table is EMPTY, the first local login
   creates the admin (user + bcrypt credentials + `local` connected account
   + personal org with `owner` role) and logs them in. The strict
   zero-users condition is a security requirement — anything looser lets a
   stranger mint an admin on an existing instance.
5. **Sharing model:** admin-created users join the admin's organization
   (schema already supports it; server queries and SSE fan-out are already
   membership-based).
6. **Onboarding:** admin creates accounts with a generated temp password
   shown once; the user must change it on first login.
7. **Roles:** `owner`/`admin` = manage users/servers/settings; `member` =
   full app lifecycle, no administration. Enforced by a `requireAdmin`
   middleware that reads the role from the DB per request (demotion applies
   without re-login). Last owner cannot be demoted or removed.

## Schema

New migration `user_credentials` (1 row per local user):

```sql
CREATE TABLE user_credentials (
    user_id              char(36)/UUID PRIMARY KEY
                         REFERENCES users(user_id) ON DELETE CASCADE,
    email                VARCHAR(255) NOT NULL UNIQUE,
    password_hash        VARCHAR(100) NOT NULL,          -- bcrypt
    must_change_password BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at           timestamp NOT NULL DEFAULT now
);
```

Separate table (not columns on `users`): Google-only users have no
credentials. Emails are normalized lowercase+trimmed on every write/lookup.
bcrypt cost: DefaultCost. Dependency: `golang.org/x/crypto/bcrypt`.

## Auth flow

**Provider `local`** (go-pkgz `AddDirectProvider`, always registered) in
`internal/app/web/auth/local.go`. Its CredChecker:

1. Normalize email.
2. `CountUsers() == 0` → `BootstrapLocalAdmin(email, password)`: inside one
   transaction re-check the count, then create user (name = email
   local-part), credentials (`must_change_password=false` — the admin chose
   this password), connected account (`provider="local"`,
   `external_id=email`), org + `owner` membership. Unique constraints make a
   concurrent double-submit lose cleanly. Return ok.
3. Otherwise `VerifyLocalCredentials(email, password)`: lookup by email,
   `bcrypt.CompareHashAndPassword`. Wrong/unknown → not ok.

`ClaimsUpd` is untouched: local logins resolve through the existing
connected-accounts path (`FindOrCreateUser` finds, never creates, because
the checker guaranteed the account exists). Standalone auto-claim keeps
working (it runs in ClaimsUpd for every provider).

**Deleted:** `internal/app/web/auth/env.go`, `AddEnvAuth` call,
`GetEnvAuth`, `env` branch of `IsAuthSupported`, `AUTH_ENV_*` from
`.env.dist`, related config tests.

**Unchanged:** PATs (Bearer `wfp_…` + Basic), Google provider, JWT
sessions, `BasicAuthChecker`.

## API

Public (no auth):
- `GET /api/v1/auth/state` → `{bootstrap: bool}` — `users` count == 0.
  Login page shows a "first login creates the admin account" hint when
  true. (Google visibility already comes from `/auth/list`.)

Authenticated (any role):
- `GET /api/v1/user/get-profile` → `{user_id, name, email|null, role,
  must_change_password}` — email/must_change from credentials when the user
  has them; role from the primary org membership.
- `POST /api/v1/user/change-password` `{current_password, new_password}` —
  verify current, store new bcrypt hash, clear `must_change_password`.
  New password: min 4 chars. Users without local credentials (Google-only)
  get a 400.

Admin/owner only (gated by `requireAdmin`):
- `POST /api/v1/org/create-user` `{name, email, role}` → creates user (no
  personal org) + credentials (`must_change_password=true`, generated temp
  password) + `local` connected account + membership in the **caller's**
  org with `role` (`admin`|`member`). Returns `{user_id, email,
  temp_password}` — temp password shown once. Duplicate email → 400.
- `GET /api/v1/org/get-members` → `[{user_id, name, email|null, role,
  provider, last_seen, created_at}]` for the caller's org.
- `POST /api/v1/org/update-member` `{user_id, role}` — last-owner guard.
- `POST /api/v1/org/remove-member` `{user_id}` — deletes the user row
  outright (cascades: credentials, connected accounts, PATs, membership).
  Safe: every non-owner member is admin-created. Last-owner guard; caller
  cannot remove themself.
- `POST /api/v1/org/reset-member-password` `{user_id}` → new temp password
  (returned once), sets `must_change_password=true`. Target must be a
  member of the caller's org with local credentials.

Temp passwords: 16 chars from the PAT alphabet via crypto/rand (helper in
`pkg/pat` or local to the repo — implementer's choice, but crypto/rand).

**`requireAdmin` middleware** (`internal/app/web/middleware/rbac`): resolves
the caller's role via `RoleOf(userID)` (role in primary org); `owner` or
`admin` pass, anything else → 403 JSON. Also newly applied to:
`server/register`, `agent/update`, `registry/create`, `registry/delete`,
`network/create`, `network/delete`. Members keep: full app lifecycle,
registry/network **list**, servers list/status, public key, SSE, own PATs.

## Repository additions (port.UserRepository)

```go
CountUsers(ctx) (int, error)
BootstrapLocalAdmin(ctx, email, password string) (model.User, error)   // ErrNotEmpty-style guard inside
VerifyLocalCredentials(ctx, email, password string) (model.User, error) // ErrInvalidCredentials
CreateMemberUser(ctx, orgID, name, email, role, tempPassword string) (model.User, error)
SetPassword(ctx, userID, password string, mustChange bool) error
GetCredentials(ctx, userID) (model.Credentials, error)                 // email + must_change (never hash)
ListMembers(ctx, orgID) ([]model.Member, error)
UpdateMemberRole(ctx, orgID, userID, role string) error                 // last-owner guard
RemoveMember(ctx, orgID, userID) error                                  // deletes user; last-owner guard
RoleOf(ctx, userID) (string, error)
```

`model.Member` = User + Role + Email(nullable) + Provider.
`model.ErrInvalidCredentials`, `model.ErrLastOwner`, `model.ErrEmailTaken`.
Password hashing lives in the repo layer (single place; handlers never see
hashes).

## Web UI

- **Login page:** email+password form is primary and always shown, posting
  to the `local` provider (same direct-provider URL shape env used). Env
  branch removed. Bootstrap hint under the form when `auth/state.bootstrap`.
  Google button stays list-driven.
- **Forced password change:** after auth, the app fetches the profile; if
  `must_change_password`, all authed routes redirect to `/user/password`
  until cleared (guard in the authed layout).
- **`/user/password`:** current + new + confirm form → `change-password`.
- **`/org/members`** (avatar dropdown, "Members", rendered for
  owner/admin): members table (name, email, role, provider, last seen),
  "Add user" dialog (name, email, role select) → temp-password one-time
  reveal (same pattern as PATs), per-row actions: change role, reset
  password (reveal), remove (confirm).
- Profile data via a `use-profile` hook (or folded into user-context);
  role also drives the dropdown item visibility.

## Out of scope

Invite links, SMTP/email, linking Google+local identities, per-server
ACLs, viewer role, org switching, password strength meter, rate limiting,
account lockout, session revocation on role change.

## Testing

- Repo: bootstrap creates admin once (second call fails; concurrent-ish
  double call → one winner), verify wrong/right password, email
  normalization, member CRUD, last-owner guards, remove cascades (PATs die
  with the user), RoleOf.
- Provider checker: bootstrap on empty DB, normal login, bad password.
- Middleware: member → 403, admin/owner → pass.
- Handlers: create-user (duplicate email 400, temp password in response,
  role validation), change-password (wrong current 400, Google-only 400,
  clears must_change), profile shape, auth/state flips after bootstrap.
- E2E smoke on a scratch standalone: fresh DB → `auth/state.bootstrap` true
  → first local login creates admin → auto-claims the standalone server →
  create member (temp password) → member logs in → change-password → member
  sees servers but gets 403 on `org/create-user` and `registry/create` →
  admin resets member password → old password rejected.
- Web: `tsc -b` + lint.
