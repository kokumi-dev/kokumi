package scan

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kokumi-dev/kokumi/internal/oci"
)

var t0 = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func digestOf(n int) string {
	return fmt.Sprintf("sha256:%064d", n)
}

func imageRef(t *testing.T, n int) oci.Reference {
	t.Helper()
	ref, err := oci.Parse("oci://registry.internal/shop/vendor/app/images/nginx@" + digestOf(n))
	require.NoError(t, err)
	return ref
}

func digests(images map[string]oci.Reference) []string {
	out := make([]string, 0, len(images))
	for d := range images {
		out = append(out, d)
	}
	slices.Sort(out)
	return out
}

func TestInUse(t *testing.T) {
	release := func(name string, created time.Duration, ready bool, image int) Release {
		return Release{
			Name: name, Created: t0.Add(created), Ready: ready,
			Images: []oci.Reference{imageRef(t, image)},
		}
	}
	releases := []Release{
		release("a", 0, true, 1),
		release("e", 4*time.Second, false, 5),
		release("d", 3*time.Second, true, 4),
		release("b", time.Second, true, 2),
		release("c", time.Second, true, 3),
	}

	assert.Equal(t, []string{digestOf(2), digestOf(3), digestOf(4)}, digests(InUse(releases)),
		"the three newest Ready releases; not Ready and older ones are left out; ties go by name")
	assert.Equal(t, []string{digestOf(1), digestOf(2), digestOf(3), digestOf(4), digestOf(5)}, digests(InUse(releases, "a", "e", "")),
		"and the releases named, such as the target and the deployed one, Ready or not")
	assert.Empty(t, InUse(nil))
}

func TestDue_Next_Latest(t *testing.T) {
	interval := 24 * time.Hour
	inUse := map[string]oci.Reference{digestOf(1): imageRef(t, 1), digestOf(2): imageRef(t, 2), digestOf(3): imageRef(t, 3)}
	records := map[string]Record{
		digestOf(1): {Digest: digestOf(1), ScannedAt: t0.Add(-interval)},
		digestOf(2): {Digest: digestOf(2), ScannedAt: t0.Add(-time.Hour)},
	}

	assert.Equal(t, []string{digestOf(1), digestOf(3)}, Due(inUse, records, interval, t0), "an interval old, and never scanned")
	assert.Equal(t, time.Minute, Next(records, interval, t0), "at least a minute, when a record is due now")
	delete(records, digestOf(1))
	assert.Equal(t, 23*time.Hour, Next(records, interval, t0))

	many := map[string]Record{}
	for n := range 5 {
		many[digestOf(n)] = Record{Digest: digestOf(n), ScannedAt: t0.Add(time.Duration(n) * time.Minute)}
	}
	latest := Latest(many, 3)
	require.Len(t, latest, 3)
	assert.Equal(t, []string{digestOf(2), digestOf(3), digestOf(4)}, []string{latest[0].Digest, latest[1].Digest, latest[2].Digest},
		"the most recently scanned, sorted by digest")
}

func TestStatus_Health(t *testing.T) {
	const maxDatabaseAge, staleAfter = 7 * 24 * time.Hour, 48 * time.Hour
	fresh := []Record{{Digest: digestOf(1), ScannedAt: t0.Add(-time.Hour)}}
	tests := []struct {
		name    string
		status  Status
		reason  Reason
		message string
	}{
		{"nothing in use", Status{}, ReasonScanned, "No vendored images in use"},
		{"scanned", Status{Records: fresh, InUse: 1, DatabaseBuilt: t0.Add(-time.Hour)}, ReasonScanned, "Scanned the vendored images in use: 1"},
		{"cut", Status{Records: fresh, InUse: 3}, ReasonScanned, "Scanned the vendored images in use: 1; 2 more are in use than the status lists"},
		{"failed", Status{Records: fresh, InUse: 5, Failures: []Failure{
			{digestOf(2), errors.New("a")}, {digestOf(3), errors.New("b")}, {digestOf(4), errors.New("c")}, {digestOf(5), errors.New("d")},
		}}, ReasonScanFailed, "Could not scan 4 of 5 images: " + digestOf(2) + ": a; " + digestOf(3) + ": b; " + digestOf(4) + ": c"},
		{"database stale", Status{Records: fresh, InUse: 1, Database: "oci://mirror/trivy-db@sha256:1", DatabaseBuilt: t0.Add(-8 * 24 * time.Hour)},
			ReasonDatabaseStale, "The vulnerability database oci://mirror/trivy-db@sha256:1 was built 2026-10-11T12:00:00Z, more than 168h0m0s ago: update the mirror the Kitchen's scanning names"},
		{"scan stale", Status{Records: []Record{{Digest: digestOf(1), ScannedAt: t0.Add(-3 * 24 * time.Hour)}}, InUse: 1},
			ReasonStale, "Image " + digestOf(1) + " was last scanned 2026-10-16T12:00:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			health := tt.status.Health(maxDatabaseAge, staleAfter, t0)
			assert.Equal(t, Health{tt.reason, tt.message}, health)
			assert.Equal(t, tt.reason == ReasonScanned, health.OK())
		})
	}
}
