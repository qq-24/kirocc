package respconv

// estimatedOutputTokens returns an approximate output token count from accumulated text.
// Uses the incrementally tracked rune count with a heuristic of ~4 runes per token.
// Returns at least 1 for non-empty output to avoid reporting 0 tokens for short responses.
func (a *responseAccumulator) estimatedOutputTokens() int {
	if a.outputRuneCount == 0 {
		return 0
	}
	return max(1, a.outputRuneCount/4)
}

// resolvedUsage returns the best available input and output token counts.
// Priority: pre-counted (tiktoken) > metadata/metering > contextUsage estimate > 0.
// PreCounted is preferred because Kiro's metadataEvent reports inflated server-side
// totals that include internal history, causing Claude Code to compact prematurely.
func (a *responseAccumulator) resolvedUsage() (inputTokens, outputTokens int) {
	if a.PreCountedInputTokens > 0 {
		outputTokens = a.OutputTokens
		if outputTokens == 0 {
			outputTokens = a.estimatedOutputTokens()
		}
		return a.PreCountedInputTokens, outputTokens
	}
	if a.HasMetadata || a.InputTokens > 0 || a.OutputTokens > 0 {
		return a.InputTokens, a.OutputTokens
	}
	if a.HasContextUsage && a.ContextWindowSize > 0 {
		pct := max(0, min(100, a.ContextUsagePercentage))
		estOutput := a.estimatedOutputTokens()
		total := int(pct / 100 * float64(a.ContextWindowSize))
		estInput := max(0, total-estOutput)
		return estInput, estOutput
	}
	return 0, 0
}

// UsageMap builds an Anthropic-compatible usage map from the given token counts.
func (a *responseAccumulator) UsageMap(inputTokens, outputTokens int) map[string]any {
	cacheRead := a.CacheReadInputTokens
	cacheWrite := a.CacheWriteInputTokens
	uncached := inputTokens
	// If backend doesn't report cache stats, assume 95% cache read for cost estimation.
	// Anthropic format: input_tokens = uncached only, cache_read = cached portion.
	if cacheRead == 0 && cacheWrite == 0 && inputTokens > 0 {
		cacheRead = inputTokens * 95 / 100
		uncached = inputTokens - cacheRead
	}
	return map[string]any{
		"input_tokens":                uncached,
		"output_tokens":               outputTokens,
		"cache_read_input_tokens":     cacheRead,
		"cache_creation_input_tokens": cacheWrite,
	}
}
