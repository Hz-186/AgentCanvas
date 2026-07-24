#!/usr/bin/env bash
set -e

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

check_migration_tables() {
    echo "==> Checking migration tables against code references..."

    declared_tables=$(grep -h 'CREATE TABLE IF NOT EXISTS ' migrations/*.up.sql 2>/dev/null | sed 's/CREATE TABLE IF NOT EXISTS //' | sed 's/ (.*//' | sort -u)
    dropped_tables=$(grep -h 'DROP TABLE IF EXISTS ' migrations/*.up.sql 2>/dev/null | sed 's/DROP TABLE IF EXISTS //' | sed 's/;//' | sort -u)

    code_tables=$(grep -rh 'TableName()' internal/ | sed -n 's/.*return "\([^"]*\)".*/\1/p' | sort -u)

    unused=()

    for table in $declared_tables; do
        is_dropped=false
        for dropped_table in $dropped_tables; do
            if [ "$table" = "$dropped_table" ]; then
                is_dropped=true
                break
            fi
        done
        if [ "$is_dropped" = true ]; then
            continue
        fi
        matched=false
        for code_table in $code_tables; do
            if [ "$table" = "$code_table" ]; then
                matched=true
                break
            fi
        done
        if [ "$matched" = false ]; then
            unused+=("$table")
        fi
    done

    if [ ${#unused[@]} -gt 0 ]; then
        echo ""
        echo "WARNING: The following migration tables have NO matching TableName() in code:"
        for table in "${unused[@]}"; do
            echo "  - $table"
        done
        echo ""
        echo "These may be orphaned tables that should be removed from migrations."
        exit 1
    fi

    echo "All migration tables have matching code references."
}

check_migration_tables

echo "==> go vet ./..."
go vet ./...

echo "==> go build check (api + worker + migrate)..."
go build -o /dev/null ./cmd/api
go build -o /dev/null ./cmd/worker
go build -o /dev/null ./cmd/migrate

echo ""
echo "All validation checks passed."
