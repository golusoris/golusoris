<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — core/codec/yaml

Fleet-wide YAML codec over `go.yaml.in/yaml/v3` (the maintained fork koanf
pins). Exists so downstream tools (praetor manifests, `.needs.yaml`,
lockfiles) stop requiring their own YAML parser — one pinned version, one
place to patch. Capability key: `config.yaml`.

## Key API

| Symbol | Purpose |
|---|---|
| `Marshal(v)` / `Options.Marshal` | encode with 2-space indent (`Options.Indent` overrides) |
| `Unmarshal(data, v)` | **strict** decode — unknown fields are an error |
| `UnmarshalLenient(data, v)` | forgiving decode |
| `Options.Encode(w, v)` / `Options.Decode(r, v)` | stream variants; `Decode` is bounded by `MaxSize` (default `MaxDocumentSize` = 8 MiB) |
| `ReadFile(path, v)` | strict decode of a file |
| `WriteFile(path, v, perm)` | atomic write (temp + rename in the same dir) |
| `ErrTooLarge` | input exceeded the bound — fail closed |

## Don't

- Don't import `gopkg.in/yaml.v3` or `go.yaml.in/yaml/v3` directly in fleet
  code — go through this package so the pin is shared.
- Don't default to lenient decoding for manifests; typos in governance files
  must fail loudly.
- Don't bypass `WriteFile` for manifests — partial writes corrupt lockfiles.
