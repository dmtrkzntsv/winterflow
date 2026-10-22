#!/usr/bin/env bash
#
# Tests for scripts/install-control-plane.sh. Uses --dry-run against a scratch
# WF_INSTALL_ROOT, so nothing on the host is touched and root is not required.
# Run: bash scripts/install_control_plane_test.sh
set -uo pipefail

SCRIPT="$(cd "$(dirname "$0")" && pwd)/install-control-plane.sh"
FAILURES=0

pass() { printf 'ok   %s\n' "$1"; }
fail() { printf 'FAIL %s\n     %s\n' "$1" "$2"; FAILURES=$((FAILURES + 1)); }
assert_contains() { if grep -qF -- "$3" <<<"$2"; then pass "$1"; else fail "$1" "missing: $3"; fi; }
assert_not_contains() { if grep -qF -- "$3" <<<"$2"; then fail "$1" "unexpected: $3"; else pass "$1"; fi; }

# section OUTPUT PATH -> the dry-run block printed under "### PATH".
section() {
    awk -v h="### $2" '$0 == h { on = 1; next } on && /^### / { exit } on { print }' <<<"$1"
}

bash -n "$SCRIPT" && pass "syntax check" || fail "syntax check" "bash -n failed"

src="$(cat "$SCRIPT")"
assert_contains "script body runs via a main wrapper" "$src" 'main "$@"'
assert_not_contains "never reads /dev/tty (hangs under sudo use_pty)" "$src" "</dev/tty"

SECRET="s3cr3t-r2-value"
export WINTERFLOW_R2_ENDPOINT="https://acct.r2.cloudflarestorage.com"
export WINTERFLOW_R2_ACCESS_KEY_ID="key-id-123"
export WINTERFLOW_R2_SECRET_ACCESS_KEY="$SECRET"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# new_root NAME OS_ID -> a scratch install root with /etc/os-release.
new_root() {
    local r="$tmp/$1"
    mkdir -p "$r/etc"
    printf 'ID=%s\nVERSION_ID="12"\n' "$2" > "$r/etc/os-release"
    printf '%s' "$r"
}
debian="$(new_root debian debian)"
# dry ARGS -> dry-run output with the scratch root stripped from paths.
dry() {
    local o s=0
    o="$(WF_INSTALL_ROOT="$debian" bash "$SCRIPT" --dry-run "$@" 2>&1)" || s=$?
    printf '%s\n' "${o//$debian/}"
    return "$s"
}

# --- help / validation --------------------------------------------------------

help_out="$(bash "$SCRIPT" --help)" || fail "--help exits 0" "exit $?"
for opt in --web-url --port --hub-addr --capacity --redis-bind --redis-host --version --r2-bucket --yes --dry-run --uninstall; do
    assert_contains "--help documents ${opt}" "$help_out" "$opt"
done
dry --nonsense >/dev/null && fail "unknown option rejected" "exit 0" || pass "unknown option rejected"
dry --web-url http://flow.example.com >/dev/null && fail "plain http web url rejected" "exit 0" || pass "plain http web url rejected"
dry --port 70000 >/dev/null && fail "invalid api port rejected" "exit 0" || pass "invalid api port rejected"
dry --hub-addr 'bad/host:50051' >/dev/null && fail "invalid hub address rejected" "exit 0" || pass "invalid hub address rejected"
dry --hub-addr hub.example.com:8080 --port 8080 >/dev/null && fail "api and hub ports must differ" "exit 0" || pass "api and hub ports must differ"
dry --capacity abc >/dev/null && fail "non-numeric capacity rejected" "exit 0" || pass "non-numeric capacity rejected"
dry --redis-bind 'not an ip' >/dev/null && fail "invalid redis bind rejected" "exit 0" || pass "invalid redis bind rejected"
for w in 0.0.0.0 ::; do
    dry --redis-bind "$w" >/dev/null && fail "wildcard redis bind ${w} rejected" "exit 0" || pass "wildcard redis bind ${w} rejected"
done
assert_contains "cd / before running commands" "$src" "cd /"
dry --r2-bucket 'Bad_Bucket' >/dev/null && fail "invalid bucket rejected" "exit 0" || pass "invalid bucket rejected"

# --- refuses_without_r2_env ---------------------------------------------------

for v in WINTERFLOW_R2_ENDPOINT WINTERFLOW_R2_ACCESS_KEY_ID WINTERFLOW_R2_SECRET_ACCESS_KEY; do
    out_nor2="$(env -u "$v" WF_INSTALL_ROOT="$debian" bash "$SCRIPT" --dry-run 2>&1)" \
        && fail "refuses_without_r2_env: ${v} unset" "exit 0" || pass "refuses_without_r2_env: ${v} unset"
    assert_contains "refuses_without_r2_env: names ${v}" "$out_nor2" "$v"
