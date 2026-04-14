# deploy/argocd

[ArgoCD](https://argo-cd.readthedocs.io) manifests for deploying a golusoris-app via GitOps.

## Architecture

```
git repo
 └── apps/myapp/application.yaml   ← ArgoCD Application
 └── apps/myapp/values.yaml        ← app-specific values
 └── projects/platform.yaml        ← ArgoCD AppProject (scoping + RBAC)
```

## Install ArgoCD

```bash
kubectl create namespace argocd
kubectl apply -n argocd \
  -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
```

## Deploy golusoris-app

1. Copy [`appproject.yaml`](./appproject.yaml) into your GitOps repo.
2. Copy [`application.yaml`](./application.yaml) and adjust the `source.repoURL`, target namespace, and values.
3. Commit + push — ArgoCD syncs within its refresh interval (3 min default).

## Image updates (optional)

Install the [argocd-image-updater](https://argocd-image-updater.readthedocs.io/) controller and annotate the Application:

```yaml
metadata:
  annotations:
    argocd-image-updater.argoproj.io/image-list: app=ghcr.io/golusoris/app-myapp
    argocd-image-updater.argoproj.io/app.update-strategy: semver
    argocd-image-updater.argoproj.io/app.allow-tags: regexp:^v[0-9]+\.[0-9]+\.[0-9]+$
```
