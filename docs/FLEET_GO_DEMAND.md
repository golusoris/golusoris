<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Fleet Go demand (pre-migration input)

What the fleet's Go repositories import today, and how much of it `capabilities.yaml`
already claims to replace. This is the demand side of the pre-migration epic (#429):
it says what golusoris must provide for each consumer to drop its third-party
dependencies.

Scope is **Go only**. The fleet's TypeScript, Rust and Python dependencies are outside
golusoris's remit and are excluded deliberately.

## Method

Every `go.mod` in each repository is parsed for **direct** requirements, excluding
`vendor/`, `testdata/`, `node_modules/`, `_audit/`, and the agent scratch directories
`.claude/worktrees/` and `.workingdir/`. Requirements resolving to another module in
the same repository, or to another first-party fleet module, are not third-party demand.

Counted straight from `go.mod` rather than through `praetorctl needs scan`, because
that scanner reads only the repository-root `go.mod`. It reports one dependency for a
repo whose nested modules pull dozens, and fails with `no matching language analyzer`
on any repo without a root module (goenvoy, k8s, watershed). Its numbers are not a
usable migration input yet.

## Summary

| Metric | Value |
| :--- | ---: |
| Consumer repositories with Go | 9 of 10 |
| Distinct third-party packages in demand | 96 |
| Already claimed by `capabilities.yaml` | 38 (40%) |
| Gaps with no golusoris equivalent | 58 |

## Per repository

| Repository | Go modules | Third-party | Covered | Coverage |
| :--- | ---: | ---: | ---: | ---: |
| `VMAFx/vmafx` | 5 | 77 | 26 | 34% |
| `lusoris/k8s` | 14 | 9 | 7 | 78% |
| `lusoris/venio` | 1 | 8 | 7 | 88% |
| `lusoris/20-watts-was-enough` | 16 | 8 | 4 | 50% |
| `cordanaLLM/imago` | 2 | 4 | 3 | 75% |
| `cordanaLLM/praetor` | 1 | 1 | 1 | 100% |
| `lusoris/praetor` | 1 | 1 | 1 | 100% |
| `golusoris/goenvoy` | 69 | 0 | 0 | n/a |
| `lusoris/watershed` | 3 | 0 | 0 | n/a |
| `cordanaLLM/Aegis-OS` | 0 | 0 | 0 | n/a |

Three entries need reading rather than scoring:

- `golusoris/goenvoy` has 69 modules and zero third-party requirements. The metadata
  and arr clients are deliberately stdlib-only, so there is nothing to migrate.
- `cordanaLLM/Aegis-OS` contains no Go at all (no `go.mod`, no `.go` files); it is a
  Rust workspace. golusoris has nothing to offer it, recorded here so the question is
  not asked again.
- `cordanaLLM/praetor` and `lusoris/praetor` each have exactly one dependency,
  `gopkg.in/yaml.v3`, which `core/codec/yaml` now replaces. Both are one import rewrite
  from zero third-party dependencies, which is the point of the `core/` carve-out.

## Shared demand (two or more consumers)

Highest leverage: each of these removes a dependency from more than one repository.

| Consumers | Status | Package | Used by |
| ---: | :--- | :--- | :--- |
| 3 | covered | `github.com/modelcontextprotocol/go-sdk` | VMAFx/vmafx, cordanaLLM/imago, lusoris/k8s |
| 3 | covered | `modernc.org/sqlite` | VMAFx/vmafx, lusoris/20-watts-was-enough, lusoris/k8s |
| 2 | covered | `gopkg.in/yaml.v3` | cordanaLLM/praetor, lusoris/praetor |
| 2 | covered | `github.com/prometheus/client_golang` | VMAFx/vmafx, lusoris/k8s |
| 2 | covered | `github.com/sirupsen/logrus` | VMAFx/vmafx, lusoris/20-watts-was-enough |
| 2 | covered | `github.com/spf13/cobra` | VMAFx/vmafx, cordanaLLM/imago |
| 2 | covered | `k8s.io/api` | VMAFx/vmafx, lusoris/k8s |
| 2 | covered | `k8s.io/apimachinery` | VMAFx/vmafx, lusoris/k8s |
| 2 | covered | `k8s.io/client-go` | VMAFx/vmafx, lusoris/k8s |
| 2 | **gap** | `github.com/stretchr/testify` | cordanaLLM/imago, lusoris/venio |

## Gaps (58)

Packages the fleet imports that `capabilities.yaml` does not claim. Each is either a
capability golusoris could add, or a deliberate non-goal worth recording as one.

| Consumers | Package | Used by |
| ---: | :--- | :--- |
| 2 | `github.com/stretchr/testify` | cordanaLLM/imago, lusoris/venio |
| 1 | `cloud.google.com/go/bigquery` | VMAFx/vmafx |
| 1 | `cloud.google.com/go/pubsub` | VMAFx/vmafx |
| 1 | `cloud.google.com/go/storage` | VMAFx/vmafx |
| 1 | `contrib.go.opencensus.io/exporter/stackdriver` | VMAFx/vmafx |
| 1 | `github.com/anchore/go-logger` | lusoris/20-watts-was-enough |
| 1 | `github.com/anchore/stereoscope` | lusoris/20-watts-was-enough |
| 1 | `github.com/anchore/syft` | lusoris/20-watts-was-enough |
| 1 | `github.com/bombsimon/logrusr/v2` | VMAFx/vmafx |
| 1 | `github.com/bradleyfalzon/ghinstallation/v2` | VMAFx/vmafx |
| 1 | `github.com/c-bata/goptuna` | VMAFx/vmafx |
| 1 | `github.com/containerd/nri` | lusoris/k8s |
| 1 | `github.com/go-git/go-billy/v5` | VMAFx/vmafx |
| 1 | `github.com/go-git/go-git/v5` | VMAFx/vmafx |
| 1 | `github.com/gobwas/glob` | VMAFx/vmafx |
| 1 | `github.com/golangci/golangci-lint/v2` | VMAFx/vmafx |
| 1 | `github.com/google/addlicense` | VMAFx/vmafx |
| 1 | `github.com/google/go-cmp` | VMAFx/vmafx |
| 1 | `github.com/google/go-containerregistry` | VMAFx/vmafx |
| 1 | `github.com/google/go-github/v46` | VMAFx/vmafx |
| 1 | `github.com/google/go-github/v82` | VMAFx/vmafx |
| 1 | `github.com/google/ko` | VMAFx/vmafx |
| 1 | `github.com/google/osv-scanner/v2` | VMAFx/vmafx |
| 1 | `github.com/google/uuid` | VMAFx/vmafx |
| 1 | `github.com/goreleaser/goreleaser/v2` | VMAFx/vmafx |
| 1 | `github.com/grafeas/kritis` | VMAFx/vmafx |
| 1 | `github.com/h2non/filetype` | VMAFx/vmafx |
| 1 | `github.com/hmarr/codeowners` | VMAFx/vmafx |
| 1 | `github.com/in-toto/attestation` | VMAFx/vmafx |
| 1 | `github.com/in-toto/in-toto-golang` | lusoris/20-watts-was-enough |
| 1 | `github.com/jszwec/csvutil` | VMAFx/vmafx |
| 1 | `github.com/mcuadros/go-jsonschema-generator` | VMAFx/vmafx |
| 1 | `github.com/microsoft/azure-devops-go-api/azuredevops/v7` | VMAFx/vmafx |
| 1 | `github.com/moby/buildkit` | VMAFx/vmafx |
| 1 | `github.com/olekukonko/tablewriter` | VMAFx/vmafx |
| 1 | `github.com/onsi/ginkgo/v2` | VMAFx/vmafx |
| 1 | `github.com/onsi/gomega` | VMAFx/vmafx |
| 1 | `github.com/otiai10/copy` | VMAFx/vmafx |
| 1 | `github.com/parquet-go/parquet-go` | VMAFx/vmafx |
| 1 | `github.com/rhysd/actionlint` | VMAFx/vmafx |
| 1 | `github.com/shurcooL/githubv4` | VMAFx/vmafx |
| 1 | `github.com/shurcooL/graphql` | VMAFx/vmafx |
| 1 | `github.com/sigstore/cosign/v2` | VMAFx/vmafx |
| 1 | `github.com/xeipuuv/gojsonschema` | VMAFx/vmafx |
| 1 | `gitlab.com/gitlab-org/api/client-go` | VMAFx/vmafx |
| 1 | `go.opencensus.io` | VMAFx/vmafx |
| 1 | `go.uber.org/fx` | VMAFx/vmafx |
| 1 | `go.uber.org/mock` | VMAFx/vmafx |
| 1 | `go.yaml.in/yaml/v2` | VMAFx/vmafx |
| 1 | `gocloud.dev` | VMAFx/vmafx |
| 1 | `golang.org/x/net` | VMAFx/vmafx |
| 1 | `golang.org/x/oauth2` | VMAFx/vmafx |
| 1 | `golang.org/x/sync` | VMAFx/vmafx |
| 1 | `golang.org/x/sys` | lusoris/k8s |
| 1 | `golang.org/x/text` | VMAFx/vmafx |
| 1 | `google.golang.org/protobuf` | VMAFx/vmafx |
| 1 | `mvdan.cc/sh/v3` | VMAFx/vmafx |
| 1 | `sigs.k8s.io/release-utils` | VMAFx/vmafx |

## Proposal


Synthesis of 58 classified fleet-demand gaps (Task N14) against the pre-migration epic (#429: #431 decoupling, #432 dependency substitution, #433 verification, #434 activation). Verified against the actual `golusoris` package tree and `go.mod` rather than taken at face value — several classified `nearest_golusoris` paths referenced a `core/` prefix and packages (`core/gitx`, `core/codec/yaml`) that do not exist anywhere in the repository; those are corrected below.

**Scale**: 58 items, 51 VMAFx/vmafx-only, 7 shared with other fleet repos (lusoris/20-watts-was-enough ×4, lusoris/k8s ×2, cordanaLLM/imago + lusoris/venio ×1 via testify).

### By cluster (consumers desc, effort asc within cluster)

| Cluster | Items | Real target package | Effort | Unblocks |
|---|---|---|---|---|
| auth-crypto | 7 | new `supplychain` + `auth/oauth2client` | L | VMAFx, 20-watts-was-enough |
| cli-tui | 5 | `clikit/tui` (mostly non-goals) | S | VMAFx |
| cloud-provider-sdk | 6 | `pubsub` (GCP backend) + new `container/registry` | M | VMAFx |
| db-driver | 1 | new `db/parquet` | M | VMAFx |
| http-api | 7 | new `integrations/github` | M | VMAFx |
| infra-tooling | 7 | repo root (fx); rest mostly non-goal/new | S | VMAFx |
| k8s | 3 | `k8s/operator` (NRI) | M | lusoris/k8s |
| media-video | 1 | `storage/safety` | S | VMAFx |
| misc | 9 | `archive` + `jsonschema` | S | VMAFx, 20-watts-was-enough |
| ml-ai | 1 | new `ai/optimize` | L | VMAFx |
| observability | 5 | `log` (already covered) + supplychain (osv-scanner) | S | VMAFx, 20-watts-was-enough |
| testing | 6 | `testutil/fxtest` / `testutil/pact` | S | cordanaLLM/imago, lusoris/venio, VMAFx |

### Corrections found by re-reading the package tree

Four `nearest_golusoris` values in the classified data pointed at a `core/` prefix that does not exist in the repo (`find . -maxdepth 1 -type d -name core` returns nothing): `go.uber.org/fx` → `core`, `github.com/google/uuid` → `golusoris/golusoris/core/id` (also malformed), `github.com/anchore/go-logger` / `logrusr` → `core/log`. The real paths are the repo root (fx), `id/`, and `log/` respectively — all top-level packages, verified present with `AGENTS.md` + implementation files.

Two items claimed an existing wrapper that does not exist: `go-git` (claimed already used by `core/gitx`) and `go.yaml.in/yaml/v2` (claimed aliased by `core/codec/yaml` on v3) — grep of the full tree found zero `go-git` references and go-git is absent from `go.mod` entirely; `go.yaml.in/yaml` v2 and v3 are listed only as **indirect** dependencies, confirming no direct-use wrapper exists. Both are gaps to build, not aliases to document.

No cross-chunk disagreements (the same package classified two different ways) were found in this 58-item set — every package appears once. The issues above are single-source path/fact errors, not conflicting classifications.

### VMAFx-only demand (51 of 58 items, ~88% of this batch)

The overwhelming majority of this classified batch is VMAFx/vmafx-only demand: cloud-provider SDKs (BigQuery, GCS, Pub/Sub, Azure DevOps), the entire supply-chain-security cluster (cosign, in-toto, kritis, osv-scanner), CI/build tooling (ko, goreleaser, buildkit, golangci-lint, addlicense, actionlint), and most of http-api/cli-tui/testing. Given the fleet-wide context (77 of 96 third-party packages are VMAFx's), the maintainer should weigh how much of golusoris' roadmap should be steered by one consumer's dependency footprint versus the 7 items shared across lusoris/20-watts-was-enough, lusoris/k8s, cordanaLLM/imago, and lusoris/venio — the latter arguably deserve priority despite lower per-cluster item counts, since fixing them unblocks decoupling (#431) for repos beyond VMAFx.

### First sprint (10 cheapest, highest fleet-unblock items)

1. `storage/safety`: h2non/filetype magic-byte detection (S)
2. `archive`: otiai10/copy recursive directory copy (S)
3. `testutil/pact`: ginkgo fx-aware BDD lifecycle wrapper (S)
4. `testutil`: csvutil-based fixture codec (S)
5. `jsonschema`: go-jsonschema-generator for round-trip schema gen (M)
6. `pubsub`: GCP Pub/Sub backend beside kafka/nats (M)
7. `k8s/operator`: containerd/nri plugin hooks — 2 consumers (M)
8. `.needs.yaml`: document ~30 non-goal packages with rationale — zero code, immediate readiness-score gain
9. `container/registry`: new package wrapping go-containerregistry (M)
10. Flag `go-git` / `mvdan.cc/sh` / `go.yaml.in/yaml` as **unbuilt gaps** (not aliases) in `.needs.yaml` before task #432 substitution runs, so the substitution step doesn't silently point at nonexistent packages

### Non-goals declared (representative, full list per cluster above)

BigQuery, GCS, Azure DevOps API, gocloud.dev (cloud-provider-sdk — VMAFx's GCP/Azure-first architecture, not golusoris' S3/Postgres-shaped abstractions); anchore/stereoscope, anchore/syft, go-billy, gobwas/glob, hmarr/codeowners, x/text (stdlib-sufficient or too specialized); golangci-lint, addlicense, actionlint, ko, goreleaser, buildkit (standalone CI/CD tools, not libraries); gomega, testify, go.uber.org/mock (accepted standard testing deps); shurcooL/graphql, xeipuuv/gojsonschema, gitlab client-go (superseded by genqlient/santhosh-tekuri or provider-specific with no surface); ghinstallation (GitHub-App-specific auth, not a JWT primitive gap); protobuf, x/sys (transitive, no wrapping value).
