#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-actionlint-policy.XXXXXX")"
readonly suite_root
trap 'find "$suite_root" -depth -delete' EXIT

new_repo() {
	local name="$1"
	local root="$suite_root/$name"
	mkdir -p "$root/scripts/ci" "$root/tools" "$root/.github/workflows" \
		"$root/.gitea/workflows"
	install -m 0755 "$repo_root/scripts/ci/actionlint.sh" \
		"$root/scripts/ci/actionlint.sh"
	install -m 0644 "$repo_root/tools/tool-versions.env" \
		"$root/tools/tool-versions.env"
	install -m 0644 "$repo_root/.github/actionlint.yaml" \
		"$root/.github/actionlint.yaml"
	git -C "$root" init -q
	printf '%s\n' '.workingdir/' >"$root/.gitignore"
	printf '%s\n' "$root"
}

expect_failure() {
	local pattern="$1"
	shift
	local output
	local status=0
	output="$("$@" 2>&1)" || status=$?
	if (( status == 0 )); then
		printf 'expected failure: %s\n' "$*" >&2
		return 1
	fi
	if [[ "$output" != *"$pattern"* ]]; then
		printf 'missing failure %q in: %s\n' "$pattern" "$output" >&2
		return 1
	fi
}

check_rebuild_trigger_contract() {
	local path="$1"
	if ! grep -Fq '  workflow_call:' "$path" || \
		! grep -Fq '      image-name:' "$path" || \
		! grep -Fq '        required: true' "$path"; then
		printf 'rebuild workflow lacks required workflow_call image input\n' >&2
		return 1
	fi
	if grep -Eq '^  (repository_dispatch|workflow_dispatch):' "$path"; then
		printf 'rebuild workflow must only expose workflow_call inputs\n' >&2
		return 1
	fi
}

check_scorecard_policy_contract() {
	local workflow="$1"
	local ruleset="$2"
	python3 - "$workflow" "$ruleset" <<'PY'
import json
import pathlib
import re
import sys

workflow = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
ruleset = json.loads(pathlib.Path(sys.argv[2]).read_text(encoding="utf-8"))

match = re.search(r"(?ms)^on:\n(?P<body>.*?)(?=^permissions:)", workflow)
if match is None:
    raise SystemExit("scorecard workflow lacks bounded on block")
triggers = match.group("body")
if re.search(r"(?m)^  pull_request:\n    branches: \[main\]$", triggers) is None:
    raise SystemExit("scorecard workflow lacks pull_request main trigger")
if re.search(r"(?m)^  push:\n    branches: \[main\]$", triggers) is None:
    raise SystemExit("scorecard workflow lacks push main trigger")
if re.search(r"(?m)^  schedule:$", triggers) is None:
    raise SystemExit("scorecard workflow lacks schedule trigger")
if "publish_results: ${{ github.event_name != 'pull_request' }}" not in workflow:
    raise SystemExit("scorecard PR trigger lacks publish guard")

job = re.search(
    r"(?ms)^  analysis:\n(?P<body>.*?)(?=^  [A-Za-z0-9_-]+:\n|\Z)", workflow
)
if job is None:
    raise SystemExit("scorecard workflow lacks analysis job")
name = re.search(r"(?m)^    name: (?P<name>[^\n]+)$", job.group("body"))
if name is None or name.group("name") != "Scorecard Analysis":
    raise SystemExit("scorecard workflow must emit Scorecard Analysis")

contexts = []
for rule in ruleset.get("rules", []):
    if rule.get("type") != "required_status_checks":
        continue
    checks = rule.get("parameters", {}).get("required_status_checks", [])
    contexts.extend(check.get("context") for check in checks)
if contexts.count("Scorecard Analysis") != 1:
    raise SystemExit("ruleset must require Scorecard Analysis exactly once")
PY
}

