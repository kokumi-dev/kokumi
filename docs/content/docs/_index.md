---
title: Documentation
sidebar:
  open: true
---

Welcome to the **Kokumi** documentation.

Kokumi is a Kubernetes operator for OCI-first GitOps config delivery that
preserves the review and traceability properties of Git. As OCI becomes the
standard mechanism for shipping Kubernetes config, most tools trade away
Git's human review and audit trail to get there. Kokumi keeps both: OCI
artifacts as the immutable source of truth, with approval gates and full
render visibility before anything reaches your cluster.

You describe an application once as a **Product**, and each variant of it,
such as one environment, as an **Order**. Every change renders an immutable,
signed OCI artifact, a **Preparation**, and the Order's **Serving** promotes
one of them and deploys it through Argo CD to the **Clusters** of the Order's
**Environment**. [Architecture](architecture/) introduces every resource.

## Where to start

{{< cards >}}
  {{< card link="getting-started" title="Getting Started" icon="play" subtitle="Install Kokumi, render a Helm chart, promote it, and roll it back." >}}
  {{< card link="installation" title="Installation" icon="download" subtitle="Requirements, install, login, upgrade, and uninstall." >}}
  {{< card link="architecture" title="Architecture" icon="cube-transparent" subtitle="The ideas behind Kokumi, its resources, and how a change reaches a cluster." >}}
  {{< card link="user-guide" title="User Guide" icon="book-open" subtitle="Define applications, promote them, and deploy them." >}}
  {{< card link="operator-guide" title="Operator Guide" icon="shield-check" subtitle="Operate and secure a Kokumi installation." >}}
{{< /cards >}}
