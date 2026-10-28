package promotion

import (
	"errors"
	"fmt"
	"time"

	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/vulnerability"
)

// Predicate types of the records a stage writes. Both are referrers of the
// deployed Preparation's artifact, signed by the Kitchen key.
const (
	// PromotionPredicateType records that a stage started deploying a
	// Preparation, and the evidence of every gate it passed.
	PromotionPredicateType = "https://kokumi.dev/attestations/promotion/v1"
	// DeploymentPredicateType records that a stage's deployment of a
	// Preparation was healthy.
	DeploymentPredicateType = "https://kokumi.dev/attestations/deployment/v1"
)

// ErrRecordMismatch reports a verified deployment record that does not
// attest the deployment it is recorded for.
var ErrRecordMismatch = errors.New("deployment record does not match")

// Artifact identifies a Preparation and its artifact.
type Artifact struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

// PromotionPredicate is the predicate of a promotion record.
type PromotionPredicate struct {
	// Order is the namespace/name of the stage's Order.
	Order       string    `json:"order"`
	Preparation Artifact  `json:"preparation"`
	Previous    *Artifact `json:"previous,omitempty"`
	// ProductRevision is the Product revision the Preparation renders.
	ProductRevision string `json:"productRevision"`
	// Mode is the promotion mode of the Order.
	Mode string `json:"mode"`
	// Time is when the Preparation passed its gates, in RFC 3339 UTC.
	Time  string `json:"time"`
	Gates Gates  `json:"gates"`
}

// Gates is the evidence of the gates a promotion passed.
type Gates struct {
	// Approvals is set when an approval policy applied.
	Approvals *ApprovalsEvidence `json:"approvals,omitempty"`
	// After holds one entry per upstream stage.
	After []StageEvidence `json:"after,omitempty"`
	// Checks is set when promotion checks applied.
	Checks *ChecksEvidence `json:"checks,omitempty"`
}

// ApprovalsEvidence names the sealed approvals of the Preparation.
type ApprovalsEvidence struct {
	// Attestation is the digest of the approval attestation.
	Attestation string `json:"attestation"`
}

// StageEvidence is what qualified the Preparation for an upstream stage:
// the stage's deployment of the revision, or the stage's own earlier
// promotion of the Preparation.
type StageEvidence struct {
	// Order is the namespace/name of the upstream Order.
	Order       string `json:"order"`
	SoakSeconds int32  `json:"soakSeconds,omitempty"`
	// Deployment is the upstream deployment that qualified the revision.
	Deployment *DeploymentEvidence `json:"deployment,omitempty"`
	// Promotion is the digest of the earlier promotion record of the
	// Preparation by the same stage, which passed the gate before.
	Promotion string `json:"promotion,omitempty"`
}

// DeploymentEvidence names an upstream deployment and its record.
type DeploymentEvidence struct {
	Preparation Artifact `json:"preparation"`
	HealthyTime string   `json:"healthyTime"`
	// Record is the digest of the upstream deployment record.
	Record string `json:"record"`
}

// ChecksEvidence is the evidence of the promotion checks.
type ChecksEvidence struct {
	Vulnerabilities *VulnerabilitiesEvidence `json:"vulnerabilities,omitempty"`
}

// VulnerabilitiesEvidence names the vulnerability report every image of the
// Preparation passed the check with.
type VulnerabilitiesEvidence struct {
	MaxSeverity string          `json:"maxSeverity"`
	Images      []ImageEvidence `json:"images"`
}

// ImageEvidence names an image and the digest of the signed report of its
// scan, a referrer of the vendored image.
type ImageEvidence struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
	Report string `json:"report"`
}

// VulnerabilitiesEvidenceOf returns the evidence of a vulnerability check at
// maxSeverity that the assessed images passed: the report of every scanned
// image.
func VulnerabilitiesEvidenceOf(maxSeverity string, assessments []vulnerability.Assessment) *VulnerabilitiesEvidence {
	evidence := &VulnerabilitiesEvidence{MaxSeverity: maxSeverity, Images: []ImageEvidence{}}
	for _, a := range assessments {
		if a.Scan != nil {
			evidence.Images = append(evidence.Images, ImageEvidence{Name: a.Name, Digest: a.Digest, Report: a.Scan.Report})
		}
	}
	return evidence
}