check_workflow_job_timeouts() {
	local root="$1"
	python3 - "$root" <<'PY'
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
paths = sorted((root / ".github" / "workflows").glob("*.y*ml"))
if not paths or len(paths) > 128:
    raise SystemExit(f"workflow timeout scan requires 1..128 files, got {len(paths)}")

missing = []
job_pattern = re.compile(r"^  ([A-Za-z0-9_-]+):\s*$")
for path in paths:
    lines = path.read_text(encoding="utf-8").splitlines()
    try:
        jobs_index = lines.index("jobs:")
    except ValueError:
        continue
    starts = [
        index
        for index in range(jobs_index + 1, len(lines))
        if job_pattern.match(lines[index])
    ]
    if len(starts) > 128:
        raise SystemExit(f"workflow job bound exceeded: {path}")
    for position, start in enumerate(starts):
        end = starts[position + 1] if position + 1 < len(starts) else len(lines)
        block = lines[start + 1 : end]
        if any(re.match(r"^    uses:\s*", line) for line in block):
            continue
        if not any(re.match(r"^    timeout-minutes: [1-9][0-9]*$", line) for line in block):
            name = job_pattern.match(lines[start]).group(1)
            missing.append(f"{path.relative_to(root)}:{name}")

if missing:
    raise SystemExit("workflow jobs lack timeout-minutes: " + ", ".join(missing))
PY
}

check_go_cache_writer_contract() {
	local workflow="$1"
	python3 - "$workflow" <<'PY'
import pathlib
import re
import sys

path = pathlib.Path(sys.argv[1])
lines = path.read_text(encoding="utf-8").splitlines()
job_pattern = re.compile(r"^  ([A-Za-z0-9_-]+):\s*$")
starts = [index for index, line in enumerate(lines) if job_pattern.match(line)]
jobs = {}
for position, start in enumerate(starts):
    end = starts[position + 1] if position + 1 < len(starts) else len(lines)
    jobs[job_pattern.match(lines[start]).group(1)] = lines[start:end]

cache_jobs = []
for name, block in jobs.items():
    for index, line in enumerate(block):
        if "uses: actions/setup-go@" not in line:
            continue
        end = index + 1
        while end < len(block) and not block[end].startswith("      - "):
            end += 1
        step = block[index:end]
        if "          cache: true" not in step:
            continue
        cache_jobs.append(name)
        if "          cache-dependency-path: '**/go.sum'" not in step:
            raise SystemExit(f"setup-go cache user {name} does not use the all-module key")

if cache_jobs.count("build") != 1:
    raise SystemExit("build must be the sole setup-go cache writer")
for name in cache_jobs:
    if name == "build":
        continue
    if "    needs: build" not in jobs[name]:
        raise SystemExit(f"cache reader {name} does not depend on build")

build = jobs["build"]
timeout = next(
    (
        int(match.group(1))
        for line in build
        if (match := re.match(r"^    timeout-minutes: ([1-9][0-9]*)$", line))
    ),
    0,
)
if timeout < 60:
    raise SystemExit("cache writer build timeout must cover the measured upload bound")
if "        run: scripts/ci/go-modules.sh verify" not in build:
    raise SystemExit("cache writer build does not verify every module before saving")
PY
}

check_reusable_go_cache_writer_contract() {
	local workflow="$1"
	check_reusable_go_cache_users "$workflow" || return
	check_reusable_go_cache_writer_timeout "$workflow"
}

# check_reusable_go_cache_users requires every setup-go cache user to share one key and to read
# only after the build job wrote it.
check_reusable_go_cache_users() {
	local workflow="$1"
	python3 - "$workflow" <<'PY'
import pathlib
import re
import sys

path = pathlib.Path(sys.argv[1])
lines = path.read_text(encoding="utf-8").splitlines()
job_pattern = re.compile(r"^  ([A-Za-z0-9_-]+):\s*$")
starts = [index for index, line in enumerate(lines) if job_pattern.match(line)]
jobs = {}
for position, start in enumerate(starts):
    end = starts[position + 1] if position + 1 < len(starts) else len(lines)
    jobs[job_pattern.match(lines[start]).group(1)] = lines[start:end]

cache_jobs = {}
for name, block in jobs.items():
    for index, line in enumerate(block):
        if "uses: actions/setup-go@" not in line:
            continue
        end = index + 1
        while end < len(block) and not block[end].startswith("      - "):
            end += 1
        step = block[index:end]
        if "          cache: true" not in step:
            continue
        dependency_path = next(
            (
                line
                for line in step
                if line.startswith("          cache-dependency-path:")
            ),
            "",
        )
        if not dependency_path:
            raise SystemExit(f"setup-go cache user {name} lacks a dependency path")
        if name in cache_jobs:
            raise SystemExit(f"reusable setup-go cache user {name} is duplicated")
        cache_jobs[name] = dependency_path

expected = {"build", "lint", "gosec", "vuln", "test", "apidiff"}
if set(cache_jobs) != expected:
    raise SystemExit(
        f"reusable setup-go cache users changed: {sorted(cache_jobs)}, expected {sorted(expected)}"
    )
if len(set(cache_jobs.values())) != 1:
    raise SystemExit("reusable setup-go cache users do not share one cache key")
for name in sorted(expected - {"build"}):
    if "    needs: build" not in jobs[name]:
        raise SystemExit(f"reusable cache reader {name} does not depend on build")
PY
}

