#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Every pull-request workflow cancels its superseded runs, and only those."""

from __future__ import annotations

import datetime
import re
import sys
import tempfile
import unittest
import unittest.mock
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
# Gitea runs on its own runner without this queue; its ci.yml keeps a ref-wide group.
WORKFLOW_DIRS: tuple[str, ...] = (".github/workflows",)
PR_EVENTS: tuple[str, ...] = ("pull_request", "pull_request_target")

# path -> (reason, expiry). An expired entry fails like a missing group.
EXCEPTIONS: dict[str, tuple[str, str]] = {}

_TOP_KEY = re.compile(
    r"^(?P<q>['\"]?)(?P<key>[A-Za-z_-]+)(?P=q):(?=[ \t]|$)(?P<rest>.*)$"
)
_CHILD_KEY = re.compile(r"^  (?P<key>[A-Za-z_-]+):(?P<rest>.*)$")
_JOB_KEY = re.compile(
    r"^  (?P<q>['\"]?)(?P<key>[A-Za-z_][A-Za-z0-9_-]*)(?P=q):"
    r"(?=[ \t]|$)(?P<rest>.*)$"
)
_JOB_IF = re.compile(r"^    (?P<q>['\"]?)if(?P=q):(?=[ \t]|$)(?P<rest>.*)$")
_JOB_FIELD = re.compile(r"^    (?P<q>['\"]?)[A-Za-z_][A-Za-z0-9_-]*(?P=q):(?:[ \t]|$)")
_JOB_SEQUENCE = re.compile(r"^    -(?:[ \t]|$)")
_ALWAYS = re.compile(r"\balways\s*\(\s*\)", re.IGNORECASE)
_EXPRESSION_STRING = re.compile(r"'(?:[^']|'')*'", re.DOTALL)
_DOUBLE_QUOTED_SCALAR = re.compile(r'"(?:[^"\\]|\\["\\])*"', re.DOTALL)
_BLOCK_SCALAR_HEADER = re.compile(r"[|>](?:[-+][1-9]?|[1-9][-+]?)?[ \t]*")
_PLAIN_SCALAR_START = re.compile(r"[A-Za-z0-9$(]")
_YAML_COMMENT = re.compile(
    r"""(?P<quoted>'(?:[^']|'')*'|"(?:[^"\\]|\\.)*")|(?<!\S)#[^\n]*""",
    re.DOTALL,
)


def _top_blocks(text: str) -> dict[str, tuple[str, list[str]]]:
    """Map each top-level key to its inline value and indented body lines."""
    blocks: dict[str, tuple[str, list[str]]] = {}
    current = ""
    for line in text.splitlines():
        match = _TOP_KEY.match(line)
        if match:
            current = match.group("key")
            blocks[current] = (match.group("rest").strip(), [])
        elif current and (line.startswith(" ") or not line.strip()):
            blocks[current][1].append(line)
        elif line.strip() and not line.startswith("#"):
            current = ""
    return blocks


def _events(blocks: dict[str, tuple[str, list[str]]]) -> set[str]:
    inline, body = blocks.get("on", ("", []))
    events = set(re.findall(r"[A-Za-z_]+", inline))
    for line in body:
        match = _CHILD_KEY.match(line)
        if match:
            events.add(match.group("key"))
    return events


def _exception_findings(name: str, today: datetime.date) -> list[str] | None:
    if name not in EXCEPTIONS:
        return None
    reason, expiry = EXCEPTIONS[name]
    if today > datetime.date.fromisoformat(expiry):
        return [f"{name}: exception expired {expiry} ({reason})"]
    return []


def _concurrency_fields(lines: list[str]) -> dict[str, str]:
    fields: dict[str, str] = {}
    for line in lines:
        match = _CHILD_KEY.match(line)
        if match:
            fields[match.group("key")] = match.group("rest").strip()
    return fields


def _group_findings(name: str, group: str, events: set[str]) -> list[str]:
    findings: list[str] = []
    if group.startswith("${{") and "github.workflow" not in group:
        findings.append(
            f"{name}: concurrency group has neither github.workflow nor a literal prefix",
        )
    has_pr_number = (
        "github.event.pull_request.number" in group or "github.event.number" in group
    )
    if "pull_request_target" in events:
        if not has_pr_number:
            findings.append(
                f"{name}: pull_request_target runs share github.ref with the base "
                "branch; key the group by github.event.pull_request.number or "
                "github.event.number",
            )
    elif "github.ref" not in group and not has_pr_number:
        findings.append(
            f"{name}: concurrency group is not keyed by PR number "
            "(github.event.pull_request.number or github.event.number) or ref",
        )
    return findings


