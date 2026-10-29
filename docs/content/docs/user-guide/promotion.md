---
title: Promotion and Rollback
weight: 2
description: How a Preparation becomes the one that runs, what holds it, and how to roll back.
---

Every Order has one **Serving**, named after it, that holds which of its
Preparations is promoted and deploys it to every cluster of the Order's
Environment. You never write a Serving yourself: `spec.promotion` on the
Order decides how it moves.

## Automatic and Manual

```yaml
spec:
  promotion:
    mode: Manual          # Manual (default) | Automatic
```

- **Automatic**: the Order creates its Serving, and every new `Ready`
  Preparation is promoted once it passes its gates. An older Preparation is
  never deployed in place of the newest one.
- **Manual**: nothing deploys until you click **Promote** on a Preparation in
  the UI. The first promotion creates the Serving, which the Order then
  adopts; until then the Order has no Serving.

Promote is refused for an Order under `Automatic`, and for a Preparation that
is not signed and `Ready`.

## Gates

A promoted Preparation deploys once it passes every gate, in this order:

| Gate | Set by | Condition on the Serving | See |
|---|---|---|---|
| Upstream stages | `spec.promotion.after`, and the Environment's `promotion.requireAfter` | `UpstreamDeployed` | [Promotion Pipelines](../promotion-pipelines/) |
| Checks | `spec.promotion.checks` | `ChecksPassed` | [Vulnerability Scanning](../security-scanning/#holding-promotions-on-findings) |
| Approvals | `spec.promotion.approvals` | `Approved` | [Approvals](../approvals/) |
| Evidence | always | `Verified` | [below](#evidence-in-the-registry) |

The Preparation becomes the Serving's target
(`status.targetPreparationName`) once it passes its upstream stages and
checks, which seals its approvals. Each condition explains what still holds
it.

When it passes, the Serving records the promotion: a new entry in
`status.history`, and a signed promotion record attached to the artifact that
names the evidence of every gate.

The gates hold new deployments only. A Preparation that is deployed stays
deployed when a gate stops passing, for example when a new scan reports
findings.

### Evidence in the registry

Argo CD verifies no OCI signatures, so the Serving verifies the artifact in
the registry before every promotion, not the Preparation's status:

- a signature by a key the Kitchen trusts;
- a signed provenance that names this Serving's Order, so that an artifact
  rendered for another Order cannot be deployed here;
- signed approvals with result `Approved`, when an approval policy applies.

Otherwise `Verified` reports `SignatureMissing` or `SignatureInvalid`,
`SigningKeyNotReady` while the Kitchen has no key yet, or the reason the
registry could not be read. Argo CD then pulls exactly the digest that was
verified. Results are cached per digest until the trusted keys change.

## Queued promotions

Under `Manual`, **Promote** only names the Preparation in the Serving's
`spec.preparationName`. The Serving deploys it once it passes every gate;
until then the promotion is **queued**, and the Serving keeps what it
deploys.

- **One per Order.** Promoting another Preparation replaces the queued one;
  promotions do not line up.
- **Withdraw** by promoting the deployed Preparation again. The UI offers
  **Withdraw** on a queued Preparation.
- **Manual pins it.** A queued Preparation deploys even when newer ones are
  rendered in the meantime. Promote the newer one to take it instead.
- **It may deploy long after the click**, for example when a day-long soak
  ends. The UI marks it as queued, with the gate it waits for, on the
  Preparation, the Order and the Serving.
- **The Dashboard's open promotions** list what still needs a person: an
  approval under an approval policy, or a promote under `Manual`. Each row
  offers the viewer one step: **Review** while they may approve and have not,
  then **Promote**. A queued Preparation stays there while it waits for
  approvals, and leaves once only the Serving's own gates, such as soaks and
  checks, hold it.

## Rollback

Rolling back is promoting an earlier Preparation. It already exists in the
registry, so nothing is rendered again.

- An Order under `Automatic` always deploys its newest Preparation: switch it
  to `Manual` first. The mode is not part of the intent, so switching renders
  nothing.
- A Preparation the Order deployed before passes its upstream stages again,
  so a rollback is never held by an upstream stage that has moved on. Its
  other gates still apply.

## The Argo CD Applications

The Serving deploys through one Argo CD `Application` per Cluster of the
Order's Environment, in the namespace `argocd`:

- named `kokumi-<namespace>-<order>-<hash>`, where the hash tells apart
  Orders whose names hyphens would join, and the clusters of one Order;
- with `spec.source` pointing at the Preparation's artifact, and
  `targetRevision` at its digest;
- with `spec.destination` at the Cluster's API server and the Preparation's
  [target namespace](../products-and-orders/#the-target-namespace), in the
  Argo CD project the Environment names;
- with automated sync, prune and self-heal, server-side apply, and
  `CreateNamespace`.

```bash
kubectl get applications -n argocd -l delivery.kokumi.dev/serving=podinfo,delivery.kokumi.dev/serving-namespace=shop
# NAME                          SYNC STATUS   HEALTH STATUS
# kokumi-shop-podinfo-3f9a1c2e  Synced        Healthy
```

Kokumi only updates or deletes an Application it did not create if the
Application carries the annotation
`delivery.kokumi.dev/allowed-order: "<namespace>/<order>"` of the Serving's
Order; Kokumi adds it to the Applications it creates. A pre-existing
Application without it is never modified, its cluster is held with reason
`OptInRequired`, and deleting the Serving keeps it.

Argo CD needs read access to the registry the Preparations are in; for the
bundled registry, see
[Installation](../../installation/#argo-cd-registry-credentials).

## The Serving's status

```bash
kubectl get servings -n shop
# NAME      ORDER     TARGET                 READY   REASON     APPROVED              VERIFIED             AGE
# podinfo   podinfo   podinfo-d7ce0c46a686   True    Deployed   ApprovalNotRequired   SignaturesVerified   10s
```

`Ready` describes what runs:

| Reason | Meaning |
|---|---|
| `Pending` | Nothing is deployed yet; the message says what the Serving waits for, such as the Preparation `spec.preparationName` names. |
| `Deploying` | Not every target on a Ready Cluster runs the deployed Preparation healthily yet. |
| `Deployed` | Every target on a Ready Cluster, and at least one, reports `Healthy`, synced to the deployed digest. |
| `DeploymentFailed` | A target is degraded at the deployed digest or held, or none of the Environment's Clusters is Ready. |
| `EnvironmentNotFound`, `InvalidSelector`, `NoClusters`, `ProjectNotFound`, `ProjectNotPermitted`, `ArgoCDNotInstalled` | The Order may not deploy at all. The Serving deletes its Applications. |
| `PreparationNotFound` | The deployed Preparation was deleted. The Serving keeps its Applications, which run the artifact from the registry, until another Preparation is promoted. |

While a newer Preparation waits for a gate, the message of `Ready` says what
holds it. How each cluster is counted and reported is in
[Deploy to Several Clusters](../environments/#what-runs-where).

`status.history` keeps the 16 most recent deployments: when each was
promoted, when its targets on Ready Clusters were first healthy, and when it
ended, `Superseded` by the next promotion or `Degraded` on any target. A
recovery after `Degraded` opens a new entry. Once a deployment is healthy,
the Serving attaches a signed deployment record that names the clusters that
run it. It also records the vulnerability findings of what it deploys in
`status.deployedVulnerabilities`, and emits a Warning Event when they
increase.
