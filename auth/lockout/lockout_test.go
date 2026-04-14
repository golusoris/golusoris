package lockout_test

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/auth/lockout"
)

func TestService_LocksAfterMaxFails(t *testing.T) {
	t.Parallel()

	clk := clockwork.NewFakeClock()
	svc := lockout.New(lockout.NewMemoryStore(), clk, lockout.Options{
		MaxFails: 3,
		Window:   1 * time.Minute,
		Cooldown: 5 * time.Minute,
	})
	ctx := context.Background()

	require.NoError(t, svc.Check(ctx, "alice"))
	require.NoError(t, svc.Fail(ctx, "alice"))
	require.NoError(t, svc.Check(ctx, "alice"))
	require.NoError(t, svc.Fail(ctx, "alice"))
	require.NoError(t, svc.Fail(ctx, "alice"))

	require.Error(t, svc.Check(ctx, "alice"), "expected locked")

	// After cooldown, the lock expires.
	clk.Advance(6 * time.Minute)
	require.NoError(t, svc.Check(ctx, "alice"))
}

func TestService_ResetClearsCounter(t *testing.T) {
	t.Parallel()

	svc := lockout.New(lockout.NewMemoryStore(), nil, lockout.Options{MaxFails: 2, Window: time.Minute, Cooldown: time.Minute})
	ctx := context.Background()

	require.NoError(t, svc.Fail(ctx, "bob"))
	require.NoError(t, svc.Reset(ctx, "bob"))
	require.NoError(t, svc.Fail(ctx, "bob"))
	require.NoError(t, svc.Check(ctx, "bob"))
}

func TestService_WindowResetsCounter(t *testing.T) {
	t.Parallel()

	clk := clockwork.NewFakeClock()
	svc := lockout.New(lockout.NewMemoryStore(), clk, lockout.Options{
		MaxFails: 2,
		Window:   1 * time.Minute,
		Cooldown: 5 * time.Minute,
	})
	ctx := context.Background()

	require.NoError(t, svc.Fail(ctx, "carol"))
	clk.Advance(2 * time.Minute) // outside window
	require.NoError(t, svc.Fail(ctx, "carol"))
	require.NoError(t, svc.Check(ctx, "carol"), "counter should have reset")
}
