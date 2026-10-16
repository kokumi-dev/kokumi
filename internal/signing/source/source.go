// Package source verifies the signatures of source artifacts with the cosign
// library, as Flux's OCIRepository does: Sigstore bundles found through the
// OCI referrers API or the referrers tag schema, and the legacy
// "sha256-<hex>.sig" signatures many upstream artifacts still carry. Key-based
// signatures verify offline; keyless signatures verify against a Sigstore
// trusted root.
//
// It verifies every signature in the graph of a source, not only the ones on
// the source itself: a publisher's signature on an SBOM attached to the
// source verifies like their signature on the source.
package source

import (
	"context"
	"crypto"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/sigstore/cosign/v3/pkg/cosign"
	cosignoci "github.com/sigstore/cosign/v3/pkg/oci"
	ociremote "github.com/sigstore/cosign/v3/pkg/oci/remote"
	sgbundle "github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"

	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/signing"
)

// trustedRootRetryInterval rate-limits fetching the Sigstore public-good
// trusted root after a failure.
const trustedRootRetryInterval = time.Minute

// notarySignatureType is the artifactType of a Notary signature.
const notarySignatureType = "application/vnd.cncf.notary.signature"

// Key is a public key a source may be signed with.
type Key struct {
	// Name identifies the key, for example its Secret key.
	Name string
	// PEM is the PEM-encoded public key.
	PEM []byte
}

// Identity matches a keyless signer. Both fields are RE2 regular
// expressions.
type Identity struct {
	Issuer  string
	Subject string
}

// Policy is what a source must be signed by: one of the Keys, or a keyless
// signer matching one of the Identities.
type Policy struct {
	Keys       []Key
	Identities []Identity
	// TrustedRoot is the Sigstore trusted_root.json keyless signatures are
	// verified against. Empty uses the Sigstore public-good instance through
	// TUF.
	TrustedRoot []byte
}

// Signer identifies whose signature verified.
type Signer struct {
	// KeyID is the ID of the public key, for key-based signatures.
	KeyID string
	// Issuer and Subject identify a keyless signer.
	Issuer  string
	Subject string
}

// Result is the result of verifying one signature.
type Result string

const (
	// Verified is a signature by a key or identity the policy accepts.
	Verified Result = "Verified"
	// UntrustedSigner is a signature by a key or identity the policy does
	// not accept. The signature itself may be valid.
	UntrustedSigner Result = "UntrustedSigner"
	// Invalid is a signature that does not verify.
	Invalid Result = "Invalid"
	// Unsupported is a signature in a format that is not verified.
	Unsupported Result = "Unsupported"
)

// Verdict is the result of verifying one signature in the graph of a source.
type Verdict struct {
	// Digest is the digest of the signature manifest.
	Digest string
	// Subject is the digest of the manifest the signature signs.
	Subject string
	Result  Result
	// Signer is who signed, for a verified signature.
	Signer *Signer
	// Message says why the signature did not verify.
	Message string
	// signature reports that a verified bundle holds a signature, as
	// opposed to another statement such as a provenance.
	signature bool
}

// Report is the result of verifying the signatures in the graph of a
// source.
type Report struct {
	// Verdicts holds one verdict per signature in the graph, in the order of
	// the walk.
	Verdicts []Verdict
	// Signer is who signed the source itself; nil when no signature on it
	// verified.
	Signer *Signer
}

// Verifier verifies source signatures. It caches the Sigstore public-good
// trusted root.
type Verifier struct {
	mu          sync.Mutex
	publicGood  root.TrustedMaterial
	lastAttempt time.Time
	lastErr     error

	// ignoreSCT skips the certificate transparency check of keyless
	// signatures. Only tests set it: their virtual Sigstore issues
	// certificates without SCTs.
	ignoreSCT bool
}

// NewVerifier returns a Verifier.
func NewVerifier() *Verifier {
	return &Verifier{}
}

