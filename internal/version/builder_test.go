package version

import (
	"context"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func TestBuilderID(t *testing.T) {
	const (
		imageID   = "ghcr.io/kokumi-dev/kokumi@sha256:bb"
		namespace = "kokumi"
		podName   = "manager-0"
	)

	pod := func(id string) *corev1.Pod {
		return &corev1.Pod{
			Namespace: namespace,
			Name:      podName,
			Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
				{Name: "sidecar", ImageID: "other@sha256:aa"},
				{Name: managerContainer, ImageID: id},
			}},
		}
	}
	podEnv := map[string]string{envPodName: podName, envPodNamespace: namespace}

	tests := []struct {
		name string
		env  map[string]string
		pod  *corev1.Pod
		want string
	}{
		{name: "override wins", env: map[string]string{EnvBuilderID: " custom ", envPodName: podName, envPodNamespace: namespace}, pod: pod(imageID), want: "custom"},
		{name: "image ID of the manager container", env: podEnv, pod: pod(imageID), want: imageID},
		{name: "runtime scheme is stripped", env: podEnv, pod: pod("docker-pullable://" + imageID), want: imageID},
		{name: "no pod identity falls back", env: map[string]string{}, pod: pod(imageID), want: FallbackBuilderID()},
		{name: "missing pod falls back", env: podEnv, want: FallbackBuilderID()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getPod := func(_ context.Context, podNamespace, name string) (*corev1.Pod, error) {
				if tt.pod == nil || tt.pod.Namespace != podNamespace || tt.pod.Name != name {
					return nil, apierrors.NewNotFound(corev1.Resource("pods"), name)
				}
				return tt.pod, nil
			}

			got := BuilderID(context.Background(), func(key string) string { return tt.env[key] }, getPod)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestHelmVersion(t *testing.T) {
	const helm = "v4.3.0"

	tests := []struct {
		name string
		deps []*debug.Module
		want string
	}{
		{name: "module version", deps: []*debug.Module{{Path: "example.com/other", Version: "v1.0.0"}, {Path: helmModule, Version: helm}}, want: helm},
		{name: "replaced module", deps: []*debug.Module{{Path: helmModule, Version: helm, Replace: &debug.Module{Path: "example.com/fork", Version: "v4.3.1"}}}, want: "v4.3.1"},
		{name: "local replacement keeps the module version", deps: []*debug.Module{{Path: helmModule, Version: helm, Replace: &debug.Module{Path: "../helm"}}}, want: helm},
		{name: "module missing", deps: []*debug.Module{{Path: "example.com/other", Version: "v1.0.0"}}, want: unknownVersion},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, helmVersion(&debug.BuildInfo{Deps: tt.deps}))
		})
	}
}
