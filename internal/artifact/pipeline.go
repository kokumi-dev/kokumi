package artifact

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/afero"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/renderer"
)

// Pipeline executes the multi-stage artifact render flows: pull, render,
// patch, annotate, and push (or return for previews). It composes a Store for
// the pull machinery and is domain-agnostic and safe for concurrent use.
type Pipeline struct {
	store *Store
}

// NewPipeline returns a Pipeline composing the given Store (which supplies
// the default OCI client, filesystem, and shared pull cache).
func NewPipeline(store *Store) *Pipeline {
	return &Pipeline{store: store}
}

// RenderRequest describes one render-and-publish operation.
type RenderRequest struct {
	// Source is the artifact to pull and render.
	Source Source
	// SourceClient optionally overrides the store's default client for
	// pulling. Nil falls back to the default.
	SourceClient oci.Client
	// Destination is where the rendered artifact is pushed.
	Destination Destination
	// DestClient optionally overrides the store's default client for
	// pushing. Nil falls back to the default.
	DestClient oci.Client
	// Render configures Helm or manifest rendering. Nil treats the source
	// as a pre-rendered manifest bundle.
	Render *RenderSpec
	// Patches are applied to the rendered manifests first.
	Patches []PatchSpec
	// Edits are applied on top of patches.
	Edits []PatchSpec
	// Name and Namespace are used as Helm releaseName/namespace fallbacks
	// when the Render spec does not set them explicitly.
	Name      string
	Namespace string
	// Description is attached as org.opencontainers.image.description.
	Description string
	// ParentDigest, when non-empty, is stored as the kokumi.dev/parent
	// annotation linking to the preceding artifact in a promotion chain.
	ParentDigest string
	// ExtraAnnotations are merged into the pushed manifest annotations
	// (e.g. kokumi.dev/prerendered) and may override computed ones.
	ExtraAnnotations map[string]string
}

// RenderResult holds the outcome of a Render call.
type RenderResult struct {
	// SourceRef is the resolved source reference including digest.
	SourceRef oci.Reference
	// DestRef is the pushed destination reference including digest.
	DestRef oci.Reference
	// SCM holds Git provenance extracted from the source artifact.
	SCM SCMInfo
}

// Render pulls the source artifact, applies rendering and patches/edits, and
// pushes the result to the destination. The pushed tag is Source.Version.
func (p *Pipeline) Render(ctx context.Context, req RenderRequest) (*RenderResult, error) {
	logger := log.FromContext(ctx)

	srcClient := cmp.Or(req.SourceClient, p.store.client)
	dstClient := cmp.Or(req.DestClient, p.store.client)

	sourceRef, err := parseTagged(req.Source.OCI, req.Source.Version)
	if err != nil {
		return nil, fmt.Errorf("failed to parse source ref: %w", err)
	}

	destRef, err := parseTagged(req.Destination.OCI, req.Source.Version)
	if err != nil {
		return nil, fmt.Errorf("failed to parse destination ref: %w", err)
	}

	logger.Info("Processing artifact", "source", sourceRef.RepositoryReference(), "destination", destRef.RepositoryReference(), "version", req.Source.Version)

	pulled, err := p.store.pull(ctx, srcClient, sourceRef, req.Render.EffectiveLayout(), "order-*")
	if err != nil {
		return nil, fmt.Errorf("failed to pull artifact: %w", err)
	}
	defer p.store.cleanup(pulled.dir)
	sourceRef.Digest = pulled.digest

	scm := scmInfoFromAnnotations(pulled.annotations)

	if err := p.renderToDir(ctx, pulled.dir, req.Render, pulled.mediaType, req.Patches, req.Edits, req.Name, req.Namespace); err != nil {
		return nil, err
	}

	logger.Info("Pushing artifact to destination")

	annotations := buildAnnotations(AnnotationInputs{
		Description:  req.Description,
		ParentDigest: req.ParentDigest,
		SourceRef:    sourceRef,
		SCM:          scm,
		Extra:        req.ExtraAnnotations,
	})

	destDigest, err := dstClient.Push(ctx, destRef, pulled.dir, annotations)
	if err != nil {
		return nil, fmt.Errorf("failed to push artifact: %w", err)
	}
	destRef.Digest = destDigest

	logger.Info("Successfully processed artifact", "digest", destDigest)

	return &RenderResult{
		SourceRef: sourceRef,
		DestRef:   destRef,
		SCM:       scm,
	}, nil
}

