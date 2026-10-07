// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package timescale

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
)

// Edition is the TimescaleDB licence edition the server runs.
type Edition string

const (
	// EditionNone means the timescaledb extension is not installed in the database.
	EditionNone Edition = "none"
	// EditionApache is the Apache-2.0 build: hypertables and drop_chunks only.
	EditionApache Edition = "apache"
	// EditionCommunity is the Timescale License (TSL) build: compression,
	// policies, and continuous aggregates. The server reports it as
	// timescaledb.license = 'timescale'.
	EditionCommunity Edition = "community"
	// EditionUnknown is an installed extension reporting an unrecognised
	// licence. Every TSL feature fails closed under it.
	EditionUnknown Edition = "unknown"
)

// Feature names one TimescaleDB capability a helper needs.
type Feature string

const (
	// FeatureHypertable covers create_hypertable and set_chunk_time_interval.
	FeatureHypertable Feature = "hypertable"
	// FeatureDropChunks covers drop_chunks, the Apache-edition retention fallback.
	FeatureDropChunks Feature = "drop_chunks"
	// FeatureCompression covers columnar compression and its policies.
	FeatureCompression Feature = "compression"
	// FeatureRetentionPolicy covers add_retention_policy background jobs.
	FeatureRetentionPolicy Feature = "retention_policy"
	// FeatureContinuousAggregate covers continuous aggregates, their refresh
	// policies, and manual refreshes.
	FeatureContinuousAggregate Feature = "continuous_aggregate"
)

// ErrExtensionMissing reports that the timescaledb extension is not
// installed. Tables stay plain PostgreSQL tables; callers that can run
// without TimescaleDB match it with errors.Is and continue.
var ErrExtensionMissing = errors.New("timescale: extension not installed")

// ErrUnsupportedEdition reports a feature the detected edition does not
// ship (a TSL feature under the Apache build or an unknown licence).
var ErrUnsupportedEdition = errors.New("timescale: feature unsupported by edition")

// UnsupportedError names the feature and edition behind [ErrExtensionMissing]
// or [ErrUnsupportedEdition].
type UnsupportedError struct {
	Feature Feature
	Edition Edition
}

// Error implements error.
func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("timescale: %s unavailable under edition %s", e.Feature, e.Edition)
}

// Unwrap maps the edition to its sentinel so errors.Is works.
func (e *UnsupportedError) Unwrap() error {
	if e.Edition == EditionNone {
		return ErrExtensionMissing
	}
	return ErrUnsupportedEdition
}

// Capabilities is the detected TimescaleDB installation.
type Capabilities struct {
	// Edition is the licence edition; [EditionNone] without the extension.
	Edition Edition
	// Version is pg_extension.extversion, empty without the extension.
	Version string
	// License is the raw timescaledb.license setting, empty when unset.
	License string
}

// Installed reports whether the timescaledb extension exists in the database.
func (c Capabilities) Installed() bool {
	return c.Edition != EditionNone && c.Edition != ""
}

// Supports reports whether the detected edition ships feature f. Unknown
// features and unknown editions fail closed for every TSL feature.
func (c Capabilities) Supports(f Feature) bool {
	switch f {
	case FeatureHypertable, FeatureDropChunks:
		return c.Installed()
	case FeatureCompression, FeatureRetentionPolicy, FeatureContinuousAggregate:
		return c.Edition == EditionCommunity
	default:
		return false
	}
}

// capabilitiesSQL reads the extension version and the licence GUC in one
// round trip; missing_ok keeps plain PostgreSQL from raising an error.
const capabilitiesSQL = `SELECT
	(SELECT extversion FROM pg_catalog.pg_extension WHERE extname = 'timescaledb'),
	current_setting('timescaledb.license', true)`

// Capabilities detects the installed TimescaleDB extension and its edition.
// The licence is fixed per server start, so the result stays valid until
// the server restarts.
func (d *DB) Capabilities(ctx context.Context) (Capabilities, error) {
	if poolErr := d.validatePool(); poolErr != nil {
		return Capabilities{}, poolErr
	}
	var version, license pgtype.Text
	if err := d.db.QueryRow(ctx, capabilitiesSQL).Scan(&version, &license); err != nil {
		return Capabilities{}, fmt.Errorf("timescale: detect capabilities: %w", err)
	}
	return classify(version, license), nil
}

func classify(version, license pgtype.Text) Capabilities {
	caps := Capabilities{Edition: EditionNone, License: license.String}
	if !version.Valid {
		return caps
	}
	caps.Version = version.String
	switch license.String {
	case "timescale":
		caps.Edition = EditionCommunity
	case "apache":
		caps.Edition = EditionApache
	default:
		caps.Edition = EditionUnknown
	}
	return caps
}

// require detects capabilities and refuses f before any DDL reaches the
// server, so callers get a typed error instead of SQLSTATE 0A000.
func (d *DB) require(ctx context.Context, f Feature) error {
	caps, err := d.Capabilities(ctx)
	if err != nil {
		return err
	}
	if !caps.Supports(f) {
		return &UnsupportedError{Feature: f, Edition: caps.Edition}
	}
	return nil
}
