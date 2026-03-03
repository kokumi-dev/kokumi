---
title: Architecture & Concepts
weight: 3
description: How kokumi models release workflows and how its control loops operate.
---

## Core philosophy

Kokumi draws a hard line between three concerns that most delivery systems conflate:

1. **Intent** — what _should_ be built and how (the Recipe)
2. **Artifact** — what _was_ built, exactly (the Preparation)
3. **Activation** — what is _currently running_ (the Serving)

By keeping these separate and immutable at the artifact layer, kokumi gives
you a complete, auditable history of every version ever produced — and the
ability to promote or roll back with a single field change.

## Resource model

```
Recipe ──renders──▶ Preparation (immutable, versioned OCI artifact)
                         ▲
Serving ──selects────────┘  (mutable pointer to one Preparation)

Menu ──coordinates──▶ { Recipe₁, Recipe₂, … }  (atomic multi-Recipe rollout)
```

### Recipe

A Recipe declares:

- **Source** — OCI image reference
- **Patches** — Patches to apply

Recipes are mutable; changing a Recipe triggers a new reconciliation cycle and
produces a new Preparation.

### Preparation

A Preparation is the _output_ of rendering a Recipe at a specific point in time.
It contains:

- A reference to the parent Recipe and the exact source revision used
- An OCI artifact digest (stored in the in-cluster OCI registry)
- An immutable status — once `Ready`, a Preparation never changes

Preparations are **never garbage-collected automatically**. You retain full
history and can promote any old Preparation to active at any time.

### Serving

A Serving is the active selection. Each Recipe has at most one Serving;
changing `spec.preparation` atomically switches the active version.

Rollback is just updating the reference:

```bash
kubectl patch serving my-app \
  --type=merge \
  -p '{"spec":{"preparation":{"name":"my-app-12736216279"}}}'
```

### Menu

A Menu groups multiple Recipes and allows coordinated rollouts — useful when
you need frontend, backend, and config to move together in a single atomic
operation.

## Reconciliation loop

```
Watch Recipe ──▶ Render source ──▶ Push OCI artifact ──▶ Create/update Preparation
                                                                   │
Watch Preparation status ──────────────────────────────────────────▼
Serving selects Preparation ──▶ Deploy/apply rendered manifests
```

Key properties:

- **Idempotent** — each reconcile produces the same output for the same input
- **Level-triggered** — the controller always acts on observed state, not events
- **Owner references** — Preparations are owned by their Recipe; clean deletion is automatic

## OCI registry

Kokumi ships an in-cluster OCI-compatible registry (backed by a `PersistentVolumeClaim`)
that stores rendered manifests as OCI artifacts. This means:

- Zero external registry dependency
- Rendered manifests are portable — pull them with any OCI client
- Artifact digests are content-addressed; deduplication is automatic