done
out_nor2="$(env -u WINTERFLOW_R2_ENDPOINT -u WINTERFLOW_R2_ACCESS_KEY_ID -u WINTERFLOW_R2_SECRET_ACCESS_KEY \
    WF_INSTALL_ROOT="$debian" bash "$SCRIPT" --dry-run 2>&1)"
for v in WINTERFLOW_R2_ENDPOINT WINTERFLOW_R2_ACCESS_KEY_ID WINTERFLOW_R2_SECRET_ACCESS_KEY; do
    assert_contains "refuses_without_r2_env: all unset names ${v}" "$out_nor2" "$v"
done
WINTERFLOW_R2_ENDPOINT=http://acct.r2.cloudflarestorage.com dry >/dev/null \
    && fail "plain http R2 endpoint rejected" "exit 0" || pass "plain http R2 endpoint rejected"

# --- refuses_non_debian -------------------------------------------------------

fedora="$(new_root fedora fedora)"
out_fed="$(WF_INSTALL_ROOT="$fedora" bash "$SCRIPT" --dry-run 2>&1)" \
    && fail "refuses_non_debian: exits non-zero" "exit 0" || pass "refuses_non_debian: exits non-zero"
assert_contains "refuses_non_debian: says why" "$out_fed" "Debian or Ubuntu"
ubuntu="$(new_root ubuntu ubuntu)"
WF_INSTALL_ROOT="$ubuntu" bash "$SCRIPT" --dry-run >/dev/null 2>&1 \
    && pass "ubuntu accepted" || fail "ubuntu accepted" "exit $?"
mint="$(new_root mint linuxmint)"; printf 'ID_LIKE="ubuntu debian"\n' >> "$mint/etc/os-release"
WF_INSTALL_ROOT="$mint" bash "$SCRIPT" --dry-run >/dev/null 2>&1 \
    && pass "ID_LIKE debian accepted" || fail "ID_LIKE debian accepted" "exit $?"

# --- downloads_from_r2_with_sigv4 ---------------------------------------------

out="$(dry --web-url https://flow.example.com --port 8090 --hub-addr hub-01.example.com:50051 --capacity 500 \
    --redis-bind 100.64.0.7 --redis-host cp.tail1234.ts.net)" || fail "dry-run exits 0" "exit $?: $out"

assert_contains "downloads_from_r2_with_sigv4: sigv4" "$out" '--aws-sigv4 "aws:amz:auto:s3"'
assert_contains "downloads_from_r2_with_sigv4: latest api" "$out" "https://acct.r2.cloudflarestorage.com/econumo/winterflow/latest/winterflow-api-linux-"
assert_contains "downloads_from_r2_with_sigv4: latest hub" "$out" "/econumo/winterflow/latest/winterflow-hub-linux-"
assert_contains "downloads_from_r2_with_sigv4: checksums" "$out" "/econumo/winterflow/latest/SHA256SUMS"
assert_not_contains "downloads_from_r2_with_sigv4: secret never printed" "$out" "$SECRET"
assert_not_contains "secret not on the curl command line" "$src" 'SECRET_ACCESS_KEY" "'
out_ver="$(dry --version 260101.1 --r2-bucket other-bucket)"
assert_contains "--version picks the channel" "$out_ver" "/other-bucket/winterflow/v260101.1/winterflow-api-linux-"
assert_contains "checksums verified in the script" "$src" "sha256sum -c"

assert_contains "caddy snippet sets the client address" "$out" "header_up X-Forwarded-For {client_ip}"

# --- units_and_env ------------------------------------------------------------

api_env="$(section "$out" /etc/winterflow/control-plane-api.env)"
hub_env="$(section "$out" /etc/winterflow/control-plane-hub.env)"
api_unit="$(section "$out" /etc/systemd/system/winterflow-api.service)"
hub_unit="$(section "$out" /etc/systemd/system/winterflow-hub.service)"
[ -n "$api_env" ] && [ -n "$hub_env" ] && [ -n "$api_unit" ] && [ -n "$hub_unit" ] \
    && pass "units_and_env: all four files printed" || fail "units_and_env: all four files printed" "$out"

