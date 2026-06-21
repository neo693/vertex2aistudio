package proxy

import (
	"net/http"
	"os"
	"strings"
)

// ResolveAPIKey extracts the API key / credential from the request.
// It checks in the following order:
// 1. Header "x-goog-api-key"
// 2. Header "Authorization" with "Bearer <token>" format
// 3. Environment variable "VERTEX_API_KEY"
// 4. Environment variable "GEMINI_API_KEY" (legacy/alternative fallback)
// If no API key is found, it returns an empty string.
func ResolveAPIKey(r *http.Request) string {
	// 1. Check x-goog-api-key header
	if key := r.Header.Get("x-goog-api-key"); key != "" {
		return strings.TrimSpace(key)
	}

	// 2. Check Authorization header
	if auth := r.Header.Get("Authorization"); auth != "" {
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			token := strings.TrimSpace(auth[7:])
			if token != "" {
				return token
			}
		}
	}

	// 3. Fallback to VERTEX_API_KEY environment variable
	if key := os.Getenv("VERTEX_API_KEY"); key != "" {
		return strings.TrimSpace(key)
	}

	// 4. Fallback to GEMINI_API_KEY environment variable
	return strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
}
