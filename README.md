# Vertex AI to Google AI Studio API Converter Proxy

A lightweight, high-performance Go-based microservice that acts as a bridge between Vertex AI API clients and the Google AI Studio (Gemini Developer API).

It intercepts incoming requests formatted for Vertex AI, translates the paths and model identifiers, handles authentication conversion, and forwards the calls to Google AI Studio. It supports unary (REST) and real-time Server-Sent Events (SSE) streaming responses.

## Key Features

- **Path Translating**: Intercepts paths like `/v1/projects/{project}/locations/{region}/publishers/google/models/{model}:generateContent` and maps them to `/v1beta/models/{model}:generateContent`.
- **SSE Streaming Support**: Proxies streamed responses chunk-by-chunk using a non-buffering flushing writer, keeping latency low.
- **Flexible Authentication**: Resolves the Gemini API Key from:
  1. Request Header `x-goog-api-key`
  2. Request Header `Authorization: Bearer <API_KEY>` (will ignore GCP OAuth2 tokens starting with `ya29.`)
  3. Environment Variable `GEMINI_API_KEY` (server-side default)
- **Automatic & Custom Model Mapping**:
  - Automatically strips Vertex version suffixes (e.g. `gemini-1.5-pro-001` -> `gemini-1.5-pro`).
  - Supports custom overrides via the `MODEL_MAPPINGS` environment variable.
- **CORS Support**: Broad CORS headers enabled by default (can be disabled).
- **Health Checks**: Dedicated endpoints at `/health` and `/healthz`.

---

## Configuration (Environment Variables)

| Variable | Description | Default |
| :--- | :--- | :--- |
| `PORT` | Port the proxy server listens on | `8080` |
| `GEMINI_API_KEY` | Server-wide default Google AI Studio API key | *(Optional)* |
| `MODEL_MAPPINGS` | Comma-separated list of custom mappings (`vertex:studio`) | *(Optional)* |
| `TARGET_API_VERSION` | Target Google AI Studio API version | `v1beta` |
| `DISABLE_CORS` | Set to `true` to disable wildcard CORS headers | `false` |

### Custom Model Mapping Example
```bash
export MODEL_MAPPINGS="my-custom-pro:gemini-1.5-pro,my-custom-flash:gemini-1.5-flash"
```

---

## Getting Started

### Method 1: Local Development
Ensure you have Go installed (version 1.21 or later).

1. Clone or navigate to the repository directory.
2. Start the proxy server:
   ```bash
   export GEMINI_API_KEY="your-google-ai-studio-api-key"
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
   docker run -p 8080:8080 -e GEMINI_API_KEY="your-google-ai-studio-api-key" vertex2aistudio
   ```

---

## Verification & Usage Examples

Once the server is running on `localhost:8080`, test it using standard tools.

### 1. Unary Request (`generateContent`)
```bash
curl -X POST "http://localhost:8080/v1/projects/my-project/locations/us-central1/publishers/google/models/gemini-1.5-flash-001:generateContent" \
  -H "Content-Type: application/json" \
  -d '{
    "contents": [{
      "parts": [{"text": "Write a 3-word slogan for coding."}]
    }]
  }'
```

### 2. Streaming Request (`streamGenerateContent`)
```bash
curl -N -X POST "http://localhost:8080/v1/projects/my-project/locations/us-central1/publishers/google/models/gemini-1.5-pro-002:streamGenerateContent" \
  -H "Content-Type: application/json" \
  -d '{
    "contents": [{
      "parts": [{"text": "Count from 1 to 5."}]
    }]
  }'
```

### 3. Count Tokens Request
```bash
curl -X POST "http://localhost:8080/v1/projects/my-project/locations/us-central1/publishers/google/models/gemini-1.5-flash:countTokens" \
  -H "Content-Type: application/json" \
  -d '{
    "contents": [{
      "parts": [{"text": "Hello world"}]
    }]
  }'
```

### 4. Health Check
```bash
curl http://localhost:8080/healthz
```

---

## Integration with SDKs

To direct your Vertex AI client/SDK to this proxy, adjust the endpoint/host URL setting in your SDK configuration:

### Vertex AI Python SDK
```python
from google.cloud import aiplatform

# Initialize using the proxy endpoint
aiplatform.init(
    project="your-project-id",
    location="us-central1",
    api_endpoint="localhost:8080" # Point to the proxy (without http:// prefix)
)
```

### Vertex AI Node.js / TypeScript SDK
```javascript
const { VertexAI } = require('@google-cloud/vertexai');

// Initialize Vertex SDK pointing to proxy
const vertex_ai = new VertexAI({
  project: 'your-project-id',
  location: 'us-central1',
  apiEndpoint: 'localhost:8080' // Point to proxy
});
```

---

## Limitations

- **GCS URIs (`gs://...`)**: This proxy does not automatically download Google Cloud Storage files or upload them to AI Studio. Use base64-encoded `inlineData` for multimodal inputs.
- **Other Vertex APIs**: This proxy is designed specifically for Gemini text, multimodal, and token counting APIs. It does not support other Vertex AI platform services (e.g. AutoML, pipeline runs, endpoints management, tuning, search, etc.).
