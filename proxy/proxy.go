package proxy

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
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

	cleanedPath := path

	// Intercept OpenAI compatibility endpoints
	if strings.HasSuffix(cleanedPath, "/chat/completions") {
		p.handleOpenAIChatCompletions(w, r)
		return
	}
	if cleanedPath == "/v1/models" || cleanedPath == "/models" || cleanedPath == "/v1beta/models" {
		p.handleOpenAIModels(w, r)
		return
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

	// 4. Resolve Vertex AI configurations (Optional: custom endpoint override)

	// 5. Map model name
	targetModel := p.mapper.MapModel(model)

	// 6. Map API version (AI Studio v1beta -> Vertex v1beta1, AI Studio v1 -> Vertex v1)
	targetVersion := "v1beta1"
	if clientVersion == "v1" {
		targetVersion = "v1"
	}

	// 7. Construct target URL
	// Vertex AI API Key mode uses global endpoint:
	// https://aiplatform.googleapis.com/{version}/publishers/google/models/{model}:{action}?key={API_KEY}
	endpoint := os.Getenv("VERTEX_API_ENDPOINT")
	if endpoint == "" {
		endpoint = "https://aiplatform.googleapis.com"
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
		Path:   parsedEndpoint.Path + "/" + targetVersion + "/publishers/google/models/" + targetModel + ":" + action,
	}

	// Copy and modify query parameters
	q := r.URL.Query()
	q.Set("key", targetAPIKey) // Pass Vertex API key in query params
	if action == "streamGenerateContent" {
		q.Set("alt", "sse") // Enforce SSE if streaming
	}
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

// --- OpenAI Compatibility Structs & Translators ---

type OpenAIRequest struct {
	Model            string          `json:"model"`
	Messages         []OpenAIMessage `json:"messages"`
	Temperature      *float32        `json:"temperature,omitempty"`
	MaxTokens        *int            `json:"max_tokens,omitempty"`
	Stream           bool            `json:"stream,omitempty"`
	TopP             *float32        `json:"top_p,omitempty"`
	PresencePenalty  *float32        `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float32        `json:"frequency_penalty,omitempty"`
	Stop             interface{}     `json:"stop,omitempty"` // string or []string
}

type OpenAIMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // Can be string or []interface{} (array of content parts)
}

func (m *OpenAIMessage) ContentString() string {
	if m.Content == nil {
		return ""
	}
	switch val := m.Content.(type) {
	case string:
		return val
	case []interface{}:
		var parts []string
		for _, item := range val {
			if m, ok := item.(map[string]interface{}); ok {
				if t, ok := m["type"].(string); ok && t == "text" {
					if text, ok := m["text"].(string); ok {
						parts = append(parts, text)
					}
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

type GeminiRequest struct {
	Contents          []GeminiContent `json:"contents"`
	SystemInstruction *GeminiContent  `json:"systemInstruction,omitempty"`
	GenerationConfig  *GeminiConfig   `json:"generationConfig,omitempty"`
}

type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts"`
}

type GeminiPart struct {
	Text string `json:"text"`
}

type GeminiConfig struct {
	Temperature      *float32 `json:"temperature,omitempty"`
	MaxOutputTokens  *int     `json:"maxOutputTokens,omitempty"`
	TopP             *float32 `json:"topP,omitempty"`
	PresencePenalty  *float32 `json:"presencePenalty,omitempty"`
	FrequencyPenalty *float32 `json:"frequencyPenalty,omitempty"`
	StopSequences    []string `json:"stopSequences,omitempty"`
}

type GeminiResponse struct {
	Candidates    []GeminiCandidate    `json:"candidates"`
	UsageMetadata *GeminiUsageMetadata `json:"usageMetadata,omitempty"`
}

type GeminiCandidate struct {
	Content      GeminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
	Index        int           `json:"index"`
}

type GeminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type OpenAIResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []OpenAIChoice `json:"choices"`
	Usage   *OpenAIUsage   `json:"usage,omitempty"`
}

