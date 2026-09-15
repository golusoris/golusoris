<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/fixture/

Typed CSV fixture loading backed by jszwec/csvutil. Stateless test utility —
**no fx wiring**. Import directly from `_test.go` files.

## API

```go
accounts, err := fixture.Load[Account]("testdata/accounts.csv") // []Account, error
accounts      := fixture.MustLoad[Account](t, "testdata/accounts.csv") // t.Fatal on error
```

`Load[T any]` reads path as CSV, treats the first line as the header, and
decodes every remaining row into a `T` using the "csv" struct tag (falling
back to the field name). `MustLoad[T any]` is the same call with `t.Fatal`
on error, for test setup where a broken fixture should stop the test
immediately.

## Header validation and row bound

- `Load` sets `csvutil.Decoder.DisallowMissingColumns = true`: a header
  missing a column `T` declares is a `MissingColumnsError`, not a silently
  zero-filled field.
- `Load` decodes at most `fixture.MaxRows` (10,000) data rows (HISS-02:
  scalar loop bound); a file with more rows fails closed instead of growing
  the result slice without limit.

## Notes

- Fixtures live in `testdata/` next to the test, standard Go layout.
- An empty file (no header row) is an error, not an empty slice.
- Don't use `fixture` for non-tabular or deeply nested data — reach for plain
  JSON fixtures + `encoding/json` there; csvutil only maps flat struct fields
  to columns.
