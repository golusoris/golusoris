# ADR-0014: htmltmpl dependency choice

- **Status**: Accepted
- **Date**: 2026-06-19
- **Deciders**: @lusoris
- **Tags**: http, templating, security

## Context

The `htmltmpl` module needs a dependency. Per principles.md §2 the choice must favour a maintained, server-appropriate library with a lean footprint, justified against ecosystem alternatives.

## Decision

Use **`stdlib`** (go1.26 (html/template, text/template/parse)). Core engine = stdlib html/template (zero new deps, govulncheck-clean, and the gold standard for SSR auto-escaping: context-aware escaping across HTML/JS/CSS/URI states that no third-party engine matches). The module is pure ergonomics over it: a Renderer that owns a parsed *template.Template tree, named layout+partial composition (clone-per-render to keep block overrides safe), an io.Writer/http path, hot-reload in dev and parse-once in prod, and a curated default FuncMap. For the OPTIONAL extended helper-func set we pick go-sprout/sprout v1.0.x (github.com/go-sprout/sprout, actively maintained Dec 2025 release, modern Go, ~45% faster / 16% less memory than sprig, registry-based so dangerous functions can be omitted) — but it is wired as an EXTENSION POINT (WithFuncs / WithSproutRegistries), NOT a baseline import, so the core htmltmpl package stays stdlib-only and apps opt into sprout explicitly. By default we exclude the env/expandenv/os/network registries to avoid the sprig-class SSTI/secrets-leak vector for any app that renders partially-trusted templates. Net: dep_module_path is stdlib; sprout is a documented opt-in add-on, not a required dependency of the module.

## Alternatives considered

| Option | Pros | Cons | Why not chosen |
|---|---|---|---|
| a-h/templ (github.com/a-h/templ, v0.3.x, actively maintained May 2026) | — | The catalog hint, but it is a build-time transpiler, not a runtime library. It requires a `templ generate` CLI step that | not chosen |
| Masterminds/sprig v3 (github.com/Masterminds/sprig/v3, v3.3.0, Aug 2024) | — | Effectively unmaintained: last release Aug 2024, README states it only targets Go 1.13, Snyk classifies it Inactive, no  | not chosen |
| go-task/slim-sprig (github.com/go-task/slim-sprig/v3) | — | A maintained slimmed fork, but it exists to serve ginkgo's internal needs and deliberately strips many functions; it's n | not chosen |
| valyala/quicktemplate / flosch/pongo2 / CloudyKit/jet | — | All are alternative template *engines* with their own syntax. quicktemplate is codegen (same problem as templ); pongo2 ( | not chosen |

## Consequences

See `htmltmpl/AGENTS.md` for the resulting API + config surface. The dependency is pinned and tracked by Renovate; revisit if it goes unmaintained or a better-fit library appears.
