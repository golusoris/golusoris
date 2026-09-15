// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package nri

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/containerd/nri/pkg/api"
	nrilog "github.com/containerd/nri/pkg/log"
	nristub "github.com/containerd/nri/pkg/stub"
)

// fakeStub is a hand-rolled [nristub.Stub] — no real containerd socket is
// ever opened by these tests.
type fakeStub struct{}

var _ nristub.Stub = (*fakeStub)(nil)

func (f *fakeStub) Run(context.Context) error   { return nil }
func (f *fakeStub) Start(context.Context) error { return nil }
func (f *fakeStub) Stop()                       {}
func (f *fakeStub) Wait()                       {}

func (f *fakeStub) UpdateContainers([]*api.ContainerUpdate) ([]*api.ContainerUpdate, error) {
	return nil, nil
}

func (f *fakeStub) RegistrationTimeout() time.Duration { return nristub.DefaultRegistrationTimeout }
func (f *fakeStub) RequestTimeout() time.Duration      { return nristub.DefaultRequestTimeout }
func (f *fakeStub) Logger() nrilog.Logger              { return nrilog.Get() }
func (f *fakeStub) RuntimeNRIVersion() string          { return "test" }
func (f *fakeStub) PluginNRIVersion() string           { return "test" }

// validHooks returns a [Hooks] with every field set to a no-op that never
// errors — a baseline for tests that only care about registration wiring.
func validHooks() Hooks {
	return Hooks{
		CreateContainer: func(context.Context, *api.PodSandbox, *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
			return nil, nil, nil
		},
		StartContainer: func(context.Context, *api.PodSandbox, *api.Container) error { return nil },
		StopContainer: func(context.Context, *api.PodSandbox, *api.Container) ([]*api.ContainerUpdate, error) {
			return nil, nil
		},
		RemovePodSandbox: func(context.Context, *api.PodSandbox) error { return nil },
	}
}

// unusedFactory fails the test if the stub factory is ever invoked — used by
// tests that expect newRegistration to reject its input before reaching it.
func unusedFactory(t *testing.T) stubFactory {
	t.Helper()
	return func(any, ...nristub.Option) (nristub.Stub, error) {
		t.Fatal("stub factory must not be called when validation fails")
		return nil, nil
	}
}

// --- registration -----------------------------------------------------

func TestNewRegistrationWiresNameAndIndex(t *testing.T) {
	t.Parallel()

	var gotPlugin any
	var gotOptsCount int
	factory := func(p any, opts ...nristub.Option) (nristub.Stub, error) {
		gotPlugin = p
		gotOptsCount = len(opts)
		return &fakeStub{}, nil
	}

	opts := Options{Name: "myplugin", Index: "05", HookTimeout: time.Second}
	reg, err := newRegistration(opts, validHooks(), factory)
	if err != nil {
		t.Fatalf("newRegistration: %v", err)
	}
	if reg.Stub == nil {
		t.Fatal("Registration.Stub is nil")
	}
	if _, ok := reg.Stub.(*fakeStub); !ok {
		t.Errorf("Registration.Stub = %T, want *fakeStub (the fake, not a real containerd connection)", reg.Stub)
	}
	if gotOptsCount != 2 {
		t.Errorf("stub options count = %d, want 2 (name + index)", gotOptsCount)
	}
	if _, ok := gotPlugin.(nristub.CreateContainerInterface); !ok {
		t.Error("plugin passed to the stub factory does not implement nristub.CreateContainerInterface")
	}
}

func TestNewRegistrationOmitsUnsetNameAndIndex(t *testing.T) {
	t.Parallel()

	var gotOptsCount int
	factory := func(_ any, opts ...nristub.Option) (nristub.Stub, error) {
		gotOptsCount = len(opts)
		return &fakeStub{}, nil
	}

	_, err := newRegistration(Options{HookTimeout: time.Second}, validHooks(), factory)
	if err != nil {
		t.Fatalf("newRegistration: %v", err)
	}
	if gotOptsCount != 0 {
		t.Errorf("stub options count = %d, want 0 (Name and Index both empty)", gotOptsCount)
	}
}

func TestNewRegistrationRejectsMissingHook(t *testing.T) {
	t.Parallel()

	base := validHooks()
	cases := map[string]Hooks{
		"CreateContainer": {
			StartContainer: base.StartContainer, StopContainer: base.StopContainer,
			RemovePodSandbox: base.RemovePodSandbox,
		},
		"StartContainer": {
			CreateContainer: base.CreateContainer, StopContainer: base.StopContainer,
			RemovePodSandbox: base.RemovePodSandbox,
		},
		"StopContainer": {
			CreateContainer: base.CreateContainer, StartContainer: base.StartContainer,
			RemovePodSandbox: base.RemovePodSandbox,
		},
		"RemovePodSandbox": {
			CreateContainer: base.CreateContainer, StartContainer: base.StartContainer,
			StopContainer: base.StopContainer,
		},
	}

	for name, hooks := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := newRegistration(defaultOptions(), hooks, unusedFactory(t))
			if err == nil {
				t.Fatalf("newRegistration with missing %s hook: want error, got nil", name)
			}
		})
	}
}

