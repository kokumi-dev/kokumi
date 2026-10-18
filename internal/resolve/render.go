package resolve

import (
	"cmp"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/version"
)

// Commit messages of Preparations whose Order sets none.
const (
	initialCommitMessage   = "Initial commit"
	automatedCommitMessage = "Automatically generated"
)

// CommitMessage returns the commit message of the next Preparation of an
// Order with annotations: the one its commit-message annotation sets, else
// a default that tells the first Preparation apart, which has no parent.
func CommitMessage(annotations map[string]string, initial bool) string {
	if message, ok := annotations[deliveryv1alpha1.AnnotationCommitMessage]; ok {
		return message
	}
	if initial {
		return initialCommitMessage
	}
	return automatedCommitMessage
}

// Renderer returns the identity of the renderer that processes render,
// attributed to builderID, or to the fallback builder when it is empty.
func Renderer(render *deliveryv1alpha1.Render, builderID string) deliveryv1alpha1.Renderer {
	renderer := deliveryv1alpha1.Renderer{
		Type:    deliveryv1alpha1.RenderTypeManifest,
		Version: "v" + version.Version,
		Builder: cmp.Or(builderID, version.FallbackBuilderID()),
	}

	if render != nil && render.Helm != nil {
		renderer.Type = deliveryv1alpha1.RenderTypeHelm
		renderer.Version = version.HelmVersion()
	}

	return renderer
}

// ArtifactConfig returns the part of the artifact config the Order knows:
// what it is, the Product it was merged from, its intent and its renderer.
// The pipeline fills in the rest.
func ArtifactConfig(order *deliveryv1alpha1.Order, effective *EffectiveSpec, intent []byte, configHash string, renderer deliveryv1alpha1.Renderer) artifact.Config {
	config := artifact.Config{
		Order:      &artifact.ConfigObject{Namespace: order.Namespace, Name: order.Name, UID: order.UID},
		Product:    &artifact.ConfigProduct{Namespace: order.Namespace, Name: effective.Product.Name, ConfigHash: effective.Product.ConfigHash},
		ConfigHash: configHash,
		Renderer:   artifact.ConfigRenderer{Type: string(renderer.Type), Version: renderer.Version, Builder: renderer.Builder},
		Intent:     intent,
	}

	if verified := effective.SourceVerified; verified != nil {
		config.Source.Verified = &artifact.ConfigVerification{
			Provider: string(verified.Provider),
			KeyID:    verified.KeyID,
			Issuer:   verified.Issuer,
			Subject:  verified.Subject,
		}
	}

	return config
}
