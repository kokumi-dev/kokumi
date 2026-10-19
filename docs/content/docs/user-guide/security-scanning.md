---
title: Vulnerability Scanning
weight: 6
description: Scan the images a Product vendors for known vulnerabilities, read the signed SBOMs and reports, and hold promotions on findings.
---

Kokumi scans the container images a Product vendors for known
vulnerabilities. Each image digest is scanned once per Product, however many
Orders and Preparations run it, and again every day, so that new
vulnerabilities show up on what is already deployed. The SBOM and the
vulnerability report are signed and attached to the vendored image as OCI
referrers. The Product's status sums them up, and an Order can hold
promotions until its images pass a check.

Scanning is enabled by an operator on the Kitchen (`spec.scanning`), in the
UI under Settings → Scanning. See
[Vulnerability Scanning](../../operator-guide/scanning/) in the operator
guide. The scans run in the `kokumi-scanner` component with
[Trivy](https://trivy.dev).

## What is scanned

Scanning covers the images of Products with `spec.images.policy: Vendor`
(see [Container Images](../images/)). Kokumi copied those images and signs
what it attaches to them. It does not scan images under `Record` or `Pin`:
their repositories belong to someone else.

Of those, it scans the images of the Preparations in use, for every Order of
the Product:

- the Order's three newest `Ready` Preparations: the next release and the
  ones a rollback returns to;
- the Preparation its Serving targets;
- the Preparation its Serving deploys.

An image that one of these runs is scanned when it first appears, and again
when its last scan is older than the Kitchen's interval, one day by default.
Images that nothing uses any more drop out of the Product's status. Promoting
an older Preparation makes it the Serving's target, so it is scanned again
before the check decides.

A scan belongs to an image digest, not to a Preparation: Preparations that
run the same images have the same findings, however their values differ.

A multi-platform image is scanned for one platform: the platform of the
cluster's controller (normally `linux/amd64`), or the first platform the
image index lists when it lacks that one. The report records which.

## Which SBOM is scanned

A scan reads an SBOM, a list of the packages in the image, and looks those
packages up in the vulnerability database. Kokumi uses an SBOM that already
exists when it cannot have been forged, first found wins:

1. the SBOM Kokumi attached in an earlier scan, for the same platform;
2. an SBOM that BuildKit (`docker buildx build --sbom=true`) wrote into the
   image index. The index digest, which the Preparation pins, covers it;
3. another SBOM attached to the image and signed by a key the Kitchen trusts
   (`spec.signing.trustedKeys`, see
   [Signing](../../operator-guide/signing/)).

Otherwise the scanner generates a CycloneDX SBOM from the image, and Kokumi
signs it and attaches it.

**Unsigned SBOMs are never used.** Anyone who may push to a repository can
attach referrers to its images, and an SBOM that lists no packages scans
clean. Kokumi ignores SBOM referrers that no trusted key signed, and SBOMs
under legacy cosign `sha256-<digest>.att` tags.

## What is attached to the image

Each scan attaches to the vendored image, as Sigstore bundles signed with
Kokumi's key:

| Referrer | Predicate type | Annotations |
|---|---|---|
| SBOM, CycloneDX (only when Kokumi generated it) | `https://cyclonedx.org/bom` | `kokumi.dev/platform` |
| Vulnerability report, cosign's vuln predicate | `https://cosign.sigstore.dev/attestation/vuln/v1` | `kokumi.dev/platform`, `kokumi.dev/sbom-origin` |

The report holds Trivy's JSON result. To keep reports of images with
thousands of findings small, each finding leaves out its description, its
reference links, Trivy's vendor link, its data source, the layer it was found
in, its fingerprint and its modification date; the vulnerability's ID leads
to them. Its ID, package and package URL, versions, status, severity, CVSS
scores, CWE IDs, title and publication date stay. Its `invocation.parameters` name
the SBOM it was computed from (`sbom.digest`, `sbom.origin`) and the
platform, and `scanner.db` names the database by digest and its build time.
A new scan attaches a new report; earlier reports stay.

The stock tools read them:

```bash
cosign tree <registry>/shop/vendor/podinfo/images/ghcr.io/stefanprodan/podinfo@sha256:…
cosign verify-attestation --key kokumi.pub --insecure-ignore-tlog=true --type vuln \
  <registry>/shop/vendor/podinfo/images/ghcr.io/stefanprodan/podinfo@sha256:…
```

## The Product's status

`status.vulnerabilities` lists the latest scan of each image in use, with the
counts by severity. `fixable` counts the findings that a newer package
version fixes. The findings themselves stay in the signed reports.

```yaml
status:
  vulnerabilities:
    scanner:
      name: trivy
      version: 0.71.0
      database: oci://mirror.gcr.io/aquasec/trivy-db@sha256:…
      databaseUpdatedTime: "2026-10-19T00:57:12Z"
    images:
    - digest: sha256:…
      repository: oci://kokumi-registry.kokumi.svc.cluster.local:5000/shop/vendor/podinfo/images/ghcr.io/stefanprodan/podinfo
      platform: linux/amd64
      scannedTime: "2026-10-19T09:12:40Z"
      sbom: {digest: "sha256:…", origin: Generated}   # Generated | Image | Signed
      report: sha256:…
      summary: {critical: 0, high: 2, medium: 7, low: 12, unknown: 0, fixable: 5}
  conditions:
  - type: Scanned
    status: "True"
    reason: Scanned
    message: "Scanned the vendored images in use: 1"
```

The `Scanned` condition reports the state of the scans. It is not part of
`Ready`: findings never stop a Product, an Order or a render.

| Reason | Meaning |
|---|---|
| `Scanned` | Every image in use has a current scan. |
| `ScanFailed` | Some images could not be scanned; the message names them. They are tried again after five minutes. |
| `Stale` | An image's last scan is older than twice the interval. |
| `DatabaseStale` | The vulnerability database is older than the Kitchen allows. The reports are still written, but they miss newer vulnerabilities. |
| `ScannerUnavailable` | The `kokumi-scanner` component did not answer or was busy. |

A Product lists at most 512 images. When more are in use, it lists the most
recently scanned and the message says how many are left out.

## Holding promotions on findings

An Order can require that the images of a Preparation pass a vulnerability
check before it is deployed:

```yaml
spec:
  promotion:
    mode: Automatic
    checks:
      vulnerabilities:
        maxSeverity: High        # Critical | High | Medium | Low
        ignoreUnfixed: true      # findings no package version fixes yet do not count
        maxAgeSeconds: 172800    # how old a scan may be; default twice the Kitchen's interval
```

A Preparation passes when every one of its images has a scan that is recent
enough, and no finding at or above `maxSeverity`. Findings of unknown
severity never count.

- **The check is part of the intent,** like approvals. A Preparation keeps
  the check it was rendered under, and changing the check renders a new
  Preparation for the Order.
- **Every image must be vendored and scanned.** An image the Product does not
  vendor, or that was not scanned yet, holds the Preparation as
  `ScanMissing`. A check therefore needs `spec.images.policy: Vendor`.
- **The reports are read from the registry.** The Serving verifies each
  report against the Kitchen's keys, through the Product's Registries, and
  does not trust the Product's status alone.

The Serving reports the result in its `ChecksPassed` condition:

| Reason | Meaning |
|---|---|
| `Passed` | No image has findings at or above `maxSeverity`. |
| `VulnerabilitiesFound` | The message lists the findings that block. |
| `ScanMissing` | An image is not vendored, not scanned yet, or its report did not verify. |
| `ScanStale` | An image's scan is older than `maxAgeSeconds`. |

**The check holds new deployments only.** While a Preparation does not pass,
the Serving keeps the Preparation it deploys, as with approvals. A
Preparation that is already deployed stays deployed when a later scan finds
new vulnerabilities. Rolling it back or replacing it is a decision the check
does not make for you. Instead:

- the Serving records the summary of the deployed images in
  `status.deployedVulnerabilities`;
- it emits a Warning Event `VulnerabilitiesIncreased` when the findings at or
  above `maxSeverity` rise for the release that stays deployed.

A Preparation that waits for a scan is deployed as soon as the scan passes;
the Serving needs no new promote.

## Comparing releases

Every list of Preparations shows the findings of each Preparation next to its
version, summed over its images, with the deployed one marked. To find a
release without a finding, look for an earlier version whose count lacks it.
Preparations outside the scanned window show "not scanned"; promoting one
scans it.

The Security tab of a Preparation compares its findings with those of the
Preparation the Order's Serving deploys:

- **New** are the findings this Preparation has and the deployed one lacks:
  what promoting it adds.
- **Fixed** are the findings the deployed one has and this one lacks: what
  promoting it fixes. For a rollback target, this is what rolling back
  removes.

Findings match by vulnerability and package, so an upgrade that still
carries a vulnerability neither adds nor fixes it. Images without a verified
scan on either side are listed, since the comparison misses their findings.

## In the UI

- **Order form:** the promotion section turns the vulnerability check on and
  sets its severity, whether unfixed findings count, and how old a scan may
  be.
- **Product Security tab:** the scanner and its database, and a table of the
  images in use: their references, which Orders run them and which deploy
  them, the counts, and the SBOM download. Select an image to see its
  findings, filtered by severity, fix and text.
- **Preparation Security tab:** the check and its verdict, the Preparation's
  images with what the check blocks on and why an image has no scan, their
  findings, and the comparison with the deployed release.
- **Preparations lists:** the version and the findings of every Preparation.
- **Promote dialog:** whether the Preparation passes its check, and how its
  findings compare with the deployed release. A promote is still possible;
  the Serving then waits for the check.
- **Servings and Dashboard:** a severity badge for what each Serving deploys,
  and on the Dashboard a list of the Servings deployed with critical
  findings, shown while there are any.

A finding's ID links to its record on [cve.org](https://www.cve.org) for CVE
IDs and on [osv.dev](https://osv.dev) for all others, such as GHSA or Go IDs.

The findings are read from the signed reports in the registry with your
identity, through the Product's Registries. A Registry whose Secret you may
not read shows the error instead of the findings.
