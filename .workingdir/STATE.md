<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Session state — golusoris

> Persistent state across workstations and AI sessions. Updated as significant changes happen.
> Last update: 2026-09-14 (v0.9.0 prep: lean `core/` sub-module · capability contract · EUPL-1.2 · praetor governance).

## Session log — 2026-09-14: praetor onboarding, lean core, EUPL-1.2 (branch `feat/core-submodule-praetor-eupl`)

Goal: make golusoris the dedupe target for every Go repo in `~/dev` and
onboard it with cordanallm/praetor in both directions. Work graph in
[TODO.md](TODO.md); fleet demand in [FLEET-DEMAND.md](FLEET-DEMAND.md).

- **Local `main` was 192 commits behind origin** (tags to v0.7.0, 0.8.0 prepared
  but untagged). Fast-forwarded first. GitHub is the push target for now;
  the Gitea canonical remote comes later (user decision).
- **`core/` sub-module (ADR-0017)** — `config log clock errors crypto id
  validate version clikit mcp` moved to `github.com/golusoris/golusoris/core/…`
  (201 import sites rewritten); `clikit/tui` stays in root. Root and
  `media/{audio,img}` require `core v0.9.0` via in-repo `replace`. New core
  packages: `codec/yaml`, `crypto/receipt`, `gitx` + `gitx/worktree`, `astx`,
  `capabilities`. Root gained `db/sqlite` (modernc, 2 fleet consumers).
- **Hard bump** — `go 1.27.0` in all 22 go.mod files; `go get -u -t` everywhere.
  Only breakage: k8s `kube-openapi` had to be pinned to the version
  `apimachinery v0.37.0` requires (structured-merge-diff v6/v7 clash);
  `tint.NewHandler` → `NewTextHandler`. Local toolchain: golangci-lint v2.13.2
  (CI pin), gofumpt, apidiff, reuse 6.2.0.
- **Capability contract (ADR-0019)** — root `capabilities.yaml` (202 packages,
  21 modules, generated skeleton + hand-tuned keys/`replaces`),
  `capabilities_test.go` drift guard. praetor now reads it: fleet coverage
  20.9 % → **97.5 %**; praetor `.needs.yaml` **100 %**. Remaining real gaps
  (backlog): otelpgx, hashicorp/raft(+boltdb), anthropic-sdk-go,
  govalues/decimal, embedded-postgres, imroc/req, go-hclog, ginkgo/gomega,
  goptuna, parquet-go, containerd/nri, go.uber.org/mock, goleak.
- **praetor fixes (branch `feat/golusoris-core-onboarding`)** — module path
  `github.com/cordanaLLM/standards` → `github.com/cordanallm/praetor`;
  `needs` reads `capabilities.yaml` (`GOLUSORIS_PATH` / module cache
  discovery, no `/home/kilian` defaults), version from `git describe`,
  fleet-owned prefixes + `golang.org/x` classified native, major-version-
  insensitive `replaces` matching, `custom.v2` noise fixed, walk-root skip bug
  (`--path=.` scanned nothing) fixed; transpiler is repo-name-aware and carries
  `## <Vendor>` sections from AGENTS.md into each target; catalog paths moved
  to `core/`. `docs/golusoris-migration-plan.md` records the code migration
  (deferred to a later session).
- **EUPL-1.2 (ADR-0018)** — LICENSE/LICENSES (EUPL-1.2, CC-BY-SA-4.0,
  CC-BY-4.0, MIT, Apache-2.0, GPL-2.0-only for the eBPF program), REUSE.toml
  with third-party carve-outs, LICENSING.md, SPDX headers on 1 012 files
  (`reuse annotate` layout; front-matter and generated files covered by
  REUSE.toml instead), `goheader` lint, `reuse lint` + DCO jobs in CI,
  README badges, CONTRIBUTING DCO section, `golusoris init` scaffolds
  LICENSE + REUSE.toml + headers.
