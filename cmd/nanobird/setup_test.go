package main

import (
	"testing"

	"github.com/usunrise88/nanobird/internal/config"
)

// TestRefuseStrictNeedsTheNativeBackend pins SPEC §5.5: strict is honored on the native
// backend, and the OpenAI dialect refuses it.
func TestRefuseStrictNeedsTheNativeBackend(t *testing.T) {
	cases := []struct {
		backend, format string
		refused         bool
	}{
		{"nanollm", "strict", false},
		{"nanollm", "rescue", false},
		{"openai", "rescue", false},
		{"openai", "strict", true},
	}
	for _, c := range cases {
		cfg := config.Defaults()
		cfg.Model.Backend = c.backend
		cfg.Loop.ToolFormat = c.format
		if err := refuseStrict(cfg); (err != nil) != c.refused {
			t.Errorf("backend %s, tool_format %s: error %v, want refused %v", c.backend, c.format, err, c.refused)
		}
	}
}
