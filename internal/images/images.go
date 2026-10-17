// Package images finds the container image references in rendered
// manifests, resolves them to digests, and pins or vendors them.
//
// Image fields follow kustomize's image transformer: the containers,
// initContainers and ephemeralContainers of any object at any depth, Pod
// image volumes, and additional field specs in kustomize's FieldSpec shape.
// References are normalized with github.com/distribution/reference, as the
// kubelet does.
package images

// Policy controls what Stage.Run does with the images it finds.
type Policy string

const (
	// PolicyRecord resolves and records every image and changes nothing.
	PolicyRecord Policy = "Record"
	// PolicyPin rewrites every reference to its digest, keeping the name and
	// tag as written.
	PolicyPin Policy = "Pin"
	// PolicyVendor copies every image into a repository of its own below the
	// destination (see vendoredRepository), tagged with the tag its reference
	// names, and rewrites the references to pull it from there by digest.
	PolicyVendor Policy = "Vendor"
)

// FieldSpec locates a field that holds an image reference, as a kustomize
// FieldSpec does. Path is slash-separated, with [] for list elements.
type FieldSpec struct {
	Group   string
	Version string
	Kind    string
	Path    string
}

// Mirror redirects lookups and copies of the images whose normalized name
// starts with Prefix to Location.
type Mirror struct {
	// Prefix is a normalized repository prefix, matched at path boundaries.
	Prefix string
	// Location is the registry and repository path that replaces Prefix,
	// without the oci:// scheme.
	Location string
}

// Image is a container image the manifests reference, and what became of it.
type Image struct {
	// Ref is the reference as written in the manifests.
	Ref string `json:"ref"`
	// Name is the normalized repository of Ref. Empty when Ref is not a
	// valid reference.
	Name string `json:"name,omitempty"`
	// Digest is the manifest digest Ref resolved to.
	Digest string `json:"digest,omitempty"`
	// MediaType is the media type of the manifest Digest names.
	MediaType string `json:"mediaType,omitempty"`
	// Vendored is the reference the manifests pull the image from after it
	// was copied into its repository below the destination.
	Vendored string `json:"vendored,omitempty"`
	// Error tells why Ref could not be resolved.
	Error string `json:"error,omitempty"`
}