func TestNewRegistrationRejectsInvalidHookTimeout(t *testing.T) {
	t.Parallel()

	_, err := newRegistration(Options{HookTimeout: 0}, validHooks(), unusedFactory(t))
	if err == nil {
		t.Fatal("newRegistration with HookTimeout=0: want error, got nil")
	}
}

func TestNewDoesNotConnect(t *testing.T) {
	t.Parallel()

	// New drives the real nristub.New — verifying that, unlike Start/Run, it
	// performs no containerd I/O and is safe to call in a unit test.
	reg, err := New(Options{Name: "unit-test", Index: "00", HookTimeout: time.Second}, validHooks())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if reg.Stub == nil {
		t.Fatal("Registration.Stub is nil")
	}
}

func TestNewRegistrationRejectsNameWithoutIndex(t *testing.T) {
	t.Parallel()

	opts := Options{Name: "myplugin", HookTimeout: time.Second}
	if _, err := newRegistration(opts, validHooks(), unusedFactory(t)); err == nil {
		t.Fatal("newRegistration with Name set but Index empty: want error, got nil")
	}
}

func TestNewRegistrationRejectsIndexWithoutName(t *testing.T) {
	t.Parallel()

	opts := Options{Index: "00", HookTimeout: time.Second}
	if _, err := newRegistration(opts, validHooks(), unusedFactory(t)); err == nil {
		t.Fatal("newRegistration with Index set but Name empty: want error, got nil")
	}
}

// --- hook round-trip, error, timeout -----------------------------------

func TestPluginCreateContainerRoundTrip(t *testing.T) {
	t.Parallel()

	wantPod := &api.PodSandbox{Id: "pod-1"}
	wantCtr := &api.Container{Id: "ctr-1"}
	wantAdjust := &api.ContainerAdjustment{}
	wantUpdates := []*api.ContainerUpdate{{}}

	var gotPod *api.PodSandbox
	var gotCtr *api.Container
	p := &plugin{
		timeout: time.Second,
		hooks: Hooks{
			CreateContainer: func(_ context.Context, pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
				gotPod, gotCtr = pod, ctr
				return wantAdjust, wantUpdates, nil
			},
		},
	}

	adjust, updates, err := p.CreateContainer(context.Background(), wantPod, wantCtr)
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	if gotPod != wantPod || gotCtr != wantCtr {
		t.Error("pod/container were not forwarded to the hook unchanged")
	}
	if adjust != wantAdjust {
		t.Error("adjustment returned by the hook was not passed through")
	}
	if len(updates) != 1 || updates[0] != wantUpdates[0] {
		t.Error("updates returned by the hook were not passed through")
	}
}

func TestPluginStartContainerErrorPropagates(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("start container: boom")
	p := &plugin{
		timeout: time.Second,
		hooks: Hooks{
			StartContainer: func(context.Context, *api.PodSandbox, *api.Container) error {
				return wantErr
			},
		},
	}

	err := p.StartContainer(context.Background(), &api.PodSandbox{}, &api.Container{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("StartContainer error = %v, want wrapping %v", err, wantErr)
	}
}

func TestPluginStopContainerTimeout(t *testing.T) {
	t.Parallel()

	p := &plugin{
		timeout: 20 * time.Millisecond,
		hooks: Hooks{
			StopContainer: func(context.Context, *api.PodSandbox, *api.Container) ([]*api.ContainerUpdate, error) {
				// Deliberately ignores ctx — simulates a wedged hook that
				// does not cooperate with cancellation.
				time.Sleep(200 * time.Millisecond)
				return nil, nil
			},
		},
	}

	start := time.Now()
	_, err := p.StopContainer(context.Background(), &api.PodSandbox{}, &api.Container{})
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StopContainer error = %v, want wrapping context.DeadlineExceeded", err)
	}
	if elapsed >= 150*time.Millisecond {
		t.Fatalf("StopContainer took %s, want well under the wedged hook's 200ms sleep (bounded by a 20ms timeout)", elapsed)
	}
	if !strings.Contains(err.Error(), "StopContainer") {
		t.Errorf("error = %q, want it to name the hook (StopContainer)", err)
	}
	if strings.Contains(err.Error(), "cancel") {
		t.Errorf("error = %q, a plain timeout should not claim the caller cancelled", err)
	}
}

