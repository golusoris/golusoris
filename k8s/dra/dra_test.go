// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package dra_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/golusoris/golusoris/k8s/dra"
)

const (
	driver = "gpu.vmafx.example.com"
	node   = "node-a"
)

func newClient(objs ...runtime.Object) *fake.Clientset {
	all := append([]runtime.Object{&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: node, UID: "node-a-uid"}}}, objs...)
	k := fake.NewClientset(all...)
	apiServerLike(k)
	return k
}

func newPublisher(t *testing.T, k *fake.Clientset) *dra.Publisher {
	t.Helper()
	p, err := dra.NewPublisher(k, dra.Options{Driver: driver, Node: node}, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	return p
}

func gpu(i int, backend string) dra.Device {
	return dra.Device{
		Name:     fmt.Sprintf("gpu-%d", i),
		Strings:  map[string]string{"backend": backend},
		Ints:     map[string]int64{"index": int64(i)},
		Bools:    map[string]bool{"vmafx.example.com/shared": false},
		Versions: map[string]string{"driverVersion": "550.54.15"},
		Capacity: map[string]int64{"memory": 24 << 30},
	}
}

func listSlices(t *testing.T, k *fake.Clientset) []resourceapi.ResourceSlice {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	l, err := k.ResourceV1().ResourceSlices().List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	return l.Items
}

func ownSlices(t *testing.T, k *fake.Clientset) []resourceapi.ResourceSlice {
	t.Helper()
	var out []resourceapi.ResourceSlice
	for _, s := range listSlices(t, k) {
		if s.Spec.Driver == driver && s.Spec.NodeName != nil && *s.Spec.NodeName == node {
			out = append(out, s)
		}
	}
	return out
}

func startCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestPublisher_publishesDevices(t *testing.T) {
	t.Parallel()
	k := newClient()
	p := newPublisher(t, k)
	require.NoError(t, p.Start(startCtx(t), []dra.Device{gpu(0, "cuda"), gpu(1, "vulkan")}))
	t.Cleanup(func() { _ = p.Stop(startCtx(t)) })

	require.Eventually(t, func() bool { return len(ownSlices(t, k)) == 1 }, 10*time.Second, 20*time.Millisecond)
	s := ownSlices(t, k)[0]
	require.Equal(t, node, s.Spec.Pool.Name)
	require.Equal(t, int64(1), s.Spec.Pool.ResourceSliceCount)
	require.Len(t, s.Spec.Devices, 2)
	d := s.Spec.Devices[0]
	require.Equal(t, "gpu-0", d.Name)
	require.Equal(t, "cuda", *d.Attributes["backend"].StringValue)
	require.Equal(t, int64(0), *d.Attributes["index"].IntValue)
	require.False(t, *d.Attributes["vmafx.example.com/shared"].BoolValue)
	require.Equal(t, "550.54.15", *d.Attributes["driverVersion"].VersionValue)
	require.True(t, d.Capacity["memory"].Value.Equal(resource.MustParse("24Gi")))
	require.Len(t, s.OwnerReferences, 1)
	require.Equal(t, "Node", s.OwnerReferences[0].Kind)
}

func TestPublisher_updateOnChange(t *testing.T) {
	t.Parallel()
	k := newClient()
	p := newPublisher(t, k)
	require.NoError(t, p.Start(startCtx(t), []dra.Device{gpu(0, "cuda")}))
	t.Cleanup(func() { _ = p.Stop(startCtx(t)) })
	require.Eventually(t, func() bool { return len(ownSlices(t, k)) == 1 }, 10*time.Second, 20*time.Millisecond)

	require.NoError(t, p.Update([]dra.Device{gpu(0, "cuda"), gpu(1, "cuda")}))
	require.Eventually(t, func() bool {
		s := ownSlices(t, k)
		return len(s) == 1 && len(s[0].Spec.Devices) == 2
	}, 10*time.Second, 20*time.Millisecond)

	require.NoError(t, p.Update(nil))
	require.Eventually(t, func() bool {
		s := ownSlices(t, k)
		return len(s) == 1 && len(s[0].Spec.Devices) == 0
	}, 10*time.Second, 20*time.Millisecond)
}

func TestPublisher_splitsAt128Devices(t *testing.T) {
	t.Parallel()
	k := newClient()
	p := newPublisher(t, k)
	devices := make([]dra.Device, 0, resourceapi.ResourceSliceMaxDevices+1)
	for i := range resourceapi.ResourceSliceMaxDevices + 1 {
		devices = append(devices, dra.Device{Name: fmt.Sprintf("d-%d", i)})
	}
	require.NoError(t, p.Start(startCtx(t), devices))
	t.Cleanup(func() { _ = p.Stop(startCtx(t)) })
	require.Eventually(t, func() bool { return len(ownSlices(t, k)) == 2 }, 10*time.Second, 20*time.Millisecond)
	counts := map[int]bool{}
	for _, s := range ownSlices(t, k) {
		counts[len(s.Spec.Devices)] = true
	}
	require.Equal(t, map[int]bool{resourceapi.ResourceSliceMaxDevices: true, 1: true}, counts)
}

func TestPublisher_stopDeletesOnlyOwnSlices(t *testing.T) {
	t.Parallel()
	foreign := &resourceapi.ResourceSlice{
		ObjectMeta: metav1.ObjectMeta{Name: "other"},
		Spec: resourceapi.ResourceSliceSpec{
			Driver:   "other.example.com",
			NodeName: ptrTo(node),
			Pool:     resourceapi.ResourcePool{Name: node, ResourceSliceCount: 1},
		},
	}
	k := newClient(foreign)
	p := newPublisher(t, k)
	require.NoError(t, p.Start(startCtx(t), []dra.Device{gpu(0, "cuda")}))
	require.Eventually(t, func() bool { return len(ownSlices(t, k)) == 1 }, 10*time.Second, 20*time.Millisecond)

	require.NoError(t, p.Stop(startCtx(t)))
	require.Empty(t, ownSlices(t, k))
	all := listSlices(t, k)
	require.Len(t, all, 1)
	require.Equal(t, "other", all[0].Name)

	require.NoError(t, p.Stop(startCtx(t)), "second Stop is a no-op")
	require.ErrorIs(t, p.Update(nil), dra.ErrNotStarted)
}

func TestPublisher_startTwiceFails(t *testing.T) {
	t.Parallel()
	p := newPublisher(t, newClient())
	require.NoError(t, p.Start(startCtx(t), nil))
	t.Cleanup(func() { _ = p.Stop(startCtx(t)) })
	require.ErrorIs(t, p.Start(startCtx(t), nil), dra.ErrStarted)
}

func TestPublisher_startHonoursCanceledContext(t *testing.T) {
	t.Parallel()
	p := newPublisher(t, newClient())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorContains(t, p.Start(ctx, nil), "dra: start aborted")
	require.ErrorIs(t, p.Update(nil), dra.ErrNotStarted)
}

func TestPublisher_updateBeforeStart(t *testing.T) {
	t.Parallel()
	p := newPublisher(t, newClient())
	require.ErrorIs(t, p.Update(nil), dra.ErrNotStarted)
	require.NoError(t, p.Stop(startCtx(t)))
}

func TestNewPublisher_validatesOptions(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.DiscardHandler)
	cases := map[string]dra.Options{
		"empty driver":     {Node: node},
		"driver too long":  {Driver: strings.Repeat("a", 60) + ".com", Node: node},
		"driver uppercase": {Driver: "GPU.example.com", Node: node},
		"empty node":       {Driver: driver},
		"bad pool":         {Driver: driver, Node: node, Pool: "Bad_Pool"},
	}
	for name, o := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := dra.NewPublisher(newClient(), o, log)
			require.Error(t, err)
		})
	}
	_, err := dra.NewPublisher(nil, dra.Options{Driver: driver, Node: node}, log)
	require.ErrorContains(t, err, "kubernetes client and logger are required")

	_, err = dra.NewPublisher(newClient(), dra.Options{Driver: strings.Repeat("a", 59) + ".com", Node: node}, log)
	require.NoError(t, err, "63-byte driver name is the inclusive limit")
}

