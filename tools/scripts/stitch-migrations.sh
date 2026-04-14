#!/usr/bin/env bash
# Stitch `Migration:` commit footers between two refs into
# docs/migrations/<to>.md. See PLAN.md §10.
#
# Usage:
#   tools/scripts/stitch-migrations.sh <from-ref> <to-ref>
#   tools/scripts/stitch-migrations.sh v0.1.0 v0.2.0
#
# Output: docs/migrations/<to-ref>.md (overwritten). Empty file if no
# migrations found — callers can skip publishing on zero-byte result.

set -euo pipefail

if [ $# -ne 2 ]; then
  echo "usage: $0 <from-ref> <to-ref>" >&2
  exit 2
fi

FROM="$1"
TO="$2"
OUT="docs/migrations/${TO}.md"

mkdir -p "$(dirname "$OUT")"

{
  printf '# Migration notes — %s\n\n' "$TO"
  printf 'Generated from `Migration:` commit footers between `%s` and `%s`.\n\n' "$FROM" "$TO"

  # Each commit body is separated by \x00. We emit only commits whose body
  # contains a Migration: footer block.
  git log --format='%x00%H%n%s%n%b' "${FROM}..${TO}" \
    | awk -v RS='\0' '
      /(^|\n)Migration:/ {
        # first line is SHA, second is subject, rest is body
        n = split($0, lines, "\n")
        sha = lines[1]; subj = lines[2]
        printf("## %s\n\n_commit `%s`_\n\n", subj, substr(sha, 1, 12))
        capture = 0
        for (i = 3; i <= n; i++) {
          if (lines[i] ~ /^Migration:/) { capture = 1; continue }
          if (capture && lines[i] ~ /^[A-Za-z-]+:/) { capture = 0 }
          if (capture) print lines[i]
        }
        print ""
      }
    '
} > "$OUT"

# If nothing was stitched (only the header + blank lines), truncate so
# release automation can detect the empty case.
if [ "$(grep -c '^## ' "$OUT" || true)" = 0 ]; then
  : > "$OUT"
  echo "no Migration: footers between ${FROM} and ${TO}"
else
  echo "wrote $OUT"
fi
