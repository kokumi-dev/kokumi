package artifact_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/images"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/registry"
)

const (
	testManifestFile      = "manifest.yaml"
	testKustomizationFile = "kustomization.yaml"
	testKustomization     = "resources:\n- deployment.yaml\n"

	nginxDigest       = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	workloadYAML      = "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  template:\n    spec:\n      containers:\n        - name: web\n          image: nginx:1.27\n"
	nginxVendoredRepo = "registry.svc.cluster.local:5000/preparation/app/images/docker.io/library/nginx"
	vendoredNginx     = nginxVendoredRepo + ":1.27@" + nginxDigest
)

// filesClient pulls an artifact made of files and captures what is pushed.
type filesClient struct {
	*capturingFakeClient
	files map[string]string
}

func (c *filesClient) Pull(_ context.Context, _ oci.Reference, targetDir string) (string, string, map[string]string, error) {
	for name, content := range c.files {
		if err := afero.WriteFile(c.fs, filepath.Join(targetDir, name), []byte(content), 0600); err != nil {
			return "", "", nil, err
		}
	}
	return "", fakeDigest, nil, nil
}

// imageScope connects to Docker Hub, the registry of the test images,
// through client, and everywhere else through the store's client.
func imageScope(client oci.Client) registry.Scope {
	return registry.StaticScope(map[string]oci.Client{"docker.io": client})
}

// imageRegistry resolves nginx:1.27 and records copies.
func imageRegistry() *oci.FakeClient {
	return &oci.FakeClient{Resolved: map[string]oci.Descriptor{
		"docker.io/library/nginx:1.27": {Digest: nginxDigest, MediaType: ocispec.MediaTypeImageIndex},
	}}
}

func newFilesPipeline(files map[string]string) (*artifact.Pipeline, *filesClient) {
	fs := afero.NewMemMapFs()
	client := &filesClient{capturingFakeClient: &capturingFakeClient{fs: fs}, files: files}
	return artifact.NewPipeline(artifact.NewStore(client, fs, "")), client
}