func TestDevice_validation(t *testing.T) {
	t.Parallel()
	many := func(n int) map[string]int64 {
		m := make(map[string]int64, n)
		for i := range n {
			m[fmt.Sprintf("a%d", i)] = 1
		}
		return m
	}
	cases := []struct {
		name string
		dev  []dra.Device
		ok   bool
	}{
		{"minimal", []dra.Device{{Name: "d"}}, true},
		{"32 attributes", []dra.Device{{Name: "d", Ints: many(32)}}, true},
		{"33 attributes", []dra.Device{{Name: "d", Ints: many(32), Capacity: map[string]int64{"x": 1}}}, false},
		{"32-byte identifier", []dra.Device{{Name: "d", Ints: map[string]int64{strings.Repeat("i", 32): 1}}}, true},
		{"33-byte identifier", []dra.Device{{Name: "d", Ints: map[string]int64{strings.Repeat("i", 33): 1}}}, false},
		{"qualified name", []dra.Device{{Name: "d", Strings: map[string]string{"example.com/x": "y"}}}, true},
		{"bad domain", []dra.Device{{Name: "d", Strings: map[string]string{"Example_com/x": "y"}}}, false},
		{"non C identifier", []dra.Device{{Name: "d", Strings: map[string]string{"has-dash": "y"}}}, false},
		{"64-byte string", []dra.Device{{Name: "d", Strings: map[string]string{"s": strings.Repeat("v", 64)}}}, true},
		{"65-byte string", []dra.Device{{Name: "d", Strings: map[string]string{"s": strings.Repeat("v", 65)}}}, false},
		{"semver prerelease", []dra.Device{{Name: "d", Versions: map[string]string{"v": "1.2.3-rc.1+b5"}}}, true},
		{"not semver", []dra.Device{{Name: "d", Versions: map[string]string{"v": "1.2"}}}, false},
		{"duplicate across kinds", []dra.Device{{Name: "d", Strings: map[string]string{"x": "y"}, Ints: map[string]int64{"x": 1}}}, false},
		{"capacity clashes attribute", []dra.Device{{Name: "d", Ints: map[string]int64{"x": 1}, Capacity: map[string]int64{"x": 1}}}, false},
		{"negative capacity", []dra.Device{{Name: "d", Capacity: map[string]int64{"m": -1}}}, false},
		{"bad device name", []dra.Device{{Name: "GPU_0"}}, false},
		{"duplicate device", []dra.Device{{Name: "d"}, {Name: "d"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newPublisher(t, newClient())
			err := p.Update(tc.dev)
			if tc.ok {
				require.ErrorIs(t, err, dra.ErrNotStarted, "valid devices reach the started check")
				return
			}
			require.ErrorIs(t, err, dra.ErrInvalidDevice)
		})
	}
}

func ptrTo[T any](v T) *T { return &v }
