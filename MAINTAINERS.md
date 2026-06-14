# Maintainers

Authoritative list of people with write access to golusoris and the subtrees
they own. Review routing follows the matching entries in
[`.github/CODEOWNERS`](.github/CODEOWNERS).

For the governance model these roles fit into, see
[`GOVERNANCE.md`](GOVERNANCE.md).

## Project lead (BDFL)

| Name    | GitHub                                  | Scope        |
|---------|-----------------------------------------|--------------|
| lusoris | [@lusoris](https://github.com/lusoris)  | All subtrees |

## Maintainers

Currently the BDFL is also the sole maintainer — every subtree maps to
[@lusoris](https://github.com/lusoris). As new maintainers come on, they will be
added here with the subtree(s) they own, and [`CODEOWNERS`](.github/CODEOWNERS)
will be updated in the same PR.

| Subtree                                            | Maintainer(s) | CODEOWNERS row                              |
|----------------------------------------------------|---------------|---------------------------------------------|
| Security-critical (`auth/`, `authz/`, `crypto/`, `secrets/`, `hash/`, `webhooks/`, `payments/`) | @lusoris | `/auth/`, `/authz/`, `/crypto/`, `/secrets/`, … |
| Core (`config/`, `log/`, `clock/`, `id/`, `validate/`, `errors/`) | @lusoris | `*` (root)                              |
| Data + jobs (`db/`, `jobs/`, `outbox/`, `cache/`)  | @lusoris      | `*` (root)                                  |
| HTTP + API (`httpx/`, `api/`, `grpc/`, `graphql/`) | @lusoris      | `*` (root)                                  |
| Observability (`otel/`, `log/`)                    | @lusoris      | `*` (root)                                  |
| CI / release / deps                                | @lusoris      | `/.github/`, `/tools/`, `/go.mod`, `/go.sum` |
| ADRs / compliance docs                             | @lusoris      | `/docs/adr/`, `/docs/architecture/`, `/SECURITY.md` |

## Becoming a maintainer

New maintainers are invited at the BDFL's discretion based on a track record of
high-quality contributions in a specific subtree (typically 3+ merged
non-trivial PRs reviewed without major rework). To propose adding a maintainer,
open a PR that:

1. Updates this `MAINTAINERS.md` with the new entry.
2. Updates [`.github/CODEOWNERS`](.github/CODEOWNERS) for the subtree(s) they
   will own.
3. Cites the contribution history justifying the addition.

The BDFL approves the PR and the new maintainer is granted write access.

## Stepping down

A maintainer may step down at any time via a PR that removes their entry from
this file and the corresponding `CODEOWNERS` rows. The PR is merged immediately;
no further approval is required.

## Inactive maintainers

A maintainer inactive for 12 months (no reviews, commits, or triage) may be
moved to an `## Inactive` section by BDFL action. They retain commit credit but
no longer block review routing, and can be reinstated by a PR moving their entry
back.

(No inactive maintainers at this time.)
