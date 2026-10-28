---
title: Registries and Repositories
weight: 1
description: Connect Kokumi to private OCI registries, and see which credentials every location uses.
---

Kokumi reads and writes OCI artifacts in many places: the source of a Product,
its vendored copy, the container images of the rendered manifests, and the
destination of every Order. Two kinds of object decide how it gets there:

- A **Registry** is a connection: a URL prefix, the credentials and TLS
  settings for everything below it, and the scheme. It is the only place
  credentials come from.
- A **Repository** is a catalog entry: an OCI repository you browse in the UI
  and create Products from. Nothing renders through it.

## Which credentials a location uses

Products and Orders list the Registries they connect through in
`spec.registryRefs`. Each location uses the Registries of its **owner**:

| Location | Owner | Accessed by |
|---|---|---|
| `Product.spec.source.url` | Product | Kokumi pulls it |
| `Product.spec.vendor.url`, the vendored source | Product | Kokumi pushes it, Orders pull it |
| The upstream container images and their mirrors | Product | Kokumi resolves and copies them |
| `<vendor url>/images/**`, the vendored images (`images.policy: Vendor`) | Product | Kokumi pushes them, the kubelet pulls them |
| `Order.spec.destination.url`, the Preparations | Order | Kokumi pushes them, Argo CD pulls them |

Of the owner's Registries, the one whose `url` covers the location with the
longest path wins. Paths match at `/` boundaries, so `oci://ghcr.io/myorg`
covers `ghcr.io/myorg/app` but not `ghcr.io/myorganization`. Ports must match
exactly, and `docker.io`, `index.docker.io` and `registry-1.docker.io` are one
host. When no listed Registry covers a location, Kokumi accesses it
anonymously over HTTPS; the bundled in-cluster registry is the one location
it accesses over HTTP without a Registry.

The controller creates the Registry `in-cluster` for the bundled registry in
its own namespace when it starts and the Registry is missing, so the
Registries list shows the bundled registry and whether it answers. Changes to
it are kept; a deleted one comes back on the next start. Registries are
namespaced: objects in other namespaces cannot list it and reach the bundled
registry through the built-in rule above.

For example, with a private chart and a private destination on GitHub:

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Product
metadata:
  name: mychart
  namespace: team-a
spec:
  source:
    url: oci://ghcr.io/myorg/mychart
    version: "1.2.3"
  vendor:
    url: oci://ghcr.io/myorg/vendor/mychart
  images:
    policy: Vendor
  registryRefs:
  - name: ghcr-myorg          # oci://ghcr.io/myorg: source, vendor, vendored images
  - name: dockerhub           # oci://docker.io: upstream images, against rate limits
---
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Order
metadata:
  name: mychart-prod
  namespace: team-a
spec:
  productRef:
    name: mychart
  destination:
    url: oci://ghcr.io/myorg/mychart-rendered
  registryRefs:
  - name: ghcr-myorg-push     # oci://ghcr.io/myorg/mychart-rendered: a token that may push
```

- The chart `ghcr.io/myorg/mychart` is pulled through `ghcr-myorg`.
- It is copied to `ghcr.io/myorg/vendor/mychart` through `ghcr-myorg`.
- An image such as `docker.io/library/nginx` is resolved through `dockerhub`
  and copied to `ghcr.io/myorg/vendor/mychart/images/docker.io/library/nginx`
  through `ghcr-myorg`. All Orders of the Product share these image
  repositories.
- The Preparations are pushed to `ghcr.io/myorg/mychart-rendered` through
  `ghcr-myorg-push`. The Order's Registries never authenticate the source or
  the images, and the Product's never authenticate the destination.

### What each object needs: `status.registryAccess`

Products and Orders report every location they need in
`status.registryAccess`: the URL prefix, `Pull` or `Push`, who accesses it,
and the Registry Kokumi connects through. Kokumi gives no credentials to Argo
CD and the kubelet; their entries tell you where they need their own, for
example an Argo CD repository credential or an `imagePullSecret`.

```bash
kubectl -n team-a get order mychart-prod -o jsonpath='{.status.registryAccess}' | jq
```

```json
[
  { "url": "oci://docker.io", "operation": "Pull", "client": "Kokumi", "registry": "dockerhub" },
  { "url": "oci://ghcr.io/myorg/mychart-rendered", "operation": "Pull", "client": "ArgoCD" },
  { "url": "oci://ghcr.io/myorg/mychart-rendered", "operation": "Push", "client": "Kokumi", "registry": "ghcr-myorg-push" },
  { "url": "oci://ghcr.io/myorg/vendor/mychart", "operation": "Pull", "client": "Kokumi", "registry": "ghcr-myorg" },
  { "url": "oci://ghcr.io/myorg/vendor/mychart/images", "operation": "Pull", "client": "Kubelet" },
  { "url": "oci://ghcr.io/myorg/vendor/mychart/images", "operation": "Push", "client": "Kokumi", "registry": "ghcr-myorg" }
]
```

The UI shows the same list on the overview of a Product and an Order. A
Preparation records the Registry it was pushed through in
`spec.artifact.registryRef`; Kokumi reads the artifact through it later, for
example to verify a promotion.

### Private content stays private

Kokumi refuses to copy content it read with credentials to a location it
writes without credentials, where anyone could read it:

- a private source vendored to a vendor location without a Registry,
- a render of a private source pushed to a destination without a Registry,
- a private image vendored below a vendor location without a Registry.

The Product or Order then reports `Ready=False` with reason
`CredentialsRequired` and renders nothing. List a Registry with credentials for
the target location, or vendor elsewhere. Content counts as private when it
was read with credentials and an anonymous read of the same digest fails, so
a public image read through a Docker Hub Registry is not blocked.

## Creating a Registry

Create a `kubernetes.io/dockerconfigjson` Secret with the credentials, then a
Registry that names it. The private-registry example connects to
`ghcr.io/my-org`:

```bash
kubectl create secret docker-registry my-org-credentials \
  --namespace shop \
  --docker-server=ghcr.io \
  --docker-username=<user> \
  --docker-password=<token>
