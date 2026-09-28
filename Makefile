.PHONY: build test lint snapshot

build:
	go build -o bin/reearth ./cmd/reearth

test:
	go test ./...

lint:
	golangci-lint run ./...

snapshot:
	goreleaser release --snapshot --clean --skip=publish,sign
