#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# HISS-20 enforcement-coverage replay: proves that .semgrep.yml actually
# decides the invariants the coverage catalogue claims, in BOTH directions.
#
#   .config/hiss/semgrep/testdata/HISS-NN/go/positive/*.go  MUST report, from a rule
#                                                   this file maps to HISS-NN
#   .config/hiss/semgrep/testdata/HISS-NN/go/negative/*.go  MUST stay silent — the
#                                                   legitimate shape
#   .config/hiss/semgrep/testdata/HISS-NN/go/gap/*.go       MUST stay silent — a
#                                                   violating shape the rule
#                                                   declares it does not decide
#
# A claim is only as good as the fixture that demonstrates it: a rule that stops
# firing, a rule that starts over-matching the legitimate shape, and a declared
# gap that has quietly closed all fail here rather than silently degrading the
# catalogue. The gap direction is what keeps the rule comments honest — the day
# a gap fixture starts reporting, the comment that declared it must be corrected.
#
# The corpus is scanned from a temporary copy because the rules exclude
# .config/hiss/semgrep/testdata/ — the positive fixtures are violations by
# construction and must not fail the repository's own semgrep gate, nor the
# registry-ruleset gate in .github/workflows/security-scan.yml, which excludes
# the same directory. It runs single-job: the corpus is a couple of dozen tiny
# files, so a worker pool costs more than it saves, and semgrep's multi-core
# backend needs an io_uring queue that a memlock-limited container cannot always
# allocate.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
corpus="$root/.config/hiss/semgrep/testdata"

# SEMGREP_CMD lets a caller point at a pinned interpreter that is not on PATH,
# e.g. SEMGREP_CMD="pipx run semgrep==1.166.0" (the pin CI's semgrep job uses).
read -r -a semgrep_cmd <<<"${SEMGREP_CMD:-semgrep}"
if ! command -v "${semgrep_cmd[0]}" >/dev/null 2>&1; then
  # Hard failure on purpose: this script is a member of `make verify-all`, and a
  # gate that passes because its tool is missing reports a green tree it never
  # inspected. HISS_FIXTURES_OPTIONAL=1 is the explicit, visible opt-out.
  if [[ "${HISS_FIXTURES_OPTIONAL:-0}" == "1" ]]; then
    echo "${semgrep_cmd[0]} is not on PATH — HISS_FIXTURES_OPTIONAL=1, skipping the fixture replay" >&2
    exit 0
  fi
  echo "${semgrep_cmd[0]} is not on PATH: the HISS fixture replay cannot run." >&2
  echo "Install semgrep (pipx install semgrep), point SEMGREP_CMD at it, or set" >&2
  echo "HISS_FIXTURES_OPTIONAL=1 to skip this gate deliberately." >&2
  exit 1
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cp -R "$corpus/." "$work/"

report="$work/findings.json"
"${semgrep_cmd[@]}" scan \
  --config "$root/.semgrep.yml" \
  --metrics=off \
  --disable-version-check \
  --oss-only \
  --no-git-ignore \
  --jobs 1 \
  --json \
  --output "$report" \
  "$work" >/dev/null 2>&1 || true

python3 - "$work" "$report" <<'PY'
import json
import pathlib
import sys

# Which .semgrep.yml rules are allowed to satisfy a positive fixture, per
# invariant directory. A positive fixture that only trips some unrelated rule
# proves nothing about the invariant it is filed under, so it fails. Add the
# entry when a new HISS-NN directory appears — an unmapped directory is a
# failure, not a pass.
RULES = {
    "HISS-06": {"no-unbounded-goroutine-in-loop"},
    "HISS-08": {"no-dynamic-code-loading", "no-dynamic-exec-command"},
    "HISS-09": {"unsafe-requires-safety-proof"},
}
CATEGORIES = ("positive", "negative", "gap")

work, report = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])
payload = json.loads(report.read_text(encoding="utf-8"))
if payload.get("errors"):
    print("semgrep reported errors:", payload["errors"], file=sys.stderr)
    sys.exit(1)

scanned = {pathlib.Path(p).resolve() for p in payload.get("paths", {}).get("scanned", [])}
hits: dict[pathlib.Path, set[str]] = {}
for item in payload.get("results", []):
    hits.setdefault(pathlib.Path(item["path"]).resolve(), set()).add(
        item["check_id"].rsplit(".", 1)[-1]
    )

failures, checked = [], 0
for fixture in sorted(work.rglob("*.go")):
    parts = fixture.relative_to(work).parts
    if len(parts) < 4 or parts[2] not in CATEGORIES:
        continue
    checked += 1
    name, invariant, category = "/".join(parts), parts[0], parts[2]
    if invariant not in RULES:
        failures.append(f"{name}: {invariant} has no rule mapping in this script")
        continue
    if fixture.resolve() not in scanned:
        failures.append(f"{name}: never scanned by semgrep")
        continue
    fired = sorted(hits.get(fixture.resolve(), set()))
    if category == "positive" and not (RULES[invariant] & set(fired)):
        expected = ", ".join(sorted(RULES[invariant]))
        failures.append(f"{name}: expected a finding from [{expected}], got {fired or 'none'}")
    if category == "negative" and fired:
        failures.append(f"{name}: expected no finding on the legitimate shape, got {fired}")
    if category == "gap" and fired:
        failures.append(
            f"{name}: the declared gap now reports {fired} — the rule gained coverage; "
            "move the fixture to positive/ and correct the claim that declared it"
        )

if not checked:
    print("no fixtures found — the corpus is empty", file=sys.stderr)
    sys.exit(1)
for line in failures:
    print(line, file=sys.stderr)
if failures:
    print(f"HISS fixture replay: {len(failures)}/{checked} fixtures disagree with .semgrep.yml", file=sys.stderr)
    sys.exit(1)
print(f"HISS fixture replay: {checked} fixtures agree with .semgrep.yml")
PY
