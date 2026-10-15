---
title: Authorization
weight: 2
description: Authorization is enforced entirely through native Kubernetes RBAC via identity-to-ServiceAccount mapping and impersonation.
---

Kokumi has **no application-level permission model**. Everything a user can
do, through the UI, the API, or `kubectl`, is decided by ordinary Kubernetes
`Role`/`ClusterRole` bindings on ServiceAccounts. A user's permissions are
identical whether they use the kokumi UI or `kubectl` directly, and RBAC still
applies to users who bypass the UI entirely.

## How it works

1. **Authentication** - the server validates the OIDC token (or admin
   credentials) and extracts stable identity claims: `sub`, `email`, `groups`.
2. **Mapping** - the identity is mapped to one or more Kubernetes
   ServiceAccounts in the install namespace via annotations (below).
3. **Execution** - every API operation the server performs on the user's behalf
   is executed **as the mapped ServiceAccount** using Kubernetes
   impersonation. The API server evaluates plain RoleBindings against that
   ServiceAccount and returns real 403s, which the UI surfaces.

The cluster's API server never needs to trust the OIDC provider: the kokumi
server is the only audience of the identity provider, and it translates
identities to ServiceAccounts that the cluster already understands.

## Identity-to-ServiceAccount mapping

ServiceAccounts in the install namespace (e.g. `kokumi`) are mapped to users
via annotations:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: custom-sa
  namespace: kokumi
  annotations:
    kokumi.dev/identity-sub: "1234567890"          # matches the OIDC "sub" claim
    kokumi.dev/identity-email: "admin@kokumi.dev"  # matches the "email" claim
    kokumi.dev/identity-groups: "platform,admins"  # matches ANY of the token's groups
```

Matching rules:

- An identity matches a ServiceAccount when its `sub` equals
  `kokumi.dev/identity-sub`, its `email` equals `kokumi.dev/identity-email`,
  or any of its `groups` appears in the comma-separated
  `kokumi.dev/identity-groups` list.
- Multiple annotations on one ServiceAccount are OR-ed.
- A user may match **multiple** ServiceAccounts; reads (lists and gets) are
  merged across all of them, so the effective read access is the **union**.
  Writes are performed as the first ServiceAccount authorized for the
  operation.
- Users who match no ServiceAccount are authenticated but authorized for
  nothing (every operation returns 403).

### Adding your own ServiceAccount

A ServiceAccount you add needs two grants besides its annotations.

**1. Allow kokumi-server to impersonate it.** The server may only impersonate
the ServiceAccounts named in the `kokumi-server-impersonator` Role, by default
the three built-in ones. This keeps it from acting as any other ServiceAccount
in the install namespace, such as the controller's. Grant each additional
ServiceAccount in its own Role, so upgrades do not overwrite it:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: kokumi-server-impersonate-custom-sa
  namespace: kokumi
rules:
- apiGroups: [""]
  resources: ["serviceaccounts"]
  resourceNames: ["custom-sa"]
  verbs: ["impersonate"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: kokumi-server-impersonate-custom-sa
  namespace: kokumi
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: kokumi-server-impersonate-custom-sa
subjects:
- kind: ServiceAccount
  name: kokumi-server
  namespace: kokumi
```

Without this grant every check as `custom-sa` fails. The user keeps the access
of their other mapped ServiceAccounts.

**2. Grant its permissions** with ordinary RBAC, the same objects you would use
for any other subject:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: custom-sa-editor
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: kokumi-server-editor-role
subjects:
- kind: ServiceAccount
  name: custom-sa
  namespace: kokumi
```

A `RoleBinding` in a single namespace is enforced for API calls, but the UI
does not use it: lists come from the live event stream, and the
[UI capabilities](#ui-capabilities) are evaluated across all namespaces. Use a
`ClusterRoleBinding` for users of the UI.

Check both grants with kubectl:

```bash
kubectl -n kokumi auth can-i impersonate serviceaccounts/custom-sa \
  --as=system:serviceaccount:kokumi:kokumi-server
kubectl auth can-i create orders.delivery.kokumi.dev --all-namespaces \
  --as=system:serviceaccount:kokumi:custom-sa
