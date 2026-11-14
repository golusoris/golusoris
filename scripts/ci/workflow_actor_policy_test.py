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
    ALLOWED,
    ALLOWED_LINES,
    MAX_WORKFLOW_LINES,
    REPO_ROOT,
    check,
    scan,
    scan_with_hits,
)

_FIXTURE_NAME = ".github/workflows/security-scan.yml"
_FIXTURE_PREFIX = "on:\n  push:\njobs:\n  build:\n"
_NEW_FAILURES: dict[str, tuple[str, ...]] = {
    "computed_index": (
        "    if: github[format('{0}ctor_id', 'a')] != 1\n",
        "    steps:\n      - if: gitea[x] != 1\n",
        "    steps:\n      - run: echo ${{ github.event[x] }}\n",
        "    if: github.event.pull_request[x] != 1\n",
        "    if: github . event [0] != 1\n",
        "    if: GITHUB . event.* [inputs.k] != 1\n",
    ),
    "computed_bot": (
        "    if: startsWith(github.head_ref, format('{0}vate/', 'reno'))\n",
        "    steps:\n      - if: startsWith(github.head_ref, format('{0}vate/', 'reno'))\n",
        "    env:\n      X: ${{ startsWith(github.head_ref, join(fromJSON('[\"reno\",\"vate\"]'), '')) }}\n",
        "    'if': FORMAT('{0}', 'x') == 'x'\n",
        "    steps:\n      - \"if\": tojson(inputs.x) != ''\n",
        "    if: >-\n      fromJSON(inputs.x)\n",
    ),
    "escapes": (
        r'    if: "github.\x61ctor_id != 1"' + "\n",
        r'    if: "github.\u0061ctor_id != 1"' + "\n",
        r'    if: "github.\U00000061ctor_id != 1"' + "\n",
        r'''    if: "startsWith(github.head_ref, '\x72enovate/')"''' + "\n",
        r'    if: "github.\tactor_id != 1"' + "\n",
        r'''    if: "github['\x61ctor'] != 'x'"''' + "\n",
        r'    if: "github\x2e actor_id != 1"' + "\n",
        r'    if: "github.\nactor_id != 1"' + "\n",
        r'    if: "github.\ actor_id != 1"' + "\n",
        r'    if: "github.\Nactor_id != 1"' + "\n",
        r'    if: "github.\_actor_id != 1"' + "\n",
        r'    if: "github.\Lactor_id != 1"' + "\n",
        r'    if: "github.\Pactor_id != 1"' + "\n",
        r'    if: "github\x5binputs.k] != 1"' + "\n",
        r'    if: "\x66ormat(inputs.x)"' + "\n",
    ),
    "line_break": (
        "    if: \"!startsWith(github.head_ref, 'reno\\\n      vate/')\"\n",
    ),
    "dot_whitespace": (
        "    if: github. actor_id == 1\n",
        "    steps:\n      - run: echo ${{ github. triggering_actor }}\n",
        "    env:\n      X: ${{ github\t. actor }}\n",
        "    if: github . actor_id == 1\n",
    ),
    "multiline": (
        "    steps:\n      - run: |\n          echo ${{ github.\n          actor }}\n",
        "    if: github.\n      actor_id == 1\n",
        "    if: >-\n      github.\n      actor_id == 1\n",
        "    steps:\n      - 'if': github.\n          # comment\n\n          actor_id == 1\n",
        "    if: |\n      github .\n\n      actor_id == 1\n",
    ),
}


