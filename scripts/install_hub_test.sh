#!/usr/bin/env bash
#
# Tests for scripts/install-hub.sh. Dry runs use a scratch WF_INSTALL_ROOT, so
# nothing on the host is touched and root is not required; real runs use
# stubbed system tools on PATH.
# Run: bash scripts/install_hub_test.sh
set -uo pipefail

SCRIPT="$(cd "$(dirname "$0")" && pwd)/install-hub.sh"
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
assert_not_contains "no CA key handling" "$src" "ca.key"

SECRET="s3cr3t-r2-value"
export WINTERFLOW_R2_ENDPOINT="https://acct.r2.cloudflarestorage.com"
export WINTERFLOW_R2_ACCESS_KEY_ID="key-id-123"
export WINTERFLOW_R2_SECRET_ACCESS_KEY="$SECRET"
unset WINTERFLOW_HUB_TOKEN

TOKEN="wfh_AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcde"
REDIS_PW="redispw0123456789abcdef"
REDIS_URL="rediss://:${REDIS_PW}@cp.example.com:6380"
ADDR="hub-02.example.com:50051"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# fake_pem BODY -> a PEM block whose decoded bytes are BODY (the installer
# only looks for the winterflow-hub: URI in them).
fake_pem() {
    printf -- '-----BEGIN CERTIFICATE-----\n%s\n-----END CERTIFICATE-----\n' "$(printf '%s' "$1" | base64 -w 64)"
}
MARKED_PEM="$(fake_pem "0..URI winterflow-hub:hub-kept.example.com..")"
UNMARKED_PEM="$(fake_pem "0..DNS hub-kept.example.com..")"

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
join_args=(--token "$TOKEN" --public-addr "$ADDR" --redis-url "$REDIS_URL")

# --- help / validation --------------------------------------------------------

help_out="$(bash "$SCRIPT" --help)" || fail "--help exits 0" "exit $?"
for opt in --token --public-addr --redis-url --capacity --listen-port --api-url --version --r2-bucket --yes --dry-run --uninstall --purge; do
    assert_contains "--help documents ${opt}" "$help_out" "$opt"
done
dry --nonsense >/dev/null && fail "unknown option rejected" "exit 0" || pass "unknown option rejected"
dry "${join_args[@]}" --capacity abc >/dev/null && fail "non-numeric capacity rejected" "exit 0" || pass "non-numeric capacity rejected"
dry "${join_args[@]}" --listen-port 70000 >/dev/null && fail "invalid listen port rejected" "exit 0" || pass "invalid listen port rejected"
dry --token "$TOKEN" --public-addr 'bad/host:50051' --redis-url "$REDIS_URL" >/dev/null && fail "invalid public addr rejected" "exit 0" || pass "invalid public addr rejected"
dry --token "$TOKEN" --public-addr nohost --redis-url "$REDIS_URL" >/dev/null && fail "public addr without port rejected" "exit 0" || pass "public addr without port rejected"
dry --token nope --public-addr "$ADDR" --redis-url "$REDIS_URL" >/dev/null && fail "malformed token rejected" "exit 0" || pass "malformed token rejected"
dry "${join_args[@]}" --api-url http://api.example.com >/dev/null && fail "plain http api url rejected" "exit 0" || pass "plain http api url rejected"
dry "${join_args[@]}" --r2-bucket 'Bad_Bucket' >/dev/null && fail "invalid bucket rejected" "exit 0" || pass "invalid bucket rejected"
dry "${join_args[@]}" --purge >/dev/null && fail "--purge needs --uninstall" "exit 0" || pass "--purge needs --uninstall"
assert_contains "cd / before running commands" "$src" "cd /"

# --- redis url ----------------------------------------------------------------

out_plain="$(dry --token "$TOKEN" --public-addr "$ADDR" --redis-url "redis://:${REDIS_PW}@cp.example.com:6380")" \
    && fail "redis:// refused" "exit 0" || pass "redis:// refused"
assert_contains "redis:// refused: says TLS" "$out_plain" "rediss://"
assert_not_contains "redis:// refused: password not echoed" "$out_plain" "$REDIS_PW"
dry --token "$TOKEN" --public-addr "$ADDR" --redis-url "rediss://cp.example.com:6380" >/dev/null \
    && fail "redis url without password refused" "exit 0" || pass "redis url without password refused"
dry --token "$TOKEN" --public-addr "$ADDR" --redis-url "rediss://:${REDIS_PW}@cp.example.com" >/dev/null \
    && fail "redis url without port refused" "exit 0" || pass "redis url without port refused"

