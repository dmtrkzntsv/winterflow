#!/usr/bin/env bash
# Install the distributed WinterFlow control plane on Debian or Ubuntu.
# API and Hub run as separate systemd services. PostgreSQL and Redis use the
# distribution's native packages and services, bound to this machine.
# Build bin/api with VITE_APP_MODE=distributed and bin/hub before running this.
set -euo pipefail

CONFIG_DIR=/etc/winterflow-distributed
DATA_DIR=/var/lib/winterflow-distributed
API_ENV_FILE=$CONFIG_DIR/api.env
HUB_ENV_FILE=$CONFIG_DIR/hub.env
CERT_DIR=$CONFIG_DIR/certs
API_UNIT=/etc/systemd/system/winterflow-api.service
HUB_UNIT=/etc/systemd/system/winterflow-hub.service
API_DEST=/usr/local/bin/winterflow-api
HUB_DEST=/usr/local/bin/winterflow-hub
API_USER=winterflow-api
HUB_USER=winterflow-hub
DB_NAME=winterflow_distributed
DB_USER=winterflow_distributed
API_PORT=8080
HUB_PORT=50051
HUB_HOST="$(hostname -f 2>/dev/null || hostname)"
WEB_URL=""
API_SRC=""
HUB_SRC=""
DRY_RUN=0
ASSUME_YES=0
HOST_EXPLICIT=0
API_PORT_EXPLICIT=0
HUB_PORT_EXPLICIT=0
WEB_URL_EXPLICIT=0

usage() {
    cat <<'EOF'
Install WinterFlow's distributed API, PostgreSQL, Redis and Hub as services.
Run on Debian/Ubuntu with systemd after building bin/api and bin/hub.

Options:
  --api-binary PATH  Distributed-flavor API binary (default: bin/api in repo)
  --hub-binary PATH  Hub binary (default: bin/hub in repo)
  --hub-host HOST    DNS name or IPv4 address agents use to reach the Hub
  --port PORT        API/web port (default: 8080)
  --hub-port PORT    Hub gRPC port (default: 50051)
  --web-url URL      Public API/web URL (default: http://HOST:PORT)
  --yes              Install missing packages without prompting
  --dry-run          Show configuration and units without changing the host
  -h, --help        Show this help

The installer preserves existing config, database, and CA on reruns. Address
and port options apply only to a fresh install. It does not install an agent
on a managed host. PostgreSQL and Redis are kept on the host if the WinterFlow
services are later removed.
EOF
}

die() { printf 'error: %s\n' "$*" >&2; exit 1; }
log() { printf '==> %s\n' "$*"; }

confirm_install() {
    local name="$1" answer
    if [ "$ASSUME_YES" -eq 1 ]; then
        log "Installing missing $name (--yes)"
        return 0
    fi
    [ -t 0 ] || die "$name is missing; run interactively or pass --yes to install it"
    read -r -p "Install $name now? [y/N] " answer || return 1
    case "$answer" in
        y|Y|yes|YES|Yes) return 0 ;;
        *) return 1 ;;
    esac
}

postgres_installed() {
    command -v psql >/dev/null 2>&1 && systemctl cat postgresql.service >/dev/null 2>&1
}

redis_installed() {
    command -v redis-server >/dev/null 2>&1 &&
        command -v redis-cli >/dev/null 2>&1 &&
        systemctl cat redis-server.service >/dev/null 2>&1
}

ensure_dependencies() {
    local packages=()
    if postgres_installed; then
        log "PostgreSQL is already installed"
    else
        confirm_install PostgreSQL || die "PostgreSQL is required for the distributed API"
        packages+=(postgresql)
    fi
    if redis_installed; then
        log "Redis is already installed"
    else
        confirm_install Redis || die "Redis is required for the distributed bus"
        packages+=(redis-server)
    fi
    if ! command -v openssl >/dev/null 2>&1; then
        confirm_install OpenSSL || die "OpenSSL is required to create Hub certificates"
        packages+=(openssl)
    fi
    if [ "${#packages[@]}" -gt 0 ]; then
        apt-get update -qq
        DEBIAN_FRONTEND=noninteractive apt-get install -y "${packages[@]}"
    fi
    postgres_installed || die "PostgreSQL installation did not provide psql and postgresql.service"
    redis_installed || die "Redis installation did not provide redis-cli and redis-server.service"
    command -v openssl >/dev/null 2>&1 || die "OpenSSL installation failed"
}

