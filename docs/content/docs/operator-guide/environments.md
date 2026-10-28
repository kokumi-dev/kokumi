---
title: Environments
weight: 7
description: Group the Clusters of a namespace into Environments, decide through which Argo CD project Orders deploy there, and which Environments come first.
---

An **Environment** is a group of [Clusters](../clusters/) that the Orders of
its namespace deploy to, such as dev, staging or the production fleet. Each
decides:

- which Clusters of its namespace it covers, by their labels;
- the engine that deploys there, Argo CD, and its AppProject;
- which Environments an Order must run after before it deploys there.

Order authors name an Environment of their namespace in `spec.environmentRef`
when they create an Order; see
[Deploy to several clusters](../../user-guide/environments/).

{{< example file="multi-cluster/platform/environment.yaml" >}}

The multi-cluster example applies it with the Cluster it selects; see
[Clusters](../clusters/):

{{< apply-example name="multi-cluster/platform" namespace="platform" >}}

## Clusters

`clusterSelector` selects Clusters of the Environment's namespace by their
labels; `{}` selects every one. A Cluster joins or leaves an Environment when
its labels change:

```bash
kubectl label cluster spoke -n <namespace> fleet=edge
kubectl label cluster spoke -n <namespace> fleet-
```

A Cluster that leaves gets no new deployments, and the Argo CD Application of
each Order there is deleted. What runs there keeps running: Kokumi's
Applications carry no `resources-finalizer.argocd.argoproj.io`, so Argo CD
leaves their resources in place. Remove them by hand when you mean to.

The status counts the Clusters it selects, and those of them that are ready,
in `status.clusters`; its `Ready` message names the first ten. Each Cluster
reports on its own whether Orders can deploy to it:

```bash
kubectl get environments.delivery.kokumi.dev -n <namespace> -o wide
# NAME   DEPLOYER   PROJECT   CLUSTERS   READY CLUSTERS   READY   REASON   AGE
# edge   ArgoCD     default   2          2                True    Ready    5m
kubectl get clusters.delivery.kokumi.dev -n <namespace> -l fleet=edge
```

The short name of Environments is `env`. One Cluster does not make the
Environment unready: its `Ready` condition is False only when no Order could
deploy there at all.

| Reason | Meaning |
|---|---|
| `Ready` | Orders deploy to its Clusters. |
| `NoClusters` | It selects no Cluster of its namespace. |
| `InvalidSelector` | Its `clusterSelector` is not a valid label selector. |
| `ProjectNotFound` | Its Argo CD AppProject does not exist. |
| `ProjectNotPermitted` | Its AppProject does not admit its namespace in `sourceNamespaces`. |
| `ArgoCDNotInstalled` | Argo CD's CRDs are not installed. |

The status is for people: Servings do not read it. Each Serving checks the
Environment again whenever it deploys; see [The engine](#the-engine).

## Who may deploy where

Whoever may create Orders in the Environment's namespace may deploy there:
namespace RBAC decides. The built-in editor and viewer roles read
Environments and Clusters, for the Order form's picker; the admin role creates
and changes them.

The boundary of a workspace is the Argo CD AppProject its Environments name,
Argo CD's own model for teams:

- **`spec.sourceNamespaces`** decides which namespaces' Environments may use
  the project. Kokumi applies Argo CD's rule for Applications outside its
  control-plane namespace: an Environment in Kokumi's own namespace may use
  any project, one in another namespace only a project that admits its
  namespace; otherwise it and the Servings of its Orders report
  `ProjectNotPermitted`, and no Application is written. Argo CD's `default`
  project admits no namespace. Set the list where a project has none, and add
  to it where it has one:

  ```bash
  kubectl -n argocd patch appproject default --type=merge \
    -p '{"spec":{"sourceNamespaces":["platform","team-*"]}}'
  kubectl -n argocd patch appproject default --type=json \
    -p '[{"op":"add","path":"/spec/sourceNamespaces/-","value":"shop"}]'
  ```

- **`spec.destinations`** decides which servers and namespaces an Order may
  deploy into. Argo CD checks it: it refuses an Application whose destination
  the project does not permit, and the Serving holds that cluster with reason
  `Rejected` and Argo CD's message.

## The engine

`deployer` names the engine that deploys to the Environment's Clusters.
Today it is Argo CD: every Order deploys to each Cluster through an Argo CD
Application of its own, in the project `deployer.argoCD.project`. Every Order
deploys into its target namespace on every cluster, the `targetNamespace` of
the Order or of its Product, or the Order's namespace; Argo CD creates it where
it is missing.

Whenever a Serving deploys, it asks the engine whether the Order may deploy
there. For Argo CD, its CRDs must be installed, and the AppProject must exist
and, for an Environment outside Kokumi's namespace, admit the namespace in
`sourceNamespaces`. A change of the Environment or of the AppProject counts
at once. When an Order may not deploy at all, its Serving says why on its
`Ready` condition, with `EnvironmentNotFound` or one of the reasons of the
Environment above, and deletes its Applications. What runs on the clusters
keeps running.

## Environments that come first

`promotion.requireAfter` lists Environments of the same namespace an Order
must run after: every Order that deploys to the Environment must name, in
`spec.promotion.after`, an Order of each of them, with at least
`minSoakSeconds`. A Preparation that does not is held, and its Serving reports
`UpstreamDeployed=False` with reason `PipelineRequired`. A Preparation the
Serving promoted before passes, so that a rollback always works. The
promotion-pipeline example's prod requires staging:

{{< example file="promotion-pipeline/platform/environments.yaml" >}}

## The Environment local

Kokumi keeps the Environment `local` in its own namespace, so that Orders there
deploy to the cluster Kokumi runs in out of the box. It selects the Clusters of
its namespace labeled `delivery.kokumi.dev/in-cluster: "true"`, a label Kokumi
sets only on the Cluster `in-cluster` it keeps next to it, when it creates it.
Other namespaces get theirs from the example `local`, whose Cluster carries
the label, and an AppProject that admits them:

{{< tabs >}}
{{< tab name="environment.yaml" >}}
{{< example file="local/environment.yaml" >}}
{{< /tab >}}
{{< tab name="cluster.yaml" >}}
{{< example file="local/cluster.yaml" >}}
{{< /tab >}}
{{< /tabs >}}

{{< apply-example name="local" >}}
