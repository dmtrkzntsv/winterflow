# Installing a control plane

A control plane is the API (web UI, database, certificate signing) plus a hub
(the gRPC endpoint agents dial over mTLS), joined by a Redis bus, on one host.
`scripts/install-control-plane.sh` sets all of it up. Agents are added later
from the web UI.

Status: the script is covered by `scripts/install_control_plane_test.sh`
(dry-run, scratch root) but has not been run on a real host yet. Expect to
read its output.

## Prerequisites

- Debian or Ubuntu with systemd (other distros are refused), root access.
- Caddy (or any proxy) in front of the API, terminating TLS with your
  Cloudflare certificates and proxying to `127.0.0.1:<API port>`. The
  installer does not install or configure Caddy; it binds the API to
  loopback only and prints the address to proxy to, with the snippet
  `reverse_proxy 127.0.0.1:<port> { header_up X-Forwarded-For {client_ip} }`.
  The API's per-IP limits read the client address from the last
  `X-Forwarded-For` entry, only on connections from loopback (`X-Real-IP` is
  ignored: a plain proxy passes a client-supplied one through), so the proxy
  must overwrite it with the real client, as the snippet does. Behind
  Cloudflare, configure Caddy's global options so `{client_ip}` resolves to
  the visitor: `servers { trusted_proxies static <Cloudflare ranges>
  client_ip_headers CF-Connecting-IP }` (or the `trusted_proxies cloudflare`
  module). Otherwise every client shares Cloudflare's edge address for the
  limits.
- The hub port (default 50051) reachable from the internet, with no proxy in
  front: agents verify the hub's certificate against the Winterflow CA. The
  installer does not touch the firewall.
- Access to the release mirror (Cloudflare R2, private): export
  `WINTERFLOW_R2_ENDPOINT` (`https://<account_id>.r2.cloudflarestorage.com`),
  `WINTERFLOW_R2_ACCESS_KEY_ID` and `WINTERFLOW_R2_SECRET_ACCESS_KEY`. The
  script downloads `winterflow-api` and `winterflow-hub` with
  `curl --aws-sigv4` (curl 7.75+) and verifies `SHA256SUMS`.
- PostgreSQL and Redis are installed from distro packages after asking, if
  missing (`--yes` assumes yes).

## Running it

```sh
export WINTERFLOW_R2_ENDPOINT=https://<account_id>.r2.cloudflarestorage.com
export WINTERFLOW_R2_ACCESS_KEY_ID=...
export WINTERFLOW_R2_SECRET_ACCESS_KEY=...
sudo -E bash scripts/install-control-plane.sh --web-url https://app.example.com \
  --hub-addr hub-01.example.com:50051
```