// Verify walks the graph of the source at ref, which must carry a digest,
// and verifies every signature in it: against policy when it is set, else
// against trust, the keys the Kitchen trusts. client reads the registry;
// creds authenticate the reads of legacy signatures, nil is anonymous.
//
// With policy, any signature on the source that verifies makes it signed.
// Without, only a signature statement by a trusted key does, as Kokumi
// writes them. Verify fails when the signatures of the source itself cannot
// be listed, or when keyless signatures must be verified and the trusted
// root is not available.
func (v *Verifier) Verify(ctx context.Context, client oci.Client, creds credentials.Store, ref oci.Reference, policy *Policy, trust *signing.TrustSet) (*Report, error) {
	if ref.Digest == "" {
		return nil, fmt.Errorf("source %s has no digest", ref)
	}

	graph, err := oci.Walk(ctx, client, ref)
	if err != nil {
		return nil, fmt.Errorf("reading the graph of %s: %w", ref, err)
	}
	if graph.Root.Err != nil {
		return nil, fmt.Errorf("listing the signatures of %s: %w", ref, graph.Root.Err)
	}

	repo := ref
	repo.Tag = ""
	repo.Digest = ""
	c := &checker{verifier: v, client: client, repo: repo, policy: policy, trust: trust}
	if oci.IsPlainHTTP(repo.RepositoryReference()) {
		c.nameOpts = append(c.nameOpts, name.Insecure)
	}
	c.registryOpts = []ociremote.Option{
		ociremote.WithRemoteOptions(remote.WithContext(ctx), remote.WithAuthFromKeychain(keychain{ctx: ctx, store: creds})),
		ociremote.WithNameOptions(c.nameOpts...),
	}

	report := &Report{}
	for _, pair := range signaturesOf(graph.Root) {
		verdict, err := c.check(ctx, pair.node, pair.subject)
		if err != nil {
			return nil, err
		}
		report.Verdicts = append(report.Verdicts, verdict)

		signsSource := verdict.Result == Verified && verdict.Subject == ref.Digest && (policy != nil || verdict.signature)
		if report.Signer == nil && signsSource {
			report.Signer = verdict.Signer
		}
	}

	return report, nil
}

// signed is a signature node of a graph and the digest it signs.
type signed struct {
	node    *oci.Node
	subject string
}

// signaturesOf returns the signatures in the graph below node, depth first.
func signaturesOf(node *oci.Node) []signed {
	var found []signed
	for _, child := range append(append([]*oci.Node{}, node.Children...), node.Referrers...) {
		if IsSignature(child) {
			found = append(found, signed{node: child, subject: node.Digest})
		}
		found = append(found, signaturesOf(child)...)
	}
	return found
}

// IsSignature reports whether node holds signatures: a Sigstore bundle, a
// cosign tag of signatures or attestations, or a Notary signature. Verify
// returns a verdict for each of them.
func IsSignature(node *oci.Node) bool {
	switch {
	case node.ArtifactType == oci.SigstoreBundleMediaType, node.ArtifactType == notarySignatureType:
		return true
	case node.Link == oci.LinkTag:
		return strings.HasSuffix(node.Tag, ".sig") || strings.HasSuffix(node.Tag, ".att")
	default:
		return false
	}
}

// checker verifies the signatures of one graph.
type checker struct {
	verifier     *Verifier
	client       oci.Client
	repo         oci.Reference
	policy       *Policy
	trust        *signing.TrustSet
	nameOpts     []name.Option
	registryOpts []ociremote.Option

	trusted    root.TrustedMaterial
	trustedErr error
	loaded     bool
}

// trustedMaterial returns the trusted root of the policy, loading it once.
func (c *checker) trustedMaterial() (root.TrustedMaterial, error) {
	if !c.loaded {
		c.trusted, c.trustedErr = c.verifier.trustedMaterial(c.policy.TrustedRoot)
		c.loaded = true
	}
	return c.trusted, c.trustedErr
}

