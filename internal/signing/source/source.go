// Package source verifies the signatures of source artifacts with the cosign
// library, as Flux's OCIRepository does: Sigstore bundles found through the
// OCI referrers API or the referrers tag schema, and the legacy
// "sha256-<hex>.sig" signatures many upstream artifacts still carry. Key-based
// signatures verify offline; keyless signatures verify against a Sigstore
// trusted root.
package source

import (
	"context"
	"crypto"
	"encoding/hex"
	"errors"
	"fmt"
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

	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/signing"
)

// trustedRootRetryInterval rate-limits fetching the Sigstore public-good
// trusted root after a failure.
const trustedRootRetryInterval = time.Minute

// ErrNotVerified reports that a source carries no signature the policy
// accepts.
var ErrNotVerified = errors.New("source signature not verified")

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

// Verifier verifies source signatures. It caches the Sigstore public-good
// trusted root.
type Verifier struct {
	mu          sync.Mutex
	publicGood  root.TrustedMaterial
	lastAttempt time.Time
	lastErr     error
}

// NewVerifier returns a Verifier.
func NewVerifier() *Verifier {
	return &Verifier{}
}

// Verify verifies that the artifact at ref, which must carry a digest, is
// signed according to policy. creds authenticate against the registry; nil
// is anonymous. A signature the policy does not accept fails with an error
// wrapping ErrNotVerified.
func (v *Verifier) Verify(ctx context.Context, ref oci.Reference, creds credentials.Store, policy Policy) (*Signer, error) {
	if ref.Digest == "" {
		return nil, fmt.Errorf("source %s has no digest", ref)
	}

	var nameOpts []name.Option
	if oci.IsPlainHTTP(ref.RepositoryReference()) {
		nameOpts = append(nameOpts, name.Insecure)
	}

	digest, err := name.NewDigest(ref.RepositoryReference()+"@"+ref.Digest, nameOpts...)
	if err != nil {
		return nil, fmt.Errorf("parsing source reference %s: %w", ref, err)
	}

	registryOpts := []ociremote.Option{
		ociremote.WithRemoteOptions(remote.WithContext(ctx), remote.WithAuthFromKeychain(keychain{ctx: ctx, store: creds})),
		ociremote.WithNameOptions(nameOpts...),
	}

	bundles, _, err := cosign.GetBundles(ctx, digest, registryOpts, nameOpts...)
	var noBundles *cosign.ErrNoMatchingAttestations
	if err != nil && !errors.As(err, &noBundles) {
		return nil, fmt.Errorf("listing signatures of %s: %w", ref, err)
	}

	var errs []error

	for _, key := range policy.Keys {
		co, keyID, err := keyCheckOpts(key, registryOpts)
		if err != nil {
			return nil, err
		}
		if err := v.verify(ctx, digest, co, bundles); err != nil {
			errs = append(errs, fmt.Errorf("key %s: %w", key.Name, err))
			continue
		}
		return &Signer{KeyID: keyID}, nil
	}

	if len(policy.Identities) > 0 {
		trusted, err := v.trustedMaterial(policy.TrustedRoot)
		if err != nil {
			return nil, err
		}

		for _, identity := range policy.Identities {
			co := &cosign.CheckOpts{
				RegistryClientOpts: registryOpts,
				TrustedMaterial:    trusted,
				Identities:         []cosign.Identity{{IssuerRegExp: identity.Issuer, SubjectRegExp: identity.Subject}},
			}
			signer, err := v.verifyKeyless(ctx, digest, co, bundles)
			if err != nil {
				errs = append(errs, fmt.Errorf("identity %s (issuer %s): %w", identity.Subject, identity.Issuer, err))
				continue
			}
			return signer, nil
		}
	}

	if len(errs) == 0 {
		return nil, fmt.Errorf("%w: the policy names no key and no identity", ErrNotVerified)
	}
	return nil, fmt.Errorf("%w: %s: %w", ErrNotVerified, ref, errors.Join(errs...))
}

// keyCheckOpts returns the options that verify signatures of key offline,
// and the key's ID.
func keyCheckOpts(key Key, registryOpts []ociremote.Option) (*cosign.CheckOpts, string, error) {
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
		RegistryClientOpts: registryOpts,
		SigVerifier:        verifier,
		IgnoreTlog:         true,
		Offline:            true,
	}, keyID, nil
}

// verify verifies the bundles of digest with co, or its legacy signatures
// when it has no bundles, as the cosign CLI does.
func (v *Verifier) verify(ctx context.Context, digest name.Digest, co *cosign.CheckOpts, bundles []*sgbundle.Bundle) error {
	if len(bundles) == 0 {
		co.ClaimVerifier = cosign.SimpleClaimVerifier
		_, _, err := cosign.VerifyImageSignatures(ctx, digest, co)
		return err
	}

	_, err := verifyBundles(ctx, digest, co, bundles)
	return err
}

// verifyKeyless verifies like verify and returns the identity of the signer
// whose certificate verified.
func (v *Verifier) verifyKeyless(ctx context.Context, digest name.Digest, co *cosign.CheckOpts, bundles []*sgbundle.Bundle) (*Signer, error) {
	if len(bundles) == 0 {
		co.ClaimVerifier = cosign.SimpleClaimVerifier
		signatures, _, err := cosign.VerifyImageSignatures(ctx, digest, co)
		if err != nil {
			return nil, err
		}
		return signerOf(signatures)
	}

	result, err := verifyBundles(ctx, digest, co, bundles)
	if err != nil {
		return nil, err
	}
	if result.Signature == nil || result.Signature.Certificate == nil {
		return nil, errors.New("verified bundle carries no certificate")
	}
	return &Signer{
		Issuer:  result.Signature.Certificate.Issuer,
		Subject: result.Signature.Certificate.SubjectAlternativeName,
	}, nil
}

// verifyBundles returns the result of the first bundle that verifies with
// co for digest. Any bundle counts, as with "cosign verify": the bundle's
// in-toto subject must name digest.
func verifyBundles(ctx context.Context, digest name.Digest, co *cosign.CheckOpts, bundles []*sgbundle.Bundle) (*verify.VerificationResult, error) {
	hash, err := hex.DecodeString(digest.DigestStr()[len("sha256:"):])
	if err != nil {
		return nil, fmt.Errorf("decoding digest: %w", err)
	}

	var errs []error
	for _, b := range bundles {
		result, err := cosign.VerifyNewBundle(ctx, co, verify.WithArtifactDigest("sha256", hash), b)
		if err == nil {
			return result, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
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
