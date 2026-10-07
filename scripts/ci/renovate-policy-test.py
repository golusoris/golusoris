#!/usr/bin/env python3

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Check that Renovate extracts every repository-owned nonstandard pin."""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
INPUTS = (
    ".devcontainer/devcontainer.json",
    ".gitea/workflows/ci.yml",
    ".gitea/workflows/publish-github.yml",
    ".gitea/workflows/security-scan.yml",
    ".github/workflows/rebuild-on-base.yml",
    ".github/testcontainers-images.txt",
    "internal/testimages/images.go",
    "scripts/ci/tiny-trainer-locks.sh",
    "template/.devcontainer/devcontainer.json",
    "template/.devcontainer/docker-compose.yml",
    "tools/tool-versions.env",
)

PRIVATE_JOB_IMAGE = "harbor.infra.cauda.dev/library/gitea-actions-job"
PRIVATE_JOB_WORKFLOWS = (
    ".gitea/workflows/ci.yml",
    ".gitea/workflows/publish-github.yml",
    ".gitea/workflows/security-scan.yml",
)

EXPECTED = {
    (".github/workflows/rebuild-on-base.yml", "tonistiigi/binfmt", "docker"),
    ("tools/tool-versions.env", "mvdan.cc/gofumpt", "go"),
    ("tools/tool-versions.env", "github.com/golangci/golangci-lint/v2", "go"),
    ("tools/tool-versions.env", "github.com/securego/gosec/v2", "go"),
    ("tools/tool-versions.env", "golang.org/x/vuln", "go"),
    ("tools/tool-versions.env", "github.com/joelanford/go-apidiff", "go"),
    ("tools/tool-versions.env", "github.com/evilmartians/lefthook/v2", "go"),
    ("tools/tool-versions.env", "github.com/daixiang0/gci", "go"),
    ("tools/tool-versions.env", "github.com/vektra/mockery/v3", "go"),
    ("tools/tool-versions.env", "github.com/air-verse/air", "go"),
    ("tools/tool-versions.env", "github.com/sqlc-dev/sqlc", "go"),
    ("tools/tool-versions.env", "github.com/ogen-go/ogen", "go"),
    ("tools/tool-versions.env", "github.com/golang-migrate/migrate/v4", "go"),
    ("tools/tool-versions.env", "sigs.k8s.io/controller-tools", "go"),
    ("tools/tool-versions.env", "github.com/avito-tech/go-mutesting", "go"),
    ("tools/tool-versions.env", "helm.sh/helm/v4", "go"),
    ("tools/tool-versions.env", "koalaman/shellcheck", "github-releases"),
    ("tools/tool-versions.env", "github.com/rhysd/actionlint", "go"),
    ("tools/tool-versions.env", "goreleaser/goreleaser", "github-releases"),
    ("tools/tool-versions.env", "anchore/syft", "github-releases"),
    ("tools/tool-versions.env", "sigstore/cosign", "github-releases"),
    ("tools/tool-versions.env", "gitleaks/gitleaks", "github-releases"),
    ("tools/tool-versions.env", "ghcr.io/astral-sh/ruff", "docker"),
    ("tools/tool-versions.env", "silkeh/clang", "docker"),
    ("internal/testimages/images.go", "postgres", "docker"),
    ("internal/testimages/images.go", "timescale/timescaledb", "docker"),
    ("internal/testimages/images.go", "redis", "docker"),
    ("internal/testimages/images.go", "nats", "docker"),
    ("internal/testimages/images.go", "clickhouse/clickhouse-server", "docker"),
    ("internal/testimages/images.go", "redpandadata/redpanda", "docker"),
    ("internal/testimages/images.go", "versity/versitygw", "docker"),
    ("internal/testimages/images.go", "testcontainers/ryuk", "docker"),
    ("internal/testimages/images.go", "clamav/clamav", "docker"),
    ("internal/testimages/images.go", "fsouza/fake-gcs-server", "docker"),
    ("internal/testimages/images.go", "mcr.microsoft.com/azure-storage/azurite", "docker"),
    ("scripts/ci/tiny-trainer-locks.sh", "ghcr.io/astral-sh/uv", "docker"),
    ("scripts/ci/tiny-trainer-locks.sh", "pip-audit", "pypi"),
    (".github/testcontainers-images.txt", "postgres", "docker"),
    (".github/testcontainers-images.txt", "timescale/timescaledb", "docker"),
    (".github/testcontainers-images.txt", "redis", "docker"),
    (".github/testcontainers-images.txt", "nats", "docker"),
    (".github/testcontainers-images.txt", "clickhouse/clickhouse-server", "docker"),
    (".github/testcontainers-images.txt", "redpandadata/redpanda", "docker"),
    (".github/testcontainers-images.txt", "versity/versitygw", "docker"),
    (".github/testcontainers-images.txt", "testcontainers/ryuk", "docker"),
    ("tools/tool-versions.env", "fsfe/reuse", "docker"),
    ("tools/tool-versions.env", "ghcr.io/yannh/kubeconform", "docker"),
    (
        "tools/tool-versions.env",
        "https://github.com/yannh/kubernetes-json-schema.git",
        "git-refs",
    ),
    (
        "tools/tool-versions.env",
        "https://github.com/datreeio/CRDs-catalog.git",
        "git-refs",
    ),
    ("tools/tool-versions.env", "kubernetes/kubernetes", "github-releases"),
    ("tools/tool-versions.env", "squidfunk/mkdocs-material", "docker"),
    ("tools/tool-versions.env", "hashicorp/terraform", "docker"),
    ("tools/tool-versions.env", "semgrep/semgrep", "docker"),
    ("tools/tool-versions.env", "aquasec/trivy", "docker"),
    (".devcontainer/devcontainer.json", "golangci/golangci-lint", "github-releases"),
    (".devcontainer/devcontainer.json", "go", "golang-version"),
    ("template/.devcontainer/devcontainer.json", "docker/cli", "github-releases"),
    ("template/.devcontainer/devcontainer.json", "docker/buildx", "github-releases"),
    ("template/.devcontainer/devcontainer.json", "node", "node-version"),
    ("template/.devcontainer/devcontainer.json", "npm", "npm"),
    ("template/.devcontainer/devcontainer.json", "pnpm", "npm"),
    ("template/.devcontainer/devcontainer.json", "nvm-sh/nvm", "github-releases"),
    ("template/.devcontainer/devcontainer.json", "github.com/air-verse/air", "go"),
    (
        "template/.devcontainer/devcontainer.json",
        "github.com/golangci/golangci-lint/v2",
        "go",
    ),
    ("template/.devcontainer/devcontainer.json", "golang.org/x/vuln", "go"),
    ("template/.devcontainer/devcontainer.json", "github.com/sqlc-dev/sqlc", "go"),
    (
        "template/.devcontainer/docker-compose.yml",
        "mcr.microsoft.com/devcontainers/go",
        "docker",
    ),
}

EXPECTED_DIGESTS = {
    "tonistiigi/binfmt",
    "fsfe/reuse",
    "ghcr.io/yannh/kubeconform",
    "https://github.com/yannh/kubernetes-json-schema.git",
    "https://github.com/datreeio/CRDs-catalog.git",
    "squidfunk/mkdocs-material",
    "hashicorp/terraform",
    "semgrep/semgrep",
    "aquasec/trivy",
    "ghcr.io/astral-sh/ruff",
    "ghcr.io/astral-sh/uv",
    "silkeh/clang",
    "postgres",
    "timescale/timescaledb",
    "redis",
    "nats",
    "clickhouse/clickhouse-server",
    "redpandadata/redpanda",
    "versity/versitygw",
    "testcontainers/ryuk",
    "clamav/clamav",
    "fsouza/fake-gcs-server",
    "mcr.microsoft.com/azure-storage/azurite",
    "mcr.microsoft.com/devcontainers/go",
}


def compile_js_regex(pattern: str) -> re.Pattern[str]:
    """Compile the RE2 subset used by the checked managers with Python."""
    return re.compile(re.sub(r"\(\?<([A-Za-z][A-Za-z0-9_]*)>", r"(?P<\1>", pattern))


def matches_file(pattern: str, path: str) -> bool:
    if len(pattern) < 2 or pattern[0] != "/" or pattern[-1] != "/":
        raise ValueError(f"managerFilePatterns entry is not a slash regex: {pattern!r}")
    return compile_js_regex(pattern[1:-1]).search(path) is not None


def extract(config: dict[str, object], contents: dict[str, str]) -> list[dict[str, str]]:
    found: list[dict[str, str]] = []
    managers = config.get("customManagers")
    if not isinstance(managers, list):
        raise ValueError("customManagers must be a list")
    for manager in managers:
        if not isinstance(manager, dict) or manager.get("customType") != "regex":
            continue
        file_patterns = manager.get("managerFilePatterns")
        match_strings = manager.get("matchStrings")
        if not isinstance(file_patterns, list) or not isinstance(match_strings, list):
            raise ValueError("regex manager lacks file or match patterns")
        for path, content in contents.items():
            if not any(matches_file(str(pattern), path) for pattern in file_patterns):
                continue
            for match_string in match_strings:
                for match in compile_js_regex(str(match_string)).finditer(content):
                    captures = {key: value for key, value in match.groupdict().items() if value}
                    dep_name = captures.get("depName") or manager.get("depNameTemplate")
                    datasource = captures.get("datasource") or manager.get("datasourceTemplate")
                    current_value = captures.get("currentValue")
                    if not all(isinstance(value, str) and value for value in (dep_name, datasource, current_value)):
                        raise ValueError(f"incomplete extraction in {path}: {manager.get('description')}")
                    found.append(
                        {
                            "path": path,
                            "dep_name": str(dep_name),
                            "datasource": str(datasource),
                            "current_value": str(current_value),
                            "current_digest": captures.get("currentDigest", ""),
                        }
                    )
    return found


def identities(found: list[dict[str, str]]) -> set[tuple[str, str, str]]:
    return {(item["path"], item["dep_name"], item["datasource"]) for item in found}


# Go-authority images the primary CI test job never runs, so its cache omits
# them: ClamAV sits behind the `integration` build tag, and split-module
# emulators run in the Module sweep job, which pulls on demand.
CACHE_EXEMPT_IMAGES = frozenset(
    {"clamav/clamav", "fsouza/fake-gcs-server", "mcr.microsoft.com/azure-storage/azurite"}
)


def test_image_authorities_match(found: list[dict[str, str]]) -> bool:
    """Require duplicated CI cache pins to match the Go authority exactly."""
    paths = ("internal/testimages/images.go", ".github/testcontainers-images.txt")
    image_pins = {
        path: {
            item["dep_name"]: (item["current_value"], item["current_digest"])
            for item in found
            if item["path"] == path
        }
        for path in paths
    }
    go_images = image_pins[paths[0]]
    cache_images = image_pins[paths[1]]
    if set(go_images) - set(cache_images) != CACHE_EXEMPT_IMAGES or set(cache_images) - set(go_images):
        print("test-image authority sets differ beyond the declared cache exemptions", file=sys.stderr)
        return False
    drift = [name for name, pin in cache_images.items() if go_images[name] != pin]
    if drift:
        print(f"test-image authorities drift for {drift[0]}", file=sys.stderr)
        return False
    return True


def test_trainer_manager_coverage(config: dict[str, object]) -> bool:
    """Require Renovate ownership for both trainer bases and direct Python pins."""
    expected = {
        "dockerfile": (
            "ai/tiny/trainers/gemma/Dockerfile",
            "ai/tiny/trainers/litert/Dockerfile",
        ),
        "pip_requirements": (
            "ai/tiny/trainers/gemma/requirements.in",
            "ai/tiny/trainers/litert/requirements.in",
        ),
    }
    for manager_name, paths in expected.items():
        manager = config.get(manager_name)
        if not isinstance(manager, dict):
            print(f"Renovate lacks {manager_name} trainer manager", file=sys.stderr)
            return False
        patterns = manager.get("managerFilePatterns")
        if not isinstance(patterns, list):
            print(f"Renovate {manager_name} lacks file patterns", file=sys.stderr)
            return False
        for path in paths:
            if not any(matches_file(str(pattern), path) for pattern in patterns):
                print(f"Renovate {manager_name} does not own {path}", file=sys.stderr)
                return False
    for relative in expected["pip_requirements"]:
        content = (ROOT / relative).read_text(encoding="utf-8")
        pins = [line for line in content.splitlines() if line and not line.startswith("#")]
        if not pins or any("==" not in line for line in pins):
            print(f"trainer direct requirement is not exactly pinned: {relative}", file=sys.stderr)
            return False
    return test_trainer_frontend_pins(expected["dockerfile"])


def test_trainer_frontend_pins(paths: tuple[str, ...]) -> bool:
    """Require one digest-pinned Dockerfile frontend across trainers."""
    frontends: set[tuple[str, str]] = set()
    for relative in paths:
        first_line = (ROOT / relative).read_text(encoding="utf-8").splitlines()[0]
        match = re.fullmatch(
            r"# syntax=docker/dockerfile:([^@]+)@(sha256:[a-f0-9]{64})", first_line
        )
        if match is None:
            print(f"trainer Dockerfile frontend is not digest-pinned: {relative}", file=sys.stderr)
            return False
        frontends.add((match.group(1), match.group(2)))
    if len(frontends) != 1:
        print("trainer Dockerfile frontend pins differ", file=sys.stderr)
        return False
    return True


def template_compose_authority_valid(content: str) -> bool:
    """Accept only the one dependency the template compose file declares."""
    dependencies = re.findall(r"# renovate: datasource=\S+ depName=(\S+)", content)
    return dependencies == ["mcr.microsoft.com/devcontainers/go"]


def private_job_image_authority_valid(
    config: dict[str, object], contents: dict[str, str]
) -> bool:
    """Require one external owner and one identical private job-image pin."""
    pattern = re.compile(
        rf"image: ({re.escape(PRIVATE_JOB_IMAGE)}:[^@\s]+@sha256:[a-f0-9]{{64}})"
    )
    references = [pattern.findall(contents[path]) for path in PRIVATE_JOB_WORKFLOWS]
    if any(len(items) != 1 for items in references):
        return False
    if len({items[0] for items in references}) != 1:
        return False
    rules = config.get("packageRules")
    if not isinstance(rules, list):
        return False
    matches = [
        rule
        for rule in rules
        if isinstance(rule, dict)
        and rule.get("matchManagers") == ["github-actions"]
        and rule.get("matchDatasources") == ["docker"]
        and rule.get("matchPackageNames") == [PRIVATE_JOB_IMAGE]
        and rule.get("enabled") is False
        and "lusoris/k8s" in str(rule.get("description", ""))
        and "job-image" in str(rule.get("description", ""))
    ]
    return len(matches) == 1


def test_private_job_image_authority(
    config: dict[str, object], contents: dict[str, str]
) -> bool:
    """Report missing or drifting private image ownership."""
    if private_job_image_authority_valid(config, contents):
        return True
    print("private Gitea job-image authority or workflow pins differ", file=sys.stderr)
    return False


def test_pin_inventory(found: list[dict[str, str]]) -> bool:
    """Require the exact declared nonstandard dependency inventory."""
    actual = identities(found)
    if actual != EXPECTED or len(found) != len(EXPECTED):
        print(f"missing Renovate pins: {sorted(EXPECTED - actual)}", file=sys.stderr)
        print(f"unexpected Renovate pins: {sorted(actual - EXPECTED)}", file=sys.stderr)
        print(f"extracted {len(found)} pins; expected {len(EXPECTED)}", file=sys.stderr)
        return False
    return True


def test_digest_inventory(found: list[dict[str, str]]) -> bool:
    """Require every digest-coupled dependency and no undeclared additions."""
    digest_names = {item["dep_name"] for item in found if item["current_digest"]}
    if digest_names != EXPECTED_DIGESTS:
        print(f"digest-coupled pins differ: {sorted(digest_names)}", file=sys.stderr)
        return False
    return True


def test_negative_controls(config: dict[str, object], contents: dict[str, str]) -> bool:
    """Prove stale template and missing tool metadata are rejected."""
    template_path = "template/.devcontainer/docker-compose.yml"
    if not template_compose_authority_valid(contents[template_path]):
        print("template compose Renovate authority differs from its one declared image", file=sys.stderr)
        return False

    extra_template_dependency = (
        contents[template_path]
        + "\n# renovate: datasource=docker depName=ghcr.io/astral-sh/uv\n"
    )
    if template_compose_authority_valid(extra_template_dependency):
        print("template compose negative control accepted a stale uv dependency", file=sys.stderr)
        return False

    mutated = dict(contents)
    mutated["tools/tool-versions.env"] = mutated["tools/tool-versions.env"].replace(
        "# renovate: datasource=go depName=mvdan.cc/gofumpt versioning=semver",
        "# missing Renovate metadata",
        1,
    )
    if identities(extract(config, mutated)) == EXPECTED:
        print("negative control did not detect missing tool metadata", file=sys.stderr)
        return False

    mutated_config = json.loads(json.dumps(config))
    for rule in mutated_config["packageRules"]:
        if rule.get("matchPackageNames") == [PRIVATE_JOB_IMAGE]:
            rule["enabled"] = True
    if private_job_image_authority_valid(mutated_config, contents):
        print("negative control accepted Renovate ownership of the private image", file=sys.stderr)
        return False

    drifted = dict(contents)
    drifted[PRIVATE_JOB_WORKFLOWS[0]] = drifted[PRIVATE_JOB_WORKFLOWS[0]].replace(
        "sha256:ca08d1bee54628ae8713133ae4bf2fe7f06a20a6db6ffd1d184f6ac63660297b",
        "sha256:" + "0" * 64,
        1,
    )
    if private_job_image_authority_valid(config, drifted):
        print("negative control accepted a drifting private job-image digest", file=sys.stderr)
        return False
    return True


def core_direct_requires(gomod: str) -> list[str]:
    """Return the module paths core/go.mod requires directly, in file order."""
    names: list[str] = []
    in_block = False
    for raw in gomod.splitlines():
        line = raw.strip()
        if line.startswith("require ("):
            in_block = True
            continue
        if in_block and line == ")":
            in_block = False
            continue
        if line.startswith("require ") and not line.endswith("("):
            line = line.removeprefix("require ").strip()
        elif not in_block:
            continue
        if line and not line.startswith("//") and "// indirect" not in line:
            names.append(line.split()[0])
    return names


def core_indirect_rule_valid(config: dict[str, object], core_gomod: str) -> bool:
    """Require the rule that bumps core's direct deps where other modules list them indirect."""
    rules = [
        rule
        for rule in config.get("packageRules", [])
        if rule.get("matchDepTypes") == ["indirect"] and "core/" in rule.get("description", "")
    ]
    if len(rules) != 1 or rules[0].get("enabled") is not True:
        return False
    if "gomodTidy" not in config.get("postUpdateOptions", []):
        return False
    return sorted(rules[0].get("matchPackageNames", [])) == sorted(core_direct_requires(core_gomod))


