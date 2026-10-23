// Package transport holds shared HTTP authentication helpers for API adapters.
package transport

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// Bearer wraps a handler with constant-time bearer token authentication.
func Bearer(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
