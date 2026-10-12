package oci

import (
	"fmt"

	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
)

// CredentialStore holds registry credentials. A nil store means anonymous access.
type CredentialStore = credentials.Store

// CredentialsFromDockerConfigJSON parses a kubernetes.io/dockerconfigjson
// secret value and returns an in-memory ORAS credential store.
// The input is the raw bytes of the .dockerconfigjson key.
func CredentialsFromDockerConfigJSON(data []byte) (credentials.Store, error) {
	store, err := credentials.NewMemoryStoreFromDockerConfig(data)
	if err != nil {
		return nil, fmt.Errorf("parse docker config: %w", err)
	}
	return store, nil
}
