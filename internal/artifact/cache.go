package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/afero"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/kokumi-dev/kokumi/internal/oci"
)

// sha256Sum returns the hex-encoded SHA-256 of b.
func sha256Sum(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum)
}

// cacheEntry is the metadata written alongside a cached artifact blob.
type cacheEntry struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// pullCache caches pulled OCI artifacts on the filesystem, keyed by reference
// and file layout. A nil *pullCache disables caching entirely.
type pullCache struct {
	fs  afero.Fs
	dir string
}

// pullCacheKey returns a filesystem-safe directory name for the given OCI ref
// and layout. The layout is mixed in because merged and separate file layouts
// must not share a cache entry.
func pullCacheKey(ref oci.Reference, layout Layout) string {
	sum := sha256Sum(fmt.Appendf(nil, "%s#%s", ref.String(), layout))
	return sum
}

// get restores a cached pull into workDir. It returns the cache entry when a
// valid hit was restored, and nil on any miss or restore error.
func (c *pullCache) get(ctx context.Context, ref oci.Reference, layout Layout, workDir string) *cacheEntry {
	logger := log.FromContext(ctx)

	if c == nil {
		return nil
	}

	entryDir := filepath.Join(c.dir, pullCacheKey(ref, layout))
	metaPath := filepath.Join(entryDir, "meta.json")

	metaBytes, err := afero.ReadFile(c.fs, metaPath)
	if err != nil {
		return nil
	}

	var entry cacheEntry
	if err := json.Unmarshal(metaBytes, &entry); err != nil {
		return nil
	}

	if err := c.restore(entryDir, workDir); err != nil {
		logger.Info("Cache entry invalid, re-pulling source artifact", "ref", ref.RepositoryReference(), "version", ref.GetReference())
		return nil
	}

	logger.Info("Pulled source artifact from cache", "ref", ref.RepositoryReference(), "version", ref.GetReference(), "digest", entry.Digest)
	return &entry
}

// filesDir is the directory of a cache entry that holds the artifact's files,
// apart from meta.json, so that an artifact file of that name cannot clash.
const filesDir = "files"

// restore copies all artifact files of a cache entry into workDir, keeping
// their directories. It fails when the entry contains no artifact files (e.g.
// a partially written entry), which forces the caller to re-pull instead of
// serving an empty directory.
func (c *pullCache) restore(entryDir, workDir string) error {
	restored, err := copyTree(c.fs, filepath.Join(entryDir, filesDir), workDir)
	if err != nil {
		return err
	}
	if restored == 0 {
		return fmt.Errorf("cache entry %q contains no artifact files", entryDir)
	}

	return nil
}

// copyTree copies the regular files below from to the same paths below to and
// returns how many it copied.
func copyTree(fs afero.Fs, from, to string) (int, error) {
	copied := 0
	err := afero.Walk(fs, from, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		data, err := afero.ReadFile(fs, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		if err := fs.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		if err := afero.WriteFile(fs, target, data, 0600); err != nil {
			return err
		}
		copied++
		return nil
	})
	return copied, err
}

// put writes the pulled artifact and its metadata to the cache. The entry is
// written to a temporary directory and renamed into place, so that concurrent
// renders never restore a half-written entry. put only runs after a cache
// miss, so it replaces the entry that is there, which is missing or invalid.
// Errors are non-fatal and only logged as informational messages.
func (c *pullCache) put(ctx context.Context, mediaType, digest string, annotations map[string]string, ref oci.Reference, layout Layout, workDir string) {
	logger := log.FromContext(ctx)

	if c == nil {
		return
	}

	entryDir := filepath.Join(c.dir, pullCacheKey(ref, layout))

	tmpDir, err := afero.TempDir(c.fs, c.dir, ".tmp-")
	if err != nil {
		logger.Info("Could not create cache entry directory, skipping cache", "error", err)
		return
	}
	// RemoveAll finds nothing once the entry is renamed into place.
	defer func() {
		if err := c.fs.RemoveAll(tmpDir); err != nil {
			logger.Info("Could not remove temporary cache directory", "error", err)
		}
	}()

	if _, err := copyTree(c.fs, workDir, filepath.Join(tmpDir, filesDir)); err != nil {
		logger.Info("Could not write artifact to cache, skipping cache", "error", err)
		return
	}

	metaBytes, err := json.Marshal(cacheEntry{MediaType: mediaType, Digest: digest, Annotations: annotations})
	if err != nil {
		return
	}

	if err := afero.WriteFile(c.fs, filepath.Join(tmpDir, "meta.json"), metaBytes, 0600); err != nil {
		logger.Info("Could not write cache metadata, skipping cache", "error", err)
		return
	}

	if err := c.fs.RemoveAll(entryDir); err != nil {
		logger.Info("Could not replace cache entry, skipping cache", "error", err)
		return
	}
	// Another render may have put the same entry in the meantime; either
	// one is complete.
	if err := c.fs.Rename(tmpDir, entryDir); err != nil {
		logger.Info("Could not move cache entry into place, skipping cache", "error", err)
	}
}
