package resolve

import (
	"encoding/json"
	"fmt"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/images"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// ArtifactSpec is the domain-agnostic rendering spec derived from an
// EffectiveSpec: the inputs the artifact pipeline needs, without any CRD types.
type ArtifactSpec struct {
	Source  artifact.Source
	Render  *artifact.RenderSpec
	Patches []artifact.PatchSpec
	Edits   []artifact.PatchSpec
	// Images holds the image policy and field specs; the caller adds the
	// registry client and mirrors.
	Images artifact.ImageOptions
}

// ToArtifactSpec converts the effective spec into pipeline inputs.
func (e *EffectiveSpec) ToArtifactSpec() (*ArtifactSpec, error) {
	render, err := toRenderSpec(e.Render)
	if err != nil {
		return nil, err
	}

	return &ArtifactSpec{
		Source:  artifact.Source{OCI: e.Source.OCI, Version: e.Source.Version, Digest: e.SourceDigest},
		Render:  render,
		Patches: toPatchSpecs(e.Patches),
		Edits:   toPatchSpecs(e.Edits),
		Images:  toImageOptions(e.Images),
	}, nil
}

// toImageOptions converts a Product's image settings into the pipeline's
// image options, without client and mirrors.
func toImageOptions(spec deliveryv1alpha1.ProductImages) artifact.ImageOptions {
	opts := artifact.ImageOptions{Policy: images.Policy(spec.EffectivePolicy())}
	for _, fs := range spec.FieldSpecs {
		opts.FieldSpecs = append(opts.FieldSpecs, images.FieldSpec{Group: fs.Group, Version: fs.Version, Kind: fs.Kind, Path: fs.Path})
	}
	return opts
}

// toRenderSpec converts a CRD Render into a pipeline RenderSpec.
// A nil Render converts to a nil RenderSpec (pre-rendered bundle).
func toRenderSpec(render *deliveryv1alpha1.Render) (*artifact.RenderSpec, error) {
	if render == nil {
		return nil, nil
	}

	spec := &artifact.RenderSpec{}

	if render.Helm != nil {
		vals, err := jsonToMap(render.Helm.Values)
		if err != nil {
			return nil, err
		}

		spec.Helm = &artifact.HelmSpec{
			ReleaseName: render.Helm.ReleaseName,
			Namespace:   render.Helm.Namespace,
			IncludeCRDs: render.Helm.IncludeCRDs,
			Values:      vals,
		}
	}

	if render.Manifest != nil {
		spec.Manifest = &artifact.ManifestSpec{
			Layout: artifact.Layout(render.Manifest.Layout),
		}
	}

	return spec, nil
}

// toPatchSpecs converts CRD patches into pipeline patch specs.
func toPatchSpecs(patches []deliveryv1alpha1.Patch) []artifact.PatchSpec {
	if len(patches) == 0 {
		return nil
	}

	out := make([]artifact.PatchSpec, 0, len(patches))
	for _, patch := range patches {
		out = append(out, artifact.PatchSpec{
			Target: artifact.PatchTargetSpec{
				Kind:      patch.Target.Kind,
				Name:      patch.Target.Name,
				Namespace: patch.Target.Namespace,
			},
			Set: patch.Set,
		})
	}

	return out
}

// jsonToMap decodes an inline JSON blob into a Helm values map.
func jsonToMap(j *apiextensionsv1.JSON) (map[string]any, error) {
	if j == nil || len(j.Raw) == 0 {
		return map[string]any{}, nil
	}

	var vals map[string]any
	if err := json.Unmarshal(j.Raw, &vals); err != nil {
		return nil, fmt.Errorf("unmarshal helm values: %w", err)
	}

	return vals, nil
}
