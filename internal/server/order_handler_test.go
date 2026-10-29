package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

func TestOrderHandlers_registryRefs(t *testing.T) {
	const picked, changed = "team-registry", "mirror-registry"
	cluster := adminCluster(t)
	backend := testBackend{registry: oci.NewFakeClient(nil), cluster: cluster}
	key := types.NamespacedName{Namespace: nsA, Name: webOrder}
	path := "/api/v1/orders/" + nsA + "/" + webOrder
	stored := func() []corev1.LocalObjectReference {
		t.Helper()
		order := &deliveryv1alpha1.Order{}
		require.NoError(t, cluster.Get(context.Background(), key, order))
		return order.Spec.RegistryRefs
	}

	create := `{"namespace":"` + nsA + `","name":"` + webOrder + `","productRef":{"name":"web"},"environmentRef":{"name":"local"},"mode":"Manual",` +
		`"registryRefs":[{"name":"` + picked + `"}]}`
	rec := serveAsAdminWith(t, httptest.NewRequest(http.MethodPost, "/api/v1/orders", strings.NewReader(create)), backend)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var dto OrderDTO
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, []RegistryRefDTO{{Name: picked}}, dto.RegistryRefs)
	assert.Equal(t, []corev1.LocalObjectReference{{Name: picked}}, stored())

	update := `{"mode":"Manual","registryRefs":[{"name":"` + changed + `"}]}`
	rec = serveAsAdminWith(t, httptest.NewRequest(http.MethodPut, path, strings.NewReader(update)), backend)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []corev1.LocalObjectReference{{Name: changed}}, stored(), "an update replaces the list")

	rec = serveAsAdminWith(t, httptest.NewRequest(http.MethodPut, path, strings.NewReader(`{"mode":"Manual"}`)), backend)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, stored(), "an update without registryRefs removes them")
}
