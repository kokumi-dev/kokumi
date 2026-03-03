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

## Install

```bash
kubectl apply -f https://github.com/kokumi-dev/kokumi/releases/download/0.5.1/install.yaml
```

This installs:
- The Kokumi CRDs (`Recipe`, `Preparation`, `Serving`)
- The controller manager in the `kokumi` namespace
- The API server and web UI in the `kokumi` namespace
- RBAC roles and bindings

> **Note:** The `Menu` resource is not yet implemented and has no active
> controller. It is planned for a future release.

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

## Pin a specific version

Replace `0.5.1` with any released version:

```bash
kubectl apply -f https://github.com/kokumi-dev/kokumi/releases/download/<VERSION>/install.yaml
```

All releases are listed at [github.com/kokumi-dev/kokumi/releases](https://github.com/kokumi-dev/kokumi/releases).

## Upgrade

```bash
kubectl apply -f https://github.com/kokumi-dev/kokumi/releases/download/<NEW_VERSION>/install.yaml
```

## Uninstall

```bash
kubectl delete -f https://github.com/kokumi-dev/kokumi/releases/download/0.5.1/install.yaml
```