assert_contains "units_and_env: api postgres" "$api_env" "DATABASE_URL=postgres://winterflow:"
assert_contains "units_and_env: api postgres db" "$api_env" "@127.0.0.1:5432/winterflow"
assert_contains "units_and_env: api port" "$api_env" "API_PORT=8090"
assert_contains "units_and_env: api listens on loopback only" "$api_env" "API_LISTEN_ADDR=127.0.0.1"
assert_contains "units_and_env: api web url" "$api_env" "WEB_URL=https://flow.example.com"
assert_contains "units_and_env: api cors origin" "$api_env" "CORS_ALLOW_ORIGINS=https://flow.example.com"
assert_contains "units_and_env: api jwt secret" "$api_env" "JWT_SECRET="
assert_contains "units_and_env: api signs with the pki dir" "$api_env" "HUB_CERT_DIR=/var/lib/winterflow-control/pki"
assert_contains "units_and_env: api unit" "$api_unit" "ExecStart=/usr/local/bin/winterflow-api"
assert_contains "units_and_env: api user" "$api_unit" "User=winterflow-api"
assert_contains "units_and_env: api env file" "$api_unit" "EnvironmentFile=/etc/winterflow/control-plane-api.env"
assert_contains "units_and_env: api after redis" "$api_unit" "redis-server@winterflow.service"
assert_contains "units_and_env: api hardened" "$api_unit" "ProtectSystem=strict"

assert_contains "units_and_env: hub public addr" "$hub_env" "HUB_PUBLIC_ADDR=hub-01.example.com:50051"
assert_contains "units_and_env: hub capacity" "$hub_env" "HUB_CAPACITY=500"
assert_contains "units_and_env: hub listen port" "$hub_env" "HUB_PORT=50051"
assert_contains "units_and_env: hub cert dir" "$hub_env" "HUB_CERT_DIR=/var/lib/winterflow-control/hub-certs"
assert_not_contains "units_and_env: hub has no CA key" "$hub_env" "ca.key"
assert_not_contains "units_and_env: hub not on the pki dir" "$hub_env" "/var/lib/winterflow-control/pki"
assert_not_contains "units_and_env: hub has no db" "$hub_env" "DATABASE_URL"
assert_not_contains "units_and_env: hub has no jwt" "$hub_env" "JWT_SECRET"
assert_contains "units_and_env: hub unit" "$hub_unit" "ExecStart=/usr/local/bin/winterflow-hub"
assert_contains "units_and_env: hub user" "$hub_unit" "User=winterflow-hub"
assert_contains "units_and_env: hub env file" "$hub_unit" "EnvironmentFile=/etc/winterflow/control-plane-hub.env"
assert_contains "units_and_env: hub stop timeout" "$hub_unit" "TimeoutStopSec=150"
assert_contains "units_and_env: journal namespace" "$out" "LogNamespace=winterflow"

plan="$(section "$out" plan)"
assert_contains "pki: hub host and redis names" "$plan" "winterflow-hub pki --dir /var/lib/winterflow-control/pki --host hub-01.example.com"
assert_contains "pki: redis on loopback" "$plan" "--redis-host 127.0.0.1"
assert_contains "pki: tailscale name" "$plan" "--redis-host cp.tail1234.ts.net"
assert_contains "pki: CA key owned by the api user" "$plan" "ca.key: winterflow-api 0600"
assert_contains "pki: hub gets only the CA cert and its pair" "$plan" "/var/lib/winterflow-control/hub-certs: ca.crt hub.crt hub.key"
assert_contains "postgres: role created only if absent" "$plan" "create role and database 'winterflow' if absent"
assert_contains "finish: proxy target" "$out" "127.0.0.1:8090"
assert_contains "finish: open the hub port" "$out" "TCP 50051"
assert_contains "finish: url" "$out" "https://flow.example.com"
assert_not_contains "finish: no all-interfaces caveat" "$out" "every interface"
assert_not_contains "state never under the agent's data dir" "$out" "/var/lib/winterflow/"
assert_not_contains "state never under the agent's data dir (script)" "$src" '/var/lib/winterflow"'

# --- uninstall ----------------------------------------------------------------

out_un="$(dry --uninstall)" || fail "uninstall dry-run exits 0" "exit $?: $out_un"
assert_contains "uninstall: stops the units" "$out_un" "systemctl disable --now winterflow-api winterflow-hub"
assert_contains "uninstall: removes the units" "$out_un" "rm -f /etc/systemd/system/winterflow-api.service /etc/systemd/system/winterflow-hub.service"
assert_contains "uninstall: removes the binaries" "$out_un" "/usr/local/bin/winterflow-api /usr/local/bin/winterflow-hub"
assert_contains "uninstall: disables the redis instance" "$out_un" "systemctl disable --now redis-server@winterflow.service"
assert_contains "uninstall: removes the redis drop-in" "$out_un" "rm -f /etc/systemd/system/redis-server@winterflow.service.d/winterflow.conf"
assert_contains "uninstall: keeps the control-plane data" "$out_un" "kept: /var/lib/winterflow-control"
assert_contains "uninstall: keeps the env files" "$out_un" "/etc/winterflow/control-plane.env"
assert_contains "uninstall: keeps the redis config" "$out_un" "/etc/redis/redis-winterflow.conf"
assert_contains "uninstall: keeps the database" "$out_un" "PostgreSQL database 'winterflow'"
assert_not_contains "uninstall: never removes data" "$out_un" "rm -rf"
assert_not_contains "uninstall: default redis untouched" "$out_un" "redis-server.service"
assert_not_contains "uninstall: no download" "$out_un" "aws-sigv4"
env -u WINTERFLOW_R2_ENDPOINT -u WINTERFLOW_R2_ACCESS_KEY_ID -u WINTERFLOW_R2_SECRET_ACCESS_KEY \
    WF_INSTALL_ROOT="$debian" bash "$SCRIPT" --dry-run --uninstall >/dev/null 2>&1 \
    && pass "uninstall needs no R2 variables" || fail "uninstall needs no R2 variables" "exit $?"