o="$(WINTERFLOW_HUB_REDIS_URL="$REDIS_URL" WF_INSTALL_ROOT="$debian" bash "$SCRIPT" --dry-run --token "$TOKEN" --public-addr "$ADDR" 2>&1)" \
    && pass "redis url from the environment accepted" || fail "redis url from the environment accepted" "$o"
assert_contains "redis url from the environment: used" "$o" "REDIS_ADDR=cp.example.com:6380"
assert_not_contains "redis url from the environment: password not printed" "$o" "$REDIS_PW"
assert_contains "redis password path documented" "$help_out" "WINTERFLOW_HUB_REDIS_URL"

# --- refuses_without_r2_env ---------------------------------------------------

for v in WINTERFLOW_R2_ENDPOINT WINTERFLOW_R2_ACCESS_KEY_ID WINTERFLOW_R2_SECRET_ACCESS_KEY; do
    out_nor2="$(env -u "$v" WF_INSTALL_ROOT="$debian" bash "$SCRIPT" --dry-run "${join_args[@]}" 2>&1)" \
        && fail "refuses_without_r2_env: ${v} unset" "exit 0" || pass "refuses_without_r2_env: ${v} unset"
    assert_contains "refuses_without_r2_env: names ${v}" "$out_nor2" "$v"
done
WINTERFLOW_R2_ENDPOINT=http://acct.r2.cloudflarestorage.com dry "${join_args[@]}" >/dev/null \
    && fail "plain http R2 endpoint rejected" "exit 0" || pass "plain http R2 endpoint rejected"

# --- refuses_non_debian -------------------------------------------------------

fedora="$(new_root fedora fedora)"
out_fed="$(WF_INSTALL_ROOT="$fedora" bash "$SCRIPT" --dry-run "${join_args[@]}" 2>&1)" \
    && fail "refuses_non_debian: exits non-zero" "exit 0" || pass "refuses_non_debian: exits non-zero"
assert_contains "refuses_non_debian: says why" "$out_fed" "Debian or Ubuntu"
ubuntu="$(new_root ubuntu ubuntu)"
WF_INSTALL_ROOT="$ubuntu" bash "$SCRIPT" --dry-run "${join_args[@]}" >/dev/null 2>&1 \
    && pass "ubuntu accepted" || fail "ubuntu accepted" "exit $?"

# --- first_install_requires_token_addr_redis ----------------------------------

o="$(dry --public-addr "$ADDR" --redis-url "$REDIS_URL")" && fail "first_install_requires_token_addr_redis: no token" "exit 0" || pass "first_install_requires_token_addr_redis: no token"
assert_contains "first_install_requires_token_addr_redis: asks for the token" "$o" "--token"
o="$(dry --token "$TOKEN" --redis-url "$REDIS_URL")" && fail "first_install_requires_token_addr_redis: no public addr" "exit 0" || pass "first_install_requires_token_addr_redis: no public addr"
assert_contains "first_install_requires_token_addr_redis: asks for the address" "$o" "--public-addr"
o="$(dry --token "$TOKEN" --public-addr "$ADDR")" && fail "first_install_requires_token_addr_redis: no redis url" "exit 0" || pass "first_install_requires_token_addr_redis: no redis url"
assert_contains "first_install_requires_token_addr_redis: asks for the redis url" "$o" "--redis-url"
o="$(WINTERFLOW_HUB_TOKEN="$TOKEN" WF_INSTALL_ROOT="$debian" bash "$SCRIPT" --dry-run --public-addr "$ADDR" --redis-url "$REDIS_URL" 2>&1)" \
    && pass "token from the environment accepted" || fail "token from the environment accepted" "$o"

# --- enrolls_and_writes_env ---------------------------------------------------

