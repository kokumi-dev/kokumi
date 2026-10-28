---
title: Approvals
weight: 3
description: Require reviews from groups of people before a Preparation is deployed.
---

An Order can require reviews before any of its Preparations deploys, like
required reviews on a pull request. The approvals example deploys each
Preparation once two members of `release-approvers` approved it:

{{< example file="approvals/order.yaml" >}}

{{< apply-example name="approvals" >}}

| Field | Meaning |
|---|---|
| `requiredApprovals` | How many distinct reviewers must approve |
| `allowedGroups` | The groups a reviewer must belong to, from their OIDC login |

## How a vote counts

- Reviewers vote in the UI with **Approve** or **Request changes**. Each vote
  is an immutable `Approval` resource.
- Only the **latest vote per reviewer** counts, and only reviewers in one of
  `allowedGroups` are eligible.
- A single eligible **Request changes** blocks the Preparation.
- The policy is copied into each Preparation (`spec.promotion.approvals`) and
  recorded in the intent of its artifact. Changing the policy renders a new
  Preparation that needs fresh approvals.

Both promotion modes are gated. Under `Automatic`, the newest Preparation
deploys as soon as it is approved; an older approved Preparation is never
deployed in its place. Under `Manual`, someone still has to click
**Promote**, before or after the approvals: the promotion is
[queued](../promotion/#queued-promotions) until they are given. The
Dashboard lists the Preparations that wait for a review.

## Sealing

When an approved Preparation becomes the Serving's target, after its
upstream stages and checks pass, its votes are **sealed**: they are locked in
the Preparation's `status.approval` and pushed as a signed attestation
(predicate type `https://kokumi.dev/attestations/approval/v1`) that
references the artifact as an OCI referrer. Votes submitted later are
ignored. Before it deploys, the Serving verifies the sealed approvals in the
registry, not the Preparation's status.

Verify them with cosign and the Kitchen's public key (see
[Signing](../../operator-guide/signing/#verify-an-artifact)):

```bash
cosign verify-attestation --key kokumi.pub --insecure-ignore-tlog=true \
  --type https://kokumi.dev/attestations/approval/v1 <registry>/shop/podinfo-approvals@sha256:c3…
```

## Who may vote

- Votes are submitted **only through the Kokumi server**, which takes the
  reviewer's identity (OIDC issuer, subject, username, email, groups) from
  the verified login session. The built-in admin login cannot vote, since a
  vote needs a personal OIDC login.
- Whether a user may vote at all is Kubernetes RBAC: the custom verb
  `approve` on `preparations`, checked for the ServiceAccounts the user maps
  to. The built-in admin and editor roles both hold it; see
  [Authorization](../../operator-guide/authorization/#built-in-serviceaccounts).
- Whoever may edit an Order may change its approval policy, as they may
  change its other gates. The new policy applies to the Preparations
  rendered from then on.

## Approvals are kept

Approvals stay when their Preparation or Order is deleted, so the history
stays visible:

```bash
kubectl get approvals -n shop --field-selector spec.orderName=podinfo-approvals
kubectl get approvals -n shop --field-selector spec.preparationRef.name=podinfo-approvals-3f9a1c2d4e5f
```

## What protects them

- The admission policy `kokumi-approval-integrity` accepts new Approvals only
  from the `kokumi-server` ServiceAccount, rejects every update, and rejects
  deletes except by the namespace controller.
- The policy `kokumi-status-integrity` lets only the Kokumi controller write
  the status of Kokumi's resources, so nobody else can set the Serving's
  target, which triggers sealing.

The policies protect against everyone except cluster administrators who can
change the policies themselves, impersonate the `kokumi-server`
ServiceAccount, or write to etcd directly. Restrict those permissions, and
keep an audit policy on `approvals` and on `admissionregistration.k8s.io`
resources.
