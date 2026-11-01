# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Require stop-on-draft as the first step of every job a pull_request event runs.

One call elsewhere in a workflow must not mask a job that checks out and builds a draft. A job
whose job-level if rules out pull_request (merge_group only, or != 'pull_request' &&) is not
applicable. Line-based so the CI policy job needs no YAML package.
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
ON_KEY = re.compile(r"^(on|'on'|\"on\"):\s*(.*)$")
JOB_HEADER = re.compile(r"^  ([A-Za-z0-9_-]+):\s*$")
JOB_IF = re.compile(r"^    if:\s*(.+?)\s*$")
JOB_KEY = re.compile(r"^    [A-Za-z_-]+:")
STEP_START = re.compile(r"^      - ")
STEP_USES = re.compile(r"^      (- |  )uses:\s*(\S+)")


@dataclass
class Job:
    """Job fields the check reads."""

    name: str
    condition: str = ""
    reusable: bool = False
    in_steps: bool = False
    steps: list[str] = field(default_factory=list)


def top_level(line: str) -> bool:
    """Report whether a line starts a new top-level key."""
    return bool(line) and not line[0].isspace() and not line.startswith("#")


def pr_triggered(lines: list[str]) -> bool:
    """Report whether the workflow's on: block lists pull_request."""
    in_on = False
    for line in lines:
        head = ON_KEY.match(line)
        if head:
            if head.group(2):
                return "pull_request" in re.split(r"[\s,\[\]]+", head.group(2))
            in_on = True
        elif in_on and top_level(line):
            return False
        elif in_on and re.match(r"^  pull_request\s*:", line):
            return True
    return False


def read_job_line(job: Job, line: str) -> None:
    """Apply one line inside a job body."""
    cond = JOB_IF.match(line)
    if cond:
        job.condition = cond.group(1)
    if re.match(r"^    uses:", line):
        job.reusable = True
    if JOB_KEY.match(line):
        job.in_steps = bool(re.match(r"^    steps:\s*$", line))
        return
    if not job.in_steps:
        return
    if STEP_START.match(line):
        job.steps.append("")
    uses = STEP_USES.match(line)
    if uses and job.steps:
        job.steps[-1] = uses.group(2)


def jobs(lines: list[str]) -> list[Job]:
    """Collect the jobs under the top-level jobs: key."""
    found: list[Job] = []
    in_jobs = False
    for line in lines:
        header = JOB_HEADER.match(line)
        if re.match(r"^jobs:\s*$", line):
            in_jobs = True
        elif in_jobs and top_level(line):
            break
        elif in_jobs and header:
            found.append(Job(name=header.group(1)))
        elif in_jobs and found:
            read_job_line(found[-1], line)
    return found


def violations(path: Path) -> list[str]:
    """Return one message per applicable job without stop-on-draft first."""
    lines = path.read_text(encoding="utf-8").splitlines()
    if not pr_triggered(lines):
        return []
    return [
        f"{path.name}: job {job.name} must run the stop-on-draft action as its first step"
        for job in jobs(lines)
        if not NOT_PR.match(job.condition)
        and (job.reusable or not job.steps or "/stop-on-draft@" not in job.steps[0])
    ]


def main(argv: list[str]) -> int:
    """Check each workflow path; exit 1 on any violation, 2 on usage error."""
    if not argv:
        sys.stderr.write("usage: stop_on_draft_jobs.py WORKFLOW...\n")
        return 2
    found = [message for arg in argv for message in violations(Path(arg))]
    for message in found:
        sys.stderr.write(message + "\n")
    return 1 if found else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
