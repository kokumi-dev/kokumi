---
title: User Guide
weight: 4
description: How-to guides for Kokumi features.
---

Guides for the people who define applications and deploy them with Kokumi.

{{< cards >}}
  {{< card link="products-and-orders" title="Products and Orders" icon="collection" subtitle="Sources, values, patches, and what Orders may change." >}}
  {{< card link="promotion" title="Promotion and Rollback" icon="refresh" subtitle="Automatic and Manual promotion, gates, queued promotions, and rollback." >}}
  {{< card link="approvals" title="Approvals" icon="user-group" subtitle="Require reviews from groups of people before a Preparation deploys." >}}
  {{< card link="promotion-pipelines" title="Promotion Pipelines" icon="chevron-double-right" subtitle="Deploy a Product change stage by stage, after soak times, with signed records." >}}
  {{< card link="environments" title="Deploy to Several Clusters" icon="cloud" subtitle="Deploy an Order to every cluster of an Environment." >}}
  {{< card link="registries" title="Registries and Repositories" icon="lock-closed" subtitle="Connect Kokumi to private OCI registries." >}}
  {{< card link="images" title="Container Images" icon="cube-transparent" subtitle="Record, pin, or vendor the images every Preparation runs." >}}
  {{< card link="source-verification" title="Source Verification" icon="badge-check" subtitle="Require signed sources, by key or keyless." >}}
  {{< card link="security-scanning" title="Vulnerability Scanning" icon="shield-check" subtitle="Signed SBOMs and vulnerability reports for vendored images, and promotion checks." >}}
  {{< card link="artifacts" title="Artifacts in the Registry" icon="cube" subtitle="Read the OCI artifacts Kokumi uses and creates." >}}
  {{< card link="base-artifacts" title="Producing Base Artifacts" icon="link" subtitle="Publish sources that link every Preparation back to its commit." >}}
{{< /cards >}}
