---
title: Signing
weight: 4
description: The key that signs every Kokumi artifact, how it is protected and rotated, how to verify artifacts with cosign, and how to require signed sources.
---

Kokumi signs every artifact and attestation it writes with one key, the
**signing key** of the Kitchen. Signatures use the Sigstore bundle format that
cosign v3 writes, so the stock `cosign` CLI verifies them offline. Kokumi
itself deploys nothing whose signatures do not verify, and it can require the
sources it renders to be signed by their publishers.

## What is signed

| Artifact | Attestation (predicate type) | Written |
| -------- | ---------------------------- | ------- |
| Preparation | Signature (`https://sigstore.dev/cosign/sign/v1`) | When the Preparation is created |
| Preparation | Provenance (`https://slsa.dev/provenance/v1`) | When the Preparation is created |
| Preparation | Sealed approvals (`https://kokumi.dev/attestations/approval/v1`) | When an approved Preparation becomes the Serving's target |
| Preparation | Promotion record (`https://kokumi.dev/attestations/promotion/v1`) | When a Serving promotes it, past its gates |
| Preparation | Deployment record (`https://kokumi.dev/attestations/deployment/v1`) | When its deployment is first healthy |
| Vendored image | SBOM (`https://cyclonedx.org/bom`) | When a scan generates one, because no SBOM could be reused |
| Vendored image | Vulnerability report (`https://cosign.sigstore.dev/attestation/vuln/v1`) | On every scan |

The SBOMs and reports are written only when the Kitchen enables
[vulnerability scanning](../scanning/).

Each attestation is an OCI referrer of the artifact: a Sigstore bundle
(`application/vnd.dev.sigstore.bundle.v0.3+json`) holding a DSSE envelope over
an in-toto statement. The bundles carry no transparency log entry and no
timestamp; they are verified against the public key alone.

A Preparation is `Ready` only once it is `Signed` and its provenance is
attached. Before the Serving controller deploys a Preparation, it reads the
signature, the provenance and, when an approval policy applies, the sealed
approvals from the registry and verifies them, as it verifies the deployment
records of the [upstream stages](../../user-guide/promotion-pipelines/) that
qualify it. Argo CD verifies no OCI
signatures, so this is the only check, and Argo CD pulls exactly the digest
that was verified. The Serving's `Verified` condition reports the result.

## Verify an artifact

The public key is published in the Kitchen status, in the Settings page under
**Signing**, and without login at `GET /api/v1/signing`:

```bash
kubectl -n kokumi get kitchen default -o jsonpath='{.status.signing.publicKey}' > kokumi.pub
cosign verify --key kokumi.pub --insecure-ignore-tlog=true <registry>/shop/podinfo-prod@sha256:c3…
cosign verify-attestation --key kokumi.pub --insecure-ignore-tlog=true --type slsaprovenance1 <registry>/shop/podinfo-prod@sha256:c3…
```

`--insecure-ignore-tlog=true` tells cosign that the signature is not in a
transparency log; the key still has to verify it. Add `--allow-http-registry`
for a registry served over plain HTTP, such as the bundled one.

`cosign verify` accepts any bundle the key signed, whatever its predicate type.
Kokumi's own checks always verify the predicate type and the subject.

## The signing key

The key is an ECDSA P-256 key in the Secret `kokumi-signing-key` in the
`kokumi-signing` namespace, in the layout cosign's `k8s://` key references use:

| Key | Content |
| --- | ------- |
| `cosign.key` | The encrypted private key (PEM) |
| `cosign.password` | Its password. It protects nothing at rest; it exists so that cosign can read the Secret. |
| `cosign.pub` | The public key (PEM) |

The install manifests create the `kokumi-signing` namespace and allow the
controller to create Secrets in it. The Kitchen controller generates the key
when the Secret does not exist and publishes its public half in
`status.signing`. The `SigningKeyReady` condition of the Kitchen reports
whether the key could be loaded:

| Reason | Meaning |
| ------ | ------- |
| `KeyReady` | The key is loaded. |
| `KeyMissing` | The Secret does not exist and could not be generated, for example because the `kokumi-signing` namespace is missing. Re-apply the install manifests. |
| `KeyInvalid` | The Secret cannot be used: not cosign's layout, a wrong password, not a P-256 key, or `cosign.pub` does not match. |
| `TrustedKeyInvalid` | A key in `spec.signing.trustedKeys` cannot be parsed. |

While the key is not ready, nothing is signed and nothing is deployed.

To bring your own key, create the Secret before installing Kokumi, or before
the controller first starts:

```bash
kubectl create namespace kokumi-signing
cosign generate-key-pair k8s://kokumi-signing/kokumi-signing-key
```

The key has a namespace of its own, apart from the install namespace and
everything else users may be granted Secret access to. Its namespace and name
are fixed, so that RBAC and admission can protect it.

### Who can read and write the key

Only the controller manager and cluster admins can read or write the key:

