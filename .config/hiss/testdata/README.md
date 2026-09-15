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

`scripts/hiss/semgrep-fixtures.sh` (`make hiss-fixtures`, and a step in CI's
semgrep lane) replays the corpus against `.semgrep.yml` in all three
directions. A rule that stops firing on its positive fixtures fails; so does a
rule that starts matching the legitimate shape its negative fixtures pin; so
does a rule that quietly grows past a declared gap. The catalogue therefore
fails when it becomes too optimistic, when it becomes too pessimistic, **and**
when a documented limit stops being true — which is the only way a rule comment
that says "this shape is not decided" can stay honest.

A positive fixture must be reported by a rule the script maps to that
invariant, not merely by some rule: firing for an unrelated reason proves
nothing about the invariant the fixture is filed under. The map lives at the top
of the replay script, and an invariant directory with no entry there fails.

## Conventions

- Fixtures are ordinary `.go` files in `package p`. They are never compiled:
  the Go tool skips directories beginning with `.` (and any `testdata`), so
  `go build ./...`, `go vet` and golangci-lint never see them.
- They are excluded from the two semgrep gates that scan the repository itself:
  a `paths.exclude` entry on each rule in `.semgrep.yml`, and a
  `--exclude .config/hiss/testdata` on the registry-ruleset scan in
  `.github/workflows/security-scan.yml`. The positive and gap fixtures are
  violations by construction — `p/golang` and `p/security-audit` report the
  `unsafe` and `reflect.MakeFunc` ones just as readily as our own rules do — and
  that job gates. The replay works around its own exclusion by scanning a
  temporary copy.
- One shape per fixture, named after the shape (`plugin-open.go`,
  `semaphore-channel.go`), with a doc comment saying why it is or is not a
  violation, and — for a `gap/` fixture — what would have to change for the rule
  to decide it.

## Covered today

| Rule | Invariant | semgrep rule id |
| --- | --- | --- |
| HISS-06 | Bounded Concurrency | `no-unbounded-goroutine-in-loop` |
| HISS-08 | Static Determinism | `no-dynamic-code-loading`, `no-dynamic-exec-command` |
| HISS-09 | Reference Safety | `unsafe-requires-safety-proof` |

Each rule's comment block in `.semgrep.yml` names the shapes it decides **and
the shapes it does not** — an absent claim is honest, an unbacked one is the
defect this mechanism exists to prevent. The `gap/` fixtures are what hold the
second half of that comment to account.

No `.config/hiss/coverage.yaml` is shipped here yet, so `praetorctl hiss
coverage --verify` has nothing to consume: this corpus is exercised by
`scripts/hiss/semgrep-fixtures.sh` alone. The catalogue file that declares
per-rule, per-language states over these fixtures is a separate change.
