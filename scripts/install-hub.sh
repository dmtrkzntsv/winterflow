#!/usr/bin/env bash
#
# Install a WinterFlow hub (the gRPC endpoint agents dial over mTLS) on its
# own Debian or Ubuntu host, joined to an existing control plane.
#
# A hub holds no data. It talks to the control plane's Redis bus (TLS, as
# its own Redis user) and to agents (mTLS). This script:
#   - downloads winterflow-hub from the release mirror (Cloudflare R2, private:
#     needs WINTERFLOW_R2_ENDPOINT, WINTERFLOW_R2_ACCESS_KEY_ID and
#     WINTERFLOW_R2_SECRET_ACCESS_KEY in the environment) and verifies it
#     against SHA256SUMS,
#   - makes the hub's private key here (it never leaves this machine), sends
#     a certificate request with the one-time join token to the control
#     plane's API and stores the certificate and the CA it gets back, with
#     the hub's own Redis user (hub:<hub id>) and its password,
#   - installs a hardened systemd unit winterflow-hub, run by the system user
#     winterflow-hub.
#
# It does not touch the firewall, Caddy, or any global configuration. Open
# the hub's TCP port yourself; the installer prints which.
#
# Get the join token and the Redis address from the control plane's operator
# (tokens are single use and expire after an hour). The Redis user and
# password come from the join: no Redis password is ever given to a hub
# machine by hand, and the control plane's admin password never leaves it.
# Settings are kept in /etc/winterflow/hub.env (root only: it holds the
# hub's Redis password).
#
# A rerun on a joined hub is an update: no token needed, the certificate, the
# Redis credentials and settings are kept (options you pass override the
# kept settings), the binary is replaced and the service restarted. A rerun
# with a token joins again: a new key and certificate replace the old pair,
# the control plane retires the hub's older certificates (a leaked key) and
# resets the hub's Redis password (the new one replaces the kept one). A
# certificate from before the hub marker (no winterflow-hub: URI, which
# agents now require), or a hub without a kept Redis user, must join again
# this way: such a rerun without a token stops and asks for one. Retiring the
# hub on the control plane (winterflow-api hub-retire) also deletes its Redis
# user. Stopping the service drains the hub first: it tells its agents to
# move to other hubs and waits up to 2 minutes for them to leave (not at all
# when no other hub can take them).
#
# Logs go to the journald namespace "winterflow":
#   journalctl --namespace winterflow -u winterflow-hub -f
#
# Usage:
#   export WINTERFLOW_R2_ENDPOINT=https://<account_id>.r2.cloudflarestorage.com
#   export WINTERFLOW_R2_ACCESS_KEY_ID=... WINTERFLOW_R2_SECRET_ACCESS_KEY=...
#   sudo -E bash install-hub.sh --token wfh_... --public-addr hub-02.example.com:50051 \
#       --redis-addr cp.example.com:6380
#   # or keep the token out of the process list:
#   export WINTERFLOW_HUB_TOKEN=wfh_... WINTERFLOW_HUB_REDIS_ADDR=cp.example.com:6380
#   sudo -E bash install-hub.sh --public-addr hub-02.example.com:50051
#   sudo -E bash install-hub.sh                      # update a joined hub
#
# Options:
#   --token TOKEN        One-time join token (wfh_...). Required on the first
#                        install; on a rerun it joins again with a new key.
#                        WINTERFLOW_HUB_TOKEN in the environment works too and
#                        keeps it out of the process list.
#   --public-addr H:P    Address agents dial, host:port (a DNS name or IPv4).
#                        Its host goes into the hub's certificate and is the
#                        hub's id. Required on the first install; cannot change
#                        later (the certificate is for that name).
#   --redis-addr H:P     The control plane's Redis, host:port (always TLS,
#                        checked against the CA the join returns). Required on
#                        the first install. WINTERFLOW_HUB_REDIS_ADDR in the
#                        environment works too. The hub's Redis user and
#                        password come from the join.
#   --capacity N         Agents this hub should carry (default: kept, else 1000).
#   --listen-port PORT   Port to listen on when it differs from the public
#                        address's port, e.g. behind a port forward (default:
#                        kept, else the public address's port).
#   --api-url URL        Control plane API to join (default:
#                        https://api.winterflow.dev). Used on the first install.
#   --version TAG        Release to download instead of latest, e.g. v260823.1.
#   --r2-bucket NAME     R2 bucket holding the release mirror (default: econumo).
#   --yes                Non-interactive: assume "yes" to questions.
#   --dry-run            Print every file and command that would be used and
#                        exit without changing anything. Does not require root.
#   --uninstall          Stop (draining first) and remove the winterflow-hub unit
#                        and binary. Keeps the certificates and
#                        /etc/winterflow/hub.env, so a rerun brings the hub back.
#   --purge              With --uninstall: also delete the certificates, hub.env,
#                        the data directory and the winterflow-hub user. The hub
#                        must then join again with a new token.
#   -h, --help           Show this help.

