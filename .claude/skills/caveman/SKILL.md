---
name: caveman
description: Write internal agent briefs, returns, fan-outs, and tool notes as compact factual fragments. Use for internal-register text requiring exact paths, commands, errors, identifiers, and bounded evidence.
---

# Caveman internal register (`caveman`)

`internal` form from `register:` in `.standards.yaml` and Text Register in AGENTS.md.
Every token crosses agent hops again -> keep facts, cut grammar.

## Scope

Apply to agent briefs, task prompts, returns, workflow results, research fan-outs, tool-call
notes. Human forge text -> `social-text`. `docs/`, README, ADR, and operator reply -> full
prose. Never use caveman for human-facing text.

## Rules

1. **Cut fact-free grammar.** Drop articles, pronouns, copulas, auxiliaries, hedges,
   politeness, and framing. Never drop meaning or negation.
2. **Use fragments.** One fact per line. Lists over sentences.
3. **Replace connective prose with symbols.** `->` = cause/next; `=` = identity/meaning;
   `x2` = count; `!` = risk; `?` = open question.

   | Symbol | Example |
   | :--- | :--- |
   | `->` | `stale receipt -> push rejected` |
   | `=` | `block = 15 lines` |
   | `x2` | `retry x3` |
   | `!` | `! gate not rerun after rebase` |
   | `?` | `? Windows path untested` |
4. **Keep tokens verbatim.** Code, paths, `file:line`, commands, flags, error text, IDs,
   SHAs, versions, and numbers never change. Never compress English into invented forms
   such as `cfg ldr` or `ctx cmp`.
5. **Start with answer.** No brief restatement, step recap, or closing line. Verdict first;
   evidence next.
6. **Use tables only** for > 3 rows comparing > 2 fields. Otherwise use list.
7. **Return fixed fields.** verdict, changed paths, commands, evidence pointers, open
   questions. No extra narrative.
8. **Bound evidence.** Above 58 lines or 1500 tokens, save under `.workingdir/evidence/` and
   return `evidence: <path> sha256:<12 hex> lines:<n>`. Never paste long logs. Manifest can
   tighten limits, never widen them.

## Clarity floor

Shorter text must preserve every actionable fact. Reader never guesses file, count,
failure origin, bypass status, or next step. Keep negation explicit: `no bypass`, `not rerun`.
Run `praetorctl caveman floor <before> <after>` after any policy rewrite.

## Examples

Push return:

After: `push rejected x2: state stale (gate run wrote receipt after sync). fix: resync, push. no bypass.`

```text
push rejected x2: state stale (gate run wrote receipt after sync). fix: resync, push. no bypass.
```

Test return:

```text
verdict: pass
changed: internal/compiler/register.go (missing end marker -> error)
ran: go test -race -count=1 ./internal/compiler/ = pass; go vet = clean
```

Dedupe return:

Too far: `dedupe: 2 clones.`

After: `dedupe scan: 2 clones. 1 new (this change), 1 pre-existing on main (internal/milestone), left as is.`

```text
dedupe scan: 2 clones. 1 new (this change), 1 pre-existing on main (internal/milestone), left as is.
```
