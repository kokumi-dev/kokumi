---
title: Container Images
weight: 5
description: Record the images every Preparation runs by digest, pin the manifests to those digests, or vendor each image into a repository of its own below the Product's vendor location.
---

A chart that references `nginx:1.27` deploys whatever that tag points to when
a node pulls it, so even an immutable Preparation would run moving content.
Kokumi finds every container image in the rendered manifests, resolves each to
a digest, and records the result in the Preparation. A Product's
`spec.images.policy` decides what happens next:

| `policy` | Manifests | When an image cannot be resolved |
|---|---|---|
| `Record` (default) | unchanged | the image is listed without a digest, and one Warning Event `ImageNotResolved` per render lists the images on the Order |
| `Pin` | `repo:tag` becomes `repo:tag@sha256:…`; the tag stays for readability | the render fails |
| `Vendor` | `repo:tag` becomes `<vendor>/images/<full image name>:tag@sha256:…` | the render fails |

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Product
metadata:
  name: podinfo
  namespace: shop
spec:
  source:
    url: oci://ghcr.io/stefanprodan/charts/podinfo
    version: "6.14.1"
  render:
    helm: {}
  images:
    policy: Pin
```

The policy applies to every Order of the Product: every variant renders the
same chart. Orders cannot change it.

## When images are resolved

Images are resolved when an Order renders a new Preparation, and the digests
are recorded with it. A tag that moves upstream does not render a new
Preparation.

Under `Pin` and `Vendor` the digests are part of the rendered manifests, and
they work like a lock file: when the Order renders again, a reference that is
written the same way keeps the digest of the Order's latest Preparation,
without asking the registry. Only new or changed references are resolved. A
change to the values or patches therefore never ships other images on its
own, and promoting or rolling back a Preparation always runs exactly the
images it recorded. To move to a new image, change its reference, for example
the chart's image tag. Under `Record` the manifests keep their tags, so every
render resolves every image afresh and records what the tags point to then.

Already pinned references (`nginx@sha256:…`) are only checked to exist.

The manifests of one render may reference at most 512 distinct images, each
reference at most 1024 characters long, as many as a Preparation records.
Larger renders fail before anything is pushed.

## Which fields are searched

Kokumi follows kustomize's image transformer. It always searches:

- the `image` of every entry of `containers`, `initContainers` and
  `ephemeralContainers`, at any depth of any object. This covers Pods, every
  workload template, and custom resources that embed a PodSpec, such as
  CronJobs or Argo Rollouts;
- the `image.reference` of every entry of `volumes` (Pod image volumes).

CustomResourceDefinitions are skipped. Custom resources that hold an image in a
plain string field need a field spec, in the shape of a kustomize `FieldSpec`:

```yaml
spec:
  images:
    fieldSpecs:
    - group: monitoring.coreos.com
      kind: Prometheus
      path: spec/image
    - group: monitoring.coreos.com
      kind: Alertmanager
      path: spec/containers[]/image   # [] walks every element of a list
```

An image in a field nothing searches is silently missed. The Images tab of a
Preparation lists which fields were searched.

## Credentials

Images are resolved and copied through the Product's Registries, the one
whose URL covers an image's name with the longest path winning; images no
Registry covers are read anonymously. A Registry for `oci://docker.io` with
Docker Hub credentials avoids its rate limits:

```yaml
spec:
  registryRefs:
  - name: dockerhub          # url: oci://docker.io
  - name: registry-example   # url: oci://registry.example.com
```

Everyone who may edit the Product can use the credentials of the Registries
in its namespace, through the images the Product's manifests reference. Under
`Vendor`, the private images are also copied below the vendor location, so
everyone who may pull from there can pull them too; Kokumi refuses to copy a
private image to a vendor location it writes without credentials
(`CredentialsRequired`). See
[Registries and Repositories](../registries/).

When the Kitchen configures mirrors, images are resolved and copied through
them; see [Container Images](../../operator-guide/images/) in the operator guide.

## Vendoring

`Vendor` requires the Product to vendor its source (`spec.vendor`). Every
image is copied into a repository of its own below the vendored source,
named after the image's full name, and tagged with the tag its reference
names. All Orders of the Product share these repositories. The
image-vendoring example vendors the podinfo chart's image:

{{< example file="image-vendoring/product.yaml" >}}

