.PHONY: all build check e2e fuzz help

FUZZTIME ?= 60s

all: check build ## Run the checks and build the binary

build: ## Build the binary to result/bin/aeg
	nix build

check: ## Run the linters and the unit tests
	nix flake check

e2e: ## Run the end-to-end tests in a Lima VM, with and without srt
	nix develop --command ./e2e/run.sh

fuzz: ## Fuzz the route policy for FUZZTIME (default 60s)
	nix develop --command go test -run '^$$' -fuzz FuzzRouteAllows -fuzztime $(FUZZTIME) ./internal/proxy

help: ## Show this help
	@awk -F ':.*## ' '/^[a-z0-9]+:.*## / { printf "  %-8s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