// PreviewRequest describes one render-without-publish operation.
type PreviewRequest struct {
	// Source is the artifact to pull and render.
	Source Source
	// SourceClient optionally overrides the store's default client.
	SourceClient oci.Client
	// Render configures Helm or manifest rendering. Nil treats the source
	// as a pre-rendered manifest bundle.
	Render *RenderSpec
	// Patches are applied first.
	Patches []PatchSpec
	// Edits are applied on top of patches.
	Edits []PatchSpec
	// Name and Namespace are used as Helm releaseName/namespace fallbacks.
	Name      string
	Namespace string
}

// Preview pulls and renders the source artifact without pushing, and returns
// the processed files individually, preserving the artifact's file layout.
func (p *Pipeline) Preview(ctx context.Context, req PreviewRequest) ([]File, error) {
	files, err := p.preview(ctx, req)
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no YAML files found in artifact")
	}

	return files, nil
}

// PreviewManifest behaves like Preview but concatenates the rendered files
// into a single multi-document YAML manifest.
func (p *Pipeline) PreviewManifest(ctx context.Context, req PreviewRequest) ([]byte, error) {
	files, err := p.preview(ctx, req)
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no YAML files found in artifact")
	}

	var out strings.Builder
	for _, file := range files {
		fmt.Fprintf(&out, "---\n# Source: %s\n%s\n", file.Path, strings.TrimSpace(file.Content))
	}

	return []byte(out.String()), nil
}

// preview runs the shared pull+render flow used by Preview and PreviewManifest.
func (p *Pipeline) preview(ctx context.Context, req PreviewRequest) ([]File, error) {
	srcClient := cmp.Or(req.SourceClient, p.store.client)

	sourceRef, err := parseTagged(req.Source.OCI, req.Source.Version)
	if err != nil {
		return nil, fmt.Errorf("failed to parse source ref: %w", err)
	}

	pulled, err := p.store.pull(ctx, srcClient, sourceRef, req.Render.EffectiveLayout(), "preview-*")
	if err != nil {
		return nil, fmt.Errorf("failed to pull artifact: %w", err)
	}
	defer p.store.cleanup(pulled.dir)

	if err := p.renderToDir(ctx, pulled.dir, req.Render, pulled.mediaType, req.Patches, req.Edits, req.Name, req.Namespace); err != nil {
		return nil, err
	}

	return listFiles(p.store.fs, pulled.dir)
}

// parseTagged parses an OCI URL and applies the given tag.
func parseTagged(url, tag string) (oci.Reference, error) {
	ref, err := oci.Parse(url)
	if err != nil {
		return oci.Reference{}, err
	}
	ref.Tag = tag
	return ref, nil
}

