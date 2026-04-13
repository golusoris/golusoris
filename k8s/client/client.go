// Package client builds a *rest.Config + Kubernetes clientset with
// graceful fallback across deployment modes:
//
//   - In-cluster: standard SA token mounted by kubelet (production).
//   - Kubeconfig: KUBECONFIG env or ~/.kube/config (local dev / tooling).
//
// Cloud workload identity is handled transparently by the standard
// in-cluster path on each platform:
//
//   - GKE Workload Identity: the SA token is exchanged for a Google
//     identity by the metadata server; client-go sees a normal token
//     mount.
//   - EKS IRSA (IAM Roles for Service Accounts): the projected SA token
//   - AWS_ROLE_ARN env are picked up by aws-sdk-go-v2 when apps make
//     AWS API calls — k8s API access itself uses the regular in-cluster
//     token.
//   - Azure AD Workload Identity: same — the projected SA token is
//     consumed by the Azure SDK; k8s access stays standard.
//
// So this package is intentionally minimal: it doesn't reach into the
// cloud SDKs. Apps that need cloud-scoped credentials wire those SDKs
// directly (storage/, secrets/) and the token mount is already there.
//
// Config keys (env: APP_K8S_*):
//
//	k8s.kubeconfig    # explicit kubeconfig path (overrides KUBECONFIG env)
//	k8s.context       # named context within the kubeconfig
//	k8s.qps           # rest client QPS (default 20)
//	k8s.burst         # rest client burst (default 30)
package client

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/fx"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/k8s/podinfo"
)

// Options tunes the client.
type Options struct {
	Kubeconfig string  `koanf:"kubeconfig"`
	Context    string  `koanf:"context"`
	QPS        float32 `koanf:"qps"`
	Burst      int     `koanf:"burst"`
}

// DefaultOptions returns empty kubeconfig (auto-resolve) + 20 QPS / 30 burst
// (k8s controller default).
func DefaultOptions() Options {
	return Options{QPS: 20, Burst: 30}
}

// Source describes how the *rest.Config was resolved. Useful for logs +
// /status diagnostics.
type Source string

// Source values.
const (
	SourceInCluster  Source = "in-cluster"
	SourceKubeconfig Source = "kubeconfig"
)

// Resolved carries the rest.Config + the Source that built it.
type Resolved struct {
	Config *rest.Config
	Source Source
	// Path is the kubeconfig file path when Source == SourceKubeconfig.
	Path string
}

// New resolves a *rest.Config. Tries in-cluster first when running in a
// pod (per podinfo.IsInCluster); otherwise falls back to KUBECONFIG / opts.
// Kubeconfig / ~/.kube/config in that order.
func New(opts Options) (*Resolved, error) {
	opts = opts.withDefaults()

	if podinfo.IsInCluster() {
		cfg, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("k8s/client: in-cluster: %w", err)
		}
		applyRate(cfg, opts)
		return &Resolved{Config: cfg, Source: SourceInCluster}, nil
	}

	path := opts.Kubeconfig
	if path == "" {
		path = os.Getenv("KUBECONFIG")
	}
	if path == "" {
		home, _ := os.UserHomeDir()
		if home != "" {
			candidate := filepath.Join(home, ".kube", "config")
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
			}
		}
	}
	if path == "" {
		return nil, errors.New("k8s/client: no in-cluster mount + no kubeconfig found (set k8s.kubeconfig or KUBECONFIG)")
	}

	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: path},
		&clientcmd.ConfigOverrides{CurrentContext: opts.Context},
	)
	cfg, err := loader.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("k8s/client: load %s: %w", path, err)
	}
	applyRate(cfg, opts)
	return &Resolved{Config: cfg, Source: SourceKubeconfig, Path: path}, nil
}

func applyRate(cfg *rest.Config, opts Options) {
	cfg.QPS = opts.QPS
	cfg.Burst = opts.Burst
}

func (o Options) withDefaults() Options {
	d := DefaultOptions()
	if o.QPS == 0 {
		o.QPS = d.QPS
	}
	if o.Burst == 0 {
		o.Burst = d.Burst
	}
	return o
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("k8s", &opts); err != nil {
		return Options{}, fmt.Errorf("k8s/client: load options: %w", err)
	}
	return opts, nil
}

// Module provides *Resolved, *rest.Config, and a kubernetes.Interface
// (clientset) via fx. Apps can inject any of the three.
var Module = fx.Module("golusoris.k8s.client",
	fx.Provide(loadOptions),
	fx.Provide(New),
	fx.Provide(func(r *Resolved) *rest.Config { return r.Config }),
	fx.Provide(func(r *rest.Config) (kubernetes.Interface, error) {
		c, err := kubernetes.NewForConfig(r)
		if err != nil {
			return nil, fmt.Errorf("k8s/client: build clientset: %w", err)
		}
		return c, nil
	}),
)
