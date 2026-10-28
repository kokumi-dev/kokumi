---
title: Environments
weight: 8
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
kubectl label clusters.delivery.kokumi.dev spoke -n <namespace> fleet=edge
kubectl label clusters.delivery.kokumi.dev spoke -n <namespace> fleet-
```

A Cluster that leaves gets no new deployments, and Kokumi leaves what runs
there in place; see
[What runs where](../../user-guide/environments/#what-runs-where). Remove it
by hand when you mean to.

The status counts the Clusters it selects, and those of them that are ready,
in `status.clusters`; its `Ready` message names the first ten. Each Cluster
reports on its own whether Orders can deploy to it:

```bash
kubectl get environments.delivery.kokumi.dev -n <namespace> -o wide
# NAME   DEPLOYER   PROJECT   CLUSTERS   READY CLUSTERS   READY   REASON   AGE
# edge   ArgoCD     default   2          2                True    Ready    5m
kubectl get clusters.delivery.kokumi.dev -n <namespace> -l fleet=edge
```

The short name of Environments is `env`. Its `Ready` condition says whether
the Environment is set up so that Orders may deploy there: it selects a
Cluster, and its engine admits it. It does not follow the readiness of its
Clusters: an Environment none of whose Clusters is Ready is still `Ready`,
and only `READY CLUSTERS` shows it. Its Orders then deploy nowhere; see
[What runs where](../../user-guide/environments/#what-runs-where).

| Reason | Meaning |
|---|---|
| `Ready` | It selects a Cluster, and its engine admits it. |
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
  `ProjectNotPermitted`, and no Application is written. Kokumi matches each
  entry as a glob, with Go's `path.Match`: `*`, `?` and `[…]`, such as
  `team-*`. It reads no entry as a regular expression (`/…/`) or a negation
  (`!…`): such an entry admits no namespace for Kokumi, whatever Argo CD makes
  of it, so list the namespaces as globs. Argo CD's `default` project admits
  no namespace. Set the list where a project has none, and add to it where it
  has one:

  ```bash
  kubectl -n argocd patch appproject default --type=merge \
    -p '{"spec":{"sourceNamespaces":["platform","team-*"]}}'
  kubectl -n argocd patch appproject default --type=json \
    -p '[{"op":"add","path":"/spec/sourceNamespaces/-","value":"shop"}]'
  ```

- **`spec.destinations`** decides which servers and namespaces an Order may
  deploy into. Argo CD checks it: it refuses an Application whose destination
  the project does not permit; see
  [What runs where](../../user-guide/environments/#what-runs-where) for how
  the Serving reports it.

## The engine

`deployer` names the engine that deploys to the Environment's Clusters.
Today it is Argo CD: every Order deploys to each Cluster through an Argo CD
Application of its own, in the project `deployer.argoCD.project`, into the
Order's target namespace; see
[The namespace on the clusters](../../user-guide/environments/#the-namespace-on-the-clusters).

Whenever a Serving deploys, it asks the engine whether the Order may deploy
there. For Argo CD, its CRDs must be installed, and the AppProject must exist
and, for an Environment outside Kokumi's namespace, admit the namespace in
`sourceNamespaces`. A change of the Environment or of the AppProject counts
at once. For what the Serving reports when an Order may not deploy at all,
see [Where an Order deploys](../../user-guide/environments/#where-an-order-deploys).

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

## In the UI

**Environments** lists the Environments of every namespace you may read.
Each has the tabs Overview, Clusters and Orders. Admins create and edit them;
the cluster selector previews which Clusters of the namespace it selects.
