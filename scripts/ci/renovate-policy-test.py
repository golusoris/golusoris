#!/usr/bin/env python3

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Check that Renovate extracts every repository-owned nonstandard pin."""

from __future__ import annotations

import json
import re
import sys
from collections import Counter
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
INPUTS = (
    ".devcontainer/devcontainer.json",
    ".gitea/workflows/ci.yml",
    ".gitea/workflows/publish-github.yml",
    ".gitea/workflows/security-scan.yml",
    ".github/workflows/ci-go.yml",
    ".github/workflows/rebuild-on-base.yml",
    ".github/testcontainers-images.txt",
    ".github/testcontainers-split-images.txt",
    "container/registry/credentials/ecr/internal/motodev/images.go",
    "container/registry/sign/kms/aws/internal/localstackdev/images.go",
    "container/registry/sign/kms/azure/internal/lowkeydev/images.go",
    "container/registry/sign/kms/vault/internal/vaultdev/images.go",
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
    (".github/workflows/ci-go.yml", "mvdan.cc/gofumpt", "go"),
    (".github/workflows/ci-go.yml", "github.com/golangci/golangci-lint/v2", "go"),
    (".github/workflows/ci-go.yml", "github.com/securego/gosec/v2", "go"),
    (".github/workflows/ci-go.yml", "golang.org/x/tools", "go"),
    (".github/workflows/ci-go.yml", "golang.org/x/vuln", "go"),
    (".github/workflows/ci-go.yml", "github.com/joelanford/go-apidiff", "go"),
    ("tools/tool-versions.env", "mvdan.cc/gofumpt", "go"),
    ("tools/tool-versions.env", "github.com/golangci/golangci-lint/v2", "go"),
    ("tools/tool-versions.env", "github.com/securego/gosec/v2", "go"),
    ("tools/tool-versions.env", "golang.org/x/tools", "go"),
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
    ("tools/tool-versions.env", "sigs.k8s.io/controller-runtime/tools/setup-envtest", "go"),
    ("tools/tool-versions.env", "github.com/avito-tech/go-mutesting", "go"),
    ("tools/tool-versions.env", "helm.sh/helm/v4", "go"),
    ("tools/tool-versions.env", "github.com/bufbuild/buf", "go"),
    ("tools/tool-versions.env", "koalaman/shellcheck", "github-releases"),
    ("tools/tool-versions.env", "github.com/rhysd/actionlint", "go"),
    ("tools/tool-versions.env", "github.com/daveshanley/vacuum", "go"),
    ("tools/tool-versions.env", "goreleaser/goreleaser", "github-releases"),
    ("tools/tool-versions.env", "anchore/syft", "github-releases"),
    ("tools/tool-versions.env", "sigstore/cosign", "github-releases"),
    ("tools/tool-versions.env", "gitleaks/gitleaks", "github-releases"),
    ("tools/tool-versions.env", "ghcr.io/astral-sh/ruff", "docker"),
    ("tools/tool-versions.env", "silkeh/clang", "docker"),
    ("internal/testimages/images.go", "postgres", "docker"),
    ("internal/testimages/images.go", "postgis/postgis", "docker"),
    ("internal/testimages/images.go", "pgvector/pgvector", "docker"),
    ("internal/testimages/images.go", "timescale/timescaledb", "docker"),
    ("internal/testimages/images.go", "redis", "docker"),
    ("internal/testimages/images.go", "nats", "docker"),
    ("internal/testimages/images.go", "clickhouse/clickhouse-server", "docker"),
    ("internal/testimages/images.go", "redpandadata/redpanda", "docker"),
    ("internal/testimages/images.go", "versity/versitygw", "docker"),
    ("internal/testimages/images.go", "testcontainers/ryuk", "docker"),
    ("internal/testimages/images.go", "clamav/clamav", "docker"),
    ("internal/testimages/images.go", "gcr.io/google.com/cloudsdktool/google-cloud-cli", "docker"),
    ("internal/testimages/images.go", "axllent/mailpit", "docker"),
    ("internal/testimages/images.go", "stripe/stripe-mock", "docker"),
    ("internal/testimages/images.go", "bugsink/bugsink", "docker"),
    ("internal/testimages/images.go", "fsouza/fake-gcs-server", "docker"),
    ("internal/testimages/images.go", "mcr.microsoft.com/azure-storage/azurite", "docker"),
    ("container/registry/credentials/ecr/internal/motodev/images.go", "motoserver/moto", "docker"),
    ("container/registry/credentials/ecr/internal/motodev/images.go", "testcontainers/ryuk", "docker"),
    ("container/registry/sign/kms/vault/internal/vaultdev/images.go", "hashicorp/vault", "docker"),
    ("container/registry/sign/kms/vault/internal/vaultdev/images.go", "openbao/openbao", "docker"),
    ("container/registry/sign/kms/vault/internal/vaultdev/images.go", "testcontainers/ryuk", "docker"),
    ("container/registry/sign/kms/aws/internal/localstackdev/images.go", "localstack/localstack", "docker"),
    ("container/registry/sign/kms/aws/internal/localstackdev/images.go", "testcontainers/ryuk", "docker"),
    ("container/registry/sign/kms/azure/internal/lowkeydev/images.go", "nagyesta/lowkey-vault", "docker"),
    ("container/registry/sign/kms/azure/internal/lowkeydev/images.go", "testcontainers/ryuk", "docker"),
    ("scripts/ci/tiny-trainer-locks.sh", "ghcr.io/astral-sh/uv", "docker"),
    ("scripts/ci/tiny-trainer-locks.sh", "pip-audit", "pypi"),
    (".github/testcontainers-images.txt", "postgres", "docker"),
    (".github/testcontainers-images.txt", "postgis/postgis", "docker"),
    (".github/testcontainers-images.txt", "pgvector/pgvector", "docker"),
    (".github/testcontainers-images.txt", "timescale/timescaledb", "docker"),
    (".github/testcontainers-images.txt", "redis", "docker"),
    (".github/testcontainers-images.txt", "nats", "docker"),
    (".github/testcontainers-images.txt", "clickhouse/clickhouse-server", "docker"),
    (".github/testcontainers-images.txt", "redpandadata/redpanda", "docker"),
    (".github/testcontainers-images.txt", "versity/versitygw", "docker"),
    (".github/testcontainers-images.txt", "testcontainers/ryuk", "docker"),
    (".github/testcontainers-images.txt", "clamav/clamav", "docker"),
    (".github/testcontainers-images.txt", "gcr.io/google.com/cloudsdktool/google-cloud-cli", "docker"),
    (".github/testcontainers-images.txt", "axllent/mailpit", "docker"),
    (".github/testcontainers-images.txt", "stripe/stripe-mock", "docker"),
    (".github/testcontainers-images.txt", "bugsink/bugsink", "docker"),
    (".github/testcontainers-split-images.txt", "motoserver/moto", "docker"),
    (".github/testcontainers-split-images.txt", "localstack/localstack", "docker"),
    (".github/testcontainers-split-images.txt", "hashicorp/vault", "docker"),
    (".github/testcontainers-split-images.txt", "openbao/openbao", "docker"),
    (".github/testcontainers-split-images.txt", "testcontainers/ryuk", "docker"),
    (".github/testcontainers-split-images.txt", "fsouza/fake-gcs-server", "docker"),
    (".github/testcontainers-split-images.txt", "mcr.microsoft.com/azure-storage/azurite", "docker"),
    (".github/testcontainers-split-images.txt", "nagyesta/lowkey-vault", "docker"),
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
    ("tools/tool-versions.env", "kubernetes-sigs/kind", "github-releases"),
    ("tools/tool-versions.env", "cloudnative-pg/cloudnative-pg", "github-releases"),
    ("tools/tool-versions.env", "squidfunk/mkdocs-material", "docker"),
    ("tools/tool-versions.env", "hashicorp/terraform", "docker"),
    ("tools/tool-versions.env", "semgrep/semgrep", "docker"),
    ("tools/tool-versions.env", "aquasec/trivy", "docker"),
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

# Identities pinned more than once per file: TimescaleDB ships TSL and Apache-only (-oss) tags.
EXPECTED_MULTIPLICITY = {
    ("internal/testimages/images.go", "timescale/timescaledb", "docker"): 2,
    (".github/testcontainers-images.txt", "timescale/timescaledb", "docker"): 2,
    (".github/testcontainers-split-images.txt", "testcontainers/ryuk", "docker"): 6,
    ("tools/tool-versions.env", "kubernetes/kubernetes", "github-releases"): 2,
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
    "postgis/postgis",
    "pgvector/pgvector",
    "timescale/timescaledb",
    "redis",
    "nats",
    "clickhouse/clickhouse-server",
    "redpandadata/redpanda",
    "versity/versitygw",
    "testcontainers/ryuk",
    "clamav/clamav",
    "gcr.io/google.com/cloudsdktool/google-cloud-cli",
    "axllent/mailpit",
    "stripe/stripe-mock",
    "bugsink/bugsink",
    "motoserver/moto",
    "fsouza/fake-gcs-server",
    "mcr.microsoft.com/azure-storage/azurite",
    "hashicorp/vault",
    "openbao/openbao",
    "localstack/localstack",
    "nagyesta/lowkey-vault",
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


# Split-module emulators use the shard pre-pull manifest instead of the primary cache.
CACHE_EXEMPT_IMAGES = frozenset(
    {"fsouza/fake-gcs-server", "mcr.microsoft.com/azure-storage/azurite"}
)


def test_image_authorities_match(found: list[dict[str, str]]) -> bool:
    """Require duplicated CI cache pins to match the Go authority exactly."""
    paths = ("internal/testimages/images.go", ".github/testcontainers-images.txt")
    image_pins = {
        path: {
            (item["dep_name"], item["current_value"], item["current_digest"])
            for item in found
            if item["path"] == path
        }
        for path in paths
    }
    go_images = image_pins[paths[0]]
    cache_images = image_pins[paths[1]]
    go_only = {name for name, _, _ in go_images - cache_images}
    if go_only != CACHE_EXEMPT_IMAGES or cache_images - go_images:
        drift = sorted(map(str, cache_images - go_images)) + sorted(go_only ^ CACHE_EXEMPT_IMAGES)
        print(f"test-image authorities differ beyond the declared cache exemptions: {drift[0]}", file=sys.stderr)
        return False
    return True


SPLIT_IMAGES_PATH = ".github/testcontainers-split-images.txt"
SPLIT_IMAGE_AUTHORITIES = {
    "container/registry": (
        "container/registry/credentials/ecr/internal/motodev/images.go", None,
    ),
    "container/registry/sign/kms/aws": (
        "container/registry/sign/kms/aws/internal/localstackdev/images.go", None,
    ),
    "container/registry/sign/kms/azure": (
        "container/registry/sign/kms/azure/internal/lowkeydev/images.go", None,
    ),
    "container/registry/sign/kms/vault": (
        "container/registry/sign/kms/vault/internal/vaultdev/images.go", None,
    ),
    "storage/gcs": (
        "internal/testimages/images.go", {"fsouza/fake-gcs-server", "testcontainers/ryuk"},
    ),
    "storage/azblob": (
        "internal/testimages/images.go", {"mcr.microsoft.com/azure-storage/azurite", "testcontainers/ryuk"},
    ),
}


def split_image_authorities_valid(found: list[dict[str, str]], content: str) -> bool:
    """Require exact module, name, tag and digest parity with Go authorities."""
    expected = Counter(
        (module, item["dep_name"], item["current_value"], item["current_digest"])
        for module, (path, names) in SPLIT_IMAGE_AUTHORITIES.items()
        for item in found
        if item["path"] == path and (names is None or item["dep_name"] in names)
    )
    actual: Counter[tuple[str, ...]] = Counter()
    for line in content.splitlines():
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        match = re.fullmatch(
            r"([a-z0-9_./-]+) ([a-z0-9][a-z0-9._/-]*):([^@\s]+)@(sha256:[a-f0-9]{64})",
            line,
        )
        if match is None:
            return False
        actual[match.groups()] += 1
    return actual == expected


def test_split_image_authorities(found: list[dict[str, str]], content: str) -> bool:
    """Report split-module pre-pull drift before tests reach Docker."""
    if split_image_authorities_valid(found, content):
        return True
    print("split test-image manifest differs from Go authorities (module/name/tag/digest)", file=sys.stderr)
    return False


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


def localstack_hold_valid(config: dict[str, object]) -> bool:
    """Require the rule that keeps LocalStack below the token-gated 2026 line."""
    rules = config.get("packageRules")
    if not isinstance(rules, list):
        return False
    holds = [
        rule
        for rule in rules
        if isinstance(rule, dict)
        and rule.get("matchDatasources") == ["docker"]
        and rule.get("matchPackageNames") == ["localstack/localstack"]
        and rule.get("allowedVersions") == "<2026"
    ]
    return len(holds) == 1


def test_localstack_hold(config: dict[str, object]) -> bool:
    """Report a missing LocalStack hold and prove the check refuses its removal."""
    if not localstack_hold_valid(config):
        print("renovate.json must hold localstack/localstack below 2026 (auth token)", file=sys.stderr)
        return False
    mutated = json.loads(json.dumps(config))
    mutated["packageRules"] = [
        rule
        for rule in mutated["packageRules"]
        if rule.get("matchPackageNames") != ["localstack/localstack"]
    ]
    if localstack_hold_valid(mutated):
        print("LocalStack hold negative control was accepted", file=sys.stderr)
        return False
    return True


def python_cap_valid(config: dict[str, object], files: list[str], cap: str) -> bool:
    """Require exactly one docker Python cap rule for files at cap."""
    rules = config.get("packageRules")
    if not isinstance(rules, list):
        return False
    caps = [
        rule
        for rule in rules
        if isinstance(rule, dict)
        and rule.get("matchDatasources") == ["docker"]
        and rule.get("matchFileNames") == files
        and rule.get("matchPackageNames") == ["python"]
        and rule.get("allowedVersions") == cap
    ]
    return len(caps) == 1


TRAINER_FILES = ["ai/tiny/trainers/gemma/**", "ai/tiny/trainers/litert/**"]
SIDECAR_FILES = ["ai/tiny/serve/tflite/sidecar/Dockerfile"]


def trainer_python_cap_valid(config: dict[str, object]) -> bool:
    """Require the Python cap for both TensorFlow trainer base images."""
    return python_cap_valid(config, TRAINER_FILES, "<3.14")


def sidecar_python_cap_valid(config: dict[str, object]) -> bool:
    """Require the Python cap for the ai-edge-litert sidecar base image."""
    return python_cap_valid(config, SIDECAR_FILES, "<3.15")


def without_python_rules(config: dict[str, object]) -> dict[str, object]:
    """Return a copy of config with every Python package rule removed."""
    mutated = json.loads(json.dumps(config))
    mutated["packageRules"] = [
        rule for rule in mutated["packageRules"] if rule.get("matchPackageNames") != ["python"]
    ]
    return mutated


def test_trainer_python_cap(config: dict[str, object]) -> bool:
    """Report a missing trainer Python cap and prove its removal is refused."""
    if not trainer_python_cap_valid(config):
        print("renovate.json must hold both trainer Python bases below 3.14", file=sys.stderr)
        return False
    if trainer_python_cap_valid(without_python_rules(config)):
        print("trainer Python cap negative control was accepted", file=sys.stderr)
        return False
    return True


def test_sidecar_python_cap(config: dict[str, object]) -> bool:
    """Report a missing sidecar Python cap and prove its removal is refused."""
    if not sidecar_python_cap_valid(config):
        print("renovate.json must hold the TFLite sidecar Python base below 3.15", file=sys.stderr)
        return False
    if sidecar_python_cap_valid(without_python_rules(config)):
        print("sidecar Python cap negative control was accepted", file=sys.stderr)
        return False
    return True


def chromedp_group_valid(config: dict[str, object]) -> bool:
    """Require chromedp and cdproto in one reviewed update group."""
    rules = config.get("packageRules")
    if not isinstance(rules, list):
        return False
    groups = [
        rule
        for rule in rules
        if isinstance(rule, dict)
        and rule.get("matchDatasources") == ["go"]
        and rule.get("matchPackageNames") == [
            "github.com/chromedp/chromedp",
            "github.com/chromedp/cdproto",
        ]
        and rule.get("groupName") == "chromedp"
        and rule.get("automerge") is False
    ]
    return len(groups) == 1


def test_chromedp_group(config: dict[str, object]) -> bool:
    """Report a missing chromedp group and prove its removal is refused."""
    if not chromedp_group_valid(config):
        print("renovate.json must group chromedp and cdproto with automerge off", file=sys.stderr)
        return False
    mutated = json.loads(json.dumps(config))
    mutated["packageRules"] = [
        rule for rule in mutated["packageRules"] if rule.get("groupName") != "chromedp"
    ]
    if chromedp_group_valid(mutated):
        print("chromedp group negative control was accepted", file=sys.stderr)
        return False
    return True


def test_pin_inventory(found: list[dict[str, str]]) -> bool:
    """Require the exact declared nonstandard dependency inventory."""
    actual = identities(found)
    counts = Counter((item["path"], item["dep_name"], item["datasource"]) for item in found)
    expected_counts = {identity: EXPECTED_MULTIPLICITY.get(identity, 1) for identity in EXPECTED}
    if actual != EXPECTED or counts != expected_counts:
        expected_total = sum(expected_counts.values())
        print(f"missing Renovate pins: {sorted(EXPECTED - actual)}", file=sys.stderr)
        print(f"unexpected Renovate pins: {sorted(actual - EXPECTED)}", file=sys.stderr)
        print(f"extracted {len(found)} pins; expected {expected_total}", file=sys.stderr)
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
    split_drift = dict(contents)
    split_drift[SPLIT_IMAGES_PATH] = re.sub(
        r"@sha256:[a-f0-9]{64}", "@sha256:" + "0" * 64, contents[SPLIT_IMAGES_PATH], count=1,
    )
    if split_image_authorities_valid(extract(config, split_drift), split_drift[SPLIT_IMAGES_PATH]):
        print("negative control accepted a drifting split test-image digest", file=sys.stderr)
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


PRESET = "github>plurano/horistes"
# The preset holds minimumReleaseAge "3 days" and switches github-runner proposals off.
MINIMUM_RELEASE_AGE = "3 days"


def extends_preset(config: dict[str, object]) -> bool:
    """Require the family preset in extends."""
    extends = config.get("extends")
    return isinstance(extends, list) and PRESET in extends


def minimum_age_valid(config: dict[str, object]) -> bool:
    """Require the preset's three-day release age and refuse any setting that weakens it."""
    if not extends_preset(config):
        return False
    if config.get("minimumReleaseAge", MINIMUM_RELEASE_AGE) != MINIMUM_RELEASE_AGE:
        return False
    if config.get("internalChecksFilter", "strict") != "strict":
        return False
    rules = config.get("packageRules")
    if not isinstance(rules, list):
        return False
    return not any(isinstance(rule, dict) and "minimumReleaseAge" in rule for rule in rules)


