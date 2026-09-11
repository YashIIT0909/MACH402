BIN      := bin/cleargate-node
GO_PKG   := ./agent/...
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -X main.version=$(VERSION)

# The lease runtime a renter gets a shell on, and the proxy that is its only way
# out to the network. Built locally rather than pulled: standing up a container
# registry is not a prerequisite for a provider running the install one-liner,
# and a proxy every byte passes through should be built from source you have.
#
# The base is chosen by what the machine actually has, not by the provider
# remembering a flag. A box with the nvidia runtime gets the CUDA base; without
# it, passing a GPU into a container that has no CUDA toolkit produces a lease
# that reports a card and cannot compute on it — which is worse than selling CPU
# honestly. Override LEASE_BASE_IMAGE to pin a different one.
LEASE_IMAGE      ?= cleargate/lease-runtime:dev
LEASE_EGRESS     ?= cleargate/lease-egress:dev
LEASE_CUDA_BASE  ?= pytorch/pytorch:2.4.1-cuda12.1-cudnn9-runtime
LEASE_CPU_BASE   ?= python:3.11-slim
LEASE_BASE_IMAGE ?= $(shell docker info --format '{{if index .Runtimes "nvidia"}}$(LEASE_CUDA_BASE){{else}}$(LEASE_CPU_BASE){{end}}' 2>/dev/null || echo $(LEASE_CPU_BASE))

# Extra packages baked into the lease runtime, on top of whichever base is used.
#
# Note what this cannot fix: TensorFlow does not get the GPU on the PyTorch CUDA
# base, in any form measured — `tensorflow`, `tensorflow[and-cuda]`, and an
# isolated virtualenv all end at "Cannot dlopen some GPU libraries" and fall
# back to CPU, because TF's CUDA wheels and torch's pins cannot coexist. A
# provider expecting TensorFlow renters changes the base, not the packages:
#
#   make lease-image LEASE_BASE_IMAGE=tensorflow/tensorflow:2.17.0-gpu
LEASE_EXTRA_PIP  ?=

.PHONY: help install smoke smoke-agent supported agent lease-image dev-node dev-tui cli dev-client \
        registry-db dev-registry dev-registry-sample dev-web typecheck vet test clean

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

lease-image: ## build the lease runtime and its egress proxy (needed before a node can sell leases)
	@echo "==> lease base: $(LEASE_BASE_IMAGE)"
	docker build --build-arg BASE_IMAGE=$(LEASE_BASE_IMAGE) --build-arg EXTRA_PIP="$(LEASE_EXTRA_PIP)" \
		-t $(LEASE_IMAGE) agent/lease-image
	docker build -t $(LEASE_EGRESS) agent/lease-image/egress

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

dev-registry-sample: ## serve fixture nodes on :4400 for website work — no Postgres, no heartbeats
	node scripts/sample-registry.mjs

dev-web: ## run the website on :3000
	pnpm --filter @cleargate/web run dev

typecheck: ## typecheck every TS package
	pnpm -r typecheck

vet: ## go vet the agent
	cd agent && go vet ./...
	cd agent/lease-image/egress && go vet ./...

test: ## go tests
	cd agent && go test ./...
	# Its own module, so ./... above does not reach it.
	cd agent/lease-image/egress && go test ./...

clean:
	rm -rf bin dist
