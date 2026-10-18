package oci

import (
	"archive/tar"
	"cmp"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3"
	"github.com/oras-project/oras-go/v3/content"
	"github.com/oras-project/oras-go/v3/content/file"
	"github.com/oras-project/oras-go/v3/registry/remote"
	"github.com/oras-project/oras-go/v3/registry/remote/auth"
	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/oras-project/oras-go/v3/registry/remote/errcode"
	"github.com/oras-project/oras-go/v3/registry/remote/retry"
	ctrl "sigs.k8s.io/controller-runtime"
)

// ORASClient implements Client using the ORAS library. It talks to the
// registries of one connection: its credential, scheme and TLS settings apply
// to every repository it accesses.
type ORASClient struct {
	// authClient authenticates every repository of the client, sharing
	// one token cache. Nil means anonymous, through auth.DefaultClient.
	authClient *auth.Client
	// plainHTTP accesses registries over HTTP instead of HTTPS.
	plainHTTP bool
	// anonymous is set when the client has no credential.
	anonymous bool
}

var _ Client = (*ORASClient)(nil)

// Options configures an ORASClient. The zero value is an anonymous client
// that uses HTTPS with the system trust store.
type Options struct {
	// Host is the registry host[:port] Credential belongs to. Requests to
	// any other host are sent without it.
	Host string
	// Credential authenticates the requests to Host. The zero value is
	// anonymous.
	Credential credentials.Credential
	// PlainHTTP accesses the registry over HTTP instead of HTTPS.
	PlainHTTP bool
	// TLSConfig configures HTTPS connections, for example to trust a private
	// CA or to present a client certificate. Nil uses the system defaults.
	TLSConfig *tls.Config
}

// NewClient returns an ORASClient for opts.
func NewClient(opts Options) *ORASClient {
	anonymous := opts.Credential == credentials.EmptyCredential
	if anonymous && opts.TLSConfig == nil {
		return &ORASClient{plainHTTP: opts.PlainHTTP, anonymous: true}
	}

	httpClient := retry.DefaultClient
	if opts.TLSConfig != nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = opts.TLSConfig
		httpClient = &http.Client{Transport: retry.NewTransport(transport)}
	}

	return &ORASClient{authClient: newAuthClient(httpClient, opts.Host, opts.Credential), plainHTTP: opts.PlainHTTP, anonymous: anonymous}
}

// newRepository creates a configured remote.Repository for the given ref,
// applying plain-HTTP and credential settings.
func (c *ORASClient) newRepository(ref string) (*remote.Repository, error) {
	repo, err := remote.NewRepository(ref)
	if err != nil {
		return nil, err
	}

	repo.Registry.PlainHTTP = c.plainHTTP

	if c.authClient != nil {
		repo.Registry.Client = c.authClient
	}

	return repo, nil
}

// newAuthClient returns an auth client that sends cred to host. Like
// auth.DefaultClient, which anonymous clients use, it caches tokens and
// retries rate-limited and failed requests. It keeps the distribution token
// flow for username/password credentials; oras-go v3 would otherwise default
// to the OAuth2 password grant, which not every registry serves.
func newAuthClient(httpClient *http.Client, host string, cred credentials.Credential) *auth.Client {
	client := &auth.Client{
		Client: httpClient,
		Header: auth.DefaultClient.Header.Clone(),
		Cache:  auth.NewCache(),
	}
	if cred != credentials.EmptyCredential {
		host = CanonicalHost(host)
		client.CredentialFunc = func(_ context.Context, hostport string) (credentials.Credential, error) {
			if CanonicalHost(hostport) != host {
				return credentials.EmptyCredential, nil
			}
			return cred, nil
		}
	}
	client.TokenFetcher = auth.NewCompositeTokenFetcher(client.Client, client.Header, client.ClientID, true)

	return client
}

// dockerHubHost is the canonical host of Docker Hub, which is also reached
// as index.docker.io and registry-1.docker.io.
const dockerHubHost = "docker.io"

