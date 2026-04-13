# Agent guide — page/

Typed cursor-based and offset-based pagination helpers. No fx wiring — pure
utility imported directly by service functions and sqlc query callers.

## API

```go
// Cursor-based (recommended for large tables):
page.NewCursorPage(rows, limit, func(r Row) string { return r.ID })
// → CursorPage[Row]{Items, NextCursor, HasMore}

// Decode incoming cursor token:
id, err := page.DecodeCursor(params.Cursor)

// Offset-based:
page.NewOffsetPage(rows, total, offset, limit)
// → OffsetPage[Row]{Items, Total, Offset, Limit, HasPrev(), HasNext()}
```

## Convention

Fetch `limit + 1` rows from the DB to detect HasMore without a COUNT query.
Pass the slice and `limit` to `NewCursorPage` — the helper strips the extra.

## Don't

- Don't expose raw cursors as integer IDs — encode them with `EncodeCursor`.
- Don't use offset pagination on tables > 100k rows — prefer cursor-based.
