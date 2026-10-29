# E2E tests

The e2e tests install Kokumi from this checkout on a Kind cluster of their own
and check every feature the way a user meets it: they apply the
[examples](../../examples/) as the docs tell users to, and check what Kokumi
does with them in the cluster, in the registry and with stock tools such as
cosign.

## Running them

```bash
make test-e2e                          # every spec, on the Kind cluster kokumi-test-e2e
make test-e2e E2E_LABELS=promotion     # the specs of one feature
make test-e2e E2E_LABELS='!slow'       # all but the slow ones
make test-e2e E2E_LABELS=clusters      # multi-cluster, on a second Kind cluster
make test-e2e E2E_LABELS=environments  # Environments and Clusters, without a second cluster
make test-e2e E2E_ARGOCD=real          # with Argo CD deploying for real, see below
make test-e2e-plan                     # list the specs and their labels, without a cluster
```

`make test-e2e` creates the cluster, and deletes it when every spec passed.
The `clusters` specs create a second Kind cluster, a spoke, `kokumi-test-e2e-spoke`,
the first time they need it; `make cleanup-test-e2e` deletes it too.
After a failure it keeps the cluster, with Kokumi installed as the failed run
left it, and reuses it on the next run;
`make cleanup-test-e2e` deletes it. The cluster's kubeconfig is
`bin/kokumi-test-e2e.kubeconfig`, so the tests never touch the cluster of your
current kubeconfig context, such as the one of `make kind-setup`:

```bash
export KUBECONFIG=bin/kokumi-test-e2e.kubeconfig   # to look at a kept cluster
```

A failed spec writes the cluster's state to `_artifacts/e2e/<spec>/` (or
`$ARTIFACTS`): the Kokumi objects and events of its namespaces, the logs of
every Kokumi component, the Kitchen and the Argo CD Applications. A failed run
also exports the logs of the Kind nodes, their kubelet and every container to
`_artifacts/kind/` with `kind export logs`. The JUnit report is
`_artifacts/junit-e2e.xml`.

## What is real

| Part | In the suite |
|---|---|
| Controller, API server, bundled registry, scanner | Real, deployed by `make deploy` with an image built from this checkout. |
| Signing, attestations, source verification | Real; stock cosign verifies what Kokumi signs. |
| Admission policies and RBAC | Real. |
| Vulnerability scanning | Real Trivy, with the database from `mirror.gcr.io`. |
| Nodes pulling vendored images | Real, through `hack/kind/registry-mirror.sh`. |
| OIDC sign-in | Real Dex from `hack/oidc`, in the approvals and API server specs. |
| Private registry | A registry fixture that requires a password, in the registries spec. |
| Sources | Public: `ghcr.io/stefanprodan/charts/podinfo`, `ghcr.io/kokumi-dev/testdata`. Sources only a spec needs, such as an unsigned one, are pushed to the bundled registry. |
| Clusters | Real: a second Kind cluster, registered with Kokumi by `hack/kind/spoke-cluster.sh`, which `make kind-spoke` runs, as the docs tell admins to, and which pods of the first reach over Docker's `kind` network. |
| Argo CD | Simulated by default: only its Application and AppProject CRDs and its `default` project are installed, and `framework.SyncHealthy` reports an Application synced and healthy, as Argo CD would. This keeps the promotion gates deterministic and fast. With `E2E_ARGOCD=real`, `framework.InstallArgoCD` installs Argo CD with `make argocd` and `framework.SyncHealthy` waits for it to deploy for real. Its Redis pulls the same image from Docker Hub instead of ECR Public, which rate-limits the shared IPs of CI runners. |

## Layout

| File | Feature (label) |
|---|---|
| `e2e_suite_test.go` | Installs Kokumi once for all specs; dumps the state of a failed spec. |
| `e2e_test.go` | `install`: the controller, its metrics, the bundled registry. Scaffolded by Kubebuilder; keep its markers. |
| `rendering_test.go` | `rendering`: every kind of source, pushed as a Preparation. |
| `overrides_test.go` | `overrides`: what a Product lets its Orders change. |
| `promotion_test.go` | `promotion`: Manual promotion and rollback. |
| `pipeline_test.go` | `promotion`: stages, soak, signed records, Environments a stage must run after. |
| `target_namespace_test.go` | `target-namespace`: Orders deploying into the Product's target namespace or their own, on the cluster Kokumi runs in. |
| `clusters_test.go` | `clusters`: registering a second Kind cluster, deploying to the clusters of an Environment, clusters joining and leaving. |
| `environments_test.go` | `environments`: what an Environment's Argo CD project allows, and who may name a Secret in a Cluster; no second cluster. |
| `approvals_test.go` | `approvals`: required approvals through OIDC sign-in. |
| `lifecycle_test.go` | `lifecycle`: deleting an Order. |
| `signing_test.go` | `signing`: signatures and provenance of Preparations. |
| `source_verification_test.go` | `source-verification`: keyless, by key, and required by the Kitchen. |
| `images_test.go` | `images`: vendoring. |
| `scanning_test.go` | `scanning`, `slow`: scanning and the vulnerability check. |
| `registries_test.go` | `registries`: credentials for a private registry, and who may name a Secret. |
| `server_test.go` | `server`: the API server acts as the signed-in user, also when it registers clusters. |
| `access_test.go` | `access`: who may read and write the signing key. |
| `framework/` | What the specs share: namespaces, examples and fixtures, the typed client and matchers, the registry, cosign, Argo CD, the spoke cluster, the API server, diagnostics. |
| `testdata/` | Test-only fixtures, `${NAME}` replaced by the spec. |

## Writing a spec

A spec reads as a scenario; the mechanics live in `framework/`.

- **One file and one `Describe` per feature**, named as the docs name it, with
  the feature's `Label`.
- **One `Context` per scenario**, whose text says the setup: which example,
  which fixture. It is `Ordered`, its `BeforeAll` sets it up, one `By` per
  step, and every `It` checks one thing a user can observe. Read together, the
  texts form a sentence: "Promotion pipelines / the promotion-pipeline
  example: ... / deploys prod once the soak ends".
- **Set up with examples.** `framework.ApplyExample(ns, "<example>")` applies
  an example as the docs do; `framework.ApplyExampleOverlay` applies one with
  placeholders through `testdata/<example>/`, which fills in only what a user
  would. What only a spec needs goes into `testdata/`, never into YAML built
  in Go.
- **A namespace per scenario.** `framework.CreateNamespace` creates it, with
  the Cluster and the Environment of `examples/local`, and deletes it when
  the scenario ends; the suite waits for every one of them to go before it
  undeploys Kokumi.
- **Read the API with types.** `framework.Object[T]`, `framework.LatestPreparation`
  and matchers such as `framework.HaveConditionReason`, not kubectl output.
  Run a CLI only when the CLI is what the spec checks: `kubectl auth can-i`,
  `kubectl --as` against admission, stock `cosign`.
- **Say what should have happened** in the message of an `Eventually` whose
  failure is not obvious, and wait with the timeouts of `framework`.
- **Mark what a spec changes for the whole cluster.** A spec that changes the
  Kitchen is `Serial` and restores it, as `framework.PatchKitchen` does.
- **Name a simulation as such.** A spec never writes what Kokumi or Argo CD
  would write without a `framework` helper that says it simulates.

Every example needs a spec that applies it, and a docs page that shows it;
`make test` checks both.
