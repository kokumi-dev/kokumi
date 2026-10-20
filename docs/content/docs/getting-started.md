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
    -f https://raw.githubusercontent.com/argoproj/argo-cd/v3.5.1/manifests/install.yaml
```

### Argo CD registry credentials

The in-cluster registry currently serves plain HTTP. Until proper TLS support
is added, register it with Argo CD using `insecureOCIForceHttp=true`:

```bash
kubectl -n argocd create secret generic manifests-registry-creds \
    --from-literal=type=oci \
    --from-literal=url=oci://kokumi-registry.kokumi.svc.cluster.local:5000 \
    --from-literal=insecureOCIForceHttp=true \
    --dry-run=client -o yaml | \
kubectl label -f - argocd.argoproj.io/secret-type=repo-creds --local -o yaml | \
kubectl apply -f -
```

## Install Kokumi

<!-- x-release-please-start-version -->
```bash
kubectl apply -f \
    https://github.com/kokumi-dev/kokumi/releases/download/0.17.0/install.yaml
```
<!-- x-release-please-end -->

Verify the manager is running:

```bash
kubectl get pods -n kokumi
# NAME                                READY   STATUS    RESTARTS   AGE
# kokumi-controller-manager-xxx       1/1     Running   0          30s
```

## Create your first Product and Order

A **Product** is the complete definition of an application: the source OCI
artifact, how it is rendered, and its base values and patches.

An **Order** is a variant of exactly one Product, for example one environment.
It only declares what differs: Helm values, patches, where the rendered
artifact is pushed, and how it is promoted. Every application has at least one
Order; a Product on its own is never rendered or deployed.

Kokumi supports two source types:

- **Pre-rendered manifest bundle** — an OCI artifact containing `*.yaml` files
  at its root (no `spec.render` needed).
- **Helm chart in OCI format** — a standard Helm chart pushed to an OCI registry;
  add `spec.render.helm` to the Product to control rendering.

#### Example: pre-rendered manifest bundle

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Product
metadata:
  name: external-secrets
spec:
  source:
    url: oci://ghcr.io/kokumi-dev/testdata/external-secrets
    version: "0.1.0"

  # Orders may patch anything. Without overrides they may change nothing.
  overrides:
    patches:
      policy: All
---
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Order
metadata:
  name: external-secrets
spec:
  productRef:
    name: external-secrets

  patches:
    - target:
        kind: Deployment
        name: external-secrets-webhook
      set:
        .spec.replicas: "3"

  destination:
    url: oci://kokumi-registry.kokumi.svc.cluster.local:5000/preparation/external-secrets
```

#### Example: Helm chart in OCI format

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Product
metadata:
  name: podinfo
spec:
  source:
    url: oci://ghcr.io/stefanprodan/charts/podinfo
    version: "6.14.1"

  render:
    helm:
      namespace: default
      values:
        ui:
          color: "#EF6461"
          logo: "https://kokumi.dev/images/logo.png"

  overrides:
    values:
      policy: All
    patches:
      policy: All
---
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Order
metadata:
  name: podinfo
spec:
  productRef:
    name: podinfo

  values:
    ui:
      message: "Hello from Kokumi"

  patches:
    - target:
        kind: Deployment
        name: podinfo
      set:
        .spec.replicas: "2"

  destination:
    url: oci://kokumi-registry.kokumi.svc.cluster.local:5000/preparation/podinfo

  promotion:
    mode: Manual
```

The Order's `values` are deep-merged over the Product's
`spec.render.helm.values`, and its patches over the Product's patches. The
Order's settings win. An Order may only change what the Product's `overrides`
allow; both Products above allow everything (see
[Restrict what Orders may change](#restrict-what-orders-may-change)).

Apply it:

```bash
kubectl apply -f podinfo.yaml
```

### Add another variant

More Orders of the same Product are its other variants, for example a
production environment that runs more replicas, deploys a change only after
`podinfo` ran it healthily for an hour, and needs approvals:

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Order
metadata:
  name: podinfo-prod
spec:
  productRef:
    name: podinfo

  values:
    replicaCount: 3
    ui:
      message: "Hello from production"

  promotion:
    mode: Manual
    after:
      - order: podinfo
        soakSeconds: 3600
    approvals:
      requiredApprovals: 2
      allowedGroups:
        - release-approvers
```

The source version is set only on the Product. Changing it renders a new
Preparation for every Order of the Product, and each Order promotes its own
Preparation on its own terms: once `podinfo` is promoted, `podinfo-prod` waits
until `podinfo` ran the change healthily for an hour, and for its approvals.
Nothing is copied from `podinfo` to `podinfo-prod`; see
[Promotion Pipelines](../user-guide/promotion-pipelines/). The `productRef` of
an Order cannot be changed after creation.

## Watch a Preparation being created

Kokumi automatically reconciles the Order and produces an immutable **Preparation**.
You never create Preparations manually. Every Order change produces a new one
and the full history is retained indefinitely.

```bash
kubectl get preparations --watch
# NAME                            ORDER              READY   REASON   CREATED   AGE
# external-secrets-d7ce0c46a686   external-secrets   True    Ready    5s        5s
```

## Activate with a Serving

A **Serving** points Argo CD at the selected Preparation's immutable OCI artifact.
There is exactly one Serving per Order, and it is **created and managed
automatically** — you never write a Serving manifest yourself.