out="$(dry "${join_args[@]}" --capacity 500)" || fail "dry-run exits 0" "exit $?: $out"
env_out="$(section "$out" /etc/winterflow/hub.env)"
unit="$(section "$out" /etc/systemd/system/winterflow-hub.service)"
plan="$(section "$out" plan)"
assert_contains "enrolls_and_writes_env: public addr" "$env_out" "HUB_PUBLIC_ADDR=hub-02.example.com:50051"
assert_contains "enrolls_and_writes_env: port" "$env_out" "HUB_PORT=50051"
assert_contains "enrolls_and_writes_env: capacity" "$env_out" "HUB_CAPACITY=500"
assert_contains "enrolls_and_writes_env: cert dir" "$env_out" "HUB_CERT_DIR=/var/lib/winterflow-hub/certs"
assert_contains "enrolls_and_writes_env: redis addr" "$env_out" "REDIS_ADDR=cp.example.com:6380"
assert_contains "enrolls_and_writes_env: redis password line" "$env_out" "REDIS_PASSWORD="
assert_contains "enrolls_and_writes_env: redis tls" "$env_out" "REDIS_TLS=true"
assert_contains "enrolls_and_writes_env: redis ca is the joined ca" "$env_out" "REDIS_CA_CERT_PATH=/var/lib/winterflow-hub/certs/ca.crt"
assert_not_contains "enrolls_and_writes_env: no CA key anywhere" "$out" "ca.key"
assert_contains "enrolls_and_writes_env: join step" "$plan" "winterflow-hub join --api-url https://api.winterflow.dev --public-addr hub-02.example.com:50051 --cert-dir /var/lib/winterflow-hub/certs"
assert_contains "enrolls_and_writes_env: POST shown" "$plan" "POST https://api.winterflow.dev/api/v1/hub/enroll"
assert_contains "enrolls_and_writes_env: key stays here" "$plan" "hub.key 0640"
assert_contains "enrolls_and_writes_env: api url option" "$(dry "${join_args[@]}" --api-url https://api.example.com)" "--api-url https://api.example.com"
assert_not_contains "enrolls_and_writes_env: token never printed" "$out" "$TOKEN"
assert_not_contains "enrolls_and_writes_env: redis password never printed" "$out" "$REDIS_PW"
assert_not_contains "enrolls_and_writes_env: r2 secret never printed" "$out" "$SECRET"
assert_contains "downloads: sigv4" "$out" '--aws-sigv4 "aws:amz:auto:s3"'
assert_contains "downloads: latest hub" "$out" "https://acct.r2.cloudflarestorage.com/econumo/winterflow/latest/winterflow-hub-linux-"
assert_contains "downloads: checksums" "$out" "/econumo/winterflow/latest/SHA256SUMS"
assert_contains "--version picks the channel" "$(dry "${join_args[@]}" --version 260101.1 --r2-bucket other-bucket)" "/other-bucket/winterflow/v260101.1/winterflow-hub-linux-"
assert_not_contains "secret not on the curl command line" "$src" 'SECRET_ACCESS_KEY" "'
assert_contains "token travels on stdin" "$src" "printf '%s\\n' \"\$JOIN_TOKEN\" | "
assert_contains "listen port default is the public port; capacity default" "$(dry "${join_args[@]}")" "HUB_CAPACITY=1000"
out_lp="$(dry "${join_args[@]}" --listen-port 8443)"
assert_contains "listen-port overrides HUB_PORT" "$out_lp" "HUB_PORT=8443"
assert_contains "finish: open the listen port" "$out_lp" "TCP 8443"

# --- unit_has_timeout_stop_150 ------------------------------------------------

assert_contains "unit_has_timeout_stop_150" "$unit" "TimeoutStopSec=150"
assert_contains "unit: exec" "$unit" "ExecStart=/usr/local/bin/winterflow-hub"
assert_contains "unit: user" "$unit" "User=winterflow-hub"
assert_contains "unit: env file" "$unit" "EnvironmentFile=/etc/winterflow/hub.env"
assert_contains "unit: hardened" "$unit" "ProtectSystem=strict"
assert_contains "unit: no new privileges" "$unit" "NoNewPrivileges=true"
assert_contains "unit: journald namespace" "$out" "LogNamespace=winterflow"
assert_contains "unit: cpu quota" "$unit" "CPUQuota="
assert_contains "unit: memory max" "$unit" "MemoryMax="
assert_contains "unit: no capabilities on a high port" "$unit" "CapabilityBoundingSet="
assert_not_contains "unit: no bind capability on a high port" "$unit" "CAP_NET_BIND_SERVICE"
assert_contains "unit: bind capability on a low port" "$(dry --token "$TOKEN" --public-addr hub-02.example.com:443 --redis-url "$REDIS_URL")" "CAP_NET_BIND_SERVICE"

# --- rerun_updates_without_token ----------------------------------------------