type OpenAIChoice struct {
	Index        int           `json:"index"`
	Message      OpenAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type OpenAIStreamResponse struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Choices []OpenAIStreamChoice `json:"choices"`
}

type OpenAIStreamChoice struct {
	Index        int                 `json:"index"`
	Delta        OpenAIStreamDelta   `json:"delta"`
	FinishReason *string             `json:"finish_reason,omitempty"`
}

type OpenAIStreamDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type OpenAIModelList struct {
	Object string        `json:"object"`
	Data   []OpenAIModel `json:"data"`
}

type OpenAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

func TranslateOpenAIToGemini(req *OpenAIRequest) *GeminiRequest {
	geminiReq := &GeminiRequest{}

	var systemParts []GeminiPart
	var contents []GeminiContent

	for _, msg := range req.Messages {
		role := strings.ToLower(msg.Role)
		contentStr := msg.ContentString()
		if role == "system" {
			systemParts = append(systemParts, GeminiPart{Text: contentStr})
			continue
		}

		geminiRole := "user"
		if role == "assistant" || role == "model" {
			geminiRole = "model"
		}

		contents = append(contents, GeminiContent{
			Role:  geminiRole,
			Parts: []GeminiPart{{Text: contentStr}},
		})
	}

	if len(systemParts) > 0 {
		geminiReq.SystemInstruction = &GeminiContent{
			Parts: systemParts,
		}
	}

	geminiReq.Contents = mergeAndValidateContents(contents)

	if req.Temperature != nil || req.MaxTokens != nil || req.TopP != nil || req.PresencePenalty != nil || req.FrequencyPenalty != nil || req.Stop != nil {
		config := &GeminiConfig{}
		config.Temperature = req.Temperature
		config.MaxOutputTokens = req.MaxTokens
		config.TopP = req.TopP
		config.PresencePenalty = req.PresencePenalty
		config.FrequencyPenalty = req.FrequencyPenalty

		if req.Stop != nil {
			switch val := req.Stop.(type) {
			case string:
				config.StopSequences = []string{val}
			case []interface{}:
				var stops []string
				for _, s := range val {
					if str, ok := s.(string); ok {
						stops = append(stops, str)
					}
				}
				config.StopSequences = stops
			case []string:
				config.StopSequences = val
			}
		}
		geminiReq.GenerationConfig = config
	}

	return geminiReq
}

func mergeAndValidateContents(contents []GeminiContent) []GeminiContent {
	if len(contents) == 0 {
		return contents
	}

	var merged []GeminiContent
	for _, c := range contents {
		if len(merged) == 0 {
			merged = append(merged, c)
			continue
		}

		lastIdx := len(merged) - 1
		if merged[lastIdx].Role == c.Role {
			if len(merged[lastIdx].Parts) > 0 {
				merged[lastIdx].Parts[0].Text += "\n" + c.Parts[0].Text
			} else {
				merged[lastIdx].Parts = append(merged[lastIdx].Parts, c.Parts...)
			}
		} else {
			merged = append(merged, c)
		}
	}

	if len(merged) > 0 && merged[0].Role != "user" {
		merged = append([]GeminiContent{{
			Role:  "user",
			Parts: []GeminiPart{{Text: "Hello"}},
		}}, merged...)
	}

	return merged
}

func generateRandomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func translateFinishReason(reason string) string {
	switch strings.ToUpper(reason) {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY":
		return "content_filter"
	case "RECITATION":
		return "content_filter"
	default:
		return "stop"
	}
}