- The `kokumi-signing-key-integrity` ValidatingAdmissionPolicy rejects every
  create, update and delete of the Secret except by the
  `kokumi-controller-manager` ServiceAccount and cluster admins. A cluster
  admin is whoever may use the custom verb `manage-signing-key` on `kitchens`:
  `cluster-admin` and `system:masters` can, the Kokumi roles cannot, because
  they list their verbs. Grant the verb to delegate key rotation:

  ```yaml
  apiVersion: rbac.authorization.k8s.io/v1
  kind: Role
  metadata:
    name: kokumi-signing-key-manager
    namespace: kokumi
  rules:
  - apiGroups: ["delivery.kokumi.dev"]
    resources: ["kitchens"]
    resourceNames: ["default"]
    verbs: ["manage-signing-key"]
  ```

  The holder still needs ordinary RBAC on the Secret. The namespace controller
  may delete the Secret with the `kokumi-signing` namespace. The policy names
  the controller's ServiceAccount and the Kitchen in the install namespace
  `kokumi`; see
  [Authorization](../authorization/#the-kokumi-server-serviceaccount) for
  installing into another namespace.
- No Kokumi role grants read access. Only the controller is granted access to
  Secrets in `kokumi-signing`. The built-in `admin`, `editor` and `viewer`
  roles hold no Secret access, because they are bound cluster-wide, and the
  server reads Secrets with its own identity only in the install namespace,
  and Registry Secrets only as the user. See
  [Authorization](../authorization/#registry-credentials) for granting
  Registry credentials without exposing the key.
- The `kokumi-status-integrity` policy lets only the controller write the Kitchen
  status, which publishes the public key.

Anyone who can read the key can forge signatures that Kokumi trusts. Never
grant access to Secrets in `kokumi-signing`, and treat cluster-wide Secret
read access as access to the key.

Anyone who may `update` the Kitchen can do the same with a key of their own:
they can add it to [`spec.signing.trustedKeys`](#trusted-keys) and turn off
`requireSourceVerification`. Of the built-in roles only the admin role holds
`update` on `kitchens`. Grant it only to those you would trust with the key.

### Rotate the key

1. In **Settings → Signing**, choose **Trust the current key** and save. Or add
   the current `status.signing.publicKey` to `spec.signing.trustedKeys` of the
   Kitchen yourself.
2. As a cluster admin, delete the Secret:

   ```bash
   kubectl -n kokumi-signing delete secret kokumi-signing-key
   ```

3. The controller generates a new key and publishes it.

Artifacts signed with the old key keep verifying as long as its public key is
trusted. Remove it from the trusted keys once nothing signed with it needs to
be deployed any more.

## Trusted keys

`spec.signing.trustedKeys` lists further public keys whose signatures Kokumi
trusts for its own artifacts: the key used before a rotation, or the key of
another installation whose releases are imported.

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Kitchen
metadata:
  name: default
  namespace: kokumi
spec:
  signing:
    trustedKeys:
    - name: before-2026-10
      publicKey: |
        -----BEGIN PUBLIC KEY-----
        …
        -----END PUBLIC KEY-----
    requireSourceVerification: true
```

A trusted key that does not parse stops all signing and verification until it
is fixed; the Kitchen reports `TrustedKeyInvalid`. The Settings page validates
keys before saving them.

## Signed sources

A Product can require its source to be signed by its publisher, by key or
keyless against the expected signer identity. See
[Source Verification](../../user-guide/source-verification/) for the
fields. A source signed by a key the Kitchen trusts verifies without any
configuration.

Set `spec.signing.requireSourceVerification: true` on the Kitchen to make
verification mandatory: a Product whose source does not verify, through its own
`verify` or by a trusted key, is not ready, and none of its Orders render.

### Air-gapped clusters

Key-based verification works offline. Keyless verification needs the Sigstore
trusted root:

- Online, the controller fetches the Sigstore public-good trusted root through
  TUF, keeps it in memory, and refreshes it once a day. It needs outbound HTTPS
  access to Sigstore's TUF repository (`tuf-repo-cdn.sigstore.dev`). Sigstore's
  transparency log is never contacted: the signing time comes from the proof in
  each signature.
- Air-gapped, store a trusted root in a Secret under the key
  `trusted_root.json` and name it in `spec.source.verify.trustedRootSecretRef`:

  ```bash
  cosign trusted-root create … > trusted_root.json   # or fetch it while online
  kubectl -n shop create secret generic sigstore-root --from-file=trusted_root.json
  ```

Sigstore rotates its keys and log shards over time. Refresh the stored trusted
root regularly; Kokumi does not update it. A changed Secret is picked up at
once: the Products whose `verify` names it check their signatures again.

The signatures must reach the cluster with the artifact. Kokumi's vendoring and
`oras cp -r` copy OCI referrers, which is how cosign v3 stores signatures.
Legacy signatures under `sha256-<digest>.sig` tags are only copied by
`cosign copy`.
