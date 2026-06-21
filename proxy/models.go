package proxy

import (
	"log"
	"os"
	"strings"
)

// ModelMapper handles mapping of model names
type ModelMapper struct {
	customMappings map[string]string
}

// NewModelMapper initializes the mapper from environment variables.
// Environment variable format: MODEL_MAPPINGS=aistudio-model:vertex-model,aistudio-model2:vertex-model2
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

// MapModel translates an AI Studio model ID to a Vertex AI model ID.
func (m *ModelMapper) MapModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}

	// 1. Check custom mappings first (e.g. gemini-1.5-pro -> gemini-1.5-pro-001)
	if mapped, ok := m.customMappings[model]; ok {
		return mapped
	}

	// 2. Otherwise pass through directly (Vertex AI supports unversioned model IDs like gemini-1.5-pro)
	return model
}
