# Architecture Decision Records (ADRs)

This directory captures every architectural decision worth preserving — pinned dependencies, interface choices, cross-cutting conventions, deviations from defaults.

## Format

[Michael Nygard's format](https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions) — see [`0000-template.md`](0000-template.md). Sourced from [joelparkerhenderson/architecture-decision-record](https://github.com/joelparkerhenderson/architecture-decision-record), which is the canonical reference for ADR styles, examples, and tooling.

## Conventions

- **Filename**: `NNNN-kebab-case-title.md`, zero-padded 4-digit ID.
- **Immutable**: once `Status: Accepted`, the body is frozen. To change a decision, write a new ADR with `Supersedes: ADR-NNNN`.
- **One decision per ADR**: if you find yourself writing "and also…", split it.
- **Tag** the ADR so future readers can grep by area (`db`, `http`, `security`, …).
- **Link** ADRs from the relevant per-package `AGENTS.md` so the rationale is one click away.

## Index

| ID | Title | Status | Tags |
|---|---|---|---|
| [ADR-0001](0001-fx-over-wire-for-di.md) | fx over wire for dependency injection | Accepted | core, di |
| [ADR-0002](0002-koanf-over-viper-for-config.md) | koanf over viper for configuration | Accepted | core, config |
| [ADR-0003](0003-slog-over-zap-for-logging.md) | slog (stdlib) over zap for logging | Accepted | core, observability |
| [ADR-0004](0004-ogen-over-oapi-codegen.md) | ogen over oapi-codegen for OpenAPI | Accepted | http, api |
| [ADR-0005](0005-river-over-asynq-for-jobs.md) | river over asynq for background jobs | Accepted | jobs, db |
| [ADR-0006](0006-pluggable-leader-election.md) | Pluggable leader election (k8s Lease + pg advisory) | Accepted | leader, runtime |
| [ADR-0007](0007-rfc-9457-problem-details.md) | RFC 9457 Problem Details for HTTP error bodies | Accepted | http, api |
| [ADR-0008](0008-upload-safety-strip-and-ssrf.md) | Upload hardening — strip-by-re-encode and SSRF via dialer Control | Accepted | storage, security, ssrf, uploads |
| [ADR-0009](0009-gonertia-for-inertia-adapter.md) | gonertia/v3 for the Inertia.js server adapter | Accepted | http, frontend, inertia |
| [ADR-0010](0010-goenvoy-multimodule-clients.md) | goenvoy multi-module clients wired onto the framework's resilient HTTP stack | Accepted | integrations, http, metadata, arr |
| [ADR-0011](0011-clamd-upload-malware-scan.md) | baruwa-enterprise/clamd for upload malware scanning, fail-closed by default | Accepted | security, storage, supply-chain |
| [ADR-0012](0012-tusd-handler-bucket-datastore.md) | storage/tus dependency choice | Accepted | storage, uploads, tus |
| [ADR-0013](0013-pure-go-audio-decoders.md) | media/audio dependency choice | Accepted | media, audio |
| [ADR-0014](0014-stdlib-html-template-sprout-seam.md) | htmltmpl dependency choice | Accepted | http, templating, security |
| [ADR-0015](0015-torrent-client-backends.md) | torrent client abstraction and backend dependencies | Accepted | torrent, media, backends, dependencies |
| [ADR-0016](0016-image-pipeline-signed-urls.md) | On-demand image pipeline gated by HMAC signed URLs | Accepted | media, img, http, security |

## Backfill policy

ADRs ≤ ADR-0099 are *backfills* — decisions made before the ADR practice was formalised, captured retroactively from commit history. Status reflects the current code, not the original decision date.

New decisions start at ADR-0100.

## Further reading

- [joelparkerhenderson/architecture-decision-record](https://github.com/joelparkerhenderson/architecture-decision-record) — templates + alternatives (MADR, Y-statements, etc.) + tooling (`adr-tools`).
- [`docs/architecture/`](../architecture/) — C4 diagrams (PlantUML) referenced from the ADRs.
