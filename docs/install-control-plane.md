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
  loopback only and prints the address to proxy to.
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
certificate (the CA is kept). Pass `--version vYYMMDD.N` to pin a release.

## Uninstall

`sudo bash scripts/install-control-plane.sh --uninstall` stops and removes the
API and hub units and binaries and disables `redis-server@winterflow`. It
keeps `/var/lib/winterflow-control` (the CA), the `/etc/winterflow` env files,
the Redis config and data, and the Postgres database. There is no `--purge`.

## Adding agents

In the web UI, "Add server" shows the command to run on the new machine:

```sh
curl -fsSL https://get.winterflow.dev/agent | sudo bash -s -- --url https://app.example.com
```

The agent installer prints a 6-character code (valid 10 minutes, refreshed
automatically up to 10 times); type it into the UI. The control plane signs the
agent's certificate, picks a hub for it and the installer starts the service.

## Owner to-dos

- Add the GitHub Actions secrets `R2_ENDPOINT`, `R2_ACCESS_KEY_ID` and
  `R2_SECRET_ACCESS_KEY` so the release workflow can mirror binaries to R2
  (without them the upload is skipped with a warning).
- Set up the redirect `https://get.winterflow.dev/agent` to the raw
  `scripts/install-agent.sh` on GitHub
  (`https://raw.githubusercontent.com/dmtrkzntsv/winterflow/main/scripts/install-agent.sh`).
- Run the control-plane installer on a real host and fix what it finds.
