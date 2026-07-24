---
title: Pantry
weight: 1
description: Give Orders a named OCI location with optional credentials.
---

A **Pantry** is a namespaced resource that combines a full OCI URL with optional
registry credentials. Instead of specifying a raw OCI URL directly on every
Order, you create a Pantry once and reference it by name.

When an Order uses `pantryRef` as its source or destination, Kokumi reads the
Pantry's `spec.url` as the OCI location and uses its credentials for
authentication. This means `oci` and `pantryRef` are **mutually exclusive** on
any source or destination — you use one or the other, never both.

## How the Pantry controller works

Whenever a Pantry is created or updated (or its referenced Secret changes),
the controller:

1. Reads the credentials from the referenced Secret (if any).
2. Pings the registry hostname to verify connectivity.
3. Sets a `Ready` condition on the Pantry's status.

```bash
kubectl get pantries -n kokumi
# NAME           URL                                    READY   REASON                   AGE
# podinfo        oci://ghcr.io/stefanprodan/charts      True    Ready                    2m
# my-app         oci://quay.io/my-org/charts/myapp      False   ConnectivityCheckFailed  1m
```

## Creating a Pantry

### Public registry (no credentials)

For a public registry, create a Pantry with only a `url` field. No Secret
is required. Kokumi accesses the registry anonymously.

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Pantry
metadata:
  name: podinfo-charts
  namespace: kokumi
spec:
  url: oci://ghcr.io/stefanprodan/charts/podinfo
  description: Podinfo Helm chart (public)
```

### Private registry

For a private registry, first create a Kubernetes `kubernetes.io/dockerconfigjson`
Secret containing the credentials, then reference it from the Pantry.

Create the Secret using `kubectl`:

```bash
kubectl create secret docker-registry ghcr-creds \
  --namespace kokumi \
  --docker-server=ghcr.io \
  --docker-username=<your-github-username> \
  --docker-password=<your-token>
```

Then create the Pantry:

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Pantry
metadata:
  name: ghcr-private
  namespace: kokumi
spec:
  url: oci://ghcr.io/my-org/charts/my-app
  secretRef:
    name: ghcr-creds
  description: Private app chart on GitHub Container Registry
```

Apply it and verify the `Ready` condition:

```bash
kubectl apply -f pantry.yaml
kubectl get pantry ghcr-private -n kokumi
# NAME           URL                                    READY   REASON   AGE
# ghcr-private   oci://ghcr.io/my-org/charts/my-app    True    Ready    10s
```

If the Secret is wrong or the registry is unreachable, the `Ready` column shows
`False` with a `ConnectivityCheckFailed` reason.

## Using a Pantry in an Order

A Pantry replaces the OCI URL on an Order's source or destination — it provides
both the URL **and** the credentials. `oci` and `pantryRef` are mutually
exclusive.

### Pantry as source

Use `spec.source.pantryRef` to pull the artifact from the Pantry's URL:

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Order
metadata:
  name: my-private-app
  namespace: kokumi
spec:
  source:
    pantryRef:
      name: ghcr-private
    version: "1.2.3"
```

### Direct OCI source (no Pantry)

For a public or already-accessible registry, set `spec.source.oci` directly:

```yaml
spec:
  source:
    oci: oci://ghcr.io/my-org/my-app
    version: "1.2.3"
```

### Pantry as destination

To push rendered artifacts to a Pantry's URL, use `spec.destination.pantryRef`:

```yaml
spec:
  destination:
    pantryRef:
      name: ghcr-private
```

If `spec.destination` is omitted entirely, Kokumi pushes to the in-cluster
registry. No Pantry is needed in that case.

## Pantry spec reference

| Field | Required | Description |
|---|---|---|
| `spec.url` | Yes | Full OCI URL including path, prefixed with `oci://` (e.g. `oci://ghcr.io/my-org/charts/app`) |
| `spec.secretRef.name` | No | Name of a `kubernetes.io/dockerconfigjson` Secret in the same namespace |
| `spec.description` | No | Human-readable description of the Pantry |

The Secret referenced by `secretRef` must live in the **same namespace** as the
Pantry. Kokumi reads the `.dockerconfigjson` key to build registry credentials.
If the key is absent, the registry is accessed anonymously and a warning is logged.

