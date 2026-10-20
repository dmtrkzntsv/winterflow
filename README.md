# WinterFlow

Self-hosted app deployment for small always-on home servers — mini-PCs,
Raspberry Pi-class boards, anything Linux that runs Docker. Deploy Docker
Compose apps from a web UI: every app is a git repository under the hood, every
change is a revision you can roll back, secrets are end-to-end encrypted, and
an optional embedded reverse proxy (Caddy) can serve your apps with
automatic HTTPS (off by default — enable with `INGRESS_ENABLED=true`).

## Install standalone

```sh
curl -fsSL https://raw.githubusercontent.com/dmtrkzntsv/winterflow/main/scripts/install.sh | sudo bash
```

The one-liner is **non-interactive**: it installs with sane defaults (web UI
on port 8080, a dedicated `winterflow` service user, Docker installed if
missing) — piped scripts can't prompt, so it never asks and never hangs.
Customize with flags:

```sh
curl -fsSL https://raw.githubusercontent.com/dmtrkzntsv/winterflow/main/scripts/install.sh | sudo bash -s -- --port 9090
```

Prefer to be asked? Download first, then run — the installer prompts for the
port and the service user when it has a real terminal:

```sh
curl -fsSLO https://raw.githubusercontent.com/dmtrkzntsv/winterflow/main/scripts/install.sh
sudo bash install.sh
```

Either way, the installer downloads the latest prebuilt release for your
machine (linux amd64 / arm64 / arm), verifies its checksum, and sets it up
as a hardened systemd service:

- offers to install Docker and the compose plugin when they are missing,
- runs as an unprivileged service user (never root),
- writes `/etc/winterflow/winterflow.env` with a generated JWT secret,
- keeps data (SQLite database, certs, app repos) in `/var/lib/winterflow`,
- routes logs to a dedicated journald namespace with rotation caps, and
- caps the service's CPU and memory so orchestration bursts cannot
  overheat a small fanless box. Your app containers are not limited.

All options: `--port`, `--user`, `--version`, resource caps, `--dry-run`,
`--uninstall`, `--purge` — see `bash install.sh --help`.

**Requirements:** Linux with systemd. Docker is installed for you if missing.

## First run

Open `http://<server-ip>:8080/register` (the installer prints the exact URL)
and create the admin account. Your server is already registered — add your
first app from the **Apps** page (**New app**).

The sidebar leads to **Apps**, **Monitoring**, the **Inbox** (alert history),
**Templates** and **Settings** (Server, Organization, Account); ⌘K / Ctrl+K
opens the command palette from anywhere.

Useful commands:

```sh
systemctl status winterflow
journalctl --namespace winterflow -u winterflow -f   # follow logs
winterflow version                                   # installed version
winterflow help                                      # CLI commands
```

The service runs `winterflow serve`; more subcommands will land under the
same CLI.

Configuration lives in `/etc/winterflow/winterflow.env`;
[`.env.example`](.env.example) documents every option.

## Updating

Rerun the install one-liner: it downloads the latest release and restarts the
service, keeping your config and data. Pin a specific release with
`--version v260823.1` (versions are `vYYMMDD.{build}`; every push to main cuts one).

## Uninstall

```sh
curl -fsSL https://raw.githubusercontent.com/dmtrkzntsv/winterflow/main/scripts/install.sh | sudo bash -s -- --uninstall
```

`--uninstall` keeps your config and data; `--purge` deletes everything.

## Build from source

Requires Go 1.27+ and (for the web UI) node + pnpm:

```sh
git clone https://github.com/dmtrkzntsv/winterflow.git
cd winterflow
make build                  # bundles the SPA + compiles bin/standalone
sudo ./scripts/install.sh   # picks up bin/standalone automatically
```

`make web` runs the frontend dev server; `go test ./...` and `make lint` cover
the backend and web checks. See [AGENTS.md](AGENTS.md) for the contributor
guide and [CLAUDE.md](CLAUDE.md) for the architecture overview.

## Releases

Every push to `main` publishes a release automatically — there is nothing to
do by hand. The [release workflow](.github/workflows/release.yml) runs the
test suite, tags the commit with the next calver version, and uploads the
prebuilt binaries.

- **Versioning** is `vYYMMDD.{build}`: the UTC date plus a build number
  counting up from 1 within the day (`v260823.1`, `v260823.2`, …). The next
  number is derived from existing tags, and runs are serialized, so parallel
  pushes cannot collide.
- **Assets** per release: `winterflow-{standalone|agent}-linux-{amd64|arm64|arm}`
  raw binaries plus `checksums.txt`. The installer and the agent self-updater
  both rely on this exact naming — it is defined in
  [.goreleaser.yaml](.goreleaser.yaml) and must stay in sync with
  `scripts/install.sh` and the self-updater.
- **Dry run locally**: `goreleaser release --snapshot --clean` builds
  everything without tagging or publishing (requires Go, node + pnpm).
- **Re-run**: the workflow can also be started manually from the Actions tab
  (`workflow_dispatch`); it cuts a fresh release of whatever `main` points at.

## Topologies

The default **standalone** binary runs everything — HTTP API, embedded web UI,
agent, and the Docker Compose orchestrator — in one process over SQLite.

The same codebase also builds a horizontally scalable **distributed** topology
(`api` ⇄ Redis ⇄ `hub` ⇄ `agent` over mTLS gRPC) for managing many servers
from one control plane; see [CLAUDE.md](CLAUDE.md) for how the pieces fit.

### Install the distributed control plane

The distributed installer sets up four services on one **Debian or Ubuntu host
with systemd**: PostgreSQL and Redis from the distribution packages, plus the
WinterFlow API and Hub as separate systemd units. PostgreSQL and Redis listen
locally; only the API port and the Hub's mTLS gRPC port need to be reachable
from outside this host. The API embeds the distributed web UI. Docker is needed
on managed agent hosts, not on the control-plane host. The installer detects
PostgreSQL and Redis and asks before installing either missing package; use
`--yes` for unattended installation.

Build from this checkout with Go 1.27.2+, Node 22 and the pnpm version pinned in
`web/package.json`:

```sh
pnpm --dir web install --frozen-lockfile
make distributed-build
sudo bash scripts/install-distributed.sh --hub-host hub.example.com --web-url https://flow.example.com
```

Replace the host names with your own. `--hub-host` must be a DNS name or IPv4
address that agents can reach; it is placed in the Hub certificate. Publish
`WEB_URL` through your HTTPS reverse proxy to API port 8080, and allow agents
to reach Hub port 50051. Use `--port` or `--hub-port` to change those ports.
Run `bash scripts/install-distributed.sh --dry-run --hub-host hub.example.com`
to inspect the files first.

The installer creates separate `api.env` and `hub.env` files under
`/etc/winterflow-distributed`, keeping the API's generated JWT secret and local
PostgreSQL credentials out of the Hub process. It stores the Hub CA
and certificate under `/etc/winterflow-distributed/certs`. Keep the CA private
key, configuration, and PostgreSQL database backed up. Rerunning the installer
updates the two WinterFlow binaries and units while keeping credentials,
database, and CA.
Inspect the services with `systemctl status winterflow-api winterflow-hub
postgresql redis-server` and their logs with `journalctl -u winterflow-api -u
winterflow-hub -f`.

This script installs the **control plane**. Each managed server still needs an
agent with Docker Compose and a client certificate trusted by the Hub. The
repository does not yet include an automated agent provisioning or pairing
installer for this setup.

## License

[The O'Saasy License](LICENSE.md) — free to use, modify, and self-host; you
may not offer it as a competing hosted service.
