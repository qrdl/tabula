package main

import (
	"crypto/subtle"
	"net/http"
)

// AuthConfig turns on HTTP basic auth for every page and API call. Both empty = no auth.
type AuthConfig struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

// basicAuth asks for the configured user and password on every request.
// With no user set it returns next unchanged.
func basicAuth(cfg AuthConfig, next http.Handler) http.Handler {
	if cfg.User == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		// Both are always compared, in constant time, so the reply time doesn't hint which was wrong.
		userOK := subtle.ConstantTimeCompare([]byte(user), []byte(cfg.User)) == 1
		passwordOK := subtle.ConstantTimeCompare([]byte(password), []byte(cfg.Password)) == 1
		if !ok || !userOK || !passwordOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="Tabula", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
