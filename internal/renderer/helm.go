package renderer

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/release"
	v1release "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage"
	"helm.sh/helm/v4/pkg/storage/driver"
)

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
