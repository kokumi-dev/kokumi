package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

func TestHandleGetPreparationManifest(t *testing.T) {
	prep := &deliveryv1alpha1.Preparation{
		Name:      webPreparation,
		Namespace: nsA,
		Spec: deliveryv1alpha1.PreparationSpec{Artifact: deliveryv1alpha1.Artifact{
			OCIRef: "oci://registry.example.com/shop/web@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/preparations/"+nsA+"/"+prep.Name+"/manifest", nil)
	requireFiles(t, serveAsAdmin(t, req, prep), fakeManifestFile)
}
