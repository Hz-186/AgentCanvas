.PHONY: dev run worker docker-up docker-down tidy test migrate

dev:
	./scripts/dev.sh

run:
	go run ./cmd/api

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
