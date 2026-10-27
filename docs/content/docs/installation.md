---
title: Installation
weight: 2
description: Deploy Kokumi to any Kubernetes cluster.
---

## Requirements

| Dependency | Version |
|---|---|
| Kubernetes | ≥ 1.26 |
| Argo CD | ≥ 3.3 |

Argo CD must be installed **before** Kokumi is deployed. The Serving controller
creates and updates Argo CD `Application` resources to point at the immutable
OCI artifacts produced by Preparations. Without Argo CD, Servings will fail
and no workloads will be deployed.

See [Getting Started — Argo CD registry credentials](../getting-started/#argo-cd-registry-credentials)
for the required repo-creds secret to let Argo CD reach the in-cluster registry.

## Install

<!-- x-release-please-start-version -->
```bash
kubectl apply -f \
    https://github.com/kokumi-dev/kokumi/releases/download/0.17.0/install.yaml
```
<!-- x-release-please-end -->

This installs:
- The Kokumi CRDs (`Product`, `Recipe`, `Order`, `Preparation`, `Serving`,
  `Approval`, `Registry`, `Repository`, `Kitchen`)
- The controller manager in the `kokumi` namespace, which creates the
  `Kitchen` settings and the `Registry` of the bundled registry on start
- The API server and web UI in the `kokumi` namespace
- The `kokumi-scanner` component in the `kokumi` namespace, which idles until
  the Kitchen enables [vulnerability scanning](../operator-guide/scanning/)
- RBAC roles and bindings

`kubectl get kokumi -A` lists every Kokumi object: all its kinds are in the
category `kokumi`.

> **Model:** a `Product` defines an application completely, and each `Order`
> is a variant of one Product (for example one environment) that renders
> `Preparation` artifacts. A `Serving` activates one Preparation per Order.

## Verify

```bash
# CRDs registered
kubectl get crds | grep kokumi.dev

# Manager and server running
kubectl get pods -n kokumi

# Controller logs
kubectl logs -n kokumi deployment/kokumi-controller-manager -c manager -f
```

## Access the UI

```bash
kubectl port-forward -n kokumi svc/kokumi-server 8080:80
```

Open [http://localhost:8080](http://localhost:8080) in your browser.

### Logging in

The UI requires authentication. The default credentials are:

| Username | Password |
| -------- | -------- |
| `admin`  | `admin`  |

Change the password before any non-development use. See the
[Operator Guide — Authentication](../operator-guide/authentication/) for how to
rotate the credentials Secret, manage the token signing key
(`kokumi-server-tokens`), and configure the admin account via the `Kitchen`
resource.

## Signing key

On its first start the controller generates the key that signs every
artifact, in the Secret `kokumi-signing-key` in the `kokumi-signing`
namespace. To bring your own key instead,
create the Secret before installing:

```bash
kubectl create namespace kokumi-signing
cosign generate-key-pair k8s://kokumi-signing/kokumi-signing-key
```

See the [Operator Guide — Signing](../operator-guide/signing/) for how the key
is protected and rotated.

## Private certificate authorities

A registry whose certificate a private CA signed needs that CA. For one
Registry, put it in the Secret its `certSecretRef` names (see
[Registries and Repositories](../user-guide/registries/)). A CA that every
registry of the platform uses belongs into the trust store of the controller
and the server: mount it into both containers and list its directory in
`SSL_CERT_DIR` next to the system one, for example with a kustomize patch of
`kokumi-controller-manager` and `kokumi-server`:

```yaml
spec:
  template:
    spec:
      containers:
      - name: manager                  # server: the container of kokumi-server
        env:
        - name: SSL_CERT_DIR
          value: /etc/ssl/certs:/etc/kokumi/ca
        volumeMounts:
        - name: platform-ca
          mountPath: /etc/kokumi/ca
          readOnly: true
      volumes:
      - name: platform-ca
        configMap:
          name: platform-ca            # a ConfigMap with the CA's PEM file
```

## Pin a specific version

<!-- x-release-please-start-version -->
Replace `0.17.0` with any released version:
<!-- x-release-please-end -->

```bash
kubectl apply -f \
    https://github.com/kokumi-dev/kokumi/releases/download/<VERSION>/install.yaml
```

All releases are listed at [github.com/kokumi-dev/kokumi/releases](https://github.com/kokumi-dev/kokumi/releases).

## Upgrade

```bash
kubectl apply -f \
    https://github.com/kokumi-dev/kokumi/releases/download/<NEW_VERSION>/install.yaml
```

## Uninstall

<!-- x-release-please-start-version -->
```bash
kubectl delete -f \
    https://github.com/kokumi-dev/kokumi/releases/download/0.17.0/install.yaml
```
<!-- x-release-please-end -->
