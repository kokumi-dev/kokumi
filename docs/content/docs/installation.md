---
title: Installation
weight: 2
description: Deploy Kokumi to any Kubernetes cluster.
---

## Requirements

| Dependency | Version |
|---|---|
| Kubernetes | ≥ 1.26 |
| Argo CD | ≥ 3.3 |

Argo CD must be installed **before** Kokumi is deployed. The Serving controller
creates and updates Argo CD `Application` resources to point at the immutable
OCI artifacts produced by Preparations. Without Argo CD, Servings will fail
and no workloads will be deployed.

See [Getting Started — Argo CD registry credentials](../getting-started/#argo-cd-registry-credentials)
for the required repo-creds secret to let Argo CD reach the in-cluster registry.

## Install

<!-- x-release-please-start-version -->
```bash
kubectl apply -f \
    https://github.com/kokumi-dev/kokumi/releases/download/0.13.0/install.yaml
```
<!-- x-release-please-end -->

This installs:
- The Kokumi CRDs (`Menu`, `Recipe`, `Order`, `Preparation`, `Serving`)
- The controller manager in the `kokumi` namespace
- The API server and web UI in the `kokumi` namespace
- RBAC roles and bindings

> **Model:** `Menu` provides the reusable template, `Recipe` carries rendering
> options (for example Helm settings), and `Order` is the parameterized request
> that executes delivery and produces `Preparation` artifacts. `Order` does not
> require `Menu`: standalone Order-defined intent is supported now and intended
> to remain supported.

## Verify

```bash
# CRDs registered
kubectl get crds | grep kokumi.dev

# Manager and server running
kubectl get pods -n kokumi

# Controller logs
kubectl logs -n kokumi deployment/kokumi-controller-manager -c manager -f
```

## Access the UI

```bash
kubectl port-forward -n kokumi svc/kokumi-server 8080:80
```

Open [http://localhost:8080](http://localhost:8080) in your browser.

### Logging in

The UI requires authentication. The default credentials are:

| Username | Password |
| -------- | -------- |
| `admin`  | `admin`  |

Change the password before any non-development use. Generate a new bcrypt hash
with `htpasswd` and patch the `kokumi-server-auth` Secret:

```bash
kubectl -n kokumi patch secret kokumi-server-auth \
  --type merge \
  -p "{\"stringData\":{\"password-hash\":\"$(htpasswd -nbB admin 'your-new-password' | cut -d: -f2)\"}}"
```

Then restart the server so it picks up the change:

```bash
kubectl -n kokumi rollout restart deployment/kokumi-server
```

> You can also change the username from the default `admin` by setting
> `stringData.username` in the same Secret.

## Pin a specific version

<!-- x-release-please-start-version -->
Replace `0.13.0` with any released version:
<!-- x-release-please-end -->

```bash
kubectl apply -f \
    https://github.com/kokumi-dev/kokumi/releases/download/<VERSION>/install.yaml
```

All releases are listed at [github.com/kokumi-dev/kokumi/releases](https://github.com/kokumi-dev/kokumi/releases).

## Upgrade

```bash
kubectl apply -f \
    https://github.com/kokumi-dev/kokumi/releases/download/<NEW_VERSION>/install.yaml
```

## Uninstall

<!-- x-release-please-start-version -->
```bash
kubectl delete -f \
    https://github.com/kokumi-dev/kokumi/releases/download/0.13.0/install.yaml
```
<!-- x-release-please-end -->
