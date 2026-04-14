# Contributing

## Getting started

```sh
git clone https://github.com/<org>/<repo>
cd <repo>
cp .env.example .env
docker compose -f tools/docker-compose.dev.yml up -d
go run ./cmd/<app>
```

## Rules

- Follow [Conventional Commits](https://www.conventionalcommits.org/) for commit messages and PR titles.
- Every commit must pass `make ci` (lint + gosec + govulncheck + race tests).
- Breaking changes require a `Migration:` footer with before/after Go snippets.
- New dependencies must justify the choice over awesome-go alternatives in the PR description.
- `//nolint` directives require an inline justification comment.

## Branching

Trunk-based development: branch off `main`, open a PR, squash-merge.
No long-lived branches.

## Tests

```sh
make test          # unit + integration (requires Docker for testcontainers)
make test-race     # with -race
make cover         # coverage report
```

## Releasing

Handled automatically by release-please on merge to `main`.
Tag format: `vMAJOR.MINOR.PATCH`.
