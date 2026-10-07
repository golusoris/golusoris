<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# golusoris — coding & assurance contract

> This is the framework's foundational contract (§2 of the design plan).
> Every package in the framework — and every app built on top — is expected to follow these rules.
> Deviations require a PR comment justifying the exception.

---

## 2.1 Coding rules — Power of 10, Go-adapted

NASA/JPL's _The Power of 10: Rules for Developing Safety-Critical Code_ (Gerard J. Holzmann), adapted to Go.

Reference: <https://spinroot.com/gerard/pdf/P10.pdf>

| # | Original rule | Go adaptation |
|---|---|---|
| 1 | Restrict to simple control flow; no `goto`, `setjmp`, `longjmp`, recursion. | No `goto` or recursion; call graphs remain acyclic, including tree walks and parsers. Panic/recover only at trust boundaries (fx lifecycle, `http.Handler` recover, `ogenkit.RecoverMiddleware`). |
| 2 | All loops must have a fixed upper bound, statically provable. | Every `for` that isn't `for range` over a bounded collection must have a bound visible in the loop head (counter, max attempts, ctx deadline). Long-running loops `select` on `ctx.Done()`. |
| 3 | No dynamic memory allocation after initialization. | Soft: hot paths preallocate (`make([]T, 0, cap)`), reuse `sync.Pool` where profiles show churn. Startup-phase allocation is free; steady-state is watched. |
| 4 | No function longer than ~60 lines (single printed page). | `funlen` 60 lines / 50 statements + `gocognit` ≤ 15. Refactor when flagged; don't silence the linter. |
| 5 | ≥2 assertions per function on average; side-effect-free. | Table-driven tests + contract checks at API boundaries (validator, ogen decoders, `gerr.Wrap`). Target ≥2 assertions per test per function. `require`/`assert` via testify; no `panic(msg)` in non-test code. |
| 6 | Declare data at smallest possible scope. | Prefer block-scoped `:=`. Struct fields unexported unless explicitly part of the API. Package-level `var` only for singletons + sentinels. |
| 7 | Check every return value; check every parameter. | `errcheck` + `wrapcheck` + `nilerr` on. Errors wrapped with context via `gerr.Wrap` or `fmt.Errorf("pkg: op: %w", err)`. Exported funcs validate inputs at the boundary. |
| 8 | Preprocessor limited to simple macros. | N/A in Go. `go generate` directives stay simple + declarative. No build tags for behaviour switches in production paths. |
| 9 | Pointers restricted; one dereference per expression; no function pointers. | Soft: no multi-hop `*foo.bar.baz` chains. Small interfaces (≤5 methods) only, defined where consumed. No `unsafe` outside explicitly-reviewed performance code. |
| 10 | Compile at most pedantic warning level. | `.golangci.yml` gates Go; Praetor's `tools/markdownlint/markdownlint-cli2.yaml` gates public Markdown. Every merged commit has no unreviewed lint, gosec, or reachable govulncheck finding and is race-green. An exact vulnerability exception must bind the module checksum and patched-source hash and fail closed on drift. `//nolint` requires a justification comment and PR review. |

**Hard gates** (CI blocks on violation): rules 1, 2, 4, 7, 10.  
**Guidance** (cite rule ID in review): rules 3, 5, 6, 9.  
Rule 8 is N/A in Go. HISS-09 separately hard-gates a `// SAFETY:` proof for
every `unsafe` block or pointer cast; only remaining Rule 9 pointer-shape advice
is guidance.

---

## 2.2 Secure coding — SEI CERT for Go

The Go port of SEI CERT C. Concrete rules covering crypto, error handling, input validation, concurrency, memory, and I/O.

Reference: <https://wiki.sei.cmu.edu/confluence/display/go/>

`gosec` and `staticcheck` already enforce the majority. Reviewers cite rule IDs (e.g. `MEM30-Go`) on deviations.

---

## 2.3 Go style — Google Go Style Guide (canonical)

Reference: <https://google.github.io/styleguide/go/>

Effective Go and Go Code Review Comments are secondary references. Specific commitments:

- **Naming** — per Google style: short, idiomatic, receiver-name conventions.
- **Comments** — doc comments as full sentences; package-level comment on every package.
- **Decisions log** — when the style guide allows multiple valid options, the framework picks one in `docs/adr/` (§2.4) and sticks to it.

---

## 2.4 Architecture decisions — C4 + ADRs

### C4 model

Simon Brown's C4 model for architecture diagrams: Context → Container → Component → Code.

