#!/usr/bin/env bash
# Tests for the ship Makefile targets (ship-dist names, ship-publish).
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

publish() { # args... ; runs make ship-publish with the stub ship
    : > "$SHIP_LOG"
    PATH="$TMP/bin:$PATH" make --no-print-directory ship-publish "$@" 2>&1
}

# ship_publish: --version only for a real version, --channels only when set,
# an error (and no ship call) when neither is given.
out="$(publish VERSION=v261020.1 CHANNELS=latest)" || fail "ship_publish version+channels run" "$out"
contains "ship_publish version+channels" "$(cat "$SHIP_LOG")" "ship publish winterflow --path dist/ship --version v261020.1 --channels latest"
out="$(publish CHANNELS=dev)" || fail "ship_publish channels-only run" "$out"
log="$(cat "$SHIP_LOG")"
contains "ship_publish channels only" "$log" "ship publish winterflow --path dist/ship --channels dev"
lacks "ship_publish channels only: no version" "$log" "--version"
out="$(publish VERSION=dev CHANNELS=dev)" || fail "ship_publish VERSION=dev run" "$out"
lacks "ship_publish VERSION=dev: no version" "$(cat "$SHIP_LOG")" "--version"
out="$(publish VERSION=v261020.1)" || fail "ship_publish version-only run" "$out"
log="$(cat "$SHIP_LOG")"
contains "ship_publish version only" "$log" "ship publish winterflow --path dist/ship --version v261020.1"
lacks "ship_publish version only: no channels" "$log" "--channels"
out="$(publish)" && fail "ship_publish requires version or channels" "succeeded" || contains "ship_publish requires version or channels" "$out" "set VERSION"
[ -s "$SHIP_LOG" ] && fail "ship_publish nothing: ship not called" "$(cat "$SHIP_LOG")"
out="$(publish VERSION=dev)" && fail "ship_publish VERSION=dev alone is an error" "succeeded" || ok "ship_publish VERSION=dev alone is an error"

# ship_dist_names: ship's folder layout (asset winterflow for the standalone,
# winterflow-<component> for the rest), no SHA256SUMS of our own; the version
# is stamped without the leading v. Only dist/ship is removed, never dist/
# (goreleaser's).
plan="$(make -n ship-dist VERSION=v1.2.3 2>&1)"
for a in amd64 arm64 arm; do
    contains "ship_dist_names standalone-$a" "$plan" "-o dist/ship/winterflow-linux-$a ./cmd/standalone"
    for c in agent api hub; do
        contains "ship_dist_names $c-$a" "$plan" "-o dist/ship/winterflow-$c-linux-$a ./cmd/$c"
    done
done
lacks "ship_dist_names no SHA256SUMS" "$plan" "SHA256SUMS"
contains "ship_dist_names cleans only dist/ship" "$plan" "rm -rf dist/ship "
lacks "ship_dist_names never cleans dist/" "$plan" "rm -rf dist "
contains "ship_dist_names units and env examples" "$plan" "cp deploy/ship/*.service deploy/ship/*.env.example dist/ship/"
contains "ship_dist_names ldflags" "$plan" "-X winterflow/pkg/version.version=1.2.3\""
contains "ship_dist_names armv6" "$plan" "GOARM=6"
contains "ship_dist_names distributed web" "$plan" "VITE_APP_MODE=distributed pnpm"
# The standalone build must embed the standalone SPA: the distributed web build
# comes after every standalone binary and before every api binary.
pos() { printf '%s\n' "$plan" | grep -n -m1 -- "$1" | cut -d: -f1; }
[ "$(pos 'winterflow-linux-arm ')" -lt "$(pos 'VITE_APP_MODE=distributed')" ] && [ "$(pos 'VITE_APP_MODE=distributed')" -lt "$(pos 'winterflow-api-linux-amd64 ')" ] \
    && ok "ship_dist_names web build order" || fail "ship_dist_names web build order"

# Every asset ships a unit template and an env example.
for a in winterflow winterflow-agent winterflow-api winterflow-hub; do
    [ -f "deploy/ship/$a.service" ] && ok "unit template $a" || fail "unit template $a" "missing deploy/ship/$a.service"
    [ -f "deploy/ship/$a.env.example" ] && ok "env example $a" || fail "env example $a" "missing deploy/ship/$a.env.example"
done

[ "$fails" -eq 0 ] && { echo "all passed"; exit 0; }
echo "$fails failed"; exit 1
