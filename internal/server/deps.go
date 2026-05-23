package server

import (
	"github.com/go-logr/logr"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/spf13/afero"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// defaultNamespace is the Kubernetes namespace used when none is specified in a request.
const defaultNamespace = "default"

// apiDeps groups the runtime dependencies used by HTTP handlers.
// All fields may be nil when no Kubernetes configuration was found; handlers
// return 503 Service Unavailable in that case.
type apiDeps struct {
	reader    client.Reader
	writer    client.Client
	ociClient oci.Client
	fs        afero.Fs
	logger    logr.Logger
}
