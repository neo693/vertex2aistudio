package proxy

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

var (
	// Matches Vertex AI paths, extracting Version, Model, and Action.
	// Example matches:
	// /v1/projects/my-project/locations/us-central1/publishers/google/models/gemini-1.5-pro:generateContent
	// /v1beta1/projects/my-proj/locations/us-east1/models/gemini-1.5-flash:streamGenerateContent
	routeRegex = regexp.MustCompile(`^/(v1|v1beta|v1beta1)/projects/[^/]+/locations/[^/]+/(?:publishers/google/)?models/([^/:]+):(generateContent|streamGenerateContent|countTokens|embedContent)$`)
)

type Proxy struct {
	mapper      *ModelMapper
	client      *http.Client
	apiVersion  string
	enableCORS  bool
}

type ErrorResponse struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

func NewProxy() *Proxy {
	apiVersion := os.Getenv("TARGET_API_VERSION")
	if apiVersion == "" {
		apiVersion = "v1beta" // Default to v1beta for widest feature coverage
	}

	enableCORS := true
	if os.Getenv("DISABLE_CORS") == "true" {
		enableCORS = false
	}

	return &Proxy{
		mapper: NewModelMapper(),
		client: &http.Client{
			Timeout: 10 * time.Minute, // Long timeout for streaming
		},
		apiVersion: apiVersion,
		enableCORS: enableCORS,
	}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	// 1. Handle CORS preflight
	if p.enableCORS {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PUT, DELETE, PATCH")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
	}

	// 2. Validate route
	matches := routeRegex.FindStringSubmatch(r.URL.Path)
	if len(matches) != 4 {
		p.writeError(w, http.StatusNotFound, "NOT_FOUND", "Resource not found or path format invalid")
		log.Printf("404 Not Found - Path: %s", r.URL.Path)
		return
	}

	clientVersion := matches[1]
	model := matches[2]
	action := matches[3]

	// 3. Resolve API Key
	apiKey := ResolveAPIKey(r)
	if apiKey == "" {
		p.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Google AI Studio API key not found. Set GEMINI_API_KEY environment variable or pass x-goog-api-key / Authorization Bearer header.")
		log.Printf("401 Unauthorized - Path: %s", r.URL.Path)
		return
	}

	// 4. Map model name
	targetModel := p.mapper.MapModel(model)

	// 5. Determine target API version
	targetVersion := p.apiVersion
	// If client explicitly specified v1, and we didn't override default, we can preserve v1 or use v1beta
	if clientVersion == "v1" && os.Getenv("TARGET_API_VERSION") == "" {
		targetVersion = "v1"
	}

	// 6. Construct target URL
	// Google AI Studio uses https://generativelanguage.googleapis.com/{version}/models/{model}:{action}
	endpoint := os.Getenv("AI_STUDIO_ENDPOINT")
	if endpoint == "" {
		endpoint = "https://generativelanguage.googleapis.com"
	}
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil {
		p.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to parse API endpoint: "+err.Error())
		log.Printf("500 Internal Error - Path: %s, Error: %v", r.URL.Path, err)
		return
	}

	targetURL := &url.URL{
		Scheme: parsedEndpoint.Scheme,
		Host:   parsedEndpoint.Host,
		Path:   parsedEndpoint.Path + "/" + targetVersion + "/models/" + targetModel + ":" + action,
	}

	// Copy and modify query parameters
	q := r.URL.Query()
	if action == "streamGenerateContent" {
		q.Set("alt", "sse") // AI Studio requires alt=sse for streaming
	}
	targetURL.RawQuery = q.Encode()

	// 7. Create backend request
	ctx := r.Context()
	// Create request with body
	backendReq, err := http.NewRequestWithContext(ctx, r.Method, targetURL.String(), r.Body)
	if err != nil {
		p.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create target request: "+err.Error())
		log.Printf("500 Internal Error - Path: %s, Error: %v", r.URL.Path, err)
		return
	}

	// Copy headers
	for k, vv := range r.Header {
		// Ignore auth-related, hosting-related and GCP specific headers
		kLower := strings.ToLower(k)
		if kLower == "authorization" || kLower == "host" || kLower == "x-goog-api-key" || strings.HasPrefix(kLower, "x-goog-user-project") {
			continue
		}
		for _, v := range vv {
			backendReq.Header.Add(k, v)
		}
	}

	// Set API Key header for Google AI Studio
	backendReq.Header.Set("x-goog-api-key", apiKey)

	log.Printf("Proxying [%s] %s -> %s (model: %s -> %s)", r.Method, r.URL.Path, targetURL.String(), model, targetModel)

	// 8. Execute request
	resp, err := p.client.Do(backendReq)
	if err != nil {
		// Handle context cancellation vs backend errors
		if ctx.Err() == context.Canceled {
			log.Printf("Client disconnected - Path: %s", r.URL.Path)
			return
		}
		p.writeError(w, http.StatusBadGateway, "BAD_GATEWAY", "Failed to contact Google AI Studio: "+err.Error())
		log.Printf("502 Bad Gateway - Path: %s, Error: %v", r.URL.Path, err)
		return
	}
	defer resp.Body.Close()

	// 9. Forward response
	// Copy headers from AI Studio
	for k, vv := range resp.Header {
		// Exclude CORS headers from backend (if we override them)
		if p.enableCORS {
			kLower := strings.ToLower(k)
			if kLower == "access-control-allow-origin" || kLower == "access-control-allow-headers" || kLower == "access-control-allow-methods" {
				continue
			}
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}

	// If streaming action, enforce SSE headers
	if action == "streamGenerateContent" {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Transfer-Encoding", "chunked")
	}

	// Write status code
	w.WriteHeader(resp.StatusCode)

	// If streaming, copy stream chunk by chunk
	if action == "streamGenerateContent" {
		_, err = CopyStream(w, resp.Body)
	} else {
		_, err = io.Copy(w, resp.Body)
	}

	duration := time.Since(startTime)

	if err != nil {
		if ctx.Err() != context.Canceled {
			log.Printf("Error forwarding response - Path: %s, Error: %v", r.URL.Path, err)
		}
	} else {
		log.Printf("Completed [%d] in %v - Path: %s", resp.StatusCode, duration, r.URL.Path)
	}
}

func (p *Proxy) writeError(w http.ResponseWriter, statusCode int, statusStr string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	var errResp ErrorResponse
	errResp.Error.Code = statusCode
	errResp.Error.Message = message
	errResp.Error.Status = statusStr

	_ = json.NewEncoder(w).Encode(errResp)
}
