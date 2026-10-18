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

It models your delivery workflow as composable primitives:

| Resource | Role |
|---|---|
| **Product** | Complete definition of an application: source, rendering, base values and patches |
| **Recipe** | Rendering profile instructions _(planned, not yet implemented)_ |
| **Order** | Variant of a Product, such as one environment, holding only what differs |
| **Preparation** | Immutable OCI artifact rendered from an Order |
| **Serving** | Active deployment selecting exactly one Preparation |
| **Registry** | A connection to an OCI registry: its URL prefix, credentials, TLS settings and scheme |
| **Repository** | A catalog entry: an OCI repository to browse and create Products from |

## Where to start

{{< cards >}}
  {{< card link="getting-started" title="Getting Started" icon="play" subtitle="Install Kokumi and create your first Order in minutes." >}}
  {{< card link="installation" title="Installation" icon="download" subtitle="Requirements, install, upgrade, and uninstall." >}}
  {{< card link="architecture" title="Architecture" icon="cube-transparent" subtitle="Understand the reconciliation model and key concepts." >}}
  {{< card link="user-guide" title="User Guide" icon="book-open" subtitle="How-to guides for Kokumi features." >}}
  {{< card link="operator-guide" title="Operator Guide" icon="shield-check" subtitle="Operating and securing a Kokumi deployment." >}}
{{< /cards >}}
