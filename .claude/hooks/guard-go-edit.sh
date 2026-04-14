#!/usr/bin/env bash
# PreToolUse hook for Edit / Write on *.go files.
#
# Enforces the project conventions that are otherwise caught only at
# `golangci-lint run` time — failing them at PreToolUse saves an edit
# cycle and surfaces the rule + fix inline.
#
# Rules, each keyed off the content that would land:
#   1) time.Now() is banned outside clock/. Use clock.Now(ctx) or
#      inject clockwork.Clock via fx.In. Reason: deterministic tests.
#   2) fmt.Print / Println / Printf are banned in production code.
#      Use the slog handler from log/. Reason: structured logs + OTel.
#   3) //nolint directives without a trailing comment are banned.
#      Every suppression must carry a one-line justification.
#
# Exempt paths: *_test.go, example_*.go, testutil/, examples/, docs/.
#
# Wired from .claude/settings.json under PreToolUse / matcher: "Edit|Write".

set -euo pipefail

payload=$(cat)

if ! command -v jq >/dev/null 2>&1; then
  # Without jq we can't reliably parse the JSON envelope; skip silently.
  exit 0
fi

file_path=$(printf '%s' "$payload" | jq -r '.tool_input.file_path // ""')
tool_name=$(printf '%s' "$payload" | jq -r '.tool_name // ""')

[ -z "$file_path" ] && exit 0
[[ "$file_path" != *.go ]] && exit 0

# Determine the content that would land, depending on tool.
#   Write: .tool_input.content
#   Edit : .tool_input.new_string
case "$tool_name" in
  Write) content=$(printf '%s' "$payload" | jq -r '.tool_input.content // ""') ;;
  Edit)  content=$(printf '%s' "$payload" | jq -r '.tool_input.new_string // ""') ;;
  *)     exit 0 ;;
esac

[ -z "$content" ] && exit 0

deny() {
  printf 'blocked by .claude/hooks/guard-go-edit.sh (%s):\n  %s\n' "$file_path" "$1" >&2
  exit 2
}

# ── path exemptions ────────────────────────────────────────────────────
case "$file_path" in
  *_test.go|*/example_*.go|*/testutil/*|*/examples/*|*/docs/*) exit 0 ;;
esac

# ── rule 1: time.Now() outside clock/ ──────────────────────────────────
if [[ "$file_path" != */clock/* && "$file_path" != clock/* ]]; then
  # Match `time.Now(` (call). Allow start-of-line or a non-identifier
  # char before `time` so we don't match e.g. `somepkgtime.Now`.
  if printf '%s' "$content" | grep -En '(^|[^a-zA-Z_.])time\.Now[[:space:]]*\(' >/dev/null; then
    deny "time.Now() outside clock/ is banned. Use clock.Now(ctx) or inject clockwork.Clock via fx.In. See clock/ for the API."
  fi
fi

# ── rule 2: fmt.Print / Println / Printf in production code ────────────
if printf '%s' "$content" | grep -En '(^|[^a-zA-Z_.])fmt\.(Print|Println|Printf)[[:space:]]*\(' >/dev/null; then
  deny "fmt.Print* is banned outside tests and examples. Use *slog.Logger from the log/ module (inject via fx.In)."
fi

# ── rule 3: //nolint without justification ─────────────────────────────
# Look for //nolint or //nolint:X,Y that ends the line (no "//" explainer
# after). golangci-lint's nolintlint enforces this in CI; catching it
# here saves the round trip.
if printf '%s' "$content" | grep -En '//\s*nolint(:[[:alnum:],_-]+)?\s*$' >/dev/null; then
  deny "//nolint needs a justification comment on the same line, e.g. '//nolint:errcheck // defer-close, error already surfaced'."
fi

exit 0
