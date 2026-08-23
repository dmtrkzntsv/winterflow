#!/usr/bin/env bash
#
# Install the WinterFlow standalone binary as a systemd service.
#
# The standalone binary runs the HTTP API, the embedded agent, and the Docker
# Compose orchestrator in one process over SQLite. This script:
#   - offers to create a dedicated system user (recommended) so the service
#     does not run as root,
#   - installs the binary to /usr/local/bin/winterflow,
#   - writes /etc/winterflow/winterflow.env with a generated JWT secret,
#   - creates /var/lib/winterflow for the database, certs, and app data,
#   - installs a hardened systemd unit and starts it.
#
# Usage:
#   sudo ./scripts/install.sh [options]
#
# Options:
#   --binary PATH   Prebuilt standalone binary to install. Default: build
#                   ./cmd/standalone from the repo this script lives in.
#   --user NAME     Service user name (default: winterflow).
#   --port PORT     API port (default: 8080).
#   --yes           Non-interactive: assume "yes" to all prompts.
#   --uninstall     Stop and remove the service, binary, and unit file.
#                   Config and data are kept; remove them manually.
#   --purge         Like --uninstall, but also delete the config, all data
#                   (database, certs, deployed app repos), and the service
#                   user. Irreversible; asks for confirmation unless --yes.
#   -h, --help      Show this help.

set -euo pipefail

SERVICE_NAME="winterflow"
BINARY_DEST="/usr/local/bin/winterflow"
CONFIG_DIR="/etc/winterflow"
ENV_FILE="${CONFIG_DIR}/winterflow.env"
DATA_DIR="/var/lib/winterflow"
UNIT_FILE="/etc/systemd/system/${SERVICE_NAME}.service"

SERVICE_USER="winterflow"
API_PORT="8080"
BINARY_SRC=""
ASSUME_YES=0
UNINSTALL=0
PURGE=0

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

usage() { sed -n '2,29p' "$0" | sed 's/^# \{0,1\}//'; }

confirm() {
    # confirm "question" -> 0 on yes. Defaults to yes on empty input.
    local prompt="$1"
    if [ "$ASSUME_YES" -eq 1 ]; then
        return 0
    fi
    local answer
    read -r -p "$prompt [Y/n] " answer
    case "$answer" in
        [nN]|[nN][oO]) return 1 ;;
        *) return 0 ;;
    esac
}

while [ $# -gt 0 ]; do
    case "$1" in
        --binary)    BINARY_SRC="${2:?--binary needs a path}"; shift 2 ;;
        --user)      SERVICE_USER="${2:?--user needs a name}"; shift 2 ;;
        --port)      API_PORT="${2:?--port needs a value}"; shift 2 ;;
        --yes)       ASSUME_YES=1; shift ;;
        --uninstall) UNINSTALL=1; shift ;;
        --purge)     UNINSTALL=1; PURGE=1; shift ;;
        -h|--help)   usage; exit 0 ;;
        *) die "unknown option: $1 (see --help)" ;;
    esac
done

[ "$(id -u)" -eq 0 ] || die "must run as root (try: sudo $0)"
command -v systemctl >/dev/null 2>&1 || die "systemd is required (systemctl not found)"

if [ "$UNINSTALL" -eq 1 ]; then
    if [ "$PURGE" -eq 1 ]; then
        echo "This will PERMANENTLY delete ${CONFIG_DIR}, ${DATA_DIR} (database,"
        echo "certs, deployed app repos), and the '${SERVICE_USER}' user."
        echo "Running app containers are NOT stopped; stop them first if needed."
        confirm "Purge everything?" || die "aborted"
    fi
    log "Uninstalling ${SERVICE_NAME} service"
    systemctl disable --now "$SERVICE_NAME" 2>/dev/null || true
    rm -f "$UNIT_FILE" "$BINARY_DEST"
    systemctl daemon-reload
    log "Removed service and binary."
    if [ "$PURGE" -eq 1 ]; then
        rm -rf "$CONFIG_DIR" "$DATA_DIR"
        if id "$SERVICE_USER" >/dev/null 2>&1; then
            userdel "$SERVICE_USER" 2>/dev/null || warn "could not delete user '${SERVICE_USER}'; remove it manually"
        fi
        log "Purged config, data, and the '${SERVICE_USER}' user."
    else
        log "Kept: ${CONFIG_DIR}, ${DATA_DIR}, and the '${SERVICE_USER}' user."
        log "To remove them too, rerun with --purge."
    fi
    exit 0
fi

# --- Preflight ----------------------------------------------------------------

command -v docker >/dev/null 2>&1 \
    || die "docker is required (the agent deploys apps via the docker compose CLI)"
docker compose version >/dev/null 2>&1 \
    || die "the 'docker compose' plugin is required (docker compose version failed)"

# Locate or build the binary.
if [ -z "$BINARY_SRC" ]; then
    repo_root="$(cd "$(dirname "$0")/.." && pwd)"
    if [ -x "${repo_root}/bin/standalone" ]; then
        BINARY_SRC="${repo_root}/bin/standalone"
        log "Using prebuilt binary ${BINARY_SRC}"
    elif [ -f "${repo_root}/go.mod" ] && command -v go >/dev/null 2>&1; then
        log "Building standalone binary from ${repo_root}"
        (cd "$repo_root" && go build -o bin/standalone ./cmd/standalone)
        BINARY_SRC="${repo_root}/bin/standalone"
    else
        die "no binary found; pass --binary PATH or run from the repo with Go installed"
    fi
fi
[ -x "$BINARY_SRC" ] || die "binary is not executable: ${BINARY_SRC}"

# --- Dedicated service user ---------------------------------------------------

