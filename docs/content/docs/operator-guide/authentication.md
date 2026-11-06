---
title: Authentication
weight: 1
description: Turn on the built-in admin account, configure OIDC single sign-on, rotate credentials, and turn login off.
---

Kokumi's web UI and API server require authentication. Two identity providers
are supported and can be enabled independently:

- **Admin account**: the built-in username/password login.
- **OIDC**: single sign-on through any standards-compliant OpenID Connect
  provider (Dex, Keycloak, Okta, Google, Entra ID, ...).

## Logging in

A fresh installation has no login method. The controller creates the `Kitchen`
`default` with an empty spec, so the login page shows "No login method is
configured" and the API refuses every request. Turn on the admin account on the
Kitchen:

```bash
kubectl -n kokumi patch kitchen default --type=merge \
  -p '{"spec":{"auth":{"adminUser":{"enabled":true}}}}'
```

The controller then creates the [token signing key](#token-signing-key) and,
as the Secret `kokumi-server-auth` does not exist, generates a password for the
user `admin`:

- `kokumi-server-auth` holds its bcrypt hash under the key `password-hash`.
- `kokumi-initial-admin` holds the password under the key `password`.

Read the password, then delete `kokumi-initial-admin`. The controller does not
create it again:

```bash
kubectl -n kokumi get secret kokumi-initial-admin \
  -o jsonpath='{.data.password}' | base64 -d; echo
kubectl -n kokumi delete secret kokumi-initial-admin
```

The server picks up the change without a restart. To change the username from
the default `admin`, set `spec.auth.adminUser.username` on the Kitchen (see
below).

### Your own password

The controller keeps a `kokumi-server-auth` that exists and generates nothing.
To bring your own password, create the Secret with a bcrypt hash before you
turn on the admin account:

```bash
kubectl -n kokumi create secret generic kokumi-server-auth \
  --from-literal=password-hash="$(htpasswd -nbB admin 'your-password' | cut -d: -f2)"
```

To change the password later, replace the hash:

```bash
kubectl -n kokumi patch secret kokumi-server-auth \
  --type merge \
  -p "{\"stringData\":{\"password-hash\":\"$(htpasswd -nbB admin 'your-new-password' | cut -d: -f2)\"}}"
```

To get a new generated password instead, delete both Secrets. The controller
generates a new one at once:

```bash
kubectl -n kokumi delete secret kokumi-server-auth kokumi-initial-admin --ignore-not-found
```

Admin login stops as soon as `kokumi-server-auth` is gone or holds no
`password-hash`. A new password does not end sessions that are already signed
in; see [Sessions and signing out](#sessions-and-signing-out).

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

Replacing the signing key ends every session at once: the server rejects
every token signed with the old key, and every user signs in again. Delete the
Secret, and the controller generates a new key:

```bash
kubectl -n kokumi delete secret kokumi-server-tokens
```

For a Secret you manage yourself, for example through GitOps, replace its
`signing-key` instead.

## Sessions and signing out

A sign-in starts a session that lasts 24 hours. It issues an access token,
valid for five minutes, and a refresh token in the HttpOnly cookie
`kokumi.refresh`, which expires with the session. The UI exchanges the refresh
token for a new access token before the old one expires. Every exchange issues
a new refresh token, but none extends the session: 24 hours after signing in,
the user signs in again. The cookies are marked `Secure` only when the request
arrived over TLS or carries `X-Forwarded-Proto: https`.

To change the lifetimes, set these environment variables of the
`kokumi-server` Deployment to a Go duration, such as `15m` or `8h`:

| Variable | Default | Lifetime of |
| -------- | ------- | ----------- |
| `KOKUMI_TOKEN_TTL` | `5m` | An access token. It never outlives its session. |
| `KOKUMI_SESSION_TTL` | `24h` | A session, counted from the sign-in. |

Turning the admin account off, changing its password, or removing a user or a
group at the OIDC provider does not end sessions that are already signed in.
They end with their session at the latest. To end them at once, replace the
[token signing key](#token-signing-key).

"Sign out" in the UI ends the session in this browser, in all of its tabs:

- The server expires the refresh cookie. It does not revoke tokens: an access
  token copied before the sign-out works until it expires, which is why access
  tokens are short-lived.
- An OIDC session also ends at the provider when the provider supports
  [RP-initiated logout](https://openid.net/specs/openid-connect-rpinitiated-1_0.html),
  that is, when its discovery document has an `end_session_endpoint`. The
  browser goes there, and the provider sends it back to the UI. Register the
  post-logout redirect URI described in
  [OIDC single sign-on](#oidc-single-sign-on).
- The next "Sign in with SSO" in this browser asks the provider to
  authenticate the user again (`prompt=login`). A provider session that
  outlives the sign-out, for example at a provider without RP-initiated
  logout such as Dex, then does not sign the user back in without asking.

Another site cannot sign a user in or out: the server refuses state-changing
requests that a browser sends from another origin. A reverse proxy in front of
the server must pass the original `Host` header.

## Configuring the admin user

The built-in admin account is configured through the singleton `Kitchen`
resource (named `default` in the install namespace). It is off unless
`spec.auth.adminUser.enabled` is `true`. These fields are supported under
`spec.auth.adminUser`:

| Field | Type | Default | Description |
| ----- | ---- | ------- | ----------- |
| `enabled` | `bool` | `false` | Turns the admin account on. |
| `username` | `string` | `admin` | Login username. Must not contain whitespace or `/`. |
| `secretRef` | `LocalObjectReference` | `kokumi-server-auth` | Name of the `Secret`, in the same namespace, whose key `password-hash` holds the bcrypt hash of the password. Created with a generated password when it does not exist. |

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
      enabled: true
      username: root
      secretRef:
        name: kokumi-admin-creds
```

The server watches the Secret and the `Kitchen` resource, so credential and
configuration changes are picked up automatically; no restart is needed.
Sessions that are already signed in stay valid; see
[Sessions and signing out](#sessions-and-signing-out).

The admin login always acts as the `kokumi-admin` ServiceAccount, which has
full access to Kokumi resources. See [Authorization](../authorization/).

## Turning the admin account off

Set `enabled` to `false`, for example once an external identity provider takes
over authentication. The username and Secret reference stay, so turning the
account on again, say for an emergency, needs nothing else:

```bash
kubectl -n kokumi patch kitchen default --type=merge \
  -p '{"spec":{"auth":{"adminUser":{"enabled":false}}}}'
```

New admin logins are refused at once. Sessions that are already signed in as
the admin keep working until their session ends; replace the
[token signing key](#token-signing-key) to end them at once. The admin Secrets
stay; delete them if you do not turn the account on again.

## OIDC-only (no admin account)

You can run OIDC as the sole identity provider by leaving the admin account
off: leave out the `adminUser` block, or set `enabled: false`. The login page
then shows only the SSO button:

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
removed group, takes effect at the next sign-in, at the latest when the
session ends (see [Sessions and signing out](#sessions-and-signing-out)). A
change to the ServiceAccount mapping takes effect at the next request.

> **Authorization.** OIDC only authenticates. What a user may do is decided by
> the ServiceAccounts their identity maps to, by subject, email or groups. A
> user who maps to no ServiceAccount can sign in but is denied every
> operation. See [Authorization](../authorization/).

Register this redirect URI with the provider:

```text
https://<kokumi host>/api/v1/auth/oidc/callback
```

If the provider supports RP-initiated logout, also register the UI's root as a
post-logout redirect URI, which some providers call a sign-out redirect URI.
The provider sends the browser there after signing the user out:

```text
https://<kokumi host>/
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
