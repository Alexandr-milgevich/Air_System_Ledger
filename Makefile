.PHONY: test run db-up db-down

test:
	go test ./...

run:
	go run ./cmd/server

db-up:
	docker compose --env-file .env -f deploy/compose.yaml up -d

db-down:
	docker compose --env-file .env -f deploy/compose.yaml down
