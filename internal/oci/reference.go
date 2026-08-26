package oci

import (
	"fmt"

	"github.com/opencontainers/go-digest"
	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

const (
	ociPrefix = "oci://"
)

// Reference is a parsed, validated OCI reference.
type Reference struct {
	properties.Reference
}

// Parse parses ref as an OCI reference, accepting optional tag/digest suffixes.
func Parse(url string) (Reference, error) {
	ref, err := properties.NewReference(url)
	if err != nil {
		return Reference{}, fmt.Errorf("invalid OCI reference %q: %w", url, err)
	}
	if refDigest, err := ref.GetDigest(); err == nil && refDigest.Algorithm() != digest.SHA256 {
		return Reference{}, fmt.Errorf("unsupported digest algorithm in %q: %s", url, refDigest.Algorithm())
	}
	return Reference{ref}, nil
}

// ShortDigest returns the first 12 hex characters of the SHA-256 digest.
// It returns an empty string if the reference has no digest or the digest
// is too short to be truncated safely.
func (r Reference) ShortDigest() string {
	digest, err := r.GetDigest()
	if err != nil {
		return ""
	}
	enc := digest.Encoded()
	if len(enc) < 12 {
		return ""
	}
	return enc[:12]
}

// OCIString returns the reference as an oci://-prefixed string.
func (r Reference) OCIString() string {
	return fmt.Sprintf("%s%s", ociPrefix, r.String())
}

// OCIRepositoryReference returns the oci://-prefixed registry/repository reference.
func (r Reference) OCIRepositoryReference() string {
	return fmt.Sprintf("%s%s", ociPrefix, r.RepositoryReference())
}

// RepositoryReference returns the registry/repository reference without the oci:// prefix or digest.
func (r Reference) RepositoryReference() string {
	return r.Registry + "/" + r.Repository
}
