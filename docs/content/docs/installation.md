---
title: Installation
weight: 2
description: Deploy kokumi to any Kubernetes cluster.
---

## Requirements

| Dependency | Version |
|---|---|
| Kubernetes | ≥ 1.26 |

## Install

```bash
kubectl apply -f https://github.com/kokumi-dev/kokumi/releases/download/0.4.0/install.yaml
```

This installs:
- The kokumi CRDs (`Recipe`, `Preparation`, `Serving`, `Menu`)
- The controller manager in the `kokumi` namespace
- RBAC roles and bindings

## Verify

```bash
# CRDs registered
kubectl get crds | grep kokumi.dev

# Manager running
kubectl get pods -n kokumi

# Logs
kubectl logs -n kokumi deployment/kokumi-controller-manager -c manager -f
```

## Pin a specific version

Replace `0.4.0` with any released version:

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
kubectl delete -f https://github.com/kokumi-dev/kokumi/releases/download/0.4.0/install.yaml
```
