#!/usr/bin/env bash
# Every third-party action must be pinned to a full commit SHA (immutable; immune to deleted/renamed tags and tag-hijacking).
set -euo pipefail
cd "$(dirname "$0")/.."
bad=$(grep -rhE '^\s*-?\s*uses:' .github/workflows | grep -vE 'uses: [A-Za-z0-9_./-]+@[0-9a-f]{40}( |$)' || true)
if [ -n "$bad" ]; then echo "Unpinned actions:"; echo "$bad"; exit 1; fi
echo "All actions are SHA-pinned."

# Dockerfiles built concurrently by `docker compose` must not share unlocked cache mounts (crate-unpack race: ".cargo-ok: File exists").
if grep -nE -- '--mount=type=cache' docker/*.Dockerfile | grep -v 'sharing=locked'; then
  echo "Dockerfile cache mounts must use sharing=locked (and unique ids for target dirs)"; exit 1
fi
echo "Dockerfile cache mounts are concurrency-safe."
