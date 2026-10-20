package artifact

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/spf13/afero"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/kokumi-dev/kokumi/internal/images"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/registry"
	"github.com/kokumi-dev/kokumi/internal/renderer"
)

// Pipeline executes the multi-stage artifact render flows: pull, render,
// patch, annotate, and push (or return for previews). It composes a Store for
// the pull machinery and is domain-agnostic and safe for concurrent use.
type Pipeline struct {
	store *Store
	// PreviewImagesTimeout bounds the image lookups of one preview, which a
	// user waits for. References not resolved in time stay as written.
	PreviewImagesTimeout time.Duration
}

// defaultPreviewImagesTimeout is the PreviewImagesTimeout of NewPipeline.
const defaultPreviewImagesTimeout = 20 * time.Second

// NewPipeline returns a Pipeline composing the given Store (which supplies
// the default OCI client, filesystem, and shared pull cache).
func NewPipeline(store *Store) *Pipeline {
	return &Pipeline{store: store, PreviewImagesTimeout: defaultPreviewImagesTimeout}
}

// RenderRequest describes one render-and-publish operation.
type RenderRequest struct {
	// Source is the artifact to pull and render.
	Source Source
	// Destination is where the rendered artifact is pushed.
	Destination Destination
	// Product connects to the source, the images and their vendor
	// location. Where it gives no client, as the zero Scope does, the
	// store's default client is used.
	Product registry.Scope
	// Order connects to the destination, with the same fallback.
	Order registry.Scope
	// Render configures Helm or manifest rendering. Nil treats the source
	// as a pre-rendered manifest bundle.
	Render *RenderSpec
	// Patches are applied to the rendered manifests first.
	Patches []PatchSpec
	// Edits are applied on top of patches.
	Edits []PatchSpec
	// Images configures the image stage, which runs after the edits.
	Images ImageOptions
	// Name and Namespace are used as Helm releaseName/namespace fallbacks
	// when the Render spec does not set them explicitly.
	Name      string
	Namespace string
	// Description is attached as org.opencontainers.image.description.
	Description string
	// ParentDigest, when non-empty, is stored as the kokumi.dev/parent
	// annotation linking to the preceding artifact in a promotion chain.
	ParentDigest string
	// ArtifactType is the artifactType of the pushed manifest.
	ArtifactType string
	// Config is pushed as the artifact's config blob. The caller fills what
	// it knows (Order, ConfigHash, Product, Renderer, Intent);
	// Render fills the source, destination, parent, commit message, and
	// creation time.
	Config Config
}

// RenderResult holds the outcome of a Render call.
type RenderResult struct {
	// SourceRef is the resolved source reference including digest.
	SourceRef oci.Reference
	// DestRef is the pushed destination reference including digest.
	DestRef oci.Reference
	// SCM holds Git provenance extracted from the source artifact.
	SCM SCMInfo
	// Images lists the images the manifests reference.
	Images []images.Image
	// Config is the config blob that was pushed.
	Config Config
}

