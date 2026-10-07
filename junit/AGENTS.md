<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — junit/

JUnit XML writer for CI gates (#631): gate fails pipeline on measured value,
CI shows failing case by name. Stdlib only. Capability key: `test.junit`.

## Key API

| Symbol | Purpose |
| --- | --- |
| `Write(w, Report)` | indented UTF-8 document; counts + times derived from cases |
| `Report` / `Suite` / `Case` / `Property` | `<testsuites>` / `<testsuite>` / `<testcase>` / `<property>` |
| `Status` | `Passed` (zero), `Failed`, `Errored`, `Skipped` |
| `ErrInvalid` | empty suite/case/property name, negative duration, unknown status; nothing written |

## Format contract

- Target schema: Jenkins xUnit `junit-10.xsd`, vendored at `testdata/junit-10.xsd` (MIT, xunit-plugin@0afa700).
- `<testsuites>` carries name, tests, failures, errors, time only — schema has no `skipped` there.
- Time: seconds, 3 decimals, integer math (`1.500`). Timestamp: UTC `2006-01-02T15:04:05`, no zone (Ant pattern).
- `Suite.Duration` zero -> sum of case durations.
- XML-illegal runes (C0 except tab/LF/CR, U+FFFE, U+FFFF) -> `\xNN` / `\uNNNN`; invalid UTF-8 bytes -> `\xNN`.

## Tests

- Golden: `testdata/gate.golden.xml`; regenerate via `GOLUSORIS_UPDATE_GOLDEN=1 go test ./junit/`.
- `TestSchemaValidation` runs `xmllint --schema` when installed (skips under `-short`); planted invalid doc proves check bites.