# --- redis_instance_tls_only --------------------------------------------------

redis_conf="$(section "$out" /etc/redis/redis-winterflow.conf)"
assert_contains "redis_instance_tls_only: no plain port" "$redis_conf" "port 0"
assert_contains "redis_instance_tls_only: tls port" "$redis_conf" "tls-port 6380"
assert_contains "redis_instance_tls_only: password" "$redis_conf" "requirepass "
assert_contains "redis_instance_tls_only: binds loopback and extra" "$redis_conf" "bind 127.0.0.1 100.64.0.7"
assert_contains "redis_instance_tls_only: password auth, no client certs" "$redis_conf" "tls-auth-clients no"
assert_contains "redis_instance_tls_only: cert readable by redis" "$redis_conf" "tls-key-file /etc/redis/winterflow-tls/redis.key"
assert_contains "redis_instance_tls_only: own data dir" "$redis_conf" "dir /var/lib/redis/winterflow"
for f in "$api_env" "$hub_env"; do
    assert_contains "redis_instance_tls_only: env tls" "$f" "REDIS_TLS=true"
    assert_contains "redis_instance_tls_only: env addr" "$f" "REDIS_ADDR=127.0.0.1:6380"
done
assert_not_contains "default redis instance untouched" "$src" "/etc/redis/redis.conf"

# The instance survives a bind address that is not up yet (a Tailscale IP at
# boot): a drop-in for this instance only keeps restarting it.
dropin="$(section "$out" /etc/systemd/system/redis-server@winterflow.service.d/winterflow.conf)"
assert_contains "redis drop-in: no start limit" "$dropin" "StartLimitIntervalSec=0"
assert_contains "redis drop-in: restart delay" "$dropin" "RestartSec=5s"
assert_contains "redis drop-in: after network-online" "$dropin" "After=network-online.target"
assert_contains "redis drop-in: wants network-online" "$dropin" "Wants=network-online.target"
assert_contains "redis drop-in: after tailscaled with --redis-bind" "$dropin" "After=tailscaled.service"
dropin_plain="$(section "$(dry)" /etc/systemd/system/redis-server@winterflow.service.d/winterflow.conf)"
assert_contains "redis drop-in: always written" "$dropin_plain" "StartLimitIntervalSec=0"
assert_not_contains "redis drop-in: tailscaled only with --redis-bind" "$dropin_plain" "tailscaled"
assert_not_contains "redis drop-in: never for the default instance" "$src" "redis-server.service.d"

# --- rerun_keeps_password -----------------------------------------------------

rerun="$(new_root rerun debian)"
mkdir -p "$rerun/etc/winterflow"
cat > "$rerun/etc/winterflow/control-plane.env" <<'EOF'
POSTGRES_PASSWORD=aaaa1111bbbb2222
REDIS_PASSWORD=cccc3333dddd4444
JWT_SECRET=eeee5555ffff6666
WEB_URL=https://kept.example.com
API_PORT=8181
HUB_PUBLIC_ADDR=hub-kept.example.com:50052
HUB_CAPACITY=42
REDIS_BIND=10.0.0.9
REDIS_HOSTS=kept.tail.ts.net
EOF
out_rerun="$(WF_INSTALL_ROOT="$rerun" bash "$SCRIPT" --dry-run 2>&1)" || fail "rerun dry-run exits 0" "exit $?: $out_rerun"
out_rerun="${out_rerun//$rerun/}"
# Dry-run shows kept secrets only as a sha256 fingerprint (first 8 hex).
fp() { printf '<kept sha256:%s>' "$(printf '%s' "$1" | sha256sum | cut -c1-8)"; }
pg_fp="$(fp aaaa1111bbbb2222)"; redis_fp="$(fp cccc3333dddd4444)"; jwt_fp="$(fp eeee5555ffff6666)"
assert_contains "rerun_keeps_password: postgres" "$(section "$out_rerun" "/etc/winterflow/control-plane-api.env")" "postgres://winterflow:${pg_fp}@"
assert_contains "rerun_keeps_password: redis api" "$(section "$out_rerun" "/etc/winterflow/control-plane-api.env")" "REDIS_PASSWORD=${redis_fp}"
assert_contains "rerun_keeps_password: redis hub" "$(section "$out_rerun" "/etc/winterflow/control-plane-hub.env")" "REDIS_PASSWORD=${redis_fp}"
assert_contains "rerun_keeps_password: redis conf" "$(section "$out_rerun" "/etc/redis/redis-winterflow.conf")" "requirepass ${redis_fp}"
assert_contains "rerun_keeps_password: state file" "$(section "$out_rerun" "/etc/winterflow/control-plane.env")" "POSTGRES_PASSWORD=${pg_fp}"
assert_contains "rerun keeps the jwt secret" "$out_rerun" "JWT_SECRET=${jwt_fp}"
for raw in aaaa1111bbbb2222 cccc3333dddd4444 eeee5555ffff6666; do
    assert_not_contains "dry-run masks kept secret ${raw:0:4}..." "$out_rerun" "$raw"
