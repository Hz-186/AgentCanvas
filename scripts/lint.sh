#!/usr/bin/env bash
set -e

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

echo "==> go vet ./..."
go vet ./...

echo "==> go fmt check..."
fmt_output=$(gofmt -l .)
if [ -n "$fmt_output" ]; then
    echo "The following files need formatting:"
    echo "$fmt_output"
    exit 1
fi

echo "==> go build ./cmd/api (dry-run check)"
go build -o /dev/null ./cmd/api

echo "==> go build ./cmd/worker (dry-run check)"
go build -o /dev/null ./cmd/worker

echo "==> go build ./cmd/migrate (dry-run check)"
go build -o /dev/null ./cmd/migrate

echo "==> go build ./cmd/backfill-rule-sets (dry-run check)"
go build -o /dev/null ./cmd/backfill-rule-sets

echo "==> npm typecheck"
npm --prefix web run typecheck

echo ""
echo "All checks passed."
