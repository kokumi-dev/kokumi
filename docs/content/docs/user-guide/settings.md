---
title: Settings
weight: 3
description: Where Kokumi stores system configuration settings.
---

Every kitchen is different. Some run on chaos and takeaway boxes, others on
mise en place and labeled jars, and every kitchen works a little differently.
Kokumi is no different: its configuration lives in one place, the Kitchen
resource.

Kokumi keeps its system configuration settings on a single **Kitchen**
resource. The Kitchen is the central place where Kokumi stores configuration
for the whole instance. There is exactly one Kitchen per Kokumi instance,
named `default` in the same namespace as your Kokumi installation, and the
controller creates it automatically there. The Settings page in the UI reads
and writes this resource through the server API. It is only shown to users
who may update the Kitchen (see
[UI capabilities](../../operator-guide/authorization/#ui-capabilities)).

> **The Kitchen must be named `default`.** Kokumi only reconciles a Kitchen
> called `default`, and the server only reads and writes that one. If you
> create a Kitchen with any other name, Kokumi ignores it.

The Settings page has three tabs, each shown only to users with its
capability:

- **General**: the Argo CD base URL, stored as `spec.argoCDURL` and used to
  build deep links to applications on the Servings page. Leave it empty if you
  do not want Argo CD links.
- **Authentication**: the admin account and OIDC, stored in `spec.auth`. See
  [Authentication](../../operator-guide/authentication/).
- **Signing**: the public key Kokumi signs with, the cosign commands that
  verify its artifacts, the further keys it trusts, and whether every Product
  source must verify, stored in `spec.signing`. See
  [Signing](../../operator-guide/signing/).

## Setting the Argo CD URL

From the UI, open Settings and enter the URL. To set it directly:

```bash
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Kitchen
metadata:
  name: default
  namespace: kokumi
spec:
  argoCDURL: "https://argocd.example.com"
```
