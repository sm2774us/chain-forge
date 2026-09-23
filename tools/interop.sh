#!/usr/bin/env bash
# Real Go client ↔ real Rust engine over a Unix socket (needs the release engine binary).
set -euo pipefail
cd "$(dirname "$0")/.."
bin="${ENGINE_BIN:-$PWD/target/release/engine}"
cd services/gateway
ENGINE_BIN="$bin" go test -count=1 -tags interop -run TestGoClientAgainstRustEngine -v ./internal/engineclient
