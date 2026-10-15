// Package signingtest provides signing keys for tests.
package signingtest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"

	"github.com/kokumi-dev/kokumi/internal/signing"
)

// TB is the part of testing.TB the helpers use. Ginkgo's GinkgoT() has it.
type TB interface {
	Helper()
	Fatalf(format string, args ...any)
}

// NewSigner returns a Signer with a freshly generated key.
func NewSigner(t TB) *signing.Signer {
	t.Helper()

	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}

	key, err := signing.NewKey(private)
	if err != nil {
		t.Fatalf("creating key: %v", err)
	}

	signer, err := signing.NewSigner(key)
	if err != nil {
		t.Fatalf("creating signer: %v", err)
	}

	return signer
}

// NewProvider returns a Provider whose state signs with signer and trusts
// signer and the extra signers.
func NewProvider(t TB, signer *signing.Signer, trusted ...*signing.Signer) *signing.Provider {
	t.Helper()

	keys := make([]signing.TrustedPublicKey, 0, 1+len(trusted))
	keys = append(keys, signing.TrustedPublicKey{Name: signing.CurrentKeyName, PEM: signer.PublicKeyPEM()})
	for _, s := range trusted {
		keys = append(keys, signing.TrustedPublicKey{Name: s.KeyID(), PEM: s.PublicKeyPEM()})
	}

	trust, err := signing.NewTrustSet(keys...)
	if err != nil {
		t.Fatalf("creating trust set: %v", err)
	}

	return signing.NewStaticProvider(signing.State{Signer: signer, Trust: trust})
}
