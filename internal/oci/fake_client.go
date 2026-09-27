package oci

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"path/filepath"

	"github.com/spf13/afero"
)

// FakeClient implements Client intended for testing.
type FakeClient struct {
	fs          afero.Fs
	Annotations map[string]string
	Copies      []CopiedRefs
	Referrers   []PushedReferrer
	// PushReferrerErr, when set, is returned by PushReferrer.
	PushReferrerErr error
}

var _ Client = (*FakeClient)(nil)

// PushedReferrer records PushReferrer calls for test assertions.
type PushedReferrer struct {
	Subject  Reference
	Artifact ReferrerArtifact
}

// CopiedRefs records Copy calls for test assertions.
type CopiedRefs struct {
	Source Reference
	Target Reference
}

// NewFakeClient returns a FakeClient that uses fs for all file operations.
// Pass the same afero.Fs instance that is given to the OrderService so that
// files written by Pull are visible when the service reads them.
func NewFakeClient(fs afero.Fs) *FakeClient {
	return &FakeClient{fs: fs}
}

// Pull writes a minimal stub manifest.yaml into targetDir so that callers that
// expect a manifest after pulling an artifact do not fail. It returns the
// Annotations configured on the client (may be nil).
func (c *FakeClient) Pull(ctx context.Context, ref Reference, targetDir string) (string, string, map[string]string, error) {
	manifestPath := filepath.Join(targetDir, "manifest.yaml")
	if err := afero.WriteFile(c.fs, manifestPath, []byte("---\n"), 0600); err != nil {
		return "", "", nil, err
	}

	return "", "sha256:fdf90e00e76bf3f0d2e5042c4c4e6c42a6d38c1e2b4f5a7d8e9f0a1b2c3d4e5f", c.Annotations, nil
}

// Push returns a unique dest digest per call. Real ORAS PackManifest adds
// org.opencontainers.image.created, so same payloads still get different digests.
func (c *FakeClient) Push(_ context.Context, _ Reference, _ string, _ map[string]string) (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}

	return fmt.Sprintf("sha256:%x", b), nil
}

// ListTags returns an empty tag list. To return specific tags in a test,
// embed FakeClient in a local struct and override the ListTags method.
func (c *FakeClient) ListTags(_ context.Context, _ Reference) ([]string, error) {
	return nil, nil
}

// Resolve returns a stable fake digest.
func (c *FakeClient) Resolve(_ context.Context, _ Reference) (string, error) {
	return "sha256:fdf90e00e76bf3f0d2e5042c4c4e6c42a6d38c1e2b4f5a7d8e9f0a1b2c3d4e5f", nil
}

// Copy records the copy and returns nil.
func (c *FakeClient) Copy(_ context.Context, _ Client, srcRef, dstRef Reference) error {
	c.Copies = append(c.Copies, CopiedRefs{Source: srcRef, Target: dstRef})
	return nil
}

// PushReferrer records the referrer and returns the payload's SHA-256 digest.
func (c *FakeClient) PushReferrer(_ context.Context, subject Reference, artifact ReferrerArtifact) (string, error) {
	if c.PushReferrerErr != nil {
		return "", c.PushReferrerErr
	}
	c.Referrers = append(c.Referrers, PushedReferrer{Subject: subject, Artifact: artifact})
	return fmt.Sprintf("sha256:%x", sha256.Sum256(artifact.Payload)), nil
}
