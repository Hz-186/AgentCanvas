#!/usr/bin/env bash
set -e

docker compose -f deployments/docker-compose.yml up -d

go run ./cmd/api