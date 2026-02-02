BINARY_NAME=lns
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS=-ldflags "-X main.version=$(VERSION)"

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod

# Directories
BUILD_DIR=build
CMD_DIR=cmd/lns

.PHONY: all build clean test coverage deps lint install uninstall release help

all: build

## Build

build: ## Build for current platform
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./$(CMD_DIR)

build-fast: ## Build without optimizations (faster compile)
	$(GOBUILD) -o $(BUILD_DIR)/$(BINARY_NAME) ./$(CMD_DIR)

## Cross-platform builds

build-all: build-linux build-darwin build-windows ## Build for all platforms

build-linux: ## Build for Linux (amd64 and arm64)
	GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./$(CMD_DIR)
	GOOS=linux GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./$(CMD_DIR)

build-darwin: ## Build for macOS (amd64 and arm64)
	GOOS=darwin GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./$(CMD_DIR)
	GOOS=darwin GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./$(CMD_DIR)

build-windows: ## Build for Windows
	GOOS=windows GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./$(CMD_DIR)

## Development

run: ## Run the application
	$(GOCMD) run ./$(CMD_DIR) $(ARGS)

dev: ## Run with live reload (requires air)
	air

test: ## Run tests
	$(GOTEST) -v ./...

coverage: ## Run tests with coverage
	$(GOTEST) -v -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html

lint: ## Run linter (requires golangci-lint)
	golangci-lint run

fmt: ## Format code
	$(GOCMD) fmt ./...

vet: ## Run go vet
	$(GOCMD) vet ./...

## Dependencies

deps: ## Download dependencies
	$(GOMOD) download

tidy: ## Tidy dependencies
	$(GOMOD) tidy

## Installation

install: build ## Install to /usr/local/bin
	cp $(BUILD_DIR)/$(BINARY_NAME) /usr/local/bin/$(BINARY_NAME)

install-user: build ## Install to ~/bin
	mkdir -p ~/bin
	cp $(BUILD_DIR)/$(BINARY_NAME) ~/bin/$(BINARY_NAME)

uninstall: ## Remove from /usr/local/bin
	rm -f /usr/local/bin/$(BINARY_NAME)

## Release

release: clean build-all ## Build release binaries
	@echo "Release binaries in $(BUILD_DIR)/"
	@ls -la $(BUILD_DIR)/

checksums: ## Generate checksums for release binaries
	cd $(BUILD_DIR) && sha256sum * > checksums.txt

## Cleanup

clean: ## Remove build artifacts
	$(GOCLEAN)
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html

## Help

help: ## Show this help
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
