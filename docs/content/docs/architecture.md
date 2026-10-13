---
title: Architecture & Concepts
weight: 3
description: How Kokumi models release workflows and how its control loops operate.
---

## Core philosophy

Kokumi draws a hard line between three concerns that most delivery systems conflate:

1. **Intent** — what _should_ be built and how (the Order)
2. **Artifact** — what _was_ built, exactly (the Preparation)
3. **Activation** — what is _currently running_ (the Serving)

By keeping these separate and immutable at the artifact layer, Kokumi gives
you a complete, auditable history of every version ever produced and the
ability to promote or roll back with a single field change.

## Key advantages

### Immutability at the artifact layer

A Preparation is not a live snapshot. It is an OCI artifact identified by a
SHA-256 digest. Once a Preparation reaches `Ready`, it never changes.

- Reproduce exactly what was running at any point in time by re-fetching the
  artifact by digest.
- Drift is unambiguous. Compare the deployed digest to the desired digest.
  Any difference is a concrete, actionable signal.
- Artifacts can be signed, attested, and audited independently of the cluster.

### Separation of rendering from deployment

Kokumi keeps rendering and deployment as two distinct, independently
controllable steps:

```
Render                      Promote                    Deploy
Order ──▶ Preparation ─────────────────────────▶ Serving ──▶ Argo CD Application
           (immutable)    (human or auto)           (active pointer)
```

Because the rendered artifact is stored independently:

