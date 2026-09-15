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
```

`scripts/hiss/semgrep-fixtures.sh` (`make hiss-fixtures`, and a step in CI's
semgrep lane) replays the corpus against `.semgrep.yml` in **both** directions.
A rule that stops firing on its positive fixtures fails; so does a rule that
starts matching the legitimate shape its negative fixtures pin. The catalogue
therefore fails when it becomes too optimistic *and* when it becomes too
pessimistic.

## Conventions

- Fixtures are ordinary `.go` files in `package p`. They are never compiled:
  the Go tool skips directories beginning with `.` (and any `testdata`), so
  `go build ./...`, `go vet` and golangci-lint never see them.
- They are excluded from the repository's own semgrep gate by a
  `paths.exclude` entry on each rule — the positive fixtures are violations by
  construction and must not fail the gate. The replay script works around that
  by scanning a temporary copy.
- One shape per fixture, named after the shape (`plugin-open.go`,
  `semaphore-channel.go`), with a doc comment saying why it is or is not a
  violation.

## Covered today

| Rule | Invariant | semgrep rule id |
| --- | --- | --- |
| HISS-06 | Bounded Concurrency | `no-unbounded-goroutine-in-loop` |
| HISS-08 | Static Determinism | `no-dynamic-code-loading`, `no-dynamic-exec-command` |
| HISS-09 | Reference Safety | `unsafe-requires-safety-proof` |

Each rule's comment block in `.semgrep.yml` names the shapes it decides **and
the shapes it does not** — an absent claim is honest, an unbacked one is the
defect this mechanism exists to prevent.
