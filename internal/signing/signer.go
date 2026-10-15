package signing

import (
	"bytes"
	"context"
	"crypto"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	"github.com/sigstore/sigstore-go/pkg/sign"
	"google.golang.org/protobuf/encoding/protojson"
)

// InTotoPayloadType is the DSSE payload type of an in-toto statement.
const InTotoPayloadType = "application/vnd.in-toto+json"

// Signer signs in-toto statements with the Kitchen key.
type Signer struct {
	key   *Key
	trust *TrustSet
}

// NewSigner returns a Signer for key.
func NewSigner(key *Key) (*Signer, error) {
	trust, err := NewTrustSet(TrustedPublicKey{Name: CurrentKeyName, PEM: key.publicPEM})
	if err != nil {
		return nil, err
	}
	return &Signer{key: key, trust: trust}, nil
}

// KeyID returns the ID of the signing key.
func (s *Signer) KeyID() string {
	return s.key.id
}

// PublicKeyPEM returns the PEM-encoded public key of the signing key.
func (s *Signer) PublicKeyPEM() []byte {
	return s.key.PublicKeyPEM()
}

// TrustSet returns the trust set that holds only the signing key. Bundles it
// verifies were signed by this Signer's key.
func (s *Signer) TrustSet() *TrustSet {
	return s.trust
}

// Sign returns the Sigstore bundle that signs statement, an in-toto
// statement, in a DSSE envelope. Signing is deterministic: equal statements
// yield equal bundles, byte for byte, so a retried push attaches the same
// referrer.
func (s *Signer) Sign(statement []byte) ([]byte, error) {
	bundle, err := sign.Bundle(&sign.DSSEData{Data: statement, PayloadType: InTotoPayloadType}, keypair{key: s.key}, sign.BundleOptions{})
	if err != nil {
		return nil, fmt.Errorf("signing statement: %w", err)
	}

	// protojson varies its whitespace on purpose; compacting it keeps the
	// encoding stable across library versions.
	encoded, err := protojson.Marshal(bundle)
	if err != nil {
		return nil, fmt.Errorf("encoding bundle: %w", err)
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, encoded); err != nil {
		return nil, fmt.Errorf("encoding bundle: %w", err)
	}

	return compact.Bytes(), nil
}

// keypair implements sign.Keypair with deterministic ECDSA signatures (RFC
// 6979). sigstore-go's own keypairs sign with a random nonce, so equal
// statements would yield different bundles.
type keypair struct {
	key *Key
}

var _ sign.Keypair = keypair{}

func (k keypair) GetHashAlgorithm() protocommon.HashAlgorithm {
	return protocommon.HashAlgorithm_SHA2_256
}

func (k keypair) GetSigningAlgorithm() protocommon.PublicKeyDetails {
	return protocommon.PublicKeyDetails_PKIX_ECDSA_P256_SHA_256
}

func (k keypair) GetHint() []byte {
	return []byte(k.key.id)
}

func (k keypair) GetKeyAlgorithm() string {
	return "ECDSA"
}

func (k keypair) GetPublicKey() crypto.PublicKey {
	return &k.key.private.PublicKey
}

func (k keypair) GetPublicKeyPem() (string, error) {
	return string(k.key.publicPEM), nil
}

// SignData signs the SHA-256 of data. A nil random source makes the
// signature deterministic (RFC 6979).
func (k keypair) SignData(_ context.Context, data []byte) ([]byte, []byte, error) {
	digest := sha256.Sum256(data)

	signature, err := k.key.private.Sign(nil, digest[:], crypto.SHA256)
	if err != nil {
		return nil, nil, fmt.Errorf("signing: %w", err)
	}

	return signature, digest[:], nil
}
