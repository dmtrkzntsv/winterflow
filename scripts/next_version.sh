#!/usr/bin/env bash
# Prints the next release tag (vMAJOR.MINOR.PATCH) for the release workflow.
#   next_version.sh [REQUESTED]
# With REQUESTED (vX.Y.Z or X.Y.Z): that tag, refused unless it is above the
# latest release. Without: the latest release's patch bumped, or v0.1.0 when
# there is no release yet. Only semver tags count; any other v* tag (the old
# calver ones) is ignored. Reads the tags of the git repository it runs in.
set -euo pipefail

semver='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
requested="${1:-}"
last="$(git tag -l 'v*' | grep -E "$semver" | sort -V | tail -n 1 || true)"

if [ -n "$requested" ]; then
    tag="v${requested#v}"
    if ! printf '%s\n' "$tag" | grep -qE "$semver"; then
        echo "next_version: version must be vX.Y.Z, got '${requested}'" >&2
        exit 1
    fi
    if [ -n "$last" ]; then
        highest="$(printf '%s\n%s\n' "$last" "$tag" | sort -V | tail -n 1)"
        if [ "$tag" = "$last" ] || [ "$highest" != "$tag" ]; then
            echo "next_version: ${tag} is not above the latest release ${last}" >&2
            exit 1
        fi
    fi
elif [ -n "$last" ]; then
    IFS=. read -r major minor patch <<< "${last#v}"
    tag="v${major}.${minor}.$((patch + 1))"
else
    tag="v0.1.0"
fi
printf '%s\n' "$tag"
