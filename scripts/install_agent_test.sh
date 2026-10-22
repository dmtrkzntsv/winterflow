#!/usr/bin/env bash
#
# Tests for scripts/install-agent.sh. Uses --dry-run so nothing on the host is
# touched and root is not required. Run: bash scripts/install_agent_test.sh
set -uo pipefail

SCRIPT="$(cd "$(dirname "$0")" && pwd)/install-agent.sh"
STANDALONE="$(cd "$(dirname "$0")" && pwd)/install.sh"
FAILURES=0

pass() { printf 'ok   %s\n' "$1"; }
fail() { printf 'FAIL %s\n     %s\n' "$1" "$2"; FAILURES=$((FAILURES + 1)); }
assert_contains() { if grep -qF -- "$3" <<<"$2"; then pass "$1"; else fail "$1" "missing: $3"; fi; }
assert_not_contains() { if grep -qF -- "$3" <<<"$2"; then fail "$1" "unexpected: $3"; else pass "$1"; fi; }

bash -n "$SCRIPT" && pass "syntax check" || fail "syntax check" "bash -n failed"

src="$(cat "$SCRIPT")"
assert_contains "script body runs via a main wrapper" "$src" 'main "$@"'
assert_not_contains "never reads /dev/tty (hangs under sudo use_pty)" "$src" "</dev/tty"

# --- help / validation --------------------------------------------------------

help_out="$(bash "$SCRIPT" --help)" || fail "--help exits 0" "exit $?"
for opt in --url --version --binary --user --cpu-quota --memory-max --yes --dry-run --uninstall --purge --reenroll; do
    assert_contains "--help documents ${opt}" "$help_out" "$opt"
done
bash "$SCRIPT" --nonsense >/dev/null 2>&1 && fail "unknown option rejected" "exit 0" || pass "unknown option rejected"
bash "$SCRIPT" --dry-run --url http://example.com >/dev/null 2>&1 && fail "plain http url to a remote host rejected" "exit 0" || pass "plain http url to a remote host rejected"
bash "$SCRIPT" --dry-run --url 'not a url' >/dev/null 2>&1 && fail "garbage url rejected" "exit 0" || pass "garbage url rejected"
bash "$SCRIPT" --dry-run --user root >/dev/null 2>&1 && fail "--user root rejected" "exit 0" || pass "--user root rejected"
bash "$SCRIPT" --dry-run --cpu-quota abc >/dev/null 2>&1 && fail "non-numeric cpu quota rejected" "exit 0" || pass "non-numeric cpu quota rejected"
bash "$SCRIPT" --dry-run --url http://localhost:8080 >/dev/null 2>&1 && pass "http url to localhost accepted" || fail "http url to localhost accepted" "exit $?"

# --- unit ---------------------------------------------------------------------

out="$(bash "$SCRIPT" --dry-run --cpu-quota 250 --memory-max 1G 2>&1)" || fail "dry-run exits 0" "exit $?"

assert_contains "agent_unit_has_restart_prevent_78" "$out" "RestartPreventExitStatus=78"
assert_contains "unit: runs the agent binary" "$out" "ExecStart=/usr/local/bin/winterflow-agent"
assert_contains "unit: env file is agent.env" "$out" "EnvironmentFile=/etc/winterflow/agent.env"
assert_contains "unit: runs as service user" "$out" "User=winterflow"
assert_not_contains "unit: never runs as root" "$out" "User=root"
assert_contains "unit: cpu quota applied" "$out" "CPUQuota=250%"
assert_contains "unit: memory cap applied" "$out" "MemoryMax=1G"
assert_contains "unit: task cap" "$out" "TasksMax="
assert_contains "unit: dedicated journal namespace" "$out" "LogNamespace=winterflow"
assert_contains "unit: strict protect system" "$out" "ProtectSystem=strict"
assert_contains "unit: no new privileges" "$out" "NoNewPrivileges=true"
assert_contains "unit: data dir writable" "$out" "ReadWritePaths=/var/lib/winterflow"
assert_contains "unit: docker socket writable" "$out" "ReadWritePaths=-/run/docker.sock"
assert_not_contains "unit: no CAP_SYS_PTRACE" "$out" "CAP_SYS_PTRACE"
assert_contains "env: data dir" "$out" "AGENT_DATA_DIR=/var/lib/winterflow"
assert_contains "env: cert dir" "$out" "HUB_CERT_DIR=/var/lib/winterflow/certs"