check_reusable_go_cache_writer_timeout() {
	local workflow="$1"
	python3 - "$workflow" <<'PY'
import pathlib
import re
import sys

path = pathlib.Path(sys.argv[1])
lines = path.read_text(encoding="utf-8").splitlines()
job_pattern = re.compile(r"^  ([A-Za-z0-9_-]+):\s*$")
starts = [index for index, line in enumerate(lines) if job_pattern.match(line)]
jobs = {}
for position, start in enumerate(starts):
    end = starts[position + 1] if position + 1 < len(starts) else len(lines)
    jobs[job_pattern.match(lines[start]).group(1)] = lines[start:end]

build = jobs["build"]
timeout = next(
    (
        int(match.group(1))
        for line in build
        if (match := re.match(r"^    timeout-minutes: ([1-9][0-9]*)$", line))
    ),
    0,
)
if timeout < 60:
    raise SystemExit("reusable cache writer build timeout must cover cache upload")
PY
}

check_ryuk_reconnection_contract() {
	local primary_workflow="$1"
	local reusable_workflow="$2"
	python3 - "$primary_workflow" "$reusable_workflow" <<'PY'
import pathlib
import sys

contracts = (
    (pathlib.Path(sys.argv[1]), "Run primary-module tests with race detector and coverage"),
    (pathlib.Path(sys.argv[2]), "Run tests (Linux — full suite)"),
)
for path, step_name in contracts:
    lines = path.read_text(encoding="utf-8").splitlines()
    marker = f"      - name: {step_name}"
    try:
        start = lines.index(marker)
    except ValueError as error:
        raise SystemExit(f"{path}: missing test step {step_name}") from error
    end = next(
        (
            index
            for index in range(start + 1, len(lines))
            if lines[index].startswith("      - ")
        ),
        len(lines),
    )
    step = "\n".join(lines[start:end])
    if step.count("          RYUK_RECONNECTION_TIMEOUT: 30m") != 1:
        raise SystemExit(f"{path}: Ryuk reconnection timeout must be 30m")
    if "TESTCONTAINERS_RYUK_RECONNECTION_TIMEOUT" in step:
        raise SystemExit(f"{path}: Ryuk timeout must use the supported bare variable")
    for upstream in (
        "testcontainers/testcontainers-go#3867",
        "testcontainers/testcontainers-go#3868",
    ):
        if upstream not in step:
            raise SystemExit(f"{path}: Ryuk mitigation lacks upstream reference {upstream}")
PY
}

write_clean_workflow() {
	local path="$1"
	cat >"$path" <<'EOF'
name: Clean
on:
  workflow_dispatch: {}
permissions:
  contents: read
jobs:
  test:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - run: echo clean
EOF
}

write_bad_workflow() {
	local path="$1"
	cat >"$path" <<'EOF'
name: Broken
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: echo '${{ unknown }}'
EOF
}

clean_root="$(new_repo clean)"
write_clean_workflow "$clean_root/.github/workflows/clean.yml"
write_clean_workflow "$clean_root/.gitea/workflows/clean.yml"
git -C "$clean_root" add .
bash "$clean_root/scripts/ci/actionlint.sh" --root "$clean_root" >/dev/null

github_root="$(new_repo github-negative)"
write_bad_workflow "$github_root/.github/workflows/bad.yml"
git -C "$github_root" add .
expect_failure 'undefined variable "unknown"' \
	bash "$github_root/scripts/ci/actionlint.sh" --root "$github_root"

