.PHONY: dev run worker build-web typecheck-web test-web docker-up docker-down tidy test migrate

dev:
	./scripts/dev.sh

run: migrate build-web
	go run ./cmd/api

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