- **Praetor governance (full adopt)** — `.standards.yaml` (framework;
  security:high, api:public-contract, docs:seo-portal, agent:sandboxed),
  `.standards.lock`, baseline of **1 807** HISS infractions (HISS-04 60-LOC
  func cap dominates), compiled vendor context for 6 agents, IDE configs,
  devcontainer, `.config/labels.yaml`, `.github/rulesets/main.json`. The
  former `CLAUDE.md` body now lives in `AGENTS.md` `## Claude Code`; the
  transpiler compiles CLAUDE.md (112 lines). `standardsctl audit` → 100 %.
- **Gate (local, Windows)** — build + vet green (root, core, 20 sub-modules);
  golangci-lint 0 issues (root + core); `reuse lint` compliant; core tests
  green; root tests green except 3 Windows-only environment failures
  (`k8s/client` kubeconfig present on host, `storage` file lock on delete,
  `systemd` unixgram) and `storage/scan` (clamd is Linux-only) — CI runs
  Linux. `-race` unavailable locally (no gcc); CI covers it.
- **Release note** — v0.9.0 is breaking (import paths, Go 1.27). Tag
  `core/v0.9.0` and `v0.9.0` on the same commit; release-please has a `core`
  component with `tag-separator: "/"`.

## Session log — 2026-09-02: Gitea publisher main-only trigger fix

The Gitea-to-GitHub publisher now filters its `workflow_run` trigger to the
`main` branch. Gitea 1.27.2 had been creating a publication run for every
completed pull-request CI run; those runs shared `github-publication`
concurrency and repeatedly cancelled the waiting, validated `main` publisher.
The branch filter keeps the exact-head CI-to-publication gate while removing
the pull-request cancellation storm. Live acceptance remains an exact
Gitea-main CI success followed by a successful fast-forward of the same SHA to
public GitHub.

## Session log — 2026-09-01: Gitea apidiff path contract

The Gitea CI gate now invokes the pinned `apidiff` binary from the exact
`go env GOPATH` install path. This keeps the job rootless and independent of
whether the job image places `GOPATH/bin` on `PATH`.

## Session log — 2026-07-04: NATS integration tests (GOL-3)

**Added testcontainers-backed integration tests** for `pubsub/nats`.

- New `testutil/nats/` package: generic testcontainers helper that spins `nats:2-alpine`
  with JetStream enabled (`-js`) and returns the server URL. Patterned on `testutil/redis/`.
- `pubsub/nats/nats_test.go` expanded with three integration tests:
  `TestIntegration_ConnectAndPing`, `TestIntegration_PublishSubscribe`,
  `TestIntegration_JetStreamAvailable`.
- Tests wire the module via `fxtest.New` + `config.New` (temp YAML) — same pattern as `storage/module_test.go`.
- Changelog fragment: `changelog.d/added/GOL-3-testutil-nats.md`.

## Session log — 2026-07-04: crypto/ coverage (GOL-4)

**Lifted `crypto/` test coverage** toward the 85% security-critical target (§2.8).

New tests in `crypto_test.go` (black-box):
- `TestOpenErrShortCiphertextSentinel` — asserts `errors.Is(err, ErrShortCiphertext)` (SEI CERT ERR01-G).
- `TestOpenTamperedCiphertext` — GCM auth-tag failure path (SEI CERT MSC00-G).
- `TestVerifyPasswordNeedsRehash` — `needsRehash=true` branch when stored hash used weaker params.

New tests in `hasher_internal_test.go` (white-box):
- `TestHashSuccessPath` — blocking `Hash` success (slot available).
- `TestNewPasswordHasherFromConfig` — fx provider defaults to GOMAXPROCS (SEI CERT ENV02-G).

Changelog fragment: `changelog.d/added/test-crypto-coverage-85.md`.

## Session log — 2026-06-20: torrent/ (§4.12)

**Built `torrent/`** — a backend-agnostic `Client` over a running torrent
daemon, selected by config like `storage/` selects local vs s3. One interface
(`Add`/`AddFile`/`Remove`/`List`/`Get`/`Pause`/`Resume`/`Stats`) with a
normalised `Torrent`/`State`/`Stats` view; unknown backend → `ErrUnsupportedBackend`.

