#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Workflow actor policy regression tests."""

from __future__ import annotations

import sys
import tempfile
import unittest
from pathlib import Path

from workflow_actor_policy import (
    MAX_WORKFLOW_FILES,
    check,
    scan,
    write_fixture,
)


class WorkflowActorUsesPolicyTest(unittest.TestCase):
    def test_composite_action_actor_if_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            action = root / ".github/actions/x/action.yml"
            nested = root / ".github/actions/x/nested/action.yml"
            nested.parent.mkdir(parents=True)
            (root / ".github/actions/action.yml").write_text(
                "runs:\n  using: composite\n  steps:\n"
                "    - if: github.actor != vars.BOT_LOGIN\n",
                encoding="utf-8",
            )
            nested.write_text(
                "runs:\n  using: composite\n  steps:\n"
                "    - if: github.actor != vars.BOT_LOGIN\n",
                encoding="utf-8",
            )
            action.write_text(
                "name: Gate\nruns:\n  using: composite\n  steps:\n"
                "    - if: github.actor != vars.BOT_LOGIN\n"
                "      shell: bash\n      run: echo check\n",
                encoding="utf-8",
            )
            findings = scan(root)
            expected = (
                ".github/actions/action.yml:4:<top>:",
                ".github/actions/x/action.yml:5:<top>:",
                ".github/actions/x/nested/action.yml:4:<top>:",
            )
            self.assertEqual(len(findings), len(expected))
            for idx, prefix in enumerate(expected):
                self.assertTrue(findings[idx].startswith(prefix), findings[idx])

    def test_local_action_outside_scanned_set_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            write_fixture(
                root,
                "tools/gate/action.yml",
                "runs:\n  using: composite\n  steps:\n    - run: echo ok\n",
            )
            write_fixture(
                root,
                ".github/workflows/ci.yml",
                "on:\n  push:\njobs:\n  gate-job:\n    steps:\n"
                "      - uses: ./tools/gate\n",
            )
            self.assertEqual(
                scan(root),
                [
                    (
                        ".github/workflows/ci.yml:6:gate-job: "
                        "local action not scanned: ./tools/gate"
                    )
                ],
            )

    def test_missing_local_action_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            write_fixture(
                root,
                ".github/workflows/ci.yml",
                "on:\n  push:\njobs:\n  build:\n    steps:\n"
                "      - uses: ./.github/actions/missing\n",
            )
            self.assertEqual(
                scan(root),
                [
                    (
                        ".github/workflows/ci.yml:6:build: "
                        "local action not scanned: ./.github/actions/missing"
                    )
                ],
            )

    def test_scanned_local_action_yaml_and_reusable_workflow_pass(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            write_fixture(
                root,
                ".github/actions/x/action.yaml",
                "runs:\n  using: composite\n  steps:\n"
                "    - shell: bash\n      run: echo ok\n",
            )
            write_fixture(
                root,
                ".github/workflows/reuse.yml",
                "on:\n  workflow_call:\njobs:\n  inner:\n"
                "    runs-on: ubuntu-26.04\n    steps:\n      - run: echo ok\n",
            )
            write_fixture(
                root,
                ".github/workflows/ci.yml",
                "on:\n  push:\njobs:\n  build:\n    steps:\n"
                "      - uses: ./.github/actions/x\n"
                "  call-reuse:\n"
                "    uses: ./.github/workflows/reuse.yml\n",
            )
            self.assertEqual(scan(root), [])

    def test_quoted_local_action_outside_scanned_set_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            write_fixture(
                root,
                "tools/gate/action.yml",
                "runs:\n  using: composite\n  steps:\n    - run: echo ok\n",
            )
            write_fixture(
                root,
                ".github/workflows/ci.yml",
                "on:\n  push:\njobs:\n  gate-job:\n    steps:\n"
                "      - uses: './tools/gate'\n",
            )
            self.assertEqual(
                scan(root),
                [
                    (
                        ".github/workflows/ci.yml:6:gate-job: "
                        "local action not scanned: ./tools/gate"
                    )
                ],
            )

    def test_local_action_boundaries_and_gitea_depth_fail_closed(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            write_fixture(
                root,
                ".github/actions/clean/action.yml",
                "runs:\n  using: composite\n  steps:\n    - run: echo ok\n",
            )
            (root / ".github/actions/empty").mkdir(parents=True)
            write_fixture(root, "tools/reuse.yml", "on:\n  workflow_call:\n")
            write_fixture(
                root,
                ".github/workflows/action.yml",
                "jobs:\n  w:\n    if: github.actor_id != 1\n",
            )
            write_fixture(
                root,
                ".gitea/actions/sub/deep/action.yaml",
                "runs:\n  using: composite\n  steps:\n"
                "    - if: gitea.actor != vars.BOT_LOGIN\n",
            )
            write_fixture(
                root,
                ".github/workflows/z.yml",
                "jobs:\n  b:\n    steps:\n"
                "      # - uses: ./tools/ignored\n"
                '      - name: File target\n        uses: "./tools/reuse.yml" # note\n'
                "      - uses: ./.github/actions/empty\n"
                "      - uses: >-\n          ./tools/gate\n",
            )
            self.assertEqual(
                scan(root),
                [
                    (
                        ".github/workflows/action.yml:3:w: "
                        "disallowed actor reference: if: github.actor_id != 1"
                    ),
                    (
                        ".github/workflows/z.yml:6:b: "
                        "local action not scanned: ./tools/reuse.yml"
                    ),
                    (
                        ".github/workflows/z.yml:7:b: "
                        "local action not scanned: ./.github/actions/empty"
                    ),
                    (
                        ".github/workflows/z.yml:9:b: "
                        "local action not scanned: ./tools/gate"
                    ),
                    (
                        ".gitea/actions/sub/deep/action.yaml:4:<top>: "
                        "disallowed actor reference: - if: gitea.actor != vars.BOT_LOGIN"
                    ),
                ],
            )

    def test_local_action_symlink_target_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            try:
                write_fixture(
                    root,
                    ".github/actions/clean/action.yml",
                    "runs:\n  using: composite\n  steps:\n    - run: echo ok\n",
                )
                (root / "tools").mkdir()
                (root / "tools/link").symlink_to(
                    root / ".github/actions/clean", target_is_directory=True
                )
            except OSError as exc:
                self.skipTest(f"symlink creation unavailable: {exc}")
            write_fixture(
                root,
                ".github/workflows/ci.yml",
                "on:\n  push:\njobs:\n  b:\n    steps:\n      - uses: ./tools/link\n",
            )
            self.assertEqual(
                scan(root),
                [
                    (
                        ".github/workflows/ci.yml:6:b: "
                        "local action not scanned: ./tools/link"
                    )
                ],
            )

    def test_composite_action_renovate_literal_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            yaml_action = root / ".github/actions/x/action.yaml"
            yaml_action.parent.mkdir(parents=True)
            yaml_action.write_text(
                "runs:\n  using: composite\n  steps:\n"
                "    - shell: bash\n"
                "      run: '[ \"$ACTOR\" = renovate ] && exit 0'\n",
                encoding="utf-8",
            )
            findings = scan(root)
            self.assertEqual(len(findings), 1)
            self.assertTrue(
                findings[0].startswith(".github/actions/x/action.yaml:5:<top>:"),
                findings[0],
            )
            yml_action = root / ".github/actions/y/action.yml"
            yml_action.parent.mkdir(parents=True)
            yml_action.write_text(
                "runs:\n  using: composite\n  steps:\n"
                "    - if: !startsWith(github.head_ref, 'renovate/')\n",
                encoding="utf-8",
            )
            ordered = scan(root)
            self.assertEqual(len(ordered), 2)
            self.assertTrue(
                ordered[0].startswith(".github/actions/x/action.yaml:5:<top>:"),
                ordered[0],
            )
            self.assertTrue(
                ordered[1].startswith(".github/actions/y/action.yml:4:<top>:"),
                ordered[1],
            )

    def test_lowercase_actor_env_step_run_fails(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  semgrep:\n    steps:\n"
            "      - run: echo $env:github_actor\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:6:semgrep:"),
            findings[0],
        )
        other_envs = check(
            ".github/workflows/security-scan.yml",
            "on:\n  push:\njobs:\n  semgrep:\n    steps:\n"
            "      - run: echo $env:github_actor_id\n"
            "      - run: echo $env:github_triggering_actor\n"
            "      - run: echo $env:gitea_actor\n"
            "      - run: echo $env:gitea_triggering_actor\n",
        )
        self.assertEqual(len(other_envs), 4)

    def test_flow_mapping_local_uses_bypass_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            write_fixture(
                root,
                "tools/gate/action.yml",
                "runs:\n  using: composite\n  steps:\n"
                "    - if: github.actor != vars.BOT_LOGIN\n",
            )
            write_fixture(
                root,
                ".github/workflows/ci.yml",
                "on:\n  push:\njobs:\n  gate-job:\n    steps:\n"
                "      - {uses: ./tools/gate}\n",
            )
            self.assertEqual(
                scan(root),
                [".github/workflows/ci.yml:6:gate-job: flow-style uses refused"],
            )

    def test_alias_local_uses_bypass_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            write_fixture(
                root,
                "tools/gate/action.yml",
                "runs:\n  using: composite\n  steps:\n"
                "    - if: github.actor != vars.BOT_LOGIN\n",
            )
            write_fixture(
                root,
                ".github/workflows/ci.yml",
                "x-gate: &gate ./tools/gate\n"
                "on:\n  push:\njobs:\n  gate-job:\n    steps:\n"
                "      - uses: *gate\n",
            )
            self.assertEqual(
                scan(root),
                [
                    (
                        ".github/workflows/ci.yml:7:gate-job: "
                        "uses value not recognised: *gate"
                    )
                ],
            )

    def test_next_line_local_uses_bypass_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            write_fixture(
                root,
                "tools/gate/action.yml",
                "runs:\n  using: composite\n  steps:\n"
                "    - if: github.actor != vars.BOT_LOGIN\n",
            )
            write_fixture(
                root,
                ".github/workflows/ci.yml",
                "on:\n  push:\njobs:\n  gate-job:\n    steps:\n"
                "      - uses:\n"
                "          ./tools/gate\n",
            )
            self.assertEqual(
                scan(root),
                [".github/workflows/ci.yml:6:gate-job: uses value not recognised: "],
            )

    def test_tag_local_uses_bypass_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            write_fixture(
                root,
                "tools/gate/action.yml",
                "runs:\n  using: composite\n  steps:\n"
                "    - if: github.actor != vars.BOT_LOGIN\n",
            )
            write_fixture(
                root,
                ".github/workflows/ci.yml",
                "on:\n  push:\njobs:\n  gate-job:\n    steps:\n"
                "      - uses: !!str ./tools/gate\n",
            )
            self.assertEqual(
                scan(root),
                [
                    (
                        ".github/workflows/ci.yml:6:gate-job: "
                        "uses value not recognised: !!str ./tools/gate"
                    )
                ],
            )

    def test_recognised_remote_and_image_uses_forms_pass(self) -> None:
        text = (
            "on:\n  push:\njobs:\n  build:\n    steps:\n"
            "      - uses: actions/checkout@"
            "3d3c42e5aac5ba805825da76410c181273ba90b1 # v7\n"
            "      - uses: golusoris/golusoris/.github/actions/"
            "stop-on-draft@e601567f4e1acb72525f361f80dac6d89cbad553 # #1212\n"
            "      - uses: 'actions/cache/save@v5'\n"
            "      - uses: docker://alpine:3.22\n"
            "      - uses: >-\n"
            "          actions/checkout@"
            "3d3c42e5aac5ba805825da76410c181273ba90b1\n"
        )
        self.assertEqual(check(".github/workflows/ci.yml", text), [])

    def test_remote_uses_without_ref_fails(self) -> None:
        text = (
            "on:\n  push:\njobs:\n  build:\n    steps:\n"
            "      - uses: actions/checkout\n"
        )
        self.assertEqual(
            check(".github/workflows/ci.yml", text),
            [
                (
                    ".github/workflows/ci.yml:6:build: "
                    "uses value not recognised: actions/checkout"
                )
            ],
        )

    def test_unrecognised_anchor_block_and_comma_flow_uses_fail(self) -> None:
        text = (
            "on:\n  push:\njobs:\n  build:\n    steps:\n"
            "      - uses: &gate ./tools/gate\n"
            "      - uses: >-\n"
            "          actions/checkout\n"
            '      - {name: gate, "uses": "./tools/gate"}\n'
            "      - {'uses' : ./tools/gate}\n"
            "      - uses: ./tools/gate@v1\n"
            "      - uses: ''\n"
            '      - uses: "actions/checkout@v4 extra"\n'
            '      - uses: "docker://alpine:3.22 extra"\n'
        )
        self.assertEqual(
            check(".github/workflows/ci.yml", text),
            [
                (
                    ".github/workflows/ci.yml:6:build: "
                    "uses value not recognised: &gate ./tools/gate"
                ),
                (
                    ".github/workflows/ci.yml:8:build: "
                    "uses value not recognised: actions/checkout"
                ),
                ".github/workflows/ci.yml:9:build: flow-style uses refused",
                ".github/workflows/ci.yml:10:build: flow-style uses refused",
                (
                    ".github/workflows/ci.yml:11:build: "
                    "uses value not recognised: ./tools/gate@v1"
                ),
                ".github/workflows/ci.yml:12:build: uses value not recognised: ''",
                (
                    ".github/workflows/ci.yml:13:build: "
                    'uses value not recognised: "actions/checkout@v4 extra"'
                ),
                (
                    ".github/workflows/ci.yml:14:build: "
                    'uses value not recognised: "docker://alpine:3.22 extra"'
                ),
            ],
        )

    def test_gap_repo_variable_bot_prefix_if(self) -> None:
        """Known limit: repository variable exclusion carries no actor or bot token."""
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  semgrep:\n"
            "    if: ${{ !startsWith(github.head_ref, vars.BOT_PREFIX) }}\n"
            "    runs-on: ubuntu-26.04\n"
        )
        self.assertEqual(check(".github/workflows/security-scan.yml", text), [])

    def test_gap_script_file_step_run(self) -> None:
        """Known limit: actor logic inside an external script file is not read."""
        text = (
            "on:\n  push:\n"
            "jobs:\n  semgrep:\n    steps:\n"
            "      - run: bash scripts/ci/skip-bots.sh\n"
        )
        self.assertEqual(check(".github/workflows/security-scan.yml", text), [])

    def test_gap_third_party_action_uses_step(self) -> None:
        """Known limit: third-party action internals in uses steps are not scanned."""
        text = (
            "on:\n  push:\n"
            "jobs:\n  semgrep:\n    steps:\n"
            "      - uses: some-org/skip-bots@v1\n"
        )
        self.assertEqual(check(".github/workflows/security-scan.yml", text), [])

    def test_explicit_key_local_uses_bypass_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            write_fixture(
                root,
                "tools/gate/action.yml",
                "runs:\n  using: composite\n  steps:\n"
                "    - if: github.actor != vars.BOT_LOGIN\n",
            )
            write_fixture(
                root,
                ".github/workflows/ci.yml",
                "on:\n  push:\njobs:\n  gate-job:\n    steps:\n"
                "      - ? uses\n"
                "        : ./tools/gate\n",
            )
            self.assertEqual(
                scan(root),
                [".github/workflows/ci.yml:6:gate-job: explicit key refused"],
            )
            write_fixture(
                root,
                ".github/workflows/ci.yml",
                "on:\n  push:\njobs:\n  gate-job:\n    steps:\n"
                "      # ? uses\n"
                "      - name: check ? mark\n"
                "        env:\n          ?plain: ok\n"
                "        run: |\n          ? uses\n"
                "      - - ? uses\n"
                "          : ./tools/gate\n"
                "      ?\n"
                "      : ignored\n",
            )
            self.assertEqual(
                scan(root),
                [
                    ".github/workflows/ci.yml:12:gate-job: explicit key refused",
                    ".github/workflows/ci.yml:14:gate-job: explicit key refused",
                ],
            )

    def test_anchor_tag_and_case_uses_key_forms_fail(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            write_fixture(
                root,
                "tools/gate/action.yml",
                "runs:\n  using: composite\n  steps:\n"
                "    - if: github.actor != vars.BOT_LOGIN\n",
            )
            write_fixture(
                root,
                ".github/workflows/ci.yml",
                "on:\n  push:\njobs:\n  gate-job:\n    steps:\n"
                "      - &k uses: ./tools/gate\n"
                "      - !!str uses: ./tools/gate\n"
                "      - Uses: ./tools/gate\n",
            )
            self.assertEqual(
                scan(root),
                [
                    ".github/workflows/ci.yml:6:gate-job: uses key form not recognised",
                    ".github/workflows/ci.yml:7:gate-job: uses key form not recognised",
                    ".github/workflows/ci.yml:8:gate-job: uses key form not recognised",
                ],
            )

    def test_run_block_echo_uses_passes(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            write_fixture(
                root,
                "tools/gate/action.yml",
                "runs:\n  using: composite\n  steps:\n"
                "    - if: github.actor != vars.BOT_LOGIN\n",
            )
            write_fixture(
                root,
                ".github/workflows/ci.yml",
                "on:\n  push:\njobs:\n  gate-job:\n    steps:\n"
                "      - env:\n          reuses: ok\n          uses-cache: ok\n"
                "        run: |\n"
                '          echo "uses: ./tools/gate"\n',
            )
            self.assertEqual(scan(root), [])

    def test_prose_and_multi_match_uses_key_forms_fail(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            write_fixture(
                root,
                "tools/gate/action.yml",
                "runs:\n  using: composite\n  steps:\n"
                "    - if: github.actor != vars.BOT_LOGIN\n",
            )
            write_fixture(
                root,
                ".github/workflows/ci.yml",
                "on:\n  push:\njobs:\n  gate-job:\n    steps:\n"
                '      - name: "see uses: docs"\n'
                "        run: echo ok\n"
                "      - &q 'uses' : ./tools/gate\n"
                "      - uses: actions/checkout@v4 # uses: ./tools/gate\n",
            )
            self.assertEqual(
                scan(root),
                [
                    ".github/workflows/ci.yml:6:gate-job: uses key form not recognised",
                    ".github/workflows/ci.yml:8:gate-job: uses key form not recognised",
                    ".github/workflows/ci.yml:9:gate-job: uses key form not recognised",
                    ".github/workflows/ci.yml:9:gate-job: uses key form not recognised",
                ],
            )

    def test_unexpected_line_separators_fail(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            write_fixture(
                root,
                "tools/gate/action.yml",
                "runs:\n  using: composite\n  steps:\n"
                "    - if: github.actor != vars.BOT_LOGIN\n",
            )
            write_fixture(
                root,
                ".github/workflows/ci.yml",
                "on:\n  push:\njobs:\n  gate-job:\n    steps:\n"
                "      # hide\u2028    x: |\n"
                "      - uses: ./tools/gate\n",
            )
            self.assertEqual(
                scan(root),
                [".github/workflows/ci.yml:1:<top>: unexpected line separator"],
            )
            for sep in (
                "\x0b",
                "\x0c",
                "\x1c",
                "\x1d",
                "\x1e",
                "\x85",
                "\u2028",
                "\u2029",
            )[:MAX_WORKFLOW_FILES]:
                write_fixture(
                    root,
                    ".github/workflows/ci.yml",
                    f"on:\n  push:\njobs:\n  b:\n    steps:\n      - run: echo a{sep}b\n",
                )
                self.assertEqual(
                    scan(root),
                    [".github/workflows/ci.yml:1:<top>: unexpected line separator"],
                )


if __name__ == "__main__":
    sys.exit(0 if unittest.main(exit=False).result.wasSuccessful() else 1)