// check verifies the signature node, which signs the manifest with digest
// subject. It fails only when the trusted root keyless signatures need is
// not available.
func (c *checker) check(ctx context.Context, node *oci.Node, subject string) (Verdict, error) {
	verdict := Verdict{Digest: node.Digest, Subject: subject}

	switch {
	case node.ArtifactType == notarySignatureType:
		verdict.Result, verdict.Message = Unsupported, "Notary signatures are not verified"
		return verdict, nil
	case node.Link == oci.LinkTag && strings.HasSuffix(node.Tag, ".att"):
		verdict.Result, verdict.Message = Unsupported, "cosign attestations stored under a tag are not verified"
		return verdict, nil
	case node.Link == oci.LinkTag:
		return c.checkLegacy(ctx, verdict)
	default:
		return c.checkBundle(ctx, verdict)
	}
}

// checkBundle verifies the Sigstore bundle with verdict.Digest.
func (c *checker) checkBundle(ctx context.Context, verdict Verdict) (Verdict, error) {
	ref := c.repo
	ref.Digest = verdict.Digest
	fetched, err := c.client.FetchReferrer(ctx, ref)
	if err != nil {
		return invalid(verdict, err), nil
	}

	if c.policy == nil {
		return c.checkBundleWithTrust(verdict, fetched.Payload), nil
	}

	var bundle sgbundle.Bundle
	if err := bundle.UnmarshalJSON(fetched.Payload); err != nil {
		return invalid(verdict, fmt.Errorf("decoding bundle: %w", err)), nil
	}
	return c.checkSignedEntity(ctx, verdict, &bundle)
}

// checkBundleWithTrust verifies a bundle against the keys the Kitchen
// trusts.
func (c *checker) checkBundleWithTrust(verdict Verdict, payload []byte) Verdict {
	verification, err := c.trust.Verify(payload, verdict.Subject, "")
	var untrusted *signing.UntrustedSignerError
	switch {
	case errors.As(err, &untrusted):
		verdict.Result, verdict.Message = UntrustedSigner, err.Error()
	case err != nil:
		return invalid(verdict, err)
	default:
		verdict.Result = Verified
		verdict.Signer = &Signer{KeyID: verification.KeyID}
		verdict.signature = verification.PredicateType == attestation.SignaturePredicateType
	}
	return verdict
}

// checkSignedEntity verifies a Sigstore bundle against the policy: by key,
// or keyless by identity. A valid signature by another key or identity is
// reported as UntrustedSigner.
func (c *checker) checkSignedEntity(ctx context.Context, verdict Verdict, entity verify.SignedEntity) (Verdict, error) {
	hash, err := hex.DecodeString(strings.TrimPrefix(verdict.Subject, "sha256:"))
	if err != nil {
		return invalid(verdict, fmt.Errorf("decoding subject digest: %w", err)), nil
	}
	artifact := verify.WithArtifactDigest("sha256", hash)

	content, err := entity.VerificationContent()
	if err != nil {
		return invalid(verdict, err), nil
	}

	cert := content.Certificate()
	if cert == nil {
		return c.checkKeyBundle(ctx, verdict, entity, content, artifact), nil
	}

	if len(c.policy.Identities) == 0 {
		verdict.Result, verdict.Message = UntrustedSigner, "a keyless signature, and spec.source.verify names no identity"
		return verdict, nil
	}

	trusted, err := c.trustedMaterial()
	if err != nil {
		return verdict, err
	}

	for _, identity := range c.policy.Identities {
		co := &cosign.CheckOpts{
			TrustedMaterial: trusted,
			Identities:      []cosign.Identity{{IssuerRegExp: identity.Issuer, SubjectRegExp: identity.Subject}},
			IgnoreSCT:       c.verifier.ignoreSCT,
		}
		result, err := cosign.VerifyNewBundle(ctx, co, artifact, entity)
		if err != nil || result.Signature == nil || result.Signature.Certificate == nil {
			continue
		}
		verdict.Result = Verified
		verdict.Signer = &Signer{Issuer: result.Signature.Certificate.Issuer, Subject: result.Signature.Certificate.SubjectAlternativeName}
		return verdict, nil
	}

	// The policy accepts none of the identities. Tell a valid signature by
	// someone else from an invalid one.
	anyone := &cosign.CheckOpts{
		TrustedMaterial: trusted,
		Identities:      []cosign.Identity{{IssuerRegExp: ".*", SubjectRegExp: ".*"}},
		IgnoreSCT:       c.verifier.ignoreSCT,
	}
	result, err := cosign.VerifyNewBundle(ctx, anyone, artifact, entity)
	if err != nil {
		return invalid(verdict, err), nil
	}
	verdict.Result = UntrustedSigner
	if result.Signature != nil && result.Signature.Certificate != nil {
		signer := result.Signature.Certificate
		verdict.Message = fmt.Sprintf("signed by %s (issuer %s), which spec.source.verify does not accept", signer.SubjectAlternativeName, signer.Issuer)
	}
	return verdict, nil
}

