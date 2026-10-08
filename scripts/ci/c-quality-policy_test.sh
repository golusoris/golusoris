#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-c-quality-policy.XXXXXX")"
readonly suite_root
trap 'find "$suite_root" -depth -delete' EXIT

headers="$(GOFLAGS=-mod=readonly go -C "$repo_root" list -m -f '{{.Dir}}/examples/headers' github.com/cilium/ebpf)"
readonly headers

new_repo() {
	local name="$1"
	local root="$suite_root/$name"
	mkdir -p "$root/scripts/ci" "$root/tools"
	install -m 0755 "$repo_root/scripts/ci/c-quality.sh" "$root/scripts/ci/c-quality.sh"
	install -m 0644 "$repo_root/tools/tool-versions.env" "$root/tools/tool-versions.env"
	install -m 0644 "$repo_root/.clang-tidy" "$root/.clang-tidy"
	git -C "$root" init -q
	printf '%s\n' '.workingdir/' >"$root/.gitignore"
	printf '%s\n' "$root"
}

expect_failure() {
	local pattern="$1"
	shift
	local output status=0
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

clean_root="$(new_repo clean)"
install -m 0644 "$repo_root/.config/hiss/testdata/HISS-10/c/negative/clean.c" "$clean_root/clean.c"
git -C "$clean_root" add .
CILIUM_EBPF_HEADERS="$headers" bash "$clean_root/scripts/ci/c-quality.sh" --root "$clean_root" >/dev/null

positive_root="$(new_repo positive)"
install -m 0644 "$repo_root/.config/hiss/testdata/HISS-10/c/positive/unused-variable.c" "$positive_root/bad.c"
git -C "$positive_root" add .
expect_failure 'unused variable' env CILIUM_EBPF_HEADERS="$headers" \
	bash "$positive_root/scripts/ci/c-quality.sh" --root "$positive_root"

gap_root="$(new_repo gap)"
install -m 0644 "$repo_root/.config/hiss/testdata/HISS-10/c/gap/suppressed-unused-variable.c" "$gap_root/gap.c"
git -C "$gap_root" add .
CILIUM_EBPF_HEADERS="$headers" bash "$gap_root/scripts/ci/c-quality.sh" --root "$gap_root" >/dev/null

untracked_root="$(new_repo untracked)"
install -m 0644 "$repo_root/.config/hiss/testdata/HISS-10/c/negative/clean.c" "$untracked_root/clean.c"
git -C "$untracked_root" add .
install -m 0644 "$repo_root/.config/hiss/testdata/HISS-10/c/positive/unused-variable.c" "$untracked_root/untracked.c"
expect_failure 'untracked.c' env CILIUM_EBPF_HEADERS="$headers" \
	bash "$untracked_root/scripts/ci/c-quality.sh" --root "$untracked_root"

fixture_root="$(new_repo fixtures)"
install -m 0644 "$repo_root/.config/hiss/testdata/HISS-10/c/negative/clean.c" "$fixture_root/clean.c"
mkdir -p "$fixture_root/.config/hiss/testdata/HISS-10/c/positive"
install -m 0644 "$repo_root/.config/hiss/testdata/HISS-10/c/positive/unused-variable.c" \
	"$fixture_root/.config/hiss/testdata/HISS-10/c/positive/bad.c"
git -C "$fixture_root" add .
CILIUM_EBPF_HEADERS="$headers" bash "$fixture_root/scripts/ci/c-quality.sh" --root "$fixture_root" >/dev/null

symlink_root="$(new_repo symlink)"
install -m 0644 "$repo_root/.config/hiss/testdata/HISS-10/c/negative/clean.c" "$suite_root/outside.c"
ln -s "$suite_root/outside.c" "$symlink_root/external.c"
git -C "$symlink_root" add .
expect_failure 'Clang refuses symlink input' env CILIUM_EBPF_HEADERS="$headers" \
	bash "$symlink_root/scripts/ci/c-quality.sh" --root "$symlink_root"

parity_root="$(new_repo object-parity)"
mkdir -p "$parity_root/pkg/example"
printf 'int handler(void) { return 0; }\n' >"$parity_root/pkg/example/program.bpf.c"
printf 'stale-object\n' >"$parity_root/pkg/example/program.bpf.o"
git -C "$parity_root" add .
writer="$suite_root/docker-writer"
# The generated fake expands these values when the wrapper invokes it.
# shellcheck disable=SC2016 # Generated fake expands these variables only when invoked.
printf '%s\n' \
	'#!/usr/bin/env bash' \
	'set -euo pipefail' \
	'out_dir=' \
	'for argument in "$@"; do' \
	'  case "$argument" in *:/out:rw) out_dir=${argument%:/out:rw} ;; esac' \
	'done' \
	'output=${!#}' \
	'printf "reproducible-object\\n" >"${out_dir:?}/${output}"' >"$writer"
chmod +x "$writer"
expect_failure 'eBPF object differs' env DOCKER_BIN="$writer" CILIUM_EBPF_HEADERS="$headers" \
	bash "$parity_root/scripts/ci/c-quality.sh" --root "$parity_root"
DOCKER_BIN="$writer" CILIUM_EBPF_HEADERS="$headers" \
	bash "$parity_root/scripts/ci/c-quality.sh" --root "$parity_root" --write >/dev/null
DOCKER_BIN="$writer" CILIUM_EBPF_HEADERS="$headers" \
	bash "$parity_root/scripts/ci/c-quality.sh" --root "$parity_root" >/dev/null
grep -Fq 'reproducible-object' "$parity_root/pkg/example/program.bpf.o"

fake="$suite_root/docker"
fake_log="$suite_root/docker.log"
# The generated fake expands its own argv and log path.
# shellcheck disable=SC2016 # Generated fake expands its argv and log path only when invoked.
printf '#!/usr/bin/env bash\nprintf "%%s\\n" "$*" >"${FAKE_LOG:?}"\nexit 9\n' >"$fake"
chmod +x "$fake"
expect_failure '' env DOCKER_BIN="$fake" FAKE_LOG="$fake_log" CILIUM_EBPF_HEADERS="$headers" \
	bash "$clean_root/scripts/ci/c-quality.sh" --root "$clean_root"
for required in \
	'--platform linux/amd64' \
	'--network none' \
	'--read-only' \
	'--cap-drop ALL' \
	'silkeh/clang:22-bookworm@sha256:8a78ee07659814ef70353d7f67b742ca0ed714366d42782813799136596e6e68'; do
	if ! grep -Fq -- "$required" "$fake_log"; then
		printf 'Clang container contract lacks: %s\n' "$required" >&2
		exit 1
	fi
done

printf 'Clang/clang-tidy full-tree policy tests passed\n'
