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

# The six release platforms (SPEC §11.1).
TARGETS     := linux/amd64 linux/arm64 windows/amd64 windows/arm64 darwin/amd64 darwin/arm64

.PHONY: all build cross lint test test-race tidy clean help

all: build

## build: the binary for this machine, into dist/
build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(DIST)/nanobird ./cmd/nanobird

## cross: every release target, into dist/<os>_<arch>/
cross:
	@set -e; for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		out=$(DIST)/$${os}_$${arch}; \
		echo "build $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch $(GO) build -ldflags "$(LDFLAGS)" -o $$out/nanobird$$ext ./cmd/nanobird; \
	done

## lint: vet, gofmt and golangci-lint when it is installed
lint:
	$(GO) vet ./...
	@gofmt -l cmd internal tools | tee /dev/stderr | (! read)
	@if command -v golangci-lint >/dev/null; then golangci-lint run; fi

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
