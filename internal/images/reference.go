package images

import (
	"fmt"
	"strings"

	"github.com/distribution/reference"

	"github.com/kokumi-dev/kokumi/internal/oci"
)

// imageRef is an image reference, normalized as the kubelet normalizes it.
type imageRef struct {
	// name is the normalized repository, e.g. docker.io/library/nginx.
	name string
	// tag is the tag, "latest" when the reference names neither a tag nor
	// a digest.
	tag string
	// digest is the digest the reference pins, if any.
	digest string
}

// parseRef normalizes ref.
func parseRef(ref string) (imageRef, error) {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return imageRef{}, fmt.Errorf("invalid image reference: %w", err)
	}

	parsed := imageRef{name: named.Name()}
	if tagged, ok := named.(reference.Tagged); ok {
		parsed.tag = tagged.Tag()
	}
	if digested, ok := named.(reference.Digested); ok {
		parsed.digest = digested.Digest().String()
	}
	if parsed.tag == "" && parsed.digest == "" {
		parsed.tag = "latest"
	}

	return parsed, nil
}

// source returns the reference the image is read from: its digest when it
// pins one, its tag otherwise, at the mirror with the longest matching prefix
// when one matches.
func (r imageRef) source(mirrors []Mirror) (oci.Reference, error) {
	repository := r.name
	if mirror, ok := matchMirror(mirrors, r.name); ok {
		repository = mirror.Location + strings.TrimPrefix(r.name, mirror.Prefix)
	}

	ref, err := oci.Parse(repository)
	if err != nil {
		return oci.Reference{}, err
	}

	if r.digest != "" {
		ref.Digest = r.digest
	} else {
		ref.Tag = r.tag
	}

	return ref, nil
}

// matchMirror returns the mirror whose prefix is the longest prefix of name
// at a path boundary.
func matchMirror(mirrors []Mirror, name string) (Mirror, bool) {
	var (
		best  Mirror
		found bool
	)
	for _, mirror := range mirrors {
		prefix := strings.TrimSuffix(mirror.Prefix, "/")
		if name != prefix && !strings.HasPrefix(name, prefix+"/") {
			continue
		}
		if !found || len(prefix) > len(best.Prefix) {
			best = Mirror{Prefix: prefix, Location: strings.TrimSuffix(mirror.Location, "/")}
			found = true
		}
	}
	return best, found
}

// TaggedName returns the normalized name of the image with the tag Ref names,
// "latest" when Ref names neither a tag nor a digest, or the name alone when
// Ref names only a digest. It returns Name when Ref is not a valid reference.
func (i Image) TaggedName() string {
	parsed, err := parseRef(i.Ref)
	if err != nil {
		return i.Name
	}
	if parsed.tag == "" {
		return parsed.name
	}
	return parsed.name + ":" + parsed.tag
}

// vendoredRepository returns the repository the image with the normalized
// name is vendored into: its own repository below root, named after the
// image, such as <root>/images/ghcr.io/example/app. A registry port is
// written as _<port>, since a repository path cannot hold a colon. It fails
// when the repository is not a valid name or its path is longer than a
// registry accepts.
func vendoredRepository(root oci.Reference, name string) (oci.Reference, error) {
	target := root
	target.Tag, target.Digest = "", ""
	target.Repository = root.Repository + "/images/" + strings.ToLower(strings.ReplaceAll(name, ":", "_"))

	if _, err := reference.Parse(target.RepositoryReference()); err != nil {
		return oci.Reference{}, fmt.Errorf("cannot vendor %s into %s: %w", name, target.RepositoryReference(), err)
	}

	return target, nil
}
