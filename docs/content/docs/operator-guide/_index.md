---
title: Operator Guide
weight: 5
description: Operating and securing a Kokumi deployment.
---

Guides for operators who run and secure Kokumi in a cluster.

{{< cards >}}
  {{< card link="authentication" title="Authentication" icon="lock-closed" subtitle="Admin login, credentials, and disabling the built-in account." >}}
  {{< card link="authorization" title="Authorization" icon="key" subtitle="Kubernetes-native RBAC via identity-to-ServiceAccount mapping." >}}
  {{< card link="signing" title="Signing" icon="badge-check" subtitle="The signing key, verifying artifacts with cosign, and requiring signed sources." >}}
  {{< card link="images" title="Container Images" icon="cube-transparent" subtitle="Image mirrors, letting nodes pull vendored images, and registry sizing." >}}
  {{< card link="scanning" title="Vulnerability Scanning" icon="shield-check" subtitle="The scanner component, the vulnerability database in air-gapped clusters, and network isolation." >}}
  {{< card link="clusters" title="Clusters" icon="server" subtitle="Register the clusters Kokumi deploys to, and hand them to Argo CD." >}}
  {{< card link="environments" title="Environments" icon="globe-alt" subtitle="Group clusters, decide who deploys there, and which Environments come first." >}}
{{< /cards >}}