rerun="$(new_root rerun debian)"
mkdir -p "$rerun/etc/winterflow" "$rerun/var/lib/winterflow-hub/certs"
for f in hub.key ca.crt; do echo "$f" > "$rerun/var/lib/winterflow-hub/certs/$f"; done
printf '%s\n' "$MARKED_PEM" > "$rerun/var/lib/winterflow-hub/certs/hub.crt"
cat > "$rerun/etc/winterflow/hub.env" <<EOF
LOG_LEVEL=info
HUB_PUBLIC_ADDR=hub-kept.example.com:50052
HUB_PORT=50052
HUB_CAPACITY=42
HUB_CERT_DIR=/var/lib/winterflow-hub/certs
REDIS_ADDR=kept-redis.example.com:6380
REDIS_PASSWORD=keptpw987654321
REDIS_TLS=true
REDIS_CA_CERT_PATH=/var/lib/winterflow-hub/certs/ca.crt
EOF
out_rerun="$(WF_INSTALL_ROOT="$rerun" bash "$SCRIPT" --dry-run 2>&1)" || fail "rerun_updates_without_token: exits 0" "exit $?: $out_rerun"
out_rerun="${out_rerun//$rerun/}"
assert_contains "rerun_updates_without_token: no enroll" "$(section "$out_rerun" plan)" "no token needed"
assert_not_contains "rerun_updates_without_token: no POST" "$out_rerun" "POST "
assert_not_contains "rerun_updates_without_token: no join" "$out_rerun" "hub join"
assert_contains "rerun_updates_without_token: binary replaced" "$out_rerun" "install /usr/local/bin/winterflow-hub"
assert_contains "rerun_updates_without_token: download" "$out_rerun" "winterflow-hub-linux-"
assert_contains "rerun_updates_without_token: restart" "$out_rerun" "systemctl restart winterflow-hub"
assert_contains "rerun keeps settings: addr" "$out_rerun" "HUB_PUBLIC_ADDR=hub-kept.example.com:50052"
assert_contains "rerun keeps settings: port" "$out_rerun" "HUB_PORT=50052"
assert_contains "rerun keeps settings: capacity" "$out_rerun" "HUB_CAPACITY=42"
assert_contains "rerun keeps settings: redis" "$out_rerun" "REDIS_ADDR=kept-redis.example.com:6380"
assert_not_contains "rerun masks the kept password" "$out_rerun" "keptpw987654321"
out_flag="$(WF_INSTALL_ROOT="$rerun" bash "$SCRIPT" --dry-run --capacity 7 2>&1)"
assert_contains "rerun: a flag overrides a kept setting" "$out_flag" "HUB_CAPACITY=7"
WF_INSTALL_ROOT="$rerun" bash "$SCRIPT" --dry-run --public-addr other.example.com:50052 >/dev/null 2>&1 \
    && fail "rerun: changing the public host refused" "exit 0" || pass "rerun: changing the public host refused"
out_redis="$(WF_INSTALL_ROOT="$rerun" bash "$SCRIPT" --dry-run --redis-url "rediss://:newpw123456@new-redis.example.com:6380" 2>&1)"
assert_contains "rerun: --redis-url overrides" "$out_redis" "REDIS_ADDR=new-redis.example.com:6380"

# --- rejoin -------------------------------------------------------------------

out_rejoin="$(WF_INSTALL_ROOT="$rerun" bash "$SCRIPT" --dry-run --token "$TOKEN" 2>&1)" \
    && pass "rejoin: a rerun with a token runs" || fail "rejoin: a rerun with a token runs" "$out_rejoin"
out_rejoin="${out_rejoin//$rerun/}"
assert_contains "rejoin: says the pair is replaced" "$(section "$out_rejoin" plan)" "rejoin: a new hub.key and hub.crt replace the old pair"
assert_contains "rejoin: join step" "$(section "$out_rejoin" plan)" "winterflow-hub join --api-url"
assert_not_contains "rejoin: token never printed" "$out_rejoin" "$TOKEN"
o="$(WINTERFLOW_HUB_TOKEN="$TOKEN" WF_INSTALL_ROOT="$rerun" bash "$SCRIPT" --dry-run 2>&1)"
assert_contains "rejoin: token from the environment" "$(section "$o" plan)" "rejoin:"
printf '%s\n' "$UNMARKED_PEM" > "$rerun/var/lib/winterflow-hub/certs/hub.crt"
out_old="$(WF_INSTALL_ROOT="$rerun" bash "$SCRIPT" --dry-run 2>&1)" \
    && fail "rejoin: a certificate without the hub URI needs a token" "exit 0" || pass "rejoin: a certificate without the hub URI needs a token"
