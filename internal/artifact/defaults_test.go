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

func TestDefaultVendorDestination(t *testing.T) {
	assert.Equal(t,
		"oci://kokumi-registry.kokumi.svc.cluster.local:5000/team/vendor/app",
		DefaultVendorDestination("team", "app"))
	assert.NotEqual(t, DefaultDestination("team", "app"), DefaultVendorDestination("team", "app"),
		"a Product and an Order of the same name must not share a repository")
}
