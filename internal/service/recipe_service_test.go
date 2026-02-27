package service

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

func TestRecipeService_ProcessRecipe(t *testing.T) {
	const fakeDigest = "sha256:fdf90e00e76bf3f0d2e5042c4c4e6c42a6d38c1e2b4f5a7d8e9f0a1b2c3d4e5f"

	tests := []struct {
		name          string
		recipe        *deliveryv1alpha1.Recipe
		wantSourceRef string
		wantDestRef   string
		wantSourceDig string
		wantDestDig   string
		wantErr       bool
	}{
		{
			name: "no patches",
			recipe: &deliveryv1alpha1.Recipe{
				Spec: deliveryv1alpha1.RecipeSpec{
					Source: deliveryv1alpha1.OCISource{
						OCI:     "oci://kokumi-registry.kokumi.svc.cluster.local:5000/recipe/external-secrets",
						Version: "1.0.0",
					},
					Destination: deliveryv1alpha1.OCIDestination{
						OCI: "oci://kokumi-registry.kokumi.svc.cluster.local:5000/preparation/external-secrets",
					},
				},
			},
			wantSourceRef: "kokumi-registry.kokumi.svc.cluster.local:5000/recipe/external-secrets",
			wantDestRef:   "kokumi-registry.kokumi.svc.cluster.local:5000/preparation/external-secrets",
			wantSourceDig: fakeDigest,
			wantDestDig:   fakeDigest,
			wantErr:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			svc := NewRecipeService(oci.NewFakeClient(fs), fs)

			result, err := svc.ProcessRecipe(context.Background(), tc.recipe)

			if tc.wantErr {
				require.Error(t, err)
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
