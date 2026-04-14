# deploy/flux

[Flux CD](https://fluxcd.io) manifests for deploying a golusoris-app via GitOps.

## Architecture

```
git repo
 └── apps/myapp/release.yaml     ← HelmRelease
 └── apps/myapp/values.yaml      ← app-specific values
 └── infra/sources.yaml          ← HelmRepository pointing at chart source
```

## Install Flux

```bash
flux bootstrap github \
  --owner=<your-gh-user> \
  --repository=<gitops-repo> \
  --branch=main \
  --path=clusters/<env>
```

## Install golusoris-app chart

1. Copy [`helmrepository.yaml`](./helmrepository.yaml) into `infra/` in your GitOps repo.
2. Copy [`helmrelease.yaml`](./helmrelease.yaml) into `apps/myapp/` and adjust values.
3. Copy [`kustomization.yaml`](./kustomization.yaml) into the same directory.

Flux picks up changes on `git push`.

## Image automation (optional)

Use Flux's image-automation controllers to auto-bump Helm `values.image.tag` when a new release tag hits GHCR:

```bash
flux create image repository golusoris-app \
  --image=ghcr.io/golusoris/app-myapp \
  --interval=1m
flux create image policy golusoris-app \
  --image-ref=golusoris-app \
  --select-semver=">=0.1.0"
```
