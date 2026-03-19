---
title: Getting Started
weight: 1
description: Install Kokumi and deploy your first Order in minutes.
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

<!-- x-release-please-start-version -->
```bash
kubectl apply -f \
    https://github.com/kokumi-dev/kokumi/releases/download/0.8.0/install.yaml
```
<!-- x-release-please-end -->

Verify the manager is running:

```bash
kubectl get pods -n kokumi
# NAME                                READY   STATUS    RESTARTS   AGE
# kokumi-controller-manager-xxx       1/1     Running   0          30s
```

## Create your first Order

An **Order** is the concrete delivery request.

An Order does not require a Menu. It can fully define the intent of a single
component on its own. This standalone Order model is a first-class mode and will
always be supported.

Alternatively, an Order can reference a **Menu** to inherit source, render
configuration, and base defaults while only supplying permitted overrides. See
[Create a Menu](#create-a-menu) below for that workflow.

Kokumi supports two source types:

- **Pre-rendered manifest bundle** — an OCI artifact containing a `manifest.yaml`
  at its root (no `spec.render` needed).
- **Helm chart in OCI format** — a standard Helm chart pushed to an OCI registry;
  add `spec.render.helm` to control rendering.

#### Example: pre-rendered manifest bundle

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Order
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

#### Example: Helm chart in OCI format

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Order
metadata:
  name: podinfo
spec:
  source:
    oci: oci://ghcr.io/stefanprodan/charts/podinfo
    version: "6.10.2"

  render:
    helm:
      namespace: default
      values:
        ui:
          color: "#EF6461"
          message: "Hello from Kokumi"
          logo: "https://kokumi.dev/images/logo.png"

  patches:
    - target:
        kind: Deployment
        name: podinfo
      set:
        .spec.replicas: "2"

  destination:
    oci: oci://kokumi-registry.kokumi.svc.cluster.local:5000/preparation/podinfo

  autoDeploy: false
```

Apply it:

```bash
kubectl apply -f order.yaml
```

## Watch a Preparation being created

Kokumi automatically reconciles the Order and produces an immutable **Preparation**.
You never create Preparations manually — every Order change produces a new one
and the full history is retained indefinitely.

```bash
kubectl get preparations --watch
# NAME                            ORDER              PHASE   CREATED   AGE
# external-secrets-d7ce0c46a686   external-secrets   Ready   5s        5s
```

## Activate with a Serving

A **Serving** points Argo CD at the selected Preparation's immutable OCI artifact.
There is exactly one Serving per Order, and it is **created and managed
automatically** — you never write a Serving manifest yourself.

Three ways to activate or change a Serving:

| Method | How |
|---|---|
| **Auto-deploy** | Set `spec.autoDeploy: true` on the Order — Kokumi updates the Serving on every new Preparation |
| **Label promotion** | Label a Preparation with `delivery.kokumi.dev/approve-deploy: "true"` |
| **UI** | Click **Promote** on any Preparation in the Kokumi UI |

Once activated, Kokumi creates a matching Argo CD `Application` in the `argocd`
namespace and Argo CD syncs the manifests into the cluster.

```bash
kubectl get servings
# NAME               ORDER              PREPARATION                    PHASE    AGE
# external-secrets   external-secrets   external-secrets-d7ce0c46a686   Active   10s

kubectl get applications -n argocd
# NAME               SYNC STATUS   HEALTH STATUS
# external-secrets   Synced        Healthy
```

To roll back, promote any previous Preparation — no re-rendering required.

## Create a Menu

A **Menu** is a cluster-scoped, reusable template that pins source, version, and
render type. It defines base values and patches plus an **override policy**
controlling what consumers may customise.

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Menu
metadata:
  name: podinfo
spec:
  source:
    oci: oci://ghcr.io/stefanprodan/charts/podinfo
    version: "6.10.2"

  render:
    helm:
      namespace: default
      values:
        ui:
          color: "#EF6461"
          logo: "https://kokumi.dev/images/logo.png"

  overrides:
    values:
      policy: Restricted
      allowed:
        - "ui.message"
        - "replicaCount"
    patches:
      policy: None

  defaults:
    autoDeploy: false

```

This Menu:

- Pins the podinfo Helm chart at version `6.10.2`
- Always renders with the Kokumi logo as a base value
- Allows consumers to set only `ui.message`, `ui.color`, and `replicaCount`
- Forbids any patches

Apply it:

```bash
kubectl apply -f menu.yaml
```

### Order from a Menu

Create an Order that references the Menu instead of specifying a source directly:

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Order
metadata:
  name: podinfo-from-menu
spec:
  menuRef:
    name: podinfo

  render:
    helm:
      values:
        ui:
          message: "Ordered from Menu"

  destination:
    oci: oci://kokumi-registry.kokumi.svc.cluster.local:5000/preparation/podinfo-from-menu

  autoDeploy: false

```

The Order inherits the source, version, and base values from the Menu. Only the
allowed override keys are set. Kokumi validates the overrides against the Menu's
policy during reconciliation — any disallowed key causes the Order to fail with
a clear status message.

## Access the UI

Kokumi includes a web UI and API server deployed alongside the controller.
Port-forward the server service to access it locally:

```bash
kubectl port-forward -n kokumi svc/kokumi-server 8080:80
```

Then open [http://localhost:8080](http://localhost:8080) in your browser.

The UI lets you browse Menus, Orders, Preparations, and Servings, create
Orders from a Menu with one click, promote a Preparation to active, and view
Argo CD sync status in real time.

## Next steps

{{< cards >}}
  {{< card link="../installation" title="Installation" icon="download" subtitle="Version pinning and upgrade guide." >}}
  {{< card link="../architecture" title="Architecture" icon="cube-transparent" subtitle="How reconciliation works under the hood." >}}
{{< /cards >}}