set -euo pipefail

# WF_INSTALL_ROOT prefixes every path below, so tests can run the installer
# against a scratch directory. Unset in real use.
INSTALL_ROOT="${WF_INSTALL_ROOT:-}"

CONFIG_DIR="${INSTALL_ROOT}/etc/winterflow"
ENV_FILE="${CONFIG_DIR}/hub.env"
HUB_HOME="${INSTALL_ROOT}/var/lib/winterflow-hub"
CERT_DIR="${HUB_HOME}/certs"
HUB_DEST="${INSTALL_ROOT}/usr/local/bin/winterflow-hub"
HUB_UNIT="${INSTALL_ROOT}/etc/systemd/system/winterflow-hub.service"
JOURNAL_NAMESPACE="winterflow"
JOURNAL_CONF="${INSTALL_ROOT}/etc/systemd/journald@${JOURNAL_NAMESPACE}.conf"
GITHUB_REPO="dmtrkzntsv/winterflow"
HUB_USER="winterflow-hub"
R2_PROJECT="winterflow"
DEFAULT_API_URL="https://api.winterflow.dev"

JOIN_TOKEN="${WINTERFLOW_HUB_TOKEN:-}"
REDIS_ADDR_ARG="${WINTERFLOW_HUB_REDIS_ADDR:-}"
API_URL="$DEFAULT_API_URL"
HUB_PUBLIC_ADDR=""
HUB_CAPACITY=""
HUB_PORT=""
REDIS_ADDR=""
# The hub's own Redis user (hub:<hub id>) and password: from the join, kept
# in hub.env.
REDIS_USERNAME=""
REDIS_PASSWORD=""
R2_BUCKET="econumo"
RELEASE_VERSION=""
CHANNEL="latest"
ASSUME_YES=0
DRY_RUN=0
UNINSTALL=0
PURGE=0
JOINED=0
REJOIN=0
ENROLL_JOINED=0
CPU_QUOTA=""
MEMORY_MAX=""

HAS_LOG_NAMESPACE=0
sysd_ver=""
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
        echo "WinterFlow hub installer."
        echo "Options: https://github.com/${GITHUB_REPO}/blob/main/scripts/install-hub.sh"
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

valid_hostport() {
    local host="${1%:*}" port="${1##*:}"
    [ "$host" != "$1" ] && valid_host "$host" && valid_port "$port"
}

valid_capacity() {
    printf '%s' "$1" | grep -qE '^[1-9][0-9]{0,6}$'
}

valid_token() {
    printf '%s' "$1" | grep -qE '^wfh_[A-Za-z0-9_-]{16,128}$'
}

valid_password() {
    # What the env file can carry unquoted (the join's are base64url).
    printf '%s' "$1" | grep -qE '^[A-Za-z0-9._~-]{1,256}$'
}

valid_redis_user() {
    # A hub's Redis user: hub:<hub id>, the id as the control plane spells it.
    printf '%s' "$1" | grep -qE '^hub:[a-z0-9.-]{1,253}$'
}

hub_id_of() {
    # hub_id_of HOST:PORT — the hub id: the host, lower-cased.
    printf '%s' "${1%:*}" | tr '[:upper:]' '[:lower:]'
}

valid_api_url() {
    # https, or http to this machine (tests, a local control plane).
    case "$1" in
        https://?*) ;;
        http://localhost|http://localhost[:/]*|http://127.0.0.1|http://127.0.0.1[:/]*) ;;
        *) return 1 ;;
    esac
    printf '%s' "$1" | grep -qE '^https?://[A-Za-z0-9.-]+(:[0-9]{1,5})?(/[A-Za-z0-9._~/-]*)?$'
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

# --- Kept settings (/etc/winterflow/hub.env) ----------------------------------

env_value() {
    # env_value KEY -> the kept value (last one wins), empty when absent. The
    # file is parsed, never sourced: each value is validated by its user.
    [ -f "$ENV_FILE" ] || return 0
    sed -n "s/^$1=//p" "$ENV_FILE" | tail -n 1
}

