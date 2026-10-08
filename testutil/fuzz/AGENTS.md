<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/fuzz/

Helpers for fuzz testing: corpus/seed-file discovery and generic
round-trip assertion for parser/codec targets. Stateless test utility —
**no fx wiring**. Import directly from `_test.go` files.

## API

```go
dir   := fuzz.CorpusDir(t, "FuzzDecode")    // testdata/corpus/<target>, created when absent
files := fuzz.CorpusFiles(t, "FuzzDecode")  // every file under testdata/corpus/<target>
seeds := fuzz.SeedFiles(t, "FuzzDecode")    // testdata/fuzz/<target> (toolchain seeds); nil when absent

fuzz.RoundTrip(t, v, encode, decode)        // asserts decode(encode(v)) == v via reflect.DeepEqual
```

`RoundTrip[T any]` is generic over value type; `encode`/`decode` are
`func(T) ([]byte, error)` / `func([]byte) (T, error)`. All helpers `t.Fatalf`
on failure.

## Notes

- `CorpusDir`/`CorpusFiles` create dir (mode `0o750`); `SeedFiles` does not
 — it returns nil for missing dir so target with no seeds is not error.
- Paths are relative to test's package dir (standard Go testdata layout).
- Use to replay saved corpus as regression in normal `go test` (no `-fuzz`).
