---
title: Authentication
weight: 1
description: Configure the built-in admin account, OIDC single sign-on, rotate credentials, and disable login.
---

Kokumi's web UI and API server require authentication. Two identity providers
are supported and can be enabled independently:

- **Admin account**: the built-in username/password login (default).
- **OIDC**: single sign-on through any standards-compliant OpenID Connect
  provider (Dex, Keycloak, Okta, Google, Entra ID, ...).

## Logging in

The default admin credentials are:

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

You can also change the username from the default `admin` by setting
`stringData.username` in the same Secret.

The Secret must carry three keys:

| Key | Purpose |
| --- | ------- |
| `username` | Login name for the admin account. |
| `password-hash` | bcrypt hash of the password. |
| `signing-key` | HMAC key used to sign issued JWTs. |

## Configuring the admin user

The built-in admin account is configured through the singleton `Kitchen`
resource (named `default` in the install namespace). The following fields are
supported under `spec.auth.adminUser`:

| Field | Type | Default | Description |
| ----- | ---- | ------- | ----------- |
| `enabled` | `*bool` | `true` | Toggle the admin account. Set to `false` to disable UI login (e.g. once an external identity provider is wired up). |
| `username` | `string` | `admin` | Login username. Must not contain whitespace or `/`. |
| `secretRef` | `LocalObjectReference` | `kokumi-server-auth` | Name of the `Secret` holding the credentials (`username`, `password-hash`, `signing-key`) in the same namespace. |

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

## OIDC single sign-on

Kokumi can delegate authentication to an OIDC provider. When OIDC is enabled,
the login page shows a "Sign in with SSO" button that starts the standard
authorization-code flow (with PKCE). After the provider authenticates the
user, Kokumi mints its own short-lived session token, sp that upstream tokens
are never stored on the server.

> **Authorization scope.** OIDC in Kokumi is an authentication mechanism only.
> Any successfully authenticated user is granted full admin access. Fine-grained
> authorization (per-user roles/permissions) is currently not supported but will
> be added later.

### Configuration

OIDC is configured under `spec.auth.oidc` on the singleton `Kitchen` resource:

| Field | Type | Default | Description |
| ----- | ---- | ------- | ----------- |
| `issuerURL` | `string` | _(required)_ | OIDC issuer base URL (must be a valid `https://` or `http://` URL). Discovery is performed against `<issuerURL>/.well-known/openid-configuration`. |
| `clientID` | `string` | _(required)_ | OAuth2 client identifier registered with the provider. |
| `clientSecretRef` | `*LocalObjectReference` | `kokumi-server-oidc` | Name of the `Secret` (in the install namespace) holding the client secret under the `client-secret` key. |
| `usernameClaim` | `string` | `email` | ID-token claim used as the Kokumi username. Supports dotted nested paths (e.g. `realm_access.preferred_username`). Must resolve to a string. |
| `scopes` | `[]string` | `["openid","profile","email"]` | OAuth2 scopes requested during the flow (max 16). |

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
```
