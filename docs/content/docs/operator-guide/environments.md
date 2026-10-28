---
title: Environments
weight: 7
description: Group the Clusters of a namespace into Environments, and decide through which Argo CD project Kokumi deploys there.
---

An **Environment** is a group of [Clusters](../clusters/) for the Orders of
its namespace to deploy to, such as dev, staging or the production fleet. Each
decides:

- which Clusters of its namespace it covers, by their labels;
- the engine that deploys there, Argo CD, and its AppProject.

The example `local` gives a namespace the cluster Kokumi runs in, as a
Cluster and the Environment that selects it:

{{< tabs >}}
{{< tab name="environment.yaml" >}}
{{< example file="local/environment.yaml" >}}
{{< /tab >}}
{{< tab name="cluster.yaml" >}}
{{< example file="local/cluster.yaml" >}}
{{< /tab >}}
{{< /tabs >}}

{{< apply-example name="local" >}}

## Clusters

`clusterSelector` selects Clusters of the Environment's namespace by their
labels; `{}` selects every one. A Cluster joins or leaves an Environment when
its labels change:

```bash
kubectl label cluster spoke -n <namespace> fleet=edge
kubectl label cluster spoke -n <namespace> fleet-
```

The status counts the Clusters it selects, and those of them that are ready,
in `status.clusters`; its `Ready` message names the first ten. Each Cluster
reports on its own whether Kokumi reaches it:

```bash
kubectl get environments.delivery.kokumi.dev -n <namespace> -o wide
# NAME   DEPLOYER   PROJECT   CLUSTERS   READY CLUSTERS   READY   REASON   AGE
# edge   ArgoCD     default   2          2                True    Ready    5m
kubectl get clusters.delivery.kokumi.dev -n <namespace> -l fleet=edge
```

The short name of Environments is `env`. One Cluster does not make the
Environment unready: its `Ready` condition is False only when it selects no
Cluster or its engine does not admit it.

| Reason | Meaning |
|---|---|
| `Ready` | It selects Clusters, and its engine admits it. |
| `NoClusters` | It selects no Cluster of its namespace. |
| `InvalidSelector` | Its `clusterSelector` is not a valid label selector. |
| `ProjectNotFound` | Its Argo CD AppProject does not exist. |
| `ProjectNotPermitted` | Its AppProject does not admit its namespace in `sourceNamespaces`. |
| `ArgoCDNotInstalled` | Argo CD's CRDs are not installed. |

## Who may deploy where

The built-in editor and viewer roles read Environments and Clusters; the
admin role creates and changes them.

The boundary of a workspace is the Argo CD AppProject its Environments name,
Argo CD's own model for teams:

- **`spec.sourceNamespaces`** decides which namespaces' Environments may use
  the project. Kokumi applies Argo CD's rule for Applications outside its
  control-plane namespace: an Environment in Kokumi's own namespace may use
  any project, one in another namespace only a project that admits its
  namespace; otherwise it reports `ProjectNotPermitted`. Argo CD's `default`
  project admits no namespace. Set the list where a project has none, and add
  to it where it has one:

  ```bash
  kubectl -n argocd patch appproject default --type=merge \
    -p '{"spec":{"sourceNamespaces":["platform","team-*"]}}'
  kubectl -n argocd patch appproject default --type=json \
    -p '[{"op":"add","path":"/spec/sourceNamespaces/-","value":"shop"}]'
  ```

- **`spec.destinations`** decides which servers and namespaces Argo CD may
  deploy into through the project.

## The engine

`deployer` names the engine that deploys to the Environment's Clusters.
Today it is Argo CD, through the project `deployer.argoCD.project`. Kokumi
asks it whether it admits the Environment: Argo CD's CRDs must be installed,
and the AppProject must exist and, for an Environment outside Kokumi's
namespace, admit the namespace in `sourceNamespaces`. A change of the
Environment or of the AppProject counts at once.

## The Environment local

Kokumi keeps the Environment `local` in its own namespace. It creates it when
it is missing, also after you delete it, and does not change it otherwise. It
selects the Clusters of its namespace labeled
`delivery.kokumi.dev/in-cluster: "true"`, a label Kokumi sets only on the
Cluster `in-cluster` it keeps next to it, when it creates it. Other namespaces
get theirs from the example `local` above, whose Cluster carries the label,
and an AppProject that admits them.
