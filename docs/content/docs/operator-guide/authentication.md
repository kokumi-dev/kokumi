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

A sign-in starts a session. It issues an access token, valid for five
minutes, and a refresh token in the HttpOnly cookie `kokumi.refresh`. The UI
exchanges the refresh token for a new access token before the old one
expires, and every exchange issues a new refresh token. A session ends:

- **After an hour without activity.** Activity is user input, such as a click,
  a key press or switching to a Kokumi tab, in any tab of the browser. An open
  tab nobody uses keeps renewing its access token, so live views stay up to
  date, but it does not extend the session.
- **24 hours after the sign-in**, however active the user is.
- **When the OIDC provider ends it**, for an OIDC session that the provider
  checks (see [OIDC single sign-on](#oidc-single-sign-on)).

The refresh cookie expires when the session would end without further
activity. The cookies are marked `Secure` only when the request arrived over
TLS or carries `X-Forwarded-Proto: https`.

To change the lifetimes, set these environment variables of the
`kokumi-server` Deployment to a Go duration, such as `15m` or `8h`:

| Variable | Default | Lifetime of |
| -------- | ------- | ----------- |
| `KOKUMI_TOKEN_TTL` | `5m` | An access token. It never outlives its session. |
| `KOKUMI_SESSION_TTL` | `24h` | A session, counted from the sign-in. A change applies to running sessions too. |
| `KOKUMI_SESSION_IDLE_TTL` | `1h` | A session without activity. A value of at least `KOKUMI_SESSION_TTL` turns the limit off. |

Turning the admin account off or changing its password does not end sessions
that are already signed in, and neither does a change at an OIDC provider that
does not check its sessions. They end with their session at the latest. To end
them at once, replace the [token signing key](#token-signing-key).

"Sign out" in the UI ends the session in this browser, in all of its tabs:

- The server expires the refresh cookie. It does not revoke Kokumi's tokens:
  an access token copied before the sign-out works until it expires, which is
  why access tokens are short-lived.
- For an OIDC session that the provider checks, the server also revokes the
  provider's refresh token, if the provider offers token revocation
  ([RFC 7009](https://datatracker.ietf.org/doc/html/rfc7009)). A copy of the
  session's cookies then fails at its next check.
- For an OIDC session, `spec.auth.oidc.providerSignOut` decides whether the
  user is also signed out of the provider:
  - `Never`, the default, ends only the Kokumi session. The user stays signed
    in to the provider and to the other apps that use it, and the next "Sign
    in with SSO" uses that session without asking for credentials. Keep it for
    a provider that other apps share, such as a company's.
  - `Always` also ends the session at the provider when the provider supports
    [RP-initiated logout](https://openid.net/specs/openid-connect-rpinitiated-1_0.html),
    that is, when its discovery document has an `end_session_endpoint`. The
    browser goes there, and the provider sends it back to the UI. Register the
    post-logout redirect URI described in
    [OIDC single sign-on](#oidc-single-sign-on). The next "Sign in with SSO"
    in this browser asks the provider to authenticate the user again
    (`max_age=0`), in case the provider session outlives the sign-out: at a
    provider without RP-initiated logout, such as Dex, or when the user
    cancels the provider's confirmation. A provider that ignores `max_age`
    signs the user in without asking. Use `Always` for a provider that serves
    only Kokumi.

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

The session keeps the subject, email and groups of the ID token. A change to
the ServiceAccount mapping takes effect at the next request.

When the provider issues a refresh token at the sign-in, Kokumi asks the
provider again whether the user stays signed in, each time the provider's
access token expires, with the lifetime the provider sets:

- Kokumi keeps the provider's refresh token in the encrypted HttpOnly cookie
  `kokumi.oidc.refresh`, bound to its session, and redeems it at the provider.
  The server stores nothing.
- The provider's answer updates the session's email, groups and username, so a
  removed group takes effect within one provider token lifetime.
- When the provider refuses the refresh token (`invalid_grant`), for example
  because the user was deactivated or deleted, the Kokumi session ends.
- While the provider cannot be reached, the session goes on until its access
  token expires; then requests fail until the provider answers again.

Most providers issue a refresh token only for the scope `offline_access`. Add
it to `scopes` to have sessions checked; without a refresh token the server
logs once that sessions are not checked, and a change at the provider takes
effect at the next sign-in. A refresh token from `offline_access` usually
outlives the user's session at the provider, so the check notices a
deactivated user or a changed group, but a sign-out elsewhere only at a
provider whose refresh tokens end with its session.

> **Authorization.** OIDC only authenticates. What a user may do is decided by
> the ServiceAccounts their identity maps to, by subject, email or groups. A
> user who maps to no ServiceAccount can sign in but is denied every
> operation. See [Authorization](../authorization/).

Register this redirect URI with the provider:

```text
https://<kokumi host>/api/v1/auth/oidc/callback
```

With `providerSignOut: Always` and a provider that supports RP-initiated
logout, also register the UI's root as a post-logout redirect URI, which some
providers call a sign-out redirect URI. The provider sends the browser there
after signing the user out:

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
| `providerSignOut` | `string` | `Never` | `Never` or `Always`: whether signing out of Kokumi also signs the user out of the provider. See [Sessions and signing out](#sessions-and-signing-out). |

The default scopes do not request groups. The built-in ServiceAccounts match
by group (see [Authorization](../authorization/#built-in-serviceaccounts)), so
add the scope your provider needs to put groups into the ID token, such as
`groups` for Dex, and `offline_access` to have sessions checked at the
provider.

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
      scopes: ["openid", "profile", "email", "groups", "offline_access"]
```
