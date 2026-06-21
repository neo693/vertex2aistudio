package proxy

import (
	"net/http/httptest"
	"os"
	"testing"
)

func TestResolveAPIKey(t *testing.T) {
	// Setup env vars and clean up after
	os.Setenv("VERTEX_API_KEY", "vertex-env-key")
	os.Setenv("GEMINI_API_KEY", "gemini-env-key")
	defer func() {
		os.Unsetenv("VERTEX_API_KEY")
		os.Unsetenv("GEMINI_API_KEY")
	}()

	tests := []struct {
		name       string
		headers    map[string]string
		disableEnv bool
		expected   string
	}{
		{
			name: "Resolve from x-goog-api-key",
			headers: map[string]string{
				"x-goog-api-key": "header-key-1",
			},
			expected: "header-key-1",
		},
		{
			name: "Resolve from Authorization Bearer",
			headers: map[string]string{
				"Authorization": "Bearer my-custom-gemini-key",
			},
			expected: "my-custom-gemini-key",
		},
		{
			name: "Allow GCP Token (ya29.)",
			headers: map[string]string{
				"Authorization": "Bearer ya29.a0AfB_abcdef",
			},
			expected: "ya29.a0AfB_abcdef",
		},
		{
			name:     "Fallback to VERTEX_API_KEY",
			headers:  map[string]string{},
			expected: "vertex-env-key",
		},
		{
			name: "Fallback to GEMINI_API_KEY if VERTEX_API_KEY is empty",
			headers: map[string]string{},
			disableEnv: true, // Will delete VERTEX_API_KEY
			expected: "gemini-env-key",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.disableEnv {
				os.Unsetenv("VERTEX_API_KEY")
				defer os.Setenv("VERTEX_API_KEY", "vertex-env-key")
			}

			req := httptest.NewRequest("POST", "/", nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			actual := ResolveAPIKey(req)
			if actual != tc.expected {
				t.Errorf("expected ResolveAPIKey = %q, got %q", tc.expected, actual)
			}
		})
	}
}
