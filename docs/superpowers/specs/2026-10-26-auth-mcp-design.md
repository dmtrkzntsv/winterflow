# Auth rewrite, MCP server and secret links — design

Date: 2026-10-26

## Intent

Winterflow should be driven by the user's own coding agent. The user tells
Claude (or Codex) "deploy github.com/x/y on my homelab at y.example.com", and
the agent does the thinking: it reads the repo, writes the compose file, picks
the server, saves the app, reads the logs when it fails, fixes and retries.
Winterflow stays small and literal: it runs exactly what it was given,
checks it, and reports every outcome clearly enough for an agent to act on.
The more decisions move to the agent, the better.

This needs three things Winterflow does not have:

1. An auth stack that can act as an OAuth authorization server, so an MCP
   client connects by URL alone (no token pasting), and that supports Google,
   Apple and generic OIDC sign-in. The user wants to drop `go-pkgz/auth` and
   use the approach already built in econumo
   (https://github.com/econumo/econumo), which covers sessions, API tokens,
   Google/Apple/OIDC, the MCP OAuth server and connected apps.
2. An MCP endpoint exposing the Docker-app workflow as tools.
3. A way for the agent to get a secret from the user without ever seeing it
   (the pattern whiteagent's `secret_request` tool uses,
   https://github.com/whiteagent-org/whiteagent: a one-time link to a form).

Success: the user adds `https://<winterflow>/mcp` to Claude Code, signs in and
approves in the browser, asks Claude to deploy a repo that needs a secret,
gets a link, fills the secret in, and Claude finishes the deploy and reports
the URL and status. Revoking the connection in Winterflow cuts Claude off at
once.

### Context: what comes after

The user's motivating case is apps that cannot run in a container because
they drive coding agents installed on the host (Paperclip, Hermes Agent).
Those will need "host apps" (a git repo run natively as a systemd unit, as
the user's own account or a dedicated one, chosen per app) and a "run a
command on a server" tool. Both are explicitly **later** projects, designed
after this one is proven end to end with Docker apps. Nothing here should
make them harder; nothing here builds them.

## Goals

- Replace `go-pkgz/auth` entirely with a port of econumo's auth model,
  adapted to Winterflow's orgs.
- `/mcp` (Streamable HTTP, stateless) with tools covering the Docker-app
  deploy-and-debug loop.
- Secret links: the agent asks, the user fills a form, the browser encrypts,
  the API never sees plaintext.
- Works in both topologies (standalone and distributed API).

## Non-goals (this project)

- Host apps, running commands on servers (later projects, see above).
- MCP tools for storages, backups, templates/instances, alerts, members,
  tokens, rollouts, hubs. The first tool set is the deploy loop only.
- Secrets passing through MCP in any form, plaintext or ciphertext.
- Shared (org/server) secrets via secret links — app secrets only for now.
- GitHub sign-in (econumo has none; not wanted).
- Roles or per-tool scopes. One scope, `mcp`, = everything the user can do.
- Client ID Metadata Documents, token introspection/revocation endpoints,
  acting as an OpenID provider (same exclusions as econumo).

## Anti-goals

- The API ever seeing a secret's plaintext, including transiently.
- Winterflow guessing on the agent's behalf (detecting frameworks, inventing
  compose files). Tools take explicit input and return explicit outcomes.
- An MCP tool that answers "202, watch SSE". An agent cannot follow SSE;
  every tool returns the outcome or a clear "still running, check with X".
- A second code path with weaker checks: MCP tools call the same usecases and
  ownership guards as REST, so every save-app 400 applies unchanged.

## Delivery

One branch per PR, in order. Each PR is shippable alone.

- **PR 1 — auth port.** Everything under "Auth" below. No MCP yet, but the
  OAuth authorization server ships here (it is part of the auth stack and is
  testable on its own).
- **PR 2 — MCP + secret links.** `/mcp`, the tool set, the secret-request
  flow. The builder may split secret links into a PR 3 if PR 2 grows large.

## Auth (PR 1)

Port econumo's design; read these econumo specs before planning:
`docs/superpowers/specs/2026-07-10-opaque-tokens-sessions-design.md`,
`2026-09-07-oauth-login-design.md`, `2026-10-03-mcp-oauth-design.md`, plus
the code in `internal/oauth`, `internal/authserver`, `internal/infra/auth`
and the `access_tokens` migrations. Where econumo and this document differ,
this document wins.

### Tokens

- One table (econumo's `access_tokens` shape: id, user_id, kind, token_hash,
  scope, name, user_agent, created_at, last_used_at, expires_at, revoked_at,
  plus `grant_id` for OAuth tokens). Kinds: `session`, `pat`, `oauth`.
  Tokens are opaque random strings stored only as SHA-256 hashes. No JWTs;
  `JWT_SECRET` goes away (remove it from `.env.example`, the installers and
  their tests).
- Existing PATs (`user_tokens`, `wfp_…`) migrate into the new table as kind
  `pat` and keep working unchanged. Keep the `wfp_` prefix for PATs; pick
  analogous prefixes for sessions and OAuth tokens (e.g. `wfs_`, `wfo_`).
- **Session transport — differs from econumo (decided here):** the browser
  session is an opaque `session` token in an HttpOnly, Secure (when the
  origin is https), SameSite=Lax cookie, not a localStorage bearer token.
  Reason: the SPA's SSE `EventSource` cannot send an `Authorization` header,
  the SPA is same-origin, and an HttpOnly cookie keeps the token out of JS.
  `Authorization: Bearer` (and the existing Basic-auth-password form for
  PATs) stays accepted for API clients. State-changing requests
  authenticated by cookie must be CSRF-safe: require a JSON content type or
  a custom header on cookie-authenticated non-GET requests (builder's choice
  of mechanism; document it).
- Logout revokes the session row. "Sign out everywhere" (user settings)
  revokes every session; PATs and OAuth grants are managed separately.
- `OAUTH`-kind tokens are admitted on `/mcp` only, never on REST (as
  econumo). Sessions and PATs work on both.
- Everyone signs in once more after the upgrade (old JWT cookies are simply
  not recognised). Acceptable.

### Passwords

- Email + password stays the always-on default.
- Move to argon2id (econumo's `internal/infra/auth`). Existing bcrypt hashes
  verify as before and are rehashed to argon2id on the next successful login.
- Keep today's account rules: the one-time claim on a fresh instance,
  `REGISTRATION_ENABLED` for distributed self-signup, accounts created by
  members at `/org/members`, the per-IP auth rate limiter on every
  bcrypt/argon2-priced endpoint.

### Sign-in providers (Winterflow as OAuth client)

- Google, Apple, generic OIDC — each optional, enabled by its env vars
  (follow econumo's names where sensible; keep `AUTH_GOOGLE_CLIENT_ID` /
  `AUTH_GOOGLE_CLIENT_SECRET` working). Available in standalone too whenever
  the instance has a public https URL (today Google is distributed-only;
  that restriction goes).
- Linked identities per user (econumo's `internal/oauth/identities`): one
  account can sign in by password and by any linked provider. Today's
  `user_connected_accounts` rows migrate into it.
- A provider sign-in for an unknown email follows the same registration
  rules as password registration (refused when registration is closed).

### Authorization server (for MCP clients)

Port econumo's `internal/authserver` as is: RFC 9728 / RFC 8414 discovery,
open dynamic client registration (rate limited), authorization code + PKCE
S256 only, `resource` = `<origin>/mcp`, 1-hour access tokens, rotating
refresh tokens with theft detection, grant expiry 90 days after last refresh,
one scope `mcp`.

- **Issuer / enablement:** the instance's public origin. Winterflow's
  existing `WEB_URL` is that origin; the server is enabled when it is an
  https origin with no path (loopback http allowed for development, as
  econumo), else every OAuth route 404s and `/mcp` accepts PATs and sessions
  only. Document in `.env.example`.
- **Consent:** an SPA route (`/oauth/authorize`) showing the client name,
  redirect host and what it gets (full access to your org's servers and
  apps); approve/decline via API calls, as econumo.
- **Connected apps:** a user-settings page listing OAuth grants (client
  name, created, last used) with revoke. Revoking cuts access immediately.
- Deleting a user, or removing them from the org, revokes their sessions,
  PATs and grants.

### Org scoping

Tokens belong to a user. Every request resolves the user's org as today
(`GetPrimaryOrganizationID`), and every server-addressed operation still
goes through `webutil.RequireServerAccess`. No roles.

### Frontend

Login page (password + enabled provider buttons, from a public config
endpoint listing providers), consent page, connected apps page, linked
identities in user settings, existing tokens page moved onto the new API.
Follow the existing visual style. `web/src/lib/api.ts` keeps same-origin
cookie requests.

### What gets removed

`go-pkgz/auth` and every import of it, the `/auth/*` and `/avatar/*` mounted
routes (replace with the new routes in the typed route tables so they appear
in `api/openapi.yaml`), `JWT_SECRET`, the cookie/XSRF settings that only
existed for go-pkgz. Avatars: keep a user's provider avatar URL if cheap,
otherwise drop avatars (builder's call; the UI must not break).

## MCP (PR 2)

### Endpoint

`/mcp` on the API binary (standalone and distributed), built like econumo's
`internal/web/mcp`: `modelcontextprotocol/go-sdk`, Streamable HTTP,
`Stateless: true`, `JSONResponse: true`, each feature registering its tools
from its own package. Auth: the same token middleware (session, PAT, OAuth
`mcp` token); an unauthenticated call answers 401 with
`WWW-Authenticate: Bearer resource_metadata=…, scope="mcp"` when the
authorization server is enabled. `/mcp` gets a new per-user limiter
(keyed by the authenticated user, not IP: agents share egress IPs), a token
bucket of about 10 requests/s with burst 30, 429 past it (exact numbers are
the builder's). Unauthenticated `/mcp` hits fall under the existing per-IP
auth limiter. The secret-link submit route uses the same per-user limiter. Server instructions describe the
deploy loop in a paragraph (what an app is, compose + `x-winterflow`, that
secrets come via `request_secrets`, that ids come from the list tools).

Connecting: `claude mcp add --transport http winterflow https://<winterflow>/mcp`
(OAuth flow), or with `--header "Authorization: Bearer wfp_…"`. Document both
in a `docs/mcp-setup.md`.

### Waiting for results

Agent-bound commands answer 202 + `request_id` on REST. MCP tools instead
dispatch and **wait** for the result, but never longer than **50 s**: users
run Winterflow behind Cloudflare Tunnel and reverse proxies that drop a
response idle for ~100 s, and `/mcp` is stateless JSON (no keepalive). On
timeout the tool returns a non-error result `{status: "running",
request_id, hint: "call get_command_result"}`. `get_command_result
(request_id)` itself waits up to 50 s and returns the outcome, `running`, or
`unknown` (expired, or not this org's). A failed command is a tool result
with `isError: true` and the agent's error text, never a transport error.

**Results are persisted** so any API replica can answer: a
`command_results` table (request_id, org_id, server_id, kind, status
running/succeeded/failed/unknown, result JSON, error, created_at,
finished_at), written when an MCP tool dispatches and when the result
arrives on the replica that dispatched; pruned 24 h after it ends, on an
existing maintenance ticker. A replica without the local waiter polls the
row (1 s interval, inside the 50 s bound). If the dispatching replica dies
before the result arrives, the row is marked `unknown` after 15 minutes.
Only MCP-dispatched commands are recorded; REST keeps its SSE path. Cap
stored result JSON at 1 MiB (truncate log tails to fit, and say so).

After a successful save or control, the tool also returns the app's current
status from the status cache, so the agent sees whether containers came up
without a second call (it may need a later report to settle; the tool says
so, and `get_app_status` exists for polling).

### Tools (first set)

Thin wrappers over existing usecases; the same input validation and errors.
Names snake_case. Ids come from list tools.

- `list_servers` — servers with online state and basic info.
- `list_apps` (server) — apps with status.
- `get_app` (server, app) — files, variables (secret values masked as today),
  declared variables, expose, ingress, revision.
- `save_app` (server, app id or new, name, files or git source, variables,
  ingress, draft flag) — the same payload save-app takes, minus secret
  values: a secret variable may only be sent as the `<encrypted>` keep
  placeholder, omitted, or filled from a secret request (`secrets_from`).
  Returns the deploy outcome.
- `control_app` (start/stop/restart/update), `rename_app`, `delete_app`
  (volumes/backups flags), `rollback_app`.
- `get_app_status`, `get_app_logs` (tail, service), `get_app_revisions`.
- `list_domains`, `create_domain`, `delete_domain` (server domains, TLS
  modes as REST; certificate mode is out for MCP since it carries a key).
- `list_networks`, `list_volumes` (read only).
- `get_image_tags`.
- `list_shared_variables` (org and server, names and plain values; secrets
  by name only).
- `request_secrets`, `get_secret_request` (below); `save_app` takes
  `secrets_from`.
- `get_command_result` (above).

An app managed by a template instance answers its usual 409 on save; the
tool's error says it is managed by an instance and must be changed in the
UI for now.

## Secret links (PR 2, or PR 3)

The agent stays in control of saving and deploying; the link only collects
ciphertexts. This keeps drafts, stopped apps and "whose draft is it" out of
the picture.

### Flow

1. The agent, having read the repo, knows which secret variables the app
   needs. It calls `request_secrets` (server, app id or none for a new app,
   names, optional per-name note of at most 500 chars, optional message).
   Returns `{request_id, url, expires_at}`; the agent gives the URL to the
   user. (Separately, `save_app` with a required secret that has no value
   fails with the agent's normal error naming the variables; the tool's
   error text points the agent at `request_secrets`.)
2. The user opens `https://<winterflow>/secrets/<token>`. It requires sign-in
   (after login, return to the link) as a member of the request's org; any
   other user gets "not found". The page shows the server (and app, when
   given), the agent's message and notes (plain text), and one field per
   name.
3. On submit the browser fetches the server's public key, ECIES-encrypts
   each value exactly as the app editor does, and posts the ciphertexts with
   the token. The API checks the token (pending, unexpired, exactly the
   requested names, ciphertext shape) and stores the **ciphertexts** on the
   request (the API already stores ECIES ciphertexts for shared secrets; it
   never sees plaintext). The page says the agent can continue.
4. The agent polls `get_secret_request` (`pending | submitted | expired |
   used`), then calls `save_app` with `secrets_from: <request_id>`. The API
   puts the stored ciphertexts in as those variables' values (other secrets
   go as the `<encrypted>` keep placeholder or are omitted), and the save
   runs through the normal checks. A save refused before dispatch leaves the
   request `submitted` so the agent can retry; once dispatched it is `used`
   and its ciphertexts are deleted.

### Rules

- Token: 32 random bytes, base64url, stored hashed; one submission. A
  request expires 24 h after creation if not submitted; ciphertexts are
  deleted 24 h after submission if never used. Table `secret_requests` (id,
  org_id, server_id, app_id nullable, created_by, names, notes, message,
  token_hash, status, ciphertexts JSON, created_at, expires_at,
  submitted_at, used_at); rows pruned a week after they end.
- `secrets_from` is accepted only for a request of the caller's org and the
  same server, status `submitted`, and (when the request named an app) the
  same app id; its names must be secret variables the saved files declare.
- Ciphertexts are encrypted to the server's key at submit time; if that key
  changed before use, the save fails at the agent and the agent asks again.
- The token is never accepted as an API credential: it is a capability only
  together with an org member's session. Viewing a bad or used token
  reveals nothing about it.
- Instance-managed apps: `save_app` already 409s; out of scope.

## Constraints (from the repo; restating the ones that bite)

- Every network operation has a deadline; the standalone in-process bridge
  is single-goroutine, so MCP waits must not hold it (they wait on the
  observer, not on the bridge).
- `make openapi` after route changes; contract tests for every new route in
  `internal/app/web/contract`, including 403 for another org's server.
- Migrations for both engines (`make sqlc`, `shapes_test.go`).
- `.env.example` updated for every added/removed env var; installers and
  their tests updated when `JWT_SECRET` goes.
- Small-box resource rules: no background work when idle beyond pruning on
  existing tickers.

## Left to the builder

- Exact table/column names, token prefixes, package layout (follow econumo's
  feature-package layout where it fits Winterflow's hexagonal layering:
  domain usecases in `internal/domain/usecase`, adapters in `internal/infra`,
  HTTP in `internal/app/web`).
- The CSRF mechanism for cookie-authenticated writes.
- How provider config is surfaced to the login page.
- Tool input schemas and descriptions; the server instructions text.
- Whether secret links ship in PR 2 or a PR 3.

## Calls made in this design (not discussed with the user)

- Cookie-held opaque session instead of econumo's localStorage bearer (SSE).
- argon2id with rehash-on-login.
- Secret links collect ciphertexts only; the agent then saves with
  `secrets_from` (rather than the form redeploying the app itself).
- Secret links require a signed-in org member; 24 h, single submission.
- MCP waits capped at 50 s (proxy idle limits) with `get_command_result`;
  results persisted in `command_results` so multi-replica APIs work.
- Per-user (not per-IP) rate limit on `/mcp` and secret submission.
- First tool set as listed; certificate-mode domains excluded from MCP.
- `WEB_URL` as the OAuth issuer.
