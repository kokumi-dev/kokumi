package artifact

// Source describes where an artifact is pulled from.
type Source struct {
	// OCI is the fully-qualified OCI URL (oci://host/repo) of the artifact.
	OCI string
	// Version is the tag to resolve the source to.
	Version string
}

// Destination describes where a rendered artifact is pushed. The pushed tag
// is always the source version.
type Destination struct {
	// OCI is the fully-qualified OCI URL (oci://host/repo) to push to.
	OCI string
}

// Layout controls how a multi-file raw manifest artifact is stored in the
// rendered output artifact.
type Layout string

const (
	// LayoutSingle merges all YAML files into a single manifest.yaml.
	LayoutSingle Layout = "Single"
	// LayoutMulti keeps the artifact's individual files as they are.
	LayoutMulti Layout = "Multi"
)

// HelmSpec defines Helm rendering options for a chart artifact.
type HelmSpec struct {
	// ReleaseName defaults to Name (of the owning resource) when empty.
	ReleaseName string
	// Namespace defaults to Namespace (of the owning resource) when empty.
	Namespace string
	// IncludeCRDs controls whether CRDs are included in the rendered output.
	IncludeCRDs bool
	// Values holds inline Helm values, merged last (highest priority).
	Values map[string]any
}

// RenderSpec defines optional rendering applied to a source artifact.
// When nil, the source is treated as a pre-rendered manifest bundle.
// Helm and Manifest are mutually exclusive.
type RenderSpec struct {
	// Helm renders the source artifact as a Helm chart.
	Helm *HelmSpec
	// Manifest configures rendering of a raw manifest bundle source.
	Manifest *ManifestSpec
}

// ManifestSpec defines rendering options for raw manifest bundle sources.
type ManifestSpec struct {
	// Layout controls whether the artifact's YAML files are merged into a
	// single manifest.yaml (Single) or kept as separate files (Multi).
	// Artifacts containing a kustomization file are never merged.
	Layout Layout
}

// EffectiveLayout returns the configured layout, defaulting to LayoutSingle.
func (r *RenderSpec) EffectiveLayout() Layout {
	if r != nil && r.Manifest != nil && r.Manifest.Layout != "" {
		return r.Manifest.Layout
	}
	return LayoutSingle
}

// PatchSpec defines a modification to apply to a rendered resource.
// It mirrors renderer.Patch; kept as a local value type so callers do not
// need to import the renderer package.
type PatchSpec struct {
	// Target identifies which resource to patch.
	Target PatchTargetSpec
	// Set contains JSONPath expressions and their values to set.
	Set map[string]string
}

// PatchTargetSpec identifies which resource to patch.
type PatchTargetSpec struct {
	Kind      string
	Name      string
	Namespace string
}

// SCMInfo carries Git provenance extracted from an artifact's OCI annotations.
type SCMInfo struct {
	Repo       string
	Tag        string
	CommitHash string
}

// File is a single file of a rendered or pulled artifact, with its path
// relative to the artifact root.
type File struct {
	Path    string
	Content string
}
