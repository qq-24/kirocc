package respconv

import (
	"encoding/json/jsontext"

	"github.com/d-kuro/kirocc/internal/anthropic"
)

// WriteServerToolResult writes a server_tool_use block followed by a web_search_tool_result
// or a generic tool_result block for web_fetch.
func (s *SSEWriter) WriteServerToolResult(id, name, inputJSON, resultJSON string) {
	s.WriteServerToolUse(id, name, inputJSON)
	s.writeBlock(
		map[string]any{
			"type":        anthropic.BlockTypeWebSearchToolResult,
			"tool_use_id": id,
			"content":     jsontext.Value(resultJSON),
		},
		nil,
	)
}

// WriteServerToolError writes a server_tool_use block followed by an error result.
func (s *SSEWriter) WriteServerToolError(id, name, inputJSON, errMsg string) {
	s.WriteServerToolUse(id, name, inputJSON)
	s.writeBlock(
		map[string]any{
			"type":        anthropic.BlockTypeWebSearchToolResult,
			"tool_use_id": id,
			"is_error":    true,
			"content": []map[string]any{
				{"type": "text", "text": errMsg},
			},
		},
		nil,
	)
}
