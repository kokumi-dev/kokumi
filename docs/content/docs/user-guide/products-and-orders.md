---
title: Products and Orders
weight: 1
description: Define an application once as a Product, and deploy variants of it as Orders.
---

A **Product** defines an application completely: its source, how it renders,
its base values and patches, what happens to its images, and what its Orders
may change. An **Order** is one variant of a Product, such as one
environment, and declares only what differs. A Product on its own is never
rendered or deployed; its Orders are.

A common split: a platform team owns the Product, application teams own its
Orders, and the Product's [overrides](#restrict-what-orders-may-change)
decide what the teams may change.

## The Product

{{< example file="getting-started/product.yaml" >}}

The Product resolves its source tag to a digest, verifies its signature when
required (see [Source Verification](../source-verification/)), and publishes
the result in `status.source`: URL, version, digest and verified signer.
Orders render that digest, and only once the Product is `Ready` for its
current generation, so every Order renders the same artifact even if the tag
moves upstream. The Product resolves the tag again only when its source or
vendor location changes.

```bash
kubectl get products -n shop
# NAME      READY   REASON     SOURCE                                      VERSION   VERIFIED   AGE
# podinfo   True    Resolved   oci://ghcr.io/stefanprodan/charts/podinfo   6.14.1               1m
```

A vendored Product reports `Vendored` instead of `Resolved`.

The Product's `registryRefs` name the Registries that authenticate its
source, its images and its vendored copy. See
[Registries and Repositories](../registries/).

## Sources

Kokumi renders four kinds of OCI artifacts:

| Source | The Product sets | A Preparation holds | Example |
|---|---|---|---|
| A Helm chart | `spec.render.helm` | `manifest.yaml`, the rendered chart | `getting-started` |
| YAML files at the artifact's root | nothing | one `manifest.yaml`, every top-level `*.yaml` and `*.yml` file merged | `manifest-bundle` |
| YAML files at the artifact's root | `spec.render.manifest.layout: Multi` | the files, as they are | `multi-file-manifests` |
| A `kustomization.yaml` at the artifact's root | nothing | the files, as they are; Argo CD builds the kustomization when it deploys | `kustomize` |

Patches apply to every kind; Helm values only to charts.

{{< tabs >}}
{{< tab name="manifest-bundle" >}}
{{< example file="manifest-bundle/product.yaml" >}}
{{< example file="manifest-bundle/order.yaml" >}}
{{< /tab >}}
{{< tab name="multi-file-manifests" >}}
{{< example file="multi-file-manifests/product.yaml" >}}
{{< example file="multi-file-manifests/order.yaml" >}}
{{< /tab >}}
{{< tab name="kustomize" >}}
{{< example file="kustomize/product.yaml" >}}
{{< example file="kustomize/order.yaml" >}}
{{< /tab >}}
{{< /tabs >}}

A Helm chart renders with these options of `spec.render.helm`:

| Field | Description | Default |
|---|---|---|
| `releaseName` | The Helm release name | The Order's name |
| `includeCRDs` | Include the chart's CRDs in the output | `false` |
| `values` | Base values; each Order's `spec.values` are deep-merged over them | none |

Helm renders into the Order's [target namespace](#the-target-namespace).

