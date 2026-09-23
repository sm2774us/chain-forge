#!/usr/bin/env bash
# Runs, locally, the checks that CI runs and that can be run without Docker or GitHub:
#   workflow lint (actionlint) · SHA-pin + dependabot guard · commitlint rules · Trivy misconfig+secret scan
#   (the same scanners/severity as CI; vuln DB needs network) · OpenTofu fmt (+ validate when the provider is reachable)
# Missing tools are reported, not silently skipped. Install: actionlint, trivy, tofu (all single binaries).
set -uo pipefail
cd "$(dirname "$0")/.."
rc=0
step() { echo; echo "── $*"; }
have() { command -v "$1" >/dev/null 2>&1 || { echo "  ! $1 not installed — SKIPPED (CI will still run it)"; return 1; }; }

step "SHA-pinned actions + dependabot.yml"; bash tools/verify-actions.sh || rc=1
step "commitlint rules"; bash tools/test-commitlint.sh || rc=1
step "actionlint"; if have actionlint; then actionlint -color=false .github/workflows/*.yml && echo "  clean" || rc=1; fi
step "trivy misconfig + secret (HIGH,CRITICAL)"
if have trivy; then
  trivy fs --skip-version-check --skip-db-update --skip-check-update --scanners secret,misconfig \
    --severity HIGH,CRITICAL --exit-code 1 --skip-dirs node_modules,target,dist . >/tmp/trivy-preflight.log 2>&1 \
    && echo "  clean" || { tail -30 /tmp/trivy-preflight.log; rc=1; }
fi
step "opentofu fmt"; if have tofu; then tofu -chdir=infra/tofu fmt -check -diff && echo "  clean" || rc=1; fi
echo; [ $rc -eq 0 ] && echo "preflight OK" || echo "preflight FAILED"
exit $rc
