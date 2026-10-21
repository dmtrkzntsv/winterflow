#!/usr/bin/env bash
#
# Install the WinterFlow agent as a systemd service and enroll it.
#
# The agent is the machine-side half of a WinterFlow control plane that runs
# elsewhere (the hosted one at https://app.winterflow.dev, or your own). It
# connects out to a hub over mTLS and deploys apps with 'docker compose'. This
# script:
#   - downloads the latest prebuilt release for this machine (or builds from
#     source when run inside a repo checkout with Go installed),
#   - offers to install Docker and the compose plugin when they are missing,
#   - installs the binary to /usr/local/bin/winterflow-agent,
#   - asks which unprivileged user the service should run as (a dedicated
#     system user by default),
#   - installs a hardened systemd unit (not started yet),
#   - enrolls the machine: prints a short code, you enter it in the web UI
#     (Servers -> Add server), and the signed certificate is stored in
#     /var/lib/winterflow/certs, readable only by the service user,
#   - writes /etc/winterflow/agent.env and enables + starts the service.
#
# Logs go to a dedicated journald namespace ("winterflow") capped in size and
# age:
#   view:    journalctl --namespace winterflow -u winterflow-agent -f
#   delete:  journalctl --namespace winterflow --vacuum-time=1s
#
# Usage:
#   # Piped: NON-INTERACTIVE with sane defaults (sudo cannot forward the
#   # keyboard to a piped script). The enrollment code is still printed and
#   # polled for without a terminal. Pass options to customize:
#   curl -fsSL https://raw.githubusercontent.com/dmtrkzntsv/winterflow/main/scripts/install-agent.sh | sudo bash
#   curl -fsSL https://raw.githubusercontent.com/dmtrkzntsv/winterflow/main/scripts/install-agent.sh | sudo bash -s -- --url https://api.example.com
#
#   # Interactive (asks the API URL + service user): download first, then run.
#   curl -fsSLO https://raw.githubusercontent.com/dmtrkzntsv/winterflow/main/scripts/install-agent.sh
#   sudo bash install-agent.sh
#
# Rerunning refreshes the binary and the unit and restarts the service; an
# already enrolled machine keeps its certificate and is not enrolled again.
#
# Options:
#   --url URL        Control plane API to enroll with (default:
#                    https://api.winterflow.dev). Asked interactively with the
#                    default pre-filled; piped runs use it silently.
#   --binary PATH    Prebuilt agent binary to install. Default: build
#                    ./cmd/agent when run from a repo checkout, otherwise
#                    download the latest GitHub release for this OS/arch.
#   --version TAG    Release to download instead of the latest, e.g. v260823.1
#                    (a bare 260823.1 works too). Ignored with --binary or a
#                    repo build.
#   --user NAME      Run the service as NAME instead of asking. Created as a
#                    system user if it does not exist. Must not be root.
#   --cpu-quota PCT  CPU cap for the service in percent of one core (default:
#                    (cores-1)*100, min 100). App containers are not affected.
#   --memory-max SZ  Hard memory cap for the service (default: half of RAM,
#                    clamped to 512M..2G).
#   --yes            Non-interactive: assume "yes" to all prompts (including
#                    installing Docker if missing) and keep the default service
#                    user (winterflow) and URL unless --user/--url say otherwise.
#   --dry-run        Print the unit/config and the enrollment plan that would
#                    be used and exit without changing anything. Does not
#                    require root.
#   --uninstall      Stop and remove the service, binary, and unit file.
#                    Config, certificate and data are kept.
#   --purge          Like --uninstall, but also delete the agent config,
#                    certificate, data, and the service user (unless it is a
#                    real login account, which is kept). Irreversible; asks
#                    unless --yes. The server must be removed in the web UI.
#   -h, --help       Show this help.

set -euo pipefail

# WF_INSTALL_ROOT prefixes only the "already enrolled?" lookup, so tests can
# fake an existing certificate without root. Unset in real use.
INSTALL_ROOT="${WF_INSTALL_ROOT:-}"

SERVICE_NAME="winterflow-agent"
BINARY_DEST="/usr/local/bin/winterflow-agent"
CONFIG_DIR="/etc/winterflow"
ENV_FILE="${CONFIG_DIR}/agent.env"
DATA_DIR="/var/lib/winterflow"
CERT_DIR="${DATA_DIR}/certs"
UNIT_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
JOURNAL_NAMESPACE="winterflow"
JOURNAL_CONF="/etc/systemd/journald@${JOURNAL_NAMESPACE}.conf"
DOCKER_INSTALL_URL="https://get.docker.com"
GITHUB_REPO="dmtrkzntsv/winterflow"
DEFAULT_API_URL="https://api.winterflow.dev"
# Exit status the agent uses when the hub keeps refusing its certificate; the
# unit must not restart it (RestartPreventExitStatus).
AGENT_DENIED_EXIT=78

