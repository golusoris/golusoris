package jobs

import (
	"testing"
	"time"

	"github.com/golusoris/golusoris/config"
)

func TestWithDefaults_zeroFilled(t *testing.T) {
	t.Parallel()
	got := Options{}.withDefaults()
	d := DefaultOptions()
	if got.Queue.Default.Max != d.Queue.Default.Max {
		t.Errorf("Queue.Default.Max = %d, want %d", got.Queue.Default.Max, d.Queue.Default.Max)
	}
	if got.Job.Timeout != d.Job.Timeout {
		t.Errorf("Job.Timeout = %v, want %v", got.Job.Timeout, d.Job.Timeout)
	}
	if got.Job.MaxAttempts != d.Job.MaxAttempts {
		t.Errorf("Job.MaxAttempts = %d, want %d", got.Job.MaxAttempts, d.Job.MaxAttempts)
	}
	if got.FetchCooldown != d.FetchCooldown {
		t.Errorf("FetchCooldown = %v, want %v", got.FetchCooldown, d.FetchCooldown)
	}
	if got.RescueStuckAfter != d.RescueStuckAfter {
		t.Errorf("RescueStuckAfter = %v, want %v", got.RescueStuckAfter, d.RescueStuckAfter)
	}
}

func TestWithDefaults_preservesNonZero(t *testing.T) {
	t.Parallel()
	in := Options{
		Queue:            QueueOptions{Default: QueueDefault{Max: 5}},
		Job:              JobOptions{Timeout: 10 * time.Second, MaxAttempts: 3},
		FetchCooldown:    200 * time.Millisecond,
		RescueStuckAfter: 2 * time.Hour,
	}
	got := in.withDefaults()
	if got.Queue.Default.Max != 5 {
		t.Errorf("Queue.Default.Max = %d, want 5", got.Queue.Default.Max)
	}
	if got.Job.Timeout != 10*time.Second {
		t.Errorf("Job.Timeout = %v, want 10s", got.Job.Timeout)
	}
	if got.Job.MaxAttempts != 3 {
		t.Errorf("Job.MaxAttempts = %d, want 3", got.Job.MaxAttempts)
	}
	if got.FetchCooldown != 200*time.Millisecond {
		t.Errorf("FetchCooldown = %v, want 200ms", got.FetchCooldown)
	}
	if got.RescueStuckAfter != 2*time.Hour {
		t.Errorf("RescueStuckAfter = %v, want 2h", got.RescueStuckAfter)
	}
}

func TestLoadOptions_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_"})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := loadOptions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Queue.Default.Max != 10 {
		t.Errorf("Queue.Default.Max = %d, want 10", opts.Queue.Default.Max)
	}
	if opts.Job.Timeout != 30*time.Second {
		t.Errorf("Job.Timeout = %v, want 30s", opts.Job.Timeout)
	}
	if opts.Job.MaxAttempts != 25 {
		t.Errorf("Job.MaxAttempts = %d, want 25", opts.Job.MaxAttempts)
	}
}