gitea_root="$(new_repo gitea-negative)"
write_bad_workflow "$gitea_root/.gitea/workflows/bad.yml"
git -C "$gitea_root" add .
expect_failure '.gitea/workflows/bad.yml' \
	bash "$gitea_root/scripts/ci/actionlint.sh" --root "$gitea_root"

untracked_root="$(new_repo untracked)"
write_clean_workflow "$untracked_root/.github/workflows/clean.yml"
git -C "$untracked_root" add .
write_bad_workflow "$untracked_root/.gitea/workflows/untracked.yml"
expect_failure '.gitea/workflows/untracked.yml' \
	bash "$untracked_root/scripts/ci/actionlint.sh" --root "$untracked_root"

symlink_root="$(new_repo symlink)"
write_clean_workflow "$suite_root/outside.yml"
ln -s "$suite_root/outside.yml" "$symlink_root/.github/workflows/external.yml"
git -C "$symlink_root" add .
expect_failure 'actionlint refuses symlink input' \
	bash "$symlink_root/scripts/ci/actionlint.sh" --root "$symlink_root"

wrong_version_root="$(new_repo wrong-version)"
write_clean_workflow "$wrong_version_root/.github/workflows/clean.yml"
git -C "$wrong_version_root" add .
cat >"$suite_root/actionlint-wrong" <<'EOF'
#!/usr/bin/env bash
printf 'v0.0.0\n'
EOF
chmod +x "$suite_root/actionlint-wrong"
expect_failure 'actionlint version is v0.0.0' env ACTIONLINT_BIN="$suite_root/actionlint-wrong" \
	bash "$wrong_version_root/scripts/ci/actionlint.sh" --root "$wrong_version_root"

failure_root="$(new_repo scanner-failure)"
write_clean_workflow "$failure_root/.github/workflows/clean.yml"
git -C "$failure_root" add .
cat >"$suite_root/actionlint-failure" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == '-version' ]]; then
  printf 'v1.7.12\n'
  exit 0
fi
printf '.github/workflows/clean.yml: scanner failure\n' >&2
exit 9
EOF
chmod +x "$suite_root/actionlint-failure"
expect_failure '.github/workflows/clean.yml' env ACTIONLINT_BIN="$suite_root/actionlint-failure" \
	bash "$failure_root/scripts/ci/actionlint.sh" --root "$failure_root"

expect_failure 'undefined variable "unknown"' actionlint -no-color \
	-config-file "$repo_root/.github/actionlint.yaml" \
	"$repo_root/.config/hiss/testdata/HISS-10/workflow-yaml/positive/undefined-expression.yml"
actionlint -no-color -config-file "$repo_root/.github/actionlint.yaml" \
	"$repo_root/.config/hiss/testdata/HISS-10/workflow-yaml/negative/clean-workflow.yml" \
	"$repo_root/.config/hiss/testdata/HISS-10/workflow-yaml/gap/unpinned-action.yml"

rebuild_workflow="$repo_root/.github/workflows/rebuild-on-base.yml"
check_rebuild_trigger_contract "$rebuild_workflow"
rebuild_dispatch_negative="$suite_root/rebuild-dispatch.yml"
sed '/^on:$/a\  repository_dispatch:\n    types: [base-image-updated]' \
	"$rebuild_workflow" >"$rebuild_dispatch_negative"
expect_failure 'must only expose workflow_call inputs' \
	check_rebuild_trigger_contract "$rebuild_dispatch_negative"

check_scorecard_policy_contract "$repo_root/.github/workflows/scorecard.yml" \
	"$repo_root/.github/rulesets/main.json"

scorecard_pr_negative="$suite_root/scorecard-no-pr.yml"
python3 - "$repo_root/.github/workflows/scorecard.yml" "$scorecard_pr_negative" <<'PY'
import pathlib
import re
import sys

source = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
mutated, count = re.subn(
    r"(?m)^  pull_request:\n    branches: \[main\]\n",
    "",
    source,
    count=1,
)
if count != 1:
    raise SystemExit("scorecard pull_request mutation failed")
