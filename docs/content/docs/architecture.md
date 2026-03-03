---
title: Architecture & Concepts
weight: 3
description: How Kokumi models release workflows and how its control loops operate.
---

## Core philosophy

Kokumi draws a hard line between three concerns that most delivery systems conflate:

1. **Intent** — what _should_ be built and how (the Recipe)
2. **Artifact** — what _was_ built, exactly (the Preparation)
3. **Activation** — what is _currently running_ (the Serving)

By keeping these separate and immutable at the artifact layer, Kokumi gives
you a complete, auditable history of every version ever produced — and the
ability to promote or roll back with a single field change.

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
Recipe ──renders──▶ Preparation (immutable, versioned OCI artifact)
                         ▲
Serving ──selects────────┘  (mutable pointer to one Preparation)
   │
   └──creates/updates──▶ Argo CD Application
                              │
                              └──syncs──▶ Cluster workloads

Menu ──coordinates──▶ { Recipe₁, Recipe₂, … }  (atomic multi-Recipe rollout)
```

### Recipe

The **only resource you create manually**. A Recipe declares:

- **Source** — OCI image reference containing a `manifest.yaml` at its root
- **Patches** — Patches to apply before producing the artifact

Recipes are mutable; every change triggers a new reconciliation cycle and
automatically produces a new Preparation.

### Preparation

Preparations are **created automatically** by Kokumi whenever a Recipe changes.
You never create them directly.

A Preparation is the _output_ of rendering a Recipe at a specific point in time.
It contains:

- A reference to the parent Recipe and the exact source revision used
- An OCI artifact digest (stored in the in-cluster OCI registry)
- An immutable status — once `Ready`, a Preparation never changes

Preparations are **never garbage-collected automatically**. You retain full
history and can promote any old Preparation to active at any time.

### Serving

A Serving tracks which Preparation is actively deployed. There is exactly one
Serving per Recipe, and it is **managed automatically** — you never create one
directly. A Serving is created or updated in three ways:

- **Auto-deploy** — set `spec.autoDeployLatest: true` on the Recipe; Kokumi
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

### Menu

> **Not yet implemented — planned for a future release.**

A Menu will group multiple Recipes and allow coordinated rollouts — useful when
you need frontend, backend, and config to move together in a single atomic
operation.

## Reconciliation loop

```
Watch Recipe ──▶ Render source ──▶ Push OCI artifact ──▶ Create/update Preparation
                                                                   │
Watch Preparation status ──────────────────────────────────────────▼
Serving selects Preparation ──▶ Create/update Argo CD Application
                                        │
                                        └──▶ Argo CD syncs manifests to cluster
```

Key properties:

- **Idempotent** — each reconcile produces the same output for the same input
- **Level-triggered** — the controller always acts on observed state, not events
- **Owner references** — Preparations are owned by their Recipe; clean deletion is automatic
- **Argo CD delegates deployment** — Kokumi never applies manifests directly; it only manages the Argo CD Application resource

## OCI artifact format

Kokumi currently expects the source OCI artifact to contain a single file named
`manifest.yaml` at the root. This file must contain all Kubernetes resources
(as a single or multi-document YAML).

```
myapp:v1.0.0  (OCI artifact)
└── manifest.yaml   ← all Kubernetes resources
```

Support for additional source formats (Helm charts, Kustomize directories) is
planned for a future release.

## OCI registry

Kokumi ships an in-cluster OCI-compatible registry (backed by a `PersistentVolumeClaim`)
that stores rendered manifests as OCI artifacts. This means:

- Zero external registry dependency
- Rendered manifests are portable — pull them with any OCI client
- Artifact digests are content-addressed; deduplication is automatic
