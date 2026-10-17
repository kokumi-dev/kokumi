package images

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kokumi-dev/kokumi/internal/oci"
)

const (
	appName         = "ghcr.io/example/app"
	nginxRef        = "nginx:1.27"
	dockerHub       = "docker.io"
	prometheusKind  = "Prometheus"
	monitoringGroup = "monitoring.coreos.com"

	nginxDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	appDigest   = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	movedDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

	nginxName   = "docker.io/library/nginx"
	bundled     = "kokumi-registry.kokumi.svc.cluster.local:5000/shop/web"
	bundledURL  = "oci://" + bundled
	nginxSource = nginxName + ":1.27"
)

const stageManifest = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  template:
    spec:
      containers:
        - name: web
          image: nginx:1.27
        - name: app
          image: ghcr.io/example/app:2.0
        - name: app-again
          image: ghcr.io/example/app@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
`

// registry returns a FakeClient that resolves the images of stageManifest.
func registry() *oci.FakeClient {
	return &oci.FakeClient{Resolved: map[string]oci.Descriptor{
		"docker.io/library/nginx:1.27":     {Digest: nginxDigest, MediaType: ocispec.MediaTypeImageIndex},
		"ghcr.io/example/app:2.0":          {Digest: appDigest, MediaType: ocispec.MediaTypeImageManifest},
		"ghcr.io/example/app@" + appDigest: {Digest: appDigest, MediaType: ocispec.MediaTypeImageManifest},
	}}
}

func parse(t *testing.T, content string) *Manifest {
	t.Helper()
	manifest, err := ParseManifest([]byte(content), nil)
	require.NoError(t, err)
	return manifest
}

// copies returns the copies client made, as "source -> target".
func copies(client *oci.FakeClient) []string {
	out := make([]string, 0, len(client.Copies))
	for _, c := range client.Copies {
		out = append(out, c.Source.String()+" -> "+c.Target.String())
	}
	return out
}

func encode(t *testing.T, manifest *Manifest) string {
	t.Helper()
	data, err := manifest.Encode()
	require.NoError(t, err)
	return string(data)
}

func TestStage_recordResolvesWithoutRewriting(t *testing.T) {
	client := registry()
	manifest := parse(t, stageManifest+`        - name: missing
          image: ghcr.io/example/missing:1
        - name: broken
          image: "UPPER:case"
`)
	before := encode(t, manifest)

	images, err := Stage{Client: client}.Run(context.Background(), []*Manifest{manifest})
	require.NoError(t, err)

	assert.Equal(t, before, encode(t, manifest))
	require.Len(t, images, 5)
	assert.Equal(t, Image{Ref: "UPPER:case", Error: images[0].Error}, images[0])
	assert.Contains(t, images[0].Error, "invalid image reference")
	assert.Equal(t, Image{Ref: "ghcr.io/example/app:2.0", Name: appName, Digest: appDigest, MediaType: ocispec.MediaTypeImageManifest}, images[1])
	assert.Equal(t, Image{Ref: "ghcr.io/example/app@" + appDigest, Name: appName, Digest: appDigest, MediaType: ocispec.MediaTypeImageManifest}, images[2])
	assert.Equal(t, "ghcr.io/example/missing:1", images[3].Ref)
	assert.Empty(t, images[3].Digest)
	assert.Contains(t, images[3].Error, "not found")
	assert.Equal(t, Image{Ref: nginxRef, Name: "docker.io/library/nginx", Digest: nginxDigest, MediaType: ocispec.MediaTypeImageIndex}, images[4])
}

func TestStage_pinKeepsNameAndTag(t *testing.T) {
	manifest := parse(t, stageManifest)

	images, err := Stage{Policy: PolicyPin, Client: registry()}.Run(context.Background(), []*Manifest{manifest})
	require.NoError(t, err)
	require.Len(t, images, 3)

	out := encode(t, manifest)
	assert.Contains(t, out, "image: nginx:1.27@"+nginxDigest+"\n")
	assert.Contains(t, out, "image: ghcr.io/example/app:2.0@"+appDigest+"\n")
	assert.Contains(t, out, "image: ghcr.io/example/app@"+appDigest+"\n")
}

func TestStage_pinFailsOnUnresolvedImages(t *testing.T) {
	manifest := parse(t, stageManifest+`        - name: missing
          image: ghcr.io/example/missing:1
