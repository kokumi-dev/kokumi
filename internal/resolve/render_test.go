package resolve

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/types"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/version"
)

func TestCommitMessage(t *testing.T) {
	annotated := map[string]string{deliveryv1alpha1.AnnotationCommitMessage: "Raise the replicas"}

	assert.Equal(t, "Raise the replicas", CommitMessage(annotated, true))
	assert.Equal(t, "", CommitMessage(map[string]string{deliveryv1alpha1.AnnotationCommitMessage: ""}, false),
		"an empty message is the user's choice")
	assert.Equal(t, initialCommitMessage, CommitMessage(nil, true))
	assert.Equal(t, automatedCommitMessage, CommitMessage(nil, false))
}

func TestRenderer(t *testing.T) {
	manifest := Renderer(nil, "ghcr.io/kokumi-dev/kokumi@sha256:bb")
	assert.Equal(t, deliveryv1alpha1.RenderTypeManifest, manifest.Type)
	assert.Equal(t, "v"+version.Version, manifest.Version)
	assert.Equal(t, "ghcr.io/kokumi-dev/kokumi@sha256:bb", manifest.Builder)

	helm := Renderer(&deliveryv1alpha1.Render{Helm: &deliveryv1alpha1.HelmRender{}}, "")
	assert.Equal(t, deliveryv1alpha1.RenderTypeHelm, helm.Type)
	assert.Equal(t, version.HelmVersion(), helm.Version)
	assert.Equal(t, version.FallbackBuilderID(), helm.Builder)
}

func TestArtifactConfig(t *testing.T) {
	const namespace, productHash = "team-a", "sha256:chart"
	order := &deliveryv1alpha1.Order{Name: "app", Namespace: namespace, UID: types.UID("uid")}
	effective := &EffectiveSpec{
		Product:        ProductIdentity{Name: "chart", ConfigHash: productHash},
		SourceVerified: &deliveryv1alpha1.SourceVerification{Provider: deliveryv1alpha1.VerificationProviderCosign, KeyID: "key"},
	}
	renderer := deliveryv1alpha1.Renderer{Type: deliveryv1alpha1.RenderTypeManifest, Version: "v1", Builder: "builder"}

	config := ArtifactConfig(order, effective, []byte(`{}`), "sha256:intent", renderer)

	assert.Equal(t, &artifact.ConfigObject{Namespace: namespace, Name: "app", UID: "uid"}, config.Order)
	assert.Equal(t, &artifact.ConfigProduct{Namespace: namespace, Name: "chart", ConfigHash: productHash}, config.Product)
	assert.Equal(t, "sha256:intent", config.ConfigHash)
	assert.Equal(t, artifact.ConfigRenderer{Type: "Manifest", Version: "v1", Builder: "builder"}, config.Renderer)
	assert.Equal(t, &artifact.ConfigVerification{Provider: "cosign", KeyID: "key"}, config.Source.Verified)

	effective.SourceVerified = nil
	assert.Nil(t, ArtifactConfig(order, effective, nil, "", renderer).Source.Verified)
}
