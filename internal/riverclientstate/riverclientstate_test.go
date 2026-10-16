// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package riverclientstate_test

import (
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/golusoris/golusoris/internal/riverclientstate"
)

func newClient(t *testing.T, queues bool, leaderElectionDisabled bool) *river.Client[pgx.Tx] {
	t.Helper()
	config := &river.Config{
		LeaderElectionDisabled: leaderElectionDisabled,
		Logger:                 slog.New(slog.DiscardHandler),
	}
	if queues {
		config.Queues = map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}}
		config.Workers = river.NewWorkers()
	}
	client, err := river.NewClient(riverpgxv5.New(new(pgxpool.Pool)), config)
	if err != nil {
		t.Fatalf("river.NewClient: %v", err)
	}
	return client
}

func TestNilClient(t *testing.T) {
	t.Parallel()

	if riverclientstate.WorksJobs[pgx.Tx](nil) {
		t.Fatal("WorksJobs(nil) = true, want false")
	}
	if !riverclientstate.LeaderElectionDisabled[pgx.Tx](nil) {
		t.Fatal("LeaderElectionDisabled(nil) = false, want true")
	}
}

func TestZeroClientFailsClosed(t *testing.T) {
	t.Parallel()

	client := new(river.Client[pgx.Tx])
	if riverclientstate.WorksJobs(client) {
		t.Fatal("WorksJobs(zero client) = true, want false")
	}
	if !riverclientstate.LeaderElectionDisabled(client) {
		t.Fatal("LeaderElectionDisabled(zero client) = false, want true")
	}
}

func TestProducerOnlyClient(t *testing.T) {
	t.Parallel()

	client := newClient(t, false, false)
	if riverclientstate.WorksJobs(client) {
		t.Fatal("WorksJobs(producer-only) = true, want false")
	}
	if riverclientstate.LeaderElectionDisabled(client) {
		t.Fatal("LeaderElectionDisabled(producer-only) = true, want false")
	}
}

func TestWorkingClient(t *testing.T) {
	t.Parallel()

	client := newClient(t, true, false)
	if !riverclientstate.WorksJobs(client) {
		t.Fatal("WorksJobs(working client) = false, want true")
	}
	if riverclientstate.LeaderElectionDisabled(client) {
		t.Fatal("LeaderElectionDisabled(working client) = true, want false")
	}
}

func TestLeaderElectionDisabledClient(t *testing.T) {
	t.Parallel()

	client := newClient(t, true, true)
	if !riverclientstate.WorksJobs(client) {
		t.Fatal("WorksJobs(leader election disabled) = false, want true")
	}
	if !riverclientstate.LeaderElectionDisabled(client) {
		t.Fatal("LeaderElectionDisabled(leader election disabled) = false, want true")
	}
}
