BINARY  := ccmas
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/alexwenzel/ccmas/internal/cli.Version=$(VERSION)

.PHONY: run test test-v race cover bench build build-all py-test check clean

run:
	go run -ldflags "$(LDFLAGS)" ./cmd/ccmas

test:
	go test ./...

test-v:
	go test -v -cover ./...

race:
	go test -race ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Report: coverage.html"

bench:
	go test -bench=. -run='^$$' ./...

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/ccmas

build-all:
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-arm64 ./cmd/ccmas
	GOOS=darwin  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-amd64 ./cmd/ccmas
	GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-amd64 ./cmd/ccmas
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-windows-amd64.exe ./cmd/ccmas

py-test:
	cd runners/claude_sdk && uv run --frozen pytest -q

check:
	$(MAKE) test
	$(MAKE) race
	go vet ./...
	$(MAKE) build
	$(MAKE) py-test

clean:
	rm -rf bin coverage.out coverage.html
