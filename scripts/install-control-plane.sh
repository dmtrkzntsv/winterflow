#!/usr/bin/env bash
#
# Install a WinterFlow control plane (API + hub, Postgres, Redis) on one
# Debian or Ubuntu host.
#
# The control plane is what agents enroll with and connect to: the API (web
# UI, database, certificate signing) and a hub (the gRPC endpoint agents
# dial over mTLS), joined by a Redis bus. This script:
#   - downloads winterflow-api and winterflow-hub from the release mirror
#     (Cloudflare R2, private: needs WINTERFLOW_R2_ENDPOINT,
#     WINTERFLOW_R2_ACCESS_KEY_ID and WINTERFLOW_R2_SECRET_ACCESS_KEY in the
#     environment) and verifies them against SHA256SUMS,
#   - offers to install PostgreSQL and Redis from the distro packages when
#     they are missing,
#   - creates the Postgres role and database 'winterflow' (only if absent),
#   - creates the Winterflow CA, the hub certificate and a Redis certificate
#     once (winterflow-hub pki), keeping the CA on every rerun,
#   - runs a dedicated Redis instance, redis-server@winterflow: TLS only on
#     port 6380 with a password (the distro's default Redis is never touched),
#   - installs hardened systemd units winterflow-api and winterflow-hub, run
#     by separate system users; only winterflow-api can read the CA key.
#
# It does not install or configure Caddy, touch the firewall, or change the
# global Postgres/Redis configuration. Terminate TLS for the web UI in your
# own proxy; the installer prints the address to proxy to.
#
# Generated passwords and the JWT secret are kept in
# /etc/winterflow/control-plane.env (root only, back it up) and reused on
# every rerun, as are the CA and the data. A rerun updates the binaries and
# units and restarts the services.
#
# Logs go to the journald namespace "winterflow":
#   journalctl --namespace winterflow -u winterflow-api -u winterflow-hub -f
#
# Usage:
#   export WINTERFLOW_R2_ENDPOINT=https://<account_id>.r2.cloudflarestorage.com
#   export WINTERFLOW_R2_ACCESS_KEY_ID=... WINTERFLOW_R2_SECRET_ACCESS_KEY=...
#   sudo -E bash install-control-plane.sh            # asks the questions below
#   sudo -E bash install-control-plane.sh --yes --web-url https://app.example.com
#
# Options:
#   --web-url URL      Public URL of the web UI (https; sets WEB_URL, secure
#                      cookies and CORS_ALLOW_ORIGINS). Default: kept, else
#                      https://<this host's FQDN>.
#   --port PORT        API port, proxied to by your Caddy (default: kept, else
#                      8080).
#   --hub-addr H:P     Hub address agents dial, host:port (default: kept, else
#                      <FQDN>:50051). Its host goes into the hub certificate.
#   --capacity N       Agents this hub should carry (default: kept, else 1000).
#   --redis-bind ADDR  Extra address Redis listens on besides 127.0.0.1, e.g.
#                      a Tailscale IP for hubs on other machines (repeatable;
#                      replaces the kept list).
#   --redis-host NAME  Extra name for the Redis certificate, e.g. this host's
#                      Tailscale name (repeatable; replaces the kept list).
#   --version TAG      Release to download instead of latest, e.g. v260823.1.
#   --r2-bucket NAME   R2 bucket holding the release mirror (default: econumo).
#   --yes              Non-interactive: assume "yes" (install missing
#                      packages) and take the defaults above.
#   --dry-run          Print every file and command that would be used and
#                      exit without changing anything. Does not require root.
#   --uninstall        Stop and remove winterflow-api, winterflow-hub (units
#                      and binaries) and disable redis-server@winterflow.
#                      Keeps /var/lib/winterflow-control (the CA), the
#                      /etc/winterflow env files, the Redis config and data,
#                      and the Postgres database. There is no --purge.
#   -h, --help         Show this help.

set -euo pipefail

# WF_INSTALL_ROOT prefixes every path below, so tests can run the installer
# against a scratch directory. Unset in real use.
INSTALL_ROOT="${WF_INSTALL_ROOT:-}"

CONFIG_DIR="${INSTALL_ROOT}/etc/winterflow"
STATE_FILE="${CONFIG_DIR}/control-plane.env"
API_ENV_FILE="${CONFIG_DIR}/control-plane-api.env"
HUB_ENV_FILE="${CONFIG_DIR}/control-plane-hub.env"
# Its own data root, apart from the agent's /var/lib/winterflow, so an agent
# install or --purge on the same host never touches the CA.
DATA_DIR="${INSTALL_ROOT}/var/lib/winterflow-control"
PKI_DIR="${DATA_DIR}/pki"
HUB_CERT_DIR="${DATA_DIR}/hub-certs"
API_HOME="${DATA_DIR}/api"
HUB_HOME="${DATA_DIR}/hub"
API_DEST="${INSTALL_ROOT}/usr/local/bin/winterflow-api"
HUB_DEST="${INSTALL_ROOT}/usr/local/bin/winterflow-hub"
API_UNIT="${INSTALL_ROOT}/etc/systemd/system/winterflow-api.service"
HUB_UNIT="${INSTALL_ROOT}/etc/systemd/system/winterflow-hub.service"
REDIS_SERVICE="redis-server@winterflow.service"
REDIS_CONF="${INSTALL_ROOT}/etc/redis/redis-winterflow.conf"
REDIS_TLS_DIR="${INSTALL_ROOT}/etc/redis/winterflow-tls"
# A drop-in for this instance only; the template and the default unit stay as
# the package ships them.
REDIS_DROPIN_DIR="${INSTALL_ROOT}/etc/systemd/system/${REDIS_SERVICE}.d"
REDIS_DROPIN="${REDIS_DROPIN_DIR}/winterflow.conf"
REDIS_DATA_DIR="${INSTALL_ROOT}/var/lib/redis/winterflow"
REDIS_PORT=6380
JOURNAL_NAMESPACE="winterflow"
JOURNAL_CONF="${INSTALL_ROOT}/etc/systemd/journald@${JOURNAL_NAMESPACE}.conf"
GITHUB_REPO="dmtrkzntsv/winterflow"
API_USER="winterflow-api"
HUB_USER="winterflow-hub"
DB_NAME="winterflow"
DB_USER="winterflow"
R2_PROJECT="winterflow"
# Debian's redis-server@ template runs every instance as this user.
REDIS_USER="redis"

