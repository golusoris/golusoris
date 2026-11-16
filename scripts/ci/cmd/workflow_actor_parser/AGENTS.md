<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Workflow actor parser

- Purpose: second actor-policy barrier; YAML scalar decoding, expression AST facts.
- Input: stdin, one YAML document; output: stdout JSON `{scalars, error}`.
- Scalar keys: `path`, `text`, `line`, `key_line`, `key`, `alias`, `style`, `refs`, `calls`, `error`.
- Ref paths: lowercase dotted segments; wildcard `*`; dynamic index `[computed]`.
- Bounds: 1 MiB input, 65,536 YAML/expression nodes, depth 128, 4,096 expression tokens; 20 s I/O deadline.
- Walks: iterative; duplicate, merge, non-string keys refused; alias expansion bounded.
- Exit: 0 JSON response; 3 transport/context error; document/scalar errors in JSON.
- Pins: Go 1.27.4; actionlint v1.7.12; YAML v4.0.0-rc.3 required upstream.
