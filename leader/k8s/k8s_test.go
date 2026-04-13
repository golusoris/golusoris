package k8s_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/golusoris/golusoris/leader"
	leaderk8s "github.com/golusoris/golusoris/leader/k8s"
)

func TestRunRequiresName(t *testing.T) {
	t.Parallel()
	err := leaderk8s.Run(context.Background(), fake.NewClientset(), leaderk8s.Options{Enabled: true}, leader.Callbacks{})
	if err == nil {
		t.Fatal("expected error for missing Name")
	}
	if !strings.Contains(err.Error(), "leader.name is required") {
		t.Errorf("err = %q", err)
	}
}

func TestDefaults(t *testing.T) {
	t.Parallel()
	o := leaderk8s.DefaultOptions()
	if o.Namespace != "default" {
		t.Errorf("Namespace = %q", o.Namespace)
	}
	if o.Lease.Duration != 15*time.Second {
		t.Errorf("Lease.Duration = %v", o.Lease.Duration)
	}
	if o.Lease.Renew != 10*time.Second {
		t.Errorf("Lease.Renew = %v", o.Lease.Renew)
	}
	if o.Lease.Retry != 2*time.Second {
		t.Errorf("Lease.Retry = %v", o.Lease.Retry)
	}
}
