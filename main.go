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

	proxyAPIKey := os.Getenv("PROXY_API_KEY")
	printKey := proxyAPIKey
	if printKey == "" {
		printKey = "[Not Enforced - Any Key Allowed]"
	}

	log.Println("--------------------------------------------------")
	log.Println("Proxy is ready! Use these settings in your tool:")
	log.Printf("Base URL:  http://localhost:%s/v1beta   (or /v1)", port)
	log.Printf("API Key:   %s", printKey)
	log.Println("--------------------------------------------------")

	if proxyAPIKey != "" {
		log.Printf("Security: Custom PROXY_API_KEY is configured. Clients MUST send this key.")
		if os.Getenv("VERTEX_API_KEY") != "" {
			log.Println("Security: Clients will be authorized and proxied using VERTEX_API_KEY.")
		}
	} else {
		log.Println("Security: No PROXY_API_KEY set. Requests will pass through client-supplied keys directly to Vertex AI.")
		if os.Getenv("VERTEX_API_KEY") != "" {
			log.Println("Default VERTEX_API_KEY is configured as fallback for requests without keys.")
		}
	}

	if mappings := os.Getenv("MODEL_MAPPINGS"); mappings != "" {
		log.Printf("Active Model Mappings: %s", mappings)
	}

	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
