// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jobs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golusoris/golusoris/jobs"
	rivertest "github.com/golusoris/golusoris/testutil/river"
)

type uniqueArgs struct {
	Asset string `json:"asset"`
}

func (uniqueArgs) Kind() string { return "unique-probe" }

// TestAliases_uniqueAndScheduledInsert inserts through jobs aliases only (no
// river import): unique dedup, future scheduling, and River's ExcludeKind guard.
func TestAliases_uniqueAndScheduledInsert(t *testing.T) {
	t.Parallel()
	rv := rivertest.Start(t, rivertest.Options{})
	producer, err := jobs.New(rv.Pool, jobs.Options{Enabled: true, ProducerOnly: true}, nil, discard())
	if err != nil {
		t.Fatalf("New producer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	opts := &jobs.InsertOpts{UniqueOpts: jobs.UniqueOpts{ByArgs: true}}
	first, err := producer.Insert(ctx, uniqueArgs{Asset: "a"}, opts)
	if err != nil {
		t.Fatalf("first unique insert: %v", err)
	}
	var second *jobs.InsertResult
	if second, err = producer.Insert(ctx, uniqueArgs{Asset: "a"}, opts); err != nil {
		t.Fatalf("second unique insert: %v", err)
	}
	if first.UniqueSkippedAsDuplicate || !second.UniqueSkippedAsDuplicate || second.Job.ID != first.Job.ID {
		t.Fatalf("unique insert not deduplicated: first=%+v second=%+v", first, second)
	}

	scheduled, err := producer.Insert(ctx, uniqueArgs{Asset: "later"}, &jobs.InsertOpts{
		Queue:       jobs.DefaultQueue,
		ScheduledAt: first.Job.CreatedAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("scheduled insert: %v", err)
	}
	if scheduled.Job.State != jobs.JobStateScheduled {
		t.Fatalf("scheduled job state = %q, want %q", scheduled.Job.State, jobs.JobStateScheduled)
	}

	if _, err = producer.Insert(ctx, uniqueArgs{Asset: "b"}, &jobs.InsertOpts{
		UniqueOpts: jobs.UniqueOpts{ExcludeKind: true},
	}); err == nil {
		t.Fatal("ExcludeKind-only unique opts accepted; River v0.48+ rejects it")
	}
}

func TestJobCancel_wrapsForRiver(t *testing.T) {
	t.Parallel()
	cause := errors.New("bad input")
	err := jobs.JobCancel(cause)
	if !errors.Is(err, cause) {
		t.Fatalf("JobCancel(%v) = %v, want wrapped cause", cause, err)
	}
	if jobs.JobCancel(nil) == nil {
		t.Fatal("JobCancel(nil) = nil, want a cancel sentinel")
	}
}