SERVICE_USER="winterflow"
SERVICE_GROUP="winterflow"
USER_EXPLICIT=0
API_URL="$DEFAULT_API_URL"
URL_EXPLICIT=0
CPU_QUOTA=""
MEMORY_MAX=""
BINARY_SRC=""
RELEASE_VERSION=""
ASSUME_YES=0
DRY_RUN=0
UNINSTALL=0
PURGE=0

HAS_LOG_NAMESPACE=0
sysd_ver=""

# Temp dir for a downloaded release binary; cleaned up on exit.
WORK_DIR=""
trap '[ -n "$WORK_DIR" ] && rm -rf "$WORK_DIR"' EXIT

# --- shared: log ---
log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }
# --- end shared ---

usage() {
    # Print the header comment block (everything up to the first non-comment
    # line). When piped there is no script file to read; point at the repo.
    if [ -f "$0" ]; then
        awk 'NR==1{next} !/^#/{exit} {sub(/^# ?/,""); print}' "$0"
    else
        echo "WinterFlow agent installer."
        echo "Options: https://github.com/${GITHUB_REPO}/blob/main/scripts/install-agent.sh"
    fi
}

# --- shared: prompts ---
prompt_read() {
    # prompt_read "prompt" -> sets REPLY. Only reads when stdin is a real
    # terminal. Piped runs (curl | sudo bash) get NO prompts: sudo's use_pty
    # (default on modern distros) relays the pipe, not the keyboard, so a
    # /dev/tty read would hang with no way to type OR Ctrl-C out. Callers
    # fall back to their defaults instead.
    REPLY=""
    if [ -t 0 ]; then
        read -r -p "$1" REPLY
    else
        return 1
    fi
}

confirm() {
    # confirm "question" -> 0 on yes. Yes on empty input, --yes, or when no
    # terminal is available to ask (non-interactive install).
    if [ "$ASSUME_YES" -eq 1 ]; then
        return 0
    fi
    local REPLY
    prompt_read "$1 [Y/n] " || REPLY=""
    case "$REPLY" in
        [nN]|[nN][oO]) return 1 ;;
        *) return 0 ;;
    esac
}

ask() {
    # ask "question" "default" -> echo the answer. The default on empty input,
    # --yes, or no terminal. The prompt goes to the terminal, not stdout, so
    # this is safe inside $(...).
    local prompt="$1" default="$2" REPLY
    if [ "$ASSUME_YES" -eq 1 ]; then
        printf '%s' "$default"
        return 0
    fi
    prompt_read "${prompt} [${default}] " || REPLY=""
    printf '%s' "${REPLY:-$default}"
}
# --- end shared ---

# --- shared: users ---
is_valid_user_name() {
    # useradd's portable name rule, minus root: a typo (or a uid-0 account)
    # must never land as "User=" in the unit.
    [ "$1" != root ] || return 1
    printf '%s' "$1" | grep -qE '^[a-z_][a-z0-9_-]*$' || return 1
    ! id -u "$1" >/dev/null 2>&1 || [ "$(id -u "$1")" -ne 0 ]
}

validate_user_name() {
    is_valid_user_name "$1" || die "invalid service user '$1': use lowercase\
 letters, digits, '_' and '-', and not root (uid 0)"
}

# The primary group of an existing account is not always <name> (user-private
# groups are a convention, not a rule), so resolve it instead of assuming.
resolve_group() {
    if id "$SERVICE_USER" >/dev/null 2>&1; then
        SERVICE_GROUP="$(id -gn "$SERVICE_USER")"
    else
        SERVICE_GROUP="$SERVICE_USER"
    fi
}

