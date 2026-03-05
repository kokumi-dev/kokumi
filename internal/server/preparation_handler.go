package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/spf13/afero"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// handleListPreparations handles GET /api/v1/recipes/{namespace}/{name}/preparations.
// Returns all Preparations for the given Recipe, sorted newest-first by createdAt,
// with IsActive populated from the linked Serving.
func handleListPreparations(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		namespace := r.PathValue("namespace")
		recipeName := r.PathValue("name")

		prepList := &deliveryv1alpha1.PreparationList{}
		if err := deps.reader.List(r.Context(), prepList, client.InNamespace(namespace)); err != nil {
			deps.logger.Error(err, "Failed to list Preparations", "namespace", namespace, "recipe", recipeName)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to list preparations: %s", err))
			return
		}

		// Client-side filter by recipe name.
		filtered := prepList.Items[:0]
		for _, p := range prepList.Items {
			if p.Spec.Recipe == recipeName {
				filtered = append(filtered, p)
			}
		}
		prepList.Items = filtered

		servingList := &deliveryv1alpha1.ServingList{}
		if err := deps.reader.List(r.Context(), servingList, client.InNamespace(namespace)); err != nil {
			deps.logger.Error(err, "Failed to list Servings", "namespace", namespace)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to list servings: %s", err))
			return
		}

		respondJSON(w, http.StatusOK, enrichPreparations(prepList.Items, servingList.Items))
	}
}

// handleGetPreparationManifest handles GET /api/v1/preparations/{namespace}/{name}/manifest.
// It fetches the rendered Kubernetes YAML from the Preparation's OCI artifact.
func handleGetPreparationManifest(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		namespace := r.PathValue("namespace")
		name := r.PathValue("name")

		prep := &deliveryv1alpha1.Preparation{}
		if err := deps.reader.Get(r.Context(), types.NamespacedName{Namespace: namespace, Name: name}, prep); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("preparation %s/%s not found", namespace, name))
				return
			}
			deps.logger.Error(err, "Failed to get Preparation", "namespace", namespace, "name", name)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get preparation: %s", err))
			return
		}

		manifest, err := fetchManifest(r.Context(), deps.ociClient, deps.fs, prep.Spec.Artifact.OCIRef)
		if err != nil {
			deps.logger.Error(err, "Failed to fetch manifest from OCI",
				"namespace", namespace, "name", name,
				"ociRef", prep.Spec.Artifact.OCIRef)
			respondError(w, http.StatusBadGateway, fmt.Sprintf("failed to fetch manifest from OCI: %s", err))
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(manifest))
	}
}

// fetchManifest pulls the OCI artifact identified by ociRef into a temp directory,
// collects all YAML/JSON files, and returns them concatenated with "---" separators.
//
// Both oci.Client and afero.Fs are injected so the function can be unit-tested
// with oci.FakeClient and afero.MemMapFs without touching the real filesystem.
//
// ociRef format: oci://<registry>/<repo>@sha256:<digest>
func fetchManifest(ctx context.Context, ociClient oci.Client, fs afero.Fs, ociRef string) (string, error) {
	rawRef := strings.TrimPrefix(ociRef, "oci://")
	parts := strings.SplitN(rawRef, "@", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid OCI reference format: %q", ociRef)
	}
	ref, tag := parts[0], parts[1]

	tmpDir, err := afero.TempDir(fs, "", "kokumi-manifest-*")
	if err != nil {
		return "", fmt.Errorf("creating temp directory: %w", err)
	}
	defer fs.RemoveAll(tmpDir) //nolint:errcheck

	if _, _, err := ociClient.Pull(ctx, ref, tag, tmpDir); err != nil {
		return "", fmt.Errorf("pulling artifact %s@%s: %w", ref, tag, err)
	}

	return readYAMLFiles(fs, tmpDir)
}

// readYAMLFiles walks dir on the given filesystem and concatenates all
// .yaml/.yml/.json files with "---" separators.
func readYAMLFiles(fs afero.Fs, dir string) (string, error) {
	var parts []string

	err := afero.Walk(fs, dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" && ext != ".json" {
			return nil
		}

		data, err := afero.ReadFile(fs, path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		parts = append(parts, string(data))
		return nil
	})
	if err != nil {
		return "", err
	}

	if len(parts) == 0 {
		return "", fmt.Errorf("no YAML/JSON files found in artifact")
	}

	return strings.Join(parts, "\n---\n"), nil
}
