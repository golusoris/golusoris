// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package dra

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestUpdate_identicalDevicesSkipController(t *testing.T) {
	t.Parallel()
	k := fake.NewClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n"}})
	p, err := NewPublisher(k, Options{Driver: "d.example.com", Node: "n"}, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	devices := []Device{{Name: "a", Ints: map[string]int64{"index": 0}}}
	require.NoError(t, p.Start(ctx, devices))
	t.Cleanup(func() { _ = p.Stop(ctx) })

	first := p.last
	require.NoError(t, p.Update([]Device{{Name: "a", Ints: map[string]int64{"index": 0}}}))
	require.Same(t, first, p.last, "identical devices must not reach the controller")

	require.NoError(t, p.Update([]Device{{Name: "a", Ints: map[string]int64{"index": 1}}}))
	require.NotSame(t, first, p.last)
}

func TestOptions_poolDefaultsToNode(t *testing.T) {
	t.Parallel()
	require.Equal(t, "n", Options{Node: "n"}.withDefaults().Pool)
	require.Equal(t, "p", Options{Node: "n", Pool: "p"}.withDefaults().Pool)
}
