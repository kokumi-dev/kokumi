---
title: Source Verification
weight: 4
description: Require the source of a Product to be signed by its publisher, by key or keyless, before any Order renders it.
---

A Product can require its source artifact to carry a trusted signature before
any of its Orders renders it. Kokumi verifies the signature when the Product
resolves its source, records who signed it, and renders nothing from a source
that does not verify.

The settings have the shape of Flux's `OCIRepository.spec.verify`. Kokumi
verifies with the cosign library, which reads both Sigstore bundles and the
legacy `sha256-<hex>.sig` signatures.

## By public key

Store the publisher's public keys in a Secret in the Product's namespace.
Every key whose name ends in `.pub` is tried; one verifying signature suffices.

```bash
kubectl -n shop create secret generic podinfo-keys --from-file=cosign.pub
```

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Product
metadata:
  name: podinfo
  namespace: shop
spec:
  source:
    url: oci://ghcr.io/my-org/charts/podinfo
    version: "6.10.2"
    verify:
      secretRef:
        name: podinfo-keys
```

Key-based signatures are verified offline, without a transparency log.

## Keyless

Name the identity that signed the release, for example the GitHub Actions
workflow that published it. Both fields are RE2 regular expressions and are not
anchored, so anchor them with `^` and `$`. One matching identity suffices.
The source-verification example requires the signature that podinfo's release
workflow puts on every chart it publishes:

{{< example file="source-verification/product.yaml" >}}

{{< apply-example name="source-verification" >}}

Keyless signatures are verified against the Sigstore public-good instance:
the controller fetches its trusted root through TUF, which needs network
access. The signing time comes from the transparency-log proof in the
signature itself, so Sigstore's log is not contacted. In an air-gapped
cluster, set
`trustedRootSecretRef` to a Secret holding a Sigstore `trusted_root.json`; see
[Signing](../../operator-guide/signing/#air-gapped-clusters).

## Without configuration

A Product without `verify` still verifies when its source carries a signature
by a key the Kitchen trusts: a release of this Kokumi installation, or of
another one whose key a cluster admin added to the trusted keys. Your platform
team may require every source to verify; then a Product without a verifying
signature is not ready.

## In the UI

The Product form has a **Verify source** section for both modes. The Product
overview shows what the source must be signed by, and under **Resolved source**
who signed it.

The **Artifact** tab of the Product draws the source as the registry holds it:
its manifest with the signatures, SBOMs and attestations its publisher
attached, and, when the Product vendors it, the copy. Each signature shows the
verdict the controller recorded, and the source shows who signed it: the key,
or the keyless identity, such as the GitHub repository that released it. See
[Artifacts in the registry](../artifacts/).

## Checking the result

```bash
kubectl -n shop get product podinfo
# NAME      READY   REASON     SOURCE                                        VERSION   VERIFIED
# podinfo   True    Resolved   oci://ghcr.io/stefanprodan/charts/podinfo     6.10.2    True
kubectl -n shop get product podinfo -o jsonpath='{.status.source.verified}'
```

| `SourceVerified` reason | Meaning |
|---|---|
| `Succeeded` | The signature verified; `status.source.verified` names the key ID or the keyless issuer and subject. |
| `VerificationError` | The source carries no signature that `verify` accepts, or the keys or trusted root could not be read. |
| `VerificationRequired` | The Kitchen requires verification, and the source neither sets `verify` nor carries a signature by a trusted key. |

## Every signature, not only the source's

Kokumi checks every signature in the graph of the source, with the same `verify`
(or, without it, the keys the Kitchen trusts): the signatures on the source
itself and those on what is attached to it, such as the publisher's signature
on an SBOM. It records one verdict per signature:

```yaml
status:
  source:
    digest: sha256:0b3b1153…
    verified: {provider: cosign, keyID: 4xmB1c…}     # the decision: who signed the source
    signaturesCheckedTime: "2026-10-17T09:12:03Z"
    signatures:                                        # the evidence: every signature
    - {digest: sha256:d44574c2…, subject: sha256:0b3b1153…, result: Verified, signer: {provider: cosign, keyID: 4xmB1c…}}
    - {digest: sha256:013148c6…, subject: sha256:cb1cd01a…, result: Verified, signer: {provider: cosign, keyID: 4xmB1c…}}   # on the SBOM
```

| `result` | Meaning |
|---|---|
| `Verified` | Signed by a key or keyless identity `verify` accepts; `signer` names it. |
| `UntrustedSigner` | Signed by another key or identity. The signature may be valid; `message` names the signer when it can. |
| `Invalid` | The signature does not verify, for example because it signs another digest. |
| `Unsupported` | A format Kokumi does not verify, such as a Notary signature or a cosign attestation stored under a tag. |

Only a verified signature on the source itself makes it ready. Verdicts on
attachments are information; they never block the Product.

The signatures are checked whenever the Product reconciles: when it or its
source changes, when the Secrets of `verify` or the Kitchen's trusted keys
change, when an Order of it is created or deleted, and at least on the
controller's periodic resync, about every ten hours. A signature attached
later is shown as not checked yet until then. Verdicts do not expire: a
key-based signature stays valid as long as its key is trusted, and a keyless
signature is checked at the time it was made, which its transparency-log
entry proves.

A source that does not verify is not published, its Product is not ready, and
its Orders wait with reason `ProductNotReady`. Kokumi verifies it again every
few minutes, so a source that gets signed later is picked up. When vendoring
is configured, only a verified source is copied, together with the signatures
and attestations attached to it as OCI referrers.

The verified signer is recorded in the config blob of every Preparation
rendered from the source (`source.verified`) and in its signed provenance, so
the registry shows who published each input.