- Diagrams kept in `docs/architecture/` as PlantUML `.puml` files using [C4-PlantUML](https://github.com/plantuml-stdlib/C4-PlantUML) macros.
- L4 (code-level) is intentionally omitted — godoc + per-package `AGENTS.md` cover it.

Reference: <https://c4model.com/>

### Architecture Decision Records

Michael Nygard format. One ADR per significant decision — pinned dependencies, interface choices, cross-cutting conventions.

- ADRs supersede rather than edit: old ADRs stay, a new one overrides with `Supersedes: ADR-NNNN`.
- Template: `docs/adr/0000-template.md`.
- Index + numbering policy: `docs/adr/README.md`.
- Use next sequential ADR number. ADR-0001 through ADR-0007 are retroactive
  backfills; later records describe decisions made during development.

Reference: <https://github.com/joelparkerhenderson/architecture-decision-record>

**Backfilled ADRs (all `Accepted`):**

| ADR | Decision |
|---|---|
| ADR-0001 | fx over wire for dependency injection |
| ADR-0002 | koanf over viper for configuration |
| ADR-0003 | slog as the canonical logger interface |
| ADR-0004 | ogen over oapi-codegen for OpenAPI server generation |
| ADR-0005 | river over asynq for background jobs |
| ADR-0006 | pluggable leader-election (k8s Lease + pg advisory lock) |
| ADR-0007 | RFC 9457 Problem Details as the standard error body |

---

## 2.5 Security + supply-chain standards

golusoris provides engineering controls and reusable primitives; it does not
certify an application or make a blanket legal-compliance claim. This
repository does not ship a control-mapping catalogue. Application owners must
identify applicable requirements, test the assembled system, and retain their
own evidence.

| Reference | Evidence shipped here | Boundary |
|---|---|---|
| **praetor HISS-21 lattice** | `AGENTS.md` names each invariant and its repository gate; `praetorctl audit` and `make verify-all` run the declared checks | Engineering policy, not a certification |
| **SLSA provenance model** | release workflows produce SBOMs, cosign signatures, and build-provenance attestations with `actions/attest-build-provenance` | No SLSA level or independent conformance is claimed |
| **OWASP ASVS** | auth, input-validation, upload-safety, and HTTP-security modules provide reusable controls; CI runs gosec, govulncheck, Semgrep, and CodeQL | No ASVS verification or ZAP scan is bundled; applications own control mapping and dynamic testing |
| **NIST SSDF / OpenSSF Scorecard** | reviewed changes, dependency automation, secret scanning, signed releases, and an on-demand/reusable Scorecard workflow | Scorecard is not automatic and does not establish SSDF conformance |
| **EU CRA and coordinated disclosure** | release SBOMs and `SECURITY.md` provide technical inputs for vulnerability handling | Product classification, reporting duties, and legal compliance remain with the distributor |
| **NIS2, BSI, NCSC, ENISA, GDPR, EU AI Act** | framework modules can support app-specific controls such as structured logs, tenancy, audit events, secrets, and model access | No mappings or compliance guarantees ship. Logs do not automatically redact PII; AI modules do not provide prompt/output audit or human override |

References:

- SLSA: <https://slsa.dev/>
- OWASP ASVS: <https://owasp.org/www-project-application-security-verification-standard/>
- NIST SSDF: <https://csrc.nist.gov/Projects/ssdf>
- EU CRA: <https://digital-strategy.ec.europa.eu/en/policies/cyber-resilience-act>
- NIS2: <https://eur-lex.europa.eu/eli/dir/2022/2555>
- BSI IT-Grundschutz: <https://www.bsi.bund.de/EN/Topics/ITGrundschutz>
- BSI C5: <https://www.bsi.bund.de/EN/Topics/CloudComputing/ComplianceControlsCatalogue>
- UK NCSC: <https://www.ncsc.gov.uk/collection/developers-collection>
- ENISA: <https://www.enisa.europa.eu/>
- GDPR: <https://gdpr-info.eu/>
- EU AI Act: <https://artificialintelligenceact.eu/>

---

## 2.6 Wire protocols + API standards

| Standard | Status | Where it's enforced |
|---|---|---|
| **RFC 9457 Problem Details for HTTP** | Adopted | `ogenkit` error handler emits `application/problem+json` with `type`/`title`/`status`/`detail`/`instance` |
| **RFC 9110 HTTP Semantics** | Adopted | chi router + `httpx/middleware` follow status-code semantics (4xx client-fault, 5xx server-fault, 3xx redirects, 1xx expect-continue) |
| **OpenAPI 3.1** | Pinned | ogen generates from 3.1 specs; apps' `openapi.yaml` lints via spectral (`tools/spectral.yaml`) |
| **JSON Schema 2020-12** | Pinned | santhosh-tekuri/jsonschema for external-schema validation; ogen-generated types use matching semantics |
| **OpenTelemetry Semantic Conventions v1.26** | Pinned | `go.opentelemetry.io/otel/semconv/v1.26.0` for span/metric attribute names (`service.*`, `http.*`, `db.*`, `messaging.*`) |
| **RFC 7519 JWT** | Adopted | `auth/jwt/` uses `golang-jwt/jwt/v5` |
| **RFC 6749/6750 OAuth 2.0 + Bearer** | Adopted | `auth/oidc/`, `auth/oauth2server/` |
| **RFC 7636 PKCE** | Adopted | Default in `auth/oidc/` client flows |
| **WebAuthn Level 3** | Adopted | `auth/passkeys/` via go-webauthn |
| **RFC 8058 One-click Unsubscribe** | Adopted | `notify/unsub/` |
| **RFC 6238 TOTP** | Adopted | `auth/passkeys/` (MFA) |

---

## 2.7 Tooling + formatting

| Tool / Standard | Enforcement |
|---|---|
| **EditorConfig** | `.editorconfig` at repo root; tabs/spaces/line endings consistent across editors |
| **gofumpt** | Stricter gofmt — standalone v0.12.0 pin in `tools/tool-versions.env`, enforced by hooks and CI |
| **gci** | Grouped imports: standard / external / `prefix(github.com/golusoris/golusoris)` |
| **golines** | Line-length cap at 120 chars; long lines broken at safe points |
| **Conventional Commits 1.0** | CI PR-title check; release-please reads commit history |
| **Semantic Versioning 2.0** | release-please prepares version metadata; validated immutable tags are pushed on [GitHub](https://github.com/golusoris/golusoris), the public forge for merges, tags, and releases (the `.gitea/` workflows are an internal mirror lane that fast-forwards validated refs from Gitea to GitHub); breaking changes force major bump via `!` / `BREAKING CHANGE:` |
| **Keep a Changelog 1.1** | `CHANGELOG.md` format (auto-generated by release-please) |
| **Trunk-Based Development** | Single `main` branch; no long-lived release branches; release-please opens version-bump PRs |

---

## 2.8 Testing standards

| Practice | When it applies |
|---|---|
| **Table-driven tests** | Any function with ≥2 distinct input/output pairs |
| **`go test -race -count=1`** | Every CI run |
| **Integration over mocks at system boundaries** | DB tests use `testutil/pg` (real Postgres via testcontainers); HTTP tests use `httptest`; mock only non-infrastructure dependencies |
| **Fuzz tests** | Parsers + decoders — stdlib fuzz, corpora in `testutil/fuzz/` |
| **Property-based tests** | Opt-in via `testutil/prop/` (gopter); useful for algebraic code: serialization round-trips, sort order, set ops |
| **Golden files + snapshots** | `testutil/snapshot/` via go-snaps; use for generated output (migration diffs, Scalar HTML, ogen stubs) |
| **Coverage targets** | 70% framework-wide; **85%** on security-critical packages (`crypto/`, `auth/`, `errors/`) |

---

## 2.9 Deployment + configuration

| Standard | Application |
|---|---|
| **Twelve-Factor App** | Config from env, logs to stdout, stateless processes, declared dependencies (`go.mod`), port binding (`httpx/server`), disposability (fx shutdown hooks) |
| **CNCF Cloud Native Principles** | K8s-native manifests (`deploy/helm/`) with downward API, PodDisruptionBudget, NetworkPolicy, and ServiceMonitor |
| **OCI Image Spec** | Multi-arch via buildx (amd64 + arm64); Chainguard distroless base |
| **Rootless + read-only filesystem** | Dockerfile templates use a non-root runtime user; the Helm chart defaults `readOnlyRootFilesystem: true` and numeric non-root IDs |

---

## Quick reference: banned patterns

| Pattern | Why | Alternative |
|---|---|---|
| `time.Now()` outside `clock/` | Breaks testability (non-deterministic) | `clock.Now()` via `clockwork.Clock` |
| `fmt.Println` / `log.Printf` | Bypasses structured logging | `slog.InfoContext(ctx, ...)` via `github.com/golusoris/golusoris/core/log` |
| `init()` side effects | Breaks fx lifecycle ordering | `fx.Provide` / `fx.Invoke` hooks |
| Bare `errors.New` returned across packages | Loses context chain | `fmt.Errorf("pkg: op: %w", err)` or `gerr.Wrap` |
| `//nolint` without justification | Silently hides real issues | Add inline comment explaining why |
| `unsafe` without a `// SAFETY:` proof | Memory safety violation | Keep the operation local and document why every pointer or memory invariant holds |
