// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package timescale

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// fakeDB reports a fixed extension version and licence and records DDL.
type fakeDB struct {
	version, license pgtype.Text
	detectErr        error
	statements       []string
}

type fakeRow struct {
	scan func(dest ...any) error
}

func (r fakeRow) Scan(dest ...any) error { return r.scan(dest...) }

func (f *fakeDB) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	if sql == capabilitiesSQL {
		return fakeRow{scan: func(dest ...any) error {
			if f.detectErr != nil {
				return f.detectErr
			}
			*dest[0].(*pgtype.Text) = f.version
			*dest[1].(*pgtype.Text) = f.license
			return nil
		}}
	}
	f.statements = append(f.statements, sql)
	return fakeRow{scan: func(dest ...any) error {
		switch target := dest[0].(type) {
		case *bool:
			*target = true
		case *int:
			*target = 0
		}
		return nil
	}}
}

func (f *fakeDB) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	f.statements = append(f.statements, sql)
	return pgconn.CommandTag{}, nil
}

func fakeHelper(version, license string) (*DB, *fakeDB) {
	fake := &fakeDB{
		version: pgtype.Text{String: version, Valid: version != ""},
		license: pgtype.Text{String: license, Valid: license != ""},
	}
	return &DB{db: fake, initialized: true}, fake
}

func TestClassify(t *testing.T) {
	t.Parallel()
	text := func(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }
	tests := []struct {
		name             string
		version, license pgtype.Text
		want             Edition
	}{
		{"plain postgres", pgtype.Text{}, pgtype.Text{}, EditionNone},
		{"library preloaded, extension absent", pgtype.Text{}, text("timescale"), EditionNone},
		{"community", text("2.30.0"), text("timescale"), EditionCommunity},
		{"apache", text("2.30.0"), text("apache"), EditionApache},
		{"licence unset", text("2.30.0"), pgtype.Text{}, EditionUnknown},
		{"unrecognised licence", text("2.30.0"), text("enterprise"), EditionUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			caps := classify(tt.version, tt.license)
			if caps.Edition != tt.want {
				t.Fatalf("Edition = %q, want %q", caps.Edition, tt.want)
			}
			if caps.Installed() != tt.version.Valid {
				t.Fatalf("Installed = %v, want %v", caps.Installed(), tt.version.Valid)
			}
		})
	}
}

func TestSupportsMatrix(t *testing.T) {
	t.Parallel()
	tsl := []Feature{FeatureCompression, FeatureRetentionPolicy, FeatureContinuousAggregate}
	apache := []Feature{FeatureHypertable, FeatureDropChunks}
	for _, edition := range []Edition{EditionNone, EditionApache, EditionCommunity, EditionUnknown, ""} {
		caps := Capabilities{Edition: edition}
		for _, f := range apache {
			want := edition == EditionApache || edition == EditionCommunity || edition == EditionUnknown
			if got := caps.Supports(f); got != want {
				t.Errorf("%s.Supports(%s) = %v, want %v", edition, f, got, want)
			}
		}
		for _, f := range tsl {
			if got := caps.Supports(f); got != (edition == EditionCommunity) {
				t.Errorf("%s.Supports(%s) = %v", edition, f, got)
			}
		}
		if caps.Supports("bogus") {
			t.Errorf("%s.Supports(bogus) = true, want fail-closed", edition)
		}
	}
}

