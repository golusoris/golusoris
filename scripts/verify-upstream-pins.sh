#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly root
readonly catalogue="$root/docs/upstream/README.md"
readonly max_rows=64
readonly command_timeout=60s

# shellcheck source=/dev/null
. "$root/tools/tool-versions.env"

declare -a failures=()
declare -a rows=()
declare -A seen_packages=()
declare -A seen_snapshots=()

record_failure() {
  failures+=("$1")
}

module_version() {
  local graph="$1"
  local module="$2"
  awk -F '\t' -v module="$module" '$1 == module { print $2; found = 1; exit } END { if (!found) exit 1 }' <<<"$graph"
}

check_module() {
  local graph="$1"
  local module="$2"
  local expected="$3"
  local authority="$4"
  local actual

  if ! actual="$(module_version "$graph" "$module")"; then
    record_failure "$module: absent from $authority module graph"
    return
  fi
  if [[ "$actual" != "$expected" ]]; then
    record_failure "$module: catalogue $expected, $authority module graph $actual"
  fi
}

check_snapshot() {
  local snapshot_file="$1"
  local expected="$2"
  local heading
  local pinned
  local source

  if [[ ! -f "$snapshot_file" ]]; then
    record_failure "${snapshot_file#"$root/"}: snapshot is missing"
    return
  fi
  heading="$(awk '/^# / { print; exit }' "$snapshot_file")"
  pinned="$(awk '/^Pinned: \*\*/ { sub(/^Pinned: \*\*/, ""); sub(/\*\*.*/, ""); print; exit }' "$snapshot_file")"
  source="$(awk '/^Source: / { print; exit }' "$snapshot_file")"

  if [[ "$heading" != *"$expected"* ]]; then
    record_failure "${snapshot_file#"$root/"}: heading does not contain $expected"
  fi
  if [[ "$pinned" != "$expected" ]]; then
    record_failure "${snapshot_file#"$root/"}: Pinned is ${pinned:-missing}, want $expected"
  fi
  if [[ "$source" != *"$expected"* ]]; then
    record_failure "${snapshot_file#"$root/"}: Source does not identify $expected"
  fi
}

check_sqlc() {
  local module="$1"
  local expected="$2"
  local binary
  local binary_package
  local actual

  if [[ "$expected" != "$SQLC_VERSION" ]]; then
    record_failure "$module: catalogue $expected, repository tool pin $SQLC_VERSION"
  fi
  if [[ -z "${SQLC_BIN:-}" ]]; then
    return
  fi
  binary="$SQLC_BIN"
  if [[ ! -x "$binary" ]]; then
    record_failure "$module: SQLC_BIN is not executable: $binary"
    return
  fi
  binary_package="$(timeout "$command_timeout" go version -m "$binary" | awk '$1 == "path" { print $2; exit }')"
  actual="$(timeout "$command_timeout" go version -m "$binary" | awk -v module="$module" '$1 == "mod" && $2 == module { print $3; exit }')"
  if [[ "$binary_package" != "$module/cmd/sqlc" ]]; then
    record_failure "$module: installed binary package is ${binary_package:-unknown}"
  fi
  if [[ "$actual" != "$expected" ]]; then
    record_failure "$module: catalogue $expected, installed binary ${actual:-unknown}"
  fi
}

check_scalar() {
  local expected="$1"
  local plain="${expected#v}"
  local actual

  if [[ ! -f "$root/apidocs/embed/SCALAR_VERSION" || ! -f "$root/apidocs/embed/scalar.js" ]]; then
    record_failure "@scalar/api-reference: embedded version authority is missing"
    return
  fi
  actual="$(tr -d '\r\n' < "$root/apidocs/embed/SCALAR_VERSION")"
  if [[ "$actual" != "$plain" ]]; then
    record_failure "@scalar/api-reference: catalogue $expected, SCALAR_VERSION ${actual:-missing}"
  fi
  if ! grep -Fq "@scalar/api-reference $plain" "$root/apidocs/embed/scalar.js"; then
    record_failure "@scalar/api-reference: scalar.js header does not identify $plain"
  fi
}

check_go() {
  local expected="$1"
  local root_go
  local core_go
  local goroot
  local source_go
  local runtime_go

  root_go="go$(awk '$1 == "go" { print $2; exit }' "$root/go.mod")"
  core_go="go$(awk '$1 == "go" { print $2; exit }' "$root/core/go.mod")"
  goroot="$(timeout "$command_timeout" go env GOROOT)"
  IFS= read -r source_go < "$goroot/VERSION"
  runtime_go="$(timeout "$command_timeout" go env GOVERSION)"

  [[ "$root_go" == "$expected" ]] || record_failure "log/slog: catalogue $expected, root go.mod $root_go"
  [[ "$core_go" == "$expected" ]] || record_failure "log/slog: catalogue $expected, core/go.mod $core_go"
  [[ "$source_go" == "$expected" ]] || record_failure "log/slog: catalogue $expected, GOROOT/VERSION $source_go"
  if [[ "$runtime_go" != "$expected" && "$runtime_go" != "$expected"-* ]]; then
    record_failure "log/slog: catalogue $expected, go env GOVERSION $runtime_go"
  fi
}

