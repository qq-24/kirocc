package reqconv

import "os"

// ResolveThinkingType returns the thinking type to send to Kiro backend.
// Defaults to "adaptive". Set KIROCC_THINKING_TYPE=enabled to force thinking on every request.
func ResolveThinkingType() string {
	if v := os.Getenv("KIROCC_THINKING_TYPE"); v != "" {
		return v
	}
	return "adaptive"
}