func (p *Proxy) handleOpenAIChatCompletions(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	clientKey := ResolveAPIKey(r)
	proxyAPIKey := os.Getenv("PROXY_API_KEY")
	var targetAPIKey string

	if proxyAPIKey != "" {
		if clientKey != proxyAPIKey {
			p.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid Proxy API Key.")
			log.Printf("401 Unauthorized - Invalid Proxy API Key - Path: %s", r.URL.Path)
			return
		}
		targetAPIKey = os.Getenv("VERTEX_API_KEY")
		if targetAPIKey == "" {
			targetAPIKey = proxyAPIKey
		}
	} else {
		targetAPIKey = clientKey
		if targetAPIKey == "" {
			targetAPIKey = os.Getenv("VERTEX_API_KEY")
		}
	}

	if targetAPIKey == "" {
		p.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Vertex AI API Key not found. Please set VERTEX_API_KEY environment variable.")
		log.Printf("401 Unauthorized - No target Vertex API Key - Path: %s", r.URL.Path)
		return
	}

	var openAIReq OpenAIRequest
	if err := json.NewDecoder(r.Body).Decode(&openAIReq); err != nil {
		p.writeError(w, http.StatusBadRequest, "BAD_REQUEST", "Failed to parse request body: "+err.Error())
		log.Printf("400 Bad Request - Failed to parse OpenAI request body: %v - Path: %s", err, r.URL.Path)
		return
	}

	model := openAIReq.Model
	targetModel := p.mapper.MapModel(model)

	geminiReq := TranslateOpenAIToGemini(&openAIReq)
	geminiReqBytes, err := json.Marshal(geminiReq)
	if err != nil {
		p.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to translate request: "+err.Error())
		log.Printf("500 Internal Error - Failed to translate request: %v - Path: %s", err, r.URL.Path)
		return
	}

	action := "generateContent"
	if openAIReq.Stream {
		action = "streamGenerateContent"
	}

	targetVersion := "v1beta1"

	endpoint := os.Getenv("VERTEX_API_ENDPOINT")
	if endpoint == "" {
		endpoint = "https://aiplatform.googleapis.com"
	}
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil {
		p.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to parse Vertex API endpoint: "+err.Error())
		log.Printf("500 Internal Error - Failed to parse Vertex API endpoint: %v - Path: %s", err, r.URL.Path)
		return
	}

	targetURL := &url.URL{
		Scheme: parsedEndpoint.Scheme,
		Host:   parsedEndpoint.Host,
		Path:   parsedEndpoint.Path + "/" + targetVersion + "/publishers/google/models/" + targetModel + ":" + action,
	}

	q := targetURL.Query()
	q.Set("key", targetAPIKey)
	if openAIReq.Stream {
		q.Set("alt", "sse")
	}
	targetURL.RawQuery = q.Encode()

	ctx := r.Context()
	backendReq, err := http.NewRequestWithContext(ctx, "POST", targetURL.String(), strings.NewReader(string(geminiReqBytes)))
	if err != nil {
		p.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create target request: "+err.Error())
		log.Printf("500 Internal Error - Failed to create target request: %v - Path: %s", err, r.URL.Path)
		return
	}

	backendReq.Header.Set("Content-Type", "application/json")
	backendReq.Header.Set("x-goog-api-key", targetAPIKey)

	log.Printf("Proxying OpenAI [%s] %s -> %s (model: %s -> %s, stream: %v)", r.Method, r.URL.Path, targetURL.String(), model, targetModel, openAIReq.Stream)

	resp, err := p.client.Do(backendReq)
	if err != nil {
		if ctx.Err() == context.Canceled {
			log.Printf("Client disconnected - Path: %s", r.URL.Path)
			return
		}
		p.writeError(w, http.StatusBadGateway, "BAD_GATEWAY", "Failed to contact Vertex AI: "+err.Error())
		log.Printf("502 Bad Gateway - OpenAI Path: %s, Error: %v", r.URL.Path, err)
		return
	}
	defer resp.Body.Close()

	duration := time.Since(startTime)

	if resp.StatusCode != http.StatusOK {
		w.WriteHeader(resp.StatusCode)
		bodyBytes, _ := io.ReadAll(resp.Body)
		w.Write(bodyBytes)
		log.Printf("Completed [%d] (Error) in %v - OpenAI Path: %s, Backend Response: %s", resp.StatusCode, duration, r.URL.Path, string(bodyBytes))
		return
	}

	if openAIReq.Stream {
		p.handleStreamResponse(w, resp.Body, model)
	} else {
		p.handleUnaryResponse(w, resp.Body, model)
	}

	log.Printf("Completed [200] in %v - OpenAI Path: %s", duration, r.URL.Path)
}