`)

	_, err := Stage{Policy: PolicyPin, Client: registry()}.Run(context.Background(), []*Manifest{manifest})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ghcr.io/example/missing:1")
	assert.Contains(t, err.Error(), "Pin image policy")
}

func TestStage_vendorCopiesEachDigestOnceAndRewrites(t *testing.T) {
	client := registry()
	dest := &oci.FakeClient{}
	destination, err := oci.Parse("oci://kokumi-registry.kokumi.svc.cluster.local:5000/shop/web")
	require.NoError(t, err)
	destination.Tag = "1.0.0"

	manifest := parse(t, stageManifest)
	images, err := Stage{Policy: PolicyVendor, Client: client, Destination: &destination, DestClient: dest}.Run(context.Background(), []*Manifest{manifest})
	require.NoError(t, err)

	vendoredApp := "kokumi-registry.kokumi.svc.cluster.local:5000/shop/web@" + appDigest
	vendoredNginx := "kokumi-registry.kokumi.svc.cluster.local:5000/shop/web@" + nginxDigest

	assert.ElementsMatch(t, []string{
		"ghcr.io/example/app@" + appDigest + " -> " + vendoredApp,
		"docker.io/library/nginx@" + nginxDigest + " -> " + vendoredNginx,
	}, copies(client))
	for _, c := range client.Copies {
		assert.True(t, c.Options.IncludeReferrers)
	}

	assert.Equal(t, vendoredApp, images[0].Vendored)
	assert.Equal(t, vendoredApp, images[1].Vendored)
	assert.Equal(t, vendoredNginx, images[2].Vendored)

	out := encode(t, manifest)
	assert.Contains(t, out, "image: "+vendoredNginx+"\n")
	assert.Contains(t, out, "image: "+vendoredApp+"\n")
	assert.NotContains(t, out, "ghcr.io")
}

func TestStage_dryRunNeverCopiesOrFails(t *testing.T) {
	client := registry()
	destination, err := oci.Parse("oci://registry.example.com/shop/web")
	require.NoError(t, err)

	manifest := parse(t, stageManifest+`        - name: missing
          image: ghcr.io/example/missing:1
`)
	_, err = Stage{Policy: PolicyVendor, Client: client, Destination: &destination, DryRun: true}.Run(context.Background(), []*Manifest{manifest})
	require.NoError(t, err)

	assert.Empty(t, client.Copies)
	out := encode(t, manifest)
	assert.Contains(t, out, "image: registry.example.com/shop/web@"+nginxDigest+"\n")
	assert.Contains(t, out, "image: ghcr.io/example/missing:1\n")
}

func TestStage_vendorWithoutDestinationPins(t *testing.T) {
	client := registry()
	manifest := parse(t, stageManifest)

	_, err := Stage{Policy: PolicyVendor, Client: client, DryRun: true}.Run(context.Background(), []*Manifest{manifest})
	require.NoError(t, err)

	assert.Contains(t, encode(t, manifest), "image: nginx:1.27@"+nginxDigest+"\n")
}

func TestStage_mirrorsRedirectLookupsAndCopies(t *testing.T) {
	client := &oci.FakeClient{Resolved: map[string]oci.Descriptor{
		"harbor.example.com/dockerhub/library/nginx:1.27": {Digest: nginxDigest, MediaType: ocispec.MediaTypeImageIndex},
	}}
	destination, err := oci.Parse("oci://registry.example.com/shop/web")
	require.NoError(t, err)

	manifest := parse(t, `apiVersion: v1
kind: Pod
metadata:
  name: web
spec:
  containers:
    - name: web
      image: nginx:1.27
