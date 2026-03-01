---
title: Getting Started
weight: 1
description: Install kokumi and deploy your first Recipe in minutes.
---

## Prerequisites

- A Kubernetes cluster ≥ 1.26 with `kubectl` configured

## Install kokumi

```bash
kubectl apply -f https://github.com/kokumi-dev/kokumi/releases/download/0.4.0/install.yaml
```

Verify the manager is running:

```bash
kubectl get pods -n kokumi
# NAME                                READY   STATUS    RESTARTS   AGE
# kokumi-controller-manager-xxx       1/1     Running   0          30s
```

## Create your first Recipe

A **Recipe** declares where to pull rendered manifests from and how to patch them before producing an immutable artifact.

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Recipe
metadata:
  name: external-secrets
spec:
  source:
    oci: oci://kokumi-registry.kokumi.svc.cluster.local:5000/recipe/external-secrets
    version: "0.1.0"

  patches:
    - target:
        kind: Deployment
        name: external-secrets-webhook
      set:
        .spec.replicas: "3"

  destination:
    oci: oci://kokumi-registry.kokumi.svc.cluster.local:5000/preparation/external-secrets
```

Apply it:

```bash
kubectl apply -f recipe.yaml
```

## Watch a Preparation being created

Kokumi reconciles the Recipe and produces an immutable **Preparation**:

```bash
kubectl get preparations --watch
# NAME                         RECIPE              STATUS   AGE
# external-secrets-a1b2c3      external-secrets    Ready    5s
```

## Activate with a Serving

A **Serving** selects which Preparation is actively deployed. There is exactly one Serving per Recipe.

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Serving
metadata:
  name: external-secrets
spec:
  recipe: external-secrets
  preparation: external-secrets-a1b2c3
```

```bash
kubectl apply -f serving.yaml
kubectl get servings
# NAME               RECIPE             PREPARATION                STATUS   AGE
# external-secrets   external-secrets   external-secrets-a1b2c3   Active   10s
```

To roll back, update `spec.preparation` to any previous Preparation name and re-apply.

## Next steps

{{< cards >}}
  {{< card link="../installation" title="Installation" icon="download" subtitle="Version pinning and upgrade guide." >}}
  {{< card link="../architecture" title="Architecture" icon="cube-transparent" subtitle="How reconciliation works under the hood." >}}
{{< /cards >}}