done
assert_contains "rerun keeps settings: web url" "$out_rerun" "WEB_URL=https://kept.example.com"
assert_contains "rerun keeps settings: hub addr" "$out_rerun" "HUB_PUBLIC_ADDR=hub-kept.example.com:50052"
assert_contains "rerun keeps settings: redis bind" "$out_rerun" "bind 127.0.0.1 10.0.0.9"
assert_contains "rerun keeps settings: redis names" "$out_rerun" "--redis-host kept.tail.ts.net"
assert_not_contains "rerun generates nothing new" "$out_rerun" "<generated>"
out_flag="$(WF_INSTALL_ROOT="$rerun" bash "$SCRIPT" --dry-run --capacity 7 2>&1)"
assert_contains "rerun: a flag overrides a kept setting" "$out_flag" "HUB_CAPACITY=7"
assert_contains "fresh install generates passwords" "$out" "<generated>"

# A tampered state file is refused rather than written into units/env.
bad="$(new_root bad debian)"; mkdir -p "$bad/etc/winterflow"
printf 'POSTGRES_PASSWORD=x; rm -rf /\n' > "$bad/etc/winterflow/control-plane.env"
WF_INSTALL_ROOT="$bad" bash "$SCRIPT" --dry-run >/dev/null 2>&1 \
    && fail "invalid kept password refused" "exit 0" || pass "invalid kept password refused"

# --- real runs against a scratch root with stubbed system tools --------------

# Exercises download + checksum, the kept secrets, Postgres provisioning, the
# certificate copies and the service start. Stubs on PATH stand in for id -u
# (root check), apt-get, systemctl, runuser, psql, Redis, curl and the
# ownership commands; the "downloaded" hub binary plays `pki`. Skipped when
# the tests already run as root.
if [ "$(id -u)" -eq 0 ]; then
    pass "stubbed install runs skipped (running as root)"
else
    real_id="$(command -v id)"
    real_install="$(command -v install)"
    make_stubs() {
        # make_stubs ROOT — stubs in ROOT/stubs, R2 objects in ROOT/r2.
        local r="$1" bin="$1/stubs" a
        mkdir -p "$bin" "$r/r2"
        cat > "$bin/id" <<STUB
#!/bin/sh
if [ "\$1" = "-u" ] && [ \$# -eq 1 ]; then echo 0; exit 0; fi
if [ "\$1" = "-u" ]; then echo 999; exit 0; fi
case "\$1" in winterflow-*|redis|postgres) exit 0 ;; esac
exec "$real_id" "\$@"
STUB
        cat > "$bin/install" <<STUB
#!/bin/bash
args=()
while [ \$# -gt 0 ]; do case "\$1" in -o|-g) shift 2 ;; *) args+=("\$1"); shift ;; esac; done
exec "$real_install" "\${args[@]}"
STUB
        printf '#!/bin/sh\necho "systemctl $*" >> "%s/calls"\ncase "$1" in --version) echo "systemd 255";; cat) [ ! -e "%s/no-$2" ] || exit 1;; esac\nexit 0\n' "$r" "$r" > "$bin/systemctl"
        printf '#!/bin/sh\necho "apt-get $*" >> "%s/calls"\nexit 0\n' "$r" > "$bin/apt-get"
        printf '#!/bin/sh\nshift 3\nexec "$@"\n' > "$bin/runuser"
        printf '#!/bin/sh\nshift\nexec "$@"\n' > "$bin/timeout"
        printf '#!/bin/sh\necho "Redis server v=7.0.15 sha=00000000:0 malloc=jemalloc-5.3.0 bits=64"\n' > "$bin/redis-server"
        printf '#!/bin/sh\necho PONG\n' > "$bin/redis-cli"
        printf '#!/bin/sh\necho cp.example.com\n' > "$bin/hostname"
        printf '#!/bin/sh\ncase "$1" in -s) echo Linux;; -m) echo x86_64;; esac\n' > "$bin/uname"
        for a in chown useradd sleep; do printf '#!/bin/sh\nexit 0\n' > "$bin/$a"; done
        printf '#!/bin/sh\necho "createdb $*" >> "%s/calls"\n' "$r" > "$bin/createdb"
        # psql: answers the installer's queries; a CREATE ROLE is logged with
        # its password and makes the role exist.
        cat > "$bin/psql" <<STUB
#!/bin/sh
sql="\$(cat)"
case "\$sql" in
    "SELECT 1") echo 1 ;;
    "SHOW port") echo 5432 ;;
    *pg_roles*) [ -e "$r/role" ] && echo 1 ;;
    *datdba*) echo winterflow ;;
    *pg_database*) [ -e "$r/db" ] && echo 1 ;;
    CREATE*) echo "psql \$sql" >> "$r/calls"; : > "$r/role" ;;
