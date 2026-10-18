#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Contract test for the OpenAPI rulesets under vacuum: a complete spec passes both,
# and each planted defect fails with the rule id the ruleset promises.
# usage: openapi-ruleset-test.sh [ruleset-root]   (default: this checkout's tools/)

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
tools_dir="${1:-$repo_root/tools}"
readonly tools_dir
vacuum_bin="${VACUUM:-vacuum}"
readonly vacuum_bin
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-openapi-ruleset.XXXXXX")"
readonly work_dir
trap 'rm -rf "$work_dir"' EXIT

readonly default_ruleset="$tools_dir/spectral/oas.yaml"
readonly strict_ruleset="$tools_dir/spectral.yaml"
for ruleset in "$default_ruleset" "$strict_ruleset"; do
	if [[ ! -f "$ruleset" || -L "$ruleset" ]]; then
		printf 'OpenAPI ruleset is unavailable: %s\n' "$ruleset" >&2
		exit 1
	fi
done
if ! command -v "$vacuum_bin" >/dev/null; then
	printf 'vacuum executable is unavailable: %s\n' "$vacuum_bin" >&2
	exit 1
fi

cat >"$work_dir/good.json" <<'JSON'
{
  "openapi": "3.1.0",
  "info": {
    "title": "Ruleset contract",
    "version": "1.0.0",
    "description": "Validates the OpenAPI rulesets.",
    "contact": {"name": "Golusoris", "url": "https://example.invalid", "email": "maintainers@example.invalid"},
    "license": {"name": "EUPL-1.2", "identifier": "EUPL-1.2"}
  },
  "servers": [{"url": "https://api.example.invalid"}],
  "tags": [{"name": "items", "description": "Item operations."}],
  "paths": {
    "/items/{id}": {
      "get": {
        "summary": "Get an item",
        "description": "Returns one item.",
        "operationId": "getItem",
        "tags": ["items"],
        "parameters": [
          {"name": "id", "in": "path", "required": true, "description": "Item id.", "schema": {"type": "string"}}
        ],
        "responses": {
          "200": {
            "description": "The item.",
            "content": {
              "application/json": {
                "schema": {"$ref": "#/components/schemas/Item"},
                "example": {"id": "abc"}
              }
            }
          }
        }
      }
    }
  },
  "components": {
    "schemas": {
      "Item": {
        "type": "object",
        "description": "One item.",
        "required": ["id"],
        "properties": {"id": {"type": "string", "description": "Item id."}}
      }
    }
  }
}
JSON

readonly op='.paths["/items/{id}"].get'
# case name -> jq mutation of the good spec; the name is the rule id that must fire.
declare -A mutations=(
	[operation-summary]="del(${op}.summary)"
	[operation-description]="del(${op}.description)"
	[operation-operationId]="del(${op}.operationId)"
	[operation-tags]="del(${op}.tags)"
	[operation-operationId-unique]=".paths[\"/other\"].get = (${op} | .parameters = [])"
	[path-params]="${op}.parameters[0].name = \"other\""
	[no-\$ref-siblings]=".openapi = \"3.0.3\" | .info.license = {name: \"EUPL-1.2\", url: \"https://eupl.eu\"} | ${op}.responses[\"200\"].content[\"application/json\"].schema.description = \"sibling\""
	[oas3-valid-schema-example]="${op}.responses[\"200\"].content[\"application/json\"].example.id = 123"
	[info-contact]="del(.info.contact)"
	[info-license]="del(.info.license)"
	[info-description]="del(.info.description)"
	[oas3-server-trailing-slash]=".servers[0].url = \"https://api.example.invalid/\""
)
# spectral:oas leaves operation-summary and info-license off; only the strict ruleset adds them.
readonly default_cases=(
	operation-description operation-operationId operation-tags operation-operationId-unique
	path-params "no-\$ref-siblings" oas3-valid-schema-example info-contact info-description
	oas3-server-trailing-slash
)
readonly default_quiet=(operation-summary info-license)

# rules_fired SPEC RULESET: sorted rule ids vacuum reports.
rules_fired() {
	local report
	report="$("$vacuum_bin" spectral-report "$1" -r "$2" -o)"
	jq -r '.[].code' <<<"$report" | sort -u
}

# lint_status SPEC RULESET: vacuum's exit status at fail-severity warn.
lint_status() {
	local status=0
	"$vacuum_bin" lint "$1" -r "$2" -n warn -b -q -x >/dev/null 2>&1 || status=$?
	printf '%d' "$status"
}

failures=0
fail() {
	printf '%s\n' "$*" >&2
	failures=$((failures + 1))
}

for ruleset in "$default_ruleset" "$strict_ruleset"; do
	fired="$(rules_fired "$work_dir/good.json" "$ruleset")"
	[[ -z "$fired" ]] || fail "complete spec fires rules under ${ruleset##*/}: ${fired//$'\n'/ }"
	[[ "$(lint_status "$work_dir/good.json" "$ruleset")" == 0 ]] ||
		fail "complete spec fails ${ruleset##*/}"
done

# expect_red CASE RULESET: the planted spec fails the lint and fires the named rule.
expect_red() {
	local name="$1" ruleset="$2" spec="$work_dir/case-${1//\$/D}.json"
	if ! grep -Fxq -- "$name" <<<"$(rules_fired "$spec" "$ruleset")"; then
		fail "${ruleset##*/}: planted $name defect does not fire $name"
	fi
	[[ "$(lint_status "$spec" "$ruleset")" != 0 ]] ||
		fail "${ruleset##*/}: planted $name defect passes the lint"
}

for name in "${!mutations[@]}"; do
	jq "${mutations[$name]}" "$work_dir/good.json" >"$work_dir/case-${name//\$/D}.json"
	expect_red "$name" "$strict_ruleset"
done
for name in "${default_cases[@]}"; do
	expect_red "$name" "$default_ruleset"
done
for name in "${default_quiet[@]}"; do
	spec="$work_dir/case-${name}.json"
	[[ "$(lint_status "$spec" "$default_ruleset")" == 0 ]] ||
		fail "oas.yaml: $name is off in spectral:oas but fails the default lint"
done

if ((failures > 0)); then
	printf 'OpenAPI ruleset contract: %d failure(s)\n' "$failures" >&2
	exit 1
fi
printf 'OpenAPI ruleset contract passed (%d planted defects)\n' "${#mutations[@]}"
