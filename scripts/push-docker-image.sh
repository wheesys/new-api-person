#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"

cd "${repo_root}"

image="${IMAGE:-docker.io/walllee/new-api-person}"
platforms="${PLATFORMS:-linux/amd64,linux/arm64}"
timezone="${TIMEZONE:-Asia/Shanghai}"
source_url="${SOURCE_URL:-https://github.com/wheesys/new-api-person}"
builder_name="${BUILDER_NAME:-new-api-builder}"
dry_run="${DRY_RUN:-0}"
allow_dirty="${ALLOW_DIRTY:-0}"

if ! command -v git >/dev/null 2>&1; then
  echo "git is required." >&2
  exit 1
fi

if [[ "${allow_dirty}" != "1" ]] && [[ -n "$(git status --porcelain)" ]]; then
  echo "Refusing to publish from a dirty worktree." >&2
  echo "Commit or stash local changes first, or set ALLOW_DIRTY=1 to override." >&2
  exit 1
fi

revision="$(git rev-parse HEAD)"
short_revision="$(git rev-parse --short HEAD)"
version=""

if [[ -f VERSION ]]; then
  version="$(awk 'NF {print $1; exit}' VERSION)"
fi

tags=(
  -t "${image}:latest"
  -t "${image}:${short_revision}"
)

if [[ -n "${version}" ]]; then
  tags+=(-t "${image}:${version}")
fi

command=(
  docker buildx build
  --platform "${platforms}"
  --build-arg "TZ=${timezone}"
  --label "org.opencontainers.image.title=new-api-person"
  --label "org.opencontainers.image.source=${source_url}"
  --label "org.opencontainers.image.revision=${revision}"
  --label "org.opencontainers.image.licenses=AGPL-3.0"
  "${tags[@]}"
  --push
  .
)

printf 'Publishing image %s\n' "${image}"
printf 'Platforms: %s\n' "${platforms}"
printf 'Timezone: %s\n' "${timezone}"
printf 'Revision: %s\n' "${revision}"
if [[ -n "${version}" ]]; then
  printf 'Version tag: %s\n' "${version}"
else
  printf 'Version tag: skipped because VERSION is empty\n'
fi

printf 'Command:'
printf ' %q' "${command[@]}"
printf '\n'

if [[ "${dry_run}" == "1" ]]; then
  exit 0
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required." >&2
  exit 1
fi

docker buildx version >/dev/null

if docker buildx inspect "${builder_name}" >/dev/null 2>&1; then
  docker buildx use "${builder_name}" >/dev/null
else
  docker buildx create --name "${builder_name}" --use >/dev/null
fi

"${command[@]}"
