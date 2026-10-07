// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package twotier

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/rueidis"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/cache/memory"
	"github.com/golusoris/golusoris/realtime/pubsub"
)

// newPeer builds one replica: its own L1, the shared l2, and broadcaster b
// (nil disables invalidation).
func newPeer(t *testing.T, shared l2, b Broadcaster) *TwoTier {
	t.Helper()
	l1, err := memory.NewForTest(100, 0)
	require.NoError(t, err)
	opts := Options{L2: L2None, L1TTL: time.Hour}
	var options []Option
	if b != nil {
		opts.Invalidation = InvalidationOptions{Enabled: true}
		options = append(options, WithBroadcaster(b))
	}
	tt, err := New(l1, opts, slog.New(slog.DiscardHandler), options...)
	require.NoError(t, err)
	tt.l2 = shared
	return tt
}

// newPeers returns two listening replicas sharing one L2 and one in-process bus.
func newPeers(t *testing.T) (a, b *TwoTier, shared *stubL2) {
	t.Helper()
	shared = newStubL2()
	broadcaster, err := NewBusBroadcaster(pubsub.New(), "", nil)
	require.NoError(t, err)
	a, b = newPeer(t, shared, broadcaster), newPeer(t, shared, broadcaster)
	t.Cleanup(a.Listen())
	t.Cleanup(b.Listen())
	return a, b, shared
}