ensure_service_user() {
    # Create the service user when missing (behind a confirm) and offer the
    # docker group: the service drives docker compose through the socket.
    if id "$SERVICE_USER" >/dev/null 2>&1; then
        log "User '${SERVICE_USER}' already exists, running the service as it"
    else
        echo
        echo "'${SERVICE_USER}' does not exist yet. It will be created as an"
        echo "unprivileged system user (no login shell, home ${DATA_DIR})."
        if confirm "Create system user '${SERVICE_USER}'?"; then
            useradd --system --user-group \
                --home-dir "$DATA_DIR" --no-create-home \
                --shell /usr/sbin/nologin "$SERVICE_USER"
            log "Created system user '${SERVICE_USER}'"
        else
            die "refusing to install without a service user; rerun with --user NAME to use an existing account"
        fi
    fi
    resolve_group

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
}
# --- end shared ---

# --- shared: download ---
fetch_url() {
    # fetch_url URL DEST — HTTPS only; curl or wget, whichever is present.
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL --proto '=https' --tlsv1.2 -o "$2" "$1"
    elif command -v wget >/dev/null 2>&1; then
        wget -q --https-only -O "$2" "$1"
    else
        return 1
    fi
}

detect_platform() {
    # Sets RELEASE_OS and RELEASE_ARCH (arch as Go spells it). The release
    # asset naming built from them is a contract with .goreleaser.yaml and the
    # agent self-updater.
    RELEASE_OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
    [ "$RELEASE_OS" = "linux" ] || die "prebuilt binaries are linux-only (this is ${RELEASE_OS}); build from source (see --help)"
    case "$(uname -m)" in
        x86_64|amd64)  RELEASE_ARCH=amd64 ;;
        aarch64|arm64) RELEASE_ARCH=arm64 ;;
        armv6l|armv7l) RELEASE_ARCH=arm ;;
        *) die "no prebuilt binary for $(uname -m); build from source (see --help)" ;;
    esac
}

download_release() {
    # Fetch this script's binary (release_asset_name) and its checksum from
    # GitHub releases into WORK_DIR and point BINARY_SRC at it.
    local asset base
    asset="$(release_asset_name)"
    if [ -n "$RELEASE_VERSION" ]; then
        case "$RELEASE_VERSION" in v*) ;; *) RELEASE_VERSION="v${RELEASE_VERSION}" ;; esac
        base="https://github.com/${GITHUB_REPO}/releases/download/${RELEASE_VERSION}"
    else
        base="https://github.com/${GITHUB_REPO}/releases/latest/download"
    fi
    WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/winterflow-install.XXXXXX")"
    log "Downloading ${base}/${asset}"
    fetch_url "${base}/${asset}" "${WORK_DIR}/${asset}" \
        || die "download failed: ${base}/${asset} (no such release, or no network?)"
    fetch_url "${base}/checksums.txt" "${WORK_DIR}/checksums.txt" \
        || die "download failed: ${base}/checksums.txt"
    if command -v sha256sum >/dev/null 2>&1; then
        (cd "$WORK_DIR" && grep "[[:space:]]${asset}\$" checksums.txt | sha256sum -c --quiet -) \
            || die "checksum mismatch for ${asset}: corrupted or tampered download"
        log "Checksum verified"
    else
        warn "sha256sum not found; skipping checksum verification"
    fi
    chmod 755 "${WORK_DIR}/${asset}"
    BINARY_SRC="${WORK_DIR}/${asset}"
}
# --- end shared ---

# --- shared: docker ---
install_docker() {
    # Docker's official convenience script. It knows every distro layout we
    # care about (Debian/Ubuntu/Raspberry Pi OS, Fedora, CentOS, openSUSE),
    # wires up the package repo, and installs the compose plugin with it.
    local script status
    script="$(mktemp "${TMPDIR:-/tmp}/get-docker.XXXXXX")"
    log "Downloading ${DOCKER_INSTALL_URL}"
    if ! fetch_url "$DOCKER_INSTALL_URL" "$script"; then
        rm -f "$script"
        die "could not download ${DOCKER_INSTALL_URL} (curl or wget required)"
    fi
    log "Running Docker's installer (this can take a few minutes)"
    status=0
    sh "$script" || status=$?
    rm -f "$script"
    [ "$status" -eq 0 ] || die "Docker installation failed (exit ${status}); install it manually and rerun"
}

install_compose_plugin() {
    # Only reachable when Docker itself is already installed, so its package
    # repo is configured and the plugin is one package away.
    if command -v apt-get >/dev/null 2>&1; then
        apt-get update -qq && apt-get install -y docker-compose-plugin
    elif command -v dnf >/dev/null 2>&1; then
        dnf install -y docker-compose-plugin
    elif command -v yum >/dev/null 2>&1; then
        yum install -y docker-compose-plugin
    elif command -v zypper >/dev/null 2>&1; then
        zypper --non-interactive install docker-compose-plugin
    else
        return 1
    fi
}

