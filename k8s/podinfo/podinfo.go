// Package podinfo reads the Kubernetes downward API env vars (POD_NAME,
// POD_NAMESPACE, POD_IP, NODE_NAME, SERVICE_ACCOUNT, CONTAINER_NAME,
// CONTAINER_IMAGE) and exposes them as a typed [PodInfo] via fx.
//
// Apps + framework packages inject [PodInfo] when they need pod context for
// logs/metrics/traces. The k8s downward API yaml block looks like:
//
//	env:
//	- name: POD_NAME
//	  valueFrom: { fieldRef: { fieldPath: metadata.name } }
//	- name: POD_NAMESPACE
//	  valueFrom: { fieldRef: { fieldPath: metadata.namespace } }
//	- name: POD_IP
//	  valueFrom: { fieldRef: { fieldPath: status.podIP } }
//	- name: NODE_NAME
//	  valueFrom: { fieldRef: { fieldPath: spec.nodeName } }
//	- name: SERVICE_ACCOUNT
//	  valueFrom: { fieldRef: { fieldPath: spec.serviceAccountName } }
//
// `log/` and `otel/` read these env vars directly (they're below k8s/ in the
// dep graph). This package exists for higher-level packages that prefer
// dependency injection.
package podinfo

import (
	"os"

	"go.uber.org/fx"
)

// PodInfo describes the running pod as reported by the k8s downward API.
// Empty fields mean the corresponding env var was not set (running outside
// k8s, or the deployment didn't wire the downward API for that field).
type PodInfo struct {
	Name           string
	Namespace      string
	IP             string
	NodeName       string
	ServiceAccount string
	ContainerName  string
	ContainerImage string
}

// IsInCluster reports whether the process is running inside k8s. Detection:
// the standard service-account token file is mounted by kubelet on every pod.
func IsInCluster() bool {
	_, err := os.Stat("/var/run/secrets/kubernetes.io/serviceaccount/token")
	return err == nil
}

// New builds PodInfo from the current process environment.
func New() PodInfo {
	return PodInfo{
		Name:           os.Getenv("POD_NAME"),
		Namespace:      os.Getenv("POD_NAMESPACE"),
		IP:             os.Getenv("POD_IP"),
		NodeName:       os.Getenv("NODE_NAME"),
		ServiceAccount: os.Getenv("SERVICE_ACCOUNT"),
		ContainerName:  os.Getenv("CONTAINER_NAME"),
		ContainerImage: os.Getenv("CONTAINER_IMAGE"),
	}
}

// Module provides PodInfo via fx.
var Module = fx.Module("golusoris.k8s.podinfo",
	fx.Provide(New),
)
