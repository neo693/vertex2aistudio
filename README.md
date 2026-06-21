# AI Studio to Vertex AI API Converter Proxy

A lightweight, high-performance Go-based microservice that acts as a bridge for tools that only support the Google AI Studio API format (e.g. Cursor, LibreChat, or the standard Gemini Developer SDK) but need to connect to a Google Cloud Vertex AI backend using a Google Cloud Vertex API Key.

It intercepts incoming requests formatted for Google AI Studio, translates the paths and model identifiers, handles authentication, and forwards the calls to Google Cloud Vertex AI REST endpoints. It supports both unary (REST) and real-time Server-Sent Events (SSE) streaming responses.

## Key Features

- **Path & Format Translating**: Intercepts AI Studio API paths (like `/v1beta/models/{model}:generateContent`) and maps them to global Vertex AI REST endpoints.
- **OpenAI Compatibility Layer**: Translates OpenAI-style `/chat/completions` (and `/v1/chat/completions`) requests to Gemini format, and converts the response or SSE streams back into OpenAI-compatible format.
- **Models Endpoint**: Implements a standard `/v1/models` route returning the list of active models.
- **SSE Streaming Support**: Proxies streamed responses chunk-by-chunk using a non-buffering flushing writer, keeping latency low.
- **Flexible Authentication**: Resolves the Vertex AI API Key from:
  1. Request Header `x-goog-api-key`
  2. Request Header `Authorization: Bearer <API_KEY>` (accepts both GCP API keys and GCP OAuth tokens)
  3. Environment Variable `VERTEX_API_KEY` (server-side default)
- **Automatic & Custom Model Mapping**:
  - Passes through standard model names (e.g. `gemini-1.5-pro` -> `gemini-1.5-pro` directly supported by Vertex AI).
  - Supports custom overrides via the `MODEL_MAPPINGS` environment variable (e.g. mapping `gemini-1.5-pro` to a specific version like `gemini-1.5-pro-001`).
- **CORS Support**: Broad CORS headers enabled by default (can be disabled).
- **Health Checks**: Dedicated endpoints at `/health` and `/healthz`.

---

## Configuration (Environment Variables)

Configure these in your environment or via a `.env` file (see `.env.example`):

| Variable | Description | Default / Required |
| :--- | :--- | :--- |
| `VERTEX_API_KEY` | Server-wide default Google Cloud API Key for Vertex AI | *(Optional)* |
| `PROXY_API_KEY` | Custom API Key required from client tools to access the proxy | *(Optional)* |
| `PORT` | Port the proxy server listens on | `8080` |
| `MODEL_MAPPINGS` | Comma-separated list of custom mappings (`studio:vertex`) | *(Optional)* |
| `DISABLE_CORS` | Set to `true` to disable wildcard CORS headers | `false` |

### Custom Model Mapping Example
```bash
export MODEL_MAPPINGS="gemini-1.5-pro:gemini-1.5-pro-001,gemini-1.5-flash:gemini-1.5-flash-001"
```

---

## Getting Started

### Method 1: Local Development
Ensure you have Go installed (version 1.21 or later).

1. Clone or navigate to the repository directory.
2. Start the proxy server:
   ```bash
   export VERTEX_API_KEY="your-google-cloud-api-key"
   go run main.go
   ```
3. The proxy will be listening on `http://localhost:8080`.

### Method 2: Docker
1. Build the Docker image:
   ```bash
   docker build -t vertex2aistudio .
   ```
2. Run the container:
   ```bash
   docker run -p 8080:8080 \
     -e VERTEX_API_KEY="your-google-cloud-api-key" \
     vertex2aistudio
   ```

---

## Verification & Usage Examples

Once the server is running on `localhost:8080`, test it using standard tools.

### 1. Unary Request (`generateContent`)
```bash
curl -X POST "http://localhost:8080/v1beta/models/gemini-1.5-flash:generateContent" \
  -H "Content-Type: application/json" \
  -d '{
    "contents": [{
      "parts": [{"text": "Write a 3-word slogan for coding."}]
    }]
  }'
```

### 2. Streaming Request (`streamGenerateContent`)
```bash
curl -N -X POST "http://localhost:8080/v1beta/models/gemini-1.5-pro:streamGenerateContent" \
  -H "Content-Type: application/json" \
  -d '{
    "contents": [{
      "parts": [{"text": "Count from 1 to 5."}]
    }]
  }'
```

### 3. Count Tokens Request
```bash
curl -X POST "http://localhost:8080/v1beta/models/gemini-1.5-flash:countTokens" \
  -H "Content-Type: application/json" \
  -d '{
    "contents": [{
      "parts": [{"text": "Hello world"}]
    }]
  }'
```

### 4. OpenAI Compatibility Unary Request (`/v1/chat/completions`)
```bash
curl -X POST "http://localhost:8080/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-1.5-flash",
    "messages": [
      {"role": "user", "content": "Write a 3-word slogan for coding."}
    ]
  }'
```

### 5. OpenAI Compatibility Streaming Request
```bash
curl -N -X POST "http://localhost:8080/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-1.5-flash",
    "messages": [
      {"role": "user", "content": "Count from 1 to 5."}
    ],
    "stream": true
  }'
```

### 6. Health Check
```bash
curl http://localhost:8080/healthz
```

---

## Integration with Client Tools & SDKs

Point your AI Studio (Gemini API) or OpenAI compatible clients to this proxy server.

### Cursor IDE Integration

You can integrate this proxy into Cursor using either the **Gemini** provider configuration or the **OpenAI** provider configuration.

#### Option A: Gemini Configuration
1. Go to **Settings > Models**.
2. Select **Gemini**.
3. Under the Gemini configuration, set:
   - **Gemini API Key**: Your Google Cloud Vertex API Key (or anything if you've set `VERTEX_API_KEY` on the proxy server).
   - **Base URL / Endpoint Override**: `http://localhost:8080/v1beta` (or `http://localhost:8080/v1` depending on Cursor version).

#### Option B: OpenAI Configuration (OpenAI Compatibility Layer)
1. Go to **Settings > Models**.
2. Select **OpenAI**.
3. Under the OpenAI configuration, set:
   - **OpenAI API Key**: Your Google Cloud Vertex API Key (or `PROXY_API_KEY` if configured).
   - **Override URL**: `http://localhost:8080/v1` (or `http://localhost:8080` depending on the client).

### Gemini Developer Node.js SDK
```javascript
const { GoogleGenAI } = require('@google/genai');

// Initialize GenAI client pointing to the proxy
const ai = new GoogleGenAI({
  apiKey: 'your-vertex-api-key',
  baseURL: 'http://localhost:8080/v1beta' // Point to proxy
});
```

---

## Limitations

- **GCS URIs (`gs://...`)**: Since standard Google AI Studio client payloads use base64-encoded `inlineData` for multimodal inputs rather than GCS URIs, client payloads should continue using `inlineData`.
- **Other Vertex APIs**: This proxy is designed specifically for Gemini model queries (`generateContent`, `streamGenerateContent`, `countTokens`, `embedContent`). Other Google Cloud Vertex platform APIs (like AutoML, endpoints, pipeline runs) are not supported.