// CanonicalHost returns host in lower case with the aliases of Docker Hub
// mapped to docker.io, so that the hosts of one registry compare equal.
func CanonicalHost(host string) string {
	host = strings.ToLower(host)
	switch host {
	case "index.docker.io", "registry-1.docker.io":
		return dockerHubHost
	}
	return host
}

// Ping checks that the registry at host answers its API base endpoint with
// this client's credential, scheme and TLS settings.
func (c *ORASClient) Ping(ctx context.Context, host string) error {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		return fmt.Errorf("create registry client for %q: %w", host, err)
	}
	reg.PlainHTTP = c.plainHTTP
	if c.anonymous {
		if err := c.pingAnonymous(ctx, reg.Reference.Host()); err != nil {
			return fmt.Errorf("ping %s: %w", host, err)
		}
		return nil
	}
	if c.authClient != nil {
		reg.Client = c.authClient
	}

	if err := reg.Ping(ctx); err != nil {
		return fmt.Errorf("ping %s: %w", host, err)
	}
	return nil
}

// pingAnonymous checks that the registry at host answers its API base
// endpoint, without logging in. A registry that asks for a bearer token
// there still decides about anonymous access per repository: ghcr.io, for
// example, denies an anonymous token for the base endpoint but grants one
// for every public repository. Only a registry that asks for basic
// credentials refuses anonymous access altogether.
func (c *ORASClient) pingAnonymous(ctx context.Context, host string) error {
	scheme := "https"
	if c.plainHTTP {
		scheme = "http"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, scheme+"://"+host+"/v2/", nil)
	if err != nil {
		return err
	}

	httpClient := retry.DefaultClient
	if c.authClient != nil {
		httpClient = c.authClient.Client
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		challenge := strings.ToLower(resp.Header.Get("WWW-Authenticate"))
		if strings.HasPrefix(challenge, "bearer") {
			return nil
		}
		return fmt.Errorf("GET %q: %w", req.URL, auth.ErrBasicCredentialNotFound)
	default:
		return &errcode.ErrorResponse{Method: req.Method, URL: req.URL, StatusCode: resp.StatusCode}
	}
}

