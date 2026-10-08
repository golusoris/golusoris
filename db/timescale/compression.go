// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package timescale

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// CompressionOptions tunes [DB.EnableCompressionWithOptions].
type CompressionOptions struct {
	// SegmentBy lists columns whose equal values share a compressed batch
	// (typically the series key, e.g. device or tenant id).
	SegmentBy []string
	// OrderBy orders rows inside a batch. Empty keeps the TimescaleDB
	// default (time column descending).
	OrderBy []OrderColumn
}

// OrderColumn is one compress_orderby entry.
type OrderColumn struct {
	Column     string
	Descending bool
}

func compressionSQL(table string, opts CompressionOptions) (string, error) {
	relation, err := relationIdent(table)
	if err != nil {
		return "", err
	}
	settings := []string{"timescaledb.compress"}
	if len(opts.SegmentBy) > 0 {
		cols, colErr := columnList(opts.SegmentBy)
		if colErr != nil {
			return "", colErr
		}
		settings = append(settings, "timescaledb.compress_segmentby = "+quoteLiteral(cols))
	}
	if len(opts.OrderBy) > 0 {
		order, orderErr := orderList(opts.OrderBy)
		if orderErr != nil {
			return "", orderErr
		}
		settings = append(settings, "timescaledb.compress_orderby = "+quoteLiteral(order))
	}
	return "ALTER TABLE " + relation + " SET (" + strings.Join(settings, ", ") + ")", nil
}

func columnList(columns []string) (string, error) {
	quoted := make([]string, 0, len(columns))
	for _, column := range columns {
		ident, err := columnIdent(column)
		if err != nil {
			return "", err
		}
		quoted = append(quoted, ident)
	}
	return strings.Join(quoted, ", "), nil
}

func orderList(columns []OrderColumn) (string, error) {
	quoted := make([]string, 0, len(columns))
	for _, column := range columns {
		ident, err := columnIdent(column.Column)
		if err != nil {
			return "", err
		}
		if column.Descending {
			ident += " DESC"
		}
		quoted = append(quoted, ident)
	}
	return strings.Join(quoted, ", "), nil
}

func columnIdent(column string) (string, error) {
	if column == "" {
		return "", errors.New("column name is required")
	}
	if strings.ContainsRune(column, '\x00') {
		return "", errors.New("column name contains NUL")
	}
	return pgx.Identifier{column}.Sanitize(), nil
}

// quoteLiteral renders s as a standard-conforming SQL string literal.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
