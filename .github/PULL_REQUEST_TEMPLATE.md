# Pull Request

## Summary

<!-- 1-3 bullets: what changed and why. Reference the issue with "Closes #NNN" if applicable. -->

-

## Type of change

<!-- Delete lines that don't apply. -->

- [ ] Bug fix (non-breaking)
- [ ] New module / feature (non-breaking)
- [ ] Breaking change (public API change — add `BREAKING CHANGE:` footer to commit + `Migration:` section below)
- [ ] Refactor (behaviour unchanged)
- [ ] Docs / AGENTS.md / ADR
- [ ] CI / tooling / DX

## Checklist

- [ ] `make ci` passes locally (0 lint · 0 gosec · 0 govulncheck · race-green)
- [ ] New packages have an `AGENTS.md`
- [ ] New modules expose an `fx.Module` and have an `fxtest.New` smoke test
- [ ] New dependencies are justified against awesome-go alternatives (PR comment or ADR)
- [ ] ADR added to `docs/adr/` if this records an architecture decision
- [ ] `docs/architecture/container.puml` updated if a new top-level package landed
- [ ] `.workingdir/STATE.md` session log updated
- [ ] `README.md` "Landed so far" updated (if a PLAN step completes)
- [ ] `AGENTS.md` layout tree updated (if a new top-level package was added)

## Migration (breaking changes only)

```go
// Before

// After
```

## References

<!-- ADRs, upstream docs, related PRs. -->
