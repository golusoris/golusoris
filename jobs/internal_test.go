// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jobs

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/config"
)

type registerArgs struct{}

func (registerArgs) Kind() string { return "register-probe" }

type registerWorker struct {
	river.WorkerDefaults[registerArgs]
}

func (*registerWorker) Work(context.Context, *river.Job[registerArgs]) error { return nil }

func TestRegisterReturnsDependencyAndDuplicateErrors(t *testing.T) {
	t.Parallel()

	worker := &registerWorker{}
	if err := Register[registerArgs](nil, worker); err == nil || !strings.Contains(err.Error(), "nil workers") {
		t.Fatalf("Register nil workers error = %v", err)
	}
	if err := Register(new(Workers), worker); err == nil || !strings.Contains(err.Error(), "uninitialized workers") {
		t.Fatalf("Register zero workers error = %v", err)
	}
	var nilWorker *registerWorker
	if err := Register[registerArgs](NewWorkers(), nilWorker); err == nil || !strings.Contains(err.Error(), "nil worker") {
		t.Fatalf("Register nil worker error = %v", err)
	}
	workers := NewWorkers()
	if err := Register(workers, worker); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := Register(workers, worker); err == nil {
		t.Fatal("Register accepted duplicate worker")
	}
}

func TestNewRejectsNilLogger(t *testing.T) {
	t.Parallel()

	client, err := New(nil, Options{}, nil, nil)
	if client != nil {
		t.Fatal("New returned client with nil logger")
	}
	if err == nil || !strings.Contains(err.Error(), "nil logger") {
		t.Fatalf("New error = %v, want nil logger", err)
	}
}

func TestNewNilPoolSupportsTransactionalClientOnly(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	client, err := New(nil, Options{}, nil, logger)
	if err != nil {
		t.Fatalf("New transactional client: %v", err)
	}
	if client == nil {
		t.Fatal("New transactional client returned nil")
	}

	client, err = New(nil, Options{}, NewWorkers(), logger)
	if client != nil {
		t.Fatal("New returned worker client without pool")
	}
	if err == nil {
		t.Fatal("New accepted workers without pool")
	}
}

func TestNewRejectsNegativeNamedQueueWorkers(t *testing.T) {
	t.Parallel()

	client, err := New(
		new(pgxpool.Pool),
		Options{Queue: QueueOptions{Queues: map[string]QueueConfig{"critical": {Max: -1}}}},
		NewWorkers(),
		slog.New(slog.DiscardHandler),
	)
	if client != nil {
		t.Fatal("New returned client with negative named queue workers")
	}
	if err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("New error = %v, want negative queue limit", err)
	}
}

func TestNewDefaultsZeroNamedQueueWorkers(t *testing.T) {
	t.Parallel()

	client, err := New(
		new(pgxpool.Pool),
		Options{Queue: QueueOptions{Queues: map[string]QueueConfig{"critical": {}}}},
		NewWorkers(),
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client == nil {
		t.Fatal("New returned nil client")
	}
}

func TestProvideClientProducerOnlySkipsWorkerLifecycle(t *testing.T) {
	t.Parallel()

	lifecycle := fxtest.NewLifecycle(t)
	client, err := provideClient(
		lifecycle,
		nil,
		Options{Enabled: true, ProducerOnly: true},
		NewWorkers(),
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("provideClient: %v", err)
	}
	if client == nil {
		t.Fatal("provideClient returned nil producer client")
	}
	lifecycle.RequireStart()
	lifecycle.RequireStop()
}

type typedNilObserver struct{}

func (o *typedNilObserver) JobInserted(string, string) {
	if o == nil {
		panic("typed-nil observer dereferenced")
	}
}

func (o *typedNilObserver) JobFinished(string, string, string, time.Duration) {
	if o == nil {
		panic("typed-nil observer dereferenced")
	}
}

func TestWithDefaults_DropsTypedNilObserver(t *testing.T) {
	t.Parallel()
	var observer *typedNilObserver
	got := Options{Observer: observer}.withDefaults()
	if got.Observer != nil {
		t.Fatal("withDefaults retained a typed-nil observer")
	}
}

func TestInsertObserver_TypedNilIsNoop(t *testing.T) {
	t.Parallel()
	var observer *typedNilObserver
	middleware := &insertObserver{obs: observer}
	_, err := middleware.InsertMany(
		context.Background(),
		[]*rivertype.JobInsertParams{{Kind: "test"}},
		func(context.Context) ([]*rivertype.JobInsertResult, error) { return nil, nil },
	)
	if err != nil {
		t.Fatalf("InsertMany: %v", err)
	}
}

func TestObserveProducerOnlyClientIsNoop(t *testing.T) {
	t.Parallel()

	client, err := New(nil, DefaultOptions(), nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cancel := Observe(client, &typedNilObserver{})
	cancel()
}

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

func TestLoadOptions_RejectsNilConfig(t *testing.T) {
	t.Parallel()
	if _, err := LoadOptions(nil); err == nil {
		t.Fatal("LoadOptions accepted nil config")
	}
}

func TestProvideInserter(t *testing.T) {
	t.Parallel()
	if _, err := ProvideInserter[pgx.Tx](nil); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("ProvideInserter(nil) error = %v, want disabled", err)
	}
	client, err := New(nil, Options{}, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ins, err := ProvideInserter(client)
	if err != nil || ins == nil {
		t.Fatalf("ProvideInserter(client) = %v, %v", ins, err)
	}
}
