package promotion

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPipeline_Problem(t *testing.T) {
	stage := func(upstreams ...string) PipelineOrder { return PipelineOrder{Product: product, Upstreams: upstreams} }

	tests := []struct {
		name     string
		pipeline Pipeline
		want     string
	}{
		{
			name:     "a chain",
			pipeline: Pipeline{devOrder: stage(), stagingOrder: stage(devOrder), prodOrder: stage(stagingOrder, devOrder)},
		},
		{
			name:     "a missing upstream waits",
			pipeline: Pipeline{prodOrder: stage(stagingOrder)},
		},
		{
			name:     "an upstream of another Product",
			pipeline: Pipeline{stagingOrder: {Product: "other"}, prodOrder: stage(stagingOrder)},
			want:     "upstream Order staging is a variant of Product other, not podinfo",
		},
		{
			name:     "a cycle through the Order",
			pipeline: Pipeline{devOrder: stage(prodOrder), stagingOrder: stage(devOrder), prodOrder: stage(stagingOrder)},
			want:     "the upstream stages form a cycle: prod runs after staging runs after dev runs after prod",
		},
		{
			name:     "a cycle upstream that does not lead back is no problem of the Order",
			pipeline: Pipeline{devOrder: stage(stagingOrder), stagingOrder: stage(devOrder), prodOrder: stage(stagingOrder)},
		},
		{
			name:     "a diamond",
			pipeline: Pipeline{devOrder: stage(), "qa": stage(devOrder), stagingOrder: stage(devOrder), prodOrder: stage("qa", stagingOrder)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.pipeline.Problem(prodOrder))
		})
	}
	assert.Empty(t, Pipeline{}.Problem(prodOrder), "an Order that is not listed")
}
