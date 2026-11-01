# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Require stop-on-draft as the first step of every job a pull_request event runs.

One call elsewhere in a workflow must not mask a job that checks out and builds a draft. A job
whose job-level if rules out pull_request (merge_group only, or != 'pull_request' &&) with no
top-level || disjunction is not applicable. Line-based so the CI policy job needs no YAML package.
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
ON_KEY = re.compile(r"^(on|'on'|\"on\"):\s*(.*)$")
PR_KEY = re.compile(r"^  (pull_request|'pull_request'|\"pull_request\")\s*:")
PR_ITEM = re.compile(
    r"^\s*-\s+(pull_request|'pull_request'|\"pull_request\")\s*(#.*)?$"
)
JOB_HEADER = re.compile(r"^  (['\"]?)([A-Za-z0-9_-]+)\1:\s*(#.*)?$")
JOB_IF = re.compile(r"^    if:\s*(.+?)\s*$")
JOB_KEY = re.compile(r"^    [A-Za-z_-]+:")
STEP_START = re.compile(r"^      - ")
STEP_USES = re.compile(r"^      (- |  )uses:\s*(.+?)\s*$")


@dataclass
class Job:
    """Job fields the check reads."""

    name: str
    condition: str = ""
    reusable: bool = False
    in_steps: bool = False
    steps: list[str] = field(default_factory=list)


def unquote(value: str) -> str:
    """Drop a trailing YAML comment and one pair of matching outer quotes."""
    value = re.sub(r"(^|\s+)#.*$", "", value).strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in "'\"":
        return value[1:-1]
    return value


def top_level(line: str) -> bool:
    """Report whether a line starts a new top-level key."""
    return bool(line) and not line[0].isspace() and line[0] not in "#-"


def pr_triggered(lines: list[str]) -> bool:
    """Report whether the workflow's on: block lists pull_request."""
    in_on = False
    for line in lines:
        head = ON_KEY.match(line)
        if head:
            inline = unquote(head.group(2)) if head.group(2) else ""
            if inline:
                tokens = re.split(r"[\s,\[\]{}:]+", inline)
                return "pull_request" in {unquote(token) for token in tokens}
            in_on = True
        elif in_on and top_level(line):
            return False
        elif in_on and (PR_KEY.match(line) or PR_ITEM.match(line)):
            return True
    return False


def read_job_line(job: Job, line: str) -> None:
    """Apply one line inside a job body."""
    cond = JOB_IF.match(line)
    if cond:
        job.condition = unquote(cond.group(1))
    if re.match(r"^    uses:", line):
        job.reusable = True
    if JOB_KEY.match(line):
        job.in_steps = bool(re.match(r"^    steps:\s*(#.*)?$", line))
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


def violations(path: Path) -> list[str]:
    """Return one message per applicable job without stop-on-draft first."""
    lines = path.read_text(encoding="utf-8").splitlines()
    if not pr_triggered(lines):
        return []
    return [
        f"{path.name}: job {job.name} must run the stop-on-draft action as its first step"
        for job in jobs(lines)
        if not exempt(job) and not gated(job)
    ]


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