load_kept() {
    # Fill in what the command line did not set from the kept env file.
    local v
    if [ -f "$ENV_FILE" ] && [ ! -r "$ENV_FILE" ]; then
        die "cannot read ${ENV_FILE} (run as root)"
    fi
    if [ -z "$HUB_PUBLIC_ADDR" ]; then
        v="$(env_value HUB_PUBLIC_ADDR)"
        [ -z "$v" ] || { valid_hostport "$v" || die "${ENV_FILE}: HUB_PUBLIC_ADDR is not host:port"; HUB_PUBLIC_ADDR="$v"; }
    fi
    if [ -z "$HUB_CAPACITY" ]; then
        v="$(env_value HUB_CAPACITY)"
        [ -z "$v" ] || { valid_capacity "$v" || die "${ENV_FILE}: HUB_CAPACITY is not a number"; HUB_CAPACITY="$v"; }
    fi
    if [ -z "$HUB_PORT" ]; then
        v="$(env_value HUB_PORT)"
        [ -z "$v" ] || { valid_port "$v" || die "${ENV_FILE}: HUB_PORT is not a port"; HUB_PORT="$v"; }
    fi
    if [ -z "$REDIS_ADDR" ]; then
        v="$(env_value REDIS_ADDR)"
        [ -z "$v" ] || { valid_hostport "$v" || die "${ENV_FILE}: REDIS_ADDR is not host:port"; REDIS_ADDR="$v"; }
    fi
    v="$(env_value REDIS_USERNAME)"
    [ -z "$v" ] || { valid_redis_user "$v" || die "${ENV_FILE}: REDIS_USERNAME is not a hub's Redis user (hub:<hub id>)"; REDIS_USERNAME="$v"; }
    v="$(env_value REDIS_PASSWORD)"
    [ -z "$v" ] || { valid_password "$v" || die "${ENV_FILE}: REDIS_PASSWORD has unsupported characters"; REDIS_PASSWORD="$v"; }
    # The kept user must be this hub's (hub:<its id>); another hub's
    # credentials are dropped, so the hub has to join again for its own.
    if [ -n "$REDIS_USERNAME" ] && [ -n "$HUB_PUBLIC_ADDR" ] \
        && [ "$REDIS_USERNAME" != "hub:$(hub_id_of "$HUB_PUBLIC_ADDR")" ]; then
        warn "${ENV_FILE}: REDIS_USERNAME ${REDIS_USERNAME} is not this hub's Redis user (hub:$(hub_id_of "$HUB_PUBLIC_ADDR")); ignoring the kept Redis credentials"
        REDIS_USERNAME=""
        REDIS_PASSWORD=""
    fi
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
    [ "$(uname -s)" = Linux ] || die "the hub runs on Linux only"
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

download_binary() {
    # winterflow-hub for this arch into WORK_DIR, verified against the
    # mirror's SHA256SUMS.
    local asset
    detect_arch
    WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/winterflow-install.XXXXXX")"
    log "Downloading SHA256SUMS from $(r2_url "")"
    r2_fetch SHA256SUMS "${WORK_DIR}/SHA256SUMS" \
        || die "download failed: $(r2_url SHA256SUMS) (wrong credentials, bucket, or version?)"
    asset="winterflow-hub-linux-${RELEASE_ARCH}"
    log "Downloading ${asset}"
    r2_fetch "$asset" "${WORK_DIR}/${asset}" || die "download failed: $(r2_url "$asset")"
    (cd "$WORK_DIR" && grep -E "[[:space:]][*]?${asset}\$" SHA256SUMS | sha256sum -c --quiet -) \
        || die "checksum mismatch for ${asset}: corrupted or tampered download"
    chmod 755 "${WORK_DIR}/${asset}"
    log "Checksum verified"
}

# --- Join (certificate) -------------------------------------------------------

# hub_cert_marked FILE: true when the first certificate in FILE carries the
# winterflow-hub: URI SAN agents require. The URI is plain bytes in the DER,
# so decoding the PEM and counting it is enough (no openssl needed; grep -c
# reads all of its input, so pipefail sees no SIGPIPE).
hub_cert_marked() {
    local n
    n="$(awk '/-----BEGIN CERTIFICATE-----/ { on = 1; next } /-----END CERTIFICATE-----/ { exit } on' "$1" 2>/dev/null \
        | base64 -d 2>/dev/null | grep -acF 'winterflow-hub:')" || true
    [ "${n:-0}" -gt 0 ]
}

checked_redis_env() {
    # checked_redis_env FILE — sets REDIS_USERNAME and REDIS_PASSWORD from the
    # join's --redis-env-out file. It carries what the control plane sent,
    # so root takes exactly the two keys with plain values: this hub's user
    # and a password the env file can hold unquoted. Anything else, a missing
    # or a repeated key fails the whole file (ENROLL_ERR says why).
    local file="$1" content line key val seen=" " user="" pass=""
    ENROLL_ERR="refusing the join's Redis credentials"
    if [ ! -f "$file" ] || [ -L "$file" ]; then
        ENROLL_ERR="${ENROLL_ERR}: ${file} is missing or not a regular file"
        return 1
    fi
    content="$(cat "$file")" || return 1
    while IFS= read -r line || [ -n "$line" ]; do
        [ -n "$line" ] || continue
        key="${line%%=*}"
        val="${line#*=}"
        case "$seen" in *" ${key} "*) ENROLL_ERR="${ENROLL_ERR}: ${key} repeated"; return 1 ;; esac
        seen="${seen}${key} "
        case "$key" in
            REDIS_USERNAME)
                [ "$val" = "hub:$(hub_id_of "$HUB_PUBLIC_ADDR")" ] \
                    || { ENROLL_ERR="${ENROLL_ERR}: the user is not this hub's"; return 1; }
                user="$val" ;;
            REDIS_PASSWORD)
                valid_password "$val" || { ENROLL_ERR="${ENROLL_ERR}: invalid password"; return 1; }
                pass="$val" ;;
            *) ENROLL_ERR="${ENROLL_ERR}: unexpected line"; return 1 ;;
        esac
    done <<<"$content"
    if [ -z "$user" ] || [ -z "$pass" ]; then
        ENROLL_ERR="${ENROLL_ERR}: REDIS_USERNAME or REDIS_PASSWORD missing"
        return 1
    fi
    REDIS_USERNAME="$user"
    REDIS_PASSWORD="$pass"
}

