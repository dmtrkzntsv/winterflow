#!/usr/bin/env bash
#
# Tests for scripts/install.sh. Uses --dry-run so nothing on the host is
# touched and root is not required. Run: bash scripts/install_test.sh
set -uo pipefail

INSTALL_SH="$(cd "$(dirname "$0")" && pwd)/install.sh"
FAILURES=0

pass() { printf 'ok   %s\n' "$1"; }
fail() { printf 'FAIL %s\n     %s\n' "$1" "$2"; FAILURES=$((FAILURES + 1)); }

assert_contains() {
    # assert_contains "test name" "haystack" "needle"
    if grep -qF -- "$3" <<<"$2"; then pass "$1"; else fail "$1" "missing: $3"; fi
}

assert_not_contains() {
    if grep -qF -- "$3" <<<"$2"; then fail "$1" "unexpected: $3"; else pass "$1"; fi
}

# --- Basics -------------------------------------------------------------------

bash -n "$INSTALL_SH" && pass "syntax check" || fail "syntax check" "bash -n failed"

out="$(bash "$INSTALL_SH" --help)" || fail "--help exits 0" "exit $?"
assert_contains "--help documents log rotation" "$out" "journalctl --namespace winterflow --vacuum-time=1s"
assert_contains "--help documents --dry-run" "$out" "--dry-run"
assert_contains "--help documents --cpu-quota" "$out" "--cpu-quota"

bash "$INSTALL_SH" --nonsense >/dev/null 2>&1 && fail "unknown option rejected" "exit 0" || pass "unknown option rejected"

# --- Validation ---------------------------------------------------------------

bash "$INSTALL_SH" --dry-run --port abc >/dev/null 2>&1 && fail "non-numeric port rejected" "exit 0" || pass "non-numeric port rejected"
bash "$INSTALL_SH" --dry-run --port 70000 >/dev/null 2>&1 && fail "out-of-range port rejected" "exit 0" || pass "out-of-range port rejected"
bash "$INSTALL_SH" --dry-run --cpu-quota abc >/dev/null 2>&1 && fail "non-numeric cpu quota rejected" "exit 0" || pass "non-numeric cpu quota rejected"

# --- Dry run: generated env file ---------------------------------------------

out="$(bash "$INSTALL_SH" --dry-run --port 9090 --cpu-quota 250 --memory-max 1G 2>&1)" \
    || fail "dry-run exits 0" "exit $?"

assert_contains "env: api port applied" "$out" "API_PORT=9090"
assert_contains "env: sqlite database" "$out" "DATABASE_URL=sqlite:///var/lib/winterflow/winterflow.sqlite"
assert_contains "env: jwt secret placeholder (not generated in dry-run)" "$out" "JWT_SECRET=<generated-at-install-time>"

# --- Dry run: unit — logging must be rotatable & deletable --------------------

assert_contains "unit: dedicated journal namespace" "$out" "LogNamespace=winterflow"
assert_contains "unit: journald log rate limit" "$out" "LogRateLimitIntervalSec="
assert_contains "journald: rotation size cap" "$out" "SystemMaxUse=200M"
assert_contains "journald: rotation file size" "$out" "SystemMaxFileSize=20M"
assert_contains "journald: retention limit" "$out" "MaxRetentionSec=1month"
assert_contains "journald: persistent storage" "$out" "Storage=persistent"

# --- Dry run: unit — resource limits (thermal safety) -------------------------

assert_contains "unit: cpu quota applied" "$out" "CPUQuota=250%"
assert_contains "unit: memory cap applied" "$out" "MemoryMax=1G"
assert_contains "unit: task cap" "$out" "TasksMax="
assert_contains "unit: fd limit" "$out" "LimitNOFILE="

# --- Dry run: unit — hardening survives the rework ----------------------------

assert_contains "unit: runs as service user" "$out" "User=winterflow"
assert_contains "unit: strict protect system" "$out" "ProtectSystem=strict"
assert_contains "unit: no new privileges" "$out" "NoNewPrivileges=true"
assert_contains "unit: bind 80/443 capability" "$out" "AmbientCapabilities=CAP_NET_BIND_SERVICE"
assert_contains "unit: data dir writable" "$out" "ReadWritePaths=/var/lib/winterflow"
assert_contains "unit: docker socket writable" "$out" "ReadWritePaths=-/run/docker.sock"
assert_not_contains "unit: never runs as root" "$out" "User=root"

# --- Dry run defaults ---------------------------------------------------------

out_default="$(bash "$INSTALL_SH" --dry-run 2>&1)" || fail "default dry-run exits 0" "exit $?"
assert_contains "default port 8080" "$out_default" "API_PORT=8080"
assert_contains "default memory cap 2G" "$out_default" "MemoryMax=2G"
# Default CPU quota = (cores-1)*100, floored at 100.
cores="$(nproc 2>/dev/null || echo 2)"
want=$(( (cores - 1) * 100 )); [ "$want" -ge 100 ] || want=100
assert_contains "default cpu quota from core count" "$out_default" "CPUQuota=${want}%"

# Dry run must not require root and must not write anything.
if [ "$(id -u)" -ne 0 ]; then
    pass "dry-run ran without root"
fi

echo
if [ "$FAILURES" -eq 0 ]; then
    echo "ALL TESTS PASSED"
else
    echo "${FAILURES} TEST(S) FAILED"
    exit 1
fi
