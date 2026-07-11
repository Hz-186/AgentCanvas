.PHONY: dev dev-v1 dev-v2 run worker build build-web typecheck-web test-web docker-up docker-down tidy test migrate lint fmt verify clean

dev:
	./scripts/dev.sh

dev-v1:
	npm --prefix web run dev

dev-v2:
	npm --prefix web_v2 run dev

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

worker: migrate
	go run ./cmd/worker

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
	./scripts/lint.sh

fmt:
	gofmt -w .

verify:
	./scripts/verify.sh

verify-tables:
	@migrations/*.up.sql 2>/dev/null; \
	declared=$$(grep -h 'CREATE TABLE IF NOT EXISTS ' migrations/*.up.sql 2>/dev/null | sed 's/CREATE TABLE IF NOT EXISTS //' | sed 's/ (.*//' | sort -u); \
	codetables=$$(grep -rh 'TableName()' internal/ | grep -oP '"([^"]+)"' | tr -d '"' | sort -u); \
	unused=""; \
	for table in $$declared; do \
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
