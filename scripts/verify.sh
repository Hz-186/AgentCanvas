#!/usr/bin/env bash
set -e

export LANG=en_US.UTF-8
export LC_ALL=en_US.UTF-8

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

check_migration_tables() {
    echo "==> Checking migration tables against code references..."

    declared_tables=$(sed -nE 's/^[[:space:]]*CREATE TABLE( IF NOT EXISTS)?[[:space:]]+`?([[:alnum:]_]+)`?[[:space:]]*\(.*/\2/p' migrations/*.up.sql 2>/dev/null | sort -u)
    dropped_tables=$(sed -nE 's/^[[:space:]]*DROP TABLE( IF EXISTS)?[[:space:]]+([^;]+).*/\2/p' migrations/*.up.sql 2>/dev/null | tr ',' '\n' | sed -nE 's/.*`([^`]+)`.*/\1/p; s/^[[:space:]]*([[:alnum:]_]+).*/\1/p' | sort -u)
    code_tables=$(rg --no-filename 'TableName\(\).*return "[^"]+"' internal -g '*.go' | sed -nE 's/.*TableName\(\).*return "([^"]+)".*/\1/p' | sort -u)

    active_tables=""
    for table in $declared_tables; do
        [[ -z "$table" ]] && continue
        if ! printf '%s\n' "$dropped_tables" | grep -Fxq "$table"; then
            active_tables="${active_tables}${table}"$'\n'
        fi
    done

    migration_only=""
    for table in $active_tables; do
        [[ -z "$table" ]] && continue
        if ! printf '%s\n' "$code_tables" | grep -Fxq "$table"; then
            migration_only="$migration_only $table"
        fi
    done

    code_only=""
    for table in $code_tables; do
        [[ -z "$table" ]] && continue
        if ! printf '%s\n' "$active_tables" | grep -Fxq "$table"; then
            code_only="$code_only $table"
        fi
    done

    if [ -n "$migration_only" ] || [ -n "$code_only" ]; then
        echo "Schema/model mismatch detected."
        if [ -n "$migration_only" ]; then
            echo "Migration tables without matching TableName():"
            for table in $migration_only; do printf '  - %s\n' "$table"; done
        fi
        if [ -n "$code_only" ]; then
            echo "Code TableName() values missing from migrations:"
            for table in $code_only; do printf '  - %s\n' "$table"; done
        fi
        exit 1
    fi

    echo "Migration and code table sets match."
}

check_migration_tables

echo "==> go vet ./..."
go vet ./...

echo "==> go build check (api + worker + workspace-pruner + migrate)..."
go build -o /dev/null ./cmd/api
go build -o /dev/null ./cmd/worker
go build -o /dev/null ./cmd/workspace-pruner
go build -o /dev/null ./cmd/migrate

echo ""
echo "All validation checks passed."
