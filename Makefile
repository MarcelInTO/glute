# glute — build automation.
#
#   make            cross-compile binaries for all platforms into bin/
#   make build      quick build for the host platform only (bin/glute)
#   make run        build for the host and run it (ARGS="..." to pass flags)
#   make check      the full pre-commit gate: fmt-check, vet, test
#   make test       run unit tests
#   make vet        run go vet
#   make fmt        gofmt all sources
#   make fmt-check  fail if anything needs gofmt
#   make smoke      build and run the binary's headless self-checks
#   make drive-all  play every TUI scenario against the real binary in a pty
#   make drive SCENARIO=tools/ptydrive/scenarios/<name>.txt   play one, showing it
#   make dist       stage release archives + SHA256SUMS in dist/
#   make verify-dist  unpack this host's archive and self-check it
#   make formula    generate the Homebrew formula from dist/SHA256SUMS
#   make manifest   generate the Scoop manifest from dist/SHA256SUMS
#   make tidy       tidy go.mod / go.sum
#   make clean      remove bin/ and dist/
#   make help       list targets

BINARY   := glute
BIN_DIR  := bin
DIST_DIR := dist
PKG      := github.com/MarcelInTO/glute
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# Two spellings of the same version. The git tag and the binary keep the leading
# "v" (`git describe` produces it, so a dev build and a release build agree);
# archive names, the Homebrew formula and the Scoop manifest take the bare X.Y.Z,
# because Homebrew compares versions numerically and a leading "v" breaks upgrade
# ordering.
DIST_VERSION := $(patsubst v%,%,$(VERSION))

GO      := go
LDFLAGS := -s -w -X $(PKG)/cmd.Version=$(VERSION)
GOBUILD := CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)'

# This project's own sources, for the gofmt targets. Deliberately not a plain
# "." : gofmt descends into dot-directories, and CI parks the module cache at
# $CI_PROJECT_DIR/.go because GitLab can only cache paths inside the project
# directory. Every dependency's source would then be gofmt's to grade, and they
# are not all gofmt-clean (tcell and pflag aren't), so `make check` passed on a
# cache miss and failed on a hit. Lazily assigned so `find` only runs for a
# target that needs it.
GOFILES = $(shell find . -name '*.go' -not -path './.*' -print)

# Platforms to cross-compile — one binary per entry lands in $(BIN_DIR)/, and one
# archive per entry in $(DIST_DIR)/. The four non-Windows rows are exactly what the
# Homebrew formula's on_macos/on_linux × on_arm/on_intel matrix needs, so dropping
# one here silently breaks `make formula`; windows/amd64 is what the Scoop manifest
# needs (`make manifest`). Windows on ARM would be a windows/arm64 row here plus an
# "arm64" block in packaging/glute.json.in.
PLATFORMS := linux/amd64 linux/arm64 darwin/arm64 darwin/amd64 windows/amd64

# Rides along in every archive: MIT's attribution requirement follows the binary,
# not just the repository.
DIST_EXTRA := LICENSE README.md

# GNU coreutils on Linux, BSD on macOS. Same output format either way
# ("<hash>  <file>"), which is what `make formula` and `shasum -c` both expect.
SHA256 := $(shell command -v sha256sum >/dev/null 2>&1 && echo sha256sum || echo 'shasum -a 256')

# Where the formula and the Scoop manifest point for downloads. The GitLab project
# is private, so the public GitHub mirror's release assets are what `brew install`
# and `scoop install` can actually reach.
RELEASE_URL ?= https://github.com/MarcelInTO/glute/releases/download/$(VERSION)

# Claude Code keeps this project's memory under a per-project dir in $HOME.
# We store the real files in-repo (so they sync via git) and symlink the
# $HOME location to them — see the `memory-link` target. Claude derives the
# per-project dir name from the cwd by replacing '/' and '.' with '-'.
MEMORY_SRC  := $(CURDIR)/.claude/memory
MEMORY_LINK := $(HOME)/.claude/projects/$(subst .,-,$(subst /,-,$(CURDIR)))/memory

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