Flags (see `--help` for the full text): `--web-url`, `--port` (API port,
default 8080), `--hub-addr host:port` (its host goes into the hub certificate
and is the hub's id), `--capacity` (default 1000), `--redis-bind` and
`--redis-host` (extra address and certificate name for hubs on other machines,
e.g. a Tailscale IP and name; repeatable), `--version`, `--r2-bucket`
(default `econumo`), `--yes`, `--dry-run` (prints every file and command, no
root needed), `--uninstall`.

## What it creates

- System users `winterflow-api` and `winterflow-hub`; only the API user can
  read the CA key. Units `winterflow-api` and `winterflow-hub`, hardened, with
  CPU and memory limits; logs in the journald namespace `winterflow`
  (`journalctl --namespace winterflow -u winterflow-api -u winterflow-hub -f`).
- Binaries `/usr/local/bin/winterflow-api` and `winterflow-hub`.
- `/var/lib/winterflow-control`: the Winterflow CA, the hub certificate and
  the services' homes. It is separate from the agent's `/var/lib/winterflow`.
- `/etc/winterflow/control-plane.env` (generated Postgres and Redis
  passwords and the JWT secret; root only, back it up),
  `control-plane-api.env` and `control-plane-hub.env`.
- Postgres role and database `winterflow`, created only if absent; the global
  Postgres configuration is not changed.
- A dedicated Redis instance, `redis-server@winterflow`, config
  `/etc/redis/redis-winterflow.conf`: TLS only on port 6380 with a password,
  certificate issued by the Winterflow CA; bound to localhost plus any
  `--redis-bind` addresses. The distro's default Redis is never touched.

## Rerun and upgrade

Rerunning keeps the CA, passwords, JWT secret and data, updates the binaries
and units, and restarts the services. A changed `--hub-addr` reissues the hub
certificate (the CA is kept) and, once the new one is trusted, retires the
old hub id's certificates (`winterflow-api hub-retire`); a retire that fails
only warns and is retried on the next run. Pass `--version vYYMMDD.N` to pin a release.

### Upgrading to this version

Rerun `install-control-plane.sh` and every `install-hub.sh` before agents
update. Hub certificates now carry a `winterflow-hub:<id>` URI name, which
`pki` and `join` add when they reissue the certificate; upgraded agents refuse
a hub certificate without it. The control-plane installer reissues its own
hub's certificate by itself and records it as trusted (`winterflow-api
hub-trust`). A hub joined before this version has a certificate without the
URI and no record on the control plane: rerun its `install-hub.sh` with a new
join token (`--token` or `WINTERFLOW_HUB_TOKEN`, made with `hub-token`
below); without one the rerun stops and asks for it. An agent's `hub.json`
overrides later `HUB_HOST` edits in `agent.env`.

The Redis certificate no longer lists the host's own name (FQDN). Joined hubs
that reach Redis by that name need it back: rerun `install-control-plane.sh`
with `--redis-host <fqdn>` (kept for later reruns). Hubs that use an address
given with `--redis-bind`/`--redis-host` are not affected. An existing CA
keeps its old path-length setting; nothing to do about it.

## Back up the CA

Back up `/var/lib/winterflow-control/pki/ca.key` and `ca.crt` (with the
database and `/etc/winterflow`). Losing them means re-joining every hub and
re-enrolling every agent: nothing else can sign certificates the agents
accept. The installer repeats this when it finishes.

## A hub key leaked or a hub removed

On the control-plane host, with the API's environment:

```sh
sudo bash -c 'set -a; . /etc/winterflow/control-plane-api.env; exec winterflow-api hub-retire --hub <hub id>'
```

(`--fingerprint <sha256>` retires one certificate instead.) Agents stop
accepting the retired certificate when they next ask the API for the set of
certificates they pin: at their next start, and every 6 hours while
connected. Rejoining the hub (rerun `install-hub.sh` on it with a new token)
gives it a new key and certificate and retires its older ones. Once an agent
has pinned a set, retiring every certificate makes it refuse every hub until
the API trusts one again; it does not fall back to checking the CA only.

## Uninstall

`sudo bash scripts/install-control-plane.sh --uninstall` stops and removes the
API and hub units and binaries and disables `redis-server@winterflow`. It
keeps `/var/lib/winterflow-control` (the CA), the `/etc/winterflow` env files,
the Redis config and data, and the Postgres database. There is no `--purge`.

## Adding a hub

A second hub runs on another machine with `scripts/install-hub.sh`; it holds
no data and joins the existing control plane.

1. Let remote hubs reach Redis: install the control plane with `--redis-bind`
   (an address of this host, e.g. its Tailscale IP) and `--redis-host` (the
   name to put in the Redis certificate, e.g. its Tailscale name); rerun it
   if it was installed without them. Hubs connect over TLS only
   (`rediss://:<password>@<cp-host>:6380`); the password is in
   `/etc/winterflow/control-plane.env`.
2. On the API host, create a one-time join token (valid 1 hour, single use;
   the command needs the API's environment, `DATABASE_URL` above all):

   ```sh
   sudo bash -c 'set -a; . /etc/winterflow/control-plane-api.env; exec winterflow-api hub-token'   # --ttl 30m for another lifetime
   ```

   Whoever holds the token chooses the new hub's host name (any but one a
   running hub already uses), so hand it over only to the person who runs
   the hub.
3. On the new machine, open the hub's port (the one in `--public-addr`, or
   `--listen-port` if given; the installer does not touch the firewall) and run:

   ```sh
   export WINTERFLOW_R2_ENDPOINT=... WINTERFLOW_R2_ACCESS_KEY_ID=... WINTERFLOW_R2_SECRET_ACCESS_KEY=...
   export WINTERFLOW_HUB_REDIS_URL='rediss://:<pw>@<cp-host>:6380'
   export WINTERFLOW_HUB_TOKEN=wfh_...        # or --token (visible in the process list)
   sudo -E bash install-hub.sh --public-addr hub-02.winterflow.dev:50051 \
     --api-url https://<your API URL>
   ```

   `--api-url` is the control plane the hub joins; without it the hub joins
   the hosted control plane, `https://api.winterflow.dev`, so on your own
   control plane always pass it. Other flags: `--capacity`, `--listen-port`,
   `--version`, `--r2-bucket`, `--yes`, `--dry-run`. The public address's host goes into
   the hub's certificate and is the hub's id; it cannot change later. The
   hub registers within about 30 s and new servers can then be placed on it.

