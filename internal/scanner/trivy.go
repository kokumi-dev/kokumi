package scanner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/kokumi-dev/kokumi/internal/oci"
)

// scannerName is the name a Response gives the scanner.
const scannerName = "trivy"

// errBadDatabase reports a database that could not be fetched or read.
var errBadDatabase = errors.New("vulnerability database unusable")

// trivyTimeout bounds one Trivy run. A request is bounded by its context.
const trivyTimeout = 20 * time.Minute

// downloadTimeout bounds one database download. A download outlives the
// request that started it, so that a slow link that exceeds the manager's
// time for one request still completes it for the next one.
const downloadTimeout = 2 * time.Hour

// Directories below the cache directory.
const (
	// dbDir holds a directory per vulnerability database digest.
	dbDir = "trivy-db"
	// javaDBDir holds a directory per Java database digest.
	javaDBDir = "trivy-java-db"
	// workDir holds a directory per request, removed when it is done.
	workDir = "work"
)

// layoutClient is the part of an OCI client the engine pulls with.
// *oci.ORASClient implements it.
type layoutClient interface {
	CopyToLayout(ctx context.Context, ref oci.Reference, dir, tag string, platform *ocispec.Platform) (oci.Descriptor, error)
	ExtractLayers(ctx context.Context, ref oci.Reference, targetDir string) (oci.Descriptor, error)
}

// runner runs Trivy with args and returns what it wrote to stderr.
type runner func(ctx context.Context, args []string) (stderr []byte, err error)

// Trivy scans images with the Trivy CLI. It pulls images and databases
// itself, with Kokumi's OCI client, so Trivy never talks to a registry and a
// Registry's CA, client certificate and plain HTTP apply. Each database
// version is extracted once into a directory of its own that is never
// changed again, so concurrent requests share it.
type Trivy struct {
	cacheDir string
	version  string
	run      runner
	connect  func(Connection) (layoutClient, error)

	// mu guards locks.
	mu sync.Mutex
	// locks serializes the downloads of each database, by directory, so that
	// a download of the Java database holds up no scan that does not need it.
	locks map[string]*sync.Mutex
}

// NewTrivy returns an engine that runs the Trivy binary at binary and keeps
// its databases below cacheDir. It reads Trivy's version once.
func NewTrivy(ctx context.Context, binary, cacheDir string) (*Trivy, error) {
	t := &Trivy{
		cacheDir: cacheDir,
		run: func(ctx context.Context, args []string) ([]byte, error) {
			ctx, cancel := context.WithTimeout(ctx, trivyTimeout)
			defer cancel()
			var stderr bytes.Buffer
			cmd := exec.CommandContext(ctx, binary, args...)
			cmd.Stderr = &stderr
			err := cmd.Run()
			return stderr.Bytes(), err
		},
		connect: func(c Connection) (layoutClient, error) {
			opts, err := c.options()
			if err != nil {
				return nil, err
			}
			return oci.NewClient(opts), nil
		},
	}

	out, err := exec.CommandContext(ctx, binary, "version", "--format", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("reading the Trivy version: %w", err)
	}
	var v struct {
		Version string `json:"Version"`
	}
	if err := json.Unmarshal(out, &v); err != nil || v.Version == "" {
		return nil, fmt.Errorf("reading the Trivy version from %q: %w", out, err)
	}
	t.version = v.Version
	return t, nil
}

// Scan implements Engine. A database that cannot be fetched fails the
// request; an image that cannot be scanned fails only its own result.
func (t *Trivy) Scan(ctx context.Context, req Request) (*Response, error) {
	db, updatedAt, err := t.database(ctx, req.Database, dbDir, "db", "trivy.db")
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Join(t.cacheDir, workDir), 0o700); err != nil {
		return nil, fmt.Errorf("creating work directory: %w", err)
	}
	work, err := os.MkdirTemp(filepath.Join(t.cacheDir, workDir), "scan-")
	if err != nil {
		return nil, fmt.Errorf("creating work directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	// Trivy reads its databases from <cache-dir>/db and
	// <cache-dir>/java-db. The cache of a request links the versions it
	// scans with; --cache-backend memory keeps Trivy from writing there.
	cache := filepath.Join(work, "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return nil, fmt.Errorf("creating cache: %w", err)
	}
	if err := os.Symlink(db, filepath.Join(cache, "db")); err != nil {
		return nil, fmt.Errorf("linking database: %w", err)
	}

	client, err := t.connect(req.Registry)
	if err != nil {
		return nil, fmt.Errorf("connecting to the images: %w", err)
	}

	s := &scan{trivy: t, req: req, client: client, work: work, cache: cache}
	resp := &Response{
		Scanner:  ScannerInfo{Name: scannerName, Version: t.version},
		Database: DatabaseInfo{Ref: req.Database.Ref, UpdatedAt: updatedAt},
		Images:   make([]Result, 0, len(req.Images)),
	}
	for i, img := range req.Images {
		result := Result{Ref: img.Ref}
		result.SBOM, result.Report, err = s.image(ctx, i, img)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			result = Result{Ref: img.Ref, Error: err.Error()}
		}
		resp.Images = append(resp.Images, result)
	}
	return resp, nil
}

