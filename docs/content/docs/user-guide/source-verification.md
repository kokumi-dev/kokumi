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
    oci: oci://ghcr.io/my-org/charts/podinfo
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

```yaml
spec:
  source:
    oci: oci://ghcr.io/stefanprodan/charts/podinfo
    version: "6.10.2"
    verify:
      matchOIDCIdentity:
      - issuer: ^https://token\.actions\.githubusercontent\.com$
        subject: ^https://github\.com/stefanprodan/podinfo/\.github/workflows/release\.yml@refs/tags/.*$
```

Keyless signatures are verified against the Sigstore public-good instance,
which needs network access. In an air-gapped cluster, set
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

A source that does not verify is not published, its Product is not ready, and
its Orders wait with reason `ProductNotReady`. Kokumi verifies it again every
few minutes, so a source that gets signed later is picked up. When vendoring
is configured, only a verified source is copied, together with its signatures.

The verified signer is recorded in the config blob of every Preparation
rendered from the source (`source.verified`) and in its signed provenance, so
the registry shows who published each input.
