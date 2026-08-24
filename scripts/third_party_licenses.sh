#!/usr/bin/env bash
# Generates THIRD_PARTY_LICENSES.txt: the license text of every Go module
# compiled into the released binaries (cmd/standalone, cmd/agent) plus every
# production npm package bundled into the embedded web UI. The file is attached
# to each GitHub release by .goreleaser.yaml (release.extra_files) so the raw
# binaries on the release page ship next to their attribution notices, as the
# Apache-2.0/MIT/BSD dependencies require.
#
# Requires: go, pnpm, node, and an installed web/node_modules
# (pnpm --dir web install). Usage: scripts/third_party_licenses.sh [out-file]
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/.." && pwd)
cd "$repo_root"
out=${1:-THIRD_PARTY_LICENSES.txt}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# Pinned so releases are reproducible; `go run pkg@version` never touches go.mod.
go_licenses="go run github.com/google/go-licenses@v1.6.0"

echo "Collecting Go module licenses..." >&2
# `report` (unlike `save`, which hard-fails) still lists libraries whose
# license text it cannot classify, like modernc.org/mathutil's reworded
# BSD-3-Clause — and we copy the actual files ourselves anyway.
$go_licenses report ./cmd/standalone ./cmd/agent \
  --ignore winterflow 2>/dev/null >"$tmp/report.csv"
[ -s "$tmp/report.csv" ] || { echo "error: empty go-licenses report" >&2; exit 1; }

# A report row is a license-file scope, not necessarily a module: it can be a
# subpackage with its own LICENSE (klauspost/compress/internal/snapref) or a
# deep package covered by the module root's LICENSE (prometheus/client_model/go).
# Resolve the module by trimming path segments, then search from the scope's
# directory up to the module root for the license/notice files.
copy_go_licenses() {
  local lib=$1 mod=$1 sub="" dir
  while ! dir=$(go list -m -f '{{.Dir}}' "$mod" 2>/dev/null) || [ -z "$dir" ]; do
    [[ "$mod" == */* ]] || return 1
    sub="${mod##*/}${sub:+/$sub}"
    mod=${mod%/*}
  done
  local d="$dir${sub:+/$sub}"
  while :; do
    if find "$d" -maxdepth 1 -type f \
      \( -iname 'licen[sc]e*' -o -iname 'copying*' -o -iname 'notice*' \) |
      grep -q .; then
      mkdir -p "$tmp/go/$lib"
      find "$d" -maxdepth 1 -type f \
        \( -iname 'licen[sc]e*' -o -iname 'copying*' -o -iname 'notice*' \) \
        -exec cp {} "$tmp/go/$lib/" \;
      return 0
    fi
    [ "$d" = "$dir" ] && return 1
    d=${d%/*}
  done
}
while IFS=, read -r lib _rest; do
  copy_go_licenses "$lib" ||
    { echo "error: no license file found for $lib" >&2; exit 1; }
done <"$tmp/report.csv"

echo "Collecting npm package licenses..." >&2
# Without an installed node_modules, `pnpm licenses` still answers from the
# lockfile but with package paths that don't exist, so every license text
# would silently fall back to a stub line. Refuse to produce that.
[ -d web/node_modules ] ||
  { echo "error: web/node_modules missing — run: pnpm --dir web install" >&2; exit 1; }
pnpm --dir web licenses list --prod --json >"$tmp/npm.json"

{
  cat <<'EOF'
Third-party license notices for Winterflow
==========================================

Winterflow itself is distributed under the O'Saasy License (see LICENSE.md).
The released binaries statically link the Go modules listed below and, in the
standalone binary, additionally embed a web UI bundle built from the npm
packages listed after them. Each entry reproduces the license and copyright
notices under which that component is redistributed.
EOF

  printf '\n\n%s\n%s\n' "PART 1: GO MODULES" "=================="
  (cd "$tmp/go" && find . -type f | sort) | while IFS= read -r f; do
    f=${f#./}
    printf '\n%s\n' "--------------------------------------------------------------------------------"
    printf '%s (%s)\n\n' "$(dirname "$f")" "$(basename "$f")"
    cat "$tmp/go/$f"
  done

  printf '\n\n%s\n%s\n' "PART 2: NPM PACKAGES (embedded web UI)" "======================================"
  node - "$tmp/npm.json" <<'EOF'
const fs = require("fs"), path = require("path");
const groups = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
const pkgs = Object.values(groups).flat()
  .sort((a, b) => a.name.localeCompare(b.name));
const candidate = /^(licen[sc]e|copying|notice)(\.|$|-)/i;
for (const p of pkgs) {
  for (let i = 0; i < p.versions.length; i++) {
    const dir = p.paths[i] || p.paths[0];
    let text = null;
    try {
      const name = fs.readdirSync(dir).find(
        (n) => candidate.test(n) && fs.statSync(path.join(dir, n)).isFile());
      if (name) text = fs.readFileSync(path.join(dir, name), "utf8");
    } catch {}
    console.log("\n" + "-".repeat(80));
    console.log(`${p.name}@${p.versions[i]} (${p.license})\n`);
    console.log(text ? text.trim() :
      `No license file is shipped in this package; it is published under the ` +
      `${p.license} license. See ${p.homepage || "the npm registry"}.`);
  }
}
EOF
} >"$out"

echo "Wrote $out ($(wc -c <"$out") bytes)" >&2
