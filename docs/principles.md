# golusoris — coding & compliance contract

> This is the framework's foundational contract (§2 of the design plan).
> Every package in the framework — and every app built on top — is expected to follow these rules.
> Deviations require a PR comment justifying the exception.

---

## 2.1 Coding rules — Power of 10, Go-adapted

NASA/JPL's _The Power of 10: Rules for Developing Safety-Critical Code_ (Gerard J. Holzmann), adapted to Go.

Reference: <https://spinroot.com/gerard/pdf/P10.pdf>

| # | Original rule | Go adaptation |
|---|---|---|
| 1 | Restrict to simple control flow; no `goto`, `setjmp`, `longjmp`, recursion. | No `goto`. No hand-written recursion where a loop suffices (tree walks + parsers are the allowed exception — document the bound). Panic/recover only at trust boundaries (fx lifecycle, `http.Handler` recover, `ogenkit.RecoverMiddleware`). |
| 2 | All loops must have a fixed upper bound, statically provable. | Every `for` that isn't `for range` over a bounded collection must have a bound visible in the loop head (counter, max attempts, ctx deadline). Long-running loops `select` on `ctx.Done()`. |
| 3 | No dynamic memory allocation after initialization. | Soft: hot paths preallocate (`make([]T, 0, cap)`), reuse `sync.Pool` where profiles show churn. Startup-phase allocation is free; steady-state is watched. |
| 4 | No function longer than ~60 lines (single printed page). | `funlen` 120 lines / 60 statements + `gocognit` ≤ 30. Refactor when flagged; don't silence the linter. |
| 5 | ≥2 assertions per function on average; side-effect-free. | Table-driven tests + contract checks at API boundaries (validator, ogen decoders, `gerr.Wrap`). Target ≥2 assertions per test per function. `require`/`assert` via testify; no `panic(msg)` in non-test code. |
| 6 | Declare data at smallest possible scope. | Prefer block-scoped `:=`. Struct fields unexported unless explicitly part of the API. Package-level `var` only for singletons + sentinels. |
| 7 | Check every return value; check every parameter. | `errcheck` + `wrapcheck` + `nilerr` on. Errors wrapped with context via `gerr.Wrap` or `fmt.Errorf("pkg: op: %w", err)`. Exported funcs validate inputs at the boundary. |
| 8 | Preprocessor limited to simple macros. | N/A in Go. `go generate` directives stay simple + declarative. No build tags for behaviour switches in production paths. |
| 9 | Pointers restricted; one dereference per expression; no function pointers. | Soft: no multi-hop `*foo.bar.baz` chains. Small interfaces (≤5 methods) only, defined where consumed. No `unsafe` outside explicitly-reviewed performance code. |
| 10 | Compile at most pedantic warning level. | `golangci.yml` is the gate. Every merged commit: **0 lint · 0 gosec · 0 govulncheck · race-green**. `//nolint` requires a justification comment + PR review. |

**Hard gates** (CI blocks on violation): rules 1, 2, 4, 7, 10.  
**Guidance** (cite rule ID in review): rules 3, 5, 6, 9.  
Rule 8 is N/A in Go.

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
- Index + backfill policy: `docs/adr/README.md`.
- ADRs ≤ 0099 are retroactive backfills; new decisions start at ADR-0100.

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

Frameworks can't claim compliance — apps can, built on compliant scaffolding. Every `docs/compliance/*.md` is a machine-readable checklist keyed by control ID so auditors and AI agents can verify.

| Standard | Jurisdiction | Purpose | Enforcement |
|---|---|---|---|
| **SLSA Level 3** | OpenSSF (global) | Supply-chain provenance, immutable builds, SBOM, signed artifacts | `.github/workflows/release-go.yml` (cosign + syft + slsa-framework) |
| **OWASP ASVS Level 2** | OWASP (global) | App verification checklist (auth, session, crypto, API, config) | `SECURITY.md` declares compliance; CI runs OWASP ZAP against example apps |
| **NIST SSDF (SP 800-218)** | US | Secure Software Development Framework | OpenSSF Scorecard covers most items; CI publishes Scorecard badge |
| **EU Cyber Resilience Act (CRA)** | EU | SBOM + vuln reporting + secure-by-default for products with digital elements | SBOM generated per release; `SECURITY.md` documents coordinated disclosure |
| **NIS2 Directive** | EU | Incident handling + risk management for essential/important entities | Apps in NIS2 scope inherit the framework's logging + audit trail; `docs/compliance/nis2.md` checklist |
| **BSI IT-Grundschutz** | Germany | Baseline security controls | Framework provides the controls (crypto, auth lockout, audit log, secrets); apps map to BSI module numbers in their `SECURITY.md` |
| **BSI C5** | Germany | Cloud service criteria catalog | Relevant for apps on German government / regulated cloud; `deploy/` manifests are C5-compatible (NetworkPolicy, PodSecurityStandards, audit logs) |
| **UK NCSC Secure Development & Deployment** | UK | Developer-facing secure-dev guidance | Framework maps to NCSC's 8 principles — documented in `docs/compliance/ncsc.md` |
| **ENISA Good Practices** | EU | Sectoral security guidance | Cited in relevant module docs (IoT, AI) rather than blanket |
| **GDPR** | EU | PII handling, right to erasure | `log/` redacts documented PII fields; `audit/` + `tenancy/` support per-subject-data queries |
| **EU AI Act** | EU | High-risk AI transparency + risk management | Applies to apps using `ai/llm/` + `ai/vector/` for regulated decisions; framework provides audit log of prompts/outputs + human-override hook; compliance is per-app |

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
| **gofumpt** | Stricter gofmt — configured in `tools/golangci.yml` |
| **gci** | Grouped imports: standard / external / `prefix(github.com/golusoris/golusoris)` |
| **golines** | Line-length cap at 120 chars; long lines broken at safe points |
| **Conventional Commits 1.0** | CI PR-title check; release-please reads commit history |
| **Semantic Versioning 2.0** | Tags via release-please; breaking changes force major bump via `!` / `BREAKING CHANGE:` |
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
| **CNCF Cloud Native Principles** | K8s-native manifests (`deploy/helm/`) with downward API, PodDisruptionBudget, NetworkPolicy, CiliumNetworkPolicy |
| **OCI Image Spec** | Multi-arch via buildx (amd64 + arm64); Chainguard distroless base |
| **Rootless + read-only filesystem** | Enforced in `Dockerfile.template` (`USER 65532`, `readOnlyRootFilesystem: true`) |

---

## Quick reference: banned patterns

| Pattern | Why | Alternative |
|---|---|---|
| `time.Now()` outside `clock/` | Breaks testability (non-deterministic) | `clock.Now()` via `clockwork.Clock` |
| `fmt.Println` / `log.Printf` | Bypasses structured logging | `slog.InfoContext(ctx, ...)` via `golusoris/log` |
| `init()` side effects | Breaks fx lifecycle ordering | `fx.Provide` / `fx.Invoke` hooks |
| Bare `errors.New` returned across packages | Loses context chain | `fmt.Errorf("pkg: op: %w", err)` or `gerr.Wrap` |
| `//nolint` without justification | Silently hides real issues | Add inline comment explaining why |
| `unsafe` without review | Memory safety violation | Explicit PR review + link to benchmark |
