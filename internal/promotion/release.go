package promotion

import (
	"cmp"
	"strings"
	"time"
)

// Release is a rendered release of an Order, one of its Preparations, as
// promotion and scanning pick among them.
type Release struct {
	Name    string
	Created time.Time
	Ready   bool
}

// NewestFirst orders releases newest first: by creation time, then by name,
// both descending, so that releases created within the same second always
// come in the same order.
func NewestFirst(a, b Release) int {
	return cmp.Or(b.Created.Compare(a.Created), strings.Compare(b.Name, a.Name))
}

// NewestReady returns the name of the newest Ready release, the one
// Automatic promotion converges to, and false when none is Ready.
func NewestReady(releases []Release) (string, bool) {
	var newest *Release
	for i := range releases {
		r := &releases[i]
		if r.Ready && (newest == nil || NewestFirst(*r, *newest) < 0) {
			newest = r
		}
	}
	if newest == nil {
		return "", false
	}
	return newest.Name, true
}
