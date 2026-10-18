// Package registry connects Kokumi to OCI registry locations through the
// Registry objects that cover them.
//
// A location (a repository, or a prefix of repositories) is connected
// through the Registry with the longest url that covers it among the
// Registries its owner lists in registryRefs. Without such a Registry the
// location is accessed anonymously over HTTPS, except the bundled registry,
// which is accessed over HTTP.
package registry

import (
	"fmt"
	"strings"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

// Location is a registry host and a repository path below it. The path is
// empty for the whole registry.
type Location struct {
	// Host is the canonical host[:port] (see oci.CanonicalHost).
	Host string
	// Path is the repository path, without leading or trailing slashes.
	Path string
}

// ParseLocation parses an oci:// URL, or a reference written without a
// scheme as image names and mirror locations are, into its location. A tag
// or digest is dropped.
func ParseLocation(s string) (Location, error) {
	rest := strings.TrimPrefix(s, "oci://")
	if before, _, ok := strings.Cut(rest, "@"); ok {
		rest = before
	}

	host, path, _ := strings.Cut(rest, "/")
	if host == "" {
		return Location{}, fmt.Errorf("invalid registry location %q: missing host", s)
	}

	// A colon after the last slash of the path separates a tag.
	if i := strings.LastIndex(path, ":"); i >= 0 && !strings.Contains(path[i:], "/") {
		path = path[:i]
	}

	return Location{Host: oci.CanonicalHost(host), Path: strings.Trim(path, "/")}, nil
}

// String returns the location as host[/path].
func (l Location) String() string {
	if l.Path == "" {
		return l.Host
	}
	return l.Host + "/" + l.Path
}

// Covers reports whether other is l or a location below it. Paths match at
// slash boundaries: oci://ghcr.io/org covers ghcr.io/org/app but not
// ghcr.io/organization.
func (l Location) Covers(other Location) bool {
	if l.Host != other.Host {
		return false
	}
	return l.Path == "" || other.Path == l.Path || strings.HasPrefix(other.Path, l.Path+"/")
}

// Match returns the Registry among registries whose url covers url with the
// longest path, or nil when none covers it.
func Match(registries []deliveryv1alpha1.Registry, url string) (*deliveryv1alpha1.Registry, error) {
	target, err := ParseLocation(url)
	if err != nil {
		return nil, err
	}

	var (
		best     *deliveryv1alpha1.Registry
		bestPath = -1
	)
	for i := range registries {
		loc, err := ParseLocation(registries[i].Spec.URL)
		if err != nil {
			continue
		}
		if loc.Covers(target) && len(loc.Path) > bestPath {
			best, bestPath = &registries[i], len(loc.Path)
		}
	}
	return best, nil
}
