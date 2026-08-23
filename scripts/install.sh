#!/usr/bin/env bash
#
# Install the WinterFlow standalone binary as a systemd service.
#
# The standalone binary runs the HTTP API, the embedded web UI, the agent,
# and the Docker Compose orchestrator in one process over SQLite. This script:
#   - offers to create a dedicated system user (recommended) so the service
#     does not run as root,
#   - installs the binary to /usr/local/bin/winterflow,
#   - writes /etc/winterflow/winterflow.env with a generated JWT secret,
#   - creates /var/lib/winterflow for the database, certs, and app data,
#   - installs a hardened systemd unit and starts it.
#
# Logs go to a dedicated journald namespace ("winterflow") capped in size and
# age, so they rotate automatically and can be deleted without touching the
# rest of the system journal:
#   view:    journalctl --namespace winterflow -u winterflow -f
#   delete:  journalctl --namespace winterflow --vacuum-time=1s
#
# Usage:
#   sudo ./scripts/install.sh [options]
#
# Options:
#   --binary PATH    Prebuilt standalone binary to install. Default: build
#                    ./cmd/standalone from the repo this script lives in.
#   --user NAME      Service user name (default: winterflow).
#   --port PORT      API port (default: 8080).
#   --cpu-quota PCT  CPU cap for the service in percent of one core (default:
#                    (cores-1)*100, min 100). Keeps orchestration work from
#                    saturating the box; app containers are not affected.
#   --memory-max SZ  Hard memory cap for the service (default: half of RAM,
#                    clamped to 512M..2G).
#   --yes            Non-interactive: assume "yes" to all prompts.
#   --dry-run        Print the unit/config that would be written and exit
#                    without changing anything. Does not require root.
#   --uninstall      Stop and remove the service, binary, and unit file.
#                    Config and data are kept; remove them manually.
#   --purge          Like --uninstall, but also delete the config, all data
#                    (database, certs, deployed app repos), logs, and the
#                    service user. Irreversible; asks unless --yes.
#   -h, --help       Show this help.

set -euo pipefail

SERVICE_NAME="winterflow"
BINARY_DEST="/usr/local/bin/winterflow"
CONFIG_DIR="/etc/winterflow"
ENV_FILE="${CONFIG_DIR}/winterflow.env"
DATA_DIR="/var/lib/winterflow"
UNIT_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
JOURNAL_NAMESPACE="winterflow"
JOURNAL_CONF="/etc/systemd/journald@${JOURNAL_NAMESPACE}.conf"

SERVICE_USER="winterflow"
API_PORT="8080"
CPU_QUOTA=""
MEMORY_MAX=""
BINARY_SRC=""
ASSUME_YES=0
DRY_RUN=0
UNINSTALL=0
PURGE=0

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

# Print the header comment block (everything up to the first non-comment line).
usage() { awk 'NR==1{next} !/^#/{exit} {sub(/^# ?/,""); print}' "$0"; }

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
        --binary)     BINARY_SRC="${2:?--binary needs a path}"; shift 2 ;;
        --user)       SERVICE_USER="${2:?--user needs a name}"; shift 2 ;;
        --port)       API_PORT="${2:?--port needs a value}"; shift 2 ;;
        --cpu-quota)  CPU_QUOTA="${2:?--cpu-quota needs a value}"; shift 2 ;;
        --memory-max) MEMORY_MAX="${2:?--memory-max needs a value}"; shift 2 ;;
        --yes)        ASSUME_YES=1; shift ;;
        --dry-run)    DRY_RUN=1; shift ;;
        --uninstall)  UNINSTALL=1; shift ;;
        --purge)      UNINSTALL=1; PURGE=1; shift ;;
        -h|--help)    usage; exit 0 ;;
        *) die "unknown option: $1 (see --help)" ;;
    esac
done

case "$API_PORT" in
    ''|*[!0-9]*) die "--port must be a number, got: ${API_PORT}" ;;
esac
[ "$API_PORT" -ge 1 ] && [ "$API_PORT" -le 65535 ] || die "--port out of range: ${API_PORT}"

