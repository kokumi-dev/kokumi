package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
)

const (
	indexHTML = "<html>app</html>"
)

func TestSPAHandler(t *testing.T) {
	dist := fstest.MapFS{
		"index.html":       {Data: []byte(indexHTML)},
		"assets/app-1.js":  {Data: []byte("console.log(1)")},
		"favicon.svg":      {Data: []byte("<svg/>")},
		"assets/style.css": {Data: []byte("body{}")},
	}
	h := spaHandler(dist)

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
	}{
		{name: "root serves index", path: "/", wantStatus: http.StatusOK, wantBody: indexHTML},
		{name: "asset served", path: "/assets/app-1.js", wantStatus: http.StatusOK, wantBody: "console.log(1)"},
		{name: "deep link falls back to index", path: "/orders/default/web/files", wantStatus: http.StatusOK, wantBody: indexHTML},
		{name: "settings tab falls back to index", path: "/settings/authentication", wantStatus: http.StatusOK, wantBody: indexHTML},
		{name: "missing asset is 404", path: "/assets/missing.js", wantStatus: http.StatusNotFound},
		{name: "unknown api path is 404", path: "/api/v1/unknown", wantStatus: http.StatusNotFound},
		{name: "traversal is rejected", path: "/../../etc/passwd", wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.URL.Path = tt.path
			h.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
			if tt.wantBody != "" {
				assert.Contains(t, rec.Body.String(), tt.wantBody)
			}
		})
	}
}
