#!/usr/bin/env bash
# Fails unless every statement in services/gateway/internal/** is covered.
set -euo pipefail
cd "$(dirname "$0")/../services/gateway"
go test -race -covermode=atomic -coverprofile=/tmp/go.cover ./internal/...
missing=$(go tool cover -func=/tmp/go.cover | awk '$NF != "100.0%" && $1 != "total:"')
if [ -n "$missing" ]; then echo "Go coverage below 100%:"; echo "$missing"; exit 1; fi
echo "Go coverage: 100%"
