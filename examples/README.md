# Examples

Each directory is a scenario of one feature that you can apply to a namespace
of your choice on a cluster with Kokumi installed. Its Orders deploy to the
Environment `local` of the namespace, the cluster Kokumi runs in: Kokumi keeps
one in its own namespace, `kokumi`, and [local](local/) adds one to any other,
whose Argo CD project must admit the namespace.

```bash
kubectl apply -n <namespace> -k examples/<example>
# or without a checkout, from the release you installed:
kubectl apply -n <namespace> -k "https://github.com/kokumi-dev/kokumi//examples/<example>?ref=<release>"
```

The [docs](https://kokumi.dev/docs/) show these files as they are, and the
[e2e tests](../test/e2e/) apply them the same way and check that each does
what its docs say.

| Example | Shows | Docs | E2E spec |
|---|---|---|---|
| [local](local/) | The Cluster and the Environment that let a namespace deploy to the cluster Kokumi runs in | Getting Started, Environments | every spec, through `framework.CreateNamespace` |
| [getting-started](getting-started/) | A Helm chart, an Order that sets values, Manual promotion | Getting Started, Products and Orders | `rendering_test.go`, `overrides_test.go`, `promotion_test.go`, `signing_test.go`, `lifecycle_test.go` |
| [manifest-bundle](manifest-bundle/) | Pre-rendered manifests, patched by the Order | Products and Orders | `rendering_test.go` |
| [multi-file-manifests](multi-file-manifests/) | Manifests in several files, kept apart | Products and Orders | `rendering_test.go` |
| [kustomize](kustomize/) | A Kustomize source | Products and Orders | `rendering_test.go` |
| [promotion-pipeline](promotion-pipeline/) | dev, staging and prod stages in Environments of their own, with a soak and pinned images | Promotion Pipelines, Environments | `pipeline_test.go` |
| [multi-cluster](multi-cluster/) | One Order deployed to every cluster of an Environment, into the Product's target namespace; a spoke cluster registered as a Cluster | Deploy to Several Clusters, Clusters | `clusters_test.go` |
| [approvals](approvals/) | Two required approvals from a group | Approvals | `approvals_test.go` |
| [image-vendoring](image-vendoring/) | Images copied into the bundled registry | Images | `images_test.go` |
| [source-verification](source-verification/) | A keyless signature on the source | Source Verification | `source_verification_test.go` |
| [vulnerability-scanning](vulnerability-scanning/) | Scanning vendored images, holding promotions on findings | Security Scanning | `scanning_test.go` |
| [private-registry](private-registry/) | A source in a private registry, pulled with a Registry's credentials | Registries and Repositories | `registries_test.go` |

## Writing an example

An example

- applies with one `kubectl apply -k` to any namespace. It sets no
  `metadata.namespace`, and no `spec.destination` or `spec.vendor.url`: their
  defaults lie below the namespace. Only private content names a destination,
  since its render must stay behind credentials.
- reads public, pinned sources, so that it runs on a fresh installation. An
  example of what only a user has, such as their private registry, names a
  placeholder like `ghcr.io/my-org`, and the e2e tests apply it through an
  overlay in `test/e2e/testdata/<example>/` that fills in only that.
- explains itself in comments, since the docs show its files as they are.
- uses Product and Order names that no other example uses, so that several
  examples fit into one namespace.
- names the Environment of its Orders, `local` unless it shows Environments.
- states in its `kustomization.yaml` what it needs beyond the namespace, such
  as a Kitchen patch a cluster admin applies (`kitchen-patch.yaml`), or the
  Clusters and Environments of its `platform/` directory, a kustomization of
  its own that an admin applies to the namespace with
  `kubectl apply -n <namespace> -k examples/<example>/platform`, shown in the
  docs with `{{< apply-example name="<example>/platform" >}}`.

Show it in the docs with `{{< example file="<example>/<file>" >}}` and
`{{< apply-example name="<example>" >}}`, and apply it in an e2e spec with
`framework.ApplyExample(ns, "<example>")`, or `framework.ApplyExampleOverlay`
for one with placeholders. `make test` fails for an example the
API refuses, or that no docs page shows or no e2e spec applies.

A docs snippet that explains a single field may stay inline; a scenario a user
would apply belongs here.