WEB_URL=""
API_PORT=""
HUB_PUBLIC_ADDR=""
HUB_CAPACITY=""
REDIS_BIND=""
REDIS_HOSTS=""
WEB_URL_SET=0
API_PORT_SET=0
HUB_ADDR_SET=0
CAPACITY_SET=0
REDIS_BIND_SET=0
REDIS_HOSTS_SET=0
POSTGRES_PASSWORD=""
REDIS_PASSWORD=""
JWT_SECRET=""
POSTGRES_PASSWORD_KEPT=0
R2_BUCKET="econumo"
RELEASE_VERSION=""
CHANNEL="latest"
ASSUME_YES=0
DRY_RUN=0
UNINSTALL=0
FQDN=""
CPU_QUOTA=""
MEMORY_MAX=""

HAS_LOG_NAMESPACE=0
sysd_ver=""
PUT_CHANGED=0
REDIS_CHANGED=0
PG_PORT=""
RELEASE_ARCH=""

# Temp dir for the downloads; cleaned up on exit.
WORK_DIR=""
trap '[ -n "$WORK_DIR" ] && rm -rf "$WORK_DIR"' EXIT

# --- shared: log ---
log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }
# --- end shared ---

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

usage() {
    # Print the header comment block (everything up to the first non-comment
    # line). When piped there is no script file to read; point at the repo.
    if [ -f "$0" ]; then
        awk 'NR==1{next} !/^#/{exit} {sub(/^# ?/,""); print}' "$0"
    else
        echo "WinterFlow control-plane installer."
        echo "Options: https://github.com/${GITHUB_REPO}/blob/main/scripts/install-control-plane.sh"
    fi
}

# --- Validation ---------------------------------------------------------------

valid_port() {
    case "$1" in ''|*[!0-9]*) return 1 ;; esac
    [ "${#1}" -le 5 ] && [ "$((10#$1))" -ge 1 ] && [ "$((10#$1))" -le 65535 ]
}

valid_host() {
    # A DNS name or an IPv4 address: what goes into a certificate SAN.
    printf '%s' "$1" | grep -qE '^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$'
}

valid_ip() {
    # An IPv4 or IPv6 literal for Redis's bind list. Wildcards are refused:
    # Redis would then listen on every interface.
    case "$1" in 0.0.0.0|::|'[::]') return 1 ;; esac
    printf '%s' "$1" | grep -qE '^([0-9]{1,3}(\.[0-9]{1,3}){3}|[0-9A-Fa-f:]*:[0-9A-Fa-f:.]*)$'
}

valid_hub_addr() {
    local host="${1%:*}" port="${1##*:}"
    [ "$host" != "$1" ] && valid_host "$host" && valid_port "$port"
}

valid_web_url() {
    # https, or http to this machine. Cookies are Secure for https.
    case "$1" in
        https://?*) ;;
        http://localhost|http://localhost[:/]*|http://127.0.0.1|http://127.0.0.1[:/]*) ;;
        *) return 1 ;;
    esac
    printf '%s' "$1" | grep -qE '^https?://[A-Za-z0-9.-]+(:[0-9]{1,5})?(/[A-Za-z0-9._~/-]*)?$'
}

valid_secret() {
    printf '%s' "$1" | grep -qE '^[0-9a-f]{16,128}$'
}

valid_capacity() {
    printf '%s' "$1" | grep -qE '^[1-9][0-9]{0,6}$'
}

url_origin() {
    # scheme://host[:port] of a URL: the browser's Origin for CORS.
    printf '%s' "$1" | sed -E 's#^(https?://[^/]+).*$#\1#'
}

url_host() {
    printf '%s' "$1" | sed -E 's#^https?://([^/:]+).*$#\1#'
}

uniq_words() {
    # uniq_words WORD... -> the words once each, in order, space-separated.
    local w out=" "
    for w in "$@"; do
        [ -n "$w" ] || continue
        case "$out" in *" $w "*) ;; *) out="${out}${w} " ;; esac
    done
    out="${out# }"
    printf '%s' "${out% }"
}

random_hex() {
    # 32 random bytes as hex; no openssl dependency.
    head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
}

is_debian_like() {
    local f="${INSTALL_ROOT}/etc/os-release" id like
    [ -f "$f" ] || return 1
    id="$(sed -n 's/^ID=//p' "$f" | tr -d '"' | head -n 1)"
    like="$(sed -n 's/^ID_LIKE=//p' "$f" | tr -d '"' | head -n 1)"
    case " ${id} ${like} " in
        *" debian "*|*" ubuntu "*) return 0 ;;
    esac
    return 1
}

# --- Kept state (/etc/winterflow/control-plane.env) ---------------------------

state_value() {
    # state_value KEY -> the kept value (last one wins), empty when absent.
    # The file is parsed, never sourced: each value is validated by its user.
    [ -f "$STATE_FILE" ] || return 0
    sed -n "s/^$1=//p" "$STATE_FILE" | tail -n 1
}

load_state() {
    local v w
    if [ -f "$STATE_FILE" ] && [ ! -r "$STATE_FILE" ]; then
        die "cannot read ${STATE_FILE} (run as root)"
    fi
    v="$(state_value POSTGRES_PASSWORD)"
    if [ -n "$v" ]; then
        valid_secret "$v" || die "${STATE_FILE}: POSTGRES_PASSWORD is not a hex string; fix or remove the line"
        POSTGRES_PASSWORD="$v"; POSTGRES_PASSWORD_KEPT=1
    fi
    v="$(state_value REDIS_PASSWORD)"
    if [ -n "$v" ]; then
        valid_secret "$v" || die "${STATE_FILE}: REDIS_PASSWORD is not a hex string; fix or remove the line"
        REDIS_PASSWORD="$v"
    fi
    v="$(state_value JWT_SECRET)"
    if [ -n "$v" ]; then
        valid_secret "$v" || die "${STATE_FILE}: JWT_SECRET is not a hex string; fix or remove the line"
        JWT_SECRET="$v"
    fi
    if [ "$WEB_URL_SET" -eq 0 ]; then
        v="$(state_value WEB_URL)"; [ -z "$v" ] || WEB_URL="$v"
    fi
    if [ "$API_PORT_SET" -eq 0 ]; then
        v="$(state_value API_PORT)"; [ -z "$v" ] || API_PORT="$v"
    fi
    if [ "$HUB_ADDR_SET" -eq 0 ]; then
        v="$(state_value HUB_PUBLIC_ADDR)"; [ -z "$v" ] || HUB_PUBLIC_ADDR="$v"
    fi
    if [ "$CAPACITY_SET" -eq 0 ]; then
        v="$(state_value HUB_CAPACITY)"; [ -z "$v" ] || HUB_CAPACITY="$v"
    fi
    if [ "$REDIS_BIND_SET" -eq 0 ]; then
        REDIS_BIND="$(state_value REDIS_BIND)"
        for w in $REDIS_BIND; do valid_ip "$w" || die "${STATE_FILE}: REDIS_BIND has an invalid address '${w}'"; done
    fi
    if [ "$REDIS_HOSTS_SET" -eq 0 ]; then
        REDIS_HOSTS="$(state_value REDIS_HOSTS)"
        for w in $REDIS_HOSTS; do valid_host "$w" || die "${STATE_FILE}: REDIS_HOSTS has an invalid name '${w}'"; done
    fi
}