A kustomization is built by Argo CD, not by Kokumi, so Kokumi never sees the
resources it produces. Patches still apply to the files, but the `Pin` and
`Vendor` image policies need rendered manifests and fail; see
[Container Images](../images/#sources-with-a-kustomization-file).

To publish a source of your own, with the annotations that link each
Preparation back to its commit, see
[Producing Base Artifacts](../base-artifacts/).

## The Order

{{< example file="getting-started/order.yaml" >}}

| Field | What it does |
|---|---|
| `productRef` | The Product, in the Order's namespace. It cannot be changed after creation. |
| `environmentRef` | The [Environment](../environments/) the Order deploys to, in its namespace. Required, and it cannot be changed: another Environment is another Order. |
| `values` | Helm values deep-merged over the Product's `render.helm.values`; the Order wins. Only valid for a Helm source. |
| `patches` | [Patches](#patches) merged over the Product's, per target; on the same path the Order wins. |
| `edits` | Patches the UI writes when you edit a rendered file, applied after `patches`. |
| `targetNamespace` | Where the workload lands on the clusters. See [The target namespace](#the-target-namespace). |
| `destination` | Where Preparations are pushed. Defaults to `<bundled registry>/<namespace>/<order>`. |
| `registryRefs` | The Registries that authenticate the destination, and only the destination. |
| `promotion` | How Preparations are promoted: the mode, and the gates. See [Promotion and Rollback](../promotion/). |

An Order's name is at most 63 characters. Each Order has its own Preparation
history, its own Serving, and its own gates, so a new Product version can
deploy to `podinfo-dev` automatically while `podinfo-prod` waits for
approvals.

```bash
kubectl get orders -n shop
# NAME      READY   REASON   PRODUCT   ENVIRONMENT   LATEST PREPARATION     AGE
# podinfo   True    Ready    podinfo   local         podinfo-d7ce0c46a686   1m
```

While its Product is missing, not `Ready`, or being deleted, an Order reports
`Ready=False` with reason `ProductNotFound`, `ProductNotReady` or
`ProductDeleting` and waits; the UI shows it as **Waiting**. An Order that
sets what its Product does not allow, or whose render fails, reports
`ProcessingFailed` with the reason in the message.

### Describing a change

The annotation `delivery.kokumi.dev/commit-message` on the Order becomes the
description of the next Preparation, like a commit message. Kokumi removes it
after the render. Without it, the first Preparation is described as
`Initial commit` and later ones as `Automatically generated`.

```bash
kubectl annotate order podinfo -n shop delivery.kokumi.dev/commit-message="Scale to 3 replicas"
kubectl patch order podinfo -n shop --type=merge -p '{"spec":{"values":{"replicaCount":3}}}'
```

## Patches

A patch sets fields of the resources of one target, by path:

```yaml
spec:
  patches:
  - target:
      kind: Deployment
      name: podinfo
      # namespace: shop      # optional; without it, any namespace matches
    set:
      .spec.replicas: "3"
      .spec.template.spec.containers[0].imagePullPolicy: Always
```

- A path is dot-separated, with `[n]` for a list index. Missing fields are
  created.
- Values are strings in the Order, and are written as booleans or numbers
  when they read as one: `"3"` becomes `3`, `"true"` becomes `true`.
- Every document is written back with 2-space indentation, whether a patch
  matches it or not, so the same input always renders the same bytes. A
  document that is not valid YAML fails the render with its file and
  document number.

## Restrict what Orders may change

`spec.overrides` on the Product decides what its Orders may change,
separately for Helm values and for patches. The patch policy covers edits
made in the UI too.

| `policy` | Orders may set |
|---|---|
| `None` (default) | nothing |
| `Listed` | only the listed Helm value paths (leaf paths, dot-separated), or the listed patch targets and paths |
| `All` | anything |

```yaml
spec:
  overrides:
    values:
      policy: Listed
      paths:
      - replicaCount
      - ui.message
    patches:
      policy: Listed
      targets:
      - target:
          kind: Deployment
          name: podinfo
        paths:
        - .spec.replicas
```

`paths` and `targets` are required with `Listed` and rejected otherwise. A
target without a namespace matches the resource in any namespace; when
several targets match, the paths of all of them are allowed. An Order that
sets anything else fails with `ProcessingFailed`, naming the offending value
or patch.

## The target namespace

The target namespace is where the workload lands on every cluster of the
Order's Environment: the Order's `targetNamespace`, else the Product's, else
the Order's own namespace. Helm renders into it, and Argo CD deploys
resources without a namespace there, creating the namespace when it is
missing.

The target namespace is part of the intent, so a Preparation deploys to the
same namespace wherever it is promoted. Changing it renders a new
Preparation, and deploying that one moves the workload: Argo CD deletes it in
the old namespace.

## What renders a new Preparation

Kokumi merges an Order over its Product into the **intent**, the effective
configuration that determines the artifact. Whenever the intent changes, the
Order renders a new Preparation; while it stays the same, nothing is
rendered.

| Part of the intent | Not part of the intent |
|---|---|
| The source: URL, version and resolved digest | The promotion mode |
| The merged render config and Helm values | The overrides |
| The merged patches, and the edits | The Environment |
| The target namespace, always | The commit message |
| The image policy and field specs, unless they are the defaults, and the vendor location under `Vendor` | The digests images resolve to: a tag that moves upstream renders nothing |
| The gates: approvals, upstream stages and checks | The Registries, their Secrets, and the Kitchen's image mirrors |
| The destination | |
| The [Product revision](../promotion-pipelines/#what-a-product-change-is-the-product-revision) | |

Changing the gates renders a new Preparation that needs fresh approvals.
Because the intent holds the Product revision, a change to the Product's
source, render config, patches or image handling renders every Order again,
also an Order whose own values or patches hide the change.

{{< callout type="warning" >}}
The intent is stored inside the artifact, so **anyone who can pull the
artifact can read it**: Argo CD, cluster nodes, and anyone the artifact is
copied to. Do not put secrets into Helm values or patches.
{{< /callout >}}

[Artifacts in the Registry](../artifacts/#the-intent-and-its-hash) explains
how the intent is stored and hashed.

## Vendoring the source

A Product can copy its source, unchanged, to another OCI registry, for
example to prepare an air-gapped installation. Kokumi copies the source by
the digest that verified, together with its referrers, so the copy keeps the
publisher's signatures and attestations and still verifies against the
publisher's key. Orders then render from the copy:

```yaml
spec:
  source:
    url: oci://ghcr.io/kokumi-dev/testdata/external-secrets
    version: "0.1.0"
  vendor:
    url: oci://harbor.corp/mirror/external-secrets   # vendor: {} copies to <namespace>/vendor/<name> in the bundled registry
  registryRefs:
  - name: harbor-mirror        # its url covers the vendor location
```

The Product's Registries authenticate both the upstream source and the copy,
and `status.source` names the copy, so Orders need no knowledge of the
vendoring. Signatures that cosign stored under legacy `sha256-<digest>.sig`
tags are not referrers and are not copied. The Product's **Artifact** tab
draws the source and the copy side by side.

The images of the rendered manifests can be vendored below the copy too; see
[Container Images](../images/#vendoring).

## Deleting

- **A Product** cannot be deleted while Orders reference it. The finalizer
  `delivery.kokumi.dev/product-protection` keeps it, and its `Ready`
  condition reports `InUse` with the remaining Orders. The UI shows it as
  **Deleting** and offers no new Orders for it.
- **An Order** takes its Preparations and its Serving with it, and the
  Serving deletes its Argo CD Applications. The workloads keep running: the
  Applications carry no `resources-finalizer.argocd.argoproj.io`, so Argo CD
  leaves their resources in place. The artifacts stay in the registry.
  `kubectl delete order <name> --cascade=orphan` keeps the Serving, and a
  later Order of the same name adopts it.
- **Preparations** are never pruned while their Order exists, so every
  version can be promoted again.
