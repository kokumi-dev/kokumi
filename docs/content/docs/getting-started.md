---
title: Getting Started
weight: 1
description: Install Kokumi and deploy your first Recipe in minutes.
---

## Prerequisites

- A Kubernetes cluster ≥ 1.26 with `kubectl` configured
- **Argo CD ≥ 3.3** installed in the cluster in the `argocd` namespace

> **Argo CD is required.** Kokumi delegates all runtime deployment to Argo CD.
> When a Serving is activated, Kokumi creates or updates an Argo CD
> `Application` that points to the immutable OCI artifact of the selected
> Preparation. Without Argo CD, no workloads will be deployed.

If you don't have Argo CD installed yet:

```bash
kubectl create namespace argocd
kubectl apply -n argocd --server-side --force-conflicts \
    -f https://raw.githubusercontent.com/argoproj/argo-cd/v3.3.0/manifests/install.yaml
```

## Install Kokumi

```bash
kubectl apply -f \
    https://github.com/kokumi-dev/kokumi/releases/download/0.5.1/install.yaml
```

Verify the manager is running:

```bash
kubectl get pods -n kokumi
# NAME                                READY   STATUS    RESTARTS   AGE
# kokumi-controller-manager-xxx       1/1     Running   0          30s
```

## Create your first Recipe

**Recipe is the only resource you create directly.** Preparations and Servings
are managed automatically by Kokumi.

A Recipe declares the source OCI artifact and any patches to apply. The source
image must contain a `manifest.yaml` file at its root with all Kubernetes resources.

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Recipe
metadata:
  name: external-secrets
spec:
  source:
    oci: oci://ghcr.io/kokumi-dev/external-secrets
    version: "0.1.0"

  patches:
    - target:
        kind: Deployment
        name: external-secrets-webhook
      set:
        .spec.replicas: "3"

  destination:
    oci: oci://kokumi-registry.kokumi.svc.cluster.local:5000/preparation/external-secrets
```

Apply it:

```bash
kubectl apply -f recipe.yaml
```

## Watch a Preparation being created

Kokumi automatically reconciles the Recipe and produces an immutable **Preparation**.
You never create Preparations manually — every Recipe change produces a new one
and the full history is retained indefinitely.

```bash
kubectl get preparations --watch
# NAME                            RECIPE             PHASE   CREATED   AGE
# external-secrets-d7ce0c46a686   external-secrets   Ready   5s        5s
```

## Activate with a Serving

A **Serving** points Argo CD at the selected Preparation's immutable OCI artifact.
There is exactly one Serving per Recipe, and it is **created and managed
automatically** — you never write a Serving manifest yourself.

Three ways to activate or change a Serving:

| Method | How |
|---|---|
| **Auto-deploy** | Set `spec.autoDeployLatest: true` on the Recipe — Kokumi updates the Serving on every new Preparation |
| **Label promotion** | Label a Preparation with `delivery.kokumi.dev/approve-deploy: "true"` |
| **UI** | Click **Promote** on any Preparation in the Kokumi UI |

Once activated, Kokumi creates a matching Argo CD `Application` in the `argocd`
namespace and Argo CD syncs the manifests into the cluster.

```bash
kubectl get servings
# NAME               RECIPE             PREPARATION                    PHASE    AGE
# external-secrets   external-secrets   external-secrets-d7ce0c46a686   Active   10s

kubectl get applications -n argocd
# NAME               SYNC STATUS   HEALTH STATUS
# external-secrets   Synced        Healthy
```

To roll back, promote any previous Preparation — no re-rendering required.

## Access the UI

Kokumi includes a web UI and API server deployed alongside the controller.
Port-forward the server service to access it locally:

```bash
kubectl port-forward -n kokumi svc/kokumi-server 8080:80
```

Then open [http://localhost:8080](http://localhost:8080) in your browser.

The UI lets you browse Recipes, Preparations, and Servings, promote a
Preparation to active with one click, and view Argo CD sync status in real time.

## Next steps

{{< cards >}}
  {{< card link="../installation" title="Installation" icon="download" subtitle="Version pinning and upgrade guide." >}}
  {{< card link="../architecture" title="Architecture" icon="cube-transparent" subtitle="How reconciliation works under the hood." >}}
{{< /cards >}}
