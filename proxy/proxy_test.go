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
	// Set default environment variable API key
	os.Setenv("GEMINI_API_KEY", "test-api-key")
	defer os.Unsetenv("GEMINI_API_KEY")

	tests := []struct {
		name           string
		method         string
		path           string
		headers        map[string]string
		body           string
		disableEnv     bool
		mockBackendFn  func(w http.ResponseWriter, r *http.Request)
		expectedStatus int
		expectedBody   string
		checkHeaders   map[string]string
	}{
		{
			name:   "CORS Preflight OPTIONS",
			method: "OPTIONS",
			path:   "/v1/projects/p/locations/l/models/m:generateContent",
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
			path:       "/v1/projects/p/locations/l/models/m:generateContent",
			disableEnv: true,
			expectedStatus: http.StatusUnauthorized,
			expectedBody:   "Google AI Studio API key not found",
		},
		{
			name:   "Successful Unary Proxy request",
			method: "POST",
			path:   "/v1/projects/proj-1/locations/us-central1/publishers/google/models/gemini-1.5-pro-001:generateContent",
			headers: map[string]string{
				"x-goog-api-key": "my-client-key",
			},
			body: `{"contents": [{"parts": [{"text": "hello"}]}]}`,
			mockBackendFn: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/models/gemini-1.5-pro:generateContent" {
					t.Errorf("unexpected backend path: %s", r.URL.Path)
				}
				if r.Header.Get("x-goog-api-key") != "my-client-key" {
					t.Errorf("unexpected API key: %s", r.Header.Get("x-goog-api-key"))
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
			path:   "/v1beta1/projects/proj-1/locations/us-east1/models/gemini-1.5-flash-002:streamGenerateContent",
			body: `{"contents": [{"parts": [{"text": "stream"}]}]}`,
			mockBackendFn: func(w http.ResponseWriter, r *http.Request) {
				// v1beta1 should map to v1beta target path
				if r.URL.Path != "/v1beta/models/gemini-1.5-flash:streamGenerateContent" {
					t.Errorf("unexpected backend path: %s", r.URL.Path)
				}
				if r.URL.Query().Get("alt") != "sse" {
					t.Errorf("expected alt=sse query param, got: %s", r.URL.Query().Get("alt"))
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("data: {\"candidates\": [{\"content\": {\"parts\": [{\"text\": \"part1\"}]}}]}\n\n"))
				w.Write([]byte("data: {\"candidates\": [{\"content\": {\"parts\": [{\"text\": \"part2\"}]}}]}\n\n"))
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
			if tc.disableEnv {
				os.Unsetenv("GEMINI_API_KEY")
				defer os.Setenv("GEMINI_API_KEY", "test-api-key")
			} else {
				os.Setenv("GEMINI_API_KEY", "test-api-key")
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

			os.Setenv("AI_STUDIO_ENDPOINT", backendServer.URL)
			defer os.Unsetenv("AI_STUDIO_ENDPOINT")

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