func TestPipeline_Render_images(t *testing.T) {
	t.Run("record lists the images and leaves the manifests unchanged", func(t *testing.T) {
		p, client := newFilesPipeline(map[string]string{testManifestFile: workloadYAML})

		req := baseRenderRequest()
		req.Product = imageScope(imageRegistry())

		result, err := p.Render(context.Background(), req)
		require.NoError(t, err)

		want := []images.Image{{Ref: "nginx:1.27", Name: "docker.io/library/nginx", Digest: nginxDigest, MediaType: ocispec.MediaTypeImageIndex}}
		assert.Equal(t, want, result.Images)
		assert.Equal(t, want, result.Config.Images)
		assert.Contains(t, client.lastPushedFiles[testManifestFile], "image: nginx:1.27\n")
	})

	t.Run("pin rewrites the manifests to digests", func(t *testing.T) {
		p, client := newFilesPipeline(map[string]string{testManifestFile: workloadYAML})

		req := baseRenderRequest()
		req.Images = artifact.ImageOptions{Policy: images.PolicyPin}
		req.Product = imageScope(imageRegistry())

		_, err := p.Render(context.Background(), req)
		require.NoError(t, err)
		assert.Contains(t, client.lastPushedFiles[testManifestFile], "image: nginx:1.27@"+nginxDigest+"\n")
	})

	t.Run("vendor copies each image into its repository below the vendor location", func(t *testing.T) {
		p, client := newFilesPipeline(map[string]string{testManifestFile: workloadYAML})
		registry := imageRegistry()

		req := baseRenderRequest()
		req.Images = artifact.ImageOptions{Policy: images.PolicyVendor, VendorURL: testDestOCI}
		req.Product = imageScope(registry)

		result, err := p.Render(context.Background(), req)
		require.NoError(t, err)

		require.Len(t, registry.Copies, 1)
		assert.Equal(t, "docker.io/library/nginx@"+nginxDigest, registry.Copies[0].Source.String())
		assert.Equal(t, nginxVendoredRepo+":1.27", registry.Copies[0].Target.String(), "into the image's own repository, under its tag")
		assert.Equal(t, vendoredNginx, result.Images[0].Vendored)
		assert.Contains(t, client.lastPushedFiles[testManifestFile], "image: "+vendoredNginx+"\n")
	})

	t.Run("pin fails without pushing when an image cannot be resolved", func(t *testing.T) {
		p, client := newFilesPipeline(map[string]string{testManifestFile: workloadYAML})

		req := baseRenderRequest()
		req.Images = artifact.ImageOptions{Policy: images.PolicyPin}
		req.Product = imageScope(&oci.FakeClient{Resolved: map[string]oci.Descriptor{}})

		_, err := p.Render(context.Background(), req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nginx:1.27")
		assert.Nil(t, client.lastPushedFiles)
	})

	t.Run("pin fails on a source with a kustomization file", func(t *testing.T) {
		p, _ := newFilesPipeline(map[string]string{
			testKustomizationFile: testKustomization,
			testDeploymentFile:    workloadYAML,
		})

		req := baseRenderRequest()
		req.Images = artifact.ImageOptions{Policy: images.PolicyPin}
		req.Product = imageScope(imageRegistry())

		_, err := p.Render(context.Background(), req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "kustomization file")
	})

	t.Run("record finds images in every file of a kustomization", func(t *testing.T) {
		p, _ := newFilesPipeline(map[string]string{
			testKustomizationFile: testKustomization,
			testDeploymentFile:    workloadYAML,
		})

		req := baseRenderRequest()
		req.Product = imageScope(imageRegistry())

		result, err := p.Render(context.Background(), req)
		require.NoError(t, err)
		require.Len(t, result.Images, 1)
		assert.Equal(t, nginxDigest, result.Images[0].Digest)
	})
}

func TestPipeline_Preview_images(t *testing.T) {
	previewRequest := func(vendorURL string, client oci.Client) artifact.PreviewRequest {
		return artifact.PreviewRequest{
			Source:    artifact.Source{OCI: testSourceOCI, Version: testVersion},
			Product:   imageScope(client),
			Images:    artifact.ImageOptions{Policy: images.PolicyVendor, VendorURL: vendorURL},
			Name:      testAppName,
			Namespace: testNamespaceName,
		}
	}

	t.Run("vendor shows the vendored location without copying", func(t *testing.T) {
		p, _ := newFilesPipeline(map[string]string{testManifestFile: workloadYAML})
		registry := imageRegistry()

		files, err := p.Preview(context.Background(), previewRequest(testDestOCI, registry))
		require.NoError(t, err)

		require.Len(t, files, 1)
		assert.Contains(t, files[0].Content, "image: "+vendoredNginx+"\n")
		assert.Empty(t, registry.Copies)
	})

	t.Run("vendor without a vendor location pins", func(t *testing.T) {
		p, _ := newFilesPipeline(map[string]string{testManifestFile: workloadYAML})

		files, err := p.Preview(context.Background(), previewRequest("", imageRegistry()))
		require.NoError(t, err)
		assert.Contains(t, files[0].Content, "image: nginx:1.27@"+nginxDigest+"\n")
	})

	t.Run("unresolved images stay as written", func(t *testing.T) {
		p, _ := newFilesPipeline(map[string]string{testManifestFile: workloadYAML})

		files, err := p.Preview(context.Background(), previewRequest(testDestOCI, &oci.FakeClient{Resolved: map[string]oci.Descriptor{}}))
		require.NoError(t, err)
		assert.Contains(t, files[0].Content, "image: nginx:1.27\n")
	})

	t.Run("record looks nothing up", func(t *testing.T) {
		p, _ := newFilesPipeline(map[string]string{testManifestFile: workloadYAML})
		registry := imageRegistry()
		req := previewRequest(testDestOCI, registry)
		req.Images.Policy = images.PolicyRecord

		files, err := p.Preview(context.Background(), req)
		require.NoError(t, err)
		assert.Contains(t, files[0].Content, "image: nginx:1.27\n")
		assert.Empty(t, registry.Resolves)
	})

	t.Run("lookups stop at the time limit and leave the references as written", func(t *testing.T) {
		p, _ := newFilesPipeline(map[string]string{testManifestFile: workloadYAML})
		p.PreviewImagesTimeout = 50 * time.Millisecond
		req := previewRequest(testDestOCI, stallingClient{FakeClient: &oci.FakeClient{}})
		req.Images.Policy = images.PolicyPin

		start := time.Now()
		files, err := p.Preview(context.Background(), req)
		require.NoError(t, err)
		assert.Less(t, time.Since(start), 5*time.Second)
		assert.Contains(t, files[0].Content, "image: nginx:1.27\n")
	})
}

// stallingClient is a registry that never answers a lookup.
type stallingClient struct {
	*oci.FakeClient
}

func (stallingClient) Resolve(ctx context.Context, _ oci.Reference) (oci.Descriptor, error) {
	<-ctx.Done()
	return oci.Descriptor{}, ctx.Err()
}
