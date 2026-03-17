package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

func TestOrderService_ProcessOrder(t *testing.T) {
	const fakeDigest = "sha256:fdf90e00e76bf3f0d2e5042c4c4e6c42a6d38c1e2b4f5a7d8e9f0a1b2c3d4e5f"

	tests := []struct {
		name          string
		order         *deliveryv1alpha1.Order
		wantSourceRef string
		wantDestRef   string
		wantSourceDig string
		wantDestDig   string
		wantErr       bool
		wantErrMsg    string
	}{
		{
			name: "no patches",
			order: &deliveryv1alpha1.Order{
				Spec: deliveryv1alpha1.OrderSpec{
					Source: deliveryv1alpha1.OCISource{
						OCI:     "oci://kokumi-registry.kokumi.svc.cluster.local:5000/order/external-secrets",
						Version: "1.0.0",
					},
					Destination: deliveryv1alpha1.OCIDestination{
						OCI: "oci://kokumi-registry.kokumi.svc.cluster.local:5000/preparation/external-secrets",
					},
				},
			},
			wantSourceRef: "kokumi-registry.kokumi.svc.cluster.local:5000/order/external-secrets",
			wantDestRef:   "kokumi-registry.kokumi.svc.cluster.local:5000/preparation/external-secrets",
			wantSourceDig: fakeDigest,
			wantDestDig:   fakeDigest,
		},
		{
			name: "helm render rejected when source is not a helm chart",
			order: &deliveryv1alpha1.Order{
				Spec: deliveryv1alpha1.OrderSpec{
					Source: deliveryv1alpha1.OCISource{
						OCI:     "oci://kokumi-registry.kokumi.svc.cluster.local:5000/order/my-app",
						Version: "1.0.0",
					},
					Destination: deliveryv1alpha1.OCIDestination{
						OCI: "oci://kokumi-registry.kokumi.svc.cluster.local:5000/preparation/my-app",
					},
					Render: &deliveryv1alpha1.Render{
						Helm: &deliveryv1alpha1.HelmRender{
							ReleaseName: "my-app",
							Namespace:   "default",
						},
					},
				},
			},
			wantErr:    true,
			wantErrMsg: "source is not a Helm chart",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			svc := NewOrderService(oci.NewFakeClient(fs), fs, "")

			result, err := svc.ProcessOrder(context.Background(), tc.order)

			if tc.wantErr {
				require.Error(t, err)
				if tc.wantErrMsg != "" {
					assert.Contains(t, err.Error(), tc.wantErrMsg)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tc.wantSourceRef, result.SourceRef)
			assert.Equal(t, tc.wantDestRef, result.DestRef)
			assert.Equal(t, tc.wantSourceDig, result.SourceDigest)
			assert.Equal(t, tc.wantDestDig, result.DestDigest)
		})
	}
}

// helmFakeClient is a FakeClient variant that simulates an OCI artifact whose
// primary layer is a Helm chart. It writes a minimal (empty) chart.tgz so that
// the Helm loader can be invoked in tests that exercise the full service pipeline.
type helmFakeClient struct {
	fs afero.Fs
}

var _ oci.Client = (*helmFakeClient)(nil)

func (c *helmFakeClient) Pull(_ context.Context, _, _, targetDir string) (string, string, error) {
	chartPath := filepath.Join(targetDir, "chart.tgz")
	if err := afero.WriteFile(c.fs, chartPath, []byte{}, 0600); err != nil {
		return "", "", err
	}

	return oci.HelmChartLayerMediaType, "sha256:fdf90e00e76bf3f0d2e5042c4c4e6c42a6d38c1e2b4f5a7d8e9f0a1b2c3d4e5f", nil
}

func (c *helmFakeClient) Push(_ context.Context, _, _, _ string) (string, error) {
	return "sha256:fdf90e00e76bf3f0d2e5042c4c4e6c42a6d38c1e2b4f5a7d8e9f0a1b2c3d4e5f", nil
}

func TestOrderService_PullCache(t *testing.T) {
	const (
		fakeDigest = "sha256:fdf90e00e76bf3f0d2e5042c4c4e6c42a6d38c1e2b4f5a7d8e9f0a1b2c3d4e5f"
		cacheDir   = "/cache"
	)

	order := &deliveryv1alpha1.Order{
		Spec: deliveryv1alpha1.OrderSpec{
			Source: deliveryv1alpha1.OCISource{
				OCI:     "oci://registry.svc.cluster.local:5000/order/app",
				Version: "1.0.0",
			},
			Destination: deliveryv1alpha1.OCIDestination{
				OCI: "oci://registry.svc.cluster.local:5000/preparation/app",
			},
		},
	}

	t.Run("cache miss populates cache", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		pullCount := 0
		client := &countingFakeClient{fs: fs, onPull: func() { pullCount++ }}

		svc := NewOrderService(client, fs, cacheDir)
		_, err := svc.ProcessOrder(context.Background(), order)
		require.NoError(t, err)

		assert.Equal(t, 1, pullCount, "expected one pull on cache miss")

		// Verify cache entry was written.
		key := pullCacheKey(
			"registry.svc.cluster.local:5000/order/app",
			"1.0.0",
		)
		exists, err := afero.Exists(fs, filepath.Join(cacheDir, key, "meta.json"))
		require.NoError(t, err)
		assert.True(t, exists, "meta.json should be written to cache")
	})

	t.Run("cache hit skips pull", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		pullCount := 0
		client := &countingFakeClient{fs: fs, onPull: func() { pullCount++ }}

		svc := NewOrderService(client, fs, cacheDir)

		// First call populates the cache.
		_, err := svc.ProcessOrder(context.Background(), order)
		require.NoError(t, err)
		require.Equal(t, 1, pullCount)

		// Second call with identical spec should hit the cache.
		_, err = svc.ProcessOrder(context.Background(), order)
		require.NoError(t, err)
		assert.Equal(t, 1, pullCount, "second call should be served from cache without pulling")
	})
}

// countingFakeClient wraps FakeClient and invokes onPull on every Pull call.
type countingFakeClient struct {
	fs     afero.Fs
	onPull func()
}

var _ oci.Client = (*countingFakeClient)(nil)

func (c *countingFakeClient) Pull(ctx context.Context, ref, tag, targetDir string) (string, string, error) {
	c.onPull()
	return oci.NewFakeClient(c.fs).Pull(ctx, ref, tag, targetDir)
}

func (c *countingFakeClient) Push(ctx context.Context, ref, tag, sourceDir string) (string, error) {
	return oci.NewFakeClient(c.fs).Push(ctx, ref, tag, sourceDir)
}
