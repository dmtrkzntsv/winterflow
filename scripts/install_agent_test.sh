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
for opt in --url --version --binary --user --cpu-quota --memory-max --yes --dry-run --uninstall --purge; do
    assert_contains "--help documents ${opt}" "$help_out" "$opt"
done
bash "$SCRIPT" --nonsense >/dev/null 2>&1 && fail "unknown option rejected" "exit 0" || pass "unknown option rejected"
bash "$SCRIPT" --dry-run --url http://example.com >/dev/null 2>&1 && fail "plain http url to a remote host rejected" "exit 0" || pass "plain http url to a remote host rejected"
bash "$SCRIPT" --dry-run --url 'not a url' >/dev/null 2>&1 && fail "garbage url rejected" "exit 0" || pass "garbage url rejected"
bash "$SCRIPT" --dry-run --user root >/dev/null 2>&1 && fail "--user root rejected" "exit 0" || pass "--user root rejected"
bash "$SCRIPT" --dry-run --cpu-quota abc >/dev/null 2>&1 && fail "non-numeric cpu quota rejected" "exit 0" || pass "non-numeric cpu quota rejected"
bash "$SCRIPT" --dry-run --url http://localhost:8080 >/dev/null 2>&1 && pass "http url to localhost accepted" || fail "http url to localhost accepted" "exit $?"

# --- unit ---------------------------------------------------------------------

out="$(WF_INSTALL_ROOT=/nonexistent bash "$SCRIPT" --dry-run --cpu-quota 250 --memory-max 1G 2>&1)" || fail "dry-run exits 0" "exit $?"

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

out_piped="$(cat "$SCRIPT" | WF_INSTALL_ROOT=/nonexistent bash -s -- --dry-run 2>&1)" || fail "piped dry-run exits 0" "exit $?"
assert_contains "agent_default_url_piped" "$out_piped" "enroll --url https://api.winterflow.dev"

out_url="$(WF_INSTALL_ROOT=/nonexistent bash "$SCRIPT" --dry-run --url https://api.example.com/ 2>&1)" || fail "--url dry-run exits 0" "exit $?"
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

# --- release download ---------------------------------------------------------

assert_contains "agent_downloads_new_asset_name" "$out" "asset: agent-linux-"
assert_contains "asset template" "$src" "agent-%s-%s"
assert_not_contains "no old asset name" "$src" "winterflow-agent-%s"
assert_contains "downloads are checksum-verified" "$src" "sha256sum -c"
assert_contains "release download is HTTPS from the project repo" "$src" 'GITHUB_REPO="dmtrkzntsv/winterflow"'
assert_contains "enroll output is not captured" "$src" '--cert-dir "$CERT_DIR" --env-out "$envout"'
assert_contains "unit is not enabled before enrollment" "$src" "written, not enabled until enrollment succeeded"

# --- standalone asset name ----------------------------------------------------

assert_contains "standalone_uses_new_asset_name" "$(bash "$STANDALONE" --dry-run 2>&1; cat "$STANDALONE")" "winterflow-%s-%s"
assert_not_contains "standalone: no old asset name" "$(cat "$STANDALONE")" "winterflow-standalone-"

echo
if [ "$FAILURES" -eq 0 ]; then echo "ALL TESTS PASSED"; else echo "${FAILURES} TEST(S) FAILED"; exit 1; fi