// TestPluginStopContainerCallerCancellation covers the caller-cancellation
// half of bounded's ctx.Done() branch: when the caller's own context is
// cancelled — not the per-call timeout expiring — the error must say so
// instead of claiming the hook "exceeded timeout" (which would be
// misleading: the hook was never given the chance to run that long).
func TestPluginStopContainerCallerCancellation(t *testing.T) {
	t.Parallel()

	p := &plugin{
		// A long timeout that would never itself fire during this test —
		// isolates the assertion to caller cancellation.
		timeout: 10 * time.Second,
		hooks: Hooks{
			StopContainer: func(context.Context, *api.PodSandbox, *api.Container) ([]*api.ContainerUpdate, error) {
				time.Sleep(200 * time.Millisecond)
				return nil, nil
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := p.StopContainer(ctx, &api.PodSandbox{}, &api.Container{})
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("StopContainer error = %v, want wrapping context.Canceled", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StopContainer error = %v, must not also present as a DeadlineExceeded timeout", err)
	}
	if strings.Contains(err.Error(), "timeout") {
		t.Errorf("error = %q, should not claim a timeout when the caller cancelled", err)
	}
	if elapsed >= 150*time.Millisecond {
		t.Fatalf("StopContainer took %s, want well under the wedged hook's 200ms sleep (bounded by caller cancellation)", elapsed)
	}
}

// TestPluginHookPanicRecovers is the regression test for bounded running a
// hook in a bare goroutine with no recover: before the fix, any one of these
// panics would crash the whole plugin process (an NRI plugin runs as its own
// OS process, so there is no outer recover to catch it) instead of just
// failing the one containerd request that triggered it.
func TestPluginHookPanicRecovers(t *testing.T) {
	t.Parallel()

	t.Run("CreateContainer", func(t *testing.T) {
		t.Parallel()
		p := &plugin{
			timeout: time.Second,
			hooks: Hooks{
				CreateContainer: func(context.Context, *api.PodSandbox, *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
					panic("boom: create")
				},
			},
		}
		_, _, err := p.CreateContainer(context.Background(), &api.PodSandbox{}, &api.Container{})
		assertPanicRecovered(t, err, "CreateContainer", "boom: create")
	})

	t.Run("StartContainer", func(t *testing.T) {
		t.Parallel()
		p := &plugin{
			timeout: time.Second,
			hooks: Hooks{
				StartContainer: func(context.Context, *api.PodSandbox, *api.Container) error {
					panic("boom: start")
				},
			},
		}
		err := p.StartContainer(context.Background(), &api.PodSandbox{}, &api.Container{})
		assertPanicRecovered(t, err, "StartContainer", "boom: start")
	})

	t.Run("StopContainer", func(t *testing.T) {
		t.Parallel()
		p := &plugin{
			timeout: time.Second,
			hooks: Hooks{
				StopContainer: func(context.Context, *api.PodSandbox, *api.Container) ([]*api.ContainerUpdate, error) {
					panic("boom: stop")
				},
			},
		}
		_, err := p.StopContainer(context.Background(), &api.PodSandbox{}, &api.Container{})
		assertPanicRecovered(t, err, "StopContainer", "boom: stop")
	})

	t.Run("RemovePodSandbox", func(t *testing.T) {
		t.Parallel()
		p := &plugin{
			timeout: time.Second,
			hooks: Hooks{
				RemovePodSandbox: func(context.Context, *api.PodSandbox) error {
					panic("boom: remove")
				},
			},
		}
		err := p.RemovePodSandbox(context.Background(), &api.PodSandbox{})
		assertPanicRecovered(t, err, "RemovePodSandbox", "boom: remove")
	})
}

// assertPanicRecovered checks that a hook panic came back as an error naming
// both the hook and the panic value, rather than crashing the test binary
// (which is exactly what would happen without bounded's recover — there
// would be no error to assert on at all).
func assertPanicRecovered(t *testing.T, err error, hookName, panicMsg string) {
	t.Helper()
	if err == nil {
		t.Fatal("want an error recovered from the panic, got nil")
	}
	if !strings.Contains(err.Error(), hookName) {
		t.Errorf("error = %q, want it to name the hook (%s)", err, hookName)
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Errorf("error = %q, want it to say the hook panicked", err)
	}
	if !strings.Contains(err.Error(), panicMsg) {
		t.Errorf("error = %q, want it to include the panic value (%s)", err, panicMsg)
	}
}

// --- RemovePodSandbox ---------------------------------------------------

// TestPluginRemovePodSandboxRoundTrip mirrors the other three hooks' round-
// trip tests — RemovePodSandbox had none, leaving it at 0% coverage.
func TestPluginRemovePodSandboxRoundTrip(t *testing.T) {
	t.Parallel()

	wantPod := &api.PodSandbox{Id: "pod-1"}

	var gotPod *api.PodSandbox
	p := &plugin{
		timeout: time.Second,
		hooks: Hooks{
			RemovePodSandbox: func(_ context.Context, pod *api.PodSandbox) error {
				gotPod = pod
				return nil
			},
		},
	}

	if err := p.RemovePodSandbox(context.Background(), wantPod); err != nil {
		t.Fatalf("RemovePodSandbox: %v", err)
	}
	if gotPod != wantPod {
		t.Error("pod was not forwarded to the hook unchanged")
	}
}

// TestPluginRemovePodSandboxErrorPropagates covers RemovePodSandbox's error
// path, matching TestPluginStartContainerErrorPropagates.
func TestPluginRemovePodSandboxErrorPropagates(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("remove pod sandbox: boom")
	p := &plugin{
		timeout: time.Second,
		hooks: Hooks{
			RemovePodSandbox: func(context.Context, *api.PodSandbox) error {
				return wantErr
			},
		},
	}

	err := p.RemovePodSandbox(context.Background(), &api.PodSandbox{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RemovePodSandbox error = %v, want wrapping %v", err, wantErr)
	}
}
