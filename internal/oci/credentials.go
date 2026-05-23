package oci

import (
	"fmt"

	"oras.land/oras-go/v2/registry/remote/credentials"
)

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
