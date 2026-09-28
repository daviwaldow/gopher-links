.PHONY: run build test test-race vet

build:
	go build -o bin/gopher-links ./cmd/server

run:
	go run ./cmd/server

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...