def test_core_indirect_rule(config: dict[str, object], core_gomod: str) -> bool:
    """Report drift between the core-indirect rule and core/go.mod, with negative controls."""
    if not core_indirect_rule_valid(config, core_gomod):
        print("renovate core-indirect rule differs from core/go.mod direct requirements "
              "or gomodTidy is missing", file=sys.stderr)
        return False
    added_dependency = core_gomod.replace("require (\n", "require (\n\texample.com/new v1.0.0\n", 1)
    if core_indirect_rule_valid(config, added_dependency):
        print("negative control accepted a core dependency the rule does not name", file=sys.stderr)
        return False
    without_tidy = json.loads(json.dumps(config))
    without_tidy["postUpdateOptions"] = []
    if core_indirect_rule_valid(without_tidy, core_gomod):
        print("negative control accepted the rule without gomodTidy", file=sys.stderr)
        return False
    return True


def main() -> int:
    config = json.loads((ROOT / "renovate.json").read_text(encoding="utf-8"))
    contents = {path: (ROOT / path).read_text(encoding="utf-8") for path in INPUTS}
    core_gomod = (ROOT / "core/go.mod").read_text(encoding="utf-8")
    found = extract(config, contents)
    checks = (
        test_pin_inventory(found),
        test_digest_inventory(found),
        test_image_authorities_match(found),
        test_trainer_manager_coverage(config),
        test_private_job_image_authority(config, contents),
        test_negative_controls(config, contents),
        test_core_indirect_rule(config, core_gomod),
    )
    if not all(checks):
        return 1

    print(f"Renovate policy extracted {len(found)} nonstandard pins; negative control passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
