package serve

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
)

// AuthMiddleware returns a handler that checks the Authorization: Bearer <secret> header.
// If secret is empty, all requests are allowed (no auth).
func AuthMiddleware(secret string, next http.Handler) http.Handler {
	if secret == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" {
			http.Error(w, "missing Authorization header", http.StatusUnauthorized)
			return
		}
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "expected Bearer token in Authorization header", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")
		if token != secret {
			http.Error(w, "invalid API key", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// GenerateAPIKey creates a random API key for use when none is configured.
func GenerateAPIKey() string {
	b := make([]byte, 16)
	rand.Read(b)
	return "dcl-" + hex.EncodeToString(b)
}
