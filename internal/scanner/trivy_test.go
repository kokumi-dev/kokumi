package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	ggcrregistry "github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kokumi-dev/kokumi/internal/oci"
)

const (
	testVersion = "0.71.0"
	testLinux   = "linux"
	cmdImage    = "image"
	cmdSBOM     = "sbom"

	testSBOM   = `{"bomFormat":"CycloneDX","specVersion":"1.6","components":[]}`
	testReport = `{"invocation":{"parameters":null},"scanner":{"uri":"pkg:github/aquasecurity/trivy@0.71.0","version":"0.71.0","db":{"uri":"","version":""},"result":{"Results":[]}},"metadata":{}}`
)

// testRegistry starts an in-memory registry and returns its host.
func testRegistry(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(ggcrregistry.New(ggcrregistry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(server.Close)
	return strings.TrimPrefix(server.URL, "http://")
}

// pushDatabase pushes a database artifact holding file and a metadata.json
// to ref and returns its reference by digest.
func pushDatabase(t *testing.T, ref, file, updatedAt string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte("bolt"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(`{"Version":2,"UpdatedAt":"`+updatedAt+`"}`), 0o600))

	parsed, err := oci.Parse(ref)
	require.NoError(t, err)
	digest, err := oci.NewClient(oci.Options{PlainHTTP: true}).Push(context.Background(), parsed, dir, oci.PushOptions{})
	require.NoError(t, err)
	parsed.Tag, parsed.Digest = "", digest
	return parsed.OCIString()
}

// pushImage pushes a two-platform image index to ref and returns its
// reference by digest.
func pushImage(t *testing.T, host, repo string) string {
	t.Helper()
	amd64, err := random.Image(64, 1)
	require.NoError(t, err)
	arm64, err := random.Image(64, 1)
	require.NoError(t, err)
	index := mutate.AppendManifests(empty.Index,
		mutate.IndexAddendum{Add: amd64, Platform: &v1.Platform{OS: testLinux, Architecture: "amd64"}},
		mutate.IndexAddendum{Add: arm64, Platform: &v1.Platform{OS: testLinux, Architecture: "arm64"}},
	)
	tag, err := name.NewTag(host+"/"+repo+":1.0", name.Insecure)
	require.NoError(t, err)
	require.NoError(t, remote.WriteIndex(tag, index))
	digest, err := index.Digest()
	require.NoError(t, err)
	return "oci://" + host + "/" + repo + "@" + digest.String()
}

// fakeTrivy records the Trivy runs and writes their outputs. imageErr, when
// set, is what a run of "trivy image" fails with first.
type fakeTrivy struct {
	mu       sync.Mutex
	runs     [][]string
	imageErr []byte
}

func (f *fakeTrivy) run(_ context.Context, args []string) ([]byte, error) {
	f.mu.Lock()
	f.runs = append(f.runs, args)
	failImage := f.imageErr
	f.imageErr = nil
	f.mu.Unlock()

	output := args[slices.Index(args, "--output")+1]
	switch args[0] {
	case cmdImage:
		if failImage != nil {
			return failImage, errors.New("exit status 1")
		}
		if _, err := os.Stat(filepath.Join(args[slices.Index(args, "--input")+1], "oci-layout")); err != nil {
			return []byte("no OCI layout"), err
		}
		return nil, os.WriteFile(output, []byte(testSBOM), 0o600)
	case cmdSBOM:
		return nil, os.WriteFile(output, []byte(testReport), 0o600)
	}
	return []byte("unknown command"), errors.New("exit status 2")
}

func (f *fakeTrivy) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.runs))
	for _, r := range f.runs {
		out = append(out, r[0])
	}
	return out
}

// newTestTrivy returns an engine that runs fake instead of Trivy.
func newTestTrivy(t *testing.T, fake *fakeTrivy) *Trivy {
	t.Helper()
	return &Trivy{
		cacheDir: t.TempDir(),
		version:  testVersion,
		run:      fake.run,
		connect: func(c Connection) (layoutClient, error) {
			opts, err := c.options()
			if err != nil {
				return nil, err
			}
			return oci.NewClient(opts), nil
		},
	}
}

