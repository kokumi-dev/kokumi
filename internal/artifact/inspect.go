package artifact

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/afero"

	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/renderer"
)

// InspectResult describes a pulled artifact without rendering or pushing it.
type InspectResult struct {
	// MediaType is the artifact's first layer media type.
	MediaType string
	// Digest is the artifact's manifest digest.
	Digest string
	// Annotations are the artifact's OCI manifest annotations.
	Annotations map[string]string
	// ChartInfo is set when the artifact is a Helm chart.
	ChartInfo *renderer.ChartInfo
	// Files are the artifact's .yaml/.yml/.json files (recursive, sorted).
	Files []File
	// Manifest is the files joined with "---" separators.
	Manifest string
	// IsHelm reports whether the artifact is a Helm chart.
	IsHelm bool
}

// Inspect pulls the artifact identified by the OCI ref (which may carry a tag
// or digest) and returns its classification, files, and chart metadata.
// client may be nil to use the store's default client.
func (s *Store) Inspect(ctx context.Context, refURL string, client oci.Client) (*InspectResult, error) {
	c := cmp.Or(client, s.client)

	ref, err := oci.Parse(refURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ref: %w", err)
	}

	pulled, err := s.pull(ctx, c, ref, layoutRaw, "inspect-*")
	if err != nil {
		return nil, fmt.Errorf("failed to pull artifact: %w", err)
	}
	defer s.cleanup(pulled.dir)

	result := &InspectResult{
		MediaType:   pulled.mediaType,
		Digest:      pulled.digest,
		Annotations: pulled.annotations,
	}

	if pulled.mediaType == oci.HelmChartLayerMediaType {
		result.IsHelm = true

		chartTgz, err := afero.ReadFile(s.fs, filepath.Join(pulled.dir, "chart.tgz"))
		if err != nil {
			return nil, fmt.Errorf("failed to read Helm chart: %w", err)
		}

		info, err := renderer.InspectChart(chartTgz)
		if err != nil {
			return nil, fmt.Errorf("failed to inspect Helm chart: %w", err)
		}
		result.ChartInfo = info

		return result, nil
	}

	files, err := listFiles(s.fs, pulled.dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read artifact files: %w", err)
	}

	result.Files = files
	result.Manifest = joinFiles(files)

	return result, nil
}
