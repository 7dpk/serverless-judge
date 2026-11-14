#!/usr/bin/env bash
# Build one image with Cloud Build, unless that exact tag already exists.
# Tags are content hashes, so "exists" means "nothing changed".
#
#   PROJECT=<id> cloudbuild/build.sh <image> <context dir> <dockerfile> [machine type or ""] [KEY=VALUE ...]
#
# Used by deploy.sh and by terraform/images.tf. Paths are relative to the
# repository root.
set -euo pipefail
cd "$(dirname "$0")/.."

[ -n "${PROJECT:-}" ] || { echo "PROJECT is not set" >&2; exit 1; }
[ $# -ge 3 ] || { sed -n '2,8p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 1; }
image="$1" context="$2" dockerfile="$3" machine="${4:-}"
shift 3
[ $# -gt 0 ] && shift

G=(gcloud --project "$PROJECT" --quiet)
name="${image##*/}"

if "${G[@]}" artifacts docker images describe "$image" >/dev/null 2>&1; then
  echo "up to date: $name"
  exit 0
fi

args=""
for a in "$@"; do args="$args --build-arg $a"; done
extra=()
[ -n "$machine" ] && extra+=(--machine-type "$machine")

echo "building $name"
"${G[@]}" builds submit "$context" --config cloudbuild/image.yaml ${extra[@]+"${extra[@]}"} \
  --substitutions "^|^_IMAGE=$image|_CACHE=${image%%:*}:cache|_DOCKERFILE=$dockerfile|_BUILD_ARGS=$args" \
  --suppress-logs >/dev/null || {
    echo "build failed: $name. Logs: gcloud builds list --limit 3, then gcloud builds log <id>" >&2
    exit 1
  }
echo "built $name"
