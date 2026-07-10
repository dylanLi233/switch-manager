.PHONY: test test-race vet check run

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

check: test test-race vet

run:
	go run ./cmd/server -config configs/config.example.yaml
