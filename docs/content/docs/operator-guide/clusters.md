---
title: Clusters
weight: 6
description: Register the clusters Kokumi deploys to as Clusters with a kubeconfig Secret, and hand them to Argo CD.
---

Kokumi deploys to **Clusters**: one object per cluster, in the namespace whose
Orders deploy there. A Cluster says how to reach the cluster, and Kokumi reports
whether it can be deployed to and its Kubernetes version. Admins put Clusters
into [Environments](../environments/) of the same namespace by their labels, and
Orders deploy to Environments.

A Cluster reaches its cluster in one of two ways:

- **`Kubeconfig`**: with the kubeconfig in a Secret of the same namespace. This
  is how Kokumi reaches every other cluster.
- **`InCluster`**: the cluster Kokumi runs in, with Kokumi's own connection. It
  needs no Secret.

Kokumi still never applies manifests itself: it registers each Cluster an
Environment selects with Argo CD, and Argo CD deploys there.

## Workspaces

Every reference stays inside one namespace, as all of Kokumi's do: an Order
names an Environment of its namespace, an Environment selects Clusters of its
namespace, and a Cluster names a Secret of its namespace. A namespace with
Products, Orders, Environments and Clusters is a workspace:

- **Everything in one namespace**, such as `kokumi`, where Kokumi keeps the
  Cluster `in-cluster` and the Environment `local`.
- **A platform namespace** with the fleet's Clusters and Environments, and the
  Orders of the teams that deploy there.
- **Team namespaces** with clusters of their own.

