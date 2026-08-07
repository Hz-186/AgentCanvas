#!/usr/bin/env bash
set -e

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

OUT_DIR="${OUT_DIR:-./bin}"
mkdir -p "$OUT_DIR"

echo "==> Running migrations..."
./scripts/migrate.sh

echo "==> Building frontend..."
npm --prefix web run build

echo "==> Building api..."
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$OUT_DIR/api" ./cmd/api

echo "==> Building worker..."
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$OUT_DIR/worker" ./cmd/worker

echo "==> Building workspace pruner..."
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$OUT_DIR/workspace-pruner" ./cmd/workspace-pruner

echo "==> Building migrate..."
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$OUT_DIR/migrate" ./cmd/migrate

echo ""
echo "Build complete. Binaries in $OUT_DIR/"
ls -lh "$OUT_DIR/"
