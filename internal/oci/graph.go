package oci

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Limits of Walk. Anyone who can push to a repository can attach any number
// of referrers, so a walk stops at these and marks what it left out.
const (
	// maxGraphDepth is how many levels below the root Walk descends.
	maxGraphDepth = 4
	// maxGraphNodes is how many manifests, the root included, Walk fetches.
	maxGraphNodes = 64
)

// Link is how a node of a Graph is attached to its parent node.
type Link string

const (
	// LinkSubject attaches an OCI 1.1 referrer: its subject names the parent.
	LinkSubject Link = "Subject"
	// LinkTag attaches an artifact by the tag cosign names it with before OCI
	// 1.1 referrers: sha256-<hex of the parent digest>.sig, .att or .sbom.
	LinkTag Link = "Tag"
	// LinkIndex attaches a child of an image index.
	LinkIndex Link = "Index"
)

// legacyTagSuffixes are the suffixes of the tags cosign stores signatures,
// attestations and SBOMs under in registries without OCI 1.1 referrers.
var legacyTagSuffixes = []string{".sig", ".att", ".sbom"}

// Graph is an artifact with everything attached to it in its repository.
type Graph struct {
	// Repository is the oci:// URL of the repository.
	Repository string
	// Tag is the tag the graph was requested by, empty when it was requested
	// by digest only.
	Tag string
	// TagDigest is the digest Tag points at now. It differs from the root's
	// digest when the tag moved, and is empty when the tag did not resolve.
	TagDigest string
	// Root is the requested artifact.
	Root *Node
}

// Node is one manifest of a Graph.
type Node struct {
	Manifest
	// Link is how the node is attached to its parent; empty for the root.
	Link Link
	// Tag is the tag of a LinkTag node.
	Tag string
	// Children are the children of an image index.
	Children []*Node
	// Referrers are the artifacts attached to this one, oldest first.
	Referrers []*Node
	// Truncated reports that children or referrers were left out at the
	// limits of the walk.
	Truncated bool
	// Err reports why children or referrers could not be read completely.
	Err error
}

// Walk returns the graph of the artifact at ref: the children of image
// indexes and the referrers of every manifest, recursively, found through
// the OCI referrers API (or its tag schema fallback) and cosign's legacy
// tags. A ref with both a tag and a digest walks the digest and reports
// where the tag points now.
func Walk(ctx context.Context, client Client, ref Reference) (*Graph, error) {
	root, err := client.FetchManifest(ctx, ref)
	if err != nil {
		return nil, err
	}

	repo := ref
	repo.Tag = ""
	repo.Digest = ""

	graph := &Graph{Repository: repo.OCIRepositoryReference(), Tag: ref.Tag, Root: &Node{Manifest: root}}
	switch {
	case ref.Tag != "" && ref.Digest == "":
		graph.TagDigest = root.Digest
	case ref.Tag != "":
		tagged := repo
		tagged.Tag = ref.Tag
		if desc, err := client.Resolve(ctx, tagged); err == nil {
			graph.TagDigest = desc.Digest
		}
	}

	w := &walker{client: client, repo: repo, seen: map[string]bool{root.Digest: true}, nodes: 1}
	tags, err := client.ListTags(ctx, repo)
	if err != nil {
		graph.Root.Err = fmt.Errorf("listing tags for cosign signatures: %w", err)
	}
	w.tags = tags

	w.walk(ctx, graph.Root)

	return graph, nil
}

// walker walks one repository breadth first, so that the referrers closest
// to the root are kept when the limits cut the graph.
type walker struct {
	client Client
	repo   Reference
	tags   []string
	seen   map[string]bool
	nodes  int
}

type queued struct {
	node  *Node
	depth int
}

func (w *walker) walk(ctx context.Context, root *Node) {
	queue := []queued{{node: root}}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]

		for _, child := range w.attached(ctx, next.node) {
			if next.depth >= maxGraphDepth || w.nodes >= maxGraphNodes {
				next.node.Truncated = true
				break
			}
			if !w.add(ctx, next.node, child) {
				continue
			}
			queue = append(queue, queued{node: child.node, depth: next.depth + 1})
		}

		// Registries need not repeat the annotations in the referrers list,
		// so the fetched manifests decide the order.
		slices.SortStableFunc(next.node.Referrers, func(a, b *Node) int {
			return compareCreated(a.Descriptor, b.Descriptor)
		})
	}
}

// compareCreated orders descriptors by their creation annotation, then by
// digest.
func compareCreated(a, b Descriptor) int {
	return cmp.Or(
		strings.Compare(a.Annotations[ocispec.AnnotationCreated], b.Annotations[ocispec.AnnotationCreated]),
		strings.Compare(a.Digest, b.Digest),
	)
}

// candidate is a manifest attached to a node, before it is fetched.
type candidate struct {
	ref  Reference
	link Link
	tag  string
	node *Node
}

// attached returns what is attached to node: the children of an index, its
// referrers and its cosign tags. It records listing errors on node.
func (w *walker) attached(ctx context.Context, node *Node) []*candidate {
	var candidates []*candidate

	for _, child := range node.Manifests {
		candidates = append(candidates, &candidate{ref: w.byDigest(child.Digest), link: LinkIndex})
	}

	referrers, err := w.client.ListReferrers(ctx, w.byDigest(node.Digest), "")
	if err != nil {
		node.Err = errors.Join(node.Err, err)
	}
	// Oldest first, so that the limits keep the first attached.
	slices.SortFunc(referrers, compareCreated)
	for _, referrer := range referrers {
		candidates = append(candidates, &candidate{ref: w.byDigest(referrer.Digest), link: LinkSubject})
	}

	if hex, ok := strings.CutPrefix(node.Digest, "sha256:"); ok {
		for _, suffix := range legacyTagSuffixes {
			tag := "sha256-" + hex + suffix
			if !slices.Contains(w.tags, tag) {
				continue
			}
			ref := w.repo
			ref.Tag = tag
			candidates = append(candidates, &candidate{ref: ref, link: LinkTag, tag: tag})
		}
	}

	return candidates
}

// add fetches the candidate and attaches it to parent. It reports false when
// the candidate was already seen or could not be fetched.
func (w *walker) add(ctx context.Context, parent *Node, c *candidate) bool {
	if c.ref.Digest != "" && w.seen[c.ref.Digest] {
		return false
	}

	manifest, err := w.client.FetchManifest(ctx, c.ref)
	if err != nil {
		parent.Err = errors.Join(parent.Err, err)
		return false
	}
	if w.seen[manifest.Digest] {
		return false
	}
	w.seen[manifest.Digest] = true
	w.nodes++

	c.node = &Node{Manifest: manifest, Link: c.link, Tag: c.tag}
	if c.link == LinkIndex {
		parent.Children = append(parent.Children, c.node)
	} else {
		parent.Referrers = append(parent.Referrers, c.node)
	}
	return true
}

func (w *walker) byDigest(digest string) Reference {
	ref := w.repo
	ref.Digest = digest
	return ref
}
