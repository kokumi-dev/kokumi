package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/renderer"
	"github.com/spf13/afero"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// RecipeResult holds the outcome of processing a Recipe artifact.
type RecipeResult struct {
	SourceRef    string
	SourceDigest string
	DestRef      string
	DestDigest   string
}

// RecipeService handles the FS and OCI operations for a Recipe.
type RecipeService struct {
	client oci.Client
	fs     afero.Fs
}

// NewRecipeService returns a new RecipeService.
func NewRecipeService(client oci.Client, fs afero.Fs) *RecipeService {
	return &RecipeService{
		client: client,
		fs:     fs,
	}
}

// ProcessRecipe pulls the source artifact, applies patches or normalizes YAML,
// pushes the result to the destination, and returns the source/dest refs and digests.
func (rs *RecipeService) ProcessRecipe(ctx context.Context, recipe *deliveryv1alpha1.Recipe) (*RecipeResult, error) {
	logger := log.FromContext(ctx)

	sourceRef := strings.TrimPrefix(recipe.Spec.Source.OCI, "oci://")
	destRef := strings.TrimPrefix(recipe.Spec.Destination.OCI, "oci://")

	logger.Info("Processing artifact", "source", sourceRef, "destination", destRef, "version", recipe.Spec.Source.Version)

	tempDir, err := afero.TempDir(rs.fs, "", "recipe-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer rs.fs.RemoveAll(tempDir) //nolint:errcheck

	logger.Info("Fetching artifact from source")

	sourceDigest, err := rs.client.Pull(ctx, sourceRef, recipe.Spec.Source.Version, tempDir)
	if err != nil {
		return nil, fmt.Errorf("failed to pull artifact: %w", err)
	}

	logger.Info("Pulled source artifact", "digest", sourceDigest)

	manifestPath := filepath.Join(tempDir, "manifest.yaml")

	content, err := afero.ReadFile(rs.fs, manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	processedContent, err := rs.processManifest(ctx, content, recipe.Spec.Patches)
	if err != nil {
		return nil, err
	}

	if err := afero.WriteFile(rs.fs, manifestPath, processedContent, 0600); err != nil {
		return nil, fmt.Errorf("failed to write manifest: %w", err)
	}

	logger.Info("Pushing artifact to destination")

	destDigest, err := rs.client.Push(ctx, destRef, recipe.Spec.Source.Version, tempDir)
	if err != nil {
		return nil, fmt.Errorf("failed to push artifact: %w", err)
	}

	logger.Info("Successfully processed artifact", "digest", destDigest)

	return &RecipeResult{
		SourceRef:    sourceRef,
		SourceDigest: sourceDigest,
		DestRef:      destRef,
		DestDigest:   destDigest,
	}, nil
}

// processManifest applies patches when present, otherwise normalizes YAML formatting.
func (rs *RecipeService) processManifest(ctx context.Context, content []byte, patches []deliveryv1alpha1.Patch) ([]byte, error) {
	logger := log.FromContext(ctx)

	if len(patches) > 0 {
		logger.Info("Applying patches", "count", len(patches))

		processed, err := renderer.ApplyPatches(ctx, content, patches)
		if err != nil {
			return nil, fmt.Errorf("failed to apply patches: %w", err)
		}

		logger.Info("Successfully applied patches")

		return processed, nil
	}

	logger.Info("Normalizing YAML formatting")

	processed, err := renderer.NormalizeYAML(content)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize YAML: %w", err)
	}

	return processed, nil
}
