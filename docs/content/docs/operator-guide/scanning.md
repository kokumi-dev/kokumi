---
title: Vulnerability Scanning
weight: 6
description: The kokumi-scanner component, enabling scanning on the Kitchen, the vulnerability database in air-gapped clusters, network isolation and sizing.
---

Kokumi scans the images Products vendor with the `kokumi-scanner` component,
when the Kitchen enables scanning. What users see is described in
[Vulnerability Scanning](../../user-guide/security-scanning/) in the user
guide. This page covers what an installation needs for it.

## Enabling scanning

Scanning is off until the Kitchen has `spec.scanning`. In the UI, turn it on
under Settings → Scanning, which edits the fields below. On the Kitchen, the
empty object enables it with the defaults, as the vulnerability-scanning
example's patch does:

{{< example file="vulnerability-scanning/kitchen-patch.yaml" >}}

```bash
kubectl patch kitchen default -n kokumi --type=merge \
    --patch-file examples/vulnerability-scanning/kitchen-patch.yaml
```

| Field | Default | Meaning |
|---|---|---|
| `intervalSeconds` | `86400` (a day) | How often an image in use is scanned again, from one hour to 30 days. |
| `maxDatabaseAgeSeconds` | `604800` (a week) | How old the vulnerability database may be before Products report `DatabaseStale`. |
| `dbRepository` | `oci://mirror.gcr.io/aquasec/trivy-db` | The Trivy vulnerability database. A repository is read at tag `2`; a tag or digest in the URL pins it. |
| `javaDBRepository` | `oci://mirror.gcr.io/aquasec/trivy-java-db` | The Trivy Java database, read at tag `1`. |
| `registryRefs` | none | Registries in the install namespace that connect to the database repositories. |

Removing `spec.scanning` stops scanning and removes the scan status from the
Products. The SBOMs and reports already attached stay in the registry.

## The scanner component

`config/scanner` installs, in the install namespace:

| Object | Purpose |
|---|---|
| Deployment `kokumi-scanner` | One replica. The official Trivy image, pinned by digest; an init container copies the `scanner` binary from the Kokumi image into it. It idles until the Kitchen enables scanning. |
| Service `kokumi-scanner` | Port 8080, plain HTTP. |
| NetworkPolicy `kokumi-scanner` | Ingress only from the controller manager. |
| ServiceAccount `kokumi-scanner` | No token and no RBAC: the scanner never calls the Kubernetes API. |

The controller manager drives every scan:

1. It picks the images that are due and looks for an SBOM it may reuse.
2. It resolves the database tags to digests, through the Kitchen's
   `registryRefs`.
3. It sends one request per Product to the scanner, with the image and
   database references by digest, any reused SBOM, and the registry
   credentials of the Product's Registries.
4. The scanner downloads the database when its digest is new, copies the
   images, runs Trivy, and returns the SBOM and the report.
5. The manager signs them with Kokumi's key and attaches them to the
   vendored images. The scanner never holds the signing key, and the server
   never calls the scanner.

Two manager flags configure it:

| Flag | Default | Meaning |
|---|---|---|
| `--scanner-url` | `http://kokumi-scanner.<install namespace>.svc:8080` | Where the manager reaches the scanner. |
| `--scan-platform` | the manager's own, such as `linux/amd64` | The platform scanned of multi-platform images. Set it to the platform your nodes run. |

The scanner image is the stock Trivy release. To update Trivy, change the
digest of the `trivy` image in `config/scanner/kustomization.yaml`, or
override the container image of the Deployment.

## Network isolation

The scanner serves plain HTTP inside the cluster, and the manager sends
registry credentials with each request. The NetworkPolicy lets only the
controller manager connect to it. **This holds only when the cluster's CNI
enforces NetworkPolicy**, as Calico and Cilium do and flannel does not.

What a caller who reaches the scanner can do:

- **Without NetworkPolicy enforcement,** any Pod in the cluster can make the
  scanner pull an image with credentials the caller supplies itself. The
  scanner holds no credentials, no Kubernetes identity and no key, so the
  caller gains no access it did not already have. It can make the scanner
  busy.
- **A compromised scanner** can return wrong findings and see the
  credentials of the Products it scans. It cannot sign: the manager signs
  only what the scanner returns for the images the manager asked for.

To encrypt the traffic, run the install namespace in a service mesh with
mTLS, such as Istio or Linkerd. Built-in TLS between the manager and the
scanner is planned.

### Restricting egress

The scanner's egress is not restricted, so that it reaches the public
database by default. When the database is mirrored, restrict it to DNS and
your registries, for example with the bundled registry:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: kokumi-scanner-egress
  namespace: kokumi
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/component: scanner
      app.kubernetes.io/name: kokumi
  policyTypes:
  - Egress
  egress:
  - to:
    - namespaceSelector: {}
      podSelector:
        matchLabels:
          k8s-app: kube-dns
    ports:
    - {port: 53, protocol: UDP}
    - {port: 53, protocol: TCP}
  - to:
    - podSelector:
        matchLabels:
          app: registry
    ports:
    - {port: 5000, protocol: TCP}
