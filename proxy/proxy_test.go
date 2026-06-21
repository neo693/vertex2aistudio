package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestProxy_ServeHTTP(t *testing.T) {
	// Setup standard configurations
	os.Setenv("VERTEX_API_KEY", "test-vertex-key")
	os.Setenv("VERTEX_PROJECT_ID", "test-project-123")
	os.Setenv("VERTEX_REGION", "us-central1")
	defer func() {
		os.Unsetenv("VERTEX_API_KEY")
		os.Unsetenv("VERTEX_PROJECT_ID")
		os.Unsetenv("VERTEX_REGION")
	}()

	tests := []struct {
		name           string
		method         string
		path           string
		headers        map[string]string
		body           string
		disableKey     bool
		disableProject bool
		mockBackendFn  func(w http.ResponseWriter, r *http.Request)
		expectedStatus int
		expectedBody   string
		checkHeaders   map[string]string
	}{
		{
			name:   "CORS Preflight OPTIONS",
			method: "OPTIONS",
			path:   "/v1beta/models/gemini-1.5-pro:generateContent",
			expectedStatus: http.StatusOK,
			checkHeaders: map[string]string{
				"Access-Control-Allow-Origin": "*",
				"Access-Control-Allow-Headers": "*",
			},
		},
		{
			name:   "Invalid Path 404",
			method: "POST",
			path:   "/v1/invalid/path",
			expectedStatus: http.StatusNotFound,
			expectedBody:   "Resource not found",
		},
		{
			name:       "Missing API Key 401",
			method:     "POST",
			path:       "/v1beta/models/gemini-1.5-pro:generateContent",
			disableKey: true,
			expectedStatus: http.StatusUnauthorized,
			expectedBody:   "Vertex AI API Key not found",
		},
		{
			name:           "Missing Project ID 500",
			method:         "POST",
			path:           "/v1beta/models/gemini-1.5-pro:generateContent",
			disableProject: true,
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "VERTEX_PROJECT_ID environment variable is not set",
		},
		{
			name:   "Successful Unary Proxy request",
			method: "POST",
			path:   "/v1/models/gemini-1.5-pro:generateContent",
			headers: map[string]string{
				"x-goog-api-key": "custom-client-key",
			},
			body: `{"contents": [{"parts": [{"text": "hello"}]}]}`,
			mockBackendFn: func(w http.ResponseWriter, r *http.Request) {
				expectedPath := "/v1/projects/test-project-123/locations/us-central1/publishers/google/models/gemini-1.5-pro:generateContent"
				if r.URL.Path != expectedPath {
					t.Errorf("unexpected backend path: %s, expected: %s", r.URL.Path, expectedPath)
				}
				if r.Header.Get("x-goog-api-key") != "custom-client-key" {
					t.Errorf("unexpected API key header: %s", r.Header.Get("x-goog-api-key"))
				}
				if r.URL.Query().Get("key") != "custom-client-key" {
					t.Errorf("unexpected API key query param: %s", r.URL.Query().Get("key"))
				}
				bodyBytes, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(bodyBytes), "hello") {
					t.Errorf("unexpected body content: %s", string(bodyBytes))
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"candidates": [{"content": {"parts": [{"text": "hi there"}]}}]}`))
			},
			expectedStatus: http.StatusOK,
			expectedBody:   "hi there",
		},
		{
			name:   "Successful Streaming Proxy request",
			method: "POST",
			path:   "/v1beta/models/gemini-1.5-flash:streamGenerateContent",
			body: `{"contents": [{"parts": [{"text": "stream"}]}]}`,
			mockBackendFn: func(w http.ResponseWriter, r *http.Request) {
				// client v1beta maps to target v1beta1 version path on Vertex
				expectedPath := "/v1beta1/projects/test-project-123/locations/us-central1/publishers/google/models/gemini-1.5-flash:streamGenerateContent"
				if r.URL.Path != expectedPath {
					t.Errorf("unexpected backend path: %s, expected: %s", r.URL.Path, expectedPath)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("data: {\"candidates\": [{\"content\": {\"parts\": [{\"text\": \"part1\"}]}}]}\n\n"))
			},
			expectedStatus: http.StatusOK,
			checkHeaders: map[string]string{
				"Content-Type": "text/event-stream",
			},
			expectedBody: "part1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.disableKey {
				os.Unsetenv("VERTEX_API_KEY")
				defer os.Setenv("VERTEX_API_KEY", "test-vertex-key")
			} else {
				os.Setenv("VERTEX_API_KEY", "test-vertex-key")
			}

			if tc.disableProject {
				os.Unsetenv("VERTEX_PROJECT_ID")
				defer os.Setenv("VERTEX_PROJECT_ID", "test-project-123")
			} else {
				os.Setenv("VERTEX_PROJECT_ID", "test-project-123")
			}

			// Define mock backend handler dynamically
			var backendHandler http.Handler
			if tc.mockBackendFn != nil {
				backendHandler = http.HandlerFunc(tc.mockBackendFn)
			} else {
				backendHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
			}
			backendServer := httptest.NewServer(backendHandler)
			defer backendServer.Close()

			os.Setenv("VERTEX_API_ENDPOINT", backendServer.URL)
			defer os.Unsetenv("VERTEX_API_ENDPOINT")

			p := NewProxy()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			rr := httptest.NewRecorder()
			p.ServeHTTP(rr, req)

			resp := rr.Result()
			defer resp.Body.Close()

			if resp.StatusCode != tc.expectedStatus {
				t.Errorf("expected status %d, got %d", tc.expectedStatus, resp.StatusCode)
			}

			bodyBytes, _ := io.ReadAll(resp.Body)
			bodyStr := string(bodyBytes)

			if tc.expectedBody != "" && !strings.Contains(bodyStr, tc.expectedBody) {
				t.Errorf("expected response to contain %q, got %q", tc.expectedBody, bodyStr)
			}

			for k, expectedVal := range tc.checkHeaders {
				actualVal := resp.Header.Get(k)
				if actualVal != expectedVal {
					t.Errorf("expected header %s to be %q, got %q", k, expectedVal, actualVal)
				}
			}
		})
	}
}
