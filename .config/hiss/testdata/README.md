<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# `.config/hiss/testdata/` — enforcement-coverage corpus (HISS-20)

Every claim that golusoris *enforces* an invariant is only as good as the
fixture that demonstrates it. This tree is that demonstration, laid out the way
[praetor's own corpus](https://github.com/cordanaLLM/praetor) is:

```text
.config/hiss/testdata/HISS-NN/<language>/
  positive/   the violating shape — the rule MUST report it
  negative/   the legitimate shape — the rule MUST stay silent
  gap/        a violating shape the rule declares it does NOT decide — it MUST
              stay silent, and the day it fires the declaration is wrong
```

`praetorctl hiss coverage --verify` replays scanner claims. Delegated runner
claims use focused policy tests for each declared runner. A
rule that stops firing on its positive fixture fails; so does one that reports
its negative or grows past a declared gap. The catalogue then fails when it
overstates or understates coverage.

A positive fixture must be reported by its declared invariant, not merely by an
unrelated rule: an unrelated finding proves nothing about the claim.

## Conventions

- Go fixtures use `package p`. The Go tool skips dot-prefixed testdata, so
  build, vet, and golangci-lint never compile them.
- Shell and workflow-YAML fixtures stay outside the production selectors. Their
  policy tests invoke the pinned tools directly.
- Custom Semgrep fixtures live separately under
  `.config/hiss/semgrep/testdata/`; `scripts/hiss/semgrep-fixtures.sh` replays
  them from a temporary copy using the declared Semgrep image digest.
- One shape per fixture, named after the shape (`plugin-open.go`,
  `semaphore-channel.go`), with a doc comment saying why it is or is not a
  violation, and — for a `gap/` fixture — what would have to change for the rule
  to decide it.

## Custom Semgrep coverage

| Rule | Invariant | semgrep rule id |
| --- | --- | --- |
| HISS-06 | Bounded Concurrency | `no-unbounded-goroutine-in-loop` |
| HISS-08 | Static Determinism | `no-dynamic-code-loading`, `no-dynamic-exec-command` |
| HISS-09 | Reference Safety | `unsafe-requires-safety-proof` |

Each rule's comment block in `.semgrep.yml` names the shapes it decides **and
the shapes it does not** — an absent claim is honest, an unbacked one is the
defect this mechanism exists to prevent. The `gap/` fixtures are what hold the
second half of that comment to account.

## Delegated HISS-10 coverage

| Language | Runner | Replay |
| --- | --- | --- |
| Python | Ruff | `scripts/ci/python-lint-policy_test.sh` |
| C | Clang + clang-tidy | `scripts/ci/c-quality-policy_test.sh` |
| Markdown | `markdownlint-cli2` | `scripts/ci/markdownlint-policy_test.sh` |
| MkDocs Markdown | MkDocs strict build | `scripts/ci/mkdocs-build-policy_test.sh` |
| Shell | ShellCheck | `scripts/ci/shellcheck-policy_test.sh` |
| Workflow YAML | actionlint | `scripts/ci/actionlint-policy_test.sh` |
| HCL | Terraform | `scripts/ci/terraform-policy_test.sh` |
| Kubernetes YAML | kubeconform | `scripts/ci/kubeconform-policy_test.sh` |

`.config/hiss/coverage.yaml` declares scanner and delegated states.
`praetorctl hiss coverage --verify` verifies scanner claims and attribution;
each delegated policy test proves its tool's positive, negative, and gap
fixtures.

HISS-15 Python execution is replayed by
`scripts/ci/python-tests-policy_test.sh`; its passing positive-only gap records
that unittest execution cannot decide 3D test shape.

HISS-21 Go portability is replayed by `scripts/ci/portability_test.py` and the
three-OS workflow. Its gap records that a successful `go test -short` can still
contain only a stated platform skip; the driver prevents zero modules, packages,
or phases, but does not reinterpret Go test events.
