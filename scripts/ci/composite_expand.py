#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Expand reviewed local composites for workflow policy readers."""

from __future__ import annotations

import re
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
MAX_LINES = 4096


def read_bounded_lines(path: Path) -> list[str]:
    # A file past the bound fails instead of being checked only in part.
    lines: list[str] = []
    with path.open("r", encoding="utf-8") as f:
        for idx, line in enumerate(f):
            if idx >= MAX_LINES:
                raise ValueError(f"{path}: more than {MAX_LINES} lines")
            lines.append(line)
    return lines


COMPOSITE_ACTIONS = frozenset(
    {"setup-oci-publish", "setup-image-signing", "publish-tiny-image"}
)
INPUT_EXPRESSION = re.compile(r"\$\{\{ inputs\.([a-z][a-z0-9-]*) \}\}")


def scalar_block(lines: list[str], start: int, indent: int) -> str:
    """Stop at the next scalar instead of consuming adjacent caller inputs."""
    block = []
    for follow in lines[start:]:
        if follow.strip() and not follow.startswith(" " * (indent + 2)):
            break
        block.append(follow[indent + 2 :])
    return "\n".join(block).rstrip() + "\n"


def scalar_values(text: str, indent: int) -> dict[str, str]:
    """Read the bounded literal scalar subset used by local composite callers."""
    lines = text.splitlines()
    values: dict[str, str] = {}
    pattern = re.compile(r"^" + " " * indent + r"([a-z][a-z0-9-]*): (.*)$")
    for index, line in enumerate(lines):
        match = pattern.fullmatch(line)
        if match is None:
            continue
        key, value = match.groups()
        if value == "|":
            value = scalar_block(lines, index + 1, indent)
        elif value.startswith("'") and value.endswith("'"):
            value = value[1:-1].replace("''", "'")
        if key in values:
            raise ValueError(f"duplicate composite scalar: {key}")
        values[key] = value
    return values


def composite_defaults(text: str) -> dict[str, str]:
    """Read optional defaults without interpreting workflow expressions."""
    values: dict[str, str] = {}
    name = ""
    for line in text.splitlines():
        match = re.fullmatch(r"  ([a-z][a-z0-9-]*):", line)
        if match:
            name = match.group(1)
        if line.startswith("    default: "):
            values[name] = scalar_values("  value: " + line[13:], 2)["value"]
    return values


def substitute_inputs(text: str, values: dict[str, str]) -> str:
    """Substitute literal inputs while retaining YAML block indentation."""
    result = []
    for line in text.splitlines():
        match = re.fullmatch(r"(\s*[^:]+): \$\{\{ inputs\.([a-z][a-z0-9-]*) \}\}", line)
        if match and "\n" in values[match.group(2)]:
            prefix, key = match.groups()
            indent = len(line) - len(line.lstrip()) + 2
            result.append(prefix + ": |")
            result.extend(" " * indent + part for part in values[key].splitlines())
            continue
        result.append(INPUT_EXPRESSION.sub(lambda m: values[m.group(1)], line))
    return "\n".join(result) + "\n"


def composite_outputs(action: str, chunk: str) -> dict[str, str]:
    """Expose declared outputs so caller references retain their original meaning."""
    caller_id = re.search(r"(?m)^        id: ([a-z][a-z0-9_-]*)$", chunk)
    outputs = re.search(r"(?ms)^outputs:\n(.*?)^runs:", action)
    if caller_id is None or outputs is None:
        return {}
    aliases: dict[str, str] = {}
    name = ""
    for line in outputs.group(1).splitlines():
        if re.fullmatch(r"  [a-z][a-z0-9-]*:", line):
            name = line.strip()[:-1]
        value = re.fullmatch(r"    value: \$\{\{ (steps\.[^ ]+) \}\}", line)
        if value:
            aliases[f"steps.{caller_id.group(1)}.outputs.{name}"] = value.group(1)
    return aliases


def expand_composite_step(chunk: str, root: Path) -> tuple[str, dict[str, str]] | None:
    """Leave unrelated actions intact and reject guards that hide skipped checks."""
    match = re.search(
        r"(?m)^      (?:  |- )uses: \./\.github/actions/([a-z-]+)$", chunk
    )
    if match is None or match.group(1) not in COMPOSITE_ACTIONS:
        return None
    if re.search(r"(?m)^        (?:if|continue-on-error):", chunk):
        raise ValueError("conditional composite call cannot satisfy publication policy")
    path = root / ".github/actions" / match.group(1) / "action.yml"
    action = "".join(read_bounded_lines(path))
    values = composite_defaults(action)
    values.update(scalar_values(chunk, 10))
    expanded = substitute_inputs(action.split("  steps:\n", 1)[1], values)
    body = "\n".join("  " + line if line else "" for line in expanded.splitlines())
    return body + "\n", composite_outputs(action, chunk)


def expand_workflow(path: Path, root: Path) -> str:
    """Inline only the reviewed local composites; bound nesting and input syntax."""
    text = "".join(read_bounded_lines(path))
    output_aliases: dict[str, str] = {}
    for _ in range(4):
        changed = False
        chunks = re.split(r"(?m)(?=^      - |^  [a-z][a-z0-9_-]*:)", text)
        for index, chunk in enumerate(chunks):
            result = expand_composite_step(chunk, root)
            if result is not None:
                changed = True
                chunks[index], aliases = result
                output_aliases.update(aliases)
        text = "".join(chunks)
        if not changed:
            for alias, value in output_aliases.items():
                text = text.replace(alias, value)
            return text
    raise ValueError("composite expansion exceeds four levels")


def write_expanded_workflows(source: Path, target: Path) -> None:
    """Keep a complete snapshot for shell policy readers and their negative fixtures."""
    workflows = sorted(source.glob("*.yml"))
    if len(workflows) > 128:
        raise ValueError("workflow expansion exceeds 128 files")
    target.mkdir(parents=True, exist_ok=True)
    for workflow in workflows:
        (target / workflow.name).write_text(
            expand_workflow(workflow, REPO_ROOT), encoding="utf-8"
        )


def main(arguments: list[str]) -> int:
    """Expose expansion without importing or running a policy test suite."""
    if len(arguments) == 2 and arguments[0] == "--expand-workflow":
        print(expand_workflow(Path(arguments[1]), REPO_ROOT), end="")
    elif len(arguments) == 3 and arguments[0] == "--expand-workflows":
        write_expanded_workflows(Path(arguments[1]), Path(arguments[2]))
    else:
        raise ValueError(
            "usage: composite_expand.py --expand-workflow FILE "
            "| --expand-workflows SOURCE TARGET"
        )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main(sys.argv[1:]))
    except (OSError, ValueError, KeyError) as error:
        print(f"composite expansion failed: {error}", file=sys.stderr)
        raise SystemExit(1) from error
