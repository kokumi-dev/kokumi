package oci

import (
	"testing"
)

func Test_isPlainHTTP(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		// In-cluster Kubernetes service DNS
		{"kokumi-registry.kokumi.svc.cluster.local:5000/repo/image", true},
		{"registry.default.svc.cluster.local/repo/image", true},
		{"registry.svc/image", true},
		// Loopback and bare IPs
		{"localhost:5000/image", true},
		{"127.0.0.1:5000/image", true},
		{"10.96.0.1/image", true},
		// Public / external registries
		{"ghcr.io/myorg/myimage", false},
		{"registry-1.docker.io/library/nginx", false},
		{"public.ecr.aws/myimage", false},
		{"gcr.io/google-containers/pause", false},
	}

	for _, tc := range tests {
		t.Run(tc.ref, func(t *testing.T) {
			got := isPlainHTTP(tc.ref)
			if got != tc.want {
				t.Errorf("isPlainHTTP(%q) = %v, want %v", tc.ref, got, tc.want)
			}
		})
	}
}
