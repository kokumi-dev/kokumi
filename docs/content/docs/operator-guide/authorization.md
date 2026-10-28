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
   credentials) and extracts stable identity claims: `sub`, the email and the
   groups, from the claims that
   [the OIDC configuration](../authentication/#configuration) names.
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

Map by `sub` or by group rather than by email. The provider assigns the
subject and the groups, while some providers let users set their own email
address. Groups reach Kokumi only when the requested scopes put them into the
ID token; see [OIDC configuration](../authentication/#configuration).

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
| `kokumi-admin` | `kokumi-server-role` (full CRUD incl. `kitchens`, `environments` and `clusters`; `approve` on `preparations`) | `kokumi.dev/identity-groups: "kokumi-admin"` | Full access; also used by the built-in admin login |
| `kokumi-editor` | `kokumi-server-editor-role` (CRUD, no `kitchens`; reads `environments` and `clusters`; `approve` on `preparations`) | `kokumi.dev/identity-groups: "kokumi-editor"` | Editors |
| `kokumi-viewer` | `kokumi-server-viewer-role` (read-only) | `kokumi.dev/identity-groups: "kokumi-viewer"` | Read-only users |

Both the admin and the editor role hold the custom verb `approve` on
`preparations`, so every OIDC user mapped to `kokumi-editor` may approve. The
built-in admin login cannot approve, since a vote needs a personal OIDC login.
To keep editors from approving, bind them to a role of your own without the
verb.

None of the built-in roles grants access to Secrets. They are bound
cluster-wide, which would include the `kokumi-signing` namespace, and the key
that signs every Kokumi artifact is a Secret there: an identity that could
read or replace it could forge trusted signatures. See
[Registry credentials](#registry-credentials) and
[Cluster credentials](#cluster-credentials) for granting Secret access per
namespace, and [Signing](../signing/) for how the key is protected.

`update` on the Kitchen is as powerful as the signing key: it decides which
keys Kokumi trusts, whether sources must verify, and how users sign in. Of the
built-in roles only `kokumi-server-role` holds it. Grant it only to those you
would trust with the key.

The built-in admin login always acts as `kokumi-admin`, regardless of the
annotations. Additionally, OIDC users are matched against the default
`kokumi.dev/identity-groups` values: with the defaults, an OIDC identity whose
token carries the `kokumi-admin` group gets full access, `kokumi-editor`
editor access, and `kokumi-viewer` read-only access (multiple matches are
merged as described above). The token carries groups only when the requested
scopes ask for them; see [OIDC configuration](../authentication/#configuration).

To grant the `devops` OIDC group editor access:

```bash
kubectl -n kokumi annotate sa kokumi-editor \
  kokumi.dev/identity-groups=kokumi-editor,devops --overwrite
```

`--overwrite` replaces the whole annotation, so list the default group too to
keep its mapping.

## Registry credentials

A Registry lends the credentials of its Secret to every Product and Order in
its namespace that lists it, and the controller reads them with its own
identity. Who may name a Secret in a Registry therefore decides who may use
it. Three things guard that:

- **The admission policy `kokumi-registry-secret-access`** admits a Registry
  that names a Secret (`secretRef` or `certSecretRef`) only when its author
  may `get` that Secret. An update that keeps the `url`, the `scheme` and both
  references needs no Secret access; changing the `url` or switching to
  `HTTP` does, because either sends the credentials somewhere else.
- **The UI reads through a Registry with the user's identity.** Browsing a
  Repository, a Product's source or a Preparation's artifact, and previews,
  connect through the Registries of the object with the user's own `get` on
  their Secrets. Without it the server answers 403 and names the Registry and
  the Secret; it never falls back to an anonymous read. The server's pull
  cache serves only content the caller could resolve with their own
  credentials.
- **The UI stores credentials as the user.** A Registry saved with a username
  and password gets a `kubernetes.io/dockerconfigjson` Secret
  `<registry>-credentials`, created after the Registry and owned by it. The
  server never overwrites a Secret of that name it did not create, and
  refuses such a name before it creates anything.

The built-in roles grant no Secret access. Platform admins grant it per tenant
namespace by binding the `kokumi-registry-credentials` ClusterRole (`get`,
`create` and `patch` on `secrets`) with a RoleBinding:

```bash
kubectl -n team-a create rolebinding kokumi-registry-credentials \
  --clusterrole=kokumi-registry-credentials \
  --serviceaccount=kokumi:kokumi-editor
```

{{< callout type="warning" >}}
Never bind `kokumi-registry-credentials` with a ClusterRoleBinding, in the
`kokumi-signing` namespace, or in the install namespace. `kokumi-signing` holds
the artifact signing key. The install namespace holds the Secrets of the
Kitchen's `auth` settings: the session token signing key, the admin password
hash and the OIDC client secret. Reading or patching them lets a user forge
sessions or take over login. Treat the install namespace as admin-only, as
Argo CD does with its own namespace, and keep Registries with credentials in
tenant namespaces.
{{< /callout >}}

Without the binding, users can still create Registries without credentials
and list the Registries others created in Products and Orders; the
controller connects through them. The Registry form shows whether the user
may use credentials in the selected namespace, from the namespaced capability
`registries.credentials:update`.

Everyone who may edit Products or Orders in a namespace can use the
credentials of its Registries, as with `imagePullSecrets`. Keep credentials
of different trust levels in different namespaces.

## Cluster credentials

A [Cluster](../clusters/) lends the credential in its kubeconfig Secret to
every Order of the Environments that select it: the controller reads it with
its own identity and hands it to Argo CD. Two things guard that:

- **The admission policy `kokumi-cluster-secret-access`** admits a Cluster
  that names a kubeconfig Secret only when its author may `get` that Secret.
  An update that keeps the Secret's name and key needs no Secret access;
  changing either does, because another key could hold another credential.
- **The UI stores the kubeconfig as the user**, in a Secret
  `<cluster>-kubeconfig`, created after the Cluster and owned by it. A Secret
  of that name the UI did not create is refused before anything is created.

The built-in admin role creates Clusters in every namespace but grants no
Secret access. Bind the `kokumi-cluster-credentials` ClusterRole (`get`,
`create` and `patch` on `secrets`) with a RoleBinding in each workspace that
holds Clusters with a kubeconfig:

```bash
kubectl -n platform create rolebinding kokumi-cluster-credentials \
  --clusterrole=kokumi-cluster-credentials \
  --serviceaccount=kokumi:kokumi-admin
```

Never bind it with a ClusterRoleBinding, in the `kokumi-signing` namespace, or
in the install namespace, for the reasons above. The Cluster `in-cluster` that
Kokumi keeps in the install namespace needs no Secret. The Cluster form shows
whether the user may store a kubeconfig in the selected namespace, from the
namespaced capability `clusters.credentials:update`.

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
| `settings.signing:update` | `update` the `kitchens/default` object in the install namespace | Settings, Signing tab |
| `settings.scanning:update` | `update` the `kitchens/default` object in the install namespace | Settings, Scanning tab |
| `orders:create` / `orders:update` / `orders:delete` | `create` / `update` / `delete` `orders` | New, Edit, Delete Order; edits on rendered files |
| `products:create` / `products:update` / `products:delete` | `create` / `update` / `delete` `products` | New, Edit, Delete Product |
| `registries:create` / `registries:update` / `registries:delete` | `create` / `patch` / `delete` `registries` | New, Edit, Delete Registry |
| `repositories:create` / `repositories:update` / `repositories:delete` | `create` / `patch` / `delete` `repositories` | New, Edit, Delete Repository |
| `environments:create` / `environments:update` / `environments:delete` | `create` / `update` / `delete` `environments` | New, Edit, Delete Environment |
| `clusters:create` / `clusters:update` / `clusters:delete` | `create` / `patch` / `delete` `clusters` | New, Edit, Delete Cluster |
| `preparations:promote` | `create` and `update` `servings` | Promote and Rollback |
| `preparations:approve` | `approve` `preparations`, and the user signed in with OIDC | Approve and Request changes |

`orders:delete` stops delivery without `delete` on `servings`: the garbage
collector deletes the Order's Serving, and with it the Argo CD Application.
The deployed workloads keep running until you remove them.

Two capabilities hold per namespace and are reported by
`GET /api/v1/namespaces/{namespace}/capabilities`:

| Capability | Granted when a mapped ServiceAccount may | UI |
| ---------- | ---------------------------------------- | -- |
| `registries.credentials:update` | `get`, `create` and `patch` `secrets` in the namespace | Credentials and Secrets in the Registry form |
| `clusters.credentials:update` | `get`, `create` and `patch` `secrets` in the namespace | The kubeconfig in the Cluster form |

Except for the settings and the namespaced capabilities, capabilities are
evaluated **across all namespaces**:
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

The server's own ServiceAccount holds **one write permission on kokumi
resources**: it creates Approvals. It can only:

- `impersonate` the identity ServiceAccounts in the install namespace, by
  default `kokumi-admin`, `kokumi-editor` and `kokumi-viewer` (see
  [Adding your own ServiceAccount](#adding-your-own-serviceaccount)). This is
  the trust boundary: treat access to this ServiceAccount as cluster-admin
  equivalent for kokumi resources,
- read ServiceAccounts (mapping resolution),
- read all Secrets in the install namespace: the login and OIDC Secrets, and
  the Secrets of Registries kept there. It cannot read the signing key, which
  lives in the `kokumi-signing` namespace,
- read (get/list/watch) kokumi resources to power the live UI event stream:
  the hub must be able to see all events in order to filter them per user
  (each connected user only receives events for resources their mapped
  ServiceAccounts may list),
- `create` `approvals` in every namespace, through the ClusterRole
  `kokumi-server-approval-submitter`. The server records a vote with its own
  identity, after it checked that the voter's mapped ServiceAccount may
  `approve` the Preparation. The admission policy `kokumi-approval-integrity`
  admits new Approvals only from this ServiceAccount, and rejects every update
  and every delete except the namespace controller's.

Apart from Approvals, the server never writes kokumi resources with its own
identity.

The admission policies `kokumi-approval-integrity`, `kokumi-status-integrity`,
`kokumi-signing-key-integrity` and `kokumi-argocd-secret-integrity` name the
ServiceAccounts of the install namespace `kokumi`, and the signing key policy
also the Kitchen there. When you install into another namespace, change these
names to match. Otherwise the policies reject the writes of the controller
and the server, and stop restricting the controller in `argocd`.

## The kokumi-scanner ServiceAccount

The scanner has no Kubernetes identity: its ServiceAccount mounts no token
and is bound to no role. The controller manager sends it the registry
credentials of each scan with the request, and signs what it returns. See
[Vulnerability Scanning](../scanning/#network-isolation).

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
