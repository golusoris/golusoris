// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pg

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/golusoris/golusoris/leader"
)

func TestDefaultOptions(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	if opts.Enabled {
		t.Fatal("want Enabled=false")
	}
	if opts.PG.Retry != 2*time.Second {
		t.Fatalf("want PG.Retry=2s, got %v", opts.PG.Retry)
	}
}

func TestWithDefaults_preserves(t *testing.T) {
	t.Parallel()
	opts := Options{
		Enabled:  true,
		Name:     "myapp",
		Identity: "pod-1",
		PG:       BackendOptions{Retry: 5 * time.Second},
	}
	got := opts.withDefaults()
	if got.PG.Retry != 5*time.Second {
		t.Fatalf("want Retry preserved as 5s, got %v", got.PG.Retry)
	}
	if got.Name != "myapp" {
		t.Fatalf("want Name preserved as %q, got %q", "myapp", got.Name)
	}
}

func TestKeyFor_stable(t *testing.T) {
	t.Parallel()
	a, err := keyFor("foo")
	if err != nil {
		t.Fatalf("keyFor: %v", err)
	}
	b, err := keyFor("foo")
	if err != nil {
		t.Fatalf("keyFor: %v", err)
	}
	if a != b {
		t.Fatalf("want keyFor to be deterministic; got %d and %d", a, b)
	}
}

func TestResolveIdentity_explicit(t *testing.T) {
	t.Parallel()
	if got := resolveIdentity("pod-1"); got != "pod-1" {
		t.Fatalf("resolveIdentity(%q) = %q, want unchanged", "pod-1", got)
	}
}

func TestResolveIdentity_fallsBackWhenEmpty(t *testing.T) {
	t.Parallel()
	got := resolveIdentity("")
	if got == "" {
		t.Fatal("resolveIdentity(\"\") must return a non-empty identity")
	}
	want, err := os.Hostname()
	if err != nil {
		want = "unknown"
	}
	if got != want {
		t.Fatalf("resolveIdentity(\"\") = %q, want %q", got, want)
	}
}

func TestLead_invokesCallbacksInOrderThenBlocksUntilDone(t *testing.T) {
	t.Parallel()
	var order []string
	leaderCtxDone := make(chan struct{})
	cb := leader.Callbacks{
		OnNewLeader: func(id string) { order = append(order, "new:"+id) },
		OnStartedLeading: func(lc context.Context) {
			order = append(order, "started")
			// Watch lc (not stored to an outer var) so we can assert below
			// that lead cancels it before returning.
			go func() {
				<-lc.Done()
				close(leaderCtxDone)
			}()
		},
		OnStoppedLeading: func() { order = append(order, "stopped") },
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already done: lead must return promptly instead of blocking forever.

	lead(ctx, "id-1", cb)

	want := []string{"new:id-1", "started", "stopped"}
	if len(order) != len(want) {
		t.Fatalf("callback order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("callback order = %v, want %v", order, want)
		}
	}
	select {
	case <-leaderCtxDone:
	case <-time.After(time.Second):
		t.Error("the leaderCtx passed to OnStartedLeading should be cancelled once lead returns")
	}
}

func TestLead_nilCallbacksAreSafe(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	lead(ctx, "id-1", leader.Callbacks{}) // must not panic with every callback unset
}

func TestKeyFor_different(t *testing.T) {
	t.Parallel()
	foo, err := keyFor("foo")
	if err != nil {
		t.Fatalf("keyFor: %v", err)
	}
	bar, err := keyFor("bar")
	if err != nil {
		t.Fatalf("keyFor: %v", err)
	}
	if foo == bar {
		t.Fatal("want different keys for different names")
	}
}