`)
	images, err := Stage{
		Policy:      PolicyVendor,
		Client:      client,
		Mirrors:     []Mirror{{Prefix: dockerHub, Location: "harbor.example.com/dockerhub"}, {Prefix: "docker.io/other", Location: "unused.example.com"}},
		Destination: &destination,
		DestClient:  &oci.FakeClient{},
	}.Run(context.Background(), []*Manifest{manifest})
	require.NoError(t, err)

	require.Len(t, images, 1)
	assert.Equal(t, "docker.io/library/nginx", images[0].Name, "the recorded name stays the original")
	require.Len(t, client.Copies, 1)
	assert.Equal(t, "harbor.example.com/dockerhub/library/nginx@"+nginxDigest, client.Copies[0].Source.String())
}

func TestParseRef(t *testing.T) {
	tests := []struct {
		ref  string
		want imageRef
	}{
		{"nginx", imageRef{name: "docker.io/library/nginx", tag: "latest"}},
		{"bitnami/redis:7", imageRef{name: "docker.io/bitnami/redis", tag: "7"}},
		{"localhost:5000/app:1", imageRef{name: "localhost:5000/app", tag: "1"}},
		{"ghcr.io/org/app:1@" + appDigest, imageRef{name: "ghcr.io/org/app", tag: "1", digest: appDigest}},
		{"ghcr.io/org/app@" + appDigest, imageRef{name: "ghcr.io/org/app", digest: appDigest}},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			got, err := parseRef(tt.ref)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	_, err := parseRef("Not/A:Valid:Ref")
	require.Error(t, err)
}

func TestMatchMirror(t *testing.T) {
	mirrors := []Mirror{
		{Prefix: dockerHub, Location: "mirror.example.com/hub/"},
		{Prefix: "docker.io/library/", Location: "mirror.example.com/library"},
		{Prefix: "ghcr.io/org", Location: "mirror.example.com/org"},
	}

	m, ok := matchMirror(mirrors, "docker.io/library/nginx")
	require.True(t, ok)
	assert.Equal(t, Mirror{Prefix: "docker.io/library", Location: "mirror.example.com/library"}, m)

	m, ok = matchMirror(mirrors, "docker.io/bitnami/redis")
	require.True(t, ok)
	assert.Equal(t, Mirror{Prefix: dockerHub, Location: "mirror.example.com/hub"}, m)

	_, ok = matchMirror(mirrors, "ghcr.io/organization/app")
	assert.False(t, ok, "prefixes match at path boundaries only")

	ref, err := imageRef{name: "ghcr.io/org", tag: "1"}.source(mirrors)
	require.NoError(t, err)
	assert.Equal(t, "mirror.example.com/org:1", ref.String())
}

// podManifest returns a Pod with one container per reference.
func podManifest(refs ...string) string {
	var b strings.Builder
	b.WriteString("apiVersion: v1\nkind: Pod\nmetadata:\n  name: web\nspec:\n  containers:\n")
	for i, ref := range refs {
		fmt.Fprintf(&b, "    - name: c%d\n      image: %s\n", i, ref)
	}
	return b.String()
}

// bundledDestination returns the bundled registry repository of shop/web.
func bundledDestination(t *testing.T) *oci.Reference {
	t.Helper()
	ref, err := oci.Parse(bundledURL)
	require.NoError(t, err)
	return &ref
}

func TestStage_rejectsMoreImagesThanAPreparationRecords(t *testing.T) {
	refs := make([]string, 0, MaxImages+1)
	for i := range MaxImages + 1 {
		refs = append(refs, fmt.Sprintf("ghcr.io/example/app:%d", i))
	}
	client := registry()

	_, err := Stage{Client: client}.Run(context.Background(), []*Manifest{parse(t, podManifest(refs...))})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "513 images")
	assert.Empty(t, client.Resolves, "nothing is looked up")
}

func TestStage_rejectsOverlongReferences(t *testing.T) {
	client := registry()
	manifest := parse(t, podManifest(nginxRef, "ghcr.io/example/"+strings.Repeat("a", MaxRefLength)+":1"))

	_, err := Stage{Client: client}.Run(context.Background(), []*Manifest{manifest})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "longer than 1024 characters")
	assert.Empty(t, client.Resolves, "nothing is looked up")
}

func TestStage_unreachableRegistryIsTriedOnce(t *testing.T) {
	dial := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("i/o timeout")}
	client := registry()
	client.ResolveErrs = map[string]error{
		"dead.example.com/a:1": dial,
		"dead.example.com/b:1": dial,
		"dead.example.com/c:1": dial,
	}
	manifest := parse(t, podManifest(
		"dead.example.com/a:1", "dead.example.com/b:1", "dead.example.com/c:1",
		"ghcr.io/example/app:2.0", "ghcr.io/example/gone:1", "ghcr.io/example/gone:2",
	))

	images, err := Stage{Client: client}.Run(context.Background(), []*Manifest{manifest})
	require.NoError(t, err)

	tried := map[string]int{}
	for _, ref := range client.Resolves {
		tried[ref.Registry]++
	}
	assert.Equal(t, 1, tried["dead.example.com"], "a registry that never answers is tried once")
	assert.Equal(t, 3, tried["ghcr.io"], "a registry that answers is tried for every image")

	require.Len(t, images, 6)
	assert.Contains(t, images[0].Error, "i/o timeout")
	assert.Contains(t, images[1].Error, "registry dead.example.com is unreachable")
	assert.Contains(t, images[1].Error, "i/o timeout")
	assert.Contains(t, images[2].Error, "registry dead.example.com is unreachable")
	assert.Contains(t, images[4].Error, "not found")
}

func TestStage_pinKeepsLockedDigests(t *testing.T) {
	client := registry()
	manifest := parse(t, stageManifest)
	locked := []Image{{Ref: nginxRef, Name: nginxName, Digest: movedDigest, MediaType: ocispec.MediaTypeImageIndex}}

	images, err := Stage{Policy: PolicyPin, Client: client, Locked: locked}.Run(context.Background(), []*Manifest{manifest})
	require.NoError(t, err)

	require.Len(t, images, 3)
	assert.Equal(t, Image{Ref: nginxRef, Name: nginxName, Digest: movedDigest, MediaType: ocispec.MediaTypeImageIndex}, images[2])
	assert.Contains(t, encode(t, manifest), "image: nginx:1.27@"+movedDigest+"\n")
	assert.Len(t, client.Resolves, 2, "only the references that are not locked are looked up")
	for _, ref := range client.Resolves {
		assert.NotEqual(t, nginxSource, ref.String())
	}
}

func TestStage_recordIgnoresLocks(t *testing.T) {
	client := registry()
	locked := []Image{{Ref: nginxRef, Name: nginxName, Digest: movedDigest}}

	images, err := Stage{Client: client, Locked: locked}.Run(context.Background(), []*Manifest{parse(t, stageManifest)})
	require.NoError(t, err)

	assert.Equal(t, nginxDigest, images[2].Digest)
	assert.Len(t, client.Resolves, 3)
}

func TestStage_vendorSkipsImagesTheLockVendoredAlready(t *testing.T) {
	client := registry()
	dest := &oci.FakeClient{}
	vendoredNginx := bundled + "@" + nginxDigest
	locked := []Image{{Ref: nginxRef, Name: nginxName, Digest: nginxDigest, MediaType: ocispec.MediaTypeImageIndex, Vendored: vendoredNginx}}

	images, err := Stage{
		Policy: PolicyVendor, Client: client, Destination: bundledDestination(t), DestClient: dest, Locked: locked,
	}.Run(context.Background(), []*Manifest{parse(t, stageManifest)})
	require.NoError(t, err)

	assert.Equal(t, vendoredNginx, images[2].Vendored)
	assert.Equal(t, []string{"ghcr.io/example/app@" + appDigest + " -> " + bundled + "@" + appDigest}, copies(client))
	require.Len(t, dest.Resolves, 1)
	assert.Equal(t, vendoredNginx, dest.Resolves[0].String(), "the vendored copy is checked to be still there")
}

func TestStage_vendorCopiesLockedImagesThatAreGone(t *testing.T) {
	client := registry()
	dest := &oci.FakeClient{Resolved: map[string]oci.Descriptor{}}
	locked := []Image{{Ref: nginxRef, Name: nginxName, Digest: nginxDigest, Vendored: bundled + "@" + nginxDigest}}

	_, err := Stage{
		Policy: PolicyVendor, Client: client, Destination: bundledDestination(t), DestClient: dest, Locked: locked,
	}.Run(context.Background(), []*Manifest{parse(t, stageManifest)})
	require.NoError(t, err)

	assert.Contains(t, copies(client), nginxName+"@"+nginxDigest+" -> "+bundled+"@"+nginxDigest)
}

func TestStage_vendorCopiesLockedImagesToANewDestination(t *testing.T) {
	client := registry()
	locked := []Image{{Ref: nginxRef, Name: nginxName, Digest: movedDigest, Vendored: "registry.example.com/old/web@" + movedDigest}}

	_, err := Stage{
		Policy: PolicyVendor, Client: client, Destination: bundledDestination(t), DestClient: &oci.FakeClient{}, Locked: locked,
	}.Run(context.Background(), []*Manifest{parse(t, stageManifest)})
	require.NoError(t, err)

	assert.Contains(t, copies(client), nginxName+"@"+movedDigest+" -> "+bundled+"@"+movedDigest,
		"the locked digest is copied from its source")
	for _, ref := range client.Resolves {
		assert.NotEqual(t, nginxSource, ref.String())
	}
}

// barrierClient holds every Copy until maxParallelCopies copies are in
// flight, so a stage that copies one image at a time never gets through.
type barrierClient struct {
	*oci.FakeClient
	inFlight, peak atomic.Int32
	full           chan struct{}
	once           sync.Once
}

func (c *barrierClient) Copy(ctx context.Context, target oci.Client, src, dst oci.Reference, opts oci.CopyOptions) error {
	n := c.inFlight.Add(1)
	defer c.inFlight.Add(-1)
	for p := c.peak.Load(); n > p && !c.peak.CompareAndSwap(p, n); p = c.peak.Load() {
	}
	if n == maxParallelCopies {
		c.once.Do(func() { close(c.full) })
	}

	select {
	case <-c.full:
	case <-time.After(5 * time.Second):
		return errors.New("copies did not run in parallel")
	}
	return c.FakeClient.Copy(ctx, target, src, dst, opts)
}

func TestStage_vendorCopiesInParallel(t *testing.T) {
	resolved := map[string]oci.Descriptor{}
	var refs []string
	for i := 1; i <= maxParallelCopies+1; i++ {
		ref := fmt.Sprintf("ghcr.io/example/app%d:1", i)
		resolved[ref] = oci.Descriptor{Digest: "sha256:" + strings.Repeat(strconv.Itoa(i), 64), MediaType: ocispec.MediaTypeImageManifest}
		refs = append(refs, ref)
	}
	client := &barrierClient{FakeClient: &oci.FakeClient{Resolved: resolved}, full: make(chan struct{})}

	_, err := Stage{
		Policy: PolicyVendor, Client: client, Destination: bundledDestination(t), DestClient: &oci.FakeClient{},
	}.Run(context.Background(), []*Manifest{parse(t, podManifest(refs...))})
	require.NoError(t, err)

	assert.Len(t, client.Copies, maxParallelCopies+1)
	assert.Equal(t, int32(maxParallelCopies), client.peak.Load())
}

func TestStage_vendorFailsWhenACopyFails(t *testing.T) {
	client := registry()
	client.CopyErr = errors.New("blob upload refused")

	_, err := Stage{
		Policy: PolicyVendor, Client: client, Destination: bundledDestination(t), DestClient: &oci.FakeClient{},
	}.Run(context.Background(), []*Manifest{parse(t, stageManifest)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blob upload refused")
}