New agents are spread over accepting hubs; existing agents stay where they
are until a hub drains.

## Draining and moving agents

The subcommand needs the hub's own settings (`HUB_PUBLIC_ADDR`, `REDIS_*`), so
load its env file. On a hub-only machine that is `/etc/winterflow/hub.env`; on
the control plane's own hub it is `/etc/winterflow/control-plane-hub.env`:

```sh
sudo bash -c 'set -a; . /etc/winterflow/hub.env; exec winterflow-hub drain'     # stop taking agents, send the connected ones to other hubs
sudo bash -c 'set -a; . /etc/winterflow/hub.env; exec winterflow-hub undrain'   # accept agents again
```

A draining hub announces it, tells each connected agent to move (about 20 per
second, resent after a minute), and refuses new registrations; agents ask the
API where to go and reconnect there. The flag has no expiry and survives
hub restarts (it is stored in Redis, so it is lost if Redis loses its data).
Stopping the hub service (or `--uninstall`) drains it first and waits up to
2 minutes, so a plain `systemctl stop winterflow-hub` is safe; agents left
behind find another hub on their own after a few minutes. When no other hub
accepts agents (a single hub, or every other one draining) a stopping hub
moves nobody and stops at once; its agents reconnect when it is back.

## Updating, removing a hub

Rerun `install-hub.sh` (no token needed): it replaces the binary and
restarts the service, keeping the certificate and `/etc/winterflow/hub.env`.
A rerun with a join token joins again instead: a new key and certificate
replace the old pair (only once the join succeeds) and the control plane
retires the hub's older certificates; do this after a hub key leaked, and
for a certificate from before the hub URI (such a rerun refuses to go on
without a token).
`--uninstall` drains the hub and removes the unit and binary, keeping the
certificate and settings so a rerun brings it back; `--uninstall --purge` also
deletes the certificate, `hub.env`, data and user, after which the hub needs a
new token to join.

## Adding agents

In the web UI, "Add server" shows the command to run on the new machine:

```sh
curl -fsSL https://get.winterflow.dev/agent | sudo bash -s -- --url https://app.example.com
```

The agent installer prints a 6-character code (valid 10 minutes, refreshed
automatically, up to 10 codes in total); type it into the UI. The control plane signs the
agent's certificate, picks a hub for it and the installer starts the service.

Rerunning the installer on an enrolled machine keeps its enrollment. After
removing a server in the UI, connect the machine again with `--reenroll`
(the Remove-server dialog shows the command); the old certificate and
settings are kept beside the new ones as `*.old-<timestamp>`:

```sh
curl -fsSL https://get.winterflow.dev/agent | sudo bash -s -- --url https://app.example.com --reenroll
```

## Owner to-dos

- Add the GitHub Actions secrets `R2_ENDPOINT`, `R2_ACCESS_KEY_ID` and
  `R2_SECRET_ACCESS_KEY` so the release workflow can mirror binaries to R2
  (without them the upload is skipped with a warning).
- Set up the redirect `https://get.winterflow.dev/agent` to the raw
  `scripts/install-agent.sh` on GitHub
  (`https://raw.githubusercontent.com/dmtrkzntsv/winterflow/main/scripts/install-agent.sh`).
- Run the control-plane installer on a real host and fix what it finds.
