package version

import (
	"context"
	"runtime/debug"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// EnvBuilderID overrides the builder identity of the controller.
	EnvBuilderID = "KOKUMI_BUILDER_ID"

	envPodName      = "POD_NAME"
	envPodNamespace = "POD_NAMESPACE"

	managerContainer = "manager"
	helmModule       = "helm.sh/helm/v4"
	unknownVersion   = "unknown"

	// The kubelet reports the image ID shortly after the container starts.
	imageIDAttempts = 5
	imageIDInterval = 2 * time.Second
)

// HelmVersion returns the version of the Helm library compiled into the
// binary, or "unknown" when the build carries no module information.
func HelmVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return unknownVersion
	}

	return helmVersion(info)
}

// helmVersion returns the version of the Helm module in info.
func helmVersion(info *debug.BuildInfo) string {
	for _, dep := range info.Deps {
		if dep.Path != helmModule {
			continue
		}
		if dep.Replace != nil && dep.Replace.Version != "" {
			return dep.Replace.Version
		}
		return dep.Version
	}

	return unknownVersion
}

// FallbackBuilderID is the builder identity used when the controller cannot
// determine the image it runs from.
func FallbackBuilderID() string {
	return "kokumi@" + Version
}

// PodGetter reads the Pod with the given namespace and name.
type PodGetter func(ctx context.Context, namespace, name string) (*corev1.Pod, error)

// BuilderID resolves the identity of the running controller, which is
// recorded as the builder of every artifact it renders. The first match wins:
// the KOKUMI_BUILDER_ID override, the image ID of the manager container read
// from the controller's own Pod, and finally "kokumi@<version>".
func BuilderID(ctx context.Context, getenv func(string) string, getPod PodGetter) string {
	if id := strings.TrimSpace(getenv(EnvBuilderID)); id != "" {
		return id
	}

	name, namespace := getenv(envPodName), getenv(envPodNamespace)
	if name == "" || namespace == "" || getPod == nil {
		return FallbackBuilderID()
	}

	for attempt := range imageIDAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return FallbackBuilderID()
			case <-time.After(imageIDInterval):
			}
		}

		pod, err := getPod(ctx, namespace, name)
		if err != nil {
			log.FromContext(ctx).Error(err, "Could not read own Pod, using the version as builder identity", "pod", name)
			return FallbackBuilderID()
		}

		if id := managerImageID(pod); id != "" {
			return id
		}
	}

	return FallbackBuilderID()
}

// managerImageID returns the image ID of the manager container of pod, without
// the container runtime scheme some runtimes prefix it with.
func managerImageID(pod *corev1.Pod) string {
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name != managerContainer {
			continue
		}
		if _, id, ok := strings.Cut(status.ImageID, "://"); ok {
			return id
		}
		return status.ImageID
	}

	return ""
}
