package promotion

import (
	"fmt"
	"strings"
	"time"
)

// Outcome is the result of the upstream gate.
type Outcome string

// Outcomes of the gate, from the most to the least severe; the gate reports
// the most severe of its stages.
const (
	// OutcomeInvalidPipeline: an upstream Order is a variant of another
	// Product. It cannot pass by waiting.
	OutcomeInvalidPipeline Outcome = "InvalidPipeline"
	// OutcomeNotDeployedUpstream: an upstream stage does not exist, or has
	// not deployed the revision healthily.
	OutcomeNotDeployedUpstream Outcome = "NotDeployedUpstream"
	// OutcomeSoaking: an upstream stage runs the revision healthily, but
	// not for its soak time yet, or its deployment record is not pushed
	// yet.
	OutcomeSoaking Outcome = "Soaking"
	// OutcomeSatisfied: every upstream stage qualifies the revision.
	OutcomeSatisfied Outcome = "Satisfied"
)

// severity orders the outcomes; higher is more severe.
var severity = map[Outcome]int{
	OutcomeSatisfied:           0,
	OutcomeSoaking:             1,
	OutcomeNotDeployedUpstream: 2,
	OutcomeInvalidPipeline:     3,
}

// Stage is an upstream stage a Preparation names.
type Stage struct {
	// Order is the name of the upstream Order.
	Order string
	// SoakSeconds is how long the upstream Order must have run the
	// revision healthily.
	SoakSeconds int32
}

// Soak returns how long the upstream Order must have run the revision
// healthily.
func (s Stage) Soak() time.Duration {
	return time.Duration(s.SoakSeconds) * time.Second
}

// Upstream is what Kokumi knows of an upstream Order.
type Upstream struct {
	// Product is the Product the Order is a variant of.
	Product string
	// History is the history of its Serving, empty when it has none.
	History History
}

// Request asks whether a stage may deploy a Preparation.
type Request struct {
	// Preparation is the name of the Preparation.
	Preparation string
	// Product is the Product of the stage's Order.
	Product string
	// Revision is the Product revision the Preparation renders.
	Revision string
	// Stages are the upstream stages the Preparation names.
	Stages []Stage
	// Self is the history of the stage.
	Self History
	// Upstreams holds the upstream Orders by name. An Order that is missing
	// does not exist.
	Upstreams map[string]Upstream
	// Now is the time the gate is evaluated at.
	Now time.Time
}

// Result is the decision of the upstream gate.
type Result struct {
	Outcome Outcome
	// Message explains the outcome.
	Message string
	// RequeueAfter is how long until the first soak that is under way
	// completes, 0 when none is.
	RequeueAfter time.Duration
	// Qualified holds, for a satisfied gate, the upstream deployment that
	// qualifies the revision for each stage, in the order of the stages.
	// It is empty when the stage promoted the Preparation before.
	Qualified []Qualified
	// Promotion is, for a gate that the stage's earlier promotion of the
	// Preparation satisfies, the digest of that promotion's record.
	Promotion string
}

// Satisfied reports whether the gate lets the Preparation be deployed.
func (r Result) Satisfied() bool { return r.Outcome == OutcomeSatisfied }

// Qualified is the upstream deployment that qualifies a revision for a
// stage.
type Qualified struct {
	Stage Stage
	Entry Entry
}

// verdict is the outcome of one stage.
type verdict struct {
	outcome   Outcome
	message   string
	remaining time.Duration
	qualified Entry
}

