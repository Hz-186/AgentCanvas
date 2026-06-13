.PHONY: dev run docker-up docker-down tidy test migrate

dev:
	./scripts/dev.sh

run:
	go run ./cmd/api

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