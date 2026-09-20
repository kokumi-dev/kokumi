package artifact

import (
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeYAMLFiles(t *testing.T) {
	tests := []struct {
		name         string
		setup        map[string]string
		wantManifest string
		wantGone     []string
	}{
		{
			name:         "no-op when only manifest.yaml exists",
			setup:        map[string]string{"manifest.yaml": "---\nkind: Pod\n"},
			wantManifest: "---\nkind: Pod\n",
		},
		{
			name:  "no-op when directory has no yaml files",
			setup: map[string]string{"chart.tgz": "binary"},
		},
		{
			name: "multiple yaml files are merged in sorted order",
			setup: map[string]string{
				testServiceFile:    "kind: Service\n",
				testDeploymentFile: "kind: Deployment\n",
			},
			wantManifest: "---\n# Source: deployment.yaml\nkind: Deployment\n---\n# Source: service.yaml\nkind: Service\n",
			wantGone:     []string{testDeploymentFile, testServiceFile},
		},
		{
			name: "existing manifest.yaml included and removed before rewrite",
			setup: map[string]string{
				"manifest.yaml": "kind: ConfigMap\n",
				testServiceFile: "kind: Service\n",
			},
			wantManifest: "---\n# Source: manifest.yaml\nkind: ConfigMap\n---\n# Source: service.yaml\nkind: Service\n",
			wantGone:     []string{testServiceFile},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			for name, content := range tc.setup {
				_ = afero.WriteFile(fs, filepath.Join("/dir", name), []byte(content), 0600)
			}

			require.NoError(t, mergeYAMLFiles(fs, "/dir"))

			if tc.wantManifest == "" {
				exists, _ := afero.Exists(fs, "/dir/manifest.yaml")
				assert.False(t, exists)
			} else {
				data, err := afero.ReadFile(fs, "/dir/manifest.yaml")
				require.NoError(t, err)
				assert.Equal(t, tc.wantManifest, string(data))
			}

			for _, name := range tc.wantGone {
				exists, _ := afero.Exists(fs, filepath.Join("/dir", name))
				assert.False(t, exists, "%s should be removed after merge", name)
			}
		})
	}
}

func TestMergeYAMLFiles_KustomizationSkipped(t *testing.T) {
	fs := afero.NewMemMapFs()
	_ = afero.WriteFile(fs, "/dir/kustomization.yaml", []byte("resources:\n- deployment.yaml\n"), 0600)
	_ = afero.WriteFile(fs, "/dir/deployment.yaml", []byte("kind: Deployment\n"), 0600)

	require.NoError(t, mergeYAMLFiles(fs, "/dir"))

	data, err := afero.ReadFile(fs, "/dir/manifest.yaml")
	require.NoError(t, err)
	assert.NotContains(t, string(data), "resources:")
	assert.Contains(t, string(data), "kind: Deployment")

	exists, _ := afero.Exists(fs, "/dir/kustomization.yaml")
	assert.True(t, exists, "kustomization.yaml must survive the merge")
}
