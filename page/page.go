// Package page provides typed pagination helpers for cursor-based and
// offset-based queries, designed to compose cleanly with sqlc-generated
// queries and ogen response types.
//
// Cursor encoding uses base64url so that opaque page tokens are safe to
// embed in URLs without percent-encoding.
package page

import (
	"encoding/base64"
	"fmt"
)

// CursorPage is a page of items with an opaque next-page token.
// T is typically a sqlc row type or a domain struct.
type CursorPage[T any] struct {
	Items      []T
	NextCursor string // empty = last page
	HasMore    bool
}

// OffsetPage is a page of items with numeric total + offset metadata.
type OffsetPage[T any] struct {
	Items  []T
	Total  int64
	Offset int64
	Limit  int64
}

// Params carries the caller-supplied page parameters.
type Params struct {
	// Cursor is the opaque token from the previous CursorPage.NextCursor.
	// Empty means "start from the beginning".
	Cursor string
	// Limit is the maximum number of items to return. Callers should cap
	// this to a safe maximum; helpers here do not enforce a ceiling.
	Limit int64
	// Offset is used only for offset-based pagination.
	Offset int64
}

// EncodeCursor encodes an arbitrary string value into an opaque base64url token.
// Typical values are stringified primary keys or composite sort keys.
func EncodeCursor(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

// DecodeCursor decodes a cursor token previously produced by [EncodeCursor].
func DecodeCursor(cursor string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", fmt.Errorf("page: invalid cursor: %w", err)
	}
	return string(b), nil
}

// NewCursorPage builds a CursorPage from a raw item slice fetched with
// limit+1 items. If len(items) > limit, the last element is stripped and
// HasMore is set; otherwise all items are returned as the final page.
//
// The caller must compute nextValue (the cursor key of the last kept item)
// and pass it to EncodeCursor, or pass "" when HasMore is false.
//
//	rows, _ := db.ListOrders(ctx, sqlc.ListOrdersParams{After: afterID, Limit: params.Limit + 1})
//	return page.NewCursorPage(rows, params.Limit, func(r Order) string {
//	    return strconv.FormatInt(r.ID, 10)
//	})
func NewCursorPage[T any](items []T, limit int64, cursorKey func(T) string) CursorPage[T] {
	if int64(len(items)) > limit {
		last := items[limit-1]
		return CursorPage[T]{
			Items:      items[:limit],
			NextCursor: EncodeCursor(cursorKey(last)),
			HasMore:    true,
		}
	}
	return CursorPage[T]{Items: items}
}

// NewOffsetPage builds an OffsetPage from items + a total-count query.
func NewOffsetPage[T any](items []T, total, offset, limit int64) OffsetPage[T] {
	return OffsetPage[T]{
		Items:  items,
		Total:  total,
		Offset: offset,
		Limit:  limit,
	}
}

// HasPrev reports whether there is a previous page in offset mode.
func (p OffsetPage[T]) HasPrev() bool { return p.Offset > 0 }

// HasNext reports whether there is a next page in offset mode.
func (p OffsetPage[T]) HasNext() bool { return p.Offset+int64(len(p.Items)) < p.Total }
