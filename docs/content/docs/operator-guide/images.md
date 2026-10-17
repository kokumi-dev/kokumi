---
title: Container Images
weight: 4
description: Mirrors for image resolution, letting nodes pull vendored images, and sizing the registry.
---

Every render resolves the container images of its manifests to digests, and
Products with `spec.images.policy: Vendor` copy each into a repository of its
own below the Order's destination, `<destination>/images/<image name>`. See
[Container Images](../../user-guide/images/) in the user guide.
This page covers what an installation needs for it.

## Mirrors

In an air-gapped cluster, or to avoid upstream rate limits, Kokumi can resolve
and copy images through mirrors. Like a `registries.conf` entry with a
`prefix` and a `location`, a mirror replaces part of an image's name. They are
configured on the Kitchen, or in the UI under Settings → General:

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Kitchen
metadata:
  name: default
  namespace: kokumi
spec:
  images:
    mirrors:
    - prefix: docker.io
      location: oci://harbor.example.com/dockerhub
    - prefix: ghcr.io/stefanprodan
      location: oci://harbor.example.com/podinfo
```

- `prefix` is a normalized repository prefix (`nginx` is
  `docker.io/library/nginx`), matched at path boundaries. It starts with a
  registry host, as every normalized name does. The mirror with the longest
  matching prefix replaces it with its `location`:
  `docker.io/library/nginx:1.27` is read as
  `harbor.example.com/dockerhub/library/nginx:1.27`. This is the layout of a
  Harbor proxy cache.
- There is no fallback to the original registry, so an air-gapped
  installation never waits for a registry it cannot reach.
- Manifests and records keep the original names. Nodes pull through their own
  mirror configuration (containerd `hosts.toml`, CRI-O `registries.conf`).
- Credentials for a mirror come from the Product's `images.pullSecretRef`,
  looked up by the mirror's host.
- Mirrors are not part of the intent: changing them renders nothing new.

## Unreachable registries

Every render resolves the images of its manifests, also under the default
`Record` policy. A lookup waits at most 10 seconds. The first lookup of a
render to each registry runs alone; when the registry does not answer at all
(DNS, connection, TLS or timeout errors), the other images of that registry
fail with the same error right away instead of each waiting for it. A registry
that answers, even with 401 or 404, is asked for every image. In an air-gapped
cluster without mirrors, each upstream registry therefore costs one timeout
per render.

## Concurrency and timeouts

Up to four Orders render at a time. One render may take at most 30 minutes;
vendoring copies up to three images at a time, each within 10 minutes. A
render that runs out of time fails and is retried, and the retry skips what
the destination holds already. Under `Pin` and `Vendor`, an Order that renders
again keeps the digests of its latest Preparation, so it neither looks those
images up nor copies them again.

## Letting nodes pull vendored images

Under `Vendor`, manifests pull every image from the Order's destination
registry. The kubelet resolves the image's host with node DNS, not cluster
DNS, and expects TLS. The bundled registry is reached as
`kokumi-registry.kokumi.svc.cluster.local:5000` over plain HTTP, so nodes
cannot pull from it without one of these:

- **An external registry as the destination.** Point the Orders'
  `spec.destination` at a registry the nodes can reach, for example through a
  Pantry.
- **A containerd mirror entry on every node.** Map the name to the registry's
  NodePort (30050) over HTTP, in
  `/etc/containerd/certs.d/kokumi-registry.kokumi.svc.cluster.local:5000/hosts.toml`:

  ```toml
  [host."http://localhost:30050"]
    capabilities = ["pull", "resolve"]
  ```

  containerd must read `hosts.toml` files from that directory
  (`config_path = "/etc/containerd/certs.d"`). The Kind clusters of the local
  setup and the e2e tests are configured this way by
  `hack/kind/registry-mirror.sh`.
- **An Ingress with TLS** in front of the bundled registry, under a name the
  nodes resolve, used as the destination.

Kokumi cannot check from the controller whether nodes can pull. A vendored
image that nodes cannot pull shows up as `ImagePullBackOff` and in the Argo CD
health of the Serving.

## Destination registries

Vendoring pushes to one repository per image below the Order's destination,
such as `shop/podinfo-prod/images/ghcr.io/stefanprodan/podinfo`. The
destination registry must create repositories on the first push and accept
the destination's credentials for them:

- The bundled registry, distribution, Harbor (within a project) and Google
  Artifact Registry do.
- GitHub Container Registry creates a package per repository; new packages
  are private until their visibility is changed or they are linked to a
  repository.
- Amazon ECR needs a repository creation template for the destination's
  prefix.

## Registry storage

Vendoring copies every image of every Preparation, with all platforms. Within
one registry, identical blobs are stored once, so a new Preparation of the
same images adds only manifests. Still, the registry's `PersistentVolumeClaim`
grows with every new image version. Size it for the images of all retained
Preparations; Kokumi does not garbage-collect the registry.

Do not run the registry's garbage collection with `--delete-untagged` on
repositories Kokumi writes to. A tag points at the newest artifact or image
pushed under it: a later Preparation of the same source version moves the
version tag, and a later image under the same tag moves the image's tag. The
earlier digests become untagged while older Preparations still pull them by
digest, and rolling back to one of them would fail.

## Vendored private images

A Product's `images.pullSecretRef` lets Kokumi read private images. Under
`Vendor`, those images are copied into the Order's destination, and from then
on anyone who may pull from the destination can pull them, without the
Product's credentials. The bundled registry has no authentication and is
reachable on NodePort 30050 of every node. Give Orders that vendor private
images a destination with access control, such as a Pantry for a registry
that requires credentials.

## Events

An image that cannot be resolved under the `Record` policy is recorded without
a digest. One Warning Event `ImageNotResolved` per render lists the images on
the Order, as many as fit into an Event, and names the Preparation the render
created as its related object (`kubectl get events.events.k8s.io -o yaml`
shows it under `related`). The artifact's config blob records them all:

```bash
kubectl -n shop events --for order/podinfo-prod --types Warning
```
