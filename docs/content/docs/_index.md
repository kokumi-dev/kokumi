---
title: Documentation
sidebar:
  open: true
---

Welcome to the **Kokumi** documentation.

Kokumi is a Kubernetes operator for OCI-first GitOps config delivery that preserves
the review and traceability properties of Git. As OCI becomes the standard
mechanism for shipping Kubernetes config, most tools trade away Git's human
review and audit trail to get there. Kokumi keeps both: OCI artifacts as the
immutable source of truth, with approval gates and full render visibility
before anything reaches your cluster.

It models your delivery workflow as five composable primitives:

| Resource | Role |
|---|---|
| **Menu** | Optional reusable template for Orders |
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
