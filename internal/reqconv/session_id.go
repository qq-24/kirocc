package reqconv

import (
	"github.com/d-kuro/kirocc/internal/anthropic"
	"github.com/google/uuid"
)

// DeriveSessionID generates a stable session ID from the request's system prompt.
// Same system prompt → same ID → same Kiro conversationId → KV-cache hit.
func DeriveSessionID(req *anthropic.Request) string {
	seed := ExtractSystemPrompt(req.System)
	if seed == "" {
		seed = "no-system-prompt"
	}
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("session:"+seed)).String()
}