# Default CPU quota: leave one core of headroom so orchestration bursts
# (docker compose, git) cannot pin every core and spike temperatures.
if [ -z "$CPU_QUOTA" ]; then
    cores="$(nproc 2>/dev/null || echo 2)"
    CPU_QUOTA=$(( (cores - 1) * 100 ))
    [ "$CPU_QUOTA" -ge 100 ] || CPU_QUOTA=100
fi
case "$CPU_QUOTA" in
    ''|*[!0-9]*) die "--cpu-quota must be a number (percent), got: ${CPU_QUOTA}" ;;
esac

# Default memory cap: half of RAM, clamped to 512M..2G — sized to the actual
# box, from Raspberry Pi-class boards up, instead of assuming a roomy host.
if [ -z "$MEMORY_MAX" ]; then
    mem_kb="$(awk '/^MemTotal:/{print $2}' /proc/meminfo 2>/dev/null || echo 0)"
    if [ "${mem_kb:-0}" -gt 0 ] 2>/dev/null; then
        half_mb=$(( mem_kb / 2048 ))
        [ "$half_mb" -ge 512 ]  || half_mb=512
        [ "$half_mb" -le 2048 ] || half_mb=2048
        MEMORY_MAX="${half_mb}M"
    else
        MEMORY_MAX="1G"
    fi
fi

if [ "$DRY_RUN" -eq 0 ]; then
    [ "$(id -u)" -eq 0 ] || die "must run as root (try: sudo $0)"
    command -v systemctl >/dev/null 2>&1 || die "systemd is required (systemctl not found)"
fi

