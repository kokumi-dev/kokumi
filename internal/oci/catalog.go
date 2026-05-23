package oci

import (
	"context"
	"errors"
	"fmt"

	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

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
		reg.Client = &auth.Client{
			Credential: credStore.Get,
		}
	}

	err = reg.Repositories(ctx, "", func(_ []string) error { return errStopPaging })
	if errors.Is(err, errStopPaging) {
		return nil
	}
	return err
}
