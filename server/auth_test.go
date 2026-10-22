package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestBasicAuth(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	tests := []struct {
		name           string
		cfg            AuthConfig
		user, password string // "" user = no Authorization header
		want           int
	}{
		{"auth off, no header", AuthConfig{}, "", "", 200},
		{"auth off, any header", AuthConfig{}, "x", "y", 200},
		{"no header", AuthConfig{"ilya", "secret"}, "", "", 401},
		{"wrong password", AuthConfig{"ilya", "secret"}, "ilya", "secreT", 401},
		{"wrong user", AuthConfig{"ilya", "secret"}, "ily", "secret", 401},
		{"right", AuthConfig{"ilya", "secret"}, "ilya", "secret", 200},
	}
	for _, tt := range tests {
		r := httptest.NewRequest("GET", "/api/image.bin", nil)
		if tt.user != "" {
			r.SetBasicAuth(tt.user, tt.password)
		}
		rec := httptest.NewRecorder()
		basicAuth(tt.cfg, ok).ServeHTTP(rec, r)
		if rec.Code != tt.want {
			t.Errorf("%s: status %d, want %d", tt.name, rec.Code, tt.want)
		}
		if challenge := rec.Header().Get("WWW-Authenticate"); (rec.Code == 401) != (challenge != "") {
			t.Errorf("%s: status %d with WWW-Authenticate %q", tt.name, rec.Code, challenge)
		}
	}
}

func TestLoadConfigAuth(t *testing.T) {
	tests := []struct {
		json    string
		wantErr bool
	}{
		{`{}`, false},
		{`{"auth": {"user": "", "password": ""}}`, false},
		{`{"auth": {"user": "ilya", "password": "secret"}}`, false},
		{`{"auth": {"user": "ilya"}}`, true},
		{`{"auth": {"password": "secret"}}`, true},
	}
	for _, tt := range tests {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(tt.json), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadConfig(path); (err != nil) != tt.wantErr {
			t.Errorf("%s: error %v, want error %v", tt.json, err, tt.wantErr)
		}
	}
}
