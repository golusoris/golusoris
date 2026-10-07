// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jobs

import (
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
