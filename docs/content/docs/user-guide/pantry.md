---
title: Pantry
weight: 1
description: Connect Kokumi to private OCI registries with Pantry credentials.
---

A **Pantry** is a namespaced resource that tells Kokumi about an OCI registry
and how to authenticate with it. When an Order's source or destination lives in
a private registry, you reference a Pantry. The OCI URL stays on the Order,
and the Pantry supplies the credentials.

## How the Pantry controller works

Whenever a Pantry is created or updated (or its referenced Secret changes),
the controller:

1. Reads the credentials from the referenced Secret (if any).
2. Pings the registry to verify connectivity.
3. Sets a `Ready` condition on the Pantry's status.

```bash
kubectl get pantries -n kokumi
# NAME           REGISTRY                READY   REASON                   AGE
# ghcr.          oci://ghcr.io           True    Ready                    2m
# quay.          oci://quay.io           False   ConnectivityCheckFailed  1m
```

## Creating a Pantry

### Public registry (no credentials)

For a public registry, create a Pantry with only a `registry` field. No Secret
is required. Kokumi accesses the registry anonymously.

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Pantry
metadata:
  name: ghcr-public
  namespace: kokumi
spec:
  registry: oci://ghcr.io
  description: GitHub Container Registry (public images)
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
  registry: oci://ghcr.io
  secretRef:
    name: ghcr-creds
  description: GitHub Container Registry (private images)
```

Apply it and verify the `Ready` condition:

```bash
kubectl apply -f pantry.yaml
kubectl get pantry ghcr-private -n kokumi
# NAME           REGISTRY          READY   REASON   AGE
# ghcr-private   oci://ghcr.io     True    Ready    10s
```

If the Secret is wrong or the registry is unreachable, the `Ready` column shows
`False` with a `ConnectivityCheckFailed` reason.

## Using a Pantry in an Order

A Pantry is an **auth hint** to tell Kokumi which credentials to use when
pulling or pushing an OCI artifact. The OCI URL on the Order is always required.
The Pantry only provides the credentials for that URL.

Kokumi validates that the Pantry's `spec.registry` host matches the host in the
Order's OCI URL. A mismatch is rejected immediately during reconciliation.

### Authenticated source

Set `spec.source.pantryRef` to authenticate the pull:

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Order
metadata:
  name: my-private-app
spec:
  source:
    oci: oci://ghcr.io/my-org/my-app
    version: "1.2.3"
    pantryRef:
      name: ghcr-private
      namespace: kokumi    # optional: defaults to the Order's own namespace
```

### Authenticated destination

To push the rendered artifact to a private registry instead of the default
in-cluster registry, set both `spec.destination.oci` and
`spec.destination.pantryRef`:

```yaml
spec:
  destination:
    oci: oci://ghcr.io/my-org/rendered-output
    pantryRef:
      name: ghcr-private
      namespace: kokumi
```

If `spec.destination.oci` is omitted entirely, Kokumi pushes to the in-cluster
registry, no Pantry is needed in that case.

## Pantry spec reference

| Field | Required | Description |
|---|---|---|
| `spec.registry` | Yes | Base OCI registry URL, prefixed with `oci://` (e.g. `oci://ghcr.io`) |
| `spec.secretRef.name` | No | Name of a `kubernetes.io/dockerconfigjson` Secret in the same namespace |
| `spec.description` | No | Human-readable description of the Pantry |

The Secret referenced by `secretRef` must live in the **same namespace** as the
Pantry. Kokumi reads the `.dockerconfigjson` key to build registry credentials.
If the key is absent, the registry is accessed anonymously and a warning is logged.

## Namespace resolution

When `pantryRef.namespace` is omitted on an Order's source or destination,
Kokumi looks for the Pantry in the Order's own namespace. To share a single
Pantry across multiple namespaces, set the namespace explicitly on each Order.
