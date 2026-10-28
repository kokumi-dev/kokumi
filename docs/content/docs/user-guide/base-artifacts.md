---
title: Producing Base Artifacts
weight: 11
description: How to publish OCI base artifacts so Kokumi can link each Preparation back to its source commit or tag.
---

Kokumi can trace every rendered Preparation back to the exact SCM commit or tag
that produced its base OCI artifact, but only if the base artifact carries the
standard OCI annotations:

| Annotation | Required | Meaning |
|---|---|---|
| `org.opencontainers.image.source` | yes | SCM repository URL of the artifact's source |
| `org.opencontainers.image.version` | this or `revision` | SCM tag of the artifact's source |
| `org.opencontainers.image.revision` | this or `version` | SCM commit SHA of the artifact's source |

When Kokumi pulls a base artifact that has these annotations, it copies them onto
the rendered Preparation (`spec.gitSource.repo` / `spec.gitSource.tag` /
`spec.gitSource.commitHash`) and stamps them, together with the
base identity (`org.opencontainers.image.base.name` / `.base.digest`), onto the
rendered artifact. The UI then renders a clickable linkout to the source tag,
or to the commit when the artifact names no tag. It links only repositories on
GitHub: the repository URL must contain `github.com`, such as
`https://github.com/kokumi-dev/example`.

Kokumi reads provenance from the standard OCI `annotations` field on the base
artifact. It uses `org.opencontainers.image.source` (falling back to
`org.opencontainers.image.url`) for the repository, `org.opencontainers.image.version`
for the tag, and `org.opencontainers.image.revision` for the commit hash.

The linkout names the tag whenever `org.opencontainers.image.version` is set,
so set it to the Git tag exactly as the repository writes it, or leave it out
and link the commit.

This guide shows how to set those annotations when you publish your base
artifacts.

`helm push` sets `org.opencontainers.image.source` from the first entry of the
chart's `sources` and `org.opencontainers.image.version` from its `version`,
and copies the chart's `annotations`, so an `org.opencontainers.image.revision`
annotation in `Chart.yaml` reaches the artifact too. The linkout then names
the chart version as the tag.

## Oras

`oras` lets you attach arbitrary annotations at push time. Set
`org.opencontainers.image.source`, `org.opencontainers.image.version`, and
`org.opencontainers.image.revision`. Kokumi renders the artifact as a Helm
chart only when its layer has the Helm chart media type, so push the chart
with the media types `helm push` uses, and its `Chart.yaml` as JSON for the
config:

```bash
helm show chart my-app-1.0.0.tgz | yq -o json > config.json

oras push \
  ghcr.io/kokumi-dev/example:1.0.0 \
  --config config.json:application/vnd.cncf.helm.config.v1+json \
  my-app-1.0.0.tgz:application/vnd.cncf.helm.chart.content.v1.tar+gzip \
  --annotation "org.opencontainers.image.source=https://github.com/kokumi-dev/example" \
  --annotation "org.opencontainers.image.version=1.0.0" \
  --annotation "org.opencontainers.image.revision=$(git rev-parse HEAD)"
```

## What Kokumi does with it

Once the base artifact is pushed with the annotations above and referenced by an
Order, Kokumi:

1. Reads `org.opencontainers.image.source` / `.version` / `.revision` from the
   base artifact.
2. Stores them on the Preparation as `spec.gitSource.repo` /
   `spec.gitSource.tag` / `spec.gitSource.commitHash`.
3. Stamps them and `org.opencontainers.image.base.name` / `.base.digest` on the
   rendered artifact so the chain is verifiable from the artifact alone.
4. Renders a **source** linkout in the Preparation list and overview of the UI,
   linking straight to the tag in GitHub, or to the commit when there is no tag.

If a base artifact has no provenance annotations, names neither a tag nor a
commit, or comes from a repository outside GitHub, the linkout is simply
omitted.

## Signing

Sign the artifact so that Products can require its signature (see
[Source verification](../source-verification/)). With a key:

```bash
cosign sign --key cosign.key ghcr.io/kokumi-dev/example@sha256:…
```

Or keyless from CI, for example in a GitHub Actions workflow with
`id-token: write`:

```bash
cosign sign --yes ghcr.io/kokumi-dev/example@sha256:…
```

Always sign by digest, so the signature names exactly the artifact you pushed.
