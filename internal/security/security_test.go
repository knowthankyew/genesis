package security

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthMiddleware(t *testing.T) {
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	tests := []struct {
		name          string
		expectedToken string
		authHeader    string
		queryToken    string
		expectedCode  int
	}{
		{"Auth disabled (empty expected)", "", "", "", http.StatusOK},
		{"Valid Bearer Header", "secret-123", "Bearer secret-123", "", http.StatusOK},
		{"Valid Query Param", "secret-123", "", "secret-123", http.StatusOK},
		{"Missing Token", "secret-123", "", "", http.StatusUnauthorized},
		{"Invalid Token in Header", "secret-123", "Bearer wrong-token", "", http.StatusUnauthorized},
		{"Invalid Token in Query", "secret-123", "", "wrong-token", http.StatusUnauthorized},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			middleware := AuthMiddleware(tc.expectedToken, dummyHandler)
			url := "/"
			if tc.queryToken != "" {
				url += "?token=" + tc.queryToken
			}
			req := httptest.NewRequest("GET", url, nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			w := httptest.NewRecorder()
			middleware.ServeHTTP(w, req)

			if w.Code != tc.expectedCode {
				t.Fatalf("expected status %d, got %d", tc.expectedCode, w.Code)
			}
		})
	}
}