if id "$SERVICE_USER" >/dev/null 2>&1; then
    log "User '${SERVICE_USER}' already exists, reusing it"
else
    echo
    echo "WinterFlow should run as a dedicated unprivileged system user"
    echo "('${SERVICE_USER}') instead of root, so a compromise of the service"
    echo "does not directly compromise the host."
    if confirm "Create dedicated system user '${SERVICE_USER}'?"; then
        useradd --system --user-group \
            --home-dir "$DATA_DIR" --no-create-home \
            --shell /usr/sbin/nologin "$SERVICE_USER"
        log "Created system user '${SERVICE_USER}'"
    else
        die "refusing to install without a dedicated user; rerun with --user NAME to use an existing account"
    fi
fi

# The agent drives docker compose, which needs access to the Docker socket.
if getent group docker >/dev/null 2>&1 && ! id -nG "$SERVICE_USER" | tr ' ' '\n' | grep -qx docker; then
    echo
    echo "The service deploys apps via 'docker compose', which requires access"
    echo "to the Docker socket. Note: docker group membership is effectively"
    echo "root-equivalent on the host — this is inherent to driving Docker."
    if confirm "Add '${SERVICE_USER}' to the 'docker' group?"; then
        usermod -aG docker "$SERVICE_USER"
        log "Added '${SERVICE_USER}' to the docker group"
    else
        warn "skipping docker group; app deployments will fail until '${SERVICE_USER}' can reach the Docker socket"
    fi
fi

# --- Filesystem layout --------------------------------------------------------

log "Creating ${DATA_DIR} and ${CONFIG_DIR}"
install -d -m 750 -o "$SERVICE_USER" -g "$SERVICE_USER" "$DATA_DIR"
install -d -m 750 -o root -g "$SERVICE_USER" "$CONFIG_DIR"

log "Installing binary to ${BINARY_DEST}"
install -m 755 -o root -g root "$BINARY_SRC" "$BINARY_DEST"

# --- Config -------------------------------------------------------------------

if [ -f "$ENV_FILE" ]; then
    log "Keeping existing config ${ENV_FILE}"
else
    log "Writing ${ENV_FILE}"
    jwt_secret="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
    cat > "$ENV_FILE" <<EOF
# WinterFlow standalone configuration.
# Loaded by systemd (EnvironmentFile); see .env.dist in the repo for all options.

LOG_LEVEL=info
API_PORT=${API_PORT}
WEB_URL=http://localhost:${API_PORT}
API_URL=http://localhost:${API_PORT}
REGION=local

# Generated at install time. Rotating it invalidates all sessions.
JWT_SECRET=${jwt_secret}

DATABASE_URL=sqlite://${DATA_DIR}/winterflow.sqlite
AGENT_DATA_DIR=${DATA_DIR}/data
AVATARS_STORAGE_PATH=${DATA_DIR}/data/avatars

HUB_CERT_DIR=${DATA_DIR}/data/hub-certs
HUB_CERT_EXT_PATH=${DATA_DIR}/data/ext.cnf
HUB_CA_SUBJECT="/C=CA/O=WinterFlow.io/OU=CA/CN=WinterFlow.io CA/emailAddress=info@winterflow.io"
HUB_SERVER_SUBJECT="/C=CA/O=WinterFlow.io/OU=SERVER/CN=WinterFlow.io/emailAddress=info@winterflow.io"

# Embedded ingress (Caddy). 80/443 work via CAP_NET_BIND_SERVICE in the unit.
#INGRESS_HTTP_PORT=80
#INGRESS_HTTPS_PORT=443
#INGRESS_ACME_EMAIL=you@example.com
EOF
    chown root:"$SERVICE_USER" "$ENV_FILE"
    chmod 640 "$ENV_FILE"
fi

# --- systemd unit -------------------------------------------------------------

log "Writing ${UNIT_FILE}"
cat > "$UNIT_FILE" <<EOF
[Unit]
Description=WinterFlow standalone (API + agent + orchestrator)
Documentation=https://github.com/winterflowio
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
Type=simple
User=${SERVICE_USER}
Group=${SERVICE_USER}
EnvironmentFile=${ENV_FILE}
WorkingDirectory=${DATA_DIR}
ExecStart=${BINARY_DEST}
Restart=on-failure
RestartSec=5

# Allow binding the ingress ports 80/443 without root.
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE

# Hardening. The Docker socket stays writable: app deployment shells out to
# 'docker compose'.
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=${DATA_DIR}
ReadWritePaths=-/run/docker.sock
ReadWritePaths=-/var/run/docker.sock
ProtectHome=true
PrivateTmp=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true

[Install]
WantedBy=multi-user.target
EOF

# --- Enable + start -----------------------------------------------------------

log "Enabling and starting ${SERVICE_NAME}.service"
systemctl daemon-reload
systemctl enable --now "$SERVICE_NAME"

sleep 2
if systemctl is-active --quiet "$SERVICE_NAME"; then
    log "Service is running."
else
    warn "service did not come up; inspect with: journalctl -u ${SERVICE_NAME} -e"
    exit 1
fi

echo
log "Done. Useful commands:"
echo "  systemctl status ${SERVICE_NAME}"
echo "  journalctl -u ${SERVICE_NAME} -f"
echo "  API: http://localhost:${API_PORT}  (config: ${ENV_FILE}, data: ${DATA_DIR})"
echo
echo "The 'winterflow' CLI is on PATH and auto-loads ${ENV_FILE}."
echo "Run commands that touch the data dir as the service user:"
echo "  sudo -u ${SERVICE_USER} winterflow <command>"
echo
echo "First run: register the admin account via the web UI /register page."
echo "Note: the standalone binary serves the API only — build the web UI"
echo "(pnpm --dir web run build) and host it, or front both with the ingress."
