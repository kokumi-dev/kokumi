package source

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/registry"
	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	protodsse "github.com/sigstore/protobuf-specs/gen/pb-go/dsse"
	protorekor "github.com/sigstore/protobuf-specs/gen/pb-go/rekor/v1"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore-go/pkg/tlog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/signing"
	"github.com/kokumi-dev/kokumi/internal/signing/signingtest"
)

// newRegistry starts an in-memory registry with the OCI referrers API and
// returns its host.
func newRegistry(t *testing.T) string {
	t.Helper()

	server := httptest.NewServer(registry.New(registry.WithReferrersSupport(true), registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(server.Close)
	return strings.TrimPrefix(server.URL, "http://")
}

// pushArtifact pushes a small artifact to the registry at host and returns
// its reference by digest.
func pushArtifact(t *testing.T, host, repository string) oci.Reference {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte("kind: ConfigMap\n"), 0600))

	ref, err := oci.Parse("oci://" + host + "/" + repository + ":1.0.0")
	require.NoError(t, err)

	digest, err := oci.NewORASClient().Push(context.Background(), ref, dir, oci.PushOptions{})
	require.NoError(t, err)

	ref.Tag = ""
	ref.Digest = digest
	return ref
}

// attach pushes a referrer of subject and returns it by digest.
func attach(t *testing.T, subject oci.Reference, artifactType string, payload []byte) oci.Reference {
	t.Helper()

	digest, err := oci.NewORASClient().PushReferrer(context.Background(), subject, oci.ReferrerArtifact{
		ArtifactType: artifactType,
		MediaType:    artifactType,
		Payload:      payload,
	})
	require.NoError(t, err)

	ref := subject
	ref.Digest = digest
	return ref
}

// sign attaches a signature by signer to subject and returns it by digest.
func sign(t *testing.T, signer *signing.Signer, subject oci.Reference) string {
	t.Helper()

	digest, err := attestation.Sign(context.Background(), oci.NewORASClient(), signer, subject, time.Now())
	require.NoError(t, err)
	return digest
}

// verdicts returns the verdicts of report by signature digest.
func verdicts(report *Report) map[string]Verdict {
	byDigest := make(map[string]Verdict, len(report.Verdicts))
	for _, v := range report.Verdicts {
		byDigest[v.Digest] = v
	}
	return byDigest
}

func TestVerify(t *testing.T) {
	host := newRegistry(t)
	ctx := context.Background()
	client := oci.NewORASClient()
	signer := signingtest.NewSigner(t)
	verifier := NewVerifier()
	policy := &Policy{Keys: []Key{
		{Name: "other.pub", PEM: signingtest.NewSigner(t).PublicKeyPEM()},
		{Name: "cosign.pub", PEM: signer.PublicKeyPEM()},
	}}

	t.Run("verifies every signature in the graph of the source", func(t *testing.T) {
		source := pushArtifact(t, host, "charts/signed")
		signature := sign(t, signer, source)
		sbom := attach(t, source, "application/spdx+json", []byte(`{"spdxVersion":"SPDX-2.3"}`))
		sbomSignature := sign(t, signer, sbom)
		notary := attach(t, source, notarySignatureType, []byte("notary"))

		report, err := verifier.Verify(ctx, client, nil, source, policy, nil)
		require.NoError(t, err)
		require.Equal(t, &Signer{KeyID: signer.KeyID()}, report.Signer)
		require.Len(t, report.Verdicts, 3, "the SBOM is not a signature")

		got := verdicts(report)
		assert.Equal(t, Verified, got[signature].Result)
		assert.Equal(t, source.Digest, got[signature].Subject)
		assert.Equal(t, Verified, got[sbomSignature].Result, "a signature on an attachment verifies like one on the source")
		assert.Equal(t, sbom.Digest, got[sbomSignature].Subject)
		assert.Equal(t, Unsupported, got[notary.Digest].Result)
	})

	t.Run("reports a signature by a key the policy does not name", func(t *testing.T) {
		source := pushArtifact(t, host, "charts/stranger")
		signature := sign(t, signingtest.NewSigner(t), source)

		report, err := verifier.Verify(ctx, client, nil, source, policy, nil)
		require.NoError(t, err)
		assert.Nil(t, report.Signer)
		assert.Equal(t, UntrustedSigner, verdicts(report)[signature].Result)
	})

	t.Run("verifies against the keys the Kitchen trusts without a policy", func(t *testing.T) {
		source := pushArtifact(t, host, "charts/kokumi")
		signature := sign(t, signer, source)

		report, err := verifier.Verify(ctx, client, nil, source, nil, signer.TrustSet())
		require.NoError(t, err)
		assert.Equal(t, &Signer{KeyID: signer.KeyID()}, report.Signer)
		assert.Equal(t, Verified, verdicts(report)[signature].Result)

		report, err = verifier.Verify(ctx, client, nil, source, nil, signingtest.NewSigner(t).TrustSet())
		require.NoError(t, err)
		assert.Nil(t, report.Signer)
		assert.Equal(t, UntrustedSigner, verdicts(report)[signature].Result)
	})

	t.Run("an unsigned source has no signatures", func(t *testing.T) {
		report, err := verifier.Verify(ctx, client, nil, pushArtifact(t, host, "charts/unsigned"), policy, nil)
		require.NoError(t, err)
		assert.Nil(t, report.Signer)
		assert.Empty(t, report.Verdicts)
	})

	t.Run("checks legacy cosign signatures only against a policy", func(t *testing.T) {
		source := pushArtifact(t, host, "charts/legacy")
		tagged, err := oci.Parse("oci://" + host + "/charts/legacy:sha256-" + strings.TrimPrefix(source.Digest, "sha256:") + ".sig")
		require.NoError(t, err)
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "signature"), []byte("not a signature"), 0600))
		legacy, err := client.Push(ctx, tagged, dir, oci.PushOptions{})
		require.NoError(t, err)

		report, err := verifier.Verify(ctx, client, nil, source, policy, nil)
		require.NoError(t, err)
		assert.Nil(t, report.Signer)
		assert.Equal(t, UntrustedSigner, verdicts(report)[legacy].Result)
		assert.NotEmpty(t, verdicts(report)[legacy].Message)

		report, err = verifier.Verify(ctx, client, nil, source, nil, signer.TrustSet())
		require.NoError(t, err)
		assert.Equal(t, Unsupported, verdicts(report)[legacy].Result)
	})
}

