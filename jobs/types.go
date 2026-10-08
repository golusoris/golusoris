// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jobs

import (
	"context"
	"database/sql"
	"errors"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// Aliases let apps define, insert, and work jobs without importing river, so a
// River upgrade stays contained to this package.
type (
	// JobArgs aliases river.JobArgs: a JSON-encodable struct with a Kind.
	JobArgs = river.JobArgs
	// InsertOpts aliases river.InsertOpts (queue, priority, ScheduledAt, unique).
	InsertOpts = river.InsertOpts
	// UniqueOpts aliases river.UniqueOpts for deduplicated inserts.
	UniqueOpts = river.UniqueOpts
	// InsertManyParams aliases river.InsertManyParams for batch inserts.
	InsertManyParams = river.InsertManyParams
	// InsertResult aliases rivertype.JobInsertResult.
	InsertResult = rivertype.JobInsertResult
	// JobRow aliases rivertype.JobRow, the untyped job record.
	JobRow = rivertype.JobRow
	// JobState aliases rivertype.JobState for UniqueOpts.ByState.
	JobState = rivertype.JobState
	// Job aliases river.Job[T], the typed job a worker receives.
	Job[T JobArgs] = river.Job[T]
	// Worker aliases river.Worker[T].
	Worker[T JobArgs] = river.Worker[T]
	// WorkerDefaults aliases river.WorkerDefaults[T]; embed it in workers.
	WorkerDefaults[T JobArgs] = river.WorkerDefaults[T]
)

// DefaultQueue is the name of River's default queue ("default").
const DefaultQueue = river.QueueDefault

// Job states, for UniqueOpts.ByState and state-filtered metrics.
const (
	JobStateAvailable = rivertype.JobStateAvailable
	JobStateCancelled = rivertype.JobStateCancelled
	JobStateCompleted = rivertype.JobStateCompleted
	JobStateDiscarded = rivertype.JobStateDiscarded
	JobStatePending   = rivertype.JobStatePending
	JobStateRetryable = rivertype.JobStateRetryable
	JobStateRunning   = rivertype.JobStateRunning
	JobStateScheduled = rivertype.JobStateScheduled
)

// JobCancel wraps err so River cancels the job permanently instead of retrying.
func JobCancel(err error) error {
	return river.JobCancel(err) //nolint:wrapcheck // River matches its own sentinel; wrapping would hide it
}

// ClientSQL is a River client over database/sql (jobs/sqlite).
type ClientSQL = river.Client[*sql.Tx]

// Inserter is the driver-agnostic enqueue surface: *Client and *ClientSQL
// both satisfy it, and the fx modules provide one. Transactional inserts
// (InsertTx/InsertManyTx) take the driver's own tx type, so they stay on the
// concrete client.
type Inserter interface {
	Insert(ctx context.Context, args JobArgs, opts *InsertOpts) (*InsertResult, error)
	InsertMany(ctx context.Context, params []InsertManyParams) ([]*InsertResult, error)
}

var (
	_ Inserter = (*Client)(nil)
	_ Inserter = (*ClientSQL)(nil)
)

// ProvideInserter exposes a module's client as an [Inserter]; it fails when
// the client is nil (jobs.enabled=false). Driver modules provide it.
func ProvideInserter[TTx any](c *river.Client[TTx]) (Inserter, error) {
	if c == nil {
		return nil, errors.New("jobs: inserter: jobs client disabled")
	}
	return c, nil
}
