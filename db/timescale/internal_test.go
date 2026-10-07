// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package timescale

import "testing"

func TestCompressionSQLQuotesQualifiedRelation(t *testing.T) {
	t.Parallel()
	query, err := compressionSQL("analytics.metrics")
	if err != nil {
		t.Fatalf("compressionSQL: %v", err)
	}
	want := `ALTER TABLE "analytics"."metrics" SET (timescaledb.compress)`
	if query != want {
		t.Fatalf("compressionSQL = %q, want %q", query, want)
	}
}

func TestCompressionSQLRejectsMalformedQualification(t *testing.T) {
	t.Parallel()
	for _, table := range []string{".metrics", "analytics.", "db.analytics.metrics"} {
		if _, err := compressionSQL(table); err == nil {
			t.Errorf("compressionSQL(%q) accepted malformed qualification", table)
		}
	}
}
