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
	// Matches AI Studio paths, extracting Version (optional), Model, and Action.
	// Example matches:
	// /v1beta/models/gemini-1.5-pro:generateContent
	// /models/gemini-1.5-pro:generateContent
	routeRegex = regexp.MustCompile(`^(?:/(v1|v1beta))?/models/([^/:]+):(generateContent|streamGenerateContent|countTokens|embedContent)$`)
)

type Proxy struct {
	mapper      *ModelMapper
	client      *http.Client
	defaultRegion string
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
	defaultRegion := os.Getenv("VERTEX_REGION")
	if defaultRegion == "" {
		defaultRegion = "us-central1"
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
		defaultRegion: defaultRegion,
		enableCORS:  enableCORS,
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

	path := r.URL.Path
	// Strip duplicate or misconfigured version prefixes (e.g. /v1beta/v1beta/models/...)
	if strings.HasPrefix(path, "/v1beta/v1beta/") {
		path = "/v1beta/" + path[15:]
	} else if strings.HasPrefix(path, "/v1/v1/") {
		path = "/v1/" + path[7:]
	} else if strings.HasPrefix(path, "/v1beta/v1/") {
		path = "/v1beta/" + path[11:]
	} else if strings.HasPrefix(path, "/v1/v1beta/") {
		path = "/v1beta/" + path[11:]
	}

	// 2. Validate route
	matches := routeRegex.FindStringSubmatch(path)
	if len(matches) != 4 {
		p.writeError(w, http.StatusNotFound, "NOT_FOUND", "Resource not found or path format invalid")
		log.Printf("404 Not Found - Path: %s (cleaned: %s)", r.URL.Path, path)
		return
	}

	clientVersion := matches[1]
	model := matches[2]
	action := matches[3]

	// 3. Resolve API Key from request
	clientKey := ResolveAPIKey(r)

	// If PROXY_API_KEY is configured, we must validate clientKey against it
	proxyAPIKey := os.Getenv("PROXY_API_KEY")
	var targetAPIKey string

	if proxyAPIKey != "" {
		if clientKey != proxyAPIKey {
			p.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid Proxy API Key.")
			log.Printf("401 Unauthorized - Invalid Proxy API Key - Path: %s", r.URL.Path)
			return
		}
		// If custom key matches, use VERTEX_API_KEY for the backend call
		targetAPIKey = os.Getenv("VERTEX_API_KEY")
		if targetAPIKey == "" {
			// Fallback: if no backend Vertex key is configured, use the proxy key itself
			targetAPIKey = proxyAPIKey
		}
	} else {
		// If no PROXY_API_KEY is configured, clientKey is used directly as Vertex Key
		targetAPIKey = clientKey
		if targetAPIKey == "" {
			// Fallback to VERTEX_API_KEY env var
			targetAPIKey = os.Getenv("VERTEX_API_KEY")
		}
	}

	if targetAPIKey == "" {
		p.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Vertex AI API Key not found. Please set VERTEX_API_KEY environment variable.")
		log.Printf("401 Unauthorized - No target Vertex API Key - Path: %s", r.URL.Path)
		return
	}

	// 4. Resolve Vertex AI configurations
	projectID := os.Getenv("VERTEX_PROJECT_ID")
	if projectID == "" {
		p.writeError(w, http.StatusInternalServerError, "MISCONFIGURED", "VERTEX_PROJECT_ID environment variable is not set on the proxy server.")
		log.Printf("500 Internal Error - Path: %s, Error: VERTEX_PROJECT_ID is empty", r.URL.Path)
		return
	}

	region := p.defaultRegion

	// 5. Map model name
	targetModel := p.mapper.MapModel(model)

	// 6. Map API version (AI Studio v1beta -> Vertex v1beta1, AI Studio v1 -> Vertex v1)
	targetVersion := "v1beta1"
	if clientVersion == "v1" {
		targetVersion = "v1"
	}

	// 7. Construct target URL
	// Vertex AI uses https://{region}-aiplatform.googleapis.com/{version}/projects/{project}/locations/{region}/publishers/google/models/{model}:{action}
	endpoint := os.Getenv("VERTEX_API_ENDPOINT")
	if endpoint == "" {
		endpoint = "https://" + region + "-aiplatform.googleapis.com"
	}
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil {
		p.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to parse Vertex API endpoint: "+err.Error())
		log.Printf("500 Internal Error - Path: %s, Error: %v", r.URL.Path, err)
		return
	}

	targetURL := &url.URL{
		Scheme: parsedEndpoint.Scheme,
		Host:   parsedEndpoint.Host,
		Path:   parsedEndpoint.Path + "/" + targetVersion + "/projects/" + projectID + "/locations/" + region + "/publishers/google/models/" + targetModel + ":" + action,
	}

	// Copy and modify query parameters
	q := r.URL.Query()
	q.Set("key", targetAPIKey) // Pass Vertex API key in query params
	targetURL.RawQuery = q.Encode()

	// 8. Create backend request
	ctx := r.Context()
	backendReq, err := http.NewRequestWithContext(ctx, r.Method, targetURL.String(), r.Body)
	if err != nil {
		p.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create target request: "+err.Error())
		log.Printf("500 Internal Error - Path: %s, Error: %v", r.URL.Path, err)
		return
	}

	// Copy headers
	for k, vv := range r.Header {
		// Ignore auth-related, hosting-related and other keys from client
		kLower := strings.ToLower(k)
		if kLower == "authorization" || kLower == "host" || kLower == "x-goog-api-key" {
			continue
		}
		for _, v := range vv {
			backendReq.Header.Add(k, v)
		}
	}

	// Set API Key header for Vertex AI
	backendReq.Header.Set("x-goog-api-key", targetAPIKey)

	log.Printf("Proxying [%s] %s -> %s (model: %s -> %s)", r.Method, r.URL.Path, targetURL.String(), model, targetModel)

	// 9. Execute request
	resp, err := p.client.Do(backendReq)
	if err != nil {
		if ctx.Err() == context.Canceled {
			log.Printf("Client disconnected - Path: %s", r.URL.Path)
			return
		}
		p.writeError(w, http.StatusBadGateway, "BAD_GATEWAY", "Failed to contact Vertex AI: "+err.Error())
		log.Printf("502 Bad Gateway - Path: %s, Error: %v", r.URL.Path, err)
		return
	}
	defer resp.Body.Close()

	// 10. Forward response
	// Copy headers from Vertex AI
	for k, vv := range resp.Header {
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
