// Package scanner is the API between the manager and the kokumi-scanner
// component: its wire types, the client the manager calls it with, the HTTP
// handler the component serves, and the Trivy engine behind it.
//
// The scanner is unprivileged. It has no Kubernetes identity and stores no
// credentials: every request carries the connections it needs. It never
// signs or pushes; it returns SBOMs and reports, and the manager signs them
// and attaches them to the images.
package scanner

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"time"

	"github.com/oras-project/oras-go/v3/registry/remote/credentials"

	"github.com/kokumi-dev/kokumi/internal/oci"
)

// ScanPath is the path of the scan endpoint.
const ScanPath = "/v1/scan"

// MaxRequestSize bounds a request body. Requests carry the SBOMs they scan.
const MaxRequestSize = 64 << 20

// MaxOutputSize bounds the SBOM and the report the scanner returns for one
// image.
const MaxOutputSize = 32 << 20

// Request asks the scanner to scan images of one registry location with one
// vulnerability database.
type Request struct {
	// Database is the vulnerability database to scan with.
	Database Database `json:"database"`
	// JavaDatabase is the Java database, which identifies Java archives. It
	// is downloaded only when an image holds them; without it such images
	// fail to scan.
	JavaDatabase *Database `json:"javaDatabase,omitempty"`
	// Registry connects to the images.
	Registry Connection `json:"registry"`
	// Images are the images to scan.
	Images []Image `json:"images"`
}

// Database is an OCI artifact holding a Trivy database.
type Database struct {
	// Ref is the oci:// reference of the artifact, with its digest.
	Ref string `json:"ref"`
	// Registry connects to it.
	Registry Connection `json:"registry"`
}

// Connection is how the scanner reaches a registry: its host, credential,
// scheme and TLS material.
type Connection struct {
	// Host is the registry host the credential belongs to.
	Host         string `json:"host,omitempty"`
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
	AccessToken  string `json:"accessToken,omitempty"`
	PlainHTTP    bool   `json:"plainHTTP,omitempty"`
	// CertificateAuthority is a PEM CA trusted in addition to the system's.
	CertificateAuthority []byte `json:"certificateAuthority,omitempty"`
	// ClientCertificate and ClientKey are a PEM client certificate and key.
	ClientCertificate []byte `json:"clientCertificate,omitempty"`
	ClientKey         []byte `json:"clientKey,omitempty"`
}

// Image is an image to scan.
type Image struct {
	// Ref is the oci:// reference of the image, with its digest.
	Ref string `json:"ref"`
	// Platform selects the manifest of an image index, as "os/arch" or
	// "os/arch/variant". Empty scans a single-platform image.
	Platform string `json:"platform,omitempty"`
	// SBOM is the SBOM to scan, CycloneDX or SPDX JSON. When it is empty,
	// the scanner generates one from the image and returns it.
	SBOM json.RawMessage `json:"sbom,omitempty"`
}

// Response is the result of a Request.
type Response struct {
	// Scanner identifies the scanner.
	Scanner ScannerInfo `json:"scanner"`
	// Database identifies the database the images were scanned with.
	Database DatabaseInfo `json:"database"`
	// Images holds a result per image of the request, in its order.
	Images []Result `json:"images"`
}

// ScannerInfo identifies the scanner.
type ScannerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// DatabaseInfo identifies a vulnerability database.
type DatabaseInfo struct {
	// Ref is the oci:// reference of the database, with its digest.
	Ref string `json:"ref"`
	// UpdatedAt is when the database was built.
	UpdatedAt time.Time `json:"updatedAt"`
}

// Result is the result of scanning one image.
type Result struct {
	// Ref is the reference of the image, as requested.
	Ref string `json:"ref"`
	// SBOM is the CycloneDX SBOM the scanner generated, empty when the
	// request carried one.
	SBOM json.RawMessage `json:"sbom,omitempty"`
	// Report is the vulnerability report in the shape of cosign's vuln
	// predicate.
	Report json.RawMessage `json:"report,omitempty"`
	// Error says why the image could not be scanned. SBOM and Report are
	// empty then.
	Error string `json:"error,omitempty"`
}

// options returns the options of an OCI client for c.
func (c Connection) options() (oci.Options, error) {
	opts := oci.Options{
		Host: c.Host,
		Credential: credentials.Credential{
			Username:     c.Username,
			Password:     c.Password,
			RefreshToken: c.RefreshToken,
			AccessToken:  c.AccessToken,
		},
		PlainHTTP: c.PlainHTTP,
	}
	if len(c.CertificateAuthority) == 0 && len(c.ClientCertificate) == 0 && len(c.ClientKey) == 0 {
		return opts, nil
	}

	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if len(c.CertificateAuthority) > 0 {
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(c.CertificateAuthority) {
			return oci.Options{}, fmt.Errorf("certificate authority holds no PEM certificate")
		}
		cfg.RootCAs = pool
	}
	if len(c.ClientCertificate) > 0 || len(c.ClientKey) > 0 {
		pair, err := tls.X509KeyPair(c.ClientCertificate, c.ClientKey)
		if err != nil {
			return oci.Options{}, fmt.Errorf("client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	opts.TLSConfig = cfg
	return opts, nil
}