class WorkflowActorPolicyTest(unittest.TestCase):
    def test_allowed_entries_are_pull_request_only(self) -> None:
        self.assertEqual(len(ALLOWED), 3)
        for (path, job, expr), reason in ALLOWED.items():
            self.assertTrue(path and job and reason.strip())
            self.assertTrue(
                expr.endswith("&& github.event_name == 'pull_request'"),
                f"{path}:{job}: {expr}",
            )

    def test_all_allowances_are_hit_by_repository_scan(self) -> None:
        self.assertEqual(len(ALLOWED_LINES), 21)
        for (path, job, line_text), reason in list(ALLOWED_LINES.items())[
            :MAX_WORKFLOW_LINES
        ]:
            self.assertTrue(path and job and line_text and reason.strip())
        findings, hit_ifs, hit_lines = scan_with_hits(REPO_ROOT)
        self.assertEqual(findings, [])
        self.assertEqual(hit_ifs, set(ALLOWED))
        self.assertEqual(hit_lines, set(ALLOWED_LINES))

    def test_scan_with_hits_omits_unhit_allowances(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            workflows = root / ".github/workflows"
            workflows.mkdir(parents=True)
            (workflows / "ok.yml").write_text(
                "on:\n  push:\njobs:\n  ok:\n    runs-on: ubuntu-26.04\n",
                encoding="utf-8",
            )
            findings, hit_ifs, hit_lines = scan_with_hits(root)
            self.assertEqual(findings, [])
            self.assertEqual(hit_ifs, set())
            self.assertEqual(hit_lines, set())

    def test_push_workflow_actor_if_fails(self) -> None:
        text = (
            "on:\n  push:\n    branches: [main]\n"
            "jobs:\n  semgrep:\n    if: github.actor != 'renovate[bot]'\n"
            "    runs-on: ubuntu-26.04\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertIn(".github/workflows/security-scan.yml:6:semgrep:", findings[0])

    def test_wrapped_expression_if_fails(self) -> None:
        text = (
            "on:\n  workflow_call:\n"
            "jobs:\n  spectral:\n"
            "    if: ${{ github.actor != 'renovate[bot]' && inputs.openapi-spec != '' }}\n"
        )
        findings = check(".github/workflows/ci-go.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertIn(".github/workflows/ci-go.yml:5:spectral:", findings[0])

    def test_unlisted_pr_scoped_expression_fails(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  semgrep:\n"
            "    if: github.actor != 'renovate[bot]' && github.event_name == 'pull_request'\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertIn(".github/workflows/security-scan.yml:5:semgrep:", findings[0])

    def test_block_scalar_if_naming_actor_fails(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  pr-title:\n    if: >-\n"
            "      github.actor != 'renovate[bot]' && github.event_name == 'pull_request'\n"
        )
        findings = check(".github/workflows/ci.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertIn(".github/workflows/ci.yml:6:pr-title:", findings[0])

    def test_block_scalar_if_actor_id_fails(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  semgrep:\n    if: >-\n"
            "      github.actor_id != 29139614\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:6:semgrep:"),
            findings[0],
        )

    def test_run_and_other_actor_tokens_fail(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  build:\n    steps:\n"
            "      - run: |\n"
            "          username: ${{ github.actor }}\n"
            "      - if: github.triggering_actor != vars.BOT_LOGIN\n"
            "        run: echo ${{ github.event.sender.login }} ${{ github.event.issue.user.login }}\n"
        )
        findings = check(".github/workflows/build.yml", text)
        self.assertEqual(len(findings), 3)
        self.assertIn(".github/workflows/build.yml:7:build:", findings[0])
        self.assertIn(".github/workflows/build.yml:8:build:", findings[1])
        self.assertIn(".github/workflows/build.yml:9:build:", findings[2])

    def test_username_actor_value_line_pass_and_fail_pair(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  goreleaser:\n    steps:\n"
            "      - uses: docker/login-action@v4\n"
            "        with:\n"
            "          username: ${{ github.actor }}\n"
        )
        self.assertEqual(check(".github/workflows/release-go.yml", text), [])
        findings = check(".github/workflows/other.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertTrue(
            findings[0].startswith(".github/workflows/other.yml:8:goreleaser:"),
            findings[0],
        )
        wrong_job_findings = check(
            ".github/workflows/release-go.yml",
            text.replace("  goreleaser:\n", "  publish:\n", 1),
        )
        self.assertEqual(len(wrong_job_findings), 1)
        self.assertTrue(
            wrong_job_findings[0].startswith(
                ".github/workflows/release-go.yml:8:publish:"
            ),
            wrong_job_findings[0],
        )
        run_block_findings = check(
            ".github/workflows/release-go.yml",
            "on:\n  push:\njobs:\n  goreleaser:\n    steps:\n"
            "      - run: |\n          username: ${{ github.actor }}\n",
        )
        self.assertEqual(len(run_block_findings), 1)
        self.assertTrue(
            run_block_findings[0].startswith(
                ".github/workflows/release-go.yml:7:goreleaser:"
            ),
            run_block_findings[0],
        )

    def test_allowed_entry_passes_plain_and_wrapped(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  pr-title:\n"
            "    if: github.actor != 'renovate[bot]' && github.event_name == 'pull_request'\n"
            "  dco:\n"
            "    if: ${{  github.actor != 'renovate[bot]'   && github.actor != 'github-actions[bot]' && github.event_name == 'pull_request'  }}\n"
        )
        self.assertEqual(check(".github/workflows/ci.yml", text), [])

    def test_scan_checks_github_and_gitea_workflows(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / ".github/workflows").mkdir(parents=True)
            (root / ".gitea/workflows").mkdir(parents=True)
            (root / ".github/workflows/a.yaml").write_text(
                "jobs:\n  a:\n    if: sender.login != 'bot'\n",
                encoding="utf-8",
            )
            (root / ".gitea/workflows/b.yml").write_text(
                "jobs:\n  b:\n    if: user.login != 'bot'\n",
                encoding="utf-8",
            )
            findings = scan(root)
            self.assertEqual(len(findings), 2)
            self.assertIn(".github/workflows/a.yaml:3:a:", findings[0])
            self.assertIn(".gitea/workflows/b.yml:3:b:", findings[1])

    def test_github_actor_id_job_if_fails(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  semgrep:\n"
            "    if: github.actor_id != 29139614\n"
            "    runs-on: ubuntu-26.04\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertIn(".github/workflows/security-scan.yml:5:semgrep:", findings[0])

    def test_gitea_workflow_actor_tokens_fail(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            workflows = root / ".gitea/workflows"
            workflows.mkdir(parents=True)
            (workflows / "ci.yml").write_text(
                "on:\n  push:\n"
                "jobs:\n  lint:\n"
                "    if: gitea.actor != vars.BOT_LOGIN\n"
                "    steps:\n"
                "      - if: gitea.triggering_actor != vars.BOT_LOGIN\n"
                "        run: echo ${{ gitea.actor_id }}\n",
                encoding="utf-8",
            )
            findings = scan(root)
            self.assertEqual(len(findings), 3)
            self.assertIn(".gitea/workflows/ci.yml:5:lint:", findings[0])
            self.assertIn(".gitea/workflows/ci.yml:7:lint:", findings[1])
            self.assertIn(".gitea/workflows/ci.yml:8:lint:", findings[2])

    def test_step_user_id_and_sender_id_if_fail(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  verify:\n    steps:\n"
            "      - if: github.event.pull_request.user.id != 29139614\n"
            "        run: echo pr\n"
            "      - if: github.event.sender.id != 29139614\n"
            "        run: echo sender\n"
        )
        findings = check(".github/workflows/ci.yml", text)
        self.assertEqual(len(findings), 2)
        self.assertIn(".github/workflows/ci.yml:6:verify:", findings[0])
        self.assertIn(".github/workflows/ci.yml:8:verify:", findings[1])

    def test_non_github_actor_value_lines_fail(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  publish:\n    steps:\n"
            "      - env:\n"
            "          ACTOR_ID: ${{ github.actor_id }}\n"
            "          GITEA_ACTOR: ${{ gitea.actor }}\n"
            "        run: echo ok\n"
        )
        findings = check(".github/workflows/release-go.yml", text)
        self.assertEqual(len(findings), 2)
        self.assertIn(".github/workflows/release-go.yml:7:publish:", findings[0])
        self.assertIn(".github/workflows/release-go.yml:8:publish:", findings[1])

    def test_actor_token_word_boundaries_pass(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  build:\n    steps:\n"
            "      - env:\n"
            "          A: ${{ inputs.my_actor.value }} ${{ inputs.actors }}\n"
            "          B: ${{ inputs.github.actor_identity }}\n"
            "          C: ${{ inputs.my_actor_id.value }} ${{ inputs.x_gitea.actor_ids }}\n"
            "          D: ${{ inputs.gitea.actors }} ${{ inputs.gitea.triggering_actors }}\n"
            "          E: ${{ inputs.app_user.id }}\n"
            "          F: ${{ inputs.custom_sender.id }}\n"
            "          G: ${{ inputs.users.identifier }}\n"
            "          H: ${{ inputs.senders.identity }}\n"
            "          I: githubxactor github_actors my_gitea_actor_id gitea_actor_ids sender_id user_id\n"
            "          J: ${{ inputs.my_pusher.name }} ${{ inputs.pushers.name }}\n"
            "          K: ${{ inputs.co_author.name }} ${{ inputs.authors.name }}\n"
            "          L: ${{ inputs.my_committer.email }} ${{ inputs.committers.email }}\n"
            "          M: ${{ inputs.app_user.type }} ${{ inputs.custom_sender.type }} ${{ inputs.my_merged_by.type }} ${{ inputs.merged_by_user.type }}\n"
            "          N: ${{ inputs.my_actor.id }} ${{ inputs.co_assignee.id }} ${{ inputs.assignees.id }} ${{ inputs.my_requested_reviewer.id }} ${{ inputs.requested_reviewers.id }}\n"
            "          O: ${{ inputs.my_author_association }} ${{ inputs.author_associations }} ${{ inputs.x.logins }} ${{ inputs.my_login.id }} MY_GITHUB_ACTOR GITHUB_ACTOR_NAME MY_GITEA_ACTOR GITEA_ACTOR_NAME\n"
            "        run: echo ok\n"
        )
        self.assertEqual(check(".github/workflows/build.yml", text), [])

    def test_job_if_head_ref_renovate_fails(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  semgrep:\n"
            "    if: ${{ !startsWith(github.head_ref, 'renovate/') }}\n"
            "    runs-on: ubuntu-26.04\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:5:semgrep:"),
            findings[0],
        )

    def test_step_dash_if_user_type_fails(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  verify:\n    steps:\n"
            "      - if: github.event.pull_request.user.type != 'Bot'\n"
            "        run: echo check\n"
        )
        findings = check(".github/workflows/ci.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertTrue(
            findings[0].startswith(".github/workflows/ci.yml:6:verify:"),
            findings[0],
        )

    def test_job_if_pusher_name_fails(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  trivy:\n"
            "    if: github.event.pusher.name != 'renovate[bot]'\n"
            "    runs-on: ubuntu-26.04\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:5:trivy:"),
            findings[0],
        )

    def test_step_key_if_author_username_fails(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  scan:\n    steps:\n"
            "      - name: Run scanner\n"
            "        if: github.event.head_commit.author.username != 'renovate-bot'\n"
            "        run: echo scan\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:7:scan:"),
            findings[0],
        )

    def test_job_block_scalar_if_dependabot_fails(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  semgrep:\n    if: >-\n"
            "      !startsWith(github.head_ref, 'dependabot/')\n"
            "    runs-on: ubuntu-26.04\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:6:semgrep:"),
            findings[0],
        )

    def test_renovate_on_demand_lines_pass_and_fail_pair(self) -> None:
        text = (REPO_ROOT / ".gitea/workflows/renovate-on-demand.yml").read_text(
            encoding="utf-8"
        )
        self.assertEqual(check(".gitea/workflows/renovate-on-demand.yml", text), [])
        findings = check(".github/workflows/other.yml", text)
        expected = (
            (5, "<top>"),
            (13, "<top>"),
            (17, "renovate"),
            (24, "renovate"),
            (27, "renovate"),
            (29, "renovate"),
            (30, "renovate"),
            (31, "renovate"),
            (32, "renovate"),
            (33, "renovate"),
            (34, "renovate"),
            (37, "renovate"),
            (38, "renovate"),
            (39, "renovate"),
        )
        self.assertEqual(len(findings), len(expected))
        for idx, (lineno, job) in enumerate(expected):
            self.assertTrue(
                findings[idx].startswith(
                    f".github/workflows/other.yml:{lineno}:{job}:"
                ),
                findings[idx],
            )

    def test_quoted_job_if_keys_fail(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  semgrep:\n"
            "    \"if\": ${{ !startsWith(github.head_ref, 'renovate/') }}\n"
            "  trivy:\n"
            "    'if': ${{ !startsWith(github.head_ref, 'renovate/') }}\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 2)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:5:semgrep:"),
            findings[0],
        )
        self.assertTrue(
            findings[1].startswith(".github/workflows/security-scan.yml:7:trivy:"),
            findings[1],
        )

    def test_explicit_indent_block_scalar_if_fails(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  semgrep:\n"
            "    if: >2-\n"
            "      !startsWith(github.head_ref, 'renovate/')\n"
            "  trivy:\n"
            "    if: |2-\n"
            "      !startsWith(github.head_ref, 'renovate/')\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 2)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:6:semgrep:"),
            findings[0],
        )
        self.assertTrue(
            findings[1].startswith(".github/workflows/security-scan.yml:9:trivy:"),
            findings[1],
        )

    def test_jobs_key_with_space_before_colon_fails(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs :\n  lint:\n"
            "    if: ${{ !startsWith(github.head_ref, 'renovate/') }}\n"
            "  trivy :\n"
            "    if: github.actor_id != 29139614\n"
            '  "audit"\t:\n'
            "    if: github.actor_id != 29139614\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        expected = ((5, "lint"), (7, "trivy"), (9, "audit"))
        self.assertEqual(len(findings), len(expected))
        for idx, (lineno, job) in enumerate(expected):
            self.assertTrue(
                findings[idx].startswith(
                    f".github/workflows/security-scan.yml:{lineno}:{job}:"
                ),
                findings[idx],
            )
        tab_top = check(
            ".github/workflows/security-scan.yml",
            'on:\n  push:\n"jobs"\t:\n  lint:\n    if: github.actor_id != 29139614\n',
        )
        self.assertEqual(len(tab_top), 1)
        self.assertTrue(
            tab_top[0].startswith(".github/workflows/security-scan.yml:5:lint:"),
            tab_top[0],
        )

    def test_index_syntax_actor_tokens_fail(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  semgrep:\n"
            "    if: ${{ github['actor_id'] != '29139614' }}\n"
            "  trivy:\n"
            '    if: github["actor"] != vars.BOT_LOGIN\n'
            "  chained:\n"
            "    if: github['event']['workflow_run']['actor']['login'] != vars.BOT_LOGIN\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 3)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:5:semgrep:"),
            findings[0],
        )
        self.assertIn("github['actor_id']", findings[0])
        self.assertTrue(
            findings[1].startswith(".github/workflows/security-scan.yml:7:trivy:"),
            findings[1],
        )
        self.assertIn('github["actor"]', findings[1])
        self.assertTrue(
            findings[2].startswith(".github/workflows/security-scan.yml:9:chained:"),
            findings[2],
        )

    def test_workflow_run_actor_id_fails(self) -> None:
        text = (
            "on:\n  workflow_run:\n"
            "jobs:\n  semgrep:\n"
            "    if: github.event.workflow_run.actor.id != 29139614\n"
            "    steps:\n"
            "      - if: github.event.workflow_run.actor.node_id != vars.BOT_NODE_ID\n"
            "        run: echo one\n"
            "      - if: github.event.workflow_run.actor.type != 'Bot'\n"
            "        run: echo two\n"
        )
        self.assertEqual(
            check(".github/workflows/security-scan.yml", text),
            [
                (
                    ".github/workflows/security-scan.yml:5:semgrep: "
                    "disallowed actor reference: "
                    "if: github.event.workflow_run.actor.id != 29139614"
                ),
                (
                    ".github/workflows/security-scan.yml:7:semgrep: "
                    "disallowed actor reference: "
                    "- if: github.event.workflow_run.actor.node_id != vars.BOT_NODE_ID"
                ),
                (
                    ".github/workflows/security-scan.yml:9:semgrep: "
                    "disallowed actor reference: "
                    "- if: github.event.workflow_run.actor.type != 'Bot'"
                ),
            ],
        )
        clauses = (
            "if: github.event.workflow_run.triggering_actor.id != 29139614",
            "if: github.event.release.author.login != 'x'",
            "if: github.event.pull_request.merged_by.type != 'Bot'",
            "if: github.event.sender.site_admin",
            "if: toJSON(github.event.sender) != ''",
            "if: github.event['workflow_run']['triggering_actor']['id'] != 1",
            "if: contains(github.event.head_commit.author.login, 'x')",
        )
        for clause in clauses[:MAX_WORKFLOW_LINES]:
            with self.subTest(clause=clause):
                self.assertEqual(
                    check(
                        ".github/workflows/security-scan.yml",
                        f"on:\n  workflow_run:\njobs:\n  semgrep:\n    {clause}\n",
                    ),
                    [
                        (
                            f".github/workflows/security-scan.yml:5:semgrep: "
                            f"disallowed actor reference: {clause}"
                        )
                    ],
                )

    def test_identity_words_without_property_access_pass(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  build:\n    steps:\n"
            "      - name: Check author\n"
            "        run: echo user\n"
        )
        self.assertEqual(check(".github/workflows/build.yml", text), [])

    def test_author_association_fails(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  semgrep:\n"
            "    if: github.event.pull_request.author_association != 'BOT'\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:5:semgrep:"),
            findings[0],
        )

    def test_user_node_id_fails(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  semgrep:\n"
            "    if: github.event.pull_request.user.node_id != vars.BOT_NODE_ID\n"
            "    steps:\n"
            "      - if: github.event.sender.node_id != vars.BOT_NODE_ID\n"
            "        run: echo ok\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 2)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:5:semgrep:"),
            findings[0],
        )
        self.assertTrue(
            findings[1].startswith(".github/workflows/security-scan.yml:7:semgrep:"),
            findings[1],
        )

    def test_step_actor_env_and_default_actor_env_vars_fail(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  semgrep:\n    steps:\n"
            "      - env:\n"
            "          ACTOR: ${{ github.actor }}\n"
            "        run: echo ok\n"
            '      - run: test "$GITHUB_ACTOR" != "$SKIP" || exit 0\n'
            '      - run: echo "$GITHUB_ACTOR_ID"\n'
            '      - run: echo "$GITHUB_TRIGGERING_ACTOR"\n'
            '      - run: echo "$GITEA_ACTOR"\n'
            '      - run: echo "$GITEA_ACTOR_ID"\n'
            '      - run: echo "$GITEA_TRIGGERING_ACTOR"\n'
        )
        findings = check(".github/workflows/security-scan.yml", text)
        expected = (7, 9, 10, 11, 12, 13, 14)
        self.assertEqual(len(findings), len(expected))
        for idx, lineno in enumerate(expected):
            self.assertTrue(
                findings[idx].startswith(
                    f".github/workflows/security-scan.yml:{lineno}:semgrep:"
                ),
                findings[idx],
            )

    def test_step_run_bot_literal_compare_fails(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  semgrep:\n    steps:\n"
            "      - run: '[ \"$ACTOR\" = renovate ] && exit 0'\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:6:semgrep:"),
            findings[0],
        )

    def test_scan_invalid_utf8_workflow_reports_unreadable_finding(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            workflows = root / ".github/workflows"
            workflows.mkdir(parents=True)
            (workflows / "broken.yml").write_bytes(b"name: \xff\xfe\n")
            findings = scan(root)
            self.assertEqual(len(findings), 1)
            self.assertEqual(
                findings[0],
                ".github/workflows/broken.yml:1:<top>: "
                "unreadable workflow: UnicodeDecodeError",
            )

    def test_scan_oserror_workflow_reports_unreadable_finding(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            workflows = root / ".github/workflows"
            (workflows / "dir.yaml").mkdir(parents=True)
            findings = scan(root)
            self.assertEqual(len(findings), 1)
            self.assertTrue(
                findings[0].startswith(
                    ".github/workflows/dir.yaml:1:<top>: unreadable workflow: "
                ),
                findings[0],
            )

    def test_identity_field_tokens_without_bot_literal_fail(self) -> None:
        text = (
            "jobs:\n  audit:\n"
            "    if: github.event.sender.type != 'Bot'\n"
            "    steps:\n"
            "      - if: github.event.pusher.name != 'ci'\n"
            "        env:\n"
            "          PE: ${{ github.event.pusher.email }}\n"
            "          AN: ${{ github.event.head_commit.author.name }}\n"
            "          AE: ${{ github.event.head_commit.author.email }}\n"
            "          AU: ${{ github.event.head_commit.author.username }}\n"
            "          CN: ${{ github.event.head_commit.committer.name }}\n"
            "          CE: ${{ github.event.head_commit.committer.email }}\n"
            "          ASG: ${{ github.event.issue.assignee.id }}\n"
            "          RR: ${{ github.event.pull_request.requested_reviewer.id }}\n"
            "          LG: ${{ github.event.client_payload.account.login }}\n"
            "          AI: ${{ inputs.actor_id }}\n"
            "          SB: ${{ sender [inputs.key] }}\n"
            "        run: echo ${{ github.event.head_commit.committer.username }}\n"
        )
        findings = check(".github/workflows/audit.yml", text)
        expected = (3, 5, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18)
        self.assertEqual(len(findings), len(expected))
        for idx, lineno in enumerate(expected):
            self.assertTrue(
                findings[idx].startswith(
                    f".github/workflows/audit.yml:{lineno}:audit:"
                ),
                findings[idx],
            )

    def test_bot_literal_case_and_pipe_block_if_fail(self) -> None:
        text = (
            "jobs:\n  gate:\n"
            "    if: !contains(github.event.pull_request.title, 'Dependabot')\n"
            "    steps:\n"
            "      - if: !startsWith(github.head_ref, 'RENOVATE/')\n"
            "        run: echo one\n"
            "      - name: Check suffix\n"
            "        if: !endsWith(inputs.label, '[BOT]')\n"
            "        run: echo two\n"
            "      - name: Pipe block\n"
            "        if: |\n"
            "          !contains(github.head_ref, '[bot]')\n"
            "        run: echo three\n"
        )
        findings = check(".github/workflows/gate.yml", text)
        self.assertEqual(len(findings), 4)
        self.assertTrue(findings[0].startswith(".github/workflows/gate.yml:3:gate:"))
        self.assertTrue(findings[1].startswith(".github/workflows/gate.yml:5:gate:"))
        self.assertTrue(findings[2].startswith(".github/workflows/gate.yml:8:gate:"))
        self.assertTrue(findings[3].startswith(".github/workflows/gate.yml:12:gate:"))

    def test_actor_token_case_insensitive_fails(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  semgrep:\n"
            "    if: github.Actor != vars.BOT_LOGIN\n"
            "  trivy:\n"
            "    if: ${{ GITHUB['ACTOR_ID'] != '29139614' }}\n"
            "    steps:\n"
            "      - if: Gitea.Triggering_Actor != vars.BOT_LOGIN\n"
            "        run: echo ${{ github.event.Sender.Login }} "
            "${{ github.event.pull_request.Author_Association }}\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        expected = ((5, "semgrep"), (7, "trivy"), (9, "trivy"), (10, "trivy"))
        self.assertEqual(len(findings), len(expected))
        for idx, (lineno, job) in enumerate(expected):
            self.assertTrue(
                findings[idx].startswith(
                    f".github/workflows/security-scan.yml:{lineno}:{job}:"
                ),
                findings[idx],
            )

    def test_indent_then_chomping_block_scalar_if_actor_id_fails(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  semgrep:\n"
            "    if: >2-\n"
            "      github.actor_id != 29139614\n"
            "  trivy:\n"
            "    if: |2+\n"
            "      github.actor_id != 29139614\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 2)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:6:semgrep:"),
            findings[0],
        )
        self.assertTrue(
            findings[1].startswith(".github/workflows/security-scan.yml:9:trivy:"),
            findings[1],
        )
        run_blocks = check(
            ".github/workflows/release-go.yml",
            "on:\n  push:\njobs:\n  goreleaser:\n    steps:\n"
            "      - run: >2-\n          username: ${{ github.actor }}\n"
            "      - run: |2+\n          username: ${{ github.actor }}\n"
            "      - run: >-2\n          username: ${{ github.actor }}\n"
            "      - run: |+2\n          username: ${{ github.actor }}\n",
        )
        self.assertEqual(len(run_blocks), 4)
        for idx, lineno in enumerate((7, 9, 11, 13)):
            self.assertTrue(
                run_blocks[idx].startswith(
                    f".github/workflows/release-go.yml:{lineno}:goreleaser:"
                ),
                run_blocks[idx],
            )

    def test_run_indent_chomping_block_scalar_keeps_tracked_job(self) -> None:
        indent_one = check(
            ".github/workflows/ci.yml",
            "on:\n  push:\njobs:\n  semgrep:\n    steps:\n"
            "      - run: echo ok\n"
            " run: >2-\n  pr-title:\n end: 1\n"
            "    if: github.actor_id != 29139614\n"
            "  lint:\n"
            "    if: github.actor_id != 29139614\n",
        )
        self.assertEqual(len(indent_one), 2)
        self.assertTrue(
            indent_one[0].startswith(".github/workflows/ci.yml:10:semgrep:"),
            indent_one[0],
        )
        self.assertTrue(
            indent_one[1].startswith(".github/workflows/ci.yml:12:lint:"),
            indent_one[1],
        )
        step_run = check(
            ".github/workflows/ci.yml",
            "on:\n  push:\njobs:\n  semgrep:\n    steps:\n"
            "      - run: >2-\n          pr-title:\n"
            "      - if: github.actor_id != 29139614\n        run: echo ok\n"
            "  lint:\n"
            "    if: github.actor_id != 29139614\n",
        )
        self.assertEqual(len(step_run), 2)
        self.assertTrue(
            step_run[0].startswith(".github/workflows/ci.yml:8:semgrep:"),
            step_run[0],
        )
        self.assertTrue(
            step_run[1].startswith(".github/workflows/ci.yml:11:lint:"),
            step_run[1],
        )

    def test_repository_workflows_comply(self) -> None:
        self.assertEqual(scan(REPO_ROOT), [])

    def assert_rejected_fixtures(self, category: str) -> None:
        for body in _NEW_FAILURES[category][:MAX_WORKFLOW_LINES]:
            with self.subTest(body=body):
                self.assertTrue(check(_FIXTURE_NAME, _FIXTURE_PREFIX + body))

    def test_computed_index_on_identity_context_fails(self) -> None:
        self.assert_rejected_fixtures("computed_index")
        self.assertEqual(
            check(
                _FIXTURE_NAME,
                _FIXTURE_PREFIX + "    env:\n"
                "      X: ${{ steps.a.outputs[inputs.k] }}\n"
                "      Y: ${{ matrix[inputs.k] }}\n",
            ),
            [],
        )

    def test_computed_bot_literal_fails(self) -> None:
        self.assert_rejected_fixtures("computed_bot")
        self.assertEqual(
            check(
                _FIXTURE_NAME,
                _FIXTURE_PREFIX + "    env:\n"
                "      X: ${{ fromJSON(toJSON(job)) }}\n"
                "      Y: ${{ format('{0}/go.sum', inputs.working-directory) }}\n",
            ),
            [],
        )

    def test_double_quoted_escapes_fail(self) -> None:
        self.assert_rejected_fixtures("escapes")
        self.assertTrue(
            check(_FIXTURE_NAME, _FIXTURE_PREFIX + '    if: "github.actor_id != 1"\n')
        )
        for value in (r'"x\UFFFFFFFFy"', r'"x\xZZy"', r'"x\uD800y"'):
            with self.subTest(value=value):
                self.assertEqual(
                    check(_FIXTURE_NAME, _FIXTURE_PREFIX + f"    if: {value}\n"), []
                )

    def test_escaped_line_break_fails(self) -> None:
        self.assert_rejected_fixtures("line_break")
        findings = check(
            _FIXTURE_NAME, _FIXTURE_PREFIX + _NEW_FAILURES["line_break"][0]
        )
        self.assertTrue(any("escaped line break refused" in item for item in findings))
        self.assertEqual(
            check(
                _FIXTURE_NAME,
                _FIXTURE_PREFIX + "    steps:\n      - run: |\n"
                "          echo one \\\n          two\n",
            ),
            [],
        )

    def test_whitespace_around_dot_fails(self) -> None:
        self.assert_rejected_fixtures("dot_whitespace")
        self.assertEqual(
            check(
                _FIXTURE_NAME,
                _FIXTURE_PREFIX + "    steps:\n      - run: |\n"
                '          echo "Done. Author unknown"\n',
            ),
            [],
        )

    def test_multiline_expression_forms_fail(self) -> None:
        self.assert_rejected_fixtures("multiline")
        text = (REPO_ROOT / ".gitea/workflows/publish-github.yml").read_text(
            encoding="utf-8"
        )
        self.assertIn("    if: >-\n", text)
        self.assertEqual(check(".gitea/workflows/publish-github.yml", text), [])


if __name__ == "__main__":
    sys.exit(0 if unittest.main(exit=False).result.wasSuccessful() else 1)
