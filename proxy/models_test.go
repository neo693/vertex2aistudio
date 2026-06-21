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
			name:     "Pass through gemini-1.5-pro",
			input:    "gemini-1.5-pro",
			expected: "gemini-1.5-pro",
		},
		{
			name:     "Pass through gemini-1.5-flash",
			input:    "gemini-1.5-flash",
			expected: "gemini-1.5-flash",
		},
		{
			name:     "Custom mapping override (e.g. map pro to specific version)",
			input:    "gemini-1.5-pro",
			expected: "gemini-1.5-pro-001",
			mappings: "gemini-1.5-pro:gemini-1.5-pro-001,gemini-1.5-flash:gemini-1.5-flash-001",
		},
		{
			name:     "Custom mapping with spaced entries",
			input:    "gemini-1.5-flash",
			expected: "gemini-1.5-flash-002",
			mappings: " gemini-1.5-flash : gemini-1.5-flash-002 ",
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