# enroll_once: one `winterflow-hub join`. Sets ENROLL_ERR and returns the
# status on failure; the caller decides whether that ends the install.
enroll_once() {
    # Make (or keep) the key and redeem the token: `winterflow-hub join`
    # posts the request, checks that the certificate matches the key and
    # chains to the CA, and stores ca.crt and hub.crt, then writes the hub's
    # Redis user and password to a file in the root-only work dir (merged
    # into hub.env after checks). The token reaches it on stdin, never a
    # command line.
    local st=0 out redis_env="${WORK_DIR}/redis.env"
    ENROLL_JOINED=0
    rm -f "$redis_env"
    out="$(printf '%s\n' "$JOIN_TOKEN" | "$HUB_DEST" join --api-url "$API_URL" \
        --public-addr "$HUB_PUBLIC_ADDR" --cert-dir "$CERT_DIR" --redis-env-out "$redis_env" 2>&1)" || st=$?
    case "$st" in
        0) ;;
        2) ENROLL_ERR="${out} (a token works once and expires after an hour; ask for a new one)"; return 1 ;;
        *) ENROLL_ERR="joining the control plane failed: ${out}"; return 1 ;;
    esac
    # The join went through: the new pair is stored and the control plane
    # has reset the hub's Redis password.
    ENROLL_JOINED=1
    chown root:"$HUB_USER" "${CERT_DIR}/hub.key" "${CERT_DIR}/hub.crt" "${CERT_DIR}/ca.crt"
    chmod 640 "${CERT_DIR}/hub.key"
    log "$out"
    if ! checked_redis_env "$redis_env"; then
        rm -f "$redis_env"
        ENROLL_ERR="${ENROLL_ERR} (the token is spent: join again with a new one)"
        return 1
    fi
    rm -f "$redis_env"
}

enroll() {
    enroll_once || die "$ENROLL_ERR"
}

