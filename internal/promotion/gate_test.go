package promotion

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const product = "podinfo"

// healthyEntry returns a promoted, recorded entry of a staging Preparation
// of revision, healthy from healthy on.
func healthyEntry(name, revision string, healthy time.Time) Entry {
	e := candidate(name, revision)
	e.Started = healthy.Add(-time.Minute)
	e.Healthy = healthy
	e.PromotionRecord = record1
	e.DeploymentRecord = record2
	return e
}

func TestEvaluate(t *testing.T) {
	now := t0.Add(24 * time.Hour)
	staging := Stage{Order: stagingOrder, SoakSeconds: 3600}

	// request asks whether prod may deploy its Preparation of revision1
	// after staging, whose Serving has history.
	request := func(history ...Entry) Request {
		return Request{
			Preparation: prodPreparation,
			Product:     product,
			Revision:    revision1,
			Stages:      []Stage{staging},
			Upstreams:   map[string]Upstream{stagingOrder: {Product: product, History: history}},
			Now:         now,
		}
	}

	tests := []struct {
		name        string
		req         Request
		want        Outcome
		wantRequeue time.Duration
		wantMessage string
	}{
		{
			name: "soaked for exactly the soak time",
			req:  request(healthyEntry("staging-1", revision1, now.Add(-time.Hour))),
			want: OutcomeSatisfied,
		},
		{
			name:        "still soaking",
			req:         request(healthyEntry("staging-1", revision1, now.Add(-time.Hour+time.Second))),
			want:        OutcomeSoaking,
			wantRequeue: time.Second,
			wantMessage: "Order staging runs staging-1 healthily; its soak of 1h0m0s ends at 2026-11-04T10:00:01Z",
		},
		{
			name: "moved on after soaking",
			req: request(
				healthyEntry("staging-2", revision2, now.Add(-time.Minute)),
				func() Entry {
					e := healthyEntry("staging-1", revision1, now.Add(-3*time.Hour))
					e.Ended, e.EndReason = now.Add(-2*time.Hour), EndSuperseded
					return e
				}(),
			),
			want: OutcomeSatisfied,
		},
		{
			name: "moved on before soaking",
			req: request(
				healthyEntry("staging-2", revision2, now.Add(-time.Minute)),
				func() Entry {
					e := healthyEntry("staging-1", revision1, now.Add(-3*time.Hour))
					e.Ended, e.EndReason = e.Healthy.Add(time.Minute), EndSuperseded
					return e
				}(),
			),
			want:        OutcomeNotDeployedUpstream,
			wantMessage: "Order staging has not deployed revision 111111111111 healthily; it runs staging-2 of revision 222222222222",
		},
		{
			name: "degraded after soaking does not qualify",
			req: request(func() Entry {
				e := healthyEntry("staging-1", revision1, now.Add(-3*time.Hour))
				e.Ended, e.EndReason = now.Add(-time.Minute), EndDegraded
				return e
			}()),
			want: OutcomeNotDeployedUpstream,
		},
		{
			name: "a recovery soaks again",
			req: request(
				healthyEntry("staging-1", revision1, now.Add(-30*time.Minute)),
				func() Entry {
					e := healthyEntry("staging-1", revision1, now.Add(-3*time.Hour))
					e.Ended, e.EndReason = now.Add(-31*time.Minute), EndDegraded
					return e
				}(),
			),
			want:        OutcomeSoaking,
			wantRequeue: 30 * time.Minute,
		},
		{
			name: "soaked, the deployment record not pushed yet",
			req: request(func() Entry {
				e := healthyEntry("staging-1", revision1, now.Add(-2*time.Hour))
				e.DeploymentRecord = ""
				return e
			}()),
			want:        OutcomeSoaking,
			wantMessage: "Order staging is recording its deployment of staging-1",
		},
		{
			name: "deploying, not healthy yet",
			req: request(func() Entry {
				e := candidate("staging-1", revision1)
				e.Started, e.PromotionRecord = now, record1
				return e
			}()),
			want: OutcomeNotDeployedUpstream,
		},
		{
			name: "nothing deployed upstream",
			req:  request(),
			want: OutcomeNotDeployedUpstream,
		},
		{
			name: "upstream Order does not exist",
			req: func() Request {
				r := request()
				r.Upstreams = nil
				return r
			}(),
			want:        OutcomeNotDeployedUpstream,
			wantMessage: "Order staging does not exist",
		},
		{
			name: "upstream Order of another Product",
			req: func() Request {
				r := request(healthyEntry("staging-1", revision1, now.Add(-2*time.Hour)))
				r.Upstreams[stagingOrder] = Upstream{Product: "other", History: r.Upstreams[stagingOrder].History}
				return r
			}(),
			want:        OutcomeInvalidPipeline,
			wantMessage: "Order staging is a variant of Product other, not podinfo",
		},
		{
			name: "promoted here before passes without upstream",
			req: func() Request {
				r := request()
				r.Self = deployed(nil, candidate(prodPreparation, revision1), t0, record1)
				return r
			}(),
			want: OutcomeSatisfied,
		},
		{
			name: "no upstream stages",
			req:  Request{Revision: revision1, Now: now},
			want: OutcomeSatisfied,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Evaluate(tt.req)
			assert.Equal(t, tt.want, got.Outcome, got.Message)
			assert.Equal(t, tt.wantRequeue, got.RequeueAfter)
			if tt.wantMessage != "" {
				assert.Equal(t, tt.wantMessage, got.Message)
			}
			assert.Equal(t, tt.want == OutcomeSatisfied, got.Satisfied())
		})
	}
}

