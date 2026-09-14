# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# Framework Makefile. The shared targets (lint / sec / test / cover / build …)
# come from tools/Makefile.shared — the same fragment downstream apps include.
# This file adds the multi-module loops (root + core/) and the praetor
# governance gates so `make verify-all` is the one command every agent runs.

include tools/Makefile.shared

STANDARDSCTL ?= standardsctl
REUSE        ?= reuse

# Go modules gated by CI. Heavy/native sub-modules (hw/, media/, science/,
# web3/, …) build on demand and are not part of the default gate.
MODULES := . core

# Path-based gosec exclusions for the ROOT module, read from the same file CI
# uses (.github/workflows/ci.yml, job "Security (gosec)") so `make ci-all` and
# CI agree. core/ is scanned with no exclusions in both places. The path is
# resolved against this Makefile (not CURDIR: ci-all re-invokes it with
# -C core) and the value is expanded lazily, so the file is read only by the
# root _ci-module recipe. A `#` inside a function call is literal since GNU
# make 4.3; escaping it would hand grep a stray backslash (and a warning).
GOSEC_EXCLUDE_FILE  := $(dir $(realpath $(firstword $(MAKEFILE_LIST))))tools/gosec.exclude-rules
GOSEC_EXCLUDE_RULES  = $(shell grep -v -e '^#' -e '^$$' $(GOSEC_EXCLUDE_FILE) | paste -sd ';' -)

.PHONY: ci-all
ci-all: ## lint + sec + test in every gated module
	@for m in $(MODULES); do echo "==> $$m"; $(MAKE) -C $$m -f $(CURDIR)/Makefile _ci-module MOD=$$m || exit 1; done

.PHONY: _ci-module
_ci-module:
	$(GOLANGCI) run --config $(CURDIR)/tools/golangci.yml --timeout=20m ./...
	$(GOVULNCHECK) ./...
	$(GOSEC) -quiet -exclude-generated $(if $(filter .,$(MOD)),--exclude-rules="$(GOSEC_EXCLUDE_RULES)",) ./...
	$(GO) test -race -count=1 -timeout=10m ./...

.PHONY: build-all
build-all: ## go build + vet in every gated module
	@for m in $(MODULES); do echo "==> $$m"; (cd $$m && $(GO) build ./... && $(GO) vet ./...) || exit 1; done

.PHONY: tidy-all
tidy-all: ## go mod tidy in root, core, and every sub-module
	@for f in $$(find . -name go.mod -not -path './_audit/*' -not -path './.git/*'); do (cd $$(dirname $$f) && $(GO) mod tidy) || exit 1; done

.PHONY: reuse-lint
reuse-lint: ## REUSE / SPDX compliance (LICENSING.md)
	$(REUSE) lint

.PHONY: audit
audit: ## praetor HISS-16 governance audit
	$(STANDARDSCTL) audit

.PHONY: compile-context
compile-context: ## regenerate vendor agent-context files from AGENTS.md
	$(STANDARDSCTL) compile-context

.PHONY: compile-context-verify
compile-context-verify: ## assert vendor agent-context files match AGENTS.md
	$(STANDARDSCTL) compile-context --verify

.PHONY: capabilities-check
capabilities-check: ## capabilities.yaml ↔ tree drift guard
	$(GO) test -count=1 -run 'TestCapabilities' .

.PHONY: verify-all
verify-all: build-all ci-all capabilities-check compile-context-verify audit reuse-lint ## the universal verification gate
	@echo "All verification gates passed cleanly."