- **Approval gates** — set `spec.promotion.approvals` to require votes from
  named reviewers before a Preparation can be served, in both Manual and
  Automatic mode. See [Approvals](#approvals).
- **Manual promotion** — set `spec.promotion.mode: Manual` to hold the
  Serving until a human explicitly promotes the Preparation.
- **Pre-flight validation** — inspect the full rendered manifest in the UI
  before it touches any cluster.

### Rollback without re-rendering

Rolling back means promoting any previous Preparation. The artifact already
exists in the in-cluster registry, so the exact state that previously ran is
restored instantly: No re-render, no drift.

### Air-gap friendly by design

The entire pipeline — OCI pull → render → push to in-cluster registry — has no
requirement for outbound internet access. All external dependencies are OCI
artifacts that can be mirrored in advance, making Kokumi suitable for
restricted and disconnected environments.

### GitOps integration, not replacement

Kokumi does not apply manifests directly and does not own a sync loop. Each
Serving creates or updates an Argo CD `Application` pointing at the
Preparation's OCI artifact by digest. Kokumi feeds your existing GitOps
workflow rather than replacing it.

## Dependencies

Kokumi requires **Argo CD** (≥ 3.3) installed in the `argocd` namespace.
When a Serving is created or updated, the Serving controller creates or updates
an Argo CD `Application` resource that points to the immutable OCI artifact of
the selected Preparation. Argo CD then syncs that artifact into the cluster.

> **Kokumi does not apply manifests directly.** All runtime deployment is
> delegated to Argo CD. Without a running Argo CD instance, Servings will
> remain in a `Failed` state and no workloads will be deployed.

## Resource model

```
Order ──renders──▶ Preparation (immutable, versioned OCI artifact)
                         ▲
Serving ──selects────────┘  (mutable pointer to one Preparation)
   │
   └──creates/updates──▶ Argo CD Application
                              │
                              └──syncs──▶ Cluster workloads

Menu ──provides template──▶ Order  (consumed and parameterized)
Recipe ──provides render profile──▶ Order  (for example Helm options)
```

### Menu

A Menu is a reusable deployment template. It pins a source OCI reference and
version, locks the render type, and defines default values and patches.
Consumers create Orders that reference a Menu instead of specifying source
details directly.

Each Menu carries an **override policy** that controls what consumers are
allowed to customise:

| Policy | Behaviour |
|---|---|
| `All` | Consumer may supply any values or patches |
| `Restricted` | Consumer may only set values or patches that match the Menu's allow-list |
| `None` | Consumer may not override values or patches at all |

Values and patches have independent policies. For example a Menu can allow
unrestricted Helm value overrides while forbidding any patches, or restrict
values to a specific list of keys while allowing all patches.

When an Order references a Menu:

1. The Order controller fetches the Menu and validates the consumer's overrides
   against the override policy.
2. Base values and patches from the Menu are always applied first.
3. Allowed consumer overrides are merged on top (values are deep-merged, patches
   are appended).
4. If the consumer supplies overrides that violate the policy, reconciliation
   fails with a clear status message.

#### Source resolution and vendoring

The Menu controller always publishes the consumable source in the Menu's
`status.source` (OCI URL, version, optional Pantry reference for pull
credentials, and the resolved digest). Orders consume exactly what is
advertised there.

Optionally, a Menu can **vendor** its source: copy the artifact (Helm chart or
manifest bundle) to another OCI registry. Orders then consume the vendored
copy:

```yaml
spec:
  source:
    oci: oci://ghcr.io/kokumi-dev/testdata/external-secrets
    version: "0.1.0"
  vendor:
    destination:
      oci: oci://kokumi-registry.kokumi.svc.cluster.local:5000/preparation/external-secrets  # or a pantryRef
```

The destination is either a plain `oci://` URL (anonymous) or a `pantryRef`
(credentials from the referenced Pantry). Upstream credentials come from
`spec.source.pantryRef` when the upstream registry is private. The vendored
ref and its pull credentials are advertised in `status.source`, so Orders need
no knowledge of the vendoring setup.

### Recipe

Recipe captures rendering configuration, including options like Helm rendering
behavior and values-related settings.

### Order

Order is the concrete execution request. It binds source and runtime input and
triggers rendering to produce immutable artifacts.

Order does not need Menu. It can fully define component intent on its own, and
this standalone behavior is intended to remain a first-class mode.

Alternatively, an Order can reference a Menu via `spec.menuRef`. When a Menu
reference is present, the source, render type, and base configuration are
inherited from the Menu. The consumer provides only the overrides that the
Menu's policy permits.

An Order declares:

- **Source** (standalone mode) — OCI image reference: either a pre-rendered
  manifest bundle (containing `*.yaml` files) or a Helm chart in OCI format
  (add `spec.render.helm` to configure rendering)
- **MenuRef** (template mode) — reference to a Menu by name in the Order's
  namespace; source and render configuration are inherited
- **Patches** — Patches to apply before producing the artifact

Orders are mutable; every change triggers a new reconciliation cycle and
automatically produces a new Preparation.

### Preparation

Preparations are **created automatically** by Kokumi whenever an Order changes.
You never create them directly.

A Preparation is the _output_ of rendering an Order at a specific point in time.
It contains:

- A reference to the parent Order and the exact source revision used
- An OCI artifact digest (stored in the in-cluster OCI registry)
- An immutable status: once `Ready`, a Preparation never changes

Preparations are **never garbage-collected automatically**. You retain full
history and can promote any old Preparation to active at any time.

#### Linking a Preparation back to its source

Every Preparation records where its base OCI artifact came from, so you can
trace any deployed version back to the exact commit or tag that produced it.
Kokumi reads provenance from the base artifact's OCI annotations and copies
them onto the rendered Preparation artifact and its CR. The primary keys are
`org.opencontainers.image.source`, `org.opencontainers.image.version`, and
`org.opencontainers.image.revision`, with
fallbacks so artifacts that omit one still link correctly:

| Annotation (base artifact) | Fallback | Preparation field | Meaning |
|---|---|---|---|
| `org.opencontainers.image.source` | `org.opencontainers.image.url` | `spec.gitSource.repo` | SCM repository URL of the base artifact's source |
| `org.opencontainers.image.version` | — | `spec.gitSource.tag` | SCM tag of the base artifact's source |
| `org.opencontainers.image.revision` | — | `spec.gitSource.commitHash` | SCM commit SHA of the base artifact's source |

Kokumi also stamps the base artifact's identity onto the rendered artifact so
the chain is verifiable from the artifact alone:

- `org.opencontainers.image.base.name` — the base artifact's OCI reference
- `org.opencontainers.image.base.digest` — the base artifact's digest

#### What a Preparation looks like in the registry

A Preparation artifact explains itself from the registry alone: what was asked
for, what it was built from, what built it, and what came before it.

```
kokumi-registry.kokumi.svc.cluster.local:5000/shop/podinfo-prod
│
├─ sha256:c3…  manifest   artifactType: application/vnd.kokumi.preparation.v1
│   ├─ config  application/vnd.kokumi.preparation.config.v1+json     (intent)
│   ├─ layer   application/vnd.oci.image.layer.v1.tar+gzip           (rendered YAML)
│   └─ annotations
│        org.opencontainers.image.description: "bump replicas to 3"
│        org.opencontainers.image.source / .version / .revision
│        org.opencontainers.image.base.name / .base.digest
│        kokumi.dev/parent: sha256:c1…
│        kokumi.dev/approval-policy: {…}
│
├─ referrers of sha256:c3…
│   ├─ application/vnd.kokumi.provenance.v1+json   SLSA v1 provenance (in-toto)
│   └─ application/vnd.kokumi.approvals.v1+json    written on promotion
│
└─ tag 6.10.2 → sha256:c3…
```

The **config blob** is the authoritative record. The `kokumi.dev/*` and
description annotations repeat parts of it so tools can read them without
fetching a blob.

```json
{
  "schemaVersion": 1,
  "order": { "namespace": "shop", "name": "podinfo-prod", "uid": "6f0c…" },
  "configHash": "sha256:9b1d…",
  "commitMessage": "bump replicas to 3",
  "createdTime": "2026-10-12T09:14:03Z",
  "parent": "sha256:c1…",
  "source": { "oci": "oci://ghcr.io/stefanprodan/charts/podinfo", "version": "6.10.2", "digest": "sha256:aa…" },
  "destination": { "oci": "oci://kokumi-registry.kokumi.svc.cluster.local:5000/shop/podinfo-prod" },
  "menu": { "namespace": "shop", "name": "podinfo", "configHash": "sha256:77e0…" },
  "renderer": { "type": "Helm", "version": "v4.3.0", "builder": "ghcr.io/kokumi-dev/kokumi@sha256:bb…" },
  "intent": { "source": {}, "destination": "", "render": {}, "patches": [], "edits": [], "promotion": {} }
}
```

`createdTime` equals the manifest's `org.opencontainers.image.created`
annotation.

Artifacts that a Menu pre-renders use the artifactType
`application/vnd.kokumi.menu.v1` and the same config blob without `order`,
`parent` and promotion gates. Their `configHash` is computed the same way,
from their own intent; `menu.configHash` is the Menu's own hash.

#### Intent and the config hash

The **intent** is the merged, effective configuration that determines the
artifact. `configHash` is the SHA-256 of its canonical JSON: compact, object
keys sorted at every level, numbers kept as written, and no HTML escaping. The
config blob stores the `intent` member with exactly these bytes, so equal
hashes mean equal intent, and anyone can recompute the hash: it is the SHA-256
of the `intent` value as it stands in the blob.

- Changing the approval policy creates a new Preparation that needs fresh
  approvals. Switching the promotion mode does not.
- Changing a Menu in a way that alters the merged result (a new source
  version, other values or patches) renders a new Preparation for every Order
  on that Menu. Orders on a pre-rendered Menu render again when the Menu
  publishes a new artifact, because its digest is part of their intent.

{{< callout type="warning" >}}
The intent is stored inside the artifact, so **anyone who can pull the
artifact can read it**: Argo CD, cluster nodes, and anyone the artifact is
copied to. Do not put secrets into Helm values or patches. Pull access to a
destination repository equals read access to the intent of every Preparation
in it.

The reverse holds for writing. Artifacts are not signed yet, so intent and
lineage read from the registry are only as trustworthy as push access to the
repository.
{{< /callout >}}

#### Lineage

Lineage is the **render history of one Order in one repository**. Every
Preparation names the one rendered before it as its `parent`, in the config
blob and in the `kokumi.dev/parent` annotation. Walking `parent` backwards
gives the history, including Preparations that were never deployed.

- Nothing is ever written to an older artifact. A Preparation carries no
  pointer to later revisions, so copying one artifact with its referrers
  copies exactly that one revision and its evidence.
- The newest Preparation of an Order is `Order.status.latestArtifactDigest`.
  Without cluster access it is the version tag whose config blob has the
  newest `createdTime`: every Preparation is pushed under its source version,
  so the newest one is always behind a tag.
- A history ends at a Preparation without a `parent`, or at a `parent` that
  does not exist in the repository. The second case is normal after the
  Order's destination changed.

#### Provenance and renderer identity

`spec.renderer` records what rendered a Preparation:

| Field | Meaning |
|---|---|
| `type` | `Helm` or `Manifest` |
| `version` | The Helm library version for `Helm`, the Kokumi version for `Manifest` |
| `builder` | The controller that rendered it, normally its image reference with digest |

The controller determines `builder` once at start-up: the `KOKUMI_BUILDER_ID`
environment variable if set, otherwise the image ID of its own Pod, otherwise
`kokumi@<version>`. It is self-reported.

After the artifact is pushed, the Preparation controller attaches a
[SLSA v1 provenance](https://slsa.dev/spec/v1.0/provenance) statement as an
OCI referrer and records it in `status.attestations`. The statement is built
entirely from the config blob, so it can be rebuilt at any time with identical
bytes. Its `builder.id` is `https://kokumi.dev/builders/controller/v1`;
`builder.version` names the controller image and the rendering engine, and
the source is listed by its OCI URL and digest.

A Preparation becomes `Ready` only once the `ProvenanceAttached` condition is
true, and only a `Ready` Preparation is served, under both promotion modes:
promoting one that is not ready is refused, and the Serving keeps the current
deployment. An artifact whose config blob cannot be read as a Kokumi config
never gets provenance and is never ready.

The Attestations view lists every referrer of the artifact live from the
registry. Anyone who can push to the repository can attach one, so the view
marks the ones Kokumi recorded and shows the content of only those.

```bash
oras discover kokumi-registry.kokumi.svc.cluster.local:5000/shop/podinfo-prod@sha256:c3…
```

Provenance is not signed yet. Until it is, it documents how an artifact was
produced for supply-chain tooling; it does not prove it.

### Serving

A Serving tracks which Preparation is actively deployed. There is exactly one
Serving per Order, named after the Order, and it is **managed automatically**;
you never create one directly. The promotion mode is always read from the
Order's `spec.promotion.mode`:

- **Automatic** — Kokumi creates the Serving and always targets the newest
  `Ready` Preparation of the Order (`status.targetPreparationName`).
- **Manual** — click **Promote** on a Preparation in the Kokumi UI (or set the
  Serving's `spec.preparationName`). Rolling back is promoting an older
  Preparation.

When a Serving is reconciled, the controller:

1. Resolves the target Preparation and its immutable OCI artifact digest.
2. Enforces the approval gate of the target Preparation. If it is not
   approved, or its approvals are not sealed yet, the currently deployed
   Preparation stays active and the Serving's `Approved` condition explains
   why.
3. Verifies the opt-in on any pre-existing Argo CD `Application` and
   transitions the Serving to `Deploying`, recording the preparation as the
   observed (active) one.
4. Creates or updates the Argo CD `Application` in the `argocd` namespace,
   pointing `spec.source.repoURL` at the Preparation's OCI artifact and
   `spec.source.targetRevision` at its exact digest.
5. Keeps the `Deploying` status until the Application reports `Healthy` and
   is synced to the desired revision, then transitions the Serving to
   `Deployed`. A degraded Application surfaces as `DeploymentFailed`.

Rollback is promoting any previous Preparation. No re-rendering required.

### Approvals

An Order can require reviews before any of its Preparations is served,
similar to required reviews on a pull request:

```yaml
spec:
  promotion:
    mode: Manual            # or Automatic
    approvals:
      requiredApprovals: 2
      allowedGroups: [release-approvers]
```

- The policy is **copied into each Preparation** (`spec.approvalPolicy`) and
  recorded on its OCI artifact. Changing the policy produces a new
  Preparation that needs fresh approvals.
- Reviewers vote in the UI with **Approve** or **Request changes**. Each vote
  is an immutable `Approval` resource. Only the **latest vote per reviewer**
  counts, only reviewers in one of `allowedGroups` are eligible, and a single
  eligible "Request changes" blocks the Preparation.
- Both promotion modes are gated. In Automatic mode the newest Preparation is
  deployed as soon as it is approved; an older approved Preparation is never
  deployed in its place. In Manual mode a human still has to press Promote.
- When an approved Preparation is promoted, its votes are **sealed**: they are
  locked in `status.approval` and pushed as an in-toto attestation
  (`application/vnd.kokumi.approvals.v1+json`) that references the
  Preparation's artifact as an OCI referrer. Votes submitted later are ignored.
  List the attestation with `oras discover <artifact>`.

#### Who may vote and how identity is protected

- Votes are submitted **only through the kokumi server**, which takes the
  reviewer's identity (OIDC issuer, subject, username, email, groups) from the
  verified login session. The shared built-in admin account cannot vote.
- Whether a user may vote at all is plain Kubernetes RBAC: the custom verb
  `approve` on `preparations`, checked for the user's mapped ServiceAccount.
- The `approval-integrity` ValidatingAdmissionPolicy accepts new Approvals
  only from the `kokumi-server` ServiceAccount, rejects every update, and
  rejects deletes except by the namespace controller. The `status-integrity`
  policy lets only the kokumi controller write Approval, Preparation and
  Serving status (the Serving's target Preparation triggers sealing).
- Approvals are kept even when their Preparation or Order is deleted, so the
  history stays visible:

```bash
kubectl get approvals --field-selector spec.orderName=shop
kubectl get approvals --field-selector spec.preparationRef.name=shop-3f9a1c2d4e5f
```

The admission policies protect against everyone except cluster administrators
who can change the policies themselves, impersonate the `kokumi-server`
ServiceAccount, or write to etcd directly. Restrict those permissions and keep
an audit policy on `approvals` and `admissionregistration.k8s.io` resources.

### Menu and Recipe lifecycle

Recipe defines rendering behavior. Menu provides a reusable template with
override policies controlling what consumers can customise.
Order remains the concrete execution resource, whether standalone or template-
parameterized.

## Reconciliation loop

```
Watch Order ──▶ Render source ──▶ Push OCI artifact ──▶ Create/update Preparation
                                                                   │
Watch Preparation status ──────────────────────────────────────────▼
Serving selects Preparation ──▶ Create/update Argo CD Application
                                        │
                                        └──▶ Argo CD syncs manifests to cluster
```

Key properties:

- **Idempotent** — each reconcile produces the same output for the same input
- **Level-triggered** — the controller always acts on observed state, not events
- **Owner references** — Preparations are owned by their Order; clean deletion is automatic
- **Argo CD delegates deployment** — Kokumi never applies manifests directly; it only manages the Argo CD Application resource

## OCI source formats

Kokumi supports two source OCI artifact formats, selected by the presence or
absence of `spec.render`.

### Pre-rendered manifest bundle (default)

When `spec.render` is absent, the source OCI artifact must contain one or more
`*.yaml` files at its root holding all Kubernetes resources (single or multi-
document YAML). The files are stored as-is, with no rendering step applied.

Supported layouts include:

**Single manifest file**
```
myapp:v1.0.0  (OCI artifact)
└── manifest.yaml   ← all Kubernetes resources (pre-rendered)
```

**Multiple manifest files**
```
myapp:v1.0.0  (OCI artifact)
|── deployment.yaml   ← Kubernetes Deployment
└── service.yaml      ← Kubernetes Service
```

This is the simplest format and is well-suited to components whose manifests
are already generated upstream and published as OCI bundles.

### Helm chart in OCI format

When `spec.render.helm` is present, the source OCI artifact must be a standard
Helm chart packaged and pushed to an OCI registry (e.g. via `helm push`).
Kokumi runs `helm template` internally to render the chart into a manifest
bundle, then stores the output as an immutable Preparation artifact.

```yaml
spec:
  source:
    oci: oci://ghcr.io/stefanprodan/charts/podinfo
    version: "6.10.2"
  render:
    helm:
      namespace: default
      values:
        ui:
          color: "#EF6461"
          message: "Hello from Kokumi"
          logo: "https://kokumi.dev/images/logo.png"
```

Available `render.helm` fields:

| Field | Description | Default |
|---|---|---|
| `releaseName` | Helm release name passed to `helm template` | Order name |
| `namespace` | Target namespace (`--namespace`) | Order namespace |
| `includeCRDs` | Include CRDs in the rendered output (`--include-crds`) | `false` |
| `values` | Inline Helm values merged last (highest priority) | — |

Helm OCI charts are first-class in Kokumi. Any chart published to an OCI
registry, whether an upstream community chart or an internally-built one,
can be used as an Order source.

## OCI registry

Kokumi ships an in-cluster OCI-compatible registry (backed by a `PersistentVolumeClaim`)
that stores rendered manifests as OCI artifacts. This means:

- Zero external registry dependency
- Rendered manifests are portable: pull them with any OCI client
- Artifact digests are content-addressed; deduplication is automatic

## Deployment architecture

Kokumi pulls a source artifact from a registry, renders it into an immutable artifact, and stores it in a destination registry. Argo CD then fetches the manifests by content digest and syncs them to the cluster. The source OCI reference on the Order accepts any registry (including the in-cluster one), and the destination defaults to the in-cluster registry when omitted but can be set to an external registry instead.

```mermaid
%%{init: {
  "theme": "base",
  "themeVariables": {
    "lineColor": "#313638",
    "clusterBkg": "#f7f5f0",
    "clusterBorder": "#c5c2bb",
    "edgeLabelBackground": "#f7f5f0"
  }
}}%%
flowchart LR
    subgraph ext["External"]
        ER[("External<br>Registry")]
    end

    subgraph k8s["Kubernetes Cluster"]
        direction TB
        KC["Kokumi<br>Controller"]
        IR[("In-Cluster<br>Registry")]
        ARGO["Argo CD"]
        WL(["Workloads"])
    end

    ER -- "1. Pull source<br>OCI artifact" --> KC
    KC -- "2. Push rendered<br>artifact + digest" --> IR
    KC -- "3. Create Application<br>(digest ref)" --> ARGO
    ARGO -- "4. Pull manifests<br>by digest" --> IR
    ARGO -- "5. Sync & deploy" --> WL

    classDef registry   fill:#E4B363,stroke:#b8894a,color:#313638
    classDef controller fill:#EF6461,stroke:#c94d4a,color:#ffffff
    classDef argocd     fill:#E8E9EB,stroke:#313638,color:#313638
    classDef workload   fill:#313638,stroke:#1a1f22,color:#E8E9EB

    class ER,IR registry
    class KC controller
    class ARGO argocd
    class WL workload
```