// checkKeyBundle verifies a key-based bundle with the keys of the policy.
// A signature by a key the policy does not name is UntrustedSigner.
func (c *checker) checkKeyBundle(ctx context.Context, verdict Verdict, entity verify.SignedEntity, content verify.VerificationContent, artifact verify.ArtifactPolicyOption) Verdict {
	var hint string
	if key := content.PublicKey(); key != nil {
		hint = key.Hint()
	}

	var invalidErr error
	for _, key := range c.policy.Keys {
		co, keyID, err := keyCheckOpts(key)
		if err != nil {
			return invalid(verdict, err)
		}
		if _, err := cosign.VerifyNewBundle(ctx, co, artifact, entity); err != nil {
			if keyID == hint {
				invalidErr = err
			}
			continue
		}
		verdict.Result = Verified
		verdict.Signer = &Signer{KeyID: keyID}
		return verdict
	}

	if invalidErr != nil {
		return invalid(verdict, invalidErr)
	}
	verdict.Result = UntrustedSigner
	verdict.Message = "signed by a key spec.source.verify does not name"
	if hint != "" {
		verdict.Message = fmt.Sprintf("signed by key %s, which spec.source.verify does not name", hint)
	}
	return verdict
}

// checkLegacy verifies the legacy cosign signatures stored under the
// sha256-<hex>.sig tag of verdict.Subject.
func (c *checker) checkLegacy(ctx context.Context, verdict Verdict) (Verdict, error) {
	if c.policy == nil {
		verdict.Result, verdict.Message = Unsupported, "legacy cosign signatures are verified only against spec.source.verify"
		return verdict, nil
	}

	digest, err := name.NewDigest(c.repo.RepositoryReference()+"@"+verdict.Subject, c.nameOpts...)
	if err != nil {
		return invalid(verdict, err), nil
	}

	var errs []error
	for _, key := range c.policy.Keys {
		co, keyID, err := keyCheckOpts(key)
		if err != nil {
			return invalid(verdict, err), nil
		}
		co.RegistryClientOpts = c.registryOpts
		co.ClaimVerifier = cosign.SimpleClaimVerifier
		if _, _, err := cosign.VerifyImageSignatures(ctx, digest, co); err != nil {
			errs = append(errs, fmt.Errorf("key %s: %w", key.Name, err))
			continue
		}
		verdict.Result = Verified
		verdict.Signer = &Signer{KeyID: keyID}
		return verdict, nil
	}

	if len(c.policy.Identities) > 0 {
		trusted, err := c.trustedMaterial()
		if err != nil {
			return verdict, err
		}
		for _, identity := range c.policy.Identities {
			co := &cosign.CheckOpts{
				RegistryClientOpts: c.registryOpts,
				TrustedMaterial:    trusted,
				ClaimVerifier:      cosign.SimpleClaimVerifier,
				Identities:         []cosign.Identity{{IssuerRegExp: identity.Issuer, SubjectRegExp: identity.Subject}},
			}
			signatures, _, err := cosign.VerifyImageSignatures(ctx, digest, co)
			if err != nil {
				errs = append(errs, fmt.Errorf("identity %s (issuer %s): %w", identity.Subject, identity.Issuer, err))
				continue
			}
			signer, err := signerOf(signatures)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			verdict.Result = Verified
			verdict.Signer = signer
			return verdict, nil
		}
	}

	// cosign does not tell a signature by another signer from an invalid
	// one; the message carries what it reported.
	verdict.Result = UntrustedSigner
	verdict.Message = errors.Join(errs...).Error()
	return verdict, nil
}

