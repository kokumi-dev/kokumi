package registry

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/oras-project/oras-go/v3/registry/remote/auth"
	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/errcode"
	corev1 "k8s.io/api/core/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

// Keys of the Secret named by a Registry's certSecretRef, as Flux names them.
const (
	keyCACert     = "ca.crt"
	keyClientCert = "tls.crt"
	keyClientKey  = "tls.key"
)

// Connection is how Kokumi reaches a registry location.
type Connection struct {
	// Registry is the Registry the location matched, nil when none did.
	Registry *deliveryv1alpha1.Registry
	// Host is the canonical host of the location.
	Host string
	// Credential authenticates the requests to Host. It is empty for
	// anonymous access.
	Credential credentials.Credential
	// PlainHTTP accesses the registry over HTTP instead of HTTPS.
	PlainHTTP bool
	// TLSConfig holds the Registry's CA and client certificate, nil for the
	// system defaults.
	TLSConfig *tls.Config
	// Client accesses the location.
	Client oci.Client

	// version identifies the version of the Registry and its Secrets the
	// connection was built from.
	version string
}

// Authenticated reports whether the connection identifies Kokumi to the
// registry, with a credential or a client certificate.
func (c Connection) Authenticated() bool {
	return c.Credential != credentials.EmptyCredential || (c.TLSConfig != nil && len(c.TLSConfig.Certificates) > 0)
}

// RegistryName returns the name of the matched Registry, or "" without one.
func (c Connection) RegistryName() string {
	if c.Registry == nil {
		return ""
	}
	return c.Registry.Name
}

// Keychain returns a go-containerregistry keychain that sends the
// connection's credential to its host.
func (c Connection) Keychain() authn.Keychain {
	return keychain{host: c.Host, cred: c.Credential}
}

// Transport returns an HTTP transport with the connection's TLS settings.
func (c Connection) Transport() http.RoundTripper {
	if c.TLSConfig == nil {
		return http.DefaultTransport
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = c.TLSConfig
	return transport
}

// keychain authenticates go-containerregistry requests to one host.
type keychain struct {
	host string
	cred credentials.Credential
}

// Resolve implements authn.Keychain.
func (k keychain) Resolve(resource authn.Resource) (authn.Authenticator, error) {
	if k.cred == credentials.EmptyCredential || oci.CanonicalHost(resource.RegistryStr()) != k.host {
		return authn.Anonymous, nil
	}
	return authn.FromConfig(authn.AuthConfig{
		Username:      k.cred.Username,
		Password:      k.cred.Password,
		IdentityToken: k.cred.RefreshToken,
		RegistryToken: k.cred.AccessToken,
	}), nil
}

// Error is a failure to connect that a condition reason describes, such as
// a Registry or Secret that does not exist.
type Error struct {
	// Reason is the condition reason, one of the v1alpha1 Reason constants.
	Reason string
	Err    error
}

func (e *Error) Error() string { return e.Err.Error() }

func (e *Error) Unwrap() error { return e.Err }

// Reason returns the condition reason err carries, or fallback when it
// carries none.
func Reason(err error, fallback string) string {
	if connErr, ok := errors.AsType[*Error](err); ok {
		return connErr.Reason
	}
	return fallback
}

// PingReason returns the condition reason for an error of oci.Client.Ping:
// Unauthorized when the registry rejected the request or requires
// credentials the client lacks, Unreachable otherwise.
func PingReason(err error) string {
	var resp *errcode.ErrorResponse
	if errors.As(err, &resp) && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
		return deliveryv1alpha1.ReasonUnauthorized
	}
	if errors.Is(err, auth.ErrBasicCredentialNotFound) {
		return deliveryv1alpha1.ReasonUnauthorized
	}
	return deliveryv1alpha1.ReasonUnreachable
}

// Retriable reports whether a failed oci.Client.Ping may succeed soon: the
// registry did not answer. Rejected credentials and a certificate the client
// does not trust are a matter of configuration and stay until it changes.
func Retriable(err error) bool {
	return PingReason(err) == deliveryv1alpha1.ReasonUnreachable && !untrustedCertificate(err)
}

// untrustedCertificate reports whether err is a TLS certificate the client
// does not trust.
func untrustedCertificate(err error) bool {
	var verification *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	return errors.As(err, &verification) || errors.As(err, &unknownAuthority) || errors.As(err, &hostname) || errors.As(err, &invalid)
}

// RegistryNotFound returns the error of a registryRefs entry that names no
// Registry.
func RegistryNotFound(name string) error {
	return &Error{Reason: deliveryv1alpha1.ReasonRegistryNotFound, Err: fmt.Errorf("registry %q not found", name)}
}

// SecretNotFound returns the error of a Registry whose Secret does not
// exist.
func SecretNotFound(name string) error {
	return &Error{Reason: deliveryv1alpha1.ReasonSecretNotFound, Err: fmt.Errorf("secret %q not found", name)}
}

// tlsConfig returns the TLS settings in secret: ca.crt is trusted in
// addition to the system's certificate authorities, and tls.crt with
// tls.key is presented as client certificate.
func tlsConfig(secret *corev1.Secret) (*tls.Config, error) {
	ca, cert, key := secret.Data[keyCACert], secret.Data[keyClientCert], secret.Data[keyClientKey]
	if len(ca) == 0 && len(cert) == 0 && len(key) == 0 {
		return nil, fmt.Errorf("holds none of %s, %s and %s", keyCACert, keyClientCert, keyClientKey)
	}

	cfg := &tls.Config{MinVersion: tls.VersionTLS12}

	if len(ca) > 0 {
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(ca) {
			return nil, fmt.Errorf("%s holds no PEM certificate", keyCACert)
		}
		cfg.RootCAs = pool
	}

	if len(cert) > 0 || len(key) > 0 {
		pair, err := tls.X509KeyPair(cert, key)
		if err != nil {
			return nil, fmt.Errorf("%s and %s: %w", keyClientCert, keyClientKey, err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}

	return cfg, nil
}
