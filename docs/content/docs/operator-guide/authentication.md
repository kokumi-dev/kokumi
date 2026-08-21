---
title: Authentication
weight: 1
description: Configure the built-in admin account, rotate credentials, and disable login.
---

Kokumi's web UI and API server require authentication. The built-in admin
account is the default identity provider; it can be disabled or renamed, and a
future release will add OIDC as a second provider.

## Logging in

The default credentials are:

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
supported under `spec.adminUser`:

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
  adminUser:
    enabled: false
```