Two ways to activate or change a Serving:

| Method | How |
|---|---|
| **Auto-deploy** | Set `spec.promotion.mode: Automatic` on the Order. Kokumi deploys every new Preparation once it is ready and passes its gates |
| **UI** | Set `spec.promotion.mode: Manual` and click **Promote** on any Preparation in the Kokumi UI |

Both modes hold a Preparation until it passes its gates: its upstream stages
(`spec.promotion.after`, see [Promotion Pipelines](../user-guide/promotion-pipelines/)),
its promotion checks, and its approvals. To require reviews, add
`spec.promotion.approvals` to the Order. Reviewers vote in the UI; see
[Approvals](../architecture/#approvals).

Once activated, Kokumi creates a matching Argo CD `Application` in the `argocd`
namespace and Argo CD syncs the manifests into the cluster.

> **Opt-in update protection.** Kokumi will only update an Argo CD
> `Application` it did not create if the `Application` is annotated with
> `delivery.kokumi.dev/allowed-order: "<order-name>"` matching the Order
> that owns the Serving. When Kokumi creates the `Application` itself, this
> annotation is added automatically. If a pre-existing `Application` is
> missing the annotation (or has a different value), Kokumi refuses to
> modify it and surfaces the failure on the Serving's status condition with
> the message `Cannot update Argo CD Application, opt-in annotation must
> exist`. Deleting the Serving deletes its `Application` only if it carries
> the matching annotation; any other `Application` is kept.

```bash
kubectl get servings
# NAME               ORDER              TARGET                          READY   REASON     APPROVED              VERIFIED             AGE
# external-secrets   external-secrets   external-secrets-d7ce0c46a686   True    Deployed   ApprovalNotRequired   SignaturesVerified   10s

kubectl get applications -n argocd
# NAME               SYNC STATUS   HEALTH STATUS
# external-secrets   Synced        Healthy
```

To roll back, promote any previous Preparation. No re-rendering required.

## Restrict what Orders may change

A Product decides what its Orders may change with `spec.overrides`, for
example when a platform team owns the Product and application teams own its
Orders. Helm values and patches each have a policy:

| `policy` | Orders may set |
|---|---|
| `None` (default) | nothing |
| `Listed` | only what is listed in `paths` (values) or `targets` (patches) |
| `All` | anything |

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Product
metadata:
  name: podinfo
spec:
  source:
    url: oci://ghcr.io/stefanprodan/charts/podinfo
    version: "6.14.1"

  render:
    helm:
      values:
        ui:
          color: "#EF6461"

  overrides:
    values:
      policy: Listed
      paths:
        - ui.message
        - replicaCount
    patches:
      policy: Listed
      targets:
        - target:
            kind: Deployment
            name: podinfo
          paths:
            - .spec.replicas
```

Here Orders may only set the listed Helm value paths and patch the listed
targets and paths; the patch policy applies to edits made in the UI too. A
target without a namespace matches the resource in any namespace. An Order
that sets anything else fails with a `ProcessingFailed` condition that names
the offending value or patch.

## Access the UI

Kokumi includes a web UI and API server deployed alongside the controller.
Port-forward the server service to access it locally:

```bash
kubectl port-forward -n kokumi svc/kokumi-server 8080:80
```

Then open [http://localhost:8080](http://localhost:8080) in your browser.

The UI requires authentication. The default credentials are:

| Username | Password |
| -------- | -------- |
| `admin`  | `admin`  |

Change the password before any non-development use. See the
[Operator Guide — Authentication](../operator-guide/authentication/) for how to
rotate the credentials Secret, manage the token signing key
(`kokumi-server-tokens`), and configure the admin account via the `Kitchen`
resource. The server picks up changes to the Secret and the `Kitchen` resource
automatically; no restart is needed.

The UI lets you browse Products and their Orders, Preparations, and Servings,
create an Order of a Product with one click, preview what a Product or Order
renders, promote a Preparation to active, and view Argo CD sync status in real
time.

## Using a private registry

If your source lives in a private registry, store the credentials in a
`kubernetes.io/dockerconfigjson` Secret, create a **Registry** for the part of
the registry they are for, and list it on the Product:

```bash
kubectl create secret docker-registry ghcr-my-org -n kokumi \
    --docker-server=ghcr.io --docker-username=<user> --docker-password=<token>
```

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Registry
metadata:
  name: ghcr-my-org
  namespace: kokumi
spec:
  url: oci://ghcr.io/my-org
  secretRef:
    name: ghcr-my-org
---
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Product
metadata:
  name: my-app
  namespace: kokumi
spec:
  source:
    url: oci://ghcr.io/my-org/charts/my-app
    version: "1.2.3"
  registryRefs:
  - name: ghcr-my-org
```

The Product's Registries authenticate its source, its images and its vendored
copy; an Order lists the Registries of its own destination. See
[Registries and Repositories](../user-guide/registries/) for which credentials
every location uses and what Argo CD and the nodes need.

## Next steps

{{< cards >}}
  {{< card link="../installation" title="Installation" icon="download" subtitle="Version pinning and upgrade guide." >}}
  {{< card link="../architecture" title="Architecture" icon="cube-transparent" subtitle="How reconciliation works under the hood." >}}
{{< /cards >}}
