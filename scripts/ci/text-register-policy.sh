#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root=${1:-.}
canonical_root="$repo_root/.agents/skills"
plugin_root="$repo_root/.agents/plugins/praetor"
required=(adhd-format caveman social-text)

test -f "$plugin_root/plugin.json"

for skill in "${required[@]}"; do
  canonical="$canonical_root/$skill/SKILL.md"
  projection="$plugin_root/skills/$skill/SKILL.md"
  test -f "$canonical"
  test -f "$projection"
  cmp -s "$canonical" "$projection"
done

rg -q '^name: adhd-format$' "$canonical_root/adhd-format/SKILL.md"
rg -q '^name: caveman$' "$canonical_root/caveman/SKILL.md"
rg -q '^name: social-text$' "$canonical_root/social-text/SKILL.md"
rg -q '`adhd-format`' "$canonical_root/social-text/SKILL.md"

printf '%s\n' 'text-register policy: 3 canonical skills and projections verified'
