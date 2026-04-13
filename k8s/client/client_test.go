package client_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/k8s/client"
)

const sampleKubeconfig = `apiVersion: v1
kind: Config
current-context: test
contexts:
- name: test
  context:
    cluster: test
    user: test
    namespace: default
clusters:
- name: test
  cluster:
    server: https://test.example:6443
    insecure-skip-tls-verify: true
users:
- name: test
  user:
    token: dummy-token
`

func TestNewLoadsExplicitKubeconfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(path, []byte(sampleKubeconfig), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	r, err := client.New(client.Options{Kubeconfig: path})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if r.Source != client.SourceKubeconfig {
		t.Errorf("Source = %q, want %q", r.Source, client.SourceKubeconfig)
	}
	if r.Path != path {
		t.Errorf("Path = %q, want %q", r.Path, path)
	}
	if r.Config.Host != "https://test.example:6443" {
		t.Errorf("Host = %q", r.Config.Host)
	}
	if r.Config.QPS != 20 || r.Config.Burst != 30 {
		t.Errorf("QPS/Burst = %v/%v", r.Config.QPS, r.Config.Burst)
	}
}

func TestNewMissingKubeconfigErrors(t *testing.T) {
	// t.Setenv forbids t.Parallel.
	t.Setenv("KUBECONFIG", "")
	t.Setenv("HOME", t.TempDir())

	_, err := client.New(client.Options{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no in-cluster mount") {
		t.Errorf("err = %q", err)
	}
}

func TestRateOverrides(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	_ = os.WriteFile(path, []byte(sampleKubeconfig), 0o600)

	r, err := client.New(client.Options{Kubeconfig: path, QPS: 100, Burst: 200})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if r.Config.QPS != 100 || r.Config.Burst != 200 {
		t.Errorf("QPS/Burst = %v/%v", r.Config.QPS, r.Config.Burst)
	}
}