// cacheOn makes peer hold 1 for key "k" in its L1.
func cacheOn(t *testing.T, peer *TwoTier) {
	t.Helper()
	got, err := NewTyped[int](peer, "n").Get(context.Background(), "k", func(context.Context) (int, error) {
		return 1, nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, got)
}

func TestInvalidation_DeleteEvictsPeerL1(t *testing.T) {
	t.Parallel()
	a, b, _ := newPeers(t)
	cacheOn(t, b)
	require.NoError(t, NewTyped[int](a, "n").Delete(context.Background(), "k"))
	var loads atomic.Int32
	value, err := NewTyped[int](b, "n").Get(context.Background(), "k", countingLoader(&loads, 2))
	require.NoError(t, err)
	require.Equal(t, 2, value)
	require.EqualValues(t, 1, loads.Load(), "peer reloads after the remote delete")
}

// TestInvalidation_WithoutBroadcasterPeerStaysStale is the negative control:
// without invalidation the peer serves its old L1 value until L1 TTL.
func TestInvalidation_WithoutBroadcasterPeerStaysStale(t *testing.T) {
	t.Parallel()
	shared := newStubL2()
	a, b := newPeer(t, shared, nil), newPeer(t, shared, nil)
	cacheOn(t, b)
	require.NoError(t, NewTyped[int](a, "n").Delete(context.Background(), "k"))
	var loads atomic.Int32
	value, err := NewTyped[int](b, "n").Get(context.Background(), "k", countingLoader(&loads, 2))
	require.NoError(t, err)
	require.Equal(t, 1, value)
	require.Zero(t, loads.Load())
}

func TestInvalidation_SetMakesPeerReadNewValue(t *testing.T) {
	t.Parallel()
	a, b, _ := newPeers(t)
	cacheOn(t, b)
	require.NoError(t, NewTyped[int](a, "n").Set(context.Background(), "k", 7))
	var loads atomic.Int32
	value, err := NewTyped[int](b, "n").Get(context.Background(), "k", countingLoader(&loads, 0))
	require.NoError(t, err)
	require.Equal(t, 7, value, "peer reads the new value from L2")
	require.Zero(t, loads.Load())
}

func TestInvalidation_PrefixEvictsPeerMatchesOnly(t *testing.T) {
	t.Parallel()
	a, b, _ := newPeers(t)
	peerView := NewTyped[int](b, "n")
	ctx := context.Background()
	for _, k := range []string{"user/1", "other/1"} {
		require.NoError(t, peerView.Set(ctx, k, 1))
	}
	require.NoError(t, NewTyped[int](a, "n").InvalidatePrefix(ctx, "user/"))
	var loads atomic.Int32
	for _, k := range []string{"user/1", "other/1"} {
		_, err := peerView.Get(ctx, k, countingLoader(&loads, 0))
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, loads.Load(), "only user/1 left the peer's L1 and L2")
}

func TestInvalidation_IgnoresOwnNotices(t *testing.T) {
	t.Parallel()
	a, _, shared := newPeers(t)
	view := NewTyped[int](a, "n")
	require.NoError(t, view.Set(context.Background(), "k", 5))
	before := shared.getCalls.Load()
	var loads atomic.Int32
	value, err := view.Get(context.Background(), "k", countingLoader(&loads, 0))
	require.NoError(t, err)
	require.Equal(t, 5, value)
	require.Equal(t, before, shared.getCalls.Load(), "own notice must not evict the fresh L1 entry")
}

func TestInvalidation_RemoteNoticeFencesPeerLoad(t *testing.T) {
	t.Parallel()
	a, b, _ := newPeers(t)
	release, done := inFlightLoad(NewTyped[int](b, "n"))
	require.NoError(t, NewTyped[int](a, "n").Delete(context.Background(), "a"))
	close(release)
	require.NoError(t, <-done)
	var loads atomic.Int32
	_, err := NewTyped[int](b, "n").Get(context.Background(), "a", countingLoader(&loads, 9))
	require.NoError(t, err)
	require.EqualValues(t, 1, loads.Load(), "the fenced load did not populate L1")
}

func TestInvalidation_ListenStopEndsDelivery(t *testing.T) {
	t.Parallel()
	shared := newStubL2()
	broadcaster, err := NewBusBroadcaster(pubsub.New(), "", nil)
	require.NoError(t, err)
	a, b := newPeer(t, shared, broadcaster), newPeer(t, shared, broadcaster)
	b.Listen()()
	cacheOn(t, b)
	require.NoError(t, NewTyped[int](a, "n").Delete(context.Background(), "k"))
	cacheOn(t, b)
}

// failingBroadcaster fails every Broadcast, optionally after waiting for ctx.
type failingBroadcaster struct {
	wait        bool
	sawDeadline atomic.Bool
}

var errBroadcastDown = errors.New("bus down")

func (f *failingBroadcaster) Broadcast(ctx context.Context, _ Invalidation) error {
	_, ok := ctx.Deadline()
	f.sawDeadline.Store(ok)
	if f.wait {
		<-ctx.Done()
		return ctx.Err()
	}
	return errBroadcastDown
}

func (*failingBroadcaster) Subscribe(func(Invalidation)) func() { return func() {} }

func TestInvalidation_BroadcastFailureIsSurfaced(t *testing.T) {
	t.Parallel()
	shared := newStubL2()
	broadcaster := &failingBroadcaster{}
	view := NewTyped[int](newPeer(t, shared, broadcaster), "n")
	ctx := context.Background()
	mutations := []struct {
		name   string
		mutate func() error
	}{
		{"set", func() error { return view.Set(ctx, "k", 1) }},
		{"delete", func() error { return view.Delete(ctx, "k") }},
		{"prefix", func() error { return view.InvalidatePrefix(ctx, "k") }},
	}
	for _, m := range mutations {
		err := m.mutate()
		require.ErrorIs(t, err, ErrBroadcast, m.name)
		require.ErrorIs(t, err, errBroadcastDown, m.name)
	}
	require.True(t, broadcaster.sawDeadline.Load(), "broadcast runs under a deadline")
	_, found, err := shared.Get(ctx, "n:k")
	require.NoError(t, err)
	require.False(t, found, "local tiers changed before the broadcast failed")
}

func TestInvalidation_BroadcastTimeoutBounds(t *testing.T) {
	t.Parallel()
	l1, err := memory.NewForTest(100, 0)
	require.NoError(t, err)
	opts := Options{L2: L2None, L1TTL: time.Hour, Invalidation: InvalidationOptions{Enabled: true, Timeout: 10 * time.Millisecond}}
	tt, err := New(l1, opts, slog.New(slog.DiscardHandler), WithBroadcaster(&failingBroadcaster{wait: true}))
	require.NoError(t, err)
	err = NewTyped[int](tt, "n").Delete(context.Background(), "k")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestBusBroadcaster_DropsMalformedNotices(t *testing.T) {
	t.Parallel()
	bus := pubsub.New()
	var logs bytes.Buffer
	broadcaster, err := NewBusBroadcaster(bus, "topic", slog.New(slog.NewTextHandler(&logs, nil)))
	require.NoError(t, err)
	var received atomic.Int32
	stop := broadcaster.Subscribe(func(Invalidation) { received.Add(1) })
	defer stop()
	for _, data := range []any{
		[]byte("not json"),
		42,
		`{"origin":"","kind":"key","key":"k"}`,
		`{"origin":"o","kind":"flush","key":"k"}`,
	} {
		bus.Publish(context.Background(), pubsub.Message{Topic: "topic", Data: data})
	}
	bus.Publish(context.Background(), pubsub.Message{Topic: "topic", Data: `{"origin":"o","kind":"prefix","key":"p"}`})
	require.EqualValues(t, 1, received.Load())
	require.Equal(t, 4, bytes.Count(logs.Bytes(), []byte("drop malformed invalidation")))
}

// plainBus hides LocalBus's CheckedBus so Broadcast takes the fire-and-forget path.
type plainBus struct{ pubsub.Bus }

func TestBusBroadcaster_PlainBusIsFireAndForget(t *testing.T) {
	t.Parallel()
	local := pubsub.New()
	var received atomic.Int32
	defer local.Subscribe(DefaultInvalidationTopic, func(pubsub.Message) { received.Add(1) })()
	broadcaster, err := NewBusBroadcaster(plainBus{Bus: local}, "", nil)
	require.NoError(t, err)
	require.NoError(t, broadcaster.Broadcast(context.Background(), Invalidation{Origin: "o", Kind: InvalidationKey, Key: "k"}))
	require.EqualValues(t, 1, received.Load())
	require.NotNil(t, broadcaster.Subscribe(nil))
}

func TestNewBusBroadcaster_RejectsNilBus(t *testing.T) {
	t.Parallel()
	broadcaster, err := NewBusBroadcaster(nil, "", nil)
	require.ErrorIs(t, err, errInvalidDependency)
	require.Nil(t, broadcaster)
}

func TestApplyInvalidation_IgnoresUnknownKind(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	tt := newL1OnlyTwoTier(t)
	tt.logger = slog.New(slog.NewTextHandler(&logs, nil))
	require.NoError(t, NewTyped[int](tt, "n").Set(context.Background(), "k", 1))
	tt.applyInvalidation(Invalidation{Origin: "peer", Kind: "flush", Key: "n:k"})
	var loads atomic.Int32
	_, err := NewTyped[int](tt, "n").Get(context.Background(), "k", countingLoader(&loads, 0))
	require.NoError(t, err)
	require.Zero(t, loads.Load())
	require.Contains(t, logs.String(), "unknown invalidation kind")
}

func TestListen_NoBroadcasterIsNoop(t *testing.T) {
	t.Parallel()
	var nilCache *TwoTier
	nilCache.Listen()()
	newL1OnlyTwoTier(t).Listen()()
}

// typedNilClient is a non-nil interface holding a nil client.
type typedNilClient struct{ rueidis.Client }

func TestNew_ValidatesModesAndInvalidation(t *testing.T) {
	t.Parallel()
	l1, err := memory.NewForTest(10, 0)
	require.NoError(t, err)
	logger := slog.New(slog.DiscardHandler)
	broadcaster, err := NewBusBroadcaster(pubsub.New(), "", nil)
	require.NoError(t, err)
	enabled := InvalidationOptions{Enabled: true}
	tests := map[string]struct {
		opts    Options
		options []Option
	}{
		"unknown l2":                  {opts: Options{L2: "memcached"}},
		"redis without client":        {opts: Options{L2: L2Redis}, options: []Option{WithRedis((*typedNilClient)(nil))}},
		"none with redis client":      {opts: Options{L2: L2None}, options: []Option{WithRedis(typedNilClient{})}},
		"enabled without broadcaster": {opts: Options{L2: L2None, L1TTL: time.Minute, Invalidation: enabled}},
		"broadcaster without enabled": {opts: Options{L2: L2None, L1TTL: time.Minute}, options: []Option{WithBroadcaster(broadcaster)}},
		"enabled without L1 TTL":      {opts: Options{L2: L2None, Invalidation: enabled}, options: []Option{WithBroadcaster(broadcaster)}},
		"negative broadcast timeout":  {opts: Options{L2: L2None, L1TTL: time.Minute, Invalidation: InvalidationOptions{Enabled: true, Timeout: -time.Second}}, options: []Option{WithBroadcaster(broadcaster)}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tt, newErr := New(l1, tc.opts, logger, tc.options...)
			require.Error(t, newErr)
			require.Nil(t, tt)
		})
	}
	tt, err := New(l1, Options{L2: L2None, L1TTL: time.Minute, Invalidation: enabled}, logger, WithBroadcaster(broadcaster), nil)
	require.NoError(t, err)
	require.Equal(t, defaultBroadcastTimeout, tt.broadcastTimeout)
	require.NotEmpty(t, tt.origin)
}
