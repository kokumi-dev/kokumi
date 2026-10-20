package promotion

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

const (
	devOrder        = "dev"
	stagingOrder    = "staging"
	prodOrder       = "prod"
	prodPreparation = "prod-1"
	app1            = "app-1"
	stagingKey      = "shop/podinfo-staging"

	revision1 = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	revision2 = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	record1   = "sha256:aaaa111111111111111111111111111111111111111111111111111111111111"
	record2   = "sha256:aaaa222222222222222222222222222222222222222222222222222222222222"
)

// candidate returns the Entry template of Preparation name of revision.
func candidate(name, revision string) Entry {
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(name)))
	return Entry{
		Preparation:     name,
		Digest:          digest,
		OCIRef:          "oci://registry.example/shop/" + name + "@" + digest,
		ProductRevision: revision,
	}
}

// deployed returns h with candidate opened at at and started under record.
func deployed(h History, c Entry, at time.Time, record string) History {
	return h.Open(c, at).Start(record)
}

func TestHistory_promotion(t *testing.T) {
	p1, p2 := candidate(app1, revision1), candidate("app-2", revision2)

	var h History
	assert.Nil(t, h.Deployed())
	assert.Nil(t, h.Pending())

	h = h.Open(p1, t0.Add(500*time.Millisecond))
	require.NotNil(t, h.Pending())
	assert.Nil(t, h.Deployed(), "a pending promotion deploys nothing yet")
	assert.Equal(t, t0, h[0].Started, "times are kept in whole seconds")

	h = h.Start(record1)
	require.NotNil(t, h.Deployed())
	assert.Equal(t, app1, h.Deployed().Preparation)
	assert.Nil(t, h.Pending())

	h = h.Open(p2, t0.Add(time.Hour))
	assert.Equal(t, app1, h.Deployed().Preparation, "the deployed entry stays until the promotion is recorded")
	assert.True(t, h[1].Running())

	dropped := h.Drop()
	assert.Len(t, dropped, 1)
	assert.True(t, dropped[0].Running(), "dropping a pending promotion leaves the deployed entry as it was")

	h = h.Start(record2)
	assert.Equal(t, "app-2", h.Deployed().Preparation)
	assert.Equal(t, EndSuperseded, h[1].EndReason)
	assert.Equal(t, t0.Add(time.Hour), h[1].Ended, "the previous deployment ends when the next one starts")

	assert.Equal(t, record1, h.PromotedBefore(app1).PromotionRecord)
	assert.Nil(t, h.PromotedBefore("app-3"))
	assert.Equal(t, h, h.Start("sha256:other"), "nothing is pending")
}

func TestHistory_Open_replacesPending(t *testing.T) {
	h := History{}.Open(candidate(app1, revision1), t0).Open(candidate("app-2", revision1), t0.Add(time.Minute))
	require.Len(t, h, 1)
	assert.Equal(t, "app-2", h[0].Preparation)
}