# --- enrollment URL -----------------------------------------------------------

out_piped="$(cat "$SCRIPT" | bash -s -- --dry-run 2>&1)" || fail "piped dry-run exits 0" "exit $?"
assert_contains "agent_default_url_piped" "$out_piped" "enroll --url https://api.winterflow.dev"

out_url="$(bash "$SCRIPT" --dry-run --url https://api.example.com/ 2>&1)" || fail "--url dry-run exits 0" "exit $?"
assert_contains "agent_url_flag" "$out_url" "enroll --url https://api.example.com --cert-dir /var/lib/winterflow/certs"
assert_not_contains "agent_url_flag: default not used" "$out_url" "api.winterflow.dev"
assert_contains "enroll runs as the service user" "$out_url" "run as 'winterflow'"

# --- rerun --------------------------------------------------------------------

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
mkdir -p "$root/var/lib/winterflow/certs"
: > "$root/var/lib/winterflow/certs/agent.crt"
out_rerun="$(WF_INSTALL_ROOT="$root" bash "$SCRIPT" --dry-run 2>&1)" || fail "rerun dry-run exits 0" "exit $?"
assert_contains "agent_rerun_keeps_enrollment: skip is announced" "$out_rerun" "already enrolled"
assert_not_contains "agent_rerun_keeps_enrollment: no enroll call" "$out_rerun" "winterflow-agent enroll"
assert_contains "agent_rerun_keeps_enrollment: unit is refreshed" "$out_rerun" "RestartPreventExitStatus=78"
assert_contains "fresh machine does enroll" "$out_url" "winterflow-agent enroll"
assert_contains "rerun: points at --reenroll" "$out_rerun" "--reenroll"
out_reenroll="$(WF_INSTALL_ROOT="$root" bash "$SCRIPT" --dry-run --reenroll 2>&1)" || fail "--reenroll dry-run exits 0" "exit $?"
assert_contains "reenroll dry-run: old enrollment set aside" "$out_reenroll" "set aside as *.old-<timestamp>"
assert_contains "reenroll dry-run: enrolls again" "$out_reenroll" "winterflow-agent enroll --url"
assert_not_contains "reenroll dry-run: no skip" "$out_reenroll" "already enrolled"

# --- release download ---------------------------------------------------------

assert_contains "agent_downloads_new_asset_name" "$out" "asset: agent-linux-"
assert_contains "asset template" "$src" "agent-%s-%s"
assert_not_contains "no old asset name" "$src" "winterflow-agent-%s"
assert_contains "downloads are checksum-verified" "$src" "sha256sum -c"
assert_contains "release download is HTTPS from the project repo" "$src" 'GITHUB_REPO="dmtrkzntsv/winterflow"'
assert_contains "enroll output is not captured" "$src" '--cert-dir "$CERT_DIR" --env-out "$ENROLL_ENV"'
assert_contains "unit is not enabled before enrollment" "$src" "written, not enabled until enrollment succeeded"

# --- standalone asset name ----------------------------------------------------

assert_contains "standalone_uses_new_asset_name" "$(bash "$STANDALONE" --dry-run 2>&1; cat "$STANDALONE")" "winterflow-%s-%s"
assert_not_contains "standalone: no old asset name" "$(cat "$STANDALONE")" "winterflow-standalone-"

# --- real runs against a scratch root with stubbed system tools --------------

# Exercises the enrollment exit codes (0/2/3/1), the env merge and the
# rejection of injected lines. Stubs on PATH stand in for id -u (root check),
# systemctl, runuser, docker and the ownership commands; a fake agent binary
# plays `enroll`. Skipped when the tests already run as root.
if [ "$(id -u)" -eq 0 ]; then
    pass "stubbed install runs skipped (running as root)"