func TestTrivy_Scan(t *testing.T) {
	ctx := context.Background()
	host := testRegistry(t)
	db := pushDatabase(t, "oci://"+host+"/mirror/trivy-db:2", "trivy.db", "2026-10-19T07:19:54Z")
	image := pushImage(t, host, "shop/vendor/podinfo/images/ghcr.io/stefanprodan/podinfo")
	plain := Connection{PlainHTTP: true}

	fake := &fakeTrivy{}
	engine := newTestTrivy(t, fake)

	resp, err := engine.Scan(ctx, Request{
		Database: Database{Ref: db, Registry: plain},
		Registry: plain,
		Images: []Image{
			{Ref: image, Platform: "linux/arm64"},
			{Ref: image, SBOM: json.RawMessage(testSBOM)},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, ScannerInfo{Name: scannerName, Version: testVersion}, resp.Scanner)
	assert.Equal(t, db, resp.Database.Ref)
	assert.Equal(t, "2026-10-19T07:19:54Z", resp.Database.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"))
	require.Len(t, resp.Images, 2)

	generated := resp.Images[0]
	assert.Empty(t, generated.Error)
	assert.JSONEq(t, testSBOM, string(generated.SBOM), "a generated SBOM is returned")
	assert.JSONEq(t, testReport, string(generated.Report))

	given := resp.Images[1]
	assert.Empty(t, given.Error)
	assert.Empty(t, given.SBOM, "an SBOM the request carried is not returned")
	assert.JSONEq(t, testReport, string(given.Report))

	assert.Equal(t, []string{cmdImage, cmdSBOM, cmdSBOM}, fake.commands(), "only the image without an SBOM is pulled")
	for _, run := range fake.runs {
		assert.Contains(t, strings.Join(run, " "), "--cache-backend memory --skip-db-update --skip-java-db-update --offline-scan")
	}

	entries, err := os.ReadDir(filepath.Join(engine.cacheDir, dbDir))
	require.NoError(t, err)
	require.Len(t, entries, 1, "the database is extracted once")
	assert.FileExists(t, filepath.Join(engine.cacheDir, dbDir, entries[0].Name(), "trivy.db"))

	work, err := os.ReadDir(filepath.Join(engine.cacheDir, workDir))
	require.NoError(t, err)
	assert.Empty(t, work, "the work directory of a request is removed")

	// A second request reuses the extracted database.
	installed, err := os.Stat(filepath.Join(engine.cacheDir, dbDir, entries[0].Name()))
	require.NoError(t, err)
	_, err = engine.Scan(ctx, Request{Database: Database{Ref: db, Registry: plain}, Registry: plain, Images: []Image{{Ref: image, SBOM: json.RawMessage(testSBOM)}}})
	require.NoError(t, err)
	again, err := os.Stat(filepath.Join(engine.cacheDir, dbDir, entries[0].Name()))
	require.NoError(t, err)
	assert.Equal(t, installed.ModTime(), again.ModTime())
}

func TestTrivy_Scan_javaDB(t *testing.T) {
	ctx := context.Background()
	host := testRegistry(t)
	db := pushDatabase(t, "oci://"+host+"/mirror/trivy-db:2", "trivy.db", "2026-10-19T07:19:54Z")
	javaDB := pushDatabase(t, "oci://"+host+"/mirror/trivy-java-db:1", "trivy-java.db", "2026-10-15T00:00:00Z")
	image := pushImage(t, host, "shop/vendor/app/images/docker.io/library/tomcat")
	plain := Connection{PlainHTTP: true}

	needsJava := []byte("FATAL\tUnable to initialize the Java DB: Java DB update failed: '--skip-java-db-update' cannot be specified on the first run")

	t.Run("downloaded when an image needs it", func(t *testing.T) {
		fake := &fakeTrivy{imageErr: needsJava}
		engine := newTestTrivy(t, fake)

		resp, err := engine.Scan(ctx, Request{
			Database:     Database{Ref: db, Registry: plain},
			JavaDatabase: &Database{Ref: javaDB, Registry: plain},
			Registry:     plain,
			Images:       []Image{{Ref: image, Platform: "linux/amd64"}},
		})
		require.NoError(t, err)
		assert.Empty(t, resp.Images[0].Error)
		assert.Equal(t, []string{cmdImage, cmdImage, cmdSBOM}, fake.commands(), "generated again with the Java database")

		entries, err := os.ReadDir(filepath.Join(engine.cacheDir, javaDBDir))
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.FileExists(t, filepath.Join(engine.cacheDir, javaDBDir, entries[0].Name(), "trivy-java.db"))
	})

	t.Run("an image fails without it", func(t *testing.T) {
		fake := &fakeTrivy{imageErr: needsJava}
		engine := newTestTrivy(t, fake)

		resp, err := engine.Scan(ctx, Request{
			Database: Database{Ref: db, Registry: plain},
			Registry: plain,
			Images:   []Image{{Ref: image, Platform: "linux/amd64"}},
		})
		require.NoError(t, err, "one image failing does not fail the request")
		assert.Contains(t, resp.Images[0].Error, "Java DB")
		assert.Empty(t, resp.Images[0].Report)
	})
}

func TestTrivy_Scan_errors(t *testing.T) {
	ctx := context.Background()
	host := testRegistry(t)
	db := pushDatabase(t, "oci://"+host+"/mirror/trivy-db:2", "trivy.db", "2026-10-19T07:19:54Z")
	notADB := pushDatabase(t, "oci://"+host+"/mirror/other:2", "other.db", "2026-10-19T07:19:54Z")
	plain := Connection{PlainHTTP: true}

	engine := newTestTrivy(t, &fakeTrivy{})

	_, err := engine.Scan(ctx, Request{Database: Database{Ref: notADB, Registry: plain}, Registry: plain, Images: []Image{{Ref: db}}})
	require.ErrorIs(t, err, errBadDatabase, "an artifact without trivy.db is no database")

	_, err = engine.Scan(ctx, Request{Database: Database{Ref: "oci://" + host + "/mirror/trivy-db:2", Registry: plain}, Registry: plain})
	require.ErrorIs(t, err, errBadDatabase, "a database by tag")

	resp, err := engine.Scan(ctx, Request{
		Database: Database{Ref: db, Registry: plain},
		Registry: plain,
		Images:   []Image{{Ref: "oci://" + host + "/missing/app@sha256:" + strings.Repeat("a", 64)}},
	})
	require.NoError(t, err)
	assert.Contains(t, resp.Images[0].Error, "pulling image")
}

func TestParsePlatform(t *testing.T) {
	p, err := parsePlatform("linux/arm64/v8")
	require.NoError(t, err)
	assert.Equal(t, testLinux, p.OS)
	assert.Equal(t, "arm64", p.Architecture)
	assert.Equal(t, "v8", p.Variant)

	p, err = parsePlatform("")
	require.NoError(t, err)
	assert.Nil(t, p)

	for _, bad := range []string{"linux", "/amd64", "linux/arm64/v8/extra"} {
		_, err := parsePlatform(bad)
		assert.Error(t, err, bad)
	}
}

func TestTrivy_lock(t *testing.T) {
	var engine Trivy
	unlockJava := engine.lock(javaDBDir)

	other := make(chan struct{})
	go func() {
		engine.lock(dbDir)()
		close(other)
	}()
	select {
	case <-other:
	case <-time.After(5 * time.Second):
		t.Fatal("a download of the Java database holds up the vulnerability database")
	}

	again := make(chan struct{})
	go func() {
		engine.lock(javaDBDir)()
		close(again)
	}()
	select {
	case <-again:
		t.Fatal("two downloads of the Java database at once")
	case <-time.After(50 * time.Millisecond):
	}
	unlockJava()
	<-again
}

// gatedClient holds every layer extraction until gate closes, and counts
// them.
type gatedClient struct {
	layoutClient
	gate      <-chan struct{}
	extracted *atomic.Int32
}

func (c gatedClient) ExtractLayers(ctx context.Context, ref oci.Reference, dir string) (oci.Descriptor, error) {
	<-c.gate
	c.extracted.Add(1)
	return c.layoutClient.ExtractLayers(ctx, ref, dir)
}

func TestTrivy_database_outlivesRequest(t *testing.T) {
	host := testRegistry(t)
	ref := pushDatabase(t, "oci://"+host+"/mirror/trivy-db:2", "trivy.db", "2026-10-19T07:19:54Z")
	db := Database{Ref: ref, Registry: Connection{PlainHTTP: true}}

	engine := newTestTrivy(t, &fakeTrivy{})
	gate := make(chan struct{})
	var extracted atomic.Int32
	connect := engine.connect
	engine.connect = func(c Connection) (layoutClient, error) {
		client, err := connect(c)
		return gatedClient{layoutClient: client, gate: gate, extracted: &extracted}, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := engine.database(ctx, db, dbDir, "db", "trivy.db")
	require.ErrorIs(t, err, context.Canceled, "a request that ends stops waiting")

	close(gate)
	dir, updated, err := engine.database(context.Background(), db, dbDir, "db", "trivy.db")
	require.NoError(t, err, "the next request finds the download its predecessor started")
	assert.FileExists(t, filepath.Join(dir, "trivy.db"))
	assert.Equal(t, 2026, updated.Year())
	assert.Equal(t, int32(1), extracted.Load(), "downloaded once")
}
