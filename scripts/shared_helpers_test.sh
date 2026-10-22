#!/usr/bin/env bash
#
# Checks that the helper blocks shared between the installers are identical.
# Each installer is self-contained (curl-pipeable), so shared code is copied;
# a copy is marked "# --- shared: <name> ---" ... "# --- end shared ---" and
# this test fails when two copies of one block differ.
# Run: bash scripts/shared_helpers_test.sh
set -uo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
FAILURES=0

pass() { printf 'ok   %s\n' "$1"; }
fail() { printf 'FAIL %s\n     %s\n' "$1" "$2"; FAILURES=$((FAILURES + 1)); }

# block_text FILE NAME -> the marked block, markers included.
block_text() {
    awk -v name="$2" '
        $0 == "# --- shared: " name " ---" { on = 1 }
        on { print }
        on && $0 == "# --- end shared ---" { on = 0 }
    ' "$1"
}

scripts=("$DIR"/install*.sh)
names="$(grep -h '^# --- shared: ' "${scripts[@]}" | sed 's/^# --- shared: \(.*\) ---$/\1/' | sort -u)"

[ -n "$names" ] && pass "shared blocks found" || fail "shared blocks found" "no markers in ${scripts[*]}"

for name in $names; do
    ref=""; ref_file=""; ok=1; count=0
    for f in "${scripts[@]}"; do
        text="$(block_text "$f" "$name")"
        [ -n "$text" ] || continue
        count=$((count + 1))
        if [ -z "$ref_file" ]; then
            ref="$text"; ref_file="$f"
        elif [ "$text" != "$ref" ]; then
            ok=0
            fail "shared_helpers_identical: ${name}" "$(basename "$f") differs from $(basename "$ref_file")"
        fi
    done
    if [ "$ok" -eq 1 ]; then
        if [ "$count" -ge 2 ]; then pass "shared_helpers_identical: ${name} (${count} copies)"
        else fail "shared_helpers_identical: ${name}" "only ${count} copy; a shared block must appear in at least two installers"; fi
    fi
done

# Every start marker must be closed (an unclosed block would swallow the file).
for f in "${scripts[@]}"; do
    s="$(grep -c '^# --- shared: ' "$f")"; e="$(grep -c '^# --- end shared ---$' "$f")"
    [ "$s" -eq "$e" ] && pass "markers balanced in $(basename "$f")" \
        || fail "markers balanced in $(basename "$f")" "${s} starts, ${e} ends"
done

echo
if [ "$FAILURES" -eq 0 ]; then echo "ALL TESTS PASSED"; else echo "${FAILURES} TEST(S) FAILED"; exit 1; fi
