# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Require stop-on-draft as the first step of every job a pull_request event runs.

One call elsewhere in a workflow must not mask a job that checks out and builds a draft. A job
whose job-level if rules out pull_request (merge_group only, or != 'pull_request' &&) with no
top-level || disjunction is not applicable. Line-based so the CI policy job needs no YAML package.
Workflows with gated jobs must list opened, synchronize, reopened and ready_for_review in pull_request types to rerun on ready.
Exit codes: 0 ok, 1 violation, 3 error (fail closed).
"""

from __future__ import annotations

import re
import sys
from dataclasses import dataclass, field
from pathlib import Path

NOT_PR = re.compile(
    r"^(\$\{\{\s*)?github\.event_name\s*"
    r"(!=\s*'pull_request'\s*(&&|\}\}|$)|==\s*'(?!pull_request')[a-z_]+'\s*(\}\})?$)"
)
REQUIRED = re.compile(
    r"^golusoris/golusoris/\.github/actions/stop-on-draft@[0-9a-f]{40}$"
)
REQUIRED_TYPES = ("opened", "synchronize", "reopened", "ready_for_review")
ON_KEY = re.compile(r"^(on|'on'|\"on\"):\s*(.*)$")
PR_KEY = re.compile(r"^  (pull_request|'pull_request'|\"pull_request\")\s*:")
PR_ITEM = re.compile(
    r"^(  )?-\s+(pull_request|'pull_request'|\"pull_request\")\s*(#.*)?$"
)
TYPES_KEY = re.compile(r"^\s*['\"]?types['\"]?\s*:")
TYPES_FLOW = re.compile(r"['\"]?types['\"]?:\s*\[([^\]]*)\]")
TYPES_BLOCK = re.compile(r"^\s*['\"]?types['\"]?:\s*$")
TYPES_ITEM = re.compile(r"^\s+-\s+(.+?)\s*$")
JOB_HEADER = re.compile(r"^  (['\"]?)([A-Za-z0-9_-]+)\1:\s*(#.*)?$")
JOB_IF = re.compile(r"^    if:\s*(.+?)\s*$")
JOB_KEY = re.compile(r"^    [A-Za-z_-]+:")
STEP_START = re.compile(r"^      - ")
STEP_USES = re.compile(r"^      (- |  )uses:\s*(.+?)\s*$")


YAML_ANCHOR = re.compile(r"&(\S+)")
YAML_ALIAS = re.compile(r"\*(\S+)")


@dataclass
class Trigger:
    """Pull request trigger fields the check reads."""

    present: bool = False
    in_pr: bool = False
    in_types: bool = False
    types: list[str] = field(default_factory=list)
    unreadable: str = ""


@dataclass
class Job:
    """Job fields the check reads."""

    name: str
    condition: str = ""
    reusable: bool = False
    in_steps: bool = False
    steps: list[str] = field(default_factory=list)
    steps_anchor: str = ""
    steps_alias: str = ""


def strip_comment(line: str) -> str:
    """Drop a trailing YAML comment starting with # or preceded by whitespace."""
    return re.sub(r"(^|\s+)#.*$", "", line)


def unquote(value: str) -> str:
    """Drop a trailing YAML comment and one pair of matching outer quotes."""
    value = strip_comment(value).strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in "'\"":
        return value[1:-1]
    return value


def top_level(line: str) -> bool:
    """Report whether a line starts a new top-level key."""
    return bool(line) and not line[0].isspace() and line[0] not in "#-"


def split_top(body: str) -> list[str]:
    """Split a flow collection body on commas outside nested brackets."""
    items, depth, start = [], 0, 0
    for index, char in enumerate(body):
        depth += (char in "[{") - (char in "]}")
        if char == "," and depth == 0:
            items.append(body[start:index])
            start = index + 1
    return [*items, body[start:]]


def direct_entries(value: str) -> set[str]:
    """Return the event names an inline on: value lists directly, not nested values."""
    body = value
    if len(value) >= 2 and value[0] + value[-1] in ("[]", "{}"):
        body = value[1:-1]
    return {unquote(item.split(":", 1)[0].strip()) for item in split_top(body)}


def read_pr_line(trigger: Trigger, line: str) -> None:
    """Parse a line inside the pull_request block."""
    clean = strip_comment(line)
    flow = TYPES_FLOW.search(clean)
    if flow:
        trigger.types = [
            unquote(t.strip()) for t in split_top(flow.group(1)) if t.strip()
        ]
        trigger.in_types = False
        return
    if TYPES_BLOCK.match(clean):
        trigger.in_types = True
        return
    if TYPES_KEY.match(clean):
        if not trigger.unreadable:
            trigger.unreadable = clean.strip()
        trigger.in_types = False
        return
    if trigger.in_types:
        item = TYPES_ITEM.match(clean)
        if item:
            trigger.types.append(unquote(item.group(1)))
        else:
            trigger.in_types = False


def read_trigger_line(trigger: Trigger, line: str) -> None:
    """Apply one line inside the on: block to the trigger state."""
    clean = strip_comment(line)
    if not clean.strip():
        return
    if trigger.in_pr and len(line) - len(line.lstrip(" ")) <= 2:
        trigger.in_pr = False
        trigger.in_types = False
    if not trigger.in_pr:
        if PR_ITEM.match(clean):
            trigger.present = True
        elif PR_KEY.match(clean):
            trigger.present = True
            trigger.in_pr = True
            read_pr_line(trigger, clean)
        return
    read_pr_line(trigger, clean)