pathlib.Path(sys.argv[2]).write_text(mutated, encoding="utf-8")
PY
expect_failure 'scorecard workflow lacks pull_request main trigger' \
	check_scorecard_policy_contract "$scorecard_pr_negative" \
	"$repo_root/.github/rulesets/main.json"

scorecard_name_negative="$suite_root/scorecard-renamed-job.yml"
sed 's/^    name: Scorecard Analysis$/    name: Scorecard Security/' \
	"$repo_root/.github/workflows/scorecard.yml" >"$scorecard_name_negative"
expect_failure 'scorecard workflow must emit Scorecard Analysis' \
	check_scorecard_policy_contract "$scorecard_name_negative" \
	"$repo_root/.github/rulesets/main.json"

scorecard_ruleset_negative="$suite_root/ruleset-no-scorecard.json"
python3 - "$repo_root/.github/rulesets/main.json" "$scorecard_ruleset_negative" <<'PY'
import json
import pathlib
import sys

source = pathlib.Path(sys.argv[1])
ruleset = json.loads(source.read_text(encoding="utf-8"))
for rule in ruleset["rules"]:
    if rule.get("type") != "required_status_checks":
        continue
    checks = rule["parameters"]["required_status_checks"]
    rule["parameters"]["required_status_checks"] = [
        check for check in checks if check.get("context") != "Scorecard Analysis"
    ]
pathlib.Path(sys.argv[2]).write_text(json.dumps(ruleset), encoding="utf-8")
PY
expect_failure 'ruleset must require Scorecard Analysis exactly once' \
	check_scorecard_policy_contract "$repo_root/.github/workflows/scorecard.yml" \
	"$scorecard_ruleset_negative"

check_workflow_job_timeouts "$repo_root"
check_go_cache_writer_contract "$repo_root/.github/workflows/ci.yml"
check_reusable_go_cache_writer_contract "$repo_root/.github/workflows/ci-go.yml"
check_ryuk_reconnection_contract "$repo_root/.github/workflows/ci.yml" \
	"$repo_root/.github/workflows/ci-go.yml"

timeout_negative_root="$(new_repo timeout-negative)"
write_clean_workflow "$timeout_negative_root/.github/workflows/no-timeout.yml"
sed -i '/^[[:space:]]*timeout-minutes:/d' \
	"$timeout_negative_root/.github/workflows/no-timeout.yml"
expect_failure 'workflow jobs lack timeout-minutes' \
	check_workflow_job_timeouts "$timeout_negative_root"

cache_reader_negative="$suite_root/ci-cache-reader-without-writer.yml"
python3 - "$repo_root/.github/workflows/ci.yml" "$cache_reader_negative" <<'PY'
import pathlib
import sys

source = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
marker = "    needs: build\n"
if marker not in source:
    raise SystemExit("cache-reader dependency mutation failed")
pathlib.Path(sys.argv[2]).write_text(source.replace(marker, "", 1), encoding="utf-8")
PY
expect_failure 'cache reader lint does not depend on build' \
	check_go_cache_writer_contract "$cache_reader_negative"

cache_key_negative="$suite_root/ci-cache-key-drift.yml"
python3 - "$repo_root/.github/workflows/ci.yml" "$cache_key_negative" <<'PY'
import pathlib
import sys

source = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
marker = "          cache-dependency-path: '**/go.sum'"
index = source.rfind(marker)
if index < 0:
    raise SystemExit("apidiff cache-key mutation failed")
pathlib.Path(sys.argv[2]).write_text(
    source[:index] + source[index + len(marker) :], encoding="utf-8"
)
PY
expect_failure 'setup-go cache user apidiff does not use the all-module key' \
	check_go_cache_writer_contract "$cache_key_negative"

cache_warm_negative="$suite_root/ci-cache-without-module-warm.yml"
sed '/run: scripts\/ci\/go-modules.sh verify/d' \
	"$repo_root/.github/workflows/ci.yml" >"$cache_warm_negative"
expect_failure 'cache writer build does not verify every module before saving' \
	check_go_cache_writer_contract "$cache_warm_negative"

reusable_cache_reader_negative="$suite_root/ci-go-cache-reader-without-writer.yml"
python3 - "$repo_root/.github/workflows/ci-go.yml" "$reusable_cache_reader_negative" <<'PY'
import pathlib
import sys

source = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
marker = "    needs: build\n"
if marker not in source:
    raise SystemExit("reusable cache-reader dependency mutation failed")
pathlib.Path(sys.argv[2]).write_text(source.replace(marker, "", 1), encoding="utf-8")
PY
expect_failure 'reusable cache reader lint does not depend on build' \
	check_reusable_go_cache_writer_contract "$reusable_cache_reader_negative"

reusable_cache_key_negative="$suite_root/ci-go-cache-key-drift.yml"
python3 - "$repo_root/.github/workflows/ci-go.yml" "$reusable_cache_key_negative" <<'PY'
import pathlib
import sys

source = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
marker = "          cache-dependency-path: ${{ inputs.working-directory != '.' && format('{0}/go.sum', inputs.working-directory) || '' }}"
if source.count(marker) != 6:
    raise SystemExit("reusable cache-key mutation requires six exact markers")
pathlib.Path(sys.argv[2]).write_text(
    source.replace(marker, "          cache-dependency-path: go.sum", 1),
    encoding="utf-8",
)
PY
expect_failure 'reusable setup-go cache users do not share one cache key' \
	check_reusable_go_cache_writer_contract "$reusable_cache_key_negative"

reusable_cache_duplicate_negative="$suite_root/ci-go-cache-duplicate-setup.yml"
python3 - "$repo_root/.github/workflows/ci-go.yml" "$reusable_cache_duplicate_negative" <<'PY'
import pathlib
import sys

source = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
build_marker = "  build:\n"
build_start = source.find(build_marker)
if build_start < 0:
    raise SystemExit("reusable cache duplicate mutation lacks build job")
setup_marker = "      - uses: actions/setup-go@"
setup_start = source.find(setup_marker, build_start)
if setup_start < 0:
    raise SystemExit("reusable cache duplicate mutation lacks build setup-go")
duplicate = (
    "      - uses: actions/setup-go@duplicate-cache-fixture\n"
    "        with:\n"
    "          cache: true\n"
    "          cache-dependency-path: divergent-go.sum\n"
)
pathlib.Path(sys.argv[2]).write_text(
    source[:setup_start] + duplicate + source[setup_start:], encoding="utf-8"
)
PY
expect_failure 'reusable setup-go cache user build is duplicated' \
	check_reusable_go_cache_writer_contract "$reusable_cache_duplicate_negative"

reusable_cache_timeout_negative="$suite_root/ci-go-cache-writer-short-timeout.yml"
python3 - "$repo_root/.github/workflows/ci-go.yml" "$reusable_cache_timeout_negative" <<'PY'
import pathlib
import sys

source = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
marker = "  build:\n    name: Build\n"
if marker not in source:
    raise SystemExit("reusable cache-writer job mutation failed")
build_start = source.index(marker)
timeout = "    timeout-minutes: 60\n"
timeout_index = source.find(timeout, build_start)
if timeout_index < 0:
    raise SystemExit("reusable cache-writer timeout mutation failed")
pathlib.Path(sys.argv[2]).write_text(
    source[:timeout_index]
    + "    timeout-minutes: 59\n"
    + source[timeout_index + len(timeout) :],
    encoding="utf-8",
)
PY
expect_failure 'reusable cache writer build timeout must cover cache upload' \
	check_reusable_go_cache_writer_contract "$reusable_cache_timeout_negative"

ryuk_timeout_negative="$suite_root/ci-go-ryuk-timeout-drift.yml"
python3 - "$repo_root/.github/workflows/ci-go.yml" "$ryuk_timeout_negative" <<'PY'
import pathlib
import sys

source = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
marker = "          RYUK_RECONNECTION_TIMEOUT: 30m"
if source.count(marker) != 1:
    raise SystemExit("Ryuk timeout mutation requires exactly one reusable-workflow marker")
pathlib.Path(sys.argv[2]).write_text(
    source.replace(marker, "          RYUK_RECONNECTION_TIMEOUT: 31m", 1),
    encoding="utf-8",
)
PY
expect_failure 'Ryuk reconnection timeout must be 30m' \
	check_ryuk_reconnection_contract "$repo_root/.github/workflows/ci.yml" \
	"$ryuk_timeout_negative"

printf 'actionlint workflow policy tests passed\n'
