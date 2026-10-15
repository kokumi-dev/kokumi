package server

import (
	"testing"

	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/resolve"
)

func TestPreviewRequest(t *testing.T) {
	helm := func(values string) *deliveryv1alpha1.Render {
		return &deliveryv1alpha1.Render{Helm: &deliveryv1alpha1.HelmRender{Values: &apiextensionsv1.JSON{Raw: []byte(values)}}}
	}
	source := deliveryv1alpha1.OCISource{OCI: "oci://registry/charts/app", Version: "1.0.0"}

	t.Run("rejects Helm values that are not an object", func(t *testing.T) {
		p := &preview{spec: &resolve.EffectiveSpec{Source: source, Render: helm(`[1,2]`)}, source: source}

		_, err := p.request()
		require.ErrorContains(t, err, "invalid render config")
	})

	t.Run("pulls the digest the Product resolved", func(t *testing.T) {
		const digest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
		p := &preview{spec: &resolve.EffectiveSpec{Source: source, SourceDigest: digest, Render: helm(`{"replicaCount":2}`)}, source: source}

		req, err := p.request()
		require.NoError(t, err)
		require.Equal(t, digest, req.Source.Digest)
		require.Equal(t, map[string]any{"replicaCount": float64(2)}, req.Render.Helm.Values)
	})
}
