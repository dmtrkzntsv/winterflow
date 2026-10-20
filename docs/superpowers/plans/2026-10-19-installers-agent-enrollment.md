# Installers and Agent Enrollment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One script installs Winterflow as standalone, as a distributed control plane (API + Hub + local Postgres/Redis), or as an agent that enrolls with a code typed into the hosted UI; hubs admit only agents whose server still exists.

**Architecture:** The API gains an unauthenticated enrollment API (CSR in, code out, signed cert back after a user claims the code) and signs agent certs with the CA it now holds. Hubs verify each connecting agent's certificate name and ask the API over the existing Redis bus whether the server and certificate are still active; deleting a server revokes it. Enrollment logic on the agent side is a hidden `winterflow-agent enroll` subcommand that `install.sh --agent` calls.

**Tech Stack:** Go 1.27 (chi, sqlc over SQLite/Postgres, grpc, go-redis), bash + systemd, React 19 (web).

**Spec:** `docs/superpowers/specs/2026-10-19-installers-agent-enrollment-design.md`

## Global Constraints

- Default API URL compiled into script and agent: `https://api.winterflow.dev`. Hubs named like `hub-01.winterflow.dev`.
- Code: 6 characters, `util.GenerateRandomCode(6)` alphabet (same as today), valid 10 minutes; at most 10 codes per enrollment (server-enforced too).
- Claimed result retrievable by `poll_secret` for 15 minutes after the claim.
- Agent cert CN = `server_id`; the CSR's own subject is ignored. Agent private key never leaves the agent box (0600, service user).
- Hub waits at most 5 s for the API's auth reply; no reply = `Unavailable`.
- Agent: 5 consecutive `PermissionDenied` (retry gaps 1m, 2m, 3m, 4m) → exit code 78; unit has `RestartPreventExitStatus=78`. `Unavailable` and every other failure: existing 1–30 s backoff forever. A successful connection resets the count.
- Bus channels: `agent-auth:<REGION>`, `agent-auth-reply:<hub_id>`, `agent-revoke:<REGION>`.
- Standalone defaults to SQLite (installer always uses it) but accepts Postgres; distributed API is Postgres-only (startup refuses SQLite). None of the hub auth applies to standalone.
- Every new/changed env var goes into `.env.example` in the same task (CLAUDE.md rule). Every route change: `make openapi`, contract test + `probes` entry. New SQL: same `-- name:` in `query/sqlite` and `query/pgsql`, `make sqlc`, shapes test. Migrations identical in both engines, ASCII only.
- Every network operation has a deadline; pending-reply maps are pruned on timeout (CLAUDE.md stability rules).
- `install.sh` stays one curl-pipeable file; installers never touch the firewall, Caddy, or global Postgres/Redis config; reruns keep CA, passwords, JWT secret, data.

## Review Focus

1. **Claim races a refresh**: a user types code N while the script has just refreshed to N+1. Expect "code has expired" for N (not a 500, not a claim), and N+1 works. → Task 4 test `TestClaimOldCodeAfterRefresh`.
2. **Script loses the network after the claim** and polls again minutes later. Expect `claimed` with the same cert, not a second server. Past 15 minutes: `expired`, and the server exists without a collected cert. → Task 4 test `TestStatusClaimedIsIdempotent`.
3. **Two API replicas answer the same auth question**, or a reply arrives after the 5 s timeout. Expect the first reply used, late/duplicate replies dropped without leaking map entries or blocking. → Task 8 test `TestAuthorizerLateAndDuplicateReplies`.
4. **Agent rerun on an already-enrolled box** (`install.sh --agent` again). Expect binary/unit refreshed, no new enrollment, cert untouched, service restarted. → Task 13 dry-run test `agent_rerun_keeps_enrollment`.
5. **Agent stream sends a heartbeat/event with a different `AgentId`** than its cert. Expect the stream closed, nothing published for the foreign id. → Task 8 test `TestStreamRejectsForeignAgentID`.

---

### Task 1: Topology database rules and new config

Absorbs PR #33's Go config change, relaxed: standalone may still use Postgres.

**Files:**
- Modify: `pkg/config/config.go` (`GetDbURL` ~:46; add getters next to `GetHubPort` ~:207)
- Modify: `internal/infra/bootstrap/api.go`, `internal/infra/bootstrap/standalone.go` (call validation at start)
- Modify: `.env.example`
- Test: `pkg/config/config_test.go`

