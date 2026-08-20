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
and writes this resource through the server API.

> **The Kitchen must be named `default`.** Kokumi only reconciles a Kitchen
> called `default`, and the server only reads and writes that one. If you
> create a Kitchen with any other name, Kokumi ignores it.

The one setting today is the Argo CD base URL. It is stored as
`spec.argoCDURL` on the Kitchen and used to build deep links to applications on
the Servings page. Leave it empty if you do not want Argo CD links.

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
