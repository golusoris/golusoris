# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# Framework Makefile. The shared targets (lint / sec / test / cover / build …)
# come from tools/Makefile.shared — the same fragment downstream apps include.
# This file adds all-module orchestration and the praetor governance gates so
# `make verify-all` is the one command every agent runs.

include tools/Makefile.shared
include tools/tool-versions.env

# Praetor ships one binary under two names; resolve whichever is installed.
PRAETORCTL ?= $(shell command -v praetorctl 2>/dev/null || command -v standardsctl 2>/dev/null || echo praetorctl)
GOFUMPT      ?= gofumpt

# One discovery path owns the root module and every nested Go module. It also
# drives the hosted shard sweep, so adding a go.mod cannot silently miss CI.
GO_MODULES := scripts/ci/go-modules.sh

# The root module uses .gosec.json. Nested modules use the full rule set.
# scripts/ci/go-modules.sh owns that distinction for local and hosted gates.

# Dependencies with a docs/upstream/ snapshot, by the module that pins them.
UPSTREAM_ROOT_MODULES := go.uber.org/fx github.com/jackc/pgx/v5 github.com/ogen-go/ogen github.com/riverqueue/river  github.com/maypok86/otter/v2 github.com/redis/rueidis github.com/casbin/casbin/v3 github.com/go-webauthn/webauthn  go.opentelemetry.io/otel github.com/golang-migrate/migrate/v4 k8s.io/client-go github.com/go-chi/chi/v5  github.com/yuin/goldmark/v2 github.com/prometheus/client_golang github.com/testcontainers/testcontainers-go
UPSTREAM_CORE_MODULES := github.com/knadh/koanf/v2 github.com/go-playground/validator/v10 github.com/jonboulle/clockwork


.PHONY: modules-list
modules-list: ## list every discovered Go module
	@$(GO_MODULES) list

.PHONY: lint-all
lint-all: ## golangci-lint every Go module
	@$(GO_MODULES) lint

.PHONY: fmt-check-all
fmt-check-all: ## gofumpt every tracked Go file with the repository pin
	@bash scripts/ci/check-gofumpt.sh

.PHONY: vuln-all
vuln-all: ## govulncheck every Go module
	@$(GO_MODULES) vuln

.PHONY: gosec-all
gosec-all: ## gosec every Go module
	@$(GO_MODULES) gosec

.PHONY: test-all
test-all: ## race-test every Go module; retain primary and per-module coverage privately
	@GO_COVERAGE_DIR=.workingdir/cache/go-module-coverage $(GO_MODULES) test

.PHONY: go-modules-test
go-modules-test: ## verify module selection and coverage aggregation contracts
	@bash scripts/ci/go-modules_test.sh

.PHONY: portability-test
portability-test: ## HISS-21 build, vet, and short-test every Go module on this host
	@python3 -B scripts/ci/portability_test.py
	@python3 -B scripts/ci/portability.py

.PHONY: go-apidiff-test
go-apidiff-test: ## verify multi-module API compatibility coverage and failure policy
	@bash scripts/ci/go-apidiff_test.sh

.PHONY: ci-all
ci-all: lint-all vuln-all gosec-all test-all ## lint + security + race/coverage in every Go module

.PHONY: build-all
build-all: ## go build + vet every Go module
	@$(GO_MODULES) build

.PHONY: tidy-all
tidy-all: ## go mod tidy in root, core, and every sub-module
	@for m in $$($(GO_MODULES) list); do (cd $$m && $(GO) mod tidy) || exit 1; done

.PHONY: tidy-check-all
tidy-check-all: ## assert go mod tidy would not change any Go module
	@$(GO_MODULES) tidy

.PHONY: fix-check-all
fix-check-all: ## assert go fix would not modernize any Go module
	@$(GO_MODULES) fix

.PHONY: tools-bootstrap
tools-bootstrap: ## install repository-managed developer tools at exact versions
	$(GO) install mvdan.cc/gofumpt@$(GOFUMPT_VERSION)
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	$(GO) install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)
	$(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	$(GO) install github.com/joelanford/go-apidiff@$(GO_COMPAT_TOOL_VERSION)
	$(GO) install github.com/evilmartians/lefthook/v2@$(LEFTHOOK_VERSION)
	$(GO) install github.com/daixiang0/gci@$(GCI_VERSION)
	$(GO) install github.com/vektra/mockery/v3@$(MOCKERY_VERSION)
	$(GO) install github.com/air-verse/air@$(AIR_VERSION)
	$(GO) install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
	$(GO) install github.com/ogen-go/ogen/cmd/ogen@$(OGEN_VERSION)
	$(GO) install github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)
	$(GO) install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)
	$(GO) install github.com/avito-tech/go-mutesting/cmd/go-mutesting@$(GO_MUTESTING_VERSION)
	$(GO) install helm.sh/helm/v4/cmd/helm@$(HELM_VERSION)
	$(GO) install github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)

