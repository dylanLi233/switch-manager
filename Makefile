.PHONY: test test-race vet check run migrate-up migrate-down test-migrations

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

check: test test-race vet

run:
	go run ./cmd/server -config configs/config.example.yaml

migrate-up:
	bash ./scripts/migrate.sh up

migrate-down:
	bash ./scripts/migrate.sh down 1

test-migrations:
	bash ./scripts/test-migrations.sh