func TestVerify_Keyless(t *testing.T) {
	host := newRegistry(t)
	ctx := context.Background()
	client := oci.NewORASClient()
	verifier := NewVerifier()

	sigstore, err := ca.NewVirtualSigstore()
	require.NoError(t, err)
	trustedRoot := trustedRootJSON(t, sigstore)
	// The virtual Sigstore issues certificates without SCTs.
	verifier.ignoreSCT = true

	const identity = "https://github.com/example/app/.github/workflows/release.yml@refs/tags/v1"
	policy := &Policy{
		Identities:  []Identity{{Issuer: `^https://token\.actions\.githubusercontent\.com$`, Subject: `^https://github\.com/example/app/.*$`}},
		TrustedRoot: trustedRoot,
	}

	t.Run("verifies a keyless signature by an accepted identity", func(t *testing.T) {
		source := pushArtifact(t, host, "keyless/accepted")
		signature := attach(t, source, oci.SigstoreBundleMediaType, keylessBundle(t, sigstore, identity, source.Digest))

		report, err := verifier.Verify(ctx, client, nil, source, policy, nil)
		require.NoError(t, err)
		require.Equal(t, &Signer{Issuer: keylessIssuer, Subject: identity}, report.Signer)
		assert.Equal(t, Verified, verdicts(report)[signature.Digest].Result)
	})

	t.Run("reports a keyless signature by another identity", func(t *testing.T) {
		source := pushArtifact(t, host, "keyless/stranger")
		signature := attach(t, source, oci.SigstoreBundleMediaType,
			keylessBundle(t, sigstore, "https://github.com/someone/else/.github/workflows/release.yml@refs/tags/v1", source.Digest))

		report, err := verifier.Verify(ctx, client, nil, source, policy, nil)
		require.NoError(t, err)
		assert.Nil(t, report.Signer)
		got := verdicts(report)[signature.Digest]
		assert.Equal(t, UntrustedSigner, got.Result)
		assert.Contains(t, got.Message, "github.com/someone/else")
	})

	t.Run("does not verify a signature of another digest", func(t *testing.T) {
		source := pushArtifact(t, host, "keyless/other")
		other := "sha256:" + strings.Repeat("ab", 32)
		signature := attach(t, source, oci.SigstoreBundleMediaType, keylessBundle(t, sigstore, identity, other))

		report, err := verifier.Verify(ctx, client, nil, source, policy, nil)
		require.NoError(t, err)
		assert.Nil(t, report.Signer)
		assert.Equal(t, Invalid, verdicts(report)[signature.Digest].Result)
	})

	t.Run("does not accept a keyless signature without an identity in the policy", func(t *testing.T) {
		source := pushArtifact(t, host, "keyless/keys-only")
		signature := attach(t, source, oci.SigstoreBundleMediaType, keylessBundle(t, sigstore, identity, source.Digest))

		report, err := verifier.Verify(ctx, client, nil, source, &Policy{Keys: []Key{{Name: "cosign.pub", PEM: signingtest.NewSigner(t).PublicKeyPEM()}}}, nil)
		require.NoError(t, err)
		assert.Equal(t, UntrustedSigner, verdicts(report)[signature.Digest].Result)
	})
}