.PHONY: ci-policy-test
ci-policy-test: go-apidiff-test tiny-trainer-publish-policy-test ## verify required-job fail-closed and not-applicable semantics
	@bash scripts/ci/check-required-jobs_test.sh
	@bash scripts/ci/tool-versions_test.sh
	@bash scripts/ci/release-workflows-policy_test.sh
	@bash scripts/ci/generation-policy_test.sh
	@bash scripts/ci/helm-chart_test.sh
	@bash scripts/ci/trivy-policy_test.sh
	@bash scripts/ci/govulncheck-policy_test.sh
	@bash scripts/ci/semgrep-policy_test.sh
	@bash scripts/ci/shellcheck-policy_test.sh
	@bash scripts/ci/actionlint-policy_test.sh
	@bash scripts/ci/terraform-policy_test.sh
	@bash scripts/ci/mkdocs-build-policy_test.sh
	@bash scripts/ci/allocation-budget-policy_test.sh
	@bash scripts/ci/kubeconform-policy_test.sh
	@bash scripts/ci/reuse-lint-policy_test.sh
	@bash scripts/ci/python-lint-policy_test.sh
	@bash scripts/ci/python-tests-policy_test.sh
	@bash scripts/ci/c-quality-policy_test.sh
	@python3 -B scripts/ci/renovate-policy-test.py
	@python3 -B scripts/ci/block_evasion_hook_test.py

.PHONY: python-lint
python-lint: ## lint every repository-owned Python source with immutable Ruff
	@bash scripts/ci/python-lint.sh

.PHONY: tiny-trainer-locks-check
tiny-trainer-locks-check: ## verify tiny trainer hash locks against direct requirements
	@bash scripts/ci/tiny-trainer-locks.sh --check

.PHONY: tiny-trainer-vuln
tiny-trainer-vuln: ## audit tiny trainer hash locks for known Python vulnerabilities
	@bash scripts/ci/tiny-trainer-locks.sh --audit

.PHONY: tiny-trainer-test
tiny-trainer-test: tiny-trainer-locks-check tiny-trainer-vuln tiny-trainer-publish-policy-test ## execute tiny trainer contracts and vulnerability audit
	@PYTHONDONTWRITEBYTECODE=1 PYTHONPATH=ai/tiny python3 -B -m unittest discover -s ai/tiny/trainers -p 'test_*.py' -v

.PHONY: tiny-trainer-publish-policy-test
tiny-trainer-publish-policy-test: ## verify fail-closed trainer publication and supply-chain identity
	@bash scripts/ci/tiny-trainer-publish-policy_test.sh

.PHONY: python-test python-tests
python-test: tiny-trainer-test ## execute the required Python regression suites
	@bash scripts/ci/python-tests.sh

python-tests: python-test ## alias for the Python regression gate

.PHONY: c-quality
c-quality: ## compile and clang-tidy every repository-owned C source
	@bash scripts/ci/c-quality.sh

.PHONY: reuse-lint
reuse-lint: ## REUSE / SPDX compliance (LICENSING.md)
	@bash scripts/ci/reuse-lint.sh

.PHONY: audit
audit: ## praetor HISS-21 lattice governance audit
	$(PRAETORCTL) audit

.PHONY: compile-context
compile-context: ## regenerate vendor agent-context files from AGENTS.md
	$(PRAETORCTL) compile-context

.PHONY: compile-context-verify
compile-context-verify: ## assert vendor agent-context files match AGENTS.md
	$(PRAETORCTL) compile-context --verify

.PHONY: caveman-context
caveman-context: ## lint every canonical agent-facing context surface
	@git ls-files -z --cached --others --exclude-standard -- '*AGENTS.md' '.agents/agents/*.md' '.agents/skills/*/SKILL.md' '.claude/skills/*.md' '.paperclip/harness.json' '.paperclip/rules.md' ':(exclude).claude/skills/**/README.md' | xargs -0 $(PRAETORCTL) caveman check

