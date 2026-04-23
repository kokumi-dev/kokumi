package oci

import (
	"context"
	"path/filepath"

	"github.com/spf13/afero"
)

// FakeClient implements Client intended for testing.
type FakeClient struct {
	fs afero.Fs
}

var _ Client = (*FakeClient)(nil)

// NewFakeClient returns a FakeClient that uses fs for all file operations.
// Pass the same afero.Fs instance that is given to the OrderService so that
// files written by Pull are visible when the service reads them.
func NewFakeClient(fs afero.Fs) *FakeClient {
	return &FakeClient{fs: fs}
}

// Pull writes a minimal stub manifest.yaml into targetDir so that callers that
// expect a manifest after pulling an artifact do not fail.
func (c *FakeClient) Pull(ctx context.Context, ref, tag, targetDir string) (string, string, error) {
	manifestPath := filepath.Join(targetDir, "manifest.yaml")
	if err := afero.WriteFile(c.fs, manifestPath, []byte("---\n"), 0600); err != nil {
		return "", "", err
	}

	return "", "sha256:fdf90e00e76bf3f0d2e5042c4c4e6c42a6d38c1e2b4f5a7d8e9f0a1b2c3d4e5f", nil
}

// Push returns a deterministic fake digest.
func (c *FakeClient) Push(_ context.Context, _, _, _ string, _ map[string]string) (string, error) {
	return "sha256:fdf90e00e76bf3f0d2e5042c4c4e6c42a6d38c1e2b4f5a7d8e9f0a1b2c3d4e5f", nil
}

// ListTags returns an empty tag list. To return specific tags in a test,
// embed FakeClient in a local struct and override the ListTags method.
func (c *FakeClient) ListTags(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