def _cancel_findings(name: str, cancel: str) -> list[str]:
    findings: list[str] = []
    if "github.event_name" not in cancel:
        findings.append(
            f"{name}: cancel-in-progress must be scoped to pull-request events, "
            f"got {cancel or 'nothing'}",
        )
    return findings


def _job_conditions(lines: list[str]) -> dict[str, str]:
    """Map each job id to its job-level if: text, retaining continuation lines."""
    conditions: dict[str, str] = {}
    job = ""
    in_condition = False
    for line in lines:
        if in_condition and (line.startswith("     ") or not line.strip()):
            conditions[job] += "\n" + line
            continue
        in_condition = False
        job_match = _JOB_KEY.match(line)
        if job_match:
            job = job_match.group("key")
            continue
        if_match = _JOB_IF.match(line)
        if job and if_match:
            conditions[job] = if_match.group("rest").strip()
            in_condition = True
    return conditions


def _without_condition_comments(condition: str) -> str:
    """Remove YAML comments while preserving quoted spans and block content."""
    header, separator, body = condition.partition("\n")
    if not header.startswith(("|", ">")):
        header, separator, body = condition, "", ""
    header = _YAML_COMMENT.sub(lambda token: token.group("quoted") or "", header)
    return header + separator + body


def _significant_lines(lines: list[str]) -> list[str]:
    return [line for line in lines if line.strip() and not line.lstrip().startswith("#")]


def _readable_workflow(text: str) -> bool:
    return all(
        line == "---" or _TOP_KEY.match(line)
        for line in _significant_lines(text.splitlines())
        if not line[0].isspace()
    )


def _job_entry(line: str) -> tuple[str, bool]:
    match = _JOB_KEY.match(line)
    if not match:
        return "", False
    return match.group("key"), not _without_condition_comments(match.group("rest")).strip()


def _readable_job_line(line: str, has_field: bool) -> bool:
    if _JOB_FIELD.match(line):
        return True
    indent = len(line) - len(line.lstrip(" "))
    return has_field and (indent >= 5 or bool(_JOB_SEQUENCE.match(line)))


def _unreadable_jobs(lines: list[str]) -> set[str]:
    """Accept only job entries, fields, and their indented value content."""
    unreadable: set[str] = set()
    job = ""
    has_field = False
    for line in _significant_lines(lines):
        if job and not has_field and not _JOB_FIELD.match(line):
            unreadable.add(job)
        if len(line) - len(line.lstrip(" ")) == 2:
            job, readable = _job_entry(line)
            has_field = False
            if not readable:
                unreadable.add(job)
        elif not job or not _readable_job_line(line, has_field):
            unreadable.add(job)
        else:
            has_field = True
    return unreadable


def _plain_mapping_finding(name: str, location: str) -> str:
    return f"{name}: {location}: always() check reads plain block mappings only"


def _decode_condition_scalar(condition: str) -> str:
    """Decode YAML quoting before interpreting GitHub expression literals."""
    condition = condition.strip()
    if len(condition) >= 2:
        if condition[0] == condition[-1] == "'":
            return condition[1:-1].replace("''", "'")
        if condition[0] == condition[-1] == '"':
            return re.sub(r'\\(["\\])', r"\1", condition[1:-1])
    return condition


def _condition_expression(condition: str) -> str:
    scalar = _decode_condition_scalar(_without_condition_comments(condition))
    return _EXPRESSION_STRING.sub("", scalar)


def _readable_condition(condition: str) -> bool:
    """Accept only scalar forms whose expression text the reader can inspect."""
    condition = _without_condition_comments(condition).strip()
    if not condition:
        return True
    if condition[0] == "'":
        return bool(_EXPRESSION_STRING.fullmatch(condition))
    if condition[0] == '"':
        return bool(_DOUBLE_QUOTED_SCALAR.fullmatch(condition))
    if condition[0] in "|>":
        return bool(_BLOCK_SCALAR_HEADER.fullmatch(condition.splitlines()[0]))
    return bool(_PLAIN_SCALAR_START.match(condition))


