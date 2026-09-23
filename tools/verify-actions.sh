#!/usr/bin/env bash
# Every third-party action must be pinned to a full commit SHA (immutable; immune to deleted/renamed tags and tag-hijacking).
set -euo pipefail
cd "$(dirname "$0")/.."
bad=$(grep -rhE '^\s*-?\s*uses:' .github/workflows | grep -vE 'uses: [A-Za-z0-9_./-]+@[0-9a-f]{40}( |$)' || true)
if [ -n "$bad" ]; then echo "Unpinned actions:"; echo "$bad"; exit 1; fi
echo "All actions are SHA-pinned."

# dependabot.yml must only use keys Dependabot accepts (no anchors / x- keys) and every ecosystem must set a commitlint-safe prefix.
python3 - <<'PY'
import yaml, sys
d = yaml.safe_load(open(".github/dependabot.yml"))
if set(d) != {"version", "updates"}:
    sys.exit(f"dependabot.yml: unsupported top-level keys {set(d) - {'version', 'updates'}}")
missing = [u["package-ecosystem"] for u in d["updates"] if not u.get("commit-message", {}).get("prefix", "").startswith("chore")]
if missing:
    sys.exit(f"dependabot.yml: ecosystems without a conventional-commit prefix: {missing}")
print("dependabot.yml is valid and commitlint-safe.")
PY
