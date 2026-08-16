package scmlink

import (
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Resolve extracts SCM provenance from the OCI annotation map:
//   - repo:       org.opencontainers.image.source, else org.opencontainers.image.url
//   - tag:        org.opencontainers.image.version
//   - commitHash: org.opencontainers.image.revision (a trailing "@sha265:<hash>"
//     suffix is stripped)
func Resolve(annotations map[string]string) (repo, tag, commitHash string) {
	if annotations == nil {
		return "", "", ""
	}
	repo = strings.TrimSpace(annotations[ocispec.AnnotationSource])
	if repo == "" {
		repo = strings.TrimSpace(annotations[ocispec.AnnotationURL])
	}
	tag = strings.TrimSpace(annotations[ocispec.AnnotationVersion])
	commitHash = strings.TrimSpace(annotations[ocispec.AnnotationRevision])
	commitHash = strings.SplitN(commitHash, "@", 2)[0]
	return repo, tag, commitHash
}
