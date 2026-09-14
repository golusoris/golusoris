<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — core/capabilities

Schema + loader for the framework's **capability contract**, the repo-root
[`capabilities.yaml`](../../capabilities.yaml). Downstream governance
(praetor `needs scan|report|migrate`) resolves `.needs.yaml` demands against
this file; the root test `capabilities_test.go` keeps it in sync with the tree.

## Schema (version 1)

```yaml
version: 1
framework: github.com/golusoris/golusoris        # root module
modules: [github.com/golusoris/golusoris/core]   # every published Go module
packages:
  - import: github.com/golusoris/golusoris/core/config
    module: github.com/golusoris/golusoris/core  # omit for the root module
    domain: config
    capabilities: [config.loader, config.env, config.watch]
    description: koanf v2 — env + file + YAML, hot reload
    status: stable                               # stable | beta | experimental
    replaces: [github.com/spf13/viper]           # third-party modules superseded
```

Capability keys: `domain.name[.sub]`, lowercase — the praetor taxonomy
(`db.postgres`, `cache.redis`, `http.router`, `telemetry.otel`, …).

## Key API

| Symbol | Purpose |
|---|---|
| `Parse(data)` · `Load(path)` | strict decode + `Validate()` |
| `(*Index).Covers(key)` · `ByCapability()` · `Keys()` | demand resolution |
| `(*Index).Lookup(import)` · `Replacements()` | import → package; third-party module → replacement import |
| `ErrSchemaVersion` · `ErrInvalid` | validation failures |

## Don't

- Don't add a package to the tree without adding it here — the root drift test fails.
- Don't invent new key domains casually; reuse the existing taxonomy so
  fleet-wide demand aggregation stays comparable.