// Render pulls the source artifact, applies rendering and patches/edits, and
// pushes the result to the destination. The pushed tag is Source.Version.
func (p *Pipeline) Render(ctx context.Context, req RenderRequest) (*RenderResult, error) {
	logger := log.FromContext(ctx)

	srcClient, err := p.connect(req.Product, req.Source.OCI)
	if err != nil {
		return nil, err
	}
	dstClient, err := p.connect(req.Order, req.Destination.OCI)
	if err != nil {
		return nil, err
	}

	sourceRef, err := req.Source.ref()
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

	imgs, err := p.processImages(ctx, pulled.dir, req.Images, images.Stage{Scope: req.Product})
	if err != nil {
		return nil, err
	}

	logger.Info("Pushing artifact to destination")

	annotations := buildAnnotations(AnnotationInputs{
		Description:  req.Description,
		ParentDigest: req.ParentDigest,
		SourceRef:    sourceRef,
		SCM:          scm,
	})

	// The manifest and the config blob carry the same creation time.
	created := time.Now().UTC().Format(time.RFC3339)
	annotations[ocispec.AnnotationCreated] = created

	config := completeConfig(req, sourceRef, destRef, created)
	config.Images = imgs

	data, err := config.Encode()
	if err != nil {
		return nil, err
	}

	destDigest, err := dstClient.Push(ctx, destRef, pulled.dir, oci.PushOptions{
		Annotations:     annotations,
		ArtifactType:    req.ArtifactType,
		Config:          data,
		ConfigMediaType: oci.PreparationConfigMediaType,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to push artifact: %w", err)
	}
	destRef.Digest = destDigest

	logger.Info("Successfully processed artifact", "digest", destDigest)

	return &RenderResult{
		SourceRef: sourceRef,
		DestRef:   destRef,
		SCM:       scm,
		Images:    imgs,
		Config:    config,
	}, nil
}

// completeConfig returns the request's config with the fields only the
// pipeline knows filled in.
func completeConfig(req RenderRequest, sourceRef, destRef oci.Reference, created string) Config {
	config := req.Config
	config.SchemaVersion = ConfigSchemaVersion
	config.CommitMessage = req.Description
	config.CreatedTime = created
	config.Parent = req.ParentDigest
	config.Source.OCI = sourceRef.OCIRepositoryReference()
	config.Source.Version = req.Source.Version
	config.Source.Digest = sourceRef.Digest
	config.Destination.OCI = destRef.OCIRepositoryReference()

	return config
}

// PreviewRequest describes one render-without-publish operation.
type PreviewRequest struct {
	// Source is the artifact to pull and render.
	Source Source
	// Product connects to the source and the images. Where it gives no
	// client, as the zero Scope does, the store's default client is used.
	Product registry.Scope
	// Render configures Helm or manifest rendering. Nil treats the source
	// as a pre-rendered manifest bundle.
	Render *RenderSpec
	// Patches are applied first.
	Patches []PatchSpec
	// Edits are applied on top of patches.
	Edits []PatchSpec
	// Images configures the image stage. A preview resolves and rewrites
	// images but copies none, and leaves the references it cannot resolve
	// as written.
	Images ImageOptions
	// Name and Namespace are used as Helm releaseName/namespace fallbacks.
	Name      string
	Namespace string
}

// Preview pulls and renders the source artifact without pushing, and returns
// the processed files individually, preserving the artifact's file layout.
func (p *Pipeline) Preview(ctx context.Context, req PreviewRequest) ([]File, error) {
	srcClient, err := p.connect(req.Product, req.Source.OCI)
	if err != nil {
		return nil, err
	}

	sourceRef, err := req.Source.ref()
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

	if err := p.previewImages(ctx, pulled.dir, req); err != nil {
		return nil, err
	}

	files, err := listFiles(p.store.fs, pulled.dir)
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no YAML files found in artifact")
	}

	return files, nil
}

// previewImages pins or vendors the images in dir as the render would,
// without copying, within PreviewImagesTimeout. Under Record a render leaves
// the manifests unchanged, so the preview looks nothing up.
func (p *Pipeline) previewImages(ctx context.Context, dir string, req PreviewRequest) error {
	if cmp.Or(req.Images.Policy, images.PolicyRecord) == images.PolicyRecord {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, p.PreviewImagesTimeout)
	defer cancel()

	_, err := p.processImages(ctx, dir, req.Images, images.Stage{Scope: req.Product, DryRun: true})
	return err
}

// connect returns the client scope connects to url with, or the store's
// default client when it gives none.
func (p *Pipeline) connect(scope registry.Scope, url string) (oci.Client, error) {
	conn, err := scope.Connect(url)
	if err != nil {
		return nil, err
	}
	return cmp.Or(conn.Client, p.store.client), nil
}

// ref returns the reference the source is pulled by: its digest when pinned,
// its version tag otherwise.
func (s Source) ref() (oci.Reference, error) {
	ref, err := parseTagged(s.OCI, s.Version)
	if err != nil {
		return oci.Reference{}, err
	}
	ref.Digest = s.Digest
	return ref, nil
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

	return p.processManifestFiles(ctx, dir, patches, edits)
}

// processManifestFiles applies patches and edits to the manifest files in
// dir (see manifestFiles), preserving the artifact's original file layout.
func (p *Pipeline) processManifestFiles(ctx context.Context, dir string, patches, edits []PatchSpec) error {
	paths, err := manifestFiles(p.store.fs, dir)
	if err != nil {
		return err
	}

	for _, path := range paths {
		if err := p.processManifestFile(ctx, path, patches, edits); err != nil {
			return err
		}
	}

	return nil
}