mapfile -t rows < <(
  awk -F '|' '
    function trim(value) {
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
      return value
    }
    /^\| `/ {
      package = trim($2)
      version = trim($3)
      authority = trim($4)
      snapshot = trim($5)
      gsub(/`/, "", package)
      gsub(/`/, "", version)
      sub(/^.*\]\(/, "", snapshot)
      sub(/\).*$/, "", snapshot)
      sub(/README\.md$/, "", snapshot)
      printf "%s\t%s\t%s\t%s\n", package, version, authority, snapshot
    }
  ' "$catalogue"
)

readonly row_count="${#rows[@]}"
if (( row_count == 0 )); then
  echo "upstream pin verification: catalogue has no mapped rows" >&2
  exit 1
fi
if (( row_count > max_rows )); then
  echo "upstream pin verification: $row_count rows exceed bound $max_rows" >&2
  exit 1
fi

root_graph="$(timeout "$command_timeout" go -C "$root" list -mod=readonly -m -f $'{{.Path}}\t{{.Version}}' all)"
readonly root_graph
core_graph="$(timeout "$command_timeout" go -C "$root/core" list -mod=readonly -m -f $'{{.Path}}\t{{.Version}}' all)"
readonly core_graph

for ((row_index = 0; row_index < row_count && row_index < max_rows; row_index++)); do
  IFS=$'\t' read -r package version authority snapshot <<<"${rows[$row_index]}"
  if [[ -z "$package" || -z "$version" || -z "$authority" || ! "$snapshot" =~ ^[a-z0-9][a-z0-9-]*/$ ]]; then
    record_failure "catalogue row $((row_index + 1)): malformed mapping"
    continue
  fi
  if [[ -n "${seen_packages[$package]+present}" ]]; then
    record_failure "$package: duplicate catalogue row"
  fi
  if [[ -n "${seen_snapshots[$snapshot]+present}" ]]; then
    record_failure "$snapshot: duplicate snapshot mapping"
  fi
  seen_packages[$package]=1
  seen_snapshots[$snapshot]=1
  check_snapshot "$root/docs/upstream/${snapshot}README.md" "$version"

  case "$authority" in
    root)
      check_module "$root_graph" "$package" "$version" root
      ;;
    "root + core")
      check_module "$root_graph" "$package" "$version" root
      check_module "$core_graph" "$package" "$version" core
      ;;
    "repository tool pin")
      if [[ "$package" != "github.com/sqlc-dev/sqlc" ]]; then
        record_failure "$package: repository tool authority must name github.com/sqlc-dev/sqlc"
      else
        check_sqlc "$package" "$version"
      fi
      ;;
    "embedded asset")
      if [[ "$package" != "@scalar/api-reference" ]]; then
        record_failure "$package: embedded asset authority must name @scalar/api-reference"
      else
        check_scalar "$version"
      fi
      ;;
    "root + core + toolchain")
      if [[ "$package" != "log/slog" ]]; then
        record_failure "$package: toolchain authority must name log/slog"
      else
        check_go "$version"
      fi
      ;;
    *)
      record_failure "$package: unknown authority '$authority'"
      ;;
  esac
done

snapshot_files=("$root"/docs/upstream/*/README.md)
readonly snapshot_count="${#snapshot_files[@]}"
if (( snapshot_count > max_rows )); then
  record_failure "snapshot count $snapshot_count exceeds bound $max_rows"
else
  for ((snapshot_index = 0; snapshot_index < snapshot_count && snapshot_index < max_rows; snapshot_index++)); do
    snapshot_dir="$(basename "$(dirname "${snapshot_files[$snapshot_index]}")")/"
    if [[ -z "${seen_snapshots[$snapshot_dir]+present}" ]]; then
      record_failure "docs/upstream/$snapshot_dir: snapshot has no catalogue mapping"
    fi
  done
fi

if (( ${#failures[@]} > 0 )); then
  for ((failure_index = 0; failure_index < ${#failures[@]} && failure_index < max_rows; failure_index++)); do
    echo "upstream pin verification: ${failures[$failure_index]}" >&2
  done
  exit 1
fi

echo "upstream pin verification: $row_count catalogue rows agree with their authorities and snapshots"
