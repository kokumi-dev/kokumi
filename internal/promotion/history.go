// Package promotion orchestrates promotion pipelines: the deployment history
// of a stage, the gate that holds a stage until its upstream stages deployed
// the same Product revision healthily for their soak time, and the signed
// records of every promotion and every healthy deployment. Nothing is
// promoted from stage to stage; every stage deploys only its own
// Preparations, as every environment of GitOps Promoter deploys only its own
// hydrated commits.
//
// The package owns its types; the controllers map Kubernetes objects to them.
package promotion

import (
	"slices"
	"time"
)

// MaxEntries is how many deployments a History keeps.
const MaxEntries = 16

// EndReason says why a deployment ended.
type EndReason string

const (
	// EndSuperseded ends a deployment when another Preparation is promoted.
	EndSuperseded EndReason = "Superseded"
	// EndDegraded ends a deployment when Argo CD reports it degraded.
	EndDegraded EndReason = "Degraded"
)

// Entry is one continuous deployment of a Preparation by a stage: from its
// promotion, or its recovery, until it is superseded or degraded. It is
// self-contained, so its records can be read after the Preparation is gone.
// Its times are in whole seconds, as they are stored, so the records built
// from them have equal bytes before and after a status write.
type Entry struct {
	Preparation string
	Digest      string
	// OCIRef is the artifact, with its digest.
	OCIRef string
	// Registry names the Registry that pushed the artifact, "" when none
	// did.
	Registry        string
	ProductRevision string
	// Started is when the Preparation passed its gates, or recovered.
	Started time.Time
	// Healthy is when the deployment was first healthy at the digest, zero
	// until then.
	Healthy time.Time
	// Ended is when the deployment ended, zero while it runs.
	Ended     time.Time
	EndReason EndReason
	// PromotionRecord is the digest of the promotion record. An entry
	// without one is a promotion that is being recorded. A recovery carries
	// the record of the promotion it continues.
	PromotionRecord string
	// DeploymentRecord is the digest of the deployment record, pushed once
	// the deployment was healthy.
	DeploymentRecord string
}

// Running reports whether the deployment has not ended.
func (e *Entry) Running() bool { return e.Ended.IsZero() }

// Promoted reports whether the promotion of the entry is recorded.
func (e *Entry) Promoted() bool { return e.PromotionRecord != "" }

// History is the deployments of a stage, newest first. At most its first
// entry is pending: a promotion that is being recorded.
type History []Entry

// Deployed returns the deployed entry, the newest that is promoted, and nil
// when nothing is deployed. Its Preparation is what the stage's Argo CD
// Application points at, also when it ended degraded.
func (h History) Deployed() *Entry {
	for i := range h {
		if h[i].Promoted() {
			return &h[i]
		}
	}
	return nil
}

// Pending returns the promotion that is being recorded, nil when there is
// none.
func (h History) Pending() *Entry {
	if len(h) == 0 || h[0].Promoted() {
		return nil
	}
	return &h[0]
}

// PromotedBefore returns the newest promoted entry of preparation, nil when
// the stage never promoted it or it aged out of the history.
func (h History) PromotedBefore(preparation string) *Entry {
	for i := range h {
		if h[i].Preparation == preparation && h[i].Promoted() {
			return &h[i]
		}
	}
	return nil
}

// Open returns h with a pending entry for the deployment of candidate,
// started at now, in place of any other pending entry. The candidate's
// times, end and records are ignored.
func (h History) Open(candidate Entry, now time.Time) History {
	h = h.Drop()
	return h.prepend(Entry{
		Preparation:     candidate.Preparation,
		Digest:          candidate.Digest,
		OCIRef:          candidate.OCIRef,
		Registry:        candidate.Registry,
		ProductRevision: candidate.ProductRevision,
		Started:         seconds(now),
	})
}

// Drop returns h without its pending entry.
func (h History) Drop() History {
	if h.Pending() == nil {
		return h
	}
	return slices.Clone(h[1:])
}

// Start returns h with the pending entry promoted under record, and the
// previously deployed entry, when it still runs, superseded at the pending
// entry's start. It returns h unchanged without a pending entry.
func (h History) Start(record string) History {
	if h.Pending() == nil {
		return h
	}
	h = slices.Clone(h)
	if previous := h[1:].Deployed(); previous != nil && previous.Running() {
		previous.Ended = h[0].Started
		previous.EndReason = EndSuperseded
	}
	h[0].PromotionRecord = record
	return h
}

