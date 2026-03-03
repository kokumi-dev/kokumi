---
title: Documentation
sidebar:
  open: true
---

Welcome to the **Kokumi** documentation.

Kokumi is a Kubernetes operator for structured, immutable release management.
It models your delivery workflow as four composable primitives:

| Resource | Role |
|---|---|
| **Recipe** | Declares build intent (sources, patches, config) |
| **Preparation** | Immutable OCI artifact rendered from a Recipe |
| **Serving** | Active deployment selecting exactly one Preparation |
| **Menu** | Atomic coordination across multiple Recipes _(planned, not yet implemented)_ |

## Where to start

{{< cards >}}
  {{< card link="getting-started" title="Getting Started" icon="play" subtitle="Install Kokumi and create your first Recipe in minutes." >}}
  {{< card link="installation" title="Installation" icon="download" subtitle="Requirements, install, upgrade, and uninstall." >}}
  {{< card link="architecture" title="Architecture" icon="cube-transparent" subtitle="Understand the reconciliation model and key concepts." >}}
{{< /cards >}}
