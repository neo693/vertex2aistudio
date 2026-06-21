package proxy

import (
	"log"
	"os"
	"regexp"
	"strings"
)

var (
	// Matches trailing version suffixes like -001, -002, -003
	versionSuffixRegex = regexp.MustCompile(`-\d{3}$`)
)

// ModelMapper handles mapping of model names
type ModelMapper struct {
	customMappings map[string]string
}

// NewModelMapper initializes the mapper from environment variables.
// Environment variable format: MODEL_MAPPINGS=vertex-model-1:aistudio-model-1,vertex-model-2:aistudio-model-2
func NewModelMapper() *ModelMapper {
	custom := make(map[string]string)
	envVal := os.Getenv("MODEL_MAPPINGS")
	if envVal != "" {
		pairs := strings.Split(envVal, ",")
		for _, pair := range pairs {
			parts := strings.Split(pair, ":")
			if len(parts) == 2 {
				k := strings.TrimSpace(parts[0])
				v := strings.TrimSpace(parts[1])
				if k != "" && v != "" {
					custom[k] = v
				}
			}
		}
		if len(custom) > 0 {
			log.Printf("Loaded %d custom model mappings from environment", len(custom))
		}
	}
	return &ModelMapper{customMappings: custom}
}

// MapModel translates a Vertex model ID to a Google AI Studio model ID.
func (m *ModelMapper) MapModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}

	// 1. Check custom mappings first
	if mapped, ok := m.customMappings[model]; ok {
		return mapped
	}

	// 2. Apply default suffix stripping rules (e.g. gemini-1.5-pro-001 -> gemini-1.5-pro)
	mapped := versionSuffixRegex.ReplaceAllString(model, "")

	return mapped
}
