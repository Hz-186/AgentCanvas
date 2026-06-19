.PHONY: dev run worker build-web docker-up docker-down tidy test migrate

dev:
	./scripts/dev.sh

run: build-web
	go run ./cmd/api

build-web:
	npm --prefix web run build

worker:
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
