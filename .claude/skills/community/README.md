---
name: community-skills
description: Skills cherry-picked from third-party community repos. Permissive licenses only (MIT / Apache-2.0). Each subdir is a self-contained SKILL.md vendored verbatim from upstream.
---

# Community skills (vendored, permissive licenses only)

These skills come from third-party repos surfaced in <https://github.com/BehiSecc/awesome-claude-skills>. Each was chosen for fit with golusoris conventions (security-forward Go web framework with explicit §2.5 compliance anchors). **Only MIT- and Apache-2.0-licensed content is vendored.** Anthropics' official `skills` collection is excluded because its license forbids copying and derivative works.

We plan to move these into a dedicated skills repo once the collection grows — this is a staging area, not a permanent home.

## Skills

| Skill | Source | License | Use when |
| --- | --- | --- | --- |
| `owasp-security` | [agamm/claude-code-owasp](https://github.com/agamm/claude-code-owasp) | MIT | Reviewing code for vulns, auth/authz, user-input handling. Covers OWASP Top 10:2025, ASVS 5.0, Agentic AI security 2026. Direct match for PLAN §2.5. |
| `vibesec` | [BehiSecc/VibeSec-Skill](https://github.com/BehiSecc/VibeSec-Skill) | Apache-2.0 | Writing a web endpoint, form handler, or anything touching request/response flow. Bug-hunter framing with defense-in-depth checklists. |
| `skill-check` | [olgasafonova/SkillCheck-Free](https://github.com/olgasafonova/SkillCheck-Free) | MIT | Validating a new SKILL.md against Anthropic's skill guidelines and the agentskills spec (30+ structural + semantic checks). |
| `git-pushing` | [mhattingpete/claude-skills-marketplace](https://github.com/mhattingpete/claude-skills-marketplace) | Apache-2.0 | Automating git operations: branch creation, commit scoping, remote pushes. |
| `review-implementing` | [mhattingpete/claude-skills-marketplace](https://github.com/mhattingpete/claude-skills-marketplace) | Apache-2.0 | Evaluating whether an implementation plan matches the spec before writing code. |
| `test-fixing` | [mhattingpete/claude-skills-marketplace](https://github.com/mhattingpete/claude-skills-marketplace) | Apache-2.0 | Diagnosing failing tests and proposing targeted patches. |

## Why only MIT / Apache-2.0

- **MIT** permits copying + modification with attribution — satisfied by this README + retaining each upstream repo's SKILL.md front-matter.
- **Apache-2.0** permits the same plus requires NOTICE retention and stating modifications. These files are vendored **verbatim** (no modifications); the per-skill row above is the attribution notice.
- **CC-BY-SA-4.0** (e.g., trailofbits/skills) requires share-alike relicensing, which would impose a license on golusoris that doesn't match the rest of the repo. Skipped.
- **Anthropics' proprietary license** (anthropics/skills) explicitly forbids "Reproduce or copy these materials" and "Create derivative works". Skipped entirely — use the skills via claude.ai if needed, don't copy them.

## Related first-party material

- [`.claude/skills/superpowers/`](../superpowers/) — process skills vendored from [obra/superpowers](https://github.com/obra/superpowers) (MIT).
- Sibling top-level skills (`add-migration`, `wire-fx-module`, `add-river-worker`, etc.) — first-party golusoris skills.

## Divergence policy

Files here are copies, not submodules. We don't auto-track upstream. When refreshing from upstream, note the commit SHA in the commit message so drift is auditable.