else
    me="$(id -un)"
    real_id="$(command -v id)"
    real_install="$(command -v install)"
    run_install() {
        # run_install ROOT EXITCODE ENVCONTENT [extra args] -> output, status in RUN_STATUS
        local r="$1" code="$2" content="$3"; shift 3
        local bin="$r/stubs"
        mkdir -p "$bin" "$r/fake"
        cat > "$bin/id" <<STUB
#!/bin/sh
if [ "\$1" = "-u" ] && [ \$# -eq 1 ]; then echo 0; exit 0; fi
exec "$real_id" "\$@"
STUB
        cat > "$bin/install" <<STUB
#!/bin/bash
args=()
while [ \$# -gt 0 ]; do case "\$1" in -o|-g) shift 2 ;; *) args+=("\$1"); shift ;; esac; done
exec "$real_install" "\${args[@]}"
STUB
        printf '#!/bin/sh\necho "systemctl $*" >> "%s/calls"\ncase "$1" in --version) echo "systemd 255";; is-active) exit 0;; esac\nexit 0\n' "$r" > "$bin/systemctl"
        printf '#!/bin/sh\nshift 3\nexec "$@"\n' > "$bin/runuser"
        printf '#!/bin/sh\nexit 0\n' > "$bin/docker"
        printf '#!/bin/sh\nexit 0\n' > "$bin/chown"
        printf '#!/bin/sh\nexit 0\n' > "$bin/usermod"
        printf '#!/bin/sh\nexit 0\n' > "$bin/sleep"
        cat > "$r/fake/agent" <<STUB
#!/bin/sh
# fake winterflow-agent: only the enroll subcommand
[ "\$1" = enroll ] || exit 9
while [ \$# -gt 0 ]; do case "\$1" in --env-out) out="\$2"; shift 2 ;; --cert-dir) dir="\$2"; shift 2 ;; *) shift ;; esac; done
echo "CODE: ABC123"
if [ "$code" = 0 ]; then printf '%s' '$content' > "\$out"; : > "\$dir/agent.crt"; fi
exit $code
STUB
        chmod +x "$bin"/* "$r/fake/agent"
        mkdir -p "$r/etc/docker" "$r/usr/local/bin" "$r/etc/systemd/system"
        echo '"log-opts"' > "$r/etc/docker/daemon.json"
        : > "$r/calls"
        RUN_STATUS=0
        RUN_OUT="$(PATH="$bin:$PATH" WF_INSTALL_ROOT="$r" DOCKER_DAEMON_JSON="$r/etc/docker/daemon.json" \
            bash "$SCRIPT" --yes --user "$me" --binary "$r/fake/agent" "$@" </dev/null 2>&1)" || RUN_STATUS=$?
    }
    good=$'HUB_HOST=hub-01.winterflow.dev\nHUB_PORT=50051\nAGENT_ID=srv-1\nWINTERFLOW_API_URL=https://api.winterflow.dev\n'

    r1="$(mktemp -d)"
    run_install "$r1" 0 "$good"
    [ "$RUN_STATUS" -eq 0 ] && pass "enroll exit 0: installer succeeds" || fail "enroll exit 0: installer succeeds" "status $RUN_STATUS: $RUN_OUT"
    envf="$r1/etc/winterflow/agent.env"
    assert_contains "enroll exit 0: hub host merged" "$(cat "$envf" 2>/dev/null)" "HUB_HOST=hub-01.winterflow.dev"
    assert_contains "enroll exit 0: base settings kept" "$(cat "$envf" 2>/dev/null)" "HUB_CERT_DIR="
    assert_contains "enroll exit 0: code shown live" "$RUN_OUT" "CODE: ABC123"
    assert_contains "enroll exit 0: unit enabled" "$(cat "$r1/calls")" "systemctl enable winterflow-agent"
    assert_contains "enroll exit 0: unit restarted" "$(cat "$r1/calls")" "systemctl restart winterflow-agent"
    [ ! -e "$r1/var/lib/winterflow/certs/enroll.env" ] && pass "enroll exit 0: temp enroll.env removed" || fail "enroll exit 0: temp enroll.env removed" "still there"
    [ "$(stat -c %a "$envf")" = 600 ] && pass "enroll exit 0: agent.env is 0600" || fail "enroll exit 0: agent.env is 0600" "$(stat -c %a "$envf")"

    # Rerun: agent.crt exists, enroll is not called again, env keeps HUB_HOST.
    : > "$r1/calls"
    RUN_STATUS=0
    RUN_OUT="$(PATH="$r1/stubs:$PATH" WF_INSTALL_ROOT="$r1" DOCKER_DAEMON_JSON="$r1/etc/docker/daemon.json" bash "$SCRIPT" --yes --user "$me" --binary "$r1/fake/agent" </dev/null 2>&1)" || RUN_STATUS=$?
    [ "$RUN_STATUS" -eq 0 ] && pass "enroll exit 3 (rerun): installer succeeds" || fail "enroll exit 3 (rerun): installer succeeds" "status $RUN_STATUS: $RUN_OUT"
    assert_not_contains "enroll exit 3 (rerun): no new code" "$RUN_OUT" "CODE: ABC123"
    assert_contains "enroll exit 3 (rerun): unit restarted" "$(cat "$r1/calls")" "systemctl restart winterflow-agent"
    assert_contains "enroll exit 3 (rerun): --reenroll hint" "$RUN_OUT" "rerun with --reenroll"

    # --reenroll: the unit is stopped, the old cert/key/CA and the enrollment
    # lines are set aside, and a new enrollment is stored.
    r2="$(mktemp -d)"
    run_install "$r2" 0 "$good"
    certs="$r2/var/lib/winterflow/certs"
    echo old-crt > "$certs/agent.crt"; echo old-key > "$certs/agent.key"; echo old-ca > "$certs/ca.crt"
    run_install "$r2" 0 "${good/srv-1/srv-2}" --reenroll
    [ "$RUN_STATUS" -eq 0 ] && pass "reenroll: installer succeeds" || fail "reenroll: installer succeeds" "status $RUN_STATUS: $RUN_OUT"
    assert_contains "reenroll: a new code is shown" "$RUN_OUT" "CODE: ABC123"
    calls2="$(cat "$r2/calls")"
    first="$(grep -m1 -E 'disable --now winterflow|restart winterflow' <<<"$calls2")"
    [ "$first" = "systemctl disable --now winterflow-agent" ] && pass "reenroll: unit stopped before enrolling" || fail "reenroll: unit stopped before enrolling" "$calls2"
    assert_contains "reenroll: unit restarted" "$calls2" "systemctl restart winterflow-agent"
    for f in agent.crt agent.key ca.crt; do
        set -- "$certs/$f".old-*
        [ -f "$1" ] && pass "reenroll: ${f} set aside" || fail "reenroll: ${f} set aside" "no ${f}.old-*"
        assert_contains "reenroll: ${f} backup holds the old file" "$(cat "$1" 2>/dev/null)" "old-"
    done
    env2="$(cat "$r2/etc/winterflow/agent.env")"
    assert_contains "reenroll: new server id stored" "$env2" "AGENT_ID=srv-2"
    assert_not_contains "reenroll: old server id gone" "$env2" "AGENT_ID=srv-1"
    [ "$(grep -c '^HUB_HOST=' <<<"$env2")" = 1 ] && pass "reenroll: one HUB_HOST line" || fail "reenroll: one HUB_HOST line" "$env2"
    set -- "$r2/etc/winterflow/agent.env".old-*
    assert_contains "reenroll: old env lines kept aside" "$(cat "$1" 2>/dev/null)" "AGENT_ID=srv-1"
    [ "$(stat -c %a "$1" 2>/dev/null)" = 600 ] && pass "reenroll: env backup is 0600" || fail "reenroll: env backup is 0600" "$(stat -c %a "$1" 2>/dev/null)"
    rm -rf "$r2"

    # Rerun where agent.env lost its hub: an error, the unit stays off.
    grep -v '^HUB_HOST=' "$envf" > "$envf.new" && mv "$envf.new" "$envf"
    : > "$r1/calls"
    RUN_STATUS=0
    RUN_OUT="$(PATH="$r1/stubs:$PATH" WF_INSTALL_ROOT="$r1" DOCKER_DAEMON_JSON="$r1/etc/docker/daemon.json" bash "$SCRIPT" --yes --user "$me" --binary "$r1/fake/agent" </dev/null 2>&1)" || RUN_STATUS=$?
    [ "$RUN_STATUS" -ne 0 ] && pass "exit 3 without HUB_HOST: installer fails" || fail "exit 3 without HUB_HOST: installer fails" "status 0"
    assert_contains "exit 3 without HUB_HOST: explains re-enrollment" "$RUN_OUT" "rerun this installer with --reenroll"
    assert_not_contains "exit 3 without HUB_HOST: not restarted" "$(cat "$r1/calls")" "systemctl restart"

    for code in 2 1; do
        r="$(mktemp -d)"
        run_install "$r" "$code" ""
        [ "$RUN_STATUS" -ne 0 ] && pass "enroll exit ${code}: installer fails" || fail "enroll exit ${code}: installer fails" "status 0"
        assert_contains "enroll exit ${code}: unit disabled and stopped" "$(cat "$r/calls")" "systemctl disable --now winterflow-agent"
        assert_not_contains "enroll exit ${code}: never enabled" "$(cat "$r/calls")" "systemctl enable winterflow-agent"
        assert_not_contains "enroll exit ${code}: never started" "$(cat "$r/calls")" "systemctl restart"
        rm -rf "$r"
    done

    # Injected lines never reach agent.env; the installer refuses.
    for bad in $'HUB_HOST=hub.example.com\nHUB_PORT=50051\nAGENT_ID=srv-1\nWINTERFLOW_API_URL=https://api.winterflow.dev\nLD_PRELOAD=/tmp/x.so\n' \
               $'HUB_HOST=hub.example.com\nHUB_PORT=50051\nAGENT_ID=srv-1 x\nWINTERFLOW_API_URL=https://api.winterflow.dev\n' \
               $'HUB_HOST=hub.example.com\nHUB_PORT=99999\nAGENT_ID=srv-1\nWINTERFLOW_API_URL=https://api.winterflow.dev\n' \
               $'HUB_HOST=hub.example.com\nHUB_PORT=50051\nAGENT_ID=srv-1\n'; do
        r="$(mktemp -d)"
        run_install "$r" 0 "$bad"
        [ "$RUN_STATUS" -ne 0 ] && pass "rejected enrollment output: installer fails" || fail "rejected enrollment output: installer fails" "status 0"
        assert_not_contains "rejected enrollment output: nothing injected" "$(cat "$r/etc/winterflow/agent.env" 2>/dev/null)" "LD_PRELOAD"
        assert_not_contains "rejected enrollment output: no HUB_HOST written" "$(cat "$r/etc/winterflow/agent.env" 2>/dev/null)" "HUB_HOST="
        assert_not_contains "rejected enrollment output: never enabled" "$(cat "$r/calls")" "systemctl enable winterflow-agent"
        assert_contains "rejected enrollment output: unit stopped" "$(cat "$r/calls")" "systemctl disable --now winterflow-agent"
        rm -rf "$r"
    done

    # Missing runuser and setpriv: refused before enrolling, unit stays off.
    r="$(mktemp -d)"
    run_install "$r" 0 "$good" >/dev/null 2>&1; rm -f "$r/stubs/runuser" "$r/var/lib/winterflow/certs/agent.crt"; : > "$r/calls"
    if ! command -v setpriv >/dev/null 2>&1; then
        RUN_STATUS=0
        RUN_OUT="$(PATH="$r/stubs:$PATH" WF_INSTALL_ROOT="$r" DOCKER_DAEMON_JSON="$r/etc/docker/daemon.json" bash "$SCRIPT" --yes --user "$me" --binary "$r/fake/agent" </dev/null 2>&1)" || RUN_STATUS=$?
        [ "$RUN_STATUS" -ne 0 ] && pass "no runuser/setpriv: installer fails" || fail "no runuser/setpriv: installer fails" "status 0"
    fi
    rm -rf "$r" "$r1"
fi

echo
if [ "$FAILURES" -eq 0 ]; then echo "ALL TESTS PASSED"; else echo "${FAILURES} TEST(S) FAILED"; exit 1; fi
