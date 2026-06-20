# Agent guide — testutil/fuzz/

Helpers for fuzz testing: corpus/seed-file discovery and a generic
round-trip assertion for parser/codec targets. Stateless test utility —
**no fx wiring**. Import directly from `_test.go` files.

## API

```go
dir   := fuzz.CorpusDir(t, "FuzzDecode")    // testdata/corpus/<target>, created when absent
files := fuzz.CorpusFiles(t, "FuzzDecode")  // every file under testdata/corpus/<target>
seeds := fuzz.SeedFiles(t, "FuzzDecode")    // testdata/fuzz/<target> (toolchain seeds); nil when absent

fuzz.RoundTrip(t, v, encode, decode)        // asserts decode(encode(v)) == v via reflect.DeepEqual
```

`RoundTrip[T any]` is generic over the value type; `encode`/`decode` are
`func(T) ([]byte, error)` / `func([]byte) (T, error)`. All helpers `t.Fatalf`
on failure.

## Notes

- `CorpusDir`/`CorpusFiles` create the dir (mode `0o750`); `SeedFiles` does not
  — it returns nil for a missing dir so a target with no seeds is not an error.
- Paths are relative to the test's package dir (standard Go testdata layout).
- Use to replay a saved corpus as a regression in normal `go test` (no `-fuzz`).
