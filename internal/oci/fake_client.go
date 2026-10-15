package oci

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/spf13/afero"
)

// FakeDigest is the digest the FakeClient reports for every artifact it pulls
// or resolves.
const FakeDigest = "sha256:fdf90e00e76bf3f0d2e5042c4c4e6c42a6d38c1e2b4f5a7d8e9f0a1b2c3d4e5f"

// FakeClient implements Client intended for testing. It records what is
// pushed so that the read methods can serve it back. The zero value is usable
// for everything but Pull, which needs the filesystem given to NewFakeClient.
type FakeClient struct {
	fs          afero.Fs
	Annotations map[string]string
	Pulls       []Reference
	Pushes      []PushedArtifact
	Copies      []CopiedRefs
	Referrers   []PushedReferrer
	Indexes     []PushedIndex
	// PushReferrerErr, when set, is returned by PushReferrer.
	PushReferrerErr error
}

var _ Client = (*FakeClient)(nil)

// PushedArtifact records Push calls for test assertions.
type PushedArtifact struct {
	Ref     Reference
	Options PushOptions
	Digest  string
}

// PushedReferrer records PushReferrer calls for test assertions.
type PushedReferrer struct {
	Subject  Reference
	Artifact ReferrerArtifact
	Digest   string
}

// PushedIndex records PushIndex calls for test assertions.
type PushedIndex struct {
	Ref          Reference
	ArtifactType string
	Entries      []IndexEntry
	Annotations  map[string]string
	Digest       string
}

// CopiedRefs records Copy calls for test assertions.
type CopiedRefs struct {
	Source  Reference
	Target  Reference
	Options CopyOptions
}

// NewFakeClient returns a FakeClient that uses fs for all file operations.
// Pass the same afero.Fs instance that is given to the OrderService so that
// files written by Pull are visible when the service reads them.
func NewFakeClient(fs afero.Fs) *FakeClient {
	return &FakeClient{fs: fs}
}

// Pull records the reference and writes a minimal stub manifest.yaml into
// targetDir so that callers that expect a manifest after pulling an artifact
// do not fail. It returns FakeDigest and the Annotations configured on the
// client (may be nil).
func (c *FakeClient) Pull(ctx context.Context, ref Reference, targetDir string) (string, string, map[string]string, error) {
	c.Pulls = append(c.Pulls, ref)

	manifestPath := filepath.Join(targetDir, "manifest.yaml")
	if err := afero.WriteFile(c.fs, manifestPath, []byte("---\n"), 0600); err != nil {
		return "", "", nil, err
	}

	return "", FakeDigest, c.Annotations, nil
}

// Push records the push and returns a unique dest digest per call. Real ORAS
// PackManifest adds org.opencontainers.image.created, so same payloads still
// get different digests.
func (c *FakeClient) Push(_ context.Context, ref Reference, _ string, opts PushOptions) (string, error) {
	digest, err := randomDigest()
	if err != nil {
		return "", err
	}

	c.Pushes = append(c.Pushes, PushedArtifact{Ref: ref, Options: opts, Digest: digest})
	return digest, nil
}

// randomDigest returns a random SHA-256 digest string.
func randomDigest() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}

	return fmt.Sprintf("sha256:%x", b), nil
}

// matches reports whether ref addresses the artifact stored at pushed with
// the given digest: by digest when ref carries one, else by repository and tag.
func matches(ref, pushed Reference, digest string) bool {
	if ref.Digest != "" {
		return ref.Digest == digest
	}
	return ref.RepositoryReference() == pushed.RepositoryReference() && ref.Tag == pushed.Tag
}

// ListTags returns an empty tag list. To return specific tags in a test,
// embed FakeClient in a local struct and override the ListTags method.
func (c *FakeClient) ListTags(_ context.Context, _ Reference) ([]string, error) {
	return nil, nil
}

// Resolve returns FakeDigest.
func (c *FakeClient) Resolve(_ context.Context, _ Reference) (string, error) {
	return FakeDigest, nil
}

// Copy records the copy and returns nil.
func (c *FakeClient) Copy(_ context.Context, _ Client, srcRef, dstRef Reference, opts CopyOptions) error {
	c.Copies = append(c.Copies, CopiedRefs{Source: srcRef, Target: dstRef, Options: opts})
	return nil
}

// FetchConfig returns the config of the most recent matching Push.
func (c *FakeClient) FetchConfig(_ context.Context, ref Reference) (string, []byte, error) {
	for _, p := range slices.Backward(c.Pushes) {
		if matches(ref, p.Ref, p.Digest) {
			return p.Options.ConfigMediaType, p.Options.Config, nil
		}
	}
	return "", nil, fmt.Errorf("fetch config of %s: not found", ref)
}

// PushReferrer records the referrer and returns the payload's SHA-256 digest.
func (c *FakeClient) PushReferrer(_ context.Context, subject Reference, artifact ReferrerArtifact) (string, error) {
	if c.PushReferrerErr != nil {
		return "", c.PushReferrerErr
	}

	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(artifact.Payload))
	c.Referrers = append(c.Referrers, PushedReferrer{Subject: subject, Artifact: artifact, Digest: digest})
	return digest, nil
}

// ListReferrers returns the recorded referrers of subject, matched by the
// subject's repository and digest.
func (c *FakeClient) ListReferrers(_ context.Context, subject Reference, artifactType string) ([]Referrer, error) {
	var referrers []Referrer
	for _, r := range c.Referrers {
		if r.Subject.RepositoryReference() != subject.RepositoryReference() || r.Subject.Digest != subject.Digest {
			continue
		}
		if artifactType != "" && r.Artifact.ArtifactType != artifactType {
			continue
		}
		referrers = append(referrers, Referrer{
			Digest:       r.Digest,
			ArtifactType: r.Artifact.ArtifactType,
			Annotations:  r.Artifact.Annotations,
			Size:         int64(len(r.Artifact.Payload)),
		})
	}
	return referrers, nil
}

// FetchReferrer returns the recorded referrer with ref's digest.
func (c *FakeClient) FetchReferrer(_ context.Context, ref Reference) (ReferrerArtifact, error) {
	for _, r := range c.Referrers {
		if r.Digest != ref.Digest {
			continue
		}
		if len(r.Artifact.Payload) > MaxReferrerPayloadSize {
			return ReferrerArtifact{}, fmt.Errorf("fetch referrer %s: %w", ref, ErrPayloadTooLarge)
		}
		return r.Artifact, nil
	}
	return ReferrerArtifact{}, fmt.Errorf("fetch referrer %s: not found", ref)
}

// PushIndex records the index and returns a unique digest per call.
func (c *FakeClient) PushIndex(_ context.Context, ref Reference, artifactType string, entries []IndexEntry, annotations map[string]string) (string, error) {
	digest, err := randomDigest()
	if err != nil {
		return "", err
	}

	c.Indexes = append(c.Indexes, PushedIndex{
		Ref:          ref,
		ArtifactType: artifactType,
		Entries:      entries,
		Annotations:  annotations,
		Digest:       digest,
	})
	return digest, nil
}

// FetchIndex returns the entries of the most recent matching PushIndex, or
// ErrNotAnIndex.
func (c *FakeClient) FetchIndex(_ context.Context, ref Reference) ([]IndexEntry, error) {
	for _, idx := range slices.Backward(c.Indexes) {
		if matches(ref, idx.Ref, idx.Digest) {
			return idx.Entries, nil
		}
	}
	return nil, fmt.Errorf("fetch index %s: %w", ref, ErrNotAnIndex)
}
