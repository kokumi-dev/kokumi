package service

import (
	"context"
	"fmt"
	"path/filepath"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/credential"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/renderer"
	"github.com/kokumi-dev/kokumi/internal/scmlink"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/spf13/afero"
)

// MenuService handles the OCI operations for a Menu.
type MenuService struct {
	oci oci.Client
	fs  afero.Fs
}

// NewMenuService returns a new MenuService.
func NewMenuService(client oci.Client) *MenuService {
	return &MenuService{oci: client, fs: afero.NewOsFs()}
}

// NewMenuServiceWithFS returns a new MenuService using the given filesystem
// (used by tests to inject an in-memory FS).
func NewMenuServiceWithFS(client oci.Client, fs afero.Fs) *MenuService {
	return &MenuService{oci: client, fs: fs}
}

// ResolveSource resolves the Menu's consumable source: without spec.vendor the
// upstream source is resolved (digest best-effort) and advertised as-is; with
// spec.vendor the artifact is either rendered+patched (mode Render, default)
// or copied raw (mode Copy) to the destination registry, and the published ref
// is advertised.
func (s *MenuService) ResolveSource(ctx context.Context, menu *deliveryv1alpha1.Menu, resolver credential.PantryResolver) (*deliveryv1alpha1.MenuSourceStatus, error) {
	if menu.Spec.Vendor == nil {
		return s.resolveUpstream(ctx, menu, resolver)
	}
	if effectiveVendorMode(menu) == deliveryv1alpha1.VendorModeRender {
		return s.publishRendered(ctx, menu, resolver)
	}
	return s.vendor(ctx, menu, resolver)
}

// effectiveVendorMode returns the Menu's vendor mode, defaulting to Render.
func effectiveVendorMode(menu *deliveryv1alpha1.Menu) deliveryv1alpha1.VendorMode {
	if menu.Spec.Vendor == nil || menu.Spec.Vendor.Mode == "" {
		return deliveryv1alpha1.VendorModeRender
	}
	return menu.Spec.Vendor.Mode
}

// resolveUpstream advertises the upstream source without vendoring.
func (s *MenuService) resolveUpstream(ctx context.Context, menu *deliveryv1alpha1.Menu, resolver credential.PantryResolver) (*deliveryv1alpha1.MenuSourceStatus, error) {
	resolved, client, err := resolver.ResolveSource(ctx, menu.Spec.Source, menu.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve source Pantry: %w", err)
	}

	srcRef, err := oci.Parse(resolved.OCI)
	if err != nil {
		return nil, err
	}
	srcRef.Tag = menu.Spec.Source.Version

	digest := ""
	if client != nil {
		if d, err := client.Resolve(ctx, srcRef); err != nil {
			digest = ""
		} else {
			digest = d
		}
	} else if s.oci != nil {
		if d, err := s.oci.Resolve(ctx, srcRef); err != nil {
			digest = ""
		} else {
			digest = d
		}
	}

	return &deliveryv1alpha1.MenuSourceStatus{
		OCI:       resolved.OCI,
		Version:   menu.Spec.Source.Version,
		PantryRef: menu.Spec.Source.PantryRef,
		Digest:    digest,
	}, nil
}