def _always_findings(name: str, blocks: dict[str, tuple[str, list[str]]]) -> list[str]:
    inline, lines = blocks.get("jobs", ("", []))
    if _without_condition_comments(inline).strip() not in ("", "{}"):
        return [_plain_mapping_finding(name, "jobs")]
    unreadable = _unreadable_jobs(lines)
    conditions = _job_conditions(lines)
    unreadable.update(
        job for job, condition in conditions.items() if not _readable_condition(condition)
    )
    findings = [
        _plain_mapping_finding(name, f"job {job}" if job else "jobs")
        for job in sorted(unreadable)
    ]
    return findings + [
        f"{name}: job {job} runs under always(), which survives cancellation and "
        "holds the concurrency group; use ${{ !cancelled() }}"
        for job, condition in sorted(conditions.items())
        if job not in unreadable and _ALWAYS.search(_condition_expression(condition))
    ]


def check(name: str, text: str, today: datetime.date) -> list[str]:
    """Return the policy findings for one workflow file."""
    if not _readable_workflow(text):
        return [_plain_mapping_finding(name, "workflow")]
    blocks = _top_blocks(text)
    events = _events(blocks)
    if not events.intersection(PR_EVENTS):
        # A called workflow runs inside its caller's run, so always() there holds the caller's group.
        return _always_findings(name, blocks) if "workflow_call" in events else []
    exceptions = _exception_findings(name, today)
    if exceptions is not None:
        return exceptions
    if "concurrency" not in blocks:
        return [f"{name}: pull-request workflow has no top-level concurrency group"]
    fields = _concurrency_fields(blocks["concurrency"][1])
    return (
        _group_findings(name, fields.get("group", ""), events)
        + _cancel_findings(name, fields.get("cancel-in-progress", ""))
        + _always_findings(name, blocks)
    )


def scan(root: Path, today: datetime.date) -> list[str]:
    findings: list[str] = []
    for directory in WORKFLOW_DIRS:
        for path in sorted((root / directory).glob("*.y*ml")):
            name = path.relative_to(root).as_posix()
            findings.extend(check(name, path.read_text(encoding="utf-8"), today))
    return findings


_GOOD = """name: x
'on':
  pull_request:
  push:
    branches: [main]
concurrency:
  group: ${{ github.workflow }}-${{ github.event.pull_request.number || github.ref }}
  cancel-in-progress: ${{ github.event_name == 'pull_request' }}
jobs: {}
"""
_TODAY = datetime.date(2026, 10, 16)


def _with_jobs(body: str) -> str:
    return _GOOD.replace("jobs: {}\n", "jobs:\n" + body)