valid_port() {
    case "$1" in ''|*[!0-9]*) return 1 ;; esac
    [ "$1" -ge 1 ] && [ "$1" -le 65535 ]
}

render_api_env() {
    local db_password="$1" jwt_secret="$2"
    cat <<EOF
# WinterFlow distributed control plane. Keep this file private.
LOG_LEVEL=info
API_PORT=${API_PORT}
WEB_URL=${WEB_URL}
JWT_SECRET=${jwt_secret}
REGION=local
REGISTRATION_ENABLED=false
DATABASE_URL=postgres://${DB_USER}:${db_password}@127.0.0.1:5432/${DB_NAME}?sslmode=disable
REDIS_ADDR=127.0.0.1:6379
REDIS_PASSWORD=
AVATARS_STORAGE_PATH=${DATA_DIR}/avatars
EOF
}

render_hub_env() {
    cat <<EOF
# WinterFlow distributed Hub. The API's database and JWT secrets are separate.
LOG_LEVEL=info
REGION=local
REDIS_ADDR=127.0.0.1:6379
REDIS_PASSWORD=
HUB_HOST=${HUB_HOST}
HUB_PORT=${HUB_PORT}
HUB_CERT_DIR=${CERT_DIR}
EOF
}

render_api_unit() {
    cat <<EOF
[Unit]
Description=WinterFlow distributed API and web UI
After=network-online.target postgresql.service redis-server.service
Wants=network-online.target
Requires=postgresql.service redis-server.service

[Service]
Type=simple
User=${API_USER}
Group=${API_USER}
EnvironmentFile=${API_ENV_FILE}
WorkingDirectory=${DATA_DIR}/api
ExecStart=${API_DEST}
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=${DATA_DIR}/api ${DATA_DIR}/avatars
ProtectHome=true
PrivateTmp=true
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
}

render_hub_unit() {
    cat <<EOF
[Unit]
Description=WinterFlow distributed agent Hub
After=network-online.target redis-server.service
Wants=network-online.target
Requires=redis-server.service

[Service]
Type=simple
User=${HUB_USER}
Group=${HUB_USER}
EnvironmentFile=${HUB_ENV_FILE}
WorkingDirectory=${DATA_DIR}/hub
ExecStart=${HUB_DEST}
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=${DATA_DIR}/hub
ProtectHome=true
PrivateTmp=true
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
}