# rejoin: a running hub announces this host's hub id, which the control plane
# refuses to reissue while it is live (the refusal does not spend the token).
# Stop the hub first (stopping drains it and withdraws its entry), join, and
# start it again whatever the outcome, so the old certificate keeps serving
# when the join fails. A withdraw that did not happen expires within 90 s, so
# a "live hub" refusal is retried for up to 100 s.
rejoin() {
    local tries=0
    log "Stopping winterflow-hub (the control plane refuses a new certificate while it is live)"
    systemctl stop winterflow-hub 2>/dev/null || true
    while ! enroll_once; do
        case "$ENROLL_ERR" in
            *"a live hub already uses this host"*)
                if [ "$tries" -lt 10 ]; then
                    tries=$((tries + 1))
                    log "The hub's entry has not expired yet; retrying in 10 s (${tries}/10)"
                    sleep 10
                    continue
                fi ;;
        esac
        if [ "$ENROLL_JOINED" -eq 1 ]; then
            # The join replaced the certificate and reset the Redis password,
            # but its credentials were refused: the kept password is stale.
            log "winterflow-hub's certificate was replaced, but the join's Redis credentials were refused and its previous Redis password no longer works; starting winterflow-hub again (it cannot reach Redis until you join again with a new token)"
        else
            log "Starting winterflow-hub again with its previous certificate"
        fi
        systemctl start winterflow-hub 2>/dev/null || true
        die "$ENROLL_ERR"
    done
}

# --- Renderers (shared by install and --dry-run) ------------------------------

secret_shown() {
    # The Redis password in a real run; never in a dry run (where a join
    # would bring a new one).
    if [ "$DRY_RUN" -eq 0 ]; then
        printf '%s' "$REDIS_PASSWORD"
    elif [ "$JOINED" -eq 0 ] || [ "$REJOIN" -eq 1 ]; then
        printf '<from the join>'
    else
        printf '<hidden>'
    fi
}

render_env() {
    cat <<EOF
# WinterFlow hub (written by install-hub.sh; a rerun rewrites it from these
# values and your options). Root only: holds the hub's Redis password (its
# own user, from the join; a rejoin replaces it).
LOG_LEVEL=info
HUB_PUBLIC_ADDR=${HUB_PUBLIC_ADDR}
HUB_PORT=${HUB_PORT}
HUB_CAPACITY=${HUB_CAPACITY}
HUB_CERT_DIR=${CERT_DIR}
REDIS_ADDR=${REDIS_ADDR}
REDIS_USERNAME=${REDIS_USERNAME:-hub:$(hub_id_of "$HUB_PUBLIC_ADDR")}
REDIS_PASSWORD=$(secret_shown)
REDIS_TLS=true
REDIS_CA_CERT_PATH=${CERT_DIR}/ca.crt
EOF
}

render_journald_conf() {
    cat <<EOF
# Rotation policy for the '${JOURNAL_NAMESPACE}' journal namespace
# (written by install-hub.sh; applies only to winterflow logs, never the
# system journal).
#
# View:    journalctl --namespace ${JOURNAL_NAMESPACE} -u winterflow-hub -f
# Delete:  journalctl --namespace ${JOURNAL_NAMESPACE} --vacuum-time=1s
[Journal]
Storage=persistent
SystemMaxUse=200M
SystemMaxFileSize=20M
MaxRetentionSec=1month
EOF
}

render_hub_unit() {
    local caps="CapabilityBoundingSet=" log_ns_line=""
    if [ "$((10#${HUB_PORT}))" -lt 1024 ]; then
        caps="AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE"
    fi
    if [ "$HAS_LOG_NAMESPACE" -eq 1 ]; then
        log_ns_line="LogNamespace=${JOURNAL_NAMESPACE}"
    fi
    cat <<EOF
[Unit]
Description=WinterFlow hub (agents connect here over mTLS)
Documentation=https://github.com/${GITHUB_REPO}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${HUB_USER}
Group=${HUB_USER}
EnvironmentFile=${ENV_FILE}
WorkingDirectory=${HUB_HOME}
ExecStart=${HUB_DEST}
Restart=on-failure
RestartSec=5
# Stopping drains the hub first: it tells its agents to move to other hubs
# and waits up to 2 minutes for them to leave (not at all when no other hub
# can take them), then stops gracefully.
TimeoutStopSec=150
${caps}

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
ReadWritePaths=${HUB_HOME}
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true

[Install]
WantedBy=multi-user.target
EOF
}

