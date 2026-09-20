package artifact

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultDestination(t *testing.T) {
	assert.Equal(t,
		"oci://kokumi-registry.kokumi.svc.cluster.local:5000/team/app",
		DefaultDestination("team", "app"))
}
