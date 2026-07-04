# Organization management + registration — Design

**Date:** 2026-07-03
**Status:** Approved

## Goal

Self-signup via an explicit registration form (replacing the first-login
bootstrap magic), an env toggle to close it, and organization identity
(name, icon, color) editable by admins. Standalone and distributed share one
model; standalone simply pins the org count (and server count) to 1.

## Decisions (user-approved)

1. **One model, two limits.** Distributed: every registration creates the
   user + their own organization (owner). Standalone: the org is a
   singleton — registration works only as the one-time **claim step** while
   zero users exist; afterwards accounts are provisioned by admins at
   `/org/members` (existing temp-password flow).
2. **`REGISTRATION_ENABLED`** (default **true**, documented in `.env.dist`):
   closes self-signup when `false`. The standalone claim step ignores the
   toggle — a fresh instance must never be unclaimable.
3. **Register → login.** Registration returns 200 with no session; the UI
   redirects to the login form with the email prefilled.
4. **Org identity:** name, icon, color (same icon set + color input as
   apps), shown and editable (admin-only) on the members page in both
   topologies.

## Backend

### Registration

- `POST /api/v1/auth/register` (public) `{name, email, password}`:
  - Validate: name 1–64 chars, valid email ≤255, password ≥8.
  - Allowed iff: standalone → `CountUsers()==0`; distributed →
    `REGISTRATION_ENABLED` (or `CountUsers()==0`, same never-brick rule).
    Closed → 403-style error (400 envelope is fine, message "registration
    is disabled").
  - Standalone path = `BootstrapLocalAdmin` (kept; zero-user check inside
    stays as the race guard). Distributed path = new repo method
    `RegisterLocalUser(name, email, password)`: user + own org (`<name>'s
    org`, owner) + credentials + `local` connected account;
    `model.ErrEmailTaken` on duplicates.
  - Response: `{user_id, email}` — no session, no JWT.
- `localCredChecker` loses its bootstrap branch — login is verify-only.
  `auth/local_test.go` updated accordingly.
- `GET /api/v1/auth/state` → `{bootstrap, registration_enabled}` where
  `registration_enabled` reports whether a registration would currently be
  accepted (claim-step rule included).

### Closing the Google side door

When registration is closed, Google first-login must not create accounts:
`ClaimsUpd` calls `FindOrCreateUser` only when registration is currently
allowed; otherwise `GetByConnectedAccount` (find-only) and, on miss, leaves
the claims without a `user_id` attribute. To make that a real lockout,
`util.GetUserID` **drops its fallback to `User.ID`** — every legitimate
path (ClaimsUpd, PAT bearer, BasicAuthChecker) sets the `user_id`
attribute, so unresolved identities 401 uniformly.

### Config

`pkg/config`: `IsRegistrationEnabled() bool` — `REGISTRATION_ENABLED`
unset/`true`/`1` → true; `false`/`0` → false. Tests for the parsing.

### Organization identity

- Migration: `ALTER TABLE organizations ADD COLUMN icon VARCHAR(64) NOT
  NULL DEFAULT ''` and `color VARCHAR(7) NOT NULL DEFAULT ''` (two
  statements, both dialects; SQLite supports ADD COLUMN).
- Models: `models.Organization` + new `model.Organization{ID, Name, Icon,
  Color}`.
- Repo: `GetOrganization(ctx, orgID)`, `UpdateOrganization(ctx, orgID,
  name, icon, color string)` (name required ≤64; icon/color stored as
  given, ≤64/≤7).
- API (admin-gated like the rest of `/org`):
  - `GET /api/v1/org/get-organization` → `{org_id, name, icon, color}`
    (caller's primary org). Served to ALL members (not admin-gated) so the
    UI can display it; only the update is admin-only.
  - `POST /api/v1/org/update-organization` `{name, icon, color}`.

## Web

- **`/register` page:** name, email, password, confirm; fetches
  `auth/state` — when registration closed, shows "Registration is
  disabled" instead of the form. Success → toast → `/login` with email
  prefilled (router state). Login page shows a "Create an account" link
  when `auth/state.registration_enabled`. The old bootstrap hint on the
  login form is replaced by this link (fresh instance ⇒ state reports
  enabled ⇒ link shows).
- **Members page:** "Organization" card above the table — icon (AppIcon
  rendering) + name + color swatch; admins get an Edit dialog: name input,
  icon picker (reuse `icon-picker.tsx`), color input (reuse the pattern
  from the app editor). Non-admins see it read-only. Card data from
  `get-organization`; note members page itself stays admin-only this
  round, so read-only display is future-proofing the endpoint, not a new
  page.

## Out of scope

Multiple orgs per user, org switching, deleting orgs, transferring
ownership, email verification, CAPTCHA/rate limiting on register.

## Testing

- Config: REGISTRATION_ENABLED parsing.
- Repo: RegisterLocalUser (own org, owner role, dup email), Get/Update
  organization.
- Handlers: register (validation, closed → 400, standalone claim rule,
  duplicate email), auth/state shape, get/update organization (member can
  read, only admin can write — middleware covers the latter).
- auth: checker no longer bootstraps (updated tests); GetUserID no-fallback
  behavior.
- E2E (fresh standalone): state `{bootstrap:true, registration_enabled:true}`
  → register admin → second register refused → login → state flips →
  update org (name+icon+color) → member (admin-created) can read org but
  gets 403 on update; REGISTRATION_ENABLED=false on a fresh DB still
  allows the claim registration.