render_plan() {
    echo "user: ${HUB_USER} (system user, no login shell)"
    echo "install ${HUB_DEST} (root, 0755)"
    if [ "$JOINED" -eq 1 ] && [ "$REJOIN" -eq 0 ]; then
        echo "joined: ${CERT_DIR}/hub.crt exists; certificate and key kept, no token needed"
    else
        [ "$REJOIN" -eq 0 ] || echo "rejoin: a new hub.key and hub.crt replace the old pair once the join succeeds; the control plane retires this hub's older certificates"
        echo "${HUB_DEST} join --api-url ${API_URL} --public-addr ${HUB_PUBLIC_ADDR} --cert-dir ${CERT_DIR} --redis-env-out <temp file>   (token on stdin, hidden)"
        echo "  POST ${API_URL}/api/v1/hub/enroll {token, csr, public_addr}; hub.key made here and never sent"
        echo "  checks the certificate matches the key and chains to the CA, then stores ca.crt and hub.crt"
        echo "  the answer's Redis user hub:$(hub_id_of "$HUB_PUBLIC_ADDR") and password (reset by every join) go to ${ENV_FILE}"
        echo "  ${CERT_DIR}: root:${HUB_USER} 0750; hub.key 0640"
        [ "$REJOIN" -eq 0 ] || echo "  rejoin: systemctl stop winterflow-hub before the join (the control plane refuses while it is live; retried up to 100 s), systemctl start winterflow-hub if the join fails"
    fi
    echo "systemctl enable winterflow-hub; systemctl restart winterflow-hub (stopping drains first, up to 2 minutes when another hub can take its agents)"
}