ensure_docker() {
    # Docker is the runtime for every deployed app; offer to install it rather
    # than sending the user away mid-install.
    if ! command -v docker >/dev/null 2>&1; then
        echo
        echo "Docker is required: the agent deploys apps by shelling out to"
        echo "'docker compose', and it is not installed on this machine."
        echo "Docker's official install script (${DOCKER_INSTALL_URL}) sets up the"
        echo "package repo for this distro and installs the engine, containerd and"
        echo "the compose plugin. Review it first if you prefer:"
        echo "  curl -fsSL ${DOCKER_INSTALL_URL} | less"
        if confirm "Download and run Docker's official install script?"; then
            install_docker
        else
            die "docker is required (the agent deploys apps via the docker compose CLI)"
        fi
        command -v docker >/dev/null 2>&1 \
            || die "docker is still not on PATH after installation; install it manually and rerun"
        log "Docker installed"
    fi

    # A fresh install (or a stopped unit) leaves the daemon down; compose needs it.
    if ! docker info >/dev/null 2>&1; then
        log "Starting the docker service"
        systemctl enable --now docker >/dev/null 2>&1 || true
        docker info >/dev/null 2>&1 \
            || warn "the docker daemon is not responding; app deployments will fail until it is up"
    fi

    if ! docker compose version >/dev/null 2>&1; then
        echo
        echo "Docker is installed but the compose v2 plugin ('docker compose') is"
        echo "not, and every app is deployed with it."
        if confirm "Install the docker-compose-plugin package?"; then
            install_compose_plugin \
                || die "could not install the compose plugin automatically; install docker-compose-plugin and rerun"
            docker compose version >/dev/null 2>&1 \
                || die "the compose plugin is still missing after installation; install it manually and rerun"
            log "Docker compose plugin installed"
        else
            die "the 'docker compose' plugin is required (docker compose version failed)"
        fi
    fi

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
            cat > "$DOCKER_DAEMON_JSON" <<'JSON'
{
  "log-driver": "json-file",
  "log-opts": { "max-size": "10m", "max-file": "3" }
}
JSON
            systemctl restart docker || warn "docker restart failed; restart it manually to apply log rotation"
            log "Docker container log rotation configured"
        else
            warn "skipping: container logs will grow unbounded until you configure log-opts"
        fi
    elif ! grep -q 'log-opts' "$DOCKER_DAEMON_JSON"; then
        warn "${DOCKER_DAEMON_JSON} has no log-opts: container logs grow unbounded."
        warn "add: \"log-driver\": \"json-file\", \"log-opts\": {\"max-size\": \"10m\", \"max-file\": \"3\"}"
    fi
}
# --- end shared ---

# --- shared: limits ---
default_cpu_quota() {
    # Leave one core of headroom so orchestration bursts (docker compose,
    # git) cannot pin every core and spike temperatures.
    local cores quota
    cores="$(nproc 2>/dev/null || echo 2)"
    quota=$(( (cores - 1) * 100 ))
    [ "$quota" -ge 100 ] || quota=100
    printf '%s' "$quota"
}

default_memory_max() {
    # Half of RAM, clamped to 512M..2G — sized to the actual box, from
    # Raspberry Pi-class boards up, instead of assuming a roomy host.
    local mem_kb half_mb
    mem_kb="$(awk '/^MemTotal:/{print $2}' /proc/meminfo 2>/dev/null || echo 0)"
    if [ "${mem_kb:-0}" -gt 0 ] 2>/dev/null; then
        half_mb=$(( mem_kb / 2048 ))
        [ "$half_mb" -ge 512 ]  || half_mb=512
        [ "$half_mb" -le 2048 ] || half_mb=2048
        printf '%sM' "$half_mb"
    else
        printf '1G'
    fi
}
# --- end shared ---

release_asset_name() {
    # agent-{os}-{arch}: the agent GitHub release asset (installed as
    # winterflow-agent).
    detect_platform
    printf 'agent-%s-%s' "$RELEASE_OS" "$RELEASE_ARCH"
}

is_valid_api_url() {
    # https, or http to this machine (a local control plane). Anything else
    # would send the enrollment over a link anyone on the path can read.
    case "$1" in
        https://?*) ;;
        http://localhost|http://localhost[:/]*|http://127.0.0.1|http://127.0.0.1[:/]*|http://\[::1\]|http://\[::1\][:/]*) ;;
        *) return 1 ;;
    esac
    ! printf '%s' "$1" | grep -q '[[:space:]]'
}