// DeploymentPredicate is the predicate of a deployment record.
type DeploymentPredicate struct {
	// Order is the namespace/name of the stage's Order.
	Order       string   `json:"order"`
	Preparation Artifact `json:"preparation"`
	// ProductRevision is the Product revision the Preparation renders.
	ProductRevision string `json:"productRevision"`
	// Promotion is the digest of the promotion record this deployment
	// completes.
	Promotion string `json:"promotion"`
	// HealthyTime is when the deployment was first healthy, in RFC 3339
	// UTC.
	HealthyTime string `json:"healthyTime"`
	// Targets are where the Preparation runs.
	Targets []Target `json:"targets"`
}

// Target is a cluster and namespace a Preparation is deployed to. The
// cluster is named by its Cluster, in the Serving's namespace; the server
// says which cluster that was when the record was made.
type Target struct {
	Cluster   string `json:"cluster"`
	Server    string `json:"server"`
	Namespace string `json:"namespace"`
}

// StageEvidenceOf returns the evidence of the upstream gate result passed
// with, one entry per stage, with upstream Orders named as
// namespace/name.
func StageEvidenceOf(namespace string, stages []Stage, result Result) []StageEvidence {
	if len(stages) == 0 {
		return nil
	}
	evidence := make([]StageEvidence, 0, len(stages))
	for i, stage := range stages {
		e := StageEvidence{Order: namespace + "/" + stage.Order, SoakSeconds: stage.SoakSeconds}
		switch {
		case result.Promotion != "":
			e.Promotion = result.Promotion
		case i < len(result.Qualified):
			q := result.Qualified[i].Entry
			e.Deployment = &DeploymentEvidence{
				Preparation: Artifact{Name: q.Preparation, Digest: q.Digest},
				HealthyTime: timestamp(q.Healthy),
				Record:      q.DeploymentRecord,
			}
		}
		evidence = append(evidence, e)
	}
	return evidence
}

// PromotionStatement returns the statement of the promotion record of the
// pending entry by order (namespace/name) under mode, after previous, nil
// for the first promotion. Its bytes depend only on its arguments, so a
// retry finds the record it pushed before.
func PromotionStatement(order, mode string, pending Entry, previous *Entry, gates Gates) ([]byte, error) {
	predicate := PromotionPredicate{
		Order:           order,
		Preparation:     Artifact{Name: pending.Preparation, Digest: pending.Digest},
		ProductRevision: pending.ProductRevision,
		Mode:            mode,
		Time:            timestamp(pending.Started),
		Gates:           gates,
	}
	if previous != nil {
		predicate.Previous = &Artifact{Name: previous.Preparation, Digest: previous.Digest}
	}
	return attestation.NewStatement(pending.OCIRef, pending.Digest, PromotionPredicateType, predicate)
}

// DeploymentStatement returns the statement of the deployment record of the
// healthy entry by order (namespace/name), deployed to targets.
func DeploymentStatement(order string, healthy Entry, targets []Target) ([]byte, error) {
	if healthy.Healthy.IsZero() {
		return nil, errors.New("the deployment has not been healthy")
	}
	return attestation.NewStatement(healthy.OCIRef, healthy.Digest, DeploymentPredicateType, DeploymentPredicate{
		Order:           order,
		Preparation:     Artifact{Name: healthy.Preparation, Digest: healthy.Digest},
		ProductRevision: healthy.ProductRevision,
		Promotion:       healthy.PromotionRecord,
		HealthyTime:     timestamp(healthy.Healthy),
		Targets:         targets,
	})
}

// VerifyDeployment checks that predicate, the predicate of a verified
// deployment record, attests the deployment of e by order
// (namespace/name).
func VerifyDeployment(predicate DeploymentPredicate, order string, e Entry) error {
	switch {
	case predicate.Order != order:
		return fmt.Errorf("%w: it attests a deployment by Order %s, not %s", ErrRecordMismatch, predicate.Order, order)
	case predicate.Preparation.Digest != e.Digest:
		return fmt.Errorf("%w: it attests %s, not %s", ErrRecordMismatch, predicate.Preparation.Digest, e.Digest)
	case predicate.ProductRevision != e.ProductRevision:
		return fmt.Errorf("%w: it attests revision %s, not %s", ErrRecordMismatch, predicate.ProductRevision, e.ProductRevision)
	case predicate.HealthyTime != timestamp(e.Healthy):
		return fmt.Errorf("%w: it attests healthy since %s, not %s", ErrRecordMismatch, predicate.HealthyTime, timestamp(e.Healthy))
	}
	return nil
}

// timestamp formats t as the records store times.
func timestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
