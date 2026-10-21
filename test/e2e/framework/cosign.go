/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package framework

import (
	"os"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/kokumi-dev/kokumi/test/utils"
)

// The cosign helpers run the stock cosign CLI, as a user verifying kokumi's
// artifacts would, offline: against a public key, without a transparency
// log, over plain HTTP to the bundled registry. refs are as the test process
// reaches them (see Local).

// Cosign runs the cosign CLI, $COSIGN or cosign on the PATH, with args.
func Cosign(args ...string) (string, error) {
	cosign := os.Getenv("COSIGN")
	if cosign == "" {
		cosign = "cosign"
	}
	return utils.Run(exec.Command(cosign, args...))
}

// CosignVerify verifies the signature of ref against the public key file key.
func CosignVerify(key, ref string) error {
	_, err := Cosign("verify", "--key", key, "--insecure-ignore-tlog=true", "--allow-http-registry", ref)
	return err
}

// CosignVerifyAttestation verifies an attestation of predicateType on ref
// against the public key file key.
func CosignVerifyAttestation(key, ref, predicateType string) error {
	_, err := Cosign("verify-attestation", "--key", key, "--insecure-ignore-tlog=true",
		"--allow-http-registry", "--type", predicateType, ref)
	return err
}

// CosignSign signs ref with the private key file key, which has an empty
// password, as a publisher signs a release.
func CosignSign(key, ref string) {
	GinkgoT().Setenv("COSIGN_PASSWORD", "")
	_, err := Cosign("sign", "--yes", "--key", key, "--use-signing-config=false", "--tlog-upload=false",
		"--allow-http-registry", ref)
	Expect(err).NotTo(HaveOccurred(), "Failed to sign %s", ref)
}

// GenerateCosignKey generates a key pair nobody trusts and returns the file
// of its public key.
func GenerateCosignKey() string {
	dir := GinkgoT().TempDir()
	GinkgoT().Setenv("COSIGN_PASSWORD", "")
	_, err := Cosign("generate-key-pair", "--output-key-prefix", filepath.Join(dir, "other"))
	Expect(err).NotTo(HaveOccurred())
	return filepath.Join(dir, "other.pub")
}

// KitchenPublicKey writes the public key the Kitchen publishes in
// status.signing.publicKey to a file and returns the file. The file lives as
// long as the setup node or spec that calls it.
func KitchenPublicKey() string {
	var publicKey string
	Eventually(func(g Gomega) {
		out, err := Kubectl("get", "kitchen", "default", "-n", InstallNamespace,
			"-o", "jsonpath={.status.signing.publicKey}")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(out).NotTo(BeEmpty(), "The Kitchen publishes no public key")
		publicKey = out
	}).Should(Succeed())
	file := filepath.Join(GinkgoT().TempDir(), "kokumi.pub")
	Expect(os.WriteFile(file, []byte(publicKey), 0o600)).To(Succeed())
	return file
}
