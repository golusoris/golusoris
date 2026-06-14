# `changelog.d/` — per-PR CHANGELOG fragment files

The `## [Unreleased]` block of [`CHANGELOG.md`](../CHANGELOG.md) is **rendered**
from fragment files, not edited directly. Each PR drops **one** file under the
Keep-a-Changelog section that matches its change:

```text
changelog.d/
  added/        new user-discoverable surface
  changed/      change to existing behaviour (perf goes here — prefix files perf-<topic>.md)
  deprecated/   surface marked for removal
  removed/      surface deleted
  fixed/        bug fix with user-visible delta
  security/     security-affecting change
```

## Why fragments

Editing `CHANGELOG.md` directly makes every concurrent module PR fight a merge
conflict on the same section-header lines. Fragment files are per-path, so two
PRs in flight never collide — **one fragment file per PR means zero
`CHANGELOG.md` merge conflicts.** release-please rolls the rendered Unreleased
block into a versioned section at release time.

## How to add a fragment

1. Pick the section directory matching your change (Keep-a-Changelog order:
   Added, Changed, Deprecated, Removed, Fixed, Security).
2. Create one file `changelog.d/<section>/<issue-or-slug>.md` — e.g.
   `changelog.d/added/93-changelog-fragments.md`. Filenames sort
   lexicographically inside the section, so an issue-number or task-id prefix
   gives implicit ordering.
3. Write a Markdown bullet (or a small block of bullets) — the same text you
   would have pasted into `CHANGELOG.md`. Do not add a `### Section` heading;
   the renderer emits it.
4. Run the renderer locally before pushing:

   ```sh
   scripts/changelog/render.sh --write   # splice fragments into CHANGELOG.md
   ```

   CI runs `scripts/changelog/render.sh --check` and fails on drift.

## How fragments render

[`scripts/changelog/render.sh`](../scripts/changelog/render.sh) concatenates the
`*.md` files in each section in lexical order under one `### Section` heading,
emitted in Keep-a-Changelog order. Sections with no fragments are skipped. The
rendered body replaces everything between the `## [Unreleased]` header and the
next release heading in `CHANGELOG.md`.

Notes:

- Fragments under a directory that is **not** a Keep-a-Changelog section are
  skipped (the renderer warns to stderr).
- Empty or whitespace-only fragments are skipped with a warning.
- A stray leading `#`/`##` header inside a fragment is demoted to a bold
  pseudo-header so it never collides with the release-heading splice boundary
  (`^## \[`).

The `.gitkeep` file in each section directory keeps the empty directory tracked
in git; it is ignored by the renderer.
