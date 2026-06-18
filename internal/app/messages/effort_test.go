package messages

import (
	"context"
	"testing"

	"github.com/d-kuro/kirocc/internal/anthropic"
)

func TestResolveEffort(t *testing.T) {
	tests := []struct {
		name      string
		kiroModel string
		effort    string
		thinking  bool
		want      string
	}{
		{"always max regardless of input", "claude-opus-4.8", "low", false, "max"},
		{"no effort still max", "claude-opus-4.8", "", false, "max"},
		{"thinking true still max", "claude-sonnet-4.6", "", true, "max"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &anthropic.Request{}
			if tt.effort != "" {
				req.OutputConfig = &anthropic.OutputConfig{Effort: tt.effort}
			}
			got := resolveEffort(context.Background(), tt.kiroModel, req, tt.thinking)
			if got != tt.want {
				t.Errorf("resolveEffort(%q, effort=%q, thinking=%v) = %q, want %q",
					tt.kiroModel, tt.effort, tt.thinking, got, tt.want)
			}
		})
	}
}
