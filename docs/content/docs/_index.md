---
title: Documentation
sidebar:
  open: true
---

Welcome to the **kokumi** documentation.

Kokumi is a Kubernetes operator for structured, immutable release management.
It models your delivery workflow as four composable primitives:

| Resource | Role |
|---|---|
| **Recipe** | Declares build intent (sources, patches, config) |
| **Preparation** | Immutable OCI artifact rendered from a Recipe |
| **Serving** | Active deployment selecting exactly one Preparation |
| **Menu** | Atomic coordination across multiple Recipes |

## Where to start

{{< cards >}}
  {{< card link="getting-started" title="Getting Started" icon="play" subtitle="Install kokumi and create your first Recipe in minutes." >}}
  {{< card link="installation" title="Installation" icon="download" subtitle="Deploy to any cluster with Helm or kustomize." >}}
  {{< card link="architecture" title="Architecture" icon="cube-transparent" subtitle="Understand the reconciliation model and key concepts." >}}
{{< /cards >}}