```

The scanner pulls the vendored images from the vendor locations of the
Products, so every registry they use must be allowed.

## The vulnerability database

Trivy's database is an OCI artifact, about 140 MB compressed and 1.6 GB
unpacked, and Aqua rebuilds it several times a day. On every scan run the
manager resolves `dbRepository` to a digest. The scanner downloads each
digest once and keeps it in its cache, with the previous version for scans
that still read it. Scans therefore run with the newest database, at the cost
of one download per new version.

The Java database identifies Java archives (`.jar`, `.war`) in images. It is
about 1 GB compressed, so the scanner downloads it only when an image holds
Java archives. Such images cannot be scanned without it: when its repository
cannot be reached, they fail with `ScanFailed`, and other images still scan.

### Air-gapped clusters

Mirror both databases into a registry the cluster reaches, and point the
Kitchen at it:

```bash
oras cp mirror.gcr.io/aquasec/trivy-db:2 registry.corp.example/aquasec/trivy-db:2
oras cp mirror.gcr.io/aquasec/trivy-java-db:1 registry.corp.example/aquasec/trivy-java-db:1
```

```yaml
spec:
  scanning:
    dbRepository: oci://registry.corp.example/aquasec/trivy-db
    javaDBRepository: oci://registry.corp.example/aquasec/trivy-java-db
    registryRefs:
    - name: corp-mirror        # a Registry in the install namespace
```

The Registry and its Secret live in the install namespace, whose Secrets the
`kokumi-server` ServiceAccount can read (see
[Authorization](../authorization/#the-kokumi-server-serviceaccount)). Give it
credentials that may only pull from the mirror.

The bundled registry needs no Registry. Its volume requests 1 GiB by default,
less than the two databases take together, so raise the storage request of
`kokumi-registry-pvc` first, or copy only `trivy-db` when no image holds Java
archives. It accepts pushes from anyone who reaches a node on NodePort 30050,
so pin the database by digest (see below) when you mirror it there. Copy into
it through a port-forward:

```bash
kubectl -n kokumi port-forward svc/kokumi-registry 5000:5000 &
oras cp --to-plain-http mirror.gcr.io/aquasec/trivy-db:2 localhost:5000/aquasec/trivy-db:2
oras cp --to-plain-http mirror.gcr.io/aquasec/trivy-java-db:1 localhost:5000/aquasec/trivy-java-db:1
```

```yaml
spec:
  scanning:
    dbRepository: oci://kokumi-registry.kokumi.svc.cluster.local:5000/aquasec/trivy-db
    javaDBRepository: oci://kokumi-registry.kokumi.svc.cluster.local:5000/aquasec/trivy-java-db
```

Update the mirror regularly, for example daily. A mirror that is older than
`maxDatabaseAgeSeconds` still scans, but every Product reports
`DatabaseStale`, so a forgotten update shows. To make each update a reviewed
change, pin the database by digest
(`oci://registry.corp.example/aquasec/trivy-db@sha256:…`) and change the
digest in the Kitchen.

## Sizing

| Resource | Default | Why |
|---|---|---|
| Memory | 256 MiB request, 1 GiB limit | A scan takes a few hundred MiB. The database is read through a memory map, which counts as page cache. Raise the limit for large images with many Java archives. |
| `/cache` | `emptyDir`, 16 GiB | Two versions of the database, the Java database once needed, and the images being scanned. |
| `/tmp` | `emptyDir`, 2 GiB | Trivy's temporary files. |
| Concurrent requests | 2 (`--max-concurrent`) | A busy scanner answers 503; the manager tries again later and reports `ScannerUnavailable` meanwhile. |

Measured on an 8-core development cluster (Kind, Trivy 0.71.0), one platform per
image:

| Image | SBOM | Scan | Report |
|---|---|---|---|
| `alpine:3.20` | 0.1 s | 0.03 s | 14 KB |
| `nginx:1.27-alpine`, `podinfo` (Go) | 0.2–0.3 s | 0.05 s | 0.2–0.3 MB |
| `python:3.12-slim`, `node:22-alpine`, `eclipse-temurin:21-jre-alpine` | 0.4–0.7 s | 0.04 s | 0.07–0.26 MB |
| `postgres:16` | 1.3–1.5 s | 0.05–0.08 s | 0.5 MB |
| `python:3.12` (400 MB, 4,000 findings) | 2.5–3.4 s | 0.2–0.4 s | 4.5 MB |

- Copying the image and generating its SBOM dominate; the vulnerability match
  takes milliseconds. A rescan reuses the SBOM and only matches again.
- With the defaults, 24 Products with one image each were scanned within
  seconds of their first render; a Product with 40 images took 33 s.
- The scanner used up to 420 MB of memory besides the page cache, for the
  largest image.
- Every scan attaches a new report, so the registry grows by the report size
  of every image in use every interval. Reports leave out the descriptions
  and links of their findings, about two thirds of Trivy's output; Kokumi
  reads reports up to 16 MiB back, which a report reaches at about 15,000
  findings.
- The first image with Java archives downloads the Java database, about 1 GB:
  7.5 minutes from `ghcr.io` in the test. Scans that do not need it go on
  meanwhile. A download goes on when the request that started it times out,
  for up to two hours, so the next attempt finds it. Mirror it close to the
  cluster.

The cache is an `emptyDir`, so a restarted Pod downloads the database again on
its first scan. To keep it, replace the `cache` volume with a
PersistentVolumeClaim. One replica is enough for most installations: the
manager scans two Products at a time, and each image digest once per
interval.

## Troubleshooting

- The Product's `Scanned` condition says what failed; see
  [the reasons](../../user-guide/security-scanning/#the-products-status).
- `kubectl -n kokumi logs deploy/kokumi-scanner` shows each request and
  Trivy's errors.
- `ScannerUnavailable` with `connection refused` or a timeout, while the
  scanner Pod runs, usually means that a NetworkPolicy blocks the manager, for
  example a default-deny policy in the install namespace.
