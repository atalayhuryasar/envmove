BINARY := envmove
PKG    := github.com/atalayhuryasar/envmove/cmd/envmove
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build install test lint fmt clean e2e

all: lint test build

## build — tek statik binary
build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/$(BINARY)

## install — binaryyi PATH'teki bir yere kopyalar
install:
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/$(BINARY)
	@echo "kuruldu: $$(go env GOPATH)/bin/$(BINARY)"

test:
	go test ./...

lint:
	go vet ./...
	@test -z "$$(gofmt -l . )" || (echo "gofmt gerekli:"; gofmt -l .; exit 1)

fmt:
	gofmt -w .

clean:
	rm -rf bin

## e2e — two checkouts against a bare remote, plus the global-config guard
e2e: build
	PATH="$(CURDIR)/bin:$$PATH" bash test/guard.sh

## suite — every end-to-end scenario (macOS: needs a login keychain)
suite: build
	@set -e; for s in e2e recover agent restore example hooks; do \
		printf '\033[1;36m== %s\033[0m\n' "$$s"; \
		ENVMOVE="$(CURDIR)/bin/envmove" bash "test/$$s.sh"; \
	done
	@bash test/guard.sh