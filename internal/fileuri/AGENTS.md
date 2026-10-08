<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — internal/fileuri

Local path <-> RFC 8089 `file:` URI boundary for every platform.

## Contract

- Build `file:` URIs with `fileuri.FromPath`; never `url.URL{Scheme: "file", Path: native}`.
  Windows `C:\x` must render `file:///C:/x`, not `file://C:%5Cx`.
- `FromPath` rejects relative paths.
- Parse with `url.Parse`, then `fileuri.ToPath`. Caller owns scheme, host,
  query, fragment, and absoluteness policy.
- `ToPath` strips leading `/` only before drive letter (`/C:`) on Windows.
  Driveless `/tmp/x` stays non-absolute there; callers reject it.
- Platform logic: pure `fromAbsolute`/`toLocal` take separator; Linux tests
  cover Windows inputs. Runtime proof: Windows test binary under Wine.
