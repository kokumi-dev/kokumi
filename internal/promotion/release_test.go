package promotion

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// later is the greater name of two releases created in the same second.
const later = "p-b"

func TestNewestReady(t *testing.T) {
	t0 := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	release := func(name string, created time.Duration, ready bool) Release {
		return Release{Name: name, Created: t0.Add(created), Ready: ready}
	}

	tests := []struct {
		name     string
		releases []Release
		want     string
		found    bool
	}{
		{name: "none", releases: nil},
		{name: "none Ready", releases: []Release{release("p1", 0, false)}},
		{
			name:     "the newest of the Ready ones",
			releases: []Release{release("p1", 0, true), release("p3", 2*time.Second, false), release("p2", time.Second, true)},
			want:     "p2", found: true,
		},
		{
			name:     "created in the same second: the greater name, whatever the order",
			releases: []Release{release("p-a", 0, true), release(later, 0, true)},
			want:     later, found: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := NewestReady(tt.releases)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.found, found)

			reversed := slices.Clone(tt.releases)
			slices.Reverse(reversed)
			got, _ = NewestReady(reversed)
			assert.Equal(t, tt.want, got, "the order of the list does not matter")
		})
	}
}

func TestNewestFirst(t *testing.T) {
	t0 := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	releases := []Release{
		{Name: "p-a", Created: t0},
		{Name: "p-c", Created: t0.Add(-time.Second)},
		{Name: later, Created: t0},
	}
	slices.SortFunc(releases, NewestFirst)
	assert.Equal(t, []string{later, "p-a", "p-c"}, []string{releases[0].Name, releases[1].Name, releases[2].Name})
}