One physical cluster is one Cluster: Argo CD knows a cluster by its server and
holds one credential for it. Teams that deploy to the same cluster share the
workspace that holds it. Of two Clusters for the same server, the first to
register it holds it and the other waits; see [Conflicts](#conflicts).

A Cluster's name is at most 63 characters: Kokumi puts it into labels of the
Argo CD Secrets and Applications it writes for the cluster.

## Register a cluster

Kokumi reaches a cluster as a ServiceAccount of its own there. Create it on
the cluster, with a token, as `argocd cluster add` does:

```bash
kubectl --context spoke create serviceaccount kokumi -n kube-system
kubectl --context spoke create clusterrolebinding kokumi \
  --clusterrole=cluster-admin --serviceaccount=kube-system:kokumi
kubectl --context spoke apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: kokumi-token
  namespace: kube-system
  annotations:
    kubernetes.io/service-account.name: kokumi
type: kubernetes.io/service-account-token
EOF
```

Grant less than `cluster-admin` where you can: Argo CD needs to manage what
the Orders deploy there, in their target namespaces. Then write a
self-contained kubeconfig of that ServiceAccount, with the server as Kokumi's
cluster reaches it:

```bash
server=https://spoke.example:6443
kubectl --context spoke get secret kokumi-token -n kube-system -o jsonpath='{.data.ca\.crt}' | base64 -d > ca.crt
token=$(kubectl --context spoke get secret kokumi-token -n kube-system -o jsonpath='{.data.token}' | base64 -d)
kubectl config --kubeconfig spoke.kubeconfig set-cluster spoke --server "$server" \
  --certificate-authority ca.crt --embed-certs
kubectl config --kubeconfig spoke.kubeconfig set-credentials kokumi --token "$token"
kubectl config --kubeconfig spoke.kubeconfig set-context spoke --cluster spoke --user kokumi
kubectl config --kubeconfig spoke.kubeconfig use-context spoke
```

The kubeconfig must hold everything itself, as Flux requires too: the server
and its CA, and a token or a client certificate and key, but no exec plugin,
no auth provider and no file paths. Store it in a Secret of the workspace,
under the key `value`, the format of Cluster API that Flux and Sveltos read as
well. Another key goes into `spec.kubeconfig.secretRef.key`.

```bash
kubectl create secret generic spoke-kubeconfig -n <namespace> --from-file=value=spoke.kubeconfig
```

And create the Cluster, with the labels Environments select it by:

{{< example file="multi-cluster/platform/cluster.yaml" >}}

The multi-cluster example applies it with the Environment that selects it; see
[Environments](../environments/):

{{< apply-example name="multi-cluster/platform" namespace="platform" >}}

For a local try, `make kind-spoke` creates a second Kind cluster, writes its
kubeconfig into the Secret `spoke-kubeconfig` of the namespace `platform`
(`KIND_SPOKE_NAMESPACE`), and prints the Cluster to apply.

A Cluster may only name a Secret its author may read: the admission policy
`kokumi-cluster-secret-access` refuses it otherwise, since the Cluster lends
the credential to every Order of its Environments. An update that keeps the
Secret and its key needs no access to the Secret; changing either does, since
another key could hold another credential.

In the UI, **Clusters → New Cluster** does the same in one form, from a
kubeconfig or from the server, its CA and a token, or registers the cluster
Kokumi runs in. As the signed-in user, it creates the Cluster first and then
the Secret `<name>-kubeconfig`, owned by the Cluster, so that the Secret is
deleted with it. A Secret of that name the UI did not create is refused before
anything is created.

### Secrets of Clusters

The built-in admin role may create Clusters in every namespace, but holds no
Secret access, because it is bound cluster-wide and Kokumi's signing key is a
Secret. Grant it the ClusterRole `kokumi-cluster-credentials` in each
namespace that holds Clusters with a kubeconfig:

```bash
kubectl -n platform create rolebinding kokumi-cluster-credentials \
  --clusterrole=kokumi-cluster-credentials \
  --serviceaccount=kokumi:kokumi-admin
```

Never bind it with a ClusterRoleBinding, in `kokumi-signing`, or in Kokumi's
own namespace, `kokumi`: there it would grant reading the session signing key
and the login Secrets. Keep remote clusters in a workspace of their own; the
Cluster `in-cluster` in `kokumi` needs no Secret. See
[Authorization](../authorization/#cluster-credentials).

## What Kokumi reports

```bash
kubectl get clusters.delivery.kokumi.dev -n <namespace>
# NAME     CONNECTION   SERVER                       VERSION   READY   REASON   AGE
# spoke    Kubeconfig   https://spoke.example:6443   v1.37.0   True    Ready    2m
```

Write the full name, or the short name `kcl`: on a management cluster with
Cluster API or Rancher, `kubectl get clusters` may list theirs.
`kubectl get kokumi -n <namespace>` lists every Kokumi object of a namespace.

A Cluster has one condition, **`Ready`**: whether Orders can deploy to it.
Kokumi checks every Cluster about once a minute, each a little later than the
minute at random so that the checks of many Clusters spread out, eight at a
time, and gives each check 10 seconds. A Cluster that reconciles for another
reason in between, such as a label change, reuses its last check while it is
less than half a minute old and the kubeconfig Secret is unchanged. All
in-cluster Clusters share one check of the cluster Kokumi runs in. The status
changes only when what the check finds changes.

| Reason | Meaning |
|---|---|
| `Ready` | The control plane answers ready at `/readyz`, and the Cluster is registered with Argo CD while an Environment selects it. |
| `SecretNotFound` | The kubeconfig Secret does not exist. |
| `SecretInvalid` | The Secret holds no self-contained kubeconfig under its key. |
| `Unreachable` | The control plane cannot be reached, or does not answer ready. |
| `Unauthorized` | The control plane rejects the credential. |
| `ClusterConflict` | Argo CD already knows its server: from another Cluster that registered it first, from a Secret Kokumi does not manage, or as the cluster Argo CD runs in; see [Conflicts](#conflicts). |
| `RegistrationFailed` | Kokumi could not write its registration in Argo CD's namespace. |

`status.server` and `status.kubernetesVersion` say where the cluster is and
what it runs. The UI shows the same state on the cluster, in the Environments
that select it, and on every Serving that deploys there.

## The cluster Kokumi runs in

Kokumi keeps the Cluster `in-cluster` (`connection: InCluster`) and the
Environment `local` that selects it in its own namespace. It creates them when
they are missing, also after you delete them, and does not change them
otherwise: label `in-cluster` and edit `local` as you need. To stop Orders in
`kokumi` from deploying to the cluster Kokumi runs in, change `local` rather
than deleting it.

When Kokumi creates the Cluster `in-cluster`, it labels it
`delivery.kokumi.dev/in-cluster: "true"`, and `local` selects it by that
label. Kokumi sets the label on no other Cluster, and never removes it. Other workspaces that deploy
to the cluster Kokumi runs in get a Cluster of their own, with the label, as
the example `local` adds one:

{{< example file="local/cluster.yaml" >}}

## Conflicts

One cluster is one Cluster: Argo CD holds one credential per server, so one
server is one registration. Kokumi keeps it that way:

- **The first Cluster to register a server holds it.** The cluster Secret is
  named after the server, so only one Cluster, of any namespace, can create
  it. Another Cluster that reaches the same server reports `ClusterConflict`,
  naming the Cluster that holds it, and is not registered. Once the holder
  lets go, because it is deleted, no Environment selects it any more, or its
  kubeconfig points at another server or cannot be used, a waiting Cluster
  takes the server over at once.
- **Kokumi does not take over clusters Argo CD already knows.** A cluster
  Secret in `argocd` that Kokumi did not write, with the same name or server,
  is left alone, and the Cluster reports `ClusterConflict`.
- **Argo CD's own cluster connects in-cluster.** A `Kubeconfig` Cluster whose
  server is `https://kubernetes.default.svc` reports `ClusterConflict`: its
  registration would replace the connection Argo CD uses for its own cluster,
  for every Application. Use `connection: InCluster` instead.

A Serving deploys to no Cluster that reports `ClusterConflict`: its target
there is held with `ClusterNotReady`. Of two Ready Clusters of one
Environment that connect to the same server, such as two in-cluster Clusters,
only the first by name deploys; the Serving holds the other's target with
`ClusterConflict`.

## How Kokumi hands a cluster to Argo CD

While an Environment that deploys through Argo CD selects a `Kubeconfig`
Cluster, Kokumi registers it with Argo CD in a declarative cluster Secret in
`argocd`, and deletes the Secret once no Environment selects the Cluster, or
when the Cluster is deleted. `InCluster` Clusters need none: Argo CD knows its
own cluster.

- **The Secret is named after the server:** `kokumi-<hash>`, the first 16 hex
  digits of the SHA-256 of the server URL without a trailing slash. It is
  labeled `app.kubernetes.io/managed-by: kokumi`, `delivery.kokumi.dev/cluster`
  and `delivery.kokumi.dev/cluster-namespace`, and Argo CD lists the cluster as
  `kokumi-<namespace>-<name>-<hash>`. When the kubeconfig points at another
  server, the registration moves to that server's Secret.
- **The credential is copied** from the kubeconfig: the token or client
  certificate, with the CA. Kokumi copies it again when the kubeconfig Secret
  changes. Whoever may read Secrets in Argo CD's namespace may read it, as
  with every cluster Argo CD knows.
- **Kokumi writes Secrets in Argo CD's namespace** with the ClusterRole
  `kokumi-argocd-cluster-registrar`: create, update and delete, nothing else.
  RBAC cannot limit that to Kokumi's Secrets by name, so the admission policy
  `kokumi-argocd-secret-integrity` does: in `argocd`, the controller may only
  create, change and delete Secrets labeled `app.kubernetes.io/managed-by: kokumi`
  and `argocd.argoproj.io/secret-type: cluster`, before and after. The last
  document of `install.yaml` binds the role in `argocd` only, so the
  installation needs Argo CD installed first; see
  [Installation](../../installation/#argo-cd).
- **Conflicts** are held; see [Conflicts](#conflicts).
- Kokumi's cluster Secrets are not scoped to an AppProject: several
  Environments with different projects may select one Cluster. Restrict where
  each project deploys with its destinations: Argo CD refuses an Application
  whose destination its project does not permit, and the Serving holds that
  target with `Rejected`.

Both Kokumi's manager and Argo CD must reach the API server of every cluster.