func TestHistory_Observe(t *testing.T) {
	p1 := candidate(app1, revision1)
	h := deployed(nil, p1, t0, record1)

	tests := []struct {
		name        string
		history     History
		observation Observation
		wantChanged bool
		check       func(t *testing.T, h History)
	}{
		{
			name:        "another revision says nothing",
			history:     h,
			observation: Observation{Revision: "sha256:other", Health: HealthHealthy},
		},
		{
			name:        "progressing changes nothing",
			history:     h,
			observation: Observation{Revision: p1.Digest, Health: HealthProgressing},
		},
		{
			name:        "healthy records when it first was",
			history:     h,
			observation: Observation{Revision: p1.Digest, Health: HealthHealthy},
			wantChanged: true,
			check: func(t *testing.T, h History) {
				assert.Equal(t, t0.Add(time.Minute), h[0].Healthy)
				assert.True(t, h[0].Running())
			},
		},
		{
			name:        "healthy again changes nothing",
			history:     History{{Preparation: app1, Digest: p1.Digest, Started: t0, Healthy: t0, PromotionRecord: record1}},
			observation: Observation{Revision: p1.Digest, Health: HealthHealthy},
		},
		{
			name:        "degraded ends the deployment",
			history:     h,
			observation: Observation{Revision: p1.Digest, Health: HealthDegraded},
			wantChanged: true,
			check: func(t *testing.T, h History) {
				assert.Equal(t, EndDegraded, h[0].EndReason)
				assert.Equal(t, t0.Add(time.Minute), h[0].Ended)
			},
		},
		{
			name: "a recovery opens a new entry of the same promotion",
			history: History{{Preparation: app1, Digest: p1.Digest, ProductRevision: revision1, Started: t0, Healthy: t0,
				Ended: t0.Add(time.Second), EndReason: EndDegraded, PromotionRecord: record1, DeploymentRecord: record2}},
			observation: Observation{Revision: p1.Digest, Health: HealthHealthy},
			wantChanged: true,
			check: func(t *testing.T, h History) {
				require.Len(t, h, 2)
				assert.Equal(t, t0.Add(time.Minute), h[0].Started)
				assert.Equal(t, t0.Add(time.Minute), h[0].Healthy, "soak counts again from the recovery")
				assert.Equal(t, record1, h[0].PromotionRecord)
				assert.Empty(t, h[0].DeploymentRecord, "the recovery is recorded on its own")
				assert.Equal(t, app1, h.Deployed().Preparation)
				assert.Equal(t, EndDegraded, h[1].EndReason)
			},
		},
		{
			name:        "degraded again after a degrade changes nothing",
			history:     History{{Preparation: app1, Digest: p1.Digest, Started: t0, Ended: t0, EndReason: EndDegraded, PromotionRecord: record1}},
			observation: Observation{Revision: p1.Digest, Health: HealthDegraded},
		},
		{
			name:        "a pending promotion keeps its place before a recovery",
			history:     History{{Preparation: "app-2"}, {Preparation: app1, Digest: p1.Digest, Ended: t0, EndReason: EndDegraded, PromotionRecord: record1}},
			observation: Observation{Revision: p1.Digest, Health: HealthHealthy},
			wantChanged: true,
			check: func(t *testing.T, h History) {
				require.Len(t, h, 3)
				assert.Equal(t, "app-2", h.Pending().Preparation)
				assert.True(t, h[1].Running())
			},
		},
		{
			name:        "nothing deployed",
			history:     History{}.Open(p1, t0),
			observation: Observation{Revision: p1.Digest, Health: HealthHealthy},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := append(History(nil), tt.history...)
			got, changed := tt.history.Observe(tt.observation, t0.Add(time.Minute+300*time.Millisecond))
			assert.Equal(t, tt.wantChanged, changed)
			assert.Equal(t, before, tt.history, "the history observed is not modified")
			if !tt.wantChanged {
				assert.Equal(t, tt.history, got)
				return
			}
			tt.check(t, got)
		})
	}
}

func TestHistory_bounded(t *testing.T) {
	var h History
	for i := range 2 * MaxEntries {
		h = deployed(h, candidate(fmt.Sprintf("app-%d", i), revision1), t0.Add(time.Duration(i)*time.Minute), record1)
	}
	assert.Len(t, h, MaxEntries)
	assert.Equal(t, fmt.Sprintf("app-%d", 2*MaxEntries-1), h.Deployed().Preparation)
}

func TestHistory_deploymentRecords(t *testing.T) {
	p1 := candidate(app1, revision1)
	h := deployed(nil, p1, t0, record1)
	assert.Empty(t, h.Unrecorded(), "a deployment that was never healthy is not recorded")

	h, _ = h.Observe(Observation{Revision: p1.Digest, Health: HealthHealthy}, t0.Add(time.Minute))
	unrecorded := h.Unrecorded()
	require.Len(t, unrecorded, 1)

	h = h.RecordDeployment(p1.Digest, unrecorded[0].Healthy, record2)
	assert.Equal(t, record2, h[0].DeploymentRecord)
	assert.Empty(t, h.Unrecorded())
}

func TestHistory_Next(t *testing.T) {
	p1, p2 := candidate(app1, revision1), candidate("app-2", revision2)
	running := deployed(nil, p1, t0, record1)

	tests := []struct {
		name      string
		history   History
		candidate string
		want      Step
	}{
		{"nothing deployed", nil, p1.Preparation, StepOpen},
		{"deployed already", running, p1.Preparation, StepDeployed},
		{"another is deployed", running, p2.Preparation, StepOpen},
		{"pending from the first pass", running.Open(p2, t0.Add(time.Minute)), p2.Preparation, StepRecord},
		{"another is pending", running.Open(p2, t0.Add(time.Minute)), "app-3", StepOpen},
		{"deployed while another is pending", running.Open(p2, t0.Add(time.Minute)), p1.Preparation, StepDeployed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.history.Next(tt.candidate))
		})
	}
}
