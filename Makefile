BINARY  := orakel
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: run test test-v race cover bench build build-all clean

run:
	go run .

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
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .

build-all:
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-arm64 .
	GOOS=darwin  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-amd64 .
	GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-amd64 .
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-windows-amd64.exe .

clean:
	rm -rf bin coverage.out coverage.html
