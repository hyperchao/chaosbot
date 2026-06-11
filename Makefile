# chaosbot build & dev commands
# All targets are no-ops for dry runs: `make -n <target>` is safe.

GO          ?= go
BIN_DIR     ?= bin
BIN_NAME    ?= chaosbot
BIN         := $(BIN_DIR)/$(BIN_NAME)
PKG         := ./...
BUILD_PKG   := ./cmd/chaosbot  # build only the main package; ./... can't -o to a file path
VERSION     ?= $(shell git describe --tags --dirty --always 2>/dev/null || echo "dev")
LDFLAGS     ?= -s -w -X main.version=$(VERSION)

.PHONY: all build test lint run perf fmt vet clean help

all: build ## default target = build

help: ## list targets
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  %-12s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## compile binary to bin/chaosbot
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) $(BUILD_PKG)

test: vet ## run unit tests (vet first)
	$(GO) test -race -count=1 $(PKG)

lint: fmt vet ## gofmt + go vet

fmt: ## check gofmt cleanliness
	@test -z "$$(gofmt -l . | tee /dev/stderr)"

vet: ## go vet
	$(GO) vet $(PKG)

run: build ## run the binary, pass args via ARGS="..."
	$(BIN) $(ARGS)

perf: ## performance baseline (requires scripts/measure.sh, Phase 01-3)
	@bash scripts/measure.sh

clean: ## remove build artifacts
	rm -rf $(BIN_DIR)