func TestUnknownEditionFailsClosed(t *testing.T) {
	t.Parallel()
	db, fake := fakeHelper("2.30.0", "enterprise")
	ctx := t.Context()
	calls := map[string]func() error{
		"compression":        func() error { return db.EnableCompression(ctx, "metrics") },
		"compression policy": func() error { return db.AddCompressionPolicy(ctx, "metrics", time.Hour) },
		"retention policy":   func() error { return db.SetRetention(ctx, "metrics", time.Hour) },
		"cagg": func() error {
			return db.CreateContinuousAggregate(ctx, ContinuousAggregate{Name: "m_hourly", Query: "SELECT 1"})
		},
		"cagg policy": func() error {
			return db.AddContinuousAggregatePolicy(ctx, "m_hourly", RefreshPolicy{ScheduleInterval: time.Hour})
		},
		"cagg refresh": func() error { return db.RefreshContinuousAggregate(ctx, "m_hourly", time.Time{}, time.Time{}) },
	}
	for name, call := range calls {
		err := call()
		var unsupported *UnsupportedError
		if !errors.Is(err, ErrUnsupportedEdition) || !errors.As(err, &unsupported) || unsupported.Edition != EditionUnknown {
			t.Errorf("%s: error = %v, want UnsupportedError for unknown edition", name, err)
		}
	}
	if len(fake.statements) != 0 {
		t.Fatalf("DDL reached the server under an unknown edition: %q", fake.statements)
	}
	if err := db.CreateHypertable(ctx, "metrics", "time"); err != nil {
		t.Fatalf("CreateHypertable under unknown edition: %v", err)
	}
}

func TestMissingExtensionReportsErrExtensionMissing(t *testing.T) {
	t.Parallel()
	db, fake := fakeHelper("", "")
	err := db.CreateHypertable(t.Context(), "metrics", "time")
	if !errors.Is(err, ErrExtensionMissing) || errors.Is(err, ErrUnsupportedEdition) {
		t.Fatalf("CreateHypertable error = %v, want only ErrExtensionMissing", err)
	}
	if _, err = db.DropChunks(t.Context(), "metrics", time.Hour); !errors.Is(err, ErrExtensionMissing) {
		t.Fatalf("DropChunks error = %v, want ErrExtensionMissing", err)
	}
	if len(fake.statements) != 0 {
		t.Fatalf("statements issued without extension: %q", fake.statements)
	}
}

func TestCapabilitiesPropagatesDetectError(t *testing.T) {
	t.Parallel()
	db, fake := fakeHelper("", "")
	fake.detectErr = errors.New("connection reset")
	_, err := db.Capabilities(t.Context())
	if err == nil || !errors.Is(err, fake.detectErr) || !strings.HasPrefix(err.Error(), "timescale: detect capabilities") {
		t.Fatalf("Capabilities error = %v", err)
	}
	if err = db.EnableCompression(t.Context(), "metrics"); !errors.Is(err, fake.detectErr) {
		t.Fatalf("EnableCompression error = %v, want detect error", err)
	}
}

func TestCommunityIssuesStatements(t *testing.T) {
	t.Parallel()
	db, fake := fakeHelper("2.30.0", "timescale")
	ctx := t.Context()
	if err := db.CreateHypertableWithOptions(ctx, "m", "time", HypertableOptions{ChunkTimeInterval: time.Microsecond}); err != nil {
		t.Fatalf("CreateHypertableWithOptions at 1µs boundary: %v", err)
	}
	if err := db.EnableCompressionWithOptions(ctx, "m", CompressionOptions{SegmentBy: []string{"dev"}}); err != nil {
		t.Fatalf("EnableCompressionWithOptions: %v", err)
	}
	if n, err := db.DropChunks(ctx, "m", time.Hour); err != nil || n != 0 {
		t.Fatalf("DropChunks = %d, %v", n, err)
	}
	if len(fake.statements) != 3 || !strings.Contains(fake.statements[0], "by_range($2, ($3)::interval)") {
		t.Fatalf("statements = %q", fake.statements)
	}
}