// database returns the directory below dir that holds the database at
// ref, downloading it when it is not there yet, and when it was built. file
// is the database file the artifact must hold. When ctx ends first, the
// download goes on for the next request.
func (t *Trivy) database(ctx context.Context, db Database, dir, name, file string) (string, time.Time, error) {
	ref, err := oci.Parse(db.Ref)
	if err != nil || ref.Digest == "" {
		return "", time.Time{}, fmt.Errorf("%w: %q is no reference by digest", errBadDatabase, db.Ref)
	}
	hex := strings.TrimPrefix(ref.Digest, "sha256:")
	target := filepath.Join(t.cacheDir, dir, hex)

	installed := make(chan error, 1)
	go func() { installed <- t.install(context.WithoutCancel(ctx), db, ref, dir, target, file) }()
	select {
	case err := <-installed:
		if err != nil {
			return "", time.Time{}, err
		}
	case <-ctx.Done():
		return "", time.Time{}, ctx.Err()
	}

	data, err := os.ReadFile(filepath.Join(target, "metadata.json"))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: %s has no metadata: %w", errBadDatabase, name, err)
	}
	var metadata struct {
		UpdatedAt time.Time `json:"UpdatedAt"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return "", time.Time{}, fmt.Errorf("%w: %s metadata: %w", errBadDatabase, name, err)
	}
	return target, metadata.UpdatedAt, nil
}

// install downloads the database at ref into target unless it is there,
// one download per database at a time.
func (t *Trivy) install(ctx context.Context, db Database, ref oci.Reference, dir, target, file string) error {
	defer t.lock(dir)()
	if _, err := os.Stat(filepath.Join(target, file)); err == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	return t.download(ctx, db, ref, target, file)
}

// lock locks the downloads of the database in dir and returns the unlock.
func (t *Trivy) lock(dir string) func() {
	t.mu.Lock()
	if t.locks == nil {
		t.locks = map[string]*sync.Mutex{}
	}
	l, ok := t.locks[dir]
	if !ok {
		l = &sync.Mutex{}
		t.locks[dir] = l
	}
	t.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// download extracts the database at ref into target. It extracts into a
// directory beside target and renames it into place, so that a directory
// that exists is complete. Older versions are removed, keeping the newest
// besides target, which requests still scanning may use.
func (t *Trivy) download(ctx context.Context, db Database, ref oci.Reference, target, file string) error {
	client, err := t.connect(db.Registry)
	if err != nil {
		return fmt.Errorf("%w: connecting to %s: %w", errBadDatabase, db.Ref, err)
	}

	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", parent, err)
	}
	tmp, err := os.MkdirTemp(parent, ".download-")
	if err != nil {
		return fmt.Errorf("creating %s: %w", parent, err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	if _, err := client.ExtractLayers(ctx, ref, tmp); err != nil {
		return fmt.Errorf("%w: downloading %s: %w", errBadDatabase, db.Ref, err)
	}
	if _, err := os.Stat(filepath.Join(tmp, file)); err != nil {
		return fmt.Errorf("%w: %s holds no %s", errBadDatabase, db.Ref, file)
	}

	pruneOlder(parent, target)
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("installing %s: %w", db.Ref, err)
	}
	return nil
}

// pruneOlder removes the database versions in parent but the most recently
// installed one, which requests may still read, so that two versions remain
// once keep is installed.
func pruneOlder(parent, keep string) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	var newest string
	var newestTime time.Time
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || filepath.Join(parent, e.Name()) == keep {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if newest == "" || info.ModTime().After(newestTime) {
			if newest != "" {
				_ = os.RemoveAll(filepath.Join(parent, newest))
			}
			newest, newestTime = e.Name(), info.ModTime()
			continue
		}
		_ = os.RemoveAll(filepath.Join(parent, e.Name()))
	}
}

// scan is the state of one request.
type scan struct {
	trivy  *Trivy
	req    Request
	client layoutClient
	work   string
	cache  string
	// javaDB is set once the Java database is linked into the cache.
	javaDB bool
}

// image scans one image: it generates an SBOM unless the request carries
// one, and computes the report from that SBOM. It returns the generated SBOM
// and the report.
func (s *scan) image(ctx context.Context, i int, img Image) (sbom, report json.RawMessage, err error) {
	dir := filepath.Join(s.work, fmt.Sprintf("image-%d", i))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("creating %s: %w", dir, err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	logger := logr.FromContextOrDiscard(ctx)
	start := time.Now()
	sbomPath := filepath.Join(dir, "sbom.json")
	if len(img.SBOM) > 0 {
		if err := os.WriteFile(sbomPath, img.SBOM, 0o600); err != nil {
			return nil, nil, fmt.Errorf("writing SBOM: %w", err)
		}
	} else {
		if err := s.generate(ctx, img, dir, sbomPath); err != nil {
			return nil, nil, err
		}
		if sbom, err = readOutput(sbomPath); err != nil {
			return nil, nil, err
		}
	}

	sbomDone := time.Now()
	reportPath := filepath.Join(dir, "report.json")
	if stderr, err := s.trivy.run(ctx, append(s.args("sbom"), "--format", "cosign-vuln", "--output", reportPath, sbomPath)); err != nil {
		return nil, nil, trivyError("scanning SBOM", err, stderr)
	}
	if report, err = readOutput(reportPath); err != nil {
		return nil, nil, err
	}
	logger.Info("Scanned image", "ref", img.Ref, "platform", img.Platform, "sbomReused", len(img.SBOM) > 0,
		"sbomSeconds", sbomDone.Sub(start).Seconds(), "scanSeconds", time.Since(sbomDone).Seconds(),
		"sbomBytes", len(sbom), "reportBytes", len(report))
	return sbom, report, nil
}

// generate pulls the image into an OCI layout and writes its CycloneDX SBOM
// to sbomPath. An image with Java archives needs the Java database, which is
// downloaded then and the SBOM generated again.
func (s *scan) generate(ctx context.Context, img Image, dir, sbomPath string) error {
	ref, err := oci.Parse(img.Ref)
	if err != nil {
		return err
	}
	platform, err := parsePlatform(img.Platform)
	if err != nil {
		return err
	}

	layout := filepath.Join(dir, "layout")
	if _, err := s.client.CopyToLayout(ctx, ref, layout, "scan", platform); err != nil {
		return fmt.Errorf("pulling image: %w", err)
	}
	defer func() { _ = os.RemoveAll(layout) }()

	args := append(s.args("image"), "--format", "cyclonedx", "--output", sbomPath, "--input", layout)
	stderr, err := s.trivy.run(ctx, args)
	if err != nil && needsJavaDB(stderr) && !s.javaDB && s.req.JavaDatabase != nil {
		if err := s.linkJavaDB(ctx); err != nil {
			return err
		}
		stderr, err = s.trivy.run(ctx, args)
	}
	if err != nil {
		return trivyError("generating SBOM", err, stderr)
	}
	return nil
}

// linkJavaDB downloads the Java database of the request, unless it is
// cached, and links it into the request's cache.
func (s *scan) linkJavaDB(ctx context.Context) error {
	dir, _, err := s.trivy.database(ctx, *s.req.JavaDatabase, javaDBDir, "java-db", "trivy-java.db")
	if err != nil {
		return err
	}
	if err := os.Symlink(dir, filepath.Join(s.cache, "java-db")); err != nil {
		return fmt.Errorf("linking Java database: %w", err)
	}
	s.javaDB = true
	return nil
}

// args returns the arguments every Trivy run of the request starts with:
// offline, with the request's databases and without a persistent cache.
func (s *scan) args(command string) []string {
	return []string{
		command,
		"--cache-dir", s.cache,
		"--cache-backend", "memory",
		"--skip-db-update",
		"--skip-java-db-update",
		"--offline-scan",
		"--skip-version-check",
		"--disable-telemetry",
		"--no-progress",
		"--quiet",
		"--timeout", trivyTimeout.String(),
	}
}

// needsJavaDB reports whether Trivy failed for want of the Java database.
func needsJavaDB(stderr []byte) bool {
	return bytes.Contains(stderr, []byte("Java DB")) && bytes.Contains(stderr, []byte("first run"))
}

// trivyError returns the error of a failed Trivy run, with the end of what
// it wrote to stderr.
func trivyError(action string, err error, stderr []byte) error {
	msg := strings.TrimSpace(string(stderr))
	if len(msg) > 2048 {
		msg = "..." + msg[len(msg)-2048:]
	}
	if msg == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, msg)
}

// readOutput reads a file Trivy wrote, bounded by MaxOutputSize.
func readOutput(path string) (json.RawMessage, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}
	if info.Size() > MaxOutputSize {
		return nil, fmt.Errorf("%s is %d bytes, limit is %d", filepath.Base(path), info.Size(), MaxOutputSize)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("%s is not JSON", filepath.Base(path))
	}
	return data, nil
}

// parsePlatform parses "os/arch" or "os/arch/variant", nil for "".
func parsePlatform(s string) (*ocispec.Platform, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, "/")
	if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("invalid platform %q, want os/arch[/variant]", s)
	}
	p := &ocispec.Platform{OS: parts[0], Architecture: parts[1]}
	if len(parts) == 3 {
		p.Variant = parts[2]
	}
	return p, nil
}