- **Backends**: `transmission` (default, hekmon/transmissionrpc/v3 v3.0.0 — auto
  409 CSRF; returns hash on add), `qbittorrent` (autobrr/go-qbittorrent v1.16.0
  — SID cookie login via fx `OnStart`, version-aware pause/stop), `rtorrent`
  (autobrr/go-rtorrent v1.12.0 — XML-RPC, mutations resolve by hash). All MIT;
  all take a timeout-bearing `*http.Client` from `Options.Timeout`.
- **fx**: `torrent.Module` (`golusoris.torrent`) provides `torrent.Client`;
  config under `torrent`; qBittorrent login on lifecycle `OnStart` (no `init()`).
- **Tests**: real-API-shaped httptest servers — qBittorrent WebAPI v2
  (cookie/version/CRUD), transmission JSON-RPC (409 handshake + tag echo),
  rtorrent XML-RPC (per-field + `d.multicall2`). Table-driven, `-race`.
- **Gate**: gofumpt clean, build/vet clean, golangci-lint 0, gosec 0,
  `go test -race` green, **81.2%** coverage. Deliverables: package + tests +
  `torrent/AGENTS.md` + ADR-0015.

## Session log — 2026-06-20: media/img/pipeline (§4.12)

**Built `media/img/pipeline/`** — on-demand image resize + HMAC signed-URL
serving. Sub-package of the `media/img` module (added `require golusoris/golusoris`
+ `replace => ../..` like `media/audio`, plus fx).

- **Signing**: self-contained token `base64url(payload)"."base64url(hmac-sha256)`
  over a canonical `escape(key)|w|h|q|format|expiryUnix`; required `>=16`-byte
  secret; constant-time verify (`hmac.Equal`); expiry via injected `clock.Clock`.
- **Bounds**: max width/height/pixels + format allowlist validated before decode
  (decompression-bomb guard, Power-of-10 rule 2).
- **Handler**: chi-/mux-friendly `/img/{signed}` → 200/400(bad token)/403(bad
  sig|expired)/404(missing source)/415(no libvips)/500. Correct `Content-Type` +
  `Cache-Control`.
- **fx**: `pipeline.Module` (`golusoris.media.img.pipeline`) provides `*Pipeline`
  + a named `http.Handler`; config under `media.img.pipeline`; processor closed
  on fx stop; no `init()`.
- **CGO**: signing/validation/routing is CGO-independent (stock build returns 415
  on resize since the parent `img.NewProcessor` ships stubbed). Real libvips
  round-trip gated behind the `imgvips` build tag.
- **Gate**: gofumpt clean, build/vet clean, golangci-lint 0, gosec 0,
  `go test -race` green, **85.5%** coverage of the non-CGO logic. Deliverables:
  package + tests + `pipeline/AGENTS.md` + ADR-0016.

## Session log — 2026-06-19 (cont.): catalog gap-modules built (#99)

**Built the 9 modules from the §4 catalog that were still unbuilt** — found by
diffing PLAN.md §4 against the repo (the rest of the framework is complete +
0-TODO). All gate-green (0 lint, 0 gosec, race-tested) on branch
`feat/catalog-gap-modules`:

| Module | Dep | Coverage |
|---|---|---|
| jsonschema | santhosh-tekuri/jsonschema/v6 | 94.7% |
| storage/safety | code.dny.dev/ssrf + stdlib re-encode strip | 91.1% |
| storage/scan | baruwa-enterprise/clamd (fail-closed) | 94.7% |
| storage/tus | tus/tusd/v2/pkg/handler + Bucket DataStore | 86.7% |
| httpx/inertia | romsar/gonertia/v3 | 96.3% |
| integrations/goenvoy | goenvoy submodules (pseudo-ver) | 91.8% |
| htmltmpl | stdlib html/template + sprout seam | 89.0% |
| media/audio | go-mp3/flac/ogg/wav + ebur128 (own go.mod, pure-Go) | 88.2% |
| deploy/pulumi + deploy/multiregion | pulumi-aws v7 (own go.mod, IaC) | n/a |

