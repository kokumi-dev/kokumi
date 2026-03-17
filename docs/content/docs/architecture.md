---
title: Architecture & Concepts
weight: 3
description: How Kokumi models release workflows and how its control loops operate.
---

## Core philosophy

Kokumi draws a hard line between three concerns that most delivery systems conflate:

1. **Intent** — what _should_ be built and how (the Order)
2. **Artifact** — what _was_ built, exactly (the Preparation)
3. **Activation** — what is _currently running_ (the Serving)

By keeping these separate and immutable at the artifact layer, Kokumi gives
you a complete, auditable history of every version ever produced — and the
ability to promote or roll back with a single field change.

## Key advantages

### Immutability at the artifact layer

A Preparation is not a live snapshot — it is an OCI artifact identified by a
SHA-256 digest. Once a Preparation reaches `Ready`, it never changes.

- Reproduce exactly what was running at any point in time by re-fetching the
  artifact by digest.
- Drift is unambiguous — compare the deployed digest to the desired digest;
  any difference is a concrete, actionable signal.
- Artifacts can be signed, attested, and audited independently of the cluster.

### Separation of rendering from deployment

Kokumi keeps rendering and deployment as two distinct, independently
controllable steps:

```
Render                      Promote                    Deploy
Order ──▶ Preparation ─────────────────────────▶ Serving ──▶ Argo CD Application
           (immutable)    (human or auto)           (active pointer)
```

Because the rendered artifact is stored independently:

- **Approval gates** — set `spec.autoDeploy: false` to hold the
  Serving until a human explicitly promotes the Preparation.
- **Pre-flight validation** — inspect the full rendered manifest in the UI
  before it touches any cluster.

### Rollback without re-rendering

Rolling back means promoting any previous Preparation. The artifact already
exists in the in-cluster registry, so the exact state that previously ran is
restored instantly — no re-render, no drift.

### Air-gap friendly by design

The entire pipeline — OCI pull → render → push to in-cluster registry — has no
requirement for outbound internet access. All external dependencies are OCI
artifacts that can be mirrored in advance, making Kokumi suitable for
restricted and disconnected environments.

### GitOps integration, not replacement

Kokumi does not apply manifests directly and does not own a sync loop. Each
Serving creates or updates an Argo CD `Application` pointing at the
Preparation's OCI artifact by digest. Kokumi feeds your existing GitOps
workflow rather than replacing it.

## Dependencies

Kokumi requires **Argo CD** (≥ 3.3) installed in the `argocd` namespace.
When a Serving is created or updated, the Serving controller creates or updates
an Argo CD `Application` resource that points to the immutable OCI artifact of
the selected Preparation. Argo CD then syncs that artifact into the cluster.

> **Kokumi does not apply manifests directly.** All runtime deployment is
> delegated to Argo CD. Without a running Argo CD instance, Servings will
> remain in a `Failed` state and no workloads will be deployed.

## Resource model

```
Order ──renders──▶ Preparation (immutable, versioned OCI artifact)
                         ▲
Serving ──selects────────┘  (mutable pointer to one Preparation)
   │
   └──creates/updates──▶ Argo CD Application
                              │
                              └──syncs──▶ Cluster workloads

Menu (optional, planned) ──provides template──▶ Order  (consumed and parameterized)
Recipe ──provides render profile──▶ Order  (for example Helm options)
```

### Menu

Menu is the reusable deployment template that an Order consumes and
parameterizes for a concrete rollout.

Menu consumption is not implemented yet.

### Recipe

Recipe captures rendering configuration, including options like Helm rendering
behavior and values-related settings.

### Order

Order is the concrete execution request. It binds source and runtime input and
triggers rendering to produce immutable artifacts.

Order does not need Menu. It can fully define component intent on its own, and
this standalone behavior is intended to remain a first-class mode.

An Order declares:

- **Source** — OCI image reference: either a pre-rendered manifest bundle
  (containing `manifest.yaml`) or a Helm chart in OCI format (add
  `spec.render.helm` to configure rendering)
- **Patches** — Patches to apply before producing the artifact

