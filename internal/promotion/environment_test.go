package promotion

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	webDev     = "web-dev"
	webStaging = "web-staging"
)

func TestRequired(t *testing.T) {
	environments := map[string]string{webStaging: "staging", webDev: "dev"}
	staging := make([]Requirement, 0, 2)
	staging = append(staging, Requirement{Environment: "staging", MinSoakSeconds: 3600})

	assert.Empty(t, Required(nil, environments, nil), "an Environment without requirements")
	assert.Empty(t, Required([]Stage{{Order: webStaging, SoakSeconds: 3600}}, environments, staging))
	assert.Empty(t, Required([]Stage{{Order: webDev}, {Order: webStaging, SoakSeconds: 7200}}, environments, staging))

	assert.Contains(t, Required(nil, environments, staging), "Environment staging with a soak of at least 3600s")
	assert.NotEmpty(t, Required([]Stage{{Order: webStaging, SoakSeconds: 60}}, environments, staging), "too short a soak")
	assert.NotEmpty(t, Required([]Stage{{Order: webDev, SoakSeconds: 3600}}, environments, staging), "another Environment")
	assert.NotEmpty(t, Required([]Stage{{Order: "gone", SoakSeconds: 3600}}, environments, staging), "an Order that does not exist")

	both := Required(nil, environments, append(staging, Requirement{Environment: "dev"}))
	assert.Contains(t, both, "staging")
	assert.Contains(t, both, "dev")
}
