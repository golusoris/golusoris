<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — core/validate/nilcheck/

- API: `IsNil(value any) bool`; nil + typed-nil channel/function/interface/map/pointer/slice detection.
- Imports: `reflect` only; stdlib-only dependency closure.
- Non-nillable kinds: false; no panic.
- Tests: positive, negative, boundary; `go test -race -count=1 ./validate/...` from `core/`.
