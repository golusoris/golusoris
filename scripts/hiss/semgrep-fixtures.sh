#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# HISS-20 enforcement-coverage replay: proves that .semgrep.yml actually
# decides the invariants the coverage catalogue claims, in BOTH directions.
#
#   .config/hiss/testdata/HISS-NN/go/positive/*.go  MUST each produce >= 1 finding
#   .config/hiss/testdata/HISS-NN/go/negative/*.go  MUST each produce 0 findings
#
# A claim is only as good as the fixture that demonstrates it: a rule that stops
# firing, and a rule that starts over-matching the legitimate shape, both fail
# here rather than silently degrading the catalogue.
#
# The corpus is scanned from a temporary copy because the rules exclude
# .config/hiss/testdata/ — the positive fixtures are violations by construction
# and must not fail the repository's own semgrep gate. It runs single-job: the
# corpus is a couple of dozen tiny files, so a worker pool costs more than it
# saves, and semgrep's multi-core backend needs an io_uring queue that a
# memlock-limited container cannot always allocate.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
corpus="$root/.config/hiss/testdata"

# SEMGREP_CMD lets a caller point at a pinned interpreter that is not on PATH,
# e.g. SEMGREP_CMD="pipx run semgrep==1.166.0" (the pin CI's semgrep job uses).
read -r -a semgrep_cmd <<<"${SEMGREP_CMD:-semgrep}"
if ! command -v "${semgrep_cmd[0]}" >/dev/null 2>&1; then
  echo "${semgrep_cmd[0]} is not on PATH — skipping the HISS fixture replay (CI runs it pinned)" >&2
  exit 0
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
    if len(parts) < 4 or parts[2] not in ("positive", "negative"):
        continue
    checked += 1
    if fixture.resolve() not in scanned:
        failures.append(f"{'/'.join(parts)}: never scanned by semgrep")
        continue
    fired = sorted(hits.get(fixture.resolve(), set()))
    if parts[2] == "positive" and not fired:
        failures.append(f"{'/'.join(parts)}: expected a finding, got none")
    if parts[2] == "negative" and fired:
        failures.append(f"{'/'.join(parts)}: expected no finding, got {fired}")

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
