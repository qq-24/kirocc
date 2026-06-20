package reqconv

import (
	"strings"

	"github.com/d-kuro/kirocc/internal/anthropic"
	"github.com/d-kuro/kirocc/internal/kiroproto"
	"github.com/google/uuid"
)

// extractThinkingText concatenates all thinking block content from a message.
func extractThinkingText(content anthropic.MessageContent) string {
	if content.IsString() {
		return ""
	}
	var parts []string
	for _, b := range content.Blocks {
		if b.Type == anthropic.BlockTypeThinking && b.Thinking != "" {
			parts = append(parts, b.Thinking)
		}
	}
	return strings.Join(parts, "\n")
}

// hasRealTextContent reports whether a message has actual text content (not just tool_results).
func hasRealTextContent(content anthropic.MessageContent) bool {
	if content.IsString() {
		return content.Text != "" && content.Text != syntheticEmpty && content.Text != syntheticContinue
	}
	for _, b := range content.Blocks {
		if b.Type == anthropic.BlockTypeText && b.Text != "" {
			return true
		}
	}
	return false
}

func extractToolUseIDs(msg anthropic.Message) []string {
	if msg.Content.IsString() {
		return nil
	}
	var ids []string
	for _, b := range msg.Content.Blocks {
		if b.IsToolUse() {
			ids = append(ids, b.ID)
		}
	}
	return ids
}

// buildHistory converts normalized Anthropic messages to Kiro history entries.
func buildHistory(msgs []anthropic.Message, nameMap *ToolNameMap) []kiroproto.HistoryEntry {
	var history []kiroproto.HistoryEntry

	for i, msg := range msgs {
		switch msg.Role {
		case "user":
			content := ExtractTextContent(msg.Content)
			toolResults := ExtractToolResults(msg.Content)
			if content == "" && len(toolResults) > 0 {
				content = "(tool results)"
			}
			userMsg := &kiroproto.HistoryUserInputMessage{
				Content: content,
				Origin:  kiroproto.OriginKiroCLI,
			}
			if images := ExtractImages(msg.Content); len(images) > 0 {
				userMsg.Images = images
			}
			if len(toolResults) > 1 && i > 0 && msgs[i-1].Role == "assistant" {
				toolResults = ReorderToolResults(toolResults, extractToolUseIDs(msgs[i-1]))
			}
			if len(toolResults) > 0 {
				userMsg.UserInputMessageContext = &kiroproto.UserInputMessageContext{
					ToolResults: toolResults,
				}
			}
			history = append(history, kiroproto.HistoryEntry{UserInputMessage: userMsg})

		case "assistant":
			content := ExtractTextContent(msg.Content)
			allToolUses := ExtractToolUses(msg.Content)
			// Preserve thinking for ALL assistant messages (full history).
			// Claude Code retains thinking blocks in messages; we inline them
			// as <thinking> XML tags since Kiro's format only has a content string.
			if thinking := extractThinkingText(msg.Content); thinking != "" {
				if content == "" {
					content = "<thinking>\n" + thinking + "\n</thinking>"
				} else {
					content = "<thinking>\n" + thinking + "\n</thinking>\n\n" + content
				}
			}
			for i := range allToolUses {
				allToolUses[i].Name = nameMap.Shorten(allToolUses[i].Name)
			}
			var idSeedBuilder strings.Builder
			idSeedBuilder.WriteString("assistant-msg:")
			idSeedBuilder.WriteString(content)
			for _, tu := range allToolUses {
				idSeedBuilder.WriteByte(':')
				idSeedBuilder.WriteString(tu.ToolUseID)
			}
			arm := &kiroproto.AssistantResponseMessage{
				MessageID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(idSeedBuilder.String())).String(),
				Content:   content,
			}

			// v2 captures show thinking blocks are NOT included in history toolUses.
			// Only real tool_use blocks are included.
			if len(allToolUses) > 0 {
				arm.ToolUses = allToolUses
			}

			history = append(history, kiroproto.HistoryEntry{AssistantResponseMessage: arm})
		}
	}
	// Mark the last assistant message with cachePoint so the entire history
	// prefix is cacheable on subsequent requests.
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].AssistantResponseMessage != nil {
			history[i].AssistantResponseMessage.CachePoint = &kiroproto.CachePoint{Type: "default"}
			break
		}
	}
	return history
}
