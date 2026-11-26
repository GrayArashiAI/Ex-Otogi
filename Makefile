SHELL := /bin/sh
GO ?= go
LINT := $(GO) tool -modfile=tools/go.mod golangci-lint
SQLC := $(GO) tool -modfile=tools/go.mod sqlc
FUZZTIME ?= 1x

# Test deployment (scripts/deploy.sh); override on the command line.
DEPLOY_HOST ?= misaka-lax01
DEPLOY_DIR ?= /srv/ex-otogi-next
DEPLOY_SERVICE ?= ex-otogi-next
DEPLOY_READY_URL ?= http://127.0.0.1:8383/readyz
DEPLOY_ENV := DEPLOY_HOST=$(DEPLOY_HOST) DEPLOY_DIR=$(DEPLOY_DIR) DEPLOY_SERVICE=$(DEPLOY_SERVICE) DEPLOY_READY_URL=$(DEPLOY_READY_URL)

.PHONY: generate generated web web-lint web-typecheck web-test format lint test test-race test-fuzz-smoke smoke telegram-smoke quality deploy deploy-rollback

# Go code for the SQL queries (sqlc.yaml), and the panel's API types from
# the OpenAPI document.
generate:
	$(SQLC) generate
	cd web && pnpm gen:api

# Generated code matches its sources.
generated:
	$(SQLC) diff
	@$(GO) run ./cmd/otogi openapi | diff -q - web/openapi.json >/dev/null || { echo "web/openapi.json is stale: run make generate"; exit 1; }

# The control panel, built into web/dist for the binary to embed.
web:
	cd web && pnpm install --frozen-lockfile && find dist -mindepth 1 ! -name .gitkeep -delete && pnpm build

web-lint:
	cd web && pnpm install --frozen-lockfile && pnpm lint

web-typecheck:
	cd web && pnpm install --frozen-lockfile && pnpm typecheck

web-test:
	cd web && pnpm test

format:
	$(LINT) fmt

# Formatting, static analysis, Godoc, and import boundaries (depguard).
lint:
	$(LINT) run ./...

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

test-fuzz-smoke:
	$(GO) test -run '^$$' -fuzz '^FuzzConvertEntitiesUTF16$$' -fuzztime=$(FUZZTIME) ./internal/adapter/telegram/mapper
	$(GO) test -run '^$$' -fuzz '^FuzzFormat$$' -fuzztime=$(FUZZTIME) ./pkg/markdown
	$(GO) test -run '^$$' -fuzz '^FuzzGIFDecoded$$' -fuzztime=$(FUZZTIME) ./modules/media
	$(GO) test -run '^$$' -fuzz '^FuzzComposite$$' -fuzztime=$(FUZZTIME) ./pkg/webpanim

smoke:
	@output="$$($(GO) run ./cmd/otogi smoke)" && [ "$$output" = "echo: hello" ] || { printf 'smoke: got %s\n' "$$output"; exit 1; }

# Opt-in live check against Telegram; never part of quality.
telegram-smoke:
	$(GO) run ./cmd/telegram-smoke

quality: generated lint web-lint web-typecheck web-test web test-race test-fuzz-smoke smoke

# Deploy the current checkout to the test host, or restore its previous binary.
deploy: web
	$(DEPLOY_ENV) ./scripts/deploy.sh

deploy-rollback:
	$(DEPLOY_ENV) ./scripts/deploy.sh rollback