assert_contains "rejoin: says a token is required to reissue" "$out_old" "a join token is required to reissue it"
out_old="$(WF_INSTALL_ROOT="$rerun" bash "$SCRIPT" --dry-run --token "$TOKEN" 2>&1)" \
    && pass "rejoin: a certificate without the hub URI rejoins with a token" || fail "rejoin: a certificate without the hub URI rejoins with a token" "$out_old"
assert_contains "rejoin: unmarked certificate reissued" "$(section "$out_old" plan)" "rejoin:"
printf '%s\n' "$MARKED_PEM" > "$rerun/var/lib/winterflow-hub/certs/hub.crt"

# --- uninstall ----------------------------------------------------------------

out_un="$(dry --uninstall)" || fail "uninstall dry-run exits 0" "exit $?: $out_un"
assert_contains "uninstall: stops the unit" "$out_un" "systemctl disable --now winterflow-hub"
assert_contains "uninstall: removes unit and binary" "$out_un" "rm -f /etc/systemd/system/winterflow-hub.service /usr/local/bin/winterflow-hub"
assert_contains "uninstall: keeps certs and env" "$out_un" "kept: /var/lib/winterflow-hub/certs"
assert_not_contains "uninstall: never removes data" "$out_un" "rm -rf"
env -u WINTERFLOW_R2_ENDPOINT -u WINTERFLOW_R2_ACCESS_KEY_ID -u WINTERFLOW_R2_SECRET_ACCESS_KEY \
    WF_INSTALL_ROOT="$debian" bash "$SCRIPT" --dry-run --uninstall >/dev/null 2>&1 \
    && pass "uninstall needs no R2 variables" || fail "uninstall needs no R2 variables" "exit $?"
out_purge="$(dry --uninstall --purge)"
assert_contains "purge: removes certs" "$out_purge" "rm -rf /var/lib/winterflow-hub/certs /var/lib/winterflow-hub"
assert_contains "purge: removes the env" "$out_purge" "rm -f /etc/winterflow/hub.env"

# --- real runs against a scratch root with stubbed system tools --------------

if [ "$(id -u)" -eq 0 ]; then
    pass "stubbed install runs skipped (running as root)"
