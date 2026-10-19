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
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

const (
	dockerHub      = "docker.io"
	prometheusKind = "Prometheus"
	imagePath      = "spec/image"
	harborMirror   = "oci://harbor.example.com/dockerhub"
	nginxRef       = "nginx:1.27"
	nginxName      = "docker.io/library/nginx"
)

func TestValidateImagesDTO(t *testing.T) {
	tests := []struct {
		name    string
		dto     imagesSettingsDTO
		wantErr string
	}{
		{name: "empty", dto: imagesSettingsDTO{}},
		{name: "valid mirror", dto: imagesSettingsDTO{Mirrors: []imageMirrorDTO{{Prefix: dockerHub, Location: harborMirror}}}},
		{name: "empty prefix", dto: imagesSettingsDTO{Mirrors: []imageMirrorDTO{{Location: harborMirror}}}, wantErr: "must not be empty"},
		{name: "duplicate prefix", dto: imagesSettingsDTO{Mirrors: []imageMirrorDTO{
			{Prefix: dockerHub, Location: "oci://a.example.com/hub"}, {Prefix: dockerHub, Location: "oci://b.example.com/hub"},
		}}, wantErr: "used twice"},
		{name: "not an OCI URL", dto: imagesSettingsDTO{Mirrors: []imageMirrorDTO{{Prefix: dockerHub, Location: "harbor.example.com"}}}, wantErr: "oci:// URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateImagesDTO(&tt.dto)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestImagesSettings(t *testing.T) {
	t.Run("settings without mirrors clear spec.images", func(t *testing.T) {
		assert.Nil(t, imagesSettingsFromDTO(&imagesSettingsDTO{}))
		assert.Equal(t, imagesSettingsDTO{Mirrors: []imageMirrorDTO{}}, imagesSettingsToDTO(nil))
	})

	t.Run("mirrors round-trip", func(t *testing.T) {
		images := &deliveryv1alpha1.KitchenImages{Mirrors: []deliveryv1alpha1.ImageMirror{{Prefix: dockerHub, Location: harborMirror}}}
		dto := imagesSettingsToDTO(images)
		assert.Equal(t, images, imagesSettingsFromDTO(&dto))
	})
}

func TestScanningSettings(t *testing.T) {
	t.Run("disabled settings clear spec.scanning", func(t *testing.T) {
		assert.Nil(t, scanningSettingsFromDTO(&scanningSettingsDTO{IntervalSeconds: 7200}))
		assert.Equal(t, scanningSettingsDTO{}, scanningSettingsToDTO(nil))
	})

	t.Run("enabled settings round-trip", func(t *testing.T) {
		scanning := &deliveryv1alpha1.KitchenScanning{
			IntervalSeconds:  43200,
			DBRepository:     "oci://registry.example.com/aquasec/trivy-db",
			JavaDBRepository: "oci://registry.example.com/aquasec/trivy-java-db",
			RegistryRefs:     []corev1.LocalObjectReference{{Name: "corp-mirror"}},
		}
		dto := scanningSettingsToDTO(scanning)
		assert.True(t, dto.Enabled)
		assert.Equal(t, scanning, scanningSettingsFromDTO(&dto))
		assert.Equal(t, &deliveryv1alpha1.KitchenScanning{}, scanningSettingsFromDTO(&scanningSettingsDTO{Enabled: true}), "the defaults")
	})

	t.Run("the response holds the defaults and the install namespace", func(t *testing.T) {
		resp := settingsToResponse(capsInstallNamespace, deliveryv1alpha1.KitchenSpec{})
		assert.Equal(t, capsInstallNamespace, resp.Namespace)
		assert.False(t, resp.Scanning.Enabled)
		assert.Equal(t, scanningDefaultsDTO{
			IntervalSeconds: 86400, MaxDatabaseAgeSeconds: 604800,
			DBRepository: deliveryv1alpha1.DefaultScanningDBRepository, JavaDBRepository: deliveryv1alpha1.DefaultScanningJavaDBRepository,
		}, resp.ScanningDefaults)
	})
}

func TestValidateScanningDTO(t *testing.T) {
	tests := []struct {
		name    string
		dto     scanningSettingsDTO
		wantErr string
	}{
		{name: "disabled", dto: scanningSettingsDTO{IntervalSeconds: 1}},
		{name: "defaults", dto: scanningSettingsDTO{Enabled: true}},
		{name: "interval too short", dto: scanningSettingsDTO{Enabled: true, IntervalSeconds: 60}, wantErr: "intervalSeconds"},
		{name: "database age too short", dto: scanningSettingsDTO{Enabled: true, MaxDatabaseAgeSeconds: 60}, wantErr: "maxDatabaseAgeSeconds"},
		{name: "not an OCI URL", dto: scanningSettingsDTO{Enabled: true, DBRepository: "mirror.gcr.io/aquasec/trivy-db"}, wantErr: "dbRepository must be an oci:// URL"},
		{name: "a Registry twice", dto: scanningSettingsDTO{Enabled: true, RegistryRefs: []RegistryRefDTO{{Name: "a"}, {Name: "a"}}}, wantErr: "twice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateScanningDTO(&tt.dto)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestHandlePutSettings_scanning(t *testing.T) {
	kitchen := &deliveryv1alpha1.Kitchen{Name: deliveryv1alpha1.DefaultKitchenName, Namespace: capsInstallNamespace,
		Spec: deliveryv1alpha1.KitchenSpec{ArgoCDURL: "https://argocd.example.com"}}
	cluster := adminCluster(t, kitchen)
	backend := testBackend{registry: oci.NewFakeClient(nil), cluster: cluster}
	put := func(body string) settingsResponse {
		t.Helper()
		rec := serveAsAdminWith(t, httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body)), backend)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var resp settingsResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		return resp
	}
	stored := func() *deliveryv1alpha1.KitchenSpec {
		t.Helper()
		got := &deliveryv1alpha1.Kitchen{}
		require.NoError(t, cluster.Get(context.Background(), client.ObjectKeyFromObject(kitchen), got))
		return &got.Spec
	}

	resp := put(`{"scanning":{"enabled":true,"intervalSeconds":43200,"dbRepository":"oci://registry.example.com/aquasec/trivy-db","registryRefs":[{"name":"corp-mirror"}]}}`)
	assert.True(t, resp.Scanning.Enabled)
	assert.Equal(t, capsInstallNamespace, resp.Namespace)
	assert.Equal(t, &deliveryv1alpha1.KitchenScanning{
		IntervalSeconds: 43200, DBRepository: "oci://registry.example.com/aquasec/trivy-db",
		RegistryRefs: []corev1.LocalObjectReference{{Name: "corp-mirror"}},
	}, stored().Scanning)
	assert.Equal(t, "https://argocd.example.com", stored().ArgoCDURL, "other settings stay")

	put(`{"scanning":{"enabled":false,"intervalSeconds":43200}}`)
	assert.Nil(t, stored().Scanning, "disabling removes spec.scanning")

	rec := serveAsAdminWith(t, httptest.NewRequest(http.MethodPut, "/api/v1/settings",
		strings.NewReader(`{"scanning":{"enabled":true,"intervalSeconds":60}}`)), backend)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
