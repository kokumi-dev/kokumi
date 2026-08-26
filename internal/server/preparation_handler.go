package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/spf13/afero"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// handleListPreparations handles GET /api/v1/orders/{namespace}/{name}/preparations.
// Returns all Preparations for the given Order, sorted newest-first by createdAt,
// with IsActive populated from the linked Serving.
func handleListPreparations(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		namespace := r.PathValue("namespace")
		orderName := r.PathValue("name")

		prepList := &deliveryv1alpha1.PreparationList{}
		if err := deps.reader.List(r.Context(), prepList, client.InNamespace(namespace)); err != nil {
			deps.logger.Error(err, "Failed to list Preparations", "namespace", namespace, "order", orderName)
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("failed to list preparations: %s", err))
			return
		}

		// Client-side filter by order name.
		filtered := prepList.Items[:0]
		for _, p := range prepList.Items {
			if p.Spec.OrderName == orderName {
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
func fetchManifest(ctx context.Context, ociClient oci.Client, fs afero.Fs, ref string) (string, error) {
	ociRef, err := oci.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("invalid OCI reference format: %q", ref)
	}

	tmpDir, err := afero.TempDir(fs, "", "kokumi-manifest-*")
	if err != nil {
		return "", fmt.Errorf("creating temp directory: %w", err)
	}
	defer fs.RemoveAll(tmpDir) //nolint:errcheck

	if _, _, _, err := ociClient.Pull(ctx, ociRef, tmpDir); err != nil {
		return "", fmt.Errorf("pulling artifact %s: %w", ociRef.String(), err)
	}

	return readYAMLFiles(fs, tmpDir)
}

// readYAMLFiles walks dir on the given filesystem and concatenates all
// .yaml/.yml/.json files with "---" separators.
func readYAMLFiles(fs afero.Fs, dir string) (string, error) {
	files, err := listArtifactFiles(fs, dir)
	if err != nil {
		return "", err
	}

	parts := make([]string, 0, len(files))
	for _, f := range files {
		parts = append(parts, f.Content)
	}

	return strings.Join(parts, "\n---\n"), nil
}

// listArtifactFiles walks dir on the given filesystem and returns all
// .yaml/.yml/.json files with their paths relative to dir, sorted by path.
func listArtifactFiles(fs afero.Fs, dir string) ([]ArtifactFileDTO, error) {
	var files []ArtifactFileDTO

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

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			rel = filepath.Base(path)
		}

		files = append(files, ArtifactFileDTO{
			Path:    filepath.ToSlash(rel),
			Content: string(data),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no YAML/JSON files found in artifact")
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	return files, nil
}

// handleGetPreparationManifestFiles handles GET /api/v1/preparations/{namespace}/{name}/manifest/files.
// It returns the individual YAML files of the Preparation's OCI artifact,
// preserving the artifact's file layout.
func handleGetPreparationManifestFiles(deps *apiDeps) http.HandlerFunc {
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

		ociRef, err := oci.Parse(prep.Spec.Artifact.OCIRef)
		if err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid OCI reference format: %q", prep.Spec.Artifact.OCIRef))
			return
		}

		tmpDir, err := afero.TempDir(deps.fs, "", "kokumi-manifest-*")
		if err != nil {
			respondError(w, http.StatusInternalServerError, "could not create temp directory")
			return
		}
		defer deps.fs.RemoveAll(tmpDir) //nolint:errcheck

		if _, _, _, err := deps.ociClient.Pull(r.Context(), ociRef, tmpDir); err != nil {
			deps.logger.Error(err, "Failed to pull artifact", "ociRef", prep.Spec.Artifact.OCIRef)
			respondError(w, http.StatusBadGateway, "could not pull artifact: "+err.Error())
			return
		}

		files, err := listArtifactFiles(deps.fs, tmpDir)
		if err != nil {
			deps.logger.Error(err, "Failed to read manifest files", "ociRef", prep.Spec.Artifact.OCIRef)
			respondError(w, http.StatusBadGateway, "could not read manifest files: "+err.Error())
			return
		}

		respondJSON(w, http.StatusOK, files)
	}
}
