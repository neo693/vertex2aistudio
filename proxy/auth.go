package proxy

import (
	"net/http"
	"os"
	"strings"
)

// ResolveAPIKey extracts the Google AI Studio API key from the request.
// It checks in the following order:
// 1. Header "x-goog-api-key"
// 2. Header "Authorization" with "Bearer <token>" format (unless it starts with "ya29.")
// 3. Environment variable "GEMINI_API_KEY"
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
			// GCP OAuth2 tokens start with "ya29.". If it starts with "ya29.", we shouldn't use it
			// as a Gemini API Key because it won't be accepted by Google AI Studio.
			if !strings.HasPrefix(token, "ya29.") && token != "" {
				return token
			}
		}
	}

	// 3. Fallback to server-wide environment variable
	return os.Getenv("GEMINI_API_KEY")
}