check_settings() {
    valid_web_url "$WEB_URL" || die "web URL must be https:// (http:// only for localhost), got: ${WEB_URL}"
    WEB_URL="${WEB_URL%/}"
    valid_port "$API_PORT" || die "invalid API port: ${API_PORT}"
    valid_hub_addr "$HUB_PUBLIC_ADDR" || die "hub address must be host:port (a DNS name or IPv4), got: ${HUB_PUBLIC_ADDR}"
    [ "$API_PORT" != "${HUB_PUBLIC_ADDR##*:}" ] || die "the API port and the hub port must differ (both ${API_PORT})"
    valid_capacity "$HUB_CAPACITY" || die "hub capacity must be a positive number, got: ${HUB_CAPACITY}"
}

# --- Release mirror (R2) ------------------------------------------------------

require_r2_env() {
    local missing="" v
    for v in WINTERFLOW_R2_ENDPOINT WINTERFLOW_R2_ACCESS_KEY_ID WINTERFLOW_R2_SECRET_ACCESS_KEY; do
        [ -n "${!v:-}" ] || missing="${missing} ${v}"
    done
    if [ -n "$missing" ]; then
        die "the release mirror needs these variables in the environment:${missing}
       (export them, then run with sudo -E so they reach the script)"
    fi
    printf '%s' "$WINTERFLOW_R2_ENDPOINT" | grep -qE '^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?/?$' \
        || die "WINTERFLOW_R2_ENDPOINT must be https://<host>, got: ${WINTERFLOW_R2_ENDPOINT}"
}

r2_url() {
    # r2_url FILE -> the object URL under <bucket>/winterflow/<channel>/.
    printf '%s/%s/%s/%s/%s' "${WINTERFLOW_R2_ENDPOINT%/}" "$R2_BUCKET" "$R2_PROJECT" "$CHANNEL" "$1"
}

curl_quote() {
    # Escape a value for a double-quoted curl config string.
    local s="${1//\\/\\\\}"
    printf '%s' "${s//\"/\\\"}"
}

r2_fetch() {
    # r2_fetch FILE DEST — a SigV4-signed GET (curl >= 7.75). The credentials
    # reach curl through a config on stdin, never its command line (ps), and
    # are never printed.
    printf 'user = "%s:%s"\n' "$(curl_quote "$WINTERFLOW_R2_ACCESS_KEY_ID")" \
        "$(curl_quote "$WINTERFLOW_R2_SECRET_ACCESS_KEY")" \
        | curl -fsS --proto '=https' --tlsv1.2 --connect-timeout 20 --max-time 900 \
            --retry 2 --aws-sigv4 "aws:amz:auto:s3" -K - -o "$2" "$(r2_url "$1")"
}

print_r2_fetch() {
    printf 'curl --aws-sigv4 "aws:amz:auto:s3" --user "%s:<secret hidden>" %s\n' \
        "$WINTERFLOW_R2_ACCESS_KEY_ID" "$(r2_url "$1")"
}

detect_arch() {
    # Sets RELEASE_ARCH as Go spells it (the arch part of the R2 asset names;
    # same mapping as install.sh's detect_platform).
    [ "$(uname -s)" = Linux ] || die "the control plane runs on Linux only"
    case "$(uname -m)" in
        x86_64|amd64)  RELEASE_ARCH=amd64 ;;
        aarch64|arm64) RELEASE_ARCH=arm64 ;;
        armv6l|armv7l) RELEASE_ARCH=arm ;;
        *) die "no prebuilt binary for $(uname -m)" ;;
    esac
}

check_curl() {
    local ver major minor
    command -v curl >/dev/null 2>&1 || die "curl is required (apt-get install curl)"
    ver="$(curl --version | awk 'NR==1{print $2}')"
    major="${ver%%.*}"; minor="${ver#*.}"; minor="${minor%%.*}"
    case "${major}${minor}" in ''|*[!0-9]*) die "cannot read the curl version (${ver})" ;; esac
    if [ "$major" -lt 7 ] || { [ "$major" -eq 7 ] && [ "$minor" -lt 75 ]; }; then
        die "curl ${ver} is too old: downloads from R2 need curl >= 7.75 (--aws-sigv4)"
    fi
}

download_binaries() {
    # winterflow-api and winterflow-hub for this arch into WORK_DIR, verified
    # against the mirror's SHA256SUMS.
    local c asset
    detect_arch
    WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/winterflow-install.XXXXXX")"
    log "Downloading SHA256SUMS from $(r2_url "")"
    r2_fetch SHA256SUMS "${WORK_DIR}/SHA256SUMS" \
        || die "download failed: $(r2_url SHA256SUMS) (wrong credentials, bucket, or version?)"
    for c in api hub; do
        asset="winterflow-${c}-linux-${RELEASE_ARCH}"
        log "Downloading ${asset}"
        r2_fetch "$asset" "${WORK_DIR}/${asset}" || die "download failed: $(r2_url "$asset")"
        (cd "$WORK_DIR" && grep -E "[[:space:]][*]?${asset}\$" SHA256SUMS | sha256sum -c --quiet -) \
            || die "checksum mismatch for ${asset}: corrupted or tampered download"
        chmod 755 "${WORK_DIR}/${asset}"
    done
    log "Checksums verified"
}

# --- Packages -----------------------------------------------------------------

postgres_installed() {
    command -v psql >/dev/null 2>&1 && systemctl cat postgresql.service >/dev/null 2>&1
}

redis_installed() {
    command -v redis-server >/dev/null 2>&1 && systemctl cat "$REDIS_SERVICE" >/dev/null 2>&1
}

