---
title: Installation
weight: 2
description: Requirements, install, login, upgrade and uninstall.
---

## Requirements

| Dependency | Version |
|---|---|
| Kubernetes | ≥ 1.30, for `admissionregistration.k8s.io/v1` ValidatingAdmissionPolicies |
| A default StorageClass | for the bundled registry's PersistentVolumeClaim |
| Argo CD | ≥ 3.3, in the namespace `argocd` of the same cluster |

### Argo CD

Kokumi deploys through Argo CD: every Serving creates and updates Argo CD
`Application` resources that point at the immutable OCI artifacts of its
Preparations. Install Argo CD, in the namespace `argocd`, before Kokumi:

```bash
kubectl create namespace argocd
kubectl apply -n argocd --server-side --force-conflicts \
    -f https://raw.githubusercontent.com/argoproj/argo-cd/{{< param argoCDVersion >}}/manifests/install.yaml
```

The last document of `install.yaml` is a RoleBinding in `argocd` that lets
Kokumi register [Clusters](../operator-guide/clusters/) with Argo CD: it may
create, update and delete Secrets there, and the admission policy
`kokumi-argocd-secret-integrity` keeps it to the cluster Secrets it manages.
Installed without Argo CD, everything else applies and that document fails;
apply `install.yaml` again once Argo CD is installed.

Until Argo CD's CRDs exist, Kokumi renders, signs and scans as usual, and
Servings deploy nothing and report `ArgoCDNotInstalled`. They deploy once
Argo CD is installed, without a restart.

### Argo CD registry credentials

The bundled registry serves plain HTTP. Register it with Argo CD with
`insecureOCIForceHttp=true`, so that Argo CD can pull the Preparations:

```bash
kubectl -n argocd create secret generic manifests-registry-creds \
    --from-literal=type=oci \
    --from-literal=url=oci://kokumi-registry.kokumi.svc.cluster.local:5000 \
    --from-literal=insecureOCIForceHttp=true \
    --dry-run=client -o yaml | \
kubectl label -f - argocd.argoproj.io/secret-type=repo-creds --local -o yaml | \
kubectl apply -f -
```

An Order that pushes to another registry needs Argo CD credentials for that
registry instead; see [Registries and Repositories](../user-guide/registries/).

## Install

```bash
kubectl apply -f \
    https://github.com/kokumi-dev/kokumi/releases/download/{{< param kokumiVersion >}}/install.yaml
```

This installs:

- The Kokumi CRDs. `kubectl get kokumi -A` lists every Kokumi object: all its
  kinds are in the category `kokumi`.
- In the namespace `kokumi`:
  - the controller manager, which creates the Kitchen `default`, the
    Registry of the bundled registry, the Cluster `in-cluster` and the
    Environment `local` on start;
  - the API server and web UI, Service `kokumi-server`, also exposed as
    NodePort 30051;
  - the bundled registry, Service `kokumi-registry`, also exposed as NodePort
    30050, with the 1 GiB PersistentVolumeClaim `kokumi-registry-pvc`;
  - the `kokumi-scanner` component and its NetworkPolicy, idle until the
    Kitchen enables [vulnerability scanning](../operator-guide/scanning/).
- The namespace `kokumi-signing`, for the signing key.
- RBAC roles and bindings, and in `argocd` the RoleBinding that lets the
  controller write Argo CD's cluster Secrets.
- Admission policies, named `kokumi-*`, that guard Approvals, statuses, the
  signing key, the Secrets that Registries and Clusters name, and the
  controller's writes in `argocd`. They assume the install namespace
  `kokumi`; see [Authorization](../operator-guide/authorization/).

## Verify

```bash
kubectl get pods -n kokumi
kubectl logs -n kokumi deployment/kokumi-controller-manager -c manager -f
```

## Turn on login

A fresh installation has no login method: the UI says _No login method is
configured_, and the API refuses every request. Enable the built-in admin
account on the Kitchen:

```bash
kubectl -n kokumi patch kitchen default --type=merge \
    -p '{"spec":{"auth":{"adminUser":{"enabled":true}}}}'
```

The user is `admin` and the password `admin`, from the Secret
`kokumi-server-auth` that `install.yaml` ships. Change the password, or sign
in with OIDC instead, before any non-development use; see
[Authentication](../operator-guide/authentication/). The server picks up
changes to the Kitchen and its Secrets without a restart.

## Access the UI

```bash
kubectl port-forward -n kokumi svc/kokumi-server 8080:80
```

Open [http://localhost:8080](http://localhost:8080) in your browser.

## Signing key

On its first start the controller generates the key that signs every
artifact, in the Secret `kokumi-signing-key` in the namespace
`kokumi-signing`. To bring your own key instead, create the Secret before
installing:

```bash
kubectl create namespace kokumi-signing
cosign generate-key-pair k8s://kokumi-signing/kokumi-signing-key
```

See [Signing](../operator-guide/signing/) for how the key is protected and
rotated.

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

## Upgrade

Apply the `install.yaml` of the new version:

```bash
kubectl apply -f \
    https://github.com/kokumi-dev/kokumi/releases/download/<VERSION>/install.yaml
```

Kokumi is pre-1.0, and a release may change its CRDs without a migration;
the [release notes](https://github.com/kokumi-dev/kokumi/releases) say what
to re-apply. To run an unreleased version, build it from source; see
[Run your build](../contribution-guide/#run-your-build).

## Uninstall

Delete Kokumi's CustomResourceDefinitions first, while Kokumi runs: that
deletes every Kokumi object, and Kokumi releases each of them. Servings
remove their Argo CD Applications, and what runs on the clusters keeps
running. Clusters remove their registrations from Argo CD. Then delete the
rest:

```bash
kubectl get crd -o name | grep '\.delivery\.kokumi\.dev$' | xargs kubectl delete
kubectl delete --ignore-not-found -f \
    https://github.com/kokumi-dev/kokumi/releases/download/{{< param kokumiVersion >}}/install.yaml
```

{{< callout type="warning" >}}
Deleting `install.yaml` deletes the PersistentVolumeClaim
`kokumi-registry-pvc`, with every Preparation and vendored copy in the
bundled registry, and the namespace `kokumi-signing`, with the signing key.
Copy the artifacts you want to keep to another registry first, and keep the
public key (`kubectl -n kokumi get kitchen default -o
jsonpath='{.status.signing.publicKey}'`) to verify them later.
{{< /callout >}}

Deleting `install.yaml` alone stops Kokumi together with its objects, and
the objects whose finalizers Kokumi could not release keep the `kokumi`
namespace in `Terminating`. In `argocd`, remove the Secret
`manifests-registry-creds` and the namespaces you added to the
`sourceNamespaces` of an AppProject yourself.
