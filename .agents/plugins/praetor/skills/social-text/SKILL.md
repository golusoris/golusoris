---
name: social-text
description: Write forge-facing issues, pull requests, reviews, commit bodies, and release notes as concise human prose. Use for social-register output requiring BLUF, scannability, and repository-valid conventions.
---

# Forge social register (`social-text`)

Use for human-readable forge text: issue, pull-request body, review comment, commit body,
release note. Policy source = `register:` in `.standards.yaml` and Text Register in
AGENTS.md. Agent traffic uses `internal`; documentation uses `docs`.

## Inherited from `adhd-format`

1. **Bottom line first.** Opening sentence = decision, defect, or ask.
2. **Scannable structure.** Paragraph <= 3 sentences. Action bullet starts with bold
   operative words.
3. **Progressive disclosure.** Summary first; commands second; detail linked or collapsed.

Human-facing overrides: <= 1 GitHub alert per text. No Mermaid unless flow = change point.
Tables only for 3+ rows. No emoji headings. No anchor bolding inside running prose.

## Voice

- Full sentences. Plain words. Reader can act without opening diff.
- One idea per paragraph or bullet. Add `file:line` when reader needs code location.
- State change, reason, verification, requested decision; then stop. PR summary target <= 250
  words before evidence links.
- No meta-commentary, hedge filler, or code restatement.
- Paraphrase operator neutrally. Never quote colloquial wording verbatim.
- No tool attribution: no model/tool `Co-Authored-By`, no generated-with footer. Remove such
  footer when editing existing text.

## Evidence

Link evidence; never paste beyond `register.evidence` bound: 58 lines / 1500 tokens by
default. Use `evidence: <path> sha256:<12 hex> lines:<n>` for local artifacts. Signed
receipt remains inline only when repository template explicitly requests one.

## Golusoris contracts

- **Pull request.** Fill `.github/PULL_REQUEST_TEMPLATE.md` exactly. Write 1-3 bullets under
  `## Summary`; preserve type, checklist, migration, and reference sections. No receipt
  fence exists.
- **Commit.** Subject = `type(scope): subject`; type =
  `feat | fix | docs | style | refactor | perf | test | build | ci | chore | revert`.
  `scripts/hooks/commit-msg.sh` requires Conventional Commit subject and `Signed-off-by`.
  Breaking change adds `!`, `BREAKING CHANGE:`, and `Migration:` with before/after Go.
  Body leads with reason, then change, wrapped near 72 columns.
- **Release note.** Never edit `CHANGELOG.md`; no `changelog.d/`. `release-please` derives
  entries from Conventional Commits.
- **Issue.** Title = defect. Body = observed, expected, reproduction, evidence. Search open
  issues and pull requests before filing.
- **Review.** One finding per comment. Severity first, then `file:line`, impact, fix. Address
  code, never author.
