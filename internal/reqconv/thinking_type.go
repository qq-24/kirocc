package reqconv

// ResolveThinkingType returns the thinking type for Kiro backend.
// Only "adaptive" and "disabled" are accepted. "enabled" causes 500.
func ResolveThinkingType() string {
	return "adaptive"
}