```

## Built-in ServiceAccounts

The installation ships three identity ServiceAccounts in the install namespace:

| ServiceAccount | Default binding | Default identity mapping | Purpose |
| -------------- | ---------------- | ------------------------ | ------- |
| `kokumi-admin` | `kokumi-server-role` (full CRUD incl. `kitchens`) | `kokumi.dev/identity-groups: "kokumi-admin"` | Full access; also used by the built-in admin login |
| `kokumi-editor` | `kokumi-server-editor-role` (CRUD, no `kitchens`) | `kokumi.dev/identity-groups: "kokumi-editor"` | Editors |
| `kokumi-viewer` | `kokumi-server-viewer-role` (read-only) | `kokumi.dev/identity-groups: "kokumi-viewer"` | Read-only users; cannot read Secrets |

The built-in admin login always acts as `kokumi-admin`, regardless of the
annotations. Additionally, OIDC users are matched against the default
`kokumi.dev/identity-groups` values: with the defaults, an OIDC identity whose
token carries the `kokumi-admin` group gets full access, `kokumi-editor`
editor access, and `kokumi-viewer` read-only access (multiple matches are
merged as described above).

To grant the `devops` OIDC group editor access:

```bash
kubectl -n kokumi annotate sa kokumi-editor \
  kokumi.dev/identity-groups=devops --overwrite
```

Editors and admins can manage **Pantry credential Secrets** (docker-registry
`Secret` objects the UI creates when a Pantry is saved with direct
credentials, and reads when previewing an Order whose source references that
Pantry). Viewers cannot read Secrets: previewing a Pantry-backed Order will
return 403, while plain-OCI previews work without Secret access.

## UI capabilities

The UI decides which features to offer from a list of **capabilities** the
server reports for the signed-in user (`GET /api/v1/whoami`). Each capability
is derived from SelfSubjectAccessReviews on the mapped ServiceAccounts, so it
always follows your RBAC. Pages the user can never use (such as Settings) are
hidden; actions the user may not perform (such as Promote) are shown disabled
with an explanation.

Capabilities are display hints only. Every operation is still authorized by
the API server, so a user who calls the API directly gets exactly the same
answer.

| Capability | Granted when a mapped ServiceAccount may | UI |
| ---------- | ---------------------------------------- | -- |
| `settings.general:update` | `update` the `kitchens/default` object in the install namespace | Settings, General tab |
| `settings.auth:update` | `update` the `kitchens/default` object in the install namespace | Settings, Authentication tab |
| `orders:create` / `orders:update` / `orders:delete` | `create` / `update` / `delete` `orders` | New, Edit, Delete Order; edits on rendered files |
| `products:create` / `products:update` / `products:delete` | `create` / `update` / `delete` `products` | New, Edit, Delete Product |
| `pantries:create` / `pantries:update` / `pantries:delete` | `create` / `patch` / `delete` `pantries` | New, Edit, Delete Pantry |
| `preparations:promote` | `create` and `update` `servings` | Promote and Rollback |
| `preparations:approve` | `approve` `preparations`, and the user signed in with OIDC | Approve and Request changes |

Except for the settings, capabilities are evaluated **across all namespaces**:
a permission granted only by a namespaced `RoleBinding` does not enable the
action in the UI. This matches the live event stream, which only serves users
who may list resources cluster-wide. The settings capabilities share one RBAC
check today; they are reported separately so they can diverge later.

Results are cached for up to 90 seconds, and the UI refreshes them whenever the
browser window regains focus. A check that fails, for example on a mapped
ServiceAccount the server may not impersonate, does not fail the request: the
capability is granted if another mapped ServiceAccount allows it. Otherwise it
is reported as `undetermined`, and the UI keeps its previous answer for it, or
denies it on the first load.

## The kokumi-server ServiceAccount

The server's own ServiceAccount holds **no write permissions on kokumi
resources**. It can only:

- `impersonate` the identity ServiceAccounts in the install namespace, by
  default `kokumi-admin`, `kokumi-editor` and `kokumi-viewer` (see
  [Adding your own ServiceAccount](#adding-your-own-serviceaccount)). This is
  the trust boundary: treat access to this ServiceAccount as cluster-admin
  equivalent for kokumi resources,
- read ServiceAccounts (mapping resolution),
- read Secrets in the install namespace (login/OIDC config),
- read (get/list/watch) kokumi resources to power the live UI event stream:
  the hub must be able to see all events in order to filter them per user
  (each connected user only receives events for resources their mapped
  ServiceAccounts may list). The server never writes kokumi resources with
  its own identity.

## UI and kubectl parity

Because authorization is plain Kubernetes RBAC on the mapped ServiceAccount,
the same view is available from kubectl:

```bash
kubectl auth can-i list orders \
  --as=system:serviceaccount:kokumi:kokumi-editor

# Inspect exactly what the UI sees
kubectl get orders --as=system:serviceaccount:kokumi:kokumi-editor
```

Audit logs on the API server record both identities: the kokumi-server
ServiceAccount (the impersonator) and the mapped ServiceAccount (the
impersonated user).