// Pull fetches an OCI artifact from ref:tag into targetDir.
// It inspects the manifest's first layer media type and branches accordingly:
//   - HelmChartLayerMediaType: fetches the blob directly to targetDir/chart.tgz
//   - FluxContentMediaType:    extracts the layer's tar archive into targetDir
//     (Flux layers carry no io.deis.oras.content.unpack annotation, so ORAS
//     would otherwise store them as a raw blob)
//   - anything else:           uses oras.Copy with a file store, which unpacks
//     layers annotated with io.deis.oras.content.unpack (e.g. ORAS "file"
//     artifacts) and otherwise stores raw blobs
//
// The first return value is the layer media type (empty string for non-Helm artifacts).
// The third return value is the manifest annotations map (may be nil/empty).
func (c *ORASClient) Pull(ctx context.Context, ref Reference, targetDir string) (string, string, map[string]string, error) {
	log := ctrl.LoggerFrom(ctx)

	repository := ref.RepositoryReference()
	reference := ref.GetReference()

	repo, err := c.newRepository(repository)
	if err != nil {
		return "", "", nil, fmt.Errorf("create repository for %q: %w", ref, err)
	}
	log.Info("Resolving OCI manifest", "ref", fmt.Sprintf("%s:%s", ref, reference))

	manifestDesc, err := repo.Resolve(ctx, reference)
	if err != nil {
		return "", "", nil, fmt.Errorf("resolve %s:%s: %w", ref, reference, err)
	}

	if isIndex(manifestDesc.MediaType) {
		return "", "", nil, fmt.Errorf("pull %s:%s: %w", ref, reference, ErrIsIndex)
	}

	rc, err := repo.Fetch(ctx, manifestDesc)
	if err != nil {
		return "", "", nil, fmt.Errorf("fetch manifest %s: %w", manifestDesc.Digest, err)
	}
	defer rc.Close() //nolint:errcheck

	manifestBytes, err := io.ReadAll(rc)
	if err != nil {
		return "", "", nil, fmt.Errorf("read manifest %s: %w", manifestDesc.Digest, err)
	}

	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return "", "", nil, fmt.Errorf("parse manifest: %w", err)
	}

	digest := manifestDesc.Digest.String()
	annotations := manifest.Annotations

	if len(manifest.Layers) == 0 {
		return "", "", nil, fmt.Errorf("artifact %s:%s has no layers", ref, reference)
	}

	if manifest.Layers[0].MediaType == HelmChartLayerMediaType {
		log.Info("Pulling Helm chart blob", "ref", fmt.Sprintf("%s:%s", ref, reference))

		if err := c.fetchBlob(ctx, repo, manifest.Layers[0], filepath.Join(targetDir, "chart.tgz")); err != nil {
			return "", "", nil, fmt.Errorf("fetch helm chart blob: %w", err)
		}

		return HelmChartLayerMediaType, digest, annotations, nil
	}

	if manifest.Layers[0].MediaType == FluxContentMediaType {
		log.Info("Pulling Flux content layer", "ref", fmt.Sprintf("%s:%s", ref, reference))

		for _, layer := range manifest.Layers {
			if err := c.extractLayer(ctx, repo, layer, targetDir); err != nil {
				return "", "", nil, fmt.Errorf("extract layer %s: %w", layer.Digest, err)
			}
		}

		return "", digest, annotations, nil
	}

	log.Info("Pulling OCI artifact", "ref", fmt.Sprintf("%s:%s", ref, reference))

	fs, err := file.New(targetDir)
	if err != nil {
		return "", "", nil, fmt.Errorf("create file store at %q: %w", targetDir, err)
	}
	defer fs.Close() //nolint:errcheck

	if _, err := oras.Copy(ctx, repo, reference, fs, "", oras.DefaultCopyOptions); err != nil {
		return "", "", nil, fmt.Errorf("pull artifact %s:%s: %w", ref, reference, err)
	}

	return "", digest, annotations, nil
}

// extractLayer fetches a Flux content layer blob (gzip-compressed tar) and
// extracts its archive into targetDir, preserving file names. It is only ever
// called for layers with media type FluxContentMediaType.
func (c *ORASClient) extractLayer(ctx context.Context, repo *remote.Repository, desc ocispec.Descriptor, targetDir string) error {
	rc, err := repo.Blobs().Fetch(ctx, desc)
	if err != nil {
		return fmt.Errorf("fetch blob %s: %w", desc.Digest, err)
	}
	defer rc.Close() //nolint:errcheck

	return extractGzipTar(rc, desc.MediaType, targetDir)
}

// extractGzipTar decompresses a gzip-compressed tar stream (or a plain tar
// stream when the media type is not gzip) and extracts it into targetDir.
func extractGzipTar(r io.Reader, mediaType string, targetDir string) error {
	if strings.HasSuffix(mediaType, "+gzip") || mediaType == ocispec.MediaTypeImageLayerGzip {
		gz, err := gzip.NewReader(r)
		if err != nil {
			return fmt.Errorf("decompress blob: %w", err)
		}
		defer gz.Close() //nolint:errcheck
		r = gz
	}

	return extractTar(r, targetDir)
}