// renderToDir renders the pulled artifact in dir: it renders Helm charts into
// manifest.yaml and applies patches and edits to the manifest file(s).
func (p *Pipeline) renderToDir(ctx context.Context, dir string, render *RenderSpec, mediaType string, patches, edits []PatchSpec, name, namespace string) error {
	manifestPath := filepath.Join(dir, "manifest.yaml")

	if render != nil && render.Helm != nil {
		if mediaType != oci.HelmChartLayerMediaType {
			return fmt.Errorf("source is not a Helm chart (got media type %q)", mediaType)
		}

		releaseName := render.Helm.ReleaseName
		if releaseName == "" {
			releaseName = name
		}
		helmNamespace := render.Helm.Namespace
		if helmNamespace == "" {
			helmNamespace = namespace
		}

		chartTgz, err := afero.ReadFile(p.store.fs, filepath.Join(dir, "chart.tgz"))
		if err != nil {
			return fmt.Errorf("failed to read Helm chart: %w", err)
		}

		manifest, err := renderer.RenderChart(ctx, chartTgz, releaseName, helmNamespace, render.Helm.IncludeCRDs, render.Helm.Values)
		if err != nil {
			return fmt.Errorf("failed to render Helm chart: %w", err)
		}

		if err := afero.WriteFile(p.store.fs, manifestPath, []byte(manifest), 0600); err != nil {
			return fmt.Errorf("failed to write manifest: %w", err)
		}
	}

	// Remove the pulled chart blob so only rendered manifests are published.
	_ = p.store.fs.Remove(filepath.Join(dir, "chart.tgz")) //nolint:errcheck

	return p.processManifestFiles(ctx, dir, manifestPath, patches, edits)
}

// processManifestFiles applies patches and edits to the manifest content in dir.
// When manifestPath exists, only that file is processed. Otherwise every
// top-level YAML file is processed individually, preserving the artifact's
// original file layout.
func (p *Pipeline) processManifestFiles(ctx context.Context, dir, manifestPath string, patches, edits []PatchSpec) error {
	if _, err := p.store.fs.Stat(manifestPath); err == nil {
		return p.processManifestFile(ctx, manifestPath, patches, edits)
	}

	files, err := listFiles(p.store.fs, dir)
	if err != nil {
		return err
	}

	for _, file := range files {
		if err := p.processManifestFile(ctx, filepath.Join(dir, file.Path), patches, edits); err != nil {
			return err
		}
	}

	return nil
}

// processManifestFile applies patches and edits to a single file in place.
func (p *Pipeline) processManifestFile(ctx context.Context, path string, patches, edits []PatchSpec) error {
	content, err := afero.ReadFile(p.store.fs, path)
	if err != nil {
		return fmt.Errorf("failed to read manifest: %w", err)
	}

	processed, err := p.processManifest(ctx, content, patches, edits)
	if err != nil {
		return err
	}

	if err := afero.WriteFile(p.store.fs, path, processed, 0600); err != nil {
		return fmt.Errorf("failed to write manifest: %w", err)
	}

	return nil
}

// processManifest applies patches and edits when present, otherwise normalizes
// YAML formatting. Patches are applied first, then edits on top.
func (p *Pipeline) processManifest(ctx context.Context, content []byte, patches, edits []PatchSpec) ([]byte, error) {
	logger := log.FromContext(ctx)

	if len(patches) == 0 && len(edits) == 0 {
		logger.Info("Normalizing YAML formatting")

		processed, err := renderer.NormalizeYAML(content)
		if err != nil {
			return nil, fmt.Errorf("failed to normalize YAML: %w", err)
		}

		return processed, nil
	}

	result := content

	if len(patches) > 0 {
		logger.Info("Applying patches", "count", len(patches))

		processed, err := renderer.ApplyPatches(ctx, result, toRendererPatches(patches))
		if err != nil {
			return nil, fmt.Errorf("failed to apply patches: %w", err)
		}

		result = processed
	}

	if len(edits) > 0 {
		logger.Info("Applying edits", "count", len(edits))

		processed, err := renderer.ApplyPatches(ctx, result, toRendererPatches(edits))
		if err != nil {
			return nil, fmt.Errorf("failed to apply edits: %w", err)
		}

		result = processed
	}

	return result, nil
}

// toRendererPatches converts pipeline patch specs to renderer patches.
func toRendererPatches(patches []PatchSpec) []renderer.Patch {
	if len(patches) == 0 {
		return nil
	}

	out := make([]renderer.Patch, 0, len(patches))
	for _, patch := range patches {
		out = append(out, renderer.Patch{
			Target: renderer.PatchTarget{
				Kind:      patch.Target.Kind,
				Name:      patch.Target.Name,
				Namespace: patch.Target.Namespace,
			},
			Set: patch.Set,
		})
	}

	return out
}
