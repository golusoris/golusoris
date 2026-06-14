package operator

import (
	"errors"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestBuildScheme(t *testing.T) {
	t.Parallel()
	called := false
	adder := func(*runtime.Scheme) error { called = true; return nil }
	scheme, err := buildScheme([]SchemeAdder{adder, nil}) // nil adder is skipped
	if err != nil {
		t.Fatalf("buildScheme: %v", err)
	}
	if !called {
		t.Error("adder was not invoked")
	}
	if len(scheme.AllKnownTypes()) == 0 {
		t.Error("client-go base types not registered")
	}
}

func TestBuildSchemeAdderError(t *testing.T) {
	t.Parallel()
	want := errors.New("boom")
	_, err := buildScheme([]SchemeAdder{func(*runtime.Scheme) error { return want }})
	if !errors.Is(err, want) {
		t.Fatalf("want wrapped %v, got %v", want, err)
	}
}

func TestManagerOptionsMapping(t *testing.T) {
	t.Parallel()
	o := Options{
		MetricsAddr:      ":9000",
		HealthProbeAddr:  ":9001",
		LeaderElection:   true,
		LeaderElectionID: "lock",
		GracefulShutdown: 5 * time.Second,
	}
	scheme := runtime.NewScheme()
	mo := o.managerOptions(scheme)
	if mo.Metrics.BindAddress != ":9000" {
		t.Errorf("metrics addr = %q, want :9000", mo.Metrics.BindAddress)
	}
	if mo.HealthProbeBindAddress != ":9001" {
		t.Errorf("health addr = %q, want :9001", mo.HealthProbeBindAddress)
	}
	if !mo.LeaderElection || mo.LeaderElectionID != "lock" {
		t.Error("leader election fields not mapped")
	}
	if mo.GracefulShutdownTimeout == nil || *mo.GracefulShutdownTimeout != 5*time.Second {
		t.Error("graceful shutdown timeout not mapped")
	}
	if mo.Scheme != scheme {
		t.Error("scheme not wired onto manager options")
	}
}

func TestDefaultOptions(t *testing.T) {
	t.Parallel()
	o := defaultOptions()
	if o.MetricsAddr == "" || o.HealthProbeAddr == "" {
		t.Error("default bind addresses must be set")
	}
	if o.GracefulShutdown <= 0 {
		t.Errorf("default graceful shutdown = %v, want > 0", o.GracefulShutdown)
	}
}