else
    real_id="$(command -v id)"
    real_install="$(command -v install)"
    make_stubs() {
        # make_stubs ROOT [ENROLL_STATUS] — stubs in ROOT/stubs, R2 objects in ROOT/r2.
        local r="$1" bin="$1/stubs" a
        mkdir -p "$bin" "$r/r2"
        cat > "$bin/id" <<STUB
#!/bin/sh
if [ "\$1" = "-u" ] && [ \$# -eq 1 ]; then echo 0; exit 0; fi
if [ "\$1" = "-u" ]; then echo 999; exit 0; fi
case "\$1" in winterflow-*) exit 0 ;; esac
exec "$real_id" "\$@"
STUB
        cat > "$bin/install" <<STUB
#!/bin/bash
args=()
while [ \$# -gt 0 ]; do case "\$1" in -o|-g) shift 2 ;; *) args+=("\$1"); shift ;; esac; done
exec "$real_install" "\${args[@]}"
STUB
        printf '#!/bin/sh\necho "systemctl $*" >> "%s/calls"\ncase "$1" in --version) echo "systemd 255";; esac\nexit 0\n' "$r" > "$bin/systemctl"
        printf '#!/bin/sh\necho PONG\n' > "$bin/redis-cli"
        printf '#!/bin/sh\ncase "$1" in -s) echo Linux;; -m) echo x86_64;; esac\n' > "$bin/uname"
        for a in chown useradd sleep; do printf '#!/bin/sh\nexit 0\n' > "$bin/$a"; done
        # curl: R2 downloads serve r2/<name>; the credentials arrive on stdin.
        cat > "$bin/curl" <<STUB
#!/bin/sh
if [ "\$1" = "--version" ]; then echo "curl 8.5.0 (x86_64-pc-linux-gnu)"; exit 0; fi
echo "curl \$*" >> "$r/curl-args"
out=""; url=""
while [ \$# -gt 0 ]; do case "\$1" in -o) out="\$2"; shift 2 ;; -*) shift ;; *) url="\$1"; shift ;; esac; done
cat >> "$r/curl-stdin"
cp "$r/r2/\${url##*/}" "\$out"
STUB
        # fake winterflow-hub: only `join`. It logs its argv and stdin and
        # writes the key and certificates, or fails with $r/join-exit.
        cat > "$r/r2/winterflow-hub-linux-amd64" <<STUB
#!/bin/sh
[ "\$1" = join ] || exit 0
echo "\$*" >> "$r/join-args"
cat > "$r/join-stdin"
if [ -f "$r/join-exit" ]; then echo "the control plane refused the join: invalid or expired token" >&2; exit "\$(cat "$r/join-exit")"; fi
while [ \$# -gt 0 ]; do case "\$1" in --cert-dir) dir="\$2"; shift 2 ;; *) shift ;; esac; done
echo "private key \$\$" > "\$dir/hub.key"; chmod 600 "\$dir/hub.key"
cat "$r/hub-pem" > "\$dir/hub.crt"; echo CACERT > "\$dir/ca.crt"
echo "joined: certificate stored"
STUB
        printf '%s\n' "$MARKED_PEM" > "$r/hub-pem"
        (cd "$r/r2" && sha256sum winterflow-hub-linux-amd64 > SHA256SUMS)
        chmod +x "$bin"/*
        mkdir -p "$r/etc" "$r/usr/local/bin" "$r/etc/systemd/system"
        printf 'ID=debian\n' > "$r/etc/os-release"
        : > "$r/calls"; : > "$r/curl-args"; : > "$r/curl-stdin"
    }
    run_hub() {
        # run_hub ROOT [args] -> output in RUN_OUT, status in RUN_STATUS
        local r="$1"; shift
        RUN_STATUS=0
        RUN_OUT="$(PATH="$r/stubs:$PATH" WF_INSTALL_ROOT="$r" TMPDIR="$r" \
            bash "$SCRIPT" --yes "$@" </dev/null 2>&1)" || RUN_STATUS=$?
    }

    r1="$tmp/real1"; mkdir -p "$r1"; make_stubs "$r1"
    run_hub "$r1" "${join_args[@]}"
    [ "$RUN_STATUS" -eq 0 ] && pass "real run: installer succeeds" || fail "real run: installer succeeds" "status $RUN_STATUS: $RUN_OUT"
    cd1="$r1/var/lib/winterflow-hub/certs"
    assert_contains "real run: hub.crt stored" "$(cat "$cd1/hub.crt" 2>/dev/null)" "BEGIN CERTIFICATE"
    assert_contains "real run: ca.crt stored" "$(cat "$cd1/ca.crt" 2>/dev/null)" "CACERT"
    [ "$(stat -c %a "$cd1/hub.key")" = 640 ] && pass "real run: hub.key 0640" || fail "real run: hub.key 0640" "$(stat -c %a "$cd1/hub.key")"
    [ "$(stat -c %a "$cd1")" = 750 ] && pass "real run: cert dir 0750" || fail "real run: cert dir 0750" "$(stat -c %a "$cd1")"
    [ "$(stat -c %a "$r1/etc/winterflow/hub.env")" = 600 ] && pass "real run: hub.env 0600" || fail "real run: hub.env 0600" "$(stat -c %a "$r1/etc/winterflow/hub.env")"
    assert_contains "real run: env password" "$(cat "$r1/etc/winterflow/hub.env")" "REDIS_PASSWORD=${REDIS_PW}"
    assert_contains "real run: env addr" "$(cat "$r1/etc/winterflow/hub.env")" "REDIS_ADDR=cp.example.com:6380"
    assert_contains "real run: join gets the token on stdin" "$(cat "$r1/join-stdin")" "$TOKEN"
    assert_contains "real run: join args" "$(cat "$r1/join-args")" "join --api-url https://api.winterflow.dev --public-addr ${ADDR} --cert-dir ${cd1}"
    assert_not_contains "real run: token never in join's argv" "$(cat "$r1/join-args")" "$TOKEN"
    assert_not_contains "real run: token never on a curl command line" "$(cat "$r1/curl-args")" "$TOKEN"
    assert_not_contains "real run: r2 secret never on a command line" "$(cat "$r1/curl-args")" "$SECRET"
    assert_contains "real run: r2 secret passed on curl's stdin" "$(cat "$r1/curl-stdin")" "$SECRET"
    assert_not_contains "real run: password not in the output" "$RUN_OUT" "$REDIS_PW"
    assert_not_contains "real run: token not in the output" "$RUN_OUT" "$TOKEN"
    [ -x "$r1/usr/local/bin/winterflow-hub" ] && pass "real run: binary installed" || fail "real run: binary installed" "missing"
    [ -f "$r1/etc/systemd/system/winterflow-hub.service" ] && pass "real run: unit written" || fail "real run: unit written" "missing"
    assert_contains "real run: service restarted" "$(cat "$r1/calls")" "systemctl restart winterflow-hub"

    # Rerun: no token, no enroll, binary replaced, restart, settings kept.
    : > "$r1/calls"; rm -f "$r1/join-args"
    run_hub "$r1" --capacity 77
    [ "$RUN_STATUS" -eq 0 ] && pass "rerun: installer succeeds without a token" || fail "rerun: installer succeeds without a token" "status $RUN_STATUS: $RUN_OUT"
    [ ! -e "$r1/join-args" ] && pass "rerun: no join" || fail "rerun: no join" "joined"
    assert_contains "rerun: capacity overridden" "$(cat "$r1/etc/winterflow/hub.env")" "HUB_CAPACITY=77"
    assert_contains "rerun: redis password kept" "$(cat "$r1/etc/winterflow/hub.env")" "REDIS_PASSWORD=${REDIS_PW}"
    assert_contains "rerun: addr kept" "$(cat "$r1/etc/winterflow/hub.env")" "HUB_PUBLIC_ADDR=${ADDR}"
    assert_contains "rerun: certificate kept" "$(cat "$cd1/hub.crt")" "BEGIN CERTIFICATE"
    assert_contains "rerun: service restarted" "$(cat "$r1/calls")" "systemctl restart winterflow-hub"

    # Rerun with a token: joins again, a new key replaces the old one.
    key_before="$(cat "$cd1/hub.key")"; rm -f "$r1/join-args" "$r1/join-stdin"
    run_hub "$r1" --token "$TOKEN"
    [ "$RUN_STATUS" -eq 0 ] && pass "rejoin: installer succeeds" || fail "rejoin: installer succeeds" "status $RUN_STATUS: $RUN_OUT"
    [ -e "$r1/join-args" ] && pass "rejoin: join runs" || fail "rejoin: join runs" "no join"
    assert_contains "rejoin: token on stdin" "$(cat "$r1/join-stdin" 2>/dev/null)" "$TOKEN"
    [ "$(cat "$cd1/hub.key")" != "$key_before" ] && pass "rejoin: new key" || fail "rejoin: new key" "key kept"
    [ "$(stat -c %a "$cd1/hub.key")" = 640 ] && pass "rejoin: hub.key 0640" || fail "rejoin: hub.key 0640" "$(stat -c %a "$cd1/hub.key")"
    assert_not_contains "rejoin: token not in the output" "$RUN_OUT" "$TOKEN"

    # A certificate from before the hub marker: a rerun without a token stops
    # before anything changes; with one it is reissued.
    printf '%s\n' "$UNMARKED_PEM" > "$cd1/hub.crt"; : > "$r1/calls"; rm -f "$r1/join-args"
    run_hub "$r1"
    [ "$RUN_STATUS" -ne 0 ] && pass "unmarked certificate: rerun without a token refused" || fail "unmarked certificate: rerun without a token refused" "status 0"
    assert_contains "unmarked certificate: asks for a token" "$RUN_OUT" "a join token is required to reissue it"
    [ ! -s "$r1/calls" ] && [ ! -e "$r1/join-args" ] && pass "unmarked certificate: nothing changed" || fail "unmarked certificate: nothing changed" "$(cat "$r1/calls")"
    run_hub "$r1" --token "$TOKEN"
    [ "$RUN_STATUS" -eq 0 ] && [ -e "$r1/join-args" ] && pass "unmarked certificate: reissued with a token" || fail "unmarked certificate: reissued with a token" "status $RUN_STATUS: $RUN_OUT"
    assert_contains "unmarked certificate: marked one stored" "$(cat "$cd1/hub.crt")" "$(printf '%s\n' "$MARKED_PEM" | sed -n 2p)"

    # Real uninstall: unit and binary go, certs and env stay.
    : > "$r1/calls"
    RUN_STATUS=0
    RUN_OUT="$(PATH="$r1/stubs:$PATH" WF_INSTALL_ROOT="$r1" bash "$SCRIPT" --uninstall </dev/null 2>&1)" || RUN_STATUS=$?
    [ "$RUN_STATUS" -eq 0 ] && pass "uninstall: succeeds" || fail "uninstall: succeeds" "status $RUN_STATUS: $RUN_OUT"
    [ ! -e "$r1/etc/systemd/system/winterflow-hub.service" ] && [ ! -e "$r1/usr/local/bin/winterflow-hub" ] \
        && pass "uninstall: unit and binary removed" || fail "uninstall: unit and binary removed" "still there"
    [ -f "$cd1/hub.key" ] && [ -f "$r1/etc/winterflow/hub.env" ] \
        && pass "uninstall: certs and env kept" || fail "uninstall: certs and env kept" "removed"
    RUN_OUT="$(PATH="$r1/stubs:$PATH" WF_INSTALL_ROOT="$r1" bash "$SCRIPT" --uninstall --purge </dev/null 2>&1)" && RUN_STATUS=0 || RUN_STATUS=$?
    [ "$RUN_STATUS" -ne 0 ] && [ -f "$cd1/hub.key" ] && pass "purge without a terminal needs --yes" || fail "purge without a terminal needs --yes" "status $RUN_STATUS"
    RUN_OUT="$(PATH="$r1/stubs:$PATH" WF_INSTALL_ROOT="$r1" bash "$SCRIPT" --uninstall --purge --yes </dev/null 2>&1)" || true
    [ ! -e "$cd1" ] && [ ! -e "$r1/etc/winterflow/hub.env" ] \
        && pass "purge: certs and env removed" || fail "purge: certs and env removed" "still there"

    # A control-plane or standalone host is never touched: install, uninstall
    # and purge all refuse (the units and binary names are shared).
    for marker in control-plane.env winterflow.env; do
        r5="$tmp/real5-$marker"; mkdir -p "$r5"; make_stubs "$r5"
        mkdir -p "$r5/etc/winterflow"; : > "$r5/etc/winterflow/$marker"
        echo cp-binary > "$r5/usr/local/bin/winterflow-hub"; echo cp-unit > "$r5/etc/systemd/system/winterflow-hub.service"
        for mode in "" "--uninstall" "--uninstall --purge --yes" "--dry-run" "--dry-run --uninstall"; do
            : > "$r5/calls"
            # shellcheck disable=SC2086 # mode is a word list
            RUN_OUT="$(PATH="$r5/stubs:$PATH" WF_INSTALL_ROOT="$r5" TMPDIR="$r5" bash "$SCRIPT" --yes $mode "${join_args[@]}" </dev/null 2>&1)" \
                && fail "${marker} host refused: '${mode}'" "exit 0" || pass "${marker} host refused: '${mode}'"
            [ ! -s "$r5/calls" ] && [ "$(cat "$r5/usr/local/bin/winterflow-hub")" = cp-binary ] && [ -f "$r5/etc/systemd/system/winterflow-hub.service" ] \
                && pass "${marker} host untouched: '${mode}'" || fail "${marker} host untouched: '${mode}'" "$(cat "$r5/calls")"
        done
    done

    # A refused join (exit 2) stops the install and says why; nothing is started.
    r2="$tmp/real2"; mkdir -p "$r2"; make_stubs "$r2"
    printf '2' > "$r2/join-exit"
    run_hub "$r2" "${join_args[@]}"
    [ "$RUN_STATUS" -ne 0 ] && pass "refused join: installer fails" || fail "refused join: installer fails" "status 0"
    assert_contains "refused join: says why" "$RUN_OUT" "invalid or expired token"
    [ ! -e "$r2/var/lib/winterflow-hub/certs/hub.crt" ] && pass "refused join: no certificate written" || fail "refused join: no certificate written" "hub.crt exists"
    assert_not_contains "refused join: service not started" "$(cat "$r2/calls")" "restart winterflow-hub"
    assert_not_contains "refused join: token not in the output" "$RUN_OUT" "$TOKEN"

    # A tampered download fails the checksum before the token is spent.
    r3="$tmp/real3"; mkdir -p "$r3"; make_stubs "$r3"
    echo tampered >> "$r3/r2/winterflow-hub-linux-amd64"
    run_hub "$r3" "${join_args[@]}"
    [ "$RUN_STATUS" -ne 0 ] && pass "checksum mismatch: installer fails" || fail "checksum mismatch: installer fails" "status 0"
    assert_contains "checksum mismatch: says so" "$RUN_OUT" "checksum mismatch for winterflow-hub-linux-amd64"
    [ ! -e "$r3/join-args" ] && pass "checksum mismatch: token not spent" || fail "checksum mismatch: token not spent" "joined"
fi

echo
if [ "$FAILURES" -eq 0 ]; then echo "ALL TESTS PASSED"; else echo "${FAILURES} TEST(S) FAILED"; exit 1; fi