render_finish() {
    cat <<EOF
Next steps (this installer does not touch the firewall):
  1. Open TCP ${HUB_PORT} so agents can reach the hub at ${HUB_PUBLIC_ADDR}
     (forward it to this machine's port ${HUB_PORT} if the address is a NAT or proxy).
  2. The hub is available to new agents once it shows up in the control plane
     (a minute at most).
EOF
}

# --- Install steps ------------------------------------------------------------

put_file() {
    # put_file DEST MODE OWNER GROUP < CONTENT — write atomically with the
    # given owner and mode.
    local dest="$1" tmp
    tmp="$(mktemp "$(dirname "$dest")/.wf-install.XXXXXX")"
    cat > "$tmp"
    chown "$3:$4" "$tmp"
    chmod "$2" "$tmp"
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

render_uninstall() {
    # The uninstall steps, printed by --dry-run --uninstall.
    cat <<EOF
systemctl disable --now winterflow-hub   # drains first, up to 2 minutes
rm -f ${HUB_UNIT} ${HUB_DEST}
systemctl daemon-reload
EOF
    if [ "$PURGE" -eq 1 ]; then
        cat <<EOF
rm -rf ${CERT_DIR} ${HUB_HOME}
rm -f ${ENV_FILE}
userdel ${HUB_USER}
EOF
    fi
}

render_kept() {
    if [ "$PURGE" -eq 1 ]; then
        cat <<EOF
purged: the certificates, ${ENV_FILE}, ${HUB_HOME} and the user ${HUB_USER}
kept: the journal namespace '${JOURNAL_NAMESPACE}'
Join again with a new token to bring this hub back.
EOF
    else
        cat <<EOF
kept: ${CERT_DIR} (the hub's key and certificates), ${ENV_FILE}
kept: the user ${HUB_USER} and the journal namespace '${JOURNAL_NAMESPACE}'
A rerun of this installer brings the hub back without a new token.
EOF
    fi
}

uninstall() {
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
    if [ "$PURGE" -eq 1 ]; then
        [ -t 0 ] || [ "$ASSUME_YES" -eq 1 ] || die "--purge deletes the hub's key and settings: pass --yes when there is no terminal to ask"
        confirm "Delete this hub's key, certificates and settings? It must join again with a new token." \
            || die "aborted"
    fi
    log "Stopping winterflow-hub (it drains first, up to 2 minutes) and removing it"
    systemctl disable --now winterflow-hub 2>/dev/null || true
    rm -f "$HUB_UNIT" "$HUB_DEST"
    systemctl daemon-reload
    if [ "$PURGE" -eq 1 ]; then
        # The user goes only when this installer's settings exist (it made it).
        local had_env=0
        [ ! -f "$ENV_FILE" ] || had_env=1
        rm -rf "$CERT_DIR" "$HUB_HOME"
        rm -f "$ENV_FILE"
        if [ "$had_env" -eq 1 ] && id "$HUB_USER" >/dev/null 2>&1; then userdel "$HUB_USER" 2>/dev/null || warn "could not delete the user ${HUB_USER}"; fi
    fi
    log "Removed the service and binary."
    render_kept
}

check_settings() {
    [ -n "$HUB_PUBLIC_ADDR" ] || die "--public-addr host:port is required"
    [ -n "$REDIS_ADDR" ] || die "--redis-addr (or WINTERFLOW_HUB_REDIS_ADDR) host:port is required: the control plane's Redis"
    if [ "$JOINED" -eq 0 ]; then
        [ -n "$JOIN_TOKEN" ] || die "--token is required on the first install (ask the control plane's operator for a hub join token)"
    fi
}

main() {
    local arg_public_addr=""
    while [ $# -gt 0 ]; do
        case "$1" in
            --token)        JOIN_TOKEN="${2:?--token needs a value}"; shift 2 ;;
            --public-addr)  arg_public_addr="${2:?--public-addr needs host:port}"; shift 2 ;;
            --redis-addr)   REDIS_ADDR_ARG="${2:?--redis-addr needs host:port}"; shift 2 ;;
            --redis-url)    die "--redis-url is gone: pass the address only, --redis-addr host:port (the hub's Redis user and password come from the join)" ;;
            --capacity)     HUB_CAPACITY="${2:?--capacity needs a number}"; shift 2 ;;
            --listen-port)  HUB_PORT="${2:?--listen-port needs a port}"; shift 2 ;;
            --api-url)      API_URL="${2:?--api-url needs a URL}"; shift 2 ;;
            --version)      RELEASE_VERSION="${2:?--version needs a tag}"; shift 2 ;;
            --r2-bucket)    R2_BUCKET="${2:?--r2-bucket needs a name}"; shift 2 ;;
            --yes)          ASSUME_YES=1; shift ;;
            --dry-run)      DRY_RUN=1; shift ;;
            --uninstall)    UNINSTALL=1; shift ;;
            --purge)        PURGE=1; shift ;;
            -h|--help)      usage; exit 0 ;;
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
    [ "$PURGE" -eq 0 ] || [ "$UNINSTALL" -eq 1 ] || die "--purge only goes with --uninstall"
    valid_api_url "$API_URL" || die "--api-url must be https:// (http:// only for localhost), got: ${API_URL}"
    API_URL="${API_URL%/}"
    [ -z "${WINTERFLOW_HUB_REDIS_URL:-}" ] \
        || die "WINTERFLOW_HUB_REDIS_URL is gone: set WINTERFLOW_HUB_REDIS_ADDR=host:port instead (the hub's Redis user and password come from the join)"
    if [ -n "$REDIS_ADDR_ARG" ]; then
        valid_hostport "$REDIS_ADDR_ARG" || die "--redis-addr must be host:port (a DNS name or IPv4; TLS is always used), got: ${REDIS_ADDR_ARG}"
        REDIS_ADDR="$REDIS_ADDR_ARG"
    fi
    [ -z "$JOIN_TOKEN" ] || valid_token "$JOIN_TOKEN" || die "the join token must look like wfh_..."
    [ -z "$arg_public_addr" ] || valid_hostport "$arg_public_addr" || die "--public-addr must be host:port (a DNS name or IPv4), got: ${arg_public_addr}"
    [ -z "$HUB_CAPACITY" ] || valid_capacity "$HUB_CAPACITY" || die "--capacity must be a positive number, got: ${HUB_CAPACITY}"
    [ -z "$HUB_PORT" ] || valid_port "$HUB_PORT" || die "--listen-port must be a port number, got: ${HUB_PORT}"

    cd /

    # Before every branch (uninstall and dry runs included): the control plane
    # and the standalone install use the same unit and binary names.
    [ ! -f "${CONFIG_DIR}/winterflow.env" ] \
        || die "a standalone WinterFlow install exists on this host (${CONFIG_DIR}/winterflow.env); this installer is for hub-only hosts"
    [ ! -f "${CONFIG_DIR}/control-plane.env" ] \
        || die "a control plane is installed on this host (${CONFIG_DIR}/control-plane.env); it runs its own hub, and this installer is for hub-only hosts"

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
    fi

    if [ -f "${CERT_DIR}/hub.crt" ] && [ -f "${CERT_DIR}/hub.key" ] && [ -f "${CERT_DIR}/ca.crt" ]; then
        JOINED=1
    fi

    load_kept
    if [ -n "$arg_public_addr" ]; then
        if [ "$JOINED" -eq 1 ] && [ -n "$HUB_PUBLIC_ADDR" ] && [ "${arg_public_addr%:*}" != "${HUB_PUBLIC_ADDR%:*}" ]; then
            die "this hub's certificate is for ${HUB_PUBLIC_ADDR%:*}; to change the host, uninstall with --purge and join again with a new token"
        fi
        HUB_PUBLIC_ADDR="$arg_public_addr"
    fi
    check_settings
    HUB_CAPACITY="${HUB_CAPACITY:-1000}"
    HUB_PORT="${HUB_PORT:-${HUB_PUBLIC_ADDR##*:}}"
    CPU_QUOTA="$(default_cpu_quota)"
    MEMORY_MAX="$(default_memory_max)"
    if [ "$JOINED" -eq 1 ]; then
        if [ -n "$JOIN_TOKEN" ]; then
            # A token on a rerun reissues the key and certificate.
            REJOIN=1
        elif ! hub_cert_marked "${CERT_DIR}/hub.crt"; then
            die "this hub's certificate (${CERT_DIR}/hub.crt) has no winterflow-hub: URI, which agents now require; a join token is required to reissue it: ask the control plane's operator for a hub join token and rerun with --token (or WINTERFLOW_HUB_TOKEN)"
        elif [ -z "$REDIS_USERNAME" ] || [ -z "$REDIS_PASSWORD" ]; then
            die "${ENV_FILE} has no Redis user for this hub (it joined before hubs had their own Redis users); join again: ask the control plane's operator for a hub join token and rerun with --token (or WINTERFLOW_HUB_TOKEN)"
        fi
    fi

    sysd_ver="$(systemctl --version 2>/dev/null | awk 'NR==1{print $2; exit}')"
    if [ -n "${sysd_ver:-}" ] && [ "$sysd_ver" -ge 245 ] 2>/dev/null; then
        HAS_LOG_NAMESPACE=1
    fi

    # --- Dry run --------------------------------------------------------------

    if [ "$DRY_RUN" -eq 1 ]; then
        detect_arch
        log "Dry run: printing generated files and commands, changing nothing."
        echo
        echo "### ${ENV_FILE}"
        render_env
        if [ "$HAS_LOG_NAMESPACE" -eq 1 ]; then
            echo
            echo "### ${JOURNAL_CONF}"
            render_journald_conf
        fi
        echo
        echo "### ${HUB_UNIT}"
        render_hub_unit
        echo
        echo "### download"
        print_r2_fetch SHA256SUMS
        print_r2_fetch "winterflow-hub-linux-${RELEASE_ARCH}"
        echo "sha256sum -c (the binary against SHA256SUMS)"
        echo
        echo "### plan"
        render_plan
        echo
        echo "### finish"
        render_finish
        exit 0
    fi

    # --- Preflight and download (before the token is spent) -------------------

    check_curl
    download_binary

    # --- User, directories, binary --------------------------------------------

    ensure_system_user "$HUB_USER" "$HUB_HOME"
    install -d -m 750 -o "$HUB_USER" -g "$HUB_USER" "$HUB_HOME"
    install -d -m 750 -o root -g "$HUB_USER" "$CERT_DIR"
    [ -d "$CONFIG_DIR" ] || install -d -m 755 -o root -g root "$CONFIG_DIR"

    log "Installing ${HUB_DEST}"
    install -m 755 -o root -g root "${WORK_DIR}/winterflow-hub-linux-${RELEASE_ARCH}" "$HUB_DEST"

    # --- Join -----------------------------------------------------------------

    if [ "$JOINED" -eq 0 ]; then
        enroll
    elif [ "$REJOIN" -eq 1 ]; then
        log "Joining again with the token: a new key and certificate replace the old pair"
        rejoin
    else
        log "Already joined (${CERT_DIR}/hub.crt): keeping the certificate"
    fi

    # --- Config and unit ------------------------------------------------------

    log "Writing ${ENV_FILE}"
    put_file "$ENV_FILE" 600 root root < <(render_env)

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

    log "Writing ${HUB_UNIT}"
    put_file "$HUB_UNIT" 644 root root < <(render_hub_unit)
    systemctl daemon-reload

    # --- Enable + start -------------------------------------------------------

    log "Enabling and (re)starting winterflow-hub"
    systemctl enable winterflow-hub
    # restart, not enable --now: a rerun must load the new binary (and the
    # stop half drains the running hub first).
    systemctl restart winterflow-hub
    sleep 3
    local journal_flags=""
    [ "$HAS_LOG_NAMESPACE" -eq 0 ] || journal_flags="--namespace ${JOURNAL_NAMESPACE} "
    systemctl is-active --quiet winterflow-hub \
        || die "winterflow-hub did not come up; inspect with: journalctl ${journal_flags}-u winterflow-hub -e"
    log "winterflow-hub is running."

    echo
    render_finish
    echo
    echo "Logs:   journalctl ${journal_flags}-u winterflow-hub -f"
    echo "Kept:   ${ENV_FILE} (the hub's Redis password), ${CERT_DIR} (the hub's key and certificates)."
}

main "$@"
