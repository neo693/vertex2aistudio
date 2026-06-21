package proxy

import (
	"net/http/httptest"
	"os"
	"testing"
)

func TestResolveAPIKey(t *testing.T) {
	// Setup env var and clean up after
	os.Setenv("GEMINI_API_KEY", "env-api-key")
	defer os.Unsetenv("GEMINI_API_KEY")

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
			name: "Skip GCP Token and fallback to Env Var",
			headers: map[string]string{
				"Authorization": "Bearer ya29.a0AfB_abcdef",
			},
			expected: "env-api-key",
		},
		{
			name:       "Skip GCP Token and no Env Var returns empty",
			headers:    map[string]string{
				"Authorization": "Bearer ya29.a0AfB_abcdef",
			},
			disableEnv: true,
			expected:   "",
		},
		{
			name:     "Fallback to Env Var",
			headers:  map[string]string{},
			expected: "env-api-key",
		},
		{
			name:       "No headers and no Env Var returns empty",
			headers:    map[string]string{},
			disableEnv: true,
			expected:   "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.disableEnv {
				os.Unsetenv("GEMINI_API_KEY")
				defer os.Setenv("GEMINI_API_KEY", "env-api-key")
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
