package renderer_test

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/kokumi-dev/kokumi/internal/renderer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "update expected files")

func Test_RenderChart(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		releaseName string
		namespace   string
		includeCRDs bool
	}{
		{
			name:        "Sample Chart",
			path:        "testdata/sample-chart",
			releaseName: "sample",
			namespace:   "sample",
			includeCRDs: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			renderedManifest, err := renderer.RenderChart(
				t.Context(),
				tt.path,
				tt.releaseName,
				tt.namespace,
				tt.includeCRDs,
				nil,
			)
			require.NoError(t, err)

			expectedFile := filepath.Join("testdata", "expected", t.Name()+".yaml")

			if *update {
				require.NoError(t, os.MkdirAll(filepath.Dir(expectedFile), 0755))
				require.NoError(t, os.WriteFile(expectedFile, []byte(renderedManifest), 0600))
				return
			}

			expected, err := os.ReadFile(expectedFile)
			require.NoError(t, err, "expected file missing — run with -update to create it")

			assert.Equal(t, string(expected), renderedManifest)
		})
	}
}
