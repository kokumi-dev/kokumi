---
title: Contribution Guide
weight: 6
description: How to contribute to Kokumi.
---

Contributions are highly welcome!

## Before You Start

Open an issue before writing any code or documentation. This helps align on
the approach early, avoids duplicated effort, and gives a chance to share
context or flag related work before a pull request is opened.

## DCO Sign-off

All commits must include a [Developer Certificate of Origin (DCO)](https://developercertificate.org)
sign-off, certifying that you wrote the change or have the right to submit it.
Add it with the `-s` flag:

```bash
git commit -s -m "your commit message"
```

## Tests

```bash
make test           # unit and envtest tests, and the checks of the examples
make test-e2e       # e2e tests on a Kind cluster of their own; E2E_LABELS=<feature> runs one feature
make test-e2e-plan  # list every e2e spec, without a cluster
```

A user-facing feature comes with an example in
[`examples/`](https://github.com/kokumi-dev/kokumi/tree/main/examples), which
the docs show and an e2e spec applies, as users would. `make test` fails for an
example that the API refuses, or that no docs page shows or no e2e spec
applies. The e2e
[README](https://github.com/kokumi-dev/kokumi/tree/main/test/e2e) explains how
the specs are laid out and written.

## Run your build

Kokumi publishes images for releases only. To run `main` or your own change,
build the image into the local Kind cluster and deploy it:

```bash
make kind-setup                                   # the Kind cluster "kokumi"
make argocd                                       # Argo CD; its default project admits every namespace
make docker-build kind-load-image IMG=kokumi:dev
make deploy IMG=kokumi:dev
```

Then register the bundled registry with Argo CD and turn on login, as
[Installation](../installation/#argo-cd-registry-credentials) describes.
`make deploy` writes the image into `config/manager`, `config/server` and
`config/scanner`; do not commit that change.

## Docs

The docs are a [Hugo](https://gohugo.io/) site in `docs/`. `make website-dev`
serves it at [http://localhost:1313](http://localhost:1313), and every push
to `main` publishes it at [kokumi.dev](https://kokumi.dev/docs/).

- Each fact lives on one page; other pages link to it.
- A scenario a user would apply is an example in `examples/`, shown with
  `{{</* example */>}}` and `{{</* apply-example */>}}`; a snippet that
  explains a single field may stay inline.
- The release version comes from `kokumiVersion` in `docs/hugo.toml`; use
  `{{</* param kokumiVersion */>}}` instead of writing it out.

## Community

Feel free to ask questions, share ideas, or discuss topics in the
[#kokumi](https://cloud-native.slack.com/archives/C0B457Z851P) channel on CNCF Slack.
