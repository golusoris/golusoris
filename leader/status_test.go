// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package leader_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/leader"
)

func TestStatus_zeroValueIsNotLeader(t *testing.T) {
	t.Parallel()
	var s leader.Status
	require.False(t, s.IsLeader())
	require.False(t, leader.NewStatus().IsLeader())
}

func TestStatus_tracksTermUntilContextEnds(t *testing.T) {
	t.Parallel()
	s := leader.NewStatus()
	var sawLeader bool
	cb := s.Observe(leader.Callbacks{
		OnStartedLeading: func(context.Context) { sawLeader = s.IsLeader() },
	})
	ctx, cancel := context.WithCancel(context.Background())
	cb.OnStartedLeading(ctx)
	require.True(t, sawLeader, "IsLeader must be true inside OnStartedLeading")
	require.True(t, s.IsLeader())

	cancel()
	require.Eventually(t, func() bool { return !s.IsLeader() }, 5*time.Second, time.Millisecond)
}

func TestStatus_onStoppedLeadingClears(t *testing.T) {
	t.Parallel()
	s := leader.NewStatus()
	stopped := false
	cb := s.Observe(leader.Callbacks{OnStoppedLeading: func() { stopped = true }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cb.OnStartedLeading(ctx)
	cb.OnStoppedLeading()
	require.False(t, s.IsLeader())
	require.True(t, stopped)
}

func TestStatus_lateCancelKeepsNewerTerm(t *testing.T) {
	t.Parallel()
	s := leader.NewStatus()
	cb := s.Observe(leader.Callbacks{})
	first, cancelFirst := context.WithCancel(context.Background())
	cb.OnStartedLeading(first)
	second, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	cb.OnStartedLeading(second)

	cancelFirst()
	<-first.Done()
	time.Sleep(20 * time.Millisecond) // let the first term's AfterFunc run
	require.True(t, s.IsLeader(), "ending an old term must not clear the current one")
}

func TestStatus_observeKeepsOtherCallbacksAndNilSafe(t *testing.T) {
	t.Parallel()
	var newLeader string
	s := leader.NewStatus()
	cb := s.Observe(leader.Callbacks{OnNewLeader: func(id string) { newLeader = id }})
	cb.OnNewLeader("pod-1")
	require.Equal(t, "pod-1", newLeader)

	empty := s.Observe(leader.Callbacks{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	empty.OnStartedLeading(ctx)
	empty.OnStoppedLeading()
	require.Nil(t, empty.OnNewLeader)
}