// manifestFiles returns the paths of the manifest files in dir: only
// manifest.yaml when it exists, every YAML and JSON file otherwise.
func manifestFiles(fs afero.Fs, dir string) ([]string, error) {
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if _, err := fs.Stat(manifestPath); err == nil {
		return []string{manifestPath}, nil
	}

	return manifestPaths(fs, dir)
}

// processImages runs the image stage over the manifest files in dir and
// writes back what it rewrote. stage carries the Scope and whether the stage
// is a dry run; opts fills in the rest. Pin and Vendor need fully rendered
// manifests: they fail on a source with a kustomization file, whose image
// transformer and remote bases Argo CD applies later.
func (p *Pipeline) processImages(ctx context.Context, dir string, opts ImageOptions, stage images.Stage) ([]images.Image, error) {
	stage.Policy = cmp.Or(opts.Policy, images.PolicyRecord)
	stage.Client = p.store.client
	stage.DestClient = p.store.client
	stage.Mirrors = opts.Mirrors
	stage.Locked = opts.Locked
	if opts.VendorURL != "" {
		root, err := oci.Parse(opts.VendorURL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse vendor URL: %w", err)
		}
		stage.VendorRoot = &root
	}

	if stage.Policy != images.PolicyRecord && hasKustomization(p.store.fs, dir) {
		return nil, fmt.Errorf("the %s image policy needs rendered manifests, but the source has a kustomization file that Argo CD renders later", stage.Policy)
	}

	paths, err := manifestFiles(p.store.fs, dir)
	if err != nil {
		return nil, err
	}

	var (
		refs      []string
		manifests []*images.Manifest
	)
	for _, path := range paths {
		content, err := afero.ReadFile(p.store.fs, path)
		if err != nil {
			return nil, fmt.Errorf("failed to read manifest: %w", err)
		}

		// Record rewrites nothing, so it keeps no parsed documents.
		if stage.Policy == images.PolicyRecord {
			found, err := images.FindReferences(content, opts.FieldSpecs)
			if err != nil {
				return nil, fmt.Errorf("failed to find images in %s: %w", filepath.Base(path), err)
			}
			refs = append(refs, found...)
			continue
		}

		manifest, err := images.ParseManifest(content, opts.FieldSpecs)
		if err != nil {
			return nil, fmt.Errorf("failed to find images in %s: %w", filepath.Base(path), err)
		}
		manifests = append(manifests, manifest)
		refs = append(refs, manifest.References()...)
	}

	found, rewrites, err := stage.Run(ctx, refs)
	if err != nil {
		return nil, err
	}

	log.FromContext(ctx).Info("Processed images", "policy", stage.Policy, "count", len(found))

	for i, manifest := range manifests {
		if !manifest.Rewrite(rewrites) {
			continue
		}
		content, err := manifest.Encode()
		if err != nil {
			return nil, err
		}
		if err := afero.WriteFile(p.store.fs, paths[i], content, 0600); err != nil {
			return nil, fmt.Errorf("failed to write manifest: %w", err)
		}
	}

	return found, nil
}

// processManifestFile applies patches and edits to a single file in place.
func (p *Pipeline) processManifestFile(ctx context.Context, path string, patches, edits []PatchSpec) error {
	content, err := afero.ReadFile(p.store.fs, path)
	if err != nil {
		return fmt.Errorf("failed to read manifest: %w", err)
	}

	processed, err := p.processManifest(ctx, content, patches, edits)
	if err != nil {
		return fmt.Errorf("failed to process %s: %w", filepath.Base(path), err)
	}

	if err := afero.WriteFile(p.store.fs, path, processed, 0600); err != nil {
		return fmt.Errorf("failed to write manifest: %w", err)
	}

	return nil
}

// processManifest normalizes content and applies patches, then edits on top,
// one document at a time.
func (p *Pipeline) processManifest(ctx context.Context, content []byte, patches, edits []PatchSpec) ([]byte, error) {
	processed, err := renderer.Process(ctx, content, toRendererPatches(patches))
	if err != nil {
		return nil, err
	}

	if len(edits) == 0 {
		return processed, nil
	}

	processed, err = renderer.Process(ctx, processed, toRendererPatches(edits))
	if err != nil {
		return nil, fmt.Errorf("failed to apply edits: %w", err)
	}

	return processed, nil
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
