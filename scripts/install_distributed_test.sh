#!/usr/bin/env bash
# Non-root, no-side-effect checks for the distributed installer.
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/install-distributed.sh"
bash -n "$script"
grep -qF -- '--yes' <(bash "$script" --help)
out="$(bash "$script" --dry-run --hub-host hub.example.com --web-url https://flow.example.com)"
grep -qF 'DATABASE_URL=postgres://' <<<"$out"
! grep -qF 'DATABASE_URL=sqlite://' <<<"$out"
grep -qF 'REDIS_ADDR=127.0.0.1:6379' <<<"$out"
grep -qF 'HUB_HOST=hub.example.com' <<<"$out"
grep -qF 'WEB_URL=https://flow.example.com' <<<"$out"
grep -qF 'ExecStart=/usr/local/bin/winterflow-api' <<<"$out"
grep -qF 'ExecStart=/usr/local/bin/winterflow-hub' <<<"$out"
grep -qF 'Requires=postgresql.service redis-server.service' <<<"$out"
grep -qF 'EnvironmentFile=/etc/winterflow-distributed/api.env' <<<"$out"
grep -qF 'EnvironmentFile=/etc/winterflow-distributed/hub.env' <<<"$out"
grep -qF 'User=winterflow-api' <<<"$out"
grep -qF 'User=winterflow-hub' <<<"$out"
hub_env="${out#*### /etc/winterflow-distributed/hub.env}"
hub_env="${hub_env%%### /etc/systemd/system/winterflow-api.service*}"
! grep -qF 'JWT_SECRET=' <<<"$hub_env"
! grep -qF 'DATABASE_URL=' <<<"$hub_env"

if bash "$script" --dry-run --port 70000 >/dev/null 2>&1; then
    echo 'invalid port was accepted' >&2
    exit 1
fi
if bash "$script" --dry-run --hub-host 'bad/name' >/dev/null 2>&1; then
    echo 'invalid Hub host was accepted' >&2
    exit 1
fi
if bash "$script" --dry-run --port 50051 >/dev/null 2>&1; then
    echo 'colliding API and Hub ports were accepted' >&2
    exit 1
fi

# Package checks are tested without root or changing the host. Sourcing the
# script exposes the dependency functions without running main.
bash -c '
    source "$1"
    postgres_installed() { :; }
    redis_installed() { :; }
    apt-get() { exit 99; }
    ensure_dependencies
' _ "$script" >/dev/null

trace="$(mktemp)"
trap 'rm -f "$trace"' EXIT
bash -c '
    source "$1"
    ASSUME_YES=1
    trace_file="$2"
    installed=0
    postgres_installed() { [ "$installed" -eq 1 ]; }
    redis_installed() { :; }
    apt-get() {
        printf "%s\n" "$*" >> "$trace_file"
        [ "$1" != install ] || installed=1
    }
    ensure_dependencies
' _ "$script" "$trace" >/dev/null
grep -qF 'install -y postgresql' "$trace"
! grep -qF 'redis-server' "$trace"

: > "$trace"
bash -c '
    source "$1"
    ASSUME_YES=1
    trace_file="$2"
    installed=0
    postgres_installed() { :; }
    redis_installed() { [ "$installed" -eq 1 ]; }
    apt-get() {
        printf "%s\n" "$*" >> "$trace_file"
        [ "$1" != install ] || installed=1
    }
    ensure_dependencies
' _ "$script" "$trace" >/dev/null
grep -qF 'install -y redis-server' "$trace"
! grep -qF 'postgresql' "$trace"

if bash -c '
    source "$1"
    postgres_installed() { return 1; }
    redis_installed() { :; }
    ensure_dependencies
' _ "$script" </dev/null >"$trace" 2>&1; then
    echo 'missing PostgreSQL was installed without confirmation' >&2
    exit 1
fi
grep -qF 'run interactively or pass --yes' "$trace"

if bash -c '
    source "$1"
    postgres_installed() { :; }
    redis_installed() { return 1; }
    ensure_dependencies
' _ "$script" </dev/null >"$trace" 2>&1; then
    echo 'missing Redis was installed without confirmation' >&2
    exit 1
fi
grep -qF 'Redis is missing; run interactively or pass --yes' "$trace"

echo 'distributed installer tests passed'