ensure_packages() {
    # Ask before installing each missing package; declining stops with what
    # to install by hand.
    local packages=()
    if postgres_installed; then
        log "PostgreSQL is installed"
    elif confirm "PostgreSQL is not installed. Install it with apt-get (package postgresql)?"; then
        packages+=(postgresql)
    else
        die "PostgreSQL is required: install the 'postgresql' package and rerun"
    fi
    if redis_installed; then
        log "Redis is installed"
    elif confirm "Redis is not installed. Install it with apt-get (package redis-server)?"; then
        packages+=(redis-server)
    else
        die "Redis is required: install the 'redis-server' package and rerun"
    fi
    if [ "${#packages[@]}" -gt 0 ]; then
        log "Installing ${packages[*]}"
        apt-get update -qq
        DEBIAN_FRONTEND=noninteractive apt-get install -y "${packages[@]}"
    fi
    postgres_installed || die "PostgreSQL is still missing (psql and postgresql.service); install it and rerun"
    redis_installed || die "Redis is still missing (redis-server and the ${REDIS_SERVICE} template unit); install the 'redis-server' package and rerun"
    local rv
    rv="$(redis-server --version | sed -n 's/.* v=\([0-9]*\)\..*/\1/p')"
    [ -n "$rv" ] && [ "$rv" -ge 6 ] || die "Redis ${rv:-unknown} has no TLS support; Redis >= 6 is required"
}

# --- Renderers (shared by install and --dry-run) ------------------------------

shown() {
    # shown VALUE -> VALUE in a real run. A dry run never prints a secret:
    # <generated> for a fresh one, a sha256 fingerprint for a kept one.
    if [ -z "$1" ]; then
        printf '<generated>'
    elif [ "$DRY_RUN" -eq 1 ]; then
        printf '<kept sha256:%s>' "$(printf '%s' "$1" | sha256sum | cut -c1-8)"
    else
        printf '%s' "$1"
    fi
}

render_state() {
    cat <<EOF
# WinterFlow control plane, kept by install-control-plane.sh and reused on
# every rerun. Root only: holds the database and Redis passwords and the JWT
# secret. Back it up with the database and ${PKI_DIR}.
POSTGRES_PASSWORD=$(shown "$POSTGRES_PASSWORD")
REDIS_PASSWORD=$(shown "$REDIS_PASSWORD")
JWT_SECRET=$(shown "$JWT_SECRET")
WEB_URL=${WEB_URL}
API_PORT=${API_PORT}
HUB_PUBLIC_ADDR=${HUB_PUBLIC_ADDR}
HUB_CAPACITY=${HUB_CAPACITY}
REDIS_BIND=${REDIS_BIND}
REDIS_HOSTS=${REDIS_HOSTS}
EOF
}

render_api_env() {
    cat <<EOF
# WinterFlow API (written by install-control-plane.sh from
# ${STATE_FILE}; a rerun rewrites it). See .env.example in the repo.
LOG_LEVEL=info
# The API listens on 127.0.0.1:${API_PORT} only; your Caddy proxies
# ${WEB_URL} to it.
API_LISTEN_ADDR=127.0.0.1
API_PORT=${API_PORT}
WEB_URL=${WEB_URL}
CORS_ALLOW_ORIGINS=$(url_origin "$WEB_URL")
JWT_SECRET=$(shown "$JWT_SECRET")
DATABASE_URL=postgres://${DB_USER}:$(shown "$POSTGRES_PASSWORD")@127.0.0.1:${PG_PORT:-5432}/${DB_NAME}?sslmode=disable
REDIS_ADDR=127.0.0.1:${REDIS_PORT}
REDIS_PASSWORD=$(shown "$REDIS_PASSWORD")
REDIS_TLS=true
REDIS_CA_CERT_PATH=${PKI_DIR}/ca.crt
# The CA pair (ca.crt, ca.key) the API signs agent certificates with.
HUB_CERT_DIR=${PKI_DIR}
AVATARS_STORAGE_PATH=${API_HOME}/avatars
EOF
}

