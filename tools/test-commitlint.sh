#!/usr/bin/env bash
# Regression test for commitlint.config.js: bots pass, human non-conventional messages fail.
set -uo pipefail
cd "$(dirname "$0")/.."
fail=0
expect() { # expect pass|fail <message>
  if printf '%s' "$2" | npx --no -- commitlint >/dev/null 2>&1; then got=pass; else got=fail; fi
  [ "$got" = "$1" ] || { echo "✖ expected $1 for: ${2%%$'\n'*}"; fail=1; }
}
expect pass $'Bump nginxinc/nginx-unprivileged in /docker\n\nBumps nginxinc/nginx-unprivileged from 1.27-alpine to 1.29-alpine.\n\n---\nupdated-dependencies:\n- dependency-name: x\n...\nSigned-off-by: dependabot[bot] <support@github.com>'
expect pass 'chore(deps): bump nginx from 1.27 to 1.29'
expect pass 'chore(main): release 0.2.0'
expect pass 'feat(engine): add revm simulation'
expect fail 'fixed stuff'
expect fail 'Bump me up'
[ $fail -eq 0 ] && echo "commitlint rules OK"
exit $fail
