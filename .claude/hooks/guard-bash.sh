#!/usr/bin/env bash
# PreToolUse hook for the Bash tool.
#
# Reads a JSON envelope on stdin (see Claude Code hook schema), inspects
# the .tool_input.command string, and blocks a short list of commands
# that are trivially-wrong for this repo. Exit code 2 = block; stderr is
# shown to the agent as the rejection reason.
#
# Keep this list tight. Every entry should cost zero to a legitimate
# workflow while closing a real footgun.
#
# Wired from .claude/settings.json under PreToolUse / matcher: "Bash".

set -euo pipefail

payload=$(cat)

# jq is available on every CI image; fall back to grep if it isn't local.
if command -v jq >/dev/null 2>&1; then
  cmd=$(printf '%s' "$payload" | jq -r '.tool_input.command // ""')
else
  cmd=$(printf '%s' "$payload" | grep -oE '"command":"[^"]*"' | head -n1 | sed 's/^"command":"//; s/"$//')
fi

[ -z "$cmd" ] && exit 0

deny() {
  printf 'blocked by .claude/hooks/guard-bash.sh: %s\n' "$1" >&2
  exit 2
}

# ── git safety ──────────────────────────────────────────────────────────
# Never skip commit/push hooks. Pre-commit runs lint+sec+test; bypassing
# it is how broken code reaches main.
case "$cmd" in
  *--no-verify*)        deny "'--no-verify' disables pre-commit/push hooks. Fix the failure instead." ;;
  *--no-gpg-sign*)      deny "'--no-gpg-sign' bypasses commit signing. Not allowed in this repo." ;;
esac

# Force-push to main/master — the one push that can rewrite shipped tags.
# force-with-lease is still force in spirit when the target is main.
if printf '%s' "$cmd" | grep -Eq 'git[[:space:]]+push[[:space:]].*--force(-with-lease)?\b.*\b(origin[[:space:]]+)?(main|master)\b'; then
  deny "force-push to main/master is blocked. Create a PR or revert-commit instead."
fi

# Destructive resets of shipped refs.
if printf '%s' "$cmd" | grep -Eq 'git[[:space:]]+reset[[:space:]].*--hard[[:space:]]+(origin/)?(main|master)\b'; then
  deny "'git reset --hard' on main/master will drop commits. Confirm with the user first."
fi

# ── filesystem safety ──────────────────────────────────────────────────
# rm -rf against repo-critical paths. Matches standalone tokens only so
# 'rm -rf ./coverage.out' stays allowed.
if printf '%s' "$cmd" | grep -Eq '\brm[[:space:]]+-[A-Za-z]*r[A-Za-z]*f?[A-Za-z]*[[:space:]]+(\./)?\.git(/[^[:space:]]*)?(\s|$)'; then
  deny "'rm -rf .git*' nukes the repo. Not allowed."
fi
if printf '%s' "$cmd" | grep -Eq '\brm[[:space:]]+-[A-Za-z]*r[A-Za-z]*f?[A-Za-z]*[[:space:]]+(\./)?\.workingdir(/|\s|$)'; then
  deny "'rm -rf .workingdir' destroys plan + state. Not allowed."
fi

exit 0
