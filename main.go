package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"vertex2aistudio/proxy"
)

func main() {
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
				"message": "Vertex AI to Google AI Studio Proxy is running.",
				"hint":    "Point your Vertex AI client base URL to this server.",
			})
			return
		}

		proxyHandler.ServeHTTP(w, r)
	})

	log.Printf("Starting Vertex to AI Studio proxy server on port %s...", port)
	
	// Print configuration overview for user convenience
	if os.Getenv("GEMINI_API_KEY") != "" {
		log.Println("Default GEMINI_API_KEY is configured.")
	} else {
		log.Println("WARNING: No GEMINI_API_KEY environment variable set. Clients must provide credentials via x-goog-api-key or Authorization headers.")
	}

	if mappings := os.Getenv("MODEL_MAPPINGS"); mappings != "" {
		log.Printf("Active Model Mappings: %s", mappings)
	}

	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
