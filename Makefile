.PHONY: build test lint golden snapshot

build:
	go build -o bin/reearth ./cmd/reearth

test:
	go test ./...

golden:
	go test ./cmd/reearth -run TestCommandSurface -update

lint:
	golangci-lint run ./...

snapshot:
	goreleaser release --snapshot --clean --skip=publish,sign
