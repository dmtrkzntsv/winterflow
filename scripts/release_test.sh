#!/usr/bin/env bash
# Tests for the release Makefile targets (release-binaries names, cdn-upload).
# No R2 and no aws CLI needed: a stub `aws` on PATH records its arguments.
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
mkdir -p "$TMP/bin" "$TMP/out"
cat > "$TMP/bin/aws" <<'S'
#!/usr/bin/env bash
echo "aws $* | key=${AWS_ACCESS_KEY_ID:-} secret=${AWS_SECRET_ACCESS_KEY:-} checksum=${AWS_REQUEST_CHECKSUM_CALCULATION:-}" >> "$AWS_LOG"
S
chmod +x "$TMP/bin/aws"
for f in winterflow-api-linux-amd64 winterflow-hub-linux-arm SHA256SUMS; do echo x > "$TMP/out/$f"; done
export AWS_LOG="$TMP/aws.log"

upload() { # args... ; runs make cdn-upload with the stub aws, endpoint set
    : > "$AWS_LOG"
    PATH="$TMP/bin:$PATH" WINTERFLOW_R2_ENDPOINT="https://acct.r2.example" \
        WINTERFLOW_R2_ACCESS_KEY_ID=kid WINTERFLOW_R2_SECRET_ACCESS_KEY=sec \
        make --no-print-directory cdn-upload CDN_SRC="$TMP/out" "$@" 2>&1
}

# cdn_upload_requires_channel
out="$(PATH="$TMP/bin:$PATH" WINTERFLOW_R2_ENDPOINT=https://e make --no-print-directory cdn-upload CDN_SRC="$TMP/out" 2>&1)" && fail "cdn_upload_requires_channel" "succeeded" || contains "cdn_upload_requires_channel" "$out" "set CHANNEL"
[ -s "$AWS_LOG" ] && fail "cdn_upload_requires_channel: no upload" "aws was called"

# cdn_upload_requires_endpoint
: > "$AWS_LOG"
out="$(env -u WINTERFLOW_R2_ENDPOINT PATH="$TMP/bin:$PATH" make --no-print-directory cdn-upload CHANNEL=latest CDN_SRC="$TMP/out" 2>&1)" && fail "cdn_upload_requires_endpoint" "succeeded" || contains "cdn_upload_requires_endpoint" "$out" "WINTERFLOW_R2_ENDPOINT is required"
[ -s "$AWS_LOG" ] && fail "cdn_upload_requires_endpoint: no upload" "aws was called"

# cdn_upload_dry_run_lists_targets
out="$(upload CHANNEL=v261020.1)" || fail "cdn_upload version run" "$out"
log="$(cat "$AWS_LOG")"
contains "cdn_upload targets api object" "$log" "s3://econumo/winterflow/v261020.1/winterflow-api-linux-amd64"
contains "cdn_upload targets hub arm object" "$log" "s3://econumo/winterflow/v261020.1/winterflow-hub-linux-arm"
contains "cdn_upload uploads SHA256SUMS" "$log" "s3://econumo/winterflow/v261020.1/SHA256SUMS"
contains "cdn_upload endpoint" "$log" "--endpoint-url https://acct.r2.example"
contains "cdn_upload version cache is immutable" "$log" "immutable"
contains "cdn_upload maps credentials" "$log" "key=kid secret=sec"
contains "cdn_upload disables checksum trailers" "$log" "checksum=when_required"
out="$(upload CHANNEL=latest)" || fail "cdn_upload latest run" "$out"
log="$(cat "$AWS_LOG")"
contains "cdn_upload latest path" "$log" "s3://econumo/winterflow/latest/winterflow-api-linux-amd64"
contains "cdn_upload latest is no-cache" "$log" "no-cache"
lacks "cdn_upload latest is not immutable" "$log" "immutable"
out="$(upload CHANNEL=latest R2_BUCKET=other R2_PROJECT=p)" || fail "cdn_upload overrides" "$out"
contains "cdn_upload bucket/project overridable" "$(cat "$AWS_LOG")" "s3://other/p/latest/SHA256SUMS"

# release_binaries_names
plan="$(make -n release-binaries VERSION=1.2.3 2>&1)"
for c in standalone agent api hub; do
    for a in amd64 arm64 arm; do
        contains "release_binaries_names $c-$a" "$plan" "-o release-out/winterflow-$c-linux-$a ./cmd/$c"
    done
done
contains "release_binaries_names SHA256SUMS" "$plan" "> SHA256SUMS"
contains "release_binaries_names ldflags" "$plan" "-X winterflow/pkg/version.version=1.2.3"
contains "release_binaries_names armv6" "$plan" "GOARM=6"
contains "release_binaries_names distributed web" "$plan" "VITE_APP_MODE=distributed pnpm"
# The standalone build must embed the standalone SPA: the distributed web build
# comes after every standalone binary and before every api binary.
pos() { printf '%s\n' "$plan" | grep -n -m1 -- "$1" | cut -d: -f1; }
[ "$(pos 'winterflow-standalone-linux-arm ')" -lt "$(pos 'VITE_APP_MODE=distributed')" ] && [ "$(pos 'VITE_APP_MODE=distributed')" -lt "$(pos 'winterflow-api-linux-amd64 ')" ] \
    && ok "release_binaries_names web build order" || fail "release_binaries_names web build order"

[ "$fails" -eq 0 ] && { echo "all passed"; exit 0; }
echo "$fails failed"; exit 1
