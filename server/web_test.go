package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebRoutes(t *testing.T) {
	ta := newTestAPI(t)
	mux := http.NewServeMux()
	webRoutes(mux)
	(&api{store: ta.store}).routes(mux)

	tests := []struct {
		path, contentType, contains string
	}{
		{"/", "text/html", `<script type="module" src="app.js">`},
		{"/app.js", "javascript", "function init()"},
		{"/style.css", "text/css", "--high"},
		{"/assets/MaterialIcons-Regular.ttf", "font/ttf", ""},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", tt.path, nil))
		if rec.Code != 200 {
			t.Errorf("%s: status %d", tt.path, rec.Code)
			continue
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, tt.contentType) {
			t.Errorf("%s: content type %q, want %q", tt.path, ct, tt.contentType)
		}
		if !strings.Contains(rec.Body.String(), tt.contains) {
			t.Errorf("%s: body doesn't contain %q", tt.path, tt.contains)
		}
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/assets/MaterialIcons-Regular.ttf", nil))
	if rec.Body.Len() != len(materialIconsTTF) {
		t.Errorf("font: %d bytes, want %d", rec.Body.Len(), len(materialIconsTTF))
	}
}
