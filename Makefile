# CHUM - Autonomous Agent Orchestrator
# Makefile for development, build, and operations

SHELL := /usr/bin/env bash

# Build settings
BINARY_NAME := chum
BUILD_DIR := build
DIST_DIR := $(BUILD_DIR)/dist
VERSION := $(shell cat VERSION 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# Go settings
GO := go
GOFLAGS := -trimpath
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)"

# Race test settings
RACE_PACKAGES := \
	./internal/store/... \
	./internal/dispatch/... \
	./internal/chief/... \
	./internal/temporal/... \
	./internal/api/... \
	./internal/config/... \
	./internal/matrix/... \
	./internal/graph/... \
	./internal/git/...
RACE_TIMEOUT ?= 10m
RACE_LOCK_WAIT ?= 120
RACE_JSON_OUT ?=
RACE_CI_TIMEOUT ?= 25m
RACE_CI_LOCK_WAIT ?= 60
RACE_CI_JSON_OUT := $(BUILD_DIR)/test-race.jsonl
RACE_CI_LOG_OUT := $(BUILD_DIR)/test-race.log

# Scripts
SCRIPT_DIR := scripts
DEV_SCRIPTS := $(SCRIPT_DIR)/dev
RELEASE_SCRIPTS := $(SCRIPT_DIR)/release
OPS_SCRIPTS := $(SCRIPT_DIR)/ops

# Source files
SRC_FILES := $(shell find . -type f -name '*.go' -not -path './vendor/*')

.PHONY: all help build build-all install clean test test-race test-race-ci lint fmt vet
.PHONY: service-install service-start service-stop service-logs
.PHONY: release snapshot docker

.DEFAULT_GOAL := help

##@ Development

help: ## Display this help message
	@echo "CHUM $(VERSION) - Available targets:"
	@echo ""
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: $(SRC_FILES) ## Build chum binary
	$(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BINARY_NAME) ./cmd/chum/

build-all: ## Build all binaries (chum + tools)
	$(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BINARY_NAME) ./cmd/chum/
	$(GO) build $(GOFLAGS) -o $(BUILD_DIR)/db-backup ./cmd/db-backup/
	$(GO) build $(GOFLAGS) -o $(BUILD_DIR)/db-restore ./cmd/db-restore/

install: build ## Build and install chum to ~/.local/bin
	mkdir -p ~/.local/bin
	cp $(BINARY_NAME) ~/.local/bin/

clean: ## Remove build artifacts
	rm -f $(BINARY_NAME)
	rm -rf $(BUILD_DIR)/
	rm -f *.test

test: ## Run all tests
	$(GO) test ./...

test-verbose: ## Run all tests with verbose output
	$(GO) test -v ./...

##@ Code Quality

lint: fmt vet ## Run all linters

fmt: ## Format Go code
	$(GO) fmt ./...

vet: ## Run go vet
	$(GO) vet ./...

lint-full: ## Run full golangci-lint suite (uses .golangci.yml)
	golangci-lint run ./...

test-coverage: ## Run tests with coverage report
	$(GO) test -coverprofile=$(BUILD_DIR)/coverage.out ./...
	$(GO) tool cover -func=$(BUILD_DIR)/coverage.out | tail -1
	@echo "Full report: go tool cover -html=$(BUILD_DIR)/coverage.out"

lint-docs: ## Check markdown docs for broken internal references
	@bash $(DEV_SCRIPTS)/docs-lint.sh

##@ Testing

test-race: ## Run race tests for concurrency-critical packages
	TEST_SAFE_GO_TEST_TIMEOUT=$(RACE_TIMEOUT) \
	TEST_SAFE_LOCK_WAIT_SEC=$(RACE_LOCK_WAIT) \
	TEST_SAFE_JSON_OUT="$(RACE_JSON_OUT)" \
	$(DEV_SCRIPTS)/test-safe.sh -race $(RACE_PACKAGES)

test-race-ci: ## CI race entrypoint with timeout/log output
	@mkdir -p $(BUILD_DIR)
	@set -o pipefail; \
	TEST_SAFE_GO_TEST_TIMEOUT=$(RACE_CI_TIMEOUT) \
	TEST_SAFE_LOCK_WAIT_SEC=$(RACE_CI_LOCK_WAIT) \
	TEST_SAFE_JSON_OUT="$(RACE_CI_JSON_OUT)" \
	$(DEV_SCRIPTS)/test-safe.sh -race $(RACE_PACKAGES) 2>&1 | tee "$(RACE_CI_LOG_OUT)"

##@ Systemd Service

service-install: ## Install user systemd service
	mkdir -p ~/.config/systemd/user/
	cp deploy/systemd/chum.service ~/.config/systemd/user/
	systemctl --user daemon-reload

service-start: ## Start and enable user systemd service
	systemctl --user enable --now chum.service

service-stop: ## Stop and disable user systemd service
	systemctl --user stop chum.service
	systemctl --user disable chum.service

service-logs: ## View systemd service logs
	journalctl --user -u chum.service -f

##@ Release

release: ## Create a new release (requires VERSION)
ifndef VERSION
	$(error VERSION is required. Usage: make release VERSION=x.y.z)
endif
	$(RELEASE_SCRIPTS)/bump-version.sh $(VERSION)
	$(RELEASE_SCRIPTS)/create-release-tag.sh $(VERSION)
	$(RELEASE_SCRIPTS)/generate-changelog.sh

snapshot: ## Create a snapshot release (current commit)
	$(RELEASE_SCRIPTS)/generate-changelog.sh --snapshot

dry-run: ## Dry run release process
	$(RELEASE_SCRIPTS)/dry-run-release.sh

##@ Docker

docker-build: ## Build Docker image
	docker build -t chum:$(VERSION) -f build/package/Dockerfile .

docker-run: ## Run Docker container
	docker run --rm -v $(PWD)/chum.toml:/etc/chum/chum.toml chum:$(VERSION)

##@ Utilities

check: ## Run pre-commit checks (fmt, vet, test)
	$(MAKE) fmt
	$(MAKE) vet
	$(MAKE) test

info: ## Display build information
	@echo "Version: $(VERSION)"
	@echo "Commit: $(COMMIT)"
	@echo "Build Time: $(BUILD_TIME)"
	@echo "Go Version: $(shell $(GO) version)"