class WorkflowConcurrencyPolicyTest(unittest.TestCase):
    def test_scoped_group_passes(self) -> None:
        self.assertEqual(check("w.yml", _GOOD, _TODAY), [])

    def test_missing_group_fails(self) -> None:
        text = _GOOD.split("concurrency:")[0] + "jobs: {}\n"
        self.assertIn(
            "no top-level concurrency", " ".join(check("w.yml", text, _TODAY))
        )

    def test_unconditional_cancel_fails(self) -> None:
        text = _GOOD.replace(
            "${{ github.event_name == 'pull_request' }}",
            "true",
        )
        self.assertIn(
            "scoped to pull-request events", " ".join(check("w.yml", text, _TODAY))
        )

    def test_pull_request_target_needs_pr_number(self) -> None:
        text = (
            "on:\n  pull_request_target:\n    types: [opened]\n"
            "concurrency:\n  group: ${{ github.workflow }}-${{ github.ref }}\n"
            "  cancel-in-progress: ${{ github.event_name == 'pull_request_target' }}\n"
        )
        self.assertIn("pull_request.number", " ".join(check("w.yml", text, _TODAY)))

    def test_always_aggregate_fails(self) -> None:
        text = _with_jobs("  ci-success:\n    if: always()\n    needs: [lint]\n")
        self.assertIn(
            "job ci-success runs under always()", " ".join(check("w.yml", text, _TODAY))
        )

    def test_top_mapping_always_fails_closed(self) -> None:
        text = _with_jobs("  gate:\n    if: always()\n")
        variants = [
            text.replace("jobs:", key)
            for key in ("jobs :", "? jobs", "<<: *base", "jobs:x", "%YAML 1.2")
        ]
        variants.append(text.replace("'on':", "on :"))
        for variant in variants:
            with self.subTest(text=variant):
                findings = check("w.yml", variant, _TODAY)
                self.assertEqual(len(findings), 1)
                self.assertIn(
                    "workflow: always() check reads plain block mappings only",
                    findings[0],
                )

    def test_job_mapping_always_fails_closed(self) -> None:
        for body in (
            "  gate:\n    if : always()\n",
            "  gate :\n    if: always()\n",
            "  gate:\n    'if' : always()\n",
            "  gate:\n    - if: always()\n",
            "  gate:\n    if:always()\n",
            "  1gate:\n    if: always()\n",
        ):
            with self.subTest(body=body):
                findings = check("w.yml", _with_jobs(body), _TODAY)
                self.assertEqual(len(findings), 1)
                self.assertIn(
                    "always() check reads plain block mappings only", findings[0]
                )

    def test_condition_scalar_always_fails_closed(self) -> None:
        for condition in (
            "&cond 'always()'",
            "!!str 'always()'",
            "!!str always()",
            "[always()]",
            "\n      &c always()",
            "{a: b}",
            "!cancelled()",
            "'always()",
            '"always()',
            "|0\n      always()",
        ):
            with self.subTest(condition=condition):
                text = _with_jobs(f"  gate:\n    if: {condition}\n")
                findings = check("w.yml", text, _TODAY)
                self.assertEqual(len(findings), 1)
                self.assertIn(
                    "job gate: always() check reads plain block mappings only",
                    findings[0],
                )

    def test_allowlisted_yaml_always_check_passes(self) -> None:
        variants = ["---\n" + _GOOD]
        variants.extend(
            _with_jobs(f"  gate:\n    if:{condition}\n")
            for condition in (
                " |-\n      ${{ !cancelled() }}",
                "\t${{ !cancelled() }}",
                " (github.event_name == 'push')",
                ' "${{ !cancelled() }}"',
                " 'x\n      y'",
            )
        )
        for text in variants:
            with self.subTest(text=text):
                self.assertEqual(check("w.yml", text, _TODAY), [])

    def test_case_insensitive_always_fails(self) -> None:
        for condition in ("ALWAYS()", "Always()", ">-\n      ALWAYS()"):
            with self.subTest(condition=condition):
                text = _with_jobs(f"  gate:\n    if: {condition}\n    runs-on: x\n")
                findings = check("w.yml", text, _TODAY)
                self.assertEqual(len(findings), 1)
                self.assertIn("job gate runs under always()", findings[0])

    def test_escaped_yaml_always_fails_closed(self) -> None:
        for body in (
            '  gate:\n    if: "\\u0061lways()"\n    runs-on: x\n',
            '  gate:\n    if: "\\x61lways()"\n    runs-on: x\n',
            '  "g\\u0061te":\n    runs-on: x\n',
            '  "g\\\\ate":\n    runs-on: x\n',
            '  gate:\n    "\\u0069f": always()\n    runs-on: x\n',
            '  gate:\n    "\\x69f": always()\n    runs-on: x\n',
            '  gate:\n    "\\u0069f" : always()\n    runs-on: x\n',
            '  gate:\n    "na\\\\me": x\n    runs-on: x\n',
        ):
            with self.subTest(body=body):
                findings = check("w.yml", _with_jobs(body), _TODAY)
                self.assertEqual(len(findings), 1)
                self.assertIn(
                    "always() check reads plain block mappings only", findings[0]
                )

    def test_unreadable_indentation_always_fails_closed(self) -> None:
        for body, location in (
            ("    gate:\n        if: always()\n        runs-on: x\n", "jobs"),
            ("  gate:\n   if: always()\n   runs-on: x\n", "job gate"),
            (" gate:\n   if: always()\n", "jobs"),
            ("  [gate]:\n    if: always()\n", "jobs"),
            ("  gate:\n\n    # comment\n      if: always()\n", "job gate"),
        ):
            with self.subTest(body=body):
                findings = check("w.yml", _with_jobs(body), _TODAY)
                self.assertEqual(len(findings), 1)
                self.assertIn(f"{location}: always() check reads plain", findings[0])

    def test_explicit_keys_always_fail_closed(self) -> None:
        for body in (
            "  gate:\n    ? if\n    : always()\n    runs-on: x\n",
            "  gate:\n    : always()\n    runs-on: x\n",
            "  ? gate\n  : {if: always(), runs-on: x}\n",
            "  : {if: always(), runs-on: x}\n",
        ):
            with self.subTest(body=body):
                findings = check("w.yml", _with_jobs(body), _TODAY)
                self.assertEqual(len(findings), 1)
                self.assertIn(
                    "always() check reads plain block mappings only", findings[0]
                )

    def test_alias_condition_always_fails_closed(self) -> None:
        for condition in ("*cond", "*cond # comment", "'*cond'", '"*cond"'):
            with self.subTest(condition=condition):
                text = _with_jobs(f"  gate:\n    if: {condition}\n    runs-on: x\n")
                findings = check("w.yml", text, _TODAY)
                self.assertEqual(len(findings), 1)
                self.assertIn("job gate", findings[0])
                self.assertIn(
                    "always() check reads plain block mappings only", findings[0]
                )

    def test_supported_yaml_always_check_passes(self) -> None:
        for text in (
            _with_jobs(
                "  gate:\n    needs:\n    - lint\n"
                "    if: ${{ !cancelled() }}\n    runs-on: x\n"
            ),
            _with_jobs("  gate:\n    steps: &s\n      - run: x\n"),
            _with_jobs('  gate:\n    if: "${{ !cancelled() }}"\n    runs-on: x\n'),
            _with_jobs('  gate:\n    if: "a \\"b\\" \\\\ c"\n    runs-on: x\n'),
            _with_jobs("  gate: # block job\n\n    # field comment\n    runs-on: x\n"),
            _GOOD,
        ):
            with self.subTest(text=text):
                self.assertEqual(check("w.yml", text, _TODAY), [])

    def test_quoted_job_key_always_fails(self) -> None:
        for quote in ('"', "'"):
            with self.subTest(quote=quote):
                text = _with_jobs(
                    f"  {quote}gate{quote}:\n    if: always()\n    runs-on: x\n"
                )
                findings = check("w.yml", text, _TODAY)
                self.assertEqual(len(findings), 1)
                self.assertIn("job gate runs under always()", findings[0])

    def test_quoted_if_key_always_fails(self) -> None:
        for quote in ('"', "'"):
            with self.subTest(quote=quote):
                text = _with_jobs(
                    f"  gate:\n    {quote}if{quote}: always()\n    runs-on: x\n"
                )
                findings = check("w.yml", text, _TODAY)
                self.assertEqual(len(findings), 1)
                self.assertIn("job gate runs under always()", findings[0])

    def test_unreadable_job_always_fails(self) -> None:
        for body in (
            "  gate: {if: always(), runs-on: x}\n",
            "  gate: {runs-on: x}\n",
            "  gate: *base\n",
            "  gate: &base\n    if: always()\n    runs-on: x\n",
        ):
            with self.subTest(body=body):
                findings = check("w.yml", _with_jobs(body), _TODAY)
                self.assertEqual(len(findings), 1)
                self.assertIn("job gate", findings[0])
                self.assertIn(
                    "always() check reads plain block mappings only", findings[0]
                )

    def test_inline_jobs_always_check_fails_closed(self) -> None:
        for value in ("{gate: {runs-on: x}}", "*base", "&base"):
            with self.subTest(value=value):
                text = _GOOD.replace("jobs: {}", f"jobs: {value}")
                findings = check("w.yml", text, _TODAY)
                self.assertEqual(len(findings), 1)
                self.assertIn("jobs", findings[0])
                self.assertIn(
                    "always() check reads plain block mappings only", findings[0]
                )

    def test_empty_jobs_always_check_passes(self) -> None:
        self.assertEqual(check("w.yml", _GOOD, _TODAY), [])

    def test_mapping_comments_always_check_passes(self) -> None:
        text = _with_jobs("  gate: # block job\n    runs-on: x\n")
        self.assertEqual(check("w.yml", text, _TODAY), [])
        text = _GOOD.replace("jobs: {}", "jobs: {} # empty jobs")
        self.assertEqual(check("w.yml", text, _TODAY), [])

    def test_job_merge_key_always_fails(self) -> None:
        for merge_key in ("<<", "'<<'", '"<<"'):
            with self.subTest(merge_key=merge_key):
                text = _with_jobs(f"  gate:\n    {merge_key}: *base\n    runs-on: x\n")
                findings = check("w.yml", text, _TODAY)
                self.assertEqual(len(findings), 1)
                self.assertIn("job gate", findings[0])
                self.assertIn(
                    "always() check reads plain block mappings only", findings[0]
                )

    def test_expression_string_always_passes(self) -> None:
        for condition in (
            "${{ contains(github.ref, 'always()') && !cancelled() }}",
            "'${{ contains(github.ref, ''always()'') }}'",
            "\"${{ contains(github.ref, 'always()') }}\"",
            "${{ contains(github.ref, 'it''s always()') }}",
        ):
            with self.subTest(condition=condition):
                text = _with_jobs(f"  gate:\n    if: {condition}\n    runs-on: x\n")
                self.assertEqual(check("w.yml", text, _TODAY), [])

    def test_yaml_quoted_always_call_fails(self) -> None:
        for condition in ("'always()'", '"always()"'):
            with self.subTest(condition=condition):
                text = _with_jobs(f"  gate:\n    if: {condition}\n    runs-on: x\n")
                findings = check("w.yml", text, _TODAY)
                self.assertEqual(len(findings), 1)
                self.assertIn("job gate runs under always()", findings[0])

    def test_expression_literal_then_always_call_fails(self) -> None:
        text = _with_jobs(
            "  gate:\n    if: ${{ contains(github.ref, 'x') || always() }}\n"
        )
        findings = check("w.yml", text, _TODAY)
        self.assertEqual(len(findings), 1)
        self.assertIn("job gate runs under always()", findings[0])

    def test_not_cancelled_aggregate_passes(self) -> None:
        text = _with_jobs(
            "  ci-success:\n    if: ${{ !cancelled() }}\n    needs: [lint]\n"
        )
        self.assertEqual(check("w.yml", text, _TODAY), [])

    def test_always_in_block_scalar_fails(self) -> None:
        text = _with_jobs(
            "  gate:\n    if: >-\n      always() &&\n      github.event_name == 'push'\n"
            "    runs-on: ubuntu-26.04\n"
        )
        self.assertIn(
            "job gate runs under always()", " ".join(check("w.yml", text, _TODAY))
        )

    def test_plain_multiline_always_fails(self) -> None:
        text = _with_jobs("  gate:\n    if: ${{\n      always()\n      }}\n")
        self.assertIn(
            "job gate runs under always()", " ".join(check("w.yml", text, _TODAY))
        )

    def test_called_workflow_plain_multiline_always_fails(self) -> None:
        text = (
            "on:\n  workflow_call:\njobs:\n"
            "  gate:\n    if: ${{\n      always()\n      }}\n"
        )
        self.assertIn(
            "job gate runs under always()", " ".join(check("w.yml", text, _TODAY))
        )

    def test_quoted_multiline_always_fails(self) -> None:
        for quote in ("'", '"'):
            with self.subTest(quote=quote):
                text = _with_jobs(
                    f"  gate:\n    if: {quote}${{{{\n\n     always()\n     }}}}{quote}\n"
                )
                self.assertIn(
                    "job gate runs under always()",
                    " ".join(check("w.yml", text, _TODAY)),
                )

    def test_condition_comments_pass(self) -> None:
        for condition in (
            "${{ !cancelled() }} # unlike always()",
            "'${{ !cancelled() }}' # unlike always()",
            '"${{ !cancelled() }}" # unlike always()',
            ">- # note always()\n      !cancelled()",
            "|- # note always()\n      !cancelled()",
            "${{\n      !cancelled() # unlike always()\n      }}",
            "${{\n      # unlike always()\n      !cancelled()\n      }}",
        ):
            with self.subTest(condition=condition):
                text = _with_jobs(f"  gate:\n    if: {condition}\n")
                self.assertEqual(check("w.yml", text, _TODAY), [])

    def test_always_before_comment_fails(self) -> None:
        text = _with_jobs("  gate:\n    if: always() # note\n")
        self.assertIn(
            "job gate runs under always()", " ".join(check("w.yml", text, _TODAY))
        )

    def test_quoted_hash_always_fails(self) -> None:
        for condition in (
            "${{ contains(github.ref, '#') && always() }}",
            '${{ contains(github.ref, "#") && always() }}',
            "'${{ contains(github.ref, ''#'') && always() }}'",
            '"${{ contains(github.ref, \\"#\\") && always() }}"',
            "${{ contains(github.ref, '\n      #') && always() }}",
        ):
            with self.subTest(condition=condition):
                text = _with_jobs(f"  gate:\n    if: {condition}\n")
                self.assertIn(
                    "job gate runs under always()",
                    " ".join(check("w.yml", text, _TODAY)),
                )

    def test_block_content_comment_always_fails(self) -> None:
        text = _with_jobs("  gate:\n    if: >-\n      !cancelled() # always()\n")
        self.assertIn(
            "job gate runs under always()", " ".join(check("w.yml", text, _TODAY))
        )

    def test_comment_without_whitespace_always_fails(self) -> None:
        text = _with_jobs("  gate:\n    if: !cancelled()#always()\n")
        self.assertIn(
            "job gate runs under always()", " ".join(check("w.yml", text, _TODAY))
        )

    def test_not_cancelled_before_step_level_always_passes(self) -> None:
        text = _with_jobs(
            "  upload:\n    if: ${{ !cancelled() }}\n"
            "    steps:\n      - run: 'true'\n        if: always()\n"
        )
        self.assertEqual(check("w.yml", text, _TODAY), [])

    def test_step_level_always_passes(self) -> None:
        text = _with_jobs(
            "  upload:\n    steps:\n      - run: 'true'\n        if: always()\n"
        )
        self.assertEqual(check("w.yml", text, _TODAY), [])

    def test_called_workflow_always_fails(self) -> None:
        text = "on:\n  workflow_call:\njobs:\n  gate:\n    if: ${{ always() }}\n"
        self.assertEqual(len(check("w.yml", text, _TODAY)), 1)

    def test_restored_always_on_ci_aggregate_fails(self) -> None:
        ci = (REPO_ROOT / ".github/workflows/ci.yml").read_text(encoding="utf-8")
        mutant = ci.replace("    if: ${{ !cancelled() }}\n", "    if: always()\n")
        self.assertNotEqual(mutant, ci)
        self.assertIn(
            "job ci-success runs under always()",
            " ".join(check(".github/workflows/ci.yml", mutant, _TODAY)),
        )

    def test_inline_trigger_list_is_seen(self) -> None:
        self.assertNotEqual(
            check("w.yml", "on: [push, pull_request]\njobs: {}\n", _TODAY), []
        )

    def test_non_pr_workflow_is_ignored(self) -> None:
        self.assertEqual(
            check("w.yml", "on:\n  schedule:\n    - cron: '0 0 * * *'\n", _TODAY), []
        )

    def test_exception_expires(self) -> None:
        text = "on:\n  pull_request:\n"
        with unittest.mock.patch.dict(EXCEPTIONS, {"w.yml": ("C: test", "2026-10-20")}):
            self.assertEqual(check("w.yml", text, datetime.date(2026, 10, 19)), [])
            self.assertIn(
                "exception expired",
                " ".join(check("w.yml", text, datetime.date(2026, 10, 21))),
            )

    def test_engine_event_number_group_passes(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "concurrency:\n"
            "  group: ${{ github.workflow }}-${{ github.event.number || github.run_id }}\n"
            "  cancel-in-progress: ${{ github.event_name == 'pull_request' }}\n"
            "jobs: {}\n"
        )
        self.assertEqual(check("w.yml", text, _TODAY), [])

    def test_run_id_only_group_fails(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "concurrency:\n"
            "  group: ${{ github.workflow }}-${{ github.run_id }}\n"
            "  cancel-in-progress: ${{ github.event_name == 'pull_request' }}\n"
            "jobs: {}\n"
        )
        self.assertIn("not keyed", " ".join(check("w.yml", text, _TODAY)))

    def test_group_needs_workflow_or_prefix(self) -> None:
        bare = _GOOD.replace("${{ github.workflow }}-", "")
        self.assertIn("literal prefix", " ".join(check("w.yml", bare, _TODAY)))
        prefixed = _GOOD.replace("${{ github.workflow }}-", "x-")
        self.assertEqual(check("w.yml", prefixed, _TODAY), [])

    def test_scan_reads_workflow_files(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / ".github/workflows").mkdir(parents=True)
            (root / ".github/workflows/ci.yml").write_text(
                "on: [pull_request]\n", encoding="utf-8"
            )
            self.assertEqual(len(scan(root, _TODAY)), 1)

    def test_repository_workflows_comply(self) -> None:
        self.assertEqual(
            scan(REPO_ROOT, datetime.datetime.now(datetime.UTC).date()), []
        )


if __name__ == "__main__":
    sys.exit(0 if unittest.main(exit=False).result.wasSuccessful() else 1)
