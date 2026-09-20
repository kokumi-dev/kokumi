package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
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

// restore copies all artifact files of a cache entry into workDir. It fails
// when the entry contains no artifact files (e.g. a partially written entry),
// which forces the caller to re-pull instead of serving an empty directory.
func (c *pullCache) restore(entryDir, workDir string) error {
	infos, err := afero.ReadDir(c.fs, entryDir)
	if err != nil {
		return err
	}

	restored := 0
	for _, info := range infos {
		if info.IsDir() || info.Name() == "meta.json" {
			continue
		}

		data, err := afero.ReadFile(c.fs, filepath.Join(entryDir, info.Name()))
		if err != nil {
			return err
		}

		if err := afero.WriteFile(c.fs, filepath.Join(workDir, info.Name()), data, 0600); err != nil {
			return err
		}
		restored++
	}

	if restored == 0 {
		return fmt.Errorf("cache entry %q contains no artifact files", entryDir)
	}

	return nil
}

// put writes the pulled artifact and its metadata to the cache. Errors are
// non-fatal and only logged as informational messages.
func (c *pullCache) put(ctx context.Context, mediaType, digest string, annotations map[string]string, ref oci.Reference, layout Layout, workDir string) {
	logger := log.FromContext(ctx)

	if c == nil {
		return
	}

	entryDir := filepath.Join(c.dir, pullCacheKey(ref, layout))
	metaPath := filepath.Join(entryDir, "meta.json")

	if err := c.fs.MkdirAll(entryDir, 0700); err != nil {
		logger.Info("Could not create cache entry directory, skipping cache", "error", err)
		return
	}

	infos, err := afero.ReadDir(c.fs, workDir)
	if err != nil {
		logger.Info("Could not read artifact directory for caching, skipping cache", "error", err)
		return
	}

	for _, info := range infos {
		if info.IsDir() {
			continue
		}

		data, err := afero.ReadFile(c.fs, filepath.Join(workDir, info.Name()))
		if err != nil {
			logger.Info("Could not read artifact for caching, skipping cache", "error", err)
			return
		}

		if err := afero.WriteFile(c.fs, filepath.Join(entryDir, info.Name()), data, 0600); err != nil {
			logger.Info("Could not write artifact to cache, skipping cache", "error", err)
			return
		}
	}

	metaBytes, err := json.Marshal(cacheEntry{MediaType: mediaType, Digest: digest, Annotations: annotations})
	if err != nil {
		return
	}

	if err := afero.WriteFile(c.fs, metaPath, metaBytes, 0600); err != nil {
		logger.Info("Could not write cache metadata, skipping cache", "error", err)
	}
}
