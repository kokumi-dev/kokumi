---
title: Settings
weight: 3
description: The Kitchen, where Kokumi keeps the settings of an installation.
---

Every kitchen is different. Some run on chaos and takeaway boxes, others on
mise en place and labeled jars. Kokumi keeps the settings of an installation
in one place: the **Kitchen** named `default`, in the namespace Kokumi is
installed in. The controller creates it on start, with an empty spec.

> **The Kitchen must be named `default`.** Kokumi only reconciles a Kitchen
> called `default`, and the server only reads and writes that one. A Kitchen
> of any other name is ignored.

The Settings page in the UI reads and writes the Kitchen through the server
API. It is shown only to users who may update the Kitchen (see
[UI capabilities](../authorization/#ui-capabilities)), and has four tabs, each
shown only to users with its capability:

| Tab | Field | What it sets |
|---|---|---|
| **General** | `spec.argoCDURL`, `spec.images.mirrors` | The Argo CD base URL, which the Servings and Clusters pages build deep links to Argo CD from; empty means no links. The image mirrors; see [Container Images](../images/). |
| **Authentication** | `spec.auth` | The admin account and OIDC; see [Authentication](../authentication/). |
| **Signing** | `spec.signing` | The public key Kokumi signs with, the cosign commands that verify its artifacts, the further keys it trusts, and whether every Product source must verify; see [Signing](../signing/). |
| **Scanning** | `spec.scanning` | Whether vendored images are scanned, how often, how old the vulnerability database may be, where the databases are read from, and the Registries that connect to them; see [Vulnerability Scanning](../scanning/). |

Whoever may update the Kitchen decides who can log in and which keys Kokumi
trusts, so grant it as you would grant the signing key; see
[Authorization](../authorization/).

## Setting the Argo CD URL

Open **Settings**, and enter the URL under **General**. Or set it directly:

```bash
kubectl -n kokumi patch kitchen default --type=merge \
    -p '{"spec":{"argoCDURL":"https://argocd.example.com"}}'
```
