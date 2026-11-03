package ui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestUI_ZeroExternalEgress enforces Pillar 2 of Genesis architecture:
// The UI must be completely hermetic with zero external CDN, font, or script references.
func TestUI_ZeroExternalEgress(t *testing.T) {
	if len(IndexHTML) == 0 {
		t.Fatal("IndexHTML asset is empty")
	}

	// Search for any unescaped external scheme references
	if bytes.Contains(IndexHTML, []byte("http://")) {
		t.Errorf("violation of hermetic UI invariant: found 'http://' in embedded assets")
	}
	if bytes.Contains(IndexHTML, []byte("https://")) {
		t.Errorf("violation of hermetic UI invariant: found 'https://' in embedded assets")
	}
}

func TestUI_Handler(t *testing.T) {
	handler := Handler()

	// Test GET /
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}
	if contentType := w.Header().Get("Content-Type"); contentType != "text/html; charset=utf-8" {
		t.Errorf("expected text/html; charset=utf-8, got %q", contentType)
	}
	if w.Body.Len() != len(IndexHTML) {
		t.Errorf("expected body length %d, got %d", len(IndexHTML), w.Body.Len())
	}

	// Test GET /unknown (404)
	req404 := httptest.NewRequest("GET", "/unknown", nil)
	w404 := httptest.NewRecorder()
	handler.ServeHTTP(w404, req404)

	if w404.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown path, got %d", w404.Code)
	}
}
