#!/usr/bin/env bash
set -e

export LANG=en_US.UTF-8
export LC_ALL=en_US.UTF-8

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

API_PORT="${AGENTCANVAS_DEV_PORT:-8080}"

check_existing_api() {
  local listener_pid=""
  local listener_cwd=""
  local health_response=""

  listener_pid="$(lsof -nP -tiTCP:"$API_PORT" -sTCP:LISTEN 2>/dev/null | head -n 1 || true)"
  if [[ -z "$listener_pid" ]]; then
    return 0
  fi

  while IFS= read -r record; do
    case "$record" in
      n*) listener_cwd="${record#n}" ;;
    esac
  done < <(lsof -a -p "$listener_pid" -d cwd -Fn 2>/dev/null || true)

  health_response="$(curl -fsS --max-time 2 "http://127.0.0.1:${API_PORT}/api/v1/health" 2>/dev/null || true)"
  if [[ "$listener_cwd" == "$ROOT_DIR" && "$health_response" == *'"status":"healthy"'* ]]; then
    echo "AgentCanvas dev is already running (API PID $listener_pid, port $API_PORT)."
    return 1
  fi

  if [[ "$listener_cwd" == "$ROOT_DIR" ]]; then
    echo "ERROR: AgentCanvas already owns port $API_PORT, but its health check is not ready." >&2
  else
    echo "ERROR: port $API_PORT is already used by PID $listener_pid${listener_cwd:+ (cwd: $listener_cwd)}." >&2
  fi
  echo "Stop the existing process or set AGENTCANVAS_DEV_PORT to the configured API port." >&2
  return 2
}

set +e
check_existing_api
existing_api_status=$?
set -e
if [[ "$existing_api_status" -eq 1 ]]; then
  exit 0
fi
if [[ "$existing_api_status" -ne 0 ]]; then
  exit "$existing_api_status"
fi

docker compose -f deployments/docker-compose.yml up -d

./scripts/migrate.sh
npm --prefix web run build

pids=()

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  for pid in "${pids[@]}"; do
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
    fi
  done
  for pid in "${pids[@]}"; do
    wait "$pid" 2>/dev/null || true
  done
  exit "$status"
}

trap cleanup EXIT INT TERM

go run ./cmd/worker &
pids+=("$!")

go run ./cmd/workspace-pruner &
pids+=("$!")

go run ./cmd/api &
pids+=("$!")

while true; do
  for pid in "${pids[@]}"; do
    if ! kill -0 "$pid" 2>/dev/null; then
      set +e
      wait "$pid"
      status=$?
      set -e
      exit "$status"
    fi
  done
  sleep 1
done
