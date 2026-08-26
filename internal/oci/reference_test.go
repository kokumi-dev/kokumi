package oci

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	digest64 = "fdf90e00e7605d65cdf4a5d3a404c9823ee2e473f7468f68c29694f1b909e2bc"
	repoName = "app"
	registry = "registry.example"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantReg    string
		wantRepo   string
		wantTag    string
		wantDigest string
		wantErr    bool
	}{
		{
			name:     "bare registry/repository",
			input:    registry + ":5000/my-org/my-app",
			wantReg:  registry + ":5000",
			wantRepo: "my-org/my-app",
		},
		{
			name:     "oci scheme prefix",
			input:    "oci://ghcr.io/my-org/charts/app",
			wantReg:  "ghcr.io",
			wantRepo: "my-org/charts/app",
		},
		{
			name:     "with tag",
			input:    registry + "/" + repoName + ":1.2.3",
			wantReg:  registry,
			wantRepo: repoName,
			wantTag:  "1.2.3",
		},
		{
			name:       "with digest",
			input:      registry + "/" + repoName + "@sha256:" + digest64,
			wantReg:    registry,
			wantRepo:   repoName,
			wantDigest: "sha256:" + digest64,
		},
		{
			name:       "with tag and digest",
			input:      registry + "/" + repoName + ":1.0@sha256:" + digest64,
			wantReg:    registry,
			wantRepo:   repoName,
			wantTag:    "1.0",
			wantDigest: "sha256:" + digest64,
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: true,
		},
		{
			name:    "missing repository",
			input:   registry,
			wantErr: true,
		},
		{
			name:    "unsupported digest algorithm",
			input:   registry + "/" + repoName + "@sha512:" + digest64,
			wantErr: true,
		},
		{
			name:    "malformed digest",
			input:   registry + "/" + repoName + "@notadigest",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := Parse(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantReg, ref.Registry)
			assert.Equal(t, tt.wantRepo, ref.Repository)
			assert.Equal(t, tt.wantTag, ref.Tag)
			assert.Equal(t, tt.wantDigest, ref.Digest)
		})
	}
}

func TestReference_StringForms(t *testing.T) {
	digestRef := mustParse(t, registry+"/"+repoName+"@sha256:"+digest64)
	taggedRef := mustParse(t, "oci://"+registry+":5000/org/app:1.0")
	bareRef := mustParse(t, registry+"/"+repoName)

	tests := []struct {
		name string
		ref  Reference
	}{
		{name: "digest ref", ref: digestRef},
		{name: "tagged ref", ref: taggedRef},
		{name: "bare ref", ref: bareRef},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// OCIString round-trips through Parse.
			reparsed, err := Parse(tt.ref.OCIString())
			require.NoError(t, err)
			assert.Equal(t, tt.ref.String(), reparsed.String())

			// OCIRepositoryReference round-trips through Parse.
			repoParsed, err := Parse(tt.ref.OCIRepositoryReference())
			require.NoError(t, err)
			assert.Equal(t, tt.ref.RepositoryReference(), repoParsed.RepositoryReference())
			assert.Equal(t, tt.ref.Registry, repoParsed.Registry)
			assert.Equal(t, tt.ref.Repository, repoParsed.Repository)

			// RepositoryReference has no oci:// prefix or tag/digest.
			assert.NotContains(t, tt.ref.RepositoryReference(), ociPrefix)
			assert.NotContains(t, tt.ref.RepositoryReference(), "@")
		})
	}
}

func TestReference_ShortDigest(t *testing.T) {
	full := mustParse(t, registry+"/"+repoName+"@sha256:"+digest64)
	assert.Len(t, full.ShortDigest(), 12)
	assert.Equal(t, digest64[:12], full.ShortDigest())

	noDigest := mustParse(t, registry+"/"+repoName+":1.0")
	assert.Empty(t, noDigest.ShortDigest(), "ShortDigest should be empty without a digest")

	short := Reference{
		Digest: "sha256:abc",
	}
	assert.Empty(t, short.ShortDigest(), "ShortDigest should be empty for truncated digests")
}

func mustParse(t *testing.T, s string) Reference {
	t.Helper()
	ref, err := Parse(s)
	require.NoError(t, err)
	return ref
}
