# NanoBird build (SPEC §11).
#
# One static Go binary per platform: no cgo, no runtime dependencies. Every
# target is built with CGO_ENABLED=0 and cross-compiles from one runner.

SHELL       := /bin/bash
GO          ?= go
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS     := -X main.version=$(VERSION) -X main.commit=$(COMMIT)
DIST        := dist
export CGO_ENABLED := 0

# Linux and Windows, amd64 and arm64. macOS is out for now (docs/ROADMAP.md).
TARGETS     := linux/amd64 linux/arm64 windows/amd64 windows/arm64

.PHONY: all build cross lint lang-check test test-race tidy clean help acp-tck

all: build

## build: the binary for this machine, into dist/
build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(DIST)/nanobird ./cmd/nanobird

## acp-tck: the official ACP test kit against nanobird acp, with a scripted model (SPEC §12, G2)
acp-tck:
	GO=$(GO) sh tools/acptck/run.sh

## cross: every release target, into dist/<os>_<arch>/
cross:
	@set -e; for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		out=$(DIST)/$${os}_$${arch}; \
		echo "build $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch $(GO) build -ldflags "$(LDFLAGS)" -o $$out/nanobird$$ext ./cmd/nanobird; \
	done

## lint: vet, gofmt, English-only text and golangci-lint when it is installed
lint: lang-check
	$(GO) vet ./...
	@gofmt -l cmd internal tools | tee /dev/stderr | (! read)
	@if command -v golangci-lint >/dev/null; then golangci-lint run; fi

## lang-check: fails on non-Latin letters in NanoBird's text (SPEC §5.13)
lang-check:
	$(GO) run ./tools/langcheck

## test: unit tests
test:
	$(GO) test ./...

## test-race: unit tests under the race detector (needs cgo)
test-race:
	CGO_ENABLED=1 $(GO) test -race ./...

## tidy: go.mod and go.sum
tidy:
	$(GO) mod tidy

## clean: build output
clean:
	rm -rf $(DIST)

## help: this list
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## //'
