package cluster

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/rest"
)

// fakeAPIServer returns a TLS server that answers /version and /readyz as a
// control plane does, and the configuration to connect to it with token.
func fakeAPIServer(t *testing.T, token string, ready bool) (*httptest.Server, *rest.Config) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Unauthorized","code":401}`))
			return
		}
		switch r.URL.Path {
		case "/version":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"major":"1","minor":"36","gitVersion":"v1.36.2"}`))
		case "/readyz":
			if !ready {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("[-]etcd failed"))
				return
			}
			_, _ = w.Write([]byte("ok"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	return server, &rest.Config{Host: server.URL, BearerToken: "token", CAData: ca}
}

func TestProbe(t *testing.T) {
	ctx := context.Background()

	t.Run("reports a ready control plane healthy, with its version", func(t *testing.T) {
		_, cfg := fakeAPIServer(t, testToken, true)
		health := Probe(ctx, cfg)
		assert.True(t, health.Healthy)
		assert.Equal(t, "v1.36.2", health.Version)
	})

	t.Run("reports a control plane that is not ready", func(t *testing.T) {
		_, cfg := fakeAPIServer(t, testToken, false)
		health := Probe(ctx, cfg)
		assert.False(t, health.Healthy)
		assert.False(t, health.Unauthorized)
		assert.Equal(t, "v1.36.2", health.Version)
	})

	t.Run("reports a rejected credential", func(t *testing.T) {
		_, cfg := fakeAPIServer(t, "rotated", true)
		health := Probe(ctx, cfg)
		assert.False(t, health.Healthy)
		assert.True(t, health.Unauthorized)
	})

	t.Run("reports a control plane it cannot reach", func(t *testing.T) {
		server, cfg := fakeAPIServer(t, testToken, true)
		server.Close()
		health := Probe(ctx, cfg)
		assert.False(t, health.Healthy)
		assert.False(t, health.Unauthorized)
	})
}