Built via a research→implement workflow pair (parallel subagents). Research
flagged + corrected weak catalog hints: `faiface/beep` is playback-only →
switched media/audio to pure-Go decoders; `a-h/templ` needs codegen → htmltmpl
uses stdlib html/template with a go-sprout FuncProvider seam; tusd-the-binary →
only `pkg/handler` embedded over storage.Bucket. ADRs **0008–0014** record the
dep decisions. Decision worth remembering: jsonschema is a STATELESS utility
(no fx Module), matching hash/markdown — the research suggested fx; the
stateless form is the right convention fit.

Note: 4 implement-agents hit a transient server-side rate-limit mid-run; their
code was on disk + building, a cleanup pass finished lint/tests (and caught a
real latent nil-error-wrap bug in storage/tus). No data lost.

## Session log — 2026-06-19 (cont.): v0.7.0 + release flow fully automated

**v0.7.0 released — the FIRST fully-automated, hands-off release.** Merging the
release-please PR cut the tag AND built + signed the artifacts in the same run
(15 assets: 6 archives + 6 SPDX SBOMs + cosign checksums.sig/.pem). No manual
`workflow_dispatch`, no PAT. Verified end-to-end: `release-please: success` +
`goreleaser: success` in run 27841034526.

**Both release frictions eliminated — no `RELEASE_PLEASE_TOKEN` PAT needed**
(the earlier PAT recommendation was WRONG; the app repos never used one):

| Friction | Old (v0.5.0–v0.6.1) | Fix | PR |
|---|---|---|---|
| goreleaser didn't run on bot-cut tags → manual dispatch every release | separate `release.yml` keyed on `push: tags` (GITHUB_TOKEN tags don't fire it) | `release.yml` gained `workflow_call`; `release-please.yml` calls it from a `goreleaser` job gated on `release_created`, in the same run | #294 |
| release PR couldn't merge (bot-PR CI never runs) → close+reopen dance | `enforce_admins=true` blocked admin click-merge past un-run required checks | `enforce_admins=false` on `main` (matches app-revenge/goenvoy) — admin click-merges the release PR | branch-protection API |

This SUPERSEDES the 06-15 "pending: add a RELEASE_PLEASE_TOKEN PAT" item and the
06-19 v0.6.1 note that "the dance persists" — both are now resolved. Pattern now
mirrors `golusoris/app-revenge` + `golusoris/goenvoy` exactly.

**Branch protection note:** `main` now has `enforce_admins=false`. Admins can
merge past un-run/failing required checks — used deliberately for bot release/deps
PRs whose CI doesn't auto-fire. Solo-appropriate; revisit if the team grows.

## Session log — 2026-06-19: v0.6.1 maintenance release + CI enabler