**Interfaces:**
- Produces:
  - `func (c *ServerConfig) GetDbURL() string` — standalone default `sqlite://winterflow.sqlite` when unset.
  - `func (c *ServerConfig) ValidateDatabaseURL() error` — standalone accepts `sqlite://` or `postgres://`/`postgresql://`, distributed requires `postgres://`/`postgresql://`; any other scheme is an error for both.
  - `func (c *ServerConfig) GetAgentHubAddr() string` — env `AGENT_HUB_ADDR`, `host:port`.
  - `func (c *ServerConfig) ValidateEnrollment() error` — distributed API only: `AGENT_HUB_ADDR` is a valid `host:port`, and `GetHubCACertPath()` / `GetHubCAKeyPath()` files exist and parse as a matching pair.
  - `func (c *ServerConfig) GetAgentAuthChannel() string` = `"agent-auth:" + REGION`
  - `func (c *ServerConfig) GetAgentRevokeChannel() string` = `"agent-revoke:" + REGION`
  - `func AgentAuthReplyChannel(hubID string) string` = `"agent-auth-reply:" + hubID`

- [ ] **Step 1: Write failing tests** `TestGetDbURLStandaloneDefault` (unset → `sqlite://winterflow.sqlite`), `TestValidateDatabaseURL` (table: standalone+sqlite ok, standalone+postgres ok, standalone+mysql err, distributed+postgres ok, distributed+postgresql ok, distributed+sqlite err, distributed+empty err), `TestValidateEnrollment` (missing addr err, `hub-01.winterflow.dev:50051` ok, `nohost` err, missing CA key err, valid pair from a `t.TempDir()` CA ok), `TestAgentChannels` (`REGION=eu` → `agent-auth:eu`, `agent-revoke:eu`; `AgentAuthReplyChannel("h1")` → `agent-auth-reply:h1`).
- [ ] **Step 2: Run** `go test ./pkg/config/ -run 'DbURL|DatabaseURL|Enrollment|AgentChannels'` — FAIL (undefined).
- [ ] **Step 3: Implement** the getters; `BootstrapStandalone` fatals on `ValidateDatabaseURL`, `BootstrapAPI` fatals on `ValidateDatabaseURL` and `ValidateEnrollment`. `.env.example`: document `AGENT_HUB_ADDR` (distributed API; address handed to enrolling agents), note that the API reads the CA pair from `HUB_CERT_DIR`, and the standalone `DATABASE_URL` default.
- [ ] **Step 4: Run** `go test ./pkg/config/ ./internal/infra/bootstrap/...` — PASS. Run `go test ./internal/app/web/contract/` — PASS (contract setup may need `AGENT_HUB_ADDR` + a temp CA; add to its `TestMain` if bootstrap is used there).
- [ ] **Step 5: Commit** `feat(config): SQLite by default for standalone, Postgres-only API, enrollment settings`

### Task 2: Certificate helpers

**Files:**
- Modify: `pkg/cert/cert.go`
- Create: `pkg/cert/hubpki.go`
- Test: `pkg/cert/cert_test.go`, `pkg/cert/hubpki_test.go`

**Interfaces:**
- Produces:
  - `func SignCSRAs(csrPEM, commonName string, caCert *x509.Certificate, caKey crypto.Signer, validityYears int) (certPEM string, expiresAt time.Time, err error)` — like `SignCSR` but subject is exactly `CN=commonName`, ExtKeyUsage ClientAuth only, rejects a CSR whose signature doesn't verify or whose key isn't ECDSA P-256.
  - `func Fingerprint(certPEM string) (string, error)` — lowercase hex SHA-256 of the DER.
  - `func LoadCA(certPath, keyPath string) (*x509.Certificate, crypto.Signer, error)`
  - `func EnsureHubPKI(dir string, hubHosts []string) (changed bool, err error)` — creates `ca.crt`/`ca.key` (0600) if absent (never replaces them), and (re)issues `hub.crt`/`hub.key` signed by the CA when absent or when its DNS/IP SANs differ from `hubHosts`. File names match `config` defaults (`GetHubCACertFilename` etc.).

- [ ] **Step 1: Write failing tests** `TestSignCSRAsOverridesSubject` (CSR with CN `evil` → cert CN equals given `server_id`, verifies against CA, ExtKeyUsage == [ClientAuth]), `TestSignCSRAsRejectsBadCSR` (garbage, tampered signature, RSA key → error), `TestFingerprintStable` (same PEM twice equal, 64 hex chars), `TestEnsureHubPKI` (first call creates all four files, `changed=true`; second call same hosts → `changed=false` and files byte-identical; call with an added host → hub cert reissued with both SANs, `ca.crt` byte-identical; `ca.key` mode 0600).
- [ ] **Step 2: Run** `go test ./pkg/cert/` — FAIL.
- [ ] **Step 3: Implement**, reusing the generator's existing CA/server-cert helpers where they fit.
- [ ] **Step 4: Run** `go test ./pkg/cert/` — PASS.
- [ ] **Step 5: Commit** `feat(cert): sign a CSR as a given server, fingerprints, hub PKI bootstrap`