.PHONY: check
check: fmt-check vet test ## Run the full pre-commit gate (fmt-check, vet, test)

.PHONY: test
test: ## Run unit tests
	$(GO) test ./...

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: fmt
fmt: ## gofmt all sources
	gofmt -w $(GOFILES)

.PHONY: fmt-check
fmt-check: ## Fail if any source needs gofmt (CI's read-only twin of `fmt`)
	@files="$(GOFILES)"; \
	if [ -z "$$files" ]; then echo "  no Go sources"; exit 0; fi; \
	out=$$(gofmt -l $$files); \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi; \
	echo "  gofmt clean"

# The two things a shipped binary has to do before anything else: say what it is,
# and produce a snapshot. `refresh --sample` needs no config, no token and no
# GitLab, so it exercises the whole data path headlessly — the only part of glute
# that can be asserted without a TTY.
.PHONY: smoke
smoke: build ## Build and run the binary's headless self-checks
	@$(BIN_DIR)/$(BINARY) version
	@$(BIN_DIR)/$(BINARY) refresh --sample > /dev/null
	@echo "  smoke ok"

# Driving the real TUI. Unit tests call onKey/onMouse directly, which skips
# tview's own event dispatch — how a click is built from a press and a release
# (with a move ahead of them, sharing one event), which widget a key reaches,
# what the terminal is actually sent — and bugs live exactly there.
# tools/ptydrive plays a scenario (keys, clicks, and what the screen must then
# show) against `bin/glute --sample` in a pseudo-terminal; the scenarios under
# tools/ptydrive/scenarios/ double as a record of how the key flows behave. The
# terminal emulator it reads the screen through (pyte) lives in a venv under
# .cache/, made on first use (needs python3 and network once). Local only: it
# needs a pty, so Unix, and isn't part of `make check`.
PTY_VENV   := .cache/ptydrive-venv
PTY_PYTHON := $(PTY_VENV)/bin/python
PTYDRIVE   := tools/ptydrive/ptydrive.py
SCENARIOS   = $(wildcard tools/ptydrive/scenarios/*.txt)

$(PTY_PYTHON):
	python3 -m venv $(PTY_VENV)
	$(PTY_VENV)/bin/pip install -q 'pyte==0.8.2'

.PHONY: drive
drive: build $(PTY_PYTHON) ## Play one TUI scenario in a pty, printing its `show` steps (SCENARIO=...)
	@test -n "$(SCENARIO)" || { echo "usage: make drive SCENARIO=tools/ptydrive/scenarios/<name>.txt"; exit 2; }
	@$(PTY_PYTHON) $(PTYDRIVE) $(SCENARIO) -- $(BIN_DIR)/$(BINARY) --sample

.PHONY: drive-all
drive-all: build $(PTY_PYTHON) ## Play every TUI scenario against the real binary in a pty
	@fail=0; for s in $(SCENARIOS); do \
		$(PTY_PYTHON) $(PTYDRIVE) -q "$$s" -- $(BIN_DIR)/$(BINARY) --sample || fail=1; \
	done; exit $$fail

.PHONY: dist
dist: ## Stage release archives + SHA256SUMS in dist/ (VERSION=v1.2.3 to stamp)
	@case " $(PLATFORMS) " in *" windows/"*) \
		command -v zip >/dev/null 2>&1 || { \
			echo "error: 'zip' is required to package the Windows archive"; exit 1; };; \
	esac
	@rm -rf $(DIST_DIR)
	@mkdir -p $(DIST_DIR)
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		ext=""; if [ "$$os" = windows ]; then ext=".exe"; fi; \
		stage="$(BINARY)-$(DIST_VERSION)-$$os-$$arch"; \
		echo "  packaging $$stage"; \
		mkdir -p "$(DIST_DIR)/$$stage" || exit 1; \
		GOOS=$$os GOARCH=$$arch $(GOBUILD) -o "$(DIST_DIR)/$$stage/$(BINARY)$$ext" . || exit 1; \
		cp $(DIST_EXTRA) "$(DIST_DIR)/$$stage/" || exit 1; \
		if [ "$$os" = windows ]; then \
			( cd $(DIST_DIR) && zip -qr "$$stage.zip" "$$stage" ) || exit 1; \
		else \
			tar -C $(DIST_DIR) -czf "$(DIST_DIR)/$$stage.tar.gz" "$$stage" || exit 1; \
		fi; \
		rm -rf "$(DIST_DIR)/$$stage"; \
	done
	@cd $(DIST_DIR) && $(SHA256) $(BINARY)-* > SHA256SUMS
	@cat $(DIST_DIR)/SHA256SUMS

# Tests the exact bytes that ship, not `go run`: an archive is unpacked and the
# binary inside it is asked its version, which must agree with the tag it is being
# released as. That mismatch is the one packaging bug a green test suite cannot catch.
.PHONY: verify-dist
verify-dist: ## Unpack this host's own archive from dist/ and self-check it
	@os=$$($(GO) env GOOS); arch=$$($(GO) env GOARCH); \
	stage="$(BINARY)-$(DIST_VERSION)-$$os-$$arch"; \
	archive="$(DIST_DIR)/$$stage.tar.gz"; \
	[ -f "$$archive" ] || { echo "error: $$archive not found (run 'make dist')"; exit 1; }; \
	rm -rf "$(DIST_DIR)/.verify" && mkdir -p "$(DIST_DIR)/.verify"; \
	tar -C "$(DIST_DIR)/.verify" -xzf "$$archive" || exit 1; \
	bin="$(DIST_DIR)/.verify/$$stage/$(BINARY)"; \
	got=$$("$$bin" version) || exit 1; \
	[ "$$got" = "glute $(VERSION)" ] || { \
		echo "error: binary reports '$$got', expected 'glute $(VERSION)'"; exit 1; }; \
	"$$bin" refresh --sample > /dev/null || { echo "error: refresh --sample failed"; exit 1; }; \
	rm -rf "$(DIST_DIR)/.verify"; \
	echo "  verified $$stage ($$got)"

# Written from the checksums `dist` just computed, so the formula can never
# disagree with the assets it points at. RELEASE_URL defaults to the GitHub
# mirror's release for $(VERSION); override it to test against a draft.
.PHONY: formula
formula: ## Generate dist/glute.rb from dist/SHA256SUMS (run after `make dist`)
	@test -f $(DIST_DIR)/SHA256SUMS || { echo "error: run 'make dist' first"; exit 1; }
	@{ \
		echo "# Generated by glute's release workflow from packaging/$(BINARY).rb.in."; \
		echo "# Source of truth: git@studio.wevr.com:wevr/tech/glute.git, mirrored to"; \
		echo "# github.com/MarcelInTO/glute. Hand edits here are overwritten by the next release."; \
		sed -e 's|@VERSION@|$(DIST_VERSION)|g' -e 's|@BASE@|$(RELEASE_URL)|g' \
			packaging/$(BINARY).rb.in | sed -n '/^class /,$$p'; \
	} > $(DIST_DIR)/$(BINARY).rb
	@for p in darwin-arm64 darwin-amd64 linux-arm64 linux-amd64; do \
		key=$$(echo $$p | tr 'a-z-' 'A-Z_'); \
		sum=$$(awk -v f="$(BINARY)-$(DIST_VERSION)-$$p.tar.gz" '$$2 == f {print $$1}' $(DIST_DIR)/SHA256SUMS); \
		[ -n "$$sum" ] || { echo "error: no checksum for $$p in $(DIST_DIR)/SHA256SUMS"; exit 1; }; \
		sed -i.bak "s|@SHA_$$key@|$$sum|" $(DIST_DIR)/$(BINARY).rb || exit 1; \
	done
	@rm -f $(DIST_DIR)/$(BINARY).rb.bak
	@if grep -q '@[A-Z_]*@' $(DIST_DIR)/$(BINARY).rb; then \
		echo "error: unsubstituted placeholder left in the formula"; \
		grep -n '@[A-Z_]*@' $(DIST_DIR)/$(BINARY).rb; exit 1; fi
	@echo "  wrote $(DIST_DIR)/$(BINARY).rb ($(DIST_VERSION))"

# The Windows counterpart of `formula`: the Scoop manifest, from the same checksums
# and with the same placeholder guard, so it can never point at bytes other than the
# ones `dist` just produced. JSON has no comments, so the template's header is
# dropped (everything before the opening brace) and the provenance rides in Scoop's
# "##" comment key instead. The result is parsed once before it is declared written:
# a manifest that is not valid JSON breaks `scoop install glute` for every user of
# the bucket, and that is the one thing sed cannot promise.
.PHONY: manifest
manifest: ## Generate dist/glute.json (Scoop) from dist/SHA256SUMS (run after `make dist`)
	@test -f $(DIST_DIR)/SHA256SUMS || { echo "error: run 'make dist' first"; exit 1; }
	@sum=$$(awk -v f="$(BINARY)-$(DIST_VERSION)-windows-amd64.zip" '$$2 == f {print $$1}' $(DIST_DIR)/SHA256SUMS); \
	[ -n "$$sum" ] || { echo "error: no checksum for windows-amd64 in $(DIST_DIR)/SHA256SUMS"; exit 1; }; \
	sed -n '/^{/,$$p' packaging/$(BINARY).json.in \
		| sed -e 's|@VERSION@|$(DIST_VERSION)|g' -e 's|@BASE@|$(RELEASE_URL)|g' \
			-e "s|@SHA_WINDOWS_AMD64@|$$sum|g" > $(DIST_DIR)/$(BINARY).json
	@if grep -q '@[A-Z_]*@' $(DIST_DIR)/$(BINARY).json; then \
		echo "error: unsubstituted placeholder left in the manifest"; \
		grep -n '@[A-Z_]*@' $(DIST_DIR)/$(BINARY).json; exit 1; fi
	@if command -v jq >/dev/null 2>&1; then \
		jq -e . $(DIST_DIR)/$(BINARY).json > /dev/null || exit 1; \
	elif command -v python3 >/dev/null 2>&1; then \
		python3 -m json.tool $(DIST_DIR)/$(BINARY).json > /dev/null || exit 1; \
	else echo "  warning: neither jq nor python3 found, manifest not parsed"; fi
	@echo "  wrote $(DIST_DIR)/$(BINARY).json ($(DIST_VERSION))"

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	$(GO) mod tidy

.PHONY: clean
clean: ## Remove bin/ and dist/
	rm -rf $(BIN_DIR) $(DIST_DIR)

.PHONY: memory-link
memory-link: ## Symlink Claude's per-project memory dir to .claude/memory (run on each machine)
	@if [ ! -d "$(MEMORY_SRC)" ]; then \
		echo "error: $(MEMORY_SRC) not found (is this the repo root?)"; exit 1; \
	fi
	@if [ -L "$(MEMORY_LINK)" ]; then \
		rm "$(MEMORY_LINK)"; \
	elif [ -e "$(MEMORY_LINK)" ]; then \
		echo "error: $(MEMORY_LINK) exists and is not a symlink;"; \
		echo "       merge its contents into $(MEMORY_SRC), remove it, then re-run"; exit 1; \
	fi
	@mkdir -p "$(dir $(MEMORY_LINK))"
	@ln -s "$(MEMORY_SRC)" "$(MEMORY_LINK)"
	@echo "  linked $(MEMORY_LINK) -> $(MEMORY_SRC)"

.PHONY: help
help: ## List targets
	@awk 'BEGIN{FS=":.*## "} /^[a-zA-Z_-]+:.*## /{printf "  %-13s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
