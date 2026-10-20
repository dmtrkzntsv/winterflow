# Installers and agent enrollment

Date: 2026-10-19. Status: draft for review.

## Intent

Winterflow ships in two topologies, and today only one installs cleanly.
Standalone has `scripts/install.sh`. Distributed — API, Hub, Postgres, Redis on
a host, agents on other machines — has no installer for the control plane
(draft PR #33 started one) and no way at all to install an agent: agent certs
are signed by hand with `make generate-agent-certs`, and the web UI's "Add
server" dialog already tells users to run `curl … | sudo bash` and type a
6-character code that nothing produces for a remote agent.

The owner wants to run Winterflow hosted (one control-plane host now, no
customers yet) and add servers the way the UI already promises: copy a command
from the hosted UI, run it on the new box, type the code it prints back into
the UI, done.

## Goals

1. **Standalone install** stays the existing `install.sh` flow, on SQLite by
   default.
2. **Control-plane install**: API and Hub as systemd services on one host,
   Postgres and Redis on the same host, each detected and installed only after
   asking.
3. **Agent install**: one command copied from the hosted UI installs the agent
   as a systemd service, enrolls it with a code shown in the terminal, and
   starts it only once the code is accepted.
4. **Hub identity and revocation**: an agent may only register under the id
   in its own certificate, and only while its server still exists.

## Non-goals

- Regions. There are none: hubs may sit anywhere, and the existing `REGION`
  setting is removed (see "No regions").
- Adding hubs on other hosts via the installer (a hub on another host is
  configured by hand for now; the control-plane installer sets up one).
- Moving already-enrolled agents to a new hub. An agent keeps the hub address
  it got at enrollment; changing it later is a separate project.
- Running the API or Hub on separate hosts from each other via the installer.
- Postgres/Redis on remote hosts, managed services, or HA. Local packages only.
- Packaging (deb/rpm), containers for the control plane.
- Certificate renewal/rotation (agent certs stay long-lived as today).

## Anti-goals

- An agent that ends up running but unenrolled, retrying a hub it can't reach.
  A failed enrollment leaves the service disabled and stopped.
- The agent's private key leaving the agent's machine.
- Standalone *requiring* Postgres or Redis. (Pointing it at Postgres by
  choice stays allowed.)
- An installer that clobbers an existing Postgres/Redis setup or regenerates
  the CA / passwords on a rerun.

## Decisions made with the owner

- **API URL in the command.** The hosted UI builds the command with its own
  API URL in it. The script has a compiled-in default API URL,
  `https://api.winterflow.dev`, overridable with `--url` (useful for test
  instances). Without `--url`, an interactive run asks for the URL with the
  default pre-filled (Enter accepts it); a piped run uses the default
  silently. *(My call: the owner said "ask only if the script doesn't have
  the info"; with a compiled-in default it always does, so the prompt is just
  a confirmable fallback.)*
- **The API picks the hub.** The agent is never told a hub address on the
  command line; the enrollment response carries it. Hubs will be named like
  `hub-01.winterflow.dev`.
- **Hubs announce themselves.** Each hub has its capacity in its own config
  and reports its state to the API while it runs; the API knows the active
  hubs from those reports. There is no hub list to maintain by hand.
- **A random hub per agent.** Each enrolling server gets a random active hub
  that has room, which spreads load and respects capacity.
- **Code lifetime.** Each code is valid for 10 minutes. When it expires the
  script fetches a fresh code and prints it, without rerunning anything. After
  10 codes (~100 minutes) the script fails and the agent service is left
  disabled and stopped.
- **Existing agents keep their hub.** No re-discovery for now.
- **Standalone defaults to SQLite but may use Postgres** if `DATABASE_URL`
  says so; the installer always sets it up on SQLite. **Distributed requires
  Postgres** (startup refuses SQLite).
- **One control-plane host for now**; more hubs can join later just by
  starting them with the same Redis.
- **TLS:** the owner runs Caddy with Cloudflare certificates in front of the
  API; the hub is exposed directly on its port.
- **Deleting a server locks its agent out** (see "Hub identity and
  revocation").

## Design

### Hubs and who signs certificates

The **API** signs agent certificates, not a hub: the API is the one component
every hub shares, so adding a hub later needs no change to enrollment. The CA private key lives with the API; each hub only needs the CA
certificate (to verify agents) and its own server certificate.

The distributed API refuses to start without the CA key/cert pair.

### No regions

Decided with the owner: Winterflow has no regions. The `REGION` setting and
the queue-name overrides (`BUS_REQUEST_QUEUE`, `BUS_RESPONSE_QUEUE`,
`BUS_EVENTS_QUEUE`) are removed; bus channels have fixed names: `responses`,
`events`, `hubs`, `agent-auth`, `agent-auth-reply:<hub_id>`, `agent-revoke`,
`requests:<hub_id>` per hub, and `requests` for standalone's in-process
bridge. One Redis serves one deployment.

### Hub registry and routing

**Hub config** (on each hub host):
- `HUB_PUBLIC_ADDR` — the `host:port` agents use, e.g.
  `hub-01.winterflow.dev:50051`. Its host part is the hub's permanent id
  (`hub_id`), so a server can record which hub it is on.
- `HUB_CAPACITY` — how many servers this hub should carry (default 1000).
- `HUB_ACCEPTING` — `true` by default; `false` drains the hub: it keeps its
  servers but gets no new ones.

**Status reports:** every 30 s each hub publishes on `hubs`:
`{hub_id, public_addr, capacity, connected_agents, accepting, version}`. The
API upserts a `hubs` table (`hub_id` PK, `public_addr`, `capacity`,
`connected_agents`, `accepting`, `version`, `last_seen`). A hub is **active**
while its last report is under 90 s old. Every API replica writes the same
row; last write wins, which is fine since reports are idempotent.

**Picking a hub** (at claim time, when the certificate is signed):
- Candidates: active, accepting hubs whose assigned servers (rows in
  `servers` with that `hub_id`) are below capacity. Pick one at random.
- *(My call)* If every active hub is full, pick the active hub with the
  lowest assigned/capacity ratio and log a warning: enrollment never blocks
  on capacity.
- If no hub is active, the claim fails with "no hub is available, try again
  in a minute" (the code stays valid; the user retries).
- `servers.hub_id` records the choice; the status response returns that
  hub's `public_addr`.

**Command routing:** today every hub listens on one shared request queue and
a hub that lacks the target agent answers "agent not connected", which can
beat the real answer. With several hubs that breaks commands, so *(my call)*
each hub consumes its own queue `requests:<hub_id>`, and the API
publishes each command to the queue of the hub its server is on (server ->
hub cached in memory, refreshed on miss). A server with no `hub_id` (none
exist in distributed today) gets "server has no hub; reinstall the agent".
Responses and events keep their shared queues. Standalone's in-process
bridge keeps the single queue.

### Enrollment protocol (new)

All under the API's public HTTP origin, unauthenticated, behind the existing
per-IP rate limiter (`internal/app/web/middleware/ratelimit`):

1. `POST /api/v1/agent/enroll` — body: a PEM CSR (P-256, generated on the
   agent box), hostname. Response: `enrollment_id`, `poll_secret` (random,
   high entropy; only its hash stored), `code`, `expires_at`,
   `codes_left`. Creates a pending registration holding the CSR; nothing is
   signed yet.
2. `POST /api/v1/agent/enroll/refresh` — with `poll_secret`: replaces the code
   with a new one valid 10 minutes. Refused after the 10th code; the server
   enforces the cap too, not only the script.
3. `POST /api/v1/agent/enroll/status` — with `poll_secret`: `pending`,
   `expired` (code lapsed, refresh available), `failed` (cap reached), or
   `claimed` with the signed certificate, the CA certificate, `server_id` and
   the address of the hub picked for it. The script polls every few seconds.
4. Claim: the **existing** authenticated `POST /api/v1/server/register {code}`.
   On claim the API assigns the `server_id`, signs the stored CSR with
   `CN = server_id` (the CSR's own subject is ignored), creates the `servers`
   row in the caller's org and stores the certificate — in one transaction,
   as `ClaimServer` does today.

Storage: extend `server_registrations` (CSR, poll-secret hash, codes issued,
state) or add a sibling table — builder's call; the existing standalone
self-registration path (`AgentService.Register`) must keep working. Expired and
failed pending registrations are pruned; the number of pending registrations
is capped so the unauthenticated endpoint can't grow the table without bound.

*(My call)* The claim route gets a per-user/per-org attempt limit (a handful
of wrong codes per minute), since a claimed box is root-equivalent through
Docker and the hosted instance may allow sign-up. The 6-character code and
the UI input stay as they are.

The claimed result stays retrievable by `poll_secret` for a short window
(e.g. 15 minutes) so a script that lost its connection can still collect it.

### Releases and distribution

Decided with the owner:

- **GitHub release** (public): only the standalone and agent binaries,
  renamed to `winterflow-<os>-<arch>` (standalone) and `agent-<os>-<arch>`.
  The old names (`winterflow-standalone-*`, `winterflow-agent-*`) are
  dropped outright (owner's decision): binaries installed before this
  change can no longer self-update and need the installer rerun once. The
  self-updater and scripts use the new names.
- **Cloudflare R2** (private, S3-compatible): every binary, with the
  `winterflow-` prefix: `winterflow-standalone`, `winterflow-agent`,
  `winterflow-api`, `winterflow-hub`, each `-<os>-<arch>`, plus
  `checksums.txt`, under `releases/<tag>/`, and `releases/latest` holding
  the newest tag. Uploaded by the same release run, same version.
- The API binary uploaded to R2 embeds a web UI built with
  `VITE_APP_MODE=distributed` (its own build step), so no runtime mode
  switch is needed.
- **Credentials are environment variables** the owner sets (in GitHub
  Actions secrets for upload, in the shell for the control-plane install).
  *(My call on names)*: `WINTERFLOW_R2_ENDPOINT` (the account's S3
  endpoint URL), `WINTERFLOW_R2_BUCKET`, `WINTERFLOW_R2_ACCESS_KEY_ID`,
  `WINTERFLOW_R2_SECRET_ACCESS_KEY`. The release job skips the R2 upload
  with a warning when they are absent (forks, PRs). The installer
  downloads with `curl --aws-sigv4 "aws:amz:auto:s3"` (curl >= 7.75), so
  no AWS CLI is needed.

### Scripts

Three separate files, each self-contained so it works piped from curl.
The helpers they share (download + checksum, service user, Docker offer,
journald namespace, limits, `--dry-run`, `--uninstall`/`--purge`) are
copied into each; a test fails when a shared helper's copies differ.

- `scripts/install.sh`: standalone, as today (new asset name). The only
  installer the README mentions, by its GitHub URL.
- `scripts/install-agent.sh`: the agent. The UI shows it through the
  shortcut `https://get.winterflow.dev/agent`, which redirects to this
  script's raw GitHub URL. The redirect is set up by the owner outside
  this repo.
- `scripts/install-control-plane.sh`: Postgres, Redis, API, Hub. Not in the
  README; refuses to start without the R2 variables.

### Agent install (`install-agent.sh`)

Same machinery as standalone (Docker check/offer, service user,
hardened unit, journald namespace, CPU/memory limits, `--dry-run`,
`--uninstall`/`--purge`, `--yes`, `--version`, `--binary`):

1. Resolve the API URL (`--url`, else default; https required except a
   loopback host for testing).
2. Install the agent binary (GitHub release asset `agent-<os>-<arch>`,
   installed as `/usr/local/bin/winterflow-agent`; static as backups require).
3. Generate the agent key + CSR locally (`openssl` or a hidden binary
   subcommand — builder's call; the binary is preferred so there is no
   openssl dependency). Key 0600, owned by the service user.
4. Enroll, print the code large and clear with how long it is valid, and
   poll. On expiry: refresh and print the new code. After 10: print a clear
   failure, leave unit disabled/stopped, exit non-zero.
5. On `claimed`: write cert + CA cert, write the agent env (`HUB_HOST`,
   `HUB_PORT` from the returned address, `HUB_CERT_DIR`, `AGENT_DATA_DIR`,
   `AGENT_ID` = `server_id`), enable and start the unit, confirm it's running.
6. Rerun on an enrolled box: detect existing cert and do nothing destructive
   (just reinstall binary/unit). Re-enrolling requires an explicit flag.

Piped (`curl | sudo bash`) runs are non-interactive, as standalone already is;
the code display and polling work without a TTY.

### Control-plane install (`install-control-plane.sh`)

Finishes PR #33's direction. *(My call)* This work absorbs #33 — its Go
config change, docs and CI checks — onto a fresh branch, and #33 is closed
once the new PR is open.

- Downloads `winterflow-api` and `winterflow-hub` from R2 (latest, or
  `--version`), verifying `checksums.txt`.

- Installs `api` and `hub` binaries, units `winterflow-api` and
  `winterflow-hub`, same hardening/journald/limits approach as standalone.
- Postgres and Redis: detect installed/running; if missing, ask before
  installing from distro packages (`--yes` assumes yes). If present, only
  create Winterflow's own role/database (Postgres) and point at the local
  instance (Redis); never rewrite their global config.
- Generates the CA and the hub server cert once; gives the API the CA key and
  cert, the hub the CA cert and its own cert/key. Reruns keep CA, DB
  password, JWT secret, data.
- Asks for the hub's public address (default: this host's FQDN with the hub
  port) and capacity (default 1000) and writes them as `HUB_PUBLIC_ADDR` /
  `HUB_CAPACITY`; asks for the API port as standalone does.
- Debian/Ubuntu first (as PR #33); other distros fail with a clear message.

### Standalone

Unchanged apart from a config change: `DATABASE_URL` defaults to
`sqlite://winterflow.sqlite` when unset; a `postgres://` URL is still
accepted. Anything else is refused at startup.

### Web UI

The "Add server" dialog builds the command from the API's own origin instead
of the hard-coded `get.winterflow.io` line, e.g.
`curl -fsSL https://get.winterflow.dev/agent | sudo bash -s -- --url <API origin>`.
The code step is unchanged (same route). Error copy gains the "too many
codes / start again" case if the API reports it.

### Hub identity and revocation

Two checks when an agent registers with a hub (`RegisterAgent`, i.e. when a
connection opens; not on heartbeats, no caching):

1. **Identity:** reject an `AgentId` that differs from the verified client
   certificate's CN.
2. **Ask the API:** the hub asks the API whether this server and this exact
   certificate are still active.

Decided with the owner: the hub asks the API (the database stays the only
source of truth) rather than reading a server list synced into Redis. A
synced list can't tell "deleted" from "the copy is missing", and the agent
must be able to trust a "no" enough to stop.

**Messages** (on the existing bus; Redis pub/sub, so every API replica
receives a question and a question nobody hears is lost):

| Channel | Direction | Payload |
|---|---|---|
| `agent-auth` | hub → API | `{request_id, hub_id, server_id, cert_fingerprint}` |
| `agent-auth-reply:<hub_id>` | API → the asking hub | `{request_id, verdict, reason}` |
| `agent-revoke` | API → all hubs | `{server_id}` on server delete |

- `verdict`: `allowed`, `unknown` (no such server, which includes a deleted
  one: its rows are gone), or `revoked` (the server exists but this
  certificate is not its active one). Both refusals mean `PermissionDenied`.
- Each hub subscribes to its own reply channel, named with its stable
  `hub_id` (the host part of `HUB_PUBLIC_ADDR`).

**API:** a small usecase `agentauth.Check(server_id, fingerprint) -> verdict`
reading `servers` and the server's active certificate
(`server_certificates`). Every replica answers; the hub takes the first reply
per `request_id` and drops the rest. Deleting a server publishes
`agent-revoke`. Defense in depth: the API also ignores responses and events
tagged with a server id it doesn't know.

**Hub:** waits up to 5 s for a reply.
- `allowed`: accept the stream.
- `revoked` / `unknown`: refuse with gRPC `PermissionDenied` and the reason.
- no reply in time, or a bus error: refuse with `Unavailable` (fail closed,
  but transient).
- On `agent-revoke`: close that agent's stream if this hub holds it.

**Agent:**
- `PermissionDenied` five times in a row (spread over about 10 minutes): log
  the reason and exit with code 78. The unit sets
  `RestartPreventExitStatus=78`, so systemd leaves it stopped until someone
  restarts it by hand.
- `Unavailable` (or any other failure): the normal 1-30 s retry loop,
  forever. An API or Redis outage never makes agents give up.
- Any successful connection resets the count.

Re-enrolling a box issues a new server id; the old id is never reactivated.
None of this applies to standalone (in-process bridge, one built-in agent).

### TLS and exposure

- **API:** the owner terminates TLS in their own Caddy (Cloudflare origin
  certificates). The control-plane installer only binds the API to a local
  port and prints the address to proxy to. It does not install or configure
  Caddy. `ALLOWED_ORIGINS` and secure cookies are set for the public URL the
  installer asks for.
- **Hub:** exposed directly on its port with no proxy in front. Its server
  certificate comes from Winterflow's own CA (agents trust it through the CA
  certificate they get at enrollment). The certificate's SAN must include the
  host in `HUB_PUBLIC_ADDR`, so the installer asks for that address first and
  issues the certificate for it (plus the local hostname). A rerun with a
  changed address reissues the hub certificate and keeps the CA. The installer
  reminds the owner to open the port. It does not touch the firewall.

## Builder's latitude

- Table layout for pending enrollments, endpoint names/shapes (within the
  above), poll interval, response field names.
- Key/CSR generation mechanism on the agent.
- How the shared-helper sync test marks the shared blocks.
- Contract tests, OpenAPI, `.env.example` updates follow the repo rules
  (CLAUDE.md).
