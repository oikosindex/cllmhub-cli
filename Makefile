.PHONY: build build-all clean test lint

BINARY_NAME=cllmhub
VERSION?=0.1.0
BUILD_DIR=bin
LDFLAGS=-ldflags "-X main.Version=$(VERSION)"

# Default target
build:
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/cllmhub

# Build for all platforms
build-all: clean
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/cllmhub
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/cllmhub
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/cllmhub
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/cllmhub
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/cllmhub

clean:
	rm -rf $(BUILD_DIR)

test:
	go test -v ./...

lint:
	golangci-lint run

# Install locally
install: build
	cp $(BUILD_DIR)/$(BINARY_NAME) $(GOPATH)/bin/

# Run with hot reload (requires air)
dev:
	air -c .air.toml

# Download dependencies
deps:
	go mod download
	go mod tidy