// Step is what promoting a candidate that passed its gates takes next.
type Step int

const (
	// StepDeployed: the candidate is deployed, nothing is left to promote.
	StepDeployed Step = iota
	// StepOpen: open a pending entry for the candidate, started now.
	StepOpen
	// StepRecord: record the promotion of the pending entry, then start it.
	StepRecord
)

// Next returns the step that promoting candidate, a Preparation that passed
// its gates, takes next. A promotion takes two passes, because its record
// contains the time it is decided at: the first opens a pending entry with
// that time, the second records the promotion and starts the entry.
func (h History) Next(candidate string) Step {
	if deployed := h.Deployed(); deployed != nil && deployed.Preparation == candidate {
		return StepDeployed
	}
	if pending := h.Pending(); pending != nil && pending.Preparation == candidate {
		return StepRecord
	}
	return StepOpen
}

// Health is what Argo CD reports on a deployment.
type Health int

const (
	// HealthProgressing is a deployment that is neither healthy nor
	// degraded yet.
	HealthProgressing Health = iota
	// HealthHealthy is a deployment that is synced and healthy.
	HealthHealthy
	// HealthDegraded is a deployment that is degraded.
	HealthDegraded
)

// Observation is what Argo CD reports on a stage's Application.
type Observation struct {
	// Revision is the artifact digest the Application is synced to.
	Revision string
	Health   Health
}

// Observe returns h with what Argo CD reports on the deployed entry at now,
// and whether that changed h. A report on another revision says nothing
// about the deployed entry.
//
//   - Healthy at the digest records when it first was.
//   - Degraded at the digest ends the entry.
//   - Healthy at the digest of an entry that ended degraded opens a recovery
//     entry, healthy from now on, so that soak counts without
//     interruption.
func (h History) Observe(observation Observation, now time.Time) (History, bool) {
	i := slices.IndexFunc(h, func(e Entry) bool { return e.Promoted() })
	if i < 0 || h[i].Digest != observation.Revision {
		return h, false
	}
	deployed := h[i]
	now = seconds(now)

	switch {
	case observation.Health == HealthHealthy && deployed.Running() && deployed.Healthy.IsZero():
		h = slices.Clone(h)
		h[i].Healthy = now
	case observation.Health == HealthHealthy && deployed.EndReason == EndDegraded:
		recovery := Entry{
			Preparation:     deployed.Preparation,
			Digest:          deployed.Digest,
			OCIRef:          deployed.OCIRef,
			Registry:        deployed.Registry,
			ProductRevision: deployed.ProductRevision,
			Started:         now,
			Healthy:         now,
			PromotionRecord: deployed.PromotionRecord,
		}
		h = slices.Concat(h[:i:i], History{recovery}, h[i:])
		h = h[:min(len(h), MaxEntries)]
	case observation.Health == HealthDegraded && deployed.Running():
		h = slices.Clone(h)
		h[i].Ended = now
		h[i].EndReason = EndDegraded
	default:
		return h, false
	}
	return h, true
}

// RecordDeployment returns h with record as the deployment record of the
// entry of digest that became healthy at healthy.
func (h History) RecordDeployment(digest string, healthy time.Time, record string) History {
	h = slices.Clone(h)
	for i := range h {
		if h[i].Digest == digest && h[i].Healthy.Equal(healthy) {
			h[i].DeploymentRecord = record
		}
	}
	return h
}

// Unrecorded returns the entries that were healthy and have no deployment
// record yet.
func (h History) Unrecorded() []Entry {
	var unrecorded []Entry
	for _, e := range h {
		if e.Promoted() && !e.Healthy.IsZero() && e.DeploymentRecord == "" {
			unrecorded = append(unrecorded, e)
		}
	}
	return unrecorded
}

// prepend returns h with e as its first entry, keeping at most MaxEntries.
func (h History) prepend(e Entry) History {
	h = slices.Concat(History{e}, h)
	return h[:min(len(h), MaxEntries)]
}

// seconds returns t in UTC, truncated to the seconds it is stored with.
func seconds(t time.Time) time.Time {
	return t.UTC().Truncate(time.Second)
}
