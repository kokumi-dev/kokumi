// Package signing signs Kokumi's attestations with the Kitchen key and
// verifies them against the keys the Kitchen trusts.
//
// A signature is a Sigstore bundle v0.3 holding a DSSE envelope over an
// in-toto statement, as cosign v3 writes it. Kokumi's bundles carry no
// transparency log entries and no timestamps, so they verify offline, for
// example with "cosign verify --key kokumi.pub --insecure-ignore-tlog=true".
package signing

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/sigstore/cosign/v3/pkg/cosign"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
)

// Fields of the signing key Secret, in the layout of cosign's k8s:// key
// reference, so that stock cosign can create and read it.
const (
	// SecretPrivateKey holds the password-encrypted PEM private key.
	SecretPrivateKey = "cosign.key"
	// SecretPassword holds the password of the private key. It protects
	// nothing at rest; it exists so that cosign can read the key.
	SecretPassword = "cosign.password"
	// SecretPublicKey holds the PEM public key.
	SecretPublicKey = "cosign.pub"
)

// GeneratedKey is a new signing key in the layout of the signing key Secret.
type GeneratedKey struct {
	// PrivateKey is the PEM private key, encrypted with Password.
	PrivateKey []byte
	Password   []byte
	PublicKey  []byte
}

// GenerateKey generates a signing key as "cosign generate-key-pair" does,
// encrypted with a random password.
func GenerateKey() (GeneratedKey, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return GeneratedKey{}, fmt.Errorf("generating signing key password: %w", err)
	}
	password := []byte(base64.StdEncoding.EncodeToString(secret))

	keys, err := cosign.GenerateKeyPair(func(bool) ([]byte, error) { return password, nil })
	if err != nil {
		return GeneratedKey{}, fmt.Errorf("generating signing key: %w", err)
	}
	return GeneratedKey{PrivateKey: keys.PrivateBytes, Password: password, PublicKey: keys.PublicBytes}, nil
}

// ErrInvalidKey reports that a signing key Secret cannot be used. Retrying
// does not help until the Secret changes.
var ErrInvalidKey = errors.New("invalid signing key")

// Key is the Kitchen signing key: an ECDSA P-256 private key.
type Key struct {
	private   *ecdsa.PrivateKey
	publicPEM []byte
	id        string
}

// LoadKey loads the signing key from the data of a Secret in cosign's
// layout. The private key must be an ECDSA P-256 key, encrypted with
// cosign.password as cosign writes it, and cosign.pub, when present, must be
// its public key.
func LoadKey(data map[string][]byte) (*Key, error) {
	// A nil password skips decryption; cosign's empty password is an empty
	// one.
	password := append([]byte{}, data[SecretPassword]...)

	parsed, err := cryptoutils.UnmarshalPEMToPrivateKey(data[SecretPrivateKey], cryptoutils.StaticPasswordFunc(password))
	if err != nil {
		return nil, fmt.Errorf("%w: reading %s: %w", ErrInvalidKey, SecretPrivateKey, err)
	}

	private, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%w: %s must be an ECDSA P-256 key", ErrInvalidKey, SecretPrivateKey)
	}

	key, err := NewKey(private)
	if err != nil {
		return nil, err
	}

	if stored := data[SecretPublicKey]; len(stored) > 0 {
		public, err := cryptoutils.UnmarshalPEMToPublicKey(stored)
		if err != nil {
			return nil, fmt.Errorf("%w: parsing %s: %w", ErrInvalidKey, SecretPublicKey, err)
		}
		if !private.PublicKey.Equal(public) {
			return nil, fmt.Errorf("%w: %s is not the public key of %s", ErrInvalidKey, SecretPublicKey, SecretPrivateKey)
		}
	}

	return key, nil
}

// NewKey returns the signing key for private, which must be an ECDSA P-256
// key.
func NewKey(private *ecdsa.PrivateKey) (*Key, error) {
	if private.Curve != elliptic.P256() {
		return nil, fmt.Errorf("%w: must be an ECDSA P-256 key", ErrInvalidKey)
	}

	publicPEM, err := cryptoutils.MarshalPublicKeyToPEM(&private.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("encoding public key: %w", err)
	}

	id, err := KeyID(&private.PublicKey)
	if err != nil {
		return nil, err
	}

	return &Key{private: private, publicPEM: publicPEM, id: id}, nil
}

// ID returns the key ID: the base64-encoded SHA-256 of the DER public key.
// It is the hint of every bundle the key signs.
func (k *Key) ID() string {
	return k.id
}

// PublicKeyPEM returns the PEM-encoded public key.
func (k *Key) PublicKeyPEM() []byte {
	return bytes.Clone(k.publicPEM)
}

// KeyID returns the ID of public: the base64-encoded SHA-256 of its DER
// encoding, as sigstore-go and cosign compute key hints.
func KeyID(public crypto.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		return "", fmt.Errorf("encoding public key: %w", err)
	}

	sum := sha256.Sum256(der)
	return base64.StdEncoding.EncodeToString(sum[:]), nil
}
