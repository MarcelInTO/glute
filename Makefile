# glute — build automation.
#
#   make          cross-compile binaries for all platforms into bin/
#   make build    quick build for the host platform only (bin/glute)
#   make run      build for the host and run it (ARGS="..." to pass flags)
#   make test     run unit tests
#   make vet      run go vet
#   make fmt      gofmt all sources
#   make tidy     tidy go.mod / go.sum
#   make clean    remove bin/
#   make help     list targets

BINARY  := glute
BIN_DIR := bin
PKG     := github.com/MarcelInTO/glute
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

GO      := go
LDFLAGS := -s -w -X $(PKG)/cmd.Version=$(VERSION)
GOBUILD := CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)'

# Platforms to cross-compile — one binary per entry lands in $(BIN_DIR)/.
# Add e.g. darwin/amd64 (Intel Macs) or linux/arm64 (ARM servers) as needed.
PLATFORMS := linux/amd64 darwin/arm64 windows/amd64

.DEFAULT_GOAL := all

.PHONY: all
all: ## Cross-compile binaries for all platforms into bin/
	@mkdir -p $(BIN_DIR)
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		ext=""; if [ "$$os" = windows ]; then ext=".exe"; fi; \
		out="$(BIN_DIR)/$(BINARY)-$$os-$$arch$$ext"; \
		echo "  building $$out ($(VERSION))"; \
		GOOS=$$os GOARCH=$$arch $(GOBUILD) -o "$$out" . || exit 1; \
	done
	@echo "done -> $(BIN_DIR)/"

.PHONY: build
build: ## Build for the host platform only (bin/glute)
	@mkdir -p $(BIN_DIR)
	$(GOBUILD) -o $(BIN_DIR)/$(BINARY) .

.PHONY: run
run: build ## Build for the host and run (ARGS="..." to pass flags)
	@$(BIN_DIR)/$(BINARY) $(ARGS)

.PHONY: test
test: ## Run unit tests
	$(GO) test ./...

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: fmt
fmt: ## gofmt all sources
	gofmt -w .

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	$(GO) mod tidy

.PHONY: clean
clean: ## Remove bin/
	rm -rf $(BIN_DIR)

.PHONY: help
help: ## List targets
	@awk 'BEGIN{FS=":.*## "} /^[a-zA-Z_-]+:.*## /{printf "  %-8s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