def without_preset(config: dict[str, object]) -> dict[str, object]:
    """Copy the configuration with the family preset dropped from extends."""
    mutated = json.loads(json.dumps(config))
    mutated["extends"] = [entry for entry in mutated["extends"] if entry != PRESET]
    return mutated


def minimum_age_mutants(config: dict[str, object]) -> dict[str, dict[str, object]]:
    """Build the configurations the age check must refuse."""
    shortened = json.loads(json.dumps(config))
    shortened["minimumReleaseAge"] = "1 day"
    unfiltered = json.loads(json.dumps(config))
    unfiltered["internalChecksFilter"] = "none"
    overridden = json.loads(json.dumps(config))
    overridden["packageRules"].append({"matchManagers": ["gomod"], "minimumReleaseAge": "0 days"})
    return {
        "preset removed": without_preset(config),
        "age shortened": shortened,
        "pending versions not filtered": unfiltered,
        "package rule overrides the age": overridden,
    }


def test_minimum_age(config: dict[str, object]) -> bool:
    """Report a missing age rule and prove the check refuses each way to weaken it."""
    if not minimum_age_valid(config):
        print(
            f"renovate.json must extend {PRESET} (3-day release age), keep the strict filter "
            "and override the age nowhere",
            file=sys.stderr,
        )
        return False
    for label, mutated in minimum_age_mutants(config).items():
        if minimum_age_valid(mutated):
            print(f"minimum release age negative control was accepted: {label}", file=sys.stderr)
            return False
    return True


