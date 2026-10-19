package scanner

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// engineFunc adapts a function to Engine.
type engineFunc func(ctx context.Context, req Request) (*Response, error)

func (f engineFunc) Scan(ctx context.Context, req Request) (*Response, error) { return f(ctx, req) }

func validRequest() Request {
	return Request{
		Database: Database{Ref: "oci://mirror.gcr.io/aquasec/trivy-db@" + testDigest},
		Images:   []Image{{Ref: "oci://registry.internal/shop/vendor/app/images/nginx@" + testDigest}},
	}
}

func echoEngine(_ context.Context, req Request) (*Response, error) {
	resp := &Response{Scanner: ScannerInfo{Name: scannerName, Version: testVersion}, Database: DatabaseInfo{Ref: req.Database.Ref}}
	for _, img := range req.Images {
		resp.Images = append(resp.Images, Result{Ref: img.Ref, Report: []byte(testReport)})
	}
	return resp, nil
}

func TestClientAndHandler(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(NewHandler(engineFunc(echoEngine), 1, logr.Discard()))
	t.Cleanup(server.Close)
	client := NewClient(server.URL + "/")

	resp, err := client.Scan(ctx, validRequest())
	require.NoError(t, err)
	assert.Equal(t, testVersion, resp.Scanner.Version)
	require.Len(t, resp.Images, 1)
	assert.JSONEq(t, testReport, string(resp.Images[0].Report))

	health, err := http.Get(server.URL + "/healthz")
	require.NoError(t, err)
	_ = health.Body.Close()
	assert.Equal(t, http.StatusOK, health.StatusCode)
}

func TestHandler_rejects(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(NewHandler(engineFunc(echoEngine), 1, logr.Discard()))
	t.Cleanup(server.Close)
	client := NewClient(server.URL)

	byTag := validRequest()
	byTag.Database.Ref = "oci://mirror.gcr.io/aquasec/trivy-db:2"
	imageByTag := validRequest()
	imageByTag.Images[0].Ref = "oci://registry.internal/app:1.0"
	none := validRequest()
	none.Images = nil

	for name, req := range map[string]Request{"database by tag": byTag, "image by tag": imageByTag, "no images": none} {
		_, err := client.Scan(ctx, req)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "400", name)
		assert.NotErrorIs(t, err, ErrUnavailable, name)
	}
}

func TestHandler_busy(t *testing.T) {
	ctx := context.Background()
	started, release := make(chan struct{}), make(chan struct{})
	blocking := engineFunc(func(ctx context.Context, req Request) (*Response, error) {
		close(started)
		<-release
		return echoEngine(ctx, req)
	})
	server := httptest.NewServer(NewHandler(blocking, 1, logr.Discard()))
	t.Cleanup(server.Close)
	client := NewClient(server.URL)

	done := make(chan error, 1)
	go func() {
		_, err := client.Scan(ctx, validRequest())
		done <- err
	}()
	<-started

	_, err := client.Scan(ctx, validRequest())
	var unavailable *UnavailableError
	require.ErrorAs(t, err, &unavailable, "the only slot is taken")
	assert.ErrorIs(t, err, ErrUnavailable)
	assert.Equal(t, 30*time.Second, unavailable.RetryAfter)

	close(release)
	require.NoError(t, <-done)
}

func TestHandler_engineError(t *testing.T) {
	failing := engineFunc(func(context.Context, Request) (*Response, error) {
		return nil, errors.Join(errBadDatabase, errors.New("no trivy.db"))
	})
	server := httptest.NewServer(NewHandler(failing, 1, logr.Discard()))
	t.Cleanup(server.Close)

	_, err := NewClient(server.URL).Scan(context.Background(), validRequest())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "422")
	assert.Contains(t, err.Error(), "no trivy.db")
}

func TestClient_unavailable(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()

	_, err := NewClient(url).Scan(context.Background(), validRequest())
	require.ErrorIs(t, err, ErrUnavailable, "a scanner that does not answer")
}

func TestClient_resultCount(t *testing.T) {
	short := engineFunc(func(context.Context, Request) (*Response, error) {
		return &Response{Scanner: ScannerInfo{Name: scannerName}}, nil
	})
	server := httptest.NewServer(NewHandler(short, 1, logr.Discard()))
	t.Cleanup(server.Close)

	_, err := NewClient(server.URL).Scan(context.Background(), validRequest())
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "0 results for 1 images"), err.Error())
}