// publishRendered pulls the source artifact, renders it per spec.render
// (Helm chart or raw manifest bundle), applies spec.patches to the rendered
// manifests, pushes the result to the destination registry, and advertises
// the published ref with preRendered set.
func (s *MenuService) publishRendered(ctx context.Context, menu *deliveryv1alpha1.Menu, resolver credential.PantryResolver) (*deliveryv1alpha1.MenuSourceStatus, error) {
	if menu.Spec.Render == nil {
		return nil, fmt.Errorf("vendor mode Render requires spec.render to be set")
	}

	resolved, srcClient, err := resolver.ResolveSource(ctx, menu.Spec.Source, menu.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve source Pantry: %w", err)
	}

	destURL, destClient, err := s.resolveDestination(ctx, menu, resolver)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve vendor destination: %w", err)
	}

	srcRef, err := oci.Parse(resolved.OCI)
	if err != nil {
		return nil, err
	}
	srcRef.Tag = menu.Spec.Source.Version

	dstRef, err := oci.Parse(destURL)
	if err != nil {
		return nil, err
	}
	dstRef.Tag = menu.Spec.Source.Version

	source := s.oci
	if source == nil {
		source = srcClient
	}
	if source == nil {
		source = oci.NewORASClient()
	}

	tempDir, err := afero.TempDir(s.fs, "", "menu-render-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer s.fs.RemoveAll(tempDir) //nolint:errcheck

	mediaType, sourceDigest, annotations, err := source.Pull(ctx, srcRef, tempDir)
	if err != nil {
		return nil, fmt.Errorf("failed to pull source: %w", err)
	}

	if err := s.renderToDir(ctx, menu, tempDir, mediaType); err != nil {
		return nil, err
	}

	// Remove the pulled chart blob so only rendered manifests are published.
	_ = s.fs.Remove(filepath.Join(tempDir, "chart.tgz")) //nolint:errcheck

	if destClient == nil {
		destClient = source
	}

	ociAnnotations := map[string]string{
		ocispec.AnnotationBaseImageName:   srcRef.RepositoryReference(),
		ocispec.AnnotationBaseImageDigest: sourceDigest,
		"kokumi.dev/prerendered":          "true",
		ocispec.AnnotationDescription:     fmt.Sprintf("Rendered and patched by Menu %s", menu.Name),
	}
	if repo, tag, _ := scmlink.Resolve(annotations); repo != "" {
		ociAnnotations[ocispec.AnnotationSource] = repo
		if tag != "" {
			ociAnnotations[ocispec.AnnotationVersion] = tag
		}
	}

	digest, err := destClient.Push(ctx, dstRef, tempDir, ociAnnotations)
	if err != nil {
		return nil, fmt.Errorf("failed to push rendered artifact: %w", err)
	}

	return &deliveryv1alpha1.MenuSourceStatus{
		OCI:         destURL,
		Version:     menu.Spec.Source.Version,
		PantryRef:   menu.Spec.Vendor.Destination.PantryRef,
		Digest:      digest,
		PreRendered: true,
	}, nil
}

// renderToDir renders the pulled source in dir per the Menu's render config:
// Helm charts are rendered into manifest.yaml; raw manifest bundles keep their
// layout. The Menu's spec.patches are applied to the manifest file(s).
func (s *MenuService) renderToDir(ctx context.Context, menu *deliveryv1alpha1.Menu, dir string, mediaType string) error {
	render := menu.Spec.Render
	manifestPath := filepath.Join(dir, "manifest.yaml")

	if render.Helm != nil {
		if mediaType != oci.HelmChartLayerMediaType {
			return fmt.Errorf("source is not a Helm chart (got media type %q)", mediaType)
		}

		vals, err := jsonToMap(render.Helm.Values)
		if err != nil {
			return fmt.Errorf("failed to convert values: %w", err)
		}

		releaseName := render.Helm.ReleaseName
		if releaseName == "" {
			releaseName = menu.Name
		}
		helmNamespace := render.Helm.Namespace
		if helmNamespace == "" {
			helmNamespace = menu.Namespace
		}

		chartPath := filepath.Join(dir, "chart.tgz")

		manifest, err := renderer.RenderChart(ctx, chartPath, releaseName, helmNamespace, render.Helm.IncludeCRDs, vals)
		if err != nil {
			return fmt.Errorf("failed to render Helm chart: %w", err)
		}

		if err := afero.WriteFile(s.fs, manifestPath, []byte(manifest), 0600); err != nil {
			return fmt.Errorf("failed to write manifest: %w", err)
		}
	}

	// Apply the Menu's patches to the manifest file(s).
	if _, err := s.fs.Stat(manifestPath); err == nil {
		return s.applyMenuPatches(ctx, manifestPath, menu.Spec.Patches)
	}

	files, err := yamlFiles(s.fs, dir)
	if err != nil {
		return err
	}
	for _, file := range files {
		if err := s.applyMenuPatches(ctx, file, menu.Spec.Patches); err != nil {
			return err
		}
	}

	return nil
}

