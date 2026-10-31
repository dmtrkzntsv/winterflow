#!/usr/bin/env bash
# Tests for the release Makefile targets (release-binaries names, cdn-upload).
# No R2 and no ship CLI needed: a stub `ship` on PATH records its arguments.
# Run: bash scripts/release_test.sh
set -u
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 1

fails=0
ok()   { echo "ok   - $1"; }
fail() { echo "FAIL - $1"; [ -n "${2:-}" ] && echo "       $2"; fails=$((fails + 1)); }
contains() { # name haystack needle
    case "$2" in *"$3"*) ok "$1" ;; *) fail "$1" "missing: $3"$'\n'"       in: $2" ;; esac
}
lacks() {
    case "$2" in *"$3"*) fail "$1" "unexpected: $3" ;; *) ok "$1" ;; esac
}

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"
cat > "$TMP/bin/ship" <<'S'
#!/usr/bin/env bash
echo "ship $*" >> "$SHIP_LOG"
S
chmod +x "$TMP/bin/ship"
export SHIP_LOG="$TMP/ship.log"

upload() { # args... ; runs make cdn-upload with the stub ship
    : > "$SHIP_LOG"
    PATH="$TMP/bin:$PATH" make --no-print-directory cdn-upload "$@" 2>&1
}

# cdn_upload_publishes_with_ship
out="$(upload VERSION=261020.1)" || fail "cdn_upload version run" "$out"
log="$(cat "$SHIP_LOG")"
contains "cdn_upload runs ship publish" "$log" "ship publish winterflow --path release-out --version v261020.1 --channels latest"
out="$(upload VERSION=261020.1 CHANNELS=dev)" || fail "cdn_upload channels run" "$out"
contains "cdn_upload channels overridable" "$(cat "$SHIP_LOG")" "--channels dev"

# release_binaries_names: ship's folder layout (asset winterflow for the
# standalone, winterflow-<component> for the rest), no SHA256SUMS of our own.
plan="$(make -n release-binaries VERSION=1.2.3 2>&1)"
for a in amd64 arm64 arm; do
    contains "release_binaries_names standalone-$a" "$plan" "-o release-out/winterflow-linux-$a ./cmd/standalone"
    for c in agent api hub; do
        contains "release_binaries_names $c-$a" "$plan" "-o release-out/winterflow-$c-linux-$a ./cmd/$c"
    done
done
lacks "release_binaries_names no SHA256SUMS" "$plan" "SHA256SUMS"
contains "release_binaries_names units and env examples" "$plan" "cp deploy/systemd/*.service deploy/systemd/*.env.example release-out/"
contains "release_binaries_names ldflags" "$plan" "-X winterflow/pkg/version.version=1.2.3"
contains "release_binaries_names armv6" "$plan" "GOARM=6"
contains "release_binaries_names distributed web" "$plan" "VITE_APP_MODE=distributed pnpm"
# The standalone build must embed the standalone SPA: the distributed web build
# comes after every standalone binary and before every api binary.
pos() { printf '%s\n' "$plan" | grep -n -m1 -- "$1" | cut -d: -f1; }
[ "$(pos 'winterflow-linux-arm ')" -lt "$(pos 'VITE_APP_MODE=distributed')" ] && [ "$(pos 'VITE_APP_MODE=distributed')" -lt "$(pos 'winterflow-api-linux-amd64 ')" ] \
    && ok "release_binaries_names web build order" || fail "release_binaries_names web build order"

# Every asset ships a unit template and an env example.
for a in winterflow winterflow-agent winterflow-api winterflow-hub; do
    [ -f "deploy/systemd/$a.service" ] && ok "unit template $a" || fail "unit template $a" "missing deploy/systemd/$a.service"
    [ -f "deploy/systemd/$a.env.example" ] && ok "env example $a" || fail "env example $a" "missing deploy/systemd/$a.env.example"
done

[ "$fails" -eq 0 ] && { echo "all passed"; exit 0; }
echo "$fails failed"; exit 1
