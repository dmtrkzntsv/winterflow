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

# --- Curl-pipe safety ---------------------------------------------------------

# The curl | bash path: the whole script must parse before anything runs
# (main-wrapper, so a truncated download cannot execute half a script), and
# piped runs must never prompt — sudo's use_pty relays the pipe, not the
# keyboard, so any tty read would hang with no way to type or Ctrl-C out.
src="$(cat "$INSTALL_SH")"
assert_contains "script body runs via a main wrapper" "$src" 'main "$@"'
assert_not_contains "piped runs never read /dev/tty (hangs under sudo use_pty)" "$src" "</dev/tty"
assert_contains "piped install announces non-interactive defaults" "$src" "using defaults, no questions"
assert_contains "piped --purge refuses to default to yes" "$src" "refusing to purge without a terminal to confirm on"

out_pipe="$(cat "$INSTALL_SH" | bash -s -- --dry-run 2>&1)" || fail "piped dry-run exits 0" "exit $?"
assert_contains "piped: env file rendered" "$out_pipe" "API_PORT=8080"
assert_contains "piped: unit rendered" "$out_pipe" "User=winterflow"

out_pipe_help="$(cat "$INSTALL_SH" | bash -s -- --help 2>&1)" || fail "piped --help exits 0" "exit $?"
assert_contains "piped --help points at the repo" "$out_pipe_help" "github.com"

# --- Release download ---------------------------------------------------------

# The download itself needs network + a published release, so assert the
# checkable contract: --version is documented, the asset naming matches
# .goreleaser.yaml, every supported arch is mapped, and downloads are
# checksum-verified.
assert_contains "--help documents --version" "$(bash "$INSTALL_SH" --help)" "--version TAG"
assert_contains "asset naming matches goreleaser" "$src" "winterflow-standalone-%s-%s"
assert_contains "amd64 mapped" "$src" "x86_64"
assert_contains "arm64 mapped" "$src" "aarch64"
assert_contains "32-bit arm mapped" "$src" "armv7l"
assert_contains "downloads are checksum-verified" "$src" "sha256sum -c"
assert_contains "release download is HTTPS from the project repo" "$src" 'GITHUB_REPO="dmtrkzntsv/winterflow"'
assert_not_contains "no stale CLI claim in the closing message" "$src" "winterflow <command>"

# --- Validation ---------------------------------------------------------------

bash "$INSTALL_SH" --dry-run --port abc >/dev/null 2>&1 && fail "non-numeric port rejected" "exit 0" || pass "non-numeric port rejected"
bash "$INSTALL_SH" --dry-run --port 70000 >/dev/null 2>&1 && fail "out-of-range port rejected" "exit 0" || pass "out-of-range port rejected"
bash "$INSTALL_SH" --dry-run --cpu-quota abc >/dev/null 2>&1 && fail "non-numeric cpu quota rejected" "exit 0" || pass "non-numeric cpu quota rejected"

# --- Docker prerequisites -----------------------------------------------------

# The install path itself needs root and a Docker-less host, so assert the
# contract that is checkable here: the offer is documented, the download is
# HTTPS-only, and the compose plugin is handled separately from the engine.
help_out="$(bash "$INSTALL_SH" --help)"
assert_contains "--help documents the docker install offer" "$help_out" "offers to install Docker and the compose plugin"
src="$(cat "$INSTALL_SH")"
assert_contains "docker installer is fetched over https" "$src" 'DOCKER_INSTALL_URL="https://get.docker.com"'
assert_contains "docker install is behind a confirm" "$src" "Download and run Docker's official install script?"
assert_contains "compose plugin install is behind its own confirm" "$src" "Install the docker-compose-plugin package?"
assert_not_contains "docker install script is never piped straight to a shell" "$src" "| sh -"

# --- Service user -------------------------------------------------------------

bash "$INSTALL_SH" --dry-run --user root >/dev/null 2>&1 \
    && fail "--user root rejected" "exit 0" || pass "--user root rejected"
bash "$INSTALL_SH" --dry-run --user 'bad name' >/dev/null 2>&1 \
    && fail "--user with invalid characters rejected" "exit 0" || pass "--user with invalid characters rejected"
bash "$INSTALL_SH" --dry-run --user '' >/dev/null 2>&1 \
    && fail "empty --user rejected" "exit 0" || pass "empty --user rejected"

out_user="$(bash "$INSTALL_SH" --dry-run --user winterflow-svc 2>&1)" || fail "--user dry-run exits 0" "exit $?"
assert_contains "unit: --user applied" "$out_user" "User=winterflow-svc"
assert_contains "unit: group defaults to the user name" "$out_user" "Group=winterflow-svc"

# An existing account keeps its real primary group (user-private groups are a
# convention, not a rule) so the data dir is chgrp-able.
me="$(id -un)"
if [ "$me" != "root" ]; then
    out_me="$(bash "$INSTALL_SH" --dry-run --user "$me" 2>&1)" || fail "--user <existing> dry-run exits 0" "exit $?"
    assert_contains "unit: existing user applied" "$out_me" "User=${me}"
    assert_contains "unit: existing user's primary group resolved" "$out_me" "Group=$(id -gn)"
fi

assert_contains "--help documents the service user prompt" "$(bash "$INSTALL_SH" --help)" "Must not be root."

# --- Dry run: generated env file ---------------------------------------------

out="$(bash "$INSTALL_SH" --dry-run --port 9090 --cpu-quota 250 --memory-max 1G 2>&1)" \
    || fail "dry-run exits 0" "exit $?"

assert_contains "env: api port applied" "$out" "API_PORT=9090"
assert_contains "env: sqlite database" "$out" "DATABASE_URL=sqlite:///var/lib/winterflow/winterflow.sqlite"
assert_contains "env: jwt secret placeholder (not generated in dry-run)" "$out" "JWT_SECRET=<generated-at-install-time>"
assert_contains "env: ingress documented as off by default" "$out" "#INGRESS_ENABLED=false"
# The installer must never write an active (uncommented) enable line.
if grep -qE '^INGRESS_ENABLED=true' <<<"$out"; then
    fail "env: ingress is never enabled by the installer" "found uncommented INGRESS_ENABLED=true"
else
    pass "env: ingress is never enabled by the installer"
fi

# --- Port prompt --------------------------------------------------------------

assert_contains "--help documents the port question" "$help_out" "asks which port"
assert_contains "interactive port question exists" "$src" "Which port should the WinterFlow web UI listen on?"
assert_contains "port question skipped with --port/--yes" "$src" 'if [ "$PORT_EXPLICIT" -eq 0 ] && [ "$ASSUME_YES" -eq 0 ]; then'

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

assert_contains "unit: starts via the serve subcommand" "$out" "ExecStart=/usr/local/bin/winterflow serve"
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
# Default memory cap = half of RAM, clamped to 512M..2G (sized to the box:
# Raspberry Pi-class boards get a real cap, big hosts don't over-reserve).
mem_kb="$(awk '/^MemTotal:/{print $2}' /proc/meminfo 2>/dev/null || echo 0)"
if [ "${mem_kb:-0}" -gt 0 ]; then
    half_mb=$(( mem_kb / 2048 ))
    [ "$half_mb" -ge 512 ]  || half_mb=512
    [ "$half_mb" -le 2048 ] || half_mb=2048
    assert_contains "default memory cap from RAM" "$out_default" "MemoryMax=${half_mb}M"
else
    assert_contains "default memory cap fallback" "$out_default" "MemoryMax=1G"
fi
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
