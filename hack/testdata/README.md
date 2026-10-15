# Testdata signing key

`cosign.key` and `cosign.pub` sign the testdata that `make push-artifacts`
pushes, so that the verified sample Product
(`config/samples/delivery_v1alpha1_product_verified.yaml`) and the e2e tests
can exercise source verification. The private key is public on purpose and has
an empty password. Never trust it for anything but local testdata.
