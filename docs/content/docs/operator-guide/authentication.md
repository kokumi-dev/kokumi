---
title: Authentication
weight: 1
description: Configure the built-in admin account, OIDC single sign-on, rotate credentials, and disable login.
---

Kokumi's web UI and API server require authentication. Two identity providers
are supported and can be enabled independently:

- **Admin account**: the built-in username/password login.
- **OIDC**: single sign-on through any standards-compliant OpenID Connect
  provider (Dex, Keycloak, Okta, Google, Entra ID, ...).

## Logging in

A fresh installation has no login method. The controller creates the `Kitchen`
`default` with an empty spec, so the login page shows "No login method is
configured" and the API denies every request for Kokumi resources with 403.
Enable the admin account on the Kitchen:

```bash
kubectl -n kokumi patch kitchen default --type=merge \
  -p '{"spec":{"auth":{"adminUser":{"enabled":true}}}}'
```

The controller then creates the [token signing key](#token-signing-key), and
the server picks up the change without a restart. The installation ships the
`kokumi-server-auth` Secret with these credentials:

| Username | Password |
| -------- | -------- |
| `admin`  | `admin`  |

Change the password before any non-development use. Generate a new bcrypt hash
with `htpasswd` and patch the `kokumi-server-auth` Secret:

```bash
kubectl -n kokumi patch secret kokumi-server-auth \
  --type merge \
  -p "{\"stringData\":{\"password-hash\":\"$(htpasswd -nbB admin 'your-new-password' | cut -d: -f2)\"}}"
```

A new password does not end sessions that are already signed in. Replace the
[token signing key](#token-signing-key) to end them.

To change the username from the default `admin`, set
`spec.auth.adminUser.username` on the Kitchen (see below).

The Secret holds these keys:

| Key | Purpose |
| --- | ------- |
| `username` | Fallback login name, used only when `spec.auth.adminUser.username` is empty. The Kitchen defaults that field to `admin`, so set the username there. |
| `password-hash` | bcrypt hash of the password. Required. |

## Token signing key

All session tokens (admin and OIDC alike) are signed with a single HMAC key
stored in the `kokumi-server-tokens` Secret under the `signing-key` key. The
key must be at least 32 bytes long.

- Once the Kitchen has `spec.auth`, the Kitchen controller creates the Secret
  with a generated random key if it does not exist. It is never rotated
  automatically.
- If the Secret already exists, the controller uses it as-is and never modifies
  it. A key shorter than 32 bytes sets the Kitchen's `Ready` condition to
  `False`:

  ```bash
  kubectl -n kokumi create secret generic kokumi-server-tokens \
    --from-literal=signing-key="$(openssl rand -base64 32)"
  ```

- To use a differently named Secret, set `spec.auth.tokenSigningKeySecretRef`:

  ```yaml
  spec:
    auth:
      tokenSigningKeySecretRef:
        name: my-gitops-managed-key
  ```

A sign-in issues an access token, valid for one hour, and a refresh token in
the HttpOnly cookie `kokumi.refresh`, valid for seven days. The UI exchanges
the refresh token for new access tokens, and every exchange issues a new
refresh token, so a session that is used at least once a week does not expire.
To change the lifetime of access tokens, set the environment variable
`KOKUMI_TOKEN_TTL` of the `kokumi-server` Deployment to a Go duration, such as
`15m`. The cookie is marked `Secure` only when the request arrived over TLS or
carries `X-Forwarded-Proto: https`.

Disabling the admin account, changing its password, or removing a user at the
OIDC provider does not end sessions that are already signed in. Replacing the
signing key does: the server rejects every token signed with the old key, and
every user signs in again. Delete the Secret, and the controller generates a
new key:

```bash
kubectl -n kokumi delete secret kokumi-server-tokens
```

For a Secret you manage yourself, for example through GitOps, replace its
`signing-key` instead.

## Configuring the admin user

The built-in admin account is configured through the singleton `Kitchen`
resource (named `default` in the install namespace). The following fields are
supported under `spec.auth.adminUser`:

| Field | Type | Default | Description |
| ----- | ---- | ------- | ----------- |
| `enabled` | `*bool` | `true` | Toggle the admin account. Set to `false` to disable UI login (e.g. once an external identity provider is wired up). |
| `username` | `string` | `admin` | Login username. Must not contain whitespace or `/`. |
| `secretRef` | `LocalObjectReference` | `kokumi-server-auth` | Name of the `Secret` holding the credentials (`password-hash`, and optionally `username`) in the same namespace. |

The defaults apply once the `adminUser` block is present. A Kitchen without
it has no admin account.

When `secretRef` is omitted, the server uses the default Secret name
`kokumi-server-auth`.

Example — custom username backed by a dedicated Secret:

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Kitchen
metadata:
  name: default
  namespace: kokumi
spec:
  auth:
    adminUser:
      username: root
      secretRef:
        name: kokumi-admin-creds
```

The server watches the Secret and the `Kitchen` resource, so credential and
configuration changes are picked up automatically; no restart is needed.
Sessions that are already signed in stay valid; see
[Token signing key](#token-signing-key).

The admin login always acts as the `kokumi-admin` ServiceAccount, which has
full access to Kokumi resources. See [Authorization](../authorization/).

## Disabling the admin account

Set `enabled: false` to turn off the built-in login. This is useful when an
external identity provider takes over authentication.

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Kitchen
metadata:
  name: default
  namespace: kokumi
spec:
  auth:
    adminUser:
      enabled: false
```

New admin logins are refused at once. Sessions that are already signed in as
the admin keep working; replace the [token signing key](#token-signing-key) to
end them.

## OIDC-only (no admin account)

You can run OIDC as the sole identity provider by omitting the `adminUser`
block entirely. The login page then shows only the SSO button:

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Kitchen
metadata:
  name: default
  namespace: kokumi
spec:
  auth:
    oidc:
      issuerURL: https://dex.example.com
      clientID: kokumi
      clientSecretRef:
        name: kokumi-server-oidc
```

A merge patch keeps fields it does not name. To remove the block from a
Kitchen that has it, set it to `null`:

```bash
kubectl -n kokumi patch kitchen default --type=merge \
  -p '{"spec":{"auth":{"adminUser":null}}}'
```

No admin credentials Secret is needed in this mode; only the OIDC client
secret and the token signing key (see above) are required.

## OIDC single sign-on

Kokumi can delegate authentication to an OIDC provider. When OIDC is enabled,
the login page shows a "Sign in with SSO" button that starts the standard
authorization-code flow (with PKCE). After the provider authenticates the
user, Kokumi mints its own session tokens, so that upstream tokens are never
stored on the server.

The session keeps the subject, email and groups of the ID token. Refreshing it
does not contact the provider again, so a change at the provider, such as a
removed group, takes effect at the next sign-in. A change to the
ServiceAccount mapping takes effect at the next request.

> **Authorization.** OIDC only authenticates. What a user may do is decided by
> the ServiceAccounts their identity maps to, by subject, email or groups. A
> user who maps to no ServiceAccount can sign in but is denied every
> operation. See [Authorization](../authorization/).

Register this redirect URI with the provider:

```text
https://<kokumi host>/api/v1/auth/oidc/callback
```

The server builds the redirect URI from the `Host` header of the request, and
uses `https` when the request arrived over TLS or carries
`X-Forwarded-Proto: https`. A reverse proxy in front of the server must pass
the original host and set that header.

### Configuration

OIDC is configured under `spec.auth.oidc` on the singleton `Kitchen` resource:

| Field | Type | Default | Description |
| ----- | ---- | ------- | ----------- |
| `issuerURL` | `string` | _(required)_ | OIDC issuer base URL (must be a valid `https://` or `http://` URL). Discovery is performed against `<issuerURL>/.well-known/openid-configuration`. |
| `clientID` | `string` | _(required)_ | OAuth2 client identifier registered with the provider. |
| `clientSecretRef` | `*LocalObjectReference` | `kokumi-server-oidc` | Name of the `Secret` (in the install namespace) holding the client secret under the `client-secret` key. |
| `usernameClaim` | `string` | `email` | ID-token claim shown as the user's name. Supports dotted nested paths (e.g. `user.name`). It only names the user: mapping uses the `sub` claim, and falls back to this claim only when the token has no `sub`. |
| `emailClaim` | `string` | `email` | ID-token claim matched against `kokumi.dev/identity-email`. Supports dotted nested paths. |
| `groupsClaim` | `string` | `groups` | ID-token claim holding the user's groups, a list of strings, matched against `kokumi.dev/identity-groups`. Supports dotted nested paths (e.g. `realm_access.roles` for Keycloak). |
| `scopes` | `[]string` | `["openid","profile","email"]` | OAuth2 scopes requested during the flow (max 16). |

The default scopes do not request groups. The built-in ServiceAccounts match
by group (see [Authorization](../authorization/#built-in-serviceaccounts)), so
add the scope your provider needs to put groups into the ID token, such as
`groups` for Dex.

Create the client-secret Secret:

```bash
kubectl -n kokumi create secret generic kokumi-server-oidc \
  --from-literal=client-secret='your-oauth-client-secret'
```

Enable OIDC on the `Kitchen`:

```yaml
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Kitchen
metadata:
  name: default
  namespace: kokumi
spec:
  auth:
    oidc:
      issuerURL: https://dex.example.com
      clientID: kokumi
      clientSecretRef:
        name: kokumi-server-oidc
      usernameClaim: email
      scopes: ["openid", "profile", "email", "groups"]
```
