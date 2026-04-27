package renderer

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/release"
	v1release "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage"
	"helm.sh/helm/v4/pkg/storage/driver"
)

// ChartInfo holds metadata extracted from a Helm chart tarball.
type ChartInfo struct {
	// Name is the chart name from Chart.yaml.
	Name string
	// Description is the chart description from Chart.yaml.
	Description string
	// ChartVersion is the chart version from Chart.yaml.
	ChartVersion string
	// DefaultValues is the serialised YAML of the chart's default values.
	DefaultValues string
	// Readme is the contents of README.md, empty when the chart has none.
	Readme string
	// HasSchema reports whether the chart ships a values JSON schema.
	HasSchema bool
}

// InspectChart loads the Helm chart tarball at chartPath and returns its
// metadata, default values, optional README, and whether a JSON schema is present.
func InspectChart(chartPath string) (*ChartInfo, error) {
	chrt, err := loader.Load(chartPath)
	if err != nil {
		return nil, fmt.Errorf("load chart: %w", err)
	}

	defaultValuesBytes, err := yaml.Marshal(chrt.Values)
	if err != nil {
		return nil, fmt.Errorf("marshal default values: %w", err)
	}

	var readme string
	for _, f := range chrt.Files {
		if strings.EqualFold(f.Name, "README.md") {
			readme = string(f.Data)
			break
		}
	}

	return &ChartInfo{
		Name:          chrt.Metadata.Name,
		Description:   chrt.Metadata.Description,
		ChartVersion:  chrt.Metadata.Version,
		DefaultValues: string(defaultValuesBytes),
		Readme:        readme,
		HasSchema:     len(chrt.Schema) > 0,
	}, nil
}

// RenderChart renders a Helm chart from a local chart tarball and returns the rendered manifest.
// chartPath must point to a .tgz file previously fetched from the OCI registry.
func RenderChart(ctx context.Context, chartPath, releaseName, namespace string, includeCRDs bool, vals map[string]any) (string, error) {
	var renderedManifest strings.Builder

	cfg := action.NewConfiguration()
	cfg.Releases = storage.Init(driver.NewMemory())

	client := action.NewInstall(cfg)
	client.DryRunStrategy = action.DryRunClient
	client.ReleaseName = releaseName
	client.Namespace = namespace
	client.Replace = true
	client.IncludeCRDs = includeCRDs

	chrt, err := loader.Load(chartPath)
	if err != nil {
		return "", fmt.Errorf("load chart: %w", err)
	}

	rel, err := client.RunWithContext(ctx, chrt, vals)
	if err != nil {
		return "", fmt.Errorf("render: %w", err)
	}

	acc, err := release.NewAccessor(rel)
	if err != nil {
		return "", fmt.Errorf("accessor: %w", err)
	}

	if strings.TrimSpace(acc.Manifest()) != "" {
		renderedManifest.WriteString(strings.TrimSpace(acc.Manifest()))
		renderedManifest.WriteString("\n")
	}

	for _, hook := range acc.Hooks() {
		if releaseHook, ok := hook.(*v1release.Hook); ok && slices.Contains(releaseHook.Events, v1release.HookTest) {
			continue
		}

		hookAcc, err := release.NewHookAccessor(hook)
		if err != nil {
			return "", fmt.Errorf("access hook: %w", err)
		}

		renderedManifest.WriteString("\n---\n")
		renderedManifest.WriteString(fmt.Sprintf("# Source: %s\n", hookAcc.Path()))
		renderedManifest.WriteString(strings.TrimSpace(hookAcc.Manifest()))
		renderedManifest.WriteString("\n")
	}

	return renderedManifest.String(), nil
}
