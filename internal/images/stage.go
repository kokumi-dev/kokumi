package images

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/kokumi-dev/kokumi/internal/oci"
)

const (
	// MaxImages is the most images the manifests of one render may
	// reference. It mirrors the MaxItems of Preparation spec.images.
	MaxImages = 512
	// MaxRefLength is the longest image reference the manifests may hold. It
	// mirrors the MaxLength of Preparation spec.images[].ref.
	MaxRefLength = 1024
)

const (
	// resolveTimeout bounds the lookup of one image.
	resolveTimeout = 10 * time.Second
	// copyTimeout bounds the copy of one image with all its platforms.
	copyTimeout = 10 * time.Minute
	// maxParallelResolves bounds the concurrent lookups of one render.
	maxParallelResolves = 8
	// maxParallelCopies bounds the concurrent copies of one render. Each
	// copy transfers several blobs at a time itself.
	maxParallelCopies = 3
)

// Stage is the image stage of one render or preview.
type Stage struct {
	// Policy is what to do with the images. Empty means PolicyRecord.
	Policy Policy
	// Client reads the images' registries and their mirrors.
	Client oci.Client
	// Mirrors redirect lookups and copies.
	Mirrors []Mirror
	// Destination is the repository vendored images are copied into and
	// pulled from. Under PolicyVendor without a destination, as in a preview
	// of a Product, references are pinned instead.
	Destination *oci.Reference
	// DestClient writes to Destination.
	DestClient oci.Client
	// Locked are the images of the previous render. Under PolicyPin and
	// PolicyVendor, a reference written the same way keeps the digest it
	// had instead of being resolved again, as with a lock file.
	// PolicyRecord resolves every reference afresh.
	Locked []Image
	// DryRun resolves and rewrites without copying, and leaves references
	// it cannot resolve as written instead of failing. Previews use it.
	DryRun bool
}

// resolved is the outcome of resolving one reference.
type resolved struct {
	Image
	// source is where the image is read from, pinned to its digest once it
	// is resolved.
	source oci.Reference
	// unreachable reports that the lookup failed before the registry
	// answered.
	unreachable bool
	// lockedVendored is where the previous render vendored the image to.
	lockedVendored string
}

// pin records that the reference resolved to digest.
func (r *resolved) pin(digest, mediaType string) {
	r.Digest = digest
	r.MediaType = mediaType
	r.source.Tag = ""
	r.source.Digest = digest
}

// vendorJob is the copy of one image to the destination.
type vendorJob struct {
	source oci.Reference
	target oci.Reference
	// locked reports that the previous render vendored the image to
	// target already.
	locked bool
}

// Run resolves every image reference in manifests and records the outcome.
// Under PolicyPin it rewrites the references to their digests; under
// PolicyVendor it copies the images to the destination, with all platforms
// and their referrers, and rewrites the references to pull them from there.
// Both fail when an image cannot be resolved, unless the stage is a dry run.
// Run fails before any lookup when the manifests reference more images, or
// longer references, than a Preparation can record. The images are returned
// sorted by reference.
func (s Stage) Run(ctx context.Context, manifests []*Manifest) ([]Image, error) {
	refs, err := uniqueRefs(manifests)
	if err != nil {
		return nil, err
	}

	policy := cmp.Or(s.Policy, PolicyRecord)
	results := s.resolveAll(ctx, refs, s.locks(policy))

	if policy != PolicyRecord {
		if err := s.rewrite(ctx, policy, results, manifests); err != nil {
			return nil, err
		}
	}

	var images []Image
	for _, result := range results {
		images = append(images, result.Image)
	}
	return images, nil
}

// uniqueRefs returns the image references of manifests, sorted and without
// duplicates. It fails when they exceed what a Preparation can record.
func uniqueRefs(manifests []*Manifest) ([]string, error) {
	seen := map[string]bool{}
	var refs []string
	for _, manifest := range manifests {
		for _, ref := range manifest.References() {
			if seen[ref] {
				continue
			}
			if len(ref) > MaxRefLength {
				return nil, fmt.Errorf("image reference %.64q... is longer than %d characters", ref, MaxRefLength)
			}
			seen[ref] = true
			refs = append(refs, ref)
		}
	}

	if len(refs) > MaxImages {
		return nil, fmt.Errorf("the manifests reference %d images, more than the %d a Preparation can record", len(refs), MaxImages)
	}

	slices.Sort(refs)
	return refs, nil
}

// locks returns the locked images by reference, under the policies that
// honor them.
func (s Stage) locks(policy Policy) map[string]Image {
	if policy == PolicyRecord {
		return nil
	}

	locks := map[string]Image{}
	for _, img := range s.Locked {
		if img.Digest != "" {
			locks[img.Ref] = img
		}
	}
	return locks
}