func TestHypertableRejectsInvalidInputBeforeDatabaseAccess(t *testing.T) {
	t.Parallel()
	db, fake := fakeHelper("2.30.0", "timescale")
	for name, opts := range map[string]HypertableOptions{
		"sub-microsecond": {ChunkTimeInterval: 999 * time.Nanosecond},
		"negative":        {ChunkTimeInterval: -time.Hour},
	} {
		if err := db.CreateHypertableWithOptions(t.Context(), "m", "time", opts); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := db.CreateHypertableWithOptions(t.Context(), "m", "", HypertableOptions{}); err == nil {
		t.Error("empty time column accepted")
	}
	if len(fake.statements) != 0 {
		t.Fatalf("statements = %q", fake.statements)
	}
}

func TestUnsupportedErrorMessage(t *testing.T) {
	t.Parallel()
	err := &UnsupportedError{Feature: FeatureCompression, Edition: EditionApache}
	if err.Error() != "timescale: compression unavailable under edition apache" {
		t.Fatalf("Error() = %q", err.Error())
	}
}

func TestCompressionSQLQuotesQualifiedRelation(t *testing.T) {
	t.Parallel()
	query, err := compressionSQL("analytics.metrics", CompressionOptions{})
	if err != nil {
		t.Fatalf("compressionSQL: %v", err)
	}
	want := `ALTER TABLE "analytics"."metrics" SET (timescaledb.compress)`
	if query != want {
		t.Fatalf("compressionSQL = %q, want %q", query, want)
	}
}

func TestCompressionSQLSegmentAndOrder(t *testing.T) {
	t.Parallel()
	query, err := compressionSQL("metrics", CompressionOptions{
		SegmentBy: []string{"device", `o'brien`},
		OrderBy:   []OrderColumn{{Column: "time", Descending: true}, {Column: `we"ird`}},
	})
	if err != nil {
		t.Fatalf("compressionSQL: %v", err)
	}
	want := `ALTER TABLE "metrics" SET (timescaledb.compress, ` +
		`timescaledb.compress_segmentby = '"device", "o''brien"', ` +
		`timescaledb.compress_orderby = '"time" DESC, "we""ird"')`
	if query != want {
		t.Fatalf("compressionSQL =\n%s\nwant\n%s", query, want)
	}
}

func TestCompressionSQLRejectsMalformedInput(t *testing.T) {
	t.Parallel()
	for _, table := range []string{"", ".metrics", "analytics.", "db.analytics.metrics", "me\x00trics"} {
		if _, err := compressionSQL(table, CompressionOptions{}); err == nil {
			t.Errorf("compressionSQL(%q) accepted malformed relation", table)
		}
	}
	for name, opts := range map[string]CompressionOptions{
		"empty segment column": {SegmentBy: []string{""}},
		"NUL segment column":   {SegmentBy: []string{"a\x00"}},
		"empty order column":   {OrderBy: []OrderColumn{{}}},
	} {
		if _, err := compressionSQL("metrics", opts); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestContinuousAggregateSQL(t *testing.T) {
	t.Parallel()
	query, err := continuousAggregateSQL(ContinuousAggregate{
		Name:  "public.m_hourly",
		Query: " SELECT time_bucket('1 hour', time) AS bucket, avg(val) FROM m GROUP BY 1 ",
	})
	if err != nil {
		t.Fatalf("continuousAggregateSQL: %v", err)
	}
	want := `CREATE MATERIALIZED VIEW IF NOT EXISTS "public"."m_hourly" WITH (timescaledb.continuous) AS ` +
		`SELECT time_bucket('1 hour', time) AS bucket, avg(val) FROM m GROUP BY 1 WITH NO DATA`
	if query != want {
		t.Fatalf("query =\n%s\nwant\n%s", query, want)
	}
	withData, err := continuousAggregateSQL(ContinuousAggregate{Name: "v", Query: "SELECT 1", WithData: true})
	if err != nil || !strings.HasSuffix(withData, "SELECT 1 WITH DATA") {
		t.Fatalf("WithData query = %q, %v", withData, err)
	}
	for name, agg := range map[string]ContinuousAggregate{
		"empty query":   {Name: "v", Query: "  "},
		"stacked query": {Name: "v", Query: "SELECT 1; DROP TABLE m"},
		"NUL query":     {Name: "v", Query: "SELECT 1\x00"},
		"empty name":    {Query: "SELECT 1"},
	} {
		if _, err := continuousAggregateSQL(agg); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestRefreshPolicyArgs(t *testing.T) {
	t.Parallel()
	args, err := refreshPolicyArgs("v", RefreshPolicy{ScheduleInterval: time.Microsecond})
	if err != nil {
		t.Fatalf("minimal policy: %v", err)
	}
	if start, end := args[1].(pgtype.Interval), args[2].(pgtype.Interval); start.Valid || end.Valid {
		t.Fatalf("zero offsets must map to NULL, got %v %v", start, end)
	}
	if _, err = refreshPolicyArgs("v", RefreshPolicy{
		StartOffset: 24 * time.Hour, EndOffset: time.Hour, ScheduleInterval: time.Hour,
	}); err != nil {
		t.Fatalf("valid window: %v", err)
	}
	for name, policy := range map[string]RefreshPolicy{
		"zero schedule":     {},
		"negative schedule": {ScheduleInterval: -time.Hour},
		"empty window":      {StartOffset: time.Hour, EndOffset: time.Hour, ScheduleInterval: time.Hour},
		"inverted window":   {StartOffset: time.Hour, EndOffset: 2 * time.Hour, ScheduleInterval: time.Hour},
		"sub-µs start":      {StartOffset: time.Nanosecond, ScheduleInterval: time.Hour},
		"negative end":      {EndOffset: -time.Hour, ScheduleInterval: time.Hour},
	} {
		if _, policyErr := refreshPolicyArgs("v", policy); policyErr == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err = refreshPolicyArgs("", RefreshPolicy{ScheduleInterval: time.Hour}); err == nil {
		t.Error("empty view accepted")
	}
}

func TestRefreshRejectsInvertedWindow(t *testing.T) {
	t.Parallel()
	db, fake := fakeHelper("2.30.0", "timescale")
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := db.RefreshContinuousAggregate(t.Context(), "v", at, at); err == nil {
		t.Error("empty window accepted")
	}
	if err := db.RefreshContinuousAggregate(t.Context(), "", time.Time{}, time.Time{}); err == nil {
		t.Error("empty view accepted")
	}
	if err := db.RefreshContinuousAggregate(t.Context(), "v", at, time.Time{}); err != nil {
		t.Fatalf("open-ended window: %v", err)
	}
	if len(fake.statements) != 1 {
		t.Fatalf("statements = %q", fake.statements)
	}
}

func TestOptionsValidate(t *testing.T) {
	t.Parallel()
	if err := DefaultOptions().validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
	for name, opts := range map[string]Options{
		"zero timeout":    {},
		"unknown edition": {Require: "enterprise", DetectTimeout: time.Second},
	} {
		if err := opts.validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestOptionsCheck(t *testing.T) {
	t.Parallel()
	tests := []struct {
		require Edition
		edition Edition
		want    error
	}{
		{"", EditionNone, nil},
		{EditionNone, EditionNone, nil},
		{EditionApache, EditionNone, ErrExtensionMissing},
		{EditionApache, EditionApache, nil},
		{EditionApache, EditionUnknown, nil},
		{EditionCommunity, EditionNone, ErrExtensionMissing},
		{EditionCommunity, EditionApache, ErrUnsupportedEdition},
		{EditionCommunity, EditionUnknown, ErrUnsupportedEdition},
		{EditionCommunity, EditionCommunity, nil},
	}
	for _, tt := range tests {
		err := Options{Require: tt.require}.check(Capabilities{Edition: tt.edition})
		if (tt.want == nil) != (err == nil) || (tt.want != nil && !errors.Is(err, tt.want)) {
			t.Errorf("require %q, edition %q: error = %v, want %v", tt.require, tt.edition, err, tt.want)
		}
	}
}