.PHONY: text-register-policy
text-register-policy: ## verify required register skills and plugin projections
	@bash scripts/ci/text-register-policy.sh .
	@bash scripts/ci/text-register-policy_test.sh

.PHONY: semgrep-scan
semgrep-scan: ## scan every repository-owned Go source with pinned custom Semgrep rules
	@bash scripts/ci/semgrep-scan.sh

.PHONY: shellcheck
shellcheck: ## lint every repository-owned shell script with the pinned ShellCheck
	@bash scripts/ci/shellcheck.sh

.PHONY: actionlint
actionlint: ## lint GitHub and Gitea workflow YAML with the pinned actionlint
	@bash scripts/ci/actionlint.sh

.PHONY: terraform-validate
terraform-validate: ## format and validate every Terraform module with locked providers
	@bash scripts/ci/terraform-validate.sh

.PHONY: mkdocs-build
mkdocs-build: ## build the documentation strictly with the digest-pinned image
	@bash scripts/ci/mkdocs-build.sh

.PHONY: allocation-budget
allocation-budget: ## enforce measured hot-path allocation budgets
	@bash scripts/ci/allocation-budget.sh

.PHONY: kubeconform
kubeconform: ## validate static and rendered Kubernetes manifests against pinned schemas
	@bash scripts/ci/kubeconform.sh

.PHONY: capabilities-check
capabilities-check: ## capabilities.yaml ↔ tree drift guard
	$(GO) test -count=1 -run 'TestCapabilities' .

.PHONY: hiss-fixtures
hiss-fixtures: ## HISS-20 replay: .semgrep.yml vs the .config/hiss/testdata corpus
	bash scripts/hiss/semgrep-fixtures.sh

.PHONY: trivy-scan
trivy-scan: ## pinned dependency, secret, and IaC security gate
	bash scripts/ci/trivy-scan.sh

.PHONY: dedupe-scan
dedupe-scan: ## praetor HISS-19 duplicate-block gate (Reuse Before Writing)
	$(PRAETORCTL) dedupe scan .

.PHONY: hiss-coverage
hiss-coverage: ## praetor HISS-20 enforcement-coverage catalogue (.config/hiss/coverage.yaml)
	$(PRAETORCTL) hiss coverage --verify

.PHONY: docs-upstream
docs-upstream: ## print the recipe for refreshing a docs/upstream/ snapshot (see docs/upstream/README.md)
	@echo "docs/upstream refresh recipe (one dependency at a time):"
	@echo "  1. resolve the new pin: go list -m -f '{{.Version}}' <module>   (run in the module that requires it: . or core/)"
	@echo "  2. re-fetch the upstream README / API docs at that tag into docs/upstream/<name>/"
	@echo "  3. update the pin and authority in docs/upstream/README.md"
	@echo "  4. run make docs-upstream-verify, then commit snapshot + dependency bump together"
	@echo "Current pins:"
	@for m in $(UPSTREAM_ROOT_MODULES); do printf '  %-45s %s\n' "$$m" "$$($(GO) list -m -f '{{.Version}}' $$m)"; done
	@for m in $(UPSTREAM_CORE_MODULES); do printf '  %-45s %s (core/)\n' "$$m" "$$(cd core && $(GO) list -m -f '{{.Version}}' $$m)"; done

.PHONY: docs-upstream-verify
docs-upstream-verify: ## verify upstream catalogue, authorities, and snapshots
	bash scripts/verify-upstream-pins.sh

.PHONY: verify-all
verify-all: fmt-check-all tidy-check-all fix-check-all ci-policy-test go-modules-test portability-test build-all ci-all python-lint python-test c-quality allocation-budget trivy-scan capabilities-check caveman-context text-register-policy mkdocs-build semgrep-scan shellcheck actionlint terraform-validate kubeconform compile-context-verify audit dedupe-scan hiss-fixtures hiss-coverage docs-upstream-verify reuse-lint ## the universal verification gate
	@echo "All verification gates passed cleanly."

# BEGIN praetor documentation gate
.PHONY: docs-lint docs-figures
verify-all: docs-lint docs-figures
docs-lint:
	@node tools/markdownlint/verify.mjs
docs-figures:
	@node tools/figures/build.mjs check
	@node tools/figures/build.mjs sources
# END praetor documentation gate
