package scmlink

import (
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		wantRepo    string
		wantTag     string
		wantHash    string
	}{
		{
			name: "version only, no revision",
			annotations: map[string]string{
				ocispec.AnnotationSource:  testRepo,
				ocispec.AnnotationVersion: testTag,
			},
			wantRepo: testRepo,
			wantTag:  testTag,
			wantHash: "",
		},
		{
			name: "source absent, falls back to url",
			annotations: map[string]string{
				ocispec.AnnotationURL:     testRepo,
				ocispec.AnnotationVersion: testTag,
			},
			wantRepo: testRepo,
			wantTag:  testTag,
			wantHash: "",
		},
		{
			name: "both tag and revision present",
			annotations: map[string]string{
				ocispec.AnnotationSource:   testRepo,
				ocispec.AnnotationVersion:  testTag,
				ocispec.AnnotationRevision: testCommit,
			},
			wantRepo: testRepo,
			wantTag:  testTag,
			wantHash: testCommit,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo, tag, hash := Resolve(tc.annotations)
			assert.Equal(t, tc.wantRepo, repo)
			assert.Equal(t, tc.wantTag, tag)
			assert.Equal(t, tc.wantHash, hash)
		})
	}
}
