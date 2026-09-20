package artifact

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kokumi-dev/kokumi/internal/oci"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	testSCMRepo = "https://github.com/org/repo"
	testSCMTag  = "1.2.3"
)

func TestBuildAnnotations(t *testing.T) {
	t.Run("empty inputs produce empty map", func(t *testing.T) {
		annotations := buildAnnotations(AnnotationInputs{})
		assert.Empty(t, annotations)
	})

	t.Run("description and parent digest stamped", func(t *testing.T) {
		annotations := buildAnnotations(AnnotationInputs{
			Description:  "Initial commit",
			ParentDigest: "sha256:parent",
		})

		assert.Equal(t, "Initial commit", annotations[ocispec.AnnotationDescription])
		assert.Equal(t, "sha256:parent", annotations[oci.AnnotationParentDigest])
	})

	t.Run("empty parent digest omitted", func(t *testing.T) {
		annotations := buildAnnotations(AnnotationInputs{Description: "x"})
		_, has := annotations[oci.AnnotationParentDigest]
		assert.False(t, has)
	})

	t.Run("SCM provenance mapped to standard annotations", func(t *testing.T) {
		annotations := buildAnnotations(AnnotationInputs{
			SCM: SCMInfo{Repo: testSCMRepo, Tag: testSCMTag, CommitHash: "abcdef"},
		})

		assert.Equal(t, testSCMRepo, annotations[ocispec.AnnotationSource])
		assert.Equal(t, testSCMTag, annotations[ocispec.AnnotationVersion])
		assert.Equal(t, "abcdef", annotations[ocispec.AnnotationRevision])
	})

	t.Run("partial SCM only sets present fields", func(t *testing.T) {
		annotations := buildAnnotations(AnnotationInputs{
			SCM: SCMInfo{Repo: testSCMRepo},
		})

		assert.Equal(t, testSCMRepo, annotations[ocispec.AnnotationSource])
		_, hasVersion := annotations[ocispec.AnnotationVersion]
		assert.False(t, hasVersion)
		_, hasRevision := annotations[ocispec.AnnotationRevision]
		assert.False(t, hasRevision)
	})

	t.Run("source ref stamps base image identity", func(t *testing.T) {
		ref, err := oci.Parse("oci://registry.example.com/charts/app")
		assert.NoError(t, err)
		ref.Tag = "1.0.0"
		ref.Digest = "sha256:base"

		annotations := buildAnnotations(AnnotationInputs{SourceRef: ref})

		assert.Equal(t, "registry.example.com/charts/app", annotations[ocispec.AnnotationBaseImageName])
		assert.Equal(t, "sha256:base", annotations[ocispec.AnnotationBaseImageDigest])
	})

	t.Run("extra annotations override computed ones", func(t *testing.T) {
		ref, err := oci.Parse("oci://registry.example.com/charts/app")
		assert.NoError(t, err)
		ref.Tag = "1.0.0"
		ref.Digest = "sha256:base"

		annotations := buildAnnotations(AnnotationInputs{
			Description: "computed",
			SourceRef:   ref,
			Extra: map[string]string{
				ocispec.AnnotationDescription:     "overridden",
				ocispec.AnnotationBaseImageDigest: "sha256:hijacked",
				AnnotationPrerendered:             "true",
			},
		})

		assert.Equal(t, "overridden", annotations[ocispec.AnnotationDescription])
		assert.Equal(t, "sha256:hijacked", annotations[ocispec.AnnotationBaseImageDigest])
		assert.Equal(t, "true", annotations[AnnotationPrerendered])
	})
}

func TestSCMInfoFromAnnotations(t *testing.T) {
	t.Run("full provenance", func(t *testing.T) {
		scm := scmInfoFromAnnotations(map[string]string{
			ocispec.AnnotationSource:   testSCMRepo,
			ocispec.AnnotationVersion:  testSCMTag,
			ocispec.AnnotationRevision: "abcdef@sha256:deadbeef",
		})

		assert.Equal(t, SCMInfo{Repo: testSCMRepo, Tag: testSCMTag, CommitHash: "abcdef"}, scm)
	})

	t.Run("no annotations yields empty SCM", func(t *testing.T) {
		assert.Equal(t, SCMInfo{}, scmInfoFromAnnotations(nil))
	})
}
