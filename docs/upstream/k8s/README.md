# k8s.io/client-go — v0.32.3 snapshot

Pinned: **v0.32.3**
Source: https://pkg.go.dev/k8s.io/client-go@v0.32.3

## In-cluster client

```go
import (
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/rest"
)

cfg, err := rest.InClusterConfig()
client, err := kubernetes.NewForConfig(cfg)
```

## Kubeconfig client

```go
import "k8s.io/client-go/tools/clientcmd"

cfg, err := clientcmd.BuildConfigFromFlags("", kubeConfigPath)
client, err := kubernetes.NewForConfig(cfg)
```

## Common operations

```go
// List pods
pods, err := client.CoreV1().Pods("namespace").List(ctx, metav1.ListOptions{})

// Get configmap
cm, err := client.CoreV1().ConfigMaps("namespace").Get(ctx, "name", metav1.GetOptions{})

// Create lease (leader election)
lease, err := client.CoordinationV1().Leases("namespace").Create(ctx, lease, metav1.CreateOptions{})

// Watch
watcher, err := client.CoreV1().Pods("namespace").Watch(ctx, metav1.ListOptions{})
for event := range watcher.ResultChan() {
    pod := event.Object.(*corev1.Pod)
}
```

## Leader election

```go
import (
    "k8s.io/client-go/tools/leaderelection"
    "k8s.io/client-go/tools/leaderelection/resourcelock"
)

lock := &resourcelock.LeaseLock{
    LeaseMeta:  metav1.ObjectMeta{Name: "my-lock", Namespace: "default"},
    Client:     client.CoordinationV1(),
    LockConfig: resourcelock.ResourceLockConfig{Identity: podName},
}

leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
    Lock:          lock,
    LeaseDuration: 15 * time.Second,
    RenewDeadline: 10 * time.Second,
    RetryPeriod:   2 * time.Second,
    Callbacks: leaderelection.LeaderCallbacks{
        OnStartedLeading: func(ctx context.Context) { /* ... */ },
        OnStoppedLeading: func() { /* ... */ },
        OnNewLeader:      func(identity string) { /* ... */ },
    },
})
```

## golusoris usage

- `k8s/client/` — `*kubernetes.Clientset` provided via fx; in-cluster + kubeconfig auto-detect.
- `leader/k8s/` — k8s Lease-based leader election implementing the `leader.Elector` interface.

## Links

- Changelog: https://github.com/kubernetes/client-go/blob/master/CHANGELOG.md
