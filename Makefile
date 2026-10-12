.PHONY: build test check install release
VERSION ?= 1.0.1
build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o bin/codex-switch ./cmd/codex-switch
test:
	go test -race ./...
check:
	go vet ./...
install: build
	install -d "$(HOME)/.local/bin"
	install -m 755 bin/codex-switch "$(HOME)/.local/bin/codex-switch"

release:
	python3 scripts/release.py --version "$(VERSION)"