// extractTar extracts a tar archive into targetDir, rejecting entries that
// would escape the target directory.
func extractTar(r io.Reader, targetDir string) error {
	tr := tar.NewReader(r)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}

		if header.Typeflag != tar.TypeReg {
			continue
		}

		name := filepath.Clean(filepath.FromSlash(header.Name))
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return fmt.Errorf("tar entry %q escapes target directory", header.Name)
		}

		destPath := filepath.Join(targetDir, name)
		if err := os.MkdirAll(filepath.Dir(destPath), 0700); err != nil {
			return fmt.Errorf("create directory for %q: %w", name, err)
		}

		f, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600) //nolint:gosec
		if err != nil {
			return fmt.Errorf("create %q: %w", name, err)
		}

		if _, err := io.Copy(f, tr); err != nil {
			f.Close() //nolint:errcheck
			return fmt.Errorf("write %q: %w", name, err)
		}

		if err := f.Close(); err != nil {
			return fmt.Errorf("close %q: %w", name, err)
		}
	}
}

// fetchBlob streams a single OCI layer blob to the given file path.
func (c *ORASClient) fetchBlob(ctx context.Context, repo *remote.Repository, desc ocispec.Descriptor, destPath string) error {
	rc, err := repo.Blobs().Fetch(ctx, desc)
	if err != nil {
		return fmt.Errorf("fetch blob %s: %w", desc.Digest, err)
	}
	defer rc.Close() //nolint:errcheck

	f, err := os.Create(destPath) //nolint:gosec
	if err != nil {
		return fmt.Errorf("create %s: %w", destPath, err)
	}
	defer f.Close() //nolint:errcheck

	if _, err := io.Copy(f, rc); err != nil {
		return fmt.Errorf("write blob: %w", err)
	}

	return nil
}