// Evaluate decides whether the stage may deploy the Preparation of
// req.Revision. It may once every upstream stage has a deployment of its own
// Preparation of the revision that was healthy, without degrading, for the
// stage's soak time, and whose deployment record is pushed. An upstream
// stage that has moved on since still qualifies the revision. A Preparation
// the stage promoted before passes again, so a rollback is never held by an
// upstream stage that moved on.
func Evaluate(req Request) Result {
	if len(req.Stages) == 0 {
		return Result{Outcome: OutcomeSatisfied, Message: "no upstream stages"}
	}
	if earlier := req.Self.PromotedBefore(req.Preparation); earlier != nil {
		return Result{
			Outcome:   OutcomeSatisfied,
			Message:   "promoted here at " + earlier.Started.Format(time.RFC3339),
			Promotion: earlier.PromotionRecord,
		}
	}

	result := Result{Outcome: OutcomeSatisfied}
	messages := make([]string, 0, len(req.Stages))
	for _, stage := range req.Stages {
		v := evaluateStage(req, stage)
		switch {
		case severity[v.outcome] > severity[result.Outcome]:
			result.Outcome = v.outcome
			messages = append(messages[:0], v.message)
		case v.outcome == result.Outcome && v.outcome != OutcomeSatisfied:
			messages = append(messages, v.message)
		}
		if v.outcome == OutcomeSatisfied {
			result.Qualified = append(result.Qualified, Qualified{Stage: stage, Entry: v.qualified})
		}
		if v.remaining > 0 && (result.RequeueAfter == 0 || v.remaining < result.RequeueAfter) {
			result.RequeueAfter = v.remaining
		}
	}

	if !result.Satisfied() {
		result.Qualified = nil
		result.Message = strings.Join(messages, "; ")
		return result
	}
	names := make([]string, 0, len(req.Stages))
	for _, stage := range req.Stages {
		names = append(names, stage.Order)
	}
	result.Message = "deployed upstream by " + strings.Join(names, ", ")
	return result
}

// evaluateStage decides whether stage qualifies the revision of req.
func evaluateStage(req Request, stage Stage) verdict {
	upstream, ok := req.Upstreams[stage.Order]
	if !ok {
		return verdict{outcome: OutcomeNotDeployedUpstream, message: fmt.Sprintf("Order %s does not exist", stage.Order)}
	}
	if upstream.Product != req.Product {
		return verdict{outcome: OutcomeInvalidPipeline,
			message: fmt.Sprintf("Order %s is a variant of Product %s, not %s", stage.Order, upstream.Product, req.Product)}
	}

	var soaking *verdict
	for _, e := range upstream.History {
		if e.ProductRevision != req.Revision || !e.Promoted() || e.Healthy.IsZero() || e.EndReason == EndDegraded {
			continue
		}
		until := req.Now
		if !e.Running() {
			until = e.Ended
		}
		healthyFor := until.Sub(e.Healthy)
		switch {
		case healthyFor >= stage.Soak() && e.DeploymentRecord != "":
			return verdict{outcome: OutcomeSatisfied, qualified: e}
		case healthyFor >= stage.Soak() && soaking == nil:
			soaking = &verdict{outcome: OutcomeSoaking,
				message: fmt.Sprintf("Order %s is recording its deployment of %s", stage.Order, e.Preparation)}
		case e.Running() && soaking == nil:
			soaking = &verdict{outcome: OutcomeSoaking, remaining: stage.Soak() - healthyFor,
				message: fmt.Sprintf("Order %s runs %s healthily; its soak of %s ends at %s",
					stage.Order, e.Preparation, stage.Soak(), e.Healthy.Add(stage.Soak()).UTC().Format(time.RFC3339))}
		}
	}
	if soaking != nil {
		return *soaking
	}

	message := fmt.Sprintf("Order %s has not deployed revision %s healthily", stage.Order, short(req.Revision))
	if deployed := upstream.History.Deployed(); deployed != nil && deployed.ProductRevision != req.Revision {
		message += fmt.Sprintf("; it runs %s of revision %s", deployed.Preparation, short(deployed.ProductRevision))
	}
	return verdict{outcome: OutcomeNotDeployedUpstream, message: message}
}

// short returns the first 12 hex digits of a sha256 digest, as Kokumi names
// Preparations after their digest.
func short(digest string) string {
	hex := strings.TrimPrefix(digest, "sha256:")
	return hex[:min(len(hex), 12)]
}
