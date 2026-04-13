// Package cron parses cron expressions (robfig/cron/v3) and registers them
// as river periodic jobs.
//
// Usage:
//
//	fx.Invoke(func(c *jobs.Client) error {
//	    return cron.Register(c, "0 */5 * * *", func() river.JobArgs {
//	        return RefreshArgs{}
//	    })
//	})
//
// robfig/cron/v3's grammar is the classic 5-field (minute hour day month
// weekday). Use `@every 30s` for sub-minute jobs + `@hourly`/`@daily`/…
// for standard preset schedules.
package cron

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	rcron "github.com/robfig/cron/v3"
)

// parser matches robfig v3's default (5-field + descriptors).
var parser = rcron.NewParser(rcron.Minute | rcron.Hour | rcron.Dom | rcron.Month | rcron.Dow | rcron.Descriptor)

// Validate parses expr without registering. Returns a descriptive error if
// malformed; useful for config validation at load time.
func Validate(expr string) error {
	if _, err := parser.Parse(expr); err != nil {
		return fmt.Errorf("cron: parse %q: %w", expr, err)
	}
	return nil
}

// Schedule parses expr into a river.PeriodicSchedule.
func Schedule(expr string) (river.PeriodicSchedule, error) {
	s, err := parser.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("cron: parse %q: %w", expr, err)
	}
	return &riverSchedule{s: s}, nil
}

// Register builds a PeriodicJob from expr + constructor and adds it to the
// running river client. Safe to call before or after the client starts.
//
// constructor returns the JobArgs that will be inserted on each tick.
// Return nil to skip this tick (e.g. when the job is disabled via config).
func Register[T river.JobArgs](c *river.Client[pgx.Tx], expr string, constructor func() T) error {
	sched, err := Schedule(expr)
	if err != nil {
		return err
	}
	pj := river.NewPeriodicJob(sched, func() (river.JobArgs, *river.InsertOpts) {
		return constructor(), nil
	}, nil)
	c.PeriodicJobs().Add(pj)
	return nil
}

// riverSchedule adapts a robfig cron.Schedule to river.PeriodicSchedule.
type riverSchedule struct{ s rcron.Schedule }

func (r *riverSchedule) Next(t time.Time) time.Time { return r.s.Next(t) }
