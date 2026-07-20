# Personal Access Tokens (PATs) — Design

**Date:** 2026-07-03
**Status:** Approved

## Goal

Let a user create, list, and revoke personal access tokens that authenticate
API requests the same way a browser session does — for scripts, CI, and CLI
use against a WinterFlow instance.

## What already exists

- `user_tokens` table (unused — 0 rows anywhere, feature never shipped).
- `port.UserService.FindByToken(ctx, token)` + repo implementation with
  expiry enforcement.
- go-pkgz `BasicAuthChecker` in `internal/app/web/bootstrap.go`: a request
  with `Authorization: Basic base64(anything:PAT)` already resolves the PAT
  to a user. Verification is wired; issuance, revocation, and UI are not.

## Decisions (user-approved)

1. **Hashed storage, shown once.** DB stores SHA-256 only; plaintext is
   displayed a single time at creation (GitHub-style).
2. **Bearer + Basic.** Add `Authorization: Bearer wfp_…` support; Basic
   keeps working unchanged.
3. **UI at `/user/tokens`**, reached from the avatar dropdown in the sidebar.
4. **Fields: name + expiry + last-used.** No scopes/permissions — every
   token grants the full access of its user.

## Token format

- Plaintext: `wfp_` + 40 base62 chars from `crypto/rand` (~230 bits).
- `token_prefix`: first 12 chars (e.g. `wfp_9k3Ldx2p`) — stored, shown in
  the token list for identification.
- `token_hash`: lowercase hex SHA-256 of the full plaintext. High-entropy
  input ⇒ no salt/KDF needed; lookup is a straight unique-index hit.

Generation and hashing live in `pkg/pat` (pure functions, no deps):
`Generate() (plaintext, hash, prefix string, err error)` and
`Hash(token string) string`.

## Schema

The migration **drops and recreates** `user_tokens` (safe: table is empty and
the feature is unreleased; avoids fighting Postgres `char(32)` widening):

```sql
CREATE TABLE user_tokens (
    token_id     char(36) PRIMARY KEY,
    user_id      char(36) NOT NULL REFERENCES users(user_id) ON DELETE CASCADE,
    name         VARCHAR(64)  NOT NULL,
    token_prefix VARCHAR(16)  NOT NULL,
    token_hash   VARCHAR(64)  NOT NULL UNIQUE,
    token_type   VARCHAR(16)  NOT NULL CHECK (token_type IN ('pat')),
    expires_at   timestamp NULL,
    last_used_at timestamp NULL,
    created_at   timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_user_tokens_user_id ON user_tokens (user_id);
```

Bun model `models.UserToken` is updated to match.

## Verification paths

Both paths end in the same repo call.

1. **Bearer (new):** `PATAuth` middleware wraps the existing go-pkgz `Auth`
   middleware in `routes.go`. If the Authorization header is
   `Bearer wfp_…`: hash → `FindByToken` → on success inject a go-pkgz
   `token.User` (with the `user_id` attribute `util.GetUserID` reads) into
   the request context and call the next handler directly, skipping the JWT
   parser. On failure: 401. Any other header shape falls through to the
   unmodified JWT middleware — browser sessions are untouched.
2. **Basic (existing):** `BasicAuthChecker` continues to call
   `FindByToken`; it now matches by hash transparently.

`FindByToken(ctx, plaintext)`:
- hashes the input, looks up by `token_hash`;
- rejects expired tokens (`model.ErrInvalidToken`, as today);
- stamps `last_used_at`, throttled: skip the write if the current value is
  less than 1 minute old (protects SQLite from a write per request).

## API

Synchronous 200s (pure DB, no agent round-trip), verb-style routes matching
the codebase, all behind auth:

| Route | Body | Returns |
|---|---|---|
| `POST /api/v1/user/create-token` | `{name, expires_in_days?}` | `{token_id, token, prefix, name, expires_at}` — `token` is the plaintext, returned **only here** |
| `GET /api/v1/user/get-tokens` | — | `[{token_id, name, prefix, created_at, expires_at, last_used_at}]` |
| `POST /api/v1/user/delete-token` | `{token_id}` | `{}` |

Rules:
- `name` required, ≤64 chars. `expires_in_days` optional; absent/0 = never.
- List and delete are scoped to the calling user (`user_id` from auth
  context); deleting another user's token id is a no-op returning
  not-found.
- Creating a token requires an authenticated session; PAT-authenticated
  requests may also mint tokens (single-user platform, no privilege tiers).

Layering follows the house pattern: repo methods (`CreateToken`,
`ListTokens`, `DeleteToken`, reworked `FindByToken`) → `usecase/token` →
`handler/user` → routes.

## UI

- New page `web/src/pages/user-tokens.tsx`, route `/user/tokens`,
  breadcrumbs `User / API tokens`, linked from the avatar dropdown
  (`nav-user.tsx`).
- One card:
  - Table: name, prefix (`wfp_9k3L…`), created, expires, last used,
    revoke button with confirm dialog.
  - "Generate token" dialog: name input + expiry select (30 / 90 / 365
    days / never). On success the dialog switches to a one-time display of
    the full token with a copy button and a "you won't see this again"
    warning.
- Data via a small `use-tokens` hook with plain `fetch` — synchronous
  endpoints, no SSE, stays out of apps-context.

## Testing (TDD)

- `pkg/pat`: format, prefix, uniqueness, hash stability.
- Repo: create/list/delete, hash lookup, expiry rejection, cross-user
  scoping, last-used stamping + throttle.
- Middleware: valid Bearer, invalid, expired, non-`wfp_` fall-through to
  JWT.
- Handlers: three endpoints incl. validation errors and cross-user revoke.
- Web: `tsc -b` + lint; headless-browser smoke (create token in UI → curl
  an API endpoint with it).

## Out of scope

Scopes/permissions, token rotation, org-level tokens, PAT-specific rate
limiting.
