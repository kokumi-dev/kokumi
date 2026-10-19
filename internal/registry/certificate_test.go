package registry

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

// selfSigned returns a self-signed certificate and its key, PEM-encoded.
func selfSigned(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "registry.internal"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func TestScope_Connect_certificates(t *testing.T) {
	certPEM, keyPEM := selfSigned(t)

	reg := testRegistry("internal", "oci://registry.internal")
	reg.Spec.CertSecretRef = &corev1.LocalObjectReference{Name: "client-certs"}
	certs := &corev1.Secret{Name: "client-certs", Namespace: testNamespace, Data: map[string][]byte{
		keyCACert: certPEM, keyClientCert: certPEM, keyClientKey: keyPEM,
	}}

	clients, _ := newTestClients()
	conn, err := clients.Scope([]Entry{{Registry: &reg, CertSecret: certs}}).Connect("oci://registry.internal/team-a/app")
	require.NoError(t, err)

	require.NotNil(t, conn.TLSConfig)
	assert.NotNil(t, conn.TLSConfig.RootCAs)
	assert.Len(t, conn.TLSConfig.Certificates, 1)
	assert.True(t, conn.Authenticated(), "a client certificate identifies Kokumi")
	assert.Equal(t, certPEM, conn.CertificateAuthority, "the PEM is kept to hand the connection on")
	assert.Equal(t, certPEM, conn.ClientCertificate)
	assert.Equal(t, keyPEM, conn.ClientKey)

	anon, err := clients.Scope(nil).Connect("oci://quay.io/x/app")
	require.NoError(t, err)
	assert.Nil(t, anon.CertificateAuthority)
}