// applyMenuPatches applies the Menu's patches to a single YAML file in place.
func (s *MenuService) applyMenuPatches(ctx context.Context, path string, patches []deliveryv1alpha1.Patch) error {
	content, err := afero.ReadFile(s.fs, path)
	if err != nil {
		return fmt.Errorf("failed to read manifest: %w", err)
	}

	processed, err := renderer.ApplyPatches(ctx, content, patches)
	if err != nil {
		return fmt.Errorf("failed to apply menu patches: %w", err)
	}

	if err := afero.WriteFile(s.fs, path, processed, 0600); err != nil {
		return fmt.Errorf("failed to write manifest: %w", err)
	}

	return nil
}

// vendor copies the source artifact to the destination registry and advertises
// the vendored ref.
func (s *MenuService) vendor(ctx context.Context, menu *deliveryv1alpha1.Menu, resolver credential.PantryResolver) (*deliveryv1alpha1.MenuSourceStatus, error) {
	resolved, srcClient, err := resolver.ResolveSource(ctx, menu.Spec.Source, menu.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve source Pantry: %w", err)
	}

	destURL, destClient, err := s.resolveDestination(ctx, menu, resolver)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve vendor destination: %w", err)
	}

	srcRef, err := oci.Parse(resolved.OCI)
	if err != nil {
		return nil, err
	}
	srcRef.Tag = menu.Spec.Source.Version

	dstRef, err := oci.Parse(destURL)
	if err != nil {
		return nil, err
	}
	dstRef.Tag = menu.Spec.Source.Version

	source := s.oci
	if source == nil {
		source = srcClient
	}
	if source == nil {
		source = oci.NewORASClient()
	}

	if destClient == nil {
		destClient = source
	}

	if err := source.Copy(ctx, destClient, srcRef, dstRef); err != nil {
		return nil, fmt.Errorf("failed to vendor source: %w", err)
	}

	digest, err := destClient.Resolve(ctx, dstRef)
	if err != nil {
		digest, err = source.Resolve(ctx, dstRef)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve vendored digest: %w", err)
		}
	}

	return &deliveryv1alpha1.MenuSourceStatus{
		OCI:       destURL,
		Version:   menu.Spec.Source.Version,
		PantryRef: menu.Spec.Vendor.Destination.PantryRef,
		Digest:    digest,
	}, nil
}

// resolveDestination resolves a VendorDestination into a URL and client.
// When neither oci nor pantryRef is set the in-cluster default registry is
// used (oci://<host>/<namespace>/<menu-name>). For oci destinations the client
// is nil — the caller should fall back to its own client (which may be a test
// fake).
func (s *MenuService) resolveDestination(ctx context.Context, menu *deliveryv1alpha1.Menu, resolver credential.PantryResolver) (string, oci.Client, error) {
	if menu.Spec.Vendor.Destination.PantryRef != nil {
		resolved, c, err := resolver.ResolveSource(ctx, deliveryv1alpha1.OCISource{
			PantryRef: menu.Spec.Vendor.Destination.PantryRef,
		}, menu.Namespace)
		if err != nil {
			return "", nil, err
		}
		return resolved.OCI, c, nil
	}

	if menu.Spec.Vendor.Destination.OCI != "" {
		return menu.Spec.Vendor.Destination.OCI, nil, nil
	}

	// In-cluster default, following the Order pattern: registry/namespace/name.
	return fmt.Sprintf("oci://%s/%s/%s", DefaultRegistryHost, menu.Namespace, menu.Name), nil, nil
}
