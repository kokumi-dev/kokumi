---
title: Producing Base Artifacts
weight: 5
description: How to publish OCI base artifacts so Kokumi can link each Preparation back to its source commit or tag.
---

Kokumi can trace every rendered Preparation back to the exact SCM commit or tag
that produced its base OCI artifact, but only if the base artifact carries the
standard OCI annotations:

| Annotation | Required | Meaning |
|---|---|---|
| `org.opencontainers.image.source` | yes | SCM repository URL of the artifact's source |
| `org.opencontainers.image.version` | no | SCM tag of the artifact's source |
| `org.opencontainers.image.revision` | no | SCM commit SHA of the artifact's source |

When Kokumi pulls a base artifact that has these annotations, it copies them onto
the rendered Preparation (`spec.gitSource.repo` / `spec.gitSource.tag` /
`spec.gitSource.commitHash`) and stamps the
base identity (`org.opencontainers.image.base.name` / `.base.digest`) onto the
rendered artifact. The UI then renders a clickable linkout to the source tag
or commit automatically.

Kokumi reads provenance from the standard OCI `annotations` field on the base
artifact. It uses `org.opencontainers.image.source` (falling back to
`org.opencontainers.image.url`) for the repository, `org.opencontainers.image.version`
for the tag, and `org.opencontainers.image.revision` for the commit hash.

This guide shows how to set those annotations when you publish your base
artifacts.

## Oras

`oras` lets you attach arbitrary annotations at push time. Set
`org.opencontainers.image.source`, `org.opencontainers.image.version`, and
`org.opencontainers.image.revision`:

```bash
oras push \
  ghcr.io/kokumi-dev/example:1.0.0 \
  --artifact-type application/vnd.cncf.helm.chart.content.v1.tar+zstd \
  my-app-1.0.0.tgz \
  --annotation "org.opencontainers.image.source=https://github.com/kokumi-dev/example" \
  --annotation "org.opencontainers.image.version=1.0.0" \
  --annotation "org.opencontainers.image.revision=$(git rev-parse HEAD)"
```

## What Kokumi does with it

Once the base artifact is pushed with the annotations above and referenced by an
Order, Kokumi:

1. Reads `org.opencontainers.image.source` / `.revision` from the base artifact.
2. Stores them on the Preparation as `spec.gitSource.repo` / `spec.gitSource.revision`.
3. Stamps `org.opencontainers.image.base.name` / `.base.digest` on the rendered
   artifact so the chain is verifiable from the artifact alone.
4. Renders a **source** linkout in the Preparation list of the UI, linking
   straight to the commit (or tag) in GitHub.

If a base artifact has no provenance annotations, the linkout is simply omitted.