func (p *Proxy) handleUnaryResponse(w http.ResponseWriter, src io.Reader, model string) {
	var geminiResp GeminiResponse
	if err := json.NewDecoder(src).Decode(&geminiResp); err != nil {
		p.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to parse Vertex AI response: "+err.Error())
		return
	}

	openAIResp := OpenAIResponse{
		ID:      "chatcmpl-" + generateRandomID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []OpenAIChoice{},
	}

	var contentText string
	var finishReason string

	if len(geminiResp.Candidates) > 0 {
		candidate := geminiResp.Candidates[0]
		finishReason = translateFinishReason(candidate.FinishReason)

		var parts []string
		for _, part := range candidate.Content.Parts {
			parts = append(parts, part.Text)
		}
		contentText = strings.Join(parts, "")
	}

	openAIResp.Choices = append(openAIResp.Choices, OpenAIChoice{
		Index: 0,
		Message: OpenAIMessage{
			Role:    "assistant",
			Content: contentText,
		},
		FinishReason: finishReason,
	})

	if geminiResp.UsageMetadata != nil {
		openAIResp.Usage = &OpenAIUsage{
			PromptTokens:     geminiResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: geminiResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      geminiResp.UsageMetadata.TotalTokenCount,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if p.enableCORS {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(openAIResp)
}

func (p *Proxy) handleStreamResponse(w http.ResponseWriter, src io.Reader, model string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")
	if p.enableCORS {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}
	w.WriteHeader(http.StatusOK)

	fw := NewFlushingWriter(w)
	scanner := bufio.NewScanner(src)

	streamID := "chatcmpl-" + generateRandomID()
	created := time.Now().Unix()

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "data:") {
			dataContent := strings.TrimSpace(line[5:])
			if dataContent == "" {
				continue
			}

			var geminiResp GeminiResponse
			if err := json.Unmarshal([]byte(dataContent), &geminiResp); err != nil {
				continue
			}

			if len(geminiResp.Candidates) == 0 {
				continue
			}

			candidate := geminiResp.Candidates[0]
			var parts []string
			for _, part := range candidate.Content.Parts {
				parts = append(parts, part.Text)
			}
			textChunk := strings.Join(parts, "")

			var finishReason *string
			if candidate.FinishReason != "" && candidate.FinishReason != "STOP" {
				fr := translateFinishReason(candidate.FinishReason)
				finishReason = &fr
			}

			openAIChunk := OpenAIStreamResponse{
				ID:      streamID,
				Object:  "chat.completion.chunk",
				Created: created,
				Model:   model,
				Choices: []OpenAIStreamChoice{
					{
						Index: 0,
						Delta: OpenAIStreamDelta{
							Content: textChunk,
						},
						FinishReason: finishReason,
					},
				},
			}

			chunkBytes, err := json.Marshal(openAIChunk)
			if err != nil {
				continue
			}

			_, _ = fw.Write([]byte("data: " + string(chunkBytes) + "\n\n"))
		}
	}

	_, _ = fw.Write([]byte("data: [DONE]\n\n"))
}

func (p *Proxy) handleOpenAIModels(w http.ResponseWriter, r *http.Request) {
	models := []string{
		"gemini-1.5-flash",
		"gemini-1.5-pro",
		"gemini-2.0-flash-exp",
		"gemini-2.5-flash",
		"gemini-2.5-pro",
		"gemini-1.0-pro",
	}

	for k := range p.mapper.customMappings {
		models = append(models, k)
	}

	uniqueModels := make(map[string]bool)
	var data []OpenAIModel
	for _, m := range models {
		if !uniqueModels[m] {
			uniqueModels[m] = true
			data = append(data, OpenAIModel{
				ID:      m,
				Object:  "model",
				Created: 1715731200,
				OwnedBy: "google",
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if p.enableCORS {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(OpenAIModelList{
		Object: "list",
		Data:   data,
	})
}