create_hub_certificates() {
    # Never rotate an existing CA during a binary upgrade: agents trust it.
    if [ -e "$CERT_DIR/ca.key" ] || [ -e "$CERT_DIR/ca.crt" ] ||
       [ -e "$CERT_DIR/hub.key" ] || [ -e "$CERT_DIR/hub.crt" ]; then
        [ -s "$CERT_DIR/ca.key" ] && [ -s "$CERT_DIR/ca.crt" ] &&
        [ -s "$CERT_DIR/hub.key" ] && [ -s "$CERT_DIR/hub.crt" ] ||
            die "incomplete certificates in $CERT_DIR; repair them before retrying"
        log "Keeping existing Hub CA and certificate"
        return
    fi
    local san
    if [[ "$HUB_HOST" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        san="IP:${HUB_HOST}"
    else
        san="DNS:${HUB_HOST}"
    fi
    umask 077
    openssl ecparam -name prime256v1 -genkey -noout -out "$CERT_DIR/ca.key"
    openssl req -x509 -new -sha256 -days 3650 -key "$CERT_DIR/ca.key" \
        -out "$CERT_DIR/ca.crt" -subj /CN=WinterFlow-CA \
        -addext 'basicConstraints=critical,CA:TRUE'
    openssl ecparam -name prime256v1 -genkey -noout -out "$CERT_DIR/hub.key"
    openssl req -new -key "$CERT_DIR/hub.key" -out "$CERT_DIR/hub.csr" \
        -subj "/CN=${HUB_HOST}"
    cat > "$CERT_DIR/hub.ext" <<EOF
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=${san}
EOF
    openssl x509 -req -sha256 -days 3650 -in "$CERT_DIR/hub.csr" \
        -CA "$CERT_DIR/ca.crt" -CAkey "$CERT_DIR/ca.key" -CAcreateserial \
        -out "$CERT_DIR/hub.crt" -extfile "$CERT_DIR/hub.ext"
    rm -f "$CERT_DIR/hub.csr" "$CERT_DIR/hub.ext"
    chown root:"$HUB_USER" "$CERT_DIR/ca.crt" "$CERT_DIR/hub.crt" "$CERT_DIR/hub.key"
    chmod 640 "$CERT_DIR/hub.key"
    chmod 644 "$CERT_DIR/ca.crt" "$CERT_DIR/hub.crt"
    log "Created Hub CA and certificate for $HUB_HOST"
}

provision_postgres() {
    local password="$1"
    # Names are constants and the generated password is hexadecimal; neither
    # user-supplied SQL nor shell text enters these statements.
    local existing_owner
    existing_owner="$(runuser -u postgres -- psql -XAtqc "SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname='${DB_NAME}'")"
    [ -z "$existing_owner" ] || [ "$existing_owner" = "$DB_USER" ] ||
        die "existing PostgreSQL database $DB_NAME is owned by $existing_owner"
    if [ "$(runuser -u postgres -- psql -XAtqc "SELECT 1 FROM pg_roles WHERE rolname='${DB_USER}'")" != 1 ]; then
        runuser -u postgres -- psql -Xv ON_ERROR_STOP=1 -c \
            "CREATE ROLE ${DB_USER} LOGIN PASSWORD '${password}'" >/dev/null
    else
        runuser -u postgres -- psql -Xv ON_ERROR_STOP=1 -c \
            "ALTER ROLE ${DB_USER} PASSWORD '${password}'" >/dev/null
    fi
    if [ "$(runuser -u postgres -- psql -XAtqc "SELECT 1 FROM pg_database WHERE datname='${DB_NAME}'")" != 1 ]; then
        runuser -u postgres -- createdb -O "$DB_USER" "$DB_NAME"
    fi
}

main() {
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --api-binary) API_SRC="${2:?--api-binary needs a path}"; shift 2 ;;
            --hub-binary) HUB_SRC="${2:?--hub-binary needs a path}"; shift 2 ;;
            --hub-host) HUB_HOST="${2:?--hub-host needs a host}"; HOST_EXPLICIT=1; shift 2 ;;
            --port) API_PORT="${2:?--port needs a port}"; API_PORT_EXPLICIT=1; shift 2 ;;
            --hub-port) HUB_PORT="${2:?--hub-port needs a port}"; HUB_PORT_EXPLICIT=1; shift 2 ;;
            --web-url) WEB_URL="${2:?--web-url needs a URL}"; WEB_URL_EXPLICIT=1; shift 2 ;;
            --yes) ASSUME_YES=1; shift ;;
            --dry-run) DRY_RUN=1; shift ;;
            -h|--help) usage; return ;;
            *) die "unknown option: $1" ;;
        esac
    done
    valid_port "$API_PORT" || die "invalid API port: $API_PORT"
    valid_port "$HUB_PORT" || die "invalid Hub port: $HUB_PORT"
    [ "$API_PORT" != "$HUB_PORT" ] || die "API and Hub ports must differ"
    [[ "$HUB_HOST" =~ ^[A-Za-z0-9][A-Za-z0-9.-]*$ ]] || die "invalid Hub host: $HUB_HOST"
    WEB_URL="${WEB_URL:-http://${HUB_HOST}:${API_PORT}}"
    [[ "$WEB_URL" =~ ^https?://[^[:space:]]+$ ]] || die "--web-url must be an http(s) URL"

    if [ "$DRY_RUN" -eq 1 ]; then
        log "Dry run: no files or services will change"
        printf '\n### %s\n' "$API_ENV_FILE"
        render_api_env '<generated>' '<generated>'
        printf '\n### %s\n' "$HUB_ENV_FILE"
        render_hub_env
        printf '\n### %s\n' "$API_UNIT"
        render_api_unit
        printf '\n### %s\n' "$HUB_UNIT"
        render_hub_unit
        return
    fi

    [ "$(id -u)" -eq 0 ] || die "run as root (sudo)"
    [ -f /etc/debian_version ] || die "this installer supports Debian and Ubuntu"
    command -v systemctl >/dev/null || die "systemd is required"
    command -v apt-get >/dev/null || die "apt-get is required"

    local repo_root
    repo_root="$(cd "$(dirname "$0")/.." && pwd)"
    API_SRC="${API_SRC:-${repo_root}/bin/api}"
    HUB_SRC="${HUB_SRC:-${repo_root}/bin/hub}"
    [ -x "$API_SRC" ] || die "missing $API_SRC; build the distributed API first (see README)"
    [ -x "$HUB_SRC" ] || die "missing $HUB_SRC; build the Hub first (see README)"
    if [ -e /etc/winterflow/winterflow.env ] || [ -e /etc/systemd/system/winterflow.service ]; then
        die "a standalone installation exists on this host; use a separate control-plane host"
    fi
    if { [ -e "$API_UNIT" ] || [ -e "$HUB_UNIT" ]; } && [ ! -e "$API_ENV_FILE" ]; then
        die "existing WinterFlow API/Hub units have no installer configuration; inspect them before continuing"
    fi
    if [ -e "$API_ENV_FILE" ] && ! grep -q '^DATABASE_URL=postgres://' "$API_ENV_FILE"; then
        die "$API_ENV_FILE is not configured for PostgreSQL"
    fi

    ensure_dependencies
    systemctl enable --now postgresql redis-server
    runuser -u postgres -- psql -XAtqc 'SELECT 1' | grep -qx 1 || die "PostgreSQL is not ready"
    local pg_listen
    pg_listen="$(runuser -u postgres -- psql -XAtqc 'SHOW listen_addresses' | tr -d ' ')"
    case "$pg_listen" in
        localhost|127.0.0.1|::1|localhost,127.0.0.1|127.0.0.1,::1|localhost,::1)
            ;;
        *) die "PostgreSQL must listen only on loopback (found $pg_listen)" ;;
    esac
    redis-cli -h 127.0.0.1 ping | grep -qx PONG || die "Redis must answer on 127.0.0.1:6379 without a password"
    local redis_bind address
    redis_bind="$(redis-cli --raw -h 127.0.0.1 CONFIG GET bind | tail -n 1)"
    [ -n "$redis_bind" ] || die "Redis has no bind address; bind it to loopback first"
    for address in $redis_bind; do
        case "${address#-}" in
            127.*|::1) ;;
            *) die "Redis must be bound only to loopback addresses (found $address)" ;;
        esac
    done

    if [ -e "$API_ENV_FILE" ] || [ -e "$HUB_ENV_FILE" ]; then
        [ -s "$API_ENV_FILE" ] && [ -s "$HUB_ENV_FILE" ] ||
            die "incomplete control-plane config in $CONFIG_DIR"
        log "Keeping existing configuration and database"
        local saved_host saved_hub_port saved_api_port saved_web_url
        saved_host="$(sed -n 's/^HUB_HOST=//p' "$HUB_ENV_FILE" | head -1)"
        saved_hub_port="$(sed -n 's/^HUB_PORT=//p' "$HUB_ENV_FILE" | head -1)"
        saved_api_port="$(sed -n 's/^API_PORT=//p' "$API_ENV_FILE" | head -1)"
        saved_web_url="$(sed -n 's/^WEB_URL=//p' "$API_ENV_FILE" | head -1)"
        [ "$HOST_EXPLICIT" -eq 0 ] || [ "$HUB_HOST" = "$saved_host" ] || die "--hub-host differs from the existing certificate; update the configuration and certificate explicitly"
        [ "$HUB_PORT_EXPLICIT" -eq 0 ] || [ "$HUB_PORT" = "$saved_hub_port" ] || die "--hub-port differs from the existing configuration; edit $HUB_ENV_FILE explicitly"
        [ "$API_PORT_EXPLICIT" -eq 0 ] || [ "$API_PORT" = "$saved_api_port" ] || die "--port differs from the existing configuration; edit $API_ENV_FILE explicitly"
        [ "$WEB_URL_EXPLICIT" -eq 0 ] || [ "$WEB_URL" = "$saved_web_url" ] || die "--web-url differs from the existing configuration; edit $API_ENV_FILE explicitly"
        HUB_HOST="$saved_host"
        HUB_PORT="$saved_hub_port"
        API_PORT="$saved_api_port"
        WEB_URL="$saved_web_url"
        valid_port "$API_PORT" && valid_port "$HUB_PORT" && [ "$API_PORT" != "$HUB_PORT" ] ||
            die "existing config has invalid ports"
        [[ "$HUB_HOST" =~ ^[A-Za-z0-9][A-Za-z0-9.-]*$ ]] || die "existing config has invalid Hub host"
    fi

    local service_user
    for service_user in "$API_USER" "$HUB_USER"; do
        if ! id "$service_user" >/dev/null 2>&1; then
            useradd --system --user-group --home-dir "$DATA_DIR" --no-create-home \
                --shell /usr/sbin/nologin "$service_user"
        elif [ "$(id -u "$service_user")" -eq 0 ] || [ "$(id -gn "$service_user")" != "$service_user" ]; then
            die "existing $service_user account is not a dedicated non-root account with its own group"
        fi
    done
    install -d -m 755 -o root -g root "$DATA_DIR" "$CONFIG_DIR"
    install -d -m 750 -o "$API_USER" -g "$API_USER" "$DATA_DIR/api" "$DATA_DIR/avatars"
    install -d -m 750 -o "$HUB_USER" -g "$HUB_USER" "$DATA_DIR/hub"
    install -d -m 750 -o root -g "$HUB_USER" "$CERT_DIR"
    create_hub_certificates
    if [[ "$HUB_HOST" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        openssl x509 -in "$CERT_DIR/hub.crt" -noout -checkip "$HUB_HOST" >/dev/null ||
            die "existing Hub certificate does not cover $HUB_HOST"
    else
        openssl x509 -in "$CERT_DIR/hub.crt" -noout -checkhost "$HUB_HOST" >/dev/null ||
            die "existing Hub certificate does not cover $HUB_HOST"
    fi

    if [ ! -e "$API_ENV_FILE" ]; then
        local db_password jwt_secret
        db_password="$(openssl rand -hex 32)"
        jwt_secret="$(openssl rand -hex 32)"
        provision_postgres "$db_password"
        render_api_env "$db_password" "$jwt_secret" > "$API_ENV_FILE"
        render_hub_env > "$HUB_ENV_FILE"
        chown root:"$API_USER" "$API_ENV_FILE"
        chown root:"$HUB_USER" "$HUB_ENV_FILE"
        chmod 640 "$API_ENV_FILE" "$HUB_ENV_FILE"
    else
        : # Existing secrets and database stay unchanged on upgrade.
    fi

    install -m 755 -o root -g root "$API_SRC" "$API_DEST"
    install -m 755 -o root -g root "$HUB_SRC" "$HUB_DEST"
    render_api_unit > "$API_UNIT"
    render_hub_unit > "$HUB_UNIT"
    systemctl daemon-reload
    systemctl enable winterflow-api winterflow-hub
    systemctl restart winterflow-api winterflow-hub
    systemctl is-active --quiet winterflow-api || die "winterflow-api failed; check journalctl -u winterflow-api"
    systemctl is-active --quiet winterflow-hub || die "winterflow-hub failed; check journalctl -u winterflow-hub"
    log "API: $WEB_URL"
    log "Hub: $HUB_HOST:$HUB_PORT"
    log "Check: systemctl status winterflow-api winterflow-hub"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
