package oci

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbeReferrersAPI(t *testing.T) {
	tests := []struct {
		name          string
		handler       http.HandlerFunc
		wantSupported bool
		wantErr       bool
	}{
		{
			name: "referrers API answers with an index",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", ocispec.MediaTypeImageIndex)
				_, _ = w.Write([]byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`))
			},
			wantSupported: true,
		},
		{
			name: "referrers API not found",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			wantSupported: false,
		},
		{
			name: "repository unknown",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"errors":[{"code":"NAME_UNKNOWN","message":"repository name not known to registry"}]}`))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/referrers/") {
					tt.handler(w, r)
					return
				}
				// Anything else is the tag-schema fallback lookup.
				w.WriteHeader(http.StatusNotFound)
			}))
			t.Cleanup(srv.Close)

			ref, err := Parse(strings.TrimPrefix(srv.URL, "http://") + "/test/repo")
			require.NoError(t, err)

			supported, err := ProbeReferrersAPI(context.Background(), ref, nil)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantSupported, supported)
		})
	}
}
