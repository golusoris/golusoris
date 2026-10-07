#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
fixture=$(mktemp -d)
trap 'find "$fixture" -depth -delete' EXIT

mkdir -p "$fixture/.agents/skills" "$fixture/.agents/plugins/praetor/skills"
install -m 0644 "$repo_root/.agents/plugins/praetor/plugin.json" \
  "$fixture/.agents/plugins/praetor/plugin.json"

for skill in adhd-format caveman social-text; do
  mkdir -p "$fixture/.agents/skills/$skill" \
    "$fixture/.agents/plugins/praetor/skills/$skill"
  install -m 0644 "$repo_root/.agents/skills/$skill/SKILL.md" \
    "$fixture/.agents/skills/$skill/SKILL.md"
  install -m 0644 "$repo_root/.agents/skills/$skill/SKILL.md" \
    "$fixture/.agents/plugins/praetor/skills/$skill/SKILL.md"
done

bash "$repo_root/scripts/ci/text-register-policy.sh" "$fixture" >/dev/null

find "$fixture/.agents/skills/caveman/SKILL.md" -delete
if bash "$repo_root/scripts/ci/text-register-policy.sh" "$fixture" >/dev/null 2>&1; then
  printf '%s\n' 'missing canonical skill passed policy' >&2
  exit 1
fi
install -m 0644 "$repo_root/.agents/skills/caveman/SKILL.md" \
  "$fixture/.agents/skills/caveman/SKILL.md"

printf '%s\n' 'drift' >>"$fixture/.agents/plugins/praetor/skills/caveman/SKILL.md"
if bash "$repo_root/scripts/ci/text-register-policy.sh" "$fixture" >/dev/null 2>&1; then
  printf '%s\n' 'drifted projection passed policy' >&2
  exit 1
fi
install -m 0644 "$repo_root/.agents/skills/caveman/SKILL.md" \
  "$fixture/.agents/plugins/praetor/skills/caveman/SKILL.md"

adhd_reference=$'\x60adhd-format\x60'
social_skill="$fixture/.agents/skills/social-text/SKILL.md"
filtered_skill="$fixture/social-text.filtered"
awk -v needle="$adhd_reference" 'index($0, needle) == 0' "$social_skill" >"$filtered_skill"
mv "$filtered_skill" "$social_skill"
install -m 0644 "$fixture/.agents/skills/social-text/SKILL.md" \
  "$fixture/.agents/plugins/praetor/skills/social-text/SKILL.md"
if bash "$repo_root/scripts/ci/text-register-policy.sh" "$fixture" >/dev/null 2>&1; then
  printf '%s\n' 'missing ADHD dependency passed policy' >&2
  exit 1
fi

printf '%s\n' 'text-register policy negative controls: pass'
