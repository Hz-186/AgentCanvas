.PHONY: dev dev-v1 dev-v2 run worker workspace-pruner backfill-context-index build build-web typecheck-web test-web test-python benchmark-python docker-up docker-down tidy test migrate lint fmt verify clean

dev:
	./scripts/dev.sh

dev-v1:
	npm --prefix web run dev

run: migrate verify-tables build-web
	go run ./cmd/api

build: verify-tables migrate build-web
	./scripts/build.sh

build-web:
	npm --prefix web run build

typecheck-web:
	npm --prefix web run typecheck

test-web:
	npm --prefix web test -- --run

test-python:
	PYTHONDONTWRITEBYTECODE=1 PYTHONPATH=python python3 -m unittest discover -s python/tests -v

benchmark-python:
	@test -n "$$AGENTCANVAS_PYTHON_BRIDGE_TEST_TARGET" || (echo "set AGENTCANVAS_PYTHON_BRIDGE_TEST_TARGET and AGENTCANVAS_PYTHON_BRIDGE_TOKEN"; exit 1)
	go test ./internal/infrastructure/pythonbridge -run TestLivePythonBridgeBenchmark -count=1 -v
	@docker stats --no-stream --format 'python-bridge memory={{.MemUsage}}' agentcanvas-python-bridge 2>/dev/null || true

worker: migrate
	go run ./cmd/worker

workspace-pruner: migrate
	go run ./cmd/workspace-pruner

backfill-context-index: migrate
	go run ./cmd/backfill-context-index

docker-up:
	docker compose -f deployments/docker-compose.yml up -d

docker-down:
	docker compose -f deployments/docker-compose.yml down

tidy:
	go mod tidy

test:
	go test ./...

migrate:
	./scripts/migrate.sh

lint:
	bash ./scripts/lint.sh

fmt:
	gofmt -w .

verify:
	./scripts/verify.sh

verify-tables:
	@declared=$$(grep -h '^CREATE TABLE ' migrations/*.up.sql 2>/dev/null | sed 's/^CREATE TABLE //' | sed 's/ (.*//' | tr -d '\140' | sort -u); \
	dropped=$$(grep -h 'DROP TABLE IF EXISTS ' migrations/*.up.sql 2>/dev/null | sed 's/DROP TABLE IF EXISTS //' | sed 's/;//' | sort -u); \
	active_declared=""; \
	for table in $$declared; do \
		is_dropped=false; \
		for dropped_table in $$dropped; do \
			if [ "$$table" = "$$dropped_table" ]; then is_dropped=true; break; fi; \
		done; \
		if [ "$$is_dropped" = false ]; then active_declared="$$active_declared $$table"; fi; \
	done; \
	codetables=$$(grep -rh 'TableName()' internal/ | sed -n 's/.*return "\([^"]*\)".*/\1/p' | sort -u); \
	unused=""; \
	for table in $$active_declared; do \
		matched=false; \
		for ct in $$codetables; do \
			if [ "$$table" = "$$ct" ]; then \
				matched=true; \
				break; \
			fi; \
		done; \
		if [ "$$matched" = false ]; then \
			unused="$$unused $$table"; \
		fi; \
	done; \
	if [ -n "$$unused" ]; then \
		echo "ERROR: Orphaned migration tables with no code references:"; \
		for table in $$unused; do \
			echo "  - $$table"; \
		done; \
		echo "Remove these from migrations/ or add matching TableName() in code."; \
		exit 1; \
	fi

clean:
	rm -rf bin/ web/dist/
