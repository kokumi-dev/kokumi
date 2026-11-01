---
title: Release Verification
weight: 9
description: Verify that install.yaml and the Kokumi image come from Kokumi's release workflow, and scan the image's SBOMs.
---

Kokumi's GitHub workflows sign what they publish with Sigstore, keyless,
under the identity of the workflow that built it:

| Artifact | Carries |
|---|---|
| `install.yaml`, a release asset | SLSA build provenance; it pins the image by digest |
| The image index `ghcr.io/kokumi-dev/kokumi` | A cosign signature and SLSA build provenance |
| Each platform image of the index | An SPDX SBOM |

The SBOMs list the Go modules of the binaries, the Debian packages of the
distroless base, and the npm packages that the UI bundles into the server.

## How a release is built

The job that builds the image holds no signing credentials, and the jobs that
sign run no code from the repository. A tag points at an image only once its
signature, provenance and SBOMs verify with the commands on this page; the
release workflow runs them before it tags. `install.yaml` is attested before
the release is published, and a published release is immutable.

The signatures, provenance and SBOMs are OCI referrers of the image, so they
travel with a mirrored or [vendored](../../user-guide/images/) copy:
`--bundle-from-oci` reads them from the registry instead of from GitHub.

The commands below need `gh`, signed in, `cosign`, `docker buildx` and `jq`;
scanning needs `grype` or `trivy`.

## Verify install.yaml

Download `install.yaml`, verify its provenance, and take the image digest
from it:

```bash
VERSION={{< param kokumiVersion >}}
curl -LO https://github.com/kokumi-dev/kokumi/releases/download/$VERSION/install.yaml
gh attestation verify install.yaml --repo kokumi-dev/kokumi

DIGEST=$(grep -m1 -o 'ghcr.io/kokumi-dev/kokumi:.*@sha256:[0-9a-f]*' install.yaml | cut -d@ -f2)
IMAGE=ghcr.io/kokumi-dev/kokumi@$DIGEST
```

## Verify the image

Verify the signature and the provenance of the image index:

```bash
cosign verify "$IMAGE" \
    --certificate-identity-regexp '^https://github\.com/kokumi-dev/kokumi/\.github/workflows/image\.yml@' \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    --certificate-github-workflow-ref refs/tags/$VERSION
gh attestation verify "oci://$IMAGE" --bundle-from-oci --repo kokumi-dev/kokumi \
    --signer-workflow kokumi-dev/kokumi/.github/workflows/image.yml \
    --source-ref refs/tags/$VERSION
```

The image `latest` is built from `main`: verify it with `refs/heads/main` in
place of `refs/tags/$VERSION`.

## Scan the SBOMs

The SBOMs belong to the platform images, not the index. Verify the SBOM of
one platform and scan it:

```bash
PLATFORM_IMAGE=ghcr.io/kokumi-dev/kokumi@$(docker buildx imagetools inspect --raw "$IMAGE" |
    jq -r '.manifests[] | select(.platform.architecture == "amd64") | .digest')
gh attestation verify "oci://$PLATFORM_IMAGE" --bundle-from-oci --repo kokumi-dev/kokumi \
    --signer-workflow kokumi-dev/kokumi/.github/workflows/image.yml \
    --source-ref refs/tags/$VERSION \
    --predicate-type https://spdx.dev/Document/v2.3 \
    --format json --jq '.[0].verificationResult.statement.predicate' > kokumi.spdx.json
grype sbom:kokumi.spdx.json
```

Syft generates the SBOMs, and Grype scans all three kinds of packages in
them; `trivy sbom` scans the Go modules and the npm packages, but not the
Debian packages. The image itself also carries the SBOM of the UI, at
`/var/lib/db/sbom/kokumi-ui.spdx.json`, so `trivy image` scans everything
without the attestation.