render_hub_env() {
    cat <<EOF
# WinterFlow hub (written by install-control-plane.sh; a rerun rewrites it).
# Holds only the CA certificate and the hub's own pair, never the CA key.
LOG_LEVEL=info
HUB_PUBLIC_ADDR=${HUB_PUBLIC_ADDR}
HUB_PORT=${HUB_PUBLIC_ADDR##*:}
HUB_CAPACITY=${HUB_CAPACITY}
HUB_CERT_DIR=${HUB_CERT_DIR}
REDIS_ADDR=127.0.0.1:${REDIS_PORT}
REDIS_PASSWORD=$(shown "$REDIS_PASSWORD")
REDIS_TLS=true
REDIS_CA_CERT_PATH=${HUB_CERT_DIR}/ca.crt
EOF
}

render_redis_conf() {
    # shellcheck disable=SC2086 # REDIS_BIND is a word list
    cat <<EOF
# Winterflow's dedicated Redis (redis-server@winterflow), written by
# install-control-plane.sh; a rerun rewrites it. TLS only, password auth.
# The distro's default instance (redis-server.service) is separate.
port 0
tls-port ${REDIS_PORT}
bind $(uniq_words 127.0.0.1 ${REDIS_BIND})
protected-mode yes
tls-cert-file ${REDIS_TLS_DIR}/redis.crt
tls-key-file ${REDIS_TLS_DIR}/redis.key
tls-ca-cert-file ${REDIS_TLS_DIR}/ca.crt
tls-auth-clients no
tls-protocols "TLSv1.2 TLSv1.3"
requirepass $(shown "$REDIS_PASSWORD")
supervised systemd
daemonize no
pidfile /run/redis-winterflow/redis-server.pid
logfile ""
dir ${REDIS_DATA_DIR}
EOF
}

render_redis_dropin() {
    # Redis exits when a bind address is not up yet (a Tailscale IP at boot);
    # Debian's template restarts it at once and gives up at the start limit,
    # leaving the API without its bus. Keep retrying every 5 s instead.
    local tailscale=""
    if [ -n "$REDIS_BIND" ]; then
        tailscale="After=tailscaled.service"
    fi
    cat <<EOF
# Written by install-control-plane.sh for redis-server@winterflow only.
[Unit]
Wants=network-online.target
After=network-online.target
${tailscale}
StartLimitIntervalSec=0

[Service]
RestartSec=5s
EOF
}

render_journald_conf() {
    cat <<EOF
# Rotation policy for the '${JOURNAL_NAMESPACE}' journal namespace
# (written by install-control-plane.sh; applies only to winterflow logs,
# never the system journal).
#
# View:    journalctl --namespace ${JOURNAL_NAMESPACE} -u winterflow-api -u winterflow-hub -f
# Delete:  journalctl --namespace ${JOURNAL_NAMESPACE} --vacuum-time=1s
[Journal]
Storage=persistent
SystemMaxUse=200M
SystemMaxFileSize=20M
MaxRetentionSec=1month
EOF
}

render_unit_common() {
    # render_unit_common RW_DIR — logging, limits and hardening both units share.
    local log_ns_line=""
    if [ "$HAS_LOG_NAMESPACE" -eq 1 ]; then
        log_ns_line="LogNamespace=${JOURNAL_NAMESPACE}"
    fi
    cat <<EOF

# Logging: dedicated journald namespace with size/age caps (rotation policy
# in ${JOURNAL_CONF}), plus a rate limit.
${log_ns_line}
LogRateLimitIntervalSec=30s
LogRateLimitBurst=10000

# Resource limits: thermal and memory safety on small machines.
CPUQuota=${CPU_QUOTA}%
CPUWeight=90
MemoryMax=${MEMORY_MAX}
TasksMax=512
LimitNOFILE=65536

# Hardening.
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=$1
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
EOF
}

render_api_unit() {
    cat <<EOF
[Unit]
Description=WinterFlow API (control plane + web UI)
Documentation=https://github.com/${GITHUB_REPO}
After=network-online.target postgresql.service ${REDIS_SERVICE}
Wants=network-online.target postgresql.service ${REDIS_SERVICE}

[Service]
Type=simple
User=${API_USER}
Group=${API_USER}
EnvironmentFile=${API_ENV_FILE}
WorkingDirectory=${API_HOME}
ExecStart=${API_DEST}
Restart=on-failure
RestartSec=5
CapabilityBoundingSet=
EOF
    render_unit_common "$API_HOME"
    cat <<EOF

[Install]
WantedBy=multi-user.target
EOF
}

render_hub_unit() {
    local caps="CapabilityBoundingSet="
    if [ "$((10#${HUB_PUBLIC_ADDR##*:}))" -lt 1024 ]; then
        caps="AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE"
    fi
    cat <<EOF
[Unit]
Description=WinterFlow hub (agents connect here over mTLS)
Documentation=https://github.com/${GITHUB_REPO}
After=network-online.target ${REDIS_SERVICE}
Wants=network-online.target ${REDIS_SERVICE}

[Service]
Type=simple
User=${HUB_USER}
Group=${HUB_USER}
EnvironmentFile=${HUB_ENV_FILE}
WorkingDirectory=${HUB_HOME}
ExecStart=${HUB_DEST}
Restart=on-failure
RestartSec=5
# Stopping drains the hub first: it tells its agents to move to other hubs
# and waits up to 2 minutes for them to leave, then stops gracefully.
TimeoutStopSec=150
${caps}
EOF
    render_unit_common "$HUB_HOME"
    cat <<EOF

[Install]
WantedBy=multi-user.target
EOF
}

hub_pki_hosts() { uniq_words "${HUB_PUBLIC_ADDR%:*}" "$FQDN"; }

redis_pki_hosts() {
    # 127.0.0.1 because the API and the local hub dial Redis there (the
    # client checks it against the certificate), plus each bind address.
    # shellcheck disable=SC2086 # word lists by design
    uniq_words 127.0.0.1 "$FQDN" $REDIS_HOSTS $REDIS_BIND
}

pki_args() {
    # The winterflow-hub pki arguments, one per line.
    local h
    printf '%s\n' --dir "$PKI_DIR"
    for h in $(hub_pki_hosts); do printf '%s\n' --host "$h"; done
    for h in $(redis_pki_hosts); do printf '%s\n' --redis-host "$h"; done
}

render_plan() {
    local pkg_line
    if postgres_installed; then pkg_line="PostgreSQL: installed"; else pkg_line="PostgreSQL: missing, ask, then apt-get install -y postgresql"; fi
    echo "$pkg_line"
    if redis_installed; then pkg_line="Redis: installed"; else pkg_line="Redis: missing, ask, then apt-get install -y redis-server"; fi
    echo "$pkg_line"
    echo "users: ${API_USER}, ${HUB_USER} (system users, no login shell)"
    echo "install ${API_DEST} ${HUB_DEST} (root, 0755)"
    echo "runuser -u ${API_USER} -- winterflow-hub pki $(pki_args | paste -sd ' ' -)"
    echo "  ${PKI_DIR}: ${API_USER} 0700; ca.key: ${API_USER} 0600 (only the API reads the CA key)"
    echo "  ${HUB_CERT_DIR}: ca.crt hub.crt hub.key copied, root:${HUB_USER}, key 0640"
    echo "  ${REDIS_TLS_DIR}: ca.crt redis.crt redis.key copied, root:${REDIS_USER}, key 0640"
    echo "postgres: create role and database '${DB_NAME}' if absent (password from ${STATE_FILE})"
    echo "systemctl enable ${REDIS_SERVICE}; restart it when its config or certificate changed"
    echo "systemctl enable winterflow-api winterflow-hub; systemctl restart winterflow-api winterflow-hub"
}

render_finish() {
    local hub_port="${HUB_PUBLIC_ADDR##*:}"
    cat <<EOF
Next steps (this installer does not touch Caddy or the firewall):
  1. Proxy your Caddy to 127.0.0.1:${API_PORT}, e.g.
       $(url_host "$WEB_URL") {
           reverse_proxy 127.0.0.1:${API_PORT} {
               header_up X-Forwarded-For {client_ip}
           }
       }
  2. Open TCP ${hub_port} so agents can reach the hub at ${HUB_PUBLIC_ADDR}.
  3. Open ${WEB_URL} and create the first account.
EOF
}

# --- Install steps ------------------------------------------------------------

put_file() {
    # put_file DEST MODE OWNER GROUP < CONTENT — write atomically with the
    # given owner and mode; PUT_CHANGED=1 when the content differs. Feed it
    # by redirection, not a pipe: a piped put_file runs in a subshell and
    # its PUT_CHANGED is lost.
    local dest="$1" tmp
    tmp="$(mktemp "$(dirname "$dest")/.wf-install.XXXXXX")"
    cat > "$tmp"
    chown "$3:$4" "$tmp"
    chmod "$2" "$tmp"
    PUT_CHANGED=1
    if [ -f "$dest" ] && cmp -s "$tmp" "$dest"; then
        PUT_CHANGED=0
    fi
    mv -f "$tmp" "$dest"
}

ensure_system_user() {
    # ensure_system_user NAME HOME
    if id "$1" >/dev/null 2>&1; then
        [ "$(id -u "$1")" -ne 0 ] || die "user '$1' exists with uid 0; refusing to run a service as root"
        log "User '$1' already exists"
    else
        useradd --system --user-group --home-dir "$2" --no-create-home --shell /usr/sbin/nologin "$1"
        log "Created system user '$1'"
    fi
}

pg() {
    # psql as the postgres superuser over the local socket; SQL on stdin.
    runuser -u postgres -- psql -XAtq -v ON_ERROR_STOP=1
}

provision_postgres() {
    local i role db owner
    systemctl enable --now postgresql >/dev/null 2>&1 || true
    for i in $(seq 1 30); do
        [ "$(echo 'SELECT 1' | pg 2>/dev/null)" = 1 ] && break
        [ "$i" -lt 30 ] || die "PostgreSQL is not answering (systemctl status postgresql)"
        sleep 1
    done
    PG_PORT="$(echo 'SHOW port' | pg)"
    valid_port "$PG_PORT" || die "unexpected PostgreSQL port: ${PG_PORT}"
    # Names are constants and the password is hex: nothing user-supplied
    # reaches SQL. Statements go through stdin, not the process list.
    role="$(echo "SELECT 1 FROM pg_roles WHERE rolname='${DB_USER}'" | pg)"
    if [ "$role" != 1 ]; then
        printf "CREATE ROLE %s LOGIN PASSWORD '%s';\n" "$DB_USER" "$POSTGRES_PASSWORD" | pg >/dev/null
        log "Created PostgreSQL role '${DB_USER}'"
    elif [ "$POSTGRES_PASSWORD_KEPT" -eq 0 ]; then
        die "PostgreSQL role '${DB_USER}' exists but ${STATE_FILE} has no POSTGRES_PASSWORD; add POSTGRES_PASSWORD=<its password> (hex) there, or drop the role, and rerun"
    else
        log "PostgreSQL role '${DB_USER}' exists, keeping it"
    fi
    db="$(echo "SELECT 1 FROM pg_database WHERE datname='${DB_NAME}'" | pg)"
    if [ "$db" != 1 ]; then
        runuser -u postgres -- createdb -O "$DB_USER" "$DB_NAME"
        log "Created PostgreSQL database '${DB_NAME}'"
    else
        owner="$(echo "SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname='${DB_NAME}'" | pg)"
        [ "$owner" = "$DB_USER" ] || die "PostgreSQL database '${DB_NAME}' exists but is owned by '${owner}', not '${DB_USER}'"
        log "PostgreSQL database '${DB_NAME}' exists, keeping it"
    fi
}

issue_pki() {
    # The CA is created once and kept; hub and Redis certificates are
    # (re)issued when their names change. Run as the API user, so ca.key is
    # owned by it from the start.
    local args=() line
    while IFS= read -r line; do args+=("$line"); done < <(pki_args)
    install -d -m 700 -o "$API_USER" -g "$API_USER" "$PKI_DIR"
    runuser -u "$API_USER" -- "$HUB_DEST" pki "${args[@]}" >/dev/null \
        || die "winterflow-hub pki failed; see the message above"
    chown -R "$API_USER:$API_USER" "$PKI_DIR"
    chmod 700 "$PKI_DIR"
    chmod 600 "$PKI_DIR"/*.key

    # The hub gets copies of the CA certificate and its own pair only (it is
    # restarted on every run, so no change tracking).
    install -d -m 750 -o root -g "$HUB_USER" "$HUB_CERT_DIR"
    put_file "$HUB_CERT_DIR/ca.crt" 644 root "$HUB_USER" < "$PKI_DIR/ca.crt"
    put_file "$HUB_CERT_DIR/hub.crt" 644 root "$HUB_USER" < "$PKI_DIR/hub.crt"
    put_file "$HUB_CERT_DIR/hub.key" 640 root "$HUB_USER" < "$PKI_DIR/hub.key"

    # Redis (user redis) gets the CA certificate and its pair.
    REDIS_CHANGED=0
    install -d -m 750 -o root -g "$REDIS_USER" "$REDIS_TLS_DIR"
    put_file "$REDIS_TLS_DIR/ca.crt" 644 root "$REDIS_USER" < "$PKI_DIR/ca.crt"; [ "$PUT_CHANGED" -eq 0 ] || REDIS_CHANGED=1
    put_file "$REDIS_TLS_DIR/redis.crt" 644 root "$REDIS_USER" < "$PKI_DIR/redis.crt"; [ "$PUT_CHANGED" -eq 0 ] || REDIS_CHANGED=1
    put_file "$REDIS_TLS_DIR/redis.key" 640 root "$REDIS_USER" < "$PKI_DIR/redis.key"; [ "$PUT_CHANGED" -eq 0 ] || REDIS_CHANGED=1
}

start_redis() {
    local i
    install -d -m 750 -o "$REDIS_USER" -g "$REDIS_USER" "$REDIS_DATA_DIR"
    put_file "$REDIS_CONF" 640 root "$REDIS_USER" < <(render_redis_conf)
    [ "$PUT_CHANGED" -eq 0 ] || REDIS_CHANGED=1
    install -d -m 755 -o root -g root "$REDIS_DROPIN_DIR"
    put_file "$REDIS_DROPIN" 644 root root < <(render_redis_dropin)
    [ "$PUT_CHANGED" -eq 0 ] || REDIS_CHANGED=1
    systemctl daemon-reload
    systemctl enable "$REDIS_SERVICE" >/dev/null
    if [ "$REDIS_CHANGED" -eq 1 ] || ! systemctl is-active --quiet "$REDIS_SERVICE"; then
        log "(Re)starting ${REDIS_SERVICE}"
        systemctl restart "$REDIS_SERVICE"
    fi
    for i in $(seq 1 15); do
        if REDISCLI_AUTH="$REDIS_PASSWORD" timeout 5 redis-cli --tls --cacert "$REDIS_TLS_DIR/ca.crt" \
            -h 127.0.0.1 -p "$REDIS_PORT" ping 2>/dev/null | grep -qx PONG; then
            log "Redis answers on 127.0.0.1:${REDIS_PORT} (TLS)"
            return 0
        fi
        sleep 1
    done
    die "${REDIS_SERVICE} does not answer on 127.0.0.1:${REDIS_PORT}; see journalctl -u ${REDIS_SERVICE}"
}

render_uninstall() {
    # The uninstall steps, printed by --dry-run --uninstall.
    cat <<EOF
systemctl disable --now winterflow-api winterflow-hub
rm -f ${API_UNIT} ${HUB_UNIT} ${API_DEST} ${HUB_DEST}
systemctl disable --now ${REDIS_SERVICE}
rm -f ${REDIS_DROPIN}
systemctl daemon-reload
EOF
}

render_kept() {
    cat <<EOF
kept: ${DATA_DIR} (the CA and certificates)
kept: ${STATE_FILE}, ${API_ENV_FILE}, ${HUB_ENV_FILE}
kept: ${REDIS_CONF}, ${REDIS_TLS_DIR}, ${REDIS_DATA_DIR}
kept: the PostgreSQL database '${DB_NAME}' and role '${DB_USER}', the default Redis
kept: the users ${API_USER} and ${HUB_USER}, and the journal namespace '${JOURNAL_NAMESPACE}'
A rerun of this installer brings the control plane back with the same CA and data.
EOF
}

uninstall() {
    # Stop and remove the services; keep every piece of state.
    if [ "$DRY_RUN" -eq 1 ]; then
        log "Dry run: printing the uninstall steps, changing nothing."
        echo
        echo "### uninstall"
        render_uninstall
        echo
        echo "### kept"
        render_kept
        return 0
    fi
    log "Stopping and removing winterflow-api and winterflow-hub"
    systemctl disable --now winterflow-api winterflow-hub 2>/dev/null || true
    rm -f "$API_UNIT" "$HUB_UNIT" "$API_DEST" "$HUB_DEST"
    log "Stopping and disabling ${REDIS_SERVICE} (its config and data stay)"
    systemctl disable --now "$REDIS_SERVICE" 2>/dev/null || true
    rm -f "$REDIS_DROPIN"
    rmdir "$REDIS_DROPIN_DIR" 2>/dev/null || true
    systemctl daemon-reload
    log "Removed the services and binaries."
    render_kept
}

ask_settings() {
    local answer
    if [ "$WEB_URL_SET" -eq 0 ]; then
        echo
        echo "Public URL of the web UI: your Caddy serves it with TLS and proxies"
        echo "to the API. Secure cookies and CORS are set for it."
        while :; do
            answer="$(ask "Web UI URL" "$WEB_URL")"
            if valid_web_url "$answer"; then WEB_URL="${answer%/}"; break; fi
            warn "'${answer}' is not an https:// URL (http:// only for localhost)"
        done
    fi
    if [ "$API_PORT_SET" -eq 0 ]; then
        while :; do
            answer="$(ask "API port (Caddy proxies to it)" "$API_PORT")"
            if valid_port "$answer"; then API_PORT="$answer"; break; fi
            warn "'${answer}' is not a port number"
        done
    fi
    if [ "$HUB_ADDR_SET" -eq 0 ]; then
        echo
        echo "Address agents dial to reach the hub (host:port). The host goes into"
        echo "the hub's certificate, so use the name agents will connect to."
        while :; do
            answer="$(ask "Hub public address" "$HUB_PUBLIC_ADDR")"
            if valid_hub_addr "$answer"; then HUB_PUBLIC_ADDR="$answer"; break; fi
            warn "'${answer}' is not host:port"
        done
    fi
    if [ "$CAPACITY_SET" -eq 0 ]; then
        while :; do
            answer="$(ask "Hub capacity (agents)" "$HUB_CAPACITY")"
            if valid_capacity "$answer"; then HUB_CAPACITY="$answer"; break; fi
            warn "'${answer}' is not a positive number"
        done
    fi
}

main() {
    local w
    # shellcheck disable=SC2086 # REDIS_BIND / REDIS_HOSTS are word lists
    while [ $# -gt 0 ]; do
        case "$1" in
            --web-url)    WEB_URL="${2:?--web-url needs a URL}"; WEB_URL_SET=1; shift 2 ;;
            --port)       API_PORT="${2:?--port needs a port}"; API_PORT_SET=1; shift 2 ;;
            --hub-addr)   HUB_PUBLIC_ADDR="${2:?--hub-addr needs host:port}"; HUB_ADDR_SET=1; shift 2 ;;
            --capacity)   HUB_CAPACITY="${2:?--capacity needs a number}"; CAPACITY_SET=1; shift 2 ;;
            --redis-bind) valid_ip "${2:?--redis-bind needs an address}" || die "--redis-bind needs an IP address, got: $2"
                          [ "$REDIS_BIND_SET" -eq 1 ] || REDIS_BIND=""
                          REDIS_BIND="$(uniq_words $REDIS_BIND "$2")"; REDIS_BIND_SET=1; shift 2 ;;
            --redis-host) valid_host "${2:?--redis-host needs a name}" || die "--redis-host needs a DNS name or IPv4, got: $2"
                          [ "$REDIS_HOSTS_SET" -eq 1 ] || REDIS_HOSTS=""
                          REDIS_HOSTS="$(uniq_words $REDIS_HOSTS "$2")"; REDIS_HOSTS_SET=1; shift 2 ;;
            --version)    RELEASE_VERSION="${2:?--version needs a tag}"; shift 2 ;;
            --r2-bucket)  R2_BUCKET="${2:?--r2-bucket needs a name}"; shift 2 ;;
            --yes)        ASSUME_YES=1; shift ;;
            --dry-run)    DRY_RUN=1; shift ;;
            --uninstall)  UNINSTALL=1; shift ;;
            -h|--help)    usage; exit 0 ;;
            *) die "unknown option: $1 (see --help)" ;;
        esac
    done

    printf '%s' "$R2_BUCKET" | grep -qE '^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$' \
        || die "invalid --r2-bucket: ${R2_BUCKET}"
    if [ -n "$RELEASE_VERSION" ]; then
        case "$RELEASE_VERSION" in v*) ;; *) RELEASE_VERSION="v${RELEASE_VERSION}" ;; esac
        printf '%s' "$RELEASE_VERSION" | grep -qE '^v[0-9][0-9A-Za-z.-]*$' \
            || die "invalid --version: ${RELEASE_VERSION}"
        CHANNEL="$RELEASE_VERSION"
    fi

    # Nothing below uses relative paths; runuser -u postgres would warn about
    # a caller's cwd it cannot enter.
    cd /

    if [ "$UNINSTALL" -eq 1 ]; then
        if [ "$DRY_RUN" -eq 0 ]; then
            [ "$(id -u)" -eq 0 ] || die "must run as root (try: sudo $0 --uninstall)"
            command -v systemctl >/dev/null 2>&1 || die "systemd is required (systemctl not found)"
        fi
        uninstall
        exit 0
    fi

    require_r2_env
    is_debian_like || die "this installer supports Debian or Ubuntu only (no Debian-family ${INSTALL_ROOT}/etc/os-release)"

    if [ "$DRY_RUN" -eq 0 ]; then
        [ "$(id -u)" -eq 0 ] || die "must run as root (try: sudo -E $0)"
        command -v systemctl >/dev/null 2>&1 || die "systemd is required (systemctl not found)"
        command -v apt-get >/dev/null 2>&1 || die "apt-get is required"
        [ ! -f "${CONFIG_DIR}/winterflow.env" ] \
            || die "a standalone WinterFlow install exists on this host (${CONFIG_DIR}/winterflow.env); use a separate host for the control plane"
    fi

    FQDN="$(hostname -f 2>/dev/null || true)"
    valid_host "$FQDN" || FQDN="$(hostname 2>/dev/null || true)"
    valid_host "$FQDN" || FQDN="localhost"

    load_state
    WEB_URL="${WEB_URL:-https://${FQDN}}"
    API_PORT="${API_PORT:-8080}"
    HUB_PUBLIC_ADDR="${HUB_PUBLIC_ADDR:-${FQDN}:50051}"
    HUB_CAPACITY="${HUB_CAPACITY:-1000}"
    CPU_QUOTA="$(default_cpu_quota)"
    MEMORY_MAX="$(default_memory_max)"

    sysd_ver="$(systemctl --version 2>/dev/null | awk 'NR==1{print $2; exit}')"
    if [ -n "${sysd_ver:-}" ] && [ "$sysd_ver" -ge 245 ] 2>/dev/null; then
        HAS_LOG_NAMESPACE=1
    fi

    # --- Dry run --------------------------------------------------------------

    if [ "$DRY_RUN" -eq 1 ]; then
        check_settings
        detect_arch
        log "Dry run: printing generated files and commands, changing nothing."
        echo
        echo "### ${STATE_FILE}"
        render_state
        echo
        echo "### ${API_ENV_FILE}"
        render_api_env
        echo
        echo "### ${HUB_ENV_FILE}"
        render_hub_env
        echo
        echo "### ${REDIS_CONF}"
        render_redis_conf
        echo
        echo "### ${REDIS_DROPIN}"
        render_redis_dropin
        if [ "$HAS_LOG_NAMESPACE" -eq 1 ]; then
            echo
            echo "### ${JOURNAL_CONF}"
            render_journald_conf
        fi
        echo
        echo "### ${API_UNIT}"
        render_api_unit
        echo
        echo "### ${HUB_UNIT}"
        render_hub_unit
        echo
        echo "### download"
        print_r2_fetch SHA256SUMS
        print_r2_fetch "winterflow-api-linux-${RELEASE_ARCH}"
        print_r2_fetch "winterflow-hub-linux-${RELEASE_ARCH}"
        echo "sha256sum -c (each binary against SHA256SUMS)"
        echo
        echo "### plan"
        render_plan
        echo
        echo "### finish"
        render_finish
        exit 0
    fi

    # --- Piped install is non-interactive -------------------------------------

    if [ ! -t 0 ] && [ "$ASSUME_YES" -eq 0 ]; then
        echo
        log "Piped install (no terminal on stdin): using defaults, no questions."
        echo "    web UI ${WEB_URL}, API port ${API_PORT}, hub ${HUB_PUBLIC_ADDR} (capacity ${HUB_CAPACITY});"
        echo "    PostgreSQL and Redis are installed if missing. Pass options to change them (see --help)."
        echo
        ASSUME_YES=1
    fi

    # --- Settings -------------------------------------------------------------

    if [ "$ASSUME_YES" -eq 0 ]; then
        ask_settings
    fi
    check_settings

    # --- Preflight ------------------------------------------------------------

    check_curl
    command -v runuser >/dev/null 2>&1 || die "runuser (util-linux) is required"
    ensure_packages
    download_binaries

    # --- Kept secrets ---------------------------------------------------------

    # Generated once; written before anything uses them, so a failed run
    # never loses a password a database role already has.
    [ -n "$POSTGRES_PASSWORD" ] || POSTGRES_PASSWORD="$(random_hex)"
    [ -n "$REDIS_PASSWORD" ] || REDIS_PASSWORD="$(random_hex)"
    [ -n "$JWT_SECRET" ] || JWT_SECRET="$(random_hex)"
    [ -d "$CONFIG_DIR" ] || install -d -m 755 -o root -g root "$CONFIG_DIR"
    put_file "$STATE_FILE" 600 root root < <(render_state)
    log "Settings and secrets kept in ${STATE_FILE}"

    # --- Users, directories, binaries -----------------------------------------

    install -d -m 755 -o root -g root "$DATA_DIR"
    ensure_system_user "$API_USER" "$API_HOME"
    ensure_system_user "$HUB_USER" "$HUB_HOME"
    install -d -m 750 -o "$API_USER" -g "$API_USER" "$API_HOME" "$API_HOME/avatars"
    install -d -m 750 -o "$HUB_USER" -g "$HUB_USER" "$HUB_HOME"

    log "Installing ${API_DEST} and ${HUB_DEST}"
    install -m 755 -o root -g root "${WORK_DIR}/winterflow-api-linux-${RELEASE_ARCH}" "$API_DEST"
    install -m 755 -o root -g root "${WORK_DIR}/winterflow-hub-linux-${RELEASE_ARCH}" "$HUB_DEST"

    # --- Certificates, Redis, Postgres ----------------------------------------

    log "Issuing certificates in ${PKI_DIR} (the CA is kept once it exists)"
    issue_pki
    start_redis
    provision_postgres

    # --- Config and units -----------------------------------------------------

    log "Writing ${API_ENV_FILE} and ${HUB_ENV_FILE}"
    put_file "$API_ENV_FILE" 600 root root < <(render_api_env)
    put_file "$HUB_ENV_FILE" 600 root root < <(render_hub_env)

    if [ "$HAS_LOG_NAMESPACE" -eq 1 ]; then
        if [ -f "$JOURNAL_CONF" ]; then
            log "Keeping existing journal rotation policy ${JOURNAL_CONF}"
        else
            put_file "$JOURNAL_CONF" 644 root root < <(render_journald_conf)
        fi
        systemctl try-restart "systemd-journald@${JOURNAL_NAMESPACE}.service" 2>/dev/null || true
    else
        warn "systemd ${sysd_ver:-unknown} < 245: logs go to the shared system journal"
    fi

    log "Writing ${API_UNIT} and ${HUB_UNIT}"
    put_file "$API_UNIT" 644 root root < <(render_api_unit)
    put_file "$HUB_UNIT" 644 root root < <(render_hub_unit)
    systemctl daemon-reload

    # --- Enable + start -------------------------------------------------------

    log "Enabling and (re)starting winterflow-api and winterflow-hub"
    systemctl enable winterflow-api winterflow-hub
    # restart, not enable --now: a rerun must load the new binaries.
    systemctl restart winterflow-api winterflow-hub
    sleep 3
    local journal_flags=""
    [ "$HAS_LOG_NAMESPACE" -eq 0 ] || journal_flags="--namespace ${JOURNAL_NAMESPACE} "
    for w in winterflow-api winterflow-hub; do
        systemctl is-active --quiet "$w" \
            || die "${w} did not come up; inspect with: journalctl ${journal_flags}-u ${w} -e"
    done
    log "winterflow-api and winterflow-hub are running."

    echo
    render_finish
    echo
    echo "Logs:   journalctl ${journal_flags}-u winterflow-api -u winterflow-hub -f"
    echo "Kept:   ${STATE_FILE} (secrets), ${PKI_DIR} (the CA): back them up with the database."
}

main "$@"
