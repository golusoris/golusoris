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

# .gosec.json (repo root) is the ONE gosec config shared with CI
# (.github/workflows/ci.yml, job "Security (gosec)") and praetor's own
# `gosec -conf .gosec.json` gate, so `make ci-all`, CI, and the governance
# gate cannot drift. core/ is scanned with no config (full rule set) in all
# three places; the ROOT module's former path-scoped exclusions are now
# targeted inline `// #nosec Gxxx -- reason` comments at each flagged line.
GOSEC_CONFIG := $(CURDIR)/.gosec.json

# Dependencies with a docs/upstream/ snapshot, by the module that pins them.
UPSTREAM_ROOT_MODULES := go.uber.org/fx github.com/jackc/pgx/v5 github.com/ogen-go/ogen github.com/riverqueue/river  github.com/maypok86/otter/v2 github.com/redis/rueidis github.com/casbin/casbin/v3 github.com/go-webauthn/webauthn  go.opentelemetry.io/otel github.com/golang-migrate/migrate/v4 k8s.io/client-go github.com/go-chi/chi/v5  github.com/yuin/goldmark github.com/prometheus/client_golang github.com/testcontainers/testcontainers-go
UPSTREAM_CORE_MODULES := github.com/knadh/koanf/v2 github.com/go-playground/validator/v10 github.com/jonboulle/clockwork


.PHONY: ci-all
ci-all: ## lint + sec + test in every gated module
	@for m in $(MODULES); do echo "==> $$m"; $(MAKE) -C $$m -f $(CURDIR)/Makefile _ci-module MOD=$$m || exit 1; done

.PHONY: _ci-module
_ci-module:
	$(GOLANGCI) run --config $(CURDIR)/.golangci.yml --timeout=30m ./...
	$(GOVULNCHECK) ./...
	$(GOSEC) -quiet -exclude-generated $(if $(filter .,$(MOD)),-conf $(GOSEC_CONFIG),) ./...
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
audit: ## praetor HISS-20 lattice governance audit
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

.PHONY: hiss-fixtures
hiss-fixtures: ## HISS-20 replay: .semgrep.yml vs the .config/hiss/testdata corpus
	bash scripts/hiss/semgrep-fixtures.sh

.PHONY: dedupe-scan
dedupe-scan: ## praetor HISS-19 duplicate-block gate (Reuse Before Writing)
	$(STANDARDSCTL) dedupe scan .

.PHONY: hiss-coverage
hiss-coverage: ## praetor HISS-20 enforcement-coverage catalogue (.config/hiss/coverage.yaml)
	$(STANDARDSCTL) hiss coverage --verify

.PHONY: docs-upstream
docs-upstream: ## print the recipe for refreshing a docs/upstream/ snapshot (see docs/upstream/README.md)
	@echo "docs/upstream refresh recipe (one dependency at a time):"
	@echo "  1. resolve the new pin: go list -m -f '{{.Version}}' <module>   (run in the module that requires it: . or core/)"
	@echo "  2. re-fetch the upstream README / API docs at that tag into docs/upstream/<name>/"
	@echo "  3. update the pin in docs/upstream/README.md and the table in AGENTS.md"
	@echo "  4. commit the snapshot diff together with the go.mod bump"
	@echo "Current pins:"
	@for m in $(UPSTREAM_ROOT_MODULES); do printf '  %-45s %s\n' "$$m" "$$($(GO) list -m -f '{{.Version}}' $$m)"; done
	@for m in $(UPSTREAM_CORE_MODULES); do printf '  %-45s %s (core/)\n' "$$m" "$$(cd core && $(GO) list -m -f '{{.Version}}' $$m)"; done

.PHONY: verify-all
verify-all: build-all ci-all capabilities-check compile-context-verify audit dedupe-scan hiss-fixtures hiss-coverage reuse-lint ## the universal verification gate
	@echo "All verification gates passed cleanly."