**v0.6.1 released and signed** — maintenance/deps only, **no new features**
(sockmap/tflite/grpc already shipped in v0.6.0). Bundles the post-v0.6.0
dependency bumps (ogen 1.22, aws-sdk-go-v2 1.104, sentry-go 0.47, temporal
1.45, go-oidc 3.19, testcontainers-go 0.43) + GitHub Actions → v7 (#290).
release-please cut the tag (#283 — unblocked via close+reopen, since bot-PR CI
doesn't auto-fire); `release.yml` dispatched manually for the signed build
(15 assets: 6 archives + 6 SPDX SBOMs + cosign checksums). Still no
`RELEASE_PLEASE_TOKEN` PAT → the close+reopen + manual-dispatch dance persists
(see the 06-15 pending item).

**#180 closed (won't-do):** SHA-pinning is already enforced repo-wide
(`actions/permissions.sha_pinning_required: true`) — the real anti-hijack
control. A `selected`-action allowlist is solo-over-hardening like #189/#190;
revisit only if org membership grows.

**#184 → PR #292 (ci-go.yml enabler):** added backward-compatible `container` +
`system-packages` inputs so system-dep apps (cgo/libvips/ffmpeg) can adopt the
reusable workflow. Per-app migration PRs follow in each app repo (0/6 adopt
today). actionlint-clean.

**CAUTION — do NOT re-investigate #266/#268/#27/#156:** these squash-merged on
06-15 (db673ef sockmap, b65b72e tflite) and ARE in v0.6.0+. Their lingering
`feat/issue-27` / `feat/issue-156` branches + stale PR API state can read as
"open/DIRTY"; the features are on main. Verify with
`git ls-tree v0.6.0 -- pkg/sockmap` before acting, not the PR list. (This turn
burned effort re-merging already-merged work by trusting the PR list over git.)

## Session log — 2026-06-15: v0.6.0 release + release-pipeline hardening

**v0.6.0 released and verified** — first fully-correct release (v0.5.0 lost its
signed binaries). Resolves Go proxy `@latest`; ships 6-platform archives + per-archive
SPDX SBOMs + cosign keyless signatures + SLSA build provenance. Verified end-to-end:
checksum match, `cosign verify-blob` OK, `gh attestation verify` exit 0
(builder `release.yml@refs/heads/main`, github-hosted). Bundles sockmap (#27),
tflite serve adapter + PG registry (#156), grpc `ProvideServerOptionFn` (#269) — all closed.

**Four release-pipeline bugs fixed (all permanent on main):**

| Bug | Fix | PR |
|---|---|---|
| release-please cut component-prefixed tags (`golusoris-v*`) → not a Go version, missed goreleaser | `include-component-in-tag: false` (dropping `component` in #274 was insufficient — `package-name` still feeds the component) | #279 |
| Tag cut by release-please's GITHUB_TOKEN doesn't trigger tag-keyed `release.yml` (goreleaser never runs) | `workflow_dispatch` escape hatch on release.yml (checkout `inputs.tag`, `GORELEASER_CURRENT_TAG`) | #281 |
| goreleaser before-hook `go generate ./...` ran pkg/sockmap's clang eBPF compile → `asm/types.h` not found on runner | dropped `go generate` from before-hooks (the `.o` is committed + embedded; CGO off) | #282 |
| Dispatched rebuild checks out the immutable tag's tree, which still has the stale before-hooks | `--skip=before` on the goreleaser args when dispatched | #284 |

**Pending (needs org-admin, not autonomously doable):** add a `RELEASE_PLEASE_TOKEN`
PAT (Contents+PRs+Workflows read/write) as a repo secret and set
`token: ${{ secrets.RELEASE_PLEASE_TOKEN }}` on the release-please-action step. That
makes the whole flow hands-off: release-PR CI runs without the close+reopen dance, and
the tag is cut by a real actor so `release.yml` fires automatically (no manual dispatch).

**Known flake:** `ai/tiny/serve/tflite` `TestPredictor_Predict_*` (parallel httptest)
flaked once on the full-suite-with-Docker CI job; not reproducible locally (8× `-race`).
If it recurs, deparallelize those tests — likely runner resource contention, not a bug.

## Naming conventions (Option B)

| Kind | Path | Example |
|---|---|---|
| Framework (namesake) | `golusoris/golusoris` | the framework |
| Library | `golusoris/<name>` | `golusoris/goenvoy` |
| App | `golusoris/app-<name>` | `golusoris/app-lurkarr` |
| Tool/CLI (future) | `golusoris/cmd-<name>` (proposed) | — |

## Repos (current state)

### `golusoris/` org
| Repo | Status | Notes |
|---|---|---|
| `golusoris/golusoris` | created (empty), public, 14 topics | local scaffold ready, awaiting first push |
| `golusoris/goenvoy` | transferred from lusoris ✓ | library; FUNDING + security defaults inherited |
| `golusoris/.github` | populated ✓ | FUNDING.yml (Ko-fi: lusoris) + profile/README.md + labels.yml + sync-labels.yml workflow |
| `golusoris/app-lurkarr` | transferred + renamed ✓ | redirect from `lusoris/Lurkarr` active |
| `golusoris/app-subdo` | transferred + renamed ✓ | redirect from `lusoris/subdo` active |
| `golusoris/app-revenge` | transferred + renamed ✓ | redirect from `lusoris/revenge` active |
| `golusoris/app-arca` | transferred + renamed ✓ | redirect from `lusoris/arca` active |

### `lusoris/` user
| Repo | Disposition |
|---|---|
| `lusoris/.github` | KEEP (covers any future personal repos) |
| (apps all transferred away) | redirects from old paths still functional via GitHub's automatic forwarding |

## Org settings — `golusoris/`

- Display name: `golusoris`
- Description: "Composable Go framework — opt-in fx modules for production backends."
- Blog/website: https://github.com/golusoris/golusoris
- Default branch: `main`
- Default repo permission: `read`
- 2FA required: ✅ enabled
- New-repo security defaults: Dependabot alerts ✓, security updates ✓, dependency graph ✓, secret scanning ✓, push protection ✓
- Workflow permissions: `default_workflow_permissions=read`, `can_approve_pull_request_reviews=false`
- Actions allowlist: `selected` — github-owned ✓, verified ✓, plus pattern allowlist (cosign, syft/anchore, goreleaser, golangci, docker buildx, release-please, digestabot, slsa-framework, etc.)

## Repo settings (new repos in `golusoris/` org)

| Setting | Value |
|---|---|
| `allow_squash_merge` | true |
| `allow_rebase_merge` | true |
| `allow_merge_commit` | false |
| `delete_branch_on_merge` | true |
| `squash_merge_commit_title` | `PR_TITLE` |
| `squash_merge_commit_message` | `PR_BODY` |

(Org has no central control on free plan — apply per-repo as new repos are created.)

## Pending action items

- [x] Transfer + rename 4 apps to `golusoris/app-*` ✓ 2026-04-13
- [x] Apply PR merge settings + security defaults to each app ✓ 2026-04-13
- [x] First commit + push of `golusoris/golusoris` framework code ✓ 2026-04-13
- [x] Apply per-repo branch protection on `golusoris/golusoris` main ✓ 2026-04-13
- [x] Update `golusoris/.github/profile/README.md` to proper org overview ✓ 2026-04-13
- [x] Add CI workflow (lint + test + vuln + build) to `golusoris/golusoris` ✓ 2026-04-13
- [x] Add auto-assign workflow to `golusoris/golusoris` ✓ 2026-04-13
- [x] Apply branch protection to `goenvoy` + 4 app repos ✓ 2026-04-13 (note: app-arca / app-revenge default branch is `develop`, not `main`)
- [x] Org profile README rewritten as proper org overview ✓ 2026-04-13
- [ ] Pin `golusoris/golusoris` and `golusoris/goenvoy` on org page — UI-only: <https://github.com/orgs/golusoris>
- [ ] Upload org avatar — UI-only: <https://github.com/organizations/golusoris/settings/profile>
- [ ] Add `.github/workflows/labels.yml` in each repo to sync labels from `golusoris/.github/labels.yml`
- [ ] GitHub Sponsors enrollment (if desired; Ko-fi already set)
- [ ] Org-wide ruleset would require Team plan ($4/mo) — currently using per-repo classic branch protection (free, applied after first push)

## Session log (recent)

- 2026-09-14: **portability: build + test green on Windows** (branch `hiss/portability`).
  - `storage/scan`: clamd backend (`baruwa-enterprise/clamd`, unix-only
    syscalls) constrained to `//go:build unix`; `clamd_stub.go` on `!unix`
    keeps the exported API and fails closed with new `scan.ErrUnsupported`
    (wraps `errors.ErrUnsupported`). Unit tests split by tag, stub tests added.
  - `pkg/sockmap/cgroup.go` is `//go:build linux` (only `loader_linux.go`
    uses it; `unused` fired on Windows) and its deferred close now goes
    through `gerr.CloseInto`.
  - `scripts/hooks/pre-push.sh` drops the storage/scan exclusion: root
    `go build ./...` / `go vet ./...` pass on every OS.
  - Audited tests for unix sockets / file locks / `$HOME`: only the three
    already fixed (k8s/client, storage, systemd) touched the host.

- 2026-09-14: **lefthook git-hook gate** landed (`lefthook.yml` + `scripts/hooks/`).
  - pre-commit (parallel): gofumpt / gci / golangci-lint / go vet on the
    staged packages, `standardsctl compile-context --verify`, `reuse lint`,
    `gitleaks git --staged`; commit-msg: Conventional Commits + DCO trailer;
    pre-push: `go build` + `go test -short` in root and `core/`. Missing
    tools skip with an install hint — CI stays authoritative.
  - Replaces the phantom `pre-commit install` / "pre-commit runs make ci"
    claims in CONTRIBUTING.md and AGENTS.md.
  - Three Windows-only test fixes surfaced by the pre-push gate
    (k8s/client USERPROFILE, storage open-handle delete, systemd unixgram skip).

- 2026-06-15: **mcp/ reusable MCP server fx module** landed (#255).
  - New opt-in `mcp.Module` wraps the official
    `modelcontextprotocol/go-sdk`: provides a tool-less `*mcp.Server`;
    apps register tools via `fx.Invoke(func(s *mcp.Server){ s.AddTool(...) })`.
  - Runs the configured transport under the fx lifecycle: stdio (default,
    ends the app via `fx.Shutdowner` on client disconnect) or
    streamable-HTTP (dedicated `*http.Server` at `mcp.http.path`,
    graceful shutdown on Stop).
  - Stdout purity in stdio mode: pins the real stdout to the transport and
    redirects the process-global `os.Stdout` to stderr for the transport's
    lifetime, so stray `fmt.Println` can't corrupt JSON-RPC framing.
  - No new dependency (SDK already vendored for `cmd/golusoris-mcp` +
    `apidocs`). 84.8% coverage, race-clean, 0 lint/gosec/vuln.
  - Orchestrator wires the umbrella `golusoris.go` + root AGENTS.md tree
    separately.

- 2026-06-15: **Integration (testcontainers) coverage for external-service backends** (#151, branch `feat/issue-151`):
  - New testutil helpers: `pg.StartReplication(t)` (boots `wal_level=logical` Postgres, returns `(pool, replicationDSN)`), `pg.StartTimescale(t)` (boots `timescale/timescaledb` image + `CREATE EXTENSION`), `redistest.Addr(t)` (returns `host:port` for driving a custom client). All guarded by `testcontainers.SkipIfProviderIsNotHealthy` so macOS/no-Docker skips cleanly.
  - `db/cdc`: end-to-end logical-replication test — INSERT/UPDATE/DELETE through `connect → runSetup → ensureSlot → StartReplication → handleMessage → dispatch → Parse → tupleToMap`. Drives `handleMessage` directly (not `runLoop`) so the read is never interrupted on a sub-second cadence — deterministic under `-race` (20+ consecutive clean runs).
  - `db/timescale`: full hypertable lifecycle against real TimescaleDB — `CreateHypertable`/`SetRetention`/`EnableCompression`/`AddCompressionPolicy`, asserting policies register in the jobs catalog.
  - `cache/redis`: `newClient` SET/GET round-trip + whitespace-trim + bad-addr error path against real Redis.
  - **Two boundary bugs found + fixed** (the integration tests revealed them; "real boundary correctness" per the issue):
    1. `db/timescale` `SetRetention`/`AddCompressionPolicy` used `INTERVAL $2` — a Postgres syntax error (the `INTERVAL` keyword rejects a bound parameter). Both calls would fail at runtime. Fixed to `($2)::interval`.
    2. `db/cdc` `runSetup` started replication from `sysident.XLogPos` (current WAL head). On any restart of a persistent slot this silently skips every change between the slot's confirmed position and now — data loss. Fixed to start LSN `0`, which resumes from `confirmed_flush_lsn`.
  - 0 lint · 0 gosec · race-green across the changed packages. No new dependencies.

> Entries dated before 2026-06-01 (the v0.1.0 → v0.5.x build-out, Steps 1-24) are archived in [archive/STATE-2026-04-to-05.md](archive/STATE-2026-04-to-05.md), together with the Step 2/3/5 decision tables and the Step-2-era "Local repo state" note.

## How to use this file

- `.workingdir/PLAN.md` is the architectural source of truth (decisions log + module catalog).
- `.workingdir/STATE.md` (this file) is the operational state — what exists, what's pending, what was configured where.
- Both are committed to the framework repo so any clone on any workstation gets the full context.
- `.workingdir/` should NOT be added to `.gitignore`.
