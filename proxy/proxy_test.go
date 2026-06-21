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
	os.Setenv("VERTEX_REGION", "us-central1")
	defer func() {
		os.Unsetenv("VERTEX_API_KEY")
		os.Unsetenv("VERTEX_REGION")
	}()

	tests := []struct {
		name           string
		method         string
		path           string
		headers        map[string]string
		body           string
		disableKey     bool
		proxyKey       string
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
			name:     "Invalid Proxy API Key 401",
			method:   "POST",
			path:     "/v1beta/models/gemini-1.5-pro:generateContent",
			proxyKey: "required-proxy-key",
			headers: map[string]string{
				"x-goog-api-key": "wrong-key",
			},
			expectedStatus: http.StatusUnauthorized,
			expectedBody:   "Invalid Proxy API Key",
		},
		{
			name:     "Valid Proxy API Key 200",
			method:   "POST",
			path:     "/v1/models/gemini-1.5-pro:generateContent",
			proxyKey: "required-proxy-key",
			headers: map[string]string{
				"x-goog-api-key": "required-proxy-key",
			},
			body: `{"contents": [{"parts": [{"text": "hello"}]}]}`,
			mockBackendFn: func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("x-goog-api-key") != "test-vertex-key" {
					t.Errorf("expected backend to receive Vertex Key, got: %s", r.Header.Get("x-goog-api-key"))
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"candidates": [{"content": {"parts": [{"text": "proxy works"}]}}]}`))
			},
			expectedStatus: http.StatusOK,
			expectedBody:   "proxy works",
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
				expectedPath := "/v1/publishers/google/models/gemini-1.5-pro:generateContent"
				if r.URL.Path != expectedPath {
					t.Errorf("unexpected backend path: %s, expected: %s", r.URL.Path, expectedPath)
				}
				if r.Header.Get("x-goog-api-key") != "custom-client-key" {
					t.Errorf("unexpected API key header: %s", r.Header.Get("x-goog-api-key"))
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
				expectedPath := "/v1beta1/publishers/google/models/gemini-1.5-flash:streamGenerateContent"
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
		{
			name:   "Successful Unary Proxy request without version prefix",
			method: "POST",
			path:   "/models/gemini-1.5-pro:generateContent",
			body:   `{"contents": [{"parts": [{"text": "hello"}]}]}`,
			mockBackendFn: func(w http.ResponseWriter, r *http.Request) {
				expectedPath := "/v1beta1/publishers/google/models/gemini-1.5-pro:generateContent"
				if r.URL.Path != expectedPath {
					t.Errorf("unexpected backend path: %s, expected: %s", r.URL.Path, expectedPath)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"candidates": [{"content": {"parts": [{"text": "no-version response"}]}}]}`))
			},
			expectedStatus: http.StatusOK,
			expectedBody:   "no-version response",
		},
		{
			name:   "Successful Unary Proxy request with duplicate version prefix",
			method: "POST",
			path:   "/v1beta/v1beta/models/gemini-1.5-pro:generateContent",
			body:   `{"contents": [{"parts": [{"text": "hello"}]}]}`,
			mockBackendFn: func(w http.ResponseWriter, r *http.Request) {
				expectedPath := "/v1beta1/publishers/google/models/gemini-1.5-pro:generateContent"
				if r.URL.Path != expectedPath {
					t.Errorf("unexpected backend path: %s, expected: %s", r.URL.Path, expectedPath)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"candidates": [{"content": {"parts": [{"text": "cleaned-version response"}]}}]}`))
			},
			expectedStatus: http.StatusOK,
			expectedBody:   "cleaned-version response",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.proxyKey != "" {
				os.Setenv("PROXY_API_KEY", tc.proxyKey)
				defer os.Unsetenv("PROXY_API_KEY")
			} else {
				os.Unsetenv("PROXY_API_KEY")
			}

			if tc.disableKey {
				os.Unsetenv("VERTEX_API_KEY")
				defer os.Setenv("VERTEX_API_KEY", "test-vertex-key")
			} else {
				os.Setenv("VERTEX_API_KEY", "test-vertex-key")
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
