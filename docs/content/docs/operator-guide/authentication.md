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
`password-hash`. That, and a new hash, end the admin sessions that are already
signed in; see [Sessions and signing out](#sessions-and-signing-out).

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
expires. Every exchange also sets a new refresh token, which carries the
session's idle deadline. The earlier refresh tokens are not revoked: each
works until its own expiry, and a sign-out ends them all. A session ends:

- **After an hour without activity.** Activity is user input, such as a click,
  a key press or switching to a Kokumi tab, in any tab of the browser. An open
  tab nobody uses keeps renewing its access token, so live views stay up to
  date, but it does not extend the session. The browser reports the activity,
  so the limit ends sessions that nobody attends: whoever holds a session's
  cookies can keep it going until its 24 hours are up.
- **24 hours after the sign-in**, however active the user is.
- **At a sign-out**, also for every copy of its tokens.
- **When what signed it in no longer does**:
  - an admin session when the admin account is turned off, or its username or
    its password hash changes;
  - an OIDC session when the Kitchen no longer configures OIDC, or configures
    another `issuerURL`.
- **When the OIDC provider ends it**: at a check of the session, or through
  [back-channel logout](#back-channel-logout) (see
  [OIDC single sign-on](#oidc-single-sign-on)).

Every request checks its session, so a session that ends this way is refused
at its next request, and its live updates stop at once. After a restart the
server answers `503` until it has read the Kitchen; the UI keeps its session
and tries again.

The refresh cookie expires when the session would end without further
activity. The cookies are marked `Secure` only when the request arrived over
TLS or carries `X-Forwarded-Proto: https`.

Browsers drop a cookie larger than 4096 bytes, and proxies refuse a request
whose headers exceed their limit, often 8 KiB. The refresh cookie carries the
user's groups, so Kokumi keeps it under 4,000 bytes, and the cookies of a
session under 7,000 bytes together:

- A sign-in whose cookies would be larger fails. The login page asks the user
  to have the groups limited, and the server logs the number of groups. Have
  the provider send only the groups Kokumi maps, for example with a group
  filter; with Microsoft Entra ID's object IDs of 36 characters, a session
  holds about 60 groups.
- A refresh whose groups have grown that large since the sign-in ends the
  session.
- The ID token, which only a sign-out at the provider uses, is left out first
  (see `providerSignOut` below).

To change the lifetimes, set these environment variables of the
`kokumi-server` Deployment to a Go duration, such as `15m` or `8h`:

| Variable | Default | Lifetime of |
| -------- | ------- | ----------- |
| `KOKUMI_TOKEN_TTL` | `5m` | An access token. It never outlives its session. |
| `KOKUMI_SESSION_TTL` | `24h` | A session, counted from the sign-in. A change applies to running sessions too. |
| `KOKUMI_SESSION_IDLE_TTL` | `1h` | A session without activity. A value of at least `KOKUMI_SESSION_TTL` turns the limit off. |

A change at an OIDC provider that does not check its sessions reaches Kokumi
only at the next sign-in. To end every session at once, replace the
[token signing key](#token-signing-key).

"Sign out" in the UI ends the session in this browser, in all of its tabs:

- The server expires the refresh cookie and records the session as ended (see
  [Ended sessions](#ended-sessions)), so an access token or a refresh cookie
  copied before the sign-out stops working at once.
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
    [OIDC single sign-on](#oidc-single-sign-on). Use `Always` for a provider
    that serves only Kokumi.

    For the sign-out, the server keeps the provider's ID token in the
    HttpOnly cookie `kokumi.oidc.id_token`, which only the sign-out receives,
    and sends it as `id_token_hint`. With `Never` it keeps none. An ID token
    that does not fit beside the session's other cookies is left out; the
    sign-out then names only the client, and some providers ask the user to
    confirm it.

    The next "Sign in with SSO" in this browser asks the provider to
    authenticate the user again (`prompt=login`), in case a session at the
    provider outlives the sign-out: at a provider without RP-initiated logout,
    such as Dex, when the user cancels the provider's confirmation, or when the
    browser also holds the session of another account that signed in there
    earlier. The browser keeps this in the cookie `kokumi.oidc.reauth` until
    the next sign-in. A provider that cannot authenticate the user again
    answers with an error, such as `login_required`; the sign-in ends with
    that error, and the next one is an ordinary sign-in.

Another site cannot sign a user in or out: the server refuses state-changing
requests that a browser sends from another origin. A reverse proxy in front of
the server must pass the original `Host` header.

### Ended sessions

Kokumi's tokens are signed, and the server checks them without asking anyone.
To end a session before its tokens expire, the server records it in the
ConfigMap `kokumi-server-ended-sessions` in the install namespace, and refuses
its tokens at their next request:

- A sign-out records its session.
- A [back-channel logout](#back-channel-logout) from the OIDC provider records
  the provider session it names, or, without one, the user: their sessions
  that signed in before the logout end.

Every server replica watches the ConfigMap, so an entry takes effect on all of
them within a moment, and live updates of an ended session stop. The entries
are hashes of the session and user IDs and the time the session ended, nothing
secret. The server drops an entry 24 hours (`KOKUMI_SESSION_TTL`) after its
time, when every session it can match has ended anyway. The Kitchen controller
creates the ConfigMap empty; the server may patch it and no other ConfigMap
(see [Authorization](../authorization/#the-kokumi-server-serviceaccount)).

If the server cannot write the record, a sign-out still ends the session in
the browser, and a copy of its tokens then works until the access token
expires and the refresh cookie's idle deadline passes.

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
configuration changes are picked up automatically; no restart is needed. A new
username or password hash ends the admin sessions that are already signed in;
see [Sessions and signing out](#sessions-and-signing-out).

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

New admin logins are refused at once, and sessions that are already signed in
as the admin end at their next request. The check keeps no state: turn the
account on again with the same password, and a session that has not reached
its idle or 24-hour limit goes on. To end the admin sessions for good, change
the password. The admin Secrets stay; delete them if you do not turn the
account on again.

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

- The ID token must have a `sub` claim, as OpenID Connect requires; a token
  without it fails the sign-in.
- The email is the `email` claim, and only when the token's `email_verified`
  is `true`: ServiceAccounts map by it, and some providers let users set an
  address they do not own. Providers that send no `email_verified`, such as
  Microsoft Entra ID, map by subject or group (see
  [Authorization](../authorization/)).
- Removing `spec.auth.oidc` or changing its `issuerURL` ends the sessions
  signed in through it. While the client Secret cannot be read, the sessions
  go on, but their checks at the provider wait until it can.

When the provider issues a refresh token at the sign-in, Kokumi asks the
provider again whether the user stays signed in, each time the provider's
access token expires, with the lifetime the provider sets:

- Kokumi keeps the provider's refresh token in the encrypted HttpOnly cookie
  `kokumi.oidc.refresh`, bound to its session, and redeems it at the provider.
  The server stores nothing.
- The provider's answer updates the session's email, groups and username, so a
  removed group takes effect within one provider token lifetime.
- The Kokumi session ends when the provider refuses the refresh token with
  `invalid_grant`, for example because the user was deactivated or deleted,
  or with `invalid_request`, which Dex sends for a refresh token it no longer
  knows.
- Any other answer pauses the session: an error about the client, such as
  `invalid_client` for a wrong client secret, a server error, or no answer at
  all. The current access token works until it expires; after that, requests
  fail until a check succeeds, and the session then goes on. Its idle and
  24-hour limits still apply.

Request the scope `offline_access` only where the provider issues no refresh
token without it, as Dex does. Without a refresh token the server logs once
that sessions are not checked, and a change at the provider takes effect at
the next sign-in. With `offline_access`:

- Each sign-in creates a grant at the provider that outlives the user's
  session there, often by weeks. A sign-out in Kokumi revokes it, if the
  provider offers token revocation; a session that ends after inactivity or
  after 24 hours leaves it at the provider until it expires.
- The check notices a deactivated user or a changed group, but not a sign-out
  elsewhere.

Keycloak issues refresh tokens without the scope, and they end with the
user's Keycloak session, so the check also notices a sign-out there; with
`offline_access`, Keycloak creates offline sessions instead. Google rejects
the scope and issues Kokumi no refresh token, so sessions are not checked
there.

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
| `usernameClaim` | `string` | `email` | ID-token claim shown as the user's name. Supports dotted nested paths (e.g. `user.name`). It only names the user: Kokumi identifies the user by `sub`, and maps by `sub`, the verified `email` and the groups. |
| `groupsClaim` | `string` | `groups` | ID-token claim holding the user's groups, a list of strings, matched against `kokumi.dev/identity-groups`. Supports dotted nested paths (e.g. `realm_access.roles` for Keycloak). |
| `scopes` | `[]string` | `["openid","profile","email"]` | OAuth2 scopes requested during the flow (max 16). |
| `providerSignOut` | `string` | `Never` | `Never` or `Always`: whether signing out of Kokumi also signs the user out of the provider. See [Sessions and signing out](#sessions-and-signing-out). |

The default scopes do not request groups. The built-in ServiceAccounts match
by group (see [Authorization](../authorization/#built-in-serviceaccounts)), so
add the scope your provider needs to put groups into the ID token, such as
`groups` for Dex, and, where the provider needs it, `offline_access` to have
sessions checked at the provider (see above).

Create the client-secret Secret:

```bash
kubectl -n kokumi create secret generic kokumi-server-oidc \
  --from-literal=client-secret='your-oauth-client-secret'
```

The server sends the client secret to the provider's token and revocation
endpoints in one way, chosen by the methods its discovery document lists
under `token_endpoint_auth_methods_supported`: `client_secret_post`, in the
request body, when the list names it but not `client_secret_basic`, else
`client_secret_basic`, in the `Authorization` header, which every provider
supports. It never tries the other one. Some
providers, such as Okta and Auth0, set the method per client: register the
Kokumi client for the method the server chooses, or every sign-in fails and
the server logs the provider's error.

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

### Back-channel logout

When a user signs out at the provider, or an administrator ends their session
there, the provider can tell Kokumi through
[OpenID Connect Back-Channel Logout](https://openid.net/specs/openid-connect-backchannel-1_0.html).
Kokumi then ends the sessions signed in with that provider session at their
next request (see [Ended sessions](#ended-sessions)). To turn it on, register
this URL with the provider as the client's back-channel logout URI:

```text
https://<kokumi host>/api/v1/auth/oidc/backchannel-logout
```

- The provider posts a signed logout token to it from its own servers, not
  from the browser, so it must reach Kokumi through the network path it uses,
  usually the same ingress as browsers.
- Kokumi checks the token as the specification asks: the provider's signature,
  issuer, audience, expiry, the logout event, and that it is not an ID token.
  It answers 400 for a token that fails a check, and 500 when it cannot record
  the logout, so that the provider may retry.
- With the option that the provider includes the session ID in its tokens,
  often called "backchannel logout session required", a logout ends only the
  sessions signed in with that provider session. Without it, a logout ends
  every Kokumi session of that user that signed in before it.

Providers that send logout tokens include Keycloak, Zitadel and Auth0. Dex,
Google and Microsoft Entra ID do not; with them a sign-out elsewhere reaches
Kokumi only when the session ends by itself.
