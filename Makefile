GO ?= go
VERSION ?= $(shell cat VERSION)

.PHONY: build test release
build:
	@mkdir -p bin
	@set -e; temp=$$(mktemp -d bin/.build.XXXXXX); \
		trap 'rm -rf "$$temp"' EXIT HUP INT TERM; \
		CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" \
			-o "$$temp/agent-transcript" ./cmd/agent-transcript; \
		mv "$$temp/agent-transcript" bin/agent-transcript

test:
	$(GO) test ./...

release:
	@mkdir -p dist
	@set -e; \
		temp=$$(mktemp -d dist/.release.XXXXXX); \
		trap 'rm -rf "$$temp"' EXIT HUP INT TERM; \
		artifacts='agent-transcript-darwin-arm64 agent-transcript-darwin-amd64 agent-transcript-linux-arm64 agent-transcript-linux-amd64'; \
		for os in darwin linux; do \
			for arch in arm64 amd64; do \
				name="agent-transcript-$$os-$$arch"; \
				CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath \
					-ldflags "-s -w -X main.version=$(VERSION)" \
					-o "$$temp/$$name" ./cmd/agent-transcript; \
			done; \
		done; \
		if command -v sha256sum >/dev/null 2>&1; then \
			(cd "$$temp" && sha256sum $$artifacts) > "$$temp/checksums.txt"; \
		elif command -v shasum >/dev/null 2>&1; then \
			(cd "$$temp" && shasum -a 256 $$artifacts) > "$$temp/checksums.txt"; \
		else \
			echo "release requires sha256sum or shasum" >&2; \
			exit 1; \
		fi; \
		for name in $$artifacts checksums.txt; do \
			mv "$$temp/$$name" "dist/$$name"; \
		done