def runner_rules_off(config: dict[str, object]) -> bool:
    """Require the preset that switches runner proposals off and refuse any local runner rule."""
    if not extends_preset(config):
        return False
    rules = config.get("packageRules")
    if not isinstance(rules, list):
        return False
    return not any(
        isinstance(rule, dict) and "github-runner" in rule.get("matchDepTypes", []) for rule in rules
    )


def test_runner_hold(config: dict[str, object]) -> bool:
    """Keep the hosted runner label out of proposals (golusoris#815) and prove the check refuses."""
    if not runner_rules_off(config):
        print(
            f"renovate.json must extend {PRESET}, which switches runner proposals off, "
            "and carry no rule of its own for github-runner",
            file=sys.stderr,
        )
        return False
    reenabled = json.loads(json.dumps(config))
    reenabled["packageRules"].append(
        {"matchManagers": ["github-actions"], "matchDepTypes": ["github-runner"], "enabled": True}
    )
    for label, mutated in {"preset removed": without_preset(config), "runner re-enabled": reenabled}.items():
        if runner_rules_off(mutated):
            print(f"runner hold negative control was accepted: {label}", file=sys.stderr)
            return False
    return True


def test_backup_restore_dockerfile(config: dict[str, object]) -> bool:
    """Require Renovate ownership and digest pins for the backup-restore Dockerfile."""
    manager = config.get("dockerfile")
    if not isinstance(manager, dict):
        print("Renovate lacks dockerfile manager", file=sys.stderr)
        return False
    patterns = manager.get("managerFilePatterns")
    if not isinstance(patterns, list):
        print("Renovate dockerfile manager lacks managerFilePatterns", file=sys.stderr)
        return False
    target = "scripts/ci/backup-restore.Dockerfile"
    if not any(matches_file(str(pattern), target) for pattern in patterns):
        print(f"Renovate dockerfile manager does not own {target}", file=sys.stderr)
        return False

    dockerfile = ROOT / target
    if not dockerfile.is_file():
        print(f"missing Dockerfile: {target}", file=sys.stderr)
        return False

    from_pattern = re.compile(
        r"^FROM\s+[a-zA-Z0-9_./-]+:[a-zA-Z0-9_.-]+@sha256:[a-f0-9]{64}(?:\s+[Aa][Ss]\s+\S+)?$"
    )
    from_lines = [
        line.strip()
        for line in dockerfile.read_text(encoding="utf-8").splitlines()
        if re.match(r"\s*FROM\b", line, re.IGNORECASE)
    ]
    if not from_lines:
        print(f"{target} contains no FROM instructions", file=sys.stderr)
        return False
    for line in from_lines:
        if not from_pattern.fullmatch(line):
            print(
                f"{target} line does not match name:tag@sha256:<64 hex>: {line}",
                file=sys.stderr,
            )
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
        test_split_image_authorities(found, contents[SPLIT_IMAGES_PATH]),
        test_trainer_manager_coverage(config),
        test_private_job_image_authority(config, contents),
        test_negative_controls(config, contents),
        test_core_indirect_rule(config, core_gomod),
        test_localstack_hold(config),
        test_trainer_python_cap(config),
        test_sidecar_python_cap(config),
        test_chromedp_group(config),
        test_minimum_age(config),
        test_runner_hold(config),
        test_backup_restore_dockerfile(config),
    )
    if not all(checks):
        return 1

    print(f"Renovate policy extracted {len(found)} nonstandard pins; negative control passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
