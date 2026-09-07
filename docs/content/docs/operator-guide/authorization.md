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

Permissions are then granted with ordinary RBAC, the same objects you would
use for any other subject:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: custom-rb
  namespace: dev
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: kokumi-server-editor-role
subjects:
- kind: ServiceAccount
  name: custom-sa
  namespace: kokumi
```

## Built-in ServiceAccounts

The installation ships three identity ServiceAccounts in the install namespace:

| ServiceAccount | Default binding | Purpose |
| -------------- | ---------------- | ------- |
| `kokumi-admin` | `kokumi-server-role` (full CRUD incl. `kitchens`) | Used by the built-in admin login; OIDC users can be mapped via annotations |
| `kokumi-editor` | `kokumi-server-editor-role` (CRUD, no `kitchens`) | Editors |
| `kokumi-viewer` | `kokumi-server-viewer-role` (read-only) | Read-only users; cannot read Secrets |

The built-in admin login always acts as `kokumi-admin`. It needs no identity
annotations and cannot be reached by OIDC users unless an operator explicitly
annotates the ServiceAccount.

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

## The kokumi-server ServiceAccount

The server's own ServiceAccount holds **no write permissions on kokumi
resources**. It can only:

- `impersonate` ServiceAccounts in the install namespace (the trust boundary:
  treat access to this ServiceAccount as cluster-admin equivalent for kokumi
  resources),
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
