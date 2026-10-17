package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
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
