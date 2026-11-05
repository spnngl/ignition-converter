GO ?= go
# The release's version, printed by --version; release.yml sets it.
VERSION ?= dev
export CGO_ENABLED = 0

LDFLAGS = -s -w -X main.version=$(VERSION)
PKG = ./cmd/ignition-converter

# Every check CI runs; lint includes gofmt.
.PHONY: check
check: lint tidy-check vet test build

.PHONY: build
build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o ignition-converter $(PKG)

# The release's assets: a static binary per architecture.
.PHONY: dist
dist:
	for arch in amd64 arm64; do \
		GOOS=linux GOARCH=$$arch $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
			-o dist/ignition-converter_$(VERSION)_linux_$$arch $(PKG) || exit 1; \
	done

.PHONY: test
test:
	CGO_ENABLED=1 $(GO) test -race ./...

.PHONY: tidy-check
tidy-check:
	$(GO) mod tidy -diff

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: lint
lint:
	golangci-lint run
	shellcheck e2e.sh

.PHONY: clean
clean:
	rm -rf ignition-converter dist
