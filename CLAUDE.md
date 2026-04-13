# Claude Code guide — golusoris

> Claude Code-specific guide. For cross-tool conventions read [AGENTS.md](AGENTS.md) first; this file extends it.

## Skills available

Located in `.claude/skills/`:

| Skill | When to use |
|---|---|
| `wire-fx-module` | Adding a new opt-in fx module to the framework |
| `scaffold-ogen-handler` | Generating an ogen handler stub from an operationId |
| `add-river-worker` | Adding a river job worker (registered in fx) |
| `add-migration` | Creating a timestamped golang-migrate up/down pair |
| `bump-golusoris` | Bumping golusoris in a downstream app + applying codemods from migration notes |

Invoke via `/<skill-name>` in Claude Code.

## Hooks active

Located in `.claude/hooks/`:

- Touching `**/jobs/*.go` auto-loads `docs/upstream/river/` + `jobs/AGENTS.md`
- Touching `**/migrations/*.sql` auto-loads `docs/upstream/golang-migrate/` + the project's existing migrations summary
- Touching `**/api/*.go` (ogen) auto-loads `docs/upstream/ogen/` + the OpenAPI spec
- Pre-commit: runs `make ci` (lint + sec + test)

## Tone

- Be terse. No preamble.
- When changing public API: write the `Migration:` footer in the commit body, with before/after Go snippets.
- When adding a dependency: state which awesome-go alternatives you considered and why this one wins.
- Never add init() side effects. Always use fx lifecycle.

## Project principles — read [.workingdir/PLAN.md §2](.workingdir/PLAN.md) first

§2 is the framework's foundational contract. Quick hitlist for AI agents:

- **§2.1 Power of 10, Go-adapted** — hard gates on rules 1, 2, 4, 7, 10 (control flow, bounded loops, function size, error wrapping, zero lint/gosec/vuln). Guidance on 3, 5, 6, 9.
- **§2.2 SEI CERT for Go** — security rules (crypto, input validation, concurrency). Cite rule IDs in review.
- **§2.3 Google Go Style Guide** — canonical style. Effective Go + Code Review Comments secondary.
- **§2.4 C4 + ADRs** — architecture decisions in `docs/adr/`, one per decision, Nygard format.
- **§2.5 Security + supply-chain standards** — SLSA L3, OWASP ASVS L2, NIST SSDF, EU CRA, NIS2, BSI IT-Grundschutz, BSI C5, UK NCSC, ENISA, GDPR, EU AI Act. Framework ships scaffolding; apps assert compliance in `SECURITY.md`.
- **§2.6 Wire protocols** — RFC 9457 Problem Details (error body), OpenAPI 3.1, OTel SemConv v1.26, JWT/OAuth/PKCE/WebAuthn.
- **§2.7 Tooling + formatting** — EditorConfig, gofumpt, gci, golines, Conventional Commits, SemVer, Keep-a-Changelog, Trunk-Based Dev.
- **§2.8 Testing** — table-driven tests, `go test -race`, integration over mocks, fuzz + property-based opt-in, 70% coverage (85% on security-critical pkgs).
- **§2.9 Deployment** — Twelve-Factor, CNCF cloud-native, OCI, rootless + read-only FS.

Every merged commit: 0 lint · 0 gosec · 0 govulncheck · race-green. `//nolint` needs a justification comment.

## Don't

- Don't use `time.Now()` outside `clock/`. Use `clock.Now(ctx)`.
- Don't `fmt.Println` — use the slog handler from `log/`.
- Don't add features beyond what the task requires (per global Claude Code guidelines).
- Don't write multi-paragraph comments. One-liner WHY comments only.
- Don't create new markdown docs unless explicitly asked.
- Don't silence a linter without adding a justification comment next to the `//nolint` directive.

## Project state

- Pre-alpha. Steps 1-5 landed on `main` (`golusoris/golusoris`): Core, DB, HTTP base, HTTP extras, OTel + observability.
- See [.workingdir/PLAN.md](.workingdir/PLAN.md) for the full plan and [.workingdir/STATE.md](.workingdir/STATE.md) for the current status + decision log.

## Every commit: keep docs in sync

On each commit touching new/changed modules:

- Update [.workingdir/STATE.md](.workingdir/STATE.md) session log with the commit summary.
- Update [README.md](README.md) "Landed so far" list when a step completes.
- Update [AGENTS.md](AGENTS.md) layout tree when adding new top-level packages.
- Write per-subpackage `AGENTS.md` for any new module.
