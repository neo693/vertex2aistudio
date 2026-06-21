package main

import (
	"bufio"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"vertex2aistudio/proxy"
)

// loadEnv reads a local .env file and sets environment variables if they aren't already defined.
func loadEnv() {
	file, err := os.Open(".env")
	if err != nil {
		return // Silent return if file doesn't exist (e.g. in Docker)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])
			// Strip single/double quotes around values
			if (strings.HasPrefix(v, "\"") && strings.HasSuffix(v, "\"")) ||
				(strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'")) {
				v = v[1 : len(v)-1]
			}
			// Only set if not already set in shell (preserves command line overrides)
			if os.Getenv(k) == "" && k != "" {
				os.Setenv(k, v)
			}
		}
	}
}

func main() {
	// Load environment variables from .env file if present
	loadEnv()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	proxyHandler := proxy.NewProxy()

	// Wrapper handler to catch health check and route everything else to proxy
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/healthz" || path == "/health" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}

		// Ensure we also log non-api requests that are just hitting root
		if path == "/" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"message": "Google AI Studio API to Vertex AI Proxy is running.",
				"hint":    "Point your AI Studio (Gemini API) client base URL to this server.",
			})
			return
		}

		proxyHandler.ServeHTTP(w, r)
	})

	log.Printf("Starting AI Studio to Vertex AI proxy server on port %s...", port)
	
	// Print configuration overview for user convenience
	if projectID := os.Getenv("VERTEX_PROJECT_ID"); projectID != "" {
		log.Printf("Target GCP Project ID: %s", projectID)
	} else {
		log.Println("WARNING: VERTEX_PROJECT_ID environment variable is not set. Requests will fail unless configured.")
	}

	if region := os.Getenv("VERTEX_REGION"); region != "" {
		log.Printf("Target GCP Region: %s", region)
	} else {
		log.Println("Default GCP Region: us-central1")
	}

	if os.Getenv("VERTEX_API_KEY") != "" {
		log.Println("Default VERTEX_API_KEY is configured.")
	} else if os.Getenv("GEMINI_API_KEY") != "" {
		log.Println("Default GEMINI_API_KEY is configured (legacy).")
	} else {
		log.Println("WARNING: No VERTEX_API_KEY environment variable set. Clients must provide credentials via x-goog-api-key or Authorization headers.")
	}

	if mappings := os.Getenv("MODEL_MAPPINGS"); mappings != "" {
		log.Printf("Active Model Mappings: %s", mappings)
	}

	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
