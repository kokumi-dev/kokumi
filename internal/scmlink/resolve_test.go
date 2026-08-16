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
				ocispec.AnnotationSource:  "https://github.com/kokumi-dev/example",
				ocispec.AnnotationVersion: "1.2.3",
			},
			wantRepo: "https://github.com/kokumi-dev/example",
			wantTag:  "1.2.3",
			wantHash: "",
		},
		{
			name: "source absent, falls back to url",
			annotations: map[string]string{
				ocispec.AnnotationURL:     "https://github.com/kokumi-dev/example",
				ocispec.AnnotationVersion: "1.2.3",
			},
			wantRepo: "https://github.com/kokumi-dev/example",
			wantTag:  "1.2.3",
			wantHash: "",
		},
		{
			name: "both tag and revision present",
			annotations: map[string]string{
				ocispec.AnnotationSource:   "https://github.com/kokumi-dev/example",
				ocispec.AnnotationVersion:  "1.2.3",
				ocispec.AnnotationRevision: "abcdef1234567890abcdef1234567890abcdef12",
			},
			wantRepo: "https://github.com/kokumi-dev/example",
			wantTag:  "1.2.3",
			wantHash: "abcdef1234567890abcdef1234567890abcdef12",
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
