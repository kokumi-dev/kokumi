package oci

import (
	"context"
	"errors"
	"fmt"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/registry/remote"
	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/properties"
)

// zeroDigest addresses a manifest that never exists; registries answer a
// referrers request for it without needing any content.
const zeroDigest = digest.Digest("sha256:0000000000000000000000000000000000000000000000000000000000000000")

// errStopPaging is used as a sentinel to stop catalog pagination after the
// first page without treating it as an actual error.
var errStopPaging = errors.New("stop paging")

// PingRegistry tests connectivity to a registry by attempting to list at most
// one page of repositories. It returns nil when the registry is reachable and
// credentials (if any) are accepted.
func PingRegistry(ctx context.Context, host string, credStore credentials.Store) error {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		return fmt.Errorf("create registry client for %q: %w", host, err)
	}

	reg.PlainHTTP = isPlainHTTP(host)
	reg.RepositoryListPageSize = 1

	if credStore != nil {
		reg.Client = newAuthClient(credStore)
	}

	err = reg.Repositories(ctx, "", func(_ []string) error { return errStopPaging })
	if errors.Is(err, errStopPaging) {
		return nil
	}
	return err
}

// ProbeReferrersAPI reports whether the registry serves the OCI referrers API
// for ref's repository. It lets oras run its own detection, so the result is
// the path oras takes for that repository. It returns an error when the
// capability could not be determined, for example because the repository does
// not exist yet.
func ProbeReferrersAPI(ctx context.Context, ref Reference, credStore credentials.Store) (bool, error) {
	repo, err := NewAuthenticatedORASClient(credStore).newRepository(ref.RepositoryReference())
	if err != nil {
		return false, fmt.Errorf("create repository for %q: %w", ref, err)
	}

	subject := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: zeroDigest}
	if err := repo.Referrers(ctx, subject, "", func([]ocispec.Descriptor) error { return nil }); err != nil {
		return false, fmt.Errorf("probe referrers API of %q: %w", ref.RepositoryReference(), err)
	}

	switch repo.ReferrersCapability() {
	case properties.ReferrersAPISupported:
		return true, nil
	case properties.ReferrersAPIUnsupported:
		return false, nil
	default:
		return false, fmt.Errorf("probe referrers API of %q: capability unknown", ref.RepositoryReference())
	}
}
