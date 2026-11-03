package security

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// AuthMiddleware enforces Bearer token authentication if expectedToken is non-empty
func AuthMiddleware(expectedToken string, next http.Handler) http.Handler {
	if expectedToken == "" {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Check Authorization: Bearer <token>
		authHeader := r.Header.Get("Authorization")
		var token string
		if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
			token = strings.TrimSpace(authHeader[7:])
		}

		// 2. Fallback to query param ?token= (essential for browser EventSource /tail)
		if token == "" {
			token = r.URL.Query().Get("token")
		}

		// Constant-time comparison to prevent timing attacks
		if subtle.ConstantTimeCompare([]byte(token), []byte(expectedToken)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="Genesis"`)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized: invalid or missing bearer token"}`))
			return
		}

		next.ServeHTTP(w, r)
	})
}
