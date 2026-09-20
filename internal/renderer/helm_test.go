package renderer_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/kokumi-dev/kokumi/internal/renderer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "update expected files")

// packChartDir tars+gzips the unpacked chart directory into a .tgz archive,
// matching the shape the artifact pipeline hands to RenderChart.
func packChartDir(t *testing.T, srcRoot string) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	require.NoError(t, filepath.Walk(srcRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()

		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = "sample-chart/" + filepath.ToSlash(rel)

		require.NoError(t, tw.WriteHeader(hdr))
		_, err = io.Copy(tw, file)
		return err
	}))
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())

	return buf.Bytes()
}

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
			chartTgz := packChartDir(t, tt.path)

			renderedManifest, err := renderer.RenderChart(
				t.Context(),
				chartTgz,
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