esac
exit 0
STUB
        # curl: logs its arguments and stdin (the config), serves r2/<name>.
        cat > "$bin/curl" <<STUB
#!/bin/sh
if [ "\$1" = "--version" ]; then echo "curl 8.5.0 (x86_64-pc-linux-gnu)"; exit 0; fi
echo "curl \$*" >> "$r/curl-args"
cat >> "$r/curl-stdin"
out=""; url=""
while [ \$# -gt 0 ]; do case "\$1" in -o) out="\$2"; shift 2 ;; *) url="\$1"; shift ;; esac; done
cp "$r/r2/\${url##*/}" "\$out"
STUB
        cat > "$r/r2/winterflow-hub-linux-amd64" <<'STUB'
#!/bin/sh
# fake winterflow-hub: only the pki subcommand
[ "$1" = pki ] || exit 0
shift
while [ $# -gt 0 ]; do case "$1" in --dir) dir="$2"; shift 2 ;; *) echo "$1 $2" >> "$dir/pki-args"; shift 2 ;; esac; done
for f in ca hub redis; do echo "$f cert" > "$dir/$f.crt"; echo "$f key" > "$dir/$f.key"; done
echo changed
STUB
        printf '#!/bin/sh\nexit 0\n' > "$r/r2/winterflow-api-linux-amd64"
        (cd "$r/r2" && sha256sum winterflow-api-linux-amd64 winterflow-hub-linux-amd64 > SHA256SUMS)
        chmod +x "$bin"/*
        mkdir -p "$r/etc" "$r/usr/local/bin" "$r/etc/systemd/system" "$r/etc/redis" "$r/var/lib/redis"
        printf 'ID=debian\n' > "$r/etc/os-release"
        : > "$r/calls"; : > "$r/curl-args"; : > "$r/curl-stdin"
    }
    run_cp() {
        # run_cp ROOT [args] -> output in RUN_OUT, status in RUN_STATUS
        local r="$1"; shift
        RUN_STATUS=0
        RUN_OUT="$(PATH="$r/stubs:$PATH" WF_INSTALL_ROOT="$r" TMPDIR="$r" \
            bash "$SCRIPT" --yes --web-url https://flow.example.com "$@" </dev/null 2>&1)" || RUN_STATUS=$?
    }
    kept() { sed -n "s/^$2=//p" "$1/etc/winterflow/control-plane.env"; }

    r1="$tmp/real1"; mkdir -p "$r1"; make_stubs "$r1"
    run_cp "$r1" --redis-host cp.tail.ts.net
    [ "$RUN_STATUS" -eq 0 ] && pass "real run: installer succeeds" || fail "real run: installer succeeds" "status $RUN_STATUS: $RUN_OUT"
    pg_pw="$(kept "$r1" POSTGRES_PASSWORD)"; redis_pw="$(kept "$r1" REDIS_PASSWORD)"; jwt="$(kept "$r1" JWT_SECRET)"
    grep -qE '^[0-9a-f]{64}$' <<<"$pg_pw" && grep -qE '^[0-9a-f]{64}$' <<<"$redis_pw" && grep -qE '^[0-9a-f]{64}$' <<<"$jwt" \
        && pass "real run: secrets generated" || fail "real run: secrets generated" "pg=$pg_pw redis=$redis_pw jwt=$jwt"
    [ "$pg_pw" != "$redis_pw" ] && pass "real run: distinct secrets" || fail "real run: distinct secrets" "same"
    [ "$(stat -c %a "$r1/etc/winterflow/control-plane.env")" = 600 ] && pass "real run: state file 0600" || fail "real run: state file 0600" "$(stat -c %a "$r1/etc/winterflow/control-plane.env")"
    assert_contains "real run: role created with the kept password" "$(cat "$r1/calls")" "CREATE ROLE winterflow LOGIN PASSWORD '${pg_pw}'"
    assert_contains "real run: database created" "$(cat "$r1/calls")" "createdb -O winterflow winterflow"
    assert_contains "real run: api env uses it" "$(cat "$r1/etc/winterflow/control-plane-api.env")" "postgres://winterflow:${pg_pw}@127.0.0.1:5432/winterflow"
    assert_contains "real run: redis conf password" "$(cat "$r1/etc/redis/redis-winterflow.conf")" "requirepass ${redis_pw}"
    assert_contains "real run: hub env password" "$(cat "$r1/etc/winterflow/control-plane-hub.env")" "REDIS_PASSWORD=${redis_pw}"
    assert_not_contains "real run: secret never on curl's command line" "$(cat "$r1/curl-args")" "$SECRET"
    assert_contains "real run: secret passed on curl's stdin" "$(cat "$r1/curl-stdin")" "$SECRET"
    assert_contains "real run: sigv4" "$(cat "$r1/curl-args")" "--aws-sigv4 aws:amz:auto:s3"
    [ -x "$r1/usr/local/bin/winterflow-api" ] && [ -x "$r1/usr/local/bin/winterflow-hub" ] \
        && pass "real run: binaries installed" || fail "real run: binaries installed" "missing"
    assert_contains "real run: pki for the hub host" "$(cat "$r1/var/lib/winterflow-control/pki/pki-args")" "--host cp.example.com"
    assert_contains "real run: pki redis on loopback" "$(cat "$r1/var/lib/winterflow-control/pki/pki-args")" "--redis-host 127.0.0.1"
    assert_contains "real run: pki redis tailscale name" "$(cat "$r1/var/lib/winterflow-control/pki/pki-args")" "--redis-host cp.tail.ts.net"
    [ "$(stat -c %a "$r1/var/lib/winterflow-control/pki/ca.key")" = 600 ] && pass "real run: ca.key 0600" || fail "real run: ca.key 0600" "$(stat -c %a "$r1/var/lib/winterflow-control/pki/ca.key")"
    [ "$(ls "$r1/var/lib/winterflow-control/hub-certs" | tr '\n' ' ')" = "ca.crt hub.crt hub.key " ] \
        && pass "real run: hub cert dir has no CA key" || fail "real run: hub cert dir has no CA key" "$(ls "$r1/var/lib/winterflow-control/hub-certs")"
    [ -f "$r1/etc/redis/winterflow-tls/redis.key" ] && [ ! -e "$r1/etc/redis/winterflow-tls/ca.key" ] \
        && pass "real run: redis gets its pair, not the CA key" || fail "real run: redis gets its pair, not the CA key" "$(ls "$r1/etc/redis/winterflow-tls")"
    [ -f "$r1/etc/systemd/system/winterflow-api.service" ] && [ -f "$r1/etc/systemd/system/winterflow-hub.service" ] \
        && pass "real run: units written" || fail "real run: units written" "missing"
    assert_contains "real run: redis instance restarted" "$(cat "$r1/calls")" "systemctl restart redis-server@winterflow.service"
    assert_contains "real run: redis drop-in installed" "$(cat "$r1/etc/systemd/system/redis-server@winterflow.service.d/winterflow.conf" 2>/dev/null)" "StartLimitIntervalSec=0"
    grep -q "daemon-reload" "$r1/calls" && [ "$(grep -n 'daemon-reload' "$r1/calls" | head -1 | cut -d: -f1)" -lt "$(grep -n 'restart redis-server@winterflow' "$r1/calls" | cut -d: -f1)" ] \
        && pass "real run: daemon-reload before redis restart" || fail "real run: daemon-reload before redis restart" "$(cat "$r1/calls")"
    assert_not_contains "real run: default redis untouched" "$(cat "$r1/calls")" "redis-server.service"
    assert_contains "real run: services restarted" "$(cat "$r1/calls")" "systemctl restart winterflow-api winterflow-hub"
    assert_not_contains "real run: nothing installed with apt" "$(cat "$r1/calls")" "apt-get install"
    assert_contains "real run: finish names the proxy target" "$RUN_OUT" "127.0.0.1:8080"

    # Rerun: same secrets, the existing role is kept, unchanged Redis is not restarted.
    : > "$r1/calls"; : > "$r1/db"
    run_cp "$r1"
    [ "$RUN_STATUS" -eq 0 ] && pass "rerun: installer succeeds" || fail "rerun: installer succeeds" "status $RUN_STATUS: $RUN_OUT"
    [ "$(kept "$r1" POSTGRES_PASSWORD)" = "$pg_pw" ] && [ "$(kept "$r1" REDIS_PASSWORD)" = "$redis_pw" ] && [ "$(kept "$r1" JWT_SECRET)" = "$jwt" ] \
        && pass "rerun_keeps_password: real rerun" || fail "rerun_keeps_password: real rerun" "secrets changed"
    assert_not_contains "rerun: role not created again" "$(cat "$r1/calls")" "CREATE ROLE"
    assert_not_contains "rerun: database not created again" "$(cat "$r1/calls")" "createdb"
    assert_contains "rerun: redis names kept" "$(kept "$r1" REDIS_HOSTS)" "cp.tail.ts.net"
    assert_contains "rerun: services restarted" "$(cat "$r1/calls")" "systemctl restart winterflow-api winterflow-hub"
    assert_not_contains "rerun: unchanged redis not restarted" "$(cat "$r1/calls")" "systemctl restart redis-server@winterflow.service"
    : > "$r1/calls"
    run_cp "$r1" --redis-bind 10.1.1.1
    assert_contains "rerun: new redis bind written" "$(cat "$r1/etc/redis/redis-winterflow.conf")" "bind 127.0.0.1 10.1.1.1"
    assert_contains "rerun: changed redis config restarts redis" "$(cat "$r1/calls")" "systemctl restart redis-server@winterflow.service"

    # Real uninstall: units and binaries go, data and config stay.
    : > "$r1/calls"
    RUN_STATUS=0
    RUN_OUT="$(PATH="$r1/stubs:$PATH" WF_INSTALL_ROOT="$r1" bash "$SCRIPT" --uninstall </dev/null 2>&1)" || RUN_STATUS=$?
    [ "$RUN_STATUS" -eq 0 ] && pass "uninstall: succeeds" || fail "uninstall: succeeds" "status $RUN_STATUS: $RUN_OUT"
    [ ! -e "$r1/etc/systemd/system/winterflow-api.service" ] && [ ! -e "$r1/usr/local/bin/winterflow-hub" ] \
        && pass "uninstall: units and binaries removed" || fail "uninstall: units and binaries removed" "still there"
    [ -f "$r1/var/lib/winterflow-control/pki/ca.key" ] && [ -f "$r1/etc/winterflow/control-plane.env" ] && [ -f "$r1/etc/redis/redis-winterflow.conf" ] \
        && pass "uninstall: CA, state and redis config kept" || fail "uninstall: CA, state and redis config kept" "removed"
    assert_contains "uninstall: redis instance disabled" "$(cat "$r1/calls")" "systemctl disable --now redis-server@winterflow.service"
    [ ! -e "$r1/etc/systemd/system/redis-server@winterflow.service.d" ] && pass "uninstall: redis drop-in removed" || fail "uninstall: redis drop-in removed" "still there"

    # A role that exists without a kept password is refused, not overwritten.
    r2="$tmp/real2"; mkdir -p "$r2"; make_stubs "$r2"; : > "$r2/role"
    run_cp "$r2"
    [ "$RUN_STATUS" -ne 0 ] && pass "existing role without a kept password refused" || fail "existing role without a kept password refused" "status 0"
    assert_contains "existing role: says what to do" "$RUN_OUT" "has no POSTGRES_PASSWORD"
    assert_not_contains "existing role: password not changed" "$(cat "$r2/calls")" "ROLE"

    # A tampered download fails the checksum and installs nothing.
    r3="$tmp/real3"; mkdir -p "$r3"; make_stubs "$r3"
    echo tampered >> "$r3/r2/winterflow-api-linux-amd64"
    run_cp "$r3"
    [ "$RUN_STATUS" -ne 0 ] && pass "checksum mismatch: installer fails" || fail "checksum mismatch: installer fails" "status 0"
    assert_contains "checksum mismatch: says so" "$RUN_OUT" "checksum mismatch for winterflow-api-linux-amd64"
    [ ! -e "$r3/usr/local/bin/winterflow-api" ] && pass "checksum mismatch: nothing installed" || fail "checksum mismatch: nothing installed" "binary installed"

    # Missing packages are installed after asking (--yes answers).
    r4="$tmp/real4"; mkdir -p "$r4"; make_stubs "$r4"; : > "$r4/no-postgresql.service"
    run_cp "$r4"
    assert_contains "missing postgres: installed with apt-get" "$(cat "$r4/calls")" "apt-get install -y postgresql"
    grep -qx "apt-get install -y postgresql" "$r4/calls" && pass "missing postgres: only postgresql installed" || fail "missing postgres: only postgresql installed" "$(grep apt-get "$r4/calls")"
    [ "$RUN_STATUS" -ne 0 ] && pass "missing postgres: still missing after apt fails the run" || fail "missing postgres: still missing after apt fails the run" "status 0"
fi

echo
if [ "$FAILURES" -eq 0 ]; then echo "ALL TESTS PASSED"; else echo "${FAILURES} TEST(S) FAILED"; exit 1; fi