func TestEvaluate_evidence(t *testing.T) {
	now := t0.Add(24 * time.Hour)
	qa := healthyEntry("qa-1", revision1, now.Add(-time.Hour))
	older := healthyEntry("staging-1", revision1, now.Add(-5*time.Hour))
	older.Ended, older.EndReason = now.Add(-4*time.Hour), EndDegraded
	newer := healthyEntry("staging-1", revision1, now.Add(-3*time.Hour))

	stages := []Stage{{Order: "qa"}, {Order: stagingOrder, SoakSeconds: 7200}}
	got := Evaluate(Request{
		Preparation: prodPreparation, Product: product, Revision: revision1, Stages: stages, Now: now,
		Upstreams: map[string]Upstream{
			"qa":         {Product: product, History: History{qa}},
			stagingOrder: {Product: product, History: History{newer, older}},
		},
	})
	require.True(t, got.Satisfied(), got.Message)
	assert.Equal(t, "deployed upstream by qa, staging", got.Message)
	require.Len(t, got.Qualified, 2)
	assert.Equal(t, newer, got.Qualified[1].Entry, "the newest qualifying deployment")

	evidence := StageEvidenceOf("shop", stages, got)
	assert.Equal(t, []StageEvidence{
		{Order: "shop/qa", Deployment: &DeploymentEvidence{
			Preparation: Artifact{Name: "qa-1", Digest: qa.Digest}, HealthyTime: "2026-11-04T09:00:00Z", Record: record2}},
		{Order: "shop/staging", SoakSeconds: 7200, Deployment: &DeploymentEvidence{
			Preparation: Artifact{Name: "staging-1", Digest: newer.Digest}, HealthyTime: "2026-11-04T07:00:00Z", Record: record2}},
	}, evidence)

	t.Run("the most severe stage is reported, all its messages joined", func(t *testing.T) {
		got := Evaluate(Request{
			Preparation: prodPreparation, Product: product, Revision: revision1, Now: now,
			Stages:    []Stage{{Order: "a"}, {Order: "b"}, {Order: "c", SoakSeconds: 86400}},
			Upstreams: map[string]Upstream{"c": {Product: product, History: History{qa}}},
		})
		assert.Equal(t, OutcomeNotDeployedUpstream, got.Outcome)
		assert.Equal(t, "Order a does not exist; Order b does not exist", got.Message)
		assert.Equal(t, 23*time.Hour, got.RequeueAfter, "the soak under way still wakes the gate")
		assert.Empty(t, got.Qualified)
	})

	t.Run("an earlier promotion is the evidence of a rollback", func(t *testing.T) {
		self := deployed(nil, candidate(prodPreparation, revision1), t0, record1)
		self = deployed(self, candidate("prod-2", revision2), t0.Add(time.Hour), record2)
		got := Evaluate(Request{Preparation: prodPreparation, Product: product, Revision: revision1, Stages: stages, Self: self, Now: now})
		require.True(t, got.Satisfied())
		assert.Equal(t, []StageEvidence{{Order: "shop/qa", Promotion: record1}, {Order: "shop/staging", SoakSeconds: 7200, Promotion: record1}},
			StageEvidenceOf("shop", stages, got))
	})
}