// ListTags returns all tags available for the repository at ref.
func (c *ORASClient) ListTags(ctx context.Context, ref Reference) ([]string, error) {
	repository := ref.RepositoryReference()

	repo, err := c.newRepository(repository)
	if err != nil {
		return nil, fmt.Errorf("create repository for %q: %w", ref, err)
	}
	var tags []string
	err = repo.Tags(ctx, "", func(t []string) error {
		tags = append(tags, t...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list tags for %q: %w", ref, err)
	}

	return tags, nil
}

// Resolve resolves ref (tag or digest) to the descriptor of the manifest it
// names.
func (c *ORASClient) Resolve(ctx context.Context, ref Reference) (Descriptor, error) {
	repo, err := c.newRepository(ref.RepositoryReference())
	if err != nil {
		return Descriptor{}, fmt.Errorf("create repository for %q: %w", ref, err)
	}

	desc, err := repo.Resolve(ctx, ref.GetReference())
	if err != nil {
		return Descriptor{}, fmt.Errorf("resolve %s: %w", ref, err)
	}

	return Descriptor{MediaType: desc.MediaType, Digest: desc.Digest.String(), Size: desc.Size}, nil
}

// Copy copies the artifact at srcRef (in this client's registry) and
// everything it references to dstRef (in target's registry), preserving the
// original manifests and media types. Blobs are mounted instead of transferred
// when both repositories live in the same registry; when the registry refuses
// the mount, for example because target's credential may not read srcRef,
// oras falls back to transferring them.
func (c *ORASClient) Copy(ctx context.Context, target Client, srcRef, dstRef Reference, opts CopyOptions) error {
	dst, ok := target.(*ORASClient)
	if !ok {
		return fmt.Errorf("copy target must be an ORASClient, got %T", target)
	}

	srcRepo, err := c.newRepository(srcRef.RepositoryReference())
	if err != nil {
		return fmt.Errorf("create source repository for %q: %w", srcRef, err)
	}

	dstRepo, err := dst.newRepository(dstRef.RepositoryReference())
	if err != nil {
		return fmt.Errorf("create destination repository for %q: %w", dstRef, err)
	}

	graphOpts := oras.DefaultCopyGraphOptions
	if CanonicalHost(srcRef.Registry) == CanonicalHost(dstRef.Registry) && srcRef.Repository != dstRef.Repository {
		graphOpts.MountFrom = func(context.Context, ocispec.Descriptor) ([]string, error) {
			return []string{srcRef.Repository}, nil
		}
	}

	root, err := oras.Copy(ctx, srcRepo, srcRef.GetReference(), dstRepo, dstRef.GetReference(), oras.CopyOptions{CopyGraphOptions: graphOpts})
	if err != nil {
		return fmt.Errorf("copy %s to %s: %w", srcRef, dstRef, err)
	}

	if !opts.IncludeReferrers {
		return nil
	}

	// oras.ExtendedCopy only follows the referrers of the root, so the
	// referrers of index children are copied manifest by manifest.
	manifests, err := manifestsOf(ctx, srcRepo, root)
	if err != nil {
		return fmt.Errorf("walk %s: %w", srcRef, err)
	}

	extOpts := oras.DefaultExtendedCopyGraphOptions
	extOpts.CopyGraphOptions = graphOpts
	for _, manifest := range manifests {
		if err := oras.ExtendedCopyGraph(ctx, srcRepo, dstRepo, manifest, extOpts); err != nil {
			return fmt.Errorf("copy referrers of %s to %s: %w", manifest.Digest, dstRef, err)
		}
	}

	return nil
}

// manifestsOf returns root and every manifest reachable from it through
// image indexes.
func manifestsOf(ctx context.Context, repo *remote.Repository, root ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	manifests := []ocispec.Descriptor{root}
	if !isIndex(root.MediaType) {
		return manifests, nil
	}

	children, err := content.Successors(ctx, repo, root)
	if err != nil {
		return nil, err
	}

	for _, child := range children {
		nested, err := manifestsOf(ctx, repo, child)
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, nested...)
	}

	return manifests, nil
}

// Push packages sourceDir as an OCI artifact and pushes it to ref:tag, returning its digest.
func (c *ORASClient) Push(ctx context.Context, ref Reference, sourceDir string, opts PushOptions) (string, error) {
	if len(opts.Config) > 0 && opts.ConfigMediaType == "" {
		return "", errors.New("config media type is required when a config is set")
	}

	log := ctrl.LoggerFrom(ctx)

	repository := ref.RepositoryReference()
	reference := ref.GetReference()

	repo, err := c.newRepository(repository)
	if err != nil {
		return "", fmt.Errorf("failed to create repository for %q: %w", ref, err)
	}
	fs, err := file.New(sourceDir)
	if err != nil {
		return "", fmt.Errorf("failed to create file store at %q: %w", sourceDir, err)
	}
	defer fs.Close() //nolint:errcheck

	layerDesc, err := fs.Add(ctx, ".", ImageLayerMediaType, ".")
	if err != nil {
		return "", fmt.Errorf("failed to add directory to file store: %w", err)
	}

	packOpts := oras.PackManifestOptions{
		Layers:              []ocispec.Descriptor{layerDesc},
		ManifestAnnotations: opts.Annotations,
	}

	if len(opts.Config) > 0 {
		configDesc, err := oras.PushBytes(ctx, fs, opts.ConfigMediaType, opts.Config)
		if err != nil {
			return "", fmt.Errorf("failed to add config to file store: %w", err)
		}
		packOpts.ConfigDescriptor = &configDesc
	}

	manifest, err := oras.PackManifest(ctx, fs, oras.PackManifestVersion1_1, cmp.Or(opts.ArtifactType, oras.MediaTypeUnknownArtifact), packOpts)
	if err != nil {
		return "", fmt.Errorf("failed to pack manifest: %w", err)
	}

	if err := fs.Tag(ctx, manifest, reference); err != nil {
		return "", fmt.Errorf("failed to tag manifest as %q: %w", reference, err)
	}

	log.Info("Pushing OCI artifact", "ref", fmt.Sprintf("%s:%s", ref, reference))

	desc, err := oras.Copy(ctx, fs, reference, repo, reference, oras.DefaultCopyOptions)
	if err != nil {
		return "", fmt.Errorf("failed to push artifact %s:%s: %w", ref, reference, err)
	}

	return desc.Digest.String(), nil
}