agent_enrolled() {
    [ -f "${INSTALL_ROOT}${CERT_DIR}/agent.crt" ]
}

# --- Renderers (shared by install and --dry-run) ------------------------------

render_env_file() {
    cat <<EOF
# WinterFlow agent configuration.
# Loaded by systemd (EnvironmentFile); see .env.example in the repo for all options.

LOG_LEVEL=info
AGENT_DATA_DIR=${DATA_DIR}
HUB_CERT_DIR=${CERT_DIR}
EOF
}

render_journald_conf() {
    cat <<EOF
# Rotation policy for the '${JOURNAL_NAMESPACE}' journal namespace
# (written by install-agent.sh; applies only to winterflow logs, never the
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
Description=WinterFlow agent (hub client + orchestrator)
Documentation=https://github.com/${GITHUB_REPO}
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
Type=simple
User=${SERVICE_USER}
Group=${SERVICE_GROUP}
EnvironmentFile=${ENV_FILE}
WorkingDirectory=${DATA_DIR}
ExecStart=${BINARY_DEST}
Restart=on-failure
RestartSec=5
# The agent exits ${AGENT_DENIED_EXIT} when the hub keeps refusing its
# certificate (the server was removed): restarting would only hammer the hub.
RestartPreventExitStatus=${AGENT_DENIED_EXIT}

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
# 'docker compose'. /mnt and /media (the usual mount points of USB disks and
# NFS/CIFS shares) stay writable for local backup storages; any other folder
# needs a drop-in, see docs/recipes/backup-storage.md.
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=${DATA_DIR}
ReadWritePaths=-/run/docker.sock
ReadWritePaths=-/var/run/docker.sock
ReadWritePaths=-/mnt
ReadWritePaths=-/media
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

run_as_service_user() {
    # The key must be created by (and owned by) the service user, so the
    # enrollment runs as it, not as root.
    if command -v runuser >/dev/null 2>&1; then
        runuser -u "$SERVICE_USER" -- env HOME="$DATA_DIR" "$@"
    elif command -v setpriv >/dev/null 2>&1; then
        setpriv --reuid="$SERVICE_USER" --regid="$SERVICE_GROUP" --init-groups -- env HOME="$DATA_DIR" "$@"
    else
        die "runuser or setpriv (util-linux) is required to enroll as '${SERVICE_USER}'"
    fi
}

write_env_file() {
    # write_env_file ENROLL_ENV — agent.env = the base settings (kept when the
    # file exists) + the enrollment output, replacing earlier enrollment lines.
    # Owned by root, 0600: systemd reads EnvironmentFile as root, the agent
    # itself never opens it.
    local enroll_env="$1" tmp
    tmp="$(mktemp "${CONFIG_DIR}/.agent.env.XXXXXX")"
    if [ -f "$ENV_FILE" ]; then
        grep -vE '^(HUB_HOST|HUB_PORT|AGENT_ID|WINTERFLOW_API_URL)=' "$ENV_FILE" > "$tmp" || true
    else
        render_env_file > "$tmp"
    fi
    if [ -n "$enroll_env" ]; then
        cat "$enroll_env" >> "$tmp"
    fi
    chown root:root "$tmp"
    chmod 600 "$tmp"
    mv -f "$tmp" "$ENV_FILE"
}

enroll_agent() {
    # Runs `winterflow-agent enroll` as the service user. Its output (the code
    # and instructions) goes straight to the terminal: not captured, so it
    # shows live, piped or not. Exit codes: 0 enrolled, 2 gave up after 10
    # codes, 3 already enrolled, 1 anything else. Returns the same.
    local envout status=0
    envout="${CERT_DIR}/enroll.env"
    rm -f "$envout"
    echo
    log "Enrolling this machine with ${API_URL}"
    run_as_service_user "$BINARY_DEST" enroll \
        --url "$API_URL" --cert-dir "$CERT_DIR" --env-out "$envout" \
        --hostname "$(hostname 2>/dev/null || echo winterflow-agent)" </dev/null || status=$?
    case "$status" in
        0) write_env_file "$envout" ;;
    esac
    rm -f "$envout"
    return "$status"
}

