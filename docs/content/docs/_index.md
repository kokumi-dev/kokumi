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
| **Menu** | Optional reusable template for Orders _(planned, not yet implemented)_ |
| **Recipe** | Rendering profile instructions _(planned, not yet implemented)_ |
| **Order** | Concrete release request that can define full intent or parameterize a Menu |
| **Preparation** | Immutable OCI artifact rendered from an Order |
| **Serving** | Active deployment selecting exactly one Preparation |

## Where to start

{{< cards >}}
  {{< card link="getting-started" title="Getting Started" icon="play" subtitle="Install Kokumi and create your first Order in minutes." >}}
  {{< card link="installation" title="Installation" icon="download" subtitle="Requirements, install, upgrade, and uninstall." >}}
  {{< card link="architecture" title="Architecture" icon="cube-transparent" subtitle="Understand the reconciliation model and key concepts." >}}
{{< /cards >}}
