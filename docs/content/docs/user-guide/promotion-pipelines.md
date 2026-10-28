---
title: Promotion Pipelines
weight: 7
description: Order the variants of a Product into stages that each deploy a Product change only after the stages before them ran it healthily.
---

A promotion pipeline orders the Orders of a Product into stages, such as dev,
staging and prod. Each Order names the Orders it runs after. A Product change
reaches a stage only once every stage before it ran the same change
healthily, for as long as the stage asks.

**Nothing moves between the stages.** A Product change renders every Order
into its own Preparation, with its own values, patches and image digests.
Each stage deploys only its own Preparations; the pipeline only decides when.
This is the model of [GitOps Promoter](https://argo-gitops-promoter.readthedocs.io):
every environment hydrates its own manifests from one dry commit, and is
promoted once the environments before it run that dry commit healthily.

## Define the stages

Name the upstream stages in `spec.promotion.after` of each Order. The
promotion-pipeline example runs one Product in three stages: `podinfo-dev`
deploys every change, `podinfo-staging` deploys what dev runs healthily, and
`podinfo-prod` deploys what you promote once staging ran it healthily for a
minute.

{{< tabs >}}
{{< tab name="order-staging.yaml" >}}
{{< example file="promotion-pipeline/order-staging.yaml" >}}
{{< /tab >}}
{{< tab name="order-prod.yaml" >}}
{{< example file="promotion-pipeline/order-prod.yaml" >}}
{{< /tab >}}
{{< tab name="order-dev.yaml" >}}
{{< example file="promotion-pipeline/order-dev.yaml" >}}
{{< /tab >}}
{{< tab name="product.yaml" >}}
{{< example file="promotion-pipeline/product.yaml" >}}
{{< /tab >}}
{{< /tabs >}}

Each stage deploys to an Environment of its own, which an admin creates
first in the namespace; see [Group stages into environments](#group-stages-into-environments):

{{< example file="promotion-pipeline/platform/environments.yaml" >}}

{{< apply-example name="promotion-pipeline/platform" >}}

Then apply it:

{{< apply-example name="promotion-pipeline" >}}

Every entry of `after` holds:

- **`order`** names an Order in the same namespace. It must be a variant of
  the same Product.
- **`soakSeconds`** is how long the upstream stage must have run its
  Preparation of the change healthily, without interruption. Without it, the
  stage qualifies as soon as Argo CD reports it synced and healthy.
- An Order lists at most 8 upstream stages and cannot run after itself.
- Like approvals, the stages are part of the intent: they are copied into
  each Preparation, and changing them renders a new Preparation. Add
  `spec.promotion.approvals` to a stage to also require reviews; see the
  approvals example in [Approvals](../../architecture/#approvals).

## Group stages into environments

A pipeline usually runs through environments, such as dev, staging and prod,
and often runs several stages in one, such as one per cluster group:
`int-eu` first, then `int-us` once it ran healthily, each an Environment of
its own clusters. Each Order names the [Environment](../environments/) of its
namespace it deploys to in `spec.environmentRef`:

```yaml
spec:
  environmentRef:
    name: int-eu
```

Environments belong to the admins of the namespace: they decide which
clusters an environment covers, through which Argo CD project, and which
environments a stage must run after first. The promotion-pipeline example's
three Environments all deploy to the cluster Kokumi runs in; prod requires
every stage that deploys there to run after a stage of staging, for at least
60 seconds. A stage that skips staging is held: its Serving reports
`UpstreamDeployed=False` with reason `PipelineRequired`.

The order the stages deploy in still comes from `after`. The Product's Orders
graph draws each Environment as a column, after the Environments it runs
after, and stacks its Orders by wave: an Order runs one wave after the
furthest Order of its Environment it runs after. Only the edges between
Environments are drawn; the wave number's tooltip names what an Order runs
after inside its Environment. Environments that run after each
other in a circle, such as an `int` stage after a `prod` stage that runs
after `int`, cannot be columns; the graph then draws the stages alone and
says why.

The Environment decides where a Preparation runs, not what it is: it is not
part of the intent, and setting it renders nothing. It is set when the Order
is created and cannot be changed, so that no Order reaches an Environment
without passing its gates: to deploy to another Environment, create another
Order. Select an Environment's Orders by it:

```bash
kubectl get orders -n shop                                       # the ENVIRONMENT column
kubectl get orders -n shop --field-selector spec.environmentRef.name=staging
```

## What a Product change is: the Product revision

The Preparations of one Product change are linked by the **Product
revision**, the SHA-256 of what the Product contributes to them: the source
digest, its render config and patches, and its image handling. Every
Preparation records it in `spec.productRevision`:

```bash
kubectl get preparations -n shop --field-selector spec.productRevision=sha256:5e1f…
```

A change to the Product renders every Order again, also an Order whose own
values or patches hide the change, so every stage has a Preparation of every
revision. The source URL and version, the overrides and the Registries are
not part of the revision. Neither are the digests images resolve to: two
stages may run different digests of the same tag when their renders straddle
a tag move upstream.

## How a stage waits

A Serving deploys a Preparation once, for each upstream stage, the upstream
Serving's history holds a deployment of its own Preparation of the same
revision that

- was healthy for `soakSeconds`, without degrading,
- and whose signed deployment record verifies in the registry.

A stage is healthy once every cluster of its Environment whose Cluster is
Ready runs the Preparation synced and healthy, and at least one does. A
Cluster that is not Ready is left out: it holds back neither the soak nor the
stages after it. One degraded cluster ends the healthy time; see
[Deploy to Several Clusters](../environments/#what-runs-where).

An upstream stage that has moved on to a newer revision since still qualifies
the older one. The Serving reports the gate in its `UpstreamDeployed`
condition:

| Reason | Meaning |
|---|---|
| `Satisfied` | Every upstream stage deployed the revision, or this stage promoted the Preparation before. |
| `Soaking` | An upstream stage runs the revision healthily; the message says when its soak ends. |
| `NotDeployedUpstream` | An upstream stage has not deployed the revision healthily, has nothing deployed, or does not exist yet. |
| `InvalidPipeline` | An upstream Order is a variant of another Product. |
| `SignatureMissing`, `SignatureInvalid` | The upstream deployment record is missing from the registry, or does not verify against the Kitchen's keys. |

While a soak runs, the Serving reconciles again when it ends. Under
**Automatic**, the Serving waits on its newest Ready Preparation; an older one
that passes is never deployed in its place. Under **Manual**, promote a
Preparation at any time: it is queued, and the Serving deploys it once its
upstream stages deployed its revision and its other gates pass. The UI shows
it as queued, with the gate it waits for, and offers **Withdraw**. Promoting
another Preparation replaces it (see
[Queued promotions](../../architecture/#queued-promotions)).

The gates run in this order: upstream stages, promotion checks, approvals,
then the evidence in the registry. A Preparation becomes the Serving's target,
which seals its votes, only once its upstream stages and checks pass. The
gates hold new deployments only: what is deployed stays deployed.

**Rollback.** A Preparation the stage promoted before passes the upstream
gate again, so rolling back never waits for upstream stages that moved on.

**Invalid pipelines.** An Order whose `after` names an Order of another
Product, or whose stages lead back to itself, reports `Ready=False` with
reason `InvalidPipeline` and renders nothing until the stages are fixed.

## The deployment history

Each Serving keeps its 16 most recent deployments in `status.history`, newest
first:

```bash
kubectl get serving podinfo-prod -n shop -o jsonpath='{.status.history}' | jq
```

An entry records the Preparation, its digest and Product revision, when it was
promoted (`startedTime`), when Argo CD first reported it healthy
(`healthyTime`), and when and why it ended (`Superseded` by the next
promotion, or `Degraded`). A recovery after `Degraded` opens a new entry, so a
soak counts only uninterrupted healthy time. The deployed Preparation is the
newest entry with a `promotionRecord`.

Deleting an Order deletes its Serving and its history. A stage that is created
again qualifies revisions again from its new deployments.

## Signed records

Every promotion and every healthy deployment is recorded in the registry,
signed with the Kitchen key, as a referrer of the deployed artifact:

| Record | Predicate type | Written when |
|---|---|---|
| Promotion | `https://kokumi.dev/attestations/promotion/v1` | the Preparation passed its gates |
| Deployment | `https://kokumi.dev/attestations/deployment/v1` | Argo CD first reported it healthy |

The promotion record names the Order, the Preparation and the one deployed
before, the Product revision, the promotion mode, and the evidence of every
gate: the approval attestation, the upstream deployment records with their
healthy times, and the vulnerability report of each image. The deployment
record names the promotion it completes, and the clusters that ran the
Preparation healthily. So each stage's promotion points to
the upstream deployment that qualified it, and the chain can be walked from
the registry alone.

Verify them with cosign and the Kitchen's public key (see
[Signing](../../operator-guide/signing/)):

```bash
cosign verify-attestation --key kokumi.pub --insecure-ignore-tlog=true \
  --type https://kokumi.dev/attestations/promotion/v1 <registry>/shop/podinfo-prod@sha256:c3…
cosign verify-attestation --key kokumi.pub --insecure-ignore-tlog=true \
  --type https://kokumi.dev/attestations/deployment/v1 <registry>/shop/podinfo-staging@sha256:9f…
```

## In the UI

- **Order form:** "Environment" picks the Environment of the stage, among
  those of the Order's namespace, when the Order is created; "Runs after"
  picks the other Orders of the Product and the soak after each.
- **Order overview:** the Pipeline section lists the stages of the pipeline,
  what each deploys, its revision and since when it is healthy, and what holds
  the Order's newest Preparation.
- **Product Orders graph:** each environment is a column, titled with its
  name, and its stages are numbered by wave.
  Stages that run the current Product revision are marked. A stage whose
  newest Preparation is not deployed says what holds it: "Waits for
  approvals", "Queued · upstream" for a promotion that waits, or "Not
  promoted"; its tooltip says why. The Products list shows the same, and the
  Pipeline section names each stage's environment.
- **Promote dialog and Preparation overview:** one line per gate, including
  the upstream stages and when a soak ends. While a gate holds, the dialog
  queues the promotion.
- **Queued promotions:** a promoted Preparation that waits for its gates
  shows "Queued" and the gate on the Preparation, in the Order's Preparations,
  as "Next Preparation" on the Order and Serving overviews, and as "Queued" in
  the Servings list.
- **Dashboard, open promotions:** what still needs a person, with what it
  waits for ("1 approval from release-approvers, then promote") and your
  step: Review while you are an eligible approver who has not approved yet,
  then Promote. Who may only promote can promote before the approvals; the
  promotion is queued and deploys once they arrive. A Preparation that only
  waits for soaks or checks is not listed.
- **Serving History tab:** the deployments with their records, which link to
  the Artifact tab where they are shown verified.

## Who may change the stages

Whoever may edit an Order may change its `after`, as they may change its
approvals. Gates that authors cannot remove are a separate, platform-level
concern.
