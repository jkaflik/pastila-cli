.PHONY: build test lint integration-test editor-test clean all

# Default target
all: build test

# Build the binary
build:
	go build -o pastila ./cmd/pastila

# Run unit tests
test:
	go test -v ./...

# Run linter
lint:
	golangci-lint run

# Clean build artifacts
clean:
	rm -f pastila
	
.DEFAULT_GOAL := build