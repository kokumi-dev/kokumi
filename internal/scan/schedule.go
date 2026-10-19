package scan

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/kokumi-dev/kokumi/internal/oci"
)

// RecentReleases is how many of an Order's newest Ready releases stay
// scanned: the next one and the ones a rollback returns to.
const RecentReleases = 3

// maxListedFailures bounds the failures a Health message lists.
const maxListedFailures = 3

// Release is a rendered release whose vendored images may be in use.
type Release struct {
	Name    string
	Created time.Time
	Ready   bool
	// Images are the vendored images the release runs, by digest.
	Images []oci.Reference
}

// InUse returns the images of an Order's releases in use, by digest: the
// RecentReleases newest Ready releases and the releases named, such as the
// one deployed.
func InUse(releases []Release, named ...string) map[string]oci.Reference {
	ready := slices.DeleteFunc(slices.Clone(releases), func(r Release) bool { return !r.Ready })
	slices.SortFunc(ready, func(a, b Release) int {
		return cmp.Or(b.Created.Compare(a.Created), strings.Compare(b.Name, a.Name))
	})
	keep := map[string]bool{}
	for _, r := range ready[:min(RecentReleases, len(ready))] {
		keep[r.Name] = true
	}
	for _, name := range named {
		keep[name] = true
	}

	images := map[string]oci.Reference{}
	for _, r := range releases {
		if !keep[r.Name] {
			continue
		}
		for _, img := range r.Images {
			images[img.Digest] = img
		}
	}
	return images
}

// Due returns the digests of inUse to scan at now, sorted: those without a
// record and those whose record is interval old.
func Due(inUse map[string]oci.Reference, records map[string]Record, interval time.Duration, now time.Time) []string {
	var due []string
	for digest := range inUse {
		if rec, ok := records[digest]; !ok || !rec.ScannedAt.Add(interval).After(now) {
			due = append(due, digest)
		}
	}
	slices.Sort(due)
	return due
}

// Next returns how long until the first of records is due again, at least a
// minute.
func Next(records map[string]Record, interval time.Duration, now time.Time) time.Duration {
	next := interval
	for _, rec := range records {
		next = min(next, rec.ScannedAt.Add(interval).Sub(now))
	}
	return max(next, time.Minute)
}

// Latest returns records sorted by digest; the limit most recently scanned
// when there are more.
func Latest(records map[string]Record, limit int) []Record {
	latest := slices.Collect(maps.Values(records))
	if len(latest) > limit {
		slices.SortFunc(latest, func(a, b Record) int { return b.ScannedAt.Compare(a.ScannedAt) })
		latest = latest[:limit]
	}
	slices.SortFunc(latest, func(a, b Record) int { return strings.Compare(a.Digest, b.Digest) })
	return latest
}

// Reason is why the scans of a Product are in the state they are.
type Reason string

// Reasons, from the first that applies.
const (
	// ReasonScanFailed: images could not be scanned.
	ReasonScanFailed Reason = "ScanFailed"
	// ReasonDatabaseStale: the vulnerability database is older than allowed.
	ReasonDatabaseStale Reason = "DatabaseStale"
	// ReasonStale: a scan is older than allowed.
	ReasonStale Reason = "Stale"
	// ReasonScanned: every image in use has a current scan.
	ReasonScanned Reason = "Scanned"
)

// Health is the state of the scans of a Product.
type Health struct {
	Reason  Reason
	Message string
}

// OK reports whether every image in use has a current scan.
func (h Health) OK() bool {
	return h.Reason == ReasonScanned
}

// Status is what the state of a Product's scans is judged by.
type Status struct {
	// Records are the scans recorded.
	Records []Record
	// InUse is how many images are in use, more than Records lists when
	// they were cut.
	InUse int
	// Failures are the images that failed in the last run.
	Failures []Failure
	// Database is the vulnerability database the last scan used, and
	// DatabaseBuilt when it was built; zero when unknown.
	Database      string
	DatabaseBuilt time.Time
}

// Health judges s at now: failed when images failed to scan, stale when the
// database is older than maxDatabaseAge or a scan older than staleAfter,
// scanned otherwise.
func (s Status) Health(maxDatabaseAge, staleAfter time.Duration, now time.Time) Health {
	if len(s.Failures) > 0 {
		shown := make([]string, 0, maxListedFailures)
		for _, f := range s.Failures[:min(len(s.Failures), maxListedFailures)] {
			shown = append(shown, f.String())
		}
		return Health{ReasonScanFailed, fmt.Sprintf("Could not scan %d of %d images: %s", len(s.Failures), s.InUse, strings.Join(shown, "; "))}
	}

	if !s.DatabaseBuilt.IsZero() && len(s.Records) > 0 && now.Sub(s.DatabaseBuilt) > maxDatabaseAge {
		return Health{ReasonDatabaseStale, fmt.Sprintf(
			"The vulnerability database %s was built %s, more than %s ago: update the mirror the Kitchen's scanning names",
			s.Database, s.DatabaseBuilt.UTC().Format(time.RFC3339), maxDatabaseAge)}
	}

	for _, rec := range s.Records {
		if now.Sub(rec.ScannedAt) > staleAfter {
			return Health{ReasonStale, fmt.Sprintf("Image %s was last scanned %s", rec.Digest, rec.ScannedAt.UTC().Format(time.RFC3339))}
		}
	}

	msg := fmt.Sprintf("Scanned the vendored images in use: %d", len(s.Records))
	if s.InUse == 0 {
		msg = "No vendored images in use"
	}
	if s.InUse > len(s.Records) {
		msg += fmt.Sprintf("; %d more are in use than the status lists", s.InUse-len(s.Records))
	}
	return Health{ReasonScanned, msg}
}
