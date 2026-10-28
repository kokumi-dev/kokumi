---
title: Deploy to Several Clusters
weight: 8
description: Deploy an Order to every cluster of an Environment, into its target namespace, through an Argo CD Application per cluster.
---

Every Order deploys to an [Environment](../../operator-guide/environments/) of
its namespace: a group of clusters an admin set up. It deploys every
Preparation it promotes to each cluster of the Environment, through an Argo CD
Application per cluster.

The multi-cluster example deploys podinfo to the Environment `edge` of the
workspace `platform`. An admin registers the clusters and creates the
Environment in the namespace first; see
[Clusters](../../operator-guide/clusters/):

{{< tabs >}}
{{< tab name="order.yaml" >}}
{{< example file="multi-cluster/order.yaml" >}}
{{< /tab >}}
{{< tab name="product.yaml" >}}
{{< example file="multi-cluster/product.yaml" >}}
{{< /tab >}}
{{< /tabs >}}

Apply it to the same namespace:

{{< apply-example name="multi-cluster" namespace="platform" >}}

## Where an Order deploys

- **`spec.environmentRef.name`** names an Environment of the Order's
  namespace. It is required, set when the Order is created, and cannot be
  changed, so that no Order reaches an Environment without passing what it
  requires; to deploy to another Environment, create another Order. Kokumi
  keeps the Environment `local`, the cluster it runs in, in its own namespace.
- The Order deploys to **every cluster** of the Environment. Clusters of
  another region or kind are an Environment of their own.
- The Environment decides where a Preparation runs, not what it is: it is not
  part of the intent, and renders nothing. Every cluster runs the same
  Preparation, with the same image digests.

When the Order may not deploy to the Environment at all, nothing is deployed:
the Serving deletes its Argo CD Applications, what runs on the clusters keeps
running, and its `Ready` condition says why: `EnvironmentNotFound`,
`InvalidSelector`, `NoClusters`, `ProjectNotFound`, `ProjectNotPermitted` or
`ArgoCDNotInstalled`; see [Environments](../../operator-guide/environments/).
The Serving checks this whenever it deploys. The Environment may also require
stages of other Environments first; see
[Promotion Pipelines](../promotion-pipelines/#group-stages-into-environments).

## The namespace on the clusters

An Order deploys into its **target namespace** on every cluster:

1. its own `spec.targetNamespace`, if set;
2. else its Product's `spec.targetNamespace`, the application's home, such as
   `podinfo` in the example;
3. else the Order's namespace.

The target namespace is part of what is rendered: Helm renders into it, and
resources without a namespace are deployed there. A Preparation records it and
deploys there wherever it is promoted. Changing it renders a new Preparation,
and deploying that moves the workload: Argo CD deletes it in the old namespace.
The Environment's Argo CD project decides which namespaces it may deploy into:
Argo CD refuses a destination the project does not permit, and the Serving
holds that cluster with reason `Rejected` and Argo CD's message.

## What runs where

The Serving counts the clusters it deploys to by the state of their
deployment, in `status.clusters`: `selected`, `healthy`, `progressing`,
`degraded` and `held`. It lists the clusters that do not run the deployed
Preparation healthily in `status.targets`, with what Argo CD reports on each:
held clusters first, then degraded, then progressing ones, at most 64. A
healthy cluster is only counted, so that the status stays small however many
clusters there are; its Argo CD Application says the rest:

```bash
kubectl get serving podinfo-edge -n <namespace> -o wide
# NAME           ORDER          ...   CLUSTERS   HEALTHY   READY   REASON     ...
# podinfo-edge   podinfo-edge   ...   2          2         True    Deployed   ...
kubectl get serving podinfo-edge -n <namespace> \
  -o jsonpath='{range .status.targets[*]}{.cluster}{"\t"}{.conditions[0].reason}{"\t"}{.revision}{"\n"}{end}'
kubectl -n argocd get applications -l delivery.kokumi.dev/serving=podinfo-edge,delivery.kokumi.dev/serving-namespace=<namespace>
```

- **A Preparation is deployed** (`Ready=True`, reason `Deployed`) once every
  cluster whose Cluster is Ready runs it synced and healthy, and at least one
  does. Its deployment record, signed and attached to the artifact, names the
  clusters that ran it healthily and the namespace it was healthy in.
- **A Cluster that is not Ready**, for any reason, is held with
  `ClusterNotReady`, and its Application stays as it is until the Cluster is
  Ready again. It does not hold the Serving back: the Serving is healthy
  without it, soaks, and the stages after it proceed. `Ready` names it, as in
  `Deployed Preparation … to a, b; not ready: c`. When no Cluster is Ready,
  the deployment fails.
- **A degraded cluster** fails the deployment, as on one cluster.
- **A cluster that joins** the Environment gets an Application right away. **A
  cluster that leaves** loses its Application, and what runs there keeps
  running; the Serving records an Event naming it.
- **Any other cluster Kokumi cannot deploy to** is held, counted in `held`,
  listed with the reason, and fails the deployment: `Rejected` when Argo CD
  refuses the Application, `ClusterConflict` when another Cluster of the
  Environment connects to the same server (only the first by name deploys),
  `OptInRequired` for an Application Kokumi may not change, or
  `RegistryUnreachable` below.
- **The `Ready` message** names the first clusters and counts the others, so
  that a large fleet keeps it short.

Every Order deploys its Preparations to all its clusters at once.

## Images on other clusters

The kubelet of every cluster pulls the images of what runs there. The bundled
registry is reachable only from the cluster Kokumi runs in, so an Order whose
images it vendors into the bundled registry deploys to no other cluster: the
Serving lists those clusters with reason `RegistryUnreachable`. Pin images
where they are published, as the example does, or vendor them into a
registry every cluster reaches; see [Container Images](../images/).

## In the UI

- **Order form:** "Environment" lists the Environments of the Order's
  namespace, and picks the only one if there is one; "Target namespace" sets
  where the Order deploys.
- **Serving:** a table of the clusters, with health, revision and since when,
  and a link to each Argo CD Application.
- **Cluster:** its Deployments tab lists what runs there.
