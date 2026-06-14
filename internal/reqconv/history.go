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
			// tool-result-only user messages: inject directive instead of empty content.
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
			// Reorder tool results to match the preceding assistant's tool_use order.
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
			// Include thinking content in history so the model can see its own
			// prior reasoning and avoid repeating conclusions.
			if thinking := extractThinkingText(msg.Content); thinking != "" {
				if content == "" {
					content = "<thinking>\n" + thinking + "\n</thinking>"
				} else {
					content = "<thinking>\n" + thinking + "\n</thinking>\n\n" + content
				}
			}
			// Generate a deterministic messageId from content + toolUseIDs.
			// v3 captures show messageId must be stable across requests for the same
			// assistant history entry. Using SHA1-based UUID ensures this.
			allToolUses := ExtractToolUses(msg.Content)
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
	return history
}