```

{{< example file="private-registry/registry.yaml" >}}

A Product lists the Registries it connects through:

{{< example file="private-registry/product.yaml" >}}

The render of a private source must stay private too (see
[Private content stays private](#private-content-stays-private)), so its Order
pushes the Preparations below the same Registry:

{{< example file="private-registry/order.yaml" >}}

Replace `ghcr.io/my-org` in the example with your registry, then apply it:

{{< apply-example name="private-registry" >}}

The entry of the Secret is chosen the way the kubelet chooses one in an image
pull Secret: the key's host and port must match the Registry's, a `*` stands
for one DNS label, and of the keys whose path is a prefix of the Registry's
URL the most specific wins. A Secret without a matching entry is an error,
never an anonymous fallback.

The controller checks the Secrets and then calls the registry's API base
endpoint, `GET /v2/`, with the credentials:

```bash
kubectl -n shop get registries
# NAME     URL                    READY   REASON         AGE
# my-org   oci://ghcr.io/my-org   True    Connected      12s
# harbor   oci://harbor.corp      False   Unauthorized   3m
```

| Reason | Meaning |
|---|---|
| `Connected` | The registry accepted the credentials and TLS settings. Without credentials: the registry answered and does not require basic credentials; a token registry such as ghcr.io decides about anonymous access per repository. Neither proves access to every path below `url`. |
| `SecretNotFound` | A Secret the Registry names does not exist. |
| `SecretInvalid` | The credentials Secret is not a `kubernetes.io/dockerconfigjson` Secret with an entry for the URL. |
| `CertificateInvalid` | The TLS Secret holds no usable certificate. |
| `Unauthorized` | The registry rejected the credentials, or requires basic credentials the Registry lacks. |
| `Unreachable` | DNS, TLS or the connection failed. |

The Registry is checked again when it or its Secrets change, and periodically.
A Product or Order that lists a Registry that does not exist reports
`RegistryNotFound`.

### Private CAs, client certificates and plain HTTP

```yaml
spec:
  url: oci://registry.corp.example:8443/platform
  secretRef:
    name: corp-registry
  certSecretRef:
    name: corp-registry-tls   # ca.crt; tls.crt and tls.key for a client certificate
---
spec:
  url: oci://zot.registry.svc.cluster.local:5000
  scheme: HTTP                # credentials and content travel unencrypted
```

`ca.crt` is trusted in addition to the system roots. A CA that every registry
of the platform uses belongs into the trust store of the controller and server
images instead (see [Installation](../../installation/)). Kokumi never infers
plain HTTP from a host name: only `scheme: HTTP` or the bundled registry use
it.

### Who may name a Secret

A Registry lends its credentials to every Product and Order in its namespace
that lists it, and Kokumi reads them with its own identity. The admission
policy `kokumi-registry-secret-access` therefore only admits a Registry that
names a Secret its author may `get`. Changing only other fields of an existing
Registry needs no Secret access; changing its `url` or `scheme` does, because
both send the credentials somewhere else.

### Credentials from the UI

The Registry form stores a username and password in a
`kubernetes.io/dockerconfigjson` Secret named `<registry>-credentials`, owned
by the Registry and deleted with it. It never overwrites a Secret of that name
it did not create. Storing credentials, naming an existing Secret, and reading
through a Registry in the UI all need Secret access in the namespace, which a
platform admin grants by binding the `kokumi-registry-credentials` ClusterRole
(see [Authorization](../../operator-guide/authorization/#registry-credentials)).

## Repositories

A Repository adds an OCI repository to the catalog of the UI:

{{< example file="private-registry/repository.yaml" >}}

Its `Ready` condition says whether the Registry exists (`RegistryNotFound`)
and covers the URL (`NotCovered`); it makes no network calls.

The **Tags** tab of a Repository lists its tags. Expand a tag to read the
chart's README and values or the manifests, and open **Structure** to see the
artifact behind the tag and everything attached to it, such as signatures,
SBOMs and attestations. Tags that cosign uses to store signatures
(`sha256-<digest>.sig`, `.att`) appear in the graph of the artifact they
belong to. See [Artifacts in the registry](../artifacts/).

**Create Product** on a Repository, or on one of its tags, opens a new Product
with the source URL, the version and the Registry filled in.

The UI reads a Repository through its Registry with your identity: without
`get` on the Registry's Secret you see a 403 that names the Secret, never an
anonymous result.

## Reference

| Registry field | Required | Description |
|---|---|---|
| `spec.url` | Yes | `oci://host[:port][/path]` without a tag, digest or the port 443 |
| `spec.secretRef.name` | No | A `kubernetes.io/dockerconfigjson` Secret in the same namespace |
| `spec.certSecretRef.name` | No | A Secret with `ca.crt`, and `tls.crt` with `tls.key`; only with HTTPS |
| `spec.scheme` | No | `HTTPS` (default) or `HTTP` |

| Repository field | Required | Description |
|---|---|---|
| `spec.url` | Yes | The OCI repository |
| `spec.registryRef.name` | No | The Registry the UI reads it through |
| `spec.description` | No | Shown in the catalog |

| Field on Products and Orders | Description |
|---|---|
| `spec.registryRefs` | Up to 16 Registries in the same namespace |
| `status.registryAccess` | Up to 32 locations, who accesses them, and the Registry used |