def read_inline_types(trigger: Trigger, value: str) -> None:
    """Extract types from an inline pull_request value or mark unreadable."""
    flow = TYPES_FLOW.search(value)
    if flow:
        trigger.types = [
            unquote(t.strip()) for t in split_top(flow.group(1)) if t.strip()
        ]
        return
    trimmed = value.strip()
    if trimmed.startswith("{") and trimmed.endswith("}"):
        for entry in split_top(trimmed[1:-1]):
            key = unquote(entry.partition(":")[0].strip())
            if key == "types":
                if not trigger.unreadable:
                    trigger.unreadable = entry.strip()
                return


def read_inline_on(trigger: Trigger, inline: str) -> None:
    """Parse an inline on: trigger value."""
    if inline.startswith("{") and inline.endswith("}"):
        for entry in split_top(inline[1:-1]):
            # A key with no colon is a null value: default types, still a trigger.
            key, _, value = entry.partition(":")
            if unquote(key.strip()) == "pull_request":
                trigger.present = True
                read_inline_types(trigger, value)
                return
        return
    trigger.present = "pull_request" in direct_entries(inline)


def pr_trigger(lines: list[str]) -> Trigger:
    """Collect pull_request trigger details from the workflow's on: block."""
    trigger = Trigger()
    in_on = False
    for line in lines:
        head = ON_KEY.match(line)
        if head:
            inline = unquote(head.group(2)) if head.group(2) else ""
            if inline:
                read_inline_on(trigger, inline)
                return trigger
            in_on = True
        elif in_on and top_level(line):
            break
        elif in_on:
            read_trigger_line(trigger, line)
    return trigger


def pr_triggered(lines: list[str]) -> bool:
    """Report whether the workflow's on: block lists pull_request."""
    return pr_trigger(lines).present


def read_job_line(job: Job, line: str) -> None:
    """Apply one line inside a job body."""
    cond = JOB_IF.match(line)
    if cond:
        job.condition = unquote(cond.group(1))
    if re.match(r"^    uses:", line):
        job.reusable = True
    if JOB_KEY.match(line):
        steps_match = re.match(r"^    steps:\s*(.*?)\s*(#.*)?$", line)
        if steps_match:
            tag = steps_match.group(1) or ""
            anchor = YAML_ANCHOR.search(tag)
            alias = YAML_ALIAS.search(tag)
            if anchor:
                job.steps_anchor = anchor.group(1)
            if alias:
                job.steps_alias = alias.group(1)
            job.in_steps = True
        else:
            job.in_steps = False
        return
    if not job.in_steps:
        return
    if STEP_START.match(line):
        job.steps.append("")
    uses = STEP_USES.match(line)
    if uses and job.steps:
        job.steps[-1] = unquote(uses.group(2))


def jobs(lines: list[str]) -> list[Job]:
    """Collect the jobs under the top-level jobs: key."""
    found: list[Job] = []
    in_jobs = False
    for line in lines:
        header = JOB_HEADER.match(line)
        if re.match(r"^jobs:\s*(#.*)?$", line):
            in_jobs = True
        elif in_jobs and top_level(line):
            break
        elif in_jobs and header:
            found.append(Job(name=header.group(2)))
        elif in_jobs and found:
            read_job_line(found[-1], line)
    return found


def top_level_or(condition: str) -> bool:
    """Report whether an || sits outside every parenthesised group."""
    flat = condition
    for _ in range(len(condition)):
        inner = re.sub(r"\([^()]*\)", "", flat)
        if inner == flat:
            break
        flat = inner
    return "||" in flat


def exempt(job: Job) -> bool:
    """Report whether the job-level if rules out every pull_request run."""
    return not top_level_or(job.condition) and bool(NOT_PR.match(job.condition))


def gated(job: Job) -> bool:
    """Report whether the job's first step is the pinned stop-on-draft action."""
    return not job.reusable and bool(job.steps) and bool(REQUIRED.match(job.steps[0]))


def resolve_aliases(found: list[Job]) -> None:
    """Copy steps from anchor jobs to alias jobs."""
    anchors: dict[str, Job] = {j.steps_anchor: j for j in found if j.steps_anchor}
    for job in found:
        if job.steps_alias and not job.steps:
            source = anchors.get(job.steps_alias)
            if source:
                job.steps = list(source.steps)


def violations(path: Path) -> list[str]:
    """Return one message per applicable job without stop-on-draft first."""
    lines = path.read_text(encoding="utf-8").splitlines()
    trigger = pr_trigger(lines)
    if not trigger.present:
        return []
    found = jobs(lines)
    resolve_aliases(found)
    messages = [
        f"{path.name}: job {job.name} must run the stop-on-draft action as its first step"
        for job in found
        if not exempt(job) and not gated(job)
    ]
    if any(gated(job) and not exempt(job) for job in found):
        if trigger.unreadable:
            messages.append(
                f"{path.name}: pull_request types form not readable: {trigger.unreadable};"
                " use a one-line flow list or a block sequence"
            )
        else:
            missing = [t for t in REQUIRED_TYPES if t not in trigger.types]
            if missing:
                messages.append(
                    f"{path.name}: pull_request types must list "
                    "opened, synchronize, reopened and ready_for_review "
                    "so a draft gate reruns when the pull request is marked ready; "
                    f"missing: {', '.join(missing)}"
                )
    return messages


def main(argv: list[str]) -> int:
    """Check each workflow path; exit 1 on any violation, 3 on usage or read error."""
    if not argv:
        sys.stderr.write("usage: stop_on_draft_jobs.py WORKFLOW...\n")
        return 3
    try:
        found = [message for arg in argv for message in violations(Path(arg))]
    except (OSError, UnicodeDecodeError) as err:
        sys.stderr.write(f"stop_on_draft_jobs: read: {err}\n")
        return 3
    for message in found:
        sys.stderr.write(message + "\n")
    return 1 if found else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