// resolveAll resolves refs concurrently. References in locks keep their
// locked digest without a lookup. The first lookup of each registry runs
// alone: when it fails before the registry answers, the other references of
// that registry fail with its error instead of each waiting for the same
// timeout.
func (s Stage) resolveAll(ctx context.Context, refs []string, locks map[string]Image) []resolved {
	results := make([]resolved, len(refs))

	var hosts []string
	byHost := map[string][]int{}
	for i, ref := range refs {
		results[i] = s.prepare(ref, locks)
		if results[i].Error != "" || results[i].Digest != "" {
			continue
		}
		host := results[i].source.Registry
		if _, ok := byHost[host]; !ok {
			hosts = append(hosts, host)
		}
		byHost[host] = append(byHost[host], i)
	}

	limit := make(chan struct{}, maxParallelResolves)
	lookup := func(i int) {
		limit <- struct{}{}
		defer func() { <-limit }()
		s.lookup(ctx, &results[i])
	}

	var wg sync.WaitGroup
	for _, host := range hosts {
		indexes := byHost[host]
		wg.Go(func() {
			lookup(indexes[0])
			if first := results[indexes[0]]; first.unreachable {
				for _, i := range indexes[1:] {
					results[i].Error = fmt.Sprintf("registry %s is unreachable: %s", host, first.Error)
				}
				return
			}

			var hostWG sync.WaitGroup
			for _, i := range indexes[1:] {
				hostWG.Go(func() { lookup(i) })
			}
			hostWG.Wait()
		})
	}
	wg.Wait()

	return results
}

// prepare parses ref and finds where it is read from, taking its digest
// from locks when ref is locked. It records why ref is invalid in the
// result.
func (s Stage) prepare(ref string, locks map[string]Image) resolved {
	result := resolved{Ref: ref}

	parsed, err := parseRef(ref)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Name = parsed.name

	result.source, err = parsed.source(s.Mirrors)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	if lock, ok := locks[ref]; ok {
		result.pin(lock.Digest, lock.MediaType)
		result.lockedVendored = lock.Vendored
	}

	return result
}

// lookup resolves the source of result to its digest, recording why it
// failed in the result.
func (s Stage) lookup(ctx context.Context, result *resolved) {
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	desc, err := s.Client.Resolve(ctx, result.source)
	if err != nil {
		result.Error = err.Error()
		result.unreachable = unreachable(err)
		return
	}

	result.pin(desc.Digest, desc.MediaType)
}

// unreachable reports whether err means that the registry never answered:
// a DNS, connection, TLS or timeout error, as opposed to an error response
// such as 401 or 404.
func unreachable(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded)
}

// rewrite pins or vendors the resolved images and rewrites the manifests.
func (s Stage) rewrite(ctx context.Context, policy Policy, results []resolved, manifests []*Manifest) error {
	if !s.DryRun {
		var errs []error
		for _, result := range results {
			if result.Error != "" {
				errs = append(errs, fmt.Errorf("%s: %s", result.Ref, result.Error))
			}
		}
		if len(errs) > 0 {
			return fmt.Errorf("could not resolve every image under the %s image policy: %w", policy, errors.Join(errs...))
		}
	}

	vendor := policy == PolicyVendor && s.Destination != nil
	rewrites := map[string]string{}
	var jobs []vendorJob
	queued := map[string]int{}

	for i := range results {
		result := &results[i]
		if result.Digest == "" {
			continue
		}

		if !vendor {
			if !strings.Contains(result.Ref, "@") {
				rewrites[result.Ref] = result.Ref + "@" + result.Digest
			}
			continue
		}

		target := *s.Destination
		target.Tag = ""
		target.Digest = result.Digest
		result.Vendored = target.String()
		rewrites[result.Ref] = result.Vendored

		locked := result.lockedVendored == result.Vendored
		if j, ok := queued[result.Digest]; ok {
			jobs[j].locked = jobs[j].locked || locked
			continue
		}
		queued[result.Digest] = len(jobs)
		jobs = append(jobs, vendorJob{source: result.source, target: target, locked: locked})
	}

	if !s.DryRun {
		if err := s.vendorAll(ctx, jobs); err != nil {
			return err
		}
	}

	for _, manifest := range manifests {
		manifest.Rewrite(rewrites)
	}

	return nil
}

// vendorAll copies the images of jobs to the destination, a few at a time.
// The first failure cancels the copies still running.
func (s Stage) vendorAll(ctx context.Context, jobs []vendorJob) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		once     sync.Once
		firstErr error
	)
	limit := make(chan struct{}, maxParallelCopies)
	var wg sync.WaitGroup
	for _, job := range jobs {
		wg.Go(func() {
			limit <- struct{}{}
			defer func() { <-limit }()
			if ctx.Err() != nil {
				return
			}
			if err := s.vendor(ctx, job); err != nil {
				once.Do(func() {
					firstErr = err
					cancel()
				})
			}
		})
	}
	wg.Wait()

	if firstErr != nil {
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("failed to vendor images: %w", err)
	}
	return nil
}

// vendor copies the image of job, with its referrers, to the target, unless
// the previous render already vendored it there and it is still present.
func (s Stage) vendor(ctx context.Context, job vendorJob) error {
	if job.locked && s.present(ctx, job.target) {
		return nil
	}

	log.FromContext(ctx).Info("Vendoring image", "source", job.source.String(), "destination", job.target.String())

	ctx, cancel := context.WithTimeout(ctx, copyTimeout)
	defer cancel()

	if err := s.Client.Copy(ctx, s.DestClient, job.source, job.target, oci.CopyOptions{IncludeReferrers: true}); err != nil {
		return fmt.Errorf("failed to vendor image %s: %w", job.source, err)
	}
	return nil
}

// present reports whether the destination still holds target.
func (s Stage) present(ctx context.Context, target oci.Reference) bool {
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	_, err := s.DestClient.Resolve(ctx, target)
	return err == nil
}