if [ "$UNINSTALL" -eq 1 ]; then
    if [ "$PURGE" -eq 1 ]; then
        echo "This will PERMANENTLY delete ${CONFIG_DIR}, ${DATA_DIR} (database,"
        echo "certs, deployed app repos), all winterflow logs, and the"
        echo "'${SERVICE_USER}' user."
        echo "Running app containers are NOT stopped; stop them first if needed."
        confirm "Purge everything?" || die "aborted"
    fi
    log "Uninstalling ${SERVICE_NAME} service"
    systemctl disable --now "$SERVICE_NAME" 2>/dev/null || true
    rm -f "$UNIT_FILE" "$BINARY_DEST"
    systemctl daemon-reload
    log "Removed service and binary."
    if [ "$PURGE" -eq 1 ]; then
        # Drop the dedicated journal namespace: config, its journald instance,
        # and the on-disk journal files.
        rm -f "$JOURNAL_CONF"
        systemctl stop "systemd-journald@${JOURNAL_NAMESPACE}.service" 2>/dev/null || true
        rm -rf /var/log/journal/*."${JOURNAL_NAMESPACE}" /run/log/journal/*."${JOURNAL_NAMESPACE}"
        rm -rf "$CONFIG_DIR" "$DATA_DIR"
        if id "$SERVICE_USER" >/dev/null 2>&1; then
            userdel "$SERVICE_USER" 2>/dev/null || warn "could not delete user '${SERVICE_USER}'; remove it manually"
        fi
        log "Purged config, data, logs, and the '${SERVICE_USER}' user."
    else
        log "Kept: ${CONFIG_DIR}, ${DATA_DIR}, logs, and the '${SERVICE_USER}' user."
        log "To remove them too, rerun with --purge."
    fi
    exit 0
fi

# --- Renderers (shared by install and --dry-run) ------------------------------

# Journald namespaces (LogNamespace=) need systemd >= 245. Older systems fall
# back to the shared system journal (still rotated by global journald policy).
HAS_LOG_NAMESPACE=0
sysd_ver="$(systemctl --version 2>/dev/null | awk 'NR==1{print $2; exit}')"
if [ -n "${sysd_ver:-}" ] && [ "$sysd_ver" -ge 245 ] 2>/dev/null; then
    HAS_LOG_NAMESPACE=1
fi

render_env_file() {
    local jwt_secret="$1"
    cat <<EOF
# WinterFlow standalone configuration.
# Loaded by systemd (EnvironmentFile); see .env.example in the repo for all options.

LOG_LEVEL=info
API_PORT=${API_PORT}
WEB_URL=http://localhost:${API_PORT}
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

# Ingress throttling (per client IP; 0 disables). Defaults shown — a request
# ceiling so traffic spikes answer 429 instead of overheating the machine.
#INGRESS_RATE_LIMIT_RPS=50
#INGRESS_RATE_LIMIT_BURST=100
EOF
}

render_journald_conf() {
    cat <<EOF
# Rotation policy for the '${JOURNAL_NAMESPACE}' journal namespace
# (written by install.sh; applies only to winterflow logs, never the
# system journal).
#
# View:    journalctl --namespace ${JOURNAL_NAMESPACE} -u ${SERVICE_NAME} -f
# Delete:  journalctl --namespace ${JOURNAL_NAMESPACE} --vacuum-time=1s
[Journal]
Storage=persistent
SystemMaxUse=200M
SystemMaxFileSize=20M
MaxRetentionSec=1month
EOF
}

render_unit() {
    local log_ns_line=""
    if [ "$HAS_LOG_NAMESPACE" -eq 1 ]; then
        log_ns_line="LogNamespace=${JOURNAL_NAMESPACE}"
    fi
    cat <<EOF
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

# Logging: dedicated journald namespace with size/age caps (rotation policy
# in ${JOURNAL_CONF}), plus a rate limit so a
# runaway log loop cannot flood the journal.
${log_ns_line}
LogRateLimitIntervalSec=30s
LogRateLimitBurst=10000

# Resource limits: keep orchestration bursts (docker compose, git) from
# pinning every core or eating the host's memory — thermal safety on small
# machines. App containers run under dockerd and are NOT limited by these.
CPUQuota=${CPU_QUOTA}%
CPUWeight=90
MemoryMax=${MEMORY_MAX}
TasksMax=512
LimitNOFILE=65536

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
}

# --- Dry run ------------------------------------------------------------------

if [ "$DRY_RUN" -eq 1 ]; then
    log "Dry run: printing generated files, changing nothing."
    echo
    echo "### ${ENV_FILE}"
    render_env_file "<generated-at-install-time>"
    if [ "$HAS_LOG_NAMESPACE" -eq 1 ]; then
        echo
        echo "### ${JOURNAL_CONF}"
        render_journald_conf
    else
        warn "systemd ${sysd_ver:-unknown} < 245: no journal namespace; logs go to the shared system journal"
    fi
    echo
    echo "### ${UNIT_FILE}"
    render_unit
    exit 0
fi

# --- Preflight ----------------------------------------------------------------

command -v docker >/dev/null 2>&1 \
    || die "docker is required (the agent deploys apps via the docker compose CLI)"
docker compose version >/dev/null 2>&1 \
    || die "the 'docker compose' plugin is required (docker compose version failed)"

# Containers log via the json-file driver, which does NOT rotate by default:
# one chatty app can fill the SSD. Offer daemon-wide rotation if unset.
DOCKER_DAEMON_JSON="/etc/docker/daemon.json"
if [ ! -f "$DOCKER_DAEMON_JSON" ]; then
    echo
    echo "Docker's default json-file log driver never rotates container logs."
    echo "Recommended: cap them at 10MB x 3 files per container via"
    echo "${DOCKER_DAEMON_JSON} (applies to containers created afterwards)."
    if confirm "Write ${DOCKER_DAEMON_JSON} with log rotation and restart docker?"; then
        install -d -m 755 /etc/docker
        cat > "$DOCKER_DAEMON_JSON" <<'EOF'
{
  "log-driver": "json-file",
  "log-opts": { "max-size": "10m", "max-file": "3" }
}
EOF
        systemctl restart docker || warn "docker restart failed; restart it manually to apply log rotation"
        log "Docker container log rotation configured"
    else
        warn "skipping: container logs will grow unbounded until you configure log-opts"
    fi
elif ! grep -q 'log-opts' "$DOCKER_DAEMON_JSON"; then
    warn "${DOCKER_DAEMON_JSON} has no log-opts: container logs grow unbounded."
    warn "add: \"log-driver\": \"json-file\", \"log-opts\": {\"max-size\": \"10m\", \"max-file\": \"3\"}"
fi

# Locate or build the binary.
if [ -z "$BINARY_SRC" ]; then
    repo_root="$(cd "$(dirname "$0")/.." && pwd)"
    if [ -x "${repo_root}/bin/standalone" ]; then
        BINARY_SRC="${repo_root}/bin/standalone"
        log "Using prebuilt binary ${BINARY_SRC}"
    elif [ -f "${repo_root}/go.mod" ] && command -v go >/dev/null 2>&1; then
        # Bundle the web UI first so go:embed ships it inside the binary.
        # Without a bundle the binary still works, but serves the API only.
        if command -v pnpm >/dev/null 2>&1; then
            log "Building web UI bundle (embedded into the binary)"
            (cd "$repo_root" && pnpm --dir web install --frozen-lockfile && pnpm --dir web run build)
        elif [ -f "${repo_root}/web/dist/index.html" ]; then
            log "pnpm not found; embedding the existing web/dist bundle"
        else
            warn "pnpm not found and web/dist has no build: the binary will serve the API only."
            warn "install node+pnpm and rerun, or copy a built web/dist here first"
        fi
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
    render_env_file "$jwt_secret" > "$ENV_FILE"
    chown root:"$SERVICE_USER" "$ENV_FILE"
    chmod 640 "$ENV_FILE"
fi

# --- Log rotation (journald namespace) ----------------------------------------

if [ "$HAS_LOG_NAMESPACE" -eq 1 ]; then
    if [ -f "$JOURNAL_CONF" ]; then
        log "Keeping existing journal rotation policy ${JOURNAL_CONF}"
    else
        log "Writing ${JOURNAL_CONF} (log rotation: 200M cap, 1 month retention)"
        render_journald_conf > "$JOURNAL_CONF"
    fi
    # Pick up policy changes if the namespaced journald is already running.
    systemctl try-restart "systemd-journald@${JOURNAL_NAMESPACE}.service" 2>/dev/null || true
else
    warn "systemd ${sysd_ver:-unknown} < 245: journal namespaces unavailable;"
    warn "logs go to the shared system journal (rotated by global journald policy)"
fi

# --- systemd unit -------------------------------------------------------------

log "Writing ${UNIT_FILE}"
render_unit > "$UNIT_FILE"

# --- Enable + start -----------------------------------------------------------

log "Enabling and starting ${SERVICE_NAME}.service"
systemctl daemon-reload
systemctl enable --now "$SERVICE_NAME"

journal_flags=""
if [ "$HAS_LOG_NAMESPACE" -eq 1 ]; then
    journal_flags="--namespace ${JOURNAL_NAMESPACE} "
fi

sleep 2
if systemctl is-active --quiet "$SERVICE_NAME"; then
    log "Service is running."
else
    warn "service did not come up; inspect with: journalctl ${journal_flags}-u ${SERVICE_NAME} -e"
    exit 1
fi

echo
log "Done. Useful commands:"
echo "  systemctl status ${SERVICE_NAME}"
echo "  journalctl ${journal_flags}-u ${SERVICE_NAME} -f     # follow logs"
if [ "$HAS_LOG_NAMESPACE" -eq 1 ]; then
    echo "  journalctl --namespace ${JOURNAL_NAMESPACE} --vacuum-time=1s   # delete all winterflow logs"
    echo "  (rotation policy: ${JOURNAL_CONF})"
fi
echo "  API: http://localhost:${API_PORT}  (config: ${ENV_FILE}, data: ${DATA_DIR})"
echo
echo "The 'winterflow' CLI is on PATH and auto-loads ${ENV_FILE}."
echo "Run commands that touch the data dir as the service user:"
echo "  sudo -u ${SERVICE_USER} winterflow <command>"
echo
echo "First run: open http://localhost:${API_PORT}/register and create the"
echo "admin account. The binary serves the web UI itself when it was built"
echo "with a web/dist bundle present (a prebuilt release always is; a source"
echo "build needs pnpm — this script bundles it automatically when found)."
