// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package timescale

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// ContinuousAggregate describes a continuous-aggregate materialized view.
type ContinuousAggregate struct {
	// Name is the view, as identifier or schema.identifier.
	Name string
	// Query is the trusted SELECT ... time_bucket(...) ... GROUP BY body.
	// It is embedded verbatim, so never build it from user input; a
	// semicolon is rejected so the statement cannot be stacked.
	Query string
	// WithData materializes existing rows during creation. False (default)
	// creates the view empty; a refresh or policy fills it.
	WithData bool
}

// RefreshPolicy configures add_continuous_aggregate_policy.
type RefreshPolicy struct {
	// StartOffset is the window start relative to now; zero refreshes from
	// the oldest data.
	StartOffset time.Duration
	// EndOffset is the window end relative to now; zero refreshes up to now.
	EndOffset time.Duration
	// ScheduleInterval is how often the policy job runs; must be positive.
	ScheduleInterval time.Duration
}

// CreateContinuousAggregate creates agg if it does not exist. Requires the
// community edition.
func (d *DB) CreateContinuousAggregate(ctx context.Context, agg ContinuousAggregate) error {
	query, err := continuousAggregateSQL(agg)
	if err != nil {
		return fmt.Errorf("timescale: continuous aggregate: %w", err)
	}
	if poolErr := d.validatePool(); poolErr != nil {
		return poolErr
	}
	if reqErr := d.require(ctx, FeatureContinuousAggregate); reqErr != nil {
		return reqErr
	}
	if _, err = d.db.Exec(ctx, query); err != nil {
		return fmt.Errorf("timescale: create continuous aggregate %s: %w", agg.Name, err)
	}
	return nil
}

// AddContinuousAggregatePolicy schedules automatic refreshes of view.
// Idempotent for an unchanged policy. Requires the community edition.
func (d *DB) AddContinuousAggregatePolicy(ctx context.Context, view string, policy RefreshPolicy) error {
	args, err := refreshPolicyArgs(view, policy)
	if err != nil {
		return fmt.Errorf("timescale: continuous aggregate policy: %w", err)
	}
	if poolErr := d.validatePool(); poolErr != nil {
		return poolErr
	}
	if reqErr := d.require(ctx, FeatureContinuousAggregate); reqErr != nil {
		return reqErr
	}
	_, err = d.db.Exec(
		ctx,
		`SELECT add_continuous_aggregate_policy($1,
			start_offset => ($2)::interval,
			end_offset => ($3)::interval,
			schedule_interval => ($4)::interval,
			if_not_exists => true)`,
		args...,
	)
	if err != nil {
		return fmt.Errorf("timescale: add_continuous_aggregate_policy %s: %w", view, err)
	}
	return nil
}

// RefreshContinuousAggregate materializes view for [start, end). A zero
// start or end leaves that side of the window unbounded. The window applies
// to timestamptz buckets. Requires the community edition.
func (d *DB) RefreshContinuousAggregate(ctx context.Context, view string, start, end time.Time) error {
	if view == "" {
		return errors.New("timescale: refresh continuous aggregate: view is required")
	}
	if !start.IsZero() && !end.IsZero() && !start.Before(end) {
		return errors.New("timescale: refresh continuous aggregate: start must precede end")
	}
	if poolErr := d.validatePool(); poolErr != nil {
		return poolErr
	}
	if reqErr := d.require(ctx, FeatureContinuousAggregate); reqErr != nil {
		return reqErr
	}
	_, err := d.db.Exec(
		ctx,
		"CALL refresh_continuous_aggregate($1, ($2)::timestamptz, ($3)::timestamptz)",
		view, timestampArg(start), timestampArg(end),
	)
	if err != nil {
		return fmt.Errorf("timescale: refresh_continuous_aggregate %s: %w", view, err)
	}
	return nil
}

func continuousAggregateSQL(agg ContinuousAggregate) (string, error) {
	relation, err := relationIdent(agg.Name)
	if err != nil {
		return "", err
	}
	query := strings.TrimSpace(agg.Query)
	if query == "" {
		return "", errors.New("query is required")
	}
	if strings.ContainsAny(query, ";\x00") {
		return "", errors.New("query must be one statement without semicolons or NUL")
	}
	data := " WITH NO DATA"
	if agg.WithData {
		data = " WITH DATA"
	}
	return "CREATE MATERIALIZED VIEW IF NOT EXISTS " + relation +
		" WITH (timescaledb.continuous) AS " + query + data, nil
}

func refreshPolicyArgs(view string, policy RefreshPolicy) ([]any, error) {
	if view == "" {
		return nil, errors.New("view is required")
	}
	schedule, err := intervalValue(policy.ScheduleInterval)
	if err != nil {
		return nil, fmt.Errorf("schedule interval: %w", err)
	}
	start, err := optionalInterval(policy.StartOffset)
	if err != nil {
		return nil, fmt.Errorf("start offset: %w", err)
	}
	end, err := optionalInterval(policy.EndOffset)
	if err != nil {
		return nil, fmt.Errorf("end offset: %w", err)
	}
	if start.Valid && end.Valid && policy.StartOffset <= policy.EndOffset {
		return nil, errors.New("start offset must exceed end offset")
	}
	return []any{view, start, end, schedule}, nil
}

// optionalInterval maps zero to SQL NULL and validates anything else.
func optionalInterval(d time.Duration) (pgtype.Interval, error) {
	if d == 0 {
		return pgtype.Interval{}, nil
	}
	return intervalValue(d)
}

func timestampArg(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}
