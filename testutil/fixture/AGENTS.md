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

`Load[T any]` reads path as CSV, treats first line as header, and
decodes every remaining row into `T` using "csv" struct tag (falling
back to field name). `MustLoad[T any]` is same call with `t.Fatal`
on error, for test setup where broken fixture should stop test
immediately.

## Header validation and row bound

- `Load` sets `csvutil.Decoder.DisallowMissingColumns = true`: header
 missing column `T` declares is `MissingColumnsError`, not silently
 zero-filled field.
- `Load` decodes at most `fixture.MaxRows` (10,000) data rows (HISS-02:
 scalar loop bound); file with more rows fails closed instead of growing
 result slice without limit.

## Notes

- Fixtures live in `testdata/` next to test, standard Go layout.
- empty file (no header row) is error, not empty slice.
- Don't use `fixture` for non-tabular or deeply nested data — reach for plain
 JSON fixtures + `encoding/json` there; csvutil only maps flat struct fields
 to columns.
