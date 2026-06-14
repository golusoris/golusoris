# Getting support

golusoris is a **community-maintained, pre-1.0 Go framework**. There is no paid
support tier. If you need help, start here.

## Questions about using the framework

- **A module won't wire / `fx` errors at startup** —
  [open a bug report](https://github.com/golusoris/golusoris/issues/new/choose).
- **Config / koanf key resolution, env-var mapping** — same.
- **HTTP, DB (pgx), jobs (river), cache, auth, observability modules** — same.
- **Bumping golusoris in a downstream app + applying migration codemods** —
  see the `bump-golusoris` skill and the release's `Migration:` notes first,
  then open an issue if something is unclear.

When the question is about a dependency golusoris wraps (pgx, river, koanf,
rueidis, casbin, …) rather than golusoris itself, that project's tracker is
usually the faster channel.

## Security issues

**Do not** open a public issue. See [`SECURITY.md`](SECURITY.md) — use the
private advisory flow or email <lusoris@pm.me>.

## How to ask a good question

Include:

- golusoris version (`go list -m github.com/golusoris/golusoris`) and Go version
  (`go version`).
- The set of `golusoris.*` modules you compose in your `fx.New(...)`.
- A minimal reproducer: the smallest `fx` graph + config that shows the
  behavior.
- What you expected vs what you got (full error / stack, not a paraphrase).
- What you've already tried.

Questions that include all five usually get triaged within a few days.

## Version support window

Pre-1.0, only the latest tagged minor is supported. Fixes land on `main` and
ship in the next release rather than being backported. Once the project reaches
v1.0, this section will define a concrete support window.

## Chat / real-time

None currently. Watch [the repo](https://github.com/golusoris/golusoris) for
announcements.