func invalid(verdict Verdict, err error) Verdict {
	verdict.Result, verdict.Message = Invalid, err.Error()
	return verdict
}

// keyCheckOpts returns the options that verify signatures of key offline,
// and the key's ID.
func keyCheckOpts(key Key) (*cosign.CheckOpts, string, error) {
	public, err := cryptoutils.UnmarshalPEMToPublicKey(key.PEM)
	if err != nil {
		return nil, "", fmt.Errorf("parsing public key %s: %w", key.Name, err)
	}

	verifier, err := signature.LoadVerifier(public, crypto.SHA256)
	if err != nil {
		return nil, "", fmt.Errorf("loading public key %s: %w", key.Name, err)
	}

	keyID, err := signing.KeyID(public)
	if err != nil {
		return nil, "", err
	}

	return &cosign.CheckOpts{
		SigVerifier: verifier,
		IgnoreTlog:  true,
		Offline:     true,
	}, keyID, nil
}

// signerOf returns the keyless signer of the first verified legacy
// signature.
func signerOf(signatures []cosignoci.Signature) (*Signer, error) {
	for _, sig := range signatures {
		cert, err := sig.Cert()
		if err != nil || cert == nil {
			continue
		}
		summary, err := certificate.SummarizeCertificate(cert)
		if err != nil {
			continue
		}
		return &Signer{Issuer: summary.Issuer, Subject: summary.SubjectAlternativeName}, nil
	}
	return nil, errors.New("verified signature carries no certificate")
}

// trustedMaterial returns the trusted root keyless signatures verify
// against: trustedRoot when given, else the Sigstore public-good root
// fetched through TUF and kept for the life of the Verifier.
func (v *Verifier) trustedMaterial(trustedRoot []byte) (root.TrustedMaterial, error) {
	if len(trustedRoot) > 0 {
		parsed, err := root.NewTrustedRootFromJSON(trustedRoot)
		if err != nil {
			return nil, fmt.Errorf("parsing trusted root: %w", err)
		}
		return parsed, nil
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	if v.publicGood != nil {
		return v.publicGood, nil
	}
	if v.lastErr != nil && time.Since(v.lastAttempt) < trustedRootRetryInterval {
		return nil, v.lastErr
	}

	v.lastAttempt = time.Now()
	// The TUF metadata is kept in memory: the root filesystem of the
	// controller is read-only. The root refreshes itself once a day.
	v.publicGood, v.lastErr = root.NewLiveTrustedRoot(tuf.DefaultOptions().WithDisableLocalCache())
	if v.lastErr != nil {
		v.lastErr = fmt.Errorf("fetching the Sigstore public-good trusted root: %w", v.lastErr)
	}
	return v.publicGood, v.lastErr
}

// keychain authenticates go-containerregistry requests with the credentials
// of a Pantry.
type keychain struct {
	ctx   context.Context
	store credentials.Store
}

func (k keychain) Resolve(resource authn.Resource) (authn.Authenticator, error) {
	if k.store == nil {
		return authn.Anonymous, nil
	}

	cred, err := k.store.Get(k.ctx, resource.RegistryStr())
	if err != nil {
		return nil, fmt.Errorf("reading credentials for %s: %w", resource.RegistryStr(), err)
	}
	if cred == credentials.EmptyCredential {
		return authn.Anonymous, nil
	}

	return authn.FromConfig(authn.AuthConfig{
		Username:      cred.Username,
		Password:      cred.Password,
		IdentityToken: cred.RefreshToken,
		RegistryToken: cred.AccessToken,
	}), nil
}
