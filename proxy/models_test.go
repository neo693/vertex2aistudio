package proxy

import (
	"os"
	"testing"
)

func TestMapModel(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		mappings string // MODEL_MAPPINGS env value
	}{
		{
			name:     "Identity mapping",
			input:    "gemini-1.5-pro",
			expected: "gemini-1.5-pro",
		},
		{
			name:     "Strip version suffix 001",
			input:    "gemini-1.5-pro-001",
			expected: "gemini-1.5-pro",
		},
		{
			name:     "Strip version suffix 002",
			input:    "gemini-1.5-flash-002",
			expected: "gemini-1.5-flash",
		},
		{
			name:     "Experimental model preserved",
			input:    "gemini-2.0-flash-exp",
			expected: "gemini-2.0-flash-exp",
		},
		{
			name:     "Custom mapping override",
			input:    "vertex-custom-model",
			expected: "gemini-1.5-pro",
			mappings: "vertex-custom-model:gemini-1.5-pro,other:flash",
		},
		{
			name:     "Custom mapping and space trimming",
			input:    "spaced-model",
			expected: "gemini-1.5-flash",
			mappings: " spaced-model : gemini-1.5-flash ",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mappings != "" {
				os.Setenv("MODEL_MAPPINGS", tc.mappings)
				defer os.Unsetenv("MODEL_MAPPINGS")
			} else {
				os.Unsetenv("MODEL_MAPPINGS")
			}

			mapper := NewModelMapper()
			actual := mapper.MapModel(tc.input)
			if actual != tc.expected {
				t.Errorf("expected MapModel(%q) = %q, got %q", tc.input, tc.expected, actual)
			}
		})
	}
}
