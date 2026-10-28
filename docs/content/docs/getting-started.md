---
title: Getting Started
weight: 1
description: Install Kokumi, render a Helm chart, promote it, and roll it back.
---

This tutorial renders the [podinfo](https://github.com/stefanprodan/podinfo)
Helm chart with Kokumi, promotes the result to the cluster Kokumi runs in,
changes it, and rolls the change back. It takes about ten minutes on a test
cluster such as [kind](https://kind.sigs.k8s.io/).

## Install

You need a Kubernetes cluster ≥ 1.30 with a default StorageClass, and
`kubectl`. Install Argo CD, register the bundled registry with it, install
Kokumi, and turn on the admin login, as [Installation](../installation/)
explains:

```bash
kubectl create namespace argocd
kubectl apply -n argocd --server-side --force-conflicts \
    -f https://raw.githubusercontent.com/argoproj/argo-cd/{{< param argoCDVersion >}}/manifests/install.yaml

kubectl -n argocd create secret generic manifests-registry-creds \
    --from-literal=type=oci \
    --from-literal=url=oci://kokumi-registry.kokumi.svc.cluster.local:5000 \
    --from-literal=insecureOCIForceHttp=true \
    --dry-run=client -o yaml | \
kubectl label -f - argocd.argoproj.io/secret-type=repo-creds --local -o yaml | \
kubectl apply -f -

kubectl apply -f \
    https://github.com/kokumi-dev/kokumi/releases/download/{{< param kokumiVersion >}}/install.yaml
kubectl -n kokumi rollout status deployment/kokumi-controller-manager

kubectl -n kokumi patch kitchen default --type=merge \
    -p '{"spec":{"auth":{"adminUser":{"enabled":true}}}}'
```

## Let a namespace deploy

Orders deploy to an Environment of their own namespace. Kokumi keeps the
Environment `local`, the cluster it runs in, in its own namespace `kokumi`;
the example `local` adds the same to another namespace. Argo CD's project
`default` must also admit the namespace in its `sourceNamespaces`:

```bash
kubectl create namespace shop
kubectl -n argocd patch appproject default --type=merge -p '{"spec":{"sourceNamespaces":["shop"]}}'
```

{{< apply-example name="local" >}}

The patch replaces the list; where the project lists namespaces already, add
`shop` to it instead. See
[Environments](../operator-guide/environments/#who-may-deploy-where).

## Create a Product and an Order

A **Product** defines the application: here, the podinfo chart with a base
value, and which values its Orders may change.

{{< example file="getting-started/product.yaml" >}}

An **Order** is one variant of it, and sets only what differs:

{{< example file="getting-started/order.yaml" >}}

Apply both:

{{< apply-example name="getting-started" >}}

## Watch the Preparation

Kokumi merges the Order over its Product, renders the chart, and pushes the
result to the bundled registry as an immutable, signed **Preparation**:

```bash
kubectl get preparations -n shop --watch
# NAME                   ORDER     READY   REASON   APPROVED              AGE
# podinfo-d7ce0c46a686   podinfo   True    Ready    ApprovalNotRequired   20s
```

The Order is `Manual`, so nothing deploys yet.

## Promote it

Open the UI:

```bash
kubectl port-forward -n kokumi svc/kokumi-server 8080:80
```

Sign in at [http://localhost:8080](http://localhost:8080) as `admin` with the
password `admin`. Open the Order `podinfo`, look through the rendered files of
its Preparation, and click **Promote**. The promotion creates the Order's
**Serving**, which deploys the Preparation through an Argo CD Application:

```bash
kubectl get servings -n shop
# NAME      ORDER     TARGET                 READY   REASON     APPROVED              VERIFIED             AGE
# podinfo   podinfo   podinfo-d7ce0c46a686   True    Deployed   ApprovalNotRequired   SignaturesVerified   30s

kubectl get deployments -n shop
# NAME      READY   UP-TO-DATE   AVAILABLE   AGE
# podinfo   2/2     2            2           30s
```

## Change it and roll back

Scale the Order to three replicas, with a message that describes the
change:

```bash
kubectl annotate order podinfo -n shop delivery.kokumi.dev/commit-message="Scale to 3 replicas"
kubectl patch order podinfo -n shop --type=merge -p '{"spec":{"values":{"replicaCount":3}}}'
```

A second Preparation appears, with the first one as its parent. In the UI,
its **Changes** tab shows the difference to the previous Preparation; promote
it, and podinfo runs three replicas. To roll
back, promote the first Preparation again. Its artifact is still in the
registry, so nothing is rendered again.

## Clean up

```bash
kubectl delete namespace shop
```

Deleting the namespace deletes the Order with its Preparations and Serving,
the Serving's Argo CD Application, and the workload in the namespace.

## Next steps

{{< cards >}}
  {{< card link="../user-guide/products-and-orders" title="Products and Orders" icon="collection" subtitle="Sources, values, patches, and what Orders may change." >}}
  {{< card link="../user-guide/promotion" title="Promotion and Rollback" icon="refresh" subtitle="Automatic and Manual promotion, gates, and queued promotions." >}}
  {{< card link="../user-guide/approvals" title="Approvals" icon="user-group" subtitle="Require reviews before a Preparation deploys." >}}
  {{< card link="../user-guide/promotion-pipelines" title="Promotion Pipelines" icon="chevron-double-right" subtitle="Deploy a Product change stage by stage." >}}
  {{< card link="../architecture" title="Architecture" icon="cube-transparent" subtitle="The ideas behind Kokumi and how its parts fit." >}}
{{< /cards >}}
