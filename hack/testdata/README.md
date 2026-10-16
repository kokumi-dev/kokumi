# Testdata signing key

`cosign.key` and `cosign.pub` sign the testdata that `make push-artifacts`
pushes, so that the verified sample Product
(`config/samples/delivery_v1alpha1_product_verified.yaml`) and the e2e tests
can exercise source verification. The private key is public on purpose and has
an empty password. Never trust it for anything but local testdata.

`sbom.spdx.json` is the SBOM that `make push-artifacts` attaches to the signed
package and signs with the same key, so the package carries a signature, an
SBOM and a signature of the SBOM as OCI referrers. It describes the
external-secrets chart the package is rendered from.