### Task 3: Enrollment storage

**Files:**
- Create: `internal/infra/db/migrations/{sqlite,pgsql}/0018_agent_enrollments.sql`
- Create: `internal/infra/db/sqlc/query/{sqlite,pgsql}/enrollments.sql`
- Create: `internal/infra/db/repository/enrollment/{repo.go,sqlite.go,pgsql.go,repo_test.go}`
- Modify: `internal/infra/db/sqlc/shapes_test.go`, `internal/domain/port/` (new `enrollment.go`), `internal/domain/model/` (new `enrollment.go`)
- Generated: `internal/infra/db/sqlc/gen/**` via `make sqlc`

Table `agent_enrollments`: `enrollment_id` PK, `poll_secret_hash` TEXT UNIQUE NOT NULL, `csr` TEXT NOT NULL, `hostname` TEXT NOT NULL, `code` VARCHAR(6) UNIQUE NULL, `code_expires_at` timestamp NULL, `codes_issued` INT NOT NULL, `state` TEXT NOT NULL (`pending|claimed|failed`), `server_id` NULL, `certificate` TEXT NULL, `claimed_at` NULL, `created_at` NOT NULL. Index on `created_at`. `server_registrations` (standalone's path) is untouched.

**Interfaces:**
- Produces (`internal/domain/model`): `type Enrollment struct { ID, PollSecretHash, CSR, Hostname, Code string; CodeExpiresAt time.Time; CodesIssued int; State EnrollmentState; ServerID, Certificate string; ClaimedAt, CreatedAt time.Time }`, `EnrollmentState` consts `EnrollmentPending|EnrollmentClaimed|EnrollmentFailed`.
- Produces (`port.EnrollmentRepository`):
  - `Create(ctx, model.Enrollment) error`
  - `GetBySecretHash(ctx, hash string) (model.Enrollment, error)` — `model.ErrNotFound` when absent
  - `GetByCode(ctx, code string) (model.Enrollment, error)`
  - `ReplaceCode(ctx, id, code string, expiresAt time.Time, codesIssued int) error` — conditional on `state='pending'`
  - `MarkFailed(ctx, id string) error`
  - `Claim(ctx, ClaimEnrollment) (model.Server, error)` — one transaction: `UPDATE ... SET state='claimed', code=NULL ... WHERE enrollment_id=? AND code=? AND state='pending' AND code_expires_at > now`, 0 rows → `model.ErrRegistrationCodeExpired`; insert `servers` (name = hostname truncated to 64) and an active `server_certificates` row. `type ClaimEnrollment struct { EnrollmentID, Code, ServerID, OrganizationID, CertificateID, Certificate string; CertExpiresAt, Now time.Time }`.
  - `CountPending(ctx) (int, error)`
  - `Prune(ctx, now time.Time) error` — deletes pending/failed rows older than 2 h and claimed rows with `claimed_at` older than 15 min.

- [ ] **Step 1: Write failing repo tests** (`dbtest.Open(t)`): `TestEnrollmentCreateGet`, `TestReplaceCodeOnlyPending`, `TestClaimCreatesServerAndCert` (server in org, certificate active, enrollment claimed, code cleared), `TestClaimExpiredCode` (→ `ErrRegistrationCodeExpired`, no server row), `TestClaimTwiceSecondFails`, `TestPrune` (table of ages/states → which survive), `TestCountPending`.
- [ ] **Step 2: Run** `go test ./internal/infra/db/repository/enrollment/` — FAIL.
- [ ] **Step 3: Write migration + queries, `make sqlc`, implement repo** following the existing `server` repository's `repo.go`/`sqlite.go`/`pgsql.go` split; add type pairs to `shapes_test.go`.
- [ ] **Step 4: Run** `go test ./internal/infra/db/...` and `make test-pg` — PASS; `make sqlc-check` clean.
- [ ] **Step 5: Commit** `feat(db): agent_enrollments table and repository`

### Task 4: Enrollment usecase

**Files:**
- Create: `internal/domain/usecase/enrollment/{usecase.go,usecase_test.go}`

**Interfaces:**
- Consumes: Task 2 `SignCSRAs`, `Fingerprint`; Task 3 `port.EnrollmentRepository`.
- Produces:
  ```go
  const (CodeTTL = 10 * time.Minute; MaxCodes = 10; ClaimedTTL = 15 * time.Minute; MaxPending = 1000)
  var ErrTooManyPending, ErrInvalidCSR, ErrNoCodesLeft = errors.New(...)
  type Signer interface { Sign(csrPEM, commonName string) (certPEM string, expiresAt time.Time, err error); CACertPEM() string }
  type StartResult struct { EnrollmentID, PollSecret, Code string; ExpiresAt time.Time; CodesLeft int }
  type Status struct { State string /* pending|expired|failed|claimed */; Code string; ExpiresAt time.Time; CodesLeft int; ServerID, Certificate, CACertificate, HubAddr string }
  func New(repo port.EnrollmentRepository, signer Signer, hubAddr string, now func() time.Time) *UseCase
  func (u *UseCase) Start(ctx, csrPEM, hostname string) (StartResult, error)
  func (u *UseCase) Refresh(ctx, pollSecret string) (StartResult, error)
  func (u *UseCase) Status(ctx, pollSecret string) (Status, error)
  func (u *UseCase) Claim(ctx, code, orgID string) (model.Server, error)
  ```
  `PollSecret`: 32 random bytes, base64url; only `sha256` hex stored. `Start` validates the CSR (parse + signature + P-256) before storing, prunes, and refuses with `ErrTooManyPending` at `MaxPending`. `Refresh` after 10 codes marks failed and returns `ErrNoCodesLeft`. `Status` on a pending enrollment whose code lapsed answers `expired` (or `failed` when `codes_issued == MaxCodes`). `Claim`: look up by code (`model.ErrInvalidRegistrationCode` when absent), `ErrRegistrationCodeExpired` when lapsed, generate `server_id`, sign with CN = `server_id`, then `repo.Claim`. Code collision on insert: retry generation up to 5 times.

- [ ] **Step 1: Write failing tests** with an in-memory fake repo and a test CA: `TestStartReturnsCodeAndSecret` (6-char code, `ExpiresAt == now+10m`, `CodesLeft == 9`), `TestStartRejectsBadCSR`, `TestStartTooManyPending`, `TestRefreshIssuesNewCode` (new code differs, old code no longer found), `TestRefreshCapAt10` (10th refresh path → `ErrNoCodesLeft`, status `failed`), `TestStatusExpiredWhenCodeLapsed` (clock +10m1s), `TestClaimSignsAsServerID` (cert CN == returned server's ID; status → `claimed` with `HubAddr` and CA PEM), `TestClaimOldCodeAfterRefresh` (→ invalid-code error, no server), `TestStatusClaimedIsIdempotent` (two `Status` calls return identical cert; after +15m1s and a prune → `ErrNotFound`).
- [ ] **Step 2: Run** `go test ./internal/domain/usecase/enrollment/` — FAIL.
- [ ] **Step 3: Implement.** Production `Signer` wraps `cert.LoadCA` + `SignCSRAs` (validity: the existing agent validity constant) in `internal/infra/cert` as `func NewCASigner(certPath, keyPath string) (*CASigner, error)`.
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `feat(enrollment): code-based agent enrollment usecase`

### Task 5: Enrollment HTTP API and claim integration

**Files:**
- Create: `internal/app/web/handler/enroll/{handler.go,routes.go,handler_test.go}`
- Modify: `internal/app/web/handler/server/register.go`, `handler.go` (new dependency), `internal/app/web/routes.go`, `internal/infra/bootstrap/{core.go,api.go}` (wire `enrollment.UseCase` in the API only; standalone passes nil)
- Modify: `internal/app/web/contract/*` (happy paths + probes), `api/openapi.yaml` (via `make openapi`)

**Interfaces:**
- Consumes: Task 4 `*enrollment.UseCase`.
- Produces routes (all `Auth: api.Public`, `Middleware: []api.Middleware{g.AuthLimit}`, tag `enroll`):
  - `POST /api/v1/agent/enroll` `{csr, hostname}` → `{enrollment_id, poll_secret, code, expires_at, codes_left}`
  - `POST /api/v1/agent/enroll/refresh` `{poll_secret}` → same shape
  - `POST /api/v1/agent/enroll/status` `{poll_secret}` → `{state, code, expires_at, codes_left, server_id, certificate, ca_certificate, hub_addr}`
  - Errors: 400 bad CSR / unknown secret, 409 `no codes left`, 503 `too many pending enrollments`. All return 404 in standalone (routes not mounted when the usecase is nil).
- Claim: `server.Handler.Register` tries `enrollment.Claim` first when wired; on `ErrInvalidRegistrationCode` it falls back to the existing `ClaimServer` (standalone's path). Per-user claim limit: a second `ratelimit.New(0.1, 5)` keyed by user id via `Limiter.Allow(userID, now)`; refused → 429 `too many attempts, wait a minute`.

- [ ] **Step 1: Write failing handler tests**: `TestEnrollStartOK`, `TestEnrollBadCSR400`, `TestEnrollStatusUnknownSecret400`, `TestRefreshNoCodesLeft409`, `TestRegisterClaimsEnrollment` (status afterwards `claimed`), `TestRegisterFallsBackToRegistration`, `TestRegisterRateLimitedPerUser` (6th wrong code within a minute → 429).
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement; add contract tests** (generate a CSR in the test) **and probes; `make openapi`.**
- [ ] **Step 4: Run** `go test ./internal/app/web/...` and `make test-pg` — PASS; `TestOpenAPIUpToDate` PASS.
- [ ] **Step 5: Commit** `feat(api): agent enrollment routes; claiming a code signs the agent's cert`

### Task 6: Delete a server

No delete route exists today; revocation needs one.

**Files:**
- Modify: `internal/app/web/handler/server/{routes.go,handler.go}`; create `delete.go`
- Modify: `internal/domain/port` (`ServerRepository.DeleteServer(ctx, serverID string) error`), `internal/infra/db/sqlc/query/{sqlite,pgsql}/servers.sql`, server repo + shims
- Define in `internal/domain/port`: `type Revoker interface { Revoke(ctx context.Context, serverID string) error }` (nil in standalone; Task 7 provides the bus implementation)
- Test: handler tests, contract test + probe, repo test

**Interfaces:**
- Produces: `POST /api/v1/server/delete-server` `{server_id}` → 200 `{}`; `webutil.RequireServerAccess` first (403 on another org's server). Deletes the `servers` row (FK cascades remove certificates, apps rows, capabilities…), drops the server from the in-memory status cache, then calls `Revoker.Revoke`. A revoke publish failure is logged, not returned (the hub's connect-time check still denies the next connection). Containers on the box are not touched.

- [ ] **Step 1: Write failing tests**: `TestDeleteServerOK` (row gone, revoker called with id), `TestDeleteServerOtherOrg403`, `TestDeleteServerUnknown403`, repo `TestDeleteServerCascades`.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement; contract + probe; `make sqlc`, `make openapi`.**
- [ ] **Step 4: Run** `go test ./internal/... ` and `make test-pg` — PASS.
- [ ] **Step 5: Commit** `feat(api): delete a server`

### Task 7: Agent auth on the API side

**Files:**
- Modify: `internal/infra/transport/bus/bus.go` (message types)
- Create: `internal/domain/usecase/agentauth/{check.go,responder.go,check_test.go,responder_test.go}`
- Modify: server repo (query `GetActiveServerCertificate`), `internal/infra/bootstrap/core.go` (start responder + revoker when distributed), events sink in bootstrap (unknown-server drop)

**Interfaces:**
- Produces (`bus` package):
  ```go
  type AgentAuthRequest struct { RequestID, HubID, ServerID, CertFingerprint string } // json snake_case
  type AgentVerdict string // "allowed" | "revoked" | "unknown"
  type AgentAuthReply struct { RequestID string; Verdict AgentVerdict; Reason string }
  type AgentRevoke struct { ServerID string }
  ```
- Produces (`agentauth`):
  - `func Check(ctx, repo port.ServerRepository, serverID, fingerprint string) (bus.AgentVerdict, string, error)` — no server → `unknown` "server not found"; server exists but fingerprint ≠ its active certificate's → `revoked` "certificate is not active"; else `allowed`. DB error → returned (no reply sent; the hub times out to `Unavailable`).
  - `func StartResponder(ctx, b bus.Bus, cfg *config.ServerConfig, repo port.ServerRepository, log) error` — subscribes `GetAgentAuthChannel()`, answers each request on `config.AgentAuthReplyChannel(req.HubID)`, each check bounded by 3 s.
  - `type BusRevoker struct{...}; func (r *BusRevoker) Revoke(ctx, serverID) error` — publishes `AgentRevoke` on `GetAgentRevokeChannel()`. Satisfies Task 6's `Revoker`.
- Events defense: the API's events consumer drops events whose `ServerID` is not a known server, using a lookup cached per server id for 1 minute (positive and negative), so a metrics tick costs no extra query.

- [ ] **Step 1: Write failing tests**: `TestCheckAllowed`, `TestCheckUnknownServer`, `TestCheckWrongFingerprint` (→ `revoked`), `TestResponderRepliesOnHubChannel` (mem bus: publish request with hub `h1`, receive reply on `agent-auth-reply:h1` with matching `request_id`), `TestRevokerPublishes`, `TestEventsFromUnknownServerDropped`.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement; wire in `WireCore` only for distributed** (`cfg.IsStandalone()` false).
- [ ] **Step 4: Run** `go test ./internal/... ` — PASS.
- [ ] **Step 5: Commit** `feat(api): answer hubs' agent auth checks; publish revocations`

### Task 8: Hub identity and authorization

**Files:**
- Create: `internal/infra/transport/grpc/hub/{identity.go,authorizer.go,identity_test.go,authorizer_test.go}`
- Modify: `internal/infra/transport/grpc/hub/hub.go` (`RegisterAgent` ~:290, `AgentStream` ~:347, `StartBusBridge` ~:75), `internal/infra/bootstrap/hub.go`
- Test: `internal/infra/transport/grpc/hub/e2e_test.go`

**Interfaces:**
- Consumes: Task 7 bus types; Task 2 `Fingerprint`.
- Produces:
  - `func peerIdentity(ctx context.Context) (serverID, fingerprint string, err error)` — from `peer.FromContext` → `credentials.TLSInfo` → `State.VerifiedChains[0][0]`; CN and SHA-256 of `Raw` (same hex form as `cert.Fingerprint`).
  - `type Authorizer interface { Authorize(ctx context.Context, serverID, fingerprint string) (bus.AgentVerdict, string, error) }`
  - `type BusAuthorizer struct{...}; func NewBusAuthorizer(b bus.Bus, cfg *config.ServerConfig, hubID string, timeout time.Duration, log) *BusAuthorizer; func (a *BusAuthorizer) Start(ctx) error` — subscribes its reply channel once; `Authorize` registers `request_id` in a mutex-guarded map, publishes, waits `timeout` (5 s) or ctx; always deletes its entry on return; replies for unknown ids are dropped; channel sends are non-blocking (buffer 1).
  - Hub generates `hubID` with `util.GenerateID()` at start.
- Behavior:
  - `RegisterAgent`: `peerIdentity` failure → `Unauthenticated`; `AgentId != CN` → `PermissionDenied` "agent id does not match certificate"; `Authorize` error/timeout → `Unavailable` "cannot verify server right now"; `revoked`/`unknown` → `PermissionDenied` with reason; else current behavior.
  - `AgentStream`: identity from the peer certificate; any message whose `Base.AgentId` differs → close the stream with `PermissionDenied`, publish nothing for it.
  - Revoke: subscribe `GetAgentRevokeChannel()`; on a message, if this hub holds that server's stream, cancel it and delete the registration.

- [ ] **Step 1: Write failing tests**: `TestPeerIdentity` (fake TLS peer), `TestAuthorizerAllowed`, `TestAuthorizerTimeout` (no responder → error after timeout, map empty), `TestAuthorizerLateAndDuplicateReplies` (two replies, one after timeout → first used, no panic, map empty), e2e: `TestRegisterRejectsMismatchedAgentID`, `TestRegisterDeniedForUnknownServer` (`PermissionDenied`), `TestRegisterUnavailableWithoutResponder` (`Unavailable`), `TestStreamRejectsForeignAgentID`, `TestRevokeClosesStream`.
- [ ] **Step 2: Run** `go test ./internal/infra/transport/grpc/hub/` — FAIL.
- [ ] **Step 3: Implement; wire `BusAuthorizer` in `BootstrapHUB`.** Existing e2e tests get a fake authorizer that allows, so they stay green.
- [ ] **Step 4: Run** `go test -race ./internal/infra/transport/grpc/...` — PASS.
- [ ] **Step 5: Commit** `feat(hub): agents must match their certificate and an active server`

### Task 9: Agent gives up after repeated denial

**Files:**
- Modify: `internal/infra/transport/grpc/agent/agent.go` (`Run` ~:496), `cmd/agent/main.go`
- Test: `internal/infra/transport/grpc/agent/agent_test.go` (or a new `run_test.go`)

**Interfaces:**
- Produces: `func (a *Agent) Run(ctx context.Context) error` — returns `nil` on ctx cancel, `ErrRevoked` after 5 consecutive `codes.PermissionDenied` from connect/register/stream start; `var ErrRevoked = errors.New("server was removed or its certificate is not active")`; `const ExitRevoked = 78`. Denied retries wait 1m, 2m, 3m, 4m (injectable for tests via an unexported `deniedBackoff []time.Duration`). Any other error uses the existing backoff and resets the denied count only on success (a mix of denied and unavailable keeps the count).
- `cmd/agent/main.go`: on `ErrRevoked` log the reason at error level and `os.Exit(grpcagent.ExitRevoked)`.

- [ ] **Step 1: Write failing tests** with a fake connect func: `TestRunStopsAfterFiveDenials` (returns `ErrRevoked`, exactly 5 attempts, waits match `deniedBackoff`), `TestRunUnavailableNeverStops` (cancel after N attempts → `nil`), `TestRunSuccessResetsDenials` (3 denied, success, stream ends, 4 denied → still running).
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement** (extract the bring-up into a field func so tests can fake it).
- [ ] **Step 4: Run** `go test -race ./internal/infra/transport/grpc/agent/` — PASS.
- [ ] **Step 5: Commit** `feat(agent): stop for good after the hub repeatedly refuses this server`

### Task 10: `winterflow-agent enroll`

**Files:**
- Create: `internal/app/agent/enroll/{enroll.go,client.go,enroll_test.go}`
- Modify: `cmd/agent/main.go` (hidden subcommand before config load, like `backup`)

**Interfaces:**
- Consumes: Task 5 routes and JSON shapes.
- Produces: `func Main(args []string, stdout, stderr io.Writer) int` with flags `--url` (default `https://api.winterflow.dev`), `--cert-dir` (required), `--env-out` (required: file receiving `HUB_HOST=`, `HUB_PORT=`, `AGENT_ID=` lines), `--hostname` (default `os.Hostname()`). Exit codes: 0 enrolled, 2 gave up (10 codes unused), 3 already enrolled (`agent.crt` exists; nothing changed), 1 any other error.
- Behavior: URL must be https unless the host is loopback. Writes `agent.key` (0600) and keeps it across retries within the run; every HTTP call has a 30 s timeout; transient HTTP errors retry with backoff and don't consume codes. Prints each code in a framed block with the minutes it is valid and "N codes left", then polls `status` every 3 s; on `expired` calls `refresh` and prints the new code; on `failed`/409 exits 2 with "No code was entered. Run the installer again to get a new one." On `claimed`: writes `agent.crt`, `ca.crt` (0644), the env file (0600), prints "Connected as <server_id>". Files are written to temp names and renamed.

- [ ] **Step 1: Write failing tests** against an `httptest` server scripted per case: `TestEnrollHappyPath` (files written, modes, env content `HUB_HOST=hub-01.winterflow.dev`, `HUB_PORT=50051`, `AGENT_ID=<id>`), `TestEnrollRefreshesExpiredCode` (second code printed), `TestEnrollGivesUp` (exit 2, no `agent.crt`), `TestEnrollAlreadyEnrolled` (exit 3, files untouched), `TestEnrollRejectsHTTPURL` (non-loopback http → exit 1), `TestEnrollTransientErrorRetries` (one 502 then ok).
- [ ] **Step 2: Run** `go test ./internal/app/agent/enroll/` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `feat(agent): enroll subcommand that shows a code and collects the signed certificate`

### Task 11: Release binaries for the control plane

**Files:**
- Modify: `.goreleaser.yaml` (builds `api` → `winterflow-api`, `hub` → `winterflow-hub`; same env/goos/goarch/ldflags as `agent`)
- Modify: `cmd/hub/main.go` (hidden `pki` subcommand: `winterflow-hub pki --dir D --host H [--host H2]` → `cert.EnsureHubPKI`, prints `changed`/`unchanged`)
- Modify: `internal/app/web/spa` (inject `<meta name="wf-app-mode" content="standalone|distributed">` into the served shell from the running binary's topology), `web/src/config.ts` (read the meta first, fall back to `VITE_APP_MODE`), `Makefile` (`make build` also builds hub/agent as PR #33 added)
- Test: `internal/app/web/spa/*_test.go`, `web/src/config.test.ts`, a `cmd/hub` test for `pki`

Why: the release builds the SPA once in standalone mode; the API binary must still render the distributed UI.

- [ ] **Step 1: Write failing tests**: `TestShellCarriesAppMode` (served index contains the meta for each topology), web `reads app mode from meta`, `TestPKISubcommand` (creates files, second run prints `unchanged`).
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./internal/app/web/spa/ ./cmd/hub/`, `pnpm --dir web test -- config`, `goreleaser check` (if installed) — PASS.
- [ ] **Step 5: Commit** `build: release the API and hub binaries; app mode comes from the server`

### Task 12: Web UI — install command and removing a server

**Files:**
- Modify: `web/src/components/server-creation-dialog.tsx` (+ its test)
- Modify: the server settings/details view that lists a server's actions (find via the servers page; add "Remove server"), plus a client function in `web/src/lib` next to the existing server calls

**Interfaces:**
- Consumes: Task 5 (claim errors incl. 429), Task 6 route.
- Produces: `buildInstallCommand(apiOrigin: string): string` →
  `curl -fsSL https://raw.githubusercontent.com/dmtrkzntsv/winterflow/main/scripts/install.sh | sudo bash -s -- --agent --url <apiOrigin>`. Copy for the code step: codes refresh every 10 minutes on the server, the installer gives up after 10. `codeProblem` maps `too many attempts, wait a minute` to plain words. Remove server: a confirm dialog saying the server disconnects at once, its apps vanish from Winterflow but keep running on the machine, and reconnecting it means running the installer again.

- [ ] **Step 1: Write failing tests**: `buildInstallCommand uses the API origin`, dialog renders the command with `apiBase`, rate-limit message mapped, remove-server confirm calls `delete-server` and refreshes the list.
- [ ] **Step 2: Run** `pnpm --dir web test` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `pnpm --dir web test`, `pnpm --dir web run lint`, `pnpm --dir web run build` — PASS.
- [ ] **Step 5: Commit** `feat(web): install command from this instance; remove a server`

### Task 13: `install.sh` modes

**Files:**
- Modify: `scripts/install.sh`, `scripts/install_test.sh`, `.github/workflows/ci.yml` (only if the test invocation changes)

**Interfaces:**
- Consumes: release assets `winterflow-{standalone,agent,api,hub}-linux-<arch>` (Task 11), `winterflow-agent enroll` exit codes (Task 10), `winterflow-hub pki` (Task 11), `ValidateEnrollment` env (Task 1).
- Modes: no mode flag = standalone (unchanged behavior and output); `--agent`; `--control-plane`. Shared code (download + checksum, service user, Docker offer, journald namespace, limits, `--dry-run`, `--uninstall`/`--purge`, `--yes`, `--version`, `--binary`) is reused per mode; `release_asset_name` takes the binary name.
- `--agent [--url URL]`: Docker check; install `/usr/local/bin/winterflow-agent`; `/etc/winterflow/agent.env` (`AGENT_DATA_DIR=/var/lib/winterflow`, `HUB_CERT_DIR=/var/lib/winterflow/certs`, plus the enroll output appended); unit `winterflow-agent.service` with the standalone hardening, `RestartPreventExitStatus=78`, installed but **not enabled** until `enroll` exits 0. Without `--url`: interactive run asks with `https://api.winterflow.dev` pre-filled; piped run uses it silently. Enroll exit 3 → skip enrollment (rerun). Exit 2/1 → print the failure, leave unit disabled and stopped, exit non-zero.
- `--control-plane`: Debian/Ubuntu only (else clear error). Detect Postgres (`psql`/`postgresql` service) and Redis (`redis-server`); ask before `apt-get install` of each missing one (`--yes` = yes; declining = exit with what to install). Create role/db `winterflow` with a generated password only if absent (password kept in `/etc/winterflow/control-plane.env` and reused). Ask: public UI URL (→ `ALLOWED_ORIGINS`, secure cookies), API port (default 8080, bound to 127.0.0.1), hub public address (default `$(hostname -f):50051`). Run `winterflow-hub pki --dir /var/lib/winterflow/pki --host <hub host> --host $(hostname -f)`. Units `winterflow-api.service` (env: `DATABASE_URL=postgres://…`, `REDIS_*`, `REGION`, `HUB_CERT_DIR` = the pki dir, `AGENT_HUB_ADDR`, JWT secret) and `winterflow-hub.service` (CA cert + hub pair only; the CA key file is not readable by the hub's user — separate users `winterflow-api` / `winterflow-hub`, key owned by `winterflow-api`, mode 0600). On finish print: proxy your Caddy to `127.0.0.1:<port>`, open TCP `<hub port>`, and the URL to open.
- Standalone, untouched except the shared refactor.

- [ ] **Step 1: Extend `install_test.sh` with failing dry-run cases**: `standalone_unchanged` (output matches the previous dry-run golden), `agent_unit_has_restart_prevent_78`, `agent_default_url_piped`, `agent_url_flag`, `agent_rerun_keeps_enrollment` (fake existing `agent.crt` in a temp root → no enroll call in the plan), `control_plane_units_and_env` (both units; API env has `AGENT_HUB_ADDR`, `DATABASE_URL=postgres://`; hub env has no CA key path), `control_plane_refuses_non_debian`.
- [ ] **Step 2: Run** `bash scripts/install_test.sh` — FAIL.
- [ ] **Step 3: Implement.** Dry-run prints every file and the commands it would run, as today.
- [ ] **Step 4: Run** `bash scripts/install_test.sh`, `bash -n scripts/install.sh`, `shellcheck scripts/install.sh` (if available) — PASS.
- [ ] **Step 5: Commit** `feat(install): agent and control-plane modes`

### Task 14: Docs, full check, PR

**Files:**
- Modify: `CLAUDE.md` (Architecture: enrollment, agent auth, delete-server; Commands: install modes), `AGENTS.md`, `README.md` (install section for all three modes, Caddy + open hub port note)

- [ ] **Step 1: Write the docs.**
- [ ] **Step 2: Run the CI reproduction from CLAUDE.md** (`make test`, `make test-pg …`, `make web-test`, `go run ./tools/diffcov --base main --min 80 …`, `make lint`, `make sqlc-check`, `bash scripts/install_test.sh`) — all PASS, diff coverage ≥ 80%.
- [ ] **Step 3: Commit** `docs: installers and agent enrollment`
- [ ] **Step 4: Push, open a draft PR**; after it's open, comment on PR #33 that it's superseded and close it (the approved design absorbs #33).
