package artifact

import (
	"maps"

	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/scmlink"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// AnnotationPrerendered marks artifacts that were rendered (and patched) by a
// Menu before being published to the destination registry.
const AnnotationPrerendered = "kokumi.dev/prerendered"

// AnnotationInputs collects everything that feeds the annotation set of a
// pushed artifact.
type AnnotationInputs struct {
	// Description is attached as org.opencontainers.image.description.
	Description string
	// ParentDigest links to the preceding artifact in a promotion chain via
	// the kokumi.dev/parent annotation.
	ParentDigest string
	// SourceRef supplies the base image name and digest annotations.
	SourceRef oci.Reference
	// SCM carries Git provenance (source repo, version, revision).
	SCM SCMInfo
	// Extra annotations are merged last and may override computed ones.
	Extra map[string]string
}

// buildAnnotations assembles the annotation set for a pushed artifact,
// merging the diverged Menu and Order propagation into one builder.
func buildAnnotations(in AnnotationInputs) map[string]string {
	annotations := map[string]string{}

	if in.Description != "" {
		annotations[ocispec.AnnotationDescription] = in.Description
	}
	if in.ParentDigest != "" {
		annotations[oci.AnnotationParentDigest] = in.ParentDigest
	}

	if in.SCM.Repo != "" {
		annotations[ocispec.AnnotationSource] = in.SCM.Repo
	}
	if in.SCM.Tag != "" {
		annotations[ocispec.AnnotationVersion] = in.SCM.Tag
	}
	if in.SCM.CommitHash != "" {
		annotations[ocispec.AnnotationRevision] = in.SCM.CommitHash
	}

	if in.SourceRef.Repository != "" {
		annotations[ocispec.AnnotationBaseImageName] = in.SourceRef.RepositoryReference()
		annotations[ocispec.AnnotationBaseImageDigest] = in.SourceRef.Digest
	}

	maps.Copy(annotations, in.Extra)

	return annotations
}

// scmInfoFromAnnotations extracts Git provenance from an artifact's OCI
// annotations.
func scmInfoFromAnnotations(annotations map[string]string) SCMInfo {
	repo, tag, commitHash := scmlink.Resolve(annotations)
	return SCMInfo{Repo: repo, Tag: tag, CommitHash: commitHash}
}
