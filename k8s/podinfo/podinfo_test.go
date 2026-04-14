package podinfo_test

import (
	"testing"

	"github.com/golusoris/golusoris/k8s/podinfo"
)

func TestNewReadsEnv(t *testing.T) {
	t.Setenv("POD_NAME", "myapp-abc-123")
	t.Setenv("POD_NAMESPACE", "default")
	t.Setenv("POD_IP", "10.244.1.5")
	t.Setenv("NODE_NAME", "node-1")
	t.Setenv("SERVICE_ACCOUNT", "myapp")

	p := podinfo.New()
	if p.Name != "myapp-abc-123" {
		t.Errorf("Name = %q", p.Name)
	}
	if p.Namespace != "default" {
		t.Errorf("Namespace = %q", p.Namespace)
	}
	if p.IP != "10.244.1.5" {
		t.Errorf("IP = %q", p.IP)
	}
	if p.NodeName != "node-1" {
		t.Errorf("NodeName = %q", p.NodeName)
	}
	if p.ServiceAccount != "myapp" {
		t.Errorf("ServiceAccount = %q", p.ServiceAccount)
	}
}

func TestNewMissingEnvIsEmpty(t *testing.T) { //nolint:paralleltest // mutates global state
	// Make sure no leak from prior tests.
	for _, k := range []string{"POD_NAME", "POD_NAMESPACE", "POD_IP", "NODE_NAME", "SERVICE_ACCOUNT", "CONTAINER_NAME", "CONTAINER_IMAGE"} {
		t.Setenv(k, "")
	}
	p := podinfo.New()
	if p.Name != "" || p.Namespace != "" || p.IP != "" {
		t.Errorf("expected empty PodInfo, got %+v", p)
	}
}

func TestIsInCluster(t *testing.T) {
	t.Parallel()
	// We can't reliably set the SA token file in tests; just check the
	// helper doesn't panic and returns a bool.
	_ = podinfo.IsInCluster()
}