{{< apply-example name="image-vendoring" >}}

| In the source manifests | Pulled from after vendoring (vendor `<registry>/shop/vendor/podinfo`) |
|---|---|
| `ghcr.io/stefanprodan/podinfo:6.14.1` | `<registry>/shop/vendor/podinfo/images/ghcr.io/stefanprodan/podinfo:6.14.1@sha256:…` |
| `nginx:1.27` | `<registry>/shop/vendor/podinfo/images/docker.io/library/nginx:1.27@sha256:…` |
| `nginx` | `<registry>/shop/vendor/podinfo/images/docker.io/library/nginx:latest@sha256:…` |
| `ghcr.io/example/app@sha256:…` | `<registry>/shop/vendor/podinfo/images/ghcr.io/example/app@sha256:…`, untagged |

The cluster therefore shows which image runs, under its own name and tag; only
the registry and the prefix change. The node pulls by digest, so the tag only
names what is pulled.

- The tag is the tag of the reference, nothing else. When a later render
  vendors another digest under the same tag, for example after an upstream
  `latest` moved, the tag moves to it; the earlier digest stays in the
  repository and the Preparations that pin it keep pulling it by digest. A
  registry with immutable tags rejects the move and fails that render.
- A registry port in the image's name is written as `_<port>`, since a
  repository path cannot hold a colon: `registry.example.com:5000/app` is
  vendored to `…/images/registry.example.com_5000/app`. A repository path
  longer than 255 characters fails the render before anything is copied.
- An image index stays an index, so all platforms are copied.
- Upstream signatures, SBOMs and attestations come along as OCI referrers.
  Legacy cosign `sha256-<hex>.sig` tags are not copied.
- When the image and the vendor location share a registry, blobs are mounted
  instead of copied.

The manifests then pull every image from the vendor registry by digest, so a
release runs without any other registry. **The cluster nodes must be able to
pull from the vendor registry**; `status.registryAccess` of an Order lists the
location with the client `Kubelet`. The bundled registry is reached as
`kokumi-registry.kokumi.svc.cluster.local:5000` over plain HTTP, which nodes
cannot resolve without extra configuration; see the operator guide. The
vendor registry must also create repositories on their first push below the
vendor location.

The list of vendored images, each with its original name, digest and vendored
reference, is recorded in the artifact's config blob and in its provenance,
both covered by the Preparation's signature. Copying a release with its
images to another registry follows that list; `oras cp` of the Preparation's
artifact copies the manifests and their evidence, not the images.

## Sources with a kustomization file

A source that contains a `kustomization.yaml` is pushed as it is and rendered
by Argo CD. Its `images:` transformer and remote bases can still change the
references after Kokumi has seen them, so:

- `Record` lists the images found in the files.
- `Pin` and `Vendor` fail the render.

## In the UI

- **Product form:** the Images section edits the policy and the field specs;
  the Registries section picks the Registries images are read through. The
  Product page shows them.
- **Preparation Images tab:** every image with its digest, whether it is
  multi-platform, where it is pulled from when vendored, and why it could not
  be resolved. Copy the digest reference to pull exactly that image.
- **Changes tab:** images that were added, removed, or resolved to another
  digest, even when the manifests themselves did not change.
- **Previews** under `Pin` and `Vendor` resolve and rewrite the images as a
  render would, but copy nothing. They read through the Product's Registries
  with your identity, so a Registry whose Secret you may not read fails the
  preview with 403, and they leave references they cannot resolve within 20
  seconds as written. An Order's preview keeps the digests of the Order's
  latest Preparation, as its render would. Both show the vendored location
  below the Product's vendor location.
  Under `Record` a render leaves the manifests unchanged, so previews look up
  no image.

## Where images are recorded

- `Preparation.spec.images` lists every image: the reference as written, the
  normalized name, the digest and media type, and the vendored reference.
- The artifact's config blob records the same list under `images`, with the
  reason for every image that could not be resolved.
- The signed SLSA provenance lists every resolved image under
  `resolvedDependencies`, with its digest, media type and vendored reference.
- The intent records `images.policy` and `images.fieldSpecs` unless they are
  the defaults. Changing them renders a new Preparation for every Order of the
  Product. The pull Secret, the mirrors and the resolved digests are not part
  of the intent.
