---
title: Clusters
weight: 6
description: Register clusters as Clusters with a kubeconfig Secret, and see whether Kokumi reaches them.
---

A **Cluster** describes one cluster for Kokumi to deploy to: how to reach it.
Kokumi reports whether it reaches the cluster, and the cluster's Kubernetes
version.

A Cluster reaches its cluster in one of two ways:

- **`Kubeconfig`**: with the kubeconfig in a Secret of the same namespace. This
  is how Kokumi reaches every other cluster.
- **`InCluster`**: the cluster Kokumi runs in, with Kokumi's own connection. It
  needs no Secret.

## Workspaces

Every reference stays inside one namespace, as all of Kokumi's do: a Cluster
names a Secret of its namespace. A namespace with Clusters is a workspace:

- **Kokumi's own namespace**, `kokumi`, where Kokumi keeps the Cluster
  `in-cluster`.
- **A platform namespace** with the fleet's Clusters.
- **Team namespaces** with clusters of their own.

A Cluster's name is at most 63 characters, so that it fits into a label
value.

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

Grant less than `cluster-admin` where you can: what deploys there needs to
manage the workloads in their target namespaces. Then write a self-contained
kubeconfig of that ServiceAccount, with the server as Kokumi's cluster reaches
it:

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

And create the Cluster:

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Cluster
metadata:
  name: spoke
spec:
  description: The spoke cluster of the platform team
  connection: Kubeconfig
  kubeconfig:
    secretRef:
      name: spoke-kubeconfig
```

A Cluster may only name a Secret its author may read: the admission policy
`kokumi-cluster-secret-access` refuses it otherwise, since Kokumi reads the
Secret with its own identity. An update that keeps the Secret and its key
needs no access to the Secret; changing either does, since another key could
hold another credential.

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

A Cluster has one condition, **`Ready`**: whether Kokumi reaches it. Kokumi
checks every Cluster about once a minute, each a little later than the minute
at random so that the checks of many Clusters spread out, eight at a time, and
gives each check 10 seconds. A Cluster that reconciles for another reason in
between reuses its last check while it is less than half a minute old and the
kubeconfig Secret is unchanged. All in-cluster Clusters share one check of the
cluster Kokumi runs in. The status changes only when what the check finds
changes.

| Reason | Meaning |
|---|---|
| `Ready` | The control plane answers ready at `/readyz`. |
| `SecretNotFound` | The kubeconfig Secret does not exist. |
| `SecretInvalid` | The Secret holds no self-contained kubeconfig under its key. |
| `Unreachable` | The control plane cannot be reached, or does not answer ready. |
| `Unauthorized` | The control plane rejects the credential. |

`status.server` and `status.kubernetesVersion` say where the cluster is and
what it runs.

## The cluster Kokumi runs in

Kokumi keeps the Cluster `in-cluster` (`connection: InCluster`) in its own
namespace. It creates it when it is missing, also after you delete it, and
does not change it otherwise: label it as you need.

When Kokumi creates the Cluster `in-cluster`, it labels it
`delivery.kokumi.dev/in-cluster: "true"`. Kokumi sets the label on no other
Cluster, and never removes it. Another workspace that deploys to the cluster
Kokumi runs in gets a Cluster of its own with `connection: InCluster`.

Kokumi's manager must reach the API server of every cluster.