// trustedRootJSON returns the trusted_root.json of sigstore, as an
// air-gapped installation keeps it in a Secret.
func trustedRootJSON(t *testing.T, sigstore *ca.VirtualSigstore) []byte {
	t.Helper()

	// The virtual instance keys its logs by the hex of their ID; a
	// trusted_root.json carries the ID itself.
	withRawIDs := func(logs map[string]*root.TransparencyLog) map[string]*root.TransparencyLog {
		fixed := make(map[string]*root.TransparencyLog, len(logs))
		for id, transparencyLog := range logs {
			raw, err := hex.DecodeString(id)
			require.NoError(t, err)
			copied := *transparencyLog
			copied.ID = raw
			fixed[id] = &copied
		}
		return fixed
	}

	trusted, err := root.NewTrustedRoot(root.TrustedRootMediaType01, sigstore.FulcioCertificateAuthorities(),
		withRawIDs(sigstore.CTLogs()), sigstore.TimestampingAuthorities(), withRawIDs(sigstore.RekorLogs()))
	require.NoError(t, err)
	data, err := trusted.MarshalJSON()
	require.NoError(t, err)
	return data
}

// keylessIssuer is the OIDC issuer of the keyless signers in tests.
const keylessIssuer = "https://token.actions.githubusercontent.com"

// keylessBundle returns a Sigstore bundle, as cosign v3 writes it, in which
// the keyless signer identity of keylessIssuer signs the cosign signature
// statement about digest.
func keylessBundle(t *testing.T, sigstore *ca.VirtualSigstore, identity, digest string) []byte {
	t.Helper()

	statement := fmt.Sprintf(`{"_type":"https://in-toto.io/Statement/v1","subject":[{"name":"source","digest":{"sha256":%q}}],"predicateType":%q,"predicate":{}}`,
		strings.TrimPrefix(digest, "sha256:"), attestation.SignaturePredicateType)
	entity, err := sigstore.AttestAtTime(identity, keylessIssuer, []byte(statement), time.Now(), true)
	require.NoError(t, err)

	content, err := entity.VerificationContent()
	require.NoError(t, err)
	signatureContent, err := entity.SignatureContent()
	require.NoError(t, err)
	envelope := signatureContent.EnvelopeContent().RawEnvelope()
	payload, err := base64.StdEncoding.DecodeString(envelope.Payload)
	require.NoError(t, err)
	sig, err := base64.StdEncoding.DecodeString(envelope.Signatures[0].Sig)
	require.NoError(t, err)

	entries, err := entity.TlogEntries()
	require.NoError(t, err)
	tlogEntries := make([]*protorekor.TransparencyLogEntry, 0, len(entries))
	logID, err := sigstore.RekorLogID()
	require.NoError(t, err)
	for _, entry := range entries {
		tle := entry.TransparencyLogEntry()
		// The virtual log leaves out the kind of the entry and its signed
		// entry timestamp, which the bundles of the public-good instance
		// carry and the integrated time is trusted by.
		tle.KindVersion = &protorekor.KindVersion{Kind: "dsse", Version: "0.0.1"}
		set, err := sigstore.RekorSignPayload(tlog.RekorPayload{
			Body:           base64.StdEncoding.EncodeToString(tle.GetCanonicalizedBody()),
			IntegratedTime: tle.GetIntegratedTime(),
			LogIndex:       tle.GetLogIndex(),
			LogID:          logID,
		})
		require.NoError(t, err)
		tle.InclusionPromise = &protorekor.InclusionPromise{SignedEntryTimestamp: set}
		tlogEntries = append(tlogEntries, tle)
	}

	bundle := &protobundle.Bundle{
		MediaType: oci.SigstoreBundleMediaType,
		VerificationMaterial: &protobundle.VerificationMaterial{
			Content:     &protobundle.VerificationMaterial_Certificate{Certificate: &protocommon.X509Certificate{RawBytes: content.Certificate().Raw}},
			TlogEntries: tlogEntries,
		},
		Content: &protobundle.Bundle_DsseEnvelope{DsseEnvelope: &protodsse.Envelope{
			Payload:     payload,
			PayloadType: envelope.PayloadType,
			Signatures:  []*protodsse.Signature{{Sig: sig, Keyid: envelope.Signatures[0].KeyID}},
		}},
	}
	data, err := protojson.Marshal(bundle)
	require.NoError(t, err)
	return data
}
