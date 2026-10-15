package signing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// ErrNotVerified reports that a bundle did not verify against the trust set:
// it is malformed, signed by a key the set does not hold, does not sign the
// expected subject, or carries another predicate type.
var ErrNotVerified = errors.New("signature not verified")

// CurrentKeyName is the name the current signing key is trusted under.
const CurrentKeyName = "current"

// ErrInvalidTrustedKey reports that a key to trust cannot be parsed.
var ErrInvalidTrustedKey = errors.New("invalid trusted key")

// TrustedPublicKey is a PEM public key to trust under a name.
type TrustedPublicKey struct {
	Name string
	PEM  []byte
}

// TrustSet is the set of public keys whose signatures Kokumi trusts for its
// own artifacts: the current signing key plus the Kitchen's trusted keys.
type TrustSet struct {
	keys map[string]trustedKey
	id   string
}

type trustedKey struct {
	name     string
	verifier *root.ExpiringKey
}

// NewTrustSet returns the trust set of keys. Keys are addressed by their ID;
// a key listed twice is trusted once, under its first name.
func NewTrustSet(keys ...TrustedPublicKey) (*TrustSet, error) {
	set := &TrustSet{keys: make(map[string]trustedKey, len(keys))}

	for _, k := range keys {
		public, err := cryptoutils.UnmarshalPEMToPublicKey(k.PEM)
		if err != nil {
			return nil, fmt.Errorf("%w %q: %w", ErrInvalidTrustedKey, k.Name, err)
		}

		verifier, err := signature.LoadDefaultVerifier(public)
		if err != nil {
			return nil, fmt.Errorf("%w %q: %w", ErrInvalidTrustedKey, k.Name, err)
		}

		id, err := KeyID(public)
		if err != nil {
			return nil, err
		}

		if _, exists := set.keys[id]; !exists {
			set.keys[id] = trustedKey{name: k.Name, verifier: root.NewExpiringKey(verifier, time.Time{}, time.Time{})}
		}
	}

	ids := set.KeyIDs()
	sum := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	set.id = "sha256:" + hex.EncodeToString(sum[:])

	return set, nil
}

// TrustSetForKitchen returns the trust set the Kitchen publishes: its current
// public key and its trusted keys. It holds only the trusted keys while the
// Kitchen has not published its key.
func TrustSetForKitchen(kitchen *deliveryv1alpha1.Kitchen) (*TrustSet, error) {
	var keys []TrustedPublicKey
	if s := kitchen.Status.Signing; s != nil && s.PublicKey != "" {
		keys = append(keys, TrustedPublicKey{Name: CurrentKeyName, PEM: []byte(s.PublicKey)})
	}
	return NewTrustSet(append(keys, KitchenTrustedKeys(kitchen)...)...)
}

// KitchenTrustedKeys returns the keys in the Kitchen's spec.signing.trustedKeys.
func KitchenTrustedKeys(kitchen *deliveryv1alpha1.Kitchen) []TrustedPublicKey {
	if kitchen == nil || kitchen.Spec.Signing == nil {
		return nil
	}

	keys := make([]TrustedPublicKey, 0, len(kitchen.Spec.Signing.TrustedKeys))
	for _, k := range kitchen.Spec.Signing.TrustedKeys {
		keys = append(keys, TrustedPublicKey{Name: k.Name, PEM: []byte(k.PublicKey)})
	}
	return keys
}

// ID identifies the content of the trust set: equal sets have equal IDs. It
// keys caches of verification results.
func (t *TrustSet) ID() string {
	return t.id
}

// KeyIDs returns the IDs of the trusted keys, sorted.
func (t *TrustSet) KeyIDs() []string {
	ids := make([]string, 0, len(t.keys))
	for id := range t.keys {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// KeyName returns the name the key with id is trusted under, or "" when the
// set does not hold it.
func (t *TrustSet) KeyName(id string) string {
	return t.keys[id].name
}

// Verification is the result of a verified bundle.
type Verification struct {
	// KeyID identifies the key that signed the bundle.
	KeyID string
	// PredicateType is the predicate type of the statement.
	PredicateType string
	// Statement is the signed in-toto statement.
	Statement []byte
}

// Verify verifies that data is a Sigstore bundle signed by a trusted key over
// an in-toto statement of predicateType about the artifact with
// subjectDigest ("sha256:<hex>"). An empty predicateType accepts any.
// Failures wrap ErrNotVerified.
func (t *TrustSet) Verify(data []byte, subjectDigest, predicateType string) (*Verification, error) {
	hexDigest, ok := strings.CutPrefix(subjectDigest, "sha256:")
	if !ok {
		return nil, fmt.Errorf("unsupported subject digest %q: only sha256 is supported", subjectDigest)
	}
	rawDigest, err := hex.DecodeString(hexDigest)
	if err != nil {
		return nil, fmt.Errorf("invalid subject digest %q: %w", subjectDigest, err)
	}

	var b bundle.Bundle
	if err := b.UnmarshalJSON(data); err != nil {
		return nil, fmt.Errorf("%w: decoding bundle: %w", ErrNotVerified, err)
	}

	keyID := b.GetVerificationMaterial().GetPublicKey().GetHint()
	key, ok := t.keys[keyID]
	if !ok {
		return nil, fmt.Errorf("%w: signed by untrusted key %q", ErrNotVerified, keyID)
	}

	material := root.NewTrustedPublicKeyMaterial(func(hint string) (root.TimeConstrainedVerifier, error) {
		if hint != keyID {
			return nil, fmt.Errorf("untrusted key %q", hint)
		}
		return key.verifier, nil
	})

	verifier, err := verify.NewVerifier(material, verify.WithNoObserverTimestamps())
	if err != nil {
		return nil, fmt.Errorf("creating verifier: %w", err)
	}

	if _, err := verifier.Verify(&b, verify.NewPolicy(verify.WithArtifactDigest("sha256", rawDigest), verify.WithKey())); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotVerified, err)
	}

	envelope := b.GetDsseEnvelope()
	if envelope.GetPayloadType() != InTotoPayloadType {
		return nil, fmt.Errorf("%w: payload type %q, want %q", ErrNotVerified, envelope.GetPayloadType(), InTotoPayloadType)
	}

	var statement struct {
		PredicateType string `json:"predicateType"`
	}
	if err := json.Unmarshal(envelope.GetPayload(), &statement); err != nil {
		return nil, fmt.Errorf("%w: decoding statement: %w", ErrNotVerified, err)
	}
	if predicateType != "" && statement.PredicateType != predicateType {
		return nil, fmt.Errorf("%w: predicate type %q, want %q", ErrNotVerified, statement.PredicateType, predicateType)
	}

	return &Verification{KeyID: keyID, PredicateType: statement.PredicateType, Statement: envelope.GetPayload()}, nil
}
