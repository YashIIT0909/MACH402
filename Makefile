BIN      := bin/cleargate-node
GO_PKG   := ./agent/...
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -X main.version=$(VERSION)

.PHONY: help install smoke smoke-agent supported agent dev-node dev-tui cli dev-client \
        registry-db dev-registry dev-web typecheck vet test clean

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

install: ## install TS workspace dependencies
	pnpm install

supported: ## check the facilitator advertises hedera:testnet
	pnpm --filter @cleargate/smoke run supported

smoke: ## end-to-end payment test against testnet — run before every PR
	pnpm --filter @cleargate/smoke run smoke

smoke-agent: ## pay the local Go agent with the official TS client (cross-verification gate)
	pnpm --filter @cleargate/smoke exec tsx src/run.ts http://localhost:8402/v1/jobs

agent: ## build the cleargate-node binary
	cd agent && go build -ldflags "$(LDFLAGS)" -o ../$(BIN) ./cmd/cleargate-node

dev-node: agent ## run an agent locally, headless — what a real provider runs under systemd
	./$(BIN) serve

dev-tui: agent ## run an agent locally with the provider dashboard
	./$(BIN) tui

cli: ## renter CLI, run from the repo root: make cli ARGS="quote -n http://localhost:8402"
	pnpm exec cleargate $(ARGS)

dev-client: ## show the renter CLI's help
	pnpm exec cleargate --help

registry-db: ## start the registry's Postgres in docker
	cd registry && docker compose up -d

dev-registry: ## run the discovery registry on :4400
	pnpm --filter @cleargate/registry run start

dev-web: ## run the website on :3000
	pnpm --filter @cleargate/web run dev

typecheck: ## typecheck every TS package
	pnpm -r typecheck

vet: ## go vet the agent
	cd agent && go vet ./...

test: ## go tests
	cd agent && go test ./...

clean:
	rm -rf bin dist
