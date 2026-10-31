# WinterFlow

Self-hosted app deployment for small always-on home servers — mini-PCs,
Raspberry Pi-class boards, anything Linux that runs Docker. Deploy Docker
Compose apps from a web UI: every app is a git repository under the hood, every
change is a revision you can roll back, secrets are end-to-end encrypted, and
an optional embedded reverse proxy (Caddy) can serve your apps with
automatic HTTPS (off by default — enable with `INGRESS_ENABLED=true`).

## Install

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

Upgrading from a release that used JWT sign-in? See [docs/upgrading-auth.md](docs/upgrading-auth.md).
To connect Claude or Codex over MCP, see [docs/mcp-setup.md](docs/mcp-setup.md).

## Uninstall

```sh
curl -fsSL https://raw.githubusercontent.com/dmtrkzntsv/winterflow/main/scripts/install.sh | sudo bash -s -- --uninstall
```

`--uninstall` keeps your config and data; `--purge` deletes everything.

## Deploy with ship

Besides the installers above, every release is published to Cloudflare R2
with [ship](https://github.com/dmtrkzntsv/ship), which installs it as a
hardened systemd service. The unit templates and env examples live in
[deploy/ship](deploy/ship). There are four refs:

| ref | binary | env file | what |
|---|---|---|---|
| `winterflow` | `winterflow-linux-<arch>` | `/etc/S/winterflow.env` | standalone (API + web UI + agent, SQLite) |
| `winterflow/api` | `winterflow-api-linux-<arch>` | `/etc/S/api.env` | distributed control plane + web UI |
| `winterflow/hub` | `winterflow-hub-linux-<arch>` | `/etc/S/hub.env` | hub (agents connect here over mTLS) |
| `winterflow/agent` | `winterflow-agent-linux-<arch>` | `/etc/S/agent.env` | agent on each managed server |

`S` is the service name: `winterflow` unless `--service` says otherwise.

### Publish

```sh
make ship-dist && make ship-publish VERSION=v260823.1 CHANNELS=latest
```

`ship-dist` builds `dist/ship/` (all four binaries for amd64, arm64 and arm,
plus the unit templates and env examples); `ship-publish` uploads it and does
not build. `VERSION` is optional (`dev` or empty publishes no version) and so
is `CHANNELS`, but one of them is required. The
[release workflow](.github/workflows/release.yml) does this for every
release, after goreleaser (whose `--clean` wipes `dist/`).

### Install

**Standalone**, one host runs everything:

```sh
sudo ship install winterflow --version latest
sudoedit /etc/winterflow/winterflow.env   # set ENCRYPTION_KEY, WEB_URL
sudo ship start winterflow
```

**Distributed**: the api, hub and agent install as separate refs. PostgreSQL
and a TLS Redis are not installed by ship (`scripts/install-control-plane.sh`
sets both up). The usual topology is one control-plane host with the api and
hub in one service, and an agent on every managed server:

```sh
# control-plane host
sudo ship install winterflow/api --service wf --version latest
sudo ship install winterflow/hub --service wf --version latest
sudoedit /etc/wf/api.env /etc/wf/hub.env
sudo ship start winterflow/api --service wf
sudo ship start winterflow/hub --service wf

# each managed server
sudo ship install winterflow/agent --version latest
sudoedit /etc/winterflow/agent.env       # then enroll with ship run, as it describes
sudo ship start winterflow/agent
```

A hub on another host is `ship install winterflow/hub` there, joined with a
token (the steps are in `hub.env`). Each env file keeps its files in its own
subdirectory of `/var/lib/S` (`api/`, `hub/`, `agent/`), so any mix of refs
can share a service.

One service is one user. With the api and hub in one service, the hub runs as
the same user as the api and can read the CA key in `/var/lib/wf/api/pki`,
which the installers avoid by running them as `winterflow-api` and
`winterflow-hub`. To keep that separation, install them as two services
(`--service wf-api` and `--service wf-hub`).

### Operate

```sh
sudo ship update winterflow/agent             # newest of its channel; --version V|latest|dev to switch
sudo ship rollback winterflow/agent           # back to the previous version
sudo ship logs winterflow/api --service wf    # journal; -n N --no-follow
ship status                                   # every ship-managed service
sudo ship run winterflow -- version           # a CLI command with the service's env, user and data dir
```

Update with `ship update`: the in-app update cannot replace the binary in
`/opt/S`, which the unit keeps read-only.

### Moving an existing host

A host set up by the installers moves over once, with a minute of downtime,
by following ship's generic recipe
[Moving a hand-installed service to ship](https://github.com/dmtrkzntsv/ship#moving-a-hand-installed-service-to-ship).
Do not run the installer on that host afterwards. The values it needs:

| installer | old unit | old binary | old env file | user | data dir | ship |
|---|---|---|---|---|---|---|
| `install.sh` | `winterflow` | `/usr/local/bin/winterflow` | `/etc/winterflow/winterflow.env` | `winterflow` | `/var/lib/winterflow` | `winterflow`, `S=winterflow` |
| `install-agent.sh` | `winterflow-agent` | `/usr/local/bin/winterflow-agent` | `/etc/winterflow/agent.env` | `winterflow` | `/var/lib/winterflow` (certs in `certs/`) | `winterflow/agent`, `S=winterflow` |
| `install-control-plane.sh` | `winterflow-api`, `winterflow-hub` | `/usr/local/bin/winterflow-api`, `/usr/local/bin/winterflow-hub` | `/etc/winterflow/control-plane-api.env`, `/etc/winterflow/control-plane-hub.env` | `winterflow-api`, `winterflow-hub` | `/var/lib/winterflow-control` (CA in `pki/`) | `winterflow/api` + `winterflow/hub`, `S=winterflow-control` |
| `install-hub.sh` | `winterflow-hub` | `/usr/local/bin/winterflow-hub` | `/etc/winterflow/hub.env` | `winterflow-hub` | `/var/lib/winterflow-hub` (certs in `certs/`) | `winterflow/hub`, `S=winterflow-hub` |

With these service names the data dir keeps its path, so the absolute paths
in the old env files keep working, and the key files are in the data dir
(there are none to copy from `/etc`). The control plane also changes user,
and its data dir is root's, which ship refuses: in step 1 also
`mv /var/lib/winterflow-control /var/lib/winterflow-control.pre-ship`, and
after step 2 move its contents back into `/var/lib/winterflow-control` and
`chown -R winterflow-control:winterflow-control` it. Its env files become
`/etc/winterflow-control/api.env` and `hub.env`. PostgreSQL and the
`redis-server@winterflow` instance stay as they are.

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
- **Assets** per release: `winterflow-linux-{amd64|arm64|arm}` (standalone) and
  `agent-linux-{amd64|arm64|arm}` raw binaries plus `checksums.txt`. The installer and the agent self-updater
  both rely on this exact naming — it is defined in
  [.goreleaser.yaml](.goreleaser.yaml) and must stay in sync with
  `scripts/install.sh` and the self-updater.
- **R2 / ship**: the same workflow publishes all four binaries (standalone,
  `agent`, `api`, `hub`) to Cloudflare R2 with ship; see
  [Deploy with ship](#deploy-with-ship).
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

## License

[The O'Saasy License](LICENSE.md) — free to use, modify, and self-host; you
may not offer it as a competing hosted service.
