package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
)

// ProbeTimeout bounds a whole probe, so that an unreachable cluster holds up
// a reconcile no longer than this.
const ProbeTimeout = 10 * time.Second

// Health is what a probe of a cluster's control plane found.
type Health struct {
	// Healthy reports whether the control plane answered ready.
	Healthy bool
	// Unauthorized reports that the control plane rejected the credential.
	Unauthorized bool
	// Message says what the probe found.
	Message string
	// Version is the Kubernetes version the control plane reports, empty
	// when it could not be read.
	Version string
}

// Probe asks the control plane that cfg connects to for its version and
// whether it is ready, at /version and /readyz.
func Probe(ctx context.Context, cfg *rest.Config) Health {
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	client, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return unhealthy(err)
	}

	body, err := client.RESTClient().Get().AbsPath("/version").DoRaw(ctx)
	if err != nil {
		return unhealthy(err)
	}
	var info version.Info
	if err := json.Unmarshal(body, &info); err != nil {
		return unhealthy(fmt.Errorf("failed to read the version: %w", err))
	}
	health := Health{Version: info.GitVersion}
	if _, err := client.RESTClient().Get().AbsPath("/readyz").DoRaw(ctx); err != nil {
		h := unhealthy(err)
		h.Version = health.Version
		return h
	}
	health.Healthy = true
	health.Message = fmt.Sprintf("The control plane at %s is ready", cfg.Host)
	return health
}

// unhealthy returns the Health of a probe that failed with err.
func unhealthy(err error) Health {
	if apierrors.IsUnauthorized(err) || apierrors.IsForbidden(err) {
		return Health{Unauthorized: true, Message: "The control plane rejects the credential: " + err.Error()}
	}
	return Health{Message: "The control plane is not ready or cannot be reached: " + err.Error()}
}
