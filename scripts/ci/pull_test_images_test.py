#!/usr/bin/env python3

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Exercise bounded image pulls and shard selection without a Docker daemon."""

from __future__ import annotations

import contextlib
import io
import os
import re
import runpy
import shutil
import subprocess
import tempfile
from pathlib import Path
from unittest import TestCase, main

ROOT = Path(__file__).resolve().parents[2]


class PullTestImagesTest(TestCase):
    def setUp(self) -> None:
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.log = self.root / "docker.log"
        self.bash = shutil.which("bash")
        self.assertIsNotNone(self.bash)
        docker = self.root / "docker"
        docker.write_text(
            '#!/usr/bin/env bash\n'
            'set -euo pipefail\n'
            'printf "%s\\n" "$*" >> "$DOCKER_LOG"\n'
            '[[ "$#" == 3 && "$1" == pull && "$2" == --quiet ]] || exit 97\n'
            '[[ "$3" != *:nonexistent ]] || exit 1\n',
            encoding="utf-8",
        )
        docker.chmod(0o755)
        self.environment = dict(os.environ, PATH=f"{self.root}{os.pathsep}{os.environ['PATH']}",
                                DOCKER_LOG=str(self.log), CI_SCOPE_BASE="")  # fixture is no git repository

    def run_pulls(self, *images: str) -> subprocess.CompletedProcess[str]:
        return self.run_script("pull-test-images.sh", *images)

    def run_script(
        self, script: str, *args: str, environment: dict[str, str] | None = None,
    ) -> subprocess.CompletedProcess[str]:
        return subprocess.run(  # noqa: S603 - Repository scripts and fixture arguments only, without a shell.
            [self.bash, str(ROOT / "scripts/ci" / script), *args],
            cwd=self.root, env=environment or self.environment,
            capture_output=True, text=True, timeout=10, check=False,
        )

    def test_nonexistent_tag_exhausts_retries_and_emits_error(self) -> None:
        image = "example.invalid/emulator:nonexistent"
        result = self.run_pulls(image)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(f"::error::docker pull failed after 3 attempts: {image}", result.stdout)
        self.assertEqual(self.log.read_text().splitlines(), [f"pull --quiet {image}"] * 3)

    def test_failure_in_first_batch_survives_later_success(self) -> None:
        result = self.run_pulls("emulator:nonexistent", "emulator:good", "emulator:later")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("::error::docker pull failed after 3 attempts: emulator:nonexistent", result.stdout)
        self.assertIn("pull --quiet emulator:later", self.log.read_text())

    def test_success_pulls_all_batches(self) -> None:
        images = tuple(f"emulator:good-{index}" for index in range(5))
        result = self.run_pulls(*images)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertCountEqual(self.log.read_text().splitlines(), [f"pull --quiet {image}" for image in images])

    def test_empty_shard_never_calls_docker(self) -> None:
        result = self.run_pulls()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse(self.log.exists())

    def test_image_bound_rejects_overflow_before_docker(self) -> None:
        result = self.run_pulls(*("emulator:good" for _ in range(33)))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("::error::pre-pull received 33 images; maximum 32", result.stdout)
        self.assertFalse(self.log.exists())

    def test_image_bound_accepts_exact_limit(self) -> None:
        images = tuple(f"emulator:good-{index}" for index in range(32))
        result = self.run_pulls(*images)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(len(self.log.read_text().splitlines()), 32)

    def test_each_shard_pulls_only_its_deduplicated_images(self) -> None:
        manifest = (ROOT / ".github/testcontainers-split-images.txt").read_text()
        entries = [line.split() for line in manifest.splitlines() if line and not line.startswith("#")]
        for shard in range(4):
            with self.subTest(shard=shard):
                environment = dict(self.environment, GO_MODULE_GROUP="split",
                                   GO_MODULE_SHARD_COUNT="4", GO_MODULE_SHARD_INDEX=str(shard))
                selection = self.run_script("go-modules.sh", "list", environment=environment)
                self.assertEqual(selection.returncode, 0, selection.stderr)
                selected = selection.stdout.splitlines()
                expected = {image for module, image in entries if module in selected}
                self.log.write_text("")
                result = self.run_script("pull-split-test-images.sh", environment=environment)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertCountEqual(self.log.read_text().splitlines(), [f"pull --quiet {image}" for image in expected])

    def test_invalid_shard_fails_before_docker(self) -> None:
        result = self.run_script(
            "pull-split-test-images.sh",
            environment=dict(self.environment, GO_MODULE_SHARD_COUNT="4", GO_MODULE_SHARD_INDEX="4"),
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.log.exists())

    def test_policy_main_rejects_manifest_digest_drift(self) -> None:
        policy = runpy.run_path(str(ROOT / "scripts/ci/renovate-policy-test.py"))
        fixture = self.root / "repository"
        fixture.mkdir()
        for name in (".devcontainer", ".gitea", "container", "internal", "scripts", "template", "tools", "core", "ai", "renovate.json"):
            (fixture / name).symlink_to(ROOT / name)
        (fixture / ".github").mkdir()
        for name in ("workflows", "testcontainers-images.txt"):
            (fixture / ".github" / name).symlink_to(ROOT / ".github" / name)
        manifest_path = ".github/testcontainers-split-images.txt"
        content = (ROOT / manifest_path).read_text()
        (fixture / manifest_path).write_text(re.sub(r"sha256:[a-f0-9]{64}", "sha256:" + "0" * 64, content, count=1))
        policy["main"].__globals__["ROOT"] = fixture
        errors = io.StringIO()
        with contextlib.redirect_stderr(errors):
            self.assertEqual(policy["main"](), 1)
        self.assertIn("split test-image manifest differs from Go authorities", errors.getvalue())


if __name__ == "__main__":
    main()