main() {
    while [ $# -gt 0 ]; do
        case "$1" in
            --url)        API_URL="${2:?--url needs a value}"; URL_EXPLICIT=1; shift 2 ;;
            --binary)     BINARY_SRC="${2:?--binary needs a path}"; shift 2 ;;
            --version)    RELEASE_VERSION="${2:?--version needs a tag}"; shift 2 ;;
            --user)       SERVICE_USER="${2:?--user needs a name}"; USER_EXPLICIT=1; shift 2 ;;
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

    validate_user_name "$SERVICE_USER"
    resolve_group
    is_valid_api_url "$API_URL" || die "--url must be an https:// URL (http:// only for localhost), got: ${API_URL}"
    API_URL="${API_URL%/}"

    if [ -z "$CPU_QUOTA" ]; then
        CPU_QUOTA="$(default_cpu_quota)"
    fi
    case "$CPU_QUOTA" in
        ''|*[!0-9]*) die "--cpu-quota must be a number (percent), got: ${CPU_QUOTA}" ;;
    esac

    if [ -z "$MEMORY_MAX" ]; then
        MEMORY_MAX="$(default_memory_max)"
    fi

    if [ "$DRY_RUN" -eq 0 ]; then
        [ "$(id -u)" -eq 0 ] || die "must run as root (try: sudo $0)"
        command -v systemctl >/dev/null 2>&1 || die "systemd is required (systemctl not found)"
    fi

    if [ "$UNINSTALL" -eq 1 ]; then
        if [ "$PURGE" -eq 1 ]; then
            # Piped stdin means the confirm below cannot actually ask anyone —
            # never let an irreversible purge default to yes.
            if [ "$ASSUME_YES" -eq 0 ] && [ ! -t 0 ]; then
                die "refusing to purge without a terminal to confirm on; rerun with --yes to proceed"
            fi
            echo "This will PERMANENTLY delete ${ENV_FILE}, ${DATA_DIR} (the agent's"
            echo "certificate, deployed app repos, backups state) and the"
            echo "'${SERVICE_USER}' user, if it is a dedicated service account."
            echo "Running app containers are NOT stopped; stop them first if needed."
            echo "The server stays listed in the web UI until you remove it there."
            confirm "Purge everything?" || die "aborted"
        fi
        log "Uninstalling ${SERVICE_NAME} service"
        systemctl disable --now "$SERVICE_NAME" 2>/dev/null || true
        rm -f "$UNIT_FILE" "$BINARY_DEST"
        systemctl daemon-reload
        log "Removed service and binary."
        if [ "$PURGE" -eq 1 ]; then
            rm -f "$ENV_FILE"
            if [ -f "${CONFIG_DIR}/winterflow.env" ]; then
                # A standalone install shares these directories: leave its
                # config, data and user alone, remove only the agent's files.
                warn "${CONFIG_DIR}/winterflow.env exists (standalone install): keeping ${DATA_DIR} and the '${SERVICE_USER}' user, removing only ${CERT_DIR}"
                rm -rf "$CERT_DIR"
            else
                rmdir "$CONFIG_DIR" 2>/dev/null || true
                rm -rf "$DATA_DIR"
                if id "$SERVICE_USER" >/dev/null 2>&1; then
                    # Only reap a dedicated service account (see install.sh).
                    purge_uid="$(id -u "$SERVICE_USER")"
                    if [ "$SERVICE_USER" = "${SUDO_USER:-}" ] || [ "$purge_uid" -ge 1000 ]; then
                        warn "keeping user '${SERVICE_USER}' (uid ${purge_uid}): looks like a login account, not a dedicated service user"
                    else
                        userdel "$SERVICE_USER" 2>/dev/null || warn "could not delete user '${SERVICE_USER}'; remove it manually"
                    fi
                fi
            fi
            log "Purged agent config, certificate, and data."
        else
            log "Kept: ${ENV_FILE}, ${DATA_DIR} (certificate included), logs, and the '${SERVICE_USER}' user."
            log "To remove them too, rerun with --purge."
        fi
        exit 0
    fi

    # Journald namespaces (LogNamespace=) need systemd >= 245. Older systems fall
    # back to the shared system journal (still rotated by global journald policy).
    sysd_ver="$(systemctl --version 2>/dev/null | awk 'NR==1{print $2; exit}')"
    if [ -n "${sysd_ver:-}" ] && [ "$sysd_ver" -ge 245 ] 2>/dev/null; then
        HAS_LOG_NAMESPACE=1
    fi

    # --- Dry run --------------------------------------------------------------

    if [ "$DRY_RUN" -eq 1 ]; then
        log "Dry run: printing generated files, changing nothing."
        echo
        echo "### ${ENV_FILE}"
        render_env_file
        echo "# HUB_HOST, HUB_PORT, AGENT_ID and WINTERFLOW_API_URL are added by enrollment"
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
        echo
        echo "### download"
        if [ -n "$BINARY_SRC" ]; then
            echo "binary: ${BINARY_SRC}"
        else
            echo "asset: $(release_asset_name) (installed as ${BINARY_DEST})"
        fi
        echo
        echo "### enrollment"
        if agent_enrolled; then
            echo "already enrolled: ${CERT_DIR}/agent.crt exists, enrollment is skipped"
            echo "the binary and unit are refreshed and ${SERVICE_NAME} is restarted"
        else
            echo "winterflow-agent enroll --url ${API_URL} --cert-dir ${CERT_DIR} --env-out <temp file>"
            echo "run as '${SERVICE_USER}'; on success ${SERVICE_NAME} is enabled and started"
        fi
        exit 0
    fi

    # --- Piped install is non-interactive -------------------------------------

    # curl | sudo bash cannot prompt (see prompt_read); say what will happen
    # instead of silently choosing, and how to choose differently.
    if [ ! -t 0 ] && [ "$ASSUME_YES" -eq 0 ]; then
        echo
        log "Piped install (no terminal on stdin): using defaults, no questions."
        echo "    control plane ${API_URL}, service user '${SERVICE_USER}', Docker installed if missing"
        echo
        echo "    To customize:            ... | sudo bash -s -- --url https://api.example.com --user me"
        echo "    For interactive prompts: curl -fsSLO https://raw.githubusercontent.com/${GITHUB_REPO}/main/scripts/install-agent.sh"
        echo "                             sudo bash install-agent.sh"
        echo
        ASSUME_YES=1
    fi

    # --- Preflight ------------------------------------------------------------

    ensure_docker

    # Locate the binary: --binary PATH, a repo checkout around this script
    # (prebuilt bin/agent, or build it), or — the curl | bash path — download
    # a prebuilt release from GitHub.
    if [ -z "$BINARY_SRC" ] && [ -f "$0" ]; then
        repo_root="$(cd "$(dirname "$0")/.." && pwd)"
        if [ -x "${repo_root}/bin/agent" ]; then
            BINARY_SRC="${repo_root}/bin/agent"
            log "Using prebuilt binary ${BINARY_SRC}"
        elif [ -f "${repo_root}/go.mod" ] && [ -d "${repo_root}/cmd/agent" ] && command -v go >/dev/null 2>&1; then
            log "Building agent binary from ${repo_root}"
            # CGO off: the backup helper runs this binary inside alpine (no glibc).
            (cd "$repo_root" && CGO_ENABLED=0 go build -o bin/agent ./cmd/agent)
            BINARY_SRC="${repo_root}/bin/agent"
        fi
    fi
    if [ -z "$BINARY_SRC" ]; then
        download_release
    fi
    [ -x "$BINARY_SRC" ] || die "binary is not executable: ${BINARY_SRC}"

    # --- Control plane URL ----------------------------------------------------

    # --url (or --yes, or a piped run) skips the question.
    if [ "$URL_EXPLICIT" -eq 0 ] && [ "$ASSUME_YES" -eq 0 ]; then
        echo
        echo "Which WinterFlow control plane should this machine join? Press Enter"
        echo "for the hosted one, or type the URL of your own."
        while :; do
            answer="$(ask "API URL" "$API_URL")"
            if is_valid_api_url "$answer"; then
                API_URL="${answer%/}"
                break
            fi
            warn "'${answer}' is not an https:// URL (http:// only for localhost)"
        done
    fi

    # --- Service user ---------------------------------------------------------

    # The service must not run as root. Ask which account to use, defaulting to a
    # dedicated system user; the account that invoked sudo is offered as an
    # alternative. --user NAME (or --yes) skips the question.
    INVOKING_USER="${SUDO_USER:-}"
    if [ "$INVOKING_USER" = "root" ]; then
        INVOKING_USER=""
    fi

    if [ "$USER_EXPLICIT" -eq 0 ] && [ "$ASSUME_YES" -eq 0 ]; then
        echo
        echo "Which user should the WinterFlow agent run as? It must not be root,"
        echo "so a compromise of the service does not directly compromise the host."
        echo
        echo "  1) ${SERVICE_USER} - create a dedicated system user (recommended)"
        if [ -n "$INVOKING_USER" ]; then
            echo "  2) ${INVOKING_USER} - the account you ran sudo from"
            echo "  3) another name - type it below"
        else
            echo "  2) another name - type it below"
        fi
        echo
        default_user="$SERVICE_USER"
        while :; do
            answer="$(ask "Choice (number or user name)" "1")"
            case "$answer" in
                1) SERVICE_USER="$default_user"; break ;;
                2) if [ -n "$INVOKING_USER" ]; then
                       SERVICE_USER="$INVOKING_USER"
                   else
                       SERVICE_USER="$(ask "User name" "$default_user")"
                   fi
                   break ;;
                3) if [ -n "$INVOKING_USER" ]; then
                       SERVICE_USER="$(ask "User name" "$default_user")"
                       break
                   fi
                   warn "no such choice; pick a number from the list or type a user name"
                   ;;
                [0-9]*) warn "no such choice; pick a number from the list or type a user name" ;;
                *) if is_valid_user_name "$answer"; then
                       SERVICE_USER="$answer"
                       break
                   fi
                   warn "'${answer}' is not a usable user name (lowercase letters, digits, '_' and '-'; not root)"
                   ;;
            esac
        done
        validate_user_name "$SERVICE_USER"
    fi

    ensure_service_user

    # --- Filesystem layout ----------------------------------------------------

    log "Creating ${DATA_DIR}, ${CERT_DIR} and ${CONFIG_DIR}"
    install -d -m 750 -o "$SERVICE_USER" -g "$SERVICE_GROUP" "$DATA_DIR"
    # The private key lives here: the service user only.
    install -d -m 700 -o "$SERVICE_USER" -g "$SERVICE_GROUP" "$CERT_DIR"
    install -d -m 750 -o root -g "$SERVICE_GROUP" "$CONFIG_DIR"

    log "Installing binary to ${BINARY_DEST}"
    install -m 755 -o root -g root "$BINARY_SRC" "$BINARY_DEST"

    # --- Config ---------------------------------------------------------------

    if [ -f "$ENV_FILE" ]; then
        log "Keeping existing config ${ENV_FILE}"
    else
        log "Writing ${ENV_FILE}"
        write_env_file ""
    fi

    # --- Log rotation (journald namespace) ------------------------------------

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

    # --- systemd unit (written, not enabled until enrollment succeeded) -------

    log "Writing ${UNIT_FILE}"
    render_unit > "$UNIT_FILE"
    systemctl daemon-reload

    journal_flags=""
    if [ "$HAS_LOG_NAMESPACE" -eq 1 ]; then
        journal_flags="--namespace ${JOURNAL_NAMESPACE} "
    fi

    # --- Enrollment -----------------------------------------------------------

    enroll_status=0
    if agent_enrolled; then
        log "Already enrolled (${CERT_DIR}/agent.crt exists): keeping the certificate"
        enroll_status=3
    else
        enroll_agent || enroll_status=$?
    fi
    case "$enroll_status" in
        0) ;;
        3)
            grep -q '^HUB_HOST=' "$ENV_FILE" 2>/dev/null \
                || warn "${ENV_FILE} has no HUB_HOST: the agent cannot find its hub. Remove ${CERT_DIR}/agent.crt and rerun to enroll again"
            ;;
        2)
            systemctl disable --now "$SERVICE_NAME" 2>/dev/null || true
            warn "enrollment gave up: no code was entered in time."
            warn "${SERVICE_NAME} is installed but disabled and stopped; rerun this installer to get a new code"
            exit 1
            ;;
        *)
            systemctl disable --now "$SERVICE_NAME" 2>/dev/null || true
            warn "enrollment failed (exit ${enroll_status}); see the message above."
            warn "${SERVICE_NAME} is installed but disabled and stopped; fix the cause and rerun this installer"
            exit 1
            ;;
    esac

    # --- Enable + start -------------------------------------------------------

    log "Enabling and (re)starting ${SERVICE_NAME}.service"
    systemctl enable "$SERVICE_NAME"
    # restart, not enable --now: on a rerun the service is already running the
    # old binary, and enable --now leaves a running unit alone.
    systemctl restart "$SERVICE_NAME"

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
    echo "  config: ${ENV_FILE}, data: ${DATA_DIR}"
    echo
    echo "The server connects to its hub in a few seconds and shows up as online"
    echo "in the web UI."
}

main "$@"