Orders are mutable; every change triggers a new reconciliation cycle and
automatically produces a new Preparation.

### Preparation

Preparations are **created automatically** by Kokumi whenever an Order changes.
You never create them directly.

A Preparation is the _output_ of rendering an Order at a specific point in time.
It contains:

- A reference to the parent Order and the exact source revision used
- An OCI artifact digest (stored in the in-cluster OCI registry)
- An immutable status — once `Ready`, a Preparation never changes

Preparations are **never garbage-collected automatically**. You retain full
history and can promote any old Preparation to active at any time.

### Serving

A Serving tracks which Preparation is actively deployed. There is exactly one
Serving per Order, and it is **managed automatically** — you never create one
directly. A Serving is created or updated in three ways:

- **Auto-deploy** — set `spec.autoDeploy: true` on the Order; Kokumi
  updates the Serving automatically every time a new Preparation becomes `Ready`.
- **Label promotion** — label a Preparation with
  `delivery.kokumi.dev/approve-deploy: "true"`.
- **UI** — click **Promote** on any Preparation in the Kokumi UI.

When a Serving is reconciled, the controller:

1. Resolves the referenced Preparation and its immutable OCI artifact digest.
2. Creates or updates an Argo CD `Application` in the `argocd` namespace,
   pointing `spec.source.repoURL` at the Preparation's OCI artifact and
   `spec.source.targetRevision` at its exact digest.
3. Argo CD takes over and syncs the manifests into the target namespace.

Rollback is promoting any previous Preparation — no re-rendering required.

### Menu and Recipe lifecycle

Recipe defines rendering behavior. Menu provides an optional reusable template.
Order remains the concrete execution resource, whether standalone or template-
parameterized.

## Reconciliation loop

```
Watch Order ──▶ Render source ──▶ Push OCI artifact ──▶ Create/update Preparation
                                                                   │
Watch Preparation status ──────────────────────────────────────────▼
Serving selects Preparation ──▶ Create/update Argo CD Application
                                        │
                                        └──▶ Argo CD syncs manifests to cluster
```

Key properties:

- **Idempotent** — each reconcile produces the same output for the same input
- **Level-triggered** — the controller always acts on observed state, not events
- **Owner references** — Preparations are owned by their Order; clean deletion is automatic
- **Argo CD delegates deployment** — Kokumi never applies manifests directly; it only manages the Argo CD Application resource

## OCI source formats

Kokumi supports two source OCI artifact formats, selected by the presence or
absence of `spec.render`.

### Pre-rendered manifest bundle (default)

When `spec.render` is absent, the source OCI artifact must contain a single
`manifest.yaml` file at its root holding all Kubernetes resources (single or
multi-document YAML). The file is stored as-is — no rendering step is applied.

```
myapp:v1.0.0  (OCI artifact)
└── manifest.yaml   ← all Kubernetes resources (pre-rendered)
```

This is the simplest format and is well-suited to components whose manifests
are already generated upstream and published as OCI bundles.

### Helm chart in OCI format

When `spec.render.helm` is present, the source OCI artifact must be a standard
Helm chart packaged and pushed to an OCI registry (e.g. via `helm push`).
Kokumi runs `helm template` internally to render the chart into a manifest
bundle, then stores the output as an immutable Preparation artifact.

```yaml
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
```

Available `render.helm` fields:

| Field | Description | Default |
|---|---|---|
| `releaseName` | Helm release name passed to `helm template` | Order name |
| `namespace` | Target namespace (`--namespace`) | Order namespace |
| `includeCRDs` | Include CRDs in the rendered output (`--include-crds`) | `false` |
| `values` | Inline Helm values merged last (highest priority) | — |

Helm OCI charts are first-class in Kokumi. Any chart published to an OCI
registry — whether an upstream community chart or an internally-built one —
can be used as an Order source.

## OCI registry

Kokumi ships an in-cluster OCI-compatible registry (backed by a `PersistentVolumeClaim`)
that stores rendered manifests as OCI artifacts. This means:

- Zero external registry dependency
- Rendered manifests are portable — pull them with any OCI client
- Artifact digests are content-addressed; deduplication is automatic
